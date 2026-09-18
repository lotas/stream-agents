package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func TestClaudeDecodeProject(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{"-Users-ykurmyza-dev-foo", "/Users/ykurmyza/dev/foo"},
		{"-Users-test-myproject", "/Users/test/myproject"},
		{"-root", "/root"},
	}
	for _, c := range cases {
		got := store.ClaudeDecodeProject(c.dir)
		if got != c.want {
			t.Errorf("DecodeProject(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
}

func TestClaudeListSessions(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "-Users-test-myproject")
	if err := os.Mkdir(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "..", "testdata", "claude", "session.jsonl")
	dst := filepath.Join(projDir, "test-0000-0000-0000-000000000001.jsonl")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cs := store.NewClaudeStore(tmp)
	sessions, err := cs.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if len(s.ActivityTimes) < 2 {
		t.Fatal("missing activity timestamps")
	}
	if s.Agent != "claude" {
		t.Errorf("Agent = %q, want claude", s.Agent)
	}
	if s.ID != "test-0000-0000-0000-000000000001" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.Project != "/Users/test/myproject" {
		t.Errorf("Project = %q, want /Users/test/myproject", s.Project)
	}
	if s.Title != "hello world" {
		t.Errorf("Title = %q, want 'hello world'", s.Title)
	}
}

func TestClaudeLoadSession(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "-Users-test-myproject")
	os.Mkdir(projDir, 0o755)
	src := filepath.Join("..", "..", "testdata", "claude", "session.jsonl")
	dst := filepath.Join(projDir, "test-0000-0000-0000-000000000001.jsonl")
	data, _ := os.ReadFile(src)
	os.WriteFile(dst, data, 0o644)

	cs := store.NewClaudeStore(tmp)
	cs.ListSessions(context.Background()) // populate index

	msgs, err := cs.LoadSession(context.Background(), "test-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	roles := make([]string, len(msgs))
	for i, m := range msgs {
		roles[i] = m.Role
	}
	want := []string{"user", "assistant", "tool_call", "tool_result"}
	for i, r := range want {
		if i >= len(roles) || roles[i] != r {
			t.Errorf("msgs[%d].Role = %q, want %q (all: %v)", i, roles[i], r, roles)
		}
	}
	if msgs[0].Text != "hello world" {
		t.Errorf("user text = %q", msgs[0].Text)
	}
	if msgs[1].Text != "Hello! How can I help?" {
		t.Errorf("assistant text = %q", msgs[1].Text)
	}
	if msgs[2].Meta["name"] != "Bash" {
		t.Errorf("tool_call name = %v", msgs[2].Meta["name"])
	}
	if msgs[0].Time != (time.Time{}) && msgs[0].Time.Year() != 2026 {
		t.Errorf("Time year = %d", msgs[0].Time.Year())
	}
}

func TestClaudeFilePath(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "-Users-test-myproject")
	os.Mkdir(projDir, 0o755)
	dst := filepath.Join(projDir, "test-0000-0000-0000-000000000001.jsonl")
	data, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "claude", "session.jsonl"))
	os.WriteFile(dst, data, 0o644)

	cs := store.NewClaudeStore(tmp)
	cs.ListSessions(context.Background())

	p := cs.FilePath("test-0000-0000-0000-000000000001")
	if p != dst {
		t.Errorf("FilePath = %q, want %q", p, dst)
	}
	if cs.FilePath("nonexistent") != "" {
		t.Error("expected empty string for unknown id")
	}
}

func TestClaudeEstimatedCost(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "-Users-test-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","timestamp":"2026-09-18T12:00:00Z","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"first"}],"usage":{"input_tokens":1000000,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-09-18T12:00:01Z","message":{"role":"assistant","model":"claude-fable-5","content":[{"type":"text","text":"second"}],"usage":{"input_tokens":1000000,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, "priced-session.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	cs := store.NewClaudeStore(root)
	sessions, err := cs.ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %+v, err = %v", sessions, err)
	}
	if sessions[0].Model != "claude-opus-5, claude-fable-5" || !sessions[0].HasCost || sessions[0].Cost != 15 {
		t.Fatalf("priced session = %+v", sessions[0])
	}
	msgs, err := cs.LoadSession(context.Background(), "priced-session")
	if err != nil || len(msgs) != 2 || msgs[0].Cost == nil || *msgs[0].Cost != 5 || msgs[1].Cost == nil || *msgs[1].Cost != 10 {
		t.Fatalf("priced messages = %+v, err = %v", msgs, err)
	}
}
