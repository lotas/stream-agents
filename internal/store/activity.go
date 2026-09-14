package store

import (
	"sort"
	"time"
)

// Interval is a half-open span of estimated transcript activity.
type Interval struct{ Start, End time.Time }

// ActivityIntervals excludes gaps longer than cutoff. Isolated events have no
// measurable duration; no time is invented before the first or after the last event.
func ActivityIntervals(timestamps []time.Time, cutoff time.Duration) []Interval {
	times := make([]time.Time, 0, len(timestamps))
	for _, t := range timestamps {
		if !t.IsZero() {
			times = append(times, t)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	var spans []Interval
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		if gap > 0 && gap <= cutoff {
			spans = append(spans, Interval{times[i-1], times[i]})
		}
	}
	return MergeIntervals(spans)
}

// MergeIntervals returns a sorted union without modifying the input.
func MergeIntervals(spans []Interval) []Interval {
	sorted := append([]Interval(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	var merged []Interval
	for _, span := range sorted {
		if span.Start.IsZero() || !span.End.After(span.Start) {
			continue
		}
		if len(merged) == 0 || span.Start.After(merged[len(merged)-1].End) {
			merged = append(merged, span)
		} else if span.End.After(merged[len(merged)-1].End) {
			merged[len(merged)-1].End = span.End
		}
	}
	return merged
}

func IntervalDuration(spans []Interval) time.Duration {
	var d time.Duration
	for _, span := range MergeIntervals(spans) {
		d += span.End.Sub(span.Start)
	}
	return d
}
