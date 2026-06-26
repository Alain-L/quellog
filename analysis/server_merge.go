package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several ServerAnalyzers, Merge recombines their partial states
// so a single Finalize reproduces the single-pass result.
//
// ServerAnalyzer has no per-PID in-flight state and no caches (Finalize just
// returns a.m). Counters and SignalCounts SUM; the per-occurrence time
// slices are each ascending within a shard and merge-sort to global
// chronological order. The Timeline is capped at serverTimelineCap keeping
// the EARLIEST events; merge-sorting all shards then re-truncating to the cap
// yields the globally-earliest cap events — identical to the single pass,
// because each shard keeps its own earliest cap and the global earliest are a
// subset of the per-shard earliest.
func (a *ServerAnalyzer) Merge(src *ServerAnalyzer) {
	if src == nil {
		return
	}
	d, s := &a.m, &src.m

	d.StartCount += s.StartCount
	d.ReloadCount += s.ReloadCount
	d.ShutdownFastCount += s.ShutdownFastCount
	d.ShutdownImmediateCount += s.ShutdownImmediateCount
	d.ShutdownSmartCount += s.ShutdownSmartCount
	d.ShutDownCompletedCount += s.ShutDownCompletedCount
	d.CrashRecoveryCount += s.CrashRecoveryCount
	d.InterruptedCount += s.InterruptedCount
	d.BackendCrashCount += s.BackendCrashCount
	d.AuxProcessExitCount += s.AuxProcessExitCount

	for sig, c := range s.SignalCounts {
		d.SignalCounts[sig] += c
	}

	// Ordered per-occurrence time series: merge-sort, never concatenate.
	d.StartTimes = ckptMergeSortedTimes(d.StartTimes, s.StartTimes)
	d.ReloadTimes = ckptMergeSortedTimes(d.ReloadTimes, s.ReloadTimes)
	d.ShutdownTimes = ckptMergeSortedTimes(d.ShutdownTimes, s.ShutdownTimes)
	d.CrashRecoveryTimes = ckptMergeSortedTimes(d.CrashRecoveryTimes, s.CrashRecoveryTimes)
	d.BackendCrashTimes = ckptMergeSortedTimes(d.BackendCrashTimes, s.BackendCrashTimes)

	d.ParameterChanges = serverMergeParameterChanges(d.ParameterChanges, s.ParameterChanges)

	d.Timeline = serverMergeTimeline(d.Timeline, s.Timeline)
	if len(d.Timeline) > serverTimelineCap {
		d.Timeline = d.Timeline[:serverTimelineCap]
	}
}

// serverMergeParameterChanges merges two ascending-by-Timestamp slices.
func serverMergeParameterChanges(x, y []ServerParameterChange) []ServerParameterChange {
	if len(y) == 0 {
		return x
	}
	if len(x) == 0 {
		return append([]ServerParameterChange(nil), y...)
	}
	out := make([]ServerParameterChange, 0, len(x)+len(y))
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

// serverMergeTimeline merges two ascending-by-Timestamp slices.
func serverMergeTimeline(x, y []ServerTimelineEvent) []ServerTimelineEvent {
	if len(y) == 0 {
		return x
	}
	if len(x) == 0 {
		return append([]ServerTimelineEvent(nil), y...)
	}
	out := make([]ServerTimelineEvent, 0, len(x)+len(y))
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
