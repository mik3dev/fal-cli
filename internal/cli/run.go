package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/catalog"
	"github.com/mik3dev/fal-cli/internal/config"
	"github.com/mik3dev/fal-cli/internal/download"
	"github.com/mik3dev/fal-cli/internal/ledger"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/result"
	"github.com/mik3dev/fal-cli/internal/schema"
	"github.com/mik3dev/fal-cli/internal/task"
	"github.com/mik3dev/fal-cli/internal/upload"
	"github.com/spf13/cobra"
)

const (
	videoTimeout   = 30 * time.Minute
	defaultTimeout = 10 * time.Minute
	fallbackOutDir = "./fal-output"
	expiryWarning  = "result expires minutes after completion; download now"
)

func usageErr(format string, args ...any) *output.Error {
	return &output.Error{Code: output.CodeUsage, Message: fmt.Sprintf(format, args...)}
}

func newRunCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <endpoint>",
		Short: "Submit a generation request, wait for it and download the results",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runModel(cmd, deps, args[0]) },
	}
	f := cmd.Flags()
	f.StringArrayP("param", "p", nil, "input field as key=value (repeatable; @file reads a file, @@ escapes @)")
	f.String("input", "", "JSON object file with the input ('-' for stdin); -p overrides it")
	addFinishFlags(cmd)
	f.Bool("no-wait", false, "submit and return the requestId without waiting")
	f.Bool("dry-run", false, "print the request body and price without uploading or submitting")
	f.Bool("no-validate", false, "accepted for compatibility; schema validation arrives in a later release")
	return cmd
}

// addFinishFlags adds the flags shared by `run` and `task wait`.
func addFinishFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("out", "", "download directory (default: config out_dir or ./fal-output)")
	f.Bool("no-download", false, "do not download result files")
	f.Duration("timeout", 0, "max time to wait (default 10m, 30m for video endpoints)")
}

func runModel(cmd *cobra.Command, deps Deps, endpoint string) error {
	flags := cmd.Flags()
	timeout, _ := flags.GetDuration("timeout")
	if timeout < 0 {
		return usageErr("--timeout must be positive, got %s", timeout)
	}
	c, paths, cfg, err := newAPIClientCfg(cmd, deps)
	if err != nil {
		return err
	}
	warn := warnWriter(cmd, deps)
	if nv, _ := flags.GetBool("no-validate"); nv {
		_, _ = fmt.Fprintln(warn, "warning: --no-validate is ignored; input validation is not implemented yet")
	}
	doc, err := schema.Fetch(cmd.Context(), c, paths.Cache, endpoint, false, deps.Now())
	if err != nil {
		return err
	}
	input, err := buildInput(cmd, deps)
	if err != nil {
		return err
	}
	if dry, _ := flags.GetBool("dry-run"); dry {
		return emitDryRun(cmd, deps, c, paths, endpoint, input, warn)
	}
	input, uploaded, err := upload.Resolve(cmd.Context(), c, input, upload.IsFileKey, fileExists)
	if err != nil {
		return err
	}
	if err := config.EnsureHome(paths); err != nil {
		return &output.Error{Code: output.CodeIO, Message: err.Error()}
	}
	outDir := resolveOutDir(cmd, cfg, "")
	now := deps.Now()
	id, err := task.Submit(cmd.Context(), c, paths.Ledger, endpoint, input, outDir, now, warn)
	if err != nil {
		return err
	}
	if noWait, _ := flags.GetBool("no-wait"); noWait {
		return output.Success(deps.Stdout, map[string]any{
			"requestId": id, "model": endpoint, "state": task.LedgerSubmitted,
			"ids": map[string]any{"request_id": id}, "uploaded": nonNil(uploaded),
		})
	}
	if timeout == 0 {
		timeout = timeoutFor(doc.Category)
	}
	return finish(cmd, deps, c, paths, finishArgs{id: id, endpoint: endpoint, outDir: outDir, timeout: timeout, createdAt: now, uploaded: uploaded})
}

// buildInput merges --input and the -p values (later wins). Plain -p values
// are typed by guess (numbers, booleans, JSON); @file contents and --input
// values stay as they are.
// ponytail: stage 2 replaces the guess with schema-driven coercion and validation.
func buildInput(cmd *cobra.Command, deps Deps) (map[string]any, error) {
	flags := cmd.Flags()
	var base map[string]any
	if path, _ := flags.GetString("input"); path != "" {
		var data []byte
		var err error
		if path == "-" {
			data, err = io.ReadAll(deps.Stdin)
		} else {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			return nil, usageErr("cannot read --input: %v", err)
		}
		if err := json.Unmarshal(data, &base); err != nil || base == nil {
			return nil, usageErr("--input must be a JSON object")
		}
	}
	pairs, _ := flags.GetStringArray("param")
	tagged, err := parseTagged(pairs, os.ReadFile)
	if err != nil {
		return nil, err
	}
	plain := map[string]any{}
	for k, v := range tagged {
		if _, isFile := v.(fileText); !isFile {
			plain[k] = v
		}
	}
	over := unwrapFileValues(MergeInput(tagged, schema.GuessLiterals(plain)))
	return MergeInput(base, over), nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// pendingUploads lists the local files that a real run would upload.
func pendingUploads(input map[string]any) []string {
	out := []string{}
	for k, v := range input {
		if !upload.IsFileKey(k) {
			continue
		}
		items, ok := v.([]any)
		if !ok {
			items = []any{v}
		}
		for _, it := range items {
			if s, ok := it.(string); ok && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "data:") && fileExists(s) {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

// emitDryRun prints the request that would be sent; nothing is uploaded or submitted.
func emitDryRun(cmd *cobra.Command, deps Deps, c *api.Client, paths config.Paths, endpoint string, input map[string]any, warn io.Writer) error {
	out := map[string]any{"dryRun": true, "model": endpoint, "input": input, "pendingUploads": pendingUploads(input), "price": nil}
	svc := &catalog.Service{Client: c, CacheDir: paths.Cache, Now: deps.Now}
	if p, err := svc.Price(cmd.Context(), endpoint); err != nil {
		_, _ = fmt.Fprintf(warn, "warning: could not get the price (%v)\n", err)
	} else {
		out["price"] = p
	}
	return output.Success(deps.Stdout, out)
}

// resolveOutDir picks --out, else the ledger's dir, else config, else ./fal-output.
func resolveOutDir(cmd *cobra.Command, cfg config.Config, fromLedger string) string {
	if out, _ := cmd.Flags().GetString("out"); out != "" {
		return out
	}
	if fromLedger != "" {
		return fromLedger
	}
	if cfg.OutDir != "" {
		return cfg.OutDir
	}
	return fallbackOutDir
}

func timeoutFor(category string) time.Duration {
	if strings.Contains(category, "video") {
		return videoTimeout
	}
	return defaultTimeout
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type finishArgs struct {
	id, endpoint, outDir string
	timeout              time.Duration
	createdAt            time.Time
	uploaded             []string
}

// finish waits for the request, records the final state, downloads and emits.
// Every error carries details.requestId so a paid request is never lost.
func finish(cmd *cobra.Command, deps Deps, c *api.Client, paths config.Paths, a finishArgs) error {
	warn := warnWriter(cmd, deps)
	w := task.Waiter{Sleep: deps.Sleep, Now: deps.Now, Progress: warn}
	st, err := w.Wait(cmd.Context(), c, a.endpoint, a.id, a.timeout)
	if err != nil {
		if state := finalState(err); state != "" {
			recordFinal(paths, a, state, st.Error, warn)
		}
		return withRequestID(err, a.id, nil)
	}
	raw, err := task.GetResult(cmd.Context(), c, a.endpoint, a.id)
	if err != nil {
		if state := finalState(err); state != "" {
			recordFinal(paths, a, state, "", warn)
		}
		return withRequestID(err, a.id, nil)
	}
	recordFinal(paths, a, task.LedgerSuccess, "", warn)
	media := result.Extract(raw)
	files := []download.File{}
	if noDl, _ := cmd.Flags().GetBool("no-download"); !noDl && len(media) > 0 {
		if files, err = download.All(cmd.Context(), c.HTTP, media, a.outDir, a.endpoint, a.id); err != nil {
			return withRequestID(err, a.id, map[string]any{"media": media})
		}
	}
	return output.Success(deps.Stdout, map[string]any{
		"requestId": a.id, "model": a.endpoint, "state": task.LedgerSuccess,
		"files": files, "ids": result.IDs(a.id, raw, media), "result": raw,
		"inferenceTime": st.InferenceTime, "uploaded": nonNil(a.uploaded),
	})
}

// finalState maps a wait/result error to the ledger state. Transient errors
// (network, 5xx, rate limit) return "" and leave the ledger at submitted: the
// paid request may still be running on fal.
func finalState(err error) string {
	var oe *output.Error
	if !errors.As(err, &oe) {
		return ""
	}
	switch oe.Code {
	case output.CodeTimeout:
		return "timeout"
	case output.CodeTaskFailed:
		return task.LedgerFail
	}
	return ""
}

// recordFinal appends the final state; failures only warn (the result is already known).
func recordFinal(paths config.Paths, a finishArgs, state, note string, warn io.Writer) {
	if err := config.EnsureHome(paths); err != nil {
		_, _ = fmt.Fprintf(warn, "warning: could not record request %s in the ledger: %v\n", a.id, err)
		return
	}
	e := ledger.Entry{RequestID: a.id, Endpoint: a.endpoint, State: state, OutDir: a.outDir, CreatedAt: a.createdAt, Note: note}
	if err := ledger.Append(paths.Ledger, e); err != nil {
		_, _ = fmt.Fprintf(warn, "warning: could not record request %s in the ledger: %v\n", a.id, err)
	}
}

// withRequestID returns a new *output.Error from err with details.requestId
// (and extra) added. Non-output errors become code "api" keeping the message.
func withRequestID(err error, id string, extra map[string]any) error {
	var oe *output.Error
	var ae *api.Error
	switch {
	case errors.As(err, &oe):
	case errors.As(err, &ae):
		oe = ae.AsOutput()
	default:
		oe = &output.Error{Code: output.CodeAPI, Message: err.Error()}
	}
	details := make(map[string]any, len(oe.Details)+len(extra)+1)
	for k, v := range oe.Details {
		details[k] = v
	}
	for k, v := range extra {
		details[k] = v
	}
	details["requestId"] = id
	return &output.Error{Code: oe.Code, Message: oe.Message, Details: details}
}
