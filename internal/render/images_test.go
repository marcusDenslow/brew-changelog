package render

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/marcusDenslow/brew-changelog/internal/release"
)

func TestExtractImages(t *testing.T) {
	const ghBody = `<img width="2784" height="1494" alt="issue-view-monas-cafe-with-frame" src="https://github.com/user-attachments/assets/fd2c6a89-0ac2-472d-a1df-b4e6124f270b" />

This release brings GitHub's advanced issue features.`

	clean, imgs := extractImages(ghBody)

	if len(imgs) != 1 {
		t.Fatalf("want 1 image, got %d: %+v", len(imgs), imgs)
	}
	if got := imgs[0].URL; got != "https://github.com/user-attachments/assets/fd2c6a89-0ac2-472d-a1df-b4e6124f270b" {
		t.Errorf("url = %q", got)
	}
	if got := imgs[0].Alt; got != "issue-view-monas-cafe-with-frame" {
		t.Errorf("alt = %q", got)
	}
	if containsImg(clean) {
		t.Errorf("clean body still has <img: %q", clean)
	}
}

func TestExtractImagesMarkdownAndSkipsRelative(t *testing.T) {
	body := "intro ![a screenshot](https://example.com/a.png) mid ![local](./rel.png) end"
	clean, imgs := extractImages(body)

	if len(imgs) != 1 {
		t.Fatalf("want 1 http image (relative dropped), got %d: %+v", len(imgs), imgs)
	}
	if imgs[0].URL != "https://example.com/a.png" || imgs[0].Alt != "a screenshot" {
		t.Errorf("got %+v", imgs[0])
	}
	if containsImg(clean) {
		t.Errorf("markdown image markup survived: %q", clean)
	}
}

func TestExtractImagesNone(t *testing.T) {
	body := "- fixed a bug in #1234\n- added a feature"
	clean, imgs := extractImages(body)
	if len(imgs) != 0 {
		t.Fatalf("want 0 images, got %d", len(imgs))
	}
	if clean != body {
		t.Errorf("body mutated: %q", clean)
	}
}

func containsImg(s string) bool {
	for i := 0; i+4 <= len(s); i++ {
		if s[i:i+4] == "<img" || s[i:i+2] == "![" {
			return true
		}
	}
	return false
}

func TestFlatReleaseNoImgLeak(t *testing.T) {
	r := release.Release{
		TagName:     "v2.94.0",
		Name:        "GitHub CLI 2.94.0",
		PublishedAt: "2026-06-10T00:00:00Z",
		Body: `<img width="2784" height="1494" alt="issue-view-monas-cafe-with-frame" src="https://github.com/user-attachments/assets/fd2c6a89-0ac2-472d-a1df-b4e6124f270b" />

This release brings GitHub's advanced issue features.

## What's Changed
- Added sub-issues support`,
	}

	old := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw
	FlatRelease(r, "cli", "cli")
	pw.Close()
	os.Stdout = old
	buf, _ := io.ReadAll(pr)
	out := string(buf)

	if strings.Contains(out, "<img") {
		t.Errorf("raw <img> leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "🖼") {
		t.Errorf("expected image fallback marker 🖼, output:\n%s", out)
	}
	if !strings.Contains(out, "user-attachments/assets/fd2c6a89") {
		t.Errorf("expected image URL in fallback link, output:\n%s", out)
	}
}

func TestLeadSectionEnd(t *testing.T) {
	// Single section (or none): the whole body is the lead.
	if got := leadSectionEnd("## Added\n- x"); got != len("## Added\n- x") {
		t.Errorf("single section: got %d, want len", got)
	}
	if got := leadSectionEnd("just prose, no sections"); got != len("just prose, no sections") {
		t.Errorf("no header: got %d, want len", got)
	}
	// Two sections: lead ends at the start of the second header.
	body := "## One\nintro\n\n## Two\nmore"
	if got := leadSectionEnd(body); got != strings.Index(body, "\n## Two") {
		t.Errorf("two sections: got %d, want %d", got, strings.Index(body, "\n## Two"))
	}
}

// Mirrors the real gh release shape: body starts with a ## header, the first
// image sits inside that first section (Lead), a later image sits in a second
// section (not Lead).
func TestExtractImagesLeadFlag(t *testing.T) {
	body := `## Issue types

<img src="https://x/first.png" alt="first" />

This release brings features.

## Manage discussions

<img src="https://x/second.png" alt="second" />`
	_, imgs := extractImages(body)
	if len(imgs) != 2 {
		t.Fatalf("want 2 images, got %d", len(imgs))
	}
	if !imgs[0].Lead {
		t.Errorf("first image (in first section) should be Lead")
	}
	if imgs[1].Lead {
		t.Errorf("second image (in second section) should not be Lead")
	}
}

func TestSplitSegments(t *testing.T) {
	drawables := []imageRef{{URL: "a", Rows: 3}, {URL: "b", Rows: 2}}
	// header, lead-image sentinel, blank+lead, body-image sentinel, blank+bucket
	content := "header\n" + sentinelLine(0) + "\n\nlead text\n" + sentinelLine(1) + "\n\nbucket"

	segs := splitSegments(content, drawables)
	if len(segs) != 5 {
		t.Fatalf("want 5 segments, got %d: %+v", len(segs), segs)
	}
	// 0 text(header), 1 image a, 2 text(lead), 3 image b, 4 text(bucket)
	if segs[1].image == nil || segs[1].image.URL != "a" {
		t.Errorf("seg[1] should be image a, got %+v", segs[1])
	}
	if segs[3].image == nil || segs[3].image.URL != "b" {
		t.Errorf("seg[3] should be image b, got %+v", segs[3])
	}
	for i, s := range segs {
		if s.image != nil {
			continue
		}
		for _, ln := range s.lines {
			if strings.Contains(ln, imgSentinelPrefix) {
				t.Errorf("seg[%d] leaked a sentinel: %q", i, ln)
			}
		}
	}
}
