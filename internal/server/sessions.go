package server

import (
	"net/http"
	"path/filepath"
	"time"

	"stream-agents/internal/store"
)

// SessionsResponse is the /api/v1/sessions body. Other programs parse it:
// add fields, never rename or remove them.
type SessionsResponse struct {
	GeneratedAt string          `json:"generated_at"`
	Host        string          `json:"host"`
	Sessions    []ActiveSession `json:"sessions"` // waiting first (longest waiting first), then working
}

type ActiveSession struct {
	Agent         string `json:"agent"`
	ID            string `json:"id"`
	Project       string `json:"project"`        // last element of the working directory
	Title         string `json:"title"`          // "" if none
	Model         string `json:"model"`          // model of the latest reply; "" if unknown
	State         string `json:"state"`          // working | waiting
	WaitingReason string `json:"waiting_reason"` // turn_done | permission | "" while working
	Since         string `json:"since"`          // RFC 3339 UTC, when the session entered State
	LastActivity  string `json:"last_activity"`  // RFC 3339 UTC
}

func (a *apiHandlers) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	now := a.opts.Now()
	active, err := a.idx.ActiveSessions(r.Context(), now)
	if err != nil {
		http.Error(w, "failed to list sessions", http.StatusInternalServerError)
		return
	}
	writeJSON(w, BuildSessions(active, now, a.opts.Host))
}

// BuildSessions renders active sessions as the /api/v1/sessions body.
func BuildSessions(active []store.ActiveSession, now time.Time, host string) SessionsResponse {
	out := SessionsResponse{GeneratedAt: utcStamp(now), Host: host, Sessions: make([]ActiveSession, 0, len(active))}
	for _, s := range active {
		project := ""
		if s.Project != "" {
			project = filepath.Base(s.Project)
		}
		out.Sessions = append(out.Sessions, ActiveSession{
			Agent: s.Agent, ID: s.ID, Project: project, Title: s.Title, Model: s.Turn.Model,
			State: s.State, WaitingReason: s.Reason,
			Since: utcStamp(s.Since), LastActivity: utcStamp(s.LastActivity),
		})
	}
	return out
}

func utcStamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
