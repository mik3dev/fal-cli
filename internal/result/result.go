// Package result finds media files in a queue result payload.
package result

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Media is one downloadable file found in a result.
type Media struct {
	Key         string `json:"key"`
	URL         string `json:"url"`
	ContentType string `json:"content_type,omitempty"`
	FileName    string `json:"file_name,omitempty"`
}

// Extract walks the payload depth-first and collects every File-shaped object
// ({url,  plus any of content_type, file_name, file_size, width, height}) and every fal-hosted https string under a
// *_url / *_urls key. Order of appearance; one entry per URL.
func Extract(payload json.RawMessage) []Media {
	root, err := decodeOrdered(payload)
	if err != nil {
		return nil
	}
	w := &walker{seen: map[string]bool{}}
	w.walk("", root, false)
	return w.out
}

// object is a JSON object with its keys in document order.
type object struct {
	keys []string
	vals map[string]any
}

// decodeOrdered parses JSON keeping object key order (objects become *object).
func decodeOrdered(payload []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	v, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch d := tok.(type) {
	case json.Delim:
		if d == '{' {
			return readObject(dec)
		}
		return readArray(dec)
	default:
		return tok, nil
	}
}

func readObject(dec *json.Decoder) (any, error) {
	o := &object{vals: map[string]any{}}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		if _, dup := o.vals[k]; !dup {
			o.keys = append(o.keys, k)
		}
		o.vals[k] = v
	}
	_, err := dec.Token() // '}'
	return o, err
}

func readArray(dec *json.Decoder) (any, error) {
	var arr []any
	for dec.More() {
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	_, err := dec.Token() // ']'
	return arr, err
}

type walker struct {
	out  []Media
	seen map[string]bool
}

func (w *walker) add(m Media) {
	if m.URL == "" || w.seen[m.URL] {
		return
	}
	w.seen[m.URL] = true
	w.out = append(w.out, m)
}

// walk visits node at key path; underURLKey is true inside a *_url/*_urls key.
func (w *walker) walk(key string, node any, underURLKey bool) {
	switch n := node.(type) {
	case *object:
		if m, ok := fileObject(n.vals); ok {
			m.Key = key
			w.add(m)
			return
		}
		for _, k := range n.keys {
			w.walk(join(key, k), n.vals[k], isURLKey(k))
		}
	case []any:
		for i, v := range n {
			w.walk(fmt.Sprintf("%s[%d]", key, i), v, underURLKey)
		}
	case string:
		if underURLKey && isFalURL(n) {
			w.add(Media{Key: key, URL: n})
		}
	}
}

func fileObject(n map[string]any) (Media, bool) {
	u, ok := n["url"].(string)
	if !ok || u == "" {
		return Media{}, false
	}
	if !hasFileKey(n) {
		return Media{}, false
	}
	ct, _ := n["content_type"].(string)
	fn, _ := n["file_name"].(string)
	return Media{URL: u, ContentType: ct, FileName: fn}, true
}

// fileKeys are the File properties that, next to url, mark a downloadable object.
var fileKeys = []string{"content_type", "file_name", "file_size", "width", "height"}

func hasFileKey(n map[string]any) bool {
	for _, k := range fileKeys {
		if _, ok := n[k]; ok {
			return true
		}
	}
	return false
}

func join(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

func isURLKey(k string) bool { return strings.HasSuffix(k, "_url") || strings.HasSuffix(k, "_urls") }

func isFalURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "fal.media" || h == "fal.run" || strings.HasSuffix(h, ".fal.media") || strings.HasSuffix(h, ".fal.run")
}

// IDs builds the identifiers block: request_id, root seed and duration when present and
// one <key>_url (single) or <key>_urls (several) per media group.
func IDs(requestID string, payload json.RawMessage, media []Media) map[string]any {
	ids := map[string]any{"request_id": requestID}
	var root map[string]any
	if json.Unmarshal(payload, &root) == nil {
		for _, k := range []string{"seed", "duration"} {
			if v, ok := root[k]; ok && v != nil {
				ids[k] = v
			}
		}
	}
	groups := map[string][]string{}
	var order []string
	for _, m := range media {
		g := stripIndexes(m.Key)
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], m.URL)
	}
	for _, g := range order {
		urls := groups[g]
		base := strings.TrimSuffix(strings.TrimSuffix(g, "_urls"), "_url")
		if len(urls) == 1 {
			ids[base+"_url"] = urls[0]
		} else {
			ids[base+"_urls"] = urls
		}
	}
	return ids
}

// stripIndexes turns images[0] into images and a.b[1].c into a.b.c.
func stripIndexes(key string) string {
	var b strings.Builder
	skip := false
	for _, r := range key {
		switch {
		case r == '[':
			skip = true
		case r == ']':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
}
