package analysis

import "time"

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded
// by PID across several CheckpointAnalyzers, Merge recombines their partial
// states so a single Finalize reproduces the single-pass result.
//
// Invariants this relies on:
//   - Each shard processes its entries in stream = chronological order, so
//     every per-occurrence time series (events, typeEvents[*], walDistances,
//     warningEvents) is already ascending within a shard. Merge preserves
//     the global chronological order by MERGE-SORTING the two ascending
//     slices on their timestamp key — never concatenating.
//   - lastCheckpointType is purely transient mid-Process state used to pair a
//     "starting" with its following "complete" on the same backend. It is not
//     read by Finalize, so it is irrelevant once Process has run and is
//     intentionally ignored here.
//
// Accumulated float sums (totalWriteTimeSeconds) are summed; float addition
// is not associative, so the merged result may differ from the single pass in
// the last ULPs — that is expected and tolerated by the parity test.
func (a *CheckpointAnalyzer) Merge(src *CheckpointAnalyzer) {
	// Plain counters: SUM.
	a.completeCount += src.completeCount
	a.totalBuffersWritten += src.totalBuffersWritten
	a.totalDistanceKB += src.totalDistanceKB

	// Accumulated float sum: SUM.
	a.totalWriteTimeSeconds += src.totalWriteTimeSeconds

	// Max fields (init 0 — every real value is > 0): take the larger.
	if src.maxWriteTimeSeconds > a.maxWriteTimeSeconds {
		a.maxWriteTimeSeconds = src.maxWriteTimeSeconds
	}
	if src.maxDistanceKB > a.maxDistanceKB {
		a.maxDistanceKB = src.maxDistanceKB
	}

	// Ordered per-occurrence time series: merge-sort to keep chronological order.
	a.events = ckptMergeSortedTimes(a.events, src.events)
	a.walDistances = ckptMergeSortedWAL(a.walDistances, src.walDistances)

	// Type maps: union keys, summing counts and merge-sorting the event slices.
	for k, v := range src.typeCounts {
		a.typeCounts[k] += v
	}
	for k, ev := range src.typeEvents {
		a.typeEvents[k] = ckptMergeSortedTimes(a.typeEvents[k], ev)
	}

	// Warnings. warningMinIntervalSeconds uses warningCount as its "unset"
	// sentinel (Process sets the min on the first warning regardless of the
	// zero value), so the min must only be taken from a shard that actually
	// saw a warning. Capture the destination's prior warningCount before it
	// is updated so the sentinel check stays correct.
	dstHadWarnings := a.warningCount > 0
	a.warningCount += src.warningCount
	a.warningEvents = ckptMergeSortedTimes(a.warningEvents, src.warningEvents)
	if src.warningCount > 0 {
		if !dstHadWarnings || src.warningMinIntervalSeconds < a.warningMinIntervalSeconds {
			a.warningMinIntervalSeconds = src.warningMinIntervalSeconds
		}
		if src.warningMaxIntervalSeconds > a.warningMaxIntervalSeconds {
			a.warningMaxIntervalSeconds = src.warningMaxIntervalSeconds
		}
	}

	// lastCheckpointType: transient, not read in Finalize — intentionally ignored.
}

// ckptMergeSortedTimes merges two ascending []time.Time slices into one
// ascending slice, preserving stream (chronological) order. Stable: equal
// timestamps keep x before y.
func ckptMergeSortedTimes(x, y []time.Time) []time.Time {
	if len(y) == 0 {
		return x
	}
	if len(x) == 0 {
		return append([]time.Time(nil), y...)
	}
	out := make([]time.Time, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		if !y[j].Before(x[i]) { // x[i] <= y[j]
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

// ckptMergeSortedWAL merges two []CheckpointWAL slices that are each ascending
// by Timestamp into one ascending slice, preserving chronological order.
// Stable: equal timestamps keep x before y.
func ckptMergeSortedWAL(x, y []CheckpointWAL) []CheckpointWAL {
	if len(y) == 0 {
		return x
	}
	if len(x) == 0 {
		return append([]CheckpointWAL(nil), y...)
	}
	out := make([]CheckpointWAL, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		if !y[j].Timestamp.Before(x[i].Timestamp) { // x[i] <= y[j]
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
