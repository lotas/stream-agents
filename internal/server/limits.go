package server

import (
	"net/http"
	"os"

	"stream-agents/internal/limits"
)

// handleLimits serves Claude Code's cached subscription usage limits. It
// never fails: without a readable cache or status line report it answers available:false.
func (a *apiHandlers) handleLimits(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	var src limits.Sources
	if a.opts.LimitsSources != nil {
		src = a.opts.LimitsSources()
	}
	home, _ := os.UserHomeDir()
	writeJSON(w, limits.Read(src, a.opts.Now(), a.opts.Host, home))
}
