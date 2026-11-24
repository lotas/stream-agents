package server

import (
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"stream-agents/internal/store"
)

// SessionCostsResponse is the /api/v1/session-costs body. Other programs
// (the devtainer task board) parse it: add fields, never rename or remove them.
type SessionCostsResponse struct {
	GeneratedAt     string             `json:"generated_at"`
	Currency        string             `json:"currency"`
	CostMultipliers map[string]float64 `json:"cost_multipliers"` // billed / cost per agent; unlisted agents are 1
	Sessions        []SessionCost      `json:"sessions"`         // newest start first
	Totals          CostTotals         `json:"totals"`
}

type SessionCost struct {
	Agent            string      `json:"agent"`
	ID               string      `json:"id"`
	CWD              string      `json:"cwd"`
	Title            string      `json:"title"`
	Started          string      `json:"started"`       // RFC 3339 UTC; "" if unknown
	LastActivity     string      `json:"last_activity"` // RFC 3339 UTC, subagents included
	Models           []ModelCost `json:"models"`        // most expensive first; they add up to the session's numbers
	InputTokens      int         `json:"input_tokens"`
	OutputTokens     int         `json:"output_tokens"`
	CacheReadTokens  int         `json:"cache_read_tokens"`
	CacheWriteTokens int         `json:"cache_write_tokens"`
	Cost             float64     `json:"cost"`         // estimate at standard API prices
	Billed           float64     `json:"billed"`       // cost × the agent's cost multiplier
	CostPartial      bool        `json:"cost_partial"` // some tokens had no known price
	Subagents        int         `json:"subagents"`    // child sessions folded into this row
}

type ModelCost struct {
	Provider         string  `json:"provider"` // empty when the agent does not record one
	Model            string  `json:"model"`    // empty when unknown
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	Cost             float64 `json:"cost"`
	Billed           float64 `json:"billed"`
	CostPartial      bool    `json:"cost_partial"`
}

type CostTotals struct {
	Cost        float64 `json:"cost"`
	Billed      float64 `json:"billed"`
	CostPartial bool    `json:"cost_partial"`
}

// SessionCostsQuery selects sessions: IDs ("agent/id") when set, otherwise
// those under CWD that started in the last Days local days.
type SessionCostsQuery struct {
	IDs  []string
	CWD  string
	Days int
}

func parseSessionCostsQuery(r *http.Request) (SessionCostsQuery, string) {
	v := r.URL.Query()
	var q SessionCostsQuery
	if ids := v.Get("ids"); ids != "" {
		for _, id := range strings.Split(ids, ",") {
			if id = strings.TrimSpace(id); id != "" {
				q.IDs = append(q.IDs, id)
			}
		}
		if len(q.IDs) == 0 {
			return q, "ids must list agent/id pairs"
		}
		return q, ""
	}
	q.Days = defaultUsageDays
	if d := v.Get("days"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 {
			return q, "days must be a positive integer"
		}
		q.Days = min(n, maxUsageDays)
	}
	q.CWD = v.Get("cwd")
	if q.CWD == "" || !filepath.IsAbs(q.CWD) {
		return q, "cwd must be an absolute path, or pass ids"
	}
	q.CWD = filepath.Clean(q.CWD)
	return q, ""
}

func (a *apiHandlers) handleSessionCosts(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	q, msg := parseSessionCostsQuery(r)
	if msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	sessions, err := a.idx.ListAll(r.Context(), "", "")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list sessions")
		return
	}
	writeJSON(w, BuildSessionCosts(sessions, q, a.opts.Now(), a.opts.Location, a.idx.CostMultipliers))
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	writeJSONBody(w, map[string]string{"error": msg})
}

// underDir reports whether path is dir or inside it, on a path boundary.
func underDir(path, dir string) bool {
	if path == dir || dir == "/" {
		return path != ""
	}
	return strings.HasPrefix(path, dir+"/")
}

// BuildSessionCosts selects sessions per q and reports their cost, with the
// usage of each session's subagents and other child sessions folded in.
// Billing comes from the sessions (Index.ListAll), as for /api/v1/usage.
func BuildSessionCosts(sessions []store.Session, q SessionCostsQuery, now time.Time, loc *time.Location, multipliers map[string]float64) SessionCostsResponse {
	if multipliers == nil {
		multipliers = map[string]float64{}
	}
	key := func(agent, id string) string { return agent + "/" + id }
	byKey := make(map[string]store.Session, len(sessions))
	for _, s := range sessions {
		byKey[key(s.Agent, s.ID)] = s
	}
	// root follows Parent links to the top-level session; a session whose
	// parent is unknown is its own root.
	root := func(s store.Session) string {
		k := key(s.Agent, s.ID)
		for seen := map[string]bool{k: true}; s.Parent != ""; {
			p, ok := byKey[key(s.Agent, s.Parent)]
			pk := key(p.Agent, p.ID)
			if !ok || seen[pk] {
				break
			}
			seen[pk], s, k = true, p, pk
		}
		return k
	}
	children := make(map[string][]store.Session)
	for _, s := range sessions {
		if r := root(s); r != key(s.Agent, s.ID) {
			children[r] = append(children[r], s)
		}
	}

	var selected []store.Session
	if len(q.IDs) > 0 {
		seen := map[string]bool{}
		for _, id := range q.IDs {
			s, ok := byKey[id]
			if ok && !seen[id] && root(s) == id {
				seen[id] = true
				selected = append(selected, s)
			}
		}
	} else {
		local := now.In(loc)
		from := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -q.Days+1)
		for _, s := range sessions {
			date := s.Started
			if date.IsZero() {
				date = s.Modified
			}
			if underDir(s.Project, q.CWD) && !date.Before(from) && root(s) == key(s.Agent, s.ID) {
				selected = append(selected, s)
			}
		}
	}

	out := SessionCostsResponse{
		GeneratedAt: utcStamp(now), Currency: "USD", CostMultipliers: multipliers,
		Sessions: make([]SessionCost, 0, len(selected)),
	}
	for _, s := range selected {
		row := sessionCost(s, children[key(s.Agent, s.ID)])
		out.Sessions = append(out.Sessions, row)
		out.Totals.Cost += row.Cost
		out.Totals.Billed += row.Billed
		out.Totals.CostPartial = out.Totals.CostPartial || row.CostPartial
	}
	out.Totals.Cost, out.Totals.Billed = roundCost(out.Totals.Cost), roundCost(out.Totals.Billed)
	sort.SliceStable(out.Sessions, func(i, j int) bool {
		a, b := out.Sessions[i], out.Sessions[j]
		if a.Started != b.Started {
			return a.Started > b.Started
		}
		return a.Agent+a.ID < b.Agent+b.ID
	})
	return out
}

func sessionCost(s store.Session, children []store.Session) SessionCost {
	row := SessionCost{Agent: s.Agent, ID: s.ID, CWD: s.Project, Title: s.Title, Subagents: len(children), Models: []ModelCost{}}
	if !s.Started.IsZero() {
		row.Started = utcStamp(s.Started)
	}
	var last time.Time
	models := map[string]*ModelCost{}
	var order []string
	for _, part := range append([]store.Session{s}, children...) {
		if t := lastActivity(part); t.After(last) {
			last = t
		}
		for _, u := range part.Usage {
			if u.Total() == 0 && u.Cost == 0 && u.Billed == 0 {
				continue
			}
			k := u.Provider + "/" + u.Model
			m := models[k]
			if m == nil {
				m = &ModelCost{Provider: u.Provider, Model: u.Model}
				models[k] = m
				order = append(order, k)
			}
			m.InputTokens += u.InputTokens
			m.OutputTokens += u.OutputTokens
			m.CacheReadTokens += u.CacheReadTokens
			m.CacheWriteTokens += u.CacheCreationTokens
			m.Cost += u.Cost
			m.Billed += u.Billed
			m.CostPartial = m.CostPartial || u.CostPartial()
		}
	}
	if !last.IsZero() {
		row.LastActivity = utcStamp(last)
	}
	for _, k := range order {
		m := models[k]
		m.Cost, m.Billed = roundCost(m.Cost), roundCost(m.Billed)
		row.Models = append(row.Models, *m)
		row.InputTokens += m.InputTokens
		row.OutputTokens += m.OutputTokens
		row.CacheReadTokens += m.CacheReadTokens
		row.CacheWriteTokens += m.CacheWriteTokens
		row.Cost += m.Cost
		row.Billed += m.Billed
		row.CostPartial = row.CostPartial || m.CostPartial
	}
	row.Cost, row.Billed = roundCost(row.Cost), roundCost(row.Billed)
	sort.SliceStable(row.Models, func(i, j int) bool { return row.Models[i].Cost > row.Models[j].Cost })
	return row
}

// lastActivity is the newest record time of s, or its modification time.
func lastActivity(s store.Session) time.Time {
	last := s.Modified
	for _, t := range s.ActivityTimes {
		if t.After(last) {
			last = t
		}
	}
	return last
}
