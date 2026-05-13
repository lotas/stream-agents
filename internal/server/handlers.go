package server

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

var funcMap = template.FuncMap{"shortPath": shortPath}

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
}

func (h *handlers) handleList(w http.ResponseWriter, r *http.Request) {
	// The "/" pattern is a catch-all; only serve the list at the root.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	agentFilter := r.URL.Query().Get("agent")
	projectFilter := r.URL.Query().Get("project")

	sessions, err := h.idx.ListAll(r.Context(), agentFilter, projectFilter)
	if err != nil {
		http.Error(w, "failed to list sessions: "+err.Error(), 500)
		return
	}

	data := listData{
		PageTitle:     "Sessions — stream-agents",
		Sessions:      sessions,
		Projects:      h.idx.Projects(),
		AgentFilter:   agentFilter,
		ProjectFilter: projectFilter,
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
}

// viewItem is one renderable unit in the session transcript.
type viewItem struct {
	Kind            string        // "text" | "tool"
	Role            string        // "user" | "assistant" | "system" (when Kind=="text")
	Body            template.HTML // rendered body (markdown HTML or escaped text)
	TurnID          string        // anchor id set on user-turn messages for the TOC
	Collapsible     bool          // skill payload user messages
	CollapseSummary string
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

type sessionData struct {
	PageTitle string
	Session   store.Session
	Items     []viewItem
	Turns     []turn
	ToolNames []toolNameCount
}

// buildViewItems converts a flat message list into renderable view items,
// fusing each tool_call with its matching tool_result into a single toolPair.
func buildViewItems(msgs []store.Message) (items []viewItem, turns []turn, toolNames []toolNameCount) {
	nameCounts := map[string]int{}
	pendingByID := map[string]int{} // callID -> index in items
	toolCountMap := map[string]int{}
	turnIndex := 0

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
					delete(pendingByID, resultID)
					continue
				}
			}
			// Orphan result: no matching call found.
			out, pre := renderToolOutput(m.Text)
			items = append(items, viewItem{
				Kind: "tool",
				Tool: &toolPair{Output: out, OutputPre: pre, IsError: isError},
			})

		case "user":
			vi := viewItem{Kind: "text", Role: "user", Body: render.RenderMarkdown(m.Text)}
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
			items = append(items, viewItem{
				Kind: "text",
				Role: "assistant",
				Body: render.RenderMarkdown(m.Text),
			})

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

	items, turns, toolNames := buildViewItems(msgs)

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
	}
	if err := sessionTmpl.ExecuteTemplate(w, "session.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
