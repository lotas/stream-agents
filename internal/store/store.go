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
	// Turn is where the latest turn stands, read from the transcript.
	Turn TurnState
	// Sidechain marks transcripts that are not sessions of their own:
	// subagents, and agent runs driven by another program (codex exec, Codex
	// started by Claude Code). They never count as active sessions.
	Sidechain bool
	// Parent is the ID of the session (same agent) that spawned this one, when
	// the transcript names it: old-layout Claude subagents, Codex
	// thread_spawn subagents and OpenCode child sessions. Claude subagents in
	// <session>/subagents/ are not sessions of their own; their usage is
	// already part of the parent's.
	Parent string
	// entrypoint is how a Claude session was started ("cli", "sdk-cli", ...).
	entrypoint string
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

// CostPartial reports whether u carried tokens that no known price covers.
func (u ModelUsage) CostPartial() bool { return u.Tokens && !u.HasCost && u.Total() > 0 }

// addSessionUsage adds other's usage into s: totals and per-model breakdown.
func (s *Session) addSessionUsage(other Session) {
	s.HasTokens = s.HasTokens || other.HasTokens
	s.InputTokens += other.InputTokens
	s.OutputTokens += other.OutputTokens
	s.CacheReadTokens += other.CacheReadTokens
	s.CacheCreationTokens += other.CacheCreationTokens
	if other.HasCost {
		s.HasCost = true
		s.Cost += other.Cost
	}
	for _, u := range other.Usage {
		var usage *TokenUsage
		if u.Tokens {
			usage = &u.TokenUsage
		}
		var cost *float64
		if u.HasCost {
			cost = &u.Cost
		}
		s.addUsage(u.Provider, u.Model, usage, cost)
	}
}

type TokenUsage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
}

// Total is the sum of all token kinds.
func (u TokenUsage) Total() int {
	return u.InputTokens + u.OutputTokens + u.CacheCreationTokens + u.CacheReadTokens
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

// maxTranscriptLine bounds one JSONL record. Records with inlined images or
// large tool output pass 1 MB, and a scanner stops at the first record over
// its limit, which would hide everything after it.
const maxTranscriptLine = 64 << 20
