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
// selection key (openai/anthropic/gemini/dashscope/veo/happyhorse); IconKey maps
// to a built-in vendor brand SVG on the frontend.
type CatalogEntry struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"displayName"`
	Scene       ModelScene `json:"scene"`
	Vendor      string     `json:"vendor"`
	IconKey     string     `json:"iconKey"`
	Provider    string     `json:"-"` // resolution detail, not exposed to the client
}

// modelCatalog is the static, server-authoritative list of known models. The
// frontend may only choose from the subset that AvailableModels reports as
// configured. Adding a model here (plus its credentials) makes it selectable.
var modelCatalog = []CatalogEntry{
	// 逻辑推理 (chat)
	{ID: "deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", Scene: SceneChat, Vendor: "DeepSeek", IconKey: "deepseek", Provider: "openai"},
	{ID: "gpt-5.4", DisplayName: "GPT-5.4", Scene: SceneChat, Vendor: "OpenAI", IconKey: "openai", Provider: "openai"},
	{ID: "doubao-seed-2-0-mini-260428", DisplayName: "Doubao Seed 2.0 mini", Scene: SceneChat, Vendor: "Doubao", IconKey: "doubao", Provider: "openai"},
	{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5", Scene: SceneChat, Vendor: "Anthropic", IconKey: "claude", Provider: "anthropic"},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", Scene: SceneChat, Vendor: "Anthropic", IconKey: "claude", Provider: "anthropic"},

	// 图生图 (image)
	{ID: "gpt-image-2", DisplayName: "GPT Image 2", Scene: SceneImage, Vendor: "OpenAI", IconKey: "openai", Provider: "openai"},
	{ID: "gemini-3-pro-image", DisplayName: "Gemini 3 Pro Image", Scene: SceneImage, Vendor: "Google", IconKey: "gemini", Provider: "gemini"},
	{ID: "gemini-2.5-flash-image", DisplayName: "Gemini 2.5 Flash Image", Scene: SceneImage, Vendor: "Google", IconKey: "gemini", Provider: "gemini"},
	{ID: "gemini-3.1-flash-image", DisplayName: "Gemini 3.1 Flash Image", Scene: SceneImage, Vendor: "Google", IconKey: "gemini", Provider: "gemini"},
	{ID: "gemini-3.1-flash-image-preview", DisplayName: "Gemini 3.1 Flash Image (Preview)", Scene: SceneImage, Vendor: "Google", IconKey: "gemini", Provider: "gemini"},

	// 文生图 (text_to_image)
	{ID: "wan2.7-image-pro", DisplayName: "Wan 2.7 Image Pro", Scene: SceneTextToImage, Vendor: "Alibaba", IconKey: "wan", Provider: "dashscope"},
	{ID: "qwen-image-2.0-2026-03-03", DisplayName: "Qwen Image 2.0", Scene: SceneTextToImage, Vendor: "Qwen", IconKey: "qwen", Provider: "dashscope"},

	// 图生视频 (video)
	{ID: "veo_3_1_fast_components_vip", DisplayName: "Veo 3.1 Fast", Scene: SceneVideo, Vendor: "Google", IconKey: "veo", Provider: "veo"},
	{ID: "veo_3_1_components_vip", DisplayName: "Veo 3.1", Scene: SceneVideo, Vendor: "Google", IconKey: "veo", Provider: "veo"},
}

// sceneCredential returns the configured credential backing a scene (base_url +
// api_key). All catalog models for a scene share these via the COMMON fallback;
// switching a model only changes provider+model, not the credential.
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

// AvailableModels returns the catalog filtered to scenes whose credential is
// configured (non-empty api key). A scene with no api key yields no entries, so
// the frontend never offers a model that would fail on use.
func (c *Config) AvailableModels() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(modelCatalog))
	for _, e := range modelCatalog {
		if _, key := c.sceneCredential(e.Scene); key != "" {
			out = append(out, e)
		}
	}
	return out
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
	if _, ok := catalogEntry(scene, modelID); !ok {
		return false
	}
	_, key := c.sceneCredential(scene)
	return key != ""
}

// ResolveChatModel builds the ModelConfig for a chat-scene selection, reusing
// the configured chat credential and overriding provider+model from the catalog.
// Returns false when the id is not a valid/available chat model.
func (c *Config) ResolveChatModel(modelID string) (ModelConfig, bool) {
	e, ok := catalogEntry(SceneChat, modelID)
	if !ok || !c.IsModelAvailable(SceneChat, modelID) {
		return ModelConfig{}, false
	}
	base, key := c.sceneCredential(SceneChat)
	return ModelConfig{
		Provider: e.Provider,
		Model:    e.ID,
		BaseURL:  base,
		APIKey:   key,
		Thinking: c.ChatPrimary.Thinking,
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
	return ImageProviderConfig{
		Name:     strings.ToLower(e.Vendor),
		Provider: e.Provider,
		BaseURL:  base,
		APIKey:   key,
		Model:    e.ID,
	}, true
}
