package config

import "testing"

func baseConfig() *Config {
	return &Config{
		ChatPrimary:  ModelConfig{BaseURL: "https://c/v1", APIKey: "sk-chat", Provider: "openai", Model: "deepseek-v4-flash"},
		ImagePrimary: ImageProviderConfig{BaseURL: "https://i/v1", APIKey: "sk-img"},
		TextToImage:  ImageProviderConfig{BaseURL: "https://t/v1", APIKey: ""}, // not configured
		Video:        ImageProviderConfig{BaseURL: "https://v/v1", APIKey: "sk-vid", Model: "happyhorse-1.0-r2v"},
		// chatCommon mirrors a single-gateway deployment where the shared (yunwu)
		// credential is the same one ChatPrimary uses; taiji has its own dedicated
		// credential so the standalone taiji entry stays selectable.
		chatCommon:    endpointCred{baseURL: "https://c/v1", apiKey: "sk-chat"},
		chatDedicated: map[string]endpointCred{"taiji": {baseURL: "http://taiji/openapi", apiKey: "sk-taiji"}},
	}
}

func TestAvailableModelsFiltersByCredential(t *testing.T) {
	cfg := baseConfig()
	grouped := cfg.AvailableModelsByScene()

	if len(grouped[SceneChat]) == 0 {
		t.Error("chat scene should have available models")
	}
	if len(grouped[SceneImage]) == 0 {
		t.Error("image scene should have available models")
	}
	// TextToImage has no api key => filtered out entirely.
	if len(grouped[SceneTextToImage]) != 0 {
		t.Errorf("text-to-image should be empty (no key), got %d", len(grouped[SceneTextToImage]))
	}
	if len(grouped[SceneVideo]) == 0 {
		t.Error("video scene should have available models")
	}
	// Every entry must carry an icon key.
	for _, e := range cfg.AvailableModels() {
		if e.IconKey == "" {
			t.Errorf("model %s missing icon key", e.ID)
		}
	}
}

func TestIsModelAvailable(t *testing.T) {
	cfg := baseConfig()
	if !cfg.IsModelAvailable(SceneChat, "claude-sonnet-4-6") {
		t.Error("claude-sonnet-4-6 should be available for chat")
	}
	// Unknown id for scene.
	if cfg.IsModelAvailable(SceneChat, "gpt-image-2") {
		t.Error("gpt-image-2 is not a chat model")
	}
	// Valid id but scene unconfigured.
	if cfg.IsModelAvailable(SceneTextToImage, "wan2.7-image-pro") {
		t.Error("text-to-image unconfigured should be unavailable")
	}
	// Wholly unknown id.
	if cfg.IsModelAvailable(SceneChat, "nonexistent") {
		t.Error("unknown id should be unavailable")
	}
}

func TestResolveChatModel(t *testing.T) {
	cfg := baseConfig()
	mc, ok := cfg.ResolveChatModel("claude-sonnet-4-6")
	if !ok {
		t.Fatal("expected resolve ok")
	}
	if mc.Provider != "anthropic" || mc.Model != "claude-sonnet-4-6" {
		t.Errorf("unexpected resolve: %+v", mc)
	}
	if mc.BaseURL != "https://c/v1" || mc.APIKey != "sk-chat" {
		t.Errorf("should reuse chat credential, got base=%q key=%q", mc.BaseURL, mc.APIKey)
	}
	if _, ok := cfg.ResolveChatModel("gpt-image-2"); ok {
		t.Error("non-chat model should not resolve as chat")
	}
}

// TestResolveChatModelUsesWireName verifies that a catalog entry whose provider
// wire name differs from its stable id (the taiji-hosted deepseek entry ->
// "DeepSeek-V4-Flash-Online-32k") resolves to the WIRE name for the API, while
// the id stays the selection key.
func TestResolveChatModelUsesWireName(t *testing.T) {
	cfg := baseConfig()
	mc, ok := cfg.ResolveChatModel("deepseek-v4-flash-tencent")
	if !ok {
		t.Fatal("expected resolve ok")
	}
	if mc.Model != "DeepSeek-V4-Flash-Online-32k" {
		t.Errorf("Model = %q, want the provider wire name DeepSeek-V4-Flash-Online-32k", mc.Model)
	}
}

// TestSceneDefaultMapsWireNameToCatalogID verifies the default-labeling path:
// when the server config holds the provider wire name, SceneDefaultModel returns
// the stable catalog id the frontend can match.
func TestSceneDefaultMapsWireNameToCatalogID(t *testing.T) {
	cfg := baseConfig()
	cfg.ChatPrimary.Model = "DeepSeek-V4-Flash-Online-32k" // configured as wire name
	if got := cfg.SceneDefaultModel(SceneChat); got != "deepseek-v4-flash-tencent" {
		t.Errorf("SceneDefaultModel = %q, want catalog id deepseek-v4-flash-tencent", got)
	}
}

func TestResolveImageModel(t *testing.T) {
	cfg := baseConfig()
	pc, ok := cfg.ResolveImageModel(SceneImage, "gemini-3-pro-image")
	if !ok {
		t.Fatal("expected resolve ok")
	}
	if pc.Provider != "gemini" || pc.Model != "gemini-3-pro-image" {
		t.Errorf("unexpected resolve: %+v", pc)
	}
	if pc.BaseURL != "https://i/v1" || pc.APIKey != "sk-img" {
		t.Errorf("should reuse image credential, got base=%q key=%q", pc.BaseURL, pc.APIKey)
	}
	if _, ok := cfg.ResolveImageModel(SceneTextToImage, "wan2.7-image-pro"); ok {
		t.Error("unconfigured scene should not resolve")
	}
}

// TestResolveVeoUsesEntryBaseAndWireName verifies the per-entry overrides:
// Veo shares the video scene with happyhorse but lives at a different endpoint,
// so its catalog entry carries its own BaseURL (not the scene credential's) and
// a wire name distinct from the stable id. The api key still comes from the
// shared video credential.
func TestResolveVeoUsesEntryBaseAndWireName(t *testing.T) {
	cfg := baseConfig()
	pc, ok := cfg.ResolveImageModel(SceneVideo, "veo_3_1_fast_components_vip")
	if !ok {
		t.Fatal("expected resolve ok")
	}
	if pc.Provider != "veo" {
		t.Errorf("Provider = %q, want veo", pc.Provider)
	}
	if pc.BaseURL != "https://yunwu.ai" {
		t.Errorf("BaseURL = %q, want the entry override https://yunwu.ai (not the scene base)", pc.BaseURL)
	}
	if pc.Model != "veo-3.1-fast" {
		t.Errorf("Model = %q, want wire name veo-3.1-fast", pc.Model)
	}
	if pc.APIKey != "sk-vid" {
		t.Errorf("APIKey = %q, want the shared video credential sk-vid", pc.APIKey)
	}
}

func TestSceneDefaults(t *testing.T) {
	cfg := baseConfig()
	defs := cfg.SceneDefaults()
	if defs[SceneChat] != "deepseek-v4-flash" {
		t.Errorf("chat default = %q, want deepseek-v4-flash", defs[SceneChat])
	}
	// Video default is happyhorse — the server-preselected image-to-video model.
	if defs[SceneVideo] != "happyhorse-1.0-r2v" {
		t.Errorf("video default = %q, want happyhorse-1.0-r2v", defs[SceneVideo])
	}
	// happyhorse must exist in the catalog so the default is selectable/labelable.
	if _, ok := catalogEntry(SceneVideo, "happyhorse-1.0-r2v"); !ok {
		t.Error("happyhorse-1.0-r2v should be in the video catalog")
	}
}
