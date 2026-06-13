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

// Size is one crop preset. Beyond pixel dimensions it carries optional
// constraint metadata (format/maxKB/note) that is surfaced to the UI and the
// agent as hints only — cropping never enforces them. ID is globally unique
// across the whole catalog so cropping can be addressed without name clashes
// (the same 512×512 ICON appears under many channels).
type Size struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Orientation string `json:"orientation"`
	// Format is the recommended output format (png/jpg/gif); empty = unspecified.
	Format string `json:"format,omitempty"`
	// MaxKB is the channel's file-size ceiling in KB; 0 = no limit.
	MaxKB int `json:"maxKB,omitempty"`
	// Note carries free-form requirements (e.g. "无文案", "圆角", "透明底").
	Note string `json:"note,omitempty"`
	// Producible marks whether pure cropping can produce this size. Video specs
	// and external-link specs set this false so the UI greys them out and the
	// crop tool rejects them.
	Producible bool `json:"producible"`
}

// AssetType groups sizes by their use (screenshot, icon, cover, banner, ...)
// within a single channel.
type AssetType struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Sizes []Size `json:"sizes"`
}

// Channel is one distribution channel (TapTap, B站, 华为, ...) holding its
// asset types. Group is a coarse bucket for the UI (外渠/手机厂商/腾讯内渠/PC).
type Channel struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Group      string      `json:"group"`
	AssetTypes []AssetType `json:"assetTypes"`
}

// ChannelConfig is the root of configs/channels.json.
type ChannelConfig struct {
	Channels []Channel `json:"channels"`
}

// Platform groups crop sizes under a platform label.
//
// Deprecated: retained for backward compatibility with the legacy two-level
// configs/platforms.json. The catalog is now the three-level Channel structure;
// new code should use Config.Channels.
type Platform struct {
	Name  string `json:"name"`
	Sizes []Size `json:"sizes"`
}

// PlatformConfig is the root of the legacy configs/platforms.json.
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

	// Video is the image-to-video provider (happyhorse R2V). It may be unset, in
	// which case the video capability degrades to "not configured".
	Video ImageProviderConfig

	// Crawl configures the game-asset crawling source. CrawlEndpoint empty means
	// the crawl capability degrades to "not configured".
	CrawlEndpoint string
	CrawlAPIKey   string

	// DBPath is the SQLite file location.
	DBPath string
	// AssetDir is where generated/cropped image files are stored on disk.
	AssetDir string
	// AssetRetentionHours controls how long produced assets are kept (0 = keep forever).
	AssetRetentionHours int

	// Platforms holds the data-driven crop presets.
	//
	// Deprecated: retained for backward compatibility. Use Channels.
	Platforms []Platform

	// Channels is the three-level crop catalog (channel → asset type → size).
	Channels []Channel

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

// envFirst returns the first non-empty environment value among keys, or def.
//
// This lets a service read its dedicated variable (e.g. CHAT_PRIMARY_API_KEY)
// while transparently falling back to a shared one (YUNWU_API_KEY) so a single
// proxy credential can power every backend during local development.
func envFirst(def string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
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
	// Shared proxy defaults: a single yunwu.ai credential and base URL can power
	// every OpenAI-compatible backend (chat + image) during development. Each
	// service may still override with its dedicated *_API_KEY / *_BASE_URL var.
	yunwuKey := env("YUNWU_API_KEY", "")
	yunwuBase := env("YUNWU_BASE_URL", "https://yunwu.ai/v1")

	cfg := &Config{
		Addr: env("ADDR", ":8080"),

		ChatPrimary: ModelConfig{
			Provider: env("CHAT_PRIMARY_PROVIDER", "openai"),
			Model:    env("CHAT_PRIMARY_MODEL", "deepseek-v4-flash"),
			BaseURL:  envFirst(yunwuBase, "CHAT_PRIMARY_BASE_URL"),
			APIKey:   envFirst(yunwuKey, "CHAT_PRIMARY_API_KEY"),
		},
		ChatTest: ModelConfig{
			Provider: env("CHAT_TEST_PROVIDER", "openai"),
			Model:    env("CHAT_TEST_MODEL", "claude-sonnet-4-5-20250929"),
			BaseURL:  envFirst(yunwuBase, "CHAT_TEST_BASE_URL"),
			APIKey:   envFirst(yunwuKey, "CHAT_TEST_API_KEY", "DEEPSEEK_API_KEY"),
		},
		UseTestModel: envBool("USE_TEST_MODEL", false),

		ImagePrimary: ImageProviderConfig{
			Name:    env("IMAGE_PRIMARY_NAME", "primary"),
			BaseURL: envFirst(yunwuBase, "IMAGE_PRIMARY_BASE_URL"),
			APIKey:  envFirst(yunwuKey, "IMAGE_PRIMARY_API_KEY"),
			Model:   env("IMAGE_PRIMARY_MODEL", "gpt-image-2"),
		},
		ImageBackup: ImageProviderConfig{
			Name:    env("IMAGE_BACKUP_NAME", "backup"),
			BaseURL: envFirst(yunwuBase, "IMAGE_BACKUP_BASE_URL"),
			APIKey:  envFirst(yunwuKey, "IMAGE_BACKUP_API_KEY"),
			Model:   env("IMAGE_BACKUP_MODEL", "gpt-image-2"),
		},

		// Image-to-video (happyhorse R2V). Reserved provider; APIKey/Model empty
		// means the video capability reports "not configured" rather than failing.
		Video: ImageProviderConfig{
			Name:    env("VIDEO_NAME", "happyhorse"),
			BaseURL: envFirst(env("HAPPYHORSE_BASE_URL", ""), "VIDEO_BASE_URL", yunwuBase),
			APIKey:  envFirst(env("HAPPYHORSE_API_KEY", ""), "VIDEO_API_KEY", yunwuKey),
			Model:   env("HAPPYHORSE_MODEL", ""),
		},

		// Game-asset crawling. Empty endpoint => capability degrades gracefully.
		CrawlEndpoint: env("CRAWL_ENDPOINT", ""),
		CrawlAPIKey:   env("CRAWL_API_KEY", ""),

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

	// Load the three-level channel catalog. A missing file falls back to
	// projecting the legacy platforms into single-asset-type channels so older
	// deployments keep working without a channels.json.
	channels, err := loadChannels(env("CONFIG_CHANNELS", "configs/channels.json"), platforms)
	if err != nil {
		return nil, err
	}
	cfg.Channels = channels

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

// loadChannels reads the three-level channel catalog. A missing file is not
// fatal: the supplied legacy platforms are projected into channels so behaviour
// is preserved. A malformed file is an error.
func loadChannels(path string, legacy []Platform) ([]Channel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return platformsToChannels(legacy), nil
		}
		return nil, fmt.Errorf("read channels file %q: %w", path, err)
	}
	var cc ChannelConfig
	if err := json.Unmarshal(data, &cc); err != nil {
		return nil, fmt.Errorf("parse channels file %q: %w", path, err)
	}
	if len(cc.Channels) == 0 {
		return platformsToChannels(legacy), nil
	}
	return cc.Channels, nil
}

// platformsToChannels projects legacy two-level platforms into the three-level
// channel structure, wrapping each platform's sizes under a single generic
// asset type. Sizes lacking an ID get one derived from the platform/size name
// so id-addressed cropping still works for legacy data.
func platformsToChannels(platforms []Platform) []Channel {
	channels := make([]Channel, 0, len(platforms))
	for _, p := range platforms {
		sizes := make([]Size, 0, len(p.Sizes))
		for _, s := range p.Sizes {
			if s.ID == "" {
				s.ID = slug(p.Name) + "." + slug(s.Name)
			}
			s.Producible = true // legacy presets are all crop-producible
			sizes = append(sizes, s)
		}
		channels = append(channels, Channel{
			ID:    slug(p.Name),
			Name:  p.Name,
			Group: "Universal",
			AssetTypes: []AssetType{{
				Type:  "general",
				Name:  "通用",
				Sizes: sizes,
			}},
		})
	}
	return channels
}

// slug converts a display name into a lowercase, dot/space-free identifier
// fragment suitable for composing size ids.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(" ", "-", ":", "-", "/", "-", "_", "-")
	return repl.Replace(s)
}
