package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"stream-agents/internal/limits"
)

// runStatusline handles `stream-agents statusline [command...]`, used as
// Claude Code's statusLine command. Claude Code passes the live session
// state, including the subscription rate limits from its latest API
// response, as JSON on stdin; this records the rate limits for
// /api/v1/limits and then runs command (through sh -c, with the same stdin),
// so an existing status line keeps working. Recording never affects the
// status line: its problems go to stderr only.
func runStatusline(args []string) {
	fs := flag.NewFlagSet("statusline", flag.ContinueOnError)
	stateDir := stateDirFlag(fs)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: stream-agents statusline [-state-dir dir] [status line command...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	payload, err := io.ReadAll(io.LimitReader(os.Stdin, 16<<20))
	if err != nil {
		fmt.Fprintln(os.Stderr, "stream-agents statusline: read stdin:", err)
	}
	if *stateDir != "" {
		if _, err := limits.RecordStatusline(payload, filepath.Join(*stateDir, limits.LiveFile), time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "stream-agents statusline:", err)
		}
	}
	os.Exit(runWrapped(fs.Args(), payload))
}

// runWrapped runs the status line command with payload on stdin and returns
// its exit code (0 when there is no command).
func runWrapped(command []string, payload []byte) int {
	if len(command) == 0 {
		return 0
	}
	cmd := exec.Command("/bin/sh", "-c", strings.Join(command, " "))
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "stream-agents statusline:", err)
		return 1
	}
	return 0
}
