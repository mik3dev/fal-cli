package upload_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mik3dev/fal-cli/internal/schema"
	"github.com/mik3dev/fal-cli/internal/upload"
)

func TestSchemaFileKeyByName(t *testing.T) {
	for k, want := range map[string]bool{"image_url": true, "image_urls": true, "prompt": false} {
		if got := upload.SchemaFileKey(nil, k); got != want {
			t.Errorf("nil schema %q = %v", k, got)
		}
	}
}

func TestSchemaFileKeyByUIField(t *testing.T) {
	sch := map[string]any{"properties": map[string]any{
		"mask": map[string]any{"type": "string", "ui": map[string]any{"field": "image"}},
		"refs": map[string]any{"type": "array", "items": map[string]any{"type": "string", "ui": map[string]any{"field": "image"}}},
		"tail": map[string]any{"anyOf": []any{map[string]any{"type": "string", "ui": map[string]any{"field": "video"}}, map[string]any{"type": "null"}}},
		"mode": map[string]any{"type": "string", "ui": map[string]any{"field": "select"}},
	}}
	for k, want := range map[string]bool{"mask": true, "refs": true, "tail": true, "mode": false, "prompt": false} {
		if got := upload.SchemaFileKey(sch, k); got != want {
			t.Errorf("%q = %v, want %v", k, got, want)
		}
	}
}

func TestSchemaFileKeyByDescription(t *testing.T) {
	sch := map[string]any{"properties": map[string]any{
		"source": map[string]any{"type": "string", "description": "URL of the source file"},
		"prompt": map[string]any{"type": "string", "description": "Describe the url scheme"},
		"count":  map[string]any{"type": "integer", "description": "URL count"},
		"srcs":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of URLs"},
	}}
	for k, want := range map[string]bool{"source": true, "prompt": false, "count": false, "srcs": true} {
		if got := upload.SchemaFileKey(sch, k); got != want {
			t.Errorf("%q = %v, want %v", k, got, want)
		}
	}
}

// TestSchemaFileKeyCalibration runs the heuristic over every captured schema:
// no false positive on prompt-like keys, every hit explained by name or ui.field.
func TestSchemaFileKeyCalibration(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/live/schemas/*.json")
	if len(files) < 40 {
		t.Fatalf("fixtures = %d", len(files))
	}
	total := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		comps, _ := doc["components"].(map[string]any)
		schemas, _ := comps["schemas"].(map[string]any)
		for name, raw := range schemas {
			if !strings.HasSuffix(name, "Input") {
				continue
			}
			resolved, err := schema.Resolve(doc, raw)
			if err != nil {
				t.Fatal(err)
			}
			sch := resolved.(map[string]any)
			props, _ := sch["properties"].(map[string]any)
			for k := range props {
				hit := upload.SchemaFileKey(sch, k)
				switch k {
				case "prompt", "negative_prompt", "text", "seed":
					if hit {
						t.Errorf("%s %s: %q detected as file", filepath.Base(f), name, k)
					}
				}
				if hit {
					total++
					if !upload.IsFileKey(k) && !hasUI(props[k]) {
						t.Logf("%s %s: %q detected by description only", filepath.Base(f), name, k)
					}
				}
			}
		}
	}
	t.Logf("file keys detected across fixtures: %d", total)
	if total < 60 {
		t.Fatalf("too few file keys detected: %d", total)
	}
}

func hasUI(p any) bool {
	m, _ := p.(map[string]any)
	if ui, ok := m["ui"].(map[string]any); ok && ui["field"] != nil {
		return true
	}
	if items, ok := m["items"].(map[string]any); ok {
		return hasUI(items)
	}
	alts, _ := m["anyOf"].([]any)
	for _, a := range alts {
		if hasUI(a) {
			return true
		}
	}
	return false
}

func TestResolveUsesPredicate(t *testing.T) {
	c, s := setup(t)
	png := writeFile(t, "m.png", "M")
	isMask := func(k string) bool { return k == "mask" }
	out, uploaded, err := upload.Resolve(context.Background(), c, map[string]any{"mask": png}, isMask, fileExists)
	if err != nil || out["mask"] != "https://v3.fal.media/files/m.png" || len(uploaded) != 1 || len(s.puts) != 1 {
		t.Fatalf("out=%v uploaded=%v err=%v", out, uploaded, err)
	}
	out, uploaded, err = upload.Resolve(context.Background(), c, map[string]any{"mask": png}, upload.IsFileKey, fileExists)
	if err != nil || out["mask"] != png || len(uploaded) != 0 {
		t.Fatalf("name-only: out=%v uploaded=%v err=%v", out, uploaded, err)
	}
}
