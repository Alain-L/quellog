package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several UniqueEntityAnalyzers, Merge recombines their partial
// states so a single Finalize reproduces the single-pass result.
//
// Critical: the analyzer holds a `last lastSeenCache` that batches
// consecutive identical-value inserts and keeps counts NOT yet written into
// the count maps. Finalize drains it via flushLastSeen() before reading the
// maps; a Merge that only unioned the maps would silently drop each shard's
// trailing run. So both sides are flushed first, then the six count maps are
// unioned. All values are integer counts → exactly reproducible. The derived
// fields (Unique* counts, sorted name slices) are recomputed from the maps in
// Finalize, so they need no merging here.
func (a *UniqueEntityAnalyzer) Merge(src *UniqueEntityAnalyzer) {
	if src == nil {
		return
	}
	a.flushLastSeen()
	src.flushLastSeen()

	for k, v := range src.dbCounts {
		a.dbCounts[k] += v
	}
	for k, v := range src.userCounts {
		a.userCounts[k] += v
	}
	for k, v := range src.appCounts {
		a.appCounts[k] += v
	}
	for k, v := range src.hostCounts {
		a.hostCounts[k] += v
	}
	for k, v := range src.userDbCombos {
		a.userDbCombos[k] += v
	}
	for k, v := range src.userHostCombos {
		a.userHostCombos[k] += v
	}
}
