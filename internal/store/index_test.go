package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func buildIndexWithFixtures(t *testing.T) *store.Index {
	t.Helper()

	claudeRoot := t.TempDir()
	projDir := filepath.Join(claudeRoot, "-Users-test-myproject")
	os.Mkdir(projDir, 0o755)
	src, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "claude", "session.jsonl"))
	os.WriteFile(filepath.Join(projDir, "test-0000-0000-0000-000000000001.jsonl"), src, 0o644)

	codexRoot := t.TempDir()
	dayDir := filepath.Join(codexRoot, "2026", "05", "01")
	os.MkdirAll(dayDir, 0o755)
	src2, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "codex", "session.jsonl"))
	os.WriteFile(filepath.Join(dayDir, "rollout-2026-05-01T10-00-00-codex-0000-0000-0000-000000000001.jsonl"), src2, 0o644)

	return store.NewIndex(
		store.NewClaudeStore(claudeRoot),
		store.NewCodexStore(codexRoot),
	)
}

func TestIndexListAll(t *testing.T) {
	idx := buildIndexWithFixtures(t)
	sessions, err := idx.ListAll(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestIndexFilterByAgent(t *testing.T) {
	idx := buildIndexWithFixtures(t)
	sessions, _ := idx.ListAll(context.Background(), "claude", "")
	if len(sessions) != 1 || sessions[0].Agent != "claude" {
		t.Errorf("claude filter failed: %v", sessions)
	}
	sessions, _ = idx.ListAll(context.Background(), "codex", "")
	if len(sessions) != 1 || sessions[0].Agent != "codex" {
		t.Errorf("codex filter failed: %v", sessions)
	}
}

func TestIndexFilterByProject(t *testing.T) {
	idx := buildIndexWithFixtures(t)
	sessions, _ := idx.ListAll(context.Background(), "", "/Users/test/myproject")
	if len(sessions) != 2 {
		t.Errorf("project filter: expected 2 (both agents same project), got %d", len(sessions))
	}
}

func TestIndexLoadSession(t *testing.T) {
	idx := buildIndexWithFixtures(t)
	idx.ListAll(context.Background(), "", "") // populate

	msgs, err := idx.LoadSession(context.Background(), "claude", "test-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected messages")
	}
}

func TestIndexProjectList(t *testing.T) {
	idx := buildIndexWithFixtures(t)
	idx.ListAll(context.Background(), "", "")
	projects := idx.Projects()
	if len(projects) == 0 {
		t.Error("expected at least one project")
	}
}

func TestIndexSortedByModified(t *testing.T) {
	idx := buildIndexWithFixtures(t)
	sessions, _ := idx.ListAll(context.Background(), "", "")
	for i := 1; i < len(sessions); i++ {
		if sessions[i].Modified.After(sessions[i-1].Modified) {
			t.Errorf("sessions not sorted: [%d] %v > [%d] %v",
				i, sessions[i].Modified, i-1, sessions[i-1].Modified)
		}
	}
	_ = time.Time{}
}
