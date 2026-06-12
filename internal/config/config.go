// Package config centralizes runtime configuration for the asset studio server.
//
// Secrets (API keys) and endpoints are read from environment variables so that
// nothing sensitive is committed to source. Platform crop sizes are loaded from
// a data-driven JSON file (configs/platforms.json by default) so that they can
// be edited without recompiling. All values have sensible defaults for local
// development.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ModelConfig describes a single LLM/provider endpoint.
type ModelConfig struct {
	// Provider identifies the backend kind: "anthropic", "openai", "deepseek".
	Provider string
	// Model is the exact model id passed to the provider.
	Model string
	// BaseURL is the API base URL (empty = provider default).
	BaseURL string
	// APIKey is read from the environment; never hardcoded.
	APIKey string
}

// ImageProviderConfig describes one image-generation backend (gpt-image-1).
type ImageProviderConfig struct {
	// Name is a human-readable identifier recorded on produced assets.
	Name    string
	BaseURL string
	APIKey  string
	Model   string
}

// Size is one platform crop preset.
type Size struct {
	Name        string `json:"name"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Orientation string `json:"orientation"`
}

// Platform groups crop sizes under a platform label.
type Platform struct {
	Name  string `json:"name"`
	Sizes []Size `json:"sizes"`
}

// PlatformConfig is the root of configs/platforms.json.
type PlatformConfig struct {
	Platforms []Platform `json:"platforms"`
}

// Config is the fully resolved server configuration.
type Config struct {
	// Addr is the HTTP listen address.
	Addr string

	// ChatPrimary is the main conversation-understanding model.
	ChatPrimary ModelConfig
	// ChatTest is the cheaper test model (DeepSeek via OpenAI-compatible).
	ChatTest ModelConfig
	// UseTestModel switches the agent to ChatTest when true.
	UseTestModel bool

	// ImagePrimary and ImageBackup are the two gpt-image-1 providers.
	ImagePrimary ImageProviderConfig
	ImageBackup  ImageProviderConfig

	// DBPath is the SQLite file location.
	DBPath string
	// AssetDir is where generated/cropped image files are stored on disk.
	AssetDir string
	// AssetRetentionHours controls how long produced assets are kept (0 = keep forever).
	AssetRetentionHours int

	// Platforms holds the data-driven crop presets.
	Platforms []Platform

	// ContextTokenBudget bounds the conversation sliding window.
	ContextTokenBudget int
}

// env returns the environment value for key, or def when unset/empty.
func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envInt parses an integer env var, falling back to def on missing/invalid input.
func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envBool parses a boolean env var (1/true/yes), falling back to def.
func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// Load resolves configuration from the environment and the platform JSON file.
//
// platformsPath may be empty, in which case CONFIG_PLATFORMS or the default
// "configs/platforms.json" is used. A missing platforms file is not fatal: a
// built-in universal preset is used instead.
func Load(platformsPath string) (*Config, error) {
	cfg := &Config{
		Addr: env("ADDR", ":8080"),

		ChatPrimary: ModelConfig{
			Provider: env("CHAT_PRIMARY_PROVIDER", "anthropic"),
			Model:    env("CHAT_PRIMARY_MODEL", "claude-opus-4-8"),
			BaseURL:  env("CHAT_PRIMARY_BASE_URL", ""),
			APIKey:   env("ANTHROPIC_API_KEY", ""),
		},
		ChatTest: ModelConfig{
			Provider: env("CHAT_TEST_PROVIDER", "deepseek"),
			Model:    env("CHAT_TEST_MODEL", "deepseek-chat"),
			BaseURL:  env("CHAT_TEST_BASE_URL", "https://api.deepseek.com/v1"),
			APIKey:   env("DEEPSEEK_API_KEY", ""),
		},
		UseTestModel: envBool("USE_TEST_MODEL", false),

		ImagePrimary: ImageProviderConfig{
			Name:    env("IMAGE_PRIMARY_NAME", "primary"),
			BaseURL: env("IMAGE_PRIMARY_BASE_URL", ""),
			APIKey:  env("IMAGE_PRIMARY_API_KEY", ""),
			Model:   env("IMAGE_PRIMARY_MODEL", "gpt-image-1"),
		},
		ImageBackup: ImageProviderConfig{
			Name:    env("IMAGE_BACKUP_NAME", "backup"),
			BaseURL: env("IMAGE_BACKUP_BASE_URL", ""),
			APIKey:  env("IMAGE_BACKUP_API_KEY", ""),
			Model:   env("IMAGE_BACKUP_MODEL", "gpt-image-1"),
		},

		DBPath:              env("DB_PATH", "data/asset-studio.db"),
		AssetDir:            env("ASSET_DIR", "data/assets"),
		AssetRetentionHours: envInt("ASSET_RETENTION_HOURS", 0),

		ContextTokenBudget: envInt("CONTEXT_TOKEN_BUDGET", 8000),
	}

	path := platformsPath
	if path == "" {
		path = env("CONFIG_PLATFORMS", "configs/platforms.json")
	}
	platforms, err := loadPlatforms(path)
	if err != nil {
		return nil, err
	}
	cfg.Platforms = platforms

	return cfg, nil
}

// loadPlatforms reads the platform JSON file. A missing file falls back to a
// built-in universal preset; a malformed file is an error.
func loadPlatforms(path string) ([]Platform, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultPlatforms(), nil
		}
		return nil, fmt.Errorf("read platforms file %q: %w", path, err)
	}
	var pc PlatformConfig
	if err := json.Unmarshal(data, &pc); err != nil {
		return nil, fmt.Errorf("parse platforms file %q: %w", path, err)
	}
	if len(pc.Platforms) == 0 {
		return defaultPlatforms(), nil
	}
	return pc.Platforms, nil
}

// defaultPlatforms is the built-in fallback preset used when no config exists.
func defaultPlatforms() []Platform {
	return []Platform{
		{
			Name: "Universal",
			Sizes: []Size{
				{Name: "Square", Width: 1080, Height: 1080, Orientation: "square"},
				{Name: "Landscape 16:9", Width: 1920, Height: 1080, Orientation: "landscape"},
				{Name: "Portrait 9:16", Width: 1080, Height: 1920, Orientation: "portrait"},
			},
		},
	}
}
