package usermodel

import (
	"testing"

	"gameasset/internal/config"
	"gameasset/internal/store"
)

func testCfg() *config.Config {
	return &config.Config{
		ChatPrimary:  config.ModelConfig{BaseURL: "https://c/v1", APIKey: "sk-chat", Provider: "openai", Model: "deepseek-v4-flash"},
		ImagePrimary: config.ImageProviderConfig{BaseURL: "https://i/v1", APIKey: "sk-img"},
		Video:        config.ImageProviderConfig{BaseURL: "https://v/v1", APIKey: "sk-vid"},
		// TextToImage left unconfigured (no key).
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSetAndResolveChat(t *testing.T) {
	m := NewManager(testCfg(), openStore(t))

	// Default before any selection.
	if mc, overridden := m.ChatModel("s1"); overridden || mc.Model != "deepseek-v4-flash" {
		t.Errorf("expected default chat model, got %+v overridden=%v", mc, overridden)
	}

	if err := m.Set("s1", config.SceneChat, "claude-sonnet-4-6"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	mc, overridden := m.ChatModel("s1")
	if !overridden || mc.Model != "claude-sonnet-4-6" || mc.Provider != "anthropic" {
		t.Errorf("expected claude override, got %+v overridden=%v", mc, overridden)
	}
	// Isolation: another session is unaffected.
	if _, overridden := m.ChatModel("s2"); overridden {
		t.Error("s2 should not be overridden")
	}
}

func TestSetRejectsUnavailable(t *testing.T) {
	m := NewManager(testCfg(), openStore(t))
	// Unknown id.
	if err := m.Set("s1", config.SceneChat, "nope"); err == nil {
		t.Error("expected error for unknown model")
	}
	// Valid id but unconfigured scene.
	if err := m.Set("s1", config.SceneTextToImage, "wan2.7-image-pro"); err == nil {
		t.Error("expected error for unconfigured scene")
	}
}

func TestImageModelOverride(t *testing.T) {
	m := NewManager(testCfg(), openStore(t))
	if _, ok := m.ImageModel("s1", config.SceneImage); ok {
		t.Error("no override yet")
	}
	if err := m.Set("s1", config.SceneImage, "gemini-3-pro-image"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	pc, ok := m.ImageModel("s1", config.SceneImage)
	if !ok || pc.Provider != "gemini" || pc.Model != "gemini-3-pro-image" {
		t.Errorf("expected gemini override, got %+v ok=%v", pc, ok)
	}
}

func TestOverridesPersistAcrossManagers(t *testing.T) {
	st := openStore(t)
	m1 := NewManager(testCfg(), st)
	if err := m1.Set("s1", config.SceneChat, "gpt-5.4"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// A fresh manager (simulating reconnect) sees the persisted choice.
	m2 := NewManager(testCfg(), st)
	ov, err := m2.Overrides("s1")
	if err != nil {
		t.Fatalf("Overrides: %v", err)
	}
	if ov[config.SceneChat] != "gpt-5.4" {
		t.Errorf("expected persisted gpt-5.4, got %q", ov[config.SceneChat])
	}
}
