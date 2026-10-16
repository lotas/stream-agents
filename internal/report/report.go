// Package report aggregates session usage into tables for the command line.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"stream-agents/internal/store"
)

// Sections that can be requested with Options.Sections.
const (
	SectionPeriod   = "period"
	SectionAgent    = "agent"
	SectionProvider = "provider"
	SectionModel    = "model"
)

var AllSections = []string{SectionPeriod, SectionAgent, SectionProvider, SectionModel}

type Options struct {
	Group     string    // day, month or year
	From, To  time.Time // inclusive local days; zero means unbounded
	Agent     string
	Project   string
	Breakdown bool // list per-model rows under each period
	Sections  []string
	// CostMultipliers is reported alongside Billed so readers know how it
	// relates to Cost; Billed itself comes from the sessions.
	CostMultipliers map[string]float64
}

type Row struct {
	Label       string  `json:"label"`
	Sessions    int     `json:"sessions"`
	Input       int     `json:"input"`
	Output      int     `json:"output"`
	CacheRead   int     `json:"cacheRead"`
	CacheWrite  int     `json:"cacheWrite"`
	Tokens      int     `json:"tokens"`
	Cost        float64 `json:"cost"`                  // at API prices
	Billed      float64 `json:"billed"`                // Cost scaled by per-agent cost multipliers
	CostPartial bool    `json:"costPartial,omitempty"` // some tokens had no known price
	Models      []*Row  `json:"models,omitempty"`

	// Agent, Provider and Model identify the rows of AgentModels.
	Agent    string `json:"-"`
	Provider string `json:"-"`
	Model    string `json:"-"`

	sessions    map[string]bool
	models      map[string]*Row
	agentModels map[string]*Row
}

// AgentModels returns a period's usage per agent and provider/model, sorted
// by agent, then by cost.
func (r *Row) AgentModels() []*Row {
	return sorted(r.agentModels, func(a, b *Row) bool {
		if a.Agent != b.Agent {
			return a.Agent < b.Agent
		}
		return byCost(a, b)
	})
}

type Report struct {
	Group           string             `json:"group"`
	CostMultipliers map[string]float64 `json:"costMultipliers,omitempty"`
	From            string             `json:"from,omitempty"`
	To              string             `json:"to,omitempty"`
	Total           *Row               `json:"total"`
	Periods         []*Row             `json:"periods,omitempty"`
	Agents          []*Row             `json:"agents,omitempty"`
	Providers       []*Row             `json:"providers,omitempty"`
	Models          []*Row             `json:"models,omitempty"`
}

func (r *Row) addSession(key string) {
	if r.sessions == nil {
		r.sessions = map[string]bool{}
	}
	if !r.sessions[key] {
		r.sessions[key] = true
		r.Sessions++
	}
}

func (r *Row) addUsage(u store.ModelUsage) {
	r.Input += u.InputTokens
	r.Output += u.OutputTokens
	r.CacheRead += u.CacheReadTokens
	r.CacheWrite += u.CacheCreationTokens
	r.Tokens = r.Input + r.Output + r.CacheRead + r.CacheWrite
	r.Cost += u.Cost
	r.Billed += u.Billed
	if u.Tokens && !u.HasCost && tokens(u) > 0 {
		r.CostPartial = true
	}
}

func tokens(u store.ModelUsage) int {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

func modelLabel(u store.ModelUsage) string {
	model := u.Model
	if model == "" {
		model = "(unknown)"
	}
	if u.Provider == "" {
		return model
	}
	return u.Provider + "/" + model
}

func providerLabel(u store.ModelUsage) string {
	if u.Provider == "" {
		return "(unknown)"
	}
	return u.Provider
}

func row(rows map[string]*Row, label string) *Row {
	if rows[label] == nil {
		rows[label] = &Row{Label: label}
	}
	return rows[label]
}

// Build aggregates sessions into a report. Sessions count towards the local
// (loc) day they started on.
func Build(sessions []store.Session, opts Options, loc *time.Location) *Report {
	layout := map[string]string{"year": "2006", "month": "2006-01", "day": "2006-01-02"}[opts.Group]
	rep := &Report{Group: opts.Group, CostMultipliers: opts.CostMultipliers, Total: &Row{Label: "Total"}}
	if !opts.From.IsZero() {
		rep.From = opts.From.Format("2006-01-02")
	}
	if !opts.To.IsZero() {
		rep.To = opts.To.Format("2006-01-02")
	}
	var end time.Time
	if !opts.To.IsZero() {
		end = opts.To.AddDate(0, 0, 1)
	}
	periods, agents, providers, models := map[string]*Row{}, map[string]*Row{}, map[string]*Row{}, map[string]*Row{}
	for _, s := range sessions {
		if opts.Agent != "" && s.Agent != opts.Agent || opts.Project != "" && s.Project != opts.Project {
			continue
		}
		date := s.Started
		if date.IsZero() {
			date = s.Modified
		}
		if date.IsZero() || !opts.From.IsZero() && date.Before(opts.From) || !end.IsZero() && !date.Before(end) {
			continue
		}
		key := s.Agent + "/" + s.ID
		period := row(periods, date.In(loc).Format(layout))
		agent := row(agents, s.Agent)
		for _, r := range []*Row{rep.Total, period, agent} {
			r.addSession(key)
		}
		for _, u := range s.Usage {
			if tokens(u) == 0 && u.Cost == 0 && u.Billed == 0 {
				continue
			}
			provider, model := row(providers, providerLabel(u)), row(models, modelLabel(u))
			if period.models == nil {
				period.models = map[string]*Row{}
			}
			periodModel := row(period.models, modelLabel(u))
			if period.agentModels == nil {
				period.agentModels = map[string]*Row{}
			}
			agentModel := row(period.agentModels, s.Agent+"\x00"+modelLabel(u))
			agentModel.Label, agentModel.Agent, agentModel.Provider, agentModel.Model = modelLabel(u), s.Agent, u.Provider, u.Model
			for _, r := range []*Row{provider, model, periodModel, agentModel} {
				r.addSession(key)
			}
			for _, r := range []*Row{rep.Total, period, agent, provider, model, periodModel, agentModel} {
				r.addUsage(u)
			}
		}
	}
	rep.Periods = sorted(periods, func(a, b *Row) bool { return a.Label < b.Label })
	for _, p := range rep.Periods {
		if opts.Breakdown {
			p.Models = sorted(p.models, byCost)
		}
	}
	rep.Agents = sorted(agents, byCost)
	rep.Providers = sorted(providers, byCost)
	rep.Models = sorted(models, byCost)
	return rep
}

func byCost(a, b *Row) bool {
	if a.Cost != b.Cost {
		return a.Cost > b.Cost
	}
	if a.Tokens != b.Tokens {
		return a.Tokens > b.Tokens
	}
	return a.Label < b.Label
}

func sorted(rows map[string]*Row, less func(a, b *Row) bool) []*Row {
	out := make([]*Row, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// WriteJSON writes the requested sections of the report as JSON.
func WriteJSON(w io.Writer, rep *Report, sections []string) error {
	out := *rep
	if !has(sections, SectionPeriod) {
		out.Periods = nil
	}
	if !has(sections, SectionAgent) {
		out.Agents = nil
	}
	if !has(sections, SectionProvider) {
		out.Providers = nil
	}
	if !has(sections, SectionModel) {
		out.Models = nil
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// WriteText writes the requested sections of the report as aligned tables.
func WriteText(w io.Writer, rep *Report, sections []string) error {
	var b strings.Builder
	span := "all time"
	switch {
	case rep.From != "" && rep.To != "":
		span = rep.From + " to " + rep.To
	case rep.From != "":
		span = "since " + rep.From
	case rep.To != "":
		span = "until " + rep.To
	}
	fmt.Fprintf(&b, "Agent usage, %s (sessions count towards the local day they started)\n", span)
	tables := []struct {
		section, title, column string
		rows                   []*Row
	}{
		{SectionPeriod, "By " + rep.Group, strings.ToUpper(rep.Group[:1]) + rep.Group[1:], rep.Periods},
		{SectionAgent, "By agent", "Agent", rep.Agents},
		{SectionProvider, "By provider", "Provider", rep.Providers},
		{SectionModel, "By model", "Model", rep.Models},
	}
	partial := rep.Total.CostPartial
	for _, t := range tables {
		if !has(sections, t.section) {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n", t.title)
		cells := [][]string{{t.column, "Sessions", "Input", "Output", "Cache read", "Cache write", "Total", "Cost", "Billed"}}
		for _, r := range t.rows {
			cells = append(cells, cellsFor(r.Label, r))
			for i, m := range r.Models {
				branch := "├ "
				if i == len(r.Models)-1 {
					branch = "└ "
				}
				cells = append(cells, cellsFor("  "+branch+m.Label, m))
			}
		}
		cells = append(cells, nil, cellsFor("Total", rep.Total))
		writeTable(&b, cells)
	}
	fmt.Fprintf(&b, "\nCost is at standard API prices; Billed applies cost multipliers (%s).\n", store.FormatCostMultipliers(rep.CostMultipliers))
	if partial {
		b.WriteString("* includes tokens from models without a known price\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func cellsFor(label string, r *Row) []string {
	cost, billed := fmtCost(r.Cost), fmtCost(r.Billed)
	if r.CostPartial {
		cost += "*"
		billed += "*"
	}
	return []string{label, fmt.Sprint(r.Sessions), fmtTokens(r.Input), fmtTokens(r.Output), fmtTokens(r.CacheRead), fmtTokens(r.CacheWrite), fmtTokens(r.Tokens), cost, billed}
}

// writeTable left-aligns the first column and right-aligns the rest. A nil
// row is drawn as a separator line.
func writeTable(b *strings.Builder, cells [][]string) {
	widths := make([]int, len(cells[0]))
	for _, row := range cells {
		for i, c := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	total := 2 * (len(widths) - 1)
	for _, w := range widths {
		total += w
	}
	for n, row := range cells {
		if row == nil {
			b.WriteString(strings.Repeat("─", total) + "\n")
			continue
		}
		for i, c := range row {
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
			if i == 0 {
				b.WriteString(c + pad)
			} else {
				b.WriteString("  " + pad + c)
			}
		}
		b.WriteString("\n")
		if n == 0 {
			b.WriteString(strings.Repeat("─", total) + "\n")
		}
	}
}

func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func fmtCost(cost float64) string {
	if cost > 0 && cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
