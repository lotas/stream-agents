// Package limits reads the Claude subscription usage limits that Claude Code
// caches in its .claude.json ("cachedUsageUtilization"). The cache is an
// undocumented internal format: every field is optional, unknown or renamed
// keys are skipped, and nothing here fails on a shape it doesn't expect.
package limits

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Response is the /api/v1/limits body. Other programs parse it: add fields,
// never rename or remove them.
type Response struct {
	GeneratedAt string `json:"generated_at"`
	Host        string `json:"host"`
	// Available is false when no .claude.json with a usage cache was found;
	// everything below is then null or empty.
	Available bool `json:"available"`
	// Origin is where the five_hour/seven_day/session numbers come from:
	// "statusline" (a running Claude Code session), "cache" or "".
	Origin          string          `json:"origin"`
	FetchedAt       *string         `json:"fetched_at"`       // RFC 3339 UTC, when Origin's numbers were current
	AgeSeconds      *int64          `json:"age_seconds"`      // generated_at - fetched_at
	StatuslineAt    *string         `json:"statusline_at"`    // last report from a running session
	CacheFetchedAt  *string         `json:"cache_fetched_at"` // when Claude Code last fetched its usage cache
	Source          string          `json:"source"`           // cache file read, with the home dir as ~; "" if none
	FiveHour        *Window         `json:"five_hour"`
	SevenDay        *Window         `json:"seven_day"`
	SevenDayOpus    *Window         `json:"seven_day_opus"`
	SevenDaySonnet  *Window         `json:"seven_day_sonnet"`
	Session         *Window         `json:"session"`
	Limits          []Limit         `json:"limits"`
	ExtraUsage      *ExtraUsage     `json:"extra_usage"`
	WeeklyBreakdown []BreakdownItem `json:"weekly_breakdown"`
}

// Window is one rate-limit window.
type Window struct {
	Percent  *float64 `json:"percent"`   // 0-100 used; null if unknown
	ResetsAt *string  `json:"resets_at"` // RFC 3339 UTC; null when no window is running
	// Reset is true when ResetsAt is already past at generated_at: the window
	// rolled over after the fetch, so Percent belongs to the previous one.
	Reset bool `json:"reset"`
}

// Limit is one entry of Claude Code's own limits list, as shown by /usage.
type Limit struct {
	Kind     string   `json:"kind"`     // session, weekly_all, weekly_scoped, ...
	Group    string   `json:"group"`    // session, weekly, ...
	Percent  *float64 `json:"percent"`  // 0-100
	Severity string   `json:"severity"` // normal, ... ("" if absent)
	ResetsAt *string  `json:"resets_at"`
	Reset    bool     `json:"reset"`
	IsActive bool     `json:"is_active"`
	Model    string   `json:"model"`   // scope.model.display_name for scoped limits, e.g. "Fable"
	Surface  string   `json:"surface"` // scope.surface when it is a string
}

// ExtraUsage is pay-as-you-go usage beyond the plan.
type ExtraUsage struct {
	Enabled           bool     `json:"enabled"`
	Percent           *float64 `json:"percent"` // of the monthly limit; null if none
	SpendLimitReached bool     `json:"spend_limit_reached"`
}

// BreakdownItem is one surface's share of the current weekly usage.
type BreakdownItem struct {
	Key     string   `json:"key"`  // claude_code, chat, cowork, other, ...
	Name    string   `json:"name"` // display name
	Percent *float64 `json:"percent"`
}

// Candidates lists the .claude.json files to look at: one in every Claude
// config dir (projectRoots are "<config dir>/projects" dirs), in
// $CLAUDE_CONFIG_DIR, ~/.claude.json and ~/.claude*/.claude.json.
// Missing files are fine; callers just skip them.
func Candidates(home, configDirEnv string, projectRoots []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	if configDirEnv != "" {
		add(filepath.Join(configDirEnv, ".claude.json"))
	}
	for _, r := range projectRoots {
		if r != "" {
			add(filepath.Join(filepath.Dir(filepath.Clean(r)), ".claude.json"))
		}
	}
	if home != "" {
		add(filepath.Join(home, ".claude.json"))
		matches, _ := filepath.Glob(filepath.Join(home, ".claude*", ".claude.json"))
		for _, m := range matches {
			add(m)
		}
	}
	return out
}

// Sources are where Read looks.
type Sources struct {
	ClaudeJSON []string // .claude.json candidates; the newest fetchedAtMs wins
	Statusline string   // LiveFile written by `stream-agents statusline`; "" for none
}

// Read builds the response from Claude Code's usage cache and, where newer,
// the rate limits a running session reported through its status line. It
// never fails: unreadable files and odd shapes are skipped.
func Read(src Sources, now time.Time, host, home string) Response {
	out := readCache(src.ClaudeJSON, now, host, home)
	live, ok := Response{}, false
	if src.Statusline != "" {
		var rec liveRecord
		if rec, ok = readLive(src.Statusline); ok {
			live.FiveHour, live.SevenDay = rec.windows(now)
			ok = live.FiveHour != nil || live.SevenDay != nil
			if ok {
				s := stamp(rec.RecordedAt)
				out.StatuslineAt = &s
			}
		}
		if ok && (out.CacheFetchedAt == nil || *out.StatuslineAt > *out.CacheFetchedAt) {
			out.Available, out.Origin, out.FetchedAt = true, "statusline", out.StatuslineAt
			if live.FiveHour != nil {
				out.FiveHour = live.FiveHour
				w := *live.FiveHour
				out.Session = &w
				setLimit(out.Limits, "session", live.FiveHour)
			}
			if live.SevenDay != nil {
				out.SevenDay = live.SevenDay
				setLimit(out.Limits, "weekly_all", live.SevenDay)
			}
		}
	}
	if out.FetchedAt != nil {
		if t, err := time.Parse(time.RFC3339, *out.FetchedAt); err == nil {
			age := int64(now.Sub(t).Seconds())
			out.AgeSeconds = &age
		}
	}
	return out
}

// setLimit updates the limits entry of kind with a newer reading.
func setLimit(limits []Limit, kind string, w *Window) {
	for i := range limits {
		if limits[i].Kind == kind {
			limits[i].Percent, limits[i].ResetsAt, limits[i].Reset = w.Percent, w.ResetsAt, w.Reset
		}
	}
}

func readCache(paths []string, now time.Time, host, home string) Response {
	out := Response{GeneratedAt: stamp(now), Host: host, Limits: []Limit{}, WeeklyBreakdown: []BreakdownItem{}}
	var best map[string]any
	var bestAt float64 = -1
	for _, p := range paths {
		c, ok := load(p)
		if !ok {
			continue
		}
		at, _ := num(c["fetchedAtMs"])
		if at > bestAt {
			best, bestAt, out.Source = c, at, tilde(p, home)
		}
	}
	if best == nil {
		return out
	}
	out.Available, out.Origin = true, "cache"
	if bestAt > 0 {
		s := stamp(time.UnixMilli(int64(bestAt)))
		out.FetchedAt, out.CacheFetchedAt = &s, &s
	}
	u, _ := best["utilization"].(map[string]any)
	out.FiveHour = window(u["five_hour"], now)
	out.SevenDay = window(u["seven_day"], now)
	out.SevenDayOpus = window(u["seven_day_opus"], now)
	out.SevenDaySonnet = window(u["seven_day_sonnet"], now)

	list, _ := u["limits"].([]any)
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		l := Limit{Kind: str(m["kind"]), Group: str(m["group"]), Severity: str(m["severity"]), IsActive: m["is_active"] == true}
		l.Percent = pct(m["percent"])
		l.ResetsAt, l.Reset = resets(m["resets_at"], now)
		if scope, ok := m["scope"].(map[string]any); ok {
			if model, ok := scope["model"].(map[string]any); ok {
				l.Model = str(model["display_name"])
				if l.Model == "" {
					l.Model = str(model["id"])
				}
			}
			l.Surface = str(scope["surface"])
		}
		out.Limits = append(out.Limits, l)
		if out.Session == nil && l.Kind == "session" {
			out.Session = &Window{Percent: l.Percent, ResetsAt: l.ResetsAt, Reset: l.Reset}
		}
		if out.SevenDay == nil && l.Kind == "weekly_all" {
			out.SevenDay = &Window{Percent: l.Percent, ResetsAt: l.ResetsAt, Reset: l.Reset}
		}
	}
	// The session limit is the five-hour window; fill either from the other
	// if Claude Code stops writing one of them.
	if out.Session == nil && out.FiveHour != nil {
		w := *out.FiveHour
		out.Session = &w
	}
	if out.FiveHour == nil && out.Session != nil {
		w := *out.Session
		out.FiveHour = &w
	}

	if e, ok := u["extra_usage"].(map[string]any); ok {
		out.ExtraUsage = &ExtraUsage{
			Enabled:           e["is_enabled"] == true,
			Percent:           pct(e["utilization"]),
			SpendLimitReached: e["spend_limit_reached"] == true,
		}
	}
	if b, ok := u["seven_day_breakdown"].(map[string]any); ok {
		rows, _ := b["rows"].([]any)
		for _, v := range rows {
			if m, ok := v.(map[string]any); ok {
				out.WeeklyBreakdown = append(out.WeeklyBreakdown, BreakdownItem{Key: str(m["key"]), Name: str(m["display_name"]), Percent: pct(m["percent"])})
			}
		}
	}
	return out
}

// load returns the cachedUsageUtilization object of a .claude.json.
func load(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var doc struct {
		Cache json.RawMessage `json:"cachedUsageUtilization"`
	}
	if json.Unmarshal(data, &doc) != nil || len(doc.Cache) == 0 {
		return nil, false
	}
	var c map[string]any
	if json.Unmarshal(doc.Cache, &c) != nil || c == nil {
		return nil, false
	}
	return c, true
}

func window(v any, now time.Time) *Window {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	w := &Window{Percent: pct(m["utilization"])}
	if w.Percent == nil {
		w.Percent = pct(m["percent"])
	}
	w.ResetsAt, w.Reset = resets(m["resets_at"], now)
	return w
}

func resets(v any, now time.Time) (*string, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return &s, false // pass through what we can't parse
	}
	r := stamp(t)
	return &r, !t.After(now)
}

func pct(v any) *float64 {
	f, ok := num(v)
	if !ok {
		return nil
	}
	f = math.Round(f*100) / 100
	return &f
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func tilde(p, home string) string {
	if home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
