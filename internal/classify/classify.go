// Package classify parses a GitHub release body (markdown) and groups its
// bullets into semantic buckets (Added, Fixed, Breaking, etc.).
//
// The goal is to turn a wall of release notes into a glanceable summary by:
//  1. Filtering noise sections (sponsor blurbs, contributor lists, "Full Changelog" links).
//  2. Mapping known section headers to typed categories.
//  3. Compressing each bullet: extract PR numbers, drop URLs, drop author handles,
//     strip code-span backticks and markdown link syntax, collapse whitespace.
//
// Two input formats are handled:
//   - Keep-a-Changelog style: ## Added / ## Fixed / ## Changed
//   - Markdown sections w/ free-form titles ("Bug Fixes", "Performance")
//
// Conventional-commit prefixes (feat:, fix:) are NOT handled in v1; release
// notes that use them tend to also wrap them in a section header anyway.
package classify

import (
	"regexp"
	"strings"
)

type Category int

const (
	CategoryBreaking Category = iota
	CategorySecurity
	CategoryAdded
	CategoryFixed
	CategoryPerformance
	CategoryChanged
	CategoryDeprecated
	CategoryRemoved
	CategoryDocumentation
	CategoryMaintenance
	CategoryOther
)

// Label returns a short human label suitable for a section header.
func (c Category) Label() string {
	return [...]string{
		"Breaking", "Security", "Added", "Fixed",
		"Performance", "Changed", "Deprecated", "Removed",
		"Documentation", "Maintenance", "Other",
	}[c]
}

// Icon returns a Nerd-Font glyph for the category. Falls back to readable
// Unicode glyphs that render fine even on a plain terminal.
func (c Category) Icon() string {
	return [...]string{
		"", // breaking — warning triangle
		"", // security — shield
		"", // added — star
		"", // fixed — bug
		"", // performance — bolt
		"", // changed — refresh
		"", // deprecated — clock
		"", // removed — trash
		"", // docs — book
		"", // maintenance — wrench
		"", // other — gear
	}[c]
}

// Bullet is a single change entry from the release notes.
type Bullet struct {
	Scope  string   // e.g. "github", "node" (from `**(scope)**`)
	Text   string   // compressed bullet text (no URLs/handles/backticks)
	Author string   // "@stefanhaller" or "@dependabot[bot]" — first credited author
	PRs    []string // ["#10127", "#10082"]
}

// Bucket groups bullets of the same category.
type Bucket struct {
	Category Category
	Bullets  []Bullet
}

// Classified is the result of parsing a release body.
type Classified struct {
	Buckets []Bucket // ordered by importance (Breaking first, Other last)
	Junked  int      // bullets dropped because they were in a JUNK section
}

// Total returns the sum of bullets across all buckets.
func (c Classified) Total() int {
	n := 0
	for _, b := range c.Buckets {
		n += len(b.Bullets)
	}
	return n
}

var (
	sectionRE = regexp.MustCompile(`^(#{1,4})\s+(.+?)\s*$`)
	// Top-level bullet: at most one leading space. Sub-bullets (2+ spaces) are
	// skipped — they expand on parent context, and including them blows up the
	// bullet count without adding signal.
	bulletRE = regexp.MustCompile(`^[ ]{0,1}[-*•]\s+(.+)$`)
	// Sub-bullet detector: any indented bullet line. Used to filter sub-bullets
	// out of continuation text so they don't bloat the parent bullet.
	subBulletRE = regexp.MustCompile(`^\s+[-*•]\s+`)
	codeFenceRE = regexp.MustCompile("^\\s*```")

	// Leading scope: **(scope)** or (scope)
	scopeRE = regexp.MustCompile(`^(?:\*\*\(([\w./-]+)\)\*\*|\(([\w./-]+)\))\s+`)

	// PR refs: any #NNNN of >=2 digits.
	prRE = regexp.MustCompile(`#(\d{2,})`)

	// Parenthesized "(#10127 by @user)" tail. Matches only when the parens
	// contain either a PR ref or an author handle — won't eat ordinary
	// parentheticals like "(fixes godot install on macOS/APFS)".
	prTailRE = regexp.MustCompile(`\([^()]*?(?:#\d+|by\s+@[\w-]+)[^()]*?\)`)

	// Markdown link: [text](url) -> text
	mdLinkRE = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)

	// Bare URLs.
	urlRE = regexp.MustCompile(`https?://\S+`)

	// "by @username [in {#NNNN | URL}]" tails — GitHub auto-generates these
	// for every bullet. Two forms:
	//   "by @stefanhaller in #5476"
	//   "by @stefanhaller in https://github.com/owner/repo/pull/5476"
	// Also handles bot suffix "@dependabot[bot]". Match the entire tail in one
	// shot so urlRE/prRE don't peel pieces off and leave dangling " in ".
	byAuthorRE = regexp.MustCompile(`\s+by\s+@[\w-]+(?:\[\w+\])?(?:\s+in\s+(?:#\d+|https?://\S+))?`)

	// GitHub PR/issue URLs — used to extract PR refs that aren't in #NNNN form.
	prURLRE = regexp.MustCompile(`https?://github\.com/[^/\s]+/[^/\s]+/(?:pull|issues)/(\d+)`)

	// Capture the first credited author from a "by @username[bot]" mention.
	authorCapRE = regexp.MustCompile(`\bby\s+(@[\w-]+(?:\[\w+\])?)`)

	// Inline code spans.
	codeSpanRE = regexp.MustCompile("`([^`]+)`")
)

// matchCategory maps a section title to a category.
// Returns (category, matched, junk). junk=true means this section should be
// dropped entirely (sponsor blurbs, contributor lists, etc.).
func matchCategory(title string) (Category, bool, bool) {
	t := strings.ToLower(strings.TrimSpace(title))

	// Junk sections — match first so a "💚 Sponsor mise" header doesn't fall
	// through into the regular Added/Fixed buckets.
	if strings.Contains(t, "sponsor") ||
		strings.Contains(t, "contributor") ||
		strings.Contains(t, "thanks") ||
		strings.Contains(t, "full changelog") ||
		strings.Contains(t, "what's changed") { // GitHub auto-generated "What's Changed"
		return CategoryOther, true, true
	}

	switch {
	case strings.Contains(t, "breaking"):
		return CategoryBreaking, true, false
	case strings.Contains(t, "security"):
		return CategorySecurity, true, false
	case t == "added" || strings.Contains(t, "feature") || strings.Contains(t, "enhancement") || strings.HasPrefix(t, "new "):
		return CategoryAdded, true, false
	case t == "fixed" || strings.Contains(t, "bug fix") || strings.Contains(t, "fixes") || strings.HasPrefix(t, "fix"):
		return CategoryFixed, true, false
	case strings.Contains(t, "performance") || strings.Contains(t, "perf"):
		return CategoryPerformance, true, false
	case strings.Contains(t, "maintenance") || strings.Contains(t, "chore") ||
		strings.Contains(t, "miscellaneous") || strings.Contains(t, "misc") ||
		strings.Contains(t, "build") || strings.Contains(t, "ci"):
		return CategoryMaintenance, true, false
	case t == "changed" || t == "changes":
		return CategoryChanged, true, false
	case strings.Contains(t, "deprecat"):
		return CategoryDeprecated, true, false
	case t == "removed":
		return CategoryRemoved, true, false
	case strings.Contains(t, "documentation") || t == "docs" ||
		strings.Contains(t, "i18n") || strings.Contains(t, "l10n") ||
		strings.Contains(t, "translation"):
		return CategoryDocumentation, true, false
	}
	return CategoryOther, false, false
}

// Keyword regexes for per-bullet classification when section context is missing
// (e.g. plain-text changelogs like tmux's CHANGES). Order matters: stronger
// signals (Security, Breaking) checked first.
var (
	kwSecurity   = regexp.MustCompile(`(?i)\b(security|cve|vulnerab|exploit|sanitiz)\b`)
	kwBreaking   = regexp.MustCompile(`(?i)\bbreak(?:ing)?\b`)
	kwDeprecated = regexp.MustCompile(`(?i)\bdeprecat`)
	kwPerf       = regexp.MustCompile(`(?i)\b(perf|performance|speed|faster|optimi[zs]e|cache)\b`)
	kwAdded      = regexp.MustCompile(`(?i)^(add|allow|enable|introduc|implement|new|support|expose)\b`)
	kwRemoved    = regexp.MustCompile(`(?i)^(drop|delete)\b|\b(drop\s+support\s+for|remove\s+the\s+\w+\s+(api|option|flag|command|function))\b`)
	kwChanged    = regexp.MustCompile(`(?i)^(update|bump|upgrade|change|refactor|rename|move|switch|migrate)\b`)
	// "Remove" / "Fix" / etc. default to Fixed since most "Remove X from Y"
	// lines in plain-text changelogs describe corrective behavior, not API removal.
	kwFixed = regexp.MustCompile(`(?i)^(fix|repair|correct|resolve|patch|prevent|stop|avoid|remove|delete|handle|honor|skip|reject|treat)\b`)
)

// classifyByKeyword inspects a bullet's text and returns the best-guess category.
// Returns CategoryOther when nothing matches.
func classifyByKeyword(text string) Category {
	switch {
	case kwSecurity.MatchString(text):
		return CategorySecurity
	case kwBreaking.MatchString(text):
		return CategoryBreaking
	case kwDeprecated.MatchString(text):
		return CategoryDeprecated
	case kwPerf.MatchString(text):
		return CategoryPerformance
	case kwAdded.MatchString(text):
		return CategoryAdded
	case kwRemoved.MatchString(text):
		return CategoryRemoved
	case kwChanged.MatchString(text):
		return CategoryChanged
	case kwFixed.MatchString(text):
		return CategoryFixed
	}
	return CategoryOther
}

// Classify parses a release body and returns categorized bullets.
func Classify(body string) Classified {
	body = strings.ReplaceAll(body, "\r", "")
	lines := strings.Split(body, "\n")

	buckets := map[Category]*Bucket{}
	current := CategoryOther
	inJunk := false
	inCodeFence := false
	var pending strings.Builder
	hasPending := false
	junked := 0

	flush := func() {
		if !hasPending {
			return
		}
		text := strings.TrimSpace(pending.String())
		pending.Reset()
		hasPending = false
		if inJunk {
			junked++
			return
		}
		if text == "" {
			return
		}
		b := compress(text)
		if b.Text == "" {
			return
		}
		if buckets[current] == nil {
			buckets[current] = &Bucket{Category: current}
		}
		buckets[current].Bullets = append(buckets[current].Bullets, b)
	}

	for _, line := range lines {
		// Toggle code-fence state. We drop everything inside fences from bullets.
		if codeFenceRE.MatchString(line) {
			inCodeFence = !inCodeFence
			continue
		}
		if inCodeFence {
			continue
		}

		// Section header. h1/h2 resets to Other when unmatched. h3+ keeps the
		// current category so sub-headers like "### Lock identity" inherit
		// "Fixed" from their parent "## Fixed" block.
		if m := sectionRE.FindStringSubmatch(line); m != nil {
			flush()
			depth := len(m[1])
			cat, matched, junk := matchCategory(m[2])
			if matched {
				current = cat
				inJunk = junk
			} else if depth <= 2 {
				current = CategoryOther
				inJunk = false
			}
			continue
		}

		// New bullet — flush previous, start fresh.
		if m := bulletRE.FindStringSubmatch(line); m != nil {
			flush()
			pending.WriteString(m[1])
			hasPending = true
			continue
		}

		// Blank line — break the current bullet.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}

		// Indented continuation of the current bullet — but drop nested
		// sub-bullets. Folding them into the parent text bloats the bullet and
		// loses the structure anyway.
		if hasPending && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			if subBulletRE.MatchString(line) {
				continue
			}
			pending.WriteString(" ")
			pending.WriteString(trimmed)
			continue
		}

		// Otherwise: ignored content (paragraph text outside a bullet/section).
	}
	flush()

	// Per-bullet keyword rescue: any bullet that landed in "Other" gets a
	// second pass based on its text. Catches plain-text changelogs (tmux,
	// GNU style) where the surrounding section header doesn't name a category.
	if other := buckets[CategoryOther]; other != nil {
		var stayed []Bullet
		for _, b := range other.Bullets {
			cat := classifyByKeyword(b.Text)
			if cat == CategoryOther {
				stayed = append(stayed, b)
				continue
			}
			if buckets[cat] == nil {
				buckets[cat] = &Bucket{Category: cat}
			}
			buckets[cat].Bullets = append(buckets[cat].Bullets, b)
		}
		if len(stayed) == 0 {
			delete(buckets, CategoryOther)
		} else {
			buckets[CategoryOther].Bullets = stayed
		}
	}

	// Order buckets top-to-bottom within a release: Breaking first (must-read),
	// Added next (the headline good news), then Fixed/Security/Performance,
	// trailing into less-actionable categories. The newest release still lives
	// at the bottom of the overall output (see release.Filter).
	order := []Category{
		CategoryBreaking, CategoryAdded, CategoryFixed, CategorySecurity,
		CategoryPerformance, CategoryChanged, CategoryDeprecated, CategoryRemoved,
		CategoryDocumentation, CategoryMaintenance, CategoryOther,
	}
	var result Classified
	for _, c := range order {
		if b := buckets[c]; b != nil && len(b.Bullets) > 0 {
			result.Buckets = append(result.Buckets, *b)
		}
	}
	result.Junked = junked
	return result
}

// compress strips noise from a bullet's raw text and pulls out PR refs.
//
// Order matters here:
//  1. Extract PR numbers first (we'll show them as a tail; before any mangling).
//  2. Resolve markdown links (`[text](url)` → `text`) so URLs inside parens
//     simplify before paren-matching runs.
//  3. Strip bare URLs.
//  4. Strip parens that contain PR refs or author handles.
//  5. Remove any leftover inline `#NNNN` refs.
//  6. Drop `by @user` tails that weren't inside parens.
//  7. Strip code-span backticks and bold/italic markers (no styling escapes).
//  8. Collapse whitespace, trim trailing punctuation.
func compress(text string) Bullet {
	var b Bullet

	if m := scopeRE.FindStringSubmatch(text); m != nil {
		if m[1] != "" {
			b.Scope = m[1]
		} else {
			b.Scope = m[2]
		}
		text = text[len(m[0]):]
	}

	seen := map[string]bool{}
	addPR := func(num string) {
		ref := "#" + num
		if !seen[ref] {
			seen[ref] = true
			b.PRs = append(b.PRs, ref)
		}
	}

	// Resolve [text](url) → text first. The credit tails we care about often
	// embed the PR ref as a markdown link ("by @user in [#10191](url)" or
	// "([#10191](url) by @user)"); after resolution they become bare so the
	// extractors below can read them in one shot.
	text = mdLinkRE.ReplaceAllString(text, "$1")

	// Extract author + PRs from credit tails ONLY. Two recognised shapes:
	//
	//   1. linear:        "...body by @user in #NNNN" / ".. in https://.../pull/NNNN"
	//      Matched by byAuthorRE (mise CHANGELOG.md, lazygit, fzf, etc.)
	//
	//   2. parenthesized: "...body (#NNNN by @user)." or "... ([#NNNN](url) by @user)."
	//      Matched by prTailRE (GitHub Releases auto-generated format)
	//
	// Inline #NNNN refs that are NOT in a credit tail (e.g. "Previously a
	// regression in #9147 combined with ...") must stay in body text —
	// extracting them here would both bloat the credit tail AND leave a hole
	// in the body when the strip pass runs.
	if tail := byAuthorRE.FindString(text); tail != "" {
		for _, m := range prRE.FindAllStringSubmatch(tail, -1) {
			addPR(m[1])
		}
		for _, m := range prURLRE.FindAllStringSubmatch(tail, -1) {
			addPR(m[1])
		}
		if am := authorCapRE.FindStringSubmatch(tail); am != nil {
			b.Author = am[1]
		}
	}
	if tail := prTailRE.FindString(text); tail != "" {
		for _, m := range prRE.FindAllStringSubmatch(tail, -1) {
			addPR(m[1])
		}
		if b.Author == "" {
			if am := authorCapRE.FindStringSubmatch(tail); am != nil {
				b.Author = am[1]
			}
		}
	}

	text = prTailRE.ReplaceAllString(text, "")
	// byAuthorRE must run BEFORE urlRE so the "in https://...pull/NNNN" tail
	// is removed in one piece — otherwise urlRE peels off the URL and leaves
	// a dangling " in " for trim to choke on.
	text = byAuthorRE.ReplaceAllString(text, "")
	text = urlRE.ReplaceAllString(text, "")
	// NB: don't strip #NNNN from body. Inline PR refs are part of the
	// author's narrative and should render as plain text where they appear.
	// NB: don't strip `code spans` from body either. The renderer wraps each
	// one in a Background/Foreground inline-box style; stripping here would
	// lose them.
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "__", "")

	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimRight(text, " .,;:")
	// Belt-and-suspenders: if a stripping pass left a hanging " in" at the
	// end (rare, but possible if a regex misses an edge case), drop it.
	for strings.HasSuffix(text, " in") || strings.HasSuffix(text, " by") {
		text = strings.TrimSuffix(text, " in")
		text = strings.TrimSuffix(text, " by")
		text = strings.TrimRight(text, " .,;:")
	}

	b.Text = text
	return b
}
