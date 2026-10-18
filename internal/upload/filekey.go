package upload

import (
	"strings"

	"github.com/mik3dev/fal-cli/internal/schema"
)

// fileUIFields are the ui.field values fal uses for file pickers.
var fileUIFields = map[string]bool{"image": true, "video": true, "audio": true, "file": true}

// SchemaFileKey reports whether key takes a file: by name (IsFileKey) or by a
// ui.field of image/video/audio/file on the property or its items. A
// description mentioning "URL" is deliberately not a signal: it would let
// schema text turn a value the user meant as text into an upload of a local
// file. Calibration over the 47 captured schemas found no field that only the
// description would have caught.
// ponytail: three signals (name, ui.field, example URL); calibrated against 1268 live schemas in stage 6.
func SchemaFileKey(sch map[string]any, key string) bool {
	if IsFileKey(key) {
		return true
	}
	p, ok := schema.PropSchema(sch, key)
	if !ok {
		return false
	}
	return fileProp(p)
}

// fileProp inspects one property node (descending into anyOf/oneOf and items).
// A string whose first example is an http(s) URL is a file field too: the live
// sweep (stage 6) found fields such as model_image or image_source with an
// https example and no ui.field. Examples are structured data, not prose.
func fileProp(p map[string]any) bool {
	if ui, ok := p["ui"].(map[string]any); ok {
		if f, _ := ui["field"].(string); fileUIFields[f] {
			return true
		}
	}
	if ex, ok := p["examples"].([]any); ok && len(ex) > 0 {
		if s, _ := ex[0].(string); strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
			return true
		}
	}
	if items, ok := p["items"].(map[string]any); ok && fileProp(items) {
		return true
	}
	for _, bk := range []string{"anyOf", "oneOf"} {
		alts, _ := p[bk].([]any)
		for _, a := range alts {
			if m, ok := a.(map[string]any); ok && m["type"] != "null" && fileProp(m) {
				return true
			}
		}
	}
	return false
}
