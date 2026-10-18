package schema

import (
	"sort"
	"strings"
)

const maxDescRunes = 300

// Field is one input or output key as an agent needs to know it.
type Field struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Items       string   `json:"items,omitempty"`
}

// Summary is the compact view of an endpoint's input and output.
type Summary struct {
	Required []string `json:"required"`
	Fields   []Field  `json:"fields"`
	Output   []Field  `json:"output"`
}

// Summarize lists the input fields (ordered by x-fal-order-properties, else
// by name) and the output fields of d.
func Summarize(d Doc) (Summary, error) {
	in := fields(d.Input)
	s := Summary{Required: []string{}, Fields: in, Output: fields(d.Output)}
	for _, f := range in {
		if f.Required {
			s.Required = append(s.Required, f.Name)
		}
	}
	return s, nil
}

func fields(node map[string]any) []Field {
	props, _ := node["properties"].(map[string]any)
	req := map[string]bool{}
	if r, ok := node["required"].([]any); ok {
		for _, n := range r {
			if s, ok := n.(string); ok {
				req[s] = true
			}
		}
	}
	fs := make([]Field, 0, len(props))
	for name, raw := range props {
		p, _ := raw.(map[string]any)
		f := fieldFrom(name, p)
		f.Required = req[name]
		fs = append(fs, f)
	}
	order := map[string]int{}
	if o, ok := node["x-fal-order-properties"].([]any); ok {
		for i, n := range o {
			if s, ok := n.(string); ok {
				order[s] = i + 1
			}
		}
	}
	sort.Slice(fs, func(i, j int) bool {
		oi, oj := order[fs[i].Name], order[fs[j].Name]
		if oi != oj {
			// Unordered names (0) sort last.
			if oi == 0 || oj == 0 {
				return oj == 0
			}
			return oi < oj
		}
		return fs[i].Name < fs[j].Name
	})
	return fs
}

func fieldFrom(name string, p map[string]any) Field {
	p = flattenNullable(p)
	f := Field{Name: name, Type: typeOf(p), Default: p["default"]}
	if d, ok := p["description"].(string); ok {
		f.Description = truncate(d)
	}
	if e, ok := p["enum"].([]any); ok && len(e) > 0 {
		f.Enum = e
	}
	f.Min, f.Max = number(p["minimum"]), number(p["maximum"])
	if items, ok := p["items"].(map[string]any); ok {
		f.Items = typeOf(flattenNullable(items))
	}
	return f
}

// flattenNullable rewrites anyOf [T, null] as T and anyOf [A, B, ...] as a
// node whose type is "a|b" with the enum/bounds merged, keeping p's own keys
// (description, default) on top. p is not mutated.
func flattenNullable(p map[string]any) map[string]any {
	alts, ok := p["anyOf"].([]any)
	if !ok {
		if alts, ok = p["oneOf"].([]any); !ok {
			return p
		}
	}
	out := map[string]any{}
	var types []string
	for _, a := range alts {
		m, _ := a.(map[string]any)
		m = flattenNullable(m)
		if m["type"] == "null" {
			continue
		}
		if t := typeOf(m); t != "" && !contains(types, t) {
			types = append(types, t)
		}
		for k, v := range m {
			if k == "type" {
				continue
			}
			if _, dup := out[k]; !dup {
				out[k] = v
			}
		}
	}
	out["type"] = strings.Join(types, "|")
	for k, v := range p {
		if k != "anyOf" && k != "oneOf" {
			out[k] = v
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// typeOf names a schema's type; type arrays join with "|", untyped nodes are inferred.
func typeOf(p map[string]any) string {
	switch t := p["type"].(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok && s != "null" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "|")
	}
	switch {
	case p["properties"] != nil:
		return "object"
	case p["items"] != nil:
		return "array"
	case p["enum"] != nil:
		return "string"
	}
	return ""
}

func number(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxDescRunes {
		return s
	}
	return string(r[:maxDescRunes])
}
