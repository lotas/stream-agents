package limits

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// LiveFile is the file under the state dir where `stream-agents statusline`
// records the rate limits a running Claude Code session reports.
const LiveFile = "limits.json"

// rewriteAfter limits how often an unchanged report is rewritten; the
// status line can run several times a second.
const rewriteAfter = 30 * time.Second

// liveRecord is LiveFile's content.
type liveRecord struct {
	RecordedAt time.Time       `json:"recorded_at"`
	SessionID  string          `json:"session_id,omitempty"`
	RateLimits json.RawMessage `json:"rate_limits"` // as Claude Code sent it
}

// RecordStatusline saves the rate_limits of a Claude Code status line
// payload to path. Claude Code only includes them for subscribers after the
// first API response, so payloads without them are ignored. It reports
// whether it wrote the file.
func RecordStatusline(payload []byte, path string, now time.Time) (bool, error) {
	var in struct {
		SessionID  string          `json:"session_id"`
		RateLimits json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(payload, &in); err != nil {
		return false, err
	}
	var rl map[string]any
	if json.Unmarshal(in.RateLimits, &rl) != nil || (rl["five_hour"] == nil && rl["seven_day"] == nil) {
		return false, nil
	}
	if old, ok := readLive(path); ok && now.Sub(old.RecordedAt) < rewriteAfter && now.Sub(old.RecordedAt) >= 0 &&
		jsonEqual(old.RateLimits, in.RateLimits) {
		return false, nil
	}
	data, err := json.Marshal(liveRecord{RecordedAt: now.UTC(), SessionID: in.SessionID, RateLimits: in.RateLimits})
	if err != nil {
		return false, err
	}
	return true, writeFileAtomic(path, data)
}

func readLive(path string) (liveRecord, bool) {
	var r liveRecord
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &r) != nil || r.RecordedAt.IsZero() {
		return liveRecord{}, false
	}
	return r, true
}

// liveWindows returns the status line's five_hour and seven_day windows
// (nil when absent).
func (r liveRecord) windows(now time.Time) (fiveHour, sevenDay *Window) {
	var rl map[string]any
	_ = json.Unmarshal(r.RateLimits, &rl)
	return liveWindow(rl["five_hour"], now), liveWindow(rl["seven_day"], now)
}

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

func jsonEqual(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
