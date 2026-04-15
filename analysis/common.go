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

// StreamingDurationStats accumulates duration statistics in O(1) memory.
// Uses the P² algorithm (Jain & Chlamtac, 1985) for streaming median estimation.
type StreamingDurationStats struct {
	Count int
	Sum   time.Duration
	min   time.Duration
	max   time.Duration

	// P² algorithm state for median estimation
	// 5 markers: q[0]=min, q[2]=median estimate, q[4]=max
	q  [5]float64 // marker heights (values)
	n  [5]int     // marker positions (actual)
	np [5]float64 // desired marker positions
	dn [5]float64 // desired position increments
	initialized bool
	initBuf     [5]float64 // buffer for first 5 observations
	initCount   int
}

// Add records a new duration.
func (s *StreamingDurationStats) Add(d time.Duration) {
	s.Count++
	s.Sum += d
	if s.Count == 1 || d < s.min {
		s.min = d
	}
	if d > s.max {
		s.max = d
	}

	v := float64(d)

	// P² requires 5 initial observations to bootstrap
	if !s.initialized {
		s.initBuf[s.initCount] = v
		s.initCount++
		if s.initCount == 5 {
			s.initP2()
		}
		return
	}

	s.updateP2(v)
}

// initP2 bootstraps the P² algorithm with the first 5 observations.
func (s *StreamingDurationStats) initP2() {
	// Sort the initial 5 values
	buf := s.initBuf
	for i := 1; i < 5; i++ {
		for j := i; j > 0 && buf[j] < buf[j-1]; j-- {
			buf[j], buf[j-1] = buf[j-1], buf[j]
		}
	}
	for i := 0; i < 5; i++ {
		s.q[i] = buf[i]
		s.n[i] = i + 1
	}
	// Desired positions for median (p=0.5): 1, 1+2p, 1+4p, 3+2p, 5 = 1, 2, 3, 4, 5
	s.np = [5]float64{1, 2, 3, 4, 5}
	// Increments: 0, p/2, p, (1+p)/2, 1 = 0, 0.25, 0.5, 0.75, 1
	s.dn = [5]float64{0, 0.25, 0.5, 0.75, 1}
	s.initialized = true
}

// updateP2 processes a new observation using the P² algorithm.
func (s *StreamingDurationStats) updateP2(v float64) {
	// Step 1: find cell k such that q[k] <= v < q[k+1]
	var k int
	if v < s.q[0] {
		s.q[0] = v
		k = 0
	} else if v >= s.q[4] {
		s.q[4] = v
		k = 3
	} else {
		for i := 1; i < 5; i++ {
			if v < s.q[i] {
				k = i - 1
				break
			}
		}
	}

	// Step 2: increment positions of markers k+1 through 4
	for i := k + 1; i < 5; i++ {
		s.n[i]++
	}

	// Update desired positions
	for i := 0; i < 5; i++ {
		s.np[i] += s.dn[i]
	}

	// Step 3: adjust marker heights using P² formula
	for i := 1; i < 4; i++ {
		d := s.np[i] - float64(s.n[i])
		if (d >= 1 && s.n[i+1]-s.n[i] > 1) || (d <= -1 && s.n[i-1]-s.n[i] < -1) {
			sign := 1
			if d < 0 {
				sign = -1
			}
			// Try parabolic interpolation
			qi := s.q[i] + float64(sign)/(float64(s.n[i+1]-s.n[i-1]))*
				(float64(s.n[i]-s.n[i-1]+sign)*(s.q[i+1]-s.q[i])/float64(s.n[i+1]-s.n[i])+
					float64(s.n[i+1]-s.n[i]-sign)*(s.q[i]-s.q[i-1])/float64(s.n[i]-s.n[i-1]))

			if qi > s.q[i-1] && qi < s.q[i+1] {
				s.q[i] = qi
			} else {
				// Fall back to linear interpolation
				s.q[i] += float64(sign) * (s.q[i+sign] - s.q[i]) / float64(s.n[i+sign]-s.n[i])
			}
			s.n[i] += sign
		}
	}
}

// Stats returns the computed statistics.
func (s *StreamingDurationStats) Stats() DurationStats {
	if s.Count == 0 {
		return DurationStats{}
	}

	var median time.Duration
	if s.initialized {
		median = time.Duration(s.q[2])
	} else {
		// Less than 5 observations: compute exact median from buffer
		buf := make([]time.Duration, s.initCount)
		for i := 0; i < s.initCount; i++ {
			buf[i] = time.Duration(s.initBuf[i])
		}
		median = CalculateMedian(buf)
	}

	return DurationStats{
		Count:  s.Count,
		Min:    s.min,
		Max:    s.max,
		Avg:    s.Sum / time.Duration(s.Count),
		Median: median,
	}
}

// Cumulated returns the total accumulated duration.
func (s *StreamingDurationStats) Cumulated() time.Duration {
	return s.Sum
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
