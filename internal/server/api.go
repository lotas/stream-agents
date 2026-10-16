package server

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"stream-agents/internal/report"
	"stream-agents/internal/store"
)

const (
	defaultUsageDays = 31
	maxUsageDays     = 400
)

// guard keeps non-loopback peers to /api/v1/ unless the UI is opted in, and
// logs one line per /api/ request.
func guard(next http.Handler, opts Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		local := isLoopbackPeer(r.RemoteAddr)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if !local && !opts.AllowRemoteUI {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() { opts.Logf("api: %s %s %s %d", r.RemoteAddr, r.Method, r.URL.Path, rec.status) }()
		if !local && !opts.AllowRemoteUI && !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			http.Error(rec, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rec, r)
	})
}

func isLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

type apiHandlers struct {
	idx  *store.Index
	opts Options
}

// readOnly rejects anything but GET and HEAD, and reports whether to go on.
func readOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	Host    string `json:"host"`
}

func (a *apiHandlers) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	writeJSON(w, healthResponse{OK: true, Version: a.opts.Version, Host: a.opts.Host})
}

// UsageResponse is the /api/v1/usage body. Other programs parse it: add
// fields, never rename or remove them.
type UsageResponse struct {
	GeneratedAt     string             `json:"generated_at"`
	Timezone        string             `json:"timezone"`
	UTCOffset       string             `json:"utc_offset"` // at GeneratedAt
	Currency        string             `json:"currency"`
	CostMultipliers map[string]float64 `json:"cost_multipliers"` // billed / cost per agent; unlisted agents are 1
	Days            []UsageDay         `json:"days"`             // oldest first, ending today
}

type UsageDay struct {
	Date string       `json:"date"` // local calendar day, YYYY-MM-DD
	By   []UsageEntry `json:"by"`   // empty on days without usage
}

type UsageEntry struct {
	Agent            string  `json:"agent"`
	Provider         string  `json:"provider"` // empty when the agent does not record one
	Model            string  `json:"model"`    // empty when unknown
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	Cost             float64 `json:"cost"`         // estimate at standard API prices
	Billed           float64 `json:"billed"`       // cost × the agent's cost multiplier
	CostPartial      bool    `json:"cost_partial"` // some tokens had no known price
	Sessions         int     `json:"sessions"`     // sessions started this day that used this model
}

func (a *apiHandlers) handleUsage(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	days := defaultUsageDays
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			http.Error(w, "days must be a positive integer", http.StatusBadRequest)
			return
		}
		days = min(n, maxUsageDays)
	}
	sessions, err := a.idx.ListAll(r.Context(), "", "")
	if err != nil {
		http.Error(w, "failed to list sessions", http.StatusInternalServerError)
		return
	}
	writeJSON(w, BuildUsage(sessions, a.opts.Now(), a.opts.Location, days, a.idx.CostMultipliers))
}

// BuildUsage aggregates sessions into the days local days ending on now's,
// one entry per agent and model. Sessions count towards the day they started.
func BuildUsage(sessions []store.Session, now time.Time, loc *time.Location, days int, multipliers map[string]float64) UsageResponse {
	now = now.In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from := today.AddDate(0, 0, -days+1)
	rep := report.Build(sessions, report.Options{Group: "day", From: from, To: today}, loc)
	periods := make(map[string]*report.Row, len(rep.Periods))
	for _, p := range rep.Periods {
		periods[p.Label] = p
	}
	if multipliers == nil {
		multipliers = map[string]float64{}
	}
	out := UsageResponse{
		GeneratedAt:     now.UTC().Format(time.RFC3339),
		Timezone:        timezoneName(loc),
		UTCOffset:       now.Format("-07:00"),
		Currency:        "USD",
		CostMultipliers: multipliers,
		Days:            make([]UsageDay, 0, days),
	}
	for i := range days {
		date := from.AddDate(0, 0, i).Format("2006-01-02")
		day := UsageDay{Date: date, By: []UsageEntry{}}
		if p := periods[date]; p != nil {
			for _, m := range p.AgentModels() {
				day.By = append(day.By, UsageEntry{
					Agent: m.Agent, Provider: m.Provider, Model: m.Model,
					InputTokens: m.Input, OutputTokens: m.Output, CacheReadTokens: m.CacheRead, CacheWriteTokens: m.CacheWrite,
					Cost: roundCost(m.Cost), Billed: roundCost(m.Billed), CostPartial: m.CostPartial, Sessions: m.Sessions,
				})
			}
		}
		out.Days = append(out.Days, day)
	}
	return out
}

// roundCost trims float noise such as 0.30000000000000004.
func roundCost(c float64) float64 { return math.Round(c*1e6) / 1e6 }

// timezoneName returns loc's IANA name, resolving time.Local through $TZ or
// /etc/localtime, and falling back to the zone abbreviation.
func timezoneName(loc *time.Location) string {
	if loc != time.Local {
		return loc.String()
	}
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	// Linux links to /usr/share/zoneinfo/<name>, macOS to
	// /var/db/timezone/zoneinfo/<name>.
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	name, _ := time.Now().Zone()
	return name
}
