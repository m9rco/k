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

func TestDeleteAssetScopedAndReturnsPath(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	_ = s.UpsertSession(SessionRecord{ID: "sessA", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	_ = s.UpsertSession(SessionRecord{ID: "sessB", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	if err := s.InsertAsset(AssetRecord{ID: "a1", SessionID: "sessA", Kind: "generated", Path: "/tmp/a1.png", Mime: "image/png", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	// Wrong session must not delete and returns empty path.
	path, err := s.DeleteAsset("sessB", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("cross-session delete should return empty path, got %q", path)
	}
	if got, _ := s.GetAsset("sessA", "a1"); got == nil {
		t.Fatal("asset wrongly deleted across sessions")
	}

	// Correct session deletes and returns the file path.
	path, err = s.DeleteAsset("sessA", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/a1.png" {
		t.Errorf("expected returned path /tmp/a1.png, got %q", path)
	}
	if got, _ := s.GetAsset("sessA", "a1"); got != nil {
		t.Error("asset not deleted")
	}
}

func TestDeleteSessionAssetsAndUnfinishedTasks(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	_ = s.UpsertSession(SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	_ = s.UpsertSession(SessionRecord{ID: "other", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	_ = s.InsertAsset(AssetRecord{ID: "a1", SessionID: "s", Kind: "generated", Path: "/tmp/a1.png", Mime: "image/png", CreatedAt: now})
	_ = s.InsertAsset(AssetRecord{ID: "a2", SessionID: "s", Kind: "cropped", Path: "/tmp/a2.png", Mime: "image/png", CreatedAt: now})
	_ = s.InsertAsset(AssetRecord{ID: "b1", SessionID: "other", Kind: "generated", Path: "/tmp/b1.png", Mime: "image/png", CreatedAt: now})
	_ = s.InsertTask(TaskRecord{ID: "t1", SessionID: "s", Kind: "generate", Status: "running", CreatedAt: now, UpdatedAt: now})
	_ = s.InsertTask(TaskRecord{ID: "t2", SessionID: "s", Kind: "generate", Status: "done", CreatedAt: now, UpdatedAt: now})

	paths, err := s.DeleteSessionAssets("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Errorf("expected 2 deleted paths, got %d", len(paths))
	}
	if assets, _ := s.ListAssets("s"); len(assets) != 0 {
		t.Errorf("session assets not cleared: %d remain", len(assets))
	}
	if other, _ := s.ListAssets("other"); len(other) != 1 {
		t.Error("other session's assets wrongly affected")
	}

	if err := s.DeleteUnfinishedTasks("s"); err != nil {
		t.Fatal(err)
	}
	// Running task gone, done task kept.
	if rec, _ := s.GetTask("s", "t1"); rec != nil {
		t.Error("running task not deleted")
	}
	if rec, _ := s.GetTask("s", "t2"); rec == nil {
		t.Error("completed task should be kept")
	}
}

func TestMessagesInsertAndListBySession(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	_ = s.UpsertSession(SessionRecord{ID: "s", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})
	_ = s.UpsertSession(SessionRecord{ID: "other", Fingerprint: "fp", CreatedAt: now, LastSeenAt: now})

	// Insert in chronological order; ListBySession must return oldest-first.
	_ = s.InsertMessage(MessageRecord{ID: "m1", SessionID: "s", Role: "user", Content: "把背景换成淡紫色", CreatedAt: now})
	_ = s.InsertMessage(MessageRecord{ID: "m2", SessionID: "s", Role: "assistant", Content: "好的，正在处理。", ToolRefs: "edit_image:asset_x", CreatedAt: now.Add(time.Second)})
	_ = s.InsertMessage(MessageRecord{ID: "z1", SessionID: "other", Role: "user", Content: "别的会话", CreatedAt: now})

	msgs, err := s.ListMessages("s")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages for session, got %d", len(msgs))
	}
	if msgs[0].ID != "m1" || msgs[1].ID != "m2" {
		t.Errorf("messages not oldest-first: %s, %s", msgs[0].ID, msgs[1].ID)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "把背景换成淡紫色" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].ToolRefs != "edit_image:asset_x" {
		t.Errorf("tool refs not persisted: %q", msgs[1].ToolRefs)
	}
	// Session isolation.
	other, _ := s.ListMessages("other")
	if len(other) != 1 || other[0].ID != "z1" {
		t.Errorf("session isolation broken: %+v", other)
	}

	// A session with no history returns an empty slice, not an error.
	empty, err := s.ListMessages("nobody")
	if err != nil {
		t.Fatalf("ListMessages empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected no messages, got %d", len(empty))
	}
}
