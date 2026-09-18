package server

import "testing"

func TestShortModels(t *testing.T) {
	tests := map[string]string{
		"openrouter/moonshotai/kimi-k3":                                "moonshotai/kimi-k3",
		"openrouter/z-ai/glm-5.3-flash":                                "z-ai/glm-5.3-flash",
		"github-copilot/gemini-2.5-pro":                                "gemini-2.5-pro",
		"custom-provider/acme/model, openrouter/moonshotai/kimi-k3":    "custom-provider/acme/model, moonshotai/kimi-k3",
		"openrouter/moonshotai/kimi-k3, github-copilot/gemini-2.5-pro": "moonshotai/kimi-k3, gemini-2.5-pro",
	}
	for input, want := range tests {
		if got := shortModels(input); got != want {
			t.Errorf("shortModels(%q) = %q, want %q", input, got, want)
		}
	}
}
