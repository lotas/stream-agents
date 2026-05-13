package server

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"stream-agents/internal/render"
	"stream-agents/internal/store"
)

var homeDir, _ = os.UserHomeDir()

func shortPath(path string) string {
	if homeDir != "" && strings.HasPrefix(path, homeDir+"/") {
		return "~/" + path[len(homeDir)+1:]
	}
	return path
}

var funcMap = template.FuncMap{
	"shortPath":   shortPath,
	"fmtTokens":  fmtTokens,
	"fmtDuration": func(d time.Duration) string { return formatDuration(d) },
}

var (
	listTmpl    = template.Must(template.New("").Funcs(funcMap).ParseFS(tmplFS, "templates/layout.html", "templates/list.html"))
	sessionTmpl = template.Must(template.New("").Funcs(funcMap).ParseFS(tmplFS, "templates/layout.html", "templates/session.html"))
)

type handlers struct {
	idx *store.Index
}

type listData struct {
	PageTitle     string
	Sessions      []store.Session
	Projects      []string
	AgentFilter   string
	ProjectFilter string
	DateFilter    string
}

// URL builds a "/" URL preserving the current filters, with optional overrides
// passed as alternating key/value strings ("agent","claude","date","yesterday"...).
// Empty values remove the param. "today" is the default for date and is omitted
// to keep bare URLs clean.
func (d listData) URL(kvs ...string) string {
	params := map[string]string{
		"agent":   d.AgentFilter,
		"project": d.ProjectFilter,
		"date":    d.DateFilter,
	}
	for i := 0; i+1 < len(kvs); i += 2 {
		params[kvs[i]] = kvs[i+1]
	}
	v := url.Values{}
	if params["agent"] != "" {
		v.Set("agent", params["agent"])
	}
	if params["project"] != "" {
		v.Set("project", params["project"])
	}
	if params["date"] != "" && params["date"] != "today" {
		v.Set("date", params["date"])
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

func filterByDate(sessions []store.Session, dateFilter string) []store.Session {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var from, to time.Time
	switch dateFilter {
	case "today":
		from = today
	case "yesterday":
		from = today.AddDate(0, 0, -1)
		to = today
	case "week":
		from = today.AddDate(0, 0, -6)
	default: // "all" or unrecognised
		return sessions
	}
	out := sessions[:0]
	for _, s := range sessions {
		if !from.IsZero() && s.Modified.Before(from) {
			continue
		}
		if !to.IsZero() && !s.Modified.Before(to) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (h *handlers) handleList(w http.ResponseWriter, r *http.Request) {
	// The "/" pattern is a catch-all; only serve the list at the root.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	agentFilter := r.URL.Query().Get("agent")
	projectFilter := r.URL.Query().Get("project")
	dateFilter := r.URL.Query().Get("date")
	if dateFilter == "" {
		dateFilter = "today"
	}

	sessions, err := h.idx.ListAll(r.Context(), agentFilter, projectFilter)
	if err != nil {
		http.Error(w, "failed to list sessions: "+err.Error(), 500)
		return
	}
	sessions = filterByDate(sessions, dateFilter)

	data := listData{
		PageTitle:     "Sessions — stream-agents",
		Sessions:      sessions,
		Projects:      h.idx.Projects(),
		AgentFilter:   agentFilter,
		ProjectFilter: projectFilter,
		DateFilter:    dateFilter,
	}
	if err := listTmpl.ExecuteTemplate(w, "list.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// toolPair is a fused tool_call + tool_result pair.
type toolPair struct {
	Name         string
	Index        int    // 1-based per-name occurrence
	InputJSON    string // truncated input from Meta["input"]
	InputPreview string // 1-line summary for the chip header
	Output    template.HTML // tool_result content
	OutputPre bool          // true → wrap in <pre>, false → rendered markdown div
	IsError   bool
	Pending   bool   // no matching result seen yet
	AnchorID  string
	TimeLabel string // elapsed from session start, e.g. "+1m 23s"
	ExecLabel string // tool execution duration, e.g. "0.3s"
	callTime  time.Time
}

// viewItem is one renderable unit in the session transcript.
type viewItem struct {
	Kind            string        // "text" | "tool"
	Role            string        // "user" | "assistant" | "system" (when Kind=="text")
	Body            template.HTML // rendered body (markdown HTML or escaped text)
	TurnID          string        // anchor id set on user-turn messages for the TOC
	Collapsible     bool          // skill payload user messages
	CollapseSummary string
	TimeLabel       string    // elapsed from session start, e.g. "+1m 23s"
	Tool            *toolPair // non-nil when Kind=="tool"
}

// turn is one entry in the side TOC.
type turn struct {
	Index    int
	Title    string
	AnchorID string
}

// toolNameCount is one entry in the toolbar tool-filter dropdown.
type toolNameCount struct {
	Name  string
	Count int
}

// skillSummary returns a non-empty label if text looks like a loaded skill payload.
// Claude injects skill content as user messages starting with "Base directory for this skill:".
func skillSummary(text string) string {
	const prefix = "Base directory for this skill:"
	if !strings.HasPrefix(text, prefix) {
		return ""
	}
	line := strings.SplitN(text, "\n", 2)[0]
	path := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	name := filepath.Base(path)
	return "📦 skill: " + name
}

// toolInputPreview returns a short one-line description of tool arguments.
func toolInputPreview(name, inputJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return truncate(strings.ReplaceAll(inputJSON, "\n", " "), 80)
	}
	switch name {
	case "Bash", "exec_command":
		switch v := m["command"].(type) {
		case string:
			return truncate(v, 80)
		case []any:
			parts := make([]string, len(v))
			for i, p := range v {
				parts[i] = fmt.Sprint(p)
			}
			return truncate(strings.Join(parts, " "), 80)
		}
	case "Read", "Edit", "Write", "MultiEdit":
		if fp, ok := m["file_path"].(string); ok {
			runes := []rune(fp)
			if len(runes) > 80 {
				return "…" + string(runes[len(runes)-79:])
			}
			return fp
		}
	}
	return truncate(strings.ReplaceAll(inputJSON, "\n", " "), 80)
}

// looksLikeMarkdown returns true if text contains common markdown constructs.
// Used to decide whether to render tool output as markdown or raw <pre>.
func looksLikeMarkdown(s string) bool {
	for _, line := range strings.SplitN(s, "\n", 20) {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") ||
			strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") ||
			strings.HasPrefix(t, "```") ||
			(len(t) > 2 && t[0] >= '1' && t[0] <= '9' && t[1] == '.') {
			return true
		}
	}
	return strings.Contains(s, "**") || strings.Contains(s, "__")
}

func renderToolOutput(text string) (template.HTML, bool) {
	if looksLikeMarkdown(text) {
		return render.RenderMarkdown(text), false
	}
	return template.HTML(html.EscapeString(text)), true
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}

type sessionStats struct {
	Duration        string // e.g. "23m 45s"
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	HasTokens       bool
}

type sessionData struct {
	PageTitle string
	Session   store.Session
	Items     []viewItem
	Turns     []turn
	ToolNames []toolNameCount
	Stats     sessionStats
}

func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("+%ds", s)
	}
	m := s / 60
	s = s % 60
	if m < 60 {
		return fmt.Sprintf("+%dm %ds", m, s)
	}
	return fmt.Sprintf("+%dh %dm", m/60, m%60)
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

func formatExec(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func fmtTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// buildViewItems converts a flat message list into renderable view items,
// fusing each tool_call with its matching tool_result into a single toolPair.
func buildViewItems(msgs []store.Message) (items []viewItem, turns []turn, toolNames []toolNameCount, stats sessionStats) {
	nameCounts := map[string]int{}
	pendingByID := map[string]int{} // callID -> index in items
	toolCountMap := map[string]int{}
	turnIndex := 0

	// Find session start time (first non-zero timestamp).
	var startTime, lastTime time.Time
	for _, m := range msgs {
		if !m.Time.IsZero() {
			if startTime.IsZero() {
				startTime = m.Time
			}
			lastTime = m.Time
		}
	}
	if !startTime.IsZero() && lastTime.After(startTime) {
		stats.Duration = formatDuration(lastTime.Sub(startTime))
	}

	elapsed := func(t time.Time) string {
		if t.IsZero() || startTime.IsZero() {
			return ""
		}
		return formatElapsed(t.Sub(startTime))
	}

	for _, m := range msgs {
		switch m.Role {
		case "tool_call":
			name, _ := m.Meta["name"].(string)
			nameCounts[name]++
			toolCountMap[name]++
			inputJSON, _ := m.Meta["input"].(string)

			callID, _ := m.Meta["id"].(string)
			if callID == "" {
				callID, _ = m.Meta["call_id"].(string)
			}
			anchorID := "tool-" + callID
			if callID == "" {
				anchorID = fmt.Sprintf("tool-%s-%d", name, nameCounts[name])
			}

			pair := &toolPair{
				Name:         name,
				Index:        nameCounts[name],
				InputJSON:    inputJSON,
				InputPreview: toolInputPreview(name, inputJSON),
				Pending:      true,
				AnchorID:     anchorID,
				TimeLabel:    elapsed(m.Time),
				callTime:     m.Time,
			}
			if callID != "" {
				pendingByID[callID] = len(items)
			}
			items = append(items, viewItem{Kind: "tool", Tool: pair})

		case "tool_result":
			resultID, _ := m.Meta["tool_use_id"].(string)
			if resultID == "" {
				resultID, _ = m.Meta["call_id"].(string)
			}
			isError, _ := m.Meta["is_error"].(bool)

			if resultID != "" {
				if idx, found := pendingByID[resultID]; found {
					out, pre := renderToolOutput(m.Text)
					items[idx].Tool.Output = out
					items[idx].Tool.OutputPre = pre
					items[idx].Tool.IsError = isError
					items[idx].Tool.Pending = false
					if !items[idx].Tool.callTime.IsZero() && !m.Time.IsZero() {
						items[idx].Tool.ExecLabel = formatExec(m.Time.Sub(items[idx].Tool.callTime))
					}
					delete(pendingByID, resultID)
					continue
				}
			}
			// Orphan result: no matching call found.
			out, pre := renderToolOutput(m.Text)
			items = append(items, viewItem{
				Kind: "tool",
				Tool: &toolPair{Output: out, OutputPre: pre, IsError: isError, TimeLabel: elapsed(m.Time)},
			})

		case "user":
			vi := viewItem{Kind: "text", Role: "user", Body: render.RenderMarkdown(m.Text), TimeLabel: elapsed(m.Time)}
			if summary := skillSummary(m.Text); summary != "" {
				vi.Collapsible = true
				vi.CollapseSummary = summary
			} else {
				turnIndex++
				vi.TurnID = fmt.Sprintf("turn-%d", turnIndex)
				title := strings.SplitN(m.Text, "\n", 2)[0]
				runes := []rune(title)
				if len(runes) > 60 {
					title = string(runes[:60]) + "…"
				}
				turns = append(turns, turn{Index: turnIndex, Title: title, AnchorID: vi.TurnID})
			}
			items = append(items, vi)

		case "assistant":
			vi := viewItem{
				Kind:      "text",
				Role:      "assistant",
				Body:      render.RenderMarkdown(m.Text),
				TimeLabel: elapsed(m.Time),
			}
			if m.Usage != nil {
				stats.InputTokens += m.Usage.InputTokens
				stats.OutputTokens += m.Usage.OutputTokens
				stats.CacheReadTokens += m.Usage.CacheReadTokens
				stats.HasTokens = true
			}
			items = append(items, vi)

		case "system":
			items = append(items, viewItem{
				Kind: "text",
				Role: "system",
				Body: template.HTML(html.EscapeString(m.Text)),
			})
		}
	}

	// Build sorted tool name counts for the filter dropdown.
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	for k, v := range toolCountMap {
		kvs = append(kvs, kv{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].k < kvs[j].k })
	toolNames = make([]toolNameCount, len(kvs))
	for i, e := range kvs {
		toolNames[i] = toolNameCount{Name: e.k, Count: e.v}
	}
	return
}

func (h *handlers) handleSession(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	id := r.PathValue("id")

	// Reject IDs that look like path traversal.
	if strings.Contains(id, "/") || strings.Contains(id, "..") || strings.Contains(id, "\x00") {
		http.NotFound(w, r)
		return
	}
	// Validate agent is a known value.
	if agent != "claude" && agent != "codex" {
		http.NotFound(w, r)
		return
	}

	// Ensure the store index is populated before looking up the file path.
	allSessions, err := h.idx.ListAll(r.Context(), agent, "")
	if err != nil {
		http.Error(w, "failed to list sessions: "+err.Error(), 500)
		return
	}

	fpath := h.idx.FilePath(agent, id)
	if fpath == "" {
		http.NotFound(w, r)
		return
	}
	resolved, err := filepath.EvalSymlinks(fpath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = resolved

	msgs, err := h.idx.LoadSession(r.Context(), agent, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var sess store.Session
	for _, s := range allSessions {
		if s.ID == id {
			sess = s
			break
		}
	}

	items, turns, toolNames, stats := buildViewItems(msgs)

	title := sess.Title
	if title == "" {
		title = sess.ID
	}
	data := sessionData{
		PageTitle: title + " — stream-agents",
		Session:   sess,
		Items:     items,
		Turns:     turns,
		ToolNames: toolNames,
		Stats:     stats,
	}
	if err := sessionTmpl.ExecuteTemplate(w, "session.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
