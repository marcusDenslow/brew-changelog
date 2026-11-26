package termimg

import "testing"

func withCellSize(width, height float64, fn func()) {
	old := terminalCellSize
	terminalCellSize = func() (float64, float64) { return width, height }
	defer func() { terminalCellSize = old }()
	fn()
}

func TestFitDoesNotUpscaleSmallImages(t *testing.T) {
	withCellSize(8, 16, func() {
		cols, rows := fit(80, 20, 100)
		if cols != 10 {
			t.Fatalf("cols = %d, want native width 10 cells", cols)
		}
		if rows != 2 {
			t.Fatalf("rows = %d, want 2", rows)
		}
	})
}

func TestFitCapsLargeImagesToMaxCols(t *testing.T) {
	withCellSize(8, 16, func() {
		cols, rows := fit(2784, 1494, 96)
		if cols != 96 {
			t.Fatalf("cols = %d, want max width 96 cells", cols)
		}
		if rows != 26 {
			t.Fatalf("rows = %d, want 26", rows)
		}
	})
}

func TestFitHeightCapReducesWidth(t *testing.T) {
	withCellSize(8, 16, func() {
		cols, rows := fit(1000, 3000, 100)
		if rows != maxImageRows {
			t.Fatalf("rows = %d, want maxImageRows %d", rows, maxImageRows)
		}
		if cols != 20 {
			t.Fatalf("cols = %d, want width reduced to 20 cells", cols)
		}
	})
}
