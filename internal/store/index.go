package store

import (
	"context"
	"fmt"
	"sort"
)

// Index aggregates multiple stores and provides filtered session listing.
type Index struct {
	stores []Store
}

func NewIndex(stores ...Store) *Index {
	return &Index{stores: stores}
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
