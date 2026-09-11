package server

import (
	"html/template"
	"net/http"
	"sort"
	"time"

	"stream-agents/internal/store"
)

var statsTmpl = template.Must(template.New("").Funcs(funcMap).ParseFS(tmplFS, "templates/layout.html", "templates/stats.html"))

type statsRow struct {
	Label                                                                                         string
	Sessions, Agents, Projects, Records, Input, Output, CacheRead, CacheWrite, Tokens, WithTokens int
	Duration                                                                                      time.Duration
	agents, projects                                                                              map[string]bool
}

func (row *statsRow) add(s store.Session) {
	if row.agents == nil {
		row.agents = map[string]bool{}
		row.projects = map[string]bool{}
	}
	row.Sessions++
	row.agents[s.Agent] = true
	if s.Project != "" {
		row.projects[s.Project] = true
	}
	row.Agents, row.Projects = len(row.agents), len(row.projects)
	row.Records += s.MessageCount
	row.Input += s.InputTokens
	row.Output += s.OutputTokens
	row.CacheRead += s.CacheReadTokens
	row.CacheWrite += s.CacheCreationTokens
	row.Tokens = row.Input + row.Output + row.CacheRead + row.CacheWrite
	row.Duration += s.Duration
	if s.HasTokens {
		row.WithTokens++
	}
}

type statsData struct {
	PageTitle, Group, AgentFilter, ProjectFilter, From, To string
	Projects                                               []string
	Total                                                  statsRow
	Periods, ByAgent                                       []*statsRow
}

func aggregateStats(sessions []store.Session, group string) (statsRow, []*statsRow, []*statsRow) {
	var total statsRow
	periods, agents := map[string]*statsRow{}, map[string]*statsRow{}
	layout := map[string]string{"year": "2006", "month": "2006-01", "day": "2006-01-02"}[group]
	for _, s := range sessions {
		date := s.Started
		if date.IsZero() {
			date = s.Modified
		}
		label := date.UTC().Format(layout)
		if periods[label] == nil {
			periods[label] = &statsRow{Label: label}
		}
		if agents[s.Agent] == nil {
			agents[s.Agent] = &statsRow{Label: s.Agent}
		}
		total.add(s)
		periods[label].add(s)
		agents[s.Agent].add(s)
	}
	var rows, byAgent []*statsRow
	for _, row := range periods {
		rows = append(rows, row)
	}
	for _, row := range agents {
		byAgent = append(byAgent, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Label > rows[j].Label })
	sort.Slice(byAgent, func(i, j int) bool { return byAgent[i].Label < byAgent[j].Label })
	return total, rows, byAgent
}

func (h *handlers) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := statsData{PageTitle: "Stats — stream-agents", Group: q.Get("group"), AgentFilter: q.Get("agent"), ProjectFilter: q.Get("project"), From: q.Get("from"), To: q.Get("to")}
	if d.Group == "" {
		d.Group = "month"
	}
	if d.Group != "year" && d.Group != "month" && d.Group != "day" {
		http.Error(w, "invalid grouping", 400)
		return
	}
	var from, to time.Time
	for _, field := range []struct {
		value string
		date  *time.Time
	}{{d.From, &from}, {d.To, &to}} {
		if field.value == "" {
			continue
		}
		parsed, err := time.Parse("2006-01-02", field.value)
		if err != nil {
			http.Error(w, "invalid date: use YYYY-MM-DD", 400)
			return
		}
		*field.date = parsed
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		http.Error(w, "start date must precede end date", 400)
		return
	}
	sessions, err := h.idx.ListAll(r.Context(), "", "")
	if err != nil {
		http.Error(w, "failed to list sessions: "+err.Error(), 500)
		return
	}
	projects := map[string]bool{}
	var filtered []store.Session
	for _, s := range sessions {
		if s.Project != "" {
			projects[s.Project] = true
		}
		if d.AgentFilter != "" && s.Agent != d.AgentFilter || d.ProjectFilter != "" && s.Project != d.ProjectFilter {
			continue
		}
		date := s.Started
		if date.IsZero() {
			date = s.Modified
		}
		if !from.IsZero() && date.Before(from) || !to.IsZero() && !date.Before(to.AddDate(0, 0, 1)) {
			continue
		}
		filtered = append(filtered, s)
	}
	for project := range projects {
		d.Projects = append(d.Projects, project)
	}
	sort.Strings(d.Projects)
	d.Total, d.Periods, d.ByAgent = aggregateStats(filtered, d.Group)
	if err := statsTmpl.ExecuteTemplate(w, "stats.html", d); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
