// Package brew shells out to the brew CLI and parses its JSON output.
package brew

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hashicorp/go-version"
)

// FormulaInfo is the subset of `brew info --json=v2` we care about.
type FormulaInfo struct {
	Name         string
	Installed    string // raw, e.g. "0.72.0_1"
	LatestStable string // raw, e.g. "0.73.1"
	Homepage     string
	HeadURL      string // urls.head.url
	StableURL    string // urls.stable.url
}

// Outdated returns the names of every outdated formula. Casks are excluded.
func Outdated() ([]string, error) {
	out, err := exec.Command("brew", "outdated", "--json=v2").Output()
	if err != nil {
		return nil, fmt.Errorf("brew outdated: %w", err)
	}
	var resp struct {
		Formulae []struct {
			Name string `json:"name"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse brew outdated json: %w", err)
	}
	names := make([]string, 0, len(resp.Formulae))
	for _, f := range resp.Formulae {
		names = append(names, f.Name)
	}
	return names, nil
}

// Info runs `brew info --json=v2 --formula <name>` and extracts version + URL fields.
func Info(name string) (*FormulaInfo, error) {
	out, err := exec.Command("brew", "info", "--json=v2", "--formula", name).Output()
	if err != nil {
		return nil, fmt.Errorf("brew info %s: %w", name, err)
	}
	var resp struct {
		Formulae []struct {
			Name      string `json:"name"`
			Homepage  string `json:"homepage"`
			Installed []struct {
				Version string `json:"version"`
			} `json:"installed"`
			Versions struct {
				Stable string `json:"stable"`
			} `json:"versions"`
			URLs struct {
				Stable struct {
					URL string `json:"url"`
				} `json:"stable"`
				Head struct {
					URL string `json:"url"`
				} `json:"head"`
			} `json:"urls"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parse brew info json: %w", err)
	}
	if len(resp.Formulae) == 0 {
		return nil, fmt.Errorf("no formula in brew info response for %s", name)
	}
	f := resp.Formulae[0]
	info := &FormulaInfo{
		Name:         f.Name,
		LatestStable: f.Versions.Stable,
		Homepage:     f.Homepage,
		HeadURL:      f.URLs.Head.URL,
		StableURL:    f.URLs.Stable.URL,
	}
	if len(f.Installed) > 0 {
		info.Installed = f.Installed[0].Version
	}
	return info, nil
}

// NormalizeVersion strips a leading "v" and brew's "_N" revision suffix.
// Examples: "v1.2.3"   -> "1.2.3"
//           "1.2.3_1"  -> "1.2.3"
func NormalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '_'); i >= 0 {
		v = v[:i]
	}
	return v
}

// VersionLess reports whether a < b under semver-like rules.
// Falls back to string compare when either side is unparseable.
func VersionLess(a, b string) bool {
	if a == b {
		return false
	}
	va, errA := version.NewVersion(a)
	vb, errB := version.NewVersion(b)
	if errA != nil || errB != nil {
		return a < b
	}
	return va.LessThan(vb)
}
