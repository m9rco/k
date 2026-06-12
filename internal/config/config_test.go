package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Clear env that could leak from the host.
	for _, k := range []string{"ADDR", "CHAT_PRIMARY_MODEL", "CHAT_PRIMARY_PROVIDER", "CHAT_PRIMARY_BASE_URL", "USE_TEST_MODEL", "CONTEXT_TOKEN_BUDGET", "YUNWU_BASE_URL"} {
		t.Setenv(k, "")
	}
	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.ChatPrimary.Model != "deepseek-v4-flash" {
		t.Errorf("ChatPrimary.Model = %q, want deepseek-v4-flash", cfg.ChatPrimary.Model)
	}
	if cfg.ChatPrimary.Provider != "openai" {
		t.Errorf("ChatPrimary.Provider = %q, want openai", cfg.ChatPrimary.Provider)
	}
	if cfg.ChatPrimary.BaseURL != "https://yunwu.ai/v1" {
		t.Errorf("ChatPrimary.BaseURL = %q, want yunwu default", cfg.ChatPrimary.BaseURL)
	}
	if cfg.ImagePrimary.Model != "gpt-image-2" {
		t.Errorf("ImagePrimary.Model = %q, want gpt-image-2", cfg.ImagePrimary.Model)
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
	t.Setenv("CHAT_PRIMARY_API_KEY", "sk-test")
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

func TestLoadSharedYunwuKey(t *testing.T) {
	// A single YUNWU_API_KEY should fan out to every backend that lacks a
	// dedicated credential.
	for _, k := range []string{"CHAT_PRIMARY_API_KEY", "CHAT_TEST_API_KEY", "DEEPSEEK_API_KEY", "IMAGE_PRIMARY_API_KEY", "IMAGE_BACKUP_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("YUNWU_API_KEY", "sk-shared")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for name, got := range map[string]string{
		"ChatPrimary":  cfg.ChatPrimary.APIKey,
		"ChatTest":     cfg.ChatTest.APIKey,
		"ImagePrimary": cfg.ImagePrimary.APIKey,
		"ImageBackup":  cfg.ImageBackup.APIKey,
	} {
		if got != "sk-shared" {
			t.Errorf("%s.APIKey = %q, want shared sk-shared", name, got)
		}
	}

	// A dedicated key still wins over the shared one.
	t.Setenv("CHAT_PRIMARY_API_KEY", "sk-dedicated")
	cfg, err = Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatPrimary.APIKey != "sk-dedicated" {
		t.Errorf("dedicated key should win: got %q", cfg.ChatPrimary.APIKey)
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

// TestChannelCatalogValid loads the real configs/channels.json and asserts the
// catalog is well-formed: ids globally unique, dimensions positive, and that
// producible sizes can actually be cropped.
func TestChannelCatalogValid(t *testing.T) {
	channels, err := loadChannels("../../configs/channels.json", nil)
	if err != nil {
		t.Fatalf("loadChannels: %v", err)
	}
	if len(channels) == 0 {
		t.Fatal("expected a non-empty channel catalog")
	}
	seen := make(map[string]string) // size id -> "channel/assetType"
	var producible int
	for _, ch := range channels {
		if ch.ID == "" || ch.Name == "" {
			t.Errorf("channel missing id/name: %+v", ch)
		}
		for _, at := range ch.AssetTypes {
			for _, sz := range at.Sizes {
				if sz.ID == "" {
					t.Errorf("size missing id in %s/%s: %+v", ch.ID, at.Type, sz)
					continue
				}
				if prev, dup := seen[sz.ID]; dup {
					t.Errorf("duplicate size id %q (in %s/%s and %s)", sz.ID, ch.ID, at.Type, prev)
				}
				seen[sz.ID] = ch.ID + "/" + at.Type
				if sz.Width <= 0 || sz.Height <= 0 {
					t.Errorf("size %q has non-positive dimensions %dx%d", sz.ID, sz.Width, sz.Height)
				}
				if sz.Producible {
					producible++
				}
			}
		}
	}
	if producible == 0 {
		t.Error("expected at least some producible sizes")
	}
}

func TestPlatformsToChannelsLegacy(t *testing.T) {
	legacy := []Platform{{
		Name: "Universal",
		Sizes: []Size{
			{Name: "Square", Width: 1080, Height: 1080, Orientation: "square"},
		},
	}}
	channels := platformsToChannels(legacy)
	if len(channels) != 1 || channels[0].ID != "universal" {
		t.Fatalf("unexpected channels: %+v", channels)
	}
	sz := channels[0].AssetTypes[0].Sizes[0]
	if sz.ID != "universal.square" {
		t.Errorf("derived id = %q, want universal.square", sz.ID)
	}
	if !sz.Producible {
		t.Error("legacy sizes should be producible")
	}
}
