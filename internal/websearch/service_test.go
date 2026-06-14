package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// stubSource returns a fixed set of image URLs pointing at the test image server.
type stubSource struct{ urls []string }

func (s stubSource) SearchWeb(context.Context, string, int) ([]WebResult, error) { return nil, nil }
func (s stubSource) SearchImages(_ context.Context, _, _ string, limit int) ([]ImageResult, error) {
	var out []ImageResult
	for i, u := range s.urls {
		if i >= limit {
			break
		}
		out = append(out, ImageResult{URL: u, Source: "test"})
	}
	return out, nil
}

// recordingAnnouncer captures the AnnounceTask call so the test can assert the
// kind/count broadcast to the frontend.
type recordingAnnouncer struct {
	mu                sync.Mutex
	called            bool
	kind              string
	count             int
	sessionID, taskID string
}

func (a *recordingAnnouncer) AnnounceTask(sessionID, taskID, kind string, count int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.called = true
	a.sessionID, a.taskID, a.kind, a.count = sessionID, taskID, kind, count
}

func TestStartImageSearch_KindAndAnnounceAndParent(t *testing.T) {
	// Image server returns a tiny PNG for any path.
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a} // PNG magic header is enough
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer imgSrv.Close()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	broker := transport.NewTaskBroker()
	seq := 0
	newID := func(prefix string) string { seq++; return prefix + "_" + string(rune('a'+seq)) }

	now := time.Now().UTC()
	if err := st.UpsertSession(store.SessionRecord{ID: "s1", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	src := stubSource{urls: []string{imgSrv.URL + "/1", imgSrv.URL + "/2", imgSrv.URL + "/3"}}
	svc := NewService(src, st, broker, dir, newID)
	ann := &recordingAnnouncer{}
	svc.SetAnnouncer(ann)

	const want = 3
	taskID, err := svc.StartImageSearch(context.Background(), ImageSearchParams{
		SessionID: "s1", Query: "测试", Limit: want,
	})
	if err != nil {
		t.Fatalf("StartImageSearch: %v", err)
	}

	// Announce must fire synchronously with kind=search and count=Limit.
	ann.mu.Lock()
	if !ann.called || ann.kind != "search" || ann.count != want || ann.taskID != taskID {
		t.Fatalf("announce = {called:%v kind:%q count:%d task:%q}, want {true search %d %q}",
			ann.called, ann.kind, ann.count, ann.taskID, want, taskID)
	}
	ann.mu.Unlock()

	// Task row is classified as a search task (not generate).
	rec, err := st.GetTask("s1", taskID)
	if err != nil || rec == nil {
		t.Fatalf("GetTask: %v rec=%v", err, rec)
	}
	if rec.Kind != "search" {
		t.Fatalf("task kind = %q, want search", rec.Kind)
	}

	// Wait for the async download loop to finish.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := st.GetTask("s1", taskID); r != nil && (r.Status == "done" || r.Status == "failed") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	assets, err := st.ListAssets("s1")
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(assets) != want {
		t.Fatalf("downloaded %d assets, want %d", len(assets), want)
	}
	for _, a := range assets {
		if a.Kind != "searched" {
			t.Errorf("asset %s kind = %q, want searched", a.ID, a.Kind)
		}
		if a.ParentID != taskID {
			t.Errorf("asset %s parentID = %q, want %q (search batch anchor)", a.ID, a.ParentID, taskID)
		}
	}
}
