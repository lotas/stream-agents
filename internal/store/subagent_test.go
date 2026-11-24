package store_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func claudeAssistant(sessionID, model string, sidechain bool, input int) string {
	sc := "false"
	if sidechain {
		sc = "true"
	}
	return `{"type":"assistant","isSidechain":` + sc + `,"sessionId":"` + sessionID + `","timestamp":"2026-11-11T10:00:00Z","message":{"model":"` + model + `","usage":{"input_tokens":` + strconv.Itoa(input) + `,"output_tokens":0}}}` + "\n"
}

func sessionByID(t *testing.T, sessions []store.Session, id string) store.Session {
	t.Helper()
	for _, s := range sessions {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no session %s", id)
	return store.Session{}
}

// Claude Code writes Task/Agent subagents to <session>/subagents/agent-*.jsonl;
// their usage is part of the parent session's.
func TestClaudeSubagentUsageFoldsIntoParent(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "-x-proj")
	subDir := filepath.Join(projDir, "S", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(projDir, "S.jsonl"), []byte(claudeAssistant("S", "claude-opus-5-5", false, 1000000)), 0o644)
	sub := filepath.Join(subDir, "agent-a1.jsonl")
	os.WriteFile(sub, []byte(claudeAssistant("S", "claude-haiku-5-5", true, 1000000)), 0o644)
	os.WriteFile(filepath.Join(subDir, "agent-a1.meta.json"), []byte(`{"finished":true}`), 0o644)

	cs := store.NewClaudeStore(tmp)
	sessions, err := cs.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want the parent only", len(sessions))
	}
	s := sessions[0]
	if s.InputTokens != 2000000 || s.Cost != 4.1 || len(s.Usage) != 2 {
		t.Fatalf("parent = %d tokens, $%v, usage %+v; want 2M tokens, $4.10 over opus and haiku", s.InputTokens, s.Cost, s.Usage)
	}

	// A subagent writing more is picked up though the parent did not change.
	os.WriteFile(sub, []byte(claudeAssistant("S", "claude-haiku-5-5", true, 1000000)+claudeAssistant("S", "claude-haiku-5-5", true, 1000000)), 0o644)
	later := time.Now().Add(time.Minute)
	os.Chtimes(sub, later, later)
	sessions, _ = cs.ListSessions(context.Background())
	if got := sessions[0].Cost; got < 4.199 || got > 4.201 {
		t.Errorf("after subagent grew: cost %v, want 4.2", got)
	}
}

// Old Claude versions wrote subagents as top-level agent-*.jsonl files.
func TestClaudeOldLayoutSubagentNamesParent(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "-x-proj")
	os.MkdirAll(projDir, 0o755)
	os.WriteFile(filepath.Join(projDir, "S.jsonl"), []byte(claudeAssistant("S", "claude-opus-5-5", false, 1)), 0o644)
	os.WriteFile(filepath.Join(projDir, "agent-old.jsonl"), []byte(claudeAssistant("S", "claude-haiku-5-5", true, 1)), 0o644)
	sessions, _ := store.NewClaudeStore(tmp).ListSessions(context.Background())
	if p := sessionByID(t, sessions, "agent-old").Parent; p != "S" {
		t.Errorf("agent-old parent = %q, want S", p)
	}
	if p := sessionByID(t, sessions, "S").Parent; p != "" {
		t.Errorf("S parent = %q, want none", p)
	}
}

func TestCodexThreadSpawnNamesParent(t *testing.T) {
	tmp := t.TempDir()
	dayDir := filepath.Join(tmp, "2026", "11", "11")
	os.MkdirAll(dayDir, 0o755)
	write := func(id, source string) {
		meta := `{"timestamp":"2026-11-11T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"/x/proj","originator":"codex_cli_rs","source":` + source + `}}` + "\n"
		os.WriteFile(filepath.Join(dayDir, "rollout-2026-11-11T10-00-00-"+id+".jsonl"), []byte(meta), 0o644)
	}
	write("child", `{"subagent":{"thread_spawn":{"parent_thread_id":"root","depth":1}}}`)
	write("guard", `{"subagent":{"other":"guardian"}}`)
	write("root", `"cli"`)
	sessions, _ := store.NewCodexStore(tmp).ListSessions(context.Background())
	for id, want := range map[string]string{"child": "root", "guard": "", "root": ""} {
		if got := sessionByID(t, sessions, id).Parent; got != want {
			t.Errorf("%s parent = %q, want %q", id, got, want)
		}
	}
}
