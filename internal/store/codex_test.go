package store_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestCodexCompletedItems(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "codex", "completed-items.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	const id = "01a08f88-b845-7c61-9464-dfc0118e264c"
	if err := os.WriteFile(filepath.Join(root, "rollout-2026-09-11T08-14-57-"+id+".jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cs := store.NewCodexStore(root)
	sessions, err := cs.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Title != "review the cutover script" {
		t.Fatalf("sessions = %+v", sessions)
	}
	msgs, err := cs.LoadSession(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages without duplicate response items, got %+v", msgs)
	}
	for i, role := range []string{"user", "assistant", "tool_call", "tool_result"} {
		if msgs[i].Role != role || msgs[i].Time.IsZero() {
			t.Errorf("message %d = %+v", i, msgs[i])
		}
	}
	if msgs[1].Text != "I will trace the script." {
		t.Errorf("assistant = %+v", msgs[1])
	}
	if msgs[2].Meta["name"] != "exec" || msgs[2].Meta["input"] != "const r = await tools.exec_command({cmd: 'pwd'}); text(r);" {
		t.Errorf("call = %+v", msgs[2])
	}
	if strings.TrimSpace(msgs[3].Text) != "/Users/test/myproject" || msgs[3].Meta["call_id"] != msgs[2].Meta["call_id"] {
		t.Errorf("result = %+v", msgs[3])
	}
}

func TestCodexFunctionOutputContentArray(t *testing.T) {
	msgs := store.ParseCodexJSONLLine([]byte(`{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-2","output":[{"type":"input_text","text":"first"},{"type":"output_text","text":"second"}]}}`))
	if len(msgs) != 1 || msgs[0].Text != "first\nsecond" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestCodexCumulativeStats(t *testing.T) {
	root := t.TempDir()
	event := `{"timestamp":"2026-01-01T00:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":25,"cached_input_tokens":40}}}}` + "\n"
	path := filepath.Join(root, "rollout-2026-01-01T00-00-00-test.jsonl")
	if err := os.WriteFile(path, []byte(event+event), 0600); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.NewCodexStore(root).ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions: %v %v", sessions, err)
	}
	s := sessions[0]
	if !s.HasTokens || s.InputTokens != 60 || s.OutputTokens != 25 || s.CacheReadTokens != 40 || s.Started.Year() != 2026 {
		t.Fatalf("incorrect usage: %+v", s)
	}
}
