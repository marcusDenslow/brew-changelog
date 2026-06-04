// Package source resolves a formula's upstream source URL to an owner/repo pair.
package source

import (
	"regexp"
	"strings"

	"github.com/marcusDenslow/brew-changelog/internal/brew"
	"github.com/marcusDenslow/brew-changelog/internal/sourcedb"
)

var (
	githubRE        = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)`)
	githubAPIRepoRE = regexp.MustCompile(`api\.github\.com/repos/([^/]+)/([^/]+)`)
	githubRawRepoRE = regexp.MustCompile(`raw\.githubusercontent\.com/([^/]+)/([^/]+)`)
)

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

// Resolve consults the embedded sourcedb when useMap is true. The stored URL
// is parsed back to (owner, repo) so the rest of the pipeline (gh-api
// releases, filter, render) runs unchanged — the lookup replaces the
// brew-info+regex URL discovery step but leaves output identical.
//
// Both URL shapes the generator emits are recognised:
//   - kind="releases": api.github.com/repos/<owner>/<repo>/releases
//   - kind="file":     raw.githubusercontent.com/<owner>/<repo>/HEAD/<path>
//
// On miss (or useMap=false), falls through to the legacy regex resolver.
func Resolve(name string, info *brew.FormulaInfo, useMap bool) (owner, repo string, ok bool) {
	if useMap {
		if s, found := sourcedb.Lookup(name); found && s.URL != "" {
			if m := githubAPIRepoRE.FindStringSubmatch(s.URL); m != nil {
				return m[1], m[2], true
			}
			if m := githubRawRepoRE.FindStringSubmatch(s.URL); m != nil {
				return m[1], m[2], true
			}
		}
	}
	return GitHubFrom(info)
}
