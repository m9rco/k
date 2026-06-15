package config

import "strings"

// ProviderProfile captures provider-specific quirks that can't be expressed as
// plain endpoint fields (base_url / api_key / model). Several gateways speak the
// same OpenAI-compatible wire protocol yet diverge in small, breaking ways, so a
// provider NAME carries its defaults here instead of every call site hardcoding
// per-gateway hacks.
//
// The motivating case: taiji (腾讯太极) exposes DeepSeek-*-Online models that
// only honor OpenAI function-calling when the private field "openai_infer": true
// is present; without it they answer via built-in web search and never emit
// tool_calls, silently breaking this tool-driven agent. Standard gateways
// (yunwu, api.deepseek.com) don't recognize that field and some hang on it, so it
// must NOT be sent to them. Tying the behavior to the provider keeps the two
// apart by construction: provider "taiji" opts in, everyone else stays clean.
type ProviderProfile struct {
	// OpenAIInfer is the default for ModelConfig.OpenAIInfer when a model uses
	// this provider and no explicit env override is given.
	OpenAIInfer bool
	// Standalone marks a provider whose gateway is NOT interchangeable with the
	// shared/common gateway (yunwu). yunwu proxies openai/anthropic/deepseek, so
	// those providers can fall back to the common credential; taiji is its own
	// host that only serves its own DeepSeek models, so a taiji-provider model is
	// only usable with a taiji credential. Standalone providers therefore never
	// fall back to the common gateway: they are available only when configured as
	// the primary chat model or via a dedicated <PROVIDER>_* credential. This is
	// what keeps a taiji entry from being silently routed to yunwu (where its
	// model name is unknown and the request fails).
	Standalone bool
}

// providerProfiles maps a lowercased provider name to its quirks. A provider
// absent from this map resolves to the zero ProviderProfile (no special
// behavior), which is the correct default for standard OpenAI-compatible
// gateways like yunwu and api.deepseek.com.
var providerProfiles = map[string]ProviderProfile{
	"taiji": {OpenAIInfer: true, Standalone: true},
}

// ProfileForProvider returns the registered profile for a provider name
// (case-insensitive, whitespace-trimmed), or the zero profile when the provider
// has no special handling.
func ProfileForProvider(provider string) ProviderProfile {
	return providerProfiles[strings.ToLower(strings.TrimSpace(provider))]
}
