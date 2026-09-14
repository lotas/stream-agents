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
	ActiveDuration                                                                                time.Duration
	activity                                                                                      []store.Interval
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
	Heatmaps                                               []activityHeatmap
	PageTitle, Group, AgentFilter, ProjectFilter, From, To string
	Projects                                               []string
	IdleCutoff                                             string
	Total                                                  statsRow
	Periods, ByAgent                                       []*statsRow
}

func aggregateStats(sessions []store.Session, group string, bounds ...time.Time) (statsRow, []*statsRow, []*statsRow) {
	var from, to time.Time
	if len(bounds) == 2 {
		from, to = bounds[0], bounds[1]
	}
	var total statsRow
	periods, agents := map[string]*statsRow{}, map[string]*statsRow{}
	layout := map[string]string{"year": "2006", "month": "2006-01", "day": "2006-01-02"}[group]
	for _, s := range sessions {
		date := s.Started
		if date.IsZero() {
			date = s.Modified
		}
		if agents[s.Agent] == nil {
			agents[s.Agent] = &statsRow{Label: s.Agent}
		}
		if (from.IsZero() || !date.Before(from)) && (to.IsZero() || date.Before(to)) {
			label := date.UTC().Format(layout)
			if periods[label] == nil {
				periods[label] = &statsRow{Label: label}
			}
			total.add(s)
			periods[label].add(s)
			agents[s.Agent].add(s)
		}
		for _, span := range s.Activity {
			if !from.IsZero() && span.Start.Before(from) {
				span.Start = from
			}
			if !to.IsZero() && span.End.After(to) {
				span.End = to
			}
			if !span.End.After(span.Start) {
				continue
			}
			total.activity = append(total.activity, span)
			agents[s.Agent].activity = append(agents[s.Agent].activity, span)
			for start := span.Start.UTC(); start.Before(span.End); {
				end := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, time.UTC)
				if end.After(span.End) {
					end = span.End
				}
				label := start.Format(layout)
				if periods[label] == nil {
					periods[label] = &statsRow{Label: label}
				}
				periods[label].activity = append(periods[label].activity, store.Interval{Start: start, End: end})
				start = end
			}
		}
	}
	var rows, byAgent []*statsRow
	for _, row := range periods {
		row.ActiveDuration = store.IntervalDuration(row.activity)
		rows = append(rows, row)
	}
	for _, row := range agents {
		row.ActiveDuration = store.IntervalDuration(row.activity)
		if row.Sessions > 0 || row.ActiveDuration > 0 {
			byAgent = append(byAgent, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Label > rows[j].Label })
	sort.Slice(byAgent, func(i, j int) bool { return byAgent[i].Label < byAgent[j].Label })
	total.ActiveDuration = store.IntervalDuration(total.activity)
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
	d.IdleCutoff = h.idx.IdleCutoff.String()
	projects := map[string]bool{}
	var filtered, matching []store.Session
	for _, s := range sessions {
		if s.Project != "" {
			projects[s.Project] = true
		}
		if d.AgentFilter != "" && s.Agent != d.AgentFilter || d.ProjectFilter != "" && s.Project != d.ProjectFilter {
			continue
		}
		matching = append(matching, s)
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
	var end time.Time
	if !to.IsZero() {
		end = to.AddDate(0, 0, 1)
	}
	d.Total, d.Periods, d.ByAgent = aggregateStats(matching, d.Group, from, end)
	byYear := map[int][]store.Session{}
	for _, session := range filtered {
		date := session.Started
		if date.IsZero() {
			date = session.Modified
		}
		if !date.IsZero() {
			year := date.UTC().Year()
			byYear[year] = append(byYear[year], session)
		}
	}
	var years []int
	for year := range byYear {
		years = append(years, year)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years)))
	for _, year := range years {
		d.Heatmaps = append(d.Heatmaps, buildHeatmap(byYear[year], year))
	}
	if err := statsTmpl.ExecuteTemplate(w, "stats.html", d); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
