package server

import (
	"strings"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func TestHeatmapCalendar(t *testing.T) {
	for _, year := range []int{2024, 2025, 2028} {
		h := buildHeatmap(nil, year)
		count := 0
		for _, week := range h.Weeks {
			if len(week.Days) != 7 {
				t.Fatal("incomplete week")
			}
			for weekday, day := range week.Days {
				date, err := time.Parse("2006-01-02", day.Date)
				if err != nil || int(date.Weekday()) != weekday {
					t.Fatalf("misaligned day: %+v", day)
				}
				if !day.Outside {
					count++
					if date.Year() != year || day.Level != 0 {
						t.Fatalf("invalid day: %+v", day)
					}
				}
			}
		}
		expected := 365
		if year%4 == 0 {
			expected = 366
		}
		if count != expected {
			t.Fatalf("%d: got %d days", year, count)
		}
	}
}

func TestHeatmapUsage(t *testing.T) {
	date := time.Date(2024, 2, 29, 23, 0, 0, 0, time.UTC)
	h := buildHeatmap([]store.Session{
		{Agent: "claude", Started: date, InputTokens: 10},
		{Agent: "codex", Modified: date, OutputTokens: 20},
		{Agent: "claude", Started: date.AddDate(1, 0, 0)},
	}, 2024)
	if h.Sessions != 2 || h.ActiveDays != 1 {
		t.Fatalf("bad totals: %+v", h)
	}
	for _, week := range h.Weeks {
		for _, day := range week.Days {
			if day.Date == "2024-02-29" && (day.Level != 4 || !strings.Contains(day.Detail, "2 sessions, 30 tokens, 2 agent types")) {
				t.Fatalf("bad activity: %+v", day)
			}
		}
	}
}
