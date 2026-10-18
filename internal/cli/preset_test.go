package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresetListCommand(t *testing.T) {
	code, m, _ := run(t, map[string]string{"FAL_CLI_HOME": t.TempDir()}, "preset", "list")
	ps, _ := m["presets"].([]any)
	if code != 0 || len(ps) != 10 {
		t.Fatalf("code=%d m=%v", code, m)
	}
	first := ps[0].(map[string]any)
	if first["name"] != "imagen-calidad" || first["source"] != "factory" || first["model"] == nil || first["description"] == nil {
		t.Fatalf("first = %v", first)
	}
	if _, has := first["params"]; has {
		t.Fatalf("list must not carry params: %v", first)
	}
}

func TestPresetShowAcceptsAt(t *testing.T) {
	env := map[string]string{"FAL_CLI_HOME": t.TempDir()}
	code, m, _ := run(t, env, "preset", "show", "@voz")
	p, _ := m["preset"].(map[string]any)
	params, _ := p["params"].(map[string]any)
	if code != 0 || p["model"] != "fal-ai/elevenlabs/tts/multilingual-v2" || params["voice"] != "Rachel" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	code, m, _ = run(t, env, "preset", "show", "nope")
	if code != 2 || m["error"].(map[string]any)["code"] != "usage" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestPresetUserOverride(t *testing.T) {
	f := newFal(t)
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "presets.yaml"), []byte("presets:\n- name: imagen-rapida\n  description: mine\n  model: fal-ai/flux/dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, m, _, _ := runFal(t, f, home, "preset", "list")
	ps, _ := m["presets"].([]any)
	var mine map[string]any
	for _, p := range ps {
		if pm := p.(map[string]any); pm["name"] == "imagen-rapida" {
			mine = pm
		}
	}
	if code != 0 || len(ps) != 10 || mine["source"] != "user" || mine["model"] != "fal-ai/flux/dev" {
		t.Fatalf("code=%d mine=%v", code, mine)
	}
	code, m, _, _ = runFal(t, f, home, "run", "@imagen-rapida", "-p", "prompt=a red fox in the snow", "--dry-run")
	if code != 0 || m["model"] != "fal-ai/flux/dev" || m["preset"] != "imagen-rapida" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunPresetMergesParamsAndTagsOutput(t *testing.T) {
	f := newFal(t)
	home := filepath.Join(t.TempDir(), "h")
	code, m, _, _ := runFal(t, f, home, "run", "@imagen-rapida", "-p", "prompt=fox", "--out", t.TempDir())
	if code != 0 || m["preset"] != "imagen-rapida" || m["model"] != "fal-ai/flux/schnell" || m["state"] != "success" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	body := f.submits[0]
	if body["image_size"] != "square_hd" || body["output_format"] != "jpeg" || body["prompt"] != "fox" {
		t.Fatalf("body = %v", body)
	}
	code, m, _, _ = runFal(t, f, home, "run", "@imagen-rapida", "-p", "prompt=fox", "--no-wait")
	if code != 0 || m["preset"] != "imagen-rapida" || m["state"] != "submitted" {
		t.Fatalf("no-wait: code=%d m=%v", code, m)
	}
}

func TestRunPresetParamOverride(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "@video-calidad", "-p", "prompt=a red fox in the snow", "-p", "duration=10", "--dry-run")
	in, _ := m["input"].(map[string]any)
	if code != 0 || in["duration"] != "10" || in["aspect_ratio"] != "16:9" || m["preset"] != "video-calidad" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunPresetMissingRequiredIsValidation(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "@voz", "--dry-run")
	e, _ := m["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	probs, _ := d["problems"].([]any)
	if code != 2 || e["code"] != "validation" || len(probs) != 1 || probs[0].(map[string]any)["field"] != "text" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestRunUnknownPresetIsUsage(t *testing.T) {
	f := newFal(t)
	code, m, _, _ := runFal(t, f, filepath.Join(t.TempDir(), "h"), "run", "@nope", "-p", "prompt=a red fox in the snow")
	e, _ := m["error"].(map[string]any)
	if code != 2 || e["code"] != "usage" || !strings.Contains(e["message"].(string), "preset list") {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

// presetFill supplies the "Missing fields" of each factory preset.
var presetFill = map[string][]string{
	"imagen-rapida":    {"prompt=a red fox in the snow"},
	"imagen-calidad":   {"prompt=a red fox in the snow"},
	"imagen-edicion":   {"prompt=a red fox in the snow", "image_urls=https://example.com/a.png"},
	"video-calidad":    {"prompt=a red fox in the snow"},
	"video-imagen":     {"prompt=a red fox in the snow", "image_url=https://example.com/a.png"},
	"video-inicio-fin": {"prompt=a red fox in the snow", "image_url=https://example.com/a.png", "tail_image_url=https://example.com/b.png"},
	"video-barato":     {"prompt=a red fox in the snow"},
	"musica":           {"prompt=a red fox in the snow"},
	"instrumental":     {"prompt=a red fox in the snow"},
	"voz":              {"text=hello from fal"},
}

func TestFactoryPresetsDryRunClean(t *testing.T) {
	f := newFal(t)
	home := filepath.Join(t.TempDir(), "h")
	_, list, _, _ := runFal(t, f, home, "preset", "list")
	ps, _ := list["presets"].([]any)
	if len(ps) != len(presetFill) {
		t.Fatalf("presets = %v", ps)
	}
	for _, p := range ps {
		pm := p.(map[string]any)
		name := pm["name"].(string)
		args := []string{"run", "@" + name, "--dry-run"}
		for _, kv := range presetFill[name] {
			args = append(args, "-p", kv)
		}
		code, m, _, _ := runFal(t, f, home, args...)
		if code != 0 || m["preset"] != name || m["model"] != pm["model"] || m["price"] == nil {
			t.Errorf("%s: code=%d m=%v", name, code, m)
		}
		if _, has := m["problems"]; has {
			t.Errorf("%s: problems %v", name, m["problems"])
		}
	}
}
