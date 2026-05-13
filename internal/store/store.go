package store

import (
	"context"
	"sort"
	"time"
)

type Session struct {
	Agent        string
	ID           string
	Project      string
	Title        string
	Modified     time.Time
	MessageCount int
}

type Message struct {
	Role string
	Text string
	Meta map[string]any
	Time time.Time
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
