package preset_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mik3dev/fal-cli/internal/preset"
)

func TestParseNormalizesNumbersAndSource(t *testing.T) {
	got, err := preset.Parse([]byte("presets:\n- name: a\n  description: d\n  model: m/x\n  params: {n: 5, f: 1.5, s: \"x\", b: true}\n- name: b\n  model: m/y\n"), preset.SourceFactory)
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	p := got[0]
	if p.Params["n"] != json.Number("5") || p.Params["f"] != json.Number("1.5") || p.Params["s"] != "x" || p.Params["b"] != true || p.Source != "factory" {
		t.Fatalf("bad preset: %#v", p)
	}
	if got[1].Params != nil {
		t.Fatalf("empty params must be nil: %#v", got[1].Params)
	}
}

func TestParseRejectsBadNames(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"bad name", "presets:\n- name: Bad Name\n  model: m\n", "invalid name"},
		{"empty name", "presets:\n- name: \"\"\n  model: m\n", "invalid name"},
		{"duplicate", "presets:\n- name: a\n  model: m\n- name: a\n  model: n\n", "duplicate"},
		{"missing model", "presets:\n- name: a\n", "model is required"},
		{"unknown key", "presets:\n- name: a\n  model: m\n  modle: x\n", "modle"},
		{"null entry", "presets:\n- null\n", "preset #1: empty entry"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := preset.Parse([]byte(c.doc), preset.SourceUser)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v, want containing %q", err, c.want)
			}
		})
	}
}

const factory = "presets:\n- name: a\n  model: m1\n  params: {x: 1}\n- name: b\n  model: m2\n  params: {y: 2}\n"

func TestLoadUserOverridesByName(t *testing.T) {
	user := filepath.Join(t.TempDir(), "presets.yaml")
	if err := os.WriteFile(user, []byte("presets:\n- name: b\n  model: m3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := preset.Load([]byte(factory), user)
	if err != nil || len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if b := got[1]; b.Model != "m3" || b.Params != nil || b.Source != preset.SourceUser {
		t.Fatalf("b = %#v", b)
	}
	if got[0].Source != preset.SourceFactory || got[0].Params["x"] != json.Number("1") {
		t.Fatalf("a = %#v", got[0])
	}
	only, err := preset.Load([]byte(factory), filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || len(only) != 2 || only[1].Model != "m2" {
		t.Fatalf("missing user file: got=%v err=%v", only, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(bad, []byte("presets: ["), 0o600)
	if _, err := preset.Load([]byte(factory), bad); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("bad user file: err=%v", err)
	}
	if _, ok := preset.Find(got, "b"); !ok {
		t.Fatal("Find b")
	}
	if _, ok := preset.Find(got, "zzz"); ok {
		t.Fatal("Find zzz")
	}
}

var wantFactory = []struct{ name, model string }{
	{"imagen-calidad", "fal-ai/nano-banana-pro"},
	{"imagen-edicion", "fal-ai/nano-banana-2/edit"},
	{"imagen-rapida", "fal-ai/flux/schnell"},
	{"instrumental", "fal-ai/minimax-music/v2.6"},
	{"musica", "fal-ai/minimax-music/v2.6"},
	{"video-barato", "minimax/h3-max-turbo/text-to-video"},
	{"video-calidad", "fal-ai/kling-video/v2.5-turbo/pro/text-to-video"},
	{"video-imagen", "fal-ai/kling-video/v2.5-turbo/pro/image-to-video"},
	{"video-inicio-fin", "fal-ai/kling-video/v2.5-turbo/pro/image-to-video"},
	{"voz", "fal-ai/elevenlabs/tts/multilingual-v2"},
}

func TestFactoryPresetsLoad(t *testing.T) {
	raw, err := os.ReadFile("../../presets/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := preset.Load(raw, filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil || len(got) != len(wantFactory) {
		t.Fatalf("got %d presets err=%v", len(got), err)
	}
	for i, w := range wantFactory {
		p := got[i]
		if p.Name != w.name || p.Model != w.model || p.Description == "" || !strings.Contains(p.Description, "Missing fields") {
			t.Errorf("#%d = %+v, want %+v", i, p, w)
		}
		fixture := "../../testdata/live/schemas/" + strings.ReplaceAll(p.Model, "/", "__") + ".json"
		if _, err := os.Stat(fixture); err != nil {
			t.Errorf("%s: no schema fixture for %s", p.Name, p.Model)
		}
	}
}
