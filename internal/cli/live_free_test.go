//go:build live

package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/cli"
	"github.com/mik3dev/fal-cli/internal/schema"
	"github.com/mik3dev/fal-cli/internal/upload"
)

// Live tests talk to the real fal.ai API. They run only with FAL_E2E=1 and a
// key in FAL_API_KEY (loaded in the shell, never printed). One home directory
// is shared by the whole process so the ledger links the paid tests.
var (
	liveHomeOnce sync.Once
	liveHomeDir  string
)

func liveEnv(t *testing.T) map[string]string {
	t.Helper()
	if os.Getenv("FAL_E2E") != "1" {
		t.Skip("FAL_E2E != 1")
	}
	key := os.Getenv("FAL_API_KEY")
	if key == "" {
		t.Skip("FAL_API_KEY not set")
	}
	liveHomeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fal-cli-live-")
		if err != nil {
			t.Fatal(err)
		}
		liveHomeDir = dir
	})
	return map[string]string{"FAL_API_KEY": key, "FAL_CLI_HOME": liveHomeDir}
}

// liveRun runs the CLI against the real API. The key is never part of the returned text.
func liveRun(t *testing.T, args ...string) (int, map[string]any, string) {
	t.Helper()
	env := liveEnv(t)
	deps, out, errw := authDeps(t, "", env)
	code := cli.Execute(deps, args)
	stderr := errw.String()
	if strings.Contains(out.String(), env["FAL_API_KEY"]) || strings.Contains(stderr, env["FAL_API_KEY"]) {
		t.Fatal("the API key leaked into the CLI output")
	}
	return code, decode(t, out), stderr
}

var mediaCategories = []string{"text-to-image", "image-to-image", "text-to-video", "image-to-video", "video-to-video", "text-to-audio", "text-to-speech", "audio-to-audio"}

type sweepResult struct {
	id, outcome string // ok | validation | api | summarize
	suspicious  []string
	fileKeys    int
}

func TestLiveFreeCatalogAndSchemas(t *testing.T) {
	env := liveEnv(t)
	ids := map[string]bool{}
	for _, cat := range mediaCategories {
		cursor := ""
		for page := 0; page < 50; page++ {
			args := []string{"models", "list", "--category", cat, "--limit", "100"}
			if cursor != "" {
				args = append(args, "--cursor", cursor)
			}
			code, m, _ := liveRun(t, args...)
			if code != 0 {
				t.Fatalf("models list %s: %v", cat, m)
			}
			for _, e := range m["models"].([]any) {
				ids[e.(map[string]any)["endpoint_id"].(string)] = true
			}
			next, _ := m["nextCursor"].(string)
			if m["hasMore"] != true || next == "" {
				break
			}
			cursor = next
		}
	}
	all := make([]string, 0, len(ids))
	for id := range ids {
		all = append(all, id)
	}
	sort.Strings(all)
	t.Logf("%d active media endpoints", len(all))

	c := api.New(env["FAL_API_KEY"], func(string) string { return "" })
	cacheDir := filepath.Join(liveHomeDir, "cache")
	results := make([]sweepResult, len(all))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, id := range all {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = sweepOne(ctx, c, cacheDir, id)
		}(i, id)
	}
	wg.Wait()

	counts := map[string]int{}
	var failed, suspicious []string
	fileKeys := 0
	for _, r := range results {
		counts[r.outcome]++
		fileKeys += r.fileKeys
		if r.outcome != "ok" && r.outcome != "validation" {
			failed = append(failed, r.id+" ("+r.outcome+")")
		}
		for _, s := range r.suspicious {
			suspicious = append(suspicious, r.id+": "+s)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Live sweep %s\n\nendpoints: %d — ok %d, validation(404) %d, api %d, summarize %d\nfile keys detected: %d\n\n", time.Now().UTC().Format(time.RFC3339), len(all), counts["ok"], counts["validation"], counts["api"], counts["summarize"], fileKeys)
	fmt.Fprintf(&b, "## Failed (%d)\n\n", len(failed))
	for _, f := range failed {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	fmt.Fprintf(&b, "\n## Suspicious string fields not detected as files (%d)\n\n", len(suspicious))
	for _, s := range suspicious {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	if err := os.WriteFile("../../testdata/live/REPORT-sweep.md", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("ok %d, validation %d, api %d, summarize %d, suspicious %d", counts["ok"], counts["validation"], counts["api"], counts["summarize"], len(suspicious))
	if counts["ok"] == 0 || counts["summarize"] > 0 {
		t.Fatalf("sweep: %v", failed)
	}
	// A sample goes through the CLI itself (served from the cache populated above).
	for _, id := range all[:min(20, len(all))] {
		if code, m, _ := liveRun(t, "models", "schema", id); code != 0 || m["fields"] == nil || m["output"] == nil {
			t.Errorf("models schema %s: code=%d m=%v", id, code, m)
		}
	}
}

var mediaName = regexp.MustCompile(`(^|_)(image|video|audio|mask|file)s?$`)

// exampleIsURL reports whether the property's first example is an http(s) URL.
func exampleIsURL(input map[string]any, key string) bool {
	prop, ok := schema.PropSchema(input, key)
	if !ok {
		return false
	}
	ex, _ := prop["examples"].([]any)
	if len(ex) == 0 {
		return false
	}
	s, _ := ex[0].(string)
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func sweepOne(ctx context.Context, c *api.Client, cacheDir, id string) sweepResult {
	r := sweepResult{id: id}
	doc, err := schema.Fetch(ctx, c, cacheDir, id, false, time.Now())
	if err != nil {
		r.outcome = "api"
		if strings.Contains(err.Error(), "unknown model") {
			r.outcome = "validation"
		}
		return r
	}
	s, err := schema.Summarize(doc)
	if err != nil {
		r.outcome = "summarize"
		return r
	}
	r.outcome = "ok"
	for _, f := range s.Fields {
		if upload.SchemaFileKey(doc.Input, f.Name) {
			r.fileKeys++
			continue
		}
		if f.Type != "string" {
			continue
		}
		// A string field whose example is an https URL, or whose name ends in a media word, is a likely miss.
		if exampleIsURL(doc.Input, f.Name) || mediaName.MatchString(f.Name) {
			r.suspicious = append(r.suspicious, f.Name)
		}
	}
	return r
}

func TestLiveFreeDryRunPresets(t *testing.T) {
	liveEnv(t)
	_, list, _ := liveRun(t, "preset", "list")
	for _, p := range list["presets"].([]any) {
		name := p.(map[string]any)["name"].(string)
		args := []string{"run", "@" + name, "--dry-run"}
		for _, kv := range presetFill[name] {
			args = append(args, "-p", kv)
		}
		code, m, _ := liveRun(t, args...)
		if code != 0 || m["price"] == nil {
			t.Errorf("%s: code=%d m=%v", name, code, m)
		}
	}
}

func TestLiveFreeCreditsShape(t *testing.T) {
	liveEnv(t)
	code, m, _ := liveRun(t, "credits")
	switch code {
	case 0:
		if _, ok := m["balance"].(float64); !ok {
			t.Fatalf("balance missing: %v", m)
		}
	case 3:
		if !strings.Contains(m["error"].(map[string]any)["message"].(string), "BILLING") {
			t.Fatalf("auth error without BILLING hint: %v", m)
		}
	default:
		t.Fatalf("code=%d m=%v", code, m)
	}
}
