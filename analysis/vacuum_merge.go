package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several VacuumAnalyzers, Merge recombines their partial states
// so a single Finalize reproduces the single-pass result.
//
// The analyzer carries no ordered per-occurrence slice and no per-PID
// in-flight state (Process writes directly to final aggregates), so Merge is
// a field-wise reduction: counters/byte totals SUM, per-table stat maps union
// + fold, and slowestVacuum keeps the worst-elapsed sample. The derived,
// pre-sorted views (TopVacuumTables, XminBlockedTables, TopAnalyzeTables
// ByElapsed) are recomputed from the maps in Finalize, so they need no
// merging here.
//
// Accumulated float sums (elapsed seconds, per-table elapsed) are summed;
// float addition is not associative, so the merged result may differ from the
// single pass in the last ULPs — expected, tolerated by the parity test.
func (a *VacuumAnalyzer) Merge(src *VacuumAnalyzer) {
	if src == nil {
		return
	}

	// Plain counters.
	a.vacuumCount += src.vacuumCount
	a.aggressiveVacuumCount += src.aggressiveVacuumCount
	a.analyzeCount += src.analyzeCount

	// Per-table count / space maps.
	for table, c := range src.vacuumTableCounts {
		a.vacuumTableCounts[table] += c
	}
	for table, c := range src.analyzeTableCounts {
		a.analyzeTableCounts[table] += c
	}
	for table, b := range src.vacuumSpaceRecovered {
		a.vacuumSpaceRecovered[table] += b
	}

	// Continuation-line global aggregates.
	a.totalElapsedSeconds += src.totalElapsedSeconds
	a.totalTuplesRemoved += src.totalTuplesRemoved
	a.totalTuplesNotYetRemovable += src.totalTuplesNotYetRemovable
	a.totalBufferHits += src.totalBufferHits
	a.totalBufferMisses += src.totalBufferMisses
	a.totalBufferDirtied += src.totalBufferDirtied
	a.totalBufferWritten += src.totalBufferWritten
	a.totalWALRecords += src.totalWALRecords
	a.totalWALBytes += src.totalWALBytes
	a.totalAnalyzeElapsedSeconds += src.totalAnalyzeElapsedSeconds

	// Per-table continuation stats (pointer-valued): fold shared keys,
	// deep-copy src-only keys so the two analyzers never alias.
	vacuumMergeTableStats(a.vacuumTableStats, src.vacuumTableStats)
	vacuumMergeTableStats(a.analyzeTableStats, src.analyzeTableStats)

	// Worst-elapsed sample: keep the larger elapsed; on a tie keep the
	// earlier stream position. Process replaces slowestVacuum only on a
	// strictly greater elapsed, so the FIRST sample reaching the max wins —
	// which is the smallest seq. This reproduces the single-pass choice
	// exactly even when several vacuums share the same elapsed time.
	if src.slowestVacuum != nil {
		if a.slowestVacuum == nil ||
			src.slowestVacuum.ElapsedSeconds > a.slowestVacuum.ElapsedSeconds ||
			(src.slowestVacuum.ElapsedSeconds == a.slowestVacuum.ElapsedSeconds &&
				src.slowestVacuum.seq < a.slowestVacuum.seq) {
			cp := *src.slowestVacuum
			a.slowestVacuum = &cp
		}
	}
}

// vacuumMergeTableStats folds src's per-table stats into dst.
func vacuumMergeTableStats(dst, src map[string]*VacuumTableStat) {
	for table, s := range src {
		d, ok := dst[table]
		if !ok {
			cp := *s
			dst[table] = &cp
			continue
		}
		d.VacuumCount += s.VacuumCount
		d.TotalElapsedSeconds += s.TotalElapsedSeconds
		if s.MaxElapsedSeconds > d.MaxElapsedSeconds {
			d.MaxElapsedSeconds = s.MaxElapsedSeconds
		}
		d.TuplesRemoved += s.TuplesRemoved
		d.TuplesNotYetRemovable += s.TuplesNotYetRemovable
		d.BufferHits += s.BufferHits
		d.BufferMisses += s.BufferMisses
		d.BufferDirtied += s.BufferDirtied
		d.BufferWritten += s.BufferWritten
		d.WALRecords += s.WALRecords
		d.WALBytes += s.WALBytes
	}
}
