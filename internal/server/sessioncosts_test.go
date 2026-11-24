package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func costSessions() []store.Session {
	usage := func(model string, input int, cost float64) []store.ModelUsage {
		return []store.ModelUsage{{Model: model, TokenUsage: store.TokenUsage{InputTokens: input}, Tokens: true, HasCost: true, Cost: cost}}
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 9, 0, 0, 0, apiLoc) }
	return []store.Session{
		{Agent: "claude", ID: "main", Project: "/x/proj", Title: "main", Started: day(16), Usage: usage("claude-opus-5-5", 100, 1)},
		{Agent: "claude", ID: "wt", Project: "/x/proj/.devtainer/board/worktrees/T-12", Started: day(15), Usage: usage("claude-opus-5-5", 10, .5)},
		{Agent: "claude", ID: "other", Project: "/x/proj2", Started: day(16), Usage: usage("claude-opus-5-5", 10, 7)},
		// A subagent of main, in another directory and started long before:
		// it still counts towards main, and gets no row of its own.
		{Agent: "claude", ID: "agent-1", Parent: "main", Sidechain: true, Project: "/elsewhere", Started: day(1), Modified: day(17),
			Usage: append(usage("claude-haiku-5-5", 5, .25), usage("claude-opus-5-5", 1, .25)...)},
		// A grandchild folds into the top-level session too.
		{Agent: "claude", ID: "agent-2", Parent: "agent-1", Sidechain: true, Project: "/x/proj", Started: day(16), Usage: usage("claude-haiku-5-5", 1, .125)},
		{Agent: "claude", ID: "old", Project: "/x/proj", Started: time.Date(2026, 9, 1, 9, 0, 0, 0, apiLoc), Usage: usage("claude-opus-5-5", 1, 3)},
		{Agent: "claude", ID: "unpriced", Project: "/x/proj", Started: day(14), Usage: []store.ModelUsage{{Model: "mystery", TokenUsage: store.TokenUsage{InputTokens: 9}, Tokens: true}}},
	}
}

func costMux() http.Handler {
	idx := store.NewIndex(statsStore{costSessions()})
	return NewMux(idx, Options{Now: func() time.Time { return apiNow }, Location: apiLoc, Logf: func(string, ...any) {}})
}

func getCosts(t *testing.T, query string) (int, SessionCostsResponse, map[string]any) {
	t.Helper()
	w := apiGet(costMux(), "/api/v1/session-costs?"+query, "192.168.2.50:5555")
	var resp SessionCostsResponse
	var raw map[string]any
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	return w.Code, resp, raw
}

func costIDs(r SessionCostsResponse) []string {
	var ids []string
	for _, s := range r.Sessions {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestSessionCostsCWDBoundary(t *testing.T) {
	code, resp, _ := getCosts(t, "cwd=/x/proj/&days=30")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if got, want := costIDs(resp), []string{"main", "unpriced", "wt"}; !slices.Equal(got, want) {
		t.Errorf("ids = %v, want %v (proj2, subagents and old sessions left out)", got, want)
	}
	_, resp, _ = getCosts(t, "cwd=/x/proj/.devtainer/board/worktrees/T-12")
	if got := costIDs(resp); !slices.Equal(got, []string{"wt"}) {
		t.Errorf("worktree ids = %v", got)
	}
	// The default window is 31 days; 60 reaches the September session.
	_, resp, _ = getCosts(t, "cwd=/x/proj&days=60")
	if !slices.Contains(costIDs(resp), "old") {
		t.Errorf("days=60 ids = %v, want old included", costIDs(resp))
	}
}

func TestSessionCostsFoldsSubagents(t *testing.T) {
	_, resp, _ := getCosts(t, "cwd=/x/proj&days=3")
	var main SessionCost
	for _, s := range resp.Sessions {
		if s.ID == "main" {
			main = s
		}
	}
	if main.Cost != 1.625 || main.Billed != 1.625 || main.Subagents != 2 {
		t.Fatalf("main = %+v, want cost 1.625 with 2 subagents", main)
	}
	if main.InputTokens != 107 {
		t.Errorf("input tokens = %d, want 107", main.InputTokens)
	}
	if main.LastActivity != "2026-10-17T06:00:00Z" {
		t.Errorf("last_activity = %s, want the subagent's", main.LastActivity)
	}
	var sum float64
	for _, m := range main.Models {
		sum += m.Cost
	}
	if len(main.Models) != 2 || main.Models[0].Model != "claude-opus-5-5" || main.Models[0].Cost != 1.25 || sum != main.Cost {
		t.Errorf("models = %+v, want opus 1.25 + haiku 0.375 adding up to the session", main.Models)
	}
	if resp.Totals.Cost != 2.125 || resp.Totals.CostPartial != true {
		t.Errorf("totals = %+v, want 2.125 and partial (unpriced session)", resp.Totals)
	}
}

func TestSessionCostsIDsIgnoreDays(t *testing.T) {
	code, resp, _ := getCosts(t, "ids=claude/old,claude/nope,codex/main,claude/main,claude/agent-1&days=1&cwd=/nowhere")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// Unknown ids and subagent ids are left out without an error.
	if got := costIDs(resp); !slices.Equal(got, []string{"main", "old"}) {
		t.Errorf("ids = %v, want main and old", got)
	}
	if resp.Sessions[0].ID != "main" || resp.Sessions[0].Cost != 1.625 {
		t.Errorf("first = %+v, want main (newest) with subagents folded in", resp.Sessions[0])
	}
}

func TestSessionCostsEmptyIs200(t *testing.T) {
	for _, q := range []string{"cwd=/x/none", "ids=claude/nope"} {
		code, resp, raw := getCosts(t, q)
		if code != 200 {
			t.Fatalf("%s: status %d", q, code)
		}
		if s, ok := raw["sessions"].([]any); !ok || len(s) != 0 || resp.Totals.Cost != 0 {
			t.Errorf("%s: sessions = %#v, want []", q, raw["sessions"])
		}
	}
}

func TestSessionCostsBadQuery(t *testing.T) {
	for _, q := range []string{"", "cwd=x/proj", "cwd=/x/proj&days=0", "cwd=/x/proj&days=abc", "ids=,"} {
		code, _, raw := getCosts(t, q)
		if code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, code)
		}
		if msg, _ := raw["error"].(string); msg == "" {
			t.Errorf("%q: body %v has no JSON error", q, raw)
		}
	}
}
