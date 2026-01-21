package analysis

import (
	"sort"
	"time"
)

// EntityCount represents an entity with its occurrence count.
type EntityCount struct {
	Name  string
	Count int
}

// SortByCount converts a count map to a sorted slice of EntityCount.
// Entities are sorted by count (descending), with alphabetical ordering as tiebreaker.
func SortByCount(counts map[string]int) []EntityCount {
	items := make([]EntityCount, 0, len(counts))
	for name, count := range counts {
		items = append(items, EntityCount{Name: name, Count: count})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Name < items[j].Name
	})

	return items
}

// DurationStats contains statistical information about a set of durations.
type DurationStats struct {
	Count  int
	Min    time.Duration
	Max    time.Duration
	Avg    time.Duration
	Median time.Duration
}

// CalculateDurationStats computes min, max, avg, and median for a set of durations.
func CalculateDurationStats(durations []time.Duration) DurationStats {
	if len(durations) == 0 {
		return DurationStats{}
	}

	min := durations[0]
	max := durations[0]
	var sum time.Duration

	for _, d := range durations {
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
		sum += d
	}

	avg := sum / time.Duration(len(durations))
	median := CalculateMedian(durations)

	return DurationStats{
		Count:  len(durations),
		Min:    min,
		Max:    max,
		Avg:    avg,
		Median: median,
	}
}

// CalculateMedian calculates the median duration from a slice of durations.
func CalculateMedian(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}

	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// CalculateDurationDistribution groups durations into buckets.
func CalculateDurationDistribution(durations []time.Duration) map[string]int {
	dist := map[string]int{
		"< 1s":         0,
		"1s - 1min":    0,
		"1min - 30min": 0,
		"30min - 2h":   0,
		"2h - 5h":      0,
		"> 5h":         0,
	}

	for _, d := range durations {
		switch {
		case d < time.Second:
			dist["< 1s"]++
		case d < time.Minute:
			dist["1s - 1min"]++
		case d < 30*time.Minute:
			dist["1min - 30min"]++
		case d < 2*time.Hour:
			dist["30min - 2h"]++
		case d < 5*time.Hour:
			dist["2h - 5h"]++
		default:
			dist["> 5h"]++
		}
	}

	return dist
}
