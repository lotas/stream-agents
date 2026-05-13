package server

import (
	"html/template"
	"net/http"
	"path/filepath"
	"strings"

	"stream-agents/internal/render"
	"stream-agents/internal/store"
)

var (
	listTmpl    = template.Must(template.New("").ParseFS(tmplFS, "templates/layout.html", "templates/list.html"))
	sessionTmpl = template.Must(template.New("").ParseFS(tmplFS, "templates/layout.html", "templates/session.html"))
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

type renderedMessage struct {
	store.Message
	RenderedText template.HTML
}

type sessionData struct {
	PageTitle string
	Session   store.Session
	Messages  []renderedMessage
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

	rendered := make([]renderedMessage, len(msgs))
	for i, m := range msgs {
		rm := renderedMessage{Message: m}
		if m.Role == "user" || m.Role == "assistant" {
			rm.RenderedText = render.RenderMarkdown(m.Text)
		}
		rendered[i] = rm
	}

	title := sess.Title
	if title == "" {
		title = sess.ID
	}
	data := sessionData{PageTitle: title + " — stream-agents", Session: sess, Messages: rendered}
	if err := sessionTmpl.ExecuteTemplate(w, "session.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
