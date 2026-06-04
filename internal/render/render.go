// Package render prints flat-mode changelog output using lipgloss for layout
// and glamour for markdown rendering of release bodies.
package render

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"github.com/marcusDenslow/brew-changelog/internal/changelog"
	"github.com/marcusDenslow/brew-changelog/internal/classify"
	"github.com/marcusDenslow/brew-changelog/internal/release"
)

// Catppuccin Mocha palette — the canonical dark variant most people mean
// when they say "Catppuccin." Reference: https://catppuccin.com/palette
const (
	cpRed      = "#f38ba8"
	cpMaroon   = "#eba0ac"
	cpPeach    = "#fab387"
	cpYellow   = "#f9e2af"
	cpGreen    = "#a6e3a1"
	cpTeal     = "#94e2d5"
	cpSky      = "#89dceb"
	cpBlue     = "#89b4fa"
	cpLavender = "#b4befe"
	cpMauve    = "#cba6f7"
	cpText     = "#cdd6f4"
	cpSubtext0 = "#a6adc8"
	cpOverlay2 = "#9399b2"
	cpOverlay1 = "#7f849c"
	cpOverlay0 = "#6c7086"
	cpSurface2 = "#585b70"
	cpSurface1 = "#45475a"
	cpSurface0 = "#313244"
	cpBase     = "#1e1e2e"
	cpMantle   = "#181825"
	cpCrust    = "#11111b"
)

var (
	colorBrew    = lipgloss.Color(cpPeach)    // brew accent
	colorVersion = lipgloss.Color(cpBlue)     // version range
	colorTag     = lipgloss.Color(cpPeach)    // release tag badges
	colorDim     = lipgloss.Color(cpOverlay1) // muted

	pkgNameStyle = lipgloss.NewStyle().
			Foreground(colorBrew).
			Bold(true)

	versionStyle = lipgloss.NewStyle().
			Foreground(colorVersion)

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cpSurface2))

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Release tag rendered as a solid-bg badge for prominence.
	tagStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(cpSurface1)).
			Foreground(colorTag).
			Bold(true).
			Padding(0, 1)

	dateStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true)

	relNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(cpText)).
			Bold(true)
)

var mdRenderer *glamour.TermRenderer

func init() {
	width := termWidth()
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width-4), // leave room for indent
		glamour.WithPreservedNewLines(),
	)
	if err == nil {
		mdRenderer = r
	}
}

func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		return w
	}
	return 100
}

// Header prints the package banner as a thin rounded box containing the
// package name and version range. Width-aware so the box scales with the
// terminal but never exceeds a comfortable reading width.
func Header(pkg, installed, latest string) {
	w := termWidth()
	if w > 100 {
		w = 100
	}
	innerWidth := w - 4 // 2 padding chars + 2 border chars

	content := fmt.Sprintf("%s   %s",
		pkgNameStyle.Render(pkg),
		versionStyle.Render(fmt.Sprintf("%s → %s", installed, latest)),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cpSurface2)).
		Padding(0, 1).
		Width(innerWidth).
		Render(content)

	fmt.Println()
	for _, line := range strings.Split(box, "\n") {
		fmt.Println("  " + line)
	}
}

// Skip prints a dimmed one-line reason a package was bypassed.
func Skip(pkg, reason string) {
	fmt.Println(dimStyle.Render(fmt.Sprintf("skip %s (%s)", pkg, reason)))
}

func NonGitHub(homepage string) {
	fmt.Println("  " + dimStyle.Render(
		fmt.Sprintf("no GitHub source found (homepage: %s) — manual check", homepage),
	))
}

func FetchError(owner, repo string) {
	fmt.Println("  " + dimStyle.Render(
		fmt.Sprintf("could not fetch releases for %s/%s", owner, repo),
	))
}

func NoReleases(owner, repo string) {
	fmt.Println("  " + dimStyle.Render(
		fmt.Sprintf("no releases published at %s/%s", owner, repo),
	))
}

func NoMatching(owner, repo, installed, latest string) {
	fmt.Println("  " + dimStyle.Render(
		fmt.Sprintf("no matching tags between %s and %s — browse: https://github.com/%s/%s/releases",
			installed, latest, owner, repo),
	))
}

// Per-category color from the Catppuccin Mocha palette.
var categoryColor = map[classify.Category]string{
	classify.CategoryBreaking:      cpRed,
	classify.CategorySecurity:      cpMaroon,
	classify.CategoryAdded:         cpGreen,
	classify.CategoryFixed:         cpYellow,
	classify.CategoryPerformance:   cpSky,
	classify.CategoryChanged:       cpMauve,
	classify.CategoryDeprecated:    cpPeach,
	classify.CategoryRemoved:       cpOverlay1,
	classify.CategoryDocumentation: cpOverlay0,
	classify.CategoryMaintenance:   cpOverlay1,
	classify.CategoryOther:         cpSubtext0,
}

// FlatRelease prints one release inside a rounded box, dynamically sized to
// the terminal width. Falls back to plain extracted text (from a CHANGES file)
// or glamour-rendered raw markdown when no buckets are detected.
func FlatRelease(r release.Release, owner, repo string) {
	date := r.PublishedAt
	if i := strings.IndexByte(date, 'T'); i >= 0 {
		date = date[:i]
	}
	name := r.Name
	if name == "" {
		name = r.TagName
	}
	// Many projects ship "v1.2.3: subtitle" as the release name. Drop the tag
	// prefix so we don't display it twice next to ▸ <tag>.
	for _, prefix := range []string{r.TagName + ": ", r.TagName + " - ", r.TagName} {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimSpace(strings.TrimPrefix(name, prefix))
			break
		}
	}

	arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(cpPeach)).Bold(true)

	var content strings.Builder
	content.WriteString(fmt.Sprintf("%s %s  %s",
		arrowStyle.Render("▸"),
		tagStyle.Render(r.TagName),
		dateStyle.Render(date),
	))
	if name != "" {
		content.WriteString("  " + relNameStyle.Render(name))
	}

	body := strings.TrimSpace(r.Body)
	result := classify.Classify(body)
	source := ""

	// If classifier got nothing, try fetching a CHANGES file from the repo.
	if result.Total() == 0 {
		if cl, err := changelog.Fetch(owner, repo, r.TagName); err == nil && cl != nil {
			section := changelog.ExtractSection(cl.Content, r.TagName)
			if section != "" {
				result = classify.Classify(section)
				source = cl.Path
				// Still nothing structured — show extracted text verbatim inside the box.
				if result.Total() == 0 {
					content.WriteString("\n\n")
					content.WriteString(dimStyle.Render("(from " + cl.Path + ")"))
					content.WriteString("\n")
					content.WriteString(section)
					printInBox(content.String())
					return
				}
			}
		}
	}

	// Still no buckets — fall back to glamour-rendered raw body inside the box.
	if result.Total() == 0 {
		if body != "" {
			rendered := body
			if mdRenderer != nil {
				if out, err := mdRenderer.Render(body); err == nil {
					rendered = strings.TrimRight(out, "\n")
				}
			}
			content.WriteString("\n")
			content.WriteString(rendered)
		}
		printInBox(content.String())
		return
	}

	// Inner width that bucketString will wrap against — must match printInBox.
	innerWidth := boxInnerWidth()

	// If there's prose before the first ## section (e.g. lazygit v0.62.0's
	// "The big change in this release is...", including the breaking-change
	// warning), surface it as wrapped intro paragraphs inside the box.
	// wrapMultiParagraph preserves blank-line paragraph breaks.
	if lead := extractLead(body); lead != "" {
		content.WriteString("\n\n")
		content.WriteString(wrapMultiParagraph(lead, "", innerWidth))
	}

	for _, bucket := range result.Buckets {
		content.WriteString("\n\n")
		content.WriteString(bucketString(bucket, innerWidth, owner, repo))
	}

	if source != "" {
		content.WriteString("\n\n")
		content.WriteString(dimStyle.Render("(notes fetched from " + source + ")"))
	}

	printInBox(content.String())
}

var (
	leadHTMLCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	leadMdLinkRE      = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	leadCodeSpanRE    = regexp.MustCompile("`([^`]+)`")
	// Strip ATX-style markdown headers (## Title, ### Subtitle) from lead prose.
	// They appear as sectioning, not text the user wants reproduced.
	leadHeaderRE = regexp.MustCompile(`(?m)^#{1,6}\s+.*$`)
	// Defensive: drop bullet lines that snuck into lead (rare — happens when
	// body opens with a section header and no intro prose).
	leadBulletRE = regexp.MustCompile(`(?m)^\s*[-*•]\s+.*$`)
)

// extractLead returns the prose that appears in a release body BEFORE the
// first markdown section header (## or ###). HTML comments are dropped,
// markdown links collapsed to their text, code-span backticks removed.
// Empty when the body has no lead prose worth showing.
func extractLead(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}

	idx := -1
	for _, marker := range []string{"\n## ", "\n### "} {
		if i := strings.Index(body, marker); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	if idx < 0 {
		return ""
	}

	lead := strings.TrimSpace(body[:idx])
	lead = leadHTMLCommentRE.ReplaceAllString(lead, "")
	lead = leadHeaderRE.ReplaceAllString(lead, "")
	lead = leadBulletRE.ReplaceAllString(lead, "")
	lead = leadMdLinkRE.ReplaceAllString(lead, "$1")
	lead = leadCodeSpanRE.ReplaceAllString(lead, "$1")
	lead = strings.ReplaceAll(lead, "**", "")
	lead = strings.ReplaceAll(lead, "__", "")

	// Drop hand-off lines like "The complete list of changes follows:" and any
	// surrounding blank lines. Skips empties first so a stripped HTML comment
	// (now empty) doesn't hide the handoff line above it.
	lines := strings.Split(lead, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(strings.ToLower(lines[len(lines)-1]))
		if last == "" {
			lines = lines[:len(lines)-1]
			continue
		}
		if strings.HasPrefix(last, "the complete list") ||
			strings.HasPrefix(last, "see below") ||
			strings.HasPrefix(last, "details below") {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// boxInnerWidth returns the width budget inside a release box (subtract border
// and padding from the box outer width). Shared by printInBox and bucketString
// so the two stay in sync.
func boxInnerWidth() int {
	w := termWidth() - 4
	if w > 100 {
		w = 100
	}
	if w < 40 {
		w = 40
	}
	return w - 4 // -2 border, -2 padding
}

// printInBox wraps content in a rounded lipgloss box. We do NOT set Width on
// the lipgloss style because doing so triggers an internal re-wrap that loses
// our continuation indents on already-wrapped lines. Instead, bullets are
// pre-wrapped in bucketString to fit boxInnerWidth(), and we pad each content
// line to that width manually so the box renders at a uniform size.
func printInBox(content string) {
	inner := boxInnerWidth()
	var padded []string
	for _, line := range strings.Split(content, "\n") {
		w := lipgloss.Width(line)
		if w < inner {
			line += strings.Repeat(" ", inner-w)
		}
		padded = append(padded, line)
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cpSurface1)).
		Padding(0, 1).
		Render(strings.Join(padded, "\n"))

	fmt.Println()
	for _, line := range strings.Split(box, "\n") {
		fmt.Println("  " + line)
	}
}

// bucketCap returns how many bullets to show before truncating. High-signal
// buckets (Breaking, Security, Added, Fixed, Performance) are uncapped — long
// output is preferred to dropping real signal. Maintenance is capped tight
// because it's mostly dependabot bumps. Documentation/Other are mid-tight.
func bucketCap(cat classify.Category) int {
	switch cat {
	case classify.CategoryBreaking, classify.CategorySecurity,
		classify.CategoryAdded, classify.CategoryFixed, classify.CategoryPerformance:
		return 0 // uncapped
	case classify.CategoryChanged, classify.CategoryDeprecated, classify.CategoryRemoved:
		return 10
	case classify.CategoryMaintenance:
		return 3
	default:
		return 5
	}
}

// wrapMultiParagraph word-wraps text while preserving blank-line paragraph
// breaks. Each paragraph is wrapped independently, then joined with "\n\n".
// Used for the lead prose where markdown has authored paragraph structure
// that should survive rendering.
var paragraphSplitRE = regexp.MustCompile(`\n\s*\n`)

// inlinePRRefRE matches an inline PR reference (#NNNN with at least 2 digits)
// only when it is at the start of the body or follows a non-word char. The
// non-word-prefix guard rejects compound IDs like "usage#649" where the
// reference belongs to a different repo and the hyperlink would point at the
// wrong owner/repo.
var inlinePRRefRE = regexp.MustCompile(`(?:^|[^\w])#\d{2,}`)

// inlineCodeRE matches a single-backtick code span. Classify intentionally
// leaves backticks in body text so we can box them visually here.
var inlineCodeRE = regexp.MustCompile("`([^`]+)`")

// inlineCodeStyle renders code spans as a barely-there highlight. The bg
// (#272839) sits halfway between cpBase (#1e1e2e) and cpSurface0 (#313244)
// — close enough to the box base that the chip reads as a soft tint, not a
// callout. No padding to avoid the trailing-bg leak lipgloss propagates
// when the outer box fills width after a chip.
var inlineCodeStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("#272839")).
	Foreground(lipgloss.Color(cpText))

func wrapMultiParagraph(text, indent string, width int) string {
	parts := paragraphSplitRE.Split(text, -1)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Collapse intra-paragraph newlines (markdown soft wrap) — the wrap
		// function will reflow to the target width.
		p = strings.Join(strings.Fields(p), " ")
		out = append(out, wrapToWidth(p, indent, width))
	}
	return strings.Join(out, "\n\n")
}

// wrapToWidth word-wraps text to fit `width` visible columns, prefixing every
// continuation line with `indent`. Uses lipgloss.Width so ANSI escapes inside
// tokens (e.g. styled PR refs) don't break wrapping decisions. Whitespace-
// delimited tokens are kept intact — render PR/scope as self-contained styled
// tokens so a wrap point never lands inside an ANSI sequence.
func wrapToWidth(text, indent string, width int) string {
	if width <= 0 || lipgloss.Width(text) <= width {
		return text
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var lines []string
	var cur strings.Builder
	cur.WriteString(words[0])
	curWidth := lipgloss.Width(words[0])
	for _, w := range words[1:] {
		ww := lipgloss.Width(w)
		if curWidth+1+ww > width {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
			curWidth = ww
		} else {
			cur.WriteString(" ")
			cur.WriteString(w)
			curWidth += 1 + ww
		}
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n"+indent)
}

// hyperlink wraps `text` in an OSC 8 terminal hyperlink pointing at `url`.
// Modern terminals (Ghostty, iTerm2, WezTerm, kitty) render it as clickable.
// Terminals without OSC 8 support fall through to displaying the text only.
func hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// bucketString builds the rendered output for one category bucket as a string.
// `boxInner` is the box's inner content width; bullets wrap to fit.
// `owner`/`repo` are used to build OSC 8 hyperlinks for PR refs.
func bucketString(bucket classify.Bucket, boxInner int, owner, repo string) string {
	c := lipgloss.Color(categoryColor[bucket.Category])

	badge := lipgloss.NewStyle().
		Background(c).
		Foreground(lipgloss.Color(cpCrust)).
		Bold(true).
		Padding(0, 1).
		Render(bucket.Category.Label())
	count := dimStyle.Render(fmt.Sprintf(" %d", len(bucket.Bullets)))

	scopeStyle := lipgloss.NewStyle().Foreground(c).Italic(true)
	prStyle := lipgloss.NewStyle().Foreground(colorDim).Underline(true)
	authorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(cpLavender)).Underline(true)
	connectorStyle := dimStyle
	bulletMark := dimStyle.Render("-")

	var sb strings.Builder
	sb.WriteString(badge + count)

	cap := bucketCap(bucket.Category)
	shown := bucket.Bullets
	hidden := 0
	if cap > 0 && len(shown) > cap {
		hidden = len(shown) - cap
		shown = shown[:cap]
	}

	for _, b := range shown {
		// "  - " prefix: 2-space indent, dash, space. Continuation indents
		// 4 chars deep so wrap aligns under text (not the dash).
		sb.WriteString("\n  ")
		sb.WriteString(bulletMark)
		sb.WriteString(" ")
		contIndent := "    "
		if b.Scope != "" {
			sb.WriteString(scopeStyle.Render(b.Scope))
			sb.WriteString("  ")
			contIndent += strings.Repeat(" ", lipgloss.Width(b.Scope)+2)
		}

		// Build the credit tail: " by @author in #NNNN [#NNNN ...]"
		// "by"/"in" are dimmed, author is in Lavender, PRs are OSC 8 hyperlinks.
		// Render each piece as its own self-contained styled token so word-wrap
		// never slices through an ANSI/OSC escape.
		text := b.Text

		// Inline code spans (`git::`, `task_config.includes`, etc.) become
		// boxed chips. Run BEFORE the PR pass so a `#NNNN` written inside
		// backticks (rare but possible) isn't double-styled.
		text = inlineCodeRE.ReplaceAllStringFunc(text, func(match string) string {
			return inlineCodeStyle.Render(match[1 : len(match)-1])
		})

		// Inline PR refs (e.g. "regression in #9147 combined ...") also get
		// the underlined-dim style + OSC 8 hyperlink. The `[^\w]` lookbehind
		// proxy excludes compound IDs like `usage#649` whose owner/repo
		// differs from the current package — we'd otherwise build a wrong URL.
		if owner != "" && repo != "" {
			text = inlinePRRefRE.ReplaceAllStringFunc(text, func(match string) string {
				hashIdx := strings.IndexByte(match, '#')
				prefix := match[:hashIdx]
				prRef := match[hashIdx:]
				url := fmt.Sprintf("https://github.com/%s/%s/pull/%s", owner, repo, prRef[1:])
				return prefix + hyperlink(url, prStyle.Render(prRef))
			})
		}

		var creditPieces []string
		if b.Author != "" {
			styledAuthor := authorStyle.Render(b.Author)
			// Strip leading @ and trailing [bot] for the URL. Display text
			// keeps the original form ("@jdx", "@dependabot[bot]"); the link
			// resolves to the GitHub profile URL.
			name := strings.TrimSuffix(strings.TrimPrefix(b.Author, "@"), "[bot]")
			if name != "" {
				styledAuthor = hyperlink("https://github.com/"+name, styledAuthor)
			}
			creditPieces = append(creditPieces,
				connectorStyle.Render("by"),
				styledAuthor,
			)
		}
		if len(b.PRs) > 0 {
			if b.Author != "" {
				creditPieces = append(creditPieces, connectorStyle.Render("in"))
			}
			for _, pr := range b.PRs {
				styled := prStyle.Render(pr)
				if owner != "" && repo != "" {
					url := fmt.Sprintf("https://github.com/%s/%s/pull/%s",
						owner, repo, strings.TrimPrefix(pr, "#"))
					styled = hyperlink(url, styled)
				}
				creditPieces = append(creditPieces, styled)
			}
		}
		if len(creditPieces) > 0 {
			text += "  " + strings.Join(creditPieces, " ")
		}

		textWidth := boxInner - lipgloss.Width(contIndent)
		if textWidth < 20 {
			textWidth = 20
		}
		sb.WriteString(wrapToWidth(text, contIndent, textWidth))
	}

	if hidden > 0 {
		var refs []string
		for _, b := range bucket.Bullets[len(shown):] {
			for _, pr := range b.PRs {
				if owner != "" && repo != "" {
					url := fmt.Sprintf("https://github.com/%s/%s/pull/%s",
						owner, repo, strings.TrimPrefix(pr, "#"))
					refs = append(refs, hyperlink(url, prStyle.Render(pr)))
				} else {
					refs = append(refs, prStyle.Render(pr))
				}
			}
		}
		hint := dimStyle.Render(fmt.Sprintf("+%d more", hidden))
		if len(refs) > 0 {
			hint += dimStyle.Render(": ") + strings.Join(refs, " ")
		}
		sb.WriteString("\n  ")
		sb.WriteString(wrapToWidth(hint, "  ", boxInner-2))
	}

	return sb.String()
}
