package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Clear env that could leak from the host.
	for _, k := range []string{"ADDR", "CHAT_PRIMARY_MODEL", "USE_TEST_MODEL", "CONTEXT_TOKEN_BUDGET"} {
		t.Setenv(k, "")
	}
	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.ChatPrimary.Model != "claude-opus-4-8" {
		t.Errorf("ChatPrimary.Model = %q, want claude-opus-4-8", cfg.ChatPrimary.Model)
	}
	if cfg.UseTestModel {
		t.Error("UseTestModel should default false")
	}
	if cfg.ContextTokenBudget != 8000 {
		t.Errorf("ContextTokenBudget = %d, want 8000", cfg.ContextTokenBudget)
	}
	// Missing platforms file falls back to the built-in universal preset.
	if len(cfg.Platforms) != 1 || cfg.Platforms[0].Name != "Universal" {
		t.Errorf("expected built-in Universal fallback, got %+v", cfg.Platforms)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("ADDR", ":9090")
	t.Setenv("CHAT_PRIMARY_MODEL", "claude-sonnet-4-6")
	t.Setenv("USE_TEST_MODEL", "true")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("CONTEXT_TOKEN_BUDGET", "16000")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.ChatPrimary.Model != "claude-sonnet-4-6" {
		t.Errorf("model override failed: %q", cfg.ChatPrimary.Model)
	}
	if !cfg.UseTestModel {
		t.Error("USE_TEST_MODEL=true not applied")
	}
	if cfg.ChatPrimary.APIKey != "sk-test" {
		t.Errorf("APIKey not read from env: %q", cfg.ChatPrimary.APIKey)
	}
	if cfg.ContextTokenBudget != 16000 {
		t.Errorf("budget override failed: %d", cfg.ContextTokenBudget)
	}
}

func TestLoadPlatformsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "platforms.json")
	content := `{"platforms":[{"name":"Test","sizes":[{"name":"S","width":100,"height":200,"orientation":"portrait"}]}]}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Platforms) != 1 || cfg.Platforms[0].Name != "Test" {
		t.Fatalf("unexpected platforms: %+v", cfg.Platforms)
	}
	s := cfg.Platforms[0].Sizes[0]
	if s.Width != 100 || s.Height != 200 || s.Orientation != "portrait" {
		t.Errorf("unexpected size: %+v", s)
	}
}

func TestLoadPlatformsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for malformed platforms file")
	}
}
