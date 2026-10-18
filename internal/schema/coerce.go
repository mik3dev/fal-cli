package schema

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// GuessLiterals returns a copy of in where string values that look like JSON
// literals are typed: objects/arrays for {/[, booleans and numbers (as
// json.Number). Array elements are guessed one by one. in is not mutated.
func GuessLiterals(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if items, ok := v.([]any); ok {
			g := make([]any, len(items))
			for i, it := range items {
				g[i] = guessLiteral(it)
			}
			out[k] = g
			continue
		}
		out[k] = guessLiteral(v)
	}
	return out
}

// guessLiteral types one string: JSON for {/[, bool, number. Padded strings stay strings.
func guessLiteral(v any) any {
	raw, ok := v.(string)
	if !ok {
		return v
	}
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		var out any
		if json.Unmarshal([]byte(raw), &out) == nil {
			return out
		}
		return raw
	}
	if raw != strings.TrimSpace(raw) {
		return raw
	}
	switch raw {
	case "true":
		return true
	case "false":
		return false
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && json.Valid([]byte(raw)) {
		return json.Number(raw)
	}
	return raw
}
