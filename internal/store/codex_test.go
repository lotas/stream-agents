package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"stream-agents/internal/store"
)

func TestCodexListSessions(t *testing.T) {
	tmp := t.TempDir()
	dayDir := filepath.Join(tmp, "2026", "05", "01")
	os.MkdirAll(dayDir, 0o755)
	src := filepath.Join("..", "..", "testdata", "codex", "session.jsonl")
	dst := filepath.Join(dayDir, "rollout-2026-05-01T10-00-00-codex-0000-0000-0000-000000000001.jsonl")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(dst, data, 0o644)

	cs := store.NewCodexStore(tmp)
	sessions, err := cs.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.Agent != "codex" {
		t.Errorf("Agent = %q, want codex", s.Agent)
	}
	if s.ID != "codex-0000-0000-0000-000000000001" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.Project != "/Users/test/myproject" {
		t.Errorf("Project = %q", s.Project)
	}
	if s.Title != "review the latest commit" {
		t.Errorf("Title = %q", s.Title)
	}
}

func TestCodexLoadSession(t *testing.T) {
	tmp := t.TempDir()
	dayDir := filepath.Join(tmp, "2026", "05", "01")
	os.MkdirAll(dayDir, 0o755)
	src := filepath.Join("..", "..", "testdata", "codex", "session.jsonl")
	dst := filepath.Join(dayDir, "rollout-2026-05-01T10-00-00-codex-0000-0000-0000-000000000001.jsonl")
	data, _ := os.ReadFile(src)
	os.WriteFile(dst, data, 0o644)

	cs := store.NewCodexStore(tmp)
	cs.ListSessions(context.Background())

	msgs, err := cs.LoadSession(context.Background(), "codex-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %v", len(msgs), msgs)
	}
	wantRoles := []string{"user", "assistant", "tool_call", "tool_result"}
	for i, r := range wantRoles {
		if msgs[i].Role != r {
			t.Errorf("msgs[%d].Role = %q, want %q", i, msgs[i].Role, r)
		}
	}
	if msgs[0].Text != "review the latest commit" {
		t.Errorf("user text = %q", msgs[0].Text)
	}
	if msgs[2].Meta["name"] != "exec_command" {
		t.Errorf("tool_call name = %v", msgs[2].Meta["name"])
	}
	if msgs[3].Text != "abc1234 feat: add new endpoint" {
		t.Errorf("tool_result text = %q", msgs[3].Text)
	}
}
