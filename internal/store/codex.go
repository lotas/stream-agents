package store

import (
	"bufio"
	"context"
	"encoding/json"
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
		}
		if line.Type == "session_meta" {
			var meta struct {
				CWD string `json:"cwd"`
			}
			json.Unmarshal(line.Payload, &meta)
			sess.Project = meta.CWD
		}
		if line.Type == "event_msg" && sess.Title == "" {
			var payload struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(line.Payload, &payload); err == nil && payload.Type == "user_message" {
				t := payload.Message
				if runes := []rune(t); len(runes) > 120 {
					t = string(runes[:120]) + "…"
				}
				sess.Title = t
			}
		}
	}
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

	type codexLine struct {
		Timestamp time.Time       `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}

	var msgs []Message
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	for scanner.Scan() {
		var line codexLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}

		switch line.Type {
		case "event_msg":
			var payload struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(line.Payload, &payload); err != nil {
				continue
			}
			switch payload.Type {
			case "user_message":
				msgs = append(msgs, Message{Role: "user", Text: payload.Message, Time: line.Timestamp})
			case "agent_message":
				msgs = append(msgs, Message{Role: "assistant", Text: payload.Message, Time: line.Timestamp})
			}

		case "response_item":
			var payload struct {
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				CallID    string `json:"call_id"`
				Output    string `json:"output"`
			}
			if err := json.Unmarshal(line.Payload, &payload); err != nil {
				continue
			}
			switch payload.Type {
			case "function_call":
				args := payload.Arguments
				if len(args) > 200 {
					args = args[:200] + "…"
				}
				meta := map[string]any{"name": payload.Name, "call_id": payload.CallID, "input": args}
				msgs = append(msgs, Message{Role: "tool_call", Text: payload.Name, Meta: meta, Time: line.Timestamp})
			case "function_call_output":
				out := payload.Output
				if idx := strings.Index(out, "\nOutput:\n"); idx >= 0 {
					out = out[idx+len("\nOutput:\n"):]
				}
				meta := map[string]any{"call_id": payload.CallID}
				msgs = append(msgs, Message{Role: "tool_result", Text: out, Meta: meta, Time: line.Timestamp})
			}
		}
	}
	return msgs, scanner.Err()
}
