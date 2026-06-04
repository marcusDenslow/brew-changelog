// Package changelog fetches a project's canonical changelog file from its
// GitHub repo and extracts just the section that corresponds to a given tag.
//
// Many projects ship terse GitHub releases that just point at a `CHANGES` or
// `CHANGELOG.md` in the repo (tmux, vim, GNU coreutils, lots of Apache
// projects). Without this layer, those releases render as "see the link"
// stubs. With it, we can fetch the real notes and classify them.
package changelog

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// CommonPaths is the ordered list of changelog files we probe. Order matches
// rough convention frequency: markdown first, then plain text, then GNU-style.
var CommonPaths = []string{
	"CHANGELOG.md",
	"CHANGELOG",
	"CHANGES.md",
	"CHANGES",
	"RELEASES.md",
	"RELEASE_NOTES.md",
	"NEWS.md",
	"NEWS",
	"History.md",
	"HISTORY.md",
}

// Result holds a fetched changelog and the repo path it came from.
type Result struct {
	Content string
	Path    string
}

// Fetch tries each path in CommonPaths on owner/repo at the given git ref.
// Returns the first one that exists. Returns (nil, nil) if none do.
func Fetch(owner, repo, ref string) (*Result, error) {
	for _, path := range CommonPaths {
		content, err := fetchOne(owner, repo, path, ref)
		if err == nil && content != "" {
			return &Result{Content: content, Path: path}, nil
		}
	}
	return nil, nil
}

func fetchOne(owner, repo, path, ref string) (string, error) {
	endpoint := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", owner, repo, path, ref)
	cmd := exec.Command("gh", "api", "-H", "Accept: application/vnd.github.raw", endpoint)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var (
	hashHeaderRE   = regexp.MustCompile(`^#{1,4}\s+`)
	bracketHeadRE  = regexp.MustCompile(`^\s*\[v?\d+\.\d+`)
	versionStartRE = regexp.MustCompile(`^v?\d+\.\d+`)
	nameVersionRE  = regexp.MustCompile(`^\w+\s+v?\d+\.\d+`)
	changesFromRE  = regexp.MustCompile(`(?i)^changes\s+from\s+\S+\s+to\s+\S+`)
	versionRE      = regexp.MustCompile(`v?(\d+\.\d+(?:\.\d+)?[\w.-]*)`)
)

// ExtractSection returns the slice of `content` covering just `tag`'s entries.
// It locates the first header line that mentions `tag` and slices through to
// the next header line that mentions a different version. Empty when the tag
// can't be found.
func ExtractSection(content, tag string) string {
	tag = strings.TrimPrefix(tag, "v")
	if tag == "" {
		return ""
	}

	lines := strings.Split(content, "\n")

	// Tag matcher: tag bounded by non-version chars so "3.6" doesn't match "3.60".
	tagBound := regexp.MustCompile(`(^|[^0-9a-zA-Z.])` + regexp.QuoteMeta(tag) + `($|[^0-9a-zA-Z])`)

	startLine := -1
	for i, line := range lines {
		if tagBound.MatchString(line) && looksLikeHeader(lines, i) {
			startLine = i
			break
		}
	}
	if startLine < 0 {
		return ""
	}

	endLine := len(lines)
	for i := startLine + 1; i < len(lines); i++ {
		if looksLikeHeader(lines, i) && containsOtherVersion(lines[i], tag) {
			endLine = i
			break
		}
	}

	return strings.TrimSpace(strings.Join(lines[startLine:endLine], "\n"))
}

// looksLikeHeader is true when a line is plausibly a version header for some
// release. Uses multiple heuristics because no two projects format the same.
func looksLikeHeader(lines []string, i int) bool {
	trimmed := strings.TrimSpace(lines[i])
	if len(trimmed) == 0 || len(trimmed) > 120 {
		return false
	}
	if !versionRE.MatchString(trimmed) {
		return false
	}
	// Markdown ATX header: "## 1.2.3"
	if hashHeaderRE.MatchString(trimmed) {
		return true
	}
	// Bracketed: "[1.2.3] - 2026-01-01"
	if bracketHeadRE.MatchString(trimmed) {
		return true
	}
	// Markdown Setext: next line is all = or all -
	if i+1 < len(lines) {
		next := strings.TrimSpace(lines[i+1])
		if len(next) >= 3 && (strings.Trim(next, "=") == "" || strings.Trim(next, "-") == "") {
			return true
		}
	}
	// Stand-alone version line preceded by blank.
	prevBlank := i == 0 || strings.TrimSpace(lines[i-1]) == ""
	if prevBlank {
		if versionStartRE.MatchString(trimmed) ||
			nameVersionRE.MatchString(trimmed) ||
			changesFromRE.MatchString(trimmed) {
			return true
		}
	}
	return false
}

// containsOtherVersion is true when `line` mentions a version that isn't `tag`.
func containsOtherVersion(line, tag string) bool {
	for _, m := range versionRE.FindAllStringSubmatch(line, -1) {
		v := m[1]
		if v != tag && v != "v"+tag {
			return true
		}
	}
	return false
}
