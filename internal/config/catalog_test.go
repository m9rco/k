package config

import "testing"

func baseConfig() *Config {
	return &Config{
		ChatPrimary:  ModelConfig{BaseURL: "https://c/v1", APIKey: "sk-chat", Provider: "openai", Model: "deepseek-v4-flash"},
		ImagePrimary: ImageProviderConfig{BaseURL: "https://i/v1", APIKey: "sk-img"},
		TextToImage:  ImageProviderConfig{BaseURL: "https://t/v1", APIKey: ""}, // not configured
		Video:        ImageProviderConfig{BaseURL: "https://v/v1", APIKey: "sk-vid", Model: "happyhorse-1.0-r2v"},
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
