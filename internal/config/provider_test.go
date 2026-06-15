package config

import "testing"

// TestProfileForProvider locks the provider→quirk mapping: taiji opts into
// openai_infer, standard gateways do not, and lookup is case/space-insensitive.
func TestProfileForProvider(t *testing.T) {
	cases := []struct {
		provider string
		want     bool
	}{
		{"taiji", true},
		{"TAIJI", true},
		{"  taiji  ", true},
		{"openai", false}, // yunwu / api.deepseek.com — must NOT receive openai_infer
		{"anthropic", false},
		{"", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		if got := ProfileForProvider(tc.provider).OpenAIInfer; got != tc.want {
			t.Errorf("ProfileForProvider(%q).OpenAIInfer = %v, want %v", tc.provider, got, tc.want)
		}
	}
}

// TestResolveChatModelCarriesOpenAIInfer verifies the runtime model-switch path
// derives OpenAIInfer from the provider: the taiji-hosted entry turns it on, a
// plain openai entry leaves it off. This is the guard against a runtime switch to
// taiji silently losing tool_calls.
func TestResolveChatModelCarriesOpenAIInfer(t *testing.T) {
	cfg := baseConfig()

	taiji, ok := cfg.ResolveChatModel("deepseek-v4-flash-tencent")
	if !ok {
		t.Fatal("expected taiji entry to resolve")
	}
	if taiji.Provider != "taiji" {
		t.Errorf("Provider = %q, want taiji", taiji.Provider)
	}
	if !taiji.OpenAIInfer {
		t.Error("taiji entry must resolve with OpenAIInfer=true (else tool_calls break)")
	}

	plain, ok := cfg.ResolveChatModel("deepseek-v4-flash")
	if !ok {
		t.Fatal("expected plain deepseek entry to resolve")
	}
	if plain.OpenAIInfer {
		t.Error("openai-provider entry must resolve with OpenAIInfer=false (yunwu hangs on the field)")
	}
}

// TestSceneDefaultMatchesContextVariantAlias guards the frontend "default" badge:
// when the configured wire name is a context-size variant (…-16k) that differs
// from the catalog's canonical wire name (…-32k), reverse lookup must still land
// on the stable catalog id via the entry's alias list. Otherwise the badge
// silently disappears because defaults[scene] never equals any catalog entry id.
func TestSceneDefaultMatchesContextVariantAlias(t *testing.T) {
	cfg := baseConfig()
	cfg.ChatPrimary.Model = "DeepSeek-V4-Flash-Online-16k" // alias, not the canonical 32k
	if got := cfg.SceneDefaultModel(SceneChat); got != "deepseek-v4-flash-tencent" {
		t.Errorf("SceneDefaultModel = %q, want catalog id deepseek-v4-flash-tencent (via 16k alias)", got)
	}
}

// TestChatCredentialDoesNotLeakAcrossProviders is the regression guard for the
// "switching to yunwu breaks every other model" bug: when ChatPrimary is taiji
// (a standalone gateway), selecting an openai/anthropic catalog model must route
// to the shared yunwu (common) credential — NOT to taiji's gateway/key, which
// can't serve those models. Conversely the taiji entry must use taiji's
// credential. Per-provider resolution keeps the two gateways apart in one scene.
func TestChatCredentialDoesNotLeakAcrossProviders(t *testing.T) {
	cfg := &Config{
		// Primary is taiji (the user's .env): its credential must apply ONLY to
		// taiji-provider models, never to openai/anthropic ones.
		ChatPrimary: ModelConfig{Provider: "taiji", BaseURL: "http://taiji/openapi", APIKey: "sk-taiji", Model: "DeepSeek-V4-Flash-Online-16k"},
		// Shared yunwu gateway that proxies the standard providers.
		chatCommon: endpointCred{baseURL: "https://yunwu.ai/v1", apiKey: "sk-yunwu"},
	}

	// openai model -> yunwu credential, openai_infer OFF.
	ds, ok := cfg.ResolveChatModel("deepseek-v4-flash")
	if !ok {
		t.Fatal("deepseek-v4-flash (openai/yunwu) should resolve via the common credential")
	}
	if ds.BaseURL != "https://yunwu.ai/v1" || ds.APIKey != "sk-yunwu" {
		t.Errorf("openai model leaked the taiji credential: base=%q key=%q", ds.BaseURL, ds.APIKey)
	}
	if ds.OpenAIInfer {
		t.Error("openai model must NOT carry openai_infer (yunwu hangs on it)")
	}

	// anthropic model -> yunwu credential too.
	cl, ok := cfg.ResolveChatModel("claude-sonnet-4-6")
	if !ok {
		t.Fatal("claude (anthropic/yunwu) should resolve via the common credential")
	}
	if cl.BaseURL != "https://yunwu.ai/v1" || cl.APIKey != "sk-yunwu" {
		t.Errorf("anthropic model leaked the taiji credential: base=%q key=%q", cl.BaseURL, cl.APIKey)
	}

	// taiji entry -> taiji credential (primary), openai_infer ON.
	tj, ok := cfg.ResolveChatModel("deepseek-v4-flash-tencent")
	if !ok {
		t.Fatal("taiji entry should resolve via the primary (taiji) credential")
	}
	if tj.BaseURL != "http://taiji/openapi" || tj.APIKey != "sk-taiji" {
		t.Errorf("taiji entry got the wrong credential: base=%q key=%q", tj.BaseURL, tj.APIKey)
	}
	if !tj.OpenAIInfer {
		t.Error("taiji entry must carry openai_infer=true")
	}
}

// TestStandaloneProviderUnavailableWithoutCredential verifies a standalone
// provider (taiji) is NOT offered when neither the primary nor a dedicated
// credential is configured — it must never silently fall back to the yunwu
// gateway, which would fail at call time with an unknown-model error.
func TestStandaloneProviderUnavailableWithoutCredential(t *testing.T) {
	cfg := &Config{
		ChatPrimary: ModelConfig{Provider: "openai", BaseURL: "https://yunwu.ai/v1", APIKey: "sk-yunwu", Model: "deepseek-v4-flash"},
		chatCommon:  endpointCred{baseURL: "https://yunwu.ai/v1", apiKey: "sk-yunwu"},
		// no taiji dedicated credential, primary is not taiji
	}
	if cfg.IsModelAvailable(SceneChat, "deepseek-v4-flash-tencent") {
		t.Error("taiji entry must be unavailable without a taiji credential (no silent yunwu fallback)")
	}
	if _, ok := cfg.ResolveChatModel("deepseek-v4-flash-tencent"); ok {
		t.Error("taiji entry must not resolve without a taiji credential")
	}
	// The yunwu model is still available.
	if !cfg.IsModelAvailable(SceneChat, "deepseek-v4-flash") {
		t.Error("yunwu model should remain available")
	}
}
