package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"stream-agents/internal/store"
)

type statsStore struct{ sessions []store.Session }

func (s statsStore) Agent() string                                                { return "claude" }
func (s statsStore) ListSessions(context.Context) ([]store.Session, error)        { return s.sessions, nil }
func (s statsStore) LoadSession(context.Context, string) ([]store.Message, error) { return nil, nil }
func (s statsStore) FilePath(string) string                                       { return "" }

func TestStatsGrouping(t *testing.T) {
	sessions := []store.Session{
		{Agent: "claude", Project: "/a", Started: time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC), InputTokens: 10, OutputTokens: 5, CacheReadTokens: 20, CacheCreationTokens: 3, HasTokens: true, HasCost: true, Cost: 1.25},
		{Agent: "codex", Project: "/a", Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Agent: "claude", Project: "/b", Modified: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range []struct {
		group string
		rows  int
		first string
	}{{"year", 2, "2026"}, {"month", 2, "2026-01"}, {"day", 3, "2026-01-02"}} {
		total, rows, agents := aggregateStats(sessions, tc.group)
		if len(rows) != tc.rows || rows[0].Label != tc.first {
			t.Fatalf("%s: %+v", tc.group, rows)
		}
		if total.Sessions != 3 || total.Agents != 2 || total.Projects != 2 || total.Tokens != 38 || total.WithTokens != 1 || len(agents) != 2 {
			t.Fatalf("bad totals: %+v", total)
		}
		if total.Cost != 1.25 || total.WithCost != 1 {
			t.Fatalf("bad cost totals: %+v", total)
		}
	}
}

func TestStatsPage(t *testing.T) {
	mux := NewMux(store.NewIndex(statsStore{[]store.Session{{Agent: "claude", Project: "/test", Started: time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC)}}}))
	for _, tc := range []struct {
		query    string
		status   int
		contains string
	}{
		{"", 200, "2026-01"},
		{"?group=day&from=2026-01-31&to=2026-01-31", 200, "2026-01-31"},
		{"?agent=codex", 200, "No sessions match"},
		{"?project=missing", 200, "No sessions match"},
		{"?from=2026-02-01", 200, "No sessions match"},
		{"?group=bad", 400, "invalid grouping"},
		{"?from=bad", 400, "invalid date"},
		{"?from=2026-02-01&to=2026-01-01", 400, "start date"},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/stats"+tc.query, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
	}
}

func TestActiveStatsOverlapAndDateClipping(t *testing.T) {
	midnight := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	sessions := []store.Session{
		{Agent: "claude", Started: midnight.Add(-24 * time.Hour), Activity: []store.Interval{{Start: midnight.Add(-10 * time.Minute), End: midnight.Add(10 * time.Minute)}}},
		{Agent: "codex", Started: midnight, Activity: []store.Interval{{Start: midnight, End: midnight.Add(20 * time.Minute)}}},
	}
	total, rows, agents := aggregateStats(sessions, "day")
	if total.ActiveDuration != 30*time.Minute || len(rows) != 2 || rows[0].ActiveDuration != 20*time.Minute || rows[1].ActiveDuration != 10*time.Minute {
		t.Fatalf("incorrect union/split: %+v %+v", total, rows)
	}
	if agents[0].ActiveDuration != 20*time.Minute || agents[1].ActiveDuration != 20*time.Minute {
		t.Fatal("agent rows should retain their overlapping time")
	}
	total, rows, _ = aggregateStats(sessions, "month", midnight, midnight.Add(24*time.Hour))
	if total.Sessions != 1 || total.ActiveDuration != 20*time.Minute || len(rows) != 1 || rows[0].Label != "2026-02" {
		t.Fatalf("incorrect clipped stats: %+v %+v", total, rows)
	}
	// An older resumed chat alone must still appear within the selected range.
	total, rows, _ = aggregateStats(sessions[:1], "day", midnight, midnight.Add(24*time.Hour))
	if total.Sessions != 0 || total.ActiveDuration != 10*time.Minute || len(rows) != 1 {
		t.Fatalf("resumed activity lost: %+v", total)
	}
}

func TestActiveTimePages(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	idx := store.NewIndex(statsStore{[]store.Session{{Agent: "claude", Started: start, ActivityTimes: []time.Time{start, start.Add(10 * time.Minute)}}}})
	for _, path := range []string{"/?date=all", "/stats"} {
		w := httptest.NewRecorder()
		NewMux(idx).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Active time (est.)") || !strings.Contains(w.Body.String(), "10m 0s") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	idx.IdleCutoff = 5 * time.Minute
	sessions, err := idx.ListAll(context.Background(), "", "")
	if err != nil || sessions[0].ActiveDuration != 0 {
		t.Fatal("configured cutoff not applied", err)
	}
}
