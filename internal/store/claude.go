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

// ClaudeDecodeProject converts a ~/.claude/projects dir name to an absolute path.
// Claude encodes the cwd by replacing every "/" with "-", giving a leading "-".
func ClaudeDecodeProject(dirName string) string {
	s := strings.TrimPrefix(dirName, "-")
	return "/" + strings.ReplaceAll(s, "-", "/")
}

type claudeEntry struct {
	path    string
	mtime   time.Time
	session Session
}

// ClaudeStore scans one or more Claude projects directories and serves transcripts.
type ClaudeStore struct {
	roots []string
	mu    sync.Mutex
	index map[string]claudeEntry
}

func NewClaudeStore(roots ...string) *ClaudeStore {
	return &ClaudeStore{roots: roots, index: make(map[string]claudeEntry)}
}

func (s *ClaudeStore) Agent() string { return "claude" }

func (s *ClaudeStore) ListSessions(ctx context.Context) ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, root := range s.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, projEntry := range entries {
			if !projEntry.IsDir() {
				continue
			}
			projDir := filepath.Join(root, projEntry.Name())
			files, err := os.ReadDir(projDir)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
					continue
				}
				id := strings.TrimSuffix(f.Name(), ".jsonl")
				fpath := filepath.Join(projDir, f.Name())
				fi, err := f.Info()
				if err != nil {
					continue
				}
				mtime := fi.ModTime()

				if existing, ok := s.index[id]; ok && !mtime.After(existing.mtime) {
					continue
				}

				sess := s.parseSessionMeta(fpath, id, projEntry.Name(), mtime)
				s.index[id] = claudeEntry{path: fpath, mtime: mtime, session: sess}
			}
		}
	}

	out := make([]Session, 0, len(s.index))
	for _, e := range s.index {
		out = append(out, e.session)
	}
	sortSessionsByModified(out)
	return out, nil
}

func (s *ClaudeStore) FilePath(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.index[id]; ok {
		return e.path
	}
	return ""
}

func (s *ClaudeStore) LoadSession(ctx context.Context, id string) ([]Message, error) {
	s.mu.Lock()
	e, ok := s.index[id]
	s.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return parseClaudeJSONL(e.path)
}

func (s *ClaudeStore) parseSessionMeta(fpath, id, dirName string, mtime time.Time) Session {
	sess := Session{
		Agent:    "claude",
		ID:       id,
		Project:  ClaudeDecodeProject(dirName),
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
	for scanner.Scan() {
		count++
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}
		var typ string
		if err := json.Unmarshal(raw["type"], &typ); err != nil {
			continue
		}
		if tsRaw, ok := raw["timestamp"]; ok {
			var ts time.Time
			if json.Unmarshal(tsRaw, &ts) == nil && !ts.IsZero() {
				if firstTime.IsZero() {
					firstTime = ts
				}
				lastTime = ts
			}
		}
		if typ == "user" && sess.Project == ClaudeDecodeProject(dirName) {
			var cwd string
			if cwdRaw, ok := raw["cwd"]; ok {
				json.Unmarshal(cwdRaw, &cwd)
				if cwd != "" {
					sess.Project = cwd
				}
			}
		}
		if typ == "user" && sess.Title == "" {
			if text := claudeExtractUserText(raw); text != "" {
				if runes := []rune(text); len(runes) > 120 {
					text = string(runes[:120]) + "…"
				}
				sess.Title = text
			}
		}
		if typ == "assistant" {
			if msgRaw, ok := raw["message"]; ok {
				var msg struct {
					Usage *struct {
						InputTokens         int `json:"input_tokens"`
						OutputTokens        int `json:"output_tokens"`
						CacheReadTokens     int `json:"cache_read_input_tokens"`
					} `json:"usage"`
				}
				if json.Unmarshal(msgRaw, &msg) == nil && msg.Usage != nil {
					sess.InputTokens += msg.Usage.InputTokens
					sess.OutputTokens += msg.Usage.OutputTokens
					sess.CacheReadTokens += msg.Usage.CacheReadTokens
				}
			}
		}
	}
	sess.MessageCount = count
	if !firstTime.IsZero() && lastTime.After(firstTime) {
		sess.Duration = lastTime.Sub(firstTime)
	}
	return sess
}

func claudeExtractUserText(raw map[string]json.RawMessage) string {
	msgRaw, ok := raw["message"]
	if !ok {
		return ""
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

func parseClaudeJSONL(fpath string) ([]Message, error) {
	f, err := os.Open(fpath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseClaudeMessages(f)
}

// ParseClaudeMessages parses Claude JSONL messages from r.
func ParseClaudeMessages(r io.Reader) ([]Message, error) {
	var msgs []Message
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	for scanner.Scan() {
		msgs = append(msgs, ParseClaudeJSONLLine(scanner.Bytes())...)
	}
	return msgs, scanner.Err()
}

// ParseClaudeJSONLLine parses a single raw JSONL line from a Claude session file.
func ParseClaudeJSONLLine(data []byte) []Message {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	var typ string
	if json.Unmarshal(raw["type"], &typ) != nil {
		return nil
	}
	var ts time.Time
	if tsRaw, ok := raw["timestamp"]; ok {
		json.Unmarshal(tsRaw, &ts)
	}
	switch typ {
	case "user":
		return claudeParseUserMsg(raw, ts)
	case "assistant":
		return claudeParseAssistantMsg(raw, ts)
	}
	return nil
}

func claudeParseUserMsg(raw map[string]json.RawMessage, ts time.Time) []Message {
	msgRaw, ok := raw["message"]
	if !ok {
		return nil
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return nil
	}

	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		return []Message{{Role: "user", Text: s, Time: ts}}
	}

	var blocks []json.RawMessage
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil
	}

	var out []Message
	for _, b := range blocks {
		var block struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if err := json.Unmarshal(b, &block); err != nil {
			continue
		}
		switch block.Type {
		case "text":
			if block.Text != "" {
				out = append(out, Message{Role: "user", Text: block.Text, Time: ts})
			}
		case "tool_result":
			text := toolResultText(block.Content)
			meta := map[string]any{"tool_use_id": block.ToolUseID, "is_error": block.IsError}
			out = append(out, Message{Role: "tool_result", Text: text, Meta: meta, Time: ts})
		}
	}
	return out
}

func claudeParseAssistantMsg(raw map[string]json.RawMessage, ts time.Time) []Message {
	msgRaw, ok := raw["message"]
	if !ok {
		return nil
	}
	var msg struct {
		Content []json.RawMessage `json:"content"`
		Usage   *struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return nil
	}

	var out []Message
	for _, b := range msg.Content {
		var block struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(b, &block); err != nil {
			continue
		}
		switch block.Type {
		case "text":
			if block.Text != "" {
				out = append(out, Message{Role: "assistant", Text: block.Text, Time: ts})
			}
		case "tool_use":
			inputStr := string(block.Input)
			if len(inputStr) > 200 {
				inputStr = inputStr[:200] + "…"
			}
			meta := map[string]any{"name": block.Name, "id": block.ID, "input": inputStr}
			out = append(out, Message{Role: "tool_call", Text: block.Name, Meta: meta, Time: ts})
		}
	}
	// Attach token usage to the first message of this turn.
	if msg.Usage != nil && len(out) > 0 {
		u := &TokenUsage{
			InputTokens:         msg.Usage.InputTokens,
			OutputTokens:        msg.Usage.OutputTokens,
			CacheCreationTokens: msg.Usage.CacheCreationTokens,
			CacheReadTokens:     msg.Usage.CacheReadTokens,
		}
		idx := 0
		for i := range out {
			if out[i].Role == "assistant" {
				idx = i
				break
			}
		}
		out[idx].Usage = u
	}
	return out
}

func toolResultText(content json.RawMessage) string {
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(content)
}
