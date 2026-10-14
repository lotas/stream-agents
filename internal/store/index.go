package store

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Index aggregates multiple stores and provides filtered session listing.
type Index struct {
	stores     []Store
	IdleCutoff time.Duration
	// CostMultipliers scales each agent's API-price estimate into its billed
	// cost; agents not listed bill at API prices.
	CostMultipliers map[string]float64
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
// Pass empty string to skip a filter.
func (idx *Index) ListAll(ctx context.Context, agentFilter, projectFilter string) ([]Session, error) {
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
			if projectFilter != "" && sess.Project != projectFilter {
				continue
			}
			if len(sess.ActivityTimes) > 0 {
				sess.Activity = ActivityIntervals(sess.ActivityTimes, idx.IdleCutoff)
				sess.ActiveDuration = IntervalDuration(sess.Activity)
			}
			// Copy Usage so billing never writes into the store's cached session.
			m := idx.CostMultiplier(sess.Agent)
			sess.Billed = sess.Cost * m
			sess.Usage = append([]ModelUsage(nil), sess.Usage...)
			for i := range sess.Usage {
				sess.Usage[i].Billed = sess.Usage[i].Cost * m
			}
			all = append(all, sess)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Modified.After(all[j].Modified)
	})
	return all, nil
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
