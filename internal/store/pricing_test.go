package store

import "testing"

func TestEstimateModelCost(t *testing.T) {
	tests := []struct {
		model string
		usage TokenUsage
		want  float64
		ok    bool
	}{
		{
			model: "claude-sonnet-4-6",
			usage: TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, CacheCreationTokens: 1_000_000},
			want:  22.05,
			ok:    true,
		},
		{
			model: "gpt-5.4",
			usage: TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000},
			want:  17.75,
			ok:    true,
		},
		{
			model: "claude-sonnet-4-6-20260217",
			usage: TokenUsage{InputTokens: 1_000_000},
			want:  3,
			ok:    true,
		},
		{
			model: "claude-opus-5",
			usage: TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, CacheCreationTokens: 1_000_000},
			want:  36.75,
			ok:    true,
		},
		{
			model: "claude-fable-5-20260609",
			usage: TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, CacheCreationTokens: 1_000_000},
			want:  73.5,
			ok:    true,
		},
		{
			model: "claude-fable-5-1-20260901",
			usage: TokenUsage{CacheReadTokens: 1_000_000},
			want:  .25,
			ok:    true,
		},
		{model: "unknown-model", usage: TokenUsage{InputTokens: 1_000_000}},
	}
	for _, tt := range tests {
		got, ok := estimateModelCost(tt.model, tt.usage)
		if ok != tt.ok || got != tt.want {
			t.Errorf("estimateModelCost(%q) = (%v, %v), want (%v, %v)", tt.model, got, ok, tt.want, tt.ok)
		}
	}
}
