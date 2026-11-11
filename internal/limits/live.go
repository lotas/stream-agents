package limits

import (
	"encoding/json"
	"os"
	"time"
)

// StatuslineFile is where the status line snippet (README) saves Claude
// Code's status line payload, in the config dir of the session (derived from
// its transcript_path). It needs nothing but sh, so it also works for
// sessions in containers that share the config dir but not this binary.
const StatuslineFile = "stream-agents-statusline.json"

// liveReading is the rate limits of one saved status line payload.
type liveReading struct {
	at                 time.Time // file mtime: when a session last reported them
	fiveHour, sevenDay *Window
}

// newestLive returns the most recently written payload among paths that has
// at least one rate-limit window.
func newestLive(paths []string, now time.Time) (liveReading, bool) {
	var best liveReading
	found := false
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || (found && !fi.ModTime().After(best.at)) {
			continue
		}
		r, ok := readLive(p, now)
		if !ok {
			continue
		}
		r.at = fi.ModTime()
		best, found = r, true
	}
	return best, found
}

func readLive(path string, now time.Time) (liveReading, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return liveReading{}, false
	}
	var payload struct {
		RateLimits map[string]any `json:"rate_limits"`
	}
	if json.Unmarshal(data, &payload) != nil { // e.g. caught mid-write
		return liveReading{}, false
	}
	r := liveReading{fiveHour: liveWindow(payload.RateLimits["five_hour"], now), sevenDay: liveWindow(payload.RateLimits["seven_day"], now)}
	return r, r.fiveHour != nil || r.sevenDay != nil
}

// liveWindow reads a status line window: used_percentage, resets_at in Unix
// seconds.
func liveWindow(v any, now time.Time) *Window {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	w := &Window{Percent: pct(m["used_percentage"])}
	if secs, ok := num(m["resets_at"]); ok && secs > 0 {
		t := time.Unix(int64(secs), 0)
		s := stamp(t)
		w.ResetsAt, w.Reset = &s, !t.After(now)
	} else {
		w.ResetsAt, w.Reset = resets(m["resets_at"], now)
	}
	if w.Percent == nil && w.ResetsAt == nil {
		return nil
	}
	return w
}

// StatuslineCommand is the status line command the README documents, with
// "foxtail" standing for the user's own status line command. It saves the
// payload (only when it has rate_limits, so a session without them never
// overwrites a good reading) next to the transcripts' projects dir, then
// runs foxtail with the same input if it is installed. It always exits 0.
// POSIX sh and sed only.
const StatuslineCommand = `i=$(cat); case $i in *'"rate_limits"'*) d=$(printf '%s\n' "$i" | sed -n 's|.*"transcript_path": *"\([^"]*\)/projects/.*|\1|p' | head -n 1); [ -d "$d" ] && printf '%s\n' "$i" > "$d/.stream-agents-statusline.$$" && mv -f "$d/.stream-agents-statusline.$$" "$d/` + StatuslineFile + `";; esac; command -v foxtail >/dev/null 2>&1 && printf '%s\n' "$i" | foxtail || :`
