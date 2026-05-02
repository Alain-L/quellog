// Package analysis provides analysis types and functions for PostgreSQL log analysis.
package analysis

import (
	"time"
)

// compactExecutions stores query execution events in three parallel
// slices instead of one slice of struct, plus a deduplicated string
// table for query IDs. This shrinks the per-event footprint from 48 B
// (Timestamp 24 + Duration 8 + QueryID string header 16) to 20 B
// (tsNanos 8 + duration 8 + queryIDIdx 4).
//
// On the Z corpus (40 M executions) this reclaims ~1.1 GB of heap
// versus the previous []QueryExecution slice. The QueryExecution type
// itself is preserved for the public iteration API — callers see one
// QueryExecution at a time, never an in-memory slice of them.
type compactExecutions struct {
	tsNanos    []int64   // Unix nanos of each event timestamp
	durations  []float64 // execution duration in milliseconds
	queryIDIdx []uint32  // index into queryIDs for the event's query ID

	// queryIDs is the deduplicated table of query IDs. Each unique ID
	// is stored once — typical postgres logs have hundreds to thousands
	// of unique queries vs millions of executions, so per-event indexing
	// vs per-event string headers is a 12 B/event saving on top of the
	// timestamp packing.
	queryIDs []string
	// queryIDIndex maps id → its position in queryIDs. Populated as
	// new IDs arrive in append; cleared after the parser-side build to
	// release the map memory before downstream consumers see the slice.
	queryIDIndex map[string]uint32

	// loc is the time.Location captured from the FIRST event appended
	// (postgres logs are usually all in one timezone). Reused when
	// expanding events back to QueryExecution so callers see the same
	// wall-clock display they would have gotten from the original
	// time.Time. Falls back to time.UTC when nothing was appended.
	loc *time.Location
}

// newCompactExecutions returns a compactExecutions with capacity
// pre-sized from the parser estimate. cap of 0 falls back to default.
func newCompactExecutions(execCap int) *compactExecutions {
	if execCap <= 0 {
		execCap = 10000
	}
	return &compactExecutions{
		tsNanos:      make([]int64, 0, execCap),
		durations:    make([]float64, 0, execCap),
		queryIDIdx:   make([]uint32, 0, execCap),
		queryIDs:     make([]string, 0, 1024),
		queryIDIndex: make(map[string]uint32, 1024),
	}
}

// append records one execution event. queryID is interned via the
// internal table so identical IDs across executions share a single
// string allocation. The location of the first event is remembered
// for round-trip reconstruction (postgres logs typically use one tz
// throughout).
func (c *compactExecutions) append(ts time.Time, duration float64, queryID string) {
	if c.loc == nil {
		c.loc = ts.Location()
	}
	idx, ok := c.queryIDIndex[queryID]
	if !ok {
		idx = uint32(len(c.queryIDs))
		c.queryIDs = append(c.queryIDs, queryID)
		c.queryIDIndex[queryID] = idx
	}
	c.tsNanos = append(c.tsNanos, ts.UnixNano())
	c.durations = append(c.durations, duration)
	c.queryIDIdx = append(c.queryIDIdx, idx)
}

// location returns the captured timezone or UTC when no events were
// appended.
func (c *compactExecutions) location() *time.Location {
	if c.loc == nil {
		return time.UTC
	}
	return c.loc
}

// freeIndex releases the queryIDIndex map after the parser-side build.
// The table itself (queryIDs slice) is kept for output decoding.
// Saves a few MB on corpora with thousands of unique queries.
func (c *compactExecutions) freeIndex() {
	c.queryIDIndex = nil
}

// Len returns the number of stored execution events.
func (c *compactExecutions) Len() int {
	return len(c.tsNanos)
}

// At expands the i-th event into a full QueryExecution. The returned
// time.Time uses time.Local — postgres timestamps are stored as Unix
// nanos so the wall clock is preserved across the round-trip; only the
// location pointer is set to Local for display.
func (c *compactExecutions) At(i int) QueryExecution {
	return QueryExecution{
		Timestamp: time.Unix(0, c.tsNanos[i]).In(c.location()),
		Duration:  c.durations[i],
		QueryID:   c.queryIDs[c.queryIDIdx[i]],
	}
}

// ForEach iterates every execution event in append order. The callback
// receives a freshly constructed QueryExecution; returning false stops
// iteration early. No intermediate []QueryExecution slice is built.
func (c *compactExecutions) ForEach(fn func(QueryExecution) bool) {
	for i := range c.tsNanos {
		if !fn(QueryExecution{
			Timestamp: time.Unix(0, c.tsNanos[i]).In(c.location()),
			Duration:  c.durations[i],
			QueryID:   c.queryIDs[c.queryIDIdx[i]],
		}) {
			return
		}
	}
}

// Durations returns a fresh []float64 of every event's duration in
// append order. Used by percentile / median computations that only
// need durations and not the full event tuple. Allocates one slice of
// 8 × Len() bytes — versus the previous code that copied 48 × Len() B
// to extract durations.
func (c *compactExecutions) Durations() []float64 {
	out := make([]float64, len(c.durations))
	copy(out, c.durations)
	return out
}

// CountAbove returns the number of events with Duration >= threshold.
// Cheap O(N) iteration over the durations slice, no struct expansion.
func (c *compactExecutions) CountAbove(threshold float64) int {
	n := 0
	for _, d := range c.durations {
		if d >= threshold {
			n++
		}
	}
	return n
}
