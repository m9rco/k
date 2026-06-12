package session

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gameasset/internal/store"
)

func newMgr(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewManager(st), st
}

func TestDeriveIDDeterministic(t *testing.T) {
	fp := Fingerprint{UserAgent: "UA", Language: "zh-CN", Screen: "1920x1080", Timezone: "Asia/Shanghai"}
	if fp.DeriveID() != fp.DeriveID() {
		t.Fatal("DeriveID not deterministic")
	}
	other := fp
	other.UserAgent = "different"
	if fp.DeriveID() == other.DeriveID() {
		t.Fatal("different fingerprints produced same id")
	}
}

func TestEnsureCreatesThenReuses(t *testing.T) {
	m, _ := newMgr(t)
	fp := Fingerprint{UserAgent: "UA", Language: "en"}

	id1, created1, err := m.EnsureID(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !created1 {
		t.Error("first EnsureID should create")
	}
	id2, created2, err := m.EnsureID(fp)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("ids differ: %s vs %s", id1, id2)
	}
	if created2 {
		t.Error("second EnsureID should reuse, not create")
	}
}

func TestReconnectFromStoreAfterRestart(t *testing.T) {
	m, st := newMgr(t)
	fp := Fingerprint{UserAgent: "UA"}
	id, _, err := m.EnsureID(fp)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a server restart: new manager, same store; in-memory live map empty.
	m2 := NewManager(st)
	reused, err := m2.Reconnect(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reused {
		t.Fatal("expected reconnect to recover session from store")
	}
	if len(m2.LiveIDs()) != 1 {
		t.Errorf("expected 1 live session after reconnect, got %d", len(m2.LiveIDs()))
	}
}

func TestReconnectMissReturnsFalse(t *testing.T) {
	m, _ := newMgr(t)
	reused, err := m.Reconnect("sess_unknown")
	if err != nil {
		t.Fatal(err)
	}
	if reused {
		t.Error("reconnect to unknown id should return false")
	}
}

func TestStatusReflectsIntent(t *testing.T) {
	m, _ := newMgr(t)
	id, _, _ := m.EnsureID(Fingerprint{UserAgent: "UA"})
	m.SetRecentIntent(id, "change_background")
	status, err := m.Status(id)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Active || status.RecentIntent != "change_background" {
		t.Errorf("unexpected status: %+v", status)
	}
}

func TestHandleCreateAndContext(t *testing.T) {
	m, _ := newMgr(t)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	body, _ := json.Marshal(createRequest{Fingerprint: Fingerprint{UserAgent: "UA", Language: "zh"}})
	req := httptest.NewRequest("POST", "/api/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp createResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.SessionID == "" || !resp.Created {
		t.Fatalf("unexpected create response: %+v", resp)
	}

	// Context endpoint for the new session.
	req2 := httptest.NewRequest("GET", "/api/session/"+resp.SessionID+"/context", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("context status = %d, body=%s", rec2.Code, rec2.Body.String())
	}
	var status ContextStatus
	if err := json.Unmarshal(rec2.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.SessionID != resp.SessionID || !status.Active {
		t.Errorf("unexpected context: %+v", status)
	}
}

func TestHandleContextUnknownSession(t *testing.T) {
	m, _ := newMgr(t)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest("GET", "/api/session/sess_missing/context", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown session, got %d", rec.Code)
	}
}

func TestSessionReuseViaSessionID(t *testing.T) {
	m, _ := newMgr(t)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	// First create.
	body, _ := json.Marshal(createRequest{Fingerprint: Fingerprint{UserAgent: "UA"}})
	req := httptest.NewRequest("POST", "/api/session", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp createResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	// Second call passes the sessionId (reconnect path).
	body2, _ := json.Marshal(createRequest{SessionID: resp.SessionID})
	req2 := httptest.NewRequest("POST", "/api/session", bytes.NewReader(body2))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	var resp2 createResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if !resp2.Reused || resp2.SessionID != resp.SessionID {
		t.Errorf("expected reuse of same session, got %+v", resp2)
	}
}
