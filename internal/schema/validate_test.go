package schema_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mik3dev/fal-cli/internal/schema"
)

func flux(t *testing.T) map[string]any { return fetch(t, "fal-ai/flux/schnell").Input }

func problems(t *testing.T, sch map[string]any, in map[string]any) []schema.Problem {
	t.Helper()
	ps, err := schema.Validate(sch, in)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestValidateOK(t *testing.T) {
	if ps := problems(t, flux(t), map[string]any{"prompt": "x"}); ps != nil {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestValidateRequired(t *testing.T) {
	ps := problems(t, flux(t), map[string]any{})
	if len(ps) != 1 || ps[0].Field != "prompt" || ps[0].Message != "missing required field" {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestValidateRange(t *testing.T) {
	ps := problems(t, flux(t), map[string]any{"prompt": "x", "num_images": json.Number("99")})
	if len(ps) != 1 || ps[0].Field != "num_images" || ps[0].Got != json.Number("99") || !strings.Contains(ps[0].Message, "4") {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestValidateEnumUnion(t *testing.T) {
	ps := problems(t, flux(t), map[string]any{"prompt": "x", "image_size": "bogus"})
	if len(ps) == 0 {
		t.Fatal("no problems")
	}
	found := false
	for _, p := range ps {
		if p.Field != "image_size" {
			t.Fatalf("field = %+v", p)
		}
		if strings.Contains(p.Message, "landscape_4_3") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no enum hint: %+v", ps)
	}
}

func TestValidateNullableAnyOf(t *testing.T) {
	if ps := problems(t, flux(t), map[string]any{"prompt": "x", "seed": nil}); ps != nil {
		t.Fatalf("null seed: %+v", ps)
	}
	ps := problems(t, flux(t), map[string]any{"prompt": "x", "seed": "abc"})
	if len(ps) == 0 || ps[0].Field != "seed" {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestValidateUnknownField(t *testing.T) {
	ps := problems(t, flux(t), map[string]any{"prompt": "x", "promt": "y"})
	if len(ps) != 1 || ps[0].Field != "promt" || !strings.Contains(ps[0].Message, "fal-cli models schema") || ps[0].Got != "y" {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestValidateNestedPath(t *testing.T) {
	ps := problems(t, flux(t), map[string]any{"prompt": "x", "image_size": map[string]any{"width": "wide"}})
	found := false
	for _, p := range ps {
		if p.Field == "image_size.width" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestValidateArrayPath(t *testing.T) {
	kling := fetch(t, "fal-ai/kling-video/v2.5-turbo/pro/image-to-video").Input
	ps := problems(t, kling, map[string]any{"prompt": "x", "image_url": "u", "duration": "7"})
	if len(ps) != 1 || ps[0].Field != "duration" || ps[0].Got != "7" || !strings.Contains(ps[0].Message, "5") {
		t.Fatalf("ps = %+v", ps)
	}
	sch := map[string]any{"properties": map[string]any{"image_urls": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 1}}}
	ps = problems(t, sch, map[string]any{"image_urls": []any{"a", json.Number("2")}})
	fields := make([]string, 0, len(ps))
	for _, p := range ps {
		fields = append(fields, p.Field)
	}
	if !reflect.DeepEqual(fields, []string{"image_urls", "image_urls[1]"}) {
		t.Fatalf("fields = %v (%+v)", fields, ps)
	}
}

func TestValidateSchemaCompileError(t *testing.T) {
	if _, err := schema.Validate(map[string]any{"type": "nope"}, map[string]any{}); err == nil {
		t.Fatal("expected compile error")
	}
}

func TestValidateDoesNotMutate(t *testing.T) {
	sch := map[string]any{"properties": map[string]any{"a": map[string]any{"type": "integer"}}, "required": []any{"a"}}
	in := map[string]any{"b": "x"}
	schCopy, _ := json.Marshal(sch)
	problems(t, sch, in)
	after, _ := json.Marshal(sch)
	if string(schCopy) != string(after) || len(in) != 1 || in["b"] != "x" {
		t.Fatal("mutated")
	}
}

func oneOfSchema() map[string]any {
	return map[string]any{
		"oneOf": []any{
			map[string]any{"title": "ByURL", "type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}}, "required": []any{"url"}, "additionalProperties": false},
			map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}, "n": map[string]any{"type": "integer"}}, "required": []any{"text"}, "additionalProperties": false},
		},
	}
}

func TestValidateOneOfVariantsNamed(t *testing.T) {
	ps := problems(t, oneOfSchema(), map[string]any{"n": "x"})
	if len(ps) < 2 {
		t.Fatalf("ps = %+v", ps)
	}
	var byURL, second bool
	for _, p := range ps {
		if strings.HasPrefix(p.Message, `variant "ByURL": `) {
			byURL = true
		}
		if strings.HasPrefix(p.Message, "variant 2: ") {
			second = true
		}
	}
	if !byURL || !second {
		t.Fatalf("variant prefixes missing: %+v", ps)
	}
	if ps := problems(t, oneOfSchema(), map[string]any{"text": "hi", "n": json.Number("1")}); ps != nil {
		t.Fatalf("valid branch: %+v", ps)
	}
}

func TestValidateOneOfMatchesSeveral(t *testing.T) {
	sch := map[string]any{"oneOf": []any{
		map[string]any{"title": "A", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
		map[string]any{"title": "B", "properties": map[string]any{"a": map[string]any{"type": "string"}}},
	}}
	ps := problems(t, sch, map[string]any{"a": "x"})
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "more than one variant") || !strings.Contains(ps[0].Message, `"A"`) {
		t.Fatalf("ps = %+v", ps)
	}
}

func TestValidateUnknownKeyInsideOneOfReportedOnce(t *testing.T) {
	ps := problems(t, oneOfSchema(), map[string]any{"url": "u", "bogus": 1})
	count := 0
	for _, p := range ps {
		if p.Field == "bogus" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("bogus reported %d times: %+v", count, ps)
	}
}

func TestValidateOpenSchemaAllowsUnknown(t *testing.T) {
	sch := map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}, "additionalProperties": true}
	if ps := problems(t, sch, map[string]any{"zz": 1}); ps != nil {
		t.Fatalf("ps = %+v", ps)
	}
	if ps := problems(t, map[string]any{"type": "object"}, map[string]any{"zz": 1}); ps != nil {
		t.Fatalf("no properties: %+v", ps)
	}
}
