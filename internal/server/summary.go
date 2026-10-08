package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"stream-agents/internal/store"
)

// Summary is a compact usage overview over a few fixed periods, used by the
// menu bar tray and /api/summary.json. Sessions count towards the local day
// they started on.
type Summary struct {
	Today       SummaryPeriod   `json:"today"`
	Week        SummaryPeriod   `json:"week"`        // last 7 days, including today
	Month       SummaryPeriod   `json:"month"`       // current calendar month
	LastMonth   SummaryPeriod   `json:"lastMonth"`   // previous calendar month
	Last3Months SummaryPeriod   `json:"last3Months"` // current and two previous calendar months
	Active      []SummaryActive `json:"active"`
}

type SummaryPeriod struct {
	From string `json:"from"` // first day, YYYY-MM-DD
	To   string `json:"to"`   // last day, inclusive
	SummaryUsage
	Agents []SummaryAgent `json:"agents"`
}

type SummaryAgent struct {
	Agent string `json:"agent"`
	SummaryUsage
}

type SummaryUsage struct {
	Sessions    int     `json:"sessions"`
	Tokens      int     `json:"tokens"`
	Cost        float64 `json:"cost"`
	CostPartial bool    `json:"costPartial"` // some sessions had tokens but no known price
}

type SummaryActive struct {
	Agent string `json:"agent"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func summaryUsage(row statsRow) SummaryUsage {
	return SummaryUsage{Sessions: row.Sessions, Tokens: row.Tokens, Cost: row.Cost, CostPartial: row.WithCost < row.WithTokens}
}

// summaryPeriod aggregates sessions started in [from, to).
func summaryPeriod(sessions []store.Session, from, to time.Time) SummaryPeriod {
	total, _, byAgent := aggregateStats(sessions, "day", from, to)
	p := SummaryPeriod{
		From:         from.Format("2006-01-02"),
		To:           to.AddDate(0, 0, -1).Format("2006-01-02"),
		SummaryUsage: summaryUsage(total),
		Agents:       []SummaryAgent{},
	}
	for _, row := range byAgent {
		if row.Sessions > 0 {
			p.Agents = append(p.Agents, SummaryAgent{Agent: row.Label, SummaryUsage: summaryUsage(*row)})
		}
	}
	return p
}

// BuildSummary aggregates sessions relative to now's local day. Sessions must
// be sorted by Modified desc (as returned by Index.ListAll).
func BuildSummary(sessions []store.Session, now time.Time) Summary {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.AddDate(0, 0, 1)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	s := Summary{
		Today:       summaryPeriod(sessions, today, tomorrow),
		Week:        summaryPeriod(sessions, today.AddDate(0, 0, -6), tomorrow),
		Month:       summaryPeriod(sessions, month, tomorrow),
		LastMonth:   summaryPeriod(sessions, month.AddDate(0, -1, 0), month),
		Last3Months: summaryPeriod(sessions, month.AddDate(0, -2, 0), tomorrow),
		Active:      []SummaryActive{},
	}
	for _, hot := range buildHotSessions(sessions, "", "", hotPanelLimit) {
		s.Active = append(s.Active, SummaryActive{Agent: hot.Agent, Title: hot.Title, URL: "/session/" + hot.Agent + "/" + hot.ID})
	}
	return s
}

// LoadSummary lists all sessions from idx and summarizes them as of now.
func LoadSummary(ctx context.Context, idx *store.Index) (Summary, error) {
	sessions, err := idx.ListAll(ctx, "", "")
	if err != nil {
		return Summary{}, err
	}
	return BuildSummary(sessions, time.Now()), nil
}

func (h *handlers) handleSummaryJSON(w http.ResponseWriter, r *http.Request) {
	s, err := LoadSummary(r.Context(), h.idx)
	if err != nil {
		http.Error(w, "failed to list sessions: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s)
}
