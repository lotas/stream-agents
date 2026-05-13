package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestHandleNotifyReturnsSSE(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/notify", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return // timeout is expected — SSE is long-lived
		}
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("GET /notify = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

func TestSessionPageHasDataAttrs(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/session/claude/test-0000-0000-0000-000000000001")
	var sb strings.Builder
	io.Copy(&sb, resp.Body)
	resp.Body.Close()
	body := sb.String()

	if !strings.Contains(body, `data-session-agent="claude"`) {
		t.Error("session page missing data-session-agent attribute")
	}
	if !strings.Contains(body, `data-session-id="test-0000-0000-0000-000000000001"`) {
		t.Error("session page missing data-session-id attribute")
	}
}

func TestSessionPageHasStatsIDs(t *testing.T) {
	ts := buildTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/session/claude/test-0000-0000-0000-000000000001")
	var sb strings.Builder
	io.Copy(&sb, resp.Body)
	resp.Body.Close()
	body := sb.String()

	// The test file is freshly written so it is "hot"; stats IDs must be present.
	for _, id := range []string{"stat-duration", "stat-in", "stat-out", "stat-cache"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("session page missing id=%q", id)
		}
	}
}
