package store

import (
	"testing"
	"time"
)

func TestActivityIntervals(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	times := []time.Time{at(70), at(0), at(15), at(15), {}, at(60)}
	for _, tc := range []struct{ cutoff, want time.Duration }{{15 * time.Minute, 25 * time.Minute}, {10 * time.Minute, 10 * time.Minute}} {
		spans := ActivityIntervals(times, tc.cutoff)
		if got := IntervalDuration(spans); got != tc.want {
			t.Fatalf("cutoff %s: got %s want %s", tc.cutoff, got, tc.want)
		}
	}
	if len(ActivityIntervals([]time.Time{at(0)}, 15*time.Minute)) != 0 {
		t.Fatal("isolated timestamp should not invent duration")
	}
	spans := []Interval{{at(15), at(45)}, {at(0), at(30)}, {at(5), at(10)}, {at(45), at(50)}}
	if got := IntervalDuration(spans); got != 50*time.Minute {
		t.Fatalf("union: %s", got)
	}
	if !spans[0].Start.Equal(at(15)) {
		t.Fatal("input mutated")
	}
}
