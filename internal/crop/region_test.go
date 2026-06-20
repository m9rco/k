package crop

import (
	"bytes"
	"image"
	"testing"
)

// TestRegionBytesExtractsBox verifies a normalized box yields exactly the
// selected pixel sub-rect (no scaling to a target).
func TestRegionBytesExtractsBox(t *testing.T) {
	data := makePNG(t, 1000, 500)
	// Select the right half: x=0.5,w=0.5 → 500px wide; full height.
	res, err := RegionBytes(data, 0.5, 0.0, 0.5, 1.0)
	if err != nil {
		t.Fatalf("RegionBytes: %v", err)
	}
	if res.Width != 500 || res.Height != 500 {
		t.Fatalf("region size = %dx%d, want 500x500", res.Width, res.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(res.Data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 500 || img.Bounds().Dy() != 500 {
		t.Errorf("decoded size = %v", img.Bounds())
	}
}

func TestRegionBytesClampsToBounds(t *testing.T) {
	data := makePNG(t, 400, 400)
	// Box runs to the exact edge (x+w == 1.0) — must succeed, clamped.
	if _, err := RegionBytes(data, 0.75, 0.75, 0.25, 0.25); err != nil {
		t.Fatalf("edge box should succeed: %v", err)
	}
}

func TestRegionBytesRejectsBadBox(t *testing.T) {
	data := makePNG(t, 400, 400)
	cases := []struct {
		name       string
		x, y, w, h float64
	}{
		{"zero width", 0.1, 0.1, 0, 0.5},
		{"zero height", 0.1, 0.1, 0.5, 0},
		{"negative x", -0.1, 0.1, 0.5, 0.5},
		{"out of range", 0.6, 0.1, 0.5, 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := RegionBytes(data, c.x, c.y, c.w, c.h); err == nil {
				t.Errorf("expected error for %s", c.name)
			}
		})
	}
}
