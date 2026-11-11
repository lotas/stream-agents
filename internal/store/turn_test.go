package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"stream-agents/internal/store"
)

var turnBase = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return turnBase.Add(time.Duration(sec) * time.Second) }

// claudeConfig lays out a Claude config dir with one project and returns
// the config dir and the project dir.
func claudeConfig(t *testing.T) (string, string) {
	t.Helper()
	config := t.TempDir()
	proj := filepath.Join(config, "projects", "-Users-test-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	return config, proj
}

// copyFixture copies testdata/turns/<name> to dst with modification time mtime.
func copyFixture(t *testing.T, name, dst string, mtime time.Time) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "turns", name))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, string(data), mtime)
}

func writeFile(t *testing.T, path, data string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func onlySession(t *testing.T, s store.Store) store.Session {
	t.Helper()
	sessions, err := s.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	return sessions[0]
}

func TestClaudeTurnState(t *testing.T) {
	for _, tc := range []struct {
		fixture   string
		state     string
		reason    string
		since     time.Time
		last      time.Time
		sidechain bool
	}{
		// end_turn, then stop hook and turn_duration; bookkeeping after it is ignored.
		{"finished.jsonl", store.StateWaiting, store.ReasonTurnDone, at(10), at(12), false},
		// tool_use without a result yet.
		{"pending-tool.jsonl", store.StateWorking, "", at(0), at(5), false},
		// A new prompt after a finished turn.
		{"new-prompt.jsonl", store.StateWorking, "", at(300), at(300), false},
		// Local commands, compaction and away summaries do not start a turn.
		{"after-finish-noise.jsonl", store.StateWaiting, store.ReasonTurnDone, at(10), at(60), false},
		{"interrupted.jsonl", store.StateWaiting, store.ReasonTurnDone, at(30), at(30), false},
		{"agent-sidechain.jsonl", "", "", time.Time{}, time.Time{}, true},
		{"sdk.jsonl", store.StateWaiting, store.ReasonTurnDone, at(10), at(12), true},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			_, proj := claudeConfig(t)
			copyFixture(t, tc.fixture, filepath.Join(proj, "S.jsonl"), at(20))
			s := onlySession(t, store.NewClaudeStore(filepath.Dir(proj)))
			got := s.Turn
			if got.State != tc.state || got.Reason != tc.reason || !got.Since.Equal(tc.since) || !got.LastActivity.Equal(tc.last) {
				t.Errorf("turn = %s/%s since %v last %v, want %s/%s since %v last %v",
					got.State, got.Reason, got.Since, got.LastActivity, tc.state, tc.reason, tc.since, tc.last)
			}
			if s.Sidechain != tc.sidechain {
				t.Errorf("Sidechain = %v, want %v", s.Sidechain, tc.sidechain)
			}
			if tc.state != "" && got.Model != "claude-fable-5-2" {
				t.Errorf("Model = %q", got.Model)
			}
		})
	}
}

func TestCodexTurnState(t *testing.T) {
	for _, tc := range []struct {
		fixture   string
		state     string
		since     time.Time
		sidechain bool
	}{
		{"codex-finished.jsonl", store.StateWaiting, at(21), false},
		{"codex-working.jsonl", store.StateWorking, at(1), false},
		{"codex-driven.jsonl", store.StateWaiting, at(21), true},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			root := t.TempDir()
			copyFixture(t, tc.fixture, filepath.Join(root, "2026", "11", "11", "rollout-2026-11-11T10-00-00-c1.jsonl"), at(30))
			s := onlySession(t, store.NewCodexStore(root))
			if s.Turn.State != tc.state || !s.Turn.Since.Equal(tc.since) || s.Sidechain != tc.sidechain {
				t.Errorf("turn = %+v sidechain %v, want %s since %v sidechain %v", s.Turn, s.Sidechain, tc.state, tc.since, tc.sidechain)
			}
			if s.Turn.Model != "gpt-6-sol" {
				t.Errorf("Model = %q", s.Turn.Model)
			}
		})
	}
}

func TestOpenCodeTurnState(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ms := func(sec int) int64 { return at(sec).UnixMilli() }
	msg := func(id, session string, sec int, data string) string {
		return fmt.Sprintf(`INSERT INTO message VALUES ('%s', '%s', %d, %d, '%s')`, id, session, ms(sec), ms(sec), data)
	}
	for _, stmt := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT NOT NULL, title TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		fmt.Sprintf(`INSERT INTO session VALUES
			('done', NULL, '/p', 'Done', %[1]d, %[1]d), ('tools', NULL, '/p', 'Tools', %[1]d, %[1]d),
			('parent', NULL, '/p', 'Parent', %[1]d, %[1]d), ('child', 'parent', '/p', 'Child', %[1]d, %[1]d)`, ms(0)),
		msg("d1", "done", 0, `{"role":"user","time":{"created":`+fmt.Sprint(ms(0))+`}}`),
		msg("d2", "done", 1, `{"role":"assistant","finish":"stop","providerID":"p","modelID":"m","time":{"created":`+fmt.Sprint(ms(1))+`,"completed":`+fmt.Sprint(ms(9))+`}}`),
		msg("t1", "tools", 0, `{"role":"user","time":{"created":`+fmt.Sprint(ms(0))+`}}`),
		msg("t2", "tools", 1, `{"role":"assistant","finish":"tool-calls","time":{"created":`+fmt.Sprint(ms(1))+`,"completed":`+fmt.Sprint(ms(4))+`}}`),
		msg("p1", "parent", 0, `{"role":"user","time":{"created":`+fmt.Sprint(ms(0))+`}}`),
		msg("p2", "parent", 1, `{"role":"assistant","finish":"stop","time":{"created":`+fmt.Sprint(ms(1))+`,"completed":`+fmt.Sprint(ms(2))+`}}`),
		msg("c1", "child", 3, `{"role":"assistant","time":{"created":`+fmt.Sprint(ms(3))+`}}`),
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	sessions, err := store.NewOpenCodeStore(root).ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.Session{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	if s := byID["done"]; s.Turn.State != store.StateWaiting || !s.Turn.Since.Equal(at(9)) || s.Turn.Model != "p/m" {
		t.Errorf("done = %+v", s.Turn)
	}
	if s := byID["tools"]; s.Turn.State != store.StateWorking || !s.Turn.LastActivity.Equal(at(4)) {
		t.Errorf("tools = %+v", s.Turn)
	}
	if s := byID["child"]; !s.Sidechain || s.Turn.State != store.StateWorking {
		t.Errorf("child = %+v sidechain %v", s.Turn, s.Sidechain)
	}
	if s := byID["parent"]; s.Sidechain || s.Turn.State != store.StateWaiting || !s.Turn.SubagentActivity.Equal(at(3)) {
		t.Errorf("parent = %+v", s.Turn)
	}
}

func TestClassify(t *testing.T) {
	waiting := store.TurnState{State: store.StateWaiting, Reason: store.ReasonTurnDone, Since: at(0), LastActivity: at(0)}
	working := store.TurnState{State: store.StateWorking, Since: at(0), LastActivity: at(0)}
	min := func(m int) time.Time { return at(m * 60) }
	for _, tc := range []struct {
		name     string
		turn     store.TurnState
		live     store.Liveness
		subagent time.Time
		now      time.Time
		active   bool
		state    string
		reason   string
		// permission is when the hook reported a pending prompt.
		permission time.Time
	}{
		{"waiting, no process info, recent", waiting, store.LiveUnknown, time.Time{}, min(29), true, store.StateWaiting, "", time.Time{}},
		{"waiting, no process info, old", waiting, store.LiveUnknown, time.Time{}, min(31), false, "", "", time.Time{}},
		{"waiting, process open for hours", waiting, store.LiveOpen, time.Time{}, min(5 * 60), true, store.StateWaiting, "", time.Time{}},
		{"waiting, process open past a day", waiting, store.LiveOpen, time.Time{}, min(25 * 60), false, "", "", time.Time{}},
		{"waiting, process closed", waiting, store.LiveClosed, time.Time{}, min(1), false, "", "", time.Time{}},
		{"working, recent", working, store.LiveUnknown, time.Time{}, min(9), true, store.StateWorking, "", time.Time{}},
		{"working, stale", working, store.LiveUnknown, time.Time{}, min(11), false, "", "", time.Time{}},
		{"working, process closed, recent", working, store.LiveClosed, time.Time{}, min(5), true, store.StateWorking, "", time.Time{}},
		{"working, process open, long tool", working, store.LiveOpen, time.Time{}, min(60), true, store.StateWorking, "", time.Time{}},
		{"waiting with running subagent", waiting, store.LiveOpen, min(3), min(5), true, store.StateWorking, "", time.Time{}},
		{"waiting, subagent quiet too long", waiting, store.LiveOpen, min(3), min(14), true, store.StateWaiting, "", time.Time{}},
		{"waiting, subagent before turn end", waiting, store.LiveOpen, at(-10), min(1), true, store.StateWaiting, store.ReasonTurnDone, time.Time{}},
		{"pending tool with permission prompt", working, store.LiveOpen, time.Time{}, min(5), true, store.StateWaiting, store.ReasonPermission, at(2)},
		{"permission prompt in a subagent", working, store.LiveOpen, min(4), min(5), true, store.StateWaiting, store.ReasonPermission, min(4)},
		{"prompt answered: transcript moved on", store.TurnState{State: store.StateWorking, Since: at(0), LastActivity: at(30)}, store.LiveOpen, time.Time{}, min(5), true, store.StateWorking, "", at(2)},
		{"prompt denied: turn ended after it", store.TurnState{State: store.StateWaiting, Reason: store.ReasonTurnDone, Since: at(30), LastActivity: at(30)}, store.LiveOpen, time.Time{}, min(5), true, store.StateWaiting, store.ReasonTurnDone, at(2)},
		{"permission, process closed", working, store.LiveClosed, time.Time{}, min(1), false, "", "", at(2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ok := store.Classify(tc.turn, store.Probe{Live: tc.live, Subagent: tc.subagent, Permission: tc.permission}, tc.now)
			if ok != tc.active || (ok && a.State != tc.state) {
				t.Errorf("Classify = %s %v, want %s %v", a.State, ok, tc.state, tc.active)
			}
			if ok && tc.reason != "" && a.Reason != tc.reason {
				t.Errorf("Reason = %q, want %q", a.Reason, tc.reason)
			}
			if ok && a.Reason == store.ReasonPermission && !a.Since.Equal(tc.permission) {
				t.Errorf("Since = %v, want the prompt time %v", a.Since, tc.permission)
			}
			if ok && a.State == store.StateWorking && a.Reason != "" {
				t.Errorf("working with reason %q", a.Reason)
			}
		})
	}
}

// registerProcess writes a Claude process registry entry.
func registerProcess(t *testing.T, config string, pid int, session string) {
	t.Helper()
	writeFile(t, filepath.Join(config, "sessions", fmt.Sprintf("%d.json", pid)),
		fmt.Sprintf(`{"pid":%d,"sessionId":%q,"status":"idle","kind":"interactive"}`, pid, session), at(0))
}

// deadPID returns a pid with no process.
func deadPID(t *testing.T) int {
	for pid := 99_991; pid > 90_000; pid -= 7 {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return pid
		}
	}
	t.Skip("no free pid")
	return 0
}

func TestActiveSessionsClaude(t *testing.T) {
	now := at(60 * 60) // an hour after the turn ended: past the no-process window
	setup := func(t *testing.T) (string, string) {
		config, proj := claudeConfig(t)
		copyFixture(t, "finished.jsonl", filepath.Join(proj, "S.jsonl"), at(12))
		copyFixture(t, "agent-sidechain.jsonl", filepath.Join(proj, "agent-a1.jsonl"), at(12))
		return config, proj
	}
	active := func(t *testing.T, config string) []store.ActiveSession {
		t.Helper()
		idx := store.NewIndex(store.NewClaudeStore(filepath.Join(config, "projects")))
		a, err := idx.ActiveSessions(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	t.Run("open process keeps it waiting", func(t *testing.T) {
		config, _ := setup(t)
		registerProcess(t, config, os.Getpid(), "S")
		a := active(t, config)
		if len(a) != 1 || a[0].ID != "S" || a[0].State != store.StateWaiting || a[0].Reason != store.ReasonTurnDone {
			t.Fatalf("active = %+v", a)
		}
	})
	t.Run("closed process drops it", func(t *testing.T) {
		config, _ := setup(t)
		registerProcess(t, config, deadPID(t), "S")
		registerProcess(t, config, os.Getpid(), "other")
		if a := active(t, config); len(a) != 0 {
			t.Fatalf("active = %+v, want none", a)
		}
	})
	t.Run("no registry falls back to the waiting window", func(t *testing.T) {
		config, _ := setup(t)
		if a := active(t, config); len(a) != 0 {
			t.Fatalf("active = %+v, want none an hour later", a)
		}
	})
	t.Run("running background subagent means working", func(t *testing.T) {
		config, proj := setup(t)
		registerProcess(t, config, os.Getpid(), "S")
		sub := filepath.Join(proj, "S", "subagents")
		writeFile(t, filepath.Join(sub, "agent-x.jsonl"), "{}\n", now.Add(-time.Minute))
		writeFile(t, filepath.Join(sub, "agent-x.meta.json"), `{"finished":false}`, now.Add(-time.Minute))
		a := active(t, config)
		if len(a) != 1 || a[0].State != store.StateWorking || !a[0].LastActivity.Equal(now.Add(-time.Minute)) {
			t.Fatalf("active = %+v", a)
		}
		writeFile(t, filepath.Join(sub, "agent-x.meta.json"), `{"finished":true}`, now)
		if a := active(t, config); len(a) != 1 || a[0].State != store.StateWaiting {
			t.Fatalf("after finish: active = %+v", a)
		}
	})
}

func TestActiveSessionsOrder(t *testing.T) {
	config, proj := claudeConfig(t)
	copyFixture(t, "finished.jsonl", filepath.Join(proj, "waiting-first.jsonl"), at(12))
	copyFixture(t, "interrupted.jsonl", filepath.Join(proj, "waiting-second.jsonl"), at(30))
	copyFixture(t, "new-prompt.jsonl", filepath.Join(proj, "working-first.jsonl"), at(300))
	copyFixture(t, "pending-tool.jsonl", filepath.Join(proj, "working-second.jsonl"), at(5))
	// Waiting: longest waiting first (10 s, then 30 s). Working: most recent first.
	idx := store.NewIndex(store.NewClaudeStore(filepath.Join(config, "projects")))
	a, err := idx.ActiveSessions(context.Background(), at(400))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range a {
		ids = append(ids, s.ID)
	}
	if got := fmt.Sprint(ids); got != "[waiting-first waiting-second working-first working-second]" {
		t.Errorf("order = %s", got)
	}
}

func TestClaudeTitleSkipsCommandEchoes(t *testing.T) {
	_, proj := claudeConfig(t)
	writeFile(t, filepath.Join(proj, "S.jsonl"), `{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>Caveat: the messages below</local-command-caveat>"},"timestamp":"2026-11-11T10:00:00Z"}
{"type":"user","message":{"role":"user","content":"<command-name>/model</command-name>"},"timestamp":"2026-11-11T10:00:00Z"}
{"type":"user","message":{"role":"user","content":"Add a sessions API"},"timestamp":"2026-11-11T10:00:01Z"}
`, at(1))
	if s := onlySession(t, store.NewClaudeStore(filepath.Dir(proj))); s.Title != "Add a sessions API" {
		t.Errorf("Title = %q", s.Title)
	}

	// Claude Code's own generated title wins.
	copyFixture(t, "finished.jsonl", filepath.Join(proj, "S.jsonl"), at(20))
	if s := onlySession(t, store.NewClaudeStore(filepath.Dir(proj))); s.Title != "Parser fix" {
		t.Errorf("Title = %q", s.Title)
	}
}

func TestActiveSessionsForegroundSubagentKeepsParent(t *testing.T) {
	config, proj := claudeConfig(t)
	copyFixture(t, "pending-tool.jsonl", filepath.Join(proj, "S.jsonl"), at(5))
	now := at(30 * 60) // the parent itself has been silent for half an hour
	writeFile(t, filepath.Join(proj, "S", "subagents", "agent-x.jsonl"), "{}\n", now.Add(-time.Minute))
	idx := store.NewIndex(store.NewClaudeStore(filepath.Join(config, "projects")))
	a, err := idx.ActiveSessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || a[0].State != store.StateWorking || !a[0].LastActivity.Equal(now.Add(-time.Minute)) {
		t.Fatalf("active = %+v", a)
	}
}

// registerForeignProcess writes a registry entry from another pid namespace,
// as Claude running in a container writes into a shared config dir.
func registerForeignProcess(t *testing.T, config string, pid int, session string) {
	t.Helper()
	writeFile(t, filepath.Join(config, "sessions", fmt.Sprintf("%d.json", pid)),
		fmt.Sprintf(`{"pid":%d,"sessionId":%q,"status":"idle","pidDomain":"linux:0000000000000000000000000000beef:pid:[4026599999]"}`, pid, session), at(0))
}

func TestActiveSessionsForeignPIDNamespace(t *testing.T) {
	setup := func(t *testing.T) (string, *store.Index) {
		config, proj := claudeConfig(t)
		copyFixture(t, "finished.jsonl", filepath.Join(proj, "S.jsonl"), at(12)) // turn ended at 10 s
		return config, store.NewIndex(store.NewClaudeStore(filepath.Join(config, "projects")))
	}
	active := func(t *testing.T, idx *store.Index, now time.Time) []store.ActiveSession {
		t.Helper()
		a, err := idx.ActiveSessions(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	t.Run("foreign entry is neither open nor closed", func(t *testing.T) {
		config, idx := setup(t)
		// Its pid happens to be alive here; that must not count as open.
		registerForeignProcess(t, config, os.Getpid(), "S")
		if a := active(t, idx, at(10*60)); len(a) != 1 || a[0].State != store.StateWaiting {
			t.Fatalf("10 min later: %+v, want waiting", a)
		}
		if a := active(t, idx, at(60*60)); len(a) != 0 {
			t.Fatalf("an hour later: %+v, want none (waiting window)", a)
		}
	})
	t.Run("dead pid in a foreign namespace does not close it", func(t *testing.T) {
		config, idx := setup(t)
		registerForeignProcess(t, config, deadPID(t), "S")
		if a := active(t, idx, at(10*60)); len(a) != 1 {
			t.Fatalf("active = %+v, want the session", a)
		}
	})
	t.Run("missing entry with foreign entries around is unknown", func(t *testing.T) {
		config, idx := setup(t)
		registerProcess(t, config, os.Getpid(), "other")
		registerForeignProcess(t, config, 471, "another")
		if a := active(t, idx, at(10*60)); len(a) != 1 {
			t.Fatalf("active = %+v, want the session", a)
		}
	})
	t.Run("missing entry in an all-local registry is closed", func(t *testing.T) {
		config, idx := setup(t)
		registerProcess(t, config, os.Getpid(), "other")
		if a := active(t, idx, at(10*60)); len(a) != 0 {
			t.Fatalf("active = %+v, want none", a)
		}
	})
}
