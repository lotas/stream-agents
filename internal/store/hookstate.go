package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Claude Code hooks report what transcripts can't show: a session blocked on
// a permission prompt. `stream-agents hook` turns each hook event into a
// small file under <dir>/claude/, one per session and agent (the main
// conversation or a subagent), present while a prompt is pending and removed
// when that agent moves on. The server only reads these files.

// HookState is one pending prompt as recorded by the hook.
type HookState struct {
	SessionID string    `json:"session_id"`
	AgentID   string    `json:"agent_id,omitempty"` // "" for the main conversation
	Reason    string    `json:"reason"`             // ReasonPermission
	Event     string    `json:"event"`              // hook event that set it
	Kind      string    `json:"kind,omitempty"`     // notification_type, when the event had one
	Message   string    `json:"message,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	At        time.Time `json:"at"`
}

// HookEvent is the part of a Claude Code hook payload the hook needs.
type HookEvent struct {
	SessionID        string `json:"session_id"`
	AgentID          string `json:"agent_id"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
	Message          string `json:"message"`
	ToolName         string `json:"tool_name"`
}

// hookStateMaxAge is how long a state file of a session that never sent a
// clearing event (it crashed) is kept before the hook prunes it.
const hookStateMaxAge = 24 * time.Hour

// ApplyHookEvent records ev under dir: a permission or input prompt sets the
// agent's pending state, events that mean the agent moved on clear it, and
// anything else leaves the state alone.
func ApplyHookEvent(dir string, ev HookEvent, now time.Time) error {
	if !validHookID(ev.SessionID) || (ev.AgentID != "" && !validHookID(ev.AgentID)) || dir == "" {
		return errors.New("hook payload has no usable session_id")
	}
	sdir := filepath.Join(dir, "claude")
	switch hookEffect(ev) {
	case hookSet:
		st := HookState{SessionID: ev.SessionID, AgentID: ev.AgentID, Reason: ReasonPermission, Event: ev.HookEventName,
			Kind: ev.NotificationType, Message: ev.Message, ToolName: ev.ToolName, At: now.UTC()}
		data, err := json.Marshal(st)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(sdir, hookStateName(ev.SessionID, ev.AgentID)), data); err != nil {
			return err
		}
		pruneHookStates(sdir, now)
	case hookClearAgent:
		return removeIfExists(filepath.Join(sdir, hookStateName(ev.SessionID, ev.AgentID)))
	case hookClearSession:
		entries, err := os.ReadDir(sdir)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if hookStateSession(e.Name()) == ev.SessionID {
				if err := removeIfExists(filepath.Join(sdir, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type hookAction int

const (
	hookIgnore hookAction = iota
	hookSet
	hookClearAgent   // this agent moved on
	hookClearSession // the whole session moved on
)

func hookEffect(ev HookEvent) hookAction {
	switch ev.HookEventName {
	case "PermissionRequest":
		return hookSet
	case "Notification":
		switch ev.NotificationType {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog":
			return hookSet
		case "elicitation_complete", "elicitation_response":
			return hookClearAgent
		case "":
			// Versions without notification_type: the idle reminder ("Claude
			// is waiting for your input") is a finished turn, which the
			// transcript already shows.
			if strings.Contains(strings.ToLower(ev.Message), "permission") {
				return hookSet
			}
		}
		return hookIgnore
	case "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionDenied":
		return hookClearAgent
	case "SubagentStop":
		return hookClearAgent
	case "UserPromptSubmit", "Stop", "SessionStart", "SessionEnd":
		if ev.AgentID != "" {
			return hookClearAgent
		}
		return hookClearSession
	}
	return hookIgnore
}

// State file names: <session>.json for the main conversation and
// <session>.agent-<agent>.json for a subagent.
func hookStateName(session, agent string) string {
	if agent == "" {
		return session + ".json"
	}
	return session + ".agent-" + agent + ".json"
}

func hookStateSession(name string) string {
	if !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
		return ""
	}
	name = strings.TrimSuffix(name, ".json")
	if i := strings.Index(name, ".agent-"); i >= 0 {
		name = name[:i]
	}
	return name
}

// validHookID rejects IDs that could escape the state dir or confuse the
// file naming.
func validHookID(id string) bool {
	return id != "" && len(id) <= 200 && !strings.HasPrefix(id, ".") && !strings.ContainsAny(id, `/\`+"\x00") && !strings.Contains(id, ".agent-")
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func pruneHookStates(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > hookStateMaxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// hookStates reads the hook state dir. Every write and removal is a create,
// rename or unlink in the directory, so its mtime tells when to re-read; a
// re-read at least every hookRereadAfter covers coarse mtimes.
type hookStates struct {
	dir string // <state dir>/claude

	mu      sync.Mutex
	mtime   time.Time
	read    bool
	readAt  time.Time
	pending map[string]HookState // newest pending prompt per session
}

const hookRereadAfter = 2 * time.Second

func newHookStates(stateDir string) *hookStates {
	if stateDir == "" {
		return nil
	}
	return &hookStates{dir: filepath.Join(stateDir, "claude")}
}

// newest returns the session's most recent pending prompt, if any.
func (h *hookStates) newest(sessionID string) (HookState, bool) {
	if h == nil {
		return HookState{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	fi, err := os.Stat(h.dir)
	if err != nil {
		h.pending, h.read = nil, false
		return HookState{}, false
	}
	if !h.read || !fi.ModTime().Equal(h.mtime) || time.Since(h.readAt) >= hookRereadAfter {
		h.reload(fi.ModTime())
	}
	st, ok := h.pending[sessionID]
	return st, ok
}

func (h *hookStates) reload(mtime time.Time) {
	h.mtime, h.read, h.readAt = mtime, true, time.Now()
	h.pending = map[string]HookState{}
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		session := hookStateSession(e.Name())
		if session == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.dir, e.Name()))
		if err != nil {
			continue
		}
		var st HookState
		if json.Unmarshal(data, &st) != nil || st.At.IsZero() || st.SessionID != session {
			continue
		}
		if cur, ok := h.pending[session]; !ok || st.At.After(cur.At) {
			h.pending[session] = st
		}
	}
}
