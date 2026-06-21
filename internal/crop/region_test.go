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

// TestRegionPolygonBytesCropsBBoxAndMasks verifies a triangle polygon yields a
// PNG cropped to its bounding box, with pixels outside the triangle transparent
// and pixels inside opaque.
func TestRegionPolygonBytesCropsBBoxAndMasks(t *testing.T) {
	data := makePNG(t, 400, 400)
	// Triangle covering the top-left half: (0,0)-(1,0)-(0,1).
	pts := []Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 1}}
	res, err := RegionPolygonBytes(data, pts)
	if err != nil {
		t.Fatalf("RegionPolygonBytes: %v", err)
	}
	if res.Mime != "image/png" {
		t.Fatalf("mime = %q, want image/png", res.Mime)
	}
	if res.Width != 400 || res.Height != 400 {
		t.Fatalf("bbox size = %dx%d, want 400x400", res.Width, res.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(res.Data))
	if err != nil {
		t.Fatal(err)
	}
	// A pixel near the top-left corner is inside the triangle → opaque.
	if _, _, _, a := img.At(20, 20).RGBA(); a == 0 {
		t.Errorf("inside-triangle pixel is transparent, want opaque")
	}
	// A pixel near the bottom-right corner is outside the triangle → transparent.
	if _, _, _, a := img.At(380, 380).RGBA(); a != 0 {
		t.Errorf("outside-triangle pixel is opaque, want transparent")
	}
}

func TestRegionPolygonBytesRejectsTooFewPoints(t *testing.T) {
	data := makePNG(t, 100, 100)
	if _, err := RegionPolygonBytes(data, []Point{{X: 0, Y: 0}, {X: 1, Y: 1}}); err == nil {
		t.Error("expected error for < 3 points")
	}
}
