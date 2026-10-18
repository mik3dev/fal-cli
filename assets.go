// Package falcli exposes assets embedded in the fal-cli binary.
package falcli

import "embed"

// Assets holds the factory presets (and, later, the bundled agent skill).
//
//go:embed presets/default.yaml
var Assets embed.FS
