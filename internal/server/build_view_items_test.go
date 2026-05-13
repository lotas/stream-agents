package server

import (
	"strings"
	"testing"

	"stream-agents/internal/store"
)

func msg(role string, text string, meta map[string]any) store.Message {
	return store.Message{Role: role, Text: text, Meta: meta}
}

func TestBuildViewItems_ClaudeFusion(t *testing.T) {
	msgs := []store.Message{
		msg("tool_call", "Bash", map[string]any{"name": "Bash", "id": "abc", "input": `{"command":"ls"}`}),
		msg("tool_result", "file1\nfile2", map[string]any{"tool_use_id": "abc"}),
	}
	items, _, _, _ := buildViewItems(msgs)
	if len(items) != 1 {
		t.Fatalf("want 1 fused item, got %d", len(items))
	}
	p := items[0].Tool
	if !strings.Contains(string(p.Output), "file1") {
		t.Errorf("Output = %q", p.Output)
	}
	if p.Pending {
		t.Error("pair should not be pending after result arrives")
	}
	if p.IsError {
		t.Error("unexpected error flag")
	}
}

func TestBuildViewItems_CodexFusion(t *testing.T) {
	msgs := []store.Message{
		msg("tool_call", "exec", map[string]any{"name": "exec", "call_id": "x1", "input": `{"command":"echo hi"}`}),
		msg("tool_result", "hi", map[string]any{"call_id": "x1"}),
	}
	items, _, _, _ := buildViewItems(msgs)
	if len(items) != 1 {
		t.Fatalf("want 1 fused item, got %d", len(items))
	}
	if !strings.Contains(string(items[0].Tool.Output), "hi") {
		t.Errorf("Output = %q", items[0].Tool.Output)
	}
}

func TestBuildViewItems_OrphanCall(t *testing.T) {
	msgs := []store.Message{
		msg("tool_call", "Read", map[string]any{"name": "Read", "id": "no-result", "input": `{"file_path":"/tmp/x"}`}),
	}
	items, _, _, _ := buildViewItems(msgs)
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if !items[0].Tool.Pending {
		t.Error("unpaired call should be Pending=true")
	}
}

func TestBuildViewItems_OrphanResult(t *testing.T) {
	msgs := []store.Message{
		msg("tool_result", "some output", map[string]any{"tool_use_id": "ghost"}),
	}
	items, _, _, _ := buildViewItems(msgs)
	if len(items) != 1 {
		t.Fatalf("want 1 orphan item, got %d", len(items))
	}
	p := items[0].Tool
	if !strings.Contains(string(p.Output), "some output") {
		t.Errorf("Output = %q", p.Output)
	}
	if p.Pending {
		t.Error("orphan result should not be Pending")
	}
}

func TestBuildViewItems_PerNameIndex(t *testing.T) {
	msgs := []store.Message{
		msg("tool_call", "Bash", map[string]any{"name": "Bash", "id": "b1", "input": `{}`}),
		msg("tool_result", "r1", map[string]any{"tool_use_id": "b1"}),
		msg("tool_call", "Bash", map[string]any{"name": "Bash", "id": "b2", "input": `{}`}),
		msg("tool_result", "r2", map[string]any{"tool_use_id": "b2"}),
		msg("tool_call", "Bash", map[string]any{"name": "Bash", "id": "b3", "input": `{}`}),
		msg("tool_result", "r3", map[string]any{"tool_use_id": "b3"}),
	}
	items, _, _, _ := buildViewItems(msgs)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	for i, want := range []int{1, 2, 3} {
		if items[i].Tool.Index != want {
			t.Errorf("items[%d].Index = %d, want %d", i, items[i].Tool.Index, want)
		}
	}
}

func TestBuildViewItems_TurnAnchors(t *testing.T) {
	msgs := []store.Message{
		msg("user", "first question\nmore detail", nil),
		msg("assistant", "answer", nil),
		msg("user", "second question", nil),
	}
	items, turns, _, _ := buildViewItems(msgs)
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(turns))
	}
	if turns[0].Title != "first question" {
		t.Errorf("turn[0].Title = %q", turns[0].Title)
	}
	if turns[0].AnchorID != "turn-1" {
		t.Errorf("turn[0].AnchorID = %q", turns[0].AnchorID)
	}
	// User items should carry TurnID.
	if items[0].TurnID != "turn-1" {
		t.Errorf("items[0].TurnID = %q", items[0].TurnID)
	}
}

func TestBuildViewItems_ErrorResult(t *testing.T) {
	msgs := []store.Message{
		msg("tool_call", "Bash", map[string]any{"name": "Bash", "id": "e1", "input": `{}`}),
		msg("tool_result", "permission denied", map[string]any{"tool_use_id": "e1", "is_error": true}),
	}
	items, _, _, _ := buildViewItems(msgs)
	if !items[0].Tool.IsError {
		t.Error("expected IsError=true")
	}
}

func TestToolInputPreview_Bash(t *testing.T) {
	got := toolInputPreview("Bash", `{"command":"git diff --stat HEAD~1"}`)
	want := "git diff --stat HEAD~1"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestToolInputPreview_BashArray(t *testing.T) {
	got := toolInputPreview("Bash", `{"command":["go","test","./..."]}`)
	want := "go test ./..."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestToolInputPreview_Read(t *testing.T) {
	got := toolInputPreview("Read", `{"file_path":"/home/user/project/main.go"}`)
	want := "/home/user/project/main.go"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestToolInputPreview_Unknown(t *testing.T) {
	got := toolInputPreview("WebSearch", `{"query":"golang template"}`)
	if len(got) == 0 {
		t.Error("expected non-empty preview for unknown tool")
	}
}

func TestToolInputPreview_Truncation(t *testing.T) {
	long := `{"command":"` + string(make([]byte, 200)) + `"}`
	got := toolInputPreview("Bash", long)
	// Should end with ellipsis when over 80 runes.
	if len([]rune(got)) > 82 { // 80 chars + ellipsis (…)
		t.Errorf("preview too long: %d chars", len([]rune(got)))
	}
}
