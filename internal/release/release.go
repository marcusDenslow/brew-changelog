// Package release fetches GitHub releases via the gh CLI and filters them
// to the window between a formula's installed and latest stable versions.
package release

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"

	"github.com/marcusDenslow/brew-changelog/internal/brew"
)

// Release is the subset of the GitHub release object we use.
type Release struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

// Fetch returns up to 100 most recent releases for owner/repo, newest first.
func Fetch(owner, repo string) ([]Release, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/releases?per_page=100", owner, repo)
	out, err := exec.Command("gh", "api", endpoint).Output()
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %w", endpoint, err)
	}
	var releases []Release
	if err := json.Unmarshal(out, &releases); err != nil {
		return nil, fmt.Errorf("parse releases json: %w", err)
	}
	return releases, nil
}

// Filter returns releases r where installed < tag <= latest, sorted oldest first.
func Filter(releases []Release, installed, latest string) []Release {
	matched := make([]Release, 0, len(releases))
	for _, r := range releases {
		tag := brew.NormalizeVersion(r.TagName)
		if tag == "" {
			continue
		}
		if brew.VersionLess(installed, tag) && (tag == latest || brew.VersionLess(tag, latest)) {
			matched = append(matched, r)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return brew.VersionLess(
			brew.NormalizeVersion(matched[i].TagName),
			brew.NormalizeVersion(matched[j].TagName),
		)
	})
	return matched
}
