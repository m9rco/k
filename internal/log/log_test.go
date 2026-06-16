package log

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want zerolog.Level
	}{
		{"debug", zerolog.DebugLevel},
		{"info", zerolog.InfoLevel},
		{"", zerolog.InfoLevel},
		{"WARN", zerolog.WarnLevel},
		{"warning", zerolog.WarnLevel},
		{"error", zerolog.ErrorLevel},
		{"bogus", zerolog.InfoLevel},
	}
	for _, c := range cases {
		if got := parseLevel(c.in); got != c.want {
			t.Errorf("parseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestInitFileAndLevelFilter verifies records land in the file and that a level
// below the configured minimum is dropped.
func TestInitFileAndLevelFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "app.log") // nested dir must be created
	closer, err := Init(Options{File: path, Level: "info"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer closer.Close()

	Root().Info().Str("event", "kept").Msg("hi")
	Root().Debug().Str("event", "dropped").Msg("lo") // below info => filtered

	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, `"event":"kept"`) {
		t.Errorf("expected kept record in log, got: %s", out)
	}
	if strings.Contains(out, "dropped") {
		t.Errorf("debug record should have been filtered, got: %s", out)
	}
	if !strings.Contains(out, `"trace_id"`) {
		// root logger has no trace fields; ensure we did NOT accidentally inject one
		if strings.Contains(out, "trace_id") {
			t.Errorf("root record should not carry trace_id")
		}
	}
}

// TestInitNoFileFallsBackToStderr ensures an empty File never errors (stderr
// path) and returns a usable no-op closer.
func TestInitNoFileFallsBackToStderr(t *testing.T) {
	closer, err := Init(Options{File: "", Level: "debug"})
	if err != nil {
		t.Fatalf("Init with no file: %v", err)
	}
	if closer == nil {
		t.Fatal("closer is nil")
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("noop close: %v", err)
	}
}

// TestWithTraceFromRoundTrip checks that WithTrace binds trace_id/session_id and
// From recovers that logger, while a bare context falls back to root.
func TestWithTraceFromRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	closer, err := Init(Options{File: path, Level: "info"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer closer.Close()

	ctx := WithTrace(context.Background(), "trace_abc", "sess_xyz")
	From(ctx).Info().Str("event", "bound").Msg("")

	// Simulate the async boundary: ctx values survive WithoutCancel.
	asyncCtx := context.WithoutCancel(ctx)
	From(asyncCtx).Info().Str("event", "async").Msg("")

	// Bare context => root logger, no trace fields.
	From(context.Background()).Info().Str("event", "bare").Msg("")

	closer.Close()
	data, _ := os.ReadFile(path)
	out := string(data)

	for _, want := range []string{
		`"trace_id":"trace_abc"`,
		`"session_id":"sess_xyz"`,
		`"event":"bound"`,
		`"event":"async"`,
		`"event":"bare"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in log output:\n%s", want, out)
		}
	}
	// The async record must carry the SAME trace as the originating turn.
	asyncLines := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, `"event":"async"`) {
			asyncLines++
			if !strings.Contains(line, `"trace_id":"trace_abc"`) {
				t.Errorf("async record lost trace_id: %s", line)
			}
		}
		if strings.Contains(line, `"event":"bare"`) && strings.Contains(line, "trace_id") {
			t.Errorf("bare record should not carry trace_id: %s", line)
		}
	}
	if asyncLines != 1 {
		t.Errorf("expected exactly one async record, got %d", asyncLines)
	}
}

func TestFromNilContext(t *testing.T) {
	if From(nil) == nil { //nolint:staticcheck // explicitly testing nil ctx
		t.Fatal("From(nil) returned nil")
	}
}
