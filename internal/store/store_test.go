package store_test

import (
	"testing"
	"time"

	"stream-agents/internal/store"
)

func TestSessionZeroValue(t *testing.T) {
	var s store.Session
	if s.Modified.IsZero() != (s.Modified == time.Time{}) {
		t.Fatal("unexpected zero value behavior")
	}
}
