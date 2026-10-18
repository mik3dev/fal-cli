package upload

import (
	"regexp"

	"github.com/mik3dev/fal-cli/internal/schema"
)

// fileUIFields are the ui.field values fal uses for file pickers.
var fileUIFields = map[string]bool{"image": true, "video": true, "audio": true, "file": true}

var urlWord = regexp.MustCompile(`\bURLs?\b`)

// SchemaFileKey reports whether key takes a file: by name (IsFileKey), by a
// ui.field of image/video/audio/file on the property or its items, or by a
// string property whose description mentions "URL".
// ponytail: three signals (name, ui.field, "URL" in description); stage 6 re-checks against every live schema.
func SchemaFileKey(sch map[string]any, key string) bool {
	if IsFileKey(key) {
		return true
	}
	p, ok := schema.PropSchema(sch, key)
	if !ok {
		return false
	}
	return fileProp(p, true)
}

// fileProp inspects one property node (descending into anyOf/oneOf and items).
func fileProp(p map[string]any, top bool) bool {
	if ui, ok := p["ui"].(map[string]any); ok {
		if f, _ := ui["field"].(string); fileUIFields[f] {
			return true
		}
	}
	if top {
		if d, ok := p["description"].(string); ok && urlWord.MatchString(d) && stringLike(p) {
			return true
		}
	}
	if items, ok := p["items"].(map[string]any); ok && fileProp(items, false) {
		return true
	}
	for _, bk := range []string{"anyOf", "oneOf"} {
		alts, _ := p[bk].([]any)
		for _, a := range alts {
			if m, ok := a.(map[string]any); ok && m["type"] != "null" && fileProp(m, false) {
				return true
			}
		}
	}
	return false
}

// stringLike: type string, array of strings, or a non-null anyOf branch that is.
func stringLike(p map[string]any) bool {
	switch p["type"] {
	case "string":
		return true
	case "array":
		items, _ := p["items"].(map[string]any)
		return items != nil && stringLike(items)
	}
	for _, bk := range []string{"anyOf", "oneOf"} {
		alts, _ := p[bk].([]any)
		for _, a := range alts {
			if m, ok := a.(map[string]any); ok && m["type"] != "null" && stringLike(m) {
				return true
			}
		}
	}
	return false
}
