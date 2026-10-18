// Command capture records read-only fal.ai responses (catalog, schemas,
// pricing, billing) as test fixtures. It never POSTs to the queue or storage
// and spends 0 USD. The API key is read from FAL_API_KEY and never written.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
)

var categories = []string{
	"text-to-image", "image-to-image", "text-to-video", "image-to-video", "video-to-video",
	"text-to-audio", "text-to-speech", "audio-to-audio",
}

// Preset candidates from the spec (§5) plus the probe of 2026-10-17.
var extraEndpoints = []string{
	"fal-ai/flux/schnell", "fal-ai/flux-2/klein/9b", "fal-ai/nano-banana-pro", "fal-ai/nano-banana-2",
	"fal-ai/nano-banana-pro/edit", "fal-ai/nano-banana-2/edit",
	"fal-ai/kling-video/v2.5-turbo/pro/text-to-video", "fal-ai/kling-video/v2.5-turbo/pro/image-to-video",
	"fal-ai/veo3.1/fast", "fal-ai/veo3.1/fast/first-last-frame-to-video",
	"minimax/h3-max-turbo/text-to-video", "fal-ai/minimax/hailuo-02/standard/text-to-video",
	"fal-ai/minimax-music/v2.6", "minimax/music-3",
	"fal-ai/elevenlabs/tts/multilingual-v2", "fal-ai/elevenlabs/tts/turbo-v2.5", "fal-ai/elevenlabs/sound-effects/v2",
}

func main() {
	out := flag.String("out", "testdata/live", "output directory")
	perCat := flag.Int("per-category", 5, "schemas per category")
	flag.Parse()
	key := os.Getenv("FAL_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "FAL_API_KEY not set")
		os.Exit(1)
	}
	c := api.New(key, os.Getenv)
	ctx := context.Background()
	r := &report{cats: map[string]int{}, units: map[string]int{}, fileKeys: map[string]int{}, outKeys: map[string]int{}}
	must(os.MkdirAll(filepath.Join(*out, "models"), 0o755))
	must(os.MkdirAll(filepath.Join(*out, "schemas"), 0o755))
	must(os.MkdirAll(filepath.Join(*out, "price"), 0o755))

	// Catalog: two unfiltered pages, one page per category.
	ids := map[string]bool{}
	cursor := ""
	for page := 1; page <= 2; page++ {
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var res catalogPage
		raw := get(ctx, c, api.HostPlatform, "/v1/models", q, &res)
		write(filepath.Join(*out, "models", fmt.Sprintf("all-page%d.json", page)), raw)
		r.scan(res, ids, 0)
		if !res.HasMore || res.NextCursor == nil {
			break
		}
		cursor = *res.NextCursor
	}
	for _, cat := range categories {
		var res catalogPage
		raw := get(ctx, c, api.HostPlatform, "/v1/models", url.Values{"category": {cat}, "limit": {"50"}, "status": {"active"}}, &res)
		write(filepath.Join(*out, "models", cat+".json"), raw)
		r.scan(res, ids, *perCat)
	}
	for _, id := range extraEndpoints {
		ids[id] = true
	}
	// Schemas.
	all := make([]string, 0, len(ids))
	for id := range ids {
		all = append(all, id)
	}
	sort.Strings(all)
	for _, id := range all {
		var doc map[string]any
		raw, err := tryGet(ctx, c, api.HostSchema, "/api/openapi/queue/openapi.json", url.Values{"endpoint_id": {id}}, &doc)
		if err != nil {
			r.schemaErrors = append(r.schemaErrors, id+": "+err.Error())
			continue
		}
		write(filepath.Join(*out, "schemas", strings.ReplaceAll(id, "/", "__")+".json"), raw)
		r.scanSchema(id, doc)
	}
	// Pricing in chunks of 20.
	var prices []map[string]any
	for i := 0; i < len(all); i += 20 {
		chunk := all[i:min(i+20, len(all))]
		var res struct {
			Prices []map[string]any `json:"prices"`
		}
		if _, err := tryGet(ctx, c, api.HostPlatform, "/v1/models/pricing", url.Values{"endpoint_id": chunk}, &res); err != nil {
			r.priceErrors = append(r.priceErrors, err.Error())
			continue
		}
		prices = append(prices, res.Prices...)
	}
	for _, p := range prices {
		if u, ok := p["unit"].(string); ok {
			r.units[u]++
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"prices": prices}, "", " ")
	write(filepath.Join(*out, "price", "all.json"), b)
	// Billing (expected 403 with an API-only key): keep the raw error body.
	_, err := tryGet(ctx, c, api.HostPlatform, "/v1/account/billing", url.Values{"expand": {"credits"}}, nil)
	billing := "ok"
	if err != nil {
		billing = err.Error()
		var ae *api.Error
		if asErr(err, &ae) {
			write(filepath.Join(*out, "billing.json"), ae.Raw)
		}
	}
	r.billing = billing
	write(filepath.Join(*out, "REPORT.md"), []byte(r.String(len(all), len(prices))))
	fmt.Fprintf(os.Stderr, "captured %d schemas, %d prices\n", len(all)-len(r.schemaErrors), len(prices))
}

type catalogPage struct {
	Models []struct {
		EndpointID string `json:"endpoint_id"`
		Metadata   *struct {
			Category string `json:"category"`
		} `json:"metadata"`
	} `json:"models"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

type report struct {
	cats, units, fileKeys, outKeys             map[string]int
	noMeta, schemaErrors, priceErrors, noInput []string
	fileShapes                                 map[string]bool
	billing                                    string
}

func (r *report) scan(p catalogPage, ids map[string]bool, take int) {
	n := 0
	for _, m := range p.Models {
		if m.Metadata == nil {
			r.noMeta = append(r.noMeta, m.EndpointID)
			continue
		}
		r.cats[m.Metadata.Category]++
		if take > 0 && n < take {
			ids[m.EndpointID] = true
			n++
		}
	}
}

func (r *report) scanSchema(id string, doc map[string]any) {
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	hasInput := false
	for name, v := range schemas {
		sch, _ := v.(map[string]any)
		props, _ := sch["properties"].(map[string]any)
		switch {
		case strings.HasSuffix(name, "Input"):
			hasInput = true
			for k, pv := range props {
				pm, _ := pv.(map[string]any)
				_, hasUI := pm["ui"]
				if strings.HasSuffix(k, "_url") || strings.HasSuffix(k, "_urls") || hasUI {
					r.fileKeys[k]++
				}
			}
		case strings.HasSuffix(name, "Output"):
			for k, pv := range props {
				pm, _ := pv.(map[string]any)
				ref, _ := pm["$ref"].(string)
				items, _ := pm["items"].(map[string]any)
				iref, _ := items["$ref"].(string)
				if ref != "" || iref != "" {
					r.outKeys[k+" -> "+ref+iref]++
				}
			}
		case name == "File" || name == "Image" || name == "Video" || strings.Contains(name, "Audio"):
			if r.fileShapes == nil {
				r.fileShapes = map[string]bool{}
			}
			keys := make([]string, 0, len(props))
			for k := range props {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			r.fileShapes[name+": "+strings.Join(keys, ",")] = true
		}
	}
	if !hasInput {
		r.noInput = append(r.noInput, id)
	}
}

func (r *report) String(schemas, prices int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Captura real fal.ai (%s)\n\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "Schemas: %d (errores %d) · Precios: %d · Billing: %s\n\n", schemas, len(r.schemaErrors), prices, r.billing)
	section(&b, "Endpoints por categoría (muestra)", r.cats)
	fmt.Fprintf(&b, "## Sin metadata\n\n%s\n\n", list(r.noMeta))
	fmt.Fprintf(&b, "## Schemas sin *Input\n\n%s\n\n", list(r.noInput))
	fmt.Fprintf(&b, "## Errores de schema\n\n%s\n\n", list(r.schemaErrors))
	section(&b, "Claves de input *_url/*_urls o con ui", r.fileKeys)
	section(&b, "Claves de output con $ref", r.outKeys)
	section(&b, "Unidades de precio", r.units)
	fmt.Fprintf(&b, "## Formas de File/Image/Video/Audio\n\n")
	for k := range r.fileShapes {
		fmt.Fprintf(&b, "- %s\n", k)
	}
	fmt.Fprintf(&b, "\n## Errores de pricing\n\n%s\n", list(r.priceErrors))
	return b.String()
}

func section(b *strings.Builder, title string, m map[string]int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "## %s\n\n", title)
	for _, k := range keys {
		fmt.Fprintf(b, "- %s: %d\n", k, m[k])
	}
	b.WriteString("\n")
}

func list(s []string) string {
	if len(s) == 0 {
		return "(ninguno)"
	}
	sort.Strings(s)
	return "- " + strings.Join(s, "\n- ")
}

func get(ctx context.Context, c *api.Client, h api.Host, path string, q url.Values, out any) []byte {
	raw, err := tryGet(ctx, c, h, path, q, out)
	must(err)
	return raw
}

func tryGet(ctx context.Context, c *api.Client, h api.Host, path string, q url.Values, out any) ([]byte, error) {
	time.Sleep(600 * time.Millisecond) // stay well under the platform rate limit
	resp, err := c.Do(ctx, api.Request{Method: "GET", Path: path, Query: q, Host: h}, out)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func asErr(err error, target **api.Error) bool {
	e, ok := err.(*api.Error)
	if ok {
		*target = e
	}
	return ok
}

func write(path string, b []byte) { must(os.WriteFile(path, b, 0o644)) }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
