package server

import (
	"net/http"
	"os"

	"stream-agents/internal/limits"
)

// handleLimits serves Claude Code's cached subscription usage limits. It
// never fails: without a readable cache it answers available:false.
func (a *apiHandlers) handleLimits(w http.ResponseWriter, r *http.Request) {
	if !readOnly(w, r) {
		return
	}
	var paths []string
	if a.opts.ClaudeJSONPaths != nil {
		paths = a.opts.ClaudeJSONPaths()
	}
	home, _ := os.UserHomeDir()
	writeJSON(w, limits.Read(paths, a.opts.Now(), a.opts.Host, home))
}
