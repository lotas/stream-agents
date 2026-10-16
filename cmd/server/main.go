package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
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

	var listen string
	flag.StringVar(&listen, "listen", defaultListen, "comma-separated listen addresses, e.g. 0.0.0.0:7777 (all interfaces) or 127.0.0.1:7777,100.64.0.1:7777")
	flag.StringVar(&listen, "addr", defaultListen, "deprecated alias for -listen")
	allowRemoteUI := flag.Bool("allow-remote-ui", false, "serve the HTML pages and session data to non-loopback peers, not just /api/v1/")
	newIndex := storeFlags(flag.CommandLine)
	trayMode := flag.Bool("tray", false, "show today's usage in the menu bar / system tray")
	idleCutoff := flag.Duration("idle-cutoff", 15*time.Minute, "maximum gap between events counted as active time")
	flag.Parse()
	if *idleCutoff <= 0 {
		log.Fatal("idle-cutoff must be positive")
	}
	cfg, err := loadServeConfig(listen)
	if err != nil {
		log.Fatal(err)
	}

	idx := newIndex()
	idx.IdleCutoff = *idleCutoff
	host, _ := os.Hostname()
	mux := server.NewMux(idx, server.Options{
		AllowRemoteUI: *allowRemoteUI,
		Version:       buildVersion(),
		Host:          host,
	})

	// Bind every address before serving any, so a bad one fails at startup.
	var listeners []net.Listener
	for _, addr := range cfg.addrs {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatal(err)
		}
		listeners = append(listeners, l)
		fmt.Printf("stream-agents listening on http://%s\n", addr)
	}
	if cfg.remote {
		ui := "UI loopback-only"
		if *allowRemoteUI {
			ui = "UI open to remote peers"
		}
		fmt.Printf("network access enabled: /api/v1/ open to the network, %s\n", ui)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	for _, l := range listeners[1:] {
		go func() { log.Fatal(srv.Serve(l)) }()
	}
	if !*trayMode {
		log.Fatal(srv.Serve(listeners[0]))
	}
	go func() { log.Fatal(srv.Serve(listeners[0])) }()
	tray.Run(idx, "http://"+browseHost(cfg.browseAddr()))
}

const defaultListen = "127.0.0.1:7777"

type serveConfig struct {
	addrs  []string
	remote bool // some address is not loopback
}

// browseAddr prefers a loopback address, which serves the full UI.
func (c serveConfig) browseAddr() string {
	for _, addr := range c.addrs {
		if host, _, err := net.SplitHostPort(addr); err == nil && isLoopbackHost(host) {
			return addr
		}
	}
	return c.addrs[0]
}

// loadServeConfig parses the -listen list.
func loadServeConfig(listen string) (serveConfig, error) {
	var cfg serveConfig
	for _, addr := range strings.Split(listen, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return cfg, fmt.Errorf("invalid -listen address %q: use host:port", addr)
		}
		cfg.addrs = append(cfg.addrs, addr)
		cfg.remote = cfg.remote || !isLoopbackHost(host)
	}
	if len(cfg.addrs) == 0 {
		return cfg, fmt.Errorf("-listen needs at least one address")
	}
	return cfg, nil
}

// isLoopbackHost reports whether host only accepts local connections.
// Wildcards ("", 0.0.0.0, ::) and other names count as non-loopback.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// version is set with -ldflags "-X main.version=..."; otherwise it comes
// from the module version or VCS revision embedded at build time.
var version string

func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && len(s.Value) >= 12:
			rev = s.Value[:12]
		case s.Key == "vcs.modified" && s.Value == "true":
			dirty = "-dirty"
		}
	}
	if rev == "" {
		return "dev"
	}
	return rev + dirty
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
	costMultiplier := fs.String("cost-multiplier", os.Getenv("STREAM_AGENTS_COST_MULTIPLIER"),
		"billed-to-API cost ratios as agent=factor,... overriding the defaults ("+store.FormatCostMultipliers(store.DefaultCostMultipliers)+"); env STREAM_AGENTS_COST_MULTIPLIER")
	return func() *store.Index {
		multipliers, err := store.ParseCostMultipliers(*costMultiplier)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		idx := store.NewIndex(
			store.NewClaudeStore(*claudeDir, *claudeConfigDir),
			store.NewCodexStore(*codexDir),
			store.NewOpenCodeStore(*opencodeDir),
		)
		idx.CostMultipliers = multipliers
		return idx
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
