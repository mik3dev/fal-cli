package schema_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/schema"
)

func schemaServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/openapi/queue/openapi.json" {
			w.WriteHeader(404)
			return
		}
		id := strings.ReplaceAll(r.URL.Query().Get("endpoint_id"), "/", "__")
		b, err := os.ReadFile("../../testdata/live/schemas/" + id + ".json")
		if err != nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte("null"))
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(url string) *api.Client {
	c := api.New("k", func(string) string { return "" })
	c.SchemaURL = url
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func fetch(t *testing.T, endpoint string) schema.Doc {
	t.Helper()
	var hits atomic.Int32
	c := newClient(schemaServer(t, &hits).URL)
	d, err := schema.Fetch(context.Background(), c, t.TempDir(), endpoint, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func field(s schema.Summary, name string) schema.Field {
	for _, f := range s.Fields {
		if f.Name == name {
			return f
		}
	}
	return schema.Field{}
}

func TestFetchExtractsInputAndOutput(t *testing.T) {
	d := fetch(t, "fal-ai/flux/schnell")
	if d.Endpoint != "fal-ai/flux/schnell" || d.Category != "text-to-image" {
		t.Fatalf("doc = %+v", d)
	}
	props, _ := d.Input["properties"].(map[string]any)
	if _, ok := props["prompt"]; !ok {
		t.Fatalf("input props = %v", props)
	}
	// $ref resolved: image_size.anyOf[0] is the ImageSize object, not a $ref.
	is, _ := props["image_size"].(map[string]any)
	anyOf, _ := is["anyOf"].([]any)
	first, _ := anyOf[0].(map[string]any)
	if _, isRef := first["$ref"]; isRef || first["properties"] == nil {
		t.Fatalf("image_size not resolved: %v", first)
	}
	oprops, _ := d.Output["properties"].(map[string]any)
	if _, ok := oprops["images"]; !ok {
		t.Fatalf("output props = %v", oprops)
	}
	if len(d.Raw) == 0 || !json.Valid(d.Raw) {
		t.Fatal("raw missing")
	}
}

func TestFetch404IsUnknownModel(t *testing.T) {
	var hits atomic.Int32
	c := newClient(schemaServer(t, &hits).URL)
	_, err := schema.Fetch(context.Background(), c, t.TempDir(), "fal-ai/nope", false, time.Now())
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeValidation || oe.Message != "unknown model: fal-ai/nope" {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchCaches24h(t *testing.T) {
	var hits atomic.Int32
	c := newClient(schemaServer(t, &hits).URL)
	dir := t.TempDir()
	now := time.Now()
	for i := 0; i < 2; i++ {
		if _, err := schema.Fetch(context.Background(), c, dir, "fal-ai/flux/schnell", false, now); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}
	if _, err := schema.Fetch(context.Background(), c, dir, "fal-ai/flux/schnell", false, now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := schema.Fetch(context.Background(), c, dir, "fal-ai/flux/schnell", true, now); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d", hits.Load())
	}
}

func TestSummaryRequiredAndOrder(t *testing.T) {
	s, err := schema.Summarize(fetch(t, "fal-ai/flux/schnell"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Required) != 1 || s.Required[0] != "prompt" {
		t.Fatalf("required = %v", s.Required)
	}
	names := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	want := "num_inference_steps,prompt,image_size,seed,guidance_scale,sync_mode,num_images,enable_safety_checker,output_format,acceleration"
	if strings.Join(names, ",") != want {
		t.Fatalf("order = %v", names)
	}
	if !field(s, "prompt").Required || field(s, "seed").Required {
		t.Errorf("required flags wrong: %+v", s.Fields)
	}
}

func TestSummaryNullableAnyOf(t *testing.T) {
	s, _ := schema.Summarize(fetch(t, "fal-ai/flux/schnell"))
	seed := field(s, "seed")
	if seed.Type != "integer" || seed.Required {
		t.Fatalf("seed = %+v", seed)
	}
	tail := field(mustSum(t, fetch(t, "fal-ai/kling-video/v2.5-turbo/pro/image-to-video")), "tail_image_url")
	if tail.Type != "string" {
		t.Fatalf("tail_image_url = %+v", tail)
	}
}

func mustSum(t *testing.T, d schema.Doc) schema.Summary {
	t.Helper()
	s, err := schema.Summarize(d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSummaryUnionObjectOrEnum(t *testing.T) {
	s := mustSum(t, fetch(t, "fal-ai/flux/schnell"))
	is := field(s, "image_size")
	if is.Type != "object|string" || len(is.Enum) != 6 || is.Default != "landscape_4_3" {
		t.Fatalf("image_size = %+v", is)
	}
	ni := field(s, "num_images")
	if ni.Type != "integer" || ni.Min == nil || *ni.Min != 1 || ni.Max == nil || *ni.Max != 4 {
		t.Fatalf("num_images = %+v", ni)
	}
	dur := field(mustSum(t, fetch(t, "fal-ai/kling-video/v2.5-turbo/pro/image-to-video")), "duration")
	if dur.Type != "string" || len(dur.Enum) != 2 || dur.Default != "5" {
		t.Fatalf("duration = %+v", dur)
	}
}

func TestSummaryOutputFields(t *testing.T) {
	s := mustSum(t, fetch(t, "fal-ai/kling-video/v2.5-turbo/pro/image-to-video"))
	if len(s.Output) == 0 || s.Output[0].Name != "video" || s.Output[0].Type != "object" {
		t.Fatalf("output = %+v", s.Output)
	}
	s2 := mustSum(t, fetch(t, "fal-ai/flux/schnell"))
	var images schema.Field
	for _, f := range s2.Output {
		if f.Name == "images" {
			images = f
		}
	}
	if images.Type != "array" || images.Items != "object" {
		t.Fatalf("images = %+v", images)
	}
}

func TestResolveSiblingKeysAndCycles(t *testing.T) {
	doc := map[string]any{"components": map[string]any{"schemas": map[string]any{
		"A": map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"$ref": "#/components/schemas/B"}}},
		"B": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"$ref": "#/components/schemas/A"}}},
	}}}
	r, err := schema.Resolve(doc, map[string]any{"$ref": "#/components/schemas/A", "description": "local"})
	if err != nil {
		t.Fatal(err)
	}
	m := r.(map[string]any)
	if m["description"] != "local" || m["type"] != "object" {
		t.Fatalf("r = %v", m)
	}
	if _, err := schema.Resolve(doc, map[string]any{"$ref": "#/components/schemas/Nope"}); err == nil {
		t.Fatal("expected unresolvable error")
	}
}

func TestGuessLiterals(t *testing.T) {
	in := map[string]any{"n": "3", "f": "1.5", "b": "true", "s": "hello", "j": `{"a":1}`, "arr": []any{"1", "x"}, "pad": " 3 "}
	out := schema.GuessLiterals(in)
	if out["n"] != json.Number("3") || out["f"] != json.Number("1.5") || out["b"] != true || out["s"] != "hello" || out["pad"] != " 3 " {
		t.Fatalf("out = %#v", out)
	}
	if _, ok := out["j"].(map[string]any); !ok {
		t.Fatalf("j = %#v", out["j"])
	}
	if arr := out["arr"].([]any); arr[0] != json.Number("1") || arr[1] != "x" {
		t.Fatalf("arr = %#v", arr)
	}
	if in["n"] != "3" {
		t.Fatal("input mutated")
	}
}

func TestResolveRejectsExponentialExpansion(t *testing.T) {
	// "billion laughs": each level references the previous one 10 times.
	schemas := map[string]any{"l0": map[string]any{"type": "string"}}
	for i := 1; i <= 9; i++ {
		props := map[string]any{}
		for j := 0; j < 10; j++ {
			props[fmt.Sprintf("p%d", j)] = map[string]any{"$ref": fmt.Sprintf("#/components/schemas/l%d", i-1)}
		}
		schemas[fmt.Sprintf("l%d", i)] = map[string]any{"type": "object", "properties": props}
	}
	doc := map[string]any{"components": map[string]any{"schemas": schemas}}
	_, err := schema.Resolve(doc, map[string]any{"$ref": "#/components/schemas/l9"})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v", err)
	}
}
