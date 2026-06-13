package download

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gameasset/internal/store"
)

func newTestService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now().UTC()
	if err := st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	return NewService(st), st, dir
}

// writePNGAsset writes a tiny PNG file and inserts a matching asset row.
func writePNGAsset(t *testing.T, st *store.Store, dir, id string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{10, 20, 30, 255})
		}
	}
	path := filepath.Join(dir, id+".png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := st.InsertAsset(store.AssetRecord{
		ID: id, SessionID: "s", Kind: "generated", Path: path, Mime: "image/png",
		Width: 4, Height: 4, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSingleDownloadSetsAttachment(t *testing.T) {
	svc, st, dir := newTestService(t)
	writePNGAsset(t, st, dir, "a1")
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/session/s/assets/a1/download", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "a1.png") {
		t.Errorf("unexpected Content-Disposition: %q", cd)
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("unexpected Content-Type: %q", rec.Header().Get("Content-Type"))
	}
}

func TestSingleDownloadUnknownIsNotFound(t *testing.T) {
	svc, _, _ := newTestService(t)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	req := httptest.NewRequest("GET", "/api/session/s/assets/nope/download", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestZipPackagesValidAndSkipsInvalid(t *testing.T) {
	svc, st, dir := newTestService(t)
	writePNGAsset(t, st, dir, "a1")
	writePNGAsset(t, st, dir, "a2")
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	body, _ := json.Marshal(zipRequest{AssetIDs: []string{"a1", "missing", "a2"}})
	req := httptest.NewRequest("POST", "/api/session/s/download/zip", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/zip" {
		t.Errorf("unexpected Content-Type: %q", rec.Header().Get("Content-Type"))
	}
	// Skipped ids reported.
	skipped := rec.Header().Get("X-Skipped-Assets")
	if skipped != "missing" {
		t.Errorf("expected skipped=missing, got %q", skipped)
	}
	// Zip contains exactly the two valid assets.
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("expected 2 files in zip, got %d", len(zr.File))
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".png") {
			t.Errorf("unexpected zip entry name: %q", f.Name)
		}
	}
}

func TestZipAllInvalidIsBadRequest(t *testing.T) {
	svc, _, _ := newTestService(t)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	body, _ := json.Marshal(zipRequest{AssetIDs: []string{"x", "y"}})
	req := httptest.NewRequest("POST", "/api/session/s/download/zip", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for all-invalid batch, got %d", rec.Code)
	}
}

// writeCroppedAsset writes a PNG and inserts a cropped asset row carrying
// channel/size metadata, mirroring what crop.Service persists.
func writeCroppedAsset(t *testing.T, st *store.Store, dir, id, channelID, sizeID string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	path := filepath.Join(dir, id+".png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	meta, _ := json.Marshal(map[string]string{"channelId": channelID, "sizeId": sizeID})
	if err := st.InsertAsset(store.AssetRecord{
		ID: id, SessionID: "s", Kind: "cropped", Path: path, Mime: "image/png",
		Width: 4, Height: 4, Meta: string(meta), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestZipOrganizesByChannelAndSize(t *testing.T) {
	svc, st, dir := newTestService(t)
	writeCroppedAsset(t, st, dir, "c1", "taptap", "taptap.icon.512")
	writeCroppedAsset(t, st, dir, "c2", "taptap", "taptap.icon.512") // same dir -> unique names
	writeCroppedAsset(t, st, dir, "c3", "bilibili", "bilibili.banner.1")
	writePNGAsset(t, st, dir, "g1") // no meta -> kind bucket
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	body, _ := json.Marshal(zipRequest{AssetIDs: []string{"c1", "c2", "c3", "g1"}})
	req := httptest.NewRequest("POST", "/api/session/s/download/zip", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]int{}
	for _, f := range zr.File {
		seg := f.Name[:strings.LastIndex(f.Name, "/")]
		dirs[seg]++
	}
	if dirs["taptap/taptap.icon.512"] != 2 {
		t.Errorf("expected 2 files under taptap/taptap.icon.512, got %d (%v)", dirs["taptap/taptap.icon.512"], dirs)
	}
	if dirs["bilibili/bilibili.banner.1"] != 1 {
		t.Errorf("expected 1 file under bilibili/bilibili.banner.1, got %v", dirs)
	}
	if dirs["generated"] != 1 {
		t.Errorf("expected non-cropped asset under generated/, got %v", dirs)
	}
	// Names within a directory must be unique.
	names := map[string]bool{}
	for _, f := range zr.File {
		if names[f.Name] {
			t.Errorf("duplicate zip entry name: %q", f.Name)
		}
		names[f.Name] = true
	}
}

func TestExtForMime(t *testing.T) {
	cases := map[string]string{
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/webp": ".webp",
		"video/mp4":  ".mp4",
		"weird":      ".bin",
	}
	for mime, want := range cases {
		if got := extForMime(mime); got != want {
			t.Errorf("extForMime(%q) = %q, want %q", mime, got, want)
		}
	}
}
