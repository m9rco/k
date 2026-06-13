// Package store provides SQLite-backed persistence for sessions, generated
// assets, and long-running task metadata. It uses the pure-Go modernc.org/sqlite
// driver so the server stays a single static binary with no CGO.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database handle.
type Store struct {
	db *sql.DB
}

// schema is applied on Open. It is idempotent (IF NOT EXISTS) so repeated
// startups are safe. A preferences table is created now to reserve the slot for
// the future memory system, even though the MVP does not write to it.
const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id           TEXT PRIMARY KEY,
	fingerprint  TEXT NOT NULL,
	created_at   DATETIME NOT NULL,
	last_seen_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS assets (
	id          TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	kind        TEXT NOT NULL,            -- upload | generated | cropped
	path        TEXT NOT NULL,            -- local file path
	mime        TEXT NOT NULL,
	width       INTEGER NOT NULL DEFAULT 0,
	height      INTEGER NOT NULL DEFAULT 0,
	provider    TEXT NOT NULL DEFAULT '', -- which image provider produced it
	parent_id   TEXT NOT NULL DEFAULT '', -- source asset for derived products
	meta        TEXT NOT NULL DEFAULT '', -- JSON blob for extra metadata
	created_at  DATETIME NOT NULL,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);
CREATE INDEX IF NOT EXISTS idx_assets_session ON assets(session_id);

CREATE TABLE IF NOT EXISTS tasks (
	id          TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	kind        TEXT NOT NULL,            -- generate | crop
	status      TEXT NOT NULL,            -- queued | running | done | failed
	progress    INTEGER NOT NULL DEFAULT 0,
	intent      TEXT NOT NULL DEFAULT '',
	error       TEXT NOT NULL DEFAULT '',
	asset_id    TEXT NOT NULL DEFAULT '', -- produced asset once done
	created_at  DATETIME NOT NULL,
	updated_at  DATETIME NOT NULL,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);
CREATE INDEX IF NOT EXISTS idx_tasks_session ON tasks(session_id);

-- Conversation history: one row per persisted turn message (user/assistant),
-- text + reference ids only (never raw image/base64 payloads). Lets the agent
-- rebuild a session's context window after a process restart.
CREATE TABLE IF NOT EXISTS messages (
	id          TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	role        TEXT NOT NULL,            -- user | assistant
	content     TEXT NOT NULL DEFAULT '',
	tool_refs   TEXT NOT NULL DEFAULT '', -- optional compact tool reference summary
	created_at  DATETIME NOT NULL,
	FOREIGN KEY (session_id) REFERENCES sessions(id)
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);

-- Reserved for the future memory/preference system (not used in MVP).
CREATE TABLE IF NOT EXISTS preferences (
	id          TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	key         TEXT NOT NULL,
	value       TEXT NOT NULL DEFAULT '',
	created_at  DATETIME NOT NULL
);
`

// Open opens (creating if needed) the SQLite database at dbPath and applies the
// schema. Parent directories are created automatically.
func Open(dbPath string) (*Store, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite handles concurrency best with a single writer; keep the pool small
	// and enable WAL for better read/write interleaving.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for packages that need direct queries.
func (s *Store) DB() *sql.DB { return s.db }

// --- Sessions ---

// SessionRecord is a persisted session row.
type SessionRecord struct {
	ID          string
	Fingerprint string
	CreatedAt   time.Time
	LastSeenAt  time.Time
}

// UpsertSession inserts a new session or refreshes last_seen_at for an existing one.
func (s *Store) UpsertSession(rec SessionRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, fingerprint, created_at, last_seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
		rec.ID, rec.Fingerprint, rec.CreatedAt, rec.LastSeenAt)
	if err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}
	return nil
}

// GetSession returns the session row by id, or (nil, nil) if absent.
func (s *Store) GetSession(id string) (*SessionRecord, error) {
	row := s.db.QueryRow(`SELECT id, fingerprint, created_at, last_seen_at FROM sessions WHERE id = ?`, id)
	var rec SessionRecord
	err := row.Scan(&rec.ID, &rec.Fingerprint, &rec.CreatedAt, &rec.LastSeenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &rec, nil
}

// --- Assets ---

// AssetRecord is a persisted asset row.
type AssetRecord struct {
	ID        string
	SessionID string
	Kind      string
	Path      string
	Mime      string
	Width     int
	Height    int
	Provider  string
	ParentID  string
	Meta      string
	CreatedAt time.Time
}

// InsertAsset persists a new asset.
func (s *Store) InsertAsset(a AssetRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO assets (id, session_id, kind, path, mime, width, height, provider, parent_id, meta, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.SessionID, a.Kind, a.Path, a.Mime, a.Width, a.Height, a.Provider, a.ParentID, a.Meta, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert asset: %w", err)
	}
	return nil
}

// GetAsset returns an asset by id scoped to a session, or (nil, nil) if not found.
// Scoping by session enforces cross-session isolation at the data layer.
func (s *Store) GetAsset(sessionID, id string) (*AssetRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, session_id, kind, path, mime, width, height, provider, parent_id, meta, created_at
		FROM assets WHERE id = ? AND session_id = ?`, id, sessionID)
	var a AssetRecord
	err := row.Scan(&a.ID, &a.SessionID, &a.Kind, &a.Path, &a.Mime, &a.Width, &a.Height, &a.Provider, &a.ParentID, &a.Meta, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}
	return &a, nil
}

// ListAssets returns all assets for a session, newest first.
func (s *Store) ListAssets(sessionID string) ([]AssetRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, kind, path, mime, width, height, provider, parent_id, meta, created_at
		FROM assets WHERE session_id = ? ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()
	var out []AssetRecord
	for rows.Next() {
		var a AssetRecord
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Kind, &a.Path, &a.Mime, &a.Width, &a.Height, &a.Provider, &a.ParentID, &a.Meta, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan asset: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAsset removes a single asset row scoped to its session and returns the
// deleted asset's file path (empty if not found) so the caller can remove the
// underlying file. Session scoping enforces cross-session isolation.
func (s *Store) DeleteAsset(sessionID, id string) (string, error) {
	a, err := s.GetAsset(sessionID, id)
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", nil
	}
	if _, err := s.db.Exec(`DELETE FROM assets WHERE id = ? AND session_id = ?`, id, sessionID); err != nil {
		return "", fmt.Errorf("delete asset: %w", err)
	}
	return a.Path, nil
}

// DeleteSessionAssets removes all asset rows for a session and returns their
// file paths for cleanup.
func (s *Store) DeleteSessionAssets(sessionID string) ([]string, error) {
	assets, err := s.ListAssets(sessionID)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`DELETE FROM assets WHERE session_id = ?`, sessionID); err != nil {
		return nil, fmt.Errorf("delete session assets: %w", err)
	}
	paths := make([]string, 0, len(assets))
	for _, a := range assets {
		paths = append(paths, a.Path)
	}
	return paths, nil
}

// DeleteUnfinishedTasks removes queued/running task rows for a session (their
// placeholders are cleared on workspace cleanup). Completed/failed history is
// left intact.
func (s *Store) DeleteUnfinishedTasks(sessionID string) error {
	if _, err := s.db.Exec(`DELETE FROM tasks WHERE session_id = ? AND status IN ('queued','running')`, sessionID); err != nil {
		return fmt.Errorf("delete unfinished tasks: %w", err)
	}
	return nil
}

// DeleteTask removes a single task scoped to its session. Returns the number of
// rows deleted so callers can tell a missing/already-removed task from a hit.
func (s *Store) DeleteTask(sessionID, taskID string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM tasks WHERE session_id = ? AND id = ?`, sessionID, taskID)
	if err != nil {
		return 0, fmt.Errorf("delete task: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteFailedTasks removes all failed tasks for a session (one-click cleanup of
// the failed milestone). Returns how many were removed.
func (s *Store) DeleteFailedTasks(sessionID string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM tasks WHERE session_id = ? AND status = 'failed'`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("delete failed tasks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// --- Tasks ---

// TaskRecord is a persisted long-running task row.
type TaskRecord struct {
	ID        string
	SessionID string
	Kind      string
	Status    string
	Progress  int
	Intent    string
	Error     string
	AssetID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InsertTask persists a new task.
func (s *Store) InsertTask(t TaskRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO tasks (id, session_id, kind, status, progress, intent, error, asset_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.SessionID, t.Kind, t.Status, t.Progress, t.Intent, t.Error, t.AssetID, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

// UpdateTask updates the mutable fields of a task.
func (s *Store) UpdateTask(t TaskRecord) error {
	_, err := s.db.Exec(`
		UPDATE tasks SET status = ?, progress = ?, error = ?, asset_id = ?, updated_at = ?
		WHERE id = ?`,
		t.Status, t.Progress, t.Error, t.AssetID, t.UpdatedAt, t.ID)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	return nil
}

// GetTask returns a task by id scoped to its session, or (nil, nil) if absent.
func (s *Store) GetTask(sessionID, id string) (*TaskRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, session_id, kind, status, progress, intent, error, asset_id, created_at, updated_at
		FROM tasks WHERE id = ? AND session_id = ?`, id, sessionID)
	var t TaskRecord
	err := row.Scan(&t.ID, &t.SessionID, &t.Kind, &t.Status, &t.Progress, &t.Intent, &t.Error, &t.AssetID, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return &t, nil
}

// ListTasks returns all tasks for a session, newest first.
func (s *Store) ListTasks(sessionID string) ([]TaskRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, kind, status, progress, intent, error, asset_id, created_at, updated_at
		FROM tasks WHERE session_id = ? ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var out []TaskRecord
	for rows.Next() {
		var t TaskRecord
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Kind, &t.Status, &t.Progress, &t.Intent, &t.Error, &t.AssetID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CountActiveTasks returns how many tasks for a session are queued or running.
func (s *Store) CountActiveTasks(sessionID string) (int, error) {
	row := s.db.QueryRow(`
		SELECT COUNT(*) FROM tasks WHERE session_id = ? AND status IN ('queued','running')`, sessionID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("count active tasks: %w", err)
	}
	return n, nil
}

// --- Conversation messages ---

// MessageRecord is one persisted conversation turn message. Only text and
// reference ids are stored — never raw image/binary payloads (those live as
// assets addressed by id), keeping the table small and the LLM context clean.
type MessageRecord struct {
	ID        string
	SessionID string
	Role      string // user | assistant
	Content   string
	ToolRefs  string // optional JSON/compact note of tool reference ids
	CreatedAt time.Time
}

// InsertMessage persists one conversation message so a session's chat history
// survives a server restart and can rehydrate the context window.
func (s *Store) InsertMessage(rec MessageRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO messages (id, session_id, role, content, tool_refs, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.SessionID, rec.Role, rec.Content, rec.ToolRefs, rec.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	return nil
}

// ListMessages returns a session's conversation messages oldest-first, so the
// orchestrator can replay them into a fresh context window on restart.
func (s *Store) ListMessages(sessionID string) ([]MessageRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, role, content, tool_refs, created_at
		FROM messages WHERE session_id = ? ORDER BY created_at ASC, rowid ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	var out []MessageRecord
	for rows.Next() {
		var m MessageRecord
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.ToolRefs, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMessages removes a session's persisted conversation history (used when
// the user clears context, so a fresh window is not re-seeded from old turns).
func (s *Store) DeleteMessages(sessionID string) error {
	if _, err := s.db.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}
	return nil
}
