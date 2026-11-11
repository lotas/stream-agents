package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"stream-agents/internal/store"
)

// runHook handles `stream-agents hook`: Claude Code runs it as a command
// hook with the event JSON on stdin. It records pending permission prompts
// for /api/v1/sessions and must never affect Claude, so it prints nothing
// on stdout and always exits 0; problems go to stderr, which Claude only
// shows in its debug log.
func runHook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	stateDir := stateDirFlag(fs)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return
	}
	if err := applyHook(os.Stdin, *stateDir, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "stream-agents hook:", err)
	}
}

func applyHook(r io.Reader, stateDir string, now time.Time) error {
	var ev store.HookEvent
	if err := json.NewDecoder(io.LimitReader(r, 16<<20)).Decode(&ev); err != nil {
		return fmt.Errorf("read hook payload: %w", err)
	}
	return store.ApplyHookEvent(stateDir, ev, now)
}

// stateDirFlag registers -state-dir, where the hook records pending prompts
// and the server reads them.
func stateDirFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("STREAM_AGENTS_STATE_DIR")
	if def == "" {
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			def = filepath.Join(xdg, "stream-agents")
		} else if home, err := os.UserHomeDir(); err == nil {
			def = filepath.Join(home, ".local", "state", "stream-agents")
		}
	}
	return fs.String("state-dir", def, "directory for Claude hook state (pending permission prompts); env STREAM_AGENTS_STATE_DIR")
}
