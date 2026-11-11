package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Windows that decide which sessions are active. Keep llms.txt in sync.
const (
	// ActiveWorkingWindow is how long a working session without a known
	// running process stays active after its last activity; the same window
	// as /hot.json.
	ActiveWorkingWindow = 10 * time.Minute
	// ActiveWaitingWindow is how long a waiting session without a known
	// running process stays active after its turn ended.
	ActiveWaitingWindow = 30 * time.Minute
	// ActiveOpenWindow caps sessions whose agent process is running, as a
	// guard against a reused pid.
	ActiveOpenWindow = 24 * time.Hour
)

// Liveness says whether a session's agent process is still running.
type Liveness int

const (
	LiveUnknown Liveness = iota // the agent offers no way to tell
	LiveOpen                    // a running process has the session open
	LiveClosed                  // the agent tracks processes and none has it open
)

// ActiveSession is a session that is working or waiting for the user.
type ActiveSession struct {
	Session
	State        string // StateWorking or StateWaiting
	Reason       string // ReasonTurnDone or ReasonPermission while waiting
	Since        time.Time
	LastActivity time.Time
}

// Probe is what is known about a session at request time beyond its
// transcript.
type Probe struct {
	Live Liveness
	// Subagent is the newest activity of an unfinished subagent; zero if none.
	Subagent time.Time
	// Permission is when a pending permission or input prompt was reported
	// by the Claude hook; zero if none.
	Permission time.Time
}

// prober is implemented by stores that know more about a session at request
// time than its transcript says: whether its process runs, and the newest
// activity of unfinished subagents.
type prober interface {
	probe(sess Session, now time.Time) (Liveness, time.Time)
}

// ActiveSessions lists the sessions that are working or waiting as of now,
// waiting ones first (longest waiting first), then working ones (most
// recently active first). Subagents and program-driven runs are left out.
func (idx *Index) ActiveSessions(ctx context.Context, now time.Time) ([]ActiveSession, error) {
	sessions, err := idx.ListAll(ctx, "", "")
	if err != nil {
		return nil, err
	}
	probers := map[string]prober{}
	for _, s := range idx.stores {
		if p, ok := s.(prober); ok {
			probers[s.Agent()] = p
		}
	}
	out := []ActiveSession{}
	for _, s := range sessions {
		// ListAll is sorted by Modified desc, and nothing older than the
		// longest window can be active.
		if now.Sub(s.Modified) > ActiveOpenWindow {
			break
		}
		if s.Sidechain || s.Turn.State == "" {
			continue
		}
		pr := Probe{Subagent: s.Turn.SubagentActivity}
		if p := probers[s.Agent]; p != nil {
			var sub time.Time
			pr.Live, sub = p.probe(s, now)
			if sub.After(pr.Subagent) {
				pr.Subagent = sub
			}
		}
		if s.Agent == "claude" {
			if st, ok := idx.hooks.newest(s.ID); ok {
				pr.Permission = st.At
			}
		}
		if a, ok := Classify(s.Turn, pr, now); ok {
			a.Session = s
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.State == StateWaiting) != (b.State == StateWaiting) {
			return a.State == StateWaiting
		}
		if a.State == StateWaiting {
			return a.Since.Before(b.Since)
		}
		return a.LastActivity.After(b.LastActivity)
	})
	return out, nil
}

// Classify applies the active-session rules to a session's transcript turn
// and what was probed about it at request time. It reports false when the
// session is not active.
func Classify(turn TurnState, pr Probe, now time.Time) (ActiveSession, bool) {
	a := ActiveSession{State: turn.State, Reason: turn.Reason, Since: turn.Since, LastActivity: turn.LastActivity}
	if a.State == "" {
		return a, false
	}
	live, subagent := pr.Live, pr.Subagent
	// A prompt reported by the hook counts until the transcript moves past
	// it: an answered or denied prompt is followed by a tool result.
	if !pr.Permission.IsZero() && !pr.Permission.Before(turn.LastActivity) {
		a.State, a.Reason, a.Since = StateWaiting, ReasonPermission, pr.Permission
		if pr.Permission.After(a.LastActivity) {
			a.LastActivity = pr.Permission
		}
		subagent = time.Time{} // the blocked subagent may be the one writing
	}
	// A parent that ended its turn while a background subagent still runs
	// is working.
	if a.Reason == ReasonTurnDone && subagent.After(turn.Since) && now.Sub(subagent) < ActiveWorkingWindow {
		a.State, a.Reason = StateWorking, ""
	}
	if subagent.After(a.LastActivity) {
		a.LastActivity = subagent
	}
	age := now.Sub(a.LastActivity)
	switch live {
	case LiveOpen:
		return a, age < ActiveOpenWindow
	case LiveClosed:
		// Grace for runs the registry does not list (claude -p) and for
		// the moment before a new process registers.
		return a, a.State == StateWorking && age < ActiveWorkingWindow
	}
	if a.State == StateWorking {
		return a, age < ActiveWorkingWindow
	}
	return a, age < ActiveWaitingWindow
}

// probe checks Claude Code's process registry and the session's subagents.
func (s *ClaudeStore) probe(sess Session, now time.Time) (Liveness, time.Time) {
	s.mu.Lock()
	e, ok := s.index[sess.ID]
	s.mu.Unlock()
	if !ok {
		return LiveUnknown, time.Time{}
	}
	projDir := filepath.Dir(e.path)
	// Only terminal sessions are known to register; other front ends may not.
	live := LiveUnknown
	if sess.entrypoint == "cli" {
		live = s.registry.liveness(claudeRegistryDir(projDir), sess.ID, now)
	}
	// Subagents keep a waiting parent working, and their writes count as the
	// parent's activity while it waits on a long foreground subagent.
	sub := claudeSubagentActivity(filepath.Join(projDir, sess.ID, "subagents"), sess.Turn.Since, now)
	return live, sub
}

// claudeSubagentActivity returns the newest mtime of a subagent transcript
// under dir written after since and within the working window, skipping
// subagents whose agent-*.meta.json says "finished": true.
func claudeSubagentActivity(dir string, since, now time.Time) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		m := fi.ModTime()
		if !m.After(since) || now.Sub(m) >= ActiveWorkingWindow || !m.After(newest) {
			continue
		}
		var meta struct {
			Finished bool `json:"finished"`
		}
		if data, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(name, ".jsonl")+".meta.json")); err == nil {
			_ = json.Unmarshal(data, &meta)
		}
		if !meta.Finished {
			newest = m
		}
	}
	return newest
}

// claudeRegistry reads Claude Code's process registry: one <pid>.json per
// running Claude process in <config dir>/sessions, naming the session it has
// open. Files of exited processes may linger, so the pid is checked too.
//
// Each entry records the pid namespace it belongs to ("pidDomain", e.g.
// "linux:<machine-id>:pid:[4026532824]"). A config dir shared with
// containers or VMs holds entries whose pids mean nothing here, and since
// files are named by pid, processes in different namespaces can even
// overwrite each other's entry. So a pid is only checked when its entry is
// in this process's own domain, and a missing entry only means "closed"
// when every entry in the registry is local.
type claudeRegistry struct {
	mu    sync.Mutex
	files map[string]registryFile // by path
	dirs  map[string]registryDir  // by dir, refreshed at most once a second
	// isLocal reports whether a pidDomain is this process's own; nil means
	// localPIDDomain.
	isLocal func(domain string) bool
}

type registryFile struct {
	mtime     time.Time
	pid       int
	sessionID string
	domain    string
}

type registryDir struct {
	read      time.Time
	open      map[string]bool // held by a live local process
	closed    map[string]bool // local entry whose process is gone
	foreign   map[string]bool // entry in another pid namespace: can't tell
	exclusive bool            // has local entries and no foreign ones
}

const registryTTL = time.Second

func (r *claudeRegistry) liveness(dir, id string, now time.Time) Liveness {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.dirs[dir]
	if !ok || now.Sub(d.read) >= registryTTL || now.Before(d.read) {
		d = r.readDir(dir, now)
		if r.dirs == nil {
			r.dirs = map[string]registryDir{}
		}
		r.dirs[dir] = d
	}
	switch {
	case d.open[id]:
		return LiveOpen
	case d.foreign[id]:
		return LiveUnknown
	case d.closed[id], d.exclusive:
		return LiveClosed
	}
	return LiveUnknown
}

func (r *claudeRegistry) readDir(dir string, now time.Time) registryDir {
	d := registryDir{read: now, open: map[string]bool{}, closed: map[string]bool{}, foreign: map[string]bool{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return d
	}
	if r.files == nil {
		r.files = map[string]registryFile{}
	}
	isLocal := r.isLocal
	if isLocal == nil {
		isLocal = localPIDDomain
	}
	local, foreign := 0, 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fi, err := e.Info()
		if err != nil {
			continue
		}
		f, ok := r.files[path]
		if !ok || !f.mtime.Equal(fi.ModTime()) {
			var raw struct {
				PID       int    `json:"pid"`
				SessionID string `json:"sessionId"`
				PIDDomain string `json:"pidDomain"`
			}
			if data, err := os.ReadFile(path); err == nil {
				_ = json.Unmarshal(data, &raw)
			}
			f = registryFile{mtime: fi.ModTime(), pid: raw.PID, sessionID: raw.SessionID, domain: raw.PIDDomain}
			r.files[path] = f
		}
		if f.pid <= 0 || f.sessionID == "" {
			continue
		}
		switch {
		case !isLocal(f.domain):
			foreign++
			d.foreign[f.sessionID] = true
		case pidAlive(f.pid):
			local++
			d.open[f.sessionID] = true
		default:
			local++
			d.closed[f.sessionID] = true
		}
	}
	d.exclusive = local > 0 && foreign == 0
	return d
}

var (
	ownPIDDomainOnce sync.Once
	ownPIDDomain     string // "" when it can't be determined
)

// localPIDDomain reports whether a registry entry's pidDomain is this
// process's pid namespace. Entries without one (older Claude versions) are
// assumed local. On Linux the domain is "linux:<machine-id>:<pid namespace>";
// elsewhere only the platform prefix (Node's process.platform, which matches
// GOOS for darwin and linux) can be compared.
func localPIDDomain(domain string) bool {
	if domain == "" {
		return true
	}
	if runtime.GOOS != "linux" {
		return strings.HasPrefix(domain, runtime.GOOS+":")
	}
	ownPIDDomainOnce.Do(func() {
		id, err1 := os.ReadFile("/etc/machine-id")
		ns, err2 := os.Readlink("/proc/self/ns/pid")
		if err1 == nil && err2 == nil {
			ownPIDDomain = "linux:" + strings.TrimSpace(string(id)) + ":" + ns
		}
	})
	return ownPIDDomain != "" && domain == ownPIDDomain
}

// claudeRegistryDir maps <config>/projects/<project> to <config>/sessions.
func claudeRegistryDir(projDir string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(projDir)), "sessions")
}
