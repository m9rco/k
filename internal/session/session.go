// Package session manages anonymous, login-free sessions. A session id is
// derived from browser fingerprint features (user-agent, language, screen, …)
// so a returning browser tab can reconnect to the same session. Active session
// state (recent intent, transient counters) is held in memory, while durable
// records live in the SQLite store. All conversation context, tasks, and asset
// references are scoped per session to enforce isolation.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gameasset/internal/store"
)

// Fingerprint carries the browser features used to derive a stable session id.
type Fingerprint struct {
	UserAgent string `json:"userAgent"`
	Language  string `json:"language"`
	Screen    string `json:"screen"`   // e.g. "1920x1080"
	Timezone  string `json:"timezone"` // e.g. "Asia/Shanghai"
	// Nonce lets the client force a brand-new session when desired (optional).
	Nonce string `json:"nonce"`
}

// DeriveID produces a deterministic session id from the fingerprint features.
// Identical fingerprints yield the same id, enabling reconnect; the value is
// not security-sensitive (no auth in this tool).
func (f Fingerprint) DeriveID() string {
	parts := []string{f.UserAgent, f.Language, f.Screen, f.Timezone, f.Nonce}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return "sess_" + hex.EncodeToString(sum[:8])
}

// State is the in-memory active state for one session.
type State struct {
	ID           string
	Fingerprint  string
	RecentIntent string
	CreatedAt    time.Time
	LastSeenAt   time.Time
}

// ContextStatus is the summary surfaced to the frontend context panel.
type ContextStatus struct {
	SessionID    string    `json:"sessionId"`
	Active       bool      `json:"active"`
	ActiveTasks  int       `json:"activeTasks"`
	RecentIntent string    `json:"recentIntent"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

// Manager creates and tracks sessions, backed by the store for durability.
type Manager struct {
	store *store.Store
	mu    sync.RWMutex
	live  map[string]*State

	// now is injectable for deterministic tests.
	now func() time.Time
}

// NewManager constructs a session manager over the given store.
func NewManager(st *store.Store) *Manager {
	return &Manager{
		store: st,
		live:  make(map[string]*State),
		now:   func() time.Time { return time.Now().UTC() },
	}
}

// EnsureID returns the session id for the fingerprint, creating or reusing the
// session as needed. If a session id already exists (from sessionStorage), the
// caller should prefer Reconnect; this method always derives from fingerprint.
func (m *Manager) EnsureID(fp Fingerprint) (string, bool, error) {
	id := fp.DeriveID()
	created, err := m.ensure(id, fp.UserAgent)
	return id, created, err
}

// Reconnect reuses an existing session by id. It returns (true) if the session
// was found (in memory or store) and refreshed, (false) if no such session
// exists and the caller should create one from a fingerprint instead.
func (m *Manager) Reconnect(id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	m.mu.RLock()
	st, ok := m.live[id]
	m.mu.RUnlock()
	if ok {
		m.touch(st)
		return true, nil
	}
	// Fall back to the durable store (e.g. after a server restart).
	rec, err := m.store.GetSession(id)
	if err != nil {
		return false, err
	}
	if rec == nil {
		return false, nil
	}
	now := m.now()
	m.mu.Lock()
	m.live[id] = &State{
		ID:          rec.ID,
		Fingerprint: rec.Fingerprint,
		CreatedAt:   rec.CreatedAt,
		LastSeenAt:  now,
	}
	m.mu.Unlock()
	rec.LastSeenAt = now
	if err := m.store.UpsertSession(*rec); err != nil {
		return false, err
	}
	return true, nil
}

// ensure creates the session if absent or refreshes it if present.
// Returns whether a new session was created.
func (m *Manager) ensure(id, fingerprint string) (bool, error) {
	now := m.now()
	m.mu.Lock()
	if st, ok := m.live[id]; ok {
		st.LastSeenAt = now
		m.mu.Unlock()
		if err := m.persist(id, fingerprint, st.CreatedAt, now); err != nil {
			return false, err
		}
		return false, nil
	}
	// Not in memory: check the durable store before deciding it's new.
	m.mu.Unlock()
	rec, err := m.store.GetSession(id)
	if err != nil {
		return false, err
	}
	created := rec == nil
	createdAt := now
	if rec != nil {
		createdAt = rec.CreatedAt
	}
	m.mu.Lock()
	m.live[id] = &State{ID: id, Fingerprint: fingerprint, CreatedAt: createdAt, LastSeenAt: now}
	m.mu.Unlock()
	if err := m.persist(id, fingerprint, createdAt, now); err != nil {
		return false, err
	}
	return created, nil
}

func (m *Manager) persist(id, fingerprint string, createdAt, seenAt time.Time) error {
	return m.store.UpsertSession(store.SessionRecord{
		ID:          id,
		Fingerprint: fingerprint,
		CreatedAt:   createdAt,
		LastSeenAt:  seenAt,
	})
}

func (m *Manager) touch(st *State) {
	now := m.now()
	m.mu.Lock()
	st.LastSeenAt = now
	m.mu.Unlock()
	_ = m.persist(st.ID, st.Fingerprint, st.CreatedAt, now)
}

// SetRecentIntent records the most recent recognized intent for the session.
func (m *Manager) SetRecentIntent(id, intent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.live[id]; ok {
		st.RecentIntent = intent
		st.LastSeenAt = m.now()
	}
}

// Status returns the context summary for the session, combining in-memory state
// with the live active-task count from the store.
func (m *Manager) Status(id string) (ContextStatus, error) {
	m.mu.RLock()
	st, ok := m.live[id]
	m.mu.RUnlock()
	if !ok {
		return ContextStatus{SessionID: id, Active: false}, nil
	}
	active, err := m.store.CountActiveTasks(id)
	if err != nil {
		return ContextStatus{}, fmt.Errorf("status: %w", err)
	}
	return ContextStatus{
		SessionID:    st.ID,
		Active:       true,
		ActiveTasks:  active,
		RecentIntent: st.RecentIntent,
		LastSeenAt:   st.LastSeenAt,
	}, nil
}

// LiveIDs returns the ids of currently tracked sessions (sorted, for tests).
func (m *Manager) LiveIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.live))
	for id := range m.live {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
