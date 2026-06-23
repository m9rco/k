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
	// Thinking enables Anthropic extended thinking. Default off: many proxies and
	// some model ids reject the thinking field or change tool-calling behavior
	// when it is set, so it must be opted into explicitly.
	Thinking bool
	// OpenAIInfer injects the taiji-private "openai_infer": true field into the
	// chat-completions body. taiji's DeepSeek-*-Online models otherwise route
	// every "needs external data" turn through their built-in web search and
	// never emit tool_calls, which breaks our tool-driven agent; this flag flips
	// them onto the standard OpenAI function-calling protocol. It is a
	// non-standard field and some gateways (e.g. yunwu) hang when they receive it,
	// so it must only be set for taiji-backed models. The default is derived from
	// the provider (see ProviderProfile / ProfileForProvider): provider "taiji"
	// turns it on, every other provider leaves it off. An env override
	// (CHAT_*_OPENAI_INFER) can force either way per deployment.
	OpenAIInfer bool
}

// ImageProviderConfig describes one image-generation backend (gpt-image-1).
type ImageProviderConfig struct {
	// Name is a human-readable identifier recorded on produced assets.
	Name string
	// Provider identifies the backend kind. Reserved: image/video transports are
	// currently OpenAI-compatible only and do not branch on it, but the field is
	// resolved per-model (with common fallback) so a future provider swap needs
	// no struct change. Mirrors ModelConfig.Provider.
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
}

// COSConfig describes a Tencent Cloud COS bucket used to publish assets to a
// public URL. All fields must be present for it to be considered configured.
type COSConfig struct {
	SecretID        string
	SecretKey       string
	Region          string // e.g. ap-guangzhou
	Bucket          string // e.g. t-gz-1252130512
	BasePath        string // key prefix, e.g. mlab-linux-1
	PublicURLPrefix string // e.g. https://s.0x06.cn (CDN/custom domain)
}

// Configured reports whether the COS uploader has enough to operate.
func (c COSConfig) Configured() bool {
	return c.SecretID != "" && c.SecretKey != "" && c.Region != "" && c.Bucket != "" && c.PublicURLPrefix != ""
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
	// ConvergeMode optionally pins how platform adaptation converges the AI
	// product down to this exact size: "contain" (pad, never crop) or "cover"
	// (fill, crop overflow). Empty = auto: contain when the generated ratio is
	// close to the target, cover when it diverges too far (avoids large padding
	// on extreme banners). Lets the catalog pre-set known-extreme specs.
	ConvergeMode string `json:"convergeMode,omitempty"`
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

	// chatCommon is the shared (yunwu/common) chat gateway credential. Catalog
	// chat models whose provider is NOT standalone (openai/anthropic/deepseek —
	// all proxied by yunwu) resolve their base_url/api_key from here, so they stay
	// usable regardless of which model ChatPrimary points at. Resolved once in
	// Load from COMMON_*/YUNWU_*; unexported because it's an internal routing
	// detail, not a server-facing knob.
	chatCommon endpointCred
	// chatDedicated holds per-provider chat credentials keyed by lowercased
	// provider name, populated from dedicated <PROVIDER>_API_KEY/_BASE_URL env
	// vars (e.g. TAIJI_API_KEY). A standalone provider (taiji) that is not the
	// primary model still becomes selectable when its dedicated credential is set.
	chatDedicated map[string]endpointCred

	// ImagePrimary and ImageBackup are the two gpt-image-1 providers.
	ImagePrimary ImageProviderConfig
	ImageBackup  ImageProviderConfig

	// ImageOutpaint is the provider used for the outpaint convergence step in
	// extreme-ratio platform adaptation: a product padded to the target ratio
	// with transparent margins is handed to this model to fill the margins by
	// extending the scene (e.g. a 2:1 product → 4:1 banner without dead bands).
	// Defaults to provider=gemini (image models that extend cleanly). When its
	// APIKey is unset the outpaint path falls back to band padding (ModeContain).
	ImageOutpaint ImageProviderConfig

	// TextToImage is the pure text-to-image provider (wan/qwen). When its APIKey
	// is unset the text-to-image capability stays disabled and its agent tool is
	// left out of the whitelist.
	TextToImage ImageProviderConfig

	// Vision is the marketing-analysis vision backend. Default provider "gemini"
	// (gemini-flash-latest over the native :generateContent inline API, so the
	// analysis no longer requires publishing the image to a public URL). Provider
	// "openai" falls back to the legacy OpenAI-compatible /chat/completions +
	// image_url path (still needs COS). Credentials fall back to COMMON_*.
	Vision ImageProviderConfig

	// LayerSplit is the 图层精修 subject-detection/segmentation backend, kept
	// SEPARATE from Vision so marketing-analysis can run on an image-output model
	// (e.g. gemini-3-pro-image) while layer splitting uses an analysis/segmentation
	// model that returns JSON (boxes + masks). MUST be a vision-analysis model, NOT
	// an image-generation model — a generation model hangs the :generateContent
	// call (it tries to paint instead of returning JSON). Defaults to gemini-2.5-pro
	// over the native inline API; credentials fall back to COMMON_*.
	LayerSplit ImageProviderConfig

	// Quality is the platform-adaptation quality-gate judge backend
	// (doubao-seed-1-6-vision-250815 over OpenAI-compatible /chat/completions with
	// inline data-URI images). When its APIKey is unset the quality gate is
	// disabled (every adapt product is treated as passing) so behavior matches the
	// pre-gate flow.
	Quality ImageProviderConfig
	// QualityThreshold is the weighted-total score (0-100) at/above which an adapt
	// product passes the quality gate (compliance is a separate hard red line).
	QualityThreshold int
	// KeyElementsFidelityMin is the minimum key_elements_fidelity score (0-100)
	// below which an adapt product fails regardless of the weighted total (hard red
	// line). 0 disables the check and restores pre-feature behaviour.
	KeyElementsFidelityMin int
	// QualityMaxRetry is the maximum number of regeneration attempts after a
	// quality-gate failure (default 2). Set via QUALITY_MAX_RETRY.
	QualityMaxRetry int
	// VideoPromptLLMModel is the LLM used to enrich video motion prompts.
	// Defaults to claude-haiku-4-5-20251001. Set via VIDEO_PROMPT_LLM_MODEL.
	VideoPromptLLMModel string
	// PixelBlurThreshold is the Laplacian-variance lower bound for the pixel
	// pre-filter. Adapt products below this are flagged blurry and regenerated
	// once before the AI judge. 0 disables blur detection.
	PixelBlurThreshold int
	// PixelBorderMaxRatio is the maximum fraction of edge width/height that may
	// be a uniform-color band. Exceeding it flags the product and triggers
	// regeneration. 0 disables border detection.
	PixelBorderMaxRatio float64

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
	// AssetPublicBase is the public https base URL under which asset files are
	// reachable from the internet (e.g. https://studio.example.com). Required for
	// image-to-video: the happyhorse provider fetches the source image by URL, so
	// without a public base the video capability cannot work and stays disabled.
	AssetPublicBase string
	// AssetRetentionHours controls how long produced assets are kept (0 = keep forever).
	AssetRetentionHours int

	// COS is the Tencent Cloud Object Storage config used to publish a source
	// image to a public URL before image-to-video (the provider fetches by URL).
	// When unset, the video capability stays disabled.
	COS COSConfig

	// Platforms holds the data-driven crop presets.
	//
	// Deprecated: retained for backward compatibility. Use Channels.
	Platforms []Platform

	// Channels is the three-level crop catalog (channel → asset type → size).
	Channels []Channel

	// ContextTokenBudget bounds the conversation sliding window.
	ContextTokenBudget int

	// Log configures diagnostic logging (file destination, level, stderr mirror).
	Log LogConfig
}

// LogConfig configures the structured diagnostic logger. An empty File keeps the
// historical behaviour (stderr only). MirrorStderr additionally echoes every
// record to stderr (handy during local development) while the file stays pure
// JSON for jq/grep post-mortems.
type LogConfig struct {
	// File is the JSON log destination path. Empty => stderr only.
	File string
	// Level is the minimum level: debug | info | warn | error. Default info.
	Level string
	// MirrorStderr echoes records to stderr in addition to the file.
	MirrorStderr bool
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

// commonDefaults holds the shared, semantically-neutral fallback credentials
// that any model backend inherits when it lacks a dedicated override. The
// legacy YUNWU_* vars are honored as aliases (below COMMON_*, above built-in
// defaults) so existing deployments keep working without renaming anything.
type commonDefaults struct {
	provider string
	baseURL  string
	apiKey   string
}

// loadCommon resolves the common fallback triple once per Load.
func loadCommon() commonDefaults {
	return commonDefaults{
		provider: env("COMMON_PROVIDER", ""),
		baseURL:  envFirst("https://yunwu.ai/v1", "COMMON_BASE_URL", "YUNWU_BASE_URL"),
		apiKey:   envFirst("", "COMMON_API_KEY", "YUNWU_API_KEY"),
	}
}

// endpoint is the resolved provider/base_url/api_key/model quadruple for one
// model backend.
type endpoint struct {
	provider string
	baseURL  string
	apiKey   string
	model    string
}

// resolveEndpoint resolves one backend's config with per-field three-tier
// fallback: dedicated <PREFIX>_<FIELD> → common default → built-in default.
// Fields resolve independently, so a backend may override only its BASE_URL
// while still inheriting the common API_KEY. model has no common tier (model
// ids are backend-specific); aliasKeys are extra dedicated vars consulted
// before the common/default tiers (e.g. DEEPSEEK_API_KEY, HAPPYHORSE_*).
func (c commonDefaults) resolveEndpoint(prefix, defProvider, defModel string, aliases endpointAliases) endpoint {
	return endpoint{
		provider: envFirst(orDefault(c.provider, defProvider), append([]string{prefix + "_PROVIDER"}, aliases.provider...)...),
		baseURL:  envFirst(c.baseURL, append([]string{prefix + "_BASE_URL"}, aliases.baseURL...)...),
		apiKey:   envFirst(c.apiKey, append([]string{prefix + "_API_KEY"}, aliases.apiKey...)...),
		model:    envFirst(defModel, append([]string{prefix + "_MODEL"}, aliases.model...)...),
	}
}

// endpointAliases lists legacy env-var names to consult, per field, between the
// dedicated <PREFIX>_<FIELD> var and the common/default tiers.
type endpointAliases struct {
	provider []string
	baseURL  []string
	apiKey   []string
	model    []string
}

// orDefault returns v when non-empty, else def.
func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// Load resolves configuration from the environment and the platform JSON file.
//
// platformsPath may be empty, in which case CONFIG_PLATFORMS or the default
// "configs/platforms.json" is used. A missing platforms file is not fatal: a
// built-in universal preset is used instead.
func Load(platformsPath string) (*Config, error) {
	// Resolve the shared fallback credentials once. Each model backend below
	// inherits these unless it sets its own dedicated *_PROVIDER/*_BASE_URL/
	// *_API_KEY var, so swapping one model to a new provider needs only that
	// model's overrides (design: per-model provider config).
	common := loadCommon()

	chatPrimary := common.resolveEndpoint("CHAT_PRIMARY", "openai", "deepseek-v4-flash", endpointAliases{})
	chatTest := common.resolveEndpoint("CHAT_TEST", "openai", "claude-sonnet-4-5-20250929",
		endpointAliases{apiKey: []string{"DEEPSEEK_API_KEY"}})
	imagePrimary := common.resolveEndpoint("IMAGE_PRIMARY", "openai", "gpt-image-2", endpointAliases{})
	imageBackup := common.resolveEndpoint("IMAGE_BACKUP", "openai", "gpt-image-2", endpointAliases{})
	// Outpaint provider for extreme-ratio adaptation convergence. Defaults to
	// gemini (image models that extend a scene cleanly). No API key → the
	// outpaint path falls back to band padding, so adaptation still works.
	imageOutpaint := common.resolveEndpoint("IMAGE_OUTPAINT", "gemini", "gemini-3.1-flash-image", endpointAliases{})
	// Text-to-image (wan/qwen via DashScope async). Default provider "dashscope".
	textToImage := common.resolveEndpoint("TEXT_TO_IMAGE", "dashscope", "", endpointAliases{})
	// Vision marketing-analysis backend. Default provider "gemini" with the
	// gemini-flash-latest model over the native inline API (no COS upload). The
	// legacy OpenAI-compatible image_url path is selected with VISION_PROVIDER=openai.
	visionEP := common.resolveEndpoint("VISION", "gemini", "gemini-flash-latest", endpointAliases{})
	// 图层精修 subject detection/segmentation. SEPARATE from VISION so analysis can
	// run on an image-output model while splitting uses a JSON-returning vision
	// model. Default gemini-2.5-pro (native inline). MUST NOT be an image-generation
	// model (e.g. gemini-3-pro-image) — that hangs :generateContent. Credentials
	// fall back to COMMON_*.
	layerSplitEP := common.resolveEndpoint("LAYER_SPLIT", "gemini", "gemini-2.5-pro", endpointAliases{})
	// Quality-gate judge (doubao vision over OpenAI-compatible chat/completions).
	// No API key => the quality gate is disabled and adapt behaves as before.
	qualityEP := common.resolveEndpoint("QUALITY", "openai", "gemini-flash-latest", endpointAliases{})
	// Video canonicalizes on VIDEO_*; the older HAPPYHORSE_* names remain as
	// aliases (below VIDEO_* / COMMON_*) so existing deployments don't regress.
	video := common.resolveEndpoint("VIDEO", "openai", "", endpointAliases{
		baseURL: []string{"HAPPYHORSE_BASE_URL"},
		apiKey:  []string{"HAPPYHORSE_API_KEY"},
		model:   []string{"HAPPYHORSE_MODEL"},
	})
	// Crawl endpoint has NO common fallback: the model API base URL is meaningless
	// for crawling, so an unset endpoint means "not configured". Only the api key
	// inherits the common credential. CRAWL_PROVIDER is reserved (unused) for now.
	crawlEndpoint := envFirst("", "CRAWL_BASE_URL", "CRAWL_ENDPOINT")
	crawlAPIKey := envFirst(common.apiKey, "CRAWL_API_KEY")

	cfg := &Config{
		Addr: env("ADDR", ":8080"),

		ChatPrimary: ModelConfig{
			Provider: chatPrimary.provider,
			Model:    chatPrimary.model,
			BaseURL:  chatPrimary.baseURL,
			APIKey:   chatPrimary.apiKey,
			Thinking: envBool("CHAT_PRIMARY_THINKING", false),
			// Default comes from the provider profile (taiji => true) so picking the
			// provider is enough; the env var stays an explicit per-deployment override.
			OpenAIInfer: envBool("CHAT_PRIMARY_OPENAI_INFER", ProfileForProvider(chatPrimary.provider).OpenAIInfer),
		},
		ChatTest: ModelConfig{
			Provider:    chatTest.provider,
			Model:       chatTest.model,
			BaseURL:     chatTest.baseURL,
			APIKey:      chatTest.apiKey,
			Thinking:    envBool("CHAT_TEST_THINKING", false),
			OpenAIInfer: envBool("CHAT_TEST_OPENAI_INFER", ProfileForProvider(chatTest.provider).OpenAIInfer),
		},
		UseTestModel: envBool("USE_TEST_MODEL", false),

		ImagePrimary: ImageProviderConfig{
			Name:     env("IMAGE_PRIMARY_NAME", "primary"),
			Provider: imagePrimary.provider,
			BaseURL:  imagePrimary.baseURL,
			APIKey:   imagePrimary.apiKey,
			Model:    imagePrimary.model,
		},
		ImageBackup: ImageProviderConfig{
			Name:     env("IMAGE_BACKUP_NAME", "backup"),
			Provider: imageBackup.provider,
			BaseURL:  imageBackup.baseURL,
			APIKey:   imageBackup.apiKey,
			Model:    imageBackup.model,
		},

		// Outpaint convergence provider. APIKey empty => outpaint path falls back
		// to band padding (ModeContain) rather than failing.
		ImageOutpaint: ImageProviderConfig{
			Name:     env("IMAGE_OUTPAINT_NAME", "outpaint"),
			Provider: imageOutpaint.provider,
			BaseURL:  imageOutpaint.baseURL,
			APIKey:   imageOutpaint.apiKey,
			Model:    imageOutpaint.model,
		},

		// Text-to-image (wan/qwen). APIKey empty => capability stays disabled.
		TextToImage: ImageProviderConfig{
			Name:     env("TEXT_TO_IMAGE_NAME", "text2image"),
			Provider: textToImage.provider,
			BaseURL:  textToImage.baseURL,
			APIKey:   textToImage.apiKey,
			Model:    textToImage.model,
		},

		// Vision marketing-analysis backend. Defaults to gemini-flash-latest
		// (native inline, no COS). Credentials fall back to COMMON_*.
		Vision: ImageProviderConfig{
			Name:     env("VISION_NAME", "vision"),
			Provider: visionEP.provider,
			BaseURL:  visionEP.baseURL,
			APIKey:   visionEP.apiKey,
			Model:    visionEP.model,
		},

		// 图层精修 subject detection/segmentation backend, separate from Vision.
		LayerSplit: ImageProviderConfig{
			Name:     env("LAYER_SPLIT_NAME", "layer-split"),
			Provider: layerSplitEP.provider,
			BaseURL:  layerSplitEP.baseURL,
			APIKey:   layerSplitEP.apiKey,
			Model:    layerSplitEP.model,
		},

		// Quality-gate judge. APIKey empty => quality gate disabled.
		Quality: ImageProviderConfig{
			Name:     env("QUALITY_NAME", "quality"),
			Provider: qualityEP.provider,
			BaseURL:  qualityEP.baseURL,
			APIKey:   qualityEP.apiKey,
			Model:    qualityEP.model,
		},
		QualityThreshold:       envInt("QUALITY_THRESHOLD", 75),
		KeyElementsFidelityMin: envInt("KEY_ELEMENTS_FIDELITY_MIN", 60),
		QualityMaxRetry:        envInt("QUALITY_MAX_RETRY", 2),
		VideoPromptLLMModel:    env("VIDEO_PROMPT_LLM_MODEL", "claude-haiku-4-5-20251001"),
		PixelBlurThreshold:     envInt("PIXEL_BLUR_THRESHOLD", 80),
		PixelBorderMaxRatio: func() float64 {
			v := strings.TrimSpace(os.Getenv("PIXEL_BORDER_MAX_RATIO"))
			if v == "" {
				return 0.15
			}
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return f
			}
			return 0.15
		}(),

		// Image-to-video (happyhorse R2V). APIKey/Model empty means the video
		// capability reports "not configured" rather than failing.
		Video: ImageProviderConfig{
			Name:     env("VIDEO_NAME", "happyhorse"),
			Provider: video.provider,
			BaseURL:  video.baseURL,
			APIKey:   video.apiKey,
			Model:    video.model,
		},

		// Game-asset crawling. Empty endpoint => capability degrades gracefully.
		CrawlEndpoint: crawlEndpoint,
		CrawlAPIKey:   crawlAPIKey,

		DBPath:          env("DB_PATH", "data/asset-studio.db"),
		AssetDir:        env("ASSET_DIR", "data/assets"),
		AssetPublicBase: env("ASSET_PUBLIC_BASE", ""),
		COS: COSConfig{
			SecretID:        env("COS_SECRET_ID", ""),
			SecretKey:       env("COS_SECRET_KEY", ""),
			Region:          env("COS_REGION", ""),
			Bucket:          env("COS_BUCKET", ""),
			BasePath:        env("COS_BASE_PATH", ""),
			PublicURLPrefix: strings.TrimRight(env("COS_PUBLIC_URL_PREFIX", ""), "/"),
		},
		AssetRetentionHours: envInt("ASSET_RETENTION_HOURS", 0),

		ContextTokenBudget: envInt("CONTEXT_TOKEN_BUDGET", 8000),

		Log: LogConfig{
			File:         env("LOG_FILE", "data/logs/app.log"),
			Level:        env("LOG_LEVEL", "info"),
			MirrorStderr: envBool("LOG_MIRROR_STDERR", false),
		},
	}

	// Per-provider chat credentials. chatCommon is the shared yunwu/common gateway
	// that proxies the standard providers (openai/anthropic/deepseek); standalone
	// providers (taiji) get a dedicated credential so they stay selectable even
	// when they aren't the primary chat model. Resolving these here lets a single
	// chat scene mix gateways (catalog.chatCredentialFor) instead of forcing every
	// chat model onto ChatPrimary's single credential.
	cfg.chatCommon = endpointCred{baseURL: common.baseURL, apiKey: common.apiKey}
	cfg.chatDedicated = map[string]endpointCred{
		"taiji": {
			baseURL: env("TAIJI_BASE_URL", ""),
			apiKey:  env("TAIJI_API_KEY", ""),
		},
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

// VisionCredential returns the base URL and API key for the vision analysis
// service (grok-4-fast via yunwu common gateway).
func (c *Config) VisionCredential() (baseURL, apiKey string) {
	return c.chatCommon.baseURL, c.chatCommon.apiKey
}
