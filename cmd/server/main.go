package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"stream-agents/internal/server"
	"stream-agents/internal/store"
	"stream-agents/internal/tray"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "stats" {
		runStats(os.Args[2:])
		return
	}

	addr := flag.String("addr", "127.0.0.1:7777", "listen address")
	newIndex := storeFlags(flag.CommandLine)
	trayMode := flag.Bool("tray", false, "show today's usage in the menu bar / system tray")
	idleCutoff := flag.Duration("idle-cutoff", 15*time.Minute, "maximum gap between events counted as active time")
	flag.Parse()
	if *idleCutoff <= 0 {
		log.Fatal("idle-cutoff must be positive")
	}

	idx := newIndex()
	idx.IdleCutoff = *idleCutoff
	mux := server.NewMux(idx)

	fmt.Printf("stream-agents listening on http://%s\n", *addr)
	if !*trayMode {
		log.Fatal(http.ListenAndServe(*addr, mux))
	}
	go func() { log.Fatal(http.ListenAndServe(*addr, mux)) }()
	tray.Run(idx, "http://"+browseHost(*addr))
}

// storeFlags registers the transcript location flags on fs and returns a
// constructor for an index over them, to call after parsing.
func storeFlags(fs *flag.FlagSet) func() *store.Index {
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	claudeDir := fs.String("claude-dir", filepath.Join(home, ".claude", "projects"), "Claude projects directory (~/.claude/projects)")
	claudeConfigDir := fs.String("claude-config-dir", filepath.Join(home, ".config", "claude", "projects"), "Claude projects directory (~/.config/claude/projects)")
	codexDir := fs.String("codex-dir", filepath.Join(home, ".codex", "sessions"), "Codex sessions directory")
	opencodeDir := fs.String("opencode-dir", filepath.Join(dataHome, "opencode"), "OpenCode data directory (containing opencode.db)")
	return func() *store.Index {
		return store.NewIndex(
			store.NewClaudeStore(*claudeDir, *claudeConfigDir),
			store.NewCodexStore(*codexDir),
			store.NewOpenCodeStore(*opencodeDir),
		)
	}
}

// browseHost turns a listen address into one a browser can open, mapping
// wildcard hosts to localhost.
func browseHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
