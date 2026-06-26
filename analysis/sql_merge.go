package analysis

// Merge folds the fully-processed state of src into a. It is the
// data-parallel counterpart of Process: when the entry stream is sharded by
// PID across several SQLAnalyzers, Merge recombines their partial states so a
// single Finalize reproduces the single-pass result.
//
// Preconditions / invariants this relies on:
//   - Sharding is by PID, so the per-PID in-flight maps (pendingPlanByPID and
//     pendingExecByPID) hold DISJOINT keys across shards: every entry of a
//     given backend — the auto_explain "plan:" line, the timed "execute:"
//     line, and its "DETAIL: parameters:" continuation — lands on the same
//     shard, in stream order. A plan→statement or execute→params pair
//     therefore always completes within one shard; the residual pending state
//     (a plan or execute whose follow-up never arrived before EOF) can be
//     copied verbatim.
//   - The compact executions store is consumed by Finalize only through
//     order-INDEPENDENT reductions: Durations() is sorted before the
//     median/P99 computation, and the per-dimension tallies are pure counts.
//     So appending src's events onto dst in any order reproduces Finalize
//     exactly. We re-append each decoded event through the existing append
//     path so src's dictionary indices are re-mapped into dst's dictionaries
//     (a raw slice copy would corrupt the dictionary-encoded dimensions).
//
// Accumulated float sums (per-query TotalTime, the global sumQueryDuration)
// are summed; float addition is not associative, so the merged result may
// differ from the single pass in the last ULPs — expected, tolerated by the
// parity test's almostEqual.
//
// Both of these reproduce the single pass exactly under sharding:
//   - QueryStat.LastPlan keeps the most recent auto_explain plan per query
//     signature. The single pass overwrites it in stream order, so the kept
//     plan is the one with the largest lastPlanSeq; Merge picks the larger
//     lastPlanSeq across shards, reconstructing that chronological-last choice.
//   - QueryStat.PreparedNames is a deduplicated set capped at the K smallest
//     names (see appendPreparedName); selecting smallest-K rather than
//     first-K-seen makes the retained set order-independent, so a sharded
//     union folds to the same set as a single pass.
func (a *SQLAnalyzer) Merge(src *SQLAnalyzer) {
	if src == nil {
		return
	}

	// Plain counter.
	a.totalQueries += src.totalQueries

	// Global duration extremes. minQueryDuration uses 0 as the "unset"
	// sentinel exactly like Process (it also leaves a genuine 0 ms run at 0),
	// so only adopt a non-zero src min, and only when it is smaller than
	// (or replaces an unset) dst min.
	if src.minQueryDuration != 0 && (a.minQueryDuration == 0 || src.minQueryDuration < a.minQueryDuration) {
		a.minQueryDuration = src.minQueryDuration
	}
	if src.maxQueryDuration > a.maxQueryDuration {
		a.maxQueryDuration = src.maxQueryDuration
	}
	a.sumQueryDuration += src.sumQueryDuration

	// Timestamp range: min start / max end, guarding zero values the way
	// Process does (a zero timestamp never narrows the range).
	if !src.startTimestamp.IsZero() &&
		(a.startTimestamp.IsZero() || src.startTimestamp.Before(a.startTimestamp)) {
		a.startTimestamp = src.startTimestamp
	}
	if !src.endTimestamp.IsZero() &&
		(a.endTimestamp.IsZero() || src.endTimestamp.After(a.endTimestamp)) {
		a.endTimestamp = src.endTimestamp
	}

	// Per-query stats: union by normalized-query key, fold shared keys,
	// deep-copy src-only entries so the two analyzers never alias.
	for key, s := range src.queryStats {
		dst, ok := a.queryStats[key]
		if !ok {
			a.queryStats[key] = copyQueryStat(s)
			continue
		}
		mergeQueryStat(dst, s)
	}

	// Per-dimension query-type breakdowns: nested union + fold.
	sqlMergeTypeCounts(a.queryTypesByDatabase, src.queryTypesByDatabase)
	sqlMergeTypeCounts(a.queryTypesByUser, src.queryTypesByUser)
	sqlMergeTypeCounts(a.queryTypesByHost, src.queryTypesByHost)
	sqlMergeTypeCounts(a.queryTypesByApp, src.queryTypesByApp)

	// Compact executions: re-append each of src's events through dst's
	// append path so the dictionary-encoded dimensions and interned query
	// IDs are re-mapped into dst's tables. Order does not matter (see the
	// method doc): Finalize sorts durations and tallies dimensions.
	if src.executions != nil {
		src.executions.ForEach(func(e QueryExecution) bool {
			a.executions.append(e.Timestamp, e.Duration, e.QueryID, e.Database, e.User, e.App, e.Host)
			return true
		})
	}

	// Per-PID in-flight state: keys are disjoint across shards, so copy
	// src's residual pending plans / executes verbatim. Finalize never
	// consumes these (they are only read during Process when the matching
	// follow-up line arrives), but copying keeps the merged analyzer a
	// faithful union of state.
	for pid, plan := range src.pendingPlanByPID {
		a.pendingPlanByPID[pid] = plan
	}
	for pid, pe := range src.pendingExecByPID {
		a.pendingExecByPID[pid] = pe
	}

	// normalizationCache is a pure performance cache (raw→normalized
	// mapping); it has no effect on output and is intentionally not merged.
}

// copyQueryStat returns a deep copy of s so a merged analyzer never aliases
// the source's pointer-valued SlowestRun or its PreparedNames slice.
func copyQueryStat(s *QueryStat) *QueryStat {
	cp := *s
	if s.SlowestRun != nil {
		sr := *s.SlowestRun
		cp.SlowestRun = &sr
	}
	if s.PreparedNames != nil {
		cp.PreparedNames = append([]string(nil), s.PreparedNames...)
	}
	return &cp
}

// mergeQueryStat folds src into dst for the same normalized query key.
func mergeQueryStat(dst, src *QueryStat) {
	// Deterministic raw example: keep the alphabetically-first one, matching
	// Process ("rawQuery < stats.RawQuery").
	if src.RawQuery < dst.RawQuery {
		dst.RawQuery = src.RawQuery
	}

	dst.Count += src.Count
	dst.TotalTime += src.TotalTime
	if src.MaxTime > dst.MaxTime {
		dst.MaxTime = src.MaxTime
	}

	// LastPlan: keep the chronologically-last plan across shards, i.e. the
	// one set by the execution with the largest stream position. This
	// reproduces the single pass, which overwrites LastPlan in stream order.
	if src.LastPlan != "" && (dst.LastPlan == "" || src.lastPlanSeq > dst.lastPlanSeq) {
		dst.LastPlan = src.LastPlan
		dst.lastPlanSeq = src.lastPlanSeq
	}

	// PreparedNames: deduplicated, order-preserving, capped union.
	for _, name := range src.PreparedNames {
		dst.PreparedNames = appendPreparedName(dst.PreparedNames, name)
	}

	// SlowestRun: keep the run with the larger DurationMs; on a tie keep the
	// chronologically earlier one. Process replaces SlowestRun only on
	// strictly greater duration, so the first global max must win — exactly
	// what (earlier Timestamp on tie) reproduces. Deep-copy when taking from
	// src so the two analyzers never alias.
	if src.SlowestRun != nil {
		if dst.SlowestRun == nil ||
			src.SlowestRun.DurationMs > dst.SlowestRun.DurationMs ||
			(src.SlowestRun.DurationMs == dst.SlowestRun.DurationMs &&
				src.SlowestRun.Timestamp.Before(dst.SlowestRun.Timestamp)) {
			sr := *src.SlowestRun
			dst.SlowestRun = &sr
		}
	}
}

// sqlMergeTypeCounts folds src's nested dimension→type→count map into dst.
// Shared inner entries sum their fields; src-only inner entries are
// deep-copied so the two analyzers never alias a *QueryTypeCount.
func sqlMergeTypeCounts(dst, src map[string]map[string]*QueryTypeCount) {
	for dim, srcInner := range src {
		dstInner, ok := dst[dim]
		if !ok {
			dstInner = make(map[string]*QueryTypeCount, len(srcInner))
			dst[dim] = dstInner
		}
		for qtype, sc := range srcInner {
			if dc, ok := dstInner[qtype]; ok {
				dc.Count += sc.Count
				dc.TotalTime += sc.TotalTime
			} else {
				cp := *sc
				dstInner[qtype] = &cp
			}
		}
	}
}
