package composite

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gameasset/internal/store"
)

func transparentPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	// One opaque pixel, rest fully transparent — exercises alpha preservation.
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newSvc(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	for _, sid := range []string{"s1", "s2"} {
		_ = st.UpsertSession(store.SessionRecord{ID: sid, Fingerprint: "fp-" + sid, CreatedAt: now, LastSeenAt: now})
	}
	var n int
	return NewService(filepath.Join(dir, "assets"), st, func() string { n++; return "comp" + strconv.Itoa(n) }), st, dir
}

func TestPersistTransparentRoundTripPreservesAlpha(t *testing.T) {
	svc, st, _ := newSvc(t)
	src := transparentPNG(t, 16, 16)
	res, err := svc.Persist("s1", src, []string{"layerA", "layerB"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 16 || res.Height != 16 || res.Mime != "image/png" {
		t.Fatalf("unexpected result %+v", res)
	}
	asset, err := st.GetAsset("s1", res.AssetID)
	if err != nil || asset == nil {
		t.Fatalf("asset not persisted: %v", err)
	}
	if asset.Kind != "composite" {
		t.Errorf("want kind composite, got %q", asset.Kind)
	}
	if asset.ParentID != "layerA" {
		t.Errorf("want parent layerA, got %q", asset.ParentID)
	}
	// Decode the stored file and confirm the transparent pixel survived lossless opt.
	b, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(8, 8).RGBA(); a != 0 {
		t.Errorf("expected transparent pixel preserved, got alpha=%d", a)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a == 0 {
		t.Errorf("expected opaque pixel preserved at (0,0)")
	}
}

func TestPersistCrossSessionIsolation(t *testing.T) {
	svc, st, _ := newSvc(t)
	res, err := svc.Persist("s1", transparentPNG(t, 8, 8), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := st.GetAsset("s2", res.AssetID); a != nil {
		t.Fatal("composite leaked across sessions")
	}
	if a, _ := st.GetAsset("s1", res.AssetID); a == nil {
		t.Fatal("composite not visible in owning session")
	}
}

func TestPersistRejectsInvalidInput(t *testing.T) {
	svc, _, _ := newSvc(t)
	if _, err := svc.Persist("s1", []byte("not an image"), nil, true); err == nil {
		t.Error("expected error for non-image bytes")
	}
	if _, err := svc.Persist("s1", nil, nil, true); err == nil {
		t.Error("expected error for empty body")
	}
	if _, err := svc.Persist("", transparentPNG(t, 4, 4), nil, true); err == nil {
		t.Error("expected error for missing session id")
	}
}
