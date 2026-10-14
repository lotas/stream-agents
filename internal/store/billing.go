package store

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// DefaultCostMultipliers scale API-price estimates to what an agent's plan
// actually bills.
var DefaultCostMultipliers = map[string]float64{"codex": 1.75}

// ParseCostMultipliers parses "agent=factor,agent=factor" (e.g. "codex=1.75")
// and returns the defaults overridden by the parsed values.
func ParseCostMultipliers(s string) (map[string]float64, error) {
	out := make(map[string]float64, len(DefaultCostMultipliers))
	for agent, m := range DefaultCostMultipliers {
		out[agent] = m
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		agent, value, ok := strings.Cut(part, "=")
		agent = strings.TrimSpace(agent)
		m, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if !ok || agent == "" || err != nil || m < 0 || math.IsNaN(m) || math.IsInf(m, 0) {
			return nil, fmt.Errorf("invalid cost multiplier %q: use agent=factor, e.g. codex=1.75", part)
		}
		out[agent] = m
	}
	return out, nil
}

// FormatCostMultipliers renders multipliers other than 1 as "codex ×1.75".
func FormatCostMultipliers(multipliers map[string]float64) string {
	var parts []string
	for agent, m := range multipliers {
		if m != 1 {
			parts = append(parts, fmt.Sprintf("%s ×%g", agent, m))
		}
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
