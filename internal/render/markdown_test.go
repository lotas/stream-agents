package render_test

import (
	"html/template"
	"strings"
	"testing"

	"stream-agents/internal/render"
)

func TestRenderMarkdownBasic(t *testing.T) {
	out := render.RenderMarkdown("**bold** and `code`")
	if !strings.Contains(string(out), "<strong>bold</strong>") {
		t.Errorf("expected <strong>: %q", out)
	}
	if !strings.Contains(string(out), "<code>code</code>") {
		t.Errorf("expected <code>: %q", out)
	}
}

func TestRenderMarkdownFencedCode(t *testing.T) {
	src := "```go\nfmt.Println(\"hi\")\n```"
	out := render.RenderMarkdown(src)
	if !strings.Contains(string(out), "<code") {
		t.Errorf("expected code block: %q", out)
	}
}

func TestRenderMarkdownXSSSafe(t *testing.T) {
	// Inline HTML should be escaped, not rendered
	out := render.RenderMarkdown("<script>alert(1)</script>")
	html := string(out)
	if strings.Contains(html, "<script>") {
		t.Errorf("XSS: raw <script> tag in output: %q", html)
	}
}

func TestRenderMarkdownReturnsTemplateHTML(t *testing.T) {
	out := render.RenderMarkdown("hello")
	var _ template.HTML = out // compile-time type check
}
