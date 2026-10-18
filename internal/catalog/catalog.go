// Package catalog queries the fal.ai model catalog, pricing and billing with a local cache.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/cache"
	"github.com/mik3dev/fal-cli/internal/output"
)

const (
	catalogTTL   = time.Hour
	priceTTL     = 24 * time.Hour
	maxDescRunes = 200
	defaultLimit = 50
)

// typeGroups maps --type to native categories (spec §4).
var typeGroups = map[string][]string{
	"image": {"text-to-image", "image-to-image"},
	"video": {"text-to-video", "image-to-video", "video-to-video"},
	"audio": {"text-to-audio", "text-to-speech", "audio-to-audio"},
}

// hiddenByDefault are categories left out of unfiltered list/search (not media generation).
var hiddenByDefault = map[string]bool{"llm": true, "vision": true, "speech-to-text": true, "image-to-3d": true, "json": true, "video-to-text": true}

// Model is a trimmed catalog entry.
type Model struct {
	EndpointID  string `json:"endpoint_id"`
	DisplayName string `json:"display_name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// Query selects catalog entries. Type wins over nothing; Category wins over Type.
type Query struct {
	Type, Category, Text, Cursor string
	Limit                        int
	Deprecated                   bool
}

// Page is one page of results.
type Page struct {
	Models     []Model `json:"models"`
	NextCursor string  `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

// Price is the unit price of one endpoint.
type Price struct {
	UnitPrice float64 `json:"unit_price"`
	Unit      string  `json:"unit"`
	Currency  string  `json:"currency"`
}

// Service is the catalog client with cache.
type Service struct {
	Client   *api.Client
	CacheDir string
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type rawPage struct {
	Models []struct {
		EndpointID string `json:"endpoint_id"`
		Metadata   *struct {
			DisplayName string `json:"display_name"`
			Category    string `json:"category"`
			Status      string `json:"status"`
			Description string `json:"description"`
		} `json:"metadata"`
	} `json:"models"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

func (r rawPage) models() []Model {
	out := make([]Model, 0, len(r.Models))
	for _, m := range r.Models {
		mm := Model{EndpointID: m.EndpointID}
		if m.Metadata != nil {
			mm.DisplayName, mm.Category, mm.Status = m.Metadata.DisplayName, m.Metadata.Category, m.Metadata.Status
			mm.Description = truncate(m.Metadata.Description)
		}
		out = append(out, mm)
	}
	return out
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxDescRunes {
		return s
	}
	return string(r[:maxDescRunes-1]) + "…"
}

// List returns one page. A Type spanning several categories issues one
// request per category and merges them in order; cursors then do not apply.
// ponytail: Type with 3 categories = 3 requests merged in order, cursor only when a single category applies.
func (s *Service) List(ctx context.Context, q Query, refresh bool) (Page, error) {
	if q.Limit <= 0 {
		q.Limit = defaultLimit
	}
	cats := []string{q.Category}
	if q.Category == "" && q.Type != "" {
		g, ok := typeGroups[q.Type]
		if !ok {
			return Page{}, &output.Error{Code: output.CodeUsage, Message: fmt.Sprintf("unknown --type %q: want image, video or audio", q.Type)}
		}
		cats = g
	}
	if len(cats) == 1 {
		p, err := s.fetchPage(ctx, q, cats[0], q.Cursor, q.Limit, refresh)
		if err != nil {
			return Page{}, err
		}
		return cut(p, q.Limit), nil
	}
	var all []Model
	for _, c := range cats {
		p, err := s.fetchPage(ctx, q, c, "", q.Limit, refresh)
		if err != nil {
			return Page{}, err
		}
		all = append(all, p.Models...)
	}
	return cut(Page{Models: all}, q.Limit), nil
}

// cut enforces Limit locally too (the server honors it, but the contract must not depend on it).
func cut(p Page, limit int) Page {
	if len(p.Models) > limit {
		p.Models, p.HasMore = p.Models[:limit], true
	}
	return p
}

func (s *Service) fetchPage(ctx context.Context, q Query, category, cursor string, limit int, refresh bool) (Page, error) {
	v := url.Values{"limit": {strconv.Itoa(limit)}}
	if category != "" {
		v.Set("category", category)
	}
	if q.Text != "" {
		v.Set("q", q.Text)
	}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	if !q.Deprecated {
		v.Set("status", "active")
	}
	name := "catalog/" + hash(v.Encode())
	var raw rawPage
	if !refresh {
		if hit, err := cache.Get(s.CacheDir, name, catalogTTL, s.now(), &raw); err == nil && hit {
			return s.page(raw, category), nil
		}
	}
	if _, err := s.Client.Do(ctx, api.Request{Method: http.MethodGet, Path: "/v1/models", Query: v, Host: api.HostPlatform}, &raw); err != nil {
		return Page{}, err
	}
	_ = cache.Put(s.CacheDir, name, raw)
	return s.page(raw, category), nil
}

func (s *Service) page(raw rawPage, category string) Page {
	ms := raw.models()
	if category == "" {
		kept := ms[:0:0]
		for _, m := range ms {
			if !hiddenByDefault[m.Category] {
				kept = append(kept, m)
			}
		}
		ms = kept
	}
	p := Page{Models: ms, HasMore: raw.HasMore}
	if raw.NextCursor != nil {
		p.NextCursor = *raw.NextCursor
	}
	return p
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// Price returns the unit price of an endpoint (cached 24h).
func (s *Service) Price(ctx context.Context, endpoint string) (Price, error) {
	name := "price/" + endpoint
	var p Price
	if hit, err := cache.Get(s.CacheDir, name, priceTTL, s.now(), &p); err == nil && hit {
		return p, nil
	}
	var res struct {
		Prices []struct {
			EndpointID string `json:"endpoint_id"`
			Price
		} `json:"prices"`
	}
	v := url.Values{"endpoint_id": {endpoint}}
	if _, err := s.Client.Do(ctx, api.Request{Method: http.MethodGet, Path: "/v1/models/pricing", Query: v, Host: api.HostPlatform}, &res); err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.IsNotFound() {
			return Price{}, unknownModel(endpoint)
		}
		return Price{}, err
	}
	for _, e := range res.Prices {
		if e.EndpointID == endpoint {
			_ = cache.Put(s.CacheDir, name, e.Price)
			return e.Price, nil
		}
	}
	return Price{}, unknownModel(endpoint)
}

func unknownModel(endpoint string) error {
	return &output.Error{Code: output.CodeValidation, Message: "unknown model: " + endpoint}
}

// Credits returns the account balance. Needs a key with the BILLING permission.
func Credits(ctx context.Context, c *api.Client) (balance float64, currency string, err error) {
	var res struct {
		Credits *struct {
			CurrentBalance float64 `json:"current_balance"`
			Currency       string  `json:"currency"`
		} `json:"credits"`
	}
	v := url.Values{"expand": {"credits"}}
	if _, err := c.Do(ctx, api.Request{Method: http.MethodGet, Path: "/v1/account/billing", Query: v, Host: api.HostPlatform}, &res); err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			return 0, "", &output.Error{Code: output.CodeAuth, Message: "the API key lacks the BILLING permission; create one with API+BILLING at https://fal.ai/dashboard/keys", Details: map[string]any{"status": 403}}
		}
		return 0, "", err
	}
	if res.Credits == nil {
		return 0, "", &api.Error{Status: 200, Message: "response lacks credits"}
	}
	return res.Credits.CurrentBalance, res.Credits.Currency, nil
}

// MarshalJSON keeps nextCursor null when empty, for the CLI contract.
func (p Page) MarshalJSON() ([]byte, error) {
	var next *string
	if p.NextCursor != "" {
		next = &p.NextCursor
	}
	models := p.Models
	if models == nil {
		models = []Model{}
	}
	return json.Marshal(struct {
		Models     []Model `json:"models"`
		NextCursor *string `json:"nextCursor"`
		HasMore    bool    `json:"hasMore"`
	}{models, next, p.HasMore})
}
