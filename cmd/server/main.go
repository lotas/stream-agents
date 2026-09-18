package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"stream-agents/internal/server"
	"stream-agents/internal/store"
)

func main() {
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}

	addr := flag.String("addr", "127.0.0.1:7777", "listen address")
	claudeDir := flag.String("claude-dir", filepath.Join(home, ".claude", "projects"), "Claude projects directory (~/.claude/projects)")
	claudeConfigDir := flag.String("claude-config-dir", filepath.Join(home, ".config", "claude", "projects"), "Claude projects directory (~/.config/claude/projects)")
	codexDir := flag.String("codex-dir", filepath.Join(home, ".codex", "sessions"), "Codex sessions directory")
	opencodeDir := flag.String("opencode-dir", filepath.Join(dataHome, "opencode"), "OpenCode data directory (containing opencode.db)")
	idleCutoff := flag.Duration("idle-cutoff", 15*time.Minute, "maximum gap between events counted as active time")
	flag.Parse()
	if *idleCutoff <= 0 {
		log.Fatal("idle-cutoff must be positive")
	}

	idx := store.NewIndex(
		store.NewClaudeStore(*claudeDir, *claudeConfigDir),
		store.NewCodexStore(*codexDir),
		store.NewOpenCodeStore(*opencodeDir),
	)

	idx.IdleCutoff = *idleCutoff
	mux := server.NewMux(idx)

	fmt.Printf("stream-agents listening on http://%s\n", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
