package server

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"time"

	"stream-agents/internal/store"
)

//go:embed templates
var tmplFS embed.FS

//go:embed assets
var assetsFS embed.FS

// llmsTxt describes the service for LLM agents (llmstxt.org). Keep it in the
// same change as any route or response change; TestLLMsTxtMentionsEveryRoute
// fails when a route is missing from it.
//
//go:embed llms.txt
var llmsTxt []byte

// publicPaths are reachable by non-loopback peers besides /api/v1/.
var publicPaths = map[string]bool{"/llms.txt": true, "/llm.txt": true}

// Options configure NewMux. With the zero value, loopback peers reach
// everything and other peers nothing.
type Options struct {
	// AllowRemoteUI serves the HTML pages, streams and session JSON to
	// non-loopback peers. Otherwise they only reach /api/v1/.
	AllowRemoteUI bool
	// Version and Host are reported by /api/v1/health.
	Version, Host string
	// Now and Location set the clock and the time zone of /api/v1/usage days;
	// they default to time.Now and time.Local.
	Now      func() time.Time
	Location *time.Location
	// Logf logs one line per /api/ request; it defaults to log.Printf.
	Logf func(format string, args ...any)
}

func NewMux(idx *store.Index, opts Options) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	mux := http.NewServeMux()
	for _, rt := range routes(idx, opts) {
		mux.Handle(rt.pattern, rt.handler)
	}
	return guard(mux, opts)
}

type route struct {
	pattern string
	handler http.Handler
}

func routes(idx *store.Index, opts Options) []route {
	h := &handlers{idx: idx}
	api := &apiHandlers{idx: idx, opts: opts}
	sub, _ := fs.Sub(assetsFS, "assets")
	f := func(pattern string, fn http.HandlerFunc) route { return route{pattern, fn} }
	return []route{
		{"/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub)))},
		f("/session/{agent}/{id}", h.handleSession),
		f("/stream/{agent}/{id}", h.handleStream),
		f("/notify", h.handleNotify),
		f("/hot.json", h.handleHotJSON),
		f("/api/summary.json", h.handleSummaryJSON),
		f("/api/v1/health", api.handleHealth),
		f("/api/v1/usage", api.handleUsage),
		f("/api/", http.NotFound),
		f("/llms.txt", handleLLMsTxt),
		f("/llm.txt", handleLLMsTxt),
		f("/stats", h.handleStats),
		f("/search", h.handleSearch),
		f("/", h.handleList),
	}
}

func handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(llmsTxt)
}
