// Package analysis provides log analysis functionality for PostgreSQL logs.
package analysis

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// PostgreSQL constants
const (
	// pageSize is the standard PostgreSQL page size in bytes.
	// PostgreSQL stores data in 8 KB pages.
	pageSize int64 = 8192
)

// VacuumMetrics aggregates autovacuum and autoanalyze stats.
type VacuumMetrics struct {
	// VacuumCount counts every automatic vacuum (includes aggressive,
	// which is just a type of vacuum).
	VacuumCount int
	// AggressiveVacuumCount counts "automatic aggressive vacuum" runs,
	// i.e. anti-wraparound freezes. A high ratio vs VacuumCount means
	// the cluster is under freeze pressure — worth surfacing when
	// diagnosing autovacuum tuning.
	AggressiveVacuumCount int
	AnalyzeCount          int
	VacuumTableCounts     map[string]int   // table → vacuum count
	AnalyzeTableCounts    map[string]int   // table → analyze count
	VacuumSpaceRecovered  map[string]int64 // table → bytes reclaimed (dead tuples)

	// Aggregated continuation-line metrics. PostgreSQL emits the autovacuum
	// "system usage", "buffer usage", "WAL usage" and "tuples: removed/
	// remain/not yet removable" fields on subsequent log lines that our
	// stderr parser concatenates into the same LogEntry.Message. These
	// totals let a DBA answer "which table burns most autovacuum time"
	// and the wraparound-precursor question "is something blocking
	// dead-tuple cleanup" without grep-ing the raw log.
	TotalVacuumElapsedSeconds  float64
	TotalTuplesRemoved         int64
	TotalTuplesNotYetRemovable int64
	TotalBufferHits            int64
	TotalBufferMisses          int64
	TotalBufferDirtied         int64
	TotalBufferWritten         int64
	TotalWALRecords            int64
	TotalWALBytes              int64

	// Per-table aggregates derived from the continuation lines, with
	// pre-sorted top-N views so renderers don't have to walk the maps.
	VacuumTableStats map[string]*VacuumTableStat // table → continuation stats
	TopVacuumTables  []VacuumTableStat           // top tables by total elapsed (desc)
	// XminBlockedTables lists the tables where a vacuum saw "dead but
	// not yet removable" tuples — i.e. a long-running transaction is
	// holding back the xmin horizon. Sorted by count desc.
	XminBlockedTables []VacuumTableStat
	// SlowestVacuum captures the single worst-elapsed vacuum observed,
	// useful as the headline "anomaly" line in the maintenance section.
	SlowestVacuum *VacuumSample

	// TotalAnalyzeElapsedSeconds is the global cumulative time spent in
	// autoanalyze "system usage" blocks. TopAnalyzeTablesByElapsed
	// reuses VacuumTableStat as a shape (its VacuumCount field carries
	// the analyze occurrence count when the slice originates from the
	// analyze branch). Both fields are zero when the log carries no
	// autoanalyze system-usage line.
	TotalAnalyzeElapsedSeconds float64
	TopAnalyzeTablesByElapsed  []VacuumTableStat
}

// VacuumTableStat aggregates autovacuum continuation metrics for a
// single table across every run observed in the log.
type VacuumTableStat struct {
	Table                 string
	VacuumCount           int
	TotalElapsedSeconds   float64
	MaxElapsedSeconds     float64
	TuplesRemoved         int64
	TuplesNotYetRemovable int64
	BufferHits            int64
	BufferMisses          int64
	BufferDirtied         int64
	BufferWritten         int64
	WALRecords            int64
	WALBytes              int64
}

// VacuumSample is a single autovacuum observation, useful when surfacing
// the worst-elapsed run as a headline anomaly.
type VacuumSample struct {
	Table                 string
	Timestamp             time.Time
	ElapsedSeconds        float64
	TuplesRemoved         int64
	TuplesNotYetRemovable int64
	PagesRemoved          int64
	seq                   int64 // stream position (unexported: not serialized), for stable cross-shard merge
}

// ============================================================================
// Vacuum log patterns
// ============================================================================

// Vacuum log message patterns
const (
	autoVacuumMarker           = "automatic vacuum of table"
	autoAggressiveVacuumMarker = "automatic aggressive vacuum of table"
	autoAnalyzeMarker          = "automatic analyze of table"
	pagesRemovedKey            = "pages: "
	pagesRemovedWord           = " removed"
)

// ============================================================================
// Streaming vacuum analyzer
// ============================================================================

// VacuumAnalyzer processes autovacuum and autoanalyze events from log entries.
// It tracks operation counts and space recovery per table.
//
// Usage:
//
//	analyzer := NewVacuumAnalyzer()
//	for entry := range logEntries {
//	    analyzer.Process(&entry)
//	}
//	metrics := analyzer.Finalize()
type VacuumAnalyzer struct {
	vacuumCount           int
	aggressiveVacuumCount int
	analyzeCount          int
	vacuumTableCounts     map[string]int
	analyzeTableCounts    map[string]int
	vacuumSpaceRecovered  map[string]int64

	// Continuation-line aggregates. Populated from the same Process()
	// call as the existing counters — the stderr parser has already
	// concatenated the tab-indented continuation lines into one
	// LogEntry.Message, so a single regex/Index pass per event covers
	// everything.
	vacuumTableStats           map[string]*VacuumTableStat
	totalElapsedSeconds        float64
	totalTuplesRemoved         int64
	totalTuplesNotYetRemovable int64
	totalBufferHits            int64
	totalBufferMisses          int64
	totalBufferDirtied         int64
	totalBufferWritten         int64
	totalWALRecords            int64
	totalWALBytes              int64
	slowestVacuum              *VacuumSample

	analyzeTableStats          map[string]*VacuumTableStat
	totalAnalyzeElapsedSeconds float64

	// curSeq is the stream position of the entry being processed, stamped
	// onto slowestVacuum so the cross-shard merge breaks elapsed-time ties
	// deterministically (earliest stream position wins, as a single pass does).
	curSeq int64
}

// NewVacuumAnalyzer creates a new vacuum analyzer.
func NewVacuumAnalyzer() *VacuumAnalyzer {
	return &VacuumAnalyzer{
		vacuumTableCounts:    make(map[string]int, 100),
		analyzeTableCounts:   make(map[string]int, 100),
		vacuumSpaceRecovered: make(map[string]int64, 100),
		vacuumTableStats:     make(map[string]*VacuumTableStat, 100),
		analyzeTableStats:    make(map[string]*VacuumTableStat, 100),
	}
}

// Process analyzes a single log entry for vacuum and analyze operations.
//
// Expected log formats:
//   - Vacuum: "automatic vacuum of table \"schema.table\": index scans: 0, pages: 123 removed, ..."
//   - Analyze: "automatic analyze of table \"schema.table\" system usage: CPU: ..."
func (a *VacuumAnalyzer) Process(entry *parser.LogEntry) {
	msg := entry.Message

	if len(msg) < 18 {
		return
	}
	a.curSeq = entry.Seq

	// Fast pre-filter: check for "uto" before expensive Index
	// "uto" is highly specific to "automatic" and eliminates ~99%+ of messages
	if !strings.Contains(msg, "uto") {
		return
	}

	// Search for "automatic vacuum" or "automatic analyze" anywhere
	idx := strings.Index(msg, "automatic")
	if idx < 0 {
		return
	}

	// Check what follows "automatic ". Handle three variants:
	//   "automatic vacuum of table ..."
	//   "automatic aggressive vacuum of table ..."  (anti-wraparound freeze)
	//   "automatic analyze of table ..."
	rest := msg[idx+10:]

	// Aggressive vacuum checked first because it also matches as a vacuum
	// (aggressive IS a type of vacuum, so we increment both counters).
	if strings.HasPrefix(rest, "aggressive vacuum") {
		tableName := extractTableName(msg)
		a.vacuumCount++
		a.aggressiveVacuumCount++
		a.vacuumTableCounts[tableName]++
		removedPages := extractRemovedPages(msg)
		// Guard the byte conversion against int64 overflow: a corrupt or
		// absurd page count (> ~1.1e15) would wrap negative and poison the
		// running total. Skip such values rather than report bogus bytes.
		if removedPages > 0 && removedPages <= math.MaxInt64/pageSize {
			a.vacuumSpaceRecovered[tableName] += removedPages * pageSize
		}
		a.recordContinuationStats(tableName, msg, entry.Timestamp, removedPages)
		return
	}

	if strings.HasPrefix(rest, "vacuum") {
		tableName := extractTableName(msg)
		a.vacuumCount++
		a.vacuumTableCounts[tableName]++
		removedPages := extractRemovedPages(msg)
		// Guard the byte conversion against int64 overflow: a corrupt or
		// absurd page count (> ~1.1e15) would wrap negative and poison the
		// running total. Skip such values rather than report bogus bytes.
		if removedPages > 0 && removedPages <= math.MaxInt64/pageSize {
			a.vacuumSpaceRecovered[tableName] += removedPages * pageSize
		}
		a.recordContinuationStats(tableName, msg, entry.Timestamp, removedPages)
		return
	}

	if strings.HasPrefix(rest, "analyze") {
		tableName := extractTableName(msg)
		a.analyzeCount++
		a.analyzeTableCounts[tableName]++
		// Analyze blocks only carry "system usage: CPU ... elapsed: X s"
		// — no tuples, no buffer, no WAL. Track elapsed alone so we can
		// rank tables by autoanalyze cost when the data is present.
		if elapsed := extractElapsedSeconds(msg); elapsed > 0 {
			stat := a.analyzeTableStats[tableName]
			if stat == nil {
				stat = &VacuumTableStat{Table: tableName}
				a.analyzeTableStats[tableName] = stat
			}
			stat.VacuumCount++
			stat.TotalElapsedSeconds += elapsed
			if elapsed > stat.MaxElapsedSeconds {
				stat.MaxElapsedSeconds = elapsed
			}
			a.totalAnalyzeElapsedSeconds += elapsed
		}
	}
}

// recordContinuationStats walks the concatenated vacuum message looking
// for the continuation-line fields (elapsed time, tuples removed/remain,
// dead-but-not-yet-removable, buffer usage, WAL usage) and folds them
// into both the per-table aggregate and the global running totals.
// Missing fields are tolerated — PostgreSQL versions differ in which
// continuation lines they emit, and there is no value in failing the
// whole vacuum record because one continuation is absent.
func (a *VacuumAnalyzer) recordContinuationStats(table, msg string, ts time.Time, pagesRemoved int64) {
	stat := a.vacuumTableStats[table]
	if stat == nil {
		stat = &VacuumTableStat{Table: table}
		a.vacuumTableStats[table] = stat
	}
	stat.VacuumCount++

	elapsed := extractElapsedSeconds(msg)
	if elapsed > 0 {
		stat.TotalElapsedSeconds += elapsed
		if elapsed > stat.MaxElapsedSeconds {
			stat.MaxElapsedSeconds = elapsed
		}
		a.totalElapsedSeconds += elapsed
		if a.slowestVacuum == nil || elapsed > a.slowestVacuum.ElapsedSeconds {
			a.slowestVacuum = &VacuumSample{
				Table:                 table,
				Timestamp:             ts,
				ElapsedSeconds:        elapsed,
				PagesRemoved:          pagesRemoved,
				TuplesRemoved:         extractTuplesRemoved(msg),
				TuplesNotYetRemovable: extractTuplesNotYetRemovable(msg),
				seq:                   a.curSeq,
			}
		}
	}

	if removed := extractTuplesRemoved(msg); removed > 0 {
		stat.TuplesRemoved += removed
		a.totalTuplesRemoved += removed
	}
	if nyr := extractTuplesNotYetRemovable(msg); nyr > 0 {
		stat.TuplesNotYetRemovable += nyr
		a.totalTuplesNotYetRemovable += nyr
	}

	if hits, misses, dirtied, written, ok := extractBufferUsage(msg); ok {
		stat.BufferHits += hits
		stat.BufferMisses += misses
		stat.BufferDirtied += dirtied
		stat.BufferWritten += written
		a.totalBufferHits += hits
		a.totalBufferMisses += misses
		a.totalBufferDirtied += dirtied
		a.totalBufferWritten += written
	}

	if records, bytes, ok := extractWALUsage(msg); ok {
		stat.WALRecords += records
		stat.WALBytes += bytes
		a.totalWALRecords += records
		a.totalWALBytes += bytes
	}
}

// Finalize returns the aggregated vacuum metrics.
// This should be called after all log entries have been processed.
// roundMicroSec drops sub-microsecond noise from a summed seconds value.
// Cumulative elapsed times are float64 sums whose low bits depend on the
// summation order; once the analyzer is PID-sharded the per-shard partial
// sums fold in a different order than the sequential pass, so the last ULPs
// differ. Source vacuum/analyze "elapsed: N.NN s" lines are at best
// microsecond-precise, so anything below 1 µs is noise — rounding keeps the
// per-table ordering and the rendered totals identical regardless of fold
// order (text and JSON alike).
func roundMicroSec(s float64) float64 {
	return math.Round(s*1e6) / 1e6
}

func (a *VacuumAnalyzer) Finalize() VacuumMetrics {
	// Stabilize summed elapsed times against shard-fold order before any
	// ordering or rendering reads them (see roundMicroSec). Max values are
	// order-independent and left untouched.
	a.totalElapsedSeconds = roundMicroSec(a.totalElapsedSeconds)
	a.totalAnalyzeElapsedSeconds = roundMicroSec(a.totalAnalyzeElapsedSeconds)
	for _, s := range a.vacuumTableStats {
		s.TotalElapsedSeconds = roundMicroSec(s.TotalElapsedSeconds)
	}
	for _, s := range a.analyzeTableStats {
		s.TotalElapsedSeconds = roundMicroSec(s.TotalElapsedSeconds)
	}

	top := topVacuumTablesByElapsed(a.vacuumTableStats, 200)
	xmin := topVacuumTablesByXminPressure(a.vacuumTableStats, 200)
	return VacuumMetrics{
		VacuumCount:                a.vacuumCount,
		AggressiveVacuumCount:      a.aggressiveVacuumCount,
		AnalyzeCount:               a.analyzeCount,
		VacuumTableCounts:          a.vacuumTableCounts,
		AnalyzeTableCounts:         a.analyzeTableCounts,
		VacuumSpaceRecovered:       a.vacuumSpaceRecovered,
		TotalVacuumElapsedSeconds:  a.totalElapsedSeconds,
		TotalTuplesRemoved:         a.totalTuplesRemoved,
		TotalTuplesNotYetRemovable: a.totalTuplesNotYetRemovable,
		TotalBufferHits:            a.totalBufferHits,
		TotalBufferMisses:          a.totalBufferMisses,
		TotalBufferDirtied:         a.totalBufferDirtied,
		TotalBufferWritten:         a.totalBufferWritten,
		TotalWALRecords:            a.totalWALRecords,
		TotalWALBytes:              a.totalWALBytes,
		VacuumTableStats:           a.vacuumTableStats,
		TopVacuumTables:            top,
		XminBlockedTables:          xmin,
		SlowestVacuum:              a.slowestVacuum,
		TotalAnalyzeElapsedSeconds: a.totalAnalyzeElapsedSeconds,
		TopAnalyzeTablesByElapsed:  topVacuumTablesByElapsed(a.analyzeTableStats, 200),
	}
}

// topVacuumTablesByElapsed returns the n tables with the most cumulative
// autovacuum time, sorted desc. Tables that never reported a system
// usage line (elapsed = 0) are skipped — they would crowd the table
// without adding signal.
func topVacuumTablesByElapsed(stats map[string]*VacuumTableStat, n int) []VacuumTableStat {
	if len(stats) == 0 {
		return nil
	}
	out := make([]VacuumTableStat, 0, len(stats))
	for _, s := range stats {
		if s.TotalElapsedSeconds <= 0 {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalElapsedSeconds != out[j].TotalElapsedSeconds {
			return out[i].TotalElapsedSeconds > out[j].TotalElapsedSeconds
		}
		// Tie-breaker on table name so JSON output is deterministic
		// across runs — Go map iteration order is randomized.
		return out[i].Table < out[j].Table
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// topVacuumTablesByXminPressure returns the n tables with the largest
// cumulative "dead but not yet removable" count — i.e. those most
// affected by a stuck xmin horizon, sorted desc.
func topVacuumTablesByXminPressure(stats map[string]*VacuumTableStat, n int) []VacuumTableStat {
	if len(stats) == 0 {
		return nil
	}
	out := make([]VacuumTableStat, 0, len(stats))
	for _, s := range stats {
		if s.TuplesNotYetRemovable <= 0 {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TuplesNotYetRemovable != out[j].TuplesNotYetRemovable {
			return out[i].TuplesNotYetRemovable > out[j].TuplesNotYetRemovable
		}
		return out[i].Table < out[j].Table
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// ============================================================================
// Extraction helpers
// ============================================================================

// extractTableName retrieves the table name from a vacuum/analyze log message.
// PostgreSQL logs table names in quotes: "schema.table" or "public.users"
//
// Returns "UNKNOWN" if the table name cannot be extracted.
func extractTableName(logMsg string) string {
	// Find first quote
	firstQuote := strings.Index(logMsg, `"`)
	if firstQuote == -1 {
		return "UNKNOWN"
	}

	// Find second quote (closing quote)
	secondQuote := strings.Index(logMsg[firstQuote+1:], `"`)
	if secondQuote == -1 {
		return "UNKNOWN"
	}

	// Extract table name between quotes
	tableName := logMsg[firstQuote+1 : firstQuote+1+secondQuote]
	if tableName == "" {
		return "UNKNOWN"
	}

	return tableName
}

// extractElapsedSeconds reads the "elapsed: X.XXX s" field from the
// "system usage: ..." continuation. Returns 0 when the field is absent
// (older PostgreSQL versions emit "system usage: CPU 0.00s/0.00u sec
// elapsed 12.34 s", newer versions use "elapsed: 12.345 s"). Both
// forms are matched by anchoring on " elapsed".
func extractElapsedSeconds(msg string) float64 {
	idx := strings.Index(msg, " elapsed")
	if idx == -1 {
		return 0
	}
	rest := msg[idx+len(" elapsed"):]
	rest = strings.TrimLeft(rest, " :")
	end := 0
	for end < len(rest) {
		c := rest[end]
		if (c >= '0' && c <= '9') || c == '.' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(rest[:end], 64)
	if err != nil {
		return 0
	}
	return v
}

// extractTuplesRemoved reads the "tuples: X removed, Y remain, Z dead
// but not yet removable" continuation and returns X. Returns 0 when the
// "tuples:" prefix is missing or the number cannot be parsed.
func extractTuplesRemoved(msg string) int64 {
	idx := strings.Index(msg, "tuples: ")
	if idx == -1 {
		return 0
	}
	rest := msg[idx+len("tuples: "):]
	return parseLeadingInt64(rest, " removed")
}

// extractTuplesNotYetRemovable reads the "Z are dead but not yet
// removable" component of the "tuples:" continuation. Returns 0 when
// absent — this fragment surfaces only on PG 14+ in long form, but
// every supported version emits "not yet removable" verbatim when the
// count is non-zero, so a substring search is sufficient.
func extractTuplesNotYetRemovable(msg string) int64 {
	const marker = "are dead but not yet removable"
	idx := strings.Index(msg, marker)
	if idx == -1 {
		return 0
	}
	// Walk backwards over digits and one optional space to capture the
	// number preceding "are dead but not yet removable".
	end := idx
	for end > 0 && msg[end-1] == ' ' {
		end--
	}
	start := end
	for start > 0 {
		c := msg[start-1]
		if c >= '0' && c <= '9' {
			start--
			continue
		}
		break
	}
	if start == end {
		return 0
	}
	v, err := strconv.ParseInt(msg[start:end], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// extractBufferUsage parses the "buffer usage: X hits, Y misses, Z
// dirtied[, W written]" continuation. Returns the four counters and
// ok=true when the marker is present. Earlier PostgreSQL versions omit
// the "written" component — we tolerate that by leaving it at 0.
func extractBufferUsage(msg string) (hits, misses, dirtied, written int64, ok bool) {
	idx := strings.Index(msg, "buffer usage: ")
	if idx == -1 {
		return 0, 0, 0, 0, false
	}
	rest := msg[idx+len("buffer usage: "):]
	hits = parseLeadingInt64(rest, " hits")
	misses = parseAfterKeyword(rest, " misses")
	dirtied = parseAfterKeyword(rest, " dirtied")
	written = parseAfterKeyword(rest, " written")
	if hits == 0 && misses == 0 && dirtied == 0 && written == 0 {
		return 0, 0, 0, 0, false
	}
	return hits, misses, dirtied, written, true
}

// extractWALUsage parses the "WAL usage: records=X, full page records=Y,
// bytes=Z" continuation. Only the records and bytes totals are kept —
// the full-page-records count is implicit in the buffer-dirtied stat we
// already track.
func extractWALUsage(msg string) (records, bytes int64, ok bool) {
	idx := strings.Index(msg, "WAL usage: ")
	if idx == -1 {
		return 0, 0, false
	}
	rest := msg[idx+len("WAL usage: "):]
	records = parseAfterKeyword(rest, "records=")
	bytes = parseAfterKeyword(rest, "bytes=")
	if records == 0 && bytes == 0 {
		return 0, 0, false
	}
	return records, bytes, true
}

// parseLeadingInt64 reads the leading integer from s, stopping at the
// first byte of stopMarker. Returns 0 when the marker is not found or
// the integer cannot be parsed.
func parseLeadingInt64(s, stopMarker string) int64 {
	end := strings.Index(s, stopMarker)
	if end == -1 {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s[:end]), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseAfterKeyword finds keyword in s and reads the integer that
// follows (skipping any "=" / spaces between the keyword and the
// number). Tolerates both PG's "X hits" form (number-before-keyword)
// when the caller anchors keyword on a leading space — for those, the
// number is read from the bytes preceding keyword.
func parseAfterKeyword(s, keyword string) int64 {
	idx := strings.Index(s, keyword)
	if idx == -1 {
		return 0
	}
	// "X hits" / " misses" forms (keyword starts with space): read
	// digits immediately preceding the keyword.
	if len(keyword) > 0 && keyword[0] == ' ' {
		end := idx
		start := end
		for start > 0 {
			c := s[start-1]
			if c >= '0' && c <= '9' {
				start--
				continue
			}
			break
		}
		if start == end {
			return 0
		}
		v, err := strconv.ParseInt(s[start:end], 10, 64)
		if err != nil {
			return 0
		}
		return v
	}
	// "records=" / "bytes=" forms: read digits immediately after the
	// keyword.
	rest := s[idx+len(keyword):]
	end := 0
	for end < len(rest) {
		c := rest[end]
		if c >= '0' && c <= '9' {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return 0
	}
	v, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// extractRemovedPages retrieves the number of removed pages from a vacuum log message.
//
// Expected format: "pages: 123 removed, 456 remain"
// The function extracts the number after "pages: " and before " removed".
//
// Returns 0 if the removed pages count cannot be extracted.
func extractRemovedPages(logMsg string) int64 {
	// Find "pages: " marker
	idx := strings.Index(logMsg, pagesRemovedKey)
	if idx == -1 {
		return 0
	}

	// Move past "pages: " marker
	start := idx + len(pagesRemovedKey)
	if start >= len(logMsg) {
		return 0
	}

	// Find " removed" or next space
	sub := logMsg[start:]
	spaceIdx := strings.Index(sub, " ")
	if spaceIdx == -1 {
		return 0
	}

	// Parse the number
	numStr := sub[:spaceIdx]
	removedPages, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0
	}

	return removedPages
}
