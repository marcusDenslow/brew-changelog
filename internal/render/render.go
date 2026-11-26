// Package render prints flat-mode changelog output using lipgloss for layout
// and glamour for markdown rendering of release bodies.
package render

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"github.com/marcusDenslow/brew-changelog/internal/changelog"
	"github.com/marcusDenslow/brew-changelog/internal/classify"
	"github.com/marcusDenslow/brew-changelog/internal/release"
	"github.com/marcusDenslow/brew-changelog/internal/termimg"
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
			Foreground(colorVersion).
			Underline(true)

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
	content = brightenPlain(content)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(cpSurface2)).
		Foreground(lipgloss.Color("#ffffff")).
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

// imageRef is an image discovered in a release body.
type imageRef struct {
	URL  string
	Alt  string
	Lead bool // sits in the first section → drawn at the top of the box body
	Rows int  // measured kitty render height in terminal rows (0 until measured)
	Draw bool // true once measured and drawable inline; false => link fallback
}

var (
	// htmlImgRE matches a GitHub-flavored <img ... src="URL" ...> tag (the form
	// the GitHub UI emits when you paste a screenshot). Captures the src URL.
	htmlImgRE = regexp.MustCompile(`(?is)<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["'][^>]*>`)
	// htmlAltRE pulls the alt="" attribute out of a matched <img> tag.
	htmlAltRE = regexp.MustCompile(`(?is)\balt\s*=\s*["']([^"']*)["']`)
	// mdImgRE matches a markdown image: ![alt](url "title"). Captures alt + url.
	mdImgRE = regexp.MustCompile(`!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?[^)]*\)`)
	// htmlWrapRE strips <picture>/<source> wrapper tags left behind once their
	// inner <img> is extracted, so they don't print as raw HTML.
	htmlWrapRE = regexp.MustCompile(`(?is)</?(?:picture|source)\b[^>]*>`)
)

// extractImages removes image markup (HTML <img>, markdown ![]()) from body and
// returns the cleaned text plus the images found. Only http(s) URLs are kept;
// data: URIs and relative paths are dropped (we can't fetch them). Each image is
// flagged Hero when it appears before the first ## section — a top-of-body
// screenshot — so the caller can place it under the release header instead of at
// the bottom. Stripping up front also keeps the classifier, lead extractor, and
// glamour fallback from ever printing raw <img> tags.
func extractImages(body string) (string, []imageRef) {
	leadEnd := leadSectionEnd(body)
	lead := func(start int) bool { return start < leadEnd }

	var refs []imageRef
	for _, m := range htmlImgRE.FindAllStringSubmatchIndex(body, -1) {
		url := body[m[2]:m[3]]
		if !isHTTP(url) {
			continue
		}
		alt := ""
		if a := htmlAltRE.FindStringSubmatch(body[m[0]:m[1]]); a != nil {
			alt = a[1]
		}
		refs = append(refs, imageRef{URL: url, Alt: alt, Lead: lead(m[0])})
	}
	for _, m := range mdImgRE.FindAllStringSubmatchIndex(body, -1) {
		url := body[m[4]:m[5]]
		if !isHTTP(url) {
			continue
		}
		refs = append(refs, imageRef{URL: url, Alt: body[m[2]:m[3]], Lead: lead(m[0])})
	}

	body = htmlImgRE.ReplaceAllString(body, "")
	body = mdImgRE.ReplaceAllString(body, "")
	body = htmlWrapRE.ReplaceAllString(body, "")
	return body, refs
}

// leadSectionEnd returns the byte offset where the body's first section ends —
// the start of the second ## / ### header — or len(body) if there's only one
// section (or none). Images before this offset belong to the lead section, which
// the renderer surfaces at the top of the box, so they're drawn there; images
// after it are placed lower (before the change buckets).
func leadSectionEnd(body string) int {
	end := len(body)
	for _, mk := range []string{"\n## ", "\n### "} {
		if i := strings.Index(body, mk); i >= 0 && i < end {
			end = i
		}
	}
	return end
}

func isHTTP(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// measureImages decides, per image, whether it can be drawn inline (kitty-capable
// terminal, fetch+decode succeeds) and at what pixel-accurate row height. Images
// that can't be drawn keep Draw=false and fall back to a hyperlink.
func measureImages(images []imageRef) {
	if !termimg.Capable() {
		return
	}
	inner := boxInnerWidth()
	for i := range images {
		if rows, err := termimg.Measure(images[i].URL, inner); err == nil {
			images[i].Rows = rows
			images[i].Draw = true
		}
	}
}

// imgSentinelPrefix marks a content line as a placeholder for the Nth drawable
// image. FlatRelease inserts these at the image's mapped position while building
// the content string; finishBox turns them back into inline image draws. The
// NUL bytes guarantee the marker never collides with real release text.
const imgSentinelPrefix = "\x00brewimg\x00"

func sentinelLine(i int) string { return imgSentinelPrefix + strconv.Itoa(i) }

func parseSentinel(line string) (int, bool) {
	if !strings.HasPrefix(line, imgSentinelPrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(line[len(imgSentinelPrefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

// writeSentinels appends one image-placeholder line per index to b. Each is put
// on its own line so it becomes a standalone content line.
func writeSentinels(b *strings.Builder, idxs []int) {
	for _, i := range idxs {
		b.WriteByte('\n')
		b.WriteString(sentinelLine(i))
	}
}

// finishBox renders a release box. When there are drawable images, the content
// (which carries image-placeholder sentinels) is split into text/image segments
// and drawn with a hand-built box so images sit inline at their real positions.
// Otherwise it falls back to the lipgloss box. Non-drawable images degrade to
// links printed below the box.
func finishBox(content string, drawables, links []imageRef) {
	if len(drawables) == 0 {
		printPlainBox(content)
		renderLinks(links)
		return
	}
	printSegmentedBox(splitSegments(content, drawables))
	renderLinks(links)
}

// printPlainBox is the image-free path: a lipgloss rounded box with a leading
// blank line for inter-release spacing.
func printPlainBox(content string) {
	fmt.Println()
	for _, line := range buildBoxLines(strings.Split(content, "\n")) {
		fmt.Println("  " + line)
	}
}

// boxSegment is one piece of boxed content: either text rows or a single inline
// image (exactly one of the two is set).
type boxSegment struct {
	lines []string
	image *imageRef
}

// splitSegments turns a content string (with image-placeholder sentinels) into
// ordered text/image segments.
func splitSegments(content string, drawables []imageRef) []boxSegment {
	var segs []boxSegment
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			segs = append(segs, boxSegment{lines: cur})
			cur = nil
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if i, ok := parseSentinel(line); ok && i >= 0 && i < len(drawables) {
			flush()
			segs = append(segs, boxSegment{image: &drawables[i]})
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return segs
}

// boxBorderStyle matches the lipgloss box border so the hand-built and lipgloss
// boxes look identical.
var boxBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(cpSurface1))

// printSegmentedBox draws a rounded box by hand (so we control every row),
// rendering text rows normally and image segments as inline kitty graphics.
// Because everything is printed strictly top-to-bottom — the image is drawn at
// the cursor with C=1, then the cursor is jumped past its cell region to close
// the right border — there are no cursor moves into already-printed (and possibly
// scrolled) rows, so it stays correct as the terminal scrolls.
func printSegmentedBox(segments []boxSegment) {
	inner := boxInnerWidth()
	bar := boxBorderStyle.Render("│")

	fmt.Println()
	fmt.Println("  " + boxBorderStyle.Render("╭"+strings.Repeat("─", inner+2)+"╮"))
	for _, seg := range segments {
		if seg.image != nil {
			printImageRows(*seg.image, inner, bar)
			continue
		}
		for _, line := range seg.lines {
			line = brightenPlain(line)
			pad := inner - lipgloss.Width(line)
			if pad < 0 {
				pad = 0
			}
			fmt.Println("  " + bar + " " + line + strings.Repeat(" ", pad) + " " + bar)
		}
	}
	fmt.Println("  " + boxBorderStyle.Render("╰"+strings.Repeat("─", inner+2)+"╯"))
}

// printImageRows draws one image inside the hand-built box: a leading blank row
// for separation, then im.Rows rows where the image occupies the inner cells. On
// row 0 the image is drawn (kitty C=1 leaves the cursor put) and the cursor is
// advanced past the inner region to close the border; the remaining rows just
// draw the borders, leaving the image's cells untouched (so nothing occludes it).
func printImageRows(im imageRef, inner int, bar string) {
	blank := "  " + bar + " " + strings.Repeat(" ", inner) + " " + bar
	fmt.Println(blank)

	fmt.Print("  " + bar + " ")
	if err := termimg.Draw(os.Stdout, im.URL, inner); err != nil {
		// Draw failed mid-row: finish the row as blank; the link fallback below
		// the box still carries the reference.
		fmt.Printf("\x1b[%dC ", inner)
		fmt.Println(bar)
		return
	}
	fmt.Printf("\x1b[%dC ", inner) // jump past the image cells to the right pad
	fmt.Println(bar)
	for range im.Rows - 1 {
		fmt.Print("  " + bar + " ")
		fmt.Printf("\x1b[%dC ", inner)
		fmt.Println(bar)
	}
}

// renderLinks prints dim OSC 8 hyperlinks for images that can't be drawn inline
// (non-kitty terminal, tmux, or a fetch/decode failure) so the reference is never
// silently dropped.
func renderLinks(imgs []imageRef) {
	for _, im := range imgs {
		fmt.Println("  " + dimStyle.Render(hyperlink(im.URL, "🖼 "+imageLabel(im.Alt)+" ↗")))
	}
}

func imageLabel(alt string) string {
	if alt != "" {
		return alt
	}
	return "image"
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

	// Wrap the tag badge in an OSC 8 hyperlink to the GitHub release page.
	// No style change — the cursor-hover cue on modern terminals (ghostty,
	// iterm2, wezterm, kitty) signals interactivity; bold tag look is kept.
	tag := tagStyle.Render(r.TagName)
	if owner != "" && repo != "" && r.TagName != "" {
		tag = hyperlink(
			fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", owner, repo, r.TagName),
			tag,
		)
	}

	var content strings.Builder
	content.WriteString(fmt.Sprintf("%s %s  %s",
		arrowStyle.Render("▸"),
		tag,
		dateStyle.Render(date),
	))
	if name != "" {
		content.WriteString("  " + relNameStyle.Render(name))
	}

	body := strings.TrimSpace(r.Body)
	// Pull images out before any text processing so raw <img> tags never leak into
	// the box. measureImages flags which can be drawn inline (kitty) vs. linked.
	body, images := extractImages(body)
	measureImages(images)

	// Partition: drawable images become inline sentinels placed by region — lead
	// images at the top of the body, the rest just before the change buckets — so
	// they sit where they appeared in the source. Non-drawable images fall back to
	// links printed below the box.
	var drawables, links []imageRef
	var leadIdx, bodyIdx []int
	for _, im := range images {
		if !im.Draw {
			links = append(links, im)
			continue
		}
		i := len(drawables)
		drawables = append(drawables, im)
		if im.Lead {
			leadIdx = append(leadIdx, i)
		} else {
			bodyIdx = append(bodyIdx, i)
		}
	}
	writeSentinels(&content, leadIdx) // directly under the header line

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
					writeSentinels(&content, bodyIdx)
					finishBox(content.String(), drawables, links)
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
		writeSentinels(&content, bodyIdx)
		finishBox(content.String(), drawables, links)
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

	writeSentinels(&content, bodyIdx) // between the lead prose and the buckets

	for _, bucket := range result.Buckets {
		content.WriteString("\n\n")
		content.WriteString(bucketString(bucket, innerWidth, owner, repo))
	}

	if source != "" {
		content.WriteString("\n\n")
		content.WriteString(dimStyle.Render("(notes fetched from " + source + ")"))
	}

	finishBox(content.String(), drawables, links)
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
// buildBoxLines renders content lines inside a rounded box and returns the box's
// lines (without the 2-space left margin the caller prepends when printing).
func buildBoxLines(lines []string) []string {
	inner := boxInnerWidth()
	content := brightenPlain(strings.Join(lines, "\n"))
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
		Foreground(lipgloss.Color("#ffffff")).
		Padding(0, 1).
		Render(strings.Join(padded, "\n"))
	return strings.Split(box, "\n")
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

// inlineSHARefRE matches a 40-char lowercase-hex commit SHA at a word
// boundary. Renderer shortens to the first 7 chars (GitHub default) and
// wraps in an OSC 8 hyperlink to the commit page. Pattern is strict to
// avoid catching long hex IDs from other contexts (e.g. API keys, hashes
// inside URLs).
var inlineSHARefRE = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

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

// Force cpText foreground on body prose so it reads bright regardless of the
// terminal's default fg. Re-applied after every embedded reset so nested
// styled tokens (PR refs, code chips) don't drop the color downstream.
const whiteFg = "\x1b[38;2;255;255;255m"

// Lipgloss emits `\x1b[m` (empty params) for reset; glamour or other writers
// may emit `\x1b[0m`. Match both so plain text following any styled token
// (chip, PR ref, SHA, scope, badge, author) gets the box's default foreground
// re-applied instead of falling back to the terminal's native fg color.
var resetRE = regexp.MustCompile(`\x1b\[0?m`)

func brightenPlain(s string) string {
	return resetRE.ReplaceAllStringFunc(s, func(reset string) string {
		return reset + whiteFg
	})
}

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
	prStyle := lipgloss.NewStyle().Foreground(colorVersion).Underline(true)
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

		// Commit SHAs: shorten to 7 chars + OSC 8 hyperlink to the commit
		// page. Matches existing PR-ref styling for visual consistency.
		if owner != "" && repo != "" {
			text = inlineSHARefRE.ReplaceAllStringFunc(text, func(sha string) string {
				url := fmt.Sprintf("https://github.com/%s/%s/commit/%s", owner, repo, sha)
				return hyperlink(url, prStyle.Render(sha[:7]))
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
