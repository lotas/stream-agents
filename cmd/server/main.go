package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"stream-agents/internal/server"
	"stream-agents/internal/store"
)

func main() {
	home, _ := os.UserHomeDir()

	addr := flag.String("addr", "127.0.0.1:7777", "listen address")
	claudeDir := flag.String("claude-dir", filepath.Join(home, ".claude", "projects"), "Claude projects directory (~/.claude/projects)")
	claudeConfigDir := flag.String("claude-config-dir", filepath.Join(home, ".config", "claude", "projects"), "Claude projects directory (~/.config/claude/projects)")
	codexDir := flag.String("codex-dir", filepath.Join(home, ".codex", "sessions"), "Codex sessions directory")
	flag.Parse()

	idx := store.NewIndex(
		store.NewClaudeStore(*claudeDir, *claudeConfigDir),
		store.NewCodexStore(*codexDir),
	)

	mux := server.NewMux(idx)

	fmt.Printf("stream-agents listening on http://%s\n", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
