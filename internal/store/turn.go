package store

import (
	"encoding/json"
	"strings"
	"time"
)

// Turn states read from transcripts.
const (
	StateWorking = "working"
	StateWaiting = "waiting"

	ReasonTurnDone   = "turn_done"
	ReasonPermission = "permission"
)

// TurnState is where a session's latest turn stands according to its
// transcript.
type TurnState struct {
	State        string    // StateWorking, StateWaiting, or "" before the first turn
	Reason       string    // ReasonTurnDone while waiting, otherwise ""
	Since        time.Time // when the session entered State
	LastActivity time.Time // newest meaningful record
	Model        string    // model of the newest assistant reply; "" if unknown
	// SubagentActivity is the newest activity of a subagent that has not
	// finished; zero when there is none. A session that is waiting but has
	// a subagent active after Since is still working.
	SubagentActivity time.Time
}

// set moves the turn to state at ts. Staying in the same state keeps Since.
func (t *TurnState) set(state, reason string, ts time.Time) {
	if ts.After(t.LastActivity) {
		t.LastActivity = ts
	}
	if t.State == state && t.Reason == reason {
		return
	}
	t.State, t.Reason, t.Since = state, reason, ts
}

func (t *TurnState) working(ts time.Time) { t.set(StateWorking, "", ts) }
func (t *TurnState) done(ts time.Time)    { t.set(StateWaiting, ReasonTurnDone, ts) }

// claudeTurn follows a Claude Code transcript record by record.
//
// A finished turn ends with an assistant record whose stop_reason is end_turn
// (or stop_sequence), followed by a system turn_duration record. An assistant
// record that stopped for tool_use, a tool result, or a new prompt means the
// turn is in progress. Bookkeeping records, compaction summaries, meta
// records and local slash commands (/model, /cost, ...) do not move the turn.
type claudeTurn struct {
	TurnState
	// beforeCommand is the state before an old-style <command-name> user
	// record, restored when the command turns out to be local (its output
	// follows as a <local-command-stdout> record).
	beforeCommand *TurnState
}

func (c *claudeTurn) observe(typ string, raw map[string]json.RawMessage, ts time.Time) {
	if ts.IsZero() || jsonBool(raw["isSidechain"]) {
		return
	}
	switch typ {
	case "assistant":
		var msg struct {
			Model      string `json:"model"`
			StopReason string `json:"stop_reason"`
		}
		_ = json.Unmarshal(raw["message"], &msg)
		if msg.Model != "" && msg.Model != "<synthetic>" {
			c.Model = msg.Model
		}
		c.beforeCommand = nil
		switch msg.StopReason {
		case "end_turn", "stop_sequence":
			c.done(ts)
		default: // tool_use, or still streaming
			c.working(ts)
		}
	case "system":
		var sub string
		_ = json.Unmarshal(raw["subtype"], &sub)
		switch sub {
		case "turn_duration":
			c.done(ts)
		case "api_error": // Claude Code retries
			c.working(ts)
		}
	case "user":
		if jsonBool(raw["isMeta"]) || jsonBool(raw["isCompactSummary"]) || jsonBool(raw["isVisibleInTranscriptOnly"]) {
			return
		}
		text, toolResult := claudeUserContent(raw)
		switch {
		case toolResult:
			c.working(ts)
		case strings.HasPrefix(text, "[Request interrupted"):
			c.beforeCommand = nil
			c.done(ts)
		case strings.Contains(text, "<local-command-stdout>") || strings.Contains(text, "<local-command-stderr>"):
			if c.beforeCommand != nil {
				prev := *c.beforeCommand
				c.beforeCommand = nil
				last := c.LastActivity
				c.TurnState = prev
				c.LastActivity = last
			}
		case strings.Contains(text, "<local-command-caveat>"):
		case strings.HasPrefix(strings.TrimSpace(text), "<command-name>"):
			prev := c.TurnState
			c.beforeCommand = &prev
			c.working(ts)
		default:
			c.beforeCommand = nil
			c.working(ts)
		}
	}
}

// claudeUserContent returns a user record's first text and whether it
// carries a tool result.
func claudeUserContent(raw map[string]json.RawMessage) (string, bool) {
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw["message"], &msg) != nil {
		return "", false
	}
	var s string
	if json.Unmarshal(msg.Content, &s) == nil {
		return s, false
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(msg.Content, &blocks) != nil {
		return "", false
	}
	text, tool := "", false
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			tool = true
		case "text":
			if text == "" {
				text = b.Text
			}
		}
	}
	return text, tool
}

func jsonBool(raw json.RawMessage) bool {
	var b bool
	return raw != nil && json.Unmarshal(raw, &b) == nil && b
}

// codexTurn follows a Codex rollout: task_started (or a user message) starts
// a turn, task_complete or turn_aborted ends it. Rollouts from Codex versions
// that write neither stay "working" while they are active.
type codexTurn struct{ TurnState }

func (c *codexTurn) observe(eventType string, ts time.Time) {
	if ts.IsZero() {
		return
	}
	switch eventType {
	case "task_started", "user_message":
		c.working(ts)
	case "task_complete", "turn_aborted":
		c.done(ts)
	default:
		if c.State == "" {
			c.working(ts)
		} else if ts.After(c.LastActivity) {
			c.LastActivity = ts
		}
	}
}

// openCodeTurn follows OpenCode messages in creation order: a user message,
// an assistant message still in progress (no time.completed) or one that
// finished with "tool-calls" means working; any other completed assistant
// message (stop, length, an error or an abort) ends the turn.
func openCodeTurn(t *TurnState, data openCodeMessageData, created, completed time.Time) {
	switch {
	case data.Role == "user":
		t.working(created)
	case data.Role != "assistant":
		return
	case data.Time.Completed == 0 || data.Finish == "tool-calls":
		t.working(created)
		if completed.After(t.LastActivity) {
			t.LastActivity = completed
		}
	default:
		t.done(completed)
	}
	if m := data.model(); m != "" && data.Role == "assistant" {
		t.Model = m
	}
}
