package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func usage(provider, model string, input int, cost float64, priced bool) store.ModelUsage {
	return store.ModelUsage{Provider: provider, Model: model, TokenUsage: store.TokenUsage{InputTokens: input}, Tokens: true, HasCost: priced, Cost: cost}
}

func testSessions() []store.Session {
	day := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	return []store.Session{
		{Agent: "claude", ID: "a", Project: "/p", Started: day(1, 10), Usage: []store.ModelUsage{
			usage("anthropic", "claude-opus-5-5", 100, 1, true),
			usage("anthropic", "claude-haiku-5-5", 50, .5, true),
		}},
		{Agent: "codex", ID: "b", Project: "/q", Started: day(1, 23), Usage: []store.ModelUsage{
			usage("openai", "gpt-5.5", 200, 2, true),
			usage("openai", "", 10, 0, false),
		}},
		{Agent: "opencode", ID: "c", Project: "/p", Started: day(3, 12), Usage: []store.ModelUsage{
			usage("anthropic", "claude-opus-5-5", 300, 3, true),
		}},
		{Agent: "claude", ID: "d", Project: "/p", Modified: day(5, 1)},
	}
}

func TestBuild(t *testing.T) {
	rep := Build(testSessions(), Options{Group: "day", Breakdown: true}, time.UTC)
	if rep.Total.Sessions != 4 || rep.Total.Input != 660 || rep.Total.Cost != 6.5 || !rep.Total.CostPartial {
		t.Fatalf("total = %+v", rep.Total)
	}
	var labels []string
	for _, p := range rep.Periods {
		labels = append(labels, p.Label)
	}
	if got := strings.Join(labels, " "); got != "2026-10-01 2026-10-03 2026-10-05" {
		t.Fatalf("periods = %s", got)
	}
	first := rep.Periods[0]
	if first.Sessions != 2 || first.Cost != 3.5 || len(first.Models) != 4 || first.Models[0].Label != "openai/gpt-5.5" {
		t.Fatalf("first period = %+v, models %+v", first, first.Models)
	}
	if len(rep.Providers) != 2 || rep.Providers[0].Label != "anthropic" || rep.Providers[0].Sessions != 2 || rep.Providers[0].Cost != 4.5 {
		t.Fatalf("providers = %+v", rep.Providers)
	}
	if len(rep.Models) != 4 || rep.Models[0].Label != "anthropic/claude-opus-5-5" || rep.Models[0].Sessions != 2 || rep.Models[0].Input != 400 {
		t.Fatalf("models = %+v", rep.Models)
	}
	if last := rep.Models[3]; last.Label != "openai/(unknown)" || !last.CostPartial {
		t.Fatalf("unpriced model = %+v", last)
	}
	if len(rep.Agents) != 3 || rep.Agents[0].Label != "opencode" || rep.Agents[2].Label != "claude" || rep.Agents[2].Sessions != 2 {
		t.Fatalf("agents = %+v", rep.Agents)
	}
}

func TestBuildFilters(t *testing.T) {
	opts := Options{
		Group:   "month",
		From:    time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		To:      time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
		Project: "/p",
	}
	rep := Build(testSessions(), opts, time.UTC)
	if rep.Total.Sessions != 1 || rep.Total.Input != 300 || len(rep.Periods) != 1 || rep.Periods[0].Label != "2026-10" {
		t.Fatalf("filtered = %+v periods %+v", rep.Total, rep.Periods)
	}
	if rep.Periods[0].Models != nil {
		t.Fatal("models listed without breakdown")
	}
	rep = Build(testSessions(), Options{Group: "day", Agent: "codex"}, time.UTC)
	if rep.Total.Sessions != 1 || rep.Total.Input != 210 {
		t.Fatalf("agent filter = %+v", rep.Total)
	}
}

func TestBuildLocalDay(t *testing.T) {
	loc := time.FixedZone("UTC+2", 2*60*60)
	rep := Build(testSessions()[:2], Options{Group: "day"}, loc)
	if len(rep.Periods) != 2 || rep.Periods[1].Label != "2026-10-02" {
		t.Fatalf("periods = %+v", rep.Periods)
	}
}

func TestWriteText(t *testing.T) {
	rep := Build(testSessions(), Options{Group: "day", Breakdown: true}, time.UTC)
	var b bytes.Buffer
	if err := WriteText(&b, rep, []string{SectionPeriod, SectionModel}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"By day", "  ├ anthropic/claude-haiku-5-5", "  └ openai/(unknown)", "By model", "$6.50*", "* includes tokens"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "By agent") || strings.Contains(out, "By provider") {
		t.Errorf("output includes unrequested sections:\n%s", out)
	}
}

func TestWriteJSON(t *testing.T) {
	rep := Build(testSessions(), Options{Group: "day"}, time.UTC)
	var b bytes.Buffer
	if err := WriteJSON(&b, rep, []string{SectionAgent}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["agents"] == nil || got["periods"] != nil || got["models"] != nil || got["total"].(map[string]any)["cost"] != 6.5 {
		t.Fatalf("json = %s", b.String())
	}
}
