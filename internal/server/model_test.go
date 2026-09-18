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

func TestFmtCost(t *testing.T) {
	for cost, want := range map[float64]string{
		0:        "$0.00",
		0.000233: "$0.0002",
		0.012345: "$0.0123",
		1.5:      "$1.50",
	} {
		if got := fmtCost(cost); got != want {
			t.Errorf("fmtCost(%v) = %q, want %q", cost, got, want)
		}
	}
}
