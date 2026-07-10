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
//
// Per-event dimension indices (db/user/app/host) live in dimChunk
// stored separately so the four parallel slices can be promoted from
// uint8 to uint16 in lockstep when a dictionary exceeds 255 entries.
//
// tsNanos holds the event's WALL-CLOCK-AS-UTC nanoseconds — the true
// instant shifted by the log line's own UTC offset at ingest — so a
// mixed-timezone corpus (a DST crossing, or a multi-file set mixing e.g.
// CEST and UTC lines) reconstructs each event's own local wall-clock by
// rendering it in UTC, with no per-event offset stored (zero extra bytes,
// mirroring the events analyzer). The trade-off: ordering across events is
// by wall-clock rather than by absolute instant, which is deterministic and
// matches the displayed strings.
type execChunk struct {
	tsNanos    []int64
	durations  []float64
	queryIDIdx []uint32
}

// dimChunk holds the per-event dimension indices for one execChunk.
// Each dimension is independently either a []uint8 (most common; 255
// distinct values is far above what real logs show for db/user/app/
// host) or a []uint16 after promotion. Index 0 is reserved for "value
// not present on the entry's log prefix"; real dictionary entries
// start at index 1.
type dimChunk struct {
	dbU8, userU8, appU8, hostU8     []uint8
	dbU16, userU16, appU16, hostU16 []uint16
}

// compactExecutions stores query execution events in fixed-size chunks
// of three parallel slices instead of one monolithic slice of struct,
// plus a deduplicated string table for query IDs. The per-event
// footprint is 20 B (tsNanos 8 + duration 8 + queryIDIdx 4) — the
// wall-clock-as-UTC timestamp packing carries the timezone for free — but
// allocation never doubles in place, eliminating the transient peak that
// doubled the live set during the last few growths on multi-million-event
// corpora.
//
// On the Z corpus (40 M executions) the steady-state heap is unchanged
// (~760 MB across ~610 chunks) but the run-time peak drops by ~800 MB
// since no realloc-and-copy ever happens.
//
// Dimension dictionaries (databases/users/apps/hosts) sit alongside.
// Per-event indices add 4 B/event in the steady-state uint8 case
// (16 MB on 4 M executions) and up to 8 B/event after any dictionary
// crosses 255 entries.
type compactExecutions struct {
	chunks []execChunk
	dims   []dimChunk
	n      int // total event count across all chunks

	// needsSort is set when events from several PID shards were folded in
	// (their per-shard runs are each in stream order but the concatenation is
	// not), so the JSON dump must sort before emitting to stay deterministic.
	// A single-pass / function-parallel run leaves it false — events are
	// already in stream order and stream straight out, with no materialization
	// (important for the leaking-GC WASM build).
	needsSort bool

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

	// Dimension dictionaries. Index 0 of each slice is reserved for
	// "unknown" (empty string). New entries are appended as encountered;
	// the corresponding *Index map handles O(1) lookup during build.
	databases []string
	users     []string
	apps      []string
	hosts     []string

	databasesIndex map[string]uint16
	usersIndex     map[string]uint16
	appsIndex      map[string]uint16
	hostsIndex     map[string]uint16

	// dbWide/userWide/appWide/hostWide flip to true once the
	// corresponding dictionary crosses 255 entries and we promote the
	// per-event slices to uint16.
	dbWide, userWide, appWide, hostWide bool
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
		chunks:         make([]execChunk, 0, chunkHint),
		dims:           make([]dimChunk, 0, chunkHint),
		queryIDs:       make([]string, 0, 1024),
		queryIDIndex:   make(map[string]uint32, 1024),
		databases:      []string{""}, // index 0 = unknown
		users:          []string{""},
		apps:           []string{""},
		hosts:          []string{""},
		databasesIndex: make(map[string]uint16, 16),
		usersIndex:     make(map[string]uint16, 16),
		appsIndex:      make(map[string]uint16, 16),
		hostsIndex:     make(map[string]uint16, 16),
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

// newDimChunk allocates a fresh dimension page. Each dimension starts
// in uint8 mode; promotion to uint16 is per-dimension and back-fills
// existing chunks lazily on demand.
func newDimChunk(dbWide, userWide, appWide, hostWide bool) dimChunk {
	var dc dimChunk
	if dbWide {
		dc.dbU16 = make([]uint16, 0, execChunkSize)
	} else {
		dc.dbU8 = make([]uint8, 0, execChunkSize)
	}
	if userWide {
		dc.userU16 = make([]uint16, 0, execChunkSize)
	} else {
		dc.userU8 = make([]uint8, 0, execChunkSize)
	}
	if appWide {
		dc.appU16 = make([]uint16, 0, execChunkSize)
	} else {
		dc.appU8 = make([]uint8, 0, execChunkSize)
	}
	if hostWide {
		dc.hostU16 = make([]uint16, 0, execChunkSize)
	} else {
		dc.hostU8 = make([]uint8, 0, execChunkSize)
	}
	return dc
}

// internDim looks up name in the given dictionary (index/table), adding
// it when absent. Returns the index. An empty name maps to index 0
// (reserved "unknown"). When the dictionary grows past 255 entries it
// signals promotion via *needsWiden; the caller is responsible for
// walking the chunks to copy uint8→uint16 before appending the new
// index.
func internDim(table *[]string, index map[string]uint16, name string, wide bool, needsWiden *bool) uint16 {
	if name == "" {
		return 0
	}
	if idx, ok := index[name]; ok {
		return idx
	}
	idx := uint16(len(*table))
	*table = append(*table, name)
	index[name] = idx
	// 256 entries (indices 0..255) still fit in uint8. Promotion kicks
	// in at the 257th entry (index 256), which is the first value that
	// would not survive the uint8 cast.
	if !wide && idx == 256 {
		*needsWiden = true
	}
	return idx
}

// widenDim promotes every chunk's per-dimension slice from uint8 to
// uint16 for the dimension selected by which. Called when the
// corresponding dictionary just crossed 255 entries. Allocates one
// new []uint16 per chunk and copies the existing values; the old
// []uint8 backing arrays become eligible for GC.
func (c *compactExecutions) widenDim(which int) {
	for i := range c.dims {
		dc := &c.dims[i]
		switch which {
		case 0:
			out := make([]uint16, len(dc.dbU8), execChunkSize)
			for j, v := range dc.dbU8 {
				out[j] = uint16(v)
			}
			dc.dbU16 = out
			dc.dbU8 = nil
		case 1:
			out := make([]uint16, len(dc.userU8), execChunkSize)
			for j, v := range dc.userU8 {
				out[j] = uint16(v)
			}
			dc.userU16 = out
			dc.userU8 = nil
		case 2:
			out := make([]uint16, len(dc.appU8), execChunkSize)
			for j, v := range dc.appU8 {
				out[j] = uint16(v)
			}
			dc.appU16 = out
			dc.appU8 = nil
		case 3:
			out := make([]uint16, len(dc.hostU8), execChunkSize)
			for j, v := range dc.hostU8 {
				out[j] = uint16(v)
			}
			dc.hostU16 = out
			dc.hostU8 = nil
		}
	}
}

// append records one execution event. queryID is interned via the
// internal table so identical IDs across executions share a single
// string allocation. The timestamp is stored as wall-clock-as-UTC
// nanoseconds (the true instant shifted by ts's own UTC offset) so a
// mixed-timezone corpus round-trips each event's local wall-clock by
// rendering in UTC, with no per-event offset stored. A row round-tripped
// through the sharded Merge arrives as a UTC time (offset 0), so this
// shift is applied exactly once — see sql_merge.go. A new chunk is
// allocated when the current one fills.
func (c *compactExecutions) append(ts time.Time, duration float64, queryID string, database, user, app, host string) {
	idx, ok := c.queryIDIndex[queryID]
	if !ok {
		idx = uint32(len(c.queryIDs))
		c.queryIDs = append(c.queryIDs, queryID)
		c.queryIDIndex[queryID] = idx
	}

	// Intern the four dimensions; widen the per-event slices in lockstep
	// when a dictionary crosses 255 entries.
	var widenDb, widenUser, widenApp, widenHost bool
	dbIdx := internDim(&c.databases, c.databasesIndex, database, c.dbWide, &widenDb)
	userIdx := internDim(&c.users, c.usersIndex, user, c.userWide, &widenUser)
	appIdx := internDim(&c.apps, c.appsIndex, app, c.appWide, &widenApp)
	hostIdx := internDim(&c.hosts, c.hostsIndex, host, c.hostWide, &widenHost)
	if widenDb {
		c.widenDim(0)
		c.dbWide = true
	}
	if widenUser {
		c.widenDim(1)
		c.userWide = true
	}
	if widenApp {
		c.widenDim(2)
		c.appWide = true
	}
	if widenHost {
		c.widenDim(3)
		c.hostWide = true
	}

	if len(c.chunks) == 0 || len(c.chunks[len(c.chunks)-1].tsNanos) == execChunkSize {
		c.chunks = append(c.chunks, newChunk())
		c.dims = append(c.dims, newDimChunk(c.dbWide, c.userWide, c.appWide, c.hostWide))
	}
	last := &c.chunks[len(c.chunks)-1]
	_, offSec := ts.Zone()
	last.tsNanos = append(last.tsNanos, ts.UnixNano()+int64(offSec)*int64(time.Second))
	last.durations = append(last.durations, duration)
	last.queryIDIdx = append(last.queryIDIdx, idx)

	dc := &c.dims[len(c.dims)-1]
	if c.dbWide {
		dc.dbU16 = append(dc.dbU16, dbIdx)
	} else {
		dc.dbU8 = append(dc.dbU8, uint8(dbIdx))
	}
	if c.userWide {
		dc.userU16 = append(dc.userU16, userIdx)
	} else {
		dc.userU8 = append(dc.userU8, uint8(userIdx))
	}
	if c.appWide {
		dc.appU16 = append(dc.appU16, appIdx)
	} else {
		dc.appU8 = append(dc.appU8, uint8(appIdx))
	}
	if c.hostWide {
		dc.hostU16 = append(dc.hostU16, hostIdx)
	} else {
		dc.hostU8 = append(dc.hostU8, uint8(hostIdx))
	}
	c.n++
}

// dimsAt returns the four dimension indices for event i. Used by the
// At/ForEach helpers when expanding events back to QueryExecution.
func (c *compactExecutions) dimsAt(ci, j int) (db, user, app, host uint16) {
	dc := &c.dims[ci]
	if c.dbWide {
		db = dc.dbU16[j]
	} else {
		db = uint16(dc.dbU8[j])
	}
	if c.userWide {
		user = dc.userU16[j]
	} else {
		user = uint16(dc.userU8[j])
	}
	if c.appWide {
		app = dc.appU16[j]
	} else {
		app = uint16(dc.appU8[j])
	}
	if c.hostWide {
		host = dc.hostU16[j]
	} else {
		host = uint16(dc.hostU8[j])
	}
	return
}

// freeIndex releases the queryIDIndex map after the parser-side build.
// The table itself (queryIDs slice) is kept for output decoding.
// Saves a few MB on corpora with thousands of unique queries.
func (c *compactExecutions) freeIndex() {
	c.queryIDIndex = nil
	c.databasesIndex = nil
	c.usersIndex = nil
	c.appsIndex = nil
	c.hostsIndex = nil
}

// Len returns the number of stored execution events.
func (c *compactExecutions) Len() int {
	return c.n
}

// At expands the i-th event into a full QueryExecution. tsNanos already
// holds the wall-clock-as-UTC instant, so rendering it in UTC prints the
// event's own local wall-clock even on mixed-timezone input.
func (c *compactExecutions) At(i int) QueryExecution {
	ci := i >> 16
	j := i & (execChunkSize - 1)
	ch := &c.chunks[ci]
	db, user, app, host := c.dimsAt(ci, j)
	return QueryExecution{
		Timestamp: time.Unix(0, ch.tsNanos[j]).UTC(),
		Duration:  ch.durations[j],
		QueryID:   c.queryIDs[ch.queryIDIdx[j]],
		Database:  c.databases[db],
		User:      c.users[user],
		App:       c.apps[app],
		Host:      c.hosts[host],
	}
}

// ForEach iterates every execution event in append order. The callback
// receives a freshly constructed QueryExecution; returning false stops
// iteration early. No intermediate []QueryExecution slice is built.
func (c *compactExecutions) ForEach(fn func(QueryExecution) bool) {
	for ci := range c.chunks {
		ch := &c.chunks[ci]
		for j := range ch.tsNanos {
			db, user, app, host := c.dimsAt(ci, j)
			if !fn(QueryExecution{
				Timestamp: time.Unix(0, ch.tsNanos[j]).UTC(),
				Duration:  ch.durations[j],
				QueryID:   c.queryIDs[ch.queryIDIdx[j]],
				Database:  c.databases[db],
				User:      c.users[user],
				App:       c.apps[app],
				Host:      c.hosts[host],
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
	for ci := range c.chunks {
		ch := &c.chunks[ci]
		for j := range ch.tsNanos {
			if ch.queryIDIdx[j] != idx {
				continue
			}
			db, user, app, host := c.dimsAt(ci, j)
			if !fn(QueryExecution{
				Timestamp: time.Unix(0, ch.tsNanos[j]).UTC(),
				Duration:  ch.durations[j],
				QueryID:   id,
				Database:  c.databases[db],
				User:      c.users[user],
				App:       c.apps[app],
				Host:      c.hosts[host],
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
