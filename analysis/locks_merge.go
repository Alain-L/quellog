package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several LockAnalyzers, Merge recombines their partial states so a
// single Finalize reproduces the single-pass result.
//
// Why PID-sharding is safe for locks. The core correlation is per-backend: a
// "still waiting" line is later resolved by the matching "acquired" (or a
// deadlock) line ON THE SAME PID, and the DETAIL/CONTEXT/STATEMENT
// continuations that enrich it also carry that PID. PID-sharding routes every
// line of one backend to the same shard, in stream order, so the wait→acquire
// pairing — and the activeLocks map keyed by "<pid>-<lockType>-<resource>" —
// resolves WITHIN a shard before Merge. The activeLocks keys are therefore
// PID-disjoint across shards and can be copied verbatim; Finalize then builds
// the per-query stats from the unioned map exactly as the single pass would.
//
// One cross-PID dependency survives streaming: an event's BlockingPID names a
// DIFFERENT backend, whose query text may live in another shard's
// lastQueryByPID. Streaming only resolves BlockingQueryID/BlockingQuery when
// that text is already cached on the same shard; otherwise it stays empty and
// Finalize back-fills it. Because Merge folds every shard's lastQueryByPID into
// shard 0 before shard 0's Finalize runs, the back-fill sees the complete map
// and resolves the blocking query just as the single pass does.
//
// Accumulated float sums (wait-time totals) are summed; float addition is not
// associative, so the merged result may differ from the single pass in the
// last ULPs — expected, tolerated by the parity test via almostEqual.
func (a *LockAnalyzer) Merge(src *LockAnalyzer) {
	if src == nil || !src.locksExist {
		return
	}
	a.locksExist = true

	// Plain counters.
	a.totalEvents += src.totalEvents
	a.acquiredEvents += src.acquiredEvents
	a.deadlockEvents += src.deadlockEvents

	// Accumulated wait-time total (acquired locks only): SUM.
	a.totalWaitTime += src.totalWaitTime

	// Per-dimension count maps: union + sum.
	for k, v := range src.lockTypeStats {
		a.lockTypeStats[k] += v
	}
	for k, v := range src.resourceTypeStats {
		a.resourceTypeStats[k] += v
	}
	for k, v := range src.relationStats {
		a.relationStats[k] += v
	}

	// Ordered per-occurrence timeline: each shard is already ascending by
	// Timestamp (it processed entries in stream order); merge-sort, never
	// concatenate, to reproduce the single-pass order.
	a.events = lockMergeEvents(a.events, src.events)

	// Per-backend query cache. Keyed by PID, PID-disjoint across shards, so a
	// plain copy never collides. Finalize reads this to (a) build per-query
	// stats from activeLocks and (b) back-fill BlockingQueryID/BlockingQuery
	// for cross-PID blocking edges, so the union must be complete before
	// shard 0's Finalize.
	for pid, q := range src.lastQueryByPID {
		a.lastQueryByPID[pid] = q
	}

	// Transient DETAIL-pairing map: PID-disjoint and never read in Finalize
	// (Process resolves it in-shard). Copied for completeness only.
	for pid, b := range src.pendingBlockingPID {
		a.pendingBlockingPID[pid] = b
	}

	// In-flight per-lock state keyed by "<pid>-<lockType>-<resource>". The PID
	// prefix makes the keys PID-disjoint across shards, so src-only entries are
	// copied (deep-copied so the two analyzers never alias the same *activeLock)
	// and never collide with dst. Finalize folds these into the per-query stats.
	for key, lock := range src.activeLocks {
		if _, ok := a.activeLocks[key]; ok {
			// Should not happen under PID-sharding (keys are PID-disjoint),
			// but stay defensive: keep dst, drop the impossible duplicate.
			continue
		}
		cp := *lock
		a.activeLocks[key] = &cp
	}
}

// lockMergeEvents merges two ascending LockEvent slices into one. Each input
// is already in stream order within its shard; the merge orders by Timestamp
// and tie-breaks by the stream position (seq) so equal-timestamp events from
// different shards interleave back into the exact single-pass order.
func lockMergeEvents(x, y []LockEvent) []LockEvent {
	if len(y) == 0 {
		return x
	}
	if len(x) == 0 {
		return append([]LockEvent(nil), y...)
	}
	out := make([]LockEvent, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		if x[i].Timestamp.Before(y[j].Timestamp) ||
			(x[i].Timestamp.Equal(y[j].Timestamp) && x[i].seq <= y[j].seq) {
			out = append(out, x[i])
			i++
		} else {
			out = append(out, y[j])
			j++
		}
	}
	out = append(out, x[i:]...)
	out = append(out, y[j:]...)
	return out
}
