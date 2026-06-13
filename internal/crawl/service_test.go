package crawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gameasset/internal/store"
	"gameasset/internal/transport"
)

// stubSource returns canned results for tests.
type stubSource struct {
	configured bool
	results    []Result
	err        error
}

func (s *stubSource) Configured() bool { return s.configured }
func (s *stubSource) Search(_ context.Context, _ string, _ int) ([]Result, error) {
	return s.results, s.err
}

func newCrawlService(t *testing.T, src Source) (*Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	broker := transport.NewTaskBroker()
	var n int
	id := func(p string) string { n++; return p + strconv.Itoa(n) }
	svc := NewService(src, st, broker, filepath.Join(dir, "assets"), id)
	now := time.Now().UTC()
	_ = st.UpsertSession(store.SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	return svc, st, dir
}

func waitTask(t *testing.T, st *store.Store, taskID string) *store.TaskRecord {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		rec, _ := st.GetTask("s", taskID)
		if rec != nil && (rec.Status == "done" || rec.Status == "failed") {
			return rec
		}
		select {
		case <-deadline:
			t.Fatalf("timeout (last=%v)", rec)
		case <-time.After(15 * time.Millisecond):
		}
	}
}

func TestCrawlDegradesWhenUnconfigured(t *testing.T) {
	svc, _, _ := newCrawlService(t, &stubSource{configured: false})
	if svc.Configured() {
		t.Fatal("should be unconfigured")
	}
	if _, err := svc.Start(context.Background(), Params{SessionID: "s", Game: "X"}); err == nil {
		t.Fatal("expected error when unconfigured")
	}
}

func TestCrawlNoResultsFails(t *testing.T) {
	svc, st, _ := newCrawlService(t, &stubSource{configured: true, results: nil})
	taskID, err := svc.Start(context.Background(), Params{SessionID: "s", Game: "无此游戏"})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, taskID)
	if rec.Status != "failed" {
		t.Errorf("expected failed on no results, got %q", rec.Status)
	}
}

func TestCrawlSavesAndSkipsPerItem(t *testing.T) {
	// One URL serves a valid image, another 404s — the good one is saved, the
	// bad one skipped (not a whole-task failure).
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNGBYTES"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer img.Close()

	src := &stubSource{configured: true, results: []Result{
		{URL: img.URL + "/ok.png", Source: "testsrc", Title: "cover"},
		{URL: img.URL + "/missing.png", Source: "testsrc"},
	}}
	svc, st, _ := newCrawlService(t, src)

	taskID, err := svc.Start(context.Background(), Params{SessionID: "s", Game: "原神"})
	if err != nil {
		t.Fatal(err)
	}
	rec := waitTask(t, st, taskID)
	if rec.Status != "done" {
		t.Fatalf("expected done (partial success), got %q %q", rec.Status, rec.Error)
	}
	assets, _ := st.ListAssets("s")
	if len(assets) != 1 {
		t.Fatalf("expected 1 saved asset, got %d", len(assets))
	}
	if assets[0].Kind != "crawled" {
		t.Errorf("kind = %q, want crawled", assets[0].Kind)
	}
	if assets[0].Meta == "" {
		t.Error("expected crawl meta (source attribution)")
	}
}
