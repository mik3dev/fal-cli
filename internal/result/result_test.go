package result_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mik3dev/fal-cli/internal/result"
	"github.com/mik3dev/fal-cli/internal/schema"
)

const fluxOut = `{"images":[{"url":"https://v3.fal.media/files/a.jpeg","width":1024,"height":768,"content_type":"image/jpeg"},{"url":"https://v3.fal.media/files/b.jpeg","content_type":"image/jpeg"}],"timings":{"inference":0.3},"seed":42,"has_nsfw_concepts":[false,false],"prompt":"cat"}`
const klingOut = `{"video":{"url":"https://v3.fal.media/files/v.mp4","content_type":"video/mp4","file_name":"v.mp4","file_size":1234}}`
const elevenOut = `{"audio":{"url":"https://v3.fal.media/files/s.mp3","content_type":"audio/mpeg","file_name":"s.mp3"},"timestamps":[{"text":"hi","timestamp":[0,0.5]}]}`

func TestExtractImagesArray(t *testing.T) {
	m := result.Extract(json.RawMessage(fluxOut))
	if len(m) != 2 || m[0].Key != "images[0]" || m[0].URL != "https://v3.fal.media/files/a.jpeg" || m[0].ContentType != "image/jpeg" || m[1].Key != "images[1]" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractSingleVideoFile(t *testing.T) {
	m := result.Extract(json.RawMessage(klingOut))
	if len(m) != 1 || m[0].Key != "video" || m[0].FileName != "v.mp4" || m[0].ContentType != "video/mp4" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractAudioAndTimestampsIgnored(t *testing.T) {
	m := result.Extract(json.RawMessage(elevenOut))
	if len(m) != 1 || m[0].Key != "audio" || m[0].URL != "https://v3.fal.media/files/s.mp3" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractBareFalURLsUnderURLKeys(t *testing.T) {
	out := `{"video_url":"https://fal.media/x/v.mp4","thumb_urls":["https://v3.fal.run/t1.png","https://example.com/no.png"],"homepage_url":"https://example.com","note":"https://fal.media/ignored.png"}`
	m := result.Extract(json.RawMessage(out))
	if len(m) != 2 || m[0].Key != "video_url" || m[1].Key != "thumb_urls[0]" {
		t.Fatalf("m = %+v", m)
	}
}

func TestExtractDedupes(t *testing.T) {
	out := `{"image":{"url":"https://v3.fal.media/files/a.png","content_type":"image/png"},"image_url":"https://v3.fal.media/files/a.png","garbage":"x","n":null}`
	m := result.Extract(json.RawMessage(out))
	if len(m) != 1 || m[0].Key != "image" {
		t.Fatalf("m = %+v", m)
	}
	if got := result.Extract(json.RawMessage(`not json`)); len(got) != 0 {
		t.Fatalf("invalid json: %+v", got)
	}
}

func TestIDsHaveRequestAndSeed(t *testing.T) {
	raw := json.RawMessage(fluxOut)
	ids := result.IDs("req-1", raw, result.Extract(raw))
	if ids["request_id"] != "req-1" || ids["seed"] != float64(42) {
		t.Fatalf("ids = %v", ids)
	}
	urls, _ := ids["images_urls"].([]string)
	if len(urls) != 2 || ids["images_url"] != nil {
		t.Fatalf("ids = %v", ids)
	}
	one := result.IDs("req-2", json.RawMessage(klingOut), result.Extract(json.RawMessage(klingOut)))
	if one["video_url"] != "https://v3.fal.media/files/v.mp4" || one["seed"] != nil {
		t.Fatalf("one = %v", one)
	}
	single := json.RawMessage(`{"images":[{"url":"https://v3.fal.media/files/a.jpeg","content_type":"image/jpeg"}]}`)
	if s := result.IDs("r", single, result.Extract(single)); s["images_url"] != "https://v3.fal.media/files/a.jpeg" {
		t.Fatalf("single = %v", s)
	}
}

func TestExtractFileWithoutContentType(t *testing.T) {
	out := `{"images":[{"url":"https://v3.fal.media/a.png","width":1,"height":1}],"thumb":{"url":"https://v3.fal.media/t.png","file_size":10}}`
	m := result.Extract(json.RawMessage(out))
	if len(m) != 2 || m[0].Key != "images[0]" || m[1].Key != "thumb" {
		t.Fatalf("m = %+v", m)
	}
	if got := result.Extract(json.RawMessage(`{"link":{"url":"https://x/y"}}`)); len(got) != 0 {
		t.Fatalf("url alone must not be a file: %+v", got)
	}
}

func TestExtractStems(t *testing.T) {
	out := `{"vocals":{"url":"https://v3.fal.media/v.wav","content_type":"audio/wav"},"drums":{"url":"https://v3.fal.media/d.wav","content_type":"audio/wav"},"bass":{"url":"https://v3.fal.media/b.wav","content_type":"audio/wav"}}`
	m := result.Extract(json.RawMessage(out))
	if len(m) != 3 || m[0].Key != "vocals" || m[1].Key != "drums" || m[2].Key != "bass" {
		t.Fatalf("m = %+v", m)
	}
	ids := result.IDs("r", json.RawMessage(out), m)
	if ids["vocals_url"] != "https://v3.fal.media/v.wav" || ids["drums_url"] == nil || ids["bass_url"] == nil {
		t.Fatalf("ids = %v", ids)
	}
}

func TestIDsDuration(t *testing.T) {
	out := json.RawMessage(`{"video":{"url":"https://v3.fal.media/v.mp4","content_type":"video/mp4"},"duration":5.2}`)
	ids := result.IDs("r", out, result.Extract(out))
	if ids["duration"] != float64(5.2) {
		t.Fatalf("ids = %v", ids)
	}
	if _, ok := result.IDs("r", json.RawMessage(klingOut), nil)["duration"]; ok {
		t.Fatal("duration must be absent")
	}
}

// TestExtractFindsEveryFileInCapturedOutputs instantiates every *Output schema
// of the captured fixtures and checks Extract finds each File-shaped object.
func TestExtractFindsEveryFileInCapturedOutputs(t *testing.T) {
	entries, err := os.ReadDir("../../testdata/live/schemas")
	if err != nil {
		t.Fatal(err)
	}
	files, fixtures := 0, 0
	for _, e := range entries {
		raw, err := os.ReadFile("../../testdata/live/schemas/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		comps, _ := doc["components"].(map[string]any)
		schemas, _ := comps["schemas"].(map[string]any)
		names := make([]string, 0, len(schemas))
		for n := range schemas {
			if strings.HasSuffix(n, "Output") {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		found := false
		for _, n := range names {
			resolved, err := schema.Resolve(doc, schemas[n])
			if err != nil {
				t.Fatalf("%s %s: %v", e.Name(), n, err)
			}
			gen := &instantiator{}
			v := gen.value("", resolved.(map[string]any))
			want := countFiles(v)
			payload, _ := json.Marshal(v)
			got := result.Extract(payload)
			if len(got) != want {
				t.Errorf("%s %s: want %d files, got %d: %s", e.Name(), n, want, len(got), payload)
			}
			if want > 0 {
				found = true
			}
			files += want
		}
		if found {
			fixtures++
		}
	}
	t.Logf("%d files across %d fixtures", files, fixtures)
	if files < 50 || fixtures < 40 {
		t.Fatalf("too few: %d files, %d fixtures", files, fixtures)
	}
}

type instantiator struct{ n int }

func (g *instantiator) value(key string, s map[string]any) any {
	if anyOf, ok := s["anyOf"].([]any); ok {
		for _, b := range anyOf {
			if bm, ok := b.(map[string]any); ok && bm["type"] != "null" {
				return g.value(key, bm)
			}
		}
		return nil
	}
	typ, _ := s["type"].(string)
	if typ == "" {
		if _, ok := s["properties"]; ok {
			typ = "object"
		}
	}
	switch typ {
	case "object":
		out := map[string]any{}
		props, _ := s["properties"].(map[string]any)
		for k, p := range props {
			pm, _ := p.(map[string]any)
			out[k] = g.value(k, pm)
		}
		return out
	case "array":
		items, _ := s["items"].(map[string]any)
		return []any{g.value(key, items)}
	case "string":
		if key == "url" {
			g.n++
			return fmt.Sprintf("https://v3.fal.media/files/%d.bin", g.n)
		}
		return "s"
	case "integer", "number":
		return 1
	case "boolean":
		return true
	}
	return "s"
}

var fileKeys = []string{"content_type", "file_name", "file_size", "width", "height"}

func countFiles(v any) int {
	switch n := v.(type) {
	case map[string]any:
		if _, ok := n["url"]; ok {
			for _, k := range fileKeys {
				if _, ok := n[k]; ok {
					return 1
				}
			}
		}
		c := 0
		for _, child := range n {
			c += countFiles(child)
		}
		return c
	case []any:
		c := 0
		for _, child := range n {
			c += countFiles(child)
		}
		return c
	}
	return 0
}
