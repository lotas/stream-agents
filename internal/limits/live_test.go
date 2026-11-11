package limits

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A status line payload as Claude Code sends it (trimmed).
const statuslinePayload = `{"session_id":"s1","model":{"id":"claude-opus-5-5"},
 "rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1794436800},"seven_day":{"used_percentage":13,"resets_at":1794427200}}}`

func TestStatuslineOverridesOlderCache(t *testing.T) {
	live := filepath.Join(t.TempDir(), "state", LiveFile)
	at := time.Date(2026, 11, 11, 19, 20, 0, 0, time.UTC) // after the fixture's 19:10:32 fetch
	if wrote, err := RecordStatusline([]byte(statuslinePayload), live, at); err != nil || !wrote {
		t.Fatalf("record: %v %v", wrote, err)
	}
	now := at.Add(10 * time.Second)
	got := Read(Sources{ClaudeJSON: []string{fixture}, Statusline: live}, now, "", "")
	if got.Origin != "statusline" || *got.FetchedAt != "2026-11-11T19:20:00Z" || *got.AgeSeconds != 10 ||
		*got.StatuslineAt != "2026-11-11T19:20:00Z" || *got.CacheFetchedAt != "2026-11-11T19:10:32Z" {
		t.Fatalf("origin %q fetched %v age %v", got.Origin, *got.FetchedAt, *got.AgeSeconds)
	}
	if w := got.FiveHour; *w.Percent != 23.5 || *w.ResetsAt != "2026-11-11T22:40:00Z" || w.Reset {
		t.Errorf("five_hour %+v", w)
	}
	if *got.Session.Percent != 23.5 || *got.SevenDay.Percent != 13 || *got.SevenDay.ResetsAt != "2026-11-11T20:00:00Z" {
		t.Errorf("session %+v seven_day %+v", got.Session, got.SevenDay)
	}
	// The limits list follows; the per-model limit stays from the cache.
	if *got.Limits[0].Percent != 23.5 || *got.Limits[1].Percent != 13 || *got.Limits[2].Percent != 14 {
		t.Errorf("limits %+v", got.Limits)
	}

	// A newer cache fetch wins again.
	if got := Read(Sources{ClaudeJSON: []string{fixture}, Statusline: live}, now, "", ""); got.Origin != "statusline" {
		t.Errorf("origin %q", got.Origin)
	}
	old := filepath.Join(t.TempDir(), LiveFile)
	if _, err := RecordStatusline([]byte(statuslinePayload), old, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := Read(Sources{ClaudeJSON: []string{fixture}, Statusline: old}, now, "", ""); got.Origin != "cache" || *got.FiveHour.Percent != 0 || *got.StatuslineAt != "2026-11-11T18:20:00Z" {
		t.Errorf("older statusline: origin %q five_hour %+v", got.Origin, got.FiveHour)
	}

	// Without any cache the status line alone is enough.
	got = Read(Sources{Statusline: live}, now, "", "")
	if !got.Available || got.Origin != "statusline" || *got.FiveHour.Percent != 23.5 || len(got.Limits) != 0 || got.SevenDayOpus != nil {
		t.Errorf("statusline only: %+v", got)
	}
}

func TestRecordStatuslineSkips(t *testing.T) {
	path := filepath.Join(t.TempDir(), LiveFile)
	now := time.Date(2026, 11, 11, 19, 0, 0, 0, time.UTC)
	for _, p := range []string{`{"session_id":"s"}`, `{"rate_limits":null}`, `{"rate_limits":{"spend_limit":{}}}`} {
		if wrote, err := RecordStatusline([]byte(p), path, now); wrote || err != nil {
			t.Errorf("%s: wrote %v err %v", p, wrote, err)
		}
	}
	if _, err := RecordStatusline([]byte("garbage"), path, now); err == nil {
		t.Error("garbage: no error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file written: %v", err)
	}
	// Unchanged numbers are rewritten at most every 30 s; changed ones at once.
	for i, tc := range []struct {
		payload string
		at      time.Duration
		want    bool
	}{
		{statuslinePayload, 0, true},
		{statuslinePayload, 5 * time.Second, false},
		{`{"rate_limits":{"five_hour":{"used_percentage":24,"resets_at":1794436800}}}`, 6 * time.Second, true},
		{`{"rate_limits":{"five_hour":{"used_percentage":24,"resets_at":1794436800}}}`, 40 * time.Second, true},
	} {
		if wrote, err := RecordStatusline([]byte(tc.payload), path, now.Add(tc.at)); wrote != tc.want || err != nil {
			t.Errorf("step %d: wrote %v err %v", i, wrote, err)
		}
	}
}
