package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestLLMsTxtMentionsEveryRoute(t *testing.T) {
	doc := string(llmsTxt)
	for _, rt := range routes(nil, Options{}) {
		if rt.pattern == "/" || rt.pattern == "/api/" {
			continue // catch-alls
		}
		if !strings.Contains(doc, rt.pattern) {
			t.Errorf("llms.txt does not mention route %s", rt.pattern)
		}
	}
}

func TestLLMsTxtServedToRemotePeers(t *testing.T) {
	h := apiMux(Options{})
	for _, path := range []string{"/llms.txt", "/llm.txt"} {
		w := apiGet(h, path, "192.168.2.50:5555")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: content type %q", path, ct)
		}
		if !strings.HasPrefix(w.Body.String(), "# stream-agents") {
			t.Errorf("%s: body starts %q", path, w.Body.String()[:min(40, w.Body.Len())])
		}
	}
	if w := apiGet(h, "/stats", "192.168.2.50:5555"); w.Code != http.StatusForbidden {
		t.Errorf("remote /stats: status %d, want 403", w.Code)
	}
}
