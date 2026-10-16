package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Index aggregates multiple stores and provides filtered session listing.
type Index struct {
	stores     []Store
	IdleCutoff time.Duration
	// CostMultipliers scales each agent's API-price estimate into its billed
	// cost; agents not listed bill at API prices.
	CostMultipliers map[string]float64

	mu sync.Mutex
	// derived caches each session with its activity and billing filled in,
	// keyed by agent and ID, so unchanged sessions skip the recomputation.
	derived map[string]derivedSession
}

// derivedSession is a prepared session and the inputs it was derived from.
type derivedSession struct {
	modified   time.Time
	times      int
	messages   int
	cost       float64
	cutoff     time.Duration
	multiplier float64
	sess       Session
}

func NewIndex(stores ...Store) *Index {
	return &Index{stores: stores, IdleCutoff: 15 * time.Minute, CostMultipliers: DefaultCostMultipliers}
}

// CostMultiplier returns the billed-to-API cost ratio for agent.
func (idx *Index) CostMultiplier(agent string) float64 {
	if m, ok := idx.CostMultipliers[agent]; ok {
		return m
	}
	return 1
}

// ListAll refreshes all stores and returns a combined, sorted session list.
// Pass empty string to skip a filter. The returned slice is the caller's own,
// but its sessions' slices are shared and must not be modified.
func (idx *Index) ListAll(ctx context.Context, agentFilter, projectFilter string) ([]Session, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// A full listing sees every live session, so it rebuilds the cache and
	// drops entries for deleted ones.
	derived := idx.derived
	if agentFilter == "" || derived == nil {
		derived = make(map[string]derivedSession, len(idx.derived))
	}
	var all []Session
	for _, s := range idx.stores {
		if agentFilter != "" && s.Agent() != agentFilter {
			continue
		}
		sessions, err := s.ListSessions(ctx)
		if err != nil {
			return nil, err
		}
		for _, sess := range sessions {
			sess = idx.derive(derived, sess)
			if projectFilter != "" && sess.Project != projectFilter {
				continue
			}
			all = append(all, sess)
		}
	}
	idx.derived = derived
	sort.Slice(all, func(i, j int) bool {
		return all[i].Modified.After(all[j].Modified)
	})
	return all, nil
}

// derive fills in sess's activity and billing, reusing the cached result
// when neither the session nor the settings changed. Callers hold idx.mu.
func (idx *Index) derive(into map[string]derivedSession, sess Session) Session {
	key := sess.Agent + "\x00" + sess.ID
	m := idx.CostMultiplier(sess.Agent)
	if d, ok := idx.derived[key]; ok && d.modified.Equal(sess.Modified) && d.times == len(sess.ActivityTimes) &&
		d.messages == sess.MessageCount && d.cost == sess.Cost && d.cutoff == idx.IdleCutoff && d.multiplier == m {
		into[key] = d
		return d.sess
	}
	d := derivedSession{
		modified: sess.Modified, times: len(sess.ActivityTimes), messages: sess.MessageCount,
		cost: sess.Cost, cutoff: idx.IdleCutoff, multiplier: m,
	}
	if len(sess.ActivityTimes) > 0 {
		sess.Activity = ActivityIntervals(sess.ActivityTimes, idx.IdleCutoff)
		sess.ActiveDuration = IntervalDuration(sess.Activity)
	}
	// Copy Usage so billing never writes into the store's cached session.
	sess.Billed = sess.Cost * m
	sess.Usage = append([]ModelUsage(nil), sess.Usage...)
	for i := range sess.Usage {
		sess.Usage[i].Billed = sess.Usage[i].Cost * m
	}
	d.sess = sess
	into[key] = d
	return sess
}

// LoadSession delegates to the store matching the given agent.
func (idx *Index) LoadSession(ctx context.Context, agent, id string) ([]Message, error) {
	for _, s := range idx.stores {
		if s.Agent() == agent {
			return s.LoadSession(ctx, id)
		}
	}
	return nil, ErrUnknownAgent
}

// FilePath returns the absolute file path for a session ID, searching all stores.
func (idx *Index) FilePath(agent, id string) string {
	for _, s := range idx.stores {
		if s.Agent() == agent {
			return s.FilePath(id)
		}
	}
	return ""
}

// Projects returns the deduplicated, sorted list of known project paths.
func (idx *Index) Projects() []string {
	seen := make(map[string]struct{})
	for _, s := range idx.stores {
		sessions, _ := s.ListSessions(context.Background())
		for _, sess := range sessions {
			if sess.Project != "" {
				seen[sess.Project] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownAgent is returned when no store matches the requested agent name.
var ErrUnknownAgent = fmt.Errorf("unknown agent")
