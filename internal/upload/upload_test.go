package upload_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/upload"
)

type storage struct {
	mu        sync.Mutex
	initiates []map[string]any
	puts      []string // bodies
	putCT     []string
	putAuth   []string
	n         int
}

func (s *storage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == "POST" && r.URL.Path == "/storage/upload/initiate":
		if r.URL.Query().Get("storage_type") != "fal-cdn-v3" || r.Header.Get("Authorization") == "" {
			w.WriteHeader(400)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.initiates = append(s.initiates, body)
		s.n++
		name, _ := body["file_name"].(string)
		resp := map[string]string{"file_url": "https://v3.fal.media/files/" + name, "upload_url": "http://" + r.Host + "/put/" + name + "?sig=x"}
		_ = json.NewEncoder(w).Encode(resp)
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/put/"):
		b, _ := io.ReadAll(r.Body)
		s.puts = append(s.puts, string(b))
		s.putCT = append(s.putCT, r.Header.Get("Content-Type"))
		s.putAuth = append(s.putAuth, r.Header.Get("Authorization"))
		w.WriteHeader(200)
	default:
		w.WriteHeader(404)
	}
}

func setup(t *testing.T) (*api.Client, *storage) {
	t.Helper()
	s := &storage{}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	c := api.New("k-secret", func(string) string { return "" })
	c.StorageURL = srv.URL
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c, s
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIsFileKey(t *testing.T) {
	for k, want := range map[string]bool{"image_url": true, "image_urls": true, "prompt": false, "url": false, "video_url": true, "image_url_x": false} {
		if got := upload.IsFileKey(k); got != want {
			t.Errorf("IsFileKey(%q) = %v", k, got)
		}
	}
}

func TestOneInitiatesThenPuts(t *testing.T) {
	c, s := setup(t)
	p := writeFile(t, "a.png", "PNGDATA")
	url, err := upload.One(context.Background(), c, p)
	if err != nil || url != "https://v3.fal.media/files/a.png" {
		t.Fatalf("url=%q err=%v", url, err)
	}
	if len(s.initiates) != 1 || s.initiates[0]["content_type"] != "image/png" || s.initiates[0]["file_name"] != "a.png" {
		t.Fatalf("initiates = %v", s.initiates)
	}
	if len(s.puts) != 1 || s.puts[0] != "PNGDATA" || s.putCT[0] != "image/png" || s.putAuth[0] != "" {
		t.Fatalf("puts=%v ct=%v auth=%v", s.puts, s.putCT, s.putAuth)
	}
}

func TestOneUnknownExtensionIsOctetStream(t *testing.T) {
	c, s := setup(t)
	p := writeFile(t, "blob.xyz123", "x")
	if _, err := upload.One(context.Background(), c, p); err != nil {
		t.Fatal(err)
	}
	if s.initiates[0]["content_type"] != "application/octet-stream" {
		t.Fatalf("initiates = %v", s.initiates)
	}
}

func TestOneTooLargeIsIO(t *testing.T) {
	c, _ := setup(t)
	p := filepath.Join(t.TempDir(), "big.bin")
	f, _ := os.Create(p)
	if err := f.Truncate(90<<20 + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_, err := upload.One(context.Background(), c, p)
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeIO || !strings.Contains(oe.Message, "90 MiB") {
		t.Fatalf("err = %v", err)
	}
	_, err = upload.One(context.Background(), c, filepath.Join(t.TempDir(), "missing.png"))
	if !errors.As(err, &oe) || oe.Code != output.CodeIO {
		t.Fatalf("missing err = %v", err)
	}
}

func TestOnePutFailureIsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"file_url": "https://f", "upload_url": "http://" + r.Host + "/put"})
	}))
	t.Cleanup(srv.Close)
	c := api.New("k", func(string) string { return "" })
	c.StorageURL = srv.URL
	_, err := upload.One(context.Background(), c, writeFile(t, "a.png", "x"))
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != 403 {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveUploadsOnlyFileKeys(t *testing.T) {
	c, s := setup(t)
	png := writeFile(t, "a.png", "P")
	in := map[string]any{"prompt": "README.md", "image_url": png, "seed": 3}
	out, uploaded, err := upload.Resolve(context.Background(), c, in, fileExists)
	if err != nil {
		t.Fatal(err)
	}
	if out["prompt"] != "README.md" || out["image_url"] != "https://v3.fal.media/files/a.png" || out["seed"] != 3 {
		t.Fatalf("out = %v", out)
	}
	if len(uploaded) != 1 || uploaded[0] != png || len(s.puts) != 1 {
		t.Fatalf("uploaded = %v", uploaded)
	}
	// Missing local path is left as is for the API to reject.
	out, uploaded, err = upload.Resolve(context.Background(), c, map[string]any{"image_url": "./nope.png"}, fileExists)
	if err != nil || out["image_url"] != "./nope.png" || len(uploaded) != 0 {
		t.Fatalf("out=%v uploaded=%v err=%v", out, uploaded, err)
	}
}

func TestResolveArrayItems(t *testing.T) {
	c, s := setup(t)
	png := writeFile(t, "b.png", "B")
	in := map[string]any{"image_urls": []any{png, "https://x/y.png"}}
	out, uploaded, err := upload.Resolve(context.Background(), c, in, fileExists)
	if err != nil {
		t.Fatal(err)
	}
	arr := out["image_urls"].([]any)
	if arr[0] != "https://v3.fal.media/files/b.png" || arr[1] != "https://x/y.png" || len(uploaded) != 1 || len(s.puts) != 1 {
		t.Fatalf("out=%v uploaded=%v", out, uploaded)
	}
}

func TestResolveDataURIPassThrough(t *testing.T) {
	c, s := setup(t)
	in := map[string]any{"image_url": "data:image/png;base64,AAAA", "video_url": "http://x/v.mp4"}
	out, uploaded, err := upload.Resolve(context.Background(), c, in, func(string) bool { return true })
	if err != nil || out["image_url"] != in["image_url"] || out["video_url"] != in["video_url"] || len(uploaded) != 0 || len(s.puts) != 0 {
		t.Fatalf("out=%v uploaded=%v err=%v", out, uploaded, err)
	}
}

func TestResolveDoesNotMutateInput(t *testing.T) {
	c, _ := setup(t)
	png := writeFile(t, "c.png", "C")
	arr := []any{png}
	in := map[string]any{"image_url": png, "image_urls": arr}
	if _, _, err := upload.Resolve(context.Background(), c, in, fileExists); err != nil {
		t.Fatal(err)
	}
	if in["image_url"] != png || arr[0] != png {
		t.Fatalf("input mutated: %v", in)
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
