package schema

import (
	"fmt"
	"net/url"
	"strings"
)

// maxNodes caps the expanded tree so a document whose $refs fan out
// exponentially ("billion laughs") fails fast instead of exhausting memory.
const maxNodes = 200_000

// ErrTooLarge is returned when expansion exceeds maxNodes.
var ErrTooLarge = fmt.Errorf("schema too large: more than %d nodes after $ref expansion", maxNodes)

// Resolve returns a deep copy of node with every local `$ref` expanded.
// Segments are percent-decoded (never trimmed). A ref that is already being
// expanded (cycle) is left as {"$ref": ...}; external refs are kept as is.
// Neither doc nor node is mutated.
func Resolve(doc map[string]any, node any) (any, error) {
	r := &resolver{doc: doc, active: map[string]bool{}}
	return r.resolve(node)
}

type resolver struct {
	doc    map[string]any
	active map[string]bool
	nodes  int
}

func (r *resolver) resolve(node any) (any, error) {
	if r.nodes++; r.nodes > maxNodes {
		return nil, ErrTooLarge
	}
	switch n := node.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			return r.resolveRef(n, ref)
		}
		out := make(map[string]any, len(n))
		for k, v := range n {
			rv, err := r.resolve(v)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			rv, err := r.resolve(v)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return node, nil
	}
}

func (r *resolver) resolveRef(n map[string]any, ref string) (any, error) {
	doc, active := r.doc, r.active
	if !strings.HasPrefix(ref, "#/") || active[ref] {
		return map[string]any{"$ref": ref}, nil
	}
	target, err := lookup(doc, ref)
	if err != nil {
		return nil, err
	}
	active[ref] = true
	defer delete(active, ref)
	res, err := r.resolve(target)
	if err != nil {
		return nil, err
	}
	resMap, isMap := res.(map[string]any)
	if !isMap || len(n) == 1 {
		return res, nil
	}
	// Sibling keys of $ref override the target's (e.g. a local description).
	out := make(map[string]any, len(resMap)+len(n))
	for k, v := range resMap {
		out[k] = v
	}
	for k, v := range n {
		if k == "$ref" {
			continue
		}
		rv, err := r.resolve(v)
		if err != nil {
			return nil, err
		}
		out[k] = rv
	}
	return out, nil
}

func lookup(doc map[string]any, ref string) (any, error) {
	var cur any = doc
	for _, raw := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		seg, err := url.PathUnescape(raw)
		if err != nil {
			return nil, fmt.Errorf("bad $ref %q: %w", ref, err)
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unresolvable $ref %q", ref)
		}
		if cur, ok = m[seg]; !ok {
			return nil, fmt.Errorf("unresolvable $ref %q", ref)
		}
	}
	return cur, nil
}
