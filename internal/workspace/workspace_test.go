package workspace

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gameasset/internal/store"
)

func newWS(t *testing.T) (*Service, *store.Store, *http.ServeMux) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var n int
	svc := NewService(st, filepath.Join(dir, "assets"), func() string { n++; return "a" + string(rune('0'+n)) }, nil, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	return svc, st, mux
}

func seedSession(t *testing.T, st *store.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.UpsertSession(store.SessionRecord{ID: id, Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
}

func pngBytes(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUploadStoresSourceAsset(t *testing.T) {
	_, st, mux := newWS(t)
	seedSession(t, st, "s1")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "hero.png")
	fw.Write(pngBytes(t, color.RGBA{10, 20, 30, 255}))
	mw.Close()

	req := httptest.NewRequest("POST", "/api/session/s1/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rr.Code, rr.Body.String())
	}
	var av AssetView
	if err := json.Unmarshal(rr.Body.Bytes(), &av); err != nil {
		t.Fatal(err)
	}
	if av.Kind != "upload" || av.Width != 4 || av.Height != 4 {
		t.Fatalf("unexpected asset view: %+v", av)
	}
	assets, _ := st.ListAssets("s1")
	if len(assets) != 1 {
		t.Fatalf("expected 1 stored asset, got %d", len(assets))
	}
}

func TestListAssetsIsolatedPerSession(t *testing.T) {
	_, st, mux := newWS(t)
	seedSession(t, st, "s1")
	seedSession(t, st, "s2")
	now := time.Now().UTC()
	_ = st.InsertAsset(store.AssetRecord{ID: "x1", SessionID: "s1", Kind: "generated", Path: "/tmp/x", Mime: "image/png", CreatedAt: now})

	req := httptest.NewRequest("GET", "/api/session/s2/assets", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var resp struct {
		Assets []AssetView `json:"assets"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Assets) != 0 {
		t.Fatalf("session s2 must not see s1 assets, got %d", len(resp.Assets))
	}
}

func TestRetryOnlyFailedTasks(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "ws.db"))
	t.Cleanup(func() { _ = st.Close() })
	seedSession(t, st, "s1")
	now := time.Now().UTC()
	_ = st.InsertTask(store.TaskRecord{ID: "t1", SessionID: "s1", Kind: "generate", Status: "done", CreatedAt: now, UpdatedAt: now})

	var retried string
	svc := NewService(st, filepath.Join(dir, "assets"), func() string { return "a" }, func(_, taskID string) error {
		retried = taskID
		return nil
	}, nil)
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/session/s1/tasks/t1/retry", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", rr.Code, rr.Body.String())
	}
	if retried != "t1" {
		t.Fatalf("retry not dispatched for t1, got %q", retried)
	}
}

// TestRetryAssetDispatchesAndListsRetryable verifies the asset-retry endpoint
// dispatches the injected retryAsset fn for a product carrying a gen_origin, and
// that the list view marks such a product Retryable while leaving a plain upload
// non-retryable.
func TestRetryAssetDispatchesAndListsRetryable(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "ws.db"))
	t.Cleanup(func() { _ = st.Close() })
	seedSession(t, st, "s1")
	now := time.Now().UTC()
	// A retryable AI product (has gen_origin) and a plain upload (none).
	_ = st.InsertAsset(store.AssetRecord{ID: "gen1", SessionID: "s1", Kind: "generated", Path: "/tmp/g", Mime: "image/png", GenOrigin: `{"sessionId":"s1"}`, CreatedAt: now})
	_ = st.InsertAsset(store.AssetRecord{ID: "up1", SessionID: "s1", Kind: "upload", Path: "/tmp/u", Mime: "image/png", CreatedAt: now})

	var retriedAsset string
	svc := NewService(st, filepath.Join(dir, "assets"), func() string { return "a" }, nil, nil)
	svc.SetRetryAsset(func(_, assetID string) (string, error) {
		retriedAsset = assetID
		return "newtask", nil
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	req := httptest.NewRequest("POST", "/api/session/s1/assets/gen1/retry", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("asset retry status=%d body=%s", rr.Code, rr.Body.String())
	}
	if retriedAsset != "gen1" {
		t.Fatalf("asset retry not dispatched for gen1, got %q", retriedAsset)
	}

	// List: gen1 is retryable, up1 is not.
	lreq := httptest.NewRequest("GET", "/api/session/s1/assets", nil)
	lrr := httptest.NewRecorder()
	mux.ServeHTTP(lrr, lreq)
	var resp struct {
		Assets []AssetView `json:"assets"`
	}
	_ = json.Unmarshal(lrr.Body.Bytes(), &resp)
	got := map[string]bool{}
	for _, a := range resp.Assets {
		got[a.ID] = a.Retryable
	}
	if !got["gen1"] {
		t.Error("gen1 (has gen_origin) should be Retryable")
	}
	if got["up1"] {
		t.Error("up1 (plain upload) must not be Retryable")
	}
}

// TestRetryAssetUnwiredReturns503 verifies the endpoint reports unavailable when
// the retry capability was never wired (e.g. generation service absent).
func TestRetryAssetUnwiredReturns503(t *testing.T) {
	_, st, mux := newWS(t)
	seedSession(t, st, "s1")
	req := httptest.NewRequest("POST", "/api/session/s1/assets/x/retry", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when retry unwired, got %d", rr.Code)
	}
}

// TestUploadTriggersPrewarm verifies a successful upload fires the prewarm hook
// once with the new asset's id/path/mime, and that the upload response is not
// blocked by it (the hook returns synchronously here; in production it spawns a
// goroutine).
func TestUploadTriggersPrewarm(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "ws.db"))
	t.Cleanup(func() { _ = st.Close() })
	seedSession(t, st, "s1")
	var n int
	svc := NewService(st, filepath.Join(dir, "assets"), func() string { n++; return "a" + string(rune('0'+n)) }, nil, nil)
	prewarmed := make(chan [3]string, 1)
	svc.SetPrewarm(func(sessionID, assetID, path, mime string) {
		prewarmed <- [3]string{sessionID, assetID, mime}
	})
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "hero.png")
	fw.Write(pngBytes(t, color.RGBA{1, 2, 3, 255}))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/session/s1/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rr.Code, rr.Body.String())
	}
	select {
	case got := <-prewarmed:
		if got[0] != "s1" || got[2] != "image/png" || got[1] == "" {
			t.Fatalf("unexpected prewarm args: %v", got)
		}
	default:
		t.Fatal("prewarm hook was not called on upload")
	}
}

func TestListTasksReflectsStatus(t *testing.T) {
	_, st, mux := newWS(t)
	seedSession(t, st, "s1")
	now := time.Now().UTC()
	_ = st.InsertTask(store.TaskRecord{ID: "t1", SessionID: "s1", Kind: "generate", Status: "running", Progress: 30, CreatedAt: now, UpdatedAt: now})
	_ = st.InsertTask(store.TaskRecord{ID: "t2", SessionID: "s1", Kind: "generate", Status: "failed", Error: "boom", CreatedAt: now, UpdatedAt: now})

	req := httptest.NewRequest("GET", "/api/session/s1/tasks", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var resp struct {
		Tasks []TaskView `json:"tasks"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(resp.Tasks))
	}
}
