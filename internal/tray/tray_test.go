package tray

import (
	"testing"

	"stream-agents/internal/server"
)

func TestFormatting(t *testing.T) {
	for n, want := range map[int]string{950: "950", 12_400: "12K", 1_234_567: "1.2M", 2_500_000_000: "2.5B"} {
		if got := fmtTokens(n); got != want {
			t.Errorf("fmtTokens(%d) = %q, want %q", n, got, want)
		}
	}
	for _, tc := range []struct {
		u     server.SummaryUsage
		short bool
		want  string
	}{
		{server.SummaryUsage{Cost: 14.3}, false, "$14.30"},
		{server.SummaryUsage{Cost: 14.3}, true, "$14"},
		{server.SummaryUsage{Cost: 3.456, CostPartial: true}, true, "$3.46+"},
	} {
		if got := fmtCost(tc.u, tc.short); got != tc.want {
			t.Errorf("fmtCost(%+v, %v) = %q, want %q", tc.u, tc.short, got, tc.want)
		}
	}
	if got := truncate("a\n  b", 50); got != "a b" {
		t.Errorf("truncate = %q", got)
	}
}
