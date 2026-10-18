//go:build live

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// budget caps the USD spent by one `go test -tags live` run (FAL_E2E_BUDGET, default 2).
var budget = struct {
	sync.Mutex
	spent float64
}{}

func budgetLimit() float64 {
	if v, err := strconv.ParseFloat(os.Getenv("FAL_E2E_BUDGET"), 64); err == nil && v >= 0 {
		return v
	}
	return 2
}

// reserve prices endpoint, estimates cost = unit_price × units, skips the test
// when it does not fit the remaining budget and books it otherwise.
func reserve(t *testing.T, endpoint string, units float64) float64 {
	t.Helper()
	code, m, _ := liveRun(t, "models", "price", endpoint)
	if code != 0 {
		t.Skipf("no price for %s: %v", endpoint, m["error"])
	}
	p := m["price"].(map[string]any)
	cost := p["unit_price"].(float64) * units
	budget.Lock()
	defer budget.Unlock()
	if budget.spent+cost > budgetLimit() {
		t.Skipf("cost ≈ %.4f USD (%v %s) exceeds the remaining budget (%.4f of %.2f spent)", cost, p["unit_price"], p["unit"], budget.spent, budgetLimit())
	}
	budget.spent += cost
	t.Logf("cost ≈ %.4f USD (%v per %s); spent so far %.4f", cost, p["unit_price"], p["unit"], budget.spent)
	return cost
}

// capture stores the raw result payload as a fixture; it refuses to write if the key appears.
func capture(t *testing.T, name string, m map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(m["result"], "", "  ")
	if err != nil || m["result"] == nil {
		return
	}
	if strings.Contains(string(raw), os.Getenv("FAL_API_KEY")) {
		t.Fatal("result payload contains the API key; not written")
	}
	dir := "../../testdata/live/results"
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, name+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func firstFile(t *testing.T, m map[string]any) (path, kind string) {
	t.Helper()
	files, _ := m["files"].([]any)
	if len(files) == 0 {
		t.Fatalf("no files: %v", m)
	}
	f := files[0].(map[string]any)
	path, kind = f["path"].(string), f["kind"].(string)
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		t.Fatalf("file %s: %v", path, err)
	}
	return path, kind
}

func requireOK(t *testing.T, code int, m map[string]any) {
	t.Helper()
	if code != 0 || m["ok"] != true || m["state"] != "success" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

var firstImage string // written by TestLivePaid1Image, read by TestLivePaid3EditUploadChain

func TestLivePaid1Image(t *testing.T) {
	liveEnv(t)
	reserve(t, "fal-ai/flux/schnell", 1.1)     // square_hd ≈ 1.05 megapixels
	out := filepath.Join(liveHomeDir, "files") // outlives this test: TestLivePaid3EditUploadChain uploads the image
	code, m, _ := liveRun(t, "run", "@imagen-rapida", "-p", "prompt=a red fox, watercolor", "-p", "image_size=square", "--out", out)
	requireOK(t, code, m)
	path, kind := firstFile(t, m)
	ids := m["ids"].(map[string]any)
	if kind != "image" || ids["images_url"] == nil || ids["seed"] == nil {
		t.Fatalf("kind=%s ids=%v", kind, ids)
	}
	firstImage = path
	capture(t, "flux-schnell", m)
}

func TestLivePaid2Voice(t *testing.T) {
	liveEnv(t)
	const text = "Hola desde fal"
	reserve(t, "fal-ai/elevenlabs/tts/multilingual-v2", float64(len(text))/1000)
	code, m, _ := liveRun(t, "run", "@voz", "-p", "text="+text, "--out", t.TempDir())
	requireOK(t, code, m)
	_, kind := firstFile(t, m)
	if kind != "audio" || m["ids"].(map[string]any)["audio_url"] == nil {
		t.Fatalf("kind=%s m=%v", kind, m)
	}
	capture(t, "elevenlabs-tts", m)
}

func TestLivePaid3EditUploadChain(t *testing.T) {
	liveEnv(t)
	if firstImage == "" {
		t.Skip("needs the image of TestLivePaid1Image")
	}
	reserve(t, "fal-ai/nano-banana-2/edit", 1)
	code, m, stderr := liveRun(t, "run", "@imagen-edicion", "-p", "prompt=make it night, keep the fox", "-p", "image_urls="+firstImage, "--out", t.TempDir())
	requireOK(t, code, m)
	firstFile(t, m)
	up, _ := m["uploaded"].([]any)
	if len(up) != 1 || up[0] != firstImage || !strings.Contains(stderr, "uploading") {
		t.Fatalf("uploaded=%v stderr=%q", up, stderr)
	}
	capture(t, "nano-banana-2-edit", m)
}

func TestLivePaid4NoWaitThenWait(t *testing.T) {
	liveEnv(t)
	reserve(t, "fal-ai/flux/schnell", 1.1)
	code, m, stderr := liveRun(t, "run", "fal-ai/flux/schnell", "-p", "prompt=a lighthouse at dawn", "-p", "image_size=square", "--no-wait")
	if code != 0 || m["state"] != "submitted" || !strings.Contains(stderr, "expires") {
		t.Fatalf("code=%d m=%v stderr=%q", code, m, stderr)
	}
	id := m["requestId"].(string)
	code, g, _ := liveRun(t, "task", "get", id)
	if code != 0 && code != 5 {
		t.Fatalf("task get: code=%d m=%v", code, g)
	}
	code, w, _ := liveRun(t, "task", "wait", id, "--out", t.TempDir())
	requireOK(t, code, w)
	firstFile(t, w)
	_, l, _ := liveRun(t, "task", "list", "--limit", "5")
	found := false
	for _, e := range l["tasks"].([]any) {
		if em := e.(map[string]any); em["requestId"] == id && em["state"] == "success" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ledger: %v", l)
	}
}

func TestLivePaid5Music(t *testing.T) {
	liveEnv(t)
	reserve(t, "fal-ai/minimax-music/v2.6", 1)
	code, m, _ := liveRun(t, "run", "@musica", "-p", "prompt=lofi hip hop, rainy night, mellow piano", "-p", "lyrics=[verse] rain on the window, city lights so low [chorus] stay a while", "--out", t.TempDir(), "--timeout", "15m")
	requireOK(t, code, m)
	if _, kind := firstFile(t, m); kind != "audio" {
		t.Fatalf("kind=%s", kind)
	}
	capture(t, "minimax-music-v2.6", m)
}

func TestLivePaid6CheapVideo(t *testing.T) {
	liveEnv(t)
	reserve(t, "minimax/h3-max-turbo/text-to-video", 5)
	code, m, _ := liveRun(t, "run", "@video-barato", "-p", "prompt=a drone shot over a misty forest at sunrise", "--out", t.TempDir(), "--timeout", "15m")
	requireOK(t, code, m)
	_, kind := firstFile(t, m)
	if kind != "video" || m["ids"].(map[string]any)["video_url"] == nil {
		t.Fatalf("kind=%s m=%v", kind, m)
	}
	capture(t, "h3-max-turbo-t2v", m)
}

func TestLivePaid7ServerValidation(t *testing.T) {
	liveEnv(t)
	reserve(t, "fal-ai/flux/schnell", 1.1) // charged only if fal accepts the bad enum
	code, m, _ := liveRun(t, "run", "fal-ai/flux/schnell", "-p", "prompt=a red fox", "-p", "output_format=bmp", "--no-validate", "--no-download")
	e, _ := m["error"].(map[string]any)
	if code != 2 || e["code"] != "validation" || e["details"].(map[string]any)["problems"] == nil {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestLivePaid8CancelQueued(t *testing.T) {
	liveEnv(t)
	reserve(t, "minimax/h3-max-turbo/text-to-video", 5) // charged if the cancel arrives too late
	code, m, _ := liveRun(t, "run", "@video-barato", "-p", "prompt=a cat sleeping on a sofa", "--no-wait")
	if code != 0 {
		t.Fatalf("submit: %v", m)
	}
	id := m["requestId"].(string)
	code, c, _ := liveRun(t, "task", "cancel", id)
	if code != 0 || (c["status"] != "CANCELLATION_REQUESTED" && c["status"] != "ALREADY_COMPLETED") {
		t.Fatalf("cancel: code=%d m=%v", code, c)
	}
	t.Logf("cancel → %v", c["status"])
}
