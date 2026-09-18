package store

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type codexEntry struct {
	path    string
	mtime   time.Time
	session Session
}

type codexTokenUsage struct {
	Input     int `json:"input_tokens"`
	Output    int `json:"output_tokens"`
	Cached    int `json:"cached_input_tokens"`
	Reasoning int `json:"reasoning_output_tokens"`
}

func (u codexTokenUsage) tokenUsage() TokenUsage {
	input := u.Input - u.Cached
	if input < 0 {
		input = 0
	}
	return TokenUsage{InputTokens: input, OutputTokens: u.Output, CacheReadTokens: u.Cached}
}

func (u codexTokenUsage) delta(previous codexTokenUsage) codexTokenUsage {
	if u.Input < previous.Input || u.Output < previous.Output || u.Cached < previous.Cached {
		return u
	}
	return codexTokenUsage{
		Input:     u.Input - previous.Input,
		Output:    u.Output - previous.Output,
		Cached:    u.Cached - previous.Cached,
		Reasoning: u.Reasoning - previous.Reasoning,
	}
}

// CodexStore scans ~/.codex/sessions/YYYY/MM/DD/ and serves Codex transcripts.
type CodexStore struct {
	root  string
	mu    sync.Mutex
	index map[string]codexEntry
}

func NewCodexStore(root string) *CodexStore {
	return &CodexStore{root: root, index: make(map[string]codexEntry)}
}

func (s *CodexStore) Agent() string { return "codex" }

func (s *CodexStore) ListSessions(ctx context.Context) ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		mtime := fi.ModTime()

		id := codexIDFromPath(path)
		if id == "" {
			return nil
		}

		if existing, ok := s.index[id]; ok && !mtime.After(existing.mtime) {
			return nil
		}

		sess := parseCodexSessionMeta(path, id, mtime)
		s.index[id] = codexEntry{path: path, mtime: mtime, session: sess}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	out := make([]Session, 0, len(s.index))
	for _, e := range s.index {
		out = append(out, e.session)
	}
	sortSessionsByModified(out)
	return out, nil
}

func (s *CodexStore) FilePath(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.index[id]; ok {
		return e.path
	}
	return ""
}

func (s *CodexStore) LoadSession(ctx context.Context, id string) ([]Message, error) {
	s.mu.Lock()
	e, ok := s.index[id]
	s.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return parseCodexJSONL(e.path)
}

// codexIDFromPath extracts the UUID from a Codex filename.
// Filenames: rollout-2026-02-11T13-42-40-<uuid>.jsonl
// Strip "rollout-" prefix, then strip first 20 chars (the timestamp segment).
func codexIDFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	base = strings.TrimPrefix(base, "rollout-")
	if len(base) > 20 {
		return base[20:]
	}
	return base
}

func parseCodexSessionMeta(fpath, id string, mtime time.Time) Session {
	sess := Session{
		Agent:    "codex",
		ID:       id,
		Modified: mtime,
	}

	f, err := os.Open(fpath)
	if err != nil {
		return sess
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	count := 0
	var firstTime, lastTime time.Time
	var currentModel string
	var models []string
	modelSeen := make(map[string]bool)
	var previousUsage codexTokenUsage
	for scanner.Scan() {
		count++
		var line struct {
			Timestamp time.Time       `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if !line.Timestamp.IsZero() {
			if firstTime.IsZero() {
				firstTime = line.Timestamp
			}
			lastTime = line.Timestamp
			sess.ActivityTimes = append(sess.ActivityTimes, line.Timestamp)
		}
		if line.Type == "event_msg" {
			var event struct {
				Type string `json:"type"`
				Info struct {
					Total *codexTokenUsage `json:"total_token_usage"`
				} `json:"info"`
			}
			if json.Unmarshal(line.Payload, &event) == nil && event.Type == "token_count" && event.Info.Total != nil {
				// Codex reports cumulative totals; repeated events must not be summed.
				u := event.Info.Total
				sess.HasTokens = true
				total := u.tokenUsage()
				sess.InputTokens = total.InputTokens
				sess.OutputTokens = total.OutputTokens
				sess.CacheReadTokens = total.CacheReadTokens
				if cost, ok := estimateModelCost(currentModel, u.delta(previousUsage).tokenUsage()); ok {
					sess.HasCost = true
					sess.Cost += cost
				}
				previousUsage = *u
			}
		}
		if line.Type == "turn_context" {
			var turn struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(line.Payload, &turn) == nil && turn.Model != "" {
				currentModel = turn.Model
				if !modelSeen[currentModel] {
					modelSeen[currentModel] = true
					models = append(models, currentModel)
				}
			}
		}
		if line.Type == "session_meta" {
			var meta struct {
				CWD string `json:"cwd"`
			}
			json.Unmarshal(line.Payload, &meta)
			sess.Project = meta.CWD
		}
		if sess.Title == "" {
			for _, msg := range ParseCodexJSONLLine(scanner.Bytes()) {
				if msg.Role == "user" {
					t := msg.Text
					if runes := []rune(t); len(runes) > 120 {
						t = string(runes[:120]) + "…"
					}
					sess.Title = t
					break
				}
			}
		}
	}
	sess.Model = strings.Join(models, ", ")
	sess.Started = firstTime
	sess.MessageCount = count
	if !firstTime.IsZero() && lastTime.After(firstTime) {
		sess.Duration = lastTime.Sub(firstTime)
	}
	return sess
}

func parseCodexJSONL(fpath string) ([]Message, error) {
	f, err := os.Open(fpath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseCodexMessages(f)
}

// ParseCodexMessages parses Codex JSONL messages from r.
func ParseCodexMessages(r io.Reader) ([]Message, error) {
	var msgs []Message
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	for scanner.Scan() {
		msgs = append(msgs, ParseCodexJSONLLine(scanner.Bytes())...)
	}
	return msgs, scanner.Err()
}

// ParseCodexJSONLLine parses a single raw JSONL line from a Codex session file.
func ParseCodexJSONLLine(data []byte) []Message {
	var line struct {
		Timestamp time.Time       `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(data, &line) != nil {
		return nil
	}
	switch line.Type {
	case "event_msg":
		var payload struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Item    struct {
				Type    string          `json:"type"`
				Content json.RawMessage `json:"content"`
			} `json:"item"`
		}
		if json.Unmarshal(line.Payload, &payload) != nil {
			return nil
		}
		switch payload.Type {
		case "item_completed":
			// Use completed conversation events, not response_item messages:
			// rollouts contain both, and response items also include injected context.
			var role string
			switch payload.Item.Type {
			case "UserMessage":
				role = "user"
			case "AgentMessage":
				role = "assistant"
			default:
				return nil
			}
			return []Message{{Role: role, Text: codexContentText(payload.Item.Content), Time: line.Timestamp}}
		case "user_message":
			return []Message{{Role: "user", Text: payload.Message, Time: line.Timestamp}}
		case "agent_message":
			return []Message{{Role: "assistant", Text: payload.Message, Time: line.Timestamp}}
		}
	case "response_item":
		var payload struct {
			Type      string          `json:"type"`
			Name      string          `json:"name"`
			Arguments string          `json:"arguments"`
			CallID    string          `json:"call_id"`
			Output    json.RawMessage `json:"output"`
			Input     string          `json:"input"`
		}
		if json.Unmarshal(line.Payload, &payload) != nil {
			return nil
		}
		switch payload.Type {
		case "function_call", "custom_tool_call":
			args := payload.Arguments
			if payload.Type == "custom_tool_call" {
				args = payload.Input
			}
			if len(args) > 200 {
				args = args[:200] + "…"
			}
			meta := map[string]any{"name": payload.Name, "call_id": payload.CallID, "input": args}
			return []Message{{Role: "tool_call", Text: payload.Name, Meta: meta, Time: line.Timestamp}}
		case "function_call_output", "custom_tool_call_output":
			out := codexContentText(payload.Output)
			if idx := strings.Index(out, "\nOutput:\n"); idx >= 0 {
				out = out[idx+len("\nOutput:\n"):]
			}
			meta := map[string]any{"call_id": payload.CallID}
			return []Message{{Role: "tool_result", Text: out, Meta: meta, Time: line.Timestamp}}
		}
	}
	return nil
}

// codexContentText accepts legacy string outputs and multimodal content arrays.
// Non-text blocks (such as images) do not prevent adjacent text from rendering.
func codexContentText(data json.RawMessage) string {
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text", "Text", "input_text", "output_text":
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
