package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/catalog"
	"github.com/mik3dev/fal-cli/internal/output"
)

// platform fakes api.fal.ai using the fixtures captured in testdata/live.
func platform(t *testing.T, hits *atomic.Int32, seen *[]string) *httptest.Server {
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
		if seen != nil {
			*seen = append(*seen, r.URL.RequestURI())
		}
		q := r.URL.Query()
		switch r.URL.Path {
		case "/v1/models":
			if ids := q["endpoint_id"]; len(ids) > 0 {
				if ids[0] == "fal-ai/does-not-exist" {
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"error":{"type":"not_found","message":"Endpoint(s) not found"}}`))
					return
				}
				if ids[0] == "fal-ai/veo3.1/fast" {
					_, _ = w.Write([]byte(`{"models":[{"endpoint_id":"fal-ai/veo3.1/fast"}],"next_cursor":null,"has_more":false}`))
					return
				}
				_, _ = w.Write([]byte(`{"models":[{"endpoint_id":"` + ids[0] + `","metadata":{"display_name":"X","category":"text-to-image","status":"active","description":"d"}}],"next_cursor":null,"has_more":false}`))
				return
			}
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
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"type":"not_found","message":"nf"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newService(t *testing.T, url string) *catalog.Service {
	t.Helper()
	c := api.New("k", func(string) string { return "" })
	c.PlatformURL = url
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return &catalog.Service{Client: c, CacheDir: t.TempDir(), Now: time.Now}
}

func TestListVideoTypeMergesThreeCategories(t *testing.T) {
	var hits atomic.Int32
	var seen []string
	s := newService(t, platform(t, &hits, &seen).URL)
	p, err := s.List(context.Background(), catalog.Query{Type: "video", Limit: 500}, false)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 || p.HasMore || p.NextCursor != "" {
		t.Fatalf("hits=%d page=%+v", hits.Load(), p)
	}
	cats := map[string]bool{}
	for _, m := range p.Models {
		cats[m.Category] = true
	}
	if !cats["text-to-video"] || !cats["image-to-video"] || !cats["video-to-video"] {
		t.Errorf("cats = %v", cats)
	}
	for _, u := range seen {
		if strings.Contains(u, "cursor=") {
			t.Errorf("cursor sent for multi-category: %s", u)
		}
	}
}

func TestListTypeLimitCutsTotal(t *testing.T) {
	var hits atomic.Int32
	s := newService(t, platform(t, &hits, nil).URL)
	p, err := s.List(context.Background(), catalog.Query{Type: "image", Limit: 3}, false)
	if err != nil || len(p.Models) != 3 || !p.HasMore {
		t.Fatalf("page=%+v err=%v", p, err)
	}
}

func TestListSingleCategoryPassesCursorAndLimit(t *testing.T) {
	var hits atomic.Int32
	var seen []string
	s := newService(t, platform(t, &hits, &seen).URL)
	p, err := s.List(context.Background(), catalog.Query{Category: "text-to-image", Cursor: "Mg", Limit: 7}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "cursor=Mg") || !strings.Contains(seen[0], "limit=7") || !strings.Contains(seen[0], "category=text-to-image") {
		t.Errorf("seen = %v", seen)
	}
	if p.NextCursor != "Mg" || !p.HasMore {
		t.Errorf("page = %+v", p)
	}
	for _, m := range p.Models {
		if len([]rune(m.Description)) > 200 {
			t.Errorf("description not truncated: %d runes", len([]rune(m.Description)))
		}
	}
}

func TestListExcludesLLMByDefault(t *testing.T) {
	var hits atomic.Int32
	s := newService(t, platform(t, &hits, nil).URL)
	p, err := s.List(context.Background(), catalog.Query{Limit: 100}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Models {
		switch m.Category {
		case "llm", "vision", "speech-to-text", "image-to-3d", "json", "video-to-text":
			t.Errorf("excluded category present: %s %s", m.EndpointID, m.Category)
		}
	}
	if len(p.Models) == 0 {
		t.Fatal("no models")
	}
}

func TestListStatusAndDeprecated(t *testing.T) {
	var hits atomic.Int32
	var seen []string
	s := newService(t, platform(t, &hits, &seen).URL)
	if _, err := s.List(context.Background(), catalog.Query{Category: "text-to-image"}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen[0], "status=active") {
		t.Errorf("default must request active only: %s", seen[0])
	}
	if _, err := s.List(context.Background(), catalog.Query{Category: "text-to-image", Deprecated: true}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(seen[1], "status=") {
		t.Errorf("deprecated must not filter status: %s", seen[1])
	}
}

func TestListCachesPerQuery(t *testing.T) {
	var hits atomic.Int32
	s := newService(t, platform(t, &hits, nil).URL)
	q := catalog.Query{Category: "text-to-image", Limit: 5}
	if _, err := s.List(context.Background(), q, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), q, false); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}
	if _, err := s.List(context.Background(), catalog.Query{Category: "text-to-image", Limit: 6}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), q, true); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d", hits.Load())
	}
}

func TestSearchPassesQ(t *testing.T) {
	var hits atomic.Int32
	var seen []string
	s := newService(t, platform(t, &hits, &seen).URL)
	if _, err := s.List(context.Background(), catalog.Query{Text: "red fox", Category: "text-to-image"}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen[0], "q=red+fox") {
		t.Errorf("seen = %v", seen)
	}
}

func TestPriceFromFixture(t *testing.T) {
	var hits atomic.Int32
	s := newService(t, platform(t, &hits, nil).URL)
	p, err := s.Price(context.Background(), "fal-ai/flux/schnell")
	if err != nil || p.UnitPrice != 0.003 || p.Unit != "megapixels" || p.Currency != "USD" {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	if _, err := s.Price(context.Background(), "fal-ai/flux/schnell"); err != nil || hits.Load() != 1 {
		t.Fatalf("price must be cached: hits=%d err=%v", hits.Load(), err)
	}
}

func TestPriceUnknownIsValidation(t *testing.T) {
	var hits atomic.Int32
	s := newService(t, platform(t, &hits, nil).URL)
	_, err := s.Price(context.Background(), "fal-ai/nope")
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeValidation || !strings.Contains(oe.Message, "unknown model: fal-ai/nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreditsForbiddenIsAuthWithHint(t *testing.T) {
	var hits atomic.Int32
	s := newService(t, platform(t, &hits, nil).URL)
	_, _, err := catalog.Credits(context.Background(), s.Client)
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeAuth || !strings.Contains(oe.Message, "BILLING") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreditsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"username": "u", "credits": map[string]any{"current_balance": 24.5, "currency": "USD"}})
	}))
	t.Cleanup(srv.Close)
	s := newService(t, srv.URL)
	bal, cur, err := catalog.Credits(context.Background(), s.Client)
	if err != nil || bal != 24.5 || cur != "USD" {
		t.Fatalf("bal=%v cur=%q err=%v", bal, cur, err)
	}
}
