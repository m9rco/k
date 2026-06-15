package config

import "strings"

// ModelScene identifies which capability a model serves. Used as the grouping
// key in the model catalog and as the preferences key suffix (model.<scene>).
type ModelScene string

const (
	SceneChat        ModelScene = "chat"          // 逻辑推理 / 主 agent
	SceneImage       ModelScene = "image"         // 图生图
	SceneTextToImage ModelScene = "text_to_image" // 文生图
	SceneVideo       ModelScene = "video"         // 图生视频
)

// AllScenes lists every switchable scene in display order.
var AllScenes = []ModelScene{SceneChat, SceneImage, SceneTextToImage, SceneVideo}

// CatalogEntry is one selectable model in the catalog. Provider is the adapter
// selection key (openai/taiji/anthropic/gemini/dashscope/veo/happyhorse); IconKey
// maps to a built-in vendor brand SVG on the frontend. taiji shares the
// OpenAI-compatible transport with "openai" but carries the openai_infer quirk
// via its ProviderProfile (see provider.go).
//
// ID is the stable logical key: it is what the frontend offers, what a session's
// preference stores, and how an entry is looked up. Model is the provider-facing
// "wire" name actually sent to the API. They differ when a gateway names a model
// differently from our stable id (e.g. taiji exposes DeepSeek V4 Flash as
// "DeepSeek-V4-Flash-Online-32k"); when Model is empty the ID doubles as the wire
// name. Keeping ID stable means changing a wire name never invalidates stored
// session selections.
type CatalogEntry struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"displayName"`
	Scene       ModelScene `json:"scene"`
	Vendor      string     `json:"vendor"`
	IconKey     string     `json:"iconKey"`
	Provider    string     `json:"-"` // resolution detail, not exposed to the client
	Model       string     `json:"-"` // provider wire name; falls back to ID when empty
	// BaseURL overrides the scene's shared base for this entry. Most models in a
	// scene share one endpoint (the scene credential), but some need their own —
	// e.g. Veo lives at the OpenAI-style ".../v1" host while happyhorse (same video
	// scene) lives under ".../alibailian". Empty means "use the scene credential".
	BaseURL string `json:"-"`
	// Aliases are additional provider wire names that map back to this same entry.
	// A single logical model is often exposed under several wire names — taiji's
	// DeepSeek V4 Flash ships as -Online-16k / -Online-32k context variants that
	// are the same catalog model. Listing them here lets catalogIDForModel reverse
	// a configured wire name (e.g. CHAT_PRIMARY_MODEL=...-16k) back to this id, so
	// the frontend's "default" badge matches regardless of which variant is set.
	// WireModel (the name actually SENT to the API) is unaffected — it stays the
	// canonical Model/ID; aliases only widen reverse lookup.
	Aliases []string `json:"-"`
}

// matchesWire reports whether model names this entry by any of its identifiers:
// the canonical wire name, the stable id, or a configured alias. Used by reverse
// lookup (configured wire name -> catalog id) so context-size variants of one
// logical model all resolve to the same entry.
func (e CatalogEntry) matchesWire(model string) bool {
	if model == e.WireModel() || model == e.ID {
		return true
	}
	for _, a := range e.Aliases {
		if model == a {
			return true
		}
	}
	return false
}

// WireModel returns the provider-facing model name to send to the API: the
// explicit Model override when set, else the stable ID.
func (e CatalogEntry) WireModel() string {
	if e.Model != "" {
		return e.Model
	}
	return e.ID
}

// modelCatalog is the static, server-authoritative list of known models. The
// frontend may only choose from the subset that AvailableModels reports as
// configured. Adding a model here (plus its credentials) makes it selectable.
var modelCatalog = []CatalogEntry{
	// 逻辑推理 (chat)
	{ID: "deepseek-v4-flash-tencent", DisplayName: "DeepSeek WOA（太极）", Scene: SceneChat, Vendor: "DeepSeek", IconKey: "deepseek", Provider: "taiji", Model: "DeepSeek-V4-Flash-Online-32k", Aliases: []string{"DeepSeek-V4-Flash-Online-16k"}},
	{ID: "deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Scene: SceneChat, Vendor: "DeepSeek", IconKey: "deepseek", Provider: "openai"},
	{ID: "gpt-5.4", DisplayName: "GPT-5.4", Scene: SceneChat, Vendor: "OpenAI", IconKey: "openai", Provider: "openai"},
	{ID: "doubao-seed-2-0-mini-260428", DisplayName: "Doubao Seed 2.0 mini", Scene: SceneChat, Vendor: "Doubao", IconKey: "doubao", Provider: "openai"},
	{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5", Scene: SceneChat, Vendor: "Anthropic", IconKey: "anthropic", Provider: "anthropic"},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Scene: SceneChat, Vendor: "Anthropic", IconKey: "anthropic", Provider: "anthropic"},

	// 图生图 (image)
	{ID: "gpt-image-2", DisplayName: "GPT Image 2", Scene: SceneImage, Vendor: "OpenAI", IconKey: "openai", Provider: "openai"},
	{ID: "gemini-3-pro-image", DisplayName: "Gemini 3 Pro Image", Scene: SceneImage, Vendor: "Google", IconKey: "google", Provider: "gemini"},
	{ID: "gemini-2.5-flash-image", DisplayName: "Gemini 2.5 Flash Image", Scene: SceneImage, Vendor: "Google", IconKey: "google", Provider: "gemini"},
	{ID: "gemini-3.1-flash-image", DisplayName: "Gemini 3.1 Flash Image", Scene: SceneImage, Vendor: "Google", IconKey: "google", Provider: "gemini"},
	{ID: "gemini-3.1-flash-image-preview", DisplayName: "Gemini 3.1 Flash Image (Preview)", Scene: SceneImage, Vendor: "Google", IconKey: "google", Provider: "gemini"},

	// 文生图 (text_to_image)
	{ID: "wan2.7-image-pro", DisplayName: "Wan 2.7 Image Pro", Scene: SceneTextToImage, Vendor: "Alibaba", IconKey: "alibaba", Provider: "dashscope"},
	{ID: "qwen-image-2.0-2026-03-03", DisplayName: "Qwen Image 2.0", Scene: SceneTextToImage, Vendor: "Alibaba", IconKey: "alibaba", Provider: "dashscope"},

	// 图生视频 (video)
	{ID: "happyhorse-1.0-r2v", DisplayName: "HappyHorse 1.0 R2V", Scene: SceneVideo, Vendor: "Alibaba", IconKey: "alibaba", Provider: "happyhorse"},
	{ID: "veo_3_1_fast_components_vip", DisplayName: "Veo 3.1 Fast", Scene: SceneVideo, Vendor: "Google", IconKey: "google", Provider: "veo", Model: "veo-3.1-fast", BaseURL: "https://yunwu.ai"},
	{ID: "veo_3_1_components_vip", DisplayName: "Veo 3.1", Scene: SceneVideo, Vendor: "Google", IconKey: "google", Provider: "veo", Model: "veo-3.1", BaseURL: "https://yunwu.ai"},
}

// endpointCred is a resolved base_url + api_key pair for one chat gateway.
type endpointCred struct {
	baseURL string
	apiKey  string
}

// chatCredentialFor resolves the gateway credential a chat catalog ENTRY should
// use, based on its provider — not on a single per-scene credential. This is what
// lets one chat scene mix gateways: a taiji entry hits taiji while openai/
// anthropic entries hit the shared yunwu gateway.
//
// Resolution order:
//  1. provider matches ChatPrimary's provider AND ChatPrimary has a key — use it
//     (the primary IS this provider, so its credential is authoritative);
//  2. a dedicated <PROVIDER>_* credential is configured — use it;
//  3. standalone providers (taiji) stop here: they must NOT fall back to the
//     common/yunwu gateway (which can't serve their models), so an unconfigured
//     standalone provider yields no credential and the entry is filtered out;
//  4. the shared/common (yunwu) credential — preferring an explicitly resolved
//     COMMON_*/YUNWU_* credential, and otherwise reusing ChatPrimary's credential
//     when the primary itself is a shared-gateway (non-standalone) provider, which
//     is the typical single-gateway deployment where primary==common.
//
// Returns ok=false when no usable credential exists for the entry.
func (c *Config) chatCredentialFor(provider string) (cred endpointCred, ok bool) {
	p := strings.ToLower(strings.TrimSpace(provider))
	primaryProv := strings.ToLower(strings.TrimSpace(c.ChatPrimary.Provider))
	if p == primaryProv && c.ChatPrimary.APIKey != "" {
		return endpointCred{baseURL: c.ChatPrimary.BaseURL, apiKey: c.ChatPrimary.APIKey}, true
	}
	if dc, has := c.chatDedicated[p]; has && dc.apiKey != "" {
		return dc, true
	}
	if ProfileForProvider(provider).Standalone {
		return endpointCred{}, false // never proxy a standalone provider via yunwu
	}
	if c.chatCommon.apiKey != "" {
		return c.chatCommon, true
	}
	// No separate common credential configured: when the primary is itself a
	// shared-gateway provider, its credential doubles as the common one.
	if c.ChatPrimary.APIKey != "" && !ProfileForProvider(c.ChatPrimary.Provider).Standalone {
		return endpointCred{baseURL: c.ChatPrimary.BaseURL, apiKey: c.ChatPrimary.APIKey}, true
	}
	return endpointCred{}, false
}

func (c *Config) sceneCredential(scene ModelScene) (baseURL, apiKey string) {
	switch scene {
	case SceneChat:
		return c.ChatPrimary.BaseURL, c.ChatPrimary.APIKey
	case SceneImage:
		return c.ImagePrimary.BaseURL, c.ImagePrimary.APIKey
	case SceneTextToImage:
		return c.TextToImage.BaseURL, c.TextToImage.APIKey
	case SceneVideo:
		return c.Video.BaseURL, c.Video.APIKey
	}
	return "", ""
}

// catalogIDForModel reverse-maps a configured model string to its stable catalog
// ID for the scene. The server's per-scene config (e.g. CHAT_PRIMARY_MODEL) holds
// the provider wire name, which may differ from the catalog ID; the frontend
// labels its default by catalog ID, so we match the configured value against
// each entry's wire name OR its id and return the id. Falls back to the raw
// value when no catalog entry matches, so an off-catalog default still surfaces.
func catalogIDForModel(scene ModelScene, model string) string {
	if model == "" {
		return ""
	}
	for _, e := range modelCatalog {
		if e.Scene == scene && e.matchesWire(model) {
			return e.ID
		}
	}
	return model
}

// SceneDefaultModel returns the server-preselected (default) catalog ID for a
// scene — the one used when a session has made no selection. The configured value
// (a provider wire name) is reverse-mapped to its catalog ID so the client, which
// works in catalog-id space, can label and match it. Returns "" when the scene
// has no configured default. Chat reflects the active default (test model when
// enabled).
func (c *Config) SceneDefaultModel(scene ModelScene) string {
	switch scene {
	case SceneChat:
		if c.UseTestModel {
			return catalogIDForModel(SceneChat, c.ChatTest.Model)
		}
		return catalogIDForModel(SceneChat, c.ChatPrimary.Model)
	case SceneImage:
		return catalogIDForModel(SceneImage, c.ImagePrimary.Model)
	case SceneTextToImage:
		return catalogIDForModel(SceneTextToImage, c.TextToImage.Model)
	case SceneVideo:
		return catalogIDForModel(SceneVideo, c.Video.Model)
	}
	return ""
}

// SceneDefaults returns the default model id per scene, for the client to label
// the "server preselected (default)" model in each scene group.
func (c *Config) SceneDefaults() map[ModelScene]string {
	out := make(map[ModelScene]string, len(AllScenes))
	for _, scene := range AllScenes {
		if id := c.SceneDefaultModel(scene); id != "" {
			out[scene] = id
		}
	}
	return out
}

// configured (non-empty api key). A scene with no api key yields no entries, so
// the frontend never offers a model that would fail on use.
func (c *Config) AvailableModels() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(modelCatalog))
	for _, e := range modelCatalog {
		if _, ok := c.entryCredential(e); ok {
			out = append(out, e)
		}
	}
	return out
}

// entryCredential resolves the base_url + api_key a specific catalog entry would
// use. Chat entries resolve per-provider (so a scene can mix taiji + yunwu);
// image-like scenes keep the single per-scene credential (one gateway per scene).
// ok=false means the entry has no usable credential and must be hidden/rejected.
func (c *Config) entryCredential(e CatalogEntry) (endpointCred, bool) {
	if e.Scene == SceneChat {
		cred, ok := c.chatCredentialFor(e.Provider)
		return cred, ok
	}
	base, key := c.sceneCredential(e.Scene)
	if key == "" {
		return endpointCred{}, false
	}
	return endpointCred{baseURL: base, apiKey: key}, true
}

// AvailableModelsByScene groups AvailableModels by scene for the API/UI.
func (c *Config) AvailableModelsByScene() map[ModelScene][]CatalogEntry {
	grouped := make(map[ModelScene][]CatalogEntry, len(AllScenes))
	for _, e := range c.AvailableModels() {
		grouped[e.Scene] = append(grouped[e.Scene], e)
	}
	return grouped
}

// catalogEntry looks up a catalog entry by scene + model id (only among entries
// for that scene). Returns false when not found.
func catalogEntry(scene ModelScene, modelID string) (CatalogEntry, bool) {
	for _, e := range modelCatalog {
		if e.Scene == scene && e.ID == modelID {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// IsModelAvailable reports whether modelID is a valid, configured choice for the
// scene. Used to reject selections of unknown/unconfigured models.
func (c *Config) IsModelAvailable(scene ModelScene, modelID string) bool {
	e, ok := catalogEntry(scene, modelID)
	if !ok {
		return false
	}
	_, credOK := c.entryCredential(e)
	return credOK
}

// ResolveChatModel builds the ModelConfig for a chat-scene selection, resolving
// the gateway credential PER PROVIDER (catalog.entryCredential) and overriding
// provider+model from the catalog. Returns false when the id is not a
// valid/available chat model. Per-provider resolution is what lets a session
// switch between a taiji model and a yunwu model in the same scene without one's
// credential leaking onto the other (the bug where ChatPrimary=taiji routed every
// selected chat model through taiji's gateway).
func (c *Config) ResolveChatModel(modelID string) (ModelConfig, bool) {
	e, ok := catalogEntry(SceneChat, modelID)
	if !ok {
		return ModelConfig{}, false
	}
	cred, ok := c.chatCredentialFor(e.Provider)
	if !ok {
		return ModelConfig{}, false
	}
	return ModelConfig{
		Provider: e.Provider,
		Model:    e.WireModel(),
		BaseURL:  cred.baseURL,
		APIKey:   cred.apiKey,
		Thinking: c.ChatPrimary.Thinking,
		// openai_infer follows the provider (taiji => true), so selecting the
		// taiji-hosted entry at runtime enables function-calling the same way the
		// server-default path does. Without this, a runtime switch to taiji would
		// silently lose tool_calls.
		OpenAIInfer: ProfileForProvider(e.Provider).OpenAIInfer,
	}, true
}

// ResolveImageModel builds an ImageProviderConfig for an image/text-to-image/
// video selection, reusing the scene credential and overriding provider+model.
// Returns false when the id is not a valid/available model for the scene.
func (c *Config) ResolveImageModel(scene ModelScene, modelID string) (ImageProviderConfig, bool) {
	e, ok := catalogEntry(scene, modelID)
	if !ok || !c.IsModelAvailable(scene, modelID) {
		return ImageProviderConfig{}, false
	}
	base, key := c.sceneCredential(scene)
	if e.BaseURL != "" {
		base = e.BaseURL // per-entry endpoint override (e.g. Veo vs happyhorse)
	}
	return ImageProviderConfig{
		Name:     strings.ToLower(e.Vendor),
		Provider: e.Provider,
		BaseURL:  base,
		APIKey:   key,
		Model:    e.WireModel(),
	}, true
}
