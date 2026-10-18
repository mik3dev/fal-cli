package schema_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mik3dev/fal-cli/internal/schema"
)

func TestCoerceTypedByFluxSchema(t *testing.T) {
	sch := fetch(t, "fal-ai/flux/schnell").Input
	in := map[string]any{"prompt": "x", "num_inference_steps": "4", "seed": "3", "guidance_scale": "3.5", "enable_safety_checker": "false", "sync_mode": "TRUE"}
	out := schema.Coerce(sch, in)
	want := map[string]any{"prompt": "x", "num_inference_steps": json.Number("4"), "seed": json.Number("3"), "guidance_scale": json.Number("3.5"), "enable_safety_checker": false, "sync_mode": true}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("out = %#v", out)
	}
}

func TestCoerceUnionObjectOrEnum(t *testing.T) {
	sch := fetch(t, "fal-ai/flux/schnell").Input
	out := schema.Coerce(sch, map[string]any{"image_size": "landscape_4_3"})
	if out["image_size"] != "landscape_4_3" {
		t.Fatalf("enum = %#v", out["image_size"])
	}
	out = schema.Coerce(sch, map[string]any{"image_size": `{"width":512,"height":"768"}`})
	m, ok := out["image_size"].(map[string]any)
	if !ok || m["height"] != json.Number("768") || m["width"] != json.Number("512") {
		t.Fatalf("object = %#v", out["image_size"])
	}
}

func TestCoerceArrayFromRepeatAndJSON(t *testing.T) {
	sch := map[string]any{"properties": map[string]any{
		"image_urls": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"nums":       map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
	}}
	out := schema.Coerce(sch, map[string]any{"nums": []any{"1", "2"}, "image_urls": "a"})
	if !reflect.DeepEqual(out["nums"], []any{json.Number("1"), json.Number("2")}) || !reflect.DeepEqual(out["image_urls"], []any{"a"}) {
		t.Fatalf("out = %#v", out)
	}
	out = schema.Coerce(sch, map[string]any{"nums": "[3,4]"})
	if !reflect.DeepEqual(out["nums"], []any{json.Number("3"), json.Number("4")}) {
		t.Fatalf("json array = %#v", out["nums"])
	}
	if out = schema.Coerce(sch, map[string]any{"nums": "7"}); !reflect.DeepEqual(out["nums"], []any{json.Number("7")}) {
		t.Fatalf("wrapped = %#v", out["nums"])
	}
}

func TestCoerceLeavesUnconvertible(t *testing.T) {
	sch := fetch(t, "fal-ai/flux/schnell").Input
	in := map[string]any{"num_images": "many", "seed": nil, "unknown_key": "5", "enable_safety_checker": "yes"}
	out := schema.Coerce(sch, in)
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("out = %#v", out)
	}
}

func TestCoerceDoesNotMutate(t *testing.T) {
	sch := map[string]any{"properties": map[string]any{"n": map[string]any{"type": "integer"}, "o": map[string]any{"type": "object", "properties": map[string]any{"k": map[string]any{"type": "number"}}}}}
	inner := map[string]any{"k": "1.5"}
	in := map[string]any{"n": "1", "o": inner}
	out := schema.Coerce(sch, in)
	if in["n"] != "1" || inner["k"] != "1.5" || out["n"] != json.Number("1") || out["o"].(map[string]any)["k"] != json.Number("1.5") {
		t.Fatalf("in=%#v out=%#v", in, out)
	}
	if schema.Coerce(nil, map[string]any{"a": "1"})["a"] != "1" {
		t.Fatal("nil schema must be a no-op")
	}
}

func TestPropSchemaFindsInBranches(t *testing.T) {
	sch := map[string]any{"anyOf": []any{map[string]any{"properties": map[string]any{"a": map[string]any{"type": "integer"}}}}}
	if p, ok := schema.PropSchema(sch, "a"); !ok || p["type"] != "integer" {
		t.Fatalf("a: %v %v", p, ok)
	}
	if _, ok := schema.PropSchema(sch, "zz"); ok {
		t.Fatal("zz found")
	}
	if _, ok := schema.PropSchema(nil, "a"); ok {
		t.Fatal("nil schema found")
	}
}
