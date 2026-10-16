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
	h := &handlers{idx: idx}
	api := &apiHandlers{idx: idx, opts: opts}

	sub, _ := fs.Sub(assetsFS, "assets")
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/session/{agent}/{id}", h.handleSession)
	mux.HandleFunc("/stream/{agent}/{id}", h.handleStream)
	mux.HandleFunc("/notify", h.handleNotify)
	mux.HandleFunc("/hot.json", h.handleHotJSON)
	mux.HandleFunc("/api/summary.json", h.handleSummaryJSON)
	mux.HandleFunc("/api/v1/health", api.handleHealth)
	mux.HandleFunc("/api/v1/usage", api.handleUsage)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/stats", h.handleStats)
	mux.HandleFunc("/search", h.handleSearch)
	mux.HandleFunc("/", h.handleList)
	return guard(mux, opts)
}
