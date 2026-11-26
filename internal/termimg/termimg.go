// Package termimg renders remote images inline using the kitty graphics
// protocol. It is intentionally kitty-only: terminals that don't speak the
// protocol (and multiplexers like tmux/screen that don't pass it through)
// report Capable() == false, and the caller falls back to a plain hyperlink.
//
// Images are drawn with the kitty C=1 cursor policy (the terminal does NOT
// move the cursor after placing the image), so the caller has full control
// over placement and vertical advance. This is what lets render.go drop an
// image into a reserved gap inside an already-printed box.
package termimg

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder (first frame)
	_ "image/jpeg" // register JPEG decoder
	"image/png"    // encode for kitty payload; also registers PNG decoder
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Capable reports whether stdout is a kitty-graphics-capable terminal we can
// safely write image escapes to. tmux/screen are excluded: they multiplex the
// TTY and (by default) swallow or mangle graphics passthrough, corrupting the
// pane instead of displaying.
func Capable() bool {
	if inMultiplexer() {
		return false
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	if strings.Contains(strings.ToLower(os.Getenv("TERM")), "kitty") {
		return true
	}
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "ghostty", "wezterm":
		return true
	}
	return false
}

func inMultiplexer() bool {
	if os.Getenv("TMUX") != "" {
		return true
	}
	t := strings.ToLower(os.Getenv("TERM"))
	return strings.HasPrefix(t, "tmux") || strings.HasPrefix(t, "screen")
}

// imgCache memoizes decoded images by URL so Measure and Draw (called for the
// same image) don't fetch or decode twice.
var imgCache sync.Map // url string -> image.Image

func load(url string) (image.Image, error) {
	if v, ok := imgCache.Load(url); ok {
		return v.(image.Image), nil
	}
	data, err := fetch(url)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	imgCache.Store(url, img)
	return img, nil
}

// Measure returns the number of terminal rows the image at url will occupy when
// drawn at maxCols columns. The caller reserves this many blank lines so the
// image lands in a correctly-sized gap.
func Measure(url string, maxCols int) (int, error) {
	img, err := load(url)
	if err != nil {
		return 0, err
	}
	b := img.Bounds()
	_, rows := fit(b.Dx(), b.Dy(), maxCols)
	return rows, nil
}

// Draw writes the kitty graphics escape for the image at url to w, sized to
// maxCols columns. It uses C=1, so the cursor is NOT moved: the image is
// anchored at the current cursor cell and the caller is responsible for any
// vertical advance. Pair with Measure to keep gap size and image height in sync.
func Draw(w io.Writer, url string, maxCols int) error {
	img, err := load(url)
	if err != nil {
		return err
	}
	b := img.Bounds()
	cols, rows := fit(b.Dx(), b.Dy(), maxCols)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return writeKitty(w, buf.Bytes(), cols, rows)
}

// writeKitty serializes PNG bytes into the kitty graphics in-band format,
// chunked at 4 KiB of base64 per escape. Control keys are sent only on the
// first chunk:
//
//	a=T  transmit and display
//	f=100 PNG payload
//	t=d  payload is the (base64) data, inline
//	q=2  suppress the terminal's OK/error response (keeps stdin clean)
//	C=1  do not move the cursor after display
//	c,r  display size in cells
//	m    1 = more chunks follow, 0 = final chunk
func writeKitty(w io.Writer, pngData []byte, cols, rows int) error {
	b64 := base64.StdEncoding.EncodeToString(pngData)
	const chunk = 4096
	first := true
	for len(b64) > 0 {
		n := chunk
		if n > len(b64) {
			n = len(b64)
		}
		piece := b64[:n]
		b64 = b64[n:]
		more := 0
		if len(b64) > 0 {
			more = 1
		}

		var hdr string
		if first {
			hdr = fmt.Sprintf("\x1b_Ga=T,f=100,t=d,q=2,C=1,c=%d,r=%d,m=%d;", cols, rows, more)
			first = false
		} else {
			hdr = fmt.Sprintf("\x1b_Gm=%d;", more)
		}
		if _, err := io.WriteString(w, hdr+piece+"\x1b\\"); err != nil {
			return err
		}
	}
	return nil
}

const maxImageRows = 30

// fit computes the terminal cell box (cols x rows) for an image of the given
// pixel dimensions, preserving aspect ratio. Width is capped at both maxCols and
// the image's native terminal-cell width so small badges/icons are not upscaled
// to fill the whole box. If the resulting height would exceed maxImageRows,
// width is reduced so the image fits within the height cap instead.
func fit(pxW, pxH, maxCols int) (cols, rows int) {
	if pxW <= 0 || pxH <= 0 {
		return maxCols, 1
	}
	if maxCols < 1 {
		maxCols = 40
	}
	cellW, cellH := terminalCellSize()
	cellHW := cellH / cellW // terminal cell height/width ratio (≈2.0)
	aspect := float64(pxH) / float64(pxW)

	cols = int(math.Ceil(float64(pxW) / cellW))
	if cols > maxCols {
		cols = maxCols
	}
	rows = int(math.Ceil(float64(cols) * aspect / cellHW))
	if rows > maxImageRows {
		rows = maxImageRows
		cols = int(float64(maxImageRows) * cellHW / aspect)
	}
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// terminalCellSize returns the terminal cell's pixel width and height via the
// TIOCGWINSZ ioctl. Falls back to a typical 8x16 monospace cell when the
// terminal doesn't report pixel dimensions.
var terminalCellSize = func() (width, height float64) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err == nil && ws.Xpixel > 0 && ws.Ypixel > 0 && ws.Col > 0 && ws.Row > 0 {
		cellW := float64(ws.Xpixel) / float64(ws.Col)
		cellH := float64(ws.Ypixel) / float64(ws.Row)
		if cellW > 0 && cellH > 0 {
			return cellW, cellH
		}
	}
	return 8, 16
}

// fetch returns the bytes of url, caching them on disk keyed by URL hash so
// re-runs (and repeated images across releases) don't re-download. Follows
// redirects (GitHub user-attachments URLs redirect to signed storage) and caps
// the response size defensively.
func fetch(url string) ([]byte, error) {
	key := sha256.Sum256([]byte(url))
	name := hex.EncodeToString(key[:])
	dir := cacheDir()
	path := filepath.Join(dir, name)

	if b, err := os.ReadFile(path); err == nil {
		return b, nil
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "brew-changelog")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: http %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20)) // 25 MiB cap
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0o755); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
	return b, nil
}

func cacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "brew-changelog", "images")
	}
	return filepath.Join(os.TempDir(), "brew-changelog-images")
}
