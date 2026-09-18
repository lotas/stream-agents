package store

import "strings"

// modelPrice is the standard API price in USD per million tokens. These are
// deliberately current-price estimates rather than a historical rate ledger.
type modelPrice struct {
	Input      float64
	CacheRead  float64
	CacheWrite float64
	Output     float64
}

// Prices checked 2026-09-18 against the providers' standard API pricing:
//
//	OpenAI: https://developers.openai.com/api/docs/pricing
//	Anthropic: https://docs.anthropic.com/en/docs/about-claude/pricing
//
// Fast, batch, regional, and long-context modifiers are intentionally omitted.
var modelPrices = map[string]modelPrice{
	"gpt-6-astra":       {Input: 10, CacheRead: 1, CacheWrite: 12.5, Output: 50},
	"gpt-5.6":           {Input: 4, CacheRead: .4, CacheWrite: 5, Output: 20},
	"gpt-5.6-sol":       {Input: 4, CacheRead: .4, CacheWrite: 5, Output: 20},
	"gpt-5.6-terra":     {Input: 2, CacheRead: .2, CacheWrite: 2.5, Output: 12},
	"gpt-5.6-luna":      {Input: .2, CacheRead: .02, CacheWrite: .25, Output: 1.2},
	"gpt-5.5":           {Input: 5, CacheRead: .5, Output: 30},
	"gpt-5.4":           {Input: 2.5, CacheRead: .25, Output: 15},
	"gpt-5.3-codex":     {Input: 1.75, CacheRead: .175, Output: 14},
	"gpt-5.2-codex":     {Input: 1.75, CacheRead: .175, Output: 14},
	"gpt-5.2":           {Input: 1.75, CacheRead: .175, Output: 14},
	"gpt-5.1-codex-max": {Input: 1.25, CacheRead: .125, Output: 10},
	"gpt-5.1-codex":     {Input: 1.25, CacheRead: .125, Output: 10},
	"gpt-5-codex":       {Input: 1.25, CacheRead: .125, Output: 10},
	"claude-fable-5-1":  {Input: 10, CacheRead: .25, CacheWrite: 12.5, Output: 50},
	"claude-fable-5":    {Input: 10, CacheRead: 1, CacheWrite: 12.5, Output: 50},
	"claude-mythos-5":   {Input: 10, CacheRead: 1, CacheWrite: 12.5, Output: 50},
	"claude-opus-5":     {Input: 5, CacheRead: .5, CacheWrite: 6.25, Output: 25},
	"claude-sonnet-5":   {Input: 2, CacheRead: .2, CacheWrite: 2.5, Output: 10},
	"claude-opus-4-8":   {Input: 5, CacheRead: .5, CacheWrite: 6.25, Output: 25},
	"claude-opus-4-7":   {Input: 5, CacheRead: .5, CacheWrite: 6.25, Output: 25},
	"claude-opus-4-6":   {Input: 5, CacheRead: .5, CacheWrite: 6.25, Output: 25},
	"claude-opus-4-5":   {Input: 5, CacheRead: .5, CacheWrite: 6.25, Output: 25},
	"claude-sonnet-4-6": {Input: 3, CacheRead: .3, CacheWrite: 3.75, Output: 15},
	"claude-sonnet-4-5": {Input: 3, CacheRead: .3, CacheWrite: 3.75, Output: 15},
	"claude-haiku-4-5":  {Input: 1, CacheRead: .1, CacheWrite: 1.25, Output: 5},
}

func estimateModelCost(model string, usage TokenUsage) (float64, bool) {
	price, ok := lookupModelPrice(model)
	if !ok {
		return 0, false
	}
	cost := float64(usage.InputTokens)*price.Input +
		float64(usage.CacheReadTokens)*price.CacheRead +
		float64(usage.CacheCreationTokens)*price.CacheWrite +
		float64(usage.OutputTokens)*price.Output
	return cost / 1_000_000, true
}

func lookupModelPrice(model string) (modelPrice, bool) {
	if price, ok := modelPrices[model]; ok {
		return price, true
	}
	// Claude sometimes records dated snapshots of a catalogued model.
	for _, base := range []string{
		"claude-fable-5-1",
		"claude-fable-5",
		"claude-mythos-5",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5",
		"claude-haiku-4-5",
	} {
		if strings.HasPrefix(model, base+"-") {
			return modelPrices[base], true
		}
	}
	return modelPrice{}, false
}
