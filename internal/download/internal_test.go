package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeToken(t *testing.T) {
	for in, want := range map[string]string{"fal-ai/flux/schnell": "fal-ai-flux-schnell", "../../etc": "----etc", "": "x", ".": "x", "a..b": "a-b"} {
		if got := safeToken(in); got != want {
			t.Errorf("safeToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSaveFallsBackToCopyWithoutHardLinks(t *testing.T) {
	orig := linkFile
	linkFile = func(string, string) error { return errors.New("EPERM: no links") }
	t.Cleanup(func() { linkFile = orig })
	dir := t.TempDir()
	first, err := save(context.Background(), strings.NewReader("one"), dir, "b", ".txt")
	if err != nil || filepath.Base(first) != "b.txt" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := save(context.Background(), strings.NewReader("two"), dir, "b", ".txt")
	if err != nil || filepath.Base(second) != "b-1.txt" {
		t.Fatalf("second=%q err=%v", second, err)
	}
	if b, _ := os.ReadFile(first); string(b) != "one" {
		t.Fatal("first overwritten")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".fal-*.part")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestSaveCancelledContextDropsFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	if _, err := save(ctx, strings.NewReader("x"), dir, "c", ".bin"); err == nil {
		t.Fatal("expected error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("dir not clean: %v", entries)
	}
}

func TestExtAndKindFallbacks(t *testing.T) {
	cases := []struct{ ct, header, url, ext, kind string }{
		{"image/png", "text/plain", "https://x/a", ".png", "image"},
		{"", "audio/mpeg", "https://x/a", ".mp3", "audio"},
		{"", "application/octet-stream", "https://x/a.webm", ".webm", "video"},
		{"", "", "https://x/a", ".bin", "file"},
		{"application/pdf", "", "https://x/a", ".pdf", "file"},
	}
	for _, c := range cases {
		ext, kind := extAndKind(mediaOf(c.url, c.ct), c.header)
		if ext != c.ext || kind != c.kind {
			t.Errorf("%+v → %s %s", c, ext, kind)
		}
	}
}

func TestDownloadClientRefusesNonHTTPSRedirect(t *testing.T) {
	c := downloadClient(nil)
	if c.Timeout != 0 || c.CheckRedirect == nil {
		t.Fatal("client not derived")
	}
	u, _ := parse("http://x/y")
	if err := c.CheckRedirect(u, nil); err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Fatalf("err = %v", err)
	}
	s, _ := parse("https://x/y")
	if err := c.CheckRedirect(s, make([]*reqT, 10)); err == nil {
		t.Fatal("expected redirect cap")
	}
}
