// Package analysis — time-split aggregation.
//
// AggregateMetricsBySplit partitions the entry stream into fixed wall-clock
// intervals and aggregates each independently, so one multi-file run can emit
// a per-period report. Each period reuses the regular StreamingAnalyzer, so a
// period's metrics are identical to what a single run scoped to that window
// (--begin/--end) would produce.
package analysis

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// maxSplitBuckets caps the number of periods so a too-fine interval over a long
// range cannot spawn an unbounded number of analyzers (and an unusable
// selector). One year of daily buckets, or a day of ~5-minute buckets, fit.
const maxSplitBuckets = 400

// SplitBucket is one time period and its aggregated metrics.
type SplitBucket struct {
	Label   string
	Start   time.Time
	End     time.Time
	Metrics AggregatedMetrics
}

// bucketStartFor floors t to the start of its interval, aligned to the
// timestamp's own wall clock (so 24h -> local midnight, 3h -> 00/03/06…,
// 5m -> :00/:05/:10), independent of the machine timezone.
func bucketStartFor(t time.Time, interval time.Duration) time.Time {
	_, off := t.Zone()
	o := time.Duration(off) * time.Second
	return t.Add(o).Truncate(interval).Add(-o)
}

// splitLabel renders a period's display label: a date for >= 1 day intervals,
// otherwise date + time-of-day.
func splitLabel(start time.Time, interval time.Duration) string {
	if interval >= 24*time.Hour {
		return start.Format("2006-01-02")
	}
	return start.Format("2006-01-02 15:04")
}

// AggregateMetricsBySplit consumes the stream and returns one SplitBucket per
// interval that carries data, sorted chronologically. Entries with a zero
// timestamp are skipped (they cannot be placed on the timeline). It errors if
// the interval would produce more than maxSplitBuckets periods.
func AggregateMetricsBySplit(ctx context.Context, in <-chan []parser.LogEntry, interval time.Duration) ([]SplitBucket, error) {
	if interval <= 0 {
		return nil, fmt.Errorf("split interval must be positive")
	}

	analyzers := make(map[int64]*StreamingAnalyzer)
	starts := make(map[int64]time.Time)
	// finalizeAll drains the analyzers' goroutines; always called so a split
	// run never leaks the per-period fan-out goroutines.
	finalizeAll := func() map[int64]AggregatedMetrics {
		out := make(map[int64]AggregatedMetrics, len(analyzers))
		for k, a := range analyzers {
			out[k] = a.Finalize()
		}
		return out
	}

	overflow := false
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case batch, ok := <-in:
			if !ok {
				break loop
			}
			groups := make(map[int64][]parser.LogEntry)
			for i := range batch {
				e := &batch[i]
				if e.Timestamp.IsZero() {
					continue
				}
				bs := bucketStartFor(e.Timestamp, interval)
				k := bs.Unix()
				if _, seen := analyzers[k]; !seen {
					if len(analyzers) >= maxSplitBuckets {
						overflow = true
						break
					}
					analyzers[k] = NewStreamingAnalyzer()
					starts[k] = bs
				}
				groups[k] = append(groups[k], *e)
			}
			for k, g := range groups {
				if a := analyzers[k]; a != nil {
					a.ProcessBatch(g)
				}
			}
			// The incoming batch was copied into per-bucket group slices (each
			// now owned by its analyzer), so recycle the original to the pool.
			parser.PutBatch(batch)
			if overflow {
				break loop
			}
		}
	}

	// Drain the input so upstream producers can exit cleanly, then finalize
	// every analyzer (mandatory: it stops their goroutines).
	if overflow || ctx.Err() != nil {
		for range in {
		}
	}
	finals := finalizeAll()

	if overflow {
		return nil, fmt.Errorf("--split interval too fine: more than %d periods; use a coarser interval", maxSplitBuckets)
	}

	keys := make([]int64, 0, len(finals))
	for k := range finals {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	buckets := make([]SplitBucket, 0, len(keys))
	for _, k := range keys {
		s := starts[k]
		buckets = append(buckets, SplitBucket{
			Label:   splitLabel(s, interval),
			Start:   s,
			End:     s.Add(interval),
			Metrics: finals[k],
		})
	}
	return buckets, nil
}
