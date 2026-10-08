package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func TestBuildSummary(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, loc)
	sessions := []store.Session{
		// 01:00 local today, but still the previous day in UTC.
		{Agent: "claude", Started: time.Date(2026, 10, 7, 22, 0, 0, 0, time.UTC), InputTokens: 100, HasTokens: true, HasCost: true, Cost: 1.5},
		{Agent: "codex", Started: time.Date(2026, 10, 8, 9, 0, 0, 0, loc), OutputTokens: 50, HasTokens: true},
		// 23:00 local yesterday: in the week, not today.
		{Agent: "claude", Started: time.Date(2026, 10, 7, 23, 0, 0, 0, loc), InputTokens: 1000, HasTokens: true, HasCost: true, Cost: 10},
		// 6 days ago counts towards the week, 7 days ago does not.
		{Agent: "claude", Started: time.Date(2026, 10, 2, 0, 0, 0, 0, loc), InputTokens: 7, HasTokens: true},
		{Agent: "claude", Started: time.Date(2026, 10, 1, 23, 59, 0, 0, loc), InputTokens: 9999, HasTokens: true},
		// Last month, and the edges of the 3-month window (Aug 1 in, Jul 31 out).
		{Agent: "claude", Started: time.Date(2026, 9, 30, 23, 0, 0, 0, loc), HasCost: true, Cost: 20},
		{Agent: "claude", Started: time.Date(2026, 8, 1, 0, 0, 0, 0, loc), HasCost: true, Cost: 40},
		{Agent: "claude", Started: time.Date(2026, 7, 31, 23, 59, 0, 0, loc), HasCost: true, Cost: 80},
	}
	s := BuildSummary(sessions, now)

	if s.Today.Sessions != 2 || s.Today.Tokens != 150 || s.Today.Cost != 1.5 || !s.Today.CostPartial {
		t.Fatalf("bad today: %+v", s.Today)
	}
	if len(s.Today.Agents) != 2 || s.Today.Agents[0].Agent != "claude" || s.Today.Agents[0].CostPartial || !s.Today.Agents[1].CostPartial {
		t.Fatalf("bad today agents: %+v", s.Today.Agents)
	}
	if s.Week.Sessions != 4 || s.Week.Tokens != 1157 || s.Week.Cost != 11.5 {
		t.Fatalf("bad week: %+v", s.Week)
	}
	if s.Week.From != "2026-10-02" || s.Week.To != "2026-10-08" {
		t.Fatalf("bad week range: %s..%s", s.Week.From, s.Week.To)
	}
	if s.Month.Sessions != 5 || s.Month.Cost != 11.5 || s.Month.From != "2026-10-01" || s.Month.To != "2026-10-08" {
		t.Fatalf("bad month: %+v", s.Month)
	}
	if s.LastMonth.Sessions != 1 || s.LastMonth.Cost != 20 || s.LastMonth.From != "2026-09-01" || s.LastMonth.To != "2026-09-30" {
		t.Fatalf("bad last month: %+v", s.LastMonth)
	}
	if s.Last3Months.Sessions != 7 || s.Last3Months.Cost != 71.5 || s.Last3Months.From != "2026-08-01" {
		t.Fatalf("bad last 3 months: %+v", s.Last3Months)
	}
}

func TestBuildSummaryActive(t *testing.T) {
	now := time.Now()
	s := BuildSummary([]store.Session{
		{Agent: "claude", ID: "abc", Title: "Hot", Modified: now.Add(-time.Minute)},
		{Agent: "claude", ID: "old", Title: "Cold", Modified: now.Add(-time.Hour)},
	}, now)
	if len(s.Active) != 1 || s.Active[0].URL != "/session/claude/abc" || s.Active[0].Title != "Hot" {
		t.Fatalf("bad active: %+v", s.Active)
	}
}

func TestSummaryJSON(t *testing.T) {
	mux := NewMux(store.NewIndex(statsStore{[]store.Session{{Agent: "claude", Started: time.Now(), InputTokens: 5, HasTokens: true}}}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/summary.json", nil))
	var s Summary
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil || w.Code != 200 {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	if s.Today.Tokens != 5 || s.Week.Sessions != 1 {
		t.Fatalf("bad summary: %+v", s)
	}
}
