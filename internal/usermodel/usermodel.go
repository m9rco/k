// Package usermodel manages per-session model selections: which model each
// session has chosen for each scene (chat / image / text_to_image / video).
// Selections persist in the store's preferences table and fall back to the
// server default when unset. The set of selectable models is server-authoritative
// (config catalog filtered by configured credentials), so a session can never
// select an unconfigured or unknown model.
package usermodel

import (
	"fmt"

	"gameasset/internal/config"
	"gameasset/internal/store"
)

// prefKey returns the preferences key for a scene's model selection.
func prefKey(scene config.ModelScene) string { return "model." + string(scene) }

// Manager reads and writes per-session model selections.
type Manager struct {
	cfg   *config.Config
	store *store.Store
}

// NewManager builds a model-selection manager.
func NewManager(cfg *config.Config, st *store.Store) *Manager {
	return &Manager{cfg: cfg, store: st}
}

// Overrides returns the session's model id per scene (only scenes the session
// has explicitly chosen). Scenes absent from the map use the server default.
func (m *Manager) Overrides(sessionID string) (map[config.ModelScene]string, error) {
	prefs, err := m.store.GetPreferences(sessionID)
	if err != nil {
		return nil, err
	}
	out := make(map[config.ModelScene]string)
	for _, scene := range config.AllScenes {
		if v := prefs[prefKey(scene)]; v != "" {
			out[scene] = v
		}
	}
	return out, nil
}

// ChatModel resolves the session's chat ModelConfig: its selected chat model if
// any (and still available), else the server default. The bool reports whether a
// valid session override was applied (true) versus the default (false).
func (m *Manager) ChatModel(sessionID string) (config.ModelConfig, bool) {
	prefs, err := m.store.GetPreferences(sessionID)
	if err == nil {
		if id := prefs[prefKey(config.SceneChat)]; id != "" {
			if mc, ok := m.cfg.ResolveChatModel(id); ok {
				return mc, true
			}
		}
	}
	// Fall back to the server default (primary, or test when enabled).
	def := m.cfg.ChatPrimary
	if m.cfg.UseTestModel {
		def = m.cfg.ChatTest
	}
	return def, false
}

// ImageModel resolves the session's ImageProviderConfig for an image-like scene
// (image / text_to_image / video). Returns (config, true) when a valid override
// is set; (_, false) when the caller should use the service default.
func (m *Manager) ImageModel(sessionID string, scene config.ModelScene) (config.ImageProviderConfig, bool) {
	prefs, err := m.store.GetPreferences(sessionID)
	if err != nil {
		return config.ImageProviderConfig{}, false
	}
	id := prefs[prefKey(scene)]
	if id == "" {
		return config.ImageProviderConfig{}, false
	}
	return m.cfg.ResolveImageModel(scene, id)
}

// Set records a session's model selection for a scene after validating the model
// is available. Returns an error when the model is not a valid/configured choice.
func (m *Manager) Set(sessionID string, scene config.ModelScene, modelID string) error {
	if !m.cfg.IsModelAvailable(scene, modelID) {
		return fmt.Errorf("model %q is not available for scene %q", modelID, scene)
	}
	return m.store.SetPreference(sessionID, prefKey(scene), modelID)
}
