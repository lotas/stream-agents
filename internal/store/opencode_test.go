package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func buildOpenCodeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	for _, statement := range []string{
		`CREATE TABLE session (
			id TEXT PRIMARY KEY, directory TEXT NOT NULL, title TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL
		)`,
		`CREATE TABLE message (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL, data TEXT NOT NULL
		)`,
		`CREATE TABLE part (
			id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL
		)`,
		`INSERT INTO session VALUES
			('ses_test', '/Users/test/project', 'Add OpenCode support', 1770000000000, 1770000005000)`,
		`INSERT INTO message VALUES
			('msg_user', 'ses_test', 1770000000000, 1770000000000,
			 '{"role":"user","time":{"created":1770000000000}}'),
			('msg_assistant', 'ses_test', 1770000001000, 1770000005000,
			 '{"role":"assistant","providerID":"github-copilot","modelID":"gemini-2.5-pro","cost":0.012345,"time":{"created":1770000001000,"completed":1770000005000},"tokens":{"input":100,"output":20,"reasoning":5,"cache":{"read":40,"write":10}}}')`,
		`INSERT INTO part VALUES
			('prt_user', 'msg_user', 'ses_test', 1770000000000, 1770000000000,
			 '{"type":"text","text":"please inspect this"}'),
			('prt_text', 'msg_assistant', 'ses_test', 1770000001000, 1770000001000,
			 '{"type":"text","text":"I will inspect it.","time":{"start":1770000001000,"end":1770000001100}}'),
			('prt_tool', 'msg_assistant', 'ses_test', 1770000002000, 1770000003000,
			 '{"type":"tool","tool":"bash","callID":"call_1","state":{"status":"completed","input":{"command":"pwd"},"output":"/Users/test/project","time":{"start":1770000002000,"end":1770000003000}}}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("fixture statement failed: %v\n%s", err, statement)
		}
	}
	return root
}

func TestOpenCodeListSessions(t *testing.T) {
	root := buildOpenCodeFixture(t)
	sessions, err := store.NewOpenCodeStore(root).ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	s := sessions[0]
	if s.Agent != "opencode" || s.ID != "ses_test" || s.Project != "/Users/test/project" {
		t.Errorf("identity fields = %+v", s)
	}
	if s.Title != "Add OpenCode support" || s.MessageCount != 2 {
		t.Errorf("metadata fields = %+v", s)
	}
	if s.Model != "github-copilot/gemini-2.5-pro" {
		t.Errorf("Model = %q", s.Model)
	}
	if !s.HasTokens || s.InputTokens != 100 || s.OutputTokens != 20 || s.CacheReadTokens != 40 || s.CacheCreationTokens != 10 {
		t.Errorf("token fields = %+v", s)
	}
	if !s.HasCost || s.Cost != 0.012345 {
		t.Errorf("cost fields = %+v", s)
	}
	if want := 5 * time.Second; s.Duration != want {
		t.Errorf("Duration = %v, want %v", s.Duration, want)
	}
	if got := store.NewOpenCodeStore(root).FilePath("ses_test"); got != "" {
		t.Errorf("FilePath = %q, want empty for database-backed sessions", got)
	}
}

func TestOpenCodeLoadSession(t *testing.T) {
	root := buildOpenCodeFixture(t)
	msgs, err := store.NewOpenCodeStore(root).LoadSession(context.Background(), "ses_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4: %+v", len(msgs), msgs)
	}
	for i, role := range []string{"user", "assistant", "tool_call", "tool_result"} {
		if msgs[i].Role != role {
			t.Errorf("messages[%d].Role = %q, want %q", i, msgs[i].Role, role)
		}
	}
	if msgs[0].Text != "please inspect this" || msgs[1].Text != "I will inspect it." {
		t.Errorf("text messages = %q / %q", msgs[0].Text, msgs[1].Text)
	}
	if msgs[1].Usage == nil || msgs[1].Usage.InputTokens != 100 || msgs[1].Usage.CacheReadTokens != 40 {
		t.Errorf("assistant usage = %+v", msgs[1].Usage)
	}
	if msgs[1].Cost == nil || *msgs[1].Cost != 0.012345 {
		t.Errorf("assistant cost = %v", msgs[1].Cost)
	}
	for i := 2; i < len(msgs); i++ {
		if msgs[i].Cost != nil {
			t.Errorf("message %d repeats assistant cost: %v", i, *msgs[i].Cost)
		}
	}
	if msgs[2].Meta["name"] != "bash" || msgs[2].Meta["input"] != `{"command":"pwd"}` {
		t.Errorf("tool call = %+v", msgs[2])
	}
	if msgs[3].Text != "/Users/test/project" || msgs[3].Meta["is_error"] != false {
		t.Errorf("tool result = %+v", msgs[3])
	}
}

func TestOpenCodeMissingDatabase(t *testing.T) {
	sessions, err := store.NewOpenCodeStore(t.TempDir()).ListSessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions = %v, err = %v", sessions, err)
	}
	_, err = store.NewOpenCodeStore(t.TempDir()).LoadSession(context.Background(), "missing")
	if !os.IsNotExist(err) {
		t.Fatalf("LoadSession error = %v, want os.ErrNotExist", err)
	}
}
