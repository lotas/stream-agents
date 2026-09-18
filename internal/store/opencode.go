package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// OpenCodeStore reads OpenCode's SQLite session database. OpenCode stores
// timestamps as Unix milliseconds and message/part payloads as JSON columns.
type OpenCodeStore struct {
	dbPath string
	mu     sync.Mutex
}

// NewOpenCodeStore creates a store rooted at OpenCode's data directory (the
// directory containing opencode.db).
func NewOpenCodeStore(root string) *OpenCodeStore {
	return &OpenCodeStore{
		dbPath: filepath.Join(root, "opencode.db"),
	}
}

func (s *OpenCodeStore) Agent() string { return "opencode" }

func (s *OpenCodeStore) open() (*sql.DB, error) {
	if _, err := os.Stat(s.dbPath); err != nil {
		return nil, err
	}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(s.dbPath)}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// A single read connection avoids holding several snapshots of a live WAL.
	db.SetMaxOpenConns(1)
	return db, nil
}

func (s *OpenCodeStore) ListSessions(ctx context.Context) ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.open()
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, directory, title, time_created, time_updated
		FROM session`)
	if err != nil {
		return nil, fmt.Errorf("read OpenCode sessions: %w", err)
	}

	index := make(map[string]Session)
	models := make(map[string][]string)
	modelSeen := make(map[string]map[string]bool)
	for rows.Next() {
		var id, project, title string
		var created, updated int64
		if err := rows.Scan(&id, &project, &title, &created, &updated); err != nil {
			rows.Close()
			return nil, err
		}
		sess := Session{
			Agent:    "opencode",
			ID:       id,
			Project:  project,
			Title:    title,
			Started:  unixMilli(created),
			Modified: unixMilli(updated),
		}
		index[id] = sess
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	messageRows, err := db.QueryContext(ctx, `
		SELECT session_id, time_created, time_updated, data
		FROM message
		ORDER BY session_id, time_created, id`)
	if err != nil {
		return nil, fmt.Errorf("read OpenCode messages: %w", err)
	}
	for messageRows.Next() {
		var sessionID, raw string
		var created, updated int64
		if err := messageRows.Scan(&sessionID, &created, &updated, &raw); err != nil {
			messageRows.Close()
			return nil, err
		}
		sess, ok := index[sessionID]
		if !ok {
			continue
		}
		var data openCodeMessageData
		_ = json.Unmarshal([]byte(raw), &data)

		sess.MessageCount++
		createdAt := firstTime(unixMilli(data.Time.Created), unixMilli(created))
		completedAt := firstTime(unixMilli(data.Time.Completed), unixMilli(updated))
		if !createdAt.IsZero() {
			sess.ActivityTimes = append(sess.ActivityTimes, createdAt)
		}
		if !completedAt.IsZero() && !completedAt.Equal(createdAt) {
			sess.ActivityTimes = append(sess.ActivityTimes, completedAt)
		}
		if data.Tokens != nil {
			sess.HasTokens = true
			sess.InputTokens += data.Tokens.Input
			sess.OutputTokens += data.Tokens.Output
			sess.CacheReadTokens += data.Tokens.Cache.Read
			sess.CacheCreationTokens += data.Tokens.Cache.Write
		}
		if data.Cost != nil {
			sess.HasCost = true
			sess.Cost += *data.Cost
		}
		model := data.model()
		if model != "" {
			if modelSeen[sessionID] == nil {
				modelSeen[sessionID] = make(map[string]bool)
			}
			if !modelSeen[sessionID][model] {
				modelSeen[sessionID][model] = true
				models[sessionID] = append(models[sessionID], model)
			}
		}
		index[sessionID] = sess
	}
	if err := messageRows.Close(); err != nil {
		return nil, err
	}
	if err := messageRows.Err(); err != nil {
		return nil, err
	}

	out := make([]Session, 0, len(index))
	for id, sess := range index {
		sess.Model = strings.Join(models[id], ", ")
		if len(sess.ActivityTimes) > 0 {
			first, last := sess.ActivityTimes[0], sess.ActivityTimes[0]
			for _, at := range sess.ActivityTimes[1:] {
				if at.Before(first) {
					first = at
				}
				if at.After(last) {
					last = at
				}
			}
			if sess.Started.IsZero() {
				sess.Started = first
			}
			if last.After(first) {
				sess.Duration = last.Sub(first)
			}
		}
		index[id] = sess
		out = append(out, sess)
	}
	sortSessionsByModified(out)
	return out, nil
}

// OpenCode sessions are rows in a shared database rather than independently
// tail-able transcript files, so FilePath intentionally returns no path.
func (s *OpenCodeStore) FilePath(string) string { return "" }

func (s *OpenCodeStore) LoadSession(ctx context.Context, id string) ([]Message, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var exists int
	if err := db.QueryRowContext(ctx, `SELECT 1 FROM session WHERE id = ?`, id).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, os.ErrNotExist
		}
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT m.id, m.time_created, m.data, p.data
		FROM message AS m
		LEFT JOIN part AS p ON p.message_id = m.id
		WHERE m.session_id = ?
		ORDER BY m.time_created, m.id, p.time_created, p.id`, id)
	if err != nil {
		return nil, fmt.Errorf("read OpenCode transcript: %w", err)
	}
	defer rows.Close()

	var out []Message
	usageAttached := make(map[string]bool)
	costAttached := make(map[string]bool)
	for rows.Next() {
		var messageID, messageRaw string
		var created int64
		var partRaw sql.NullString
		if err := rows.Scan(&messageID, &created, &messageRaw, &partRaw); err != nil {
			return nil, err
		}
		if !partRaw.Valid {
			continue
		}
		var info openCodeMessageData
		if json.Unmarshal([]byte(messageRaw), &info) != nil {
			continue
		}
		fallback := firstTime(unixMilli(info.Time.Created), unixMilli(created))
		parsed := parseOpenCodePart([]byte(partRaw.String), info.Role, fallback)
		if len(parsed) == 0 {
			continue
		}
		if info.Tokens != nil && !usageAttached[messageID] {
			parsed[0].Usage = &TokenUsage{
				InputTokens:         info.Tokens.Input,
				OutputTokens:        info.Tokens.Output,
				CacheCreationTokens: info.Tokens.Cache.Write,
				CacheReadTokens:     info.Tokens.Cache.Read,
			}
			usageAttached[messageID] = true
		}
		if info.Cost != nil && !costAttached[messageID] {
			parsed[0].Cost = info.Cost
			costAttached[messageID] = true
		}
		out = append(out, parsed...)
	}
	return out, rows.Err()
}

type openCodeMessageData struct {
	Role       string   `json:"role"`
	ModelID    string   `json:"modelID"`
	ProviderID string   `json:"providerID"`
	Cost       *float64 `json:"cost"`
	Model      struct {
		ModelID    string `json:"modelID"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Time struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Tokens *struct {
		Input  int `json:"input"`
		Output int `json:"output"`
		Cache  struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

func (d openCodeMessageData) model() string {
	provider, model := d.ProviderID, d.ModelID
	if model == "" {
		provider, model = d.Model.ProviderID, d.Model.ModelID
	}
	if provider == "" {
		return model
	}
	if model == "" {
		return provider
	}
	return provider + "/" + model
}

func parseOpenCodePart(raw []byte, role string, fallback time.Time) []Message {
	var part struct {
		Type   string           `json:"type"`
		Text   string           `json:"text"`
		Tool   string           `json:"tool"`
		CallID string           `json:"callID"`
		Time   openCodePartTime `json:"time"`
		State  struct {
			Status string           `json:"status"`
			Input  json.RawMessage  `json:"input"`
			Output json.RawMessage  `json:"output"`
			Error  json.RawMessage  `json:"error"`
			Time   openCodePartTime `json:"time"`
		} `json:"state"`
	}
	if json.Unmarshal(raw, &part) != nil {
		return nil
	}
	switch part.Type {
	case "text":
		if part.Text == "" || (role != "user" && role != "assistant") {
			return nil
		}
		return []Message{{Role: role, Text: part.Text, Time: firstTime(unixMilli(part.Time.Start), fallback)}}
	case "tool":
		started := firstTime(unixMilli(part.State.Time.Start), fallback)
		input := compactJSON(part.State.Input)
		call := Message{
			Role: "tool_call",
			Text: part.Tool,
			Meta: map[string]any{"name": part.Tool, "call_id": part.CallID, "input": input},
			Time: started,
		}
		if part.State.Status != "completed" && part.State.Status != "error" {
			return []Message{call}
		}
		isError := part.State.Status == "error"
		resultRaw := part.State.Output
		if isError {
			resultRaw = part.State.Error
		}
		result := Message{
			Role: "tool_result",
			Text: openCodeRawText(resultRaw),
			Meta: map[string]any{"call_id": part.CallID, "is_error": isError},
			Time: firstTime(unixMilli(part.State.Time.End), started),
		}
		return []Message{call, result}
	}
	return nil
}

type openCodePartTime struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

func unixMilli(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	text := string(data)
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

func openCodeRawText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		if data, err := json.MarshalIndent(value, "", "  "); err == nil {
			return string(data)
		}
	}
	return strings.TrimSpace(string(raw))
}
