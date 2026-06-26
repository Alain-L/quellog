package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several ConnectionAnalyzers, Merge recombines their partial
// states so a single Finalize reproduces the single-pass result.
//
// Invariants this relies on:
//   - Each shard processes its entries in stream = chronological order. So a
//     shard's flattened received-timestamp list is ascending, and its
//     completed-session list is ascending by *end* timestamp (every session is
//     appended at its disconnection entry, whose timestamp is the end). Merge
//     preserves global order by MERGE-SORTING the two ascending sequences on
//     their time key — never concatenating.
//   - activeConnections is keyed by backend PID, and shardForPID routes all
//     entries of a given PID to the same shard. The per-shard in-flight maps
//     are therefore PID-disjoint, so src's still-open connections can simply be
//     copied into dst. Finalize then flushes the union as orphan sessions
//     (sorted by received time, exactly as the single pass does).
//
// The peak (PeakConcurrentSessions / PeakConcurrentTimestamp) is NOT stored as
// per-shard analyzer state: it is recomputed in Finalize by a sweep-line over
// the complete merged sessionChunks (plus the flushed orphans). Because Merge
// unions every session into shard 0 before Finalize runs, that sweep-line sees
// the full global session set and yields the true global peak. The peak is thus
// correctly reproduced by the sharded path — see the note in the parity test.
//
// Accumulated float / duration sums (totalSessionTime and the StreamingDuration
// Stats sums) are summed; float addition is not associative, so the merged
// result may differ from the single pass in the last ULPs — expected and
// tolerated by the parity test.
//
// NOT shard-mergeable — the P² MEDIAN estimate. Each StreamingDurationStats
// keeps a P² (Jain & Chlamtac) streaming-quantile sketch whose 5 marker heights
// are a function of the *order and full history* of the values that fed it.
// Like any streaming-quantile sketch, two partial sketches cannot be combined
// into the sketch the single pass would have produced — the per-shard subsets
// are disjoint value populations and their P² states are not additive. Merge
// therefore combines Count/Sum/min/max EXACTLY (so Count, Min, Max and Avg stay
// bit-/sum-exact) and folds the P² markers only best-effort, leaving the merged
// median an approximation that can diverge materially from the single pass when
// the shards are unbalanced. The Median must be computed outside the per-shard
// workers (e.g. on the dispatcher's calling goroutine over the full stream, or
// from a mergeable sketch such as t-digest/KLL) or accepted as divergent. The
// parity test asserts Count/Min/Max/Avg exactly and EXCLUDES Median for this
// reason.
func (a *ConnectionAnalyzer) Merge(src *ConnectionAnalyzer) {
	if src == nil {
		return
	}

	// Plain counters: SUM.
	a.connectionReceivedCount += src.connectionReceivedCount
	a.disconnectionCount += src.disconnectionCount

	// Accumulated duration sum: SUM.
	a.totalSessionTime += src.totalSessionTime

	// Carry over the timezone if dst never observed an event.
	if a.loc == nil {
		a.loc = src.loc
	}

	// Latest observed timestamp: take the later one (Finalize uses it as the
	// EndTime for orphan sessions).
	if src.lastSeenTimestamp.After(a.lastSeenTimestamp) {
		a.lastSeenTimestamp = src.lastSeenTimestamp
	}

	// Ordered per-occurrence series. Each shard is ascending; merge-sort to
	// preserve global chronological order rather than concatenating.
	a.receivedChunks = connMergeReceivedChunks(a.receivedChunks, src.receivedChunks)
	a.sessionChunks = connMergeSessionChunks(a.sessionChunks, src.sessionChunks)

	// Session-duration distribution: union keys, sum counts.
	for bucket, c := range src.sessionDistribution {
		a.sessionDistribution[bucket] += c
	}

	// Global streaming stats: merge exactly on Count/Sum/min/max.
	connMergeDurationStats(&a.globalSessionStats, &src.globalSessionStats)

	// Per-dimension streaming stats: union keys, fold shared, deep-copy
	// src-only so the two analyzers never alias.
	connMergeStatsMap(a.sessionsByUser, src.sessionsByUser)
	connMergeStatsMap(a.sessionsByDatabase, src.sessionsByDatabase)
	connMergeStatsMap(a.sessionsByHost, src.sessionsByHost)

	// In-flight connections are PID-disjoint across shards: copy src's into
	// dst. Finalize flushes the union as orphan sessions.
	for pid, t := range src.activeConnections {
		a.activeConnections[pid] = t
	}
}

// connMergeStatsMap folds src's per-dimension streaming stats into dst,
// deep-copying src-only entries so the two analyzers never share a pointer.
func connMergeStatsMap(dst, src map[string]*StreamingDurationStats) {
	for k, s := range src {
		d, ok := dst[k]
		if !ok {
			cp := *s
			dst[k] = &cp
			continue
		}
		connMergeDurationStats(d, s)
	}
}

// connMergeDurationStats folds src into dst. Count, Sum, min and max are
// combined exactly. The P² median markers are history-dependent and cannot be
// reconstructed exactly from two partial states, so they are merged best-effort:
//   - If both sides are still in the pre-bootstrap buffer (Count < 5 combined),
//     the raw observations are concatenated and the median stays exact.
//   - Otherwise dst keeps whichever side is already P²-initialized (or its own
//     buffer), and the merged median becomes an estimate. Both single-pass and
//     merged values are already documented approximations, so the small
//     divergence is acceptable and tolerated by the parity test.
func connMergeDurationStats(dst, src *StreamingDurationStats) {
	if src.Count == 0 {
		return
	}
	if dst.Count == 0 {
		*dst = *src
		return
	}

	// Exact aggregates first.
	combinedMin := dst.min
	if src.min < combinedMin {
		combinedMin = src.min
	}
	combinedMax := dst.max
	if src.max > combinedMax {
		combinedMax = src.max
	}

	// Fast path: neither side bootstrapped P² yet, so every observation is
	// still in initBuf. Concatenate the raw values — the median stays exact —
	// and re-bootstrap if the combined count reaches the 5-sample threshold.
	if !dst.initialized && !src.initialized && dst.initCount+src.initCount <= 5 {
		for i := 0; i < src.initCount; i++ {
			dst.initBuf[dst.initCount] = src.initBuf[i]
			dst.initCount++
		}
		dst.Count += src.Count
		dst.Sum += src.Sum
		dst.min = combinedMin
		dst.max = combinedMax
		if dst.initCount == 5 {
			dst.initP2()
		}
		return
	}

	// General path: keep dst's median markers (or src's if only src is
	// initialized) and combine the exact aggregates. The median becomes an
	// estimate.
	if !dst.initialized && src.initialized {
		dst.q = src.q
		dst.n = src.n
		dst.np = src.np
		dst.dn = src.dn
		dst.initialized = true
	}
	dst.Count += src.Count
	dst.Sum += src.Sum
	dst.min = combinedMin
	dst.max = combinedMax
}

// connMergeReceivedChunks merges two ascending compact received-timestamp
// chunk lists into one ascending list, re-chunked with the same fixed capacity
// the analyzer uses. Stable: equal timestamps keep x before y.
func connMergeReceivedChunks(x, y [][]int64) [][]int64 {
	nx := connCountInt64Chunks(x)
	ny := connCountInt64Chunks(y)
	if ny == 0 {
		return x
	}
	if nx == 0 {
		return connRechunkInt64(y, nx+ny)
	}
	xflat := make([]int64, 0, nx)
	connWalkInt64Chunks(x, func(v int64) { xflat = append(xflat, v) })
	yflat := make([]int64, 0, ny)
	connWalkInt64Chunks(y, func(v int64) { yflat = append(yflat, v) })
	merged := make([]int64, 0, nx+ny)
	xi, yi := 0, 0
	for xi < len(xflat) && yi < len(yflat) {
		if yflat[yi] < xflat[xi] {
			merged = append(merged, yflat[yi])
			yi++
		} else {
			merged = append(merged, xflat[xi])
			xi++
		}
	}
	merged = append(merged, xflat[xi:]...)
	merged = append(merged, yflat[yi:]...)
	return connChunkInt64(merged)
}

// connMergeSessionChunks merges two compact-session chunk lists, each ascending
// by end timestamp, into one ascending-by-end list re-chunked at the analyzer's
// fixed capacity. Stable: equal end timestamps keep x before y, matching the
// stream-order convention used by the other analyzers' merges.
func connMergeSessionChunks(x, y [][]compactSession) [][]compactSession {
	nx := connCountSessionChunks(x)
	ny := connCountSessionChunks(y)
	if ny == 0 {
		return x
	}
	if nx == 0 {
		return connRechunkSessions(y, nx+ny)
	}
	xflat := make([]compactSession, 0, nx)
	connWalkSessionChunks(x, func(s compactSession) { xflat = append(xflat, s) })
	yflat := make([]compactSession, 0, ny)
	connWalkSessionChunks(y, func(s compactSession) { yflat = append(yflat, s) })
	merged := make([]compactSession, 0, nx+ny)
	xi, yi := 0, 0
	for xi < len(xflat) && yi < len(yflat) {
		if yflat[yi].endUnixMs < xflat[xi].endUnixMs {
			merged = append(merged, yflat[yi])
			yi++
		} else {
			merged = append(merged, xflat[xi])
			xi++
		}
	}
	merged = append(merged, xflat[xi:]...)
	merged = append(merged, yflat[yi:]...)
	return connChunkSessions(merged)
}

// --- small chunk helpers ----------------------------------------------------

func connCountInt64Chunks(chunks [][]int64) int {
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	return n
}

func connCountSessionChunks(chunks [][]compactSession) int {
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	return n
}

func connWalkInt64Chunks(chunks [][]int64, fn func(int64)) {
	for _, c := range chunks {
		for _, v := range c {
			fn(v)
		}
	}
}

func connWalkSessionChunks(chunks [][]compactSession, fn func(compactSession)) {
	for _, c := range chunks {
		for _, s := range c {
			fn(s)
		}
	}
}

// connChunkInt64 splits a flat ascending slice into fixed-capacity chunks,
// matching the layout addReceived builds during Process.
func connChunkInt64(flat []int64) [][]int64 {
	if len(flat) == 0 {
		return nil
	}
	out := make([][]int64, 0, (len(flat)+sessionsPerChunk-1)/sessionsPerChunk)
	for off := 0; off < len(flat); off += sessionsPerChunk {
		end := off + sessionsPerChunk
		if end > len(flat) {
			end = len(flat)
		}
		chunk := make([]int64, end-off, sessionsPerChunk)
		copy(chunk, flat[off:end])
		out = append(out, chunk)
	}
	return out
}

// connChunkSessions splits a flat slice into fixed-capacity session chunks.
func connChunkSessions(flat []compactSession) [][]compactSession {
	if len(flat) == 0 {
		return nil
	}
	out := make([][]compactSession, 0, (len(flat)+sessionsPerChunk-1)/sessionsPerChunk)
	for off := 0; off < len(flat); off += sessionsPerChunk {
		end := off + sessionsPerChunk
		if end > len(flat) {
			end = len(flat)
		}
		chunk := make([]compactSession, end-off, sessionsPerChunk)
		copy(chunk, flat[off:end])
		out = append(out, chunk)
	}
	return out
}

// connRechunkInt64 deep-copies an existing chunk list into freshly-allocated
// fixed-capacity chunks so a copied src never aliases dst.
func connRechunkInt64(chunks [][]int64, total int) [][]int64 {
	flat := make([]int64, 0, total)
	connWalkInt64Chunks(chunks, func(v int64) { flat = append(flat, v) })
	return connChunkInt64(flat)
}

// connRechunkSessions deep-copies a session chunk list into fresh fixed-capacity
// chunks so a copied src never aliases dst.
func connRechunkSessions(chunks [][]compactSession, total int) [][]compactSession {
	flat := make([]compactSession, 0, total)
	connWalkSessionChunks(chunks, func(s compactSession) { flat = append(flat, s) })
	return connChunkSessions(flat)
}
