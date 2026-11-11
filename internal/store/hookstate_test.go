package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"stream-agents/internal/store"
)

func hookFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, "claude"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestApplyHookEvent(t *testing.T) {
	dir := t.TempDir()
	apply := func(ev store.HookEvent) {
		t.Helper()
		if err := store.ApplyHookEvent(dir, ev, at(0)); err != nil {
			t.Fatalf("%+v: %v", ev, err)
		}
	}
	files := func() string { t.Helper(); return fmtList(hookFiles(t, dir)) }

	apply(store.HookEvent{SessionID: "S", HookEventName: "PermissionRequest", ToolName: "Bash"})
	apply(store.HookEvent{SessionID: "S", AgentID: "a1", HookEventName: "Notification", NotificationType: "permission_prompt", Message: "Claude needs your permission to use Bash"})
	apply(store.HookEvent{SessionID: "S", HookEventName: "Notification", NotificationType: "idle_prompt", Message: "Claude is waiting for your input"})
	apply(store.HookEvent{SessionID: "T", HookEventName: "Notification", Message: "Claude needs your permission to use Edit"}) // no notification_type
	if got := files(); got != "S.agent-a1.json S.json T.json" {
		t.Fatalf("after prompts: %s", got)
	}
	// A sibling agent's tool use leaves the subagent's prompt alone.
	apply(store.HookEvent{SessionID: "S", HookEventName: "PostToolUse", ToolName: "Bash"})
	if got := files(); got != "S.agent-a1.json T.json" {
		t.Fatalf("after main PostToolUse: %s", got)
	}
	apply(store.HookEvent{SessionID: "S", AgentID: "a1", HookEventName: "PostToolUseFailure"})
	if got := files(); got != "T.json" {
		t.Fatalf("after subagent PostToolUseFailure: %s", got)
	}
	apply(store.HookEvent{SessionID: "S", AgentID: "a2", HookEventName: "PermissionRequest"})
	apply(store.HookEvent{SessionID: "S", HookEventName: "UserPromptSubmit"})
	if got := files(); got != "T.json" {
		t.Fatalf("a prompt clears the whole session: %s", got)
	}
	apply(store.HookEvent{SessionID: "T", HookEventName: "SessionEnd"})
	if got := files(); got != "" {
		t.Fatalf("after SessionEnd: %s", got)
	}

	for _, bad := range []store.HookEvent{{SessionID: "", HookEventName: "Stop"}, {SessionID: "../x", HookEventName: "PermissionRequest"}, {SessionID: "S", AgentID: "../../x", HookEventName: "PermissionRequest"}} {
		if err := store.ApplyHookEvent(dir, bad, at(0)); err == nil {
			t.Errorf("%+v: want error", bad)
		}
	}
}

func fmtList(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += " "
		}
		out += n
	}
	return out
}

func TestActiveSessionsPermissionFromHook(t *testing.T) {
	config, proj := claudeConfig(t)
	copyFixture(t, "pending-tool.jsonl", filepath.Join(proj, "S.jsonl"), at(5)) // tool_use at 5 s
	registerProcess(t, config, os.Getpid(), "S")
	stateDir := t.TempDir()
	idx := store.NewIndex(store.NewClaudeStore(filepath.Join(config, "projects")))
	idx.SetHookStateDir(stateDir)
	check := func(state, reason string, since time.Time) {
		t.Helper()
		a, err := idx.ActiveSessions(context.Background(), at(60))
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != 1 || a[0].State != state || a[0].Reason != reason || !a[0].Since.Equal(since) {
			t.Fatalf("active = %+v, want %s/%s since %v", a, state, reason, since)
		}
	}
	check(store.StateWorking, "", at(0))
	if err := store.ApplyHookEvent(stateDir, store.HookEvent{SessionID: "S", HookEventName: "PermissionRequest", ToolName: "Bash"}, at(6)); err != nil {
		t.Fatal(err)
	}
	check(store.StateWaiting, store.ReasonPermission, at(6))
	if err := store.ApplyHookEvent(stateDir, store.HookEvent{SessionID: "S", HookEventName: "PostToolUse"}, at(20)); err != nil {
		t.Fatal(err)
	}
	check(store.StateWorking, "", at(0))
}
