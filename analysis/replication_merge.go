package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several ReplicationAnalyzers, Merge recombines their partial
// states so a single Finalize reproduces the single-pass result.
//
// pendingConflictPID is transient mid-Process state (it remembers PIDs that
// just had a conflict so the next STATEMENT continuation on the same backend
// is attributed to the conflict query). PID-sharding keeps a conflict marker
// and its continuation on the same shard, so capture always completes inside
// the shard before Merge — the map is never read in Finalize and is ignored.
func (a *ReplicationAnalyzer) Merge(src *ReplicationAnalyzer) {
	if !src.hasAny {
		return
	}
	a.hasAny = true

	for k, v := range src.markers {
		a.markers[k] += v
	}
	for h, c := range src.hourCounts {
		a.hourCounts[h] += c
	}

	// Ordered, per-occurrence timeline: merge-sort by timestamp, never concat.
	a.events = replMergeEvents(a.events, src.events)

	// Running max termination timestamp.
	if src.lastTermination.After(a.lastTermination) {
		a.lastTermination = src.lastTermination
	}

	// Conflict-query stats keyed by full hash: union + fold.
	for hash, s := range src.conflictQueries {
		if dst, ok := a.conflictQueries[hash]; ok {
			dst.Count += s.Count
			if s.RawQuery < dst.RawQuery {
				dst.RawQuery = s.RawQuery
			}
			continue
		}
		// src-only key: deep-copy the pointed-to struct so the two
		// analyzers never share mutable state after the merge.
		cp := *s
		a.conflictQueries[hash] = &cp
	}
}

// replMergeEvents merges two ascending-by-Timestamp ReplicationEvent
// slices into one ascending slice. Each input is already chronological
// (its shard processed entries in stream order); merging — rather than
// concatenating — reproduces the order a single pass would have built.
// On equal timestamps the left (dst) element is emitted first, which
// keeps the merge stable.
func replMergeEvents(x, y []ReplicationEvent) []ReplicationEvent {
	if len(y) == 0 {
		return x
	}
	if len(x) == 0 {
		return append([]ReplicationEvent(nil), y...)
	}
	out := make([]ReplicationEvent, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		// Order by timestamp, tie-break by stream position (seq) to
		// reproduce exact single-pass arrival order.
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
