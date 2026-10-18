package config_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mik3dev/fal-cli/internal/config"
	"github.com/mik3dev/fal-cli/internal/output"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolvePathsHonorsFAL_CLI_HOME(t *testing.T) {
	p, err := config.ResolvePaths(envOf(map[string]string{"FAL_CLI_HOME": "/x/fal", "HOME": "/h"}))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Paths{
		Home: "/x/fal", Config: "/x/fal/config.json", Presets: "/x/fal/presets.yaml",
		Cache: "/x/fal/cache", Ledger: "/x/fal/tasks.jsonl",
	}
	if p != want {
		t.Errorf("got %+v want %+v", p, want)
	}
}

func TestResolvePathsDefaultsToHomeDir(t *testing.T) {
	p, err := config.ResolvePaths(envOf(map[string]string{"HOME": "/h"}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Home != "/h/.fal-cli" {
		t.Errorf("home = %q", p.Home)
	}
}

func TestResolvePathsNoHomeFails(t *testing.T) {
	if _, err := config.ResolvePaths(envOf(nil)); err == nil {
		t.Fatal("expected error")
	}
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	p, err := config.ResolvePaths(envOf(map[string]string{"FAL_CLI_HOME": filepath.Join(t.TempDir(), "home")}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSaveWrites0600AndHome0700(t *testing.T) {
	p := testPaths(t)
	if err := config.Save(p, config.Config{APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config perm = %o", fi.Mode().Perm())
	}
	hi, err := os.Stat(p.Home)
	if err != nil {
		t.Fatal(err)
	}
	if hi.Mode().Perm() != 0o700 {
		t.Errorf("home perm = %o", hi.Mode().Perm())
	}
	entries, _ := os.ReadDir(p.Home)
	if len(entries) != 1 {
		t.Errorf("leftover temp files: %v", entries)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	var w bytes.Buffer
	c, err := config.Load(testPaths(t), &w)
	if err != nil || c != (config.Config{}) || w.Len() != 0 {
		t.Errorf("c=%+v err=%v warn=%q", c, err, w.String())
	}
}

func TestLoadRoundTrip(t *testing.T) {
	p := testPaths(t)
	in := config.Config{APIKey: "k", OutDir: "/o"}
	if err := config.Save(p, in); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	out, err := config.Load(p, &w)
	if err != nil || out != in || w.Len() != 0 {
		t.Errorf("out=%+v err=%v warn=%q", out, err, w.String())
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	p := testPaths(t)
	if err := config.EnsureHome(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Config, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadWarnsOnLoosePerms(t *testing.T) {
	p := testPaths(t)
	if err := config.Save(p, config.Config{APIKey: "secretkey"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Config, 0o644); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	c, err := config.Load(p, &w)
	if err != nil || c.APIKey != "secretkey" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
	if !strings.Contains(w.String(), "should be 0600") || !strings.Contains(w.String(), "0644") {
		t.Errorf("warn = %q", w.String())
	}
}

func TestResolveKeyEnvBeatsConfig(t *testing.T) {
	k, src, err := config.ResolveKey(envOf(map[string]string{"FAL_API_KEY": "envkey"}), config.Config{APIKey: "cfgkey"})
	if err != nil || k != "envkey" || src != "env" {
		t.Errorf("k=%q src=%q err=%v", k, src, err)
	}
}

func TestResolveKeyEmptyEnvFallsBackToConfig(t *testing.T) {
	k, src, err := config.ResolveKey(envOf(map[string]string{"FAL_API_KEY": ""}), config.Config{APIKey: "cfgkey"})
	if err != nil || k != "cfgkey" || src != "config" {
		t.Errorf("k=%q src=%q err=%v", k, src, err)
	}
}

func TestResolveKeyMissing(t *testing.T) {
	_, _, err := config.ResolveKey(envOf(nil), config.Config{})
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != "auth" {
		t.Fatalf("err = %v", err)
	}
	if oe.Message != "no API key: set FAL_API_KEY or run 'fal-cli auth login'" {
		t.Errorf("msg = %q", oe.Message)
	}
}

func TestRedactShortKey(t *testing.T) {
	if got := config.Redact("short"); got != "****" {
		t.Errorf("got %q", got)
	}
	if got := config.Redact("elevenchars"); got != "****" {
		t.Errorf("11 chars: got %q", got)
	}
}

func TestRedactLongKeyShowsLast4(t *testing.T) {
	if got := config.Redact("abc123456789xyz"); got != "…9xyz" {
		t.Errorf("got %q", got)
	}
}

func TestSaveFailsWhenHomeIsAFile(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(filepath.Dir(p.Home), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Home, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(p, config.Config{APIKey: "k"}); err == nil {
		t.Fatal("expected error")
	}
	if err := config.EnsureHome(p); err == nil {
		t.Fatal("expected EnsureHome error")
	}
}

func TestLoadFailsWhenConfigIsADirectory(t *testing.T) {
	p := testPaths(t)
	if err := os.MkdirAll(p.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveKeyTrimsWhitespace(t *testing.T) {
	k, src, err := config.ResolveKey(envOf(map[string]string{"FAL_API_KEY": "key\n"}), config.Config{})
	if err != nil || k != "key" || src != "env" {
		t.Errorf("env: k=%q src=%q err=%v", k, src, err)
	}
	k, src, err = config.ResolveKey(envOf(map[string]string{"FAL_API_KEY": " \t\n"}), config.Config{APIKey: " cfg \n"})
	if err != nil || k != "cfg" || src != "config" {
		t.Errorf("blank env must fall back: k=%q src=%q err=%v", k, src, err)
	}
	if _, _, err := config.ResolveKey(envOf(map[string]string{"FAL_API_KEY": "  "}), config.Config{APIKey: "\n"}); err == nil {
		t.Error("whitespace-only keys must count as unset")
	}
}

func TestUserHome(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"home", map[string]string{"HOME": "/h"}, "/h"},
		{"userprofile", map[string]string{"USERPROFILE": `C:\u`}, `C:\u`},
		{"both prefers HOME", map[string]string{"HOME": "/h", "USERPROFILE": `C:\u`}, "/h"},
		{"none", map[string]string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := config.UserHome(envOf(c.env)); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestResolvePathsUserProfile(t *testing.T) {
	dir := t.TempDir()
	p, err := config.ResolvePaths(envOf(map[string]string{"FAL_CLI_HOME": "", "HOME": "", "USERPROFILE": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".fal-cli"); p.Home != want {
		t.Errorf("home = %q want %q", p.Home, want)
	}
}

func TestLoadNoPermWarningOnWindows(t *testing.T) {
	p := config.Paths{Config: filepath.Join(t.TempDir(), "config.json")}
	if err := os.WriteFile(p.Config, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Config, 0o644); err != nil {
		t.Fatal(err)
	}
	restore := config.SetGOOS("windows")
	var w bytes.Buffer
	_, err := config.Load(p, &w)
	restore()
	if err != nil || w.Len() != 0 {
		t.Fatalf("windows: err=%v warn=%q", err, w.String())
	}
	restore = config.SetGOOS("linux")
	defer restore()
	w.Reset()
	if _, err := config.Load(p, &w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "should be 0600") {
		t.Errorf("linux: warn=%q", w.String())
	}
}

func TestResolveKeyOrder(t *testing.T) {
	k, src, err := config.ResolveKey(envOf(map[string]string{"FAL_API_KEY": "a", "FAL_KEY": "b"}), config.Config{APIKey: "c"})
	if err != nil || k != "a" || src != "env" {
		t.Errorf("FAL_API_KEY first: k=%q src=%q err=%v", k, src, err)
	}
	k, src, err = config.ResolveKey(envOf(map[string]string{"FAL_KEY": "b"}), config.Config{APIKey: "c"})
	if err != nil || k != "b" || src != "env" {
		t.Errorf("FAL_KEY second: k=%q src=%q err=%v", k, src, err)
	}
}
