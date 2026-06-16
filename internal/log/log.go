// Package log is the project's diagnostic logging facade. It wraps zerolog with
// a small, opinionated surface: a process-wide root logger configured once at
// startup, plus context helpers that bind a per-turn trace_id and session_id so
// every record produced while handling one user message can be pulled back out
// of the log file by trace.
//
// Why zerolog: zero-allocation JSON on the hot path with almost no transitive
// dependencies, and native context carrying (WithContext/Ctx) so the facade
// stays thin — no hand-rolled context keys.
//
// Destination is chosen once in Init: a JSON file, a file mirrored to stderr, or
// (when no file is configured) stderr only — the historical behaviour.
package log

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// Options configures the root logger. Zero value (empty File) logs JSON to
// stderr at info level, matching pre-facade behaviour.
type Options struct {
	// File is the JSON log destination. Empty => stderr only.
	File string
	// Level is the minimum level: debug | info | warn | error. Empty => info.
	Level string
	// MirrorStderr echoes records to stderr in addition to the file (the file
	// stays pure JSON; stderr is pretty-printed for local development).
	MirrorStderr bool
}

var (
	mu   sync.RWMutex
	root zerolog.Logger = newStderrLogger(zerolog.InfoLevel)
)

// newStderrLogger builds the default JSON-to-stderr logger used before Init and
// whenever no file destination is configured.
func newStderrLogger(level zerolog.Level) zerolog.Logger {
	return zerolog.New(os.Stderr).Level(level).With().Timestamp().Logger()
}

// parseLevel maps a level string to a zerolog level, defaulting to info.
func parseLevel(s string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return zerolog.DebugLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "info", "":
		return zerolog.InfoLevel
	default:
		return zerolog.InfoLevel
	}
}

// Init configures the process-wide root logger from opts. It returns an
// io.Closer for the underlying log file (a no-op closer when logging to stderr),
// which the caller should close on shutdown. A missing parent directory for the
// log file is created. Any failure opening the file is returned wrapped; the
// root logger is left at its safe stderr default in that case.
func Init(opts Options) (io.Closer, error) {
	level := parseLevel(opts.Level)

	// No file configured: stderr only (historical behaviour).
	if strings.TrimSpace(opts.File) == "" {
		setRoot(newStderrLogger(level))
		return noopCloser{}, nil
	}

	if dir := filepath.Dir(opts.File); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("log: create dir %q: %w", dir, err)
		}
	}
	f, err := os.OpenFile(opts.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("log: open %q: %w", opts.File, err)
	}

	var w io.Writer = f
	if opts.MirrorStderr {
		// File stays pure JSON; stderr gets a human-readable console view.
		console := zerolog.ConsoleWriter{Out: os.Stderr}
		w = zerolog.MultiLevelWriter(f, console)
	}
	setRoot(zerolog.New(w).Level(level).With().Timestamp().Logger())
	return f, nil
}

// setRoot swaps the root logger under the write lock.
func setRoot(l zerolog.Logger) {
	mu.Lock()
	root = l
	mu.Unlock()
}

// Root returns the process-wide root logger (no trace fields bound).
func Root() *zerolog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	r := root
	return &r
}

// traceKey is unexported so only this package can stash the bound logger; we
// reuse zerolog's own context machinery rather than inventing a parallel key.
//
// WithTrace derives a child logger carrying trace_id + session_id and stores it
// in ctx via zerolog.Logger.WithContext, so any downstream From(ctx) — including
// across the async generation goroutine (ctx values survive
// context.WithoutCancel) — emits records tagged with the originating turn.
func WithTrace(ctx context.Context, traceID, sessionID string) context.Context {
	mu.RLock()
	r := root
	mu.RUnlock()
	l := r.With().Str("trace_id", traceID).Str("session_id", sessionID).Logger()
	return l.WithContext(ctx)
}

// From returns the logger bound to ctx by WithTrace, or the root logger when ctx
// carries none. It never returns nil.
func From(ctx context.Context) *zerolog.Logger {
	if ctx != nil {
		if l := zerolog.Ctx(ctx); l != nil && l.GetLevel() != zerolog.Disabled {
			return l
		}
	}
	return Root()
}

// noopCloser is returned when logging to stderr (nothing to close).
type noopCloser struct{}

func (noopCloser) Close() error { return nil }
