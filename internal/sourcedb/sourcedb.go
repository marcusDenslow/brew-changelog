// Package sourcedb exposes the precomputed formula -> upstream source map
// embedded at build time from sources.json. Generator: ../../cmd/gen-sources.
package sourcedb

import (
	_ "embed"
	"encoding/json"
)

//go:embed sources.json
var raw []byte

// Source describes where a formula's changelog lives. Kind dispatches the
// runtime fetcher; URL is the literal address to hit.
//
// Kind values:
//   - "file":     URL is raw markdown (e.g. raw.githubusercontent.com/.../CHANGELOG.md).
//   - "releases": URL is the host's releases API endpoint
//     (e.g. api.github.com/repos/owner/repo/releases).
//   - "":         entry exists but couldn't be classified.
type Source struct {
	Kind string `json:"kind,omitempty"`
	URL  string `json:"url,omitempty"`
}

var entries map[string]Source

func init() {
	if err := json.Unmarshal(raw, &entries); err != nil {
		panic("sourcedb: bad embedded sources.json: " + err.Error())
	}
}

// Lookup returns the precomputed Source for a formula, if known.
func Lookup(name string) (Source, bool) {
	s, ok := entries[name]
	return s, ok
}

// Len reports the number of embedded entries. Useful for diagnostics.
func Len() int { return len(entries) }
