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

// clearProviderEnv resets every dedicated/common/alias provider var so a test
// starts from a known-empty baseline regardless of host environment.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"COMMON_PROVIDER", "COMMON_BASE_URL", "COMMON_API_KEY",
		"YUNWU_BASE_URL", "YUNWU_API_KEY", "DEEPSEEK_API_KEY",
		"CHAT_PRIMARY_PROVIDER", "CHAT_PRIMARY_BASE_URL", "CHAT_PRIMARY_API_KEY", "CHAT_PRIMARY_MODEL",
		"CHAT_TEST_PROVIDER", "CHAT_TEST_BASE_URL", "CHAT_TEST_API_KEY", "CHAT_TEST_MODEL",
		"IMAGE_PRIMARY_PROVIDER", "IMAGE_PRIMARY_BASE_URL", "IMAGE_PRIMARY_API_KEY", "IMAGE_PRIMARY_MODEL",
		"IMAGE_BACKUP_PROVIDER", "IMAGE_BACKUP_BASE_URL", "IMAGE_BACKUP_API_KEY", "IMAGE_BACKUP_MODEL",
		"TEXT_TO_IMAGE_PROVIDER", "TEXT_TO_IMAGE_BASE_URL", "TEXT_TO_IMAGE_API_KEY", "TEXT_TO_IMAGE_MODEL",
		"VIDEO_PROVIDER", "VIDEO_BASE_URL", "VIDEO_API_KEY", "VIDEO_MODEL",
		"HAPPYHORSE_BASE_URL", "HAPPYHORSE_API_KEY", "HAPPYHORSE_MODEL",
		"CRAWL_PROVIDER", "CRAWL_BASE_URL", "CRAWL_API_KEY", "CRAWL_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
}

// TestCommonFallbackFanOut: with only COMMON_* set, every model backend inherits
// the shared credential and base URL.
func TestCommonFallbackFanOut(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("COMMON_API_KEY", "sk-common")
	t.Setenv("COMMON_BASE_URL", "https://common/v1")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for name, ep := range map[string]struct{ url, key string }{
		"ChatPrimary":  {cfg.ChatPrimary.BaseURL, cfg.ChatPrimary.APIKey},
		"ChatTest":     {cfg.ChatTest.BaseURL, cfg.ChatTest.APIKey},
		"ImagePrimary": {cfg.ImagePrimary.BaseURL, cfg.ImagePrimary.APIKey},
		"ImageBackup":  {cfg.ImageBackup.BaseURL, cfg.ImageBackup.APIKey},
		"Video":        {cfg.Video.BaseURL, cfg.Video.APIKey},
	} {
		if ep.url != "https://common/v1" {
			t.Errorf("%s.BaseURL = %q, want common", name, ep.url)
		}
		if ep.key != "sk-common" {
			t.Errorf("%s.APIKey = %q, want common", name, ep.key)
		}
	}
}

// TestDedicatedOverridesCommon: a single backend's dedicated var wins; others
// still inherit the common value.
func TestDedicatedOverridesCommon(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("COMMON_API_KEY", "sk-common")
	t.Setenv("IMAGE_PRIMARY_API_KEY", "sk-image")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ImagePrimary.APIKey != "sk-image" {
		t.Errorf("ImagePrimary.APIKey = %q, want sk-image", cfg.ImagePrimary.APIKey)
	}
	if cfg.ImageBackup.APIKey != "sk-common" {
		t.Errorf("ImageBackup.APIKey = %q, want sk-common", cfg.ImageBackup.APIKey)
	}
}

// TestFieldLevelPartialOverride: overriding only BASE_URL leaves API_KEY on the
// common fallback (fields resolve independently).
func TestFieldLevelPartialOverride(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("COMMON_API_KEY", "sk-common")
	t.Setenv("COMMON_BASE_URL", "https://common/v1")
	t.Setenv("CHAT_PRIMARY_BASE_URL", "https://dedicated/v1")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatPrimary.BaseURL != "https://dedicated/v1" {
		t.Errorf("BaseURL = %q, want dedicated", cfg.ChatPrimary.BaseURL)
	}
	if cfg.ChatPrimary.APIKey != "sk-common" {
		t.Errorf("APIKey = %q, want common (field-level fallback)", cfg.ChatPrimary.APIKey)
	}
}

// TestProviderPerModelOverride: provider is overridable per model and via common.
func TestProviderPerModelOverride(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("CHAT_PRIMARY_PROVIDER", "anthropic")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatPrimary.Provider != "anthropic" {
		t.Errorf("ChatPrimary.Provider = %q, want anthropic", cfg.ChatPrimary.Provider)
	}
	if cfg.ChatTest.Provider != "openai" {
		t.Errorf("ChatTest.Provider = %q, want built-in openai", cfg.ChatTest.Provider)
	}

	t.Setenv("COMMON_PROVIDER", "anthropic")
	t.Setenv("CHAT_PRIMARY_PROVIDER", "")
	cfg, err = Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ImagePrimary.Provider != "anthropic" {
		t.Errorf("ImagePrimary.Provider = %q, want common anthropic", cfg.ImagePrimary.Provider)
	}
}

// TestCommonWinsOverYunwuAlias: COMMON_* takes precedence over the legacy alias.
func TestCommonWinsOverYunwuAlias(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("YUNWU_API_KEY", "sk-yunwu")
	t.Setenv("COMMON_API_KEY", "sk-common")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatPrimary.APIKey != "sk-common" {
		t.Errorf("APIKey = %q, want common over yunwu alias", cfg.ChatPrimary.APIKey)
	}
}

// TestHappyhorseAliasMapsToVideo: legacy HAPPYHORSE_* feed the video backend.
func TestHappyhorseAliasMapsToVideo(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("HAPPYHORSE_API_KEY", "sk-hh")
	t.Setenv("HAPPYHORSE_MODEL", "happyhorse-1.0-r2v")
	t.Setenv("HAPPYHORSE_BASE_URL", "https://hh/api")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Video.APIKey != "sk-hh" {
		t.Errorf("Video.APIKey = %q, want sk-hh", cfg.Video.APIKey)
	}
	if cfg.Video.Model != "happyhorse-1.0-r2v" {
		t.Errorf("Video.Model = %q, want happyhorse-1.0-r2v", cfg.Video.Model)
	}
	if cfg.Video.BaseURL != "https://hh/api" {
		t.Errorf("Video.BaseURL = %q, want hh", cfg.Video.BaseURL)
	}

	// VIDEO_* canonical wins over the HAPPYHORSE_* alias.
	t.Setenv("VIDEO_API_KEY", "sk-video")
	cfg, err = Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Video.APIKey != "sk-video" {
		t.Errorf("Video.APIKey = %q, want VIDEO_* over alias", cfg.Video.APIKey)
	}
}

// TestCrawlInheritsCommonKeyButNotURL: crawl api key inherits COMMON_API_KEY,
// but the endpoint has no common fallback (unset => not configured).
func TestCrawlInheritsCommonKeyButNotURL(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("COMMON_API_KEY", "sk-common")
	t.Setenv("COMMON_BASE_URL", "https://common/v1")
	t.Setenv("CRAWL_BASE_URL", "https://crawl/search")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CrawlAPIKey != "sk-common" {
		t.Errorf("CrawlAPIKey = %q, want common", cfg.CrawlAPIKey)
	}
	if cfg.CrawlEndpoint != "https://crawl/search" {
		t.Errorf("CrawlEndpoint = %q, want crawl base url", cfg.CrawlEndpoint)
	}

	// Without CRAWL_BASE_URL/ENDPOINT the endpoint stays empty despite COMMON_BASE_URL.
	t.Setenv("CRAWL_BASE_URL", "")
	cfg, err = Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CrawlEndpoint != "" {
		t.Errorf("CrawlEndpoint = %q, want empty (no common URL fallback)", cfg.CrawlEndpoint)
	}
}

// TestCrawlEndpointAlias: legacy CRAWL_ENDPOINT still feeds the endpoint.
func TestCrawlEndpointAlias(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("CRAWL_ENDPOINT", "https://legacy/search")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CrawlEndpoint != "https://legacy/search" {
		t.Errorf("CrawlEndpoint = %q, want legacy alias", cfg.CrawlEndpoint)
	}
}

// TestTextToImageDefaults: text-to-image defaults to the dashscope provider and
// inherits the common api key.
func TestTextToImageDefaults(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("COMMON_API_KEY", "sk-common")

	cfg, err := Load("does-not-exist.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TextToImage.Provider != "dashscope" {
		t.Errorf("TextToImage.Provider = %q, want dashscope", cfg.TextToImage.Provider)
	}
	if cfg.TextToImage.APIKey != "sk-common" {
		t.Errorf("TextToImage.APIKey = %q, want common", cfg.TextToImage.APIKey)
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
