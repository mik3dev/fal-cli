package download_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mik3dev/fal-cli/internal/download"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/result"
)

type cdn struct {
	srv  *httptest.Server
	mu   sync.Mutex
	auth []string
}

func newCDN(t *testing.T) *cdn {
	t.Helper()
	c := &cdn{}
	c.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.auth = append(c.auth, r.Header.Get("Authorization"))
		c.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, ".jpeg"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("JPEG"))
		case strings.HasSuffix(r.URL.Path, "/noext"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("MP4"))
		case strings.HasSuffix(r.URL.Path, "/missing"):
			w.WriteHeader(404)
		default:
			_, _ = w.Write([]byte("BIN"))
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func TestDownloadNamesAndKinds(t *testing.T) {
	c := newCDN(t)
	dir := t.TempDir()
	media := []result.Media{
		{Key: "images[0]", URL: c.srv.URL + "/files/a.jpeg", ContentType: "image/jpeg"},
		{Key: "video", URL: c.srv.URL + "/noext"},          // ext from header
		{Key: "audio", URL: c.srv.URL + "/files/song.mp3"}, // ext from URL
		{Key: "x", URL: c.srv.URL + "/files/blob"},         // unknown → .bin, kind file
	}
	files, err := download.All(context.Background(), c.srv.Client(), media, dir, "fal-ai/flux/schnell", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ name, kind string }{{"fal-ai-flux-schnell_req-1_0.jpeg", "image"}, {"fal-ai-flux-schnell_req-1_1.mp4", "video"}, {"fal-ai-flux-schnell_req-1_2.mp3", "audio"}, {"fal-ai-flux-schnell_req-1_3.bin", "file"}}
	if len(files) != len(want) {
		t.Fatalf("files = %+v", files)
	}
	for i, w := range want {
		if filepath.Base(files[i].Path) != w.name || files[i].Kind != w.kind || files[i].URL != media[i].URL {
			t.Errorf("files[%d] = %+v, want %v", i, files[i], w)
		}
	}
	if b, _ := os.ReadFile(files[0].Path); string(b) != "JPEG" {
		t.Fatalf("content = %q", b)
	}
	for _, a := range c.auth {
		if a != "" {
			t.Fatal("Authorization header sent to CDN")
		}
	}
}

func TestDownloadNeverOverwrites(t *testing.T) {
	c := newCDN(t)
	dir := t.TempDir()
	media := []result.Media{{Key: "images[0]", URL: c.srv.URL + "/files/a.jpeg", ContentType: "image/jpeg"}}
	first, _ := download.All(context.Background(), c.srv.Client(), media, dir, "m", "r")
	second, err := download.All(context.Background(), c.srv.Client(), media, dir, "m", "r")
	if err != nil || filepath.Base(second[0].Path) != "m_r_0-1.jpeg" {
		t.Fatalf("second = %+v err=%v", second, err)
	}
	if b, _ := os.ReadFile(first[0].Path); string(b) != "JPEG" {
		t.Fatal("first overwritten")
	}
}

func TestDownloadRejectsHTTP(t *testing.T) {
	_, err := download.All(context.Background(), http.DefaultClient, []result.Media{{Key: "v", URL: "http://example.com/v.mp4"}}, t.TempDir(), "m", "r")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeIO || !strings.Contains(oe.Message, "https") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadHTTPErrorIsIO(t *testing.T) {
	c := newCDN(t)
	files, err := download.All(context.Background(), c.srv.Client(), []result.Media{{Key: "v", URL: c.srv.URL + "/missing"}}, t.TempDir(), "m", "r")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeIO || !strings.Contains(oe.Message, "404") || len(files) != 0 {
		t.Fatalf("files=%v err=%v", files, err)
	}
}

func TestDownloadOutDirNotWritableIsIO(t *testing.T) {
	c := newCDN(t)
	file := filepath.Join(t.TempDir(), "afile")
	_ = os.WriteFile(file, []byte("x"), 0o600)
	_, err := download.All(context.Background(), c.srv.Client(), []result.Media{{Key: "v", URL: c.srv.URL + "/files/a.jpeg"}}, filepath.Join(file, "sub"), "m", "r")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeIO {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadNetworkErrorHidesURL(t *testing.T) {
	_, err := download.All(context.Background(), http.DefaultClient, []result.Media{{Key: "v", URL: "https://127.0.0.1:1/secret?sig=TOKEN"}}, t.TempDir(), "m", "r")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeIO || strings.Contains(oe.Message, "TOKEN") {
		t.Fatalf("err = %v", err)
	}
}
