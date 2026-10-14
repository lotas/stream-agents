package store

import (
	"context"
	"sort"
	"time"
)

type Session struct {
	Agent               string
	ID                  string
	Project             string
	Title               string
	Model               string
	Started             time.Time
	HasTokens           bool
	HasCost             bool
	Cost                float64 // estimate at API prices
	Billed              float64 // Cost scaled by the agent's cost multiplier; set by Index.ListAll
	CacheCreationTokens int
	Modified            time.Time
	MessageCount        int
	Duration            time.Duration
	ActivityTimes       []time.Time
	Activity            []Interval
	ActiveDuration      time.Duration
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	Usage               []ModelUsage // per-model token usage, in first-use order
}

// ModelUsage is the usage a session recorded against one provider/model.
type ModelUsage struct {
	Provider string
	Model    string
	TokenUsage
	Tokens  bool // usage carried token counts
	HasCost bool
	Cost    float64
	Billed  float64 // Cost scaled by the agent's cost multiplier; set by Index.ListAll
}

// addUsage accumulates usage for provider/model into the session's per-model
// breakdown. A nil cost means the price is unknown.
func (s *Session) addUsage(provider, model string, usage *TokenUsage, cost *float64) {
	i := 0
	for i < len(s.Usage) && (s.Usage[i].Provider != provider || s.Usage[i].Model != model) {
		i++
	}
	if i == len(s.Usage) {
		s.Usage = append(s.Usage, ModelUsage{Provider: provider, Model: model})
	}
	u := &s.Usage[i]
	if usage != nil {
		u.Tokens = true
		u.InputTokens += usage.InputTokens
		u.OutputTokens += usage.OutputTokens
		u.CacheCreationTokens += usage.CacheCreationTokens
		u.CacheReadTokens += usage.CacheReadTokens
	}
	if cost != nil {
		u.HasCost = true
		u.Cost += *cost
	}
}

type TokenUsage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
}

type Message struct {
	Role  string
	Text  string
	Meta  map[string]any
	Time  time.Time
	Usage *TokenUsage // non-nil on assistant messages that carry usage data
	Cost  *float64    // non-nil when the agent stored an estimated cost for this message
}

type Store interface {
	Agent() string
	ListSessions(ctx context.Context) ([]Session, error)
	LoadSession(ctx context.Context, id string) ([]Message, error)
	FilePath(id string) string
}

func sortSessionsByModified(sessions []Session) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified.After(sessions[j].Modified)
	})
}
