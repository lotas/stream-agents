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
		{Agent: "claude", Project: "/a", Started: time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC), InputTokens: 10, OutputTokens: 5, CacheReadTokens: 20, CacheCreationTokens: 3, HasTokens: true},
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
