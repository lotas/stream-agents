package limits

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A status line payload as Claude Code sends it (trimmed); TRANSCRIPT is
// replaced by a transcript path.
const statuslinePayload = `{"session_id":"s1","transcript_path":"TRANSCRIPT","model":{"id":"claude-opus-5-5"},` +
	`"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1794436800},"seven_day":{"used_percentage":13,"resets_at":1794427200}}}`

// runStatusline runs StatuslineCommand like Claude Code does, with a fake
// foxtail on PATH that prints what it got.
func runStatusline(t *testing.T, payload string, withFoxtail bool) string {
	t.Helper()
	bin := t.TempDir()
	if withFoxtail {
		if err := os.WriteFile(filepath.Join(bin, "foxtail"), []byte("#!/bin/sh\nprintf 'fox:'; cat\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("/bin/sh", "-c", StatuslineCommand)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	cmd.Stdin = strings.NewReader(payload)
	out, _ := cmd.Output()
	return string(out)
}

func configDir(t *testing.T) (dir, transcript string) {
	dir = t.TempDir()
	transcript = filepath.Join(dir, "projects", "-Users-yarik-dev-x", "s1.jsonl")
	return dir, transcript
}

func TestStatuslineCommand(t *testing.T) {
	dir, transcript := configDir(t)
	payload := strings.Replace(statuslinePayload, "TRANSCRIPT", transcript, 1)
	if out := runStatusline(t, payload, true); out != "fox:"+payload+"\n" {
		t.Errorf("foxtail output %q", out)
	}
	saved, err := os.ReadFile(filepath.Join(dir, StatuslineFile))
	if err != nil || !json.Valid(saved) || strings.TrimSpace(string(saved)) != payload {
		t.Fatalf("saved %q, %v", saved, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".stream-agents-statusline.*")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}

	// Without foxtail installed it still records and prints nothing.
	dir2, transcript2 := configDir(t)
	if out := runStatusline(t, strings.Replace(statuslinePayload, "TRANSCRIPT", transcript2, 1), false); out != "" {
		t.Errorf("output without foxtail: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir2, StatuslineFile)); err != nil {
		t.Error(err)
	}

	// No rate_limits (API key, or before the first response), or a config dir
	// that doesn't exist here: nothing is written, foxtail still runs.
	dir3, transcript3 := configDir(t)
	for _, p := range []string{
		`{"session_id":"s2","transcript_path":"` + transcript3 + `"}`,
		strings.Replace(statuslinePayload, "TRANSCRIPT", "/nonexistent/projects/x/s.jsonl", 1),
	} {
		if out := runStatusline(t, p, true); !strings.HasPrefix(out, "fox:") {
			t.Errorf("%s: output %q", p, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir3, StatuslineFile)); !os.IsNotExist(err) {
		t.Errorf("written without rate_limits: %v", err)
	}
}

func TestReadmeHasStatuslineCommand(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(StatuslineCommand)
	quoted := strings.TrimSpace(buf.String())
	if !strings.Contains(string(readme), quoted) {
		t.Errorf("README.md lacks the status line command; put this in its settings.json example:\n%s", quoted)
	}
}

func writeLive(t *testing.T, dir, payload string, at time.Time) string {
	t.Helper()
	p := filepath.Join(dir, StatuslineFile)
	if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStatuslineOverridesOlderCache(t *testing.T) {
	at := time.Date(2026, 11, 11, 19, 20, 0, 0, time.UTC) // after the fixture's 19:10:32 fetch
	live := writeLive(t, t.TempDir(), statuslinePayload, at)
	older := writeLive(t, t.TempDir(), strings.Replace(statuslinePayload, "23.5", "99", 1), at.Add(-time.Minute))
	broken := writeLive(t, t.TempDir(), `{"rate_limits":{"five_ho`, at.Add(time.Minute)) // caught mid-write
	now := at.Add(10 * time.Second)
	got := Read(Sources{ClaudeJSON: []string{fixture}, Statusline: []string{older, "/nonexistent", live, broken}}, now, "", "")
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

	// A status line report older than the cache fetch loses.
	stale := writeLive(t, t.TempDir(), statuslinePayload, at.Add(-time.Hour))
	if got := Read(Sources{ClaudeJSON: []string{fixture}, Statusline: []string{stale}}, now, "", ""); got.Origin != "cache" || *got.FiveHour.Percent != 0 || *got.StatuslineAt != "2026-11-11T18:20:00Z" {
		t.Errorf("older statusline: origin %q five_hour %+v", got.Origin, got.FiveHour)
	}

	// Without any cache the status line alone is enough.
	got = Read(Sources{Statusline: []string{live}}, now, "", "")
	if !got.Available || got.Origin != "statusline" || *got.FiveHour.Percent != 23.5 || len(got.Limits) != 0 || got.SevenDayOpus != nil {
		t.Errorf("statusline only: %+v", got)
	}
}
