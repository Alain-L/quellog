package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several TempFileAnalyzers, Merge recombines their partial states
// so a single Finalize reproduces the single-pass result.
//
// Field-by-field reasoning:
//   - count / totalSize: plain running totals, SUM. maxSize: largest single
//     event, MAX.
//   - events: one entry per temp-file creation, appended in stream order so
//     each shard's slice is ascending by Timestamp. The global single pass
//     would produce the same events ordered by Timestamp, so we MERGE-SORT the
//     two ascending slices rather than concatenate. Every event's QueryID has
//     already been resolved within its shard (see the Pattern 1 note below),
//     so no post-merge re-association is needed.
//   - queryStats (map[string]*TempFileQueryStat keyed by FullHash): union by
//     key; shared keys fold (SUM Count/TotalSize, MIN MinSize, MAX MaxSize,
//     and keep the alphabetically-first RawQuery exactly as Process does).
//     src-only keys are DEEP-COPIED so the two analyzers never alias a stat.
//   - pendingByPID / lastQueryByPID: per-PID maps. PID-sharding routes every
//     entry of a backend to one shard, so these key sets are disjoint across
//     shards — copy src entries into dst (no collision possible).
//   - tempFilesExist: OR.
//   - normalizedCache: a pure memoization cache for query normalization; it
//     does not affect Finalize output, so it is intentionally NOT merged.
//
// Pattern 1 transient globals (pendingSize, pendingPID, pendingEventIndex,
// expectingStatement): these bridge a "temporary file ... size NNN" line to
// the STATEMENT line that follows it. The temp-file line and its STATEMENT
// share a PID, and PID-sharding keeps them together in stream order inside the
// same shard, so the pairing always completes within a shard during Process.
// These fields are NEVER read by Finalize — they only carry mid-Process
// correlation state. After Process they describe at most one dangling,
// unpaired temp file at end-of-stream, exactly as the single pass would leave
// it. They are therefore irrelevant to the merged result and intentionally
// IGNORED here.
//
// Correct under repeated pairwise folding: every operation is commutative and
// associative (SUM/MIN/MAX, merge-sort, map union with the same fold), so
// folding shard1..N-1 into shard0 one at a time matches a single pass.
//
// TotalSize-style float sums: there are none here — Size lives as int64 totals
// (totalSize) and TempFileEvent.Size is per-event (carried verbatim through
// the merge-sort), so the merged result is bit-identical to the single pass.
//
// Equal-timestamp events from different backends are interleaved back into
// exact single-pass order by tie-breaking the merge on TempFileEvent.seq (the
// stream position stamped during Process), so the merged events slice is
// bit-identical to a single pass.
func (a *TempFileAnalyzer) Merge(src *TempFileAnalyzer) {
	if src == nil {
		return
	}

	// Plain counters: SUM; maxSize: MAX.
	a.count += src.count
	a.totalSize += src.totalSize
	if src.maxSize > a.maxSize {
		a.maxSize = src.maxSize
	}

	// Ordered per-occurrence events: merge-sort to keep chronological order.
	a.events = tempfileMergeSortedEvents(a.events, src.events)

	// queryStats: union by key; fold shared keys, deep-copy src-only ones.
	for hash, s := range src.queryStats {
		d, ok := a.queryStats[hash]
		if !ok {
			cp := *s
			a.queryStats[hash] = &cp
			continue
		}
		// Keep the alphabetically-first raw query (Process invariant).
		if s.RawQuery < d.RawQuery {
			d.RawQuery = s.RawQuery
		}
		d.Count += s.Count
		d.TotalSize += s.TotalSize
		if s.MinSize < d.MinSize {
			d.MinSize = s.MinSize
		}
		if s.MaxSize > d.MaxSize {
			d.MaxSize = s.MaxSize
		}
	}

	// Per-PID maps: PID-disjoint across shards, so copy src entries in.
	for pid, size := range src.pendingByPID {
		a.pendingByPID[pid] = size
	}
	for pid, q := range src.lastQueryByPID {
		a.lastQueryByPID[pid] = q
	}

	// tempFilesExist: OR.
	a.tempFilesExist = a.tempFilesExist || src.tempFilesExist

	// Pattern 1 transient globals and normalizedCache: intentionally ignored.
}

// tempfileMergeSortedEvents merges two []TempFileEvent slices that are each
// ascending into one ascending slice. Orders by Timestamp and tie-breaks by
// stream position (seq) so equal-timestamp events from different shards
// interleave back into exact single-pass order.
func tempfileMergeSortedEvents(x, y []TempFileEvent) []TempFileEvent {
	if len(y) == 0 {
		return x
	}
	if len(x) == 0 {
		return append([]TempFileEvent(nil), y...)
	}
	out := make([]TempFileEvent, 0, len(x)+len(y))
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
