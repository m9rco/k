package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSessionUpsertAndGet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	rec := SessionRecord{ID: "sess1", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}
	if err := s.UpsertSession(rec); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	got, err := s.GetSession("sess1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil || got.ID != "sess1" || got.Fingerprint != "fp" {
		t.Fatalf("unexpected session: %+v", got)
	}

	// Upsert again with a newer last_seen_at should not duplicate, only refresh.
	later := now.Add(time.Hour)
	rec.LastSeenAt = later
	if err := s.UpsertSession(rec); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = s.GetSession("sess1")
	if !got.LastSeenAt.Equal(later) {
		t.Errorf("last_seen_at not refreshed: %v", got.LastSeenAt)
	}
}

func TestGetSessionMissing(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetSession("nope")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing session, got %+v", got)
	}
}

func TestAssetIsolationBySession(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	for _, id := range []string{"sessA", "sessB"} {
		if err := s.UpsertSession(SessionRecord{ID: id, Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertAsset(AssetRecord{ID: "a1", SessionID: "sessA", Kind: "generated", Path: "/x", Mime: "image/png", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// sessB must not be able to read sessA's asset.
	got, err := s.GetAsset("sessB", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("cross-session asset access should return nil")
	}
	// sessA can read its own asset.
	got, err = s.GetAsset("sessA", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "a1" {
		t.Errorf("owner could not read own asset: %+v", got)
	}
}

func TestListAssetsOrdering(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	if err := s.UpsertSession(SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	older := AssetRecord{ID: "old", SessionID: "s", Kind: "generated", Path: "/o", Mime: "image/png", CreatedAt: now.Add(-time.Hour)}
	newer := AssetRecord{ID: "new", SessionID: "s", Kind: "generated", Path: "/n", Mime: "image/png", CreatedAt: now}
	if err := s.InsertAsset(older); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertAsset(newer); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAssets("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "new" {
		t.Errorf("expected newest first, got %+v", list)
	}
}

func TestTaskLifecycleAndActiveCount(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	if err := s.UpsertSession(SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	task := TaskRecord{ID: "t1", SessionID: "s", Kind: "generate", Status: "queued", CreatedAt: now, UpdatedAt: now}
	if err := s.InsertTask(task); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountActiveTasks("s")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("active count = %d, want 1", n)
	}

	task.Status = "done"
	task.Progress = 100
	task.AssetID = "a1"
	task.UpdatedAt = now.Add(time.Minute)
	if err := s.UpdateTask(task); err != nil {
		t.Fatal(err)
	}
	n, _ = s.CountActiveTasks("s")
	if n != 0 {
		t.Errorf("active count after done = %d, want 0", n)
	}
}
