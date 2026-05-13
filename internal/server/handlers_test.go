package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stream-agents/internal/server"
	"stream-agents/internal/store"
)

func buildTestServer(t *testing.T) *httptest.Server {
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

	idx := store.NewIndex(
		store.NewClaudeStore(claudeRoot),
		store.NewCodexStore(codexRoot),
	)
	return httptest.NewServer(server.NewMux(idx))
}

func TestHandleListReturns200(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("GET / = %d", resp.StatusCode)
	}
}

func TestHandleListContainsBothAgents(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/")
	buf := new(strings.Builder)
	io.Copy(buf, resp.Body)
	resp.Body.Close()
	body := buf.String()
	if !strings.Contains(body, "claude") {
		t.Error("list page missing 'claude'")
	}
	if !strings.Contains(body, "codex") {
		t.Error("list page missing 'codex'")
	}
}

func TestHandleSessionReturns200(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/session/claude/test-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("GET /session/claude/... = %d", resp.StatusCode)
	}
}

func TestHandleSessionContainsMessageText(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/session/claude/test-0000-0000-0000-000000000001")
	var sb strings.Builder
	io.Copy(&sb, resp.Body)
	resp.Body.Close()
	if !strings.Contains(sb.String(), "hello world") {
		t.Error("session page missing user message text")
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/session/claude/../../etc/passwd")
	if resp.StatusCode != 404 {
		t.Errorf("path traversal returned %d, want 404", resp.StatusCode)
	}
}

func TestUnknownAgentReturns404(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/session/unknown/some-id")
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
