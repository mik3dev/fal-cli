package cli_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/cli"
)

// fakeFal serves the live fixtures for every fal host (platform, schema, queue, storage share one URL in tests).
func fakeFal(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	read := func(n string) []byte {
		b, err := os.ReadFile("../../testdata/live/" + n)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		q := r.URL.Query()
		switch r.URL.Path {
		case "/v1/models":
			if cat := q.Get("category"); cat != "" {
				_, _ = w.Write(read("models/" + cat + ".json"))
				return
			}
			_, _ = w.Write(read("models/all-page1.json"))
		case "/v1/models/pricing":
			_, _ = w.Write(read("price/all.json"))
		case "/v1/account/billing":
			w.WriteHeader(403)
			_, _ = w.Write(read("billing.json"))
		case "/api/openapi/queue/openapi.json":
			id := strings.ReplaceAll(q.Get("endpoint_id"), "/", "__")
			b, err := os.ReadFile("../../testdata/live/schemas/" + id + ".json")
			if err != nil {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`null`))
				return
			}
			_, _ = w.Write(b)
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"type":"not_found","message":"nf"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func run(t *testing.T, env map[string]string, args ...string) (int, map[string]any, *bytes.Buffer) {
	t.Helper()
	deps, out, errw := authDeps(t, "", env)
	deps.Now = time.Now
	code := cli.Execute(deps, args)
	return code, decode(t, out), errw
}

func apiEnv(srv *httptest.Server) map[string]string {
	return map[string]string{"FAL_API_KEY": "k-secret-123456", "FAL_PLATFORM_URL": srv.URL, "FAL_QUEUE_URL": srv.URL, "FAL_STORAGE_URL": srv.URL, "FAL_SCHEMA_URL": srv.URL}
}

func TestCreditsCommandForbidden(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "credits")
	e, _ := m["error"].(map[string]any)
	if code != 3 || e["code"] != "auth" || !strings.Contains(e["message"].(string), "BILLING") {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestCreditsWithoutKeyIsAuthError(t *testing.T) {
	code, m, _ := run(t, map[string]string{}, "credits")
	if code != 3 || m["ok"] != false {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestModelsListCommandJSON(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "models", "list", "--category", "text-to-image", "--limit", "3")
	if code != 0 || m["ok"] != true {
		t.Fatalf("code=%d m=%v", code, m)
	}
	ms, _ := m["models"].([]any)
	if len(ms) != 3 || m["hasMore"] != true || m["nextCursor"] == nil {
		t.Fatalf("m = %v", m)
	}
	first := ms[0].(map[string]any)
	for _, k := range []string{"endpoint_id", "display_name", "category", "status", "description"} {
		if _, ok := first[k]; !ok {
			t.Errorf("missing %s in %v", k, first)
		}
	}
	if _, ok := first["thumbnail_url"]; ok {
		t.Errorf("entry not trimmed: %v", first)
	}
}

func TestModelsSearchCommand(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "models", "search", "flux", "--type", "image")
	if code != 0 || m["ok"] != true || hits.Load() != 2 {
		t.Fatalf("code=%d hits=%d m=%v", code, hits.Load(), m)
	}
}

func TestModelsSearchNeedsText(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "models", "search")
	if code != 2 || m["error"].(map[string]any)["code"] != "usage" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestModelsPriceCommand(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "models", "price", "fal-ai/flux/schnell")
	p, _ := m["price"].(map[string]any)
	if code != 0 || m["model"] != "fal-ai/flux/schnell" || p["unit_price"] != 0.003 || p["unit"] != "megapixels" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestSchemaCommandSummary(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "models", "schema", "fal-ai/flux/schnell")
	if code != 0 || m["model"] != "fal-ai/flux/schnell" || m["category"] != "text-to-image" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	req, _ := m["required"].([]any)
	fields, _ := m["fields"].([]any)
	outf, _ := m["output"].([]any)
	if len(req) != 1 || len(fields) != 10 || len(outf) == 0 {
		t.Fatalf("m = %v", m)
	}
}

func TestSchemaCommandRaw(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "models", "schema", "fal-ai/flux/schnell", "--raw")
	oa, _ := m["openapi"].(map[string]any)
	if code != 0 || oa["openapi"] != "3.0.4" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

func TestSchemaCommandUnknownModel(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "models", "schema", "fal-ai/nope")
	if code != 2 || m["error"].(map[string]any)["code"] != "validation" {
		t.Fatalf("code=%d m=%v", code, m)
	}
}
