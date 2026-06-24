package agent

import (
	"testing"

	"gameasset/internal/config"
	"gameasset/internal/generation"
)

// TestEditProviderRouting verifies edit_image's per-intent provider routing:
// character-fusion intents (change_character / add_character) use the fusion
// override (gpt-image-2 → gemini-3-pro-image, injected by the orchestrator),
// while other edit intents keep the session selection (ImageOverride).
func TestEditProviderRouting(t *testing.T) {
	gpt := &config.ImageProviderConfig{Model: "gpt-image-2"}
	gemini := &config.ImageProviderConfig{Model: "gemini-3-pro-image"}
	session := &config.ImageProviderConfig{Model: "session-pick"}

	tests := []struct {
		name    string
		kind    generation.EditKind
		fusion  *config.ImageProviderConfig // FusionModelOverride
		image   *config.ImageProviderConfig // ImageOverride
		wantNil bool
		want    string // expected Model when not nil
	}{
		{name: "fusion change_character -> gpt-image-2", kind: generation.EditCharacter, fusion: gpt, image: session, want: "gpt-image-2"},
		{name: "fusion add_character -> gpt-image-2", kind: generation.EditCharacterAdd, fusion: gpt, image: session, want: "gpt-image-2"},
		{name: "fusion gpt missing -> gemini fallback", kind: generation.EditCharacter, fusion: gemini, image: session, want: "gemini-3-pro-image"},
		{name: "fusion both missing -> session selection", kind: generation.EditCharacterAdd, fusion: nil, image: session, want: "session-pick"},
		{name: "fusion all nil -> service default (nil)", kind: generation.EditCharacter, fusion: nil, image: nil, wantNil: true},
		{name: "change_background ignores fusion override", kind: generation.EditBackground, fusion: gpt, image: session, want: "session-pick"},
		{name: "change_text ignores fusion override", kind: generation.EditText, fusion: gpt, image: session, want: "session-pick"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ToolDeps{FusionModelOverride: tt.fusion, ImageOverride: tt.image}
			got := editProvider(d, tt.kind)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("editProvider = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("editProvider = nil, want Model=%q", tt.want)
			}
			if got.Model != tt.want {
				t.Fatalf("editProvider Model = %q, want %q", got.Model, tt.want)
			}
		})
	}
}
