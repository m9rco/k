package generation

import "testing"

// TestSizeParam verifies the requested dimensions snap to the gpt-image-2 size
// enum by NEAREST aspect ratio (not a coarse orientation split): targets that
// share an orientation but differ widely in ratio (3:2 vs 4:1) both land on the
// closest legal proportion, and the convergence step trims to exact afterward.
func TestSizeParam(t *testing.T) {
	cases := []struct {
		w, h  int
		want  string
		label string
	}{
		{1024, 1024, "1024x1024", "square"},
		{1200, 800, "1536x1024", "3:2 landscape"},
		{800, 1200, "1024x1536", "2:3 portrait"},
		{1920, 1080, "1536x1024", "16:9 → nearest landscape"},
		{1080, 1920, "1024x1536", "9:16 → nearest portrait"},
		{2000, 500, "1536x1024", "4:1 extreme banner → widest legal"},
		{500, 2000, "1024x1536", "1:4 extreme tall → tallest legal"},
		{1040, 1024, "1024x1024", "near-square stays square"},
		{0, 1024, "", "zero width → provider decides"},
		{1024, 0, "", "zero height → provider decides"},
	}
	for _, c := range cases {
		if got := sizeParam(c.w, c.h); got != c.want {
			t.Errorf("[%s] sizeParam(%d,%d) = %q, want %q", c.label, c.w, c.h, got, c.want)
		}
	}
}
