package limits

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testdata/limits/claude.json is a real Claude Code cache (fetched
// 2026-11-11T19:10:32Z) with the account UUID zeroed.
const fixture = "../../testdata/limits/claude.json"

func f(v float64) *float64 { return &v }

func TestReadRealCache(t *testing.T) {
	now := time.Date(2026, 11, 11, 19, 30, 0, 0, time.UTC)
	got := Read(Sources{ClaudeJSON: []string{"/nonexistent/.claude.json", fixture}}, now, "mac-mini", "")
	if !got.Available || got.FetchedAt == nil || *got.FetchedAt != "2026-11-11T19:10:32Z" || *got.AgeSeconds != 1168 || got.Origin != "cache" || got.StatuslineAt != nil {
		t.Fatalf("available %v fetched %v age %v", got.Available, got.FetchedAt, got.AgeSeconds)
	}
	if got.Source != fixture {
		t.Errorf("source %q", got.Source)
	}
	if got.FiveHour == nil || *got.FiveHour.Percent != 0 || got.FiveHour.ResetsAt != nil {
		t.Errorf("five_hour %+v", got.FiveHour)
	}
	if got.Session == nil || *got.Session.Percent != 0 {
		t.Errorf("session %+v", got.Session)
	}
	if w := got.SevenDay; w == nil || *w.Percent != 12 || *w.ResetsAt != "2026-11-11T20:00:00Z" || w.Reset {
		t.Errorf("seven_day %+v", w)
	}
	if got.SevenDayOpus != nil {
		t.Errorf("seven_day_opus %+v, want null", got.SevenDayOpus)
	}
	if len(got.Limits) != 3 {
		t.Fatalf("limits %+v", got.Limits)
	}
	if l := got.Limits[2]; l.Kind != "weekly_scoped" || l.Group != "weekly" || *l.Percent != 14 || l.Model != "Fable" || !l.IsActive || l.Severity != "normal" {
		t.Errorf("scoped limit %+v", l)
	}
	if got.ExtraUsage == nil || got.ExtraUsage.Enabled || got.ExtraUsage.Percent != nil {
		t.Errorf("extra_usage %+v", got.ExtraUsage)
	}
	if len(got.WeeklyBreakdown) != 4 || got.WeeklyBreakdown[0] != (BreakdownItem{Key: "claude_code", Name: "Claude Code", Percent: got.WeeklyBreakdown[0].Percent}) || *got.WeeklyBreakdown[0].Percent != 94 {
		t.Errorf("breakdown %+v", got.WeeklyBreakdown)
	}
	body, _ := json.Marshal(got)
	if strings.Contains(string(body), "00000000-0000") || strings.Contains(strings.ToLower(string(body)), "account") {
		t.Errorf("account leaked: %s", body)
	}

	// After the reset time the window is flagged as rolled over.
	later := Read(Sources{ClaudeJSON: []string{fixture}}, now.Add(time.Hour), "", "")
	if !later.SevenDay.Reset || !later.Limits[1].Reset || later.Limits[0].Reset {
		t.Errorf("reset flags: seven_day %+v limits %+v", later.SevenDay, later.Limits)
	}
}

func write(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, ".claude.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadNewestFetchWins(t *testing.T) {
	dir := t.TempDir()
	old := write(t, filepath.Join(dir, "a"), `{"cachedUsageUtilization":{"fetchedAtMs":1000,"utilization":{"five_hour":{"utilization":10}}}}`)
	newer := write(t, filepath.Join(dir, "b"), `{"cachedUsageUtilization":{"fetchedAtMs":2000,"utilization":{"five_hour":{"utilization":20}}}}`)
	none := write(t, filepath.Join(dir, "c"), `{"numStartups":1}`)
	got := Read(Sources{ClaudeJSON: []string{old, none, newer}}, time.UnixMilli(3000), "", dir)
	if *got.FiveHour.Percent != 20 || got.Source != filepath.Join("~", "b", ".claude.json") {
		t.Errorf("five_hour %v source %q", *got.FiveHour.Percent, got.Source)
	}
	// The session window falls back to five_hour when "limits" is gone.
	if got.Session == nil || *got.Session.Percent != 20 {
		t.Errorf("session %+v", got.Session)
	}
}

func TestReadTolerantOfOddShapes(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"garbage":    `not json`,
		"null cache": `{"cachedUsageUtilization":null}`,
		"wrong types": `{"cachedUsageUtilization":{"fetchedAtMs":"soon","utilization":{"five_hour":"x","seven_day":{"utilization":"12","resets_at":42},
			"limits":[1,"a",{"kind":7,"percent":null,"scope":"all"},{"kind":"session","percent":33.333,"resets_at":"tomorrow"}],
			"extra_usage":[],"seven_day_breakdown":{"rows":{}}}}}`,
		"empty": `{"cachedUsageUtilization":{}}`,
	} {
		p := write(t, filepath.Join(dir, strings.ReplaceAll(name, " ", "-")), body)
		got := Read(Sources{ClaudeJSON: []string{p}}, time.Now(), "", "")
		if name == "garbage" || name == "null cache" {
			if got.Available || got.Limits == nil || got.WeeklyBreakdown == nil {
				t.Errorf("%s: %+v", name, got)
			}
			continue
		}
		if !got.Available || got.FetchedAt != nil {
			t.Errorf("%s: available %v fetched %v", name, got.Available, got.FetchedAt)
		}
		if name == "wrong types" {
			if got.FiveHour == nil || *got.FiveHour.Percent != 33.33 || *got.FiveHour.ResetsAt != "tomorrow" || got.SevenDay.Percent != nil || len(got.Limits) != 2 {
				t.Errorf("wrong types: %+v %+v %+v", got.FiveHour, got.SevenDay, got.Limits)
			}
		}
	}
	if got := Read(Sources{}, time.Now(), "h", ""); got.Available || got.Source != "" {
		t.Errorf("no files: %+v", got)
	}
}

func TestCandidates(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude-work"), `{}`)
	got := Candidates(home, "/cfg", []string{filepath.Join(home, ".claude", "projects"), "/cfg/projects/"})
	want := []string{"/cfg/.claude.json", filepath.Join(home, ".claude", ".claude.json"), filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude-work", ".claude.json")}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q\nwant %q", got, want)
	}
}
