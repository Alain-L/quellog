// Package analysis provides analysis types and functions for PostgreSQL log analysis.
package analysis

import (
	"time"
)

// execChunkSize is the fixed event count per chunk. Powers of two let
// the divisor/modulo collapse to a shift+mask. 65 536 events per chunk
// = ~1.25 MB (8 + 8 + 4 bytes per event), small enough to be friendly
// to the allocator and large enough that per-chunk header overhead
// stays negligible (24 B × 3 slice headers per chunk).
const execChunkSize = 1 << 16

// execChunk holds one fixed-size page of execution events. The slices
// are allocated once at full capacity, then filled by append until they
// reach execChunkSize, at which point a new chunk is allocated. Pages
// are never reallocated, so the previous geometric-growth spike (the
// final realloc on a 40 M-event slice was peaking at ~1.6 GB live: old
// + new during the copy) is replaced by a single ~1.25 MB allocation
// per page boundary.
type execChunk struct {
	tsNanos    []int64
	durations  []float64
	queryIDIdx []uint32
}

// compactExecutions stores query execution events in fixed-size chunks
// of three parallel slices instead of one monolithic slice of struct,
// plus a deduplicated string table for query IDs. The per-event
// footprint is 20 B (tsNanos 8 + duration 8 + queryIDIdx 4) — same
// as the previous monolithic layout — but allocation never doubles in
// place, eliminating the transient peak that doubled the live set
// during the last few growths on multi-million-event corpora.
//
// On the Z corpus (40 M executions) the steady-state heap is unchanged
// (~760 MB across ~610 chunks) but the run-time peak drops by ~800 MB
// since no realloc-and-copy ever happens.
type compactExecutions struct {
	chunks []execChunk
	n      int // total event count across all chunks

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

// newCompactExecutions returns a compactExecutions sized for the
// expected event count. The hint is used to pre-allocate the chunks
// header slice so the chunk list itself never reallocates on big
// corpora; the chunks themselves are allocated lazily on first event.
func newCompactExecutions(execCap int) *compactExecutions {
	if execCap <= 0 {
		execCap = 10000
	}
	chunkHint := (execCap + execChunkSize - 1) / execChunkSize
	if chunkHint < 1 {
		chunkHint = 1
	}
	return &compactExecutions{
		chunks:       make([]execChunk, 0, chunkHint),
		queryIDs:     make([]string, 0, 1024),
		queryIDIndex: make(map[string]uint32, 1024),
	}
}

// newChunk allocates a fresh page at full capacity. Called when the
// last chunk is full (or no chunk exists yet).
func newChunk() execChunk {
	return execChunk{
		tsNanos:    make([]int64, 0, execChunkSize),
		durations:  make([]float64, 0, execChunkSize),
		queryIDIdx: make([]uint32, 0, execChunkSize),
	}
}

// append records one execution event. queryID is interned via the
// internal table so identical IDs across executions share a single
// string allocation. The location of the first event is remembered
// for round-trip reconstruction (postgres logs typically use one tz
// throughout). A new chunk is allocated when the current one fills.
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
	if len(c.chunks) == 0 || len(c.chunks[len(c.chunks)-1].tsNanos) == execChunkSize {
		c.chunks = append(c.chunks, newChunk())
	}
	last := &c.chunks[len(c.chunks)-1]
	last.tsNanos = append(last.tsNanos, ts.UnixNano())
	last.durations = append(last.durations, duration)
	last.queryIDIdx = append(last.queryIDIdx, idx)
	c.n++
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
	return c.n
}

// At expands the i-th event into a full QueryExecution. The returned
// time.Time uses the captured location so the wall clock is preserved
// across the round-trip.
func (c *compactExecutions) At(i int) QueryExecution {
	ch := &c.chunks[i>>16]
	j := i & (execChunkSize - 1)
	return QueryExecution{
		Timestamp: time.Unix(0, ch.tsNanos[j]).In(c.location()),
		Duration:  ch.durations[j],
		QueryID:   c.queryIDs[ch.queryIDIdx[j]],
	}
}

// ForEach iterates every execution event in append order. The callback
// receives a freshly constructed QueryExecution; returning false stops
// iteration early. No intermediate []QueryExecution slice is built.
func (c *compactExecutions) ForEach(fn func(QueryExecution) bool) {
	loc := c.location()
	for ci := range c.chunks {
		ch := &c.chunks[ci]
		for j := range ch.tsNanos {
			if !fn(QueryExecution{
				Timestamp: time.Unix(0, ch.tsNanos[j]).In(loc),
				Duration:  ch.durations[j],
				QueryID:   c.queryIDs[ch.queryIDIdx[j]],
			}) {
				return
			}
		}
	}
}

// ForEachID is like ForEach but only yields events whose queryIDIdx
// matches the given index. Used by IterateExecutionsForID after the
// caller has resolved the public string id to its compact index.
func (c *compactExecutions) ForEachID(idx uint32, id string, fn func(QueryExecution) bool) {
	loc := c.location()
	for ci := range c.chunks {
		ch := &c.chunks[ci]
		for j := range ch.tsNanos {
			if ch.queryIDIdx[j] != idx {
				continue
			}
			if !fn(QueryExecution{
				Timestamp: time.Unix(0, ch.tsNanos[j]).In(loc),
				Duration:  ch.durations[j],
				QueryID:   id,
			}) {
				return
			}
		}
	}
}

// Durations returns a fresh []float64 of every event's duration in
// append order. Used by percentile / median computations that only
// need durations and not the full event tuple.
func (c *compactExecutions) Durations() []float64 {
	out := make([]float64, 0, c.n)
	for ci := range c.chunks {
		out = append(out, c.chunks[ci].durations...)
	}
	return out
}

// CountAbove returns the number of events with Duration >= threshold.
// Cheap O(N) iteration over the durations slices, no struct expansion.
func (c *compactExecutions) CountAbove(threshold float64) int {
	n := 0
	for ci := range c.chunks {
		for _, d := range c.chunks[ci].durations {
			if d >= threshold {
				n++
			}
		}
	}
	return n
}
