package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stream-agents/internal/store"
)

func TestSnippetEscapesAndHighlights(t *testing.T) {
	text := "run <script> then Deploy the app"
	got := string(snippet(text, strings.ToLower(text), []string{"deploy", "script"}))
	want := "run &lt;<mark>script</mark>&gt; then <mark>Deploy</mark> the app"
	if got != want {
		t.Errorf("snippet = %q, want %q", got, want)
	}
}

func TestSnippetTrimsLongText(t *testing.T) {
	text := strings.Repeat("a ", 200) + "needle" + strings.Repeat(" b", 200)
	got := string(snippet(text, strings.ToLower(text), []string{"needle"}))
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || !strings.Contains(got, "<mark>needle</mark>") {
		t.Errorf("unexpected snippet %q", got)
	}
}

func TestMatchSessionRequiresAllTerms(t *testing.T) {
	sess := store.Session{Title: "fix login"}
	msgs := []store.Message{
		{Role: "user", Text: "the button is broken"},
		{Role: "tool_call", Meta: map[string]any{"input": `{"command":"grep oauth"}`}},
	}
	if res, ok := matchSession(sess, msgs, []string{"login", "oauth"}); !ok || res.Hits != 1 {
		t.Errorf("expected match across title and tool input, got ok=%v hits=%d", ok, res.Hits)
	}
	if _, ok := matchSession(sess, msgs, []string{"login", "missing"}); ok {
		t.Error("expected no match when a term is absent")
	}
}

func TestHandleSearch(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-test-myproject")
	os.Mkdir(proj, 0o755)
	src, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "claude", "session.jsonl"))
	os.WriteFile(filepath.Join(proj, "test-0000-0000-0000-000000000001.jsonl"), src, 0o644)
	ts := httptest.NewServer(NewMux(store.NewIndex(store.NewClaudeStore(root))))
	defer ts.Close()

	get := func(q string) string {
		resp, err := http.Get(ts.URL + "/search?q=" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET /search?q=%s = %d", q, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	body := get("HELLO+file1")
	if !strings.Contains(body, `href="/session/claude/test-0000-0000-0000-000000000001"`) {
		t.Error("expected link to matching session")
	}
	if !strings.Contains(body, "<mark>hello</mark>") {
		t.Error("expected highlighted snippet")
	}
	if !strings.Contains(body, `value="HELLO file1"`) {
		t.Error("expected header search to keep the query")
	}
	if body := get("nomatchzzz"); strings.Contains(body, "/session/claude/") {
		t.Error("expected no results")
	}
	if body := get(""); !strings.Contains(body, `id="header-search"`) {
		t.Error("expected search page without query to render")
	}
}
