package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/cli"
	"github.com/mik3dev/fal-cli/internal/ledger"
)

// fal is a TLS fake of every fal host: schema, queue, storage, CDN and pricing.
type fal struct {
	srv        *httptest.Server
	mu         sync.Mutex
	submits    []map[string]any
	statuses   []string // consumed in order; last repeats
	result     string
	resultCode int // 0 means 200
	uploads    int
}

func newFal(t *testing.T) *fal {
	t.Helper()
	f := &fal{statuses: []string{`{"status":"COMPLETED","metrics":{"inference_time":0.7}}`}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/api/openapi/queue/openapi.json" && r.URL.Query().Get("endpoint_id") == "fal-ai/fake/mask":
			_, _ = w.Write([]byte(maskOpenAPI))
		case p == "/api/openapi/queue/openapi.json":
			id := strings.ReplaceAll(r.URL.Query().Get("endpoint_id"), "/", "__")
			b, err := os.ReadFile("../../testdata/live/schemas/" + id + ".json")
			if err != nil {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`null`))
				return
			}
			_, _ = w.Write(b)
		case p == "/v1/models/pricing":
			_, _ = w.Write([]byte(`{"prices":[{"endpoint_id":"` + r.URL.Query().Get("endpoint_id") + `","unit_price":0.003,"unit":"megapixels","currency":"USD"}]}`))
		case p == "/storage/upload/initiate":
			f.uploads++
			_, _ = w.Write([]byte(`{"file_url":"https://v3.fal.media/files/up.png","upload_url":"` + f.srv.URL + `/put/up.png"}`))
		case strings.HasPrefix(p, "/put/"):
			_, _ = io.Copy(io.Discard, r.Body)
		case strings.HasPrefix(p, "/cdn/"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("JPEG"))
		case strings.HasSuffix(p, "/status"):
			s := f.statuses[0]
			if len(f.statuses) > 1 {
				f.statuses = f.statuses[1:]
			}
			if rest, ok := strings.CutPrefix(s, "500:"); ok {
				w.WriteHeader(500)
				s = rest
			}
			_, _ = w.Write([]byte(s))
		case r.Method == "POST":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.submits = append(f.submits, body)
			_, _ = w.Write([]byte(`{"request_id":"req-1","status_url":"s","response_url":"r","cancel_url":"c"}`))
		case strings.Contains(p, "/requests/"):
			if f.resultCode != 0 {
				w.WriteHeader(f.resultCode)
			}
			_, _ = w.Write([]byte(f.result))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	f.result = `{"images":[{"url":"` + f.srv.URL + `/cdn/a.jpeg","content_type":"image/jpeg","width":1,"height":1}],"seed":7,"prompt":"x"}`
	return f
}

// maskOpenAPI is a minimal queue document whose file field has no _url suffix.
const maskOpenAPI = `{"openapi":"3.0.4","info":{"title":"mask","x-fal-metadata":{"category":"image-to-image"}},
"paths":{"/fal-ai/fake/mask":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/MaskInput"}}}}}},
"/fal-ai/fake/mask/requests/{request_id}":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/MaskOutput"}}}}}}}},
"components":{"schemas":{
"MaskInput":{"type":"object","required":["prompt"],"properties":{"prompt":{"type":"string"},"mask":{"type":"string","ui":{"field":"image"}}}},
"MaskOutput":{"type":"object","properties":{"image":{"type":"object","properties":{"url":{"type":"string"},"content_type":{"type":"string"}}}}}}}}`

type sleeps struct {
	mu   sync.Mutex
	now  time.Time
	list []time.Duration
}

// runFal runs the CLI against f with a fake clock advanced by Sleep.
func runFal(t *testing.T, f *fal, home string, args ...string) (int, map[string]any, string, *sleeps) {
	t.Helper()
	env := apiEnv(f.srv)
	env["FAL_CLI_HOME"] = home
	deps, out, errw := authDeps(t, "", env)
	s := &sleeps{now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	deps.Now = func() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.now }
	deps.Sleep = func(_ context.Context, d time.Duration) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.list = append(s.list, d)
		s.now = s.now.Add(d)
		return nil
	}
	deps.Transport = f.srv.Client().Transport
	code := cli.Execute(deps, args)
	return code, decode(t, out), errw.String(), s
}

func TestRunHappyPathDownloads(t *testing.T) {
	f := newFal(t)
	home, outDir := filepath.Join(t.TempDir(), "home"), filepath.Join(t.TempDir(), "out")
	code, m, _, _ := runFal(t, f, home, "run", "fal-ai/flux/schnell", "-p", "prompt=a fox", "-p", "num_images=2", "--out", outDir)
	if code != 0 || m["state"] != "success" || m["requestId"] != "req-1" || m["model"] != "fal-ai/flux/schnell" || m["inferenceTime"] != 0.7 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	files, _ := m["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v", m["files"])
	}
	path := files[0].(map[string]any)["path"].(string)
	if filepath.Base(path) != "fal-ai-flux-schnell_req-1_0.jpeg" || files[0].(map[string]any)["kind"] != "image" {
		t.Fatalf("file = %v", files[0])
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "JPEG" {
		t.Fatalf("content = %q err=%v", b, err)
	}
	ids, _ := m["ids"].(map[string]any)
	if ids["request_id"] != "req-1" || ids["seed"] != 7.0 || ids["images_url"] == nil {
		t.Fatalf("ids = %v", ids)
	}
	if res, _ := m["result"].(map[string]any); res["images"] == nil {
		t.Fatalf("result = %v", m["result"])
	}
	if up, _ := m["uploaded"].([]any); up == nil || len(up) != 0 {
		t.Fatalf("uploaded = %v", m["uploaded"])
	}
	if f.submits[0]["prompt"] != "a fox" || f.submits[0]["num_images"] != 2.0 {
		t.Fatalf("submit body = %v", f.submits[0])
	}
	e, ok := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-1")
	if !ok || e.State != "success" || e.OutDir != outDir {
		t.Fatalf("ledger = %+v", e)
	}
}

func TestRunUnknownModel(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/nope", "-p", "prompt=x")
	if code != 2 || m["error"].(map[string]any)["code"] != "validation" || len(f.submits) != 0 {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunDryRunNoSubmitNoUpload(t *testing.T) {
	f := newFal(t)
	png := filepath.Join(t.TempDir(), "x.png")
	_ = os.WriteFile(png, []byte("P"), 0o600)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/nano-banana-2/edit", "-p", "prompt=fox", "-p", "image_urls="+png, "-p", "seed=3", "--dry-run")
	if code != 0 || m["dryRun"] != true || m["model"] != "fal-ai/nano-banana-2/edit" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	in, _ := m["input"].(map[string]any)
	urls, _ := in["image_urls"].([]any)
	if in["prompt"] != "fox" || in["seed"] != 3.0 || len(urls) != 1 || urls[0] != png {
		t.Fatalf("input = %v", in)
	}
	pend, _ := m["pendingUploads"].([]any)
	if len(pend) != 1 || pend[0] != png {
		t.Fatalf("pendingUploads = %v", m["pendingUploads"])
	}
	if price, _ := m["price"].(map[string]any); price["unit"] != "megapixels" {
		t.Fatalf("price = %v", m["price"])
	}
	if len(f.submits) != 0 || f.uploads != 0 {
		t.Fatalf("submits=%d uploads=%d", len(f.submits), f.uploads)
	}
}

func TestRunNoWait(t *testing.T) {
	f := newFal(t)
	home := filepath.Join(t.TempDir(), "h")
	code, m, _, _ := runFal(t, f, home, "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "--no-wait")
	if code != 0 || m["state"] != "submitted" || m["requestId"] != "req-1" || m["ids"].(map[string]any)["request_id"] != "req-1" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if e, ok := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-1"); !ok || e.State != "submitted" {
		t.Fatalf("ledger = %+v", e)
	}
}

func TestRunUploadsLocalImage(t *testing.T) {
	f := newFal(t)
	png := filepath.Join(t.TempDir(), "x.png")
	_ = os.WriteFile(png, []byte("P"), 0o600)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/kling-video/v2.5-turbo/pro/image-to-video", "-p", "prompt=fox", "-p", "image_url="+png, "--no-wait")
	if code != 0 || f.uploads != 1 || f.submits[0]["image_url"] != "https://v3.fal.media/files/up.png" {
		t.Fatalf("code=%d m=%v submits=%v", code, m, f.submits)
	}
	if up, _ := m["uploaded"].([]any); len(up) != 1 || up[0] != png {
		t.Fatalf("uploaded = %v", m["uploaded"])
	}
}

func TestRunRepeatedParamBuildsArray(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/nano-banana-2/edit", "-p", "prompt=fox", "-p", "image_urls=https://a/1.png", "-p", "image_urls=https://a/2.png", "--dry-run")
	in, _ := m["input"].(map[string]any)
	arr, _ := in["image_urls"].([]any)
	if code != 0 || len(arr) != 2 || arr[1] != "https://a/2.png" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunInputFileAndParamOverride(t *testing.T) {
	f := newFal(t)
	in := filepath.Join(t.TempDir(), "in.json")
	_ = os.WriteFile(in, []byte(`{"prompt":"from file","seed":1}`), 0o600)
	code, m, errOut, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "--input", in, "-p", "seed=9", "--dry-run", "--no-validate")
	got, _ := m["input"].(map[string]any)
	if code != 0 || got["prompt"] != "from file" || got["seed"] != 9.0 || strings.Contains(errOut, "warning") {
		t.Fatalf("code=%d m=%v err=%q", code, m, errOut)
	}
}

func TestRunTaskFailedCarriesRequestID(t *testing.T) {
	f := newFal(t)
	f.statuses = []string{`{"status":"IN_PROGRESS"}`, `{"status":"COMPLETED","error":"nsfw","error_type":"content_policy_violation"}`}
	home := filepath.Join(t.TempDir(), "h")
	code, m, _, _ := runFal(t, f, home, "run", "fal-ai/flux/schnell", "-p", "prompt=fox")
	e, _ := m["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	if code != 5 || e["code"] != "task_failed" || d["requestId"] != "req-1" || d["errorType"] != "content_policy_violation" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if le, _ := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-1"); le.State != "fail" {
		t.Fatalf("ledger = %+v", le)
	}
}

func TestRunNoDownloadKeepsURLs(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "--no-download")
	files, _ := m["files"].([]any)
	if code != 0 || files == nil || len(files) != 0 || m["ids"].(map[string]any)["images_url"] == nil {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunDefaultTimeoutVideo(t *testing.T) {
	f := newFal(t)
	f.statuses = []string{`{"status":"IN_PROGRESS"}`}
	code, m, _, s := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/veo3.1/fast", "-p", "prompt=fox")
	e, _ := m["error"].(map[string]any)
	var total time.Duration
	for _, d := range s.list {
		total += d
	}
	if code != 6 || e["code"] != "timeout" || total != 30*time.Minute || e["details"].(map[string]any)["requestId"] != "req-1" {
		t.Fatalf("code=%d m=%v total=%s", code, m, total)
	}
	f2 := newFal(t)
	f2.statuses = []string{`{"status":"IN_QUEUE","queue_position":1}`}
	_, _, _, s2 := runFal(t, f2, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox")
	total = 0
	for _, d := range s2.list {
		total += d
	}
	if total != 10*time.Minute {
		t.Fatalf("image total = %s", total)
	}
	_, _, _, s3 := runFal(t, f2, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "--timeout", "5s")
	total = 0
	for _, d := range s3.list {
		total += d
	}
	if total != 5*time.Second {
		t.Fatalf("flag total = %s", total)
	}
}

func TestTaskWaitDownloadsUsingLedgerEndpoint(t *testing.T) {
	f := newFal(t)
	home, outDir := filepath.Join(t.TempDir(), "h"), filepath.Join(t.TempDir(), "out")
	_ = os.MkdirAll(home, 0o700)
	_ = ledger.Append(filepath.Join(home, "tasks.jsonl"), ledger.Entry{RequestID: "req-1", Endpoint: "fal-ai/flux/schnell", State: "submitted", OutDir: outDir, CreatedAt: time.Now()})
	code, m, errOut, _ := runFal(t, f, home, "task", "wait", "req-1")
	files, _ := m["files"].([]any)
	if code != 0 || m["state"] != "success" || len(files) != 1 || !strings.HasPrefix(files[0].(map[string]any)["path"].(string), outDir) {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if !strings.Contains(errOut, "expires") {
		t.Fatalf("stderr = %q", errOut)
	}
	if e, _ := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-1"); e.State != "success" {
		t.Fatalf("ledger = %+v", e)
	}
	code, m, _, _ = runFal(t, f, filepath.Join(t.TempDir(), "h2"), "task", "wait", "req-9")
	if code != 2 || m["error"].(map[string]any)["code"] != "usage" {
		t.Fatalf("no ledger: code=%d m=%v", code, m)
	}
}

func TestRunTransientPollErrorKeepsSubmitted(t *testing.T) {
	f := newFal(t)
	f.statuses = []string{`500:boom`}
	home := filepath.Join(t.TempDir(), "h")
	code, m, _, _ := runFal(t, f, home, "run", "fal-ai/flux/schnell", "-p", "prompt=fox")
	e, _ := m["error"].(map[string]any)
	if code != 1 || e["code"] != "api" || e["details"].(map[string]any)["requestId"] != "req-1" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if le, _ := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-1"); le.State != "submitted" {
		t.Fatalf("ledger = %+v", le)
	}
	f2 := newFal(t)
	f2.statuses = []string{`{"status":"IN_PROGRESS"}`}
	home2 := filepath.Join(t.TempDir(), "h2")
	runFal(t, f2, home2, "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "--timeout", "3s")
	if le, _ := ledger.Find(filepath.Join(home2, "tasks.jsonl"), "req-1"); le.State != "timeout" {
		t.Fatalf("timeout ledger = %+v", le)
	}
}

func TestRunCoercesTypedParams(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", "seed=5", "-p", "enable_safety_checker=false", "-p", "image_size=landscape_4_3", "-p", "num_images=2", "--no-wait")
	if code != 0 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	b := f.submits[0]
	if b["seed"] != 5.0 || b["enable_safety_checker"] != false || b["image_size"] != "landscape_4_3" || b["num_images"] != 2.0 {
		t.Fatalf("body = %v", b)
	}
}

func TestRunObjectParamJSON(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", `image_size={"width":512,"height":512}`, "--dry-run")
	in, _ := m["input"].(map[string]any)
	size, _ := in["image_size"].(map[string]any)
	if code != 0 || size["width"] != 512.0 {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunValidationErrorExit2(t *testing.T) {
	f := newFal(t)
	png := filepath.Join(t.TempDir(), "x.png")
	_ = os.WriteFile(png, []byte("P"), 0o600)
	code, _, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", "num_images=99", "-p", "image_urls="+png, "--no-validate", "--dry-run")
	if code != 0 || f.uploads != 0 {
		t.Fatalf("no-validate dry-run: code=%d uploads=%d", code, f.uploads)
	}
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", "num_images=99")
	e, _ := m["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	probs, _ := details["problems"].([]any)
	msg, _ := e["message"].(string)
	if code != 2 || e["code"] != "validation" || len(probs) == 0 || !strings.Contains(msg, "--no-validate") {
		t.Fatalf("code=%d m=%v", code, m)
	}
	p0 := probs[0].(map[string]any)
	if p0["field"] != "num_images" || p0["got"] != 99.0 {
		t.Fatalf("problem = %v", p0)
	}
	if len(f.submits) != 0 || f.uploads != 0 {
		t.Fatalf("submits=%d uploads=%d", len(f.submits), f.uploads)
	}
}

func TestRunUnknownFieldIsValidation(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "promt=fox")
	e, _ := m["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	probs, _ := details["problems"].([]any)
	fields := map[string]bool{}
	for _, p := range probs {
		fields[p.(map[string]any)["field"].(string)] = true
	}
	if code != 2 || !fields["promt"] || !fields["prompt"] {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunNoValidateSendsWithWarning(t *testing.T) {
	f := newFal(t)
	code, m, errOut, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", "num_images=99", "--no-validate", "--no-wait")
	if code != 0 || f.submits[0]["num_images"] != 99.0 || !strings.Contains(errOut, "warning: validation: num_images") {
		t.Fatalf("code=%d m=%v err=%q", code, m, errOut)
	}
}

func TestRunDryRunReportsProblemsUnderNoValidate(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", "num_images=99", "--dry-run", "--no-validate")
	probs, _ := m["problems"].([]any)
	if code != 0 || len(probs) != 1 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	code, m, _, _ = runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", "num_images=99", "--dry-run")
	if code != 2 {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunUploadsUIFieldFile(t *testing.T) {
	f := newFal(t)
	png := filepath.Join(t.TempDir(), "m.png")
	_ = os.WriteFile(png, []byte("P"), 0o600)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/fake/mask", "-p", "prompt=x", "-p", "mask="+png, "--no-wait")
	if code != 0 || f.uploads != 1 || f.submits[0]["mask"] != "https://v3.fal.media/files/up.png" {
		t.Fatalf("code=%d m=%v submits=%v", code, m, f.submits)
	}
	code, m, _, _ = runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/fake/mask", "-p", "prompt=x", "-p", "mask="+png, "--dry-run")
	pend, _ := m["pendingUploads"].([]any)
	if code != 0 || len(pend) != 1 || pend[0] != png {
		t.Fatalf("dry-run: code=%d m=%v", code, m)
	}
}

func TestRunWarnsBeforeEachUpload(t *testing.T) {
	f := newFal(t)
	png := filepath.Join(t.TempDir(), "x.png")
	_ = os.WriteFile(png, []byte("P"), 0o600)
	_, _, errOut, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/kling-video/v2.5-turbo/pro/image-to-video", "-p", "prompt=fox", "-p", "image_url="+png, "--no-wait")
	if !strings.Contains(errOut, "uploading "+png+" (image_url)") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestRunPromptPathNeverUploaded(t *testing.T) {
	f := newFal(t)
	txt := filepath.Join(t.TempDir(), "README.md")
	_ = os.WriteFile(txt, []byte("hi"), 0o600)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt="+txt, "--no-wait")
	if code != 0 || f.uploads != 0 || f.submits[0]["prompt"] != txt {
		t.Fatalf("code=%d m=%v submits=%v", code, m, f.submits)
	}
}

func TestRunNoWaitWarnsExpiry(t *testing.T) {
	f := newFal(t)
	_, _, errw, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "--no-wait")
	if !strings.Contains(errw, "expires") || !strings.Contains(errw, "task wait req-1") {
		t.Fatalf("stderr = %q", errw)
	}
}

func TestRunDownloadFailureKeepsLedgerNote(t *testing.T) {
	f := newFal(t)
	f.result = `{"images":[{"url":"` + f.srv.URL + `/cdn/ok.jpeg","content_type":"image/jpeg"},{"url":"` + f.srv.URL + `/missing/a.jpeg","content_type":"image/jpeg"}]}`
	home := filepath.Join(t.TempDir(), "h")
	code, m, _, _ := runFal(t, f, home, "run", "fal-ai/flux/schnell", "-p", "prompt=fox")
	e, _ := m["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	if code != 1 || e["code"] != "io" || d["requestId"] != "req-1" || d["media"] == nil {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if saved, _ := d["files"].([]any); len(saved) != 1 {
		t.Fatalf("partial files not reported: %v", d)
	}
	le, ok := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-1")
	if !ok || le.State != "success" || !strings.Contains(le.Note, "not downloaded") {
		t.Fatalf("ledger = %+v", le)
	}
}

// A file field holding a non-URL value that is not a local file is sent as text, with a warning.
func TestRunWarnsWhenFileValueIsNeitherURLNorFile(t *testing.T) {
	f := newFal(t)
	code, m, stderr, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/kling-video/v2.5-turbo/pro/image-to-video", "-p", "prompt=a cat turns its head", "-p", "image_url=./gone.png", "--dry-run")
	if code != 0 || m["pendingUploads"].([]any) == nil || len(m["pendingUploads"].([]any)) != 0 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if !strings.Contains(stderr, "warning: image_url: ./gone.png is not a URL and no such local file exists") {
		t.Fatalf("stderr = %q", stderr)
	}
	code, _, stderr, _ = runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "fal-ai/kling-video/v2.5-turbo/pro/image-to-video", "-p", "prompt=a cat turns its head", "-p", "image_url=https://x/a.png", "--dry-run")
	if code != 0 || strings.Contains(stderr, "no such local file") {
		t.Fatalf("url must not warn: %q", stderr)
	}
}

// A server-side 422 on the result is a validation error (exit 2) and the ledger records the failure.
func TestRunServer422IsValidationAndLedgerFail(t *testing.T) {
	f := newFal(t)
	f.resultCode, f.result = 422, `{"detail":[{"loc":["body","output_format"],"msg":"Input should be 'jpeg' or 'png'","type":"literal_error"}]}`
	home := filepath.Join(t.TempDir(), "h")
	code, m, _, _ := runFal(t, f, home, "run", "fal-ai/flux/schnell", "-p", "prompt=fox", "-p", "output_format=bmp", "--no-validate")
	e, _ := m["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	probs, _ := d["problems"].([]any)
	if code != 2 || e["code"] != "validation" || d["requestId"] != "req-1" || len(probs) != 1 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if le, _ := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-1"); le.State != "fail" || !strings.Contains(le.Note, "rejected") {
		t.Fatalf("ledger = %+v", le)
	}
}
