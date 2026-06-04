// Package source resolves a formula's upstream source URL to an owner/repo pair.
package source

import (
	"regexp"
	"strings"

	"github.com/marcusDenslow/brew-changelog/internal/brew"
)

var githubRE = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)`)

// GitHubFrom returns the (owner, repo) pair for a formula if any of its
// candidate URLs point at github.com. Candidates are tried in order:
// homepage, urls.head.url, urls.stable.url. First match wins.
func GitHubFrom(info *brew.FormulaInfo) (owner, repo string, ok bool) {
	for _, candidate := range []string{info.Homepage, info.HeadURL, info.StableURL} {
		m := githubRE.FindStringSubmatch(candidate)
		if m == nil {
			continue
		}
		repoName := strings.TrimSuffix(m[2], ".git")
		repoName = strings.TrimSuffix(repoName, "/")
		return m[1], repoName, true
	}
	return "", "", false
}
