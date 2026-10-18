// Package schema fetches the queue OpenAPI document of a fal endpoint and
// summarizes its input and output for agents.
package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/cache"
	"github.com/mik3dev/fal-cli/internal/output"
)

const schemaTTL = 24 * time.Hour

// Doc is the resolved OpenAPI document of one endpoint.
type Doc struct {
	Endpoint string
	Category string
	// Input and Output are the POST request body and the result schema with every local $ref expanded.
	Input  map[string]any
	Output map[string]any
	Raw    json.RawMessage
}

// Fetch downloads GET {SchemaURL}/api/openapi/queue/openapi.json?endpoint_id=<endpoint>
// (no auth) and caches it 24h under schemas/<endpoint>. A 404 is "unknown model".
func Fetch(ctx context.Context, c *api.Client, cacheDir, endpoint string, refresh bool, now time.Time) (Doc, error) {
	name := "schemas/" + endpoint
	var raw json.RawMessage
	if !refresh {
		if hit, err := cache.Get(cacheDir, name, schemaTTL, now, &raw); err == nil && hit {
			return parse(endpoint, raw)
		}
	}
	req := api.Request{Method: http.MethodGet, Path: "/api/openapi/queue/openapi.json", Query: url.Values{"endpoint_id": {endpoint}}, Host: api.HostSchema, NoAuth: true}
	if _, err := c.Do(ctx, req, &raw); err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.IsNotFound() {
			return Doc{}, &output.Error{Code: output.CodeValidation, Message: "unknown model: " + endpoint}
		}
		return Doc{}, err
	}
	d, err := parse(endpoint, raw)
	if err != nil {
		return Doc{}, err
	}
	_ = cache.Put(cacheDir, name, raw)
	return d, nil
}

func parse(endpoint string, raw json.RawMessage) (Doc, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return Doc{}, &api.Error{Status: 200, Message: fmt.Sprintf("schema of %s is not a JSON object", endpoint)}
	}
	d := Doc{Endpoint: endpoint, Raw: raw}
	if info, ok := doc["info"].(map[string]any); ok {
		if meta, ok := info["x-fal-metadata"].(map[string]any); ok {
			d.Category, _ = meta["category"].(string)
		}
	}
	in, out, err := bodySchemas(doc, endpoint)
	if err != nil {
		return Doc{}, err
	}
	if d.Input, err = resolved(doc, in); err != nil {
		if strings.Contains(err.Error(), "unresolvable $ref") {
			// Websocket and realtime endpoints publish a queue document without an input schema.
			return Doc{}, &output.Error{Code: output.CodeValidation, Message: fmt.Sprintf("endpoint %s has no queue input schema (websocket or realtime endpoints are not supported by run)", endpoint)}
		}
		return Doc{}, err
	}
	if d.Output, err = resolved(doc, out); err != nil {
		return Doc{}, err
	}
	return d, nil
}

// bodySchemas finds the POST request body (input) and the GET result (output)
// schemas in paths. The POST path is the one without "/requests/".
func bodySchemas(doc map[string]any, endpoint string) (in, out any, err error) {
	paths, _ := doc["paths"].(map[string]any)
	for p, v := range paths {
		ops, _ := v.(map[string]any)
		if strings.Contains(p, "/requests/") {
			if strings.HasSuffix(p, "/requests/{request_id}") {
				out = content(ops["get"], "responses", "200")
			}
			continue
		}
		in = content(ops["post"], "requestBody")
	}
	if in == nil {
		return nil, nil, &api.Error{Status: 200, Message: fmt.Sprintf("schema of %s has no POST request body", endpoint)}
	}
	return in, out, nil
}

// content digs op[keys...].content["application/json"].schema.
func content(op any, keys ...string) any {
	cur := op
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	m, _ := cur.(map[string]any)
	c, _ := m["content"].(map[string]any)
	j, _ := c["application/json"].(map[string]any)
	return j["schema"]
}

func resolved(doc map[string]any, node any) (map[string]any, error) {
	if node == nil {
		return map[string]any{}, nil
	}
	r, err := Resolve(doc, node)
	if err != nil {
		return nil, &api.Error{Status: 200, Message: err.Error()}
	}
	m, _ := r.(map[string]any)
	return m, nil
}
