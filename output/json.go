package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// JSON output structures, one per metrics section: SummaryJSON,
// SQLPerformanceJSON, TempFilesJSON, MaintenanceJSON, CheckpointsJSON,
// ConnectionsJSON, ClientsJSON, etc.
type SummaryJSON struct {
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	Duration     string `json:"duration"`
	TotalLogs    int    `json:"total_logs"`
	Throughput   string `json:"throughput"`
	ErrorCount   int    `json:"error_count"`
	FatalCount   int    `json:"fatal_count"`
	PanicCount   int    `json:"panic_count"`
	WarningCount int    `json:"warning_count"`
	LogCount     int    `json:"log_count"`
}

type SQLPerformanceJSON struct {
	TotalQueryDuration     string          `json:"total_query_duration"`
	TotalQueriesParsed     int             `json:"total_queries_parsed"`
	TotalUniqueQueries     int             `json:"total_unique_queries"`
	Top1PercentSlowQueries int             `json:"top_1_percent_slow_queries"`
	QueryMaxDuration       string          `json:"query_max_duration"`
	QueryMinDuration       string          `json:"query_min_duration"`
	QueryMedianDuration    string          `json:"query_median_duration"`
	Query99thPercentile    string          `json:"query_99th_percentile"`
	Executions             lazyExecutions  `json:"executions"`
	Queries                []QueryStatJSON `json:"queries"`
}

type QueryExecutionJSON struct {
	Timestamp  string  `json:"timestamp"`
	DurationMs float64 `json:"duration_ms"`
	QueryID    string  `json:"query_id"`
}

type QueryStatJSON struct {
	ID              string          `json:"id"`
	NormalizedQuery string          `json:"normalized_query"`
	RawQuery        string          `json:"raw_query"`
	Type            string          `json:"type"`
	Count           int             `json:"count"`
	TotalTime       float64         `json:"total_time_ms"`
	AvgTime         float64         `json:"avg_time_ms"`
	MaxTime         float64         `json:"max_time_ms"`
	PreparedNames   []string        `json:"prepared_names,omitempty"`
	SlowestRun      *SlowestRunJSON `json:"slowest_run,omitempty"`
	Plan            string          `json:"plan,omitempty"`
	// Top-N (db/user/app/host) that executed this query — surfaced
	// in the HTML modal's DIMENSIONS block. Populated in a single
	// pass over the executions storage by TopDimensionsByQuery.
	TopDatabases []NamedCountJSON `json:"top_databases,omitempty"`
	TopUsers     []NamedCountJSON `json:"top_users,omitempty"`
	TopApps      []NamedCountJSON `json:"top_apps,omitempty"`
	TopHosts     []NamedCountJSON `json:"top_hosts,omitempty"`
}

// SlowestRunJSON exposes the parameter values of the slowest observed
// execution (extended protocol, "DETAIL: parameters:" pairing).
type SlowestRunJSON struct {
	DurationMs      float64 `json:"duration_ms"`
	Timestamp       string  `json:"timestamp"`
	PID             string  `json:"pid"`
	Parameters      string  `json:"parameters"`
	QueryWithParams string  `json:"query_with_params"`
	// Client identity behind the slowest run. Empty when the source
	// log line did not carry the corresponding prefix field.
	Database string `json:"database,omitempty"`
	User     string `json:"user,omitempty"`
	App      string `json:"app,omitempty"`
	Host     string `json:"host,omitempty"`
}

// slowestRunJSON builds a SlowestRunJSON from a QueryStat, returning nil
// when no slowest-run information was paired with the query.
func slowestRunJSON(s *analysis.QueryStat) *SlowestRunJSON {
	if s == nil || s.SlowestRun == nil {
		return nil
	}
	sr := s.SlowestRun
	return &SlowestRunJSON{
		DurationMs:      sr.DurationMs,
		Timestamp:       sr.Timestamp.Format("2006-01-02T15:04:05"),
		PID:             sr.PID,
		Parameters:      sr.Parameters,
		QueryWithParams: SubstituteParameters(s.RawQuery, sr.Parameters),
		Database:        sr.Database,
		User:            sr.User,
		App:             sr.App,
		Host:            sr.Host,
	}
}

// NamedCountJSON is the JSON shape of one DimensionCount row. Used in
// SQLDetailJSON.TopDatabases/Users/Apps/Hosts.
type NamedCountJSON struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// dimensionCountsJSON converts a slice of analysis.DimensionCount into
// the JSON-friendly form. Returns nil when the source is empty so the
// caller can rely on omitempty to keep the wire shape stable.
func dimensionCountsJSON(rows []analysis.DimensionCount) []NamedCountJSON {
	if len(rows) == 0 {
		return nil
	}
	out := make([]NamedCountJSON, len(rows))
	for i, r := range rows {
		out[i] = NamedCountJSON{Name: r.Name, Count: r.Count}
	}
	return out
}

// SQL Overview JSON structures (for --sql-overview --json)

type SQLOverviewJSON struct {
	TotalQueries int                      `json:"total_queries"`
	Categories   []CategoryStatJSON       `json:"categories"`
	Types        []TypeStatJSON           `json:"types"`
	ByDatabase   []DimensionBreakdownJSON `json:"by_database,omitempty"`
	ByUser       []DimensionBreakdownJSON `json:"by_user,omitempty"`
	ByHost       []DimensionBreakdownJSON `json:"by_host,omitempty"`
	ByApp        []DimensionBreakdownJSON `json:"by_app,omitempty"`
}

type CategoryStatJSON struct {
	Category   string  `json:"category"`
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
	TotalTime  string  `json:"total_time"`
}

type TypeStatJSON struct {
	Type       string  `json:"type"`
	Category   string  `json:"category"`
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
	TotalTime  string  `json:"total_time"`
	AvgTime    string  `json:"avg_time"`
	MaxTime    string  `json:"max_time"`
}

type DimensionBreakdownJSON struct {
	Name       string               `json:"name"`
	Count      int                  `json:"count"`
	TotalTime  string               `json:"total_time"`
	QueryTypes []QueryTypeCountJSON `json:"query_types"`
}

type QueryTypeCountJSON struct {
	Type      string `json:"type"`
	Count     int    `json:"count"`
	TotalTime string `json:"total_time"`
}

// SQL Detail JSON structures (for --sql-detail --json)

type SQLDetailJSON struct {
	ID              string   `json:"id"`
	NormalizedQuery string   `json:"normalized_query"`
	RawQuery        string   `json:"raw_query,omitempty"`
	Type            string   `json:"type"`
	Category        string   `json:"category"`
	PreparedNames   []string `json:"prepared_names,omitempty"`
	// Events promoted right after the identification block so the
	// operational signal sits at the top of the JSON document, mirror
	// of the text / markdown / HTML placement.
	Events     []SQLDetailEventJSON  `json:"events,omitempty"`
	Statistics *QueryDetailStatsJSON `json:"statistics,omitempty"`
	Executions lazyExecutions        `json:"executions,omitempty"`
	// Top-N (db/user/app/host) that ran this query. Populated by
	// ExportSQLDetailJSON via SQLMetrics.TopDimensionsForID.
	TopDatabases []NamedCountJSON    `json:"top_databases,omitempty"`
	TopUsers     []NamedCountJSON    `json:"top_users,omitempty"`
	TopApps      []NamedCountJSON    `json:"top_apps,omitempty"`
	TopHosts     []NamedCountJSON    `json:"top_hosts,omitempty"`
	TempFiles    *QueryTempFilesJSON `json:"temp_files,omitempty"`
	Locks        *QueryLocksJSON     `json:"locks,omitempty"`
	SlowestRun   *SlowestRunJSON     `json:"slowest_run,omitempty"`
	Plan         string              `json:"plan,omitempty"`
}

// SQLDetailEventJSON is one row of the EVENTS section in the
// --sql-detail JSON output. event_id is shared with top_events[].id so
// downstream tooling can join the two and look up the pattern's total
// count on the events side without us duplicating it here.
type SQLDetailEventJSON struct {
	EventID      string `json:"event_id"`
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	TriggerCount int    `json:"triggered_by_query"`
}

type QueryDetailStatsJSON struct {
	Count     int    `json:"count"`
	TotalTime string `json:"total_time"`
	AvgTime   string `json:"avg_time"`
	MaxTime   string `json:"max_time"`
}

type QueryTempFilesJSON struct {
	Count     int    `json:"count"`
	TotalSize string `json:"total_size"`
}

type QueryLocksJSON struct {
	AcquiredCount    int    `json:"acquired_count"`
	AcquiredWaitTime string `json:"acquired_wait_time"`
	WaitingCount     int    `json:"waiting_count"`
	WaitingTime      string `json:"waiting_time"`
	TotalWaitTime    string `json:"total_wait_time"`
}

// SQL Performance JSON structures (for --sql-performance --json)

type SQLPerformanceDetailJSON struct {
	// Summary statistics
	TotalQueryDuration  string `json:"total_query_duration"`
	TotalQueriesParsed  int    `json:"total_queries_parsed"`
	TotalUniqueQueries  int    `json:"total_unique_queries"`
	Top1PercentSlow     int    `json:"top_1_percent_slow_queries"`
	QueryMaxDuration    string `json:"query_max_duration"`
	QueryMinDuration    string `json:"query_min_duration"`
	QueryMedianDuration string `json:"query_median_duration"`
	Query99thPercentile string `json:"query_99th_percentile"`

	// Duration distribution histogram
	DurationDistribution []DurationBucketJSON `json:"duration_distribution"`

	// Top queries by different criteria
	SlowestQueries      []QueryRankJSON `json:"slowest_queries"`
	MostFrequentQueries []QueryRankJSON `json:"most_frequent_queries"`
	MostTimeConsuming   []QueryRankJSON `json:"most_time_consuming"`

	// Full query data for HTML viewer
	Queries    []QueryStatJSON `json:"queries,omitempty"`
	Executions lazyExecutions  `json:"executions,omitempty"`
}

type DurationBucketJSON struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

type QueryRankJSON struct {
	ID              string `json:"id"`
	NormalizedQuery string `json:"normalized_query"`
	Count           int    `json:"count"`
	TotalTime       string `json:"total_time"`
	AvgTime         string `json:"avg_time"`
	MaxTime         string `json:"max_time"`
}

type TempFilesJSON struct {
	TotalMessages int                     `json:"total_messages"`
	TotalSize     string                  `json:"total_size"`
	AvgSize       string                  `json:"avg_size"`
	MaxSize       string                  `json:"max_size"`
	Events        lazyTempFileEvents      `json:"events"`
	Queries       []TempFileQueryStatJSON `json:"queries,omitempty"`
}

type TempFileEventJSON struct {
	Timestamp string `json:"timestamp"`
	Size      string `json:"size"`
	QueryID   string `json:"query_id,omitempty"`
}

type TempFileQueryStatJSON struct {
	ID              string `json:"id"`
	NormalizedQuery string `json:"normalized_query"`
	RawQuery        string `json:"raw_query"`
	Count           int    `json:"count"`
	TotalSize       string `json:"total_size"`
	MinSize         string `json:"min_size"`
	MaxSize         string `json:"max_size"`
	AvgSize         string `json:"avg_size"`
}

type MaintenanceJSON struct {
	VacuumCount           int               `json:"vacuum_count"`
	AggressiveVacuumCount int               `json:"aggressive_vacuum_count"`
	AnalyzeCount          int               `json:"analyze_count"`
	VacuumTableCounts     map[string]int    `json:"vacuum_table_counts"`
	AnalyzeTableCounts    map[string]int    `json:"analyze_table_counts"`
	VacuumSpaceRecovered  map[string]string `json:"vacuum_space_recovered"`

	// Continuation-line aggregates surfaced from PG's autovacuum log
	// blocks. Every field is omitempty so logs that never carry the
	// continuation lines (older PG versions, log_autovacuum_min_duration
	// off, …) keep the same JSON shape they always had.
	TotalVacuumElapsedSeconds  float64               `json:"total_vacuum_elapsed_seconds,omitempty"`
	TotalTuplesRemoved         int64                 `json:"total_tuples_removed,omitempty"`
	TotalTuplesNotYetRemovable int64                 `json:"total_tuples_not_yet_removable,omitempty"`
	TotalBufferHits            int64                 `json:"total_buffer_hits,omitempty"`
	TotalBufferMisses          int64                 `json:"total_buffer_misses,omitempty"`
	TotalBufferDirtied         int64                 `json:"total_buffer_dirtied,omitempty"`
	TotalBufferWritten         int64                 `json:"total_buffer_written,omitempty"`
	TotalWALRecords            int64                 `json:"total_wal_records,omitempty"`
	TotalWALBytes              int64                 `json:"total_wal_bytes,omitempty"`
	TopVacuumTables            []VacuumTableStatJSON `json:"top_vacuum_tables,omitempty"`
	XminBlockedTables          []VacuumTableStatJSON `json:"xmin_blocked_tables,omitempty"`
	SlowestVacuum              *VacuumSampleJSON     `json:"slowest_vacuum,omitempty"`

	// Autoanalyze aggregates parsed from the system-usage continuation
	// line PostgreSQL emits after autoanalyze blocks. analyze stats are
	// kept distinct from vacuum stats because the underlying log block
	// only carries elapsed (no buffer / WAL / tuples), so consumers can
	// keep their renderers shape-symmetric with the vacuum side.
	TotalAnalyzeElapsedSeconds float64               `json:"total_analyze_elapsed_seconds,omitempty"`
	TopAnalyzeTablesByElapsed  []VacuumTableStatJSON `json:"top_analyze_tables_by_elapsed,omitempty"`
}

// VacuumTableStatJSON is the per-table aggregate exposed in the
// top-N table lists of the maintenance section.
type VacuumTableStatJSON struct {
	Table                 string  `json:"table"`
	VacuumCount           int     `json:"vacuum_count"`
	TotalElapsedSeconds   float64 `json:"total_elapsed_seconds"`
	MaxElapsedSeconds     float64 `json:"max_elapsed_seconds,omitempty"`
	TuplesRemoved         int64   `json:"tuples_removed,omitempty"`
	TuplesNotYetRemovable int64   `json:"tuples_not_yet_removable,omitempty"`
	BufferHits            int64   `json:"buffer_hits,omitempty"`
	BufferMisses          int64   `json:"buffer_misses,omitempty"`
	BufferDirtied         int64   `json:"buffer_dirtied,omitempty"`
	BufferWritten         int64   `json:"buffer_written,omitempty"`
	WALRecords            int64   `json:"wal_records,omitempty"`
	WALBytes              int64   `json:"wal_bytes,omitempty"`
}

// VacuumSampleJSON captures the worst-elapsed single autovacuum
// observation, surfaced as the headline "anomaly" of the maintenance
// section when the log carries continuation lines.
type VacuumSampleJSON struct {
	Table                 string  `json:"table"`
	Timestamp             string  `json:"timestamp,omitempty"`
	ElapsedSeconds        float64 `json:"elapsed_seconds"`
	PagesRemoved          int64   `json:"pages_removed,omitempty"`
	TuplesRemoved         int64   `json:"tuples_removed,omitempty"`
	TuplesNotYetRemovable int64   `json:"tuples_not_yet_removable,omitempty"`
}

type LocksJSON struct {
	TotalEvents       int                 `json:"total_events"`
	WaitingEvents     int                 `json:"waiting_events"`
	AcquiredEvents    int                 `json:"acquired_events"`
	DeadlockEvents    int                 `json:"deadlock_events,omitempty"`
	TotalWaitTime     string              `json:"total_wait_time"`
	AvgWaitTime       string              `json:"avg_wait_time"`
	LockTypeStats     map[string]int      `json:"lock_type_stats"`
	ResourceTypeStats map[string]int      `json:"resource_type_stats"`
	RelationStats     map[string]int      `json:"relation_stats,omitempty"`
	Events            lazyLockEvents      `json:"events"`
	Queries           []LockQueryStatJSON `json:"queries,omitempty"`
}

type LockEventJSON struct {
	Timestamp       string `json:"timestamp"`
	EventType       string `json:"event_type"`
	LockType        string `json:"lock_type,omitempty"`
	ResourceType    string `json:"resource_type,omitempty"`
	WaitTime        string `json:"wait_time,omitempty"`
	ProcessID       string `json:"process_id"`
	QueryID         string `json:"query_id,omitempty"`
	BlockingPID     string `json:"blocking_pid,omitempty"`
	BlockingQueryID string `json:"blocking_query_id,omitempty"`
	BlockingQuery   string `json:"blocking_query,omitempty"`
	Relation        string `json:"relation,omitempty"`
}

type LockQueryStatJSON struct {
	ID                string `json:"id"`
	NormalizedQuery   string `json:"normalized_query"`
	RawQuery          string `json:"raw_query"`
	AcquiredCount     int    `json:"acquired_count"`
	AcquiredWaitTime  string `json:"acquired_wait_time"`
	StillWaitingCount int    `json:"still_waiting_count"`
	StillWaitingTime  string `json:"still_waiting_time"`
	TotalWaitTime     string `json:"total_wait_time"`
}

type CheckpointTypeJSON struct {
	Count      int      `json:"count"`
	Percentage float64  `json:"percentage"`
	Rate       float64  `json:"rate_per_hour"`
	Events     []string `json:"events"`
}

type WALDistanceJSON struct {
	Timestamp  string `json:"timestamp"`
	DistanceKB int64  `json:"distance_kb"`
	EstimateKB int64  `json:"estimate_kb"`
}

type CheckpointsJSON struct {
	TotalCheckpoints  int                           `json:"total_checkpoints"`
	AvgCheckpointTime string                        `json:"avg_checkpoint_time"`
	MaxCheckpointTime string                        `json:"max_checkpoint_time"`
	Events            []string                      `json:"events"`
	Types             map[string]CheckpointTypeJSON `json:"types,omitempty"`

	AvgWALDistance      string            `json:"avg_wal_distance,omitempty"`
	MaxWALDistance      string            `json:"max_wal_distance,omitempty"`
	TotalBuffersWritten int64             `json:"total_buffers_written,omitempty"`
	WALRate             string            `json:"wal_rate,omitempty"`
	FlushRate           string            `json:"flush_rate,omitempty"`
	WALDistances        []WALDistanceJSON `json:"wal_distances,omitempty"`

	WarningCount              int      `json:"warning_count,omitempty"`
	WarningMinIntervalSeconds int      `json:"warning_min_interval_seconds,omitempty"`
	WarningMaxIntervalSeconds int      `json:"warning_max_interval_seconds,omitempty"`
	WarningEvents             []string `json:"warning_events,omitempty"`
}

// ServerJSON exposes server lifecycle metrics in the JSON output.
// Counters are not omitempty (a zero is meaningful), slices and the
// nested maps use omitempty so absent markers do not bloat the output.
type ServerJSON struct {
	Starts     int      `json:"starts"`
	StartTimes []string `json:"start_times,omitempty"`

	Reloads     int      `json:"reloads"`
	ReloadTimes []string `json:"reload_times,omitempty"`

	ShutdownsFast      int `json:"shutdowns_fast"`
	ShutdownsImmediate int `json:"shutdowns_immediate"`
	ShutdownsSmart     int `json:"shutdowns_smart"`

	ShutdownCompleted int `json:"shutdown_completed,omitempty"`
	CrashRecoveries   int `json:"crash_recoveries,omitempty"`
	Interrupted       int `json:"interrupted,omitempty"`

	BackendCrashes int            `json:"backend_crashes,omitempty"`
	SignalCounts   map[string]int `json:"signal_counts,omitempty"`

	AuxProcessExits int `json:"auxiliary_process_exits,omitempty"`

	ParameterChanges []ServerParameterChangeJSON `json:"parameter_changes,omitempty"`
	Timeline         []ServerTimelineEventJSON   `json:"timeline,omitempty"`
}

// ServerParameterChangeJSON is one row in the "parameter changes" table.
// Old is empty for SIGHUP-driven changes (PG does not log the previous
// value) but the field is kept so downstream tools see a consistent
// schema.
type ServerParameterChangeJSON struct {
	Parameter string `json:"parameter"`
	Old       string `json:"old,omitempty"`
	New       string `json:"new"`
	Timestamp string `json:"timestamp"`
}

// ServerTimelineEventJSON is one event in the compact timeline.
type ServerTimelineEventJSON struct {
	Timestamp string `json:"timestamp"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail"`
}

// ReplicationEventJSON is one captured replication marker hit.
type ReplicationEventJSON struct {
	Timestamp string `json:"timestamp"`
	Marker    string `json:"marker"`
	Severity  string `json:"severity,omitempty"`
}

// ReplicationConflictQueryJSON aggregates per-query stats for queries
// killed by a recovery conflict (terminate or cancel).
type ReplicationConflictQueryJSON struct {
	ID              string `json:"id"`
	NormalizedQuery string `json:"normalized_query"`
	RawQuery        string `json:"raw_query"`
	Count           int    `json:"count"`
}

// ReplicationJSON is the JSON shape of the REPLICATION section. The
// section is emitted with omitempty so logs without any replication
// markers keep their existing JSON shape.
type ReplicationJSON struct {
	TotalEvents             int                            `json:"total_events"`
	StreamReconnects        int                            `json:"stream_reconnects,omitempty"`
	RecoveryPauses          int                            `json:"recovery_pauses,omitempty"`
	RecoveryResumes         int                            `json:"recovery_resumes,omitempty"`
	ConflictsWithRecovery   int                            `json:"conflicts_with_recovery,omitempty"`
	InvalidatedSlots        int                            `json:"invalidated_slots,omitempty"`
	ReplicationTerminations int                            `json:"replication_terminations,omitempty"`
	LastTermination         string                         `json:"last_termination,omitempty"`
	PeakHourLabel           string                         `json:"peak_hour_label,omitempty"`
	PeakHourCount           int                            `json:"peak_hour_count,omitempty"`
	Markers                 map[string]int                 `json:"markers"`
	HourCounts              map[string]int                 `json:"hour_counts,omitempty"`
	ConflictQueries         []ReplicationConflictQueryJSON `json:"conflict_queries,omitempty"`
	Events                  []ReplicationEventJSON         `json:"events,omitempty"`
}

type SessionStatsJSON struct {
	Count     int    `json:"count"`
	Min       string `json:"min_duration"`
	Max       string `json:"max_duration"`
	Avg       string `json:"avg_duration"`
	Median    string `json:"median_duration"`
	Cumulated string `json:"cumulated_duration"`
}

type ConnectionsJSON struct {
	ConnectionCount       int    `json:"connection_count"`
	AvgConnectionsPerHour string `json:"avg_connections_per_hour"`
	DisconnectionCount    int    `json:"disconnection_count"`
	AvgSessionTime        string `json:"avg_session_time"`

	// Session statistics
	SessionStats        *SessionStatsJSON `json:"session_stats,omitempty"`
	SessionDistribution map[string]int    `json:"session_distribution,omitempty"`

	// Breakdown by entity
	SessionsByUser     map[string]SessionStatsJSON `json:"sessions_by_user,omitempty"`
	SessionsByDatabase map[string]SessionStatsJSON `json:"sessions_by_database,omitempty"`
	SessionsByHost     map[string]SessionStatsJSON `json:"sessions_by_host,omitempty"`

	// Concurrent sessions
	PeakConcurrent     int    `json:"peak_concurrent_sessions,omitempty"`
	PeakConcurrentTime string `json:"peak_concurrent_timestamp,omitempty"`

	// Raw events
	Connections   lazyConnections   `json:"connections"`
	SessionEvents lazySessionEvents `json:"session_events,omitempty"`
}

// lazySessionEvents marshals session events directly to JSON without
// allocating an intermediate []SessionEventJSON slice. On large logs
// the intermediate slice was the dominant transient cost (~400 MB for
// 5.7M sessions on J.log) and required ~2M heap allocations.
//
// Source can be either a *analysis.ConnectionMetrics (chunked storage,
// no full-slice materialization) or a flat []analysis.SessionEvent
// (legacy / test paths). The metrics backing avoids materializing the
// 273 MB API-typed slice on J.log entirely; the iterator walks the
// internal compact chunks.
type lazySessionEvents struct {
	metrics *analysis.ConnectionMetrics
	events  []analysis.SessionEvent
}

// iterate yields each session event in order from whichever backing is set.
func (l lazySessionEvents) iterate(fn func(analysis.SessionEvent) bool) {
	if l.metrics != nil {
		l.metrics.IterateSessionEvents(fn)
		return
	}
	for _, e := range l.events {
		if !fn(e) {
			return
		}
	}
}

// count returns the total number of session events in either backing.
func (l lazySessionEvents) count() int {
	if l.metrics != nil {
		return l.metrics.SessionEventsCount()
	}
	return len(l.events)
}

// lazyConnections marshals received-connection timestamps directly to
// JSON without an intermediate []string slice. Same dual-backing scheme
// as lazySessionEvents — *ConnectionMetrics for chunked storage,
// []time.Time for legacy/test paths.
type lazyConnections struct {
	metrics    *analysis.ConnectionMetrics
	timestamps []time.Time
}

// iterate yields each connection timestamp in order from whichever backing is set.
func (l lazyConnections) iterate(fn func(time.Time) bool) {
	if l.metrics != nil {
		l.metrics.IterateConnections(fn)
		return
	}
	for _, t := range l.timestamps {
		if !fn(t) {
			return
		}
	}
}

// count returns the total number of timestamps in either backing.
func (l lazyConnections) count() int {
	if l.metrics != nil {
		return l.metrics.ConnectionsCount()
	}
	return len(l.timestamps)
}

// lazyExecutions marshals an []analysis.QueryExecution directly to JSON
// without an intermediate []QueryExecutionJSON slice. Biggest per-row
// gain: 5M+ executions on J.log = ~300 MB intermediate avoided.
//
// tsFormat lets the same wrapper serve the two contexts that differ
// only in timestamp separator: " " for the legacy --json output, "T"
// for the --full / sql_performance detail output (RFC3339-ish).
type lazyExecutions struct {
	// Source of executions — at most one is set:
	//   metrics: full SQLMetrics, iterated via metrics.IterateExecutions.
	//           Avoids materializing a []QueryExecution slice on hot
	//           paths with tens of millions of events.
	//   executions: pre-filtered slice (e.g. one query's events for
	//           --sql-detail, where the slice is bounded and small).
	metrics    *analysis.SQLMetrics
	executions []analysis.QueryExecution
	tsFormat   string
}

// iterate yields each execution event in order. Used by streamExecutionsJSON
// and the StreamSection helpers — avoids forcing callers to know which
// source backing is in use.
func (l lazyExecutions) iterate(fn func(analysis.QueryExecution) bool) {
	if l.metrics != nil {
		l.metrics.IterateExecutions(fn)
		return
	}
	for _, e := range l.executions {
		if !fn(e) {
			return
		}
	}
}

// count returns the number of execution events in either source.
func (l lazyExecutions) count() int {
	if l.metrics != nil {
		return l.metrics.ExecutionCount()
	}
	return len(l.executions)
}

// execRow is the minimal projection of a QueryExecution needed to emit the
// executions JSON array. Materializing three fields instead of the full
// QueryExecution roughly halves the transient footprint when the array is
// sorted for deterministic output (see sortedRows).
type execRow struct {
	ts  time.Time
	dur float64
	qid string
}

// needsSort reports whether the executions must be materialized and sorted
// before emitting. True for the bounded sql-detail slice (cheap) and for a
// metrics source folded from several PID shards; false for a single-pass /
// function-parallel run, whose events are already in stream order and stream
// out directly — no sort buffer (which matters for the leaking-GC WASM build).
func (l lazyExecutions) needsSort() bool {
	if l.metrics != nil {
		return l.metrics.ExecutionsNeedSort()
	}
	return true
}

// emit yields each execution's (timestamp, duration, query id) to fn in the
// order it should be serialized: sorted when needsSort, otherwise streamed
// straight from the compact store with no intermediate allocation.
func (l lazyExecutions) emit(fn func(ts time.Time, dur float64, qid string)) {
	if l.needsSort() {
		for _, r := range l.sortedRows() {
			fn(r.ts, r.dur, r.qid)
		}
		return
	}
	l.iterate(func(e analysis.QueryExecution) bool {
		fn(e.Timestamp, e.Duration, e.QueryID)
		return true
	})
}

// sortedRows materializes every execution ordered by (timestamp, query_id,
// duration). The compact executions store appends in stream order within a
// shard, so a PID-sharded analyzer's fold concatenates per-shard runs in
// shard order; sorting restores a single canonical order identical
// regardless of the shard count. The key need not be a strict total order:
// rows tying on all three fields serialize byte-for-byte the same, so their
// relative order is immaterial. Cost is one O(n log n) sort plus a
// three-field copy per event, paid only when an executions array actually
// needs reordering (see needsSort).
func (l lazyExecutions) sortedRows() []execRow {
	n := l.count()
	if n == 0 {
		return nil
	}
	rows := make([]execRow, 0, n)
	l.iterate(func(e analysis.QueryExecution) bool {
		rows = append(rows, execRow{ts: e.Timestamp, dur: e.Duration, qid: e.QueryID})
		return true
	})
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.ts.Equal(b.ts) {
			return a.ts.Before(b.ts)
		}
		if a.qid != b.qid {
			return a.qid < b.qid
		}
		return a.dur < b.dur
	})
	return rows
}

// lazyLockEvents marshals an []analysis.LockEvent directly to JSON
// without an intermediate []LockEventJSON slice. Per-row gain is
// modest (16k events on J.log = ~4 MB) but pathological lock-storm
// logs can grow to millions; the wrapper costs nothing when small.
type lazyLockEvents struct {
	events []analysis.LockEvent
}

// lazyTempFileEvents marshals an []analysis.TempFileEvent directly to
// JSON without an intermediate []TempFileEventJSON slice.
type lazyTempFileEvents struct {
	events []analysis.TempFileEvent
}

// MarshalJSON emits the JSON array of {"s":..,"e":..} objects. Returns
// `null` for empty so the encoder honors `omitempty` on the field tag.
//
// Uses time.Time.AppendFormat into a flat []byte instead of bytes.Buffer
// + Format(): zero intermediate string allocation per event.
func (l lazySessionEvents) MarshalJSON() ([]byte, error) {
	n := l.count()
	if n == 0 {
		return []byte("null"), nil
	}
	// Each event ≈ 52 bytes (`{"s":"...","e":"..."}`). +2 brackets.
	buf := make([]byte, 0, n*52+2)
	buf = append(buf, '[')
	first := true
	l.iterate(func(se analysis.SessionEvent) bool {
		if se.StartTime.IsZero() || se.EndTime.IsZero() {
			return true
		}
		if !first {
			buf = append(buf, ',')
		}
		first = false
		buf = append(buf, `{"s":"`...)
		buf = se.StartTime.AppendFormat(buf, "2006-01-02T15:04:05")
		buf = append(buf, `","e":"`...)
		buf = se.EndTime.AppendFormat(buf, "2006-01-02T15:04:05")
		buf = append(buf, `"}`...)
		return true
	})
	buf = append(buf, ']')
	return buf, nil
}

// MarshalJSON for lazyConnections — array of "YYYY-MM-DD HH:MM:SS" strings.
// Empty input → "[]" (the field tag has no omitempty).
func (l lazyConnections) MarshalJSON() ([]byte, error) {
	n := l.count()
	if n == 0 {
		return []byte("[]"), nil
	}
	// Each timestamp ≈ 22 bytes (`"2006-01-02 15:04:05",`). +2 brackets.
	buf := make([]byte, 0, n*22+2)
	buf = append(buf, '[')
	first := true
	l.iterate(func(t time.Time) bool {
		if !first {
			buf = append(buf, ',')
		}
		first = false
		buf = append(buf, '"')
		buf = t.AppendFormat(buf, "2006-01-02 15:04:05")
		buf = append(buf, '"')
		return true
	})
	buf = append(buf, ']')
	return buf, nil
}

// fieldEmitter writes JSON object fields one at a time to bw with the
// proper indent and comma framing — like a tiny manual encoder. Lets
// section streamers emit each small field via json.Marshal (for the
// value alone, kilobytes max) then call a stream helper for the big
// arrays without ever buffering the section as a whole.
type fieldEmitter struct {
	bw      *bufio.Writer
	inner   string // leading indent for each field line (depth-N)
	compact bool
	first   bool
}

// writeKey emits the comma+newline+indent prefix and the "key": part.
func (e *fieldEmitter) writeKey(key string) {
	if !e.first {
		e.bw.WriteByte(',')
	}
	e.first = false
	if !e.compact {
		e.bw.WriteByte('\n')
		e.bw.WriteString(e.inner)
	}
	e.bw.WriteString(strconv.Quote(key))
	if e.compact {
		e.bw.WriteByte(':')
	} else {
		e.bw.WriteString(": ")
	}
}

// emitScalar emits a small key:value pair using json.Marshal on the
// value (the marshaled bytes are kilobytes at most for scalars / small
// objects).
func (e *fieldEmitter) emitScalar(key string, value any) error {
	e.writeKey(key)
	var vb []byte
	var err error
	if e.compact {
		vb, err = json.Marshal(value)
	} else {
		vb, err = json.MarshalIndent(value, e.inner, "  ")
	}
	if err != nil {
		return err
	}
	e.bw.Write(vb)
	return nil
}

// streamTimestampsJSON writes connection timestamps as a JSON array of
// "YYYY-MM-DD HH:MM:SS" strings directly to bw — one item at a time,
// no intermediate buffer. Peak memory = bufio buffer (~4 KB), regardless
// of how many connections are stored. Source can be either the chunked
// metrics or a flat slice (legacy / test paths).
func streamTimestampsJSON(bw *bufio.Writer, src lazyConnections, prefix, indent string, compact bool) {
	if src.count() == 0 {
		bw.WriteString("[]")
		return
	}
	inner := prefix + indent
	if compact {
		bw.WriteByte('[')
		first := true
		src.iterate(func(t time.Time) bool {
			if !first {
				bw.WriteByte(',')
			}
			first = false
			bw.WriteByte('"')
			var buf [20]byte
			bw.Write(t.AppendFormat(buf[:0], "2006-01-02 15:04:05"))
			bw.WriteByte('"')
			return true
		})
		bw.WriteByte(']')
		return
	}
	bw.WriteString("[\n")
	first := true
	src.iterate(func(t time.Time) bool {
		if !first {
			bw.WriteString(",\n")
		}
		first = false
		bw.WriteString(inner)
		bw.WriteByte('"')
		var buf [20]byte
		bw.Write(t.AppendFormat(buf[:0], "2006-01-02 15:04:05"))
		bw.WriteByte('"')
		return true
	})
	bw.WriteByte('\n')
	bw.WriteString(prefix)
	bw.WriteByte(']')
}

// streamSessionEventsJSON writes session events as a JSON array of
// {"s":..,"e":..} objects directly to bw. Same zero-buffer streaming as
// streamTimestampsJSON; iterates either the chunked metrics or a flat
// slice through the lazySessionEvents wrapper.
func streamSessionEventsJSON(bw *bufio.Writer, src lazySessionEvents, prefix, indent string, compact bool) {
	if src.count() == 0 {
		bw.WriteString("[]")
		return
	}
	inner := prefix + indent
	if compact {
		bw.WriteByte('[')
		first := true
		src.iterate(func(se analysis.SessionEvent) bool {
			if se.StartTime.IsZero() || se.EndTime.IsZero() {
				return true
			}
			if !first {
				bw.WriteByte(',')
			}
			first = false
			bw.WriteString(`{"s":"`)
			var buf [20]byte
			bw.Write(se.StartTime.AppendFormat(buf[:0], "2006-01-02T15:04:05"))
			bw.WriteString(`","e":"`)
			bw.Write(se.EndTime.AppendFormat(buf[:0], "2006-01-02T15:04:05"))
			bw.WriteString(`"}`)
			return true
		})
		bw.WriteByte(']')
		return
	}
	bw.WriteString("[\n")
	first := true
	subInner := inner + indent
	src.iterate(func(se analysis.SessionEvent) bool {
		if se.StartTime.IsZero() || se.EndTime.IsZero() {
			return true
		}
		if !first {
			bw.WriteString(",\n")
		}
		first = false
		bw.WriteString(inner)
		bw.WriteString(`{`)
		bw.WriteByte('\n')
		bw.WriteString(subInner)
		bw.WriteString(`"s": "`)
		var buf [20]byte
		bw.Write(se.StartTime.AppendFormat(buf[:0], "2006-01-02T15:04:05"))
		bw.WriteString(`",`)
		bw.WriteByte('\n')
		bw.WriteString(subInner)
		bw.WriteString(`"e": "`)
		bw.Write(se.EndTime.AppendFormat(buf[:0], "2006-01-02T15:04:05"))
		bw.WriteString(`"`)
		bw.WriteByte('\n')
		bw.WriteString(inner)
		bw.WriteByte('}')
		return true
	})
	bw.WriteByte('\n')
	bw.WriteString(prefix)
	bw.WriteByte(']')
}

// streamLockEventsJSON writes events as a JSON array of LockEventJSON
// objects directly to bw, one item at a time. BlockingQuery and
// Relation can contain SQL text with quotes/backslashes/newlines, so
// strings go through json.Marshal for proper escaping (small, per-call).
func streamLockEventsJSON(bw *bufio.Writer, events []analysis.LockEvent, prefix, indent string, compact bool) {
	if len(events) == 0 {
		bw.WriteString("[]")
		return
	}
	inner := prefix + indent
	subInner := inner + indent

	emitField := func(key, value string, omitempty bool) {
		if omitempty && value == "" {
			return
		}
		bw.WriteByte(',')
		if !compact {
			bw.WriteByte('\n')
			bw.WriteString(subInner)
		}
		bw.WriteString(strconv.Quote(key))
		if compact {
			bw.WriteByte(':')
		} else {
			bw.WriteString(": ")
		}
		vb, _ := json.Marshal(value)
		bw.Write(vb)
	}
	emitFirstField := func(key, value string) {
		if !compact {
			bw.WriteByte('\n')
			bw.WriteString(subInner)
		}
		bw.WriteString(strconv.Quote(key))
		if compact {
			bw.WriteByte(':')
		} else {
			bw.WriteString(": ")
		}
		vb, _ := json.Marshal(value)
		bw.Write(vb)
	}

	if compact {
		bw.WriteByte('[')
	} else {
		bw.WriteString("[\n")
	}
	for i, ev := range events {
		if i > 0 {
			if compact {
				bw.WriteByte(',')
			} else {
				bw.WriteString(",\n")
			}
		}
		if !compact {
			bw.WriteString(inner)
		}
		bw.WriteByte('{')

		var tbuf [20]byte
		ts := string(ev.Timestamp.AppendFormat(tbuf[:0], "2006-01-02 15:04:05"))
		emitFirstField("timestamp", ts)
		emitField("event_type", ev.EventType, false)
		emitField("lock_type", ev.LockType, true)
		emitField("resource_type", ev.ResourceType, true)
		waitTime := ""
		if ev.WaitTime > 0 {
			waitTime = formatQueryDuration(ev.WaitTime)
		}
		emitField("wait_time", waitTime, true)
		emitField("process_id", ev.ProcessID, false)
		emitField("query_id", ev.QueryID, true)
		emitField("blocking_pid", ev.BlockingPID, true)
		emitField("blocking_query_id", ev.BlockingQueryID, true)
		emitField("blocking_query", ev.BlockingQuery, true)
		emitField("relation", ev.Relation, true)

		if !compact {
			bw.WriteByte('\n')
			bw.WriteString(inner)
		}
		bw.WriteByte('}')
	}
	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte(']')
}

// streamTempFileEventsJSON writes events as a JSON array of
// TempFileEventJSON objects directly to bw.
func streamTempFileEventsJSON(bw *bufio.Writer, events []analysis.TempFileEvent, prefix, indent string, compact bool) {
	if len(events) == 0 {
		bw.WriteString("[]")
		return
	}
	inner := prefix + indent
	subInner := inner + indent

	if compact {
		bw.WriteByte('[')
	} else {
		bw.WriteString("[\n")
	}
	for i, ev := range events {
		if i > 0 {
			if compact {
				bw.WriteByte(',')
			} else {
				bw.WriteString(",\n")
			}
		}
		if !compact {
			bw.WriteString(inner)
		}
		bw.WriteByte('{')
		// timestamp
		if !compact {
			bw.WriteByte('\n')
			bw.WriteString(subInner)
		}
		bw.WriteString(`"timestamp"`)
		if compact {
			bw.WriteByte(':')
		} else {
			bw.WriteString(": ")
		}
		bw.WriteByte('"')
		var tbuf [20]byte
		bw.Write(ev.Timestamp.AppendFormat(tbuf[:0], "2006-01-02 15:04:05"))
		bw.WriteByte('"')
		// size
		if compact {
			bw.WriteString(`,"size":`)
		} else {
			bw.WriteString(",\n")
			bw.WriteString(subInner)
			bw.WriteString(`"size": `)
		}
		sb, _ := json.Marshal(FormatBytes(int64(ev.Size)))
		bw.Write(sb)
		// query_id (omitempty)
		if ev.QueryID != "" {
			if compact {
				bw.WriteString(`,"query_id":`)
			} else {
				bw.WriteString(",\n")
				bw.WriteString(subInner)
				bw.WriteString(`"query_id": `)
			}
			qb, _ := json.Marshal(ev.QueryID)
			bw.Write(qb)
		}
		if !compact {
			bw.WriteByte('\n')
			bw.WriteString(inner)
		}
		bw.WriteByte('}')
	}
	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte(']')
}

// MarshalJSON for lazyLockEvents — minimal "[]" or "null" so json.Marshal
// can be called on the wrapper independently. The streaming path goes
// through StreamSection on LocksJSON, not through this method.
func (l lazyLockEvents) MarshalJSON() ([]byte, error) {
	if len(l.events) == 0 {
		return []byte("[]"), nil
	}
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	streamLockEventsJSON(bw, l.events, "", "  ", false)
	bw.Flush()
	return buf.Bytes(), nil
}

// MarshalJSON for lazyTempFileEvents — same fallback pattern.
func (l lazyTempFileEvents) MarshalJSON() ([]byte, error) {
	if len(l.events) == 0 {
		return []byte("[]"), nil
	}
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	streamTempFileEventsJSON(bw, l.events, "", "  ", false)
	bw.Flush()
	return buf.Bytes(), nil
}

// streamQueriesJSON writes a []QueryStatJSON as a JSON array directly
// to bw, marshalling one item at a time to avoid the multi-GB buffer
// that json.MarshalIndent of the full slice produces on big corpora
// (Z: 26k queries × ~190 KB each → ~5 GB peak just for this one
// section). Per-item Marshal stays in the kilobyte range.
//
// Output is byte-identical to json.MarshalIndent(slice, prefix, indent)
// — same item indentation, same comma framing, same trailing layout.
// prefix is the indent of the array's closing ']' (matches the caller's
// e.inner); indent is the per-level indent unit.
func streamQueriesJSON(bw *bufio.Writer, queries []QueryStatJSON, prefix, indent string, compact bool) error {
	if len(queries) == 0 {
		bw.WriteString("[]")
		return nil
	}
	if compact {
		bw.WriteByte('[')
		for i, q := range queries {
			if i > 0 {
				bw.WriteByte(',')
			}
			qb, err := json.Marshal(q)
			if err != nil {
				return err
			}
			bw.Write(qb)
		}
		bw.WriteByte(']')
		return nil
	}
	inner := prefix + indent
	bw.WriteString("[\n")
	for i, q := range queries {
		if i > 0 {
			bw.WriteString(",\n")
		}
		bw.WriteString(inner)
		qb, err := json.MarshalIndent(q, inner, indent)
		if err != nil {
			return err
		}
		bw.Write(qb)
	}
	bw.WriteByte('\n')
	bw.WriteString(prefix)
	bw.WriteByte(']')
	return nil
}

// streamExecutionsJSON writes executions as a JSON array of
// {timestamp, duration_ms, query_id} objects directly to bw, pulling
// each event from the lazyExecutions iterator (compact storage on the
// SQLAnalyzer side, or a pre-filtered slice for the sql_detail case).
// No []QueryExecution is materialized here.
func streamExecutionsJSON(bw *bufio.Writer, src lazyExecutions, prefix, indent string, compact bool) {
	if src.count() == 0 {
		bw.WriteString("[]")
		return
	}
	tsFormat := src.tsFormat
	if tsFormat == "" {
		tsFormat = "2006-01-02 15:04:05"
	}
	inner := prefix + indent
	// Streamed in stream order, or sorted when folded from PID shards (see emit).
	if compact {
		bw.WriteByte('[')
		first := true
		src.emit(func(ts time.Time, dur float64, qid string) {
			if !first {
				bw.WriteByte(',')
			}
			first = false
			bw.WriteString(`{"timestamp":"`)
			var tbuf [20]byte
			bw.Write(ts.AppendFormat(tbuf[:0], tsFormat))
			bw.WriteString(`","duration_ms":`)
			var nbuf [32]byte
			bw.Write(strconv.AppendFloat(nbuf[:0], dur, 'f', -1, 64))
			bw.WriteString(`,"query_id":"`)
			bw.WriteString(qid)
			bw.WriteString(`"}`)
		})
		bw.WriteByte(']')
		return
	}
	subInner := inner + indent
	bw.WriteString("[\n")
	first := true
	src.emit(func(ts time.Time, dur float64, qid string) {
		if !first {
			bw.WriteString(",\n")
		}
		first = false
		bw.WriteString(inner)
		bw.WriteString("{\n")
		bw.WriteString(subInner)
		bw.WriteString(`"timestamp": "`)
		var tbuf [20]byte
		bw.Write(ts.AppendFormat(tbuf[:0], tsFormat))
		bw.WriteString(`",`)
		bw.WriteByte('\n')
		bw.WriteString(subInner)
		bw.WriteString(`"duration_ms": `)
		var nbuf [32]byte
		bw.Write(strconv.AppendFloat(nbuf[:0], dur, 'f', -1, 64))
		bw.WriteString(`,`)
		bw.WriteByte('\n')
		bw.WriteString(subInner)
		bw.WriteString(`"query_id": `)
		qb, _ := json.Marshal(qid)
		bw.Write(qb)
		bw.WriteByte('\n')
		bw.WriteString(inner)
		bw.WriteByte('}')
	})
	bw.WriteByte('\n')
	bw.WriteString(prefix)
	bw.WriteByte(']')
}

// MarshalJSON for lazyExecutions — array of {timestamp, duration_ms,
// query_id} objects. Returns "null" when empty (no omitempty on the
// field tag means the encoder will respect what we return).
func (l lazyExecutions) MarshalJSON() ([]byte, error) {
	n := l.count()
	if n == 0 {
		// Match the encoder default: a nil slice serializes to "null"
		// while an empty slice serializes to "[]". Preserve the latter
		// because the make([], len(...)) path always built an empty slice.
		return []byte("[]"), nil
	}
	// Each execution ≈ 90 bytes (`{"timestamp":"...","duration_ms":NNN.NNN,"query_id":"se-XXX"}`).
	buf := make([]byte, 0, n*90+2)
	buf = append(buf, '[')
	tsFormat := l.tsFormat
	if tsFormat == "" {
		tsFormat = "2006-01-02 15:04:05"
	}
	// Streamed in stream order, or sorted when folded from PID shards (see emit).
	first := true
	l.emit(func(ts time.Time, dur float64, qid string) {
		if !first {
			buf = append(buf, ',')
		}
		first = false
		buf = append(buf, `{"timestamp":"`...)
		buf = ts.AppendFormat(buf, tsFormat)
		buf = append(buf, `","duration_ms":`...)
		buf = strconv.AppendFloat(buf, dur, 'f', -1, 64)
		buf = append(buf, `,"query_id":"`...)
		buf = append(buf, qid...) // QueryIDs are safe ASCII (e.g. "se-abc123")
		buf = append(buf, `"}`...)
	})
	buf = append(buf, ']')
	return buf, nil
}

// StreamSection emits a SQLPerformanceJSON to bw item-by-item for the
// big Executions array, avoiding a per-section buffer. Implements
// sectionStreamer.
func (p SQLPerformanceJSON) StreamSection(bw *bufio.Writer, prefix, indent string, compact bool) error {
	inner := prefix + indent
	e := &fieldEmitter{bw: bw, inner: inner, compact: compact, first: true}

	bw.WriteByte('{')

	if err := e.emitScalar("total_query_duration", p.TotalQueryDuration); err != nil {
		return err
	}
	if err := e.emitScalar("total_queries_parsed", p.TotalQueriesParsed); err != nil {
		return err
	}
	if err := e.emitScalar("total_unique_queries", p.TotalUniqueQueries); err != nil {
		return err
	}
	if err := e.emitScalar("top_1_percent_slow_queries", p.Top1PercentSlowQueries); err != nil {
		return err
	}
	if err := e.emitScalar("query_max_duration", p.QueryMaxDuration); err != nil {
		return err
	}
	if err := e.emitScalar("query_min_duration", p.QueryMinDuration); err != nil {
		return err
	}
	if err := e.emitScalar("query_median_duration", p.QueryMedianDuration); err != nil {
		return err
	}
	if err := e.emitScalar("query_99th_percentile", p.Query99thPercentile); err != nil {
		return err
	}

	// Big array — stream item by item, never buffered as a whole.
	e.writeKey("executions")
	streamExecutionsJSON(bw, p.Executions, inner, indent, compact)

	// Queries can also be huge (26k × ~190 KB on Z) — stream item by item.
	e.writeKey("queries")
	if err := streamQueriesJSON(bw, p.Queries, inner, indent, compact); err != nil {
		return err
	}

	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte('}')
	return nil
}

// StreamSection emits a ConnectionsJSON to bw item-by-item for the big
// Connections and SessionEvents arrays. Implements sectionStreamer.
func (c ConnectionsJSON) StreamSection(bw *bufio.Writer, prefix, indent string, compact bool) error {
	inner := prefix + indent
	e := &fieldEmitter{bw: bw, inner: inner, compact: compact, first: true}

	bw.WriteByte('{')

	if err := e.emitScalar("connection_count", c.ConnectionCount); err != nil {
		return err
	}
	if err := e.emitScalar("avg_connections_per_hour", c.AvgConnectionsPerHour); err != nil {
		return err
	}
	if err := e.emitScalar("disconnection_count", c.DisconnectionCount); err != nil {
		return err
	}
	if err := e.emitScalar("avg_session_time", c.AvgSessionTime); err != nil {
		return err
	}

	if c.SessionStats != nil {
		if err := e.emitScalar("session_stats", c.SessionStats); err != nil {
			return err
		}
	}
	if len(c.SessionDistribution) > 0 {
		if err := e.emitScalar("session_distribution", c.SessionDistribution); err != nil {
			return err
		}
	}
	if len(c.SessionsByUser) > 0 {
		if err := e.emitScalar("sessions_by_user", c.SessionsByUser); err != nil {
			return err
		}
	}
	if len(c.SessionsByDatabase) > 0 {
		if err := e.emitScalar("sessions_by_database", c.SessionsByDatabase); err != nil {
			return err
		}
	}
	if len(c.SessionsByHost) > 0 {
		if err := e.emitScalar("sessions_by_host", c.SessionsByHost); err != nil {
			return err
		}
	}
	if c.PeakConcurrent > 0 {
		if err := e.emitScalar("peak_concurrent_sessions", c.PeakConcurrent); err != nil {
			return err
		}
	}
	if c.PeakConcurrentTime != "" {
		if err := e.emitScalar("peak_concurrent_timestamp", c.PeakConcurrentTime); err != nil {
			return err
		}
	}

	// Big arrays — stream items, never buffered as a whole.
	e.writeKey("connections")
	streamTimestampsJSON(bw, c.Connections, inner, indent, compact)

	if c.SessionEvents.count() > 0 {
		e.writeKey("session_events")
		streamSessionEventsJSON(bw, c.SessionEvents, inner, indent, compact)
	}

	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte('}')
	return nil
}

// StreamSection emits a SQLPerformanceDetailJSON to bw item-by-item for
// the big Executions array. Implements sectionStreamer. Used by both
// the --full export (inside the top-level map) and the standalone
// --sql-performance --json export (top-level document).
func (p SQLPerformanceDetailJSON) StreamSection(bw *bufio.Writer, prefix, indent string, compact bool) error {
	inner := prefix + indent
	e := &fieldEmitter{bw: bw, inner: inner, compact: compact, first: true}

	bw.WriteByte('{')

	if err := e.emitScalar("total_query_duration", p.TotalQueryDuration); err != nil {
		return err
	}
	if err := e.emitScalar("total_queries_parsed", p.TotalQueriesParsed); err != nil {
		return err
	}
	if err := e.emitScalar("total_unique_queries", p.TotalUniqueQueries); err != nil {
		return err
	}
	if err := e.emitScalar("top_1_percent_slow_queries", p.Top1PercentSlow); err != nil {
		return err
	}
	if err := e.emitScalar("query_max_duration", p.QueryMaxDuration); err != nil {
		return err
	}
	if err := e.emitScalar("query_min_duration", p.QueryMinDuration); err != nil {
		return err
	}
	if err := e.emitScalar("query_median_duration", p.QueryMedianDuration); err != nil {
		return err
	}
	if err := e.emitScalar("query_99th_percentile", p.Query99thPercentile); err != nil {
		return err
	}
	if err := e.emitScalar("duration_distribution", p.DurationDistribution); err != nil {
		return err
	}
	if err := e.emitScalar("slowest_queries", p.SlowestQueries); err != nil {
		return err
	}
	if err := e.emitScalar("most_frequent_queries", p.MostFrequentQueries); err != nil {
		return err
	}
	if err := e.emitScalar("most_time_consuming", p.MostTimeConsuming); err != nil {
		return err
	}
	if len(p.Queries) > 0 {
		// Queries can be huge (26k × ~190 KB on Z) — stream item by item
		// instead of buffering the full slice through MarshalIndent.
		e.writeKey("queries")
		if err := streamQueriesJSON(bw, p.Queries, inner, indent, compact); err != nil {
			return err
		}
	}
	if p.Executions.count() > 0 {
		// Big array — stream item by item, never buffered as a whole.
		e.writeKey("executions")
		streamExecutionsJSON(bw, p.Executions, inner, indent, compact)
	}

	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte('}')
	return nil
}

// StreamSection emits a SQLOverviewJSON. No big arrays here (categories,
// types, dimensional breakdowns are bounded by query type and unique
// entity count — typically <100). Streamed for consistency so all
// SQL-* JSON exports go through the same code path.
func (o SQLOverviewJSON) StreamSection(bw *bufio.Writer, prefix, indent string, compact bool) error {
	inner := prefix + indent
	e := &fieldEmitter{bw: bw, inner: inner, compact: compact, first: true}

	bw.WriteByte('{')

	if err := e.emitScalar("total_queries", o.TotalQueries); err != nil {
		return err
	}
	if err := e.emitScalar("categories", o.Categories); err != nil {
		return err
	}
	if err := e.emitScalar("types", o.Types); err != nil {
		return err
	}
	if len(o.ByDatabase) > 0 {
		if err := e.emitScalar("by_database", o.ByDatabase); err != nil {
			return err
		}
	}
	if len(o.ByUser) > 0 {
		if err := e.emitScalar("by_user", o.ByUser); err != nil {
			return err
		}
	}
	if len(o.ByHost) > 0 {
		if err := e.emitScalar("by_host", o.ByHost); err != nil {
			return err
		}
	}
	if len(o.ByApp) > 0 {
		if err := e.emitScalar("by_app", o.ByApp); err != nil {
			return err
		}
	}

	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte('}')
	return nil
}

// StreamSection emits a SQLDetailJSON. The Executions array is filtered
// to a single query — typically modest but can be hundreds of thousands
// for hot queries. Stream it.
func (d SQLDetailJSON) StreamSection(bw *bufio.Writer, prefix, indent string, compact bool) error {
	inner := prefix + indent
	e := &fieldEmitter{bw: bw, inner: inner, compact: compact, first: true}

	bw.WriteByte('{')

	if err := e.emitScalar("id", d.ID); err != nil {
		return err
	}
	if err := e.emitScalar("normalized_query", d.NormalizedQuery); err != nil {
		return err
	}
	if d.RawQuery != "" {
		if err := e.emitScalar("raw_query", d.RawQuery); err != nil {
			return err
		}
	}
	if err := e.emitScalar("type", d.Type); err != nil {
		return err
	}
	if err := e.emitScalar("category", d.Category); err != nil {
		return err
	}
	if d.Statistics != nil {
		if err := e.emitScalar("statistics", d.Statistics); err != nil {
			return err
		}
	}
	if d.Executions.count() > 0 {
		e.writeKey("executions")
		streamExecutionsJSON(bw, d.Executions, inner, indent, compact)
	}
	if len(d.TopDatabases) > 0 {
		if err := e.emitScalar("top_databases", d.TopDatabases); err != nil {
			return err
		}
	}
	if len(d.TopUsers) > 0 {
		if err := e.emitScalar("top_users", d.TopUsers); err != nil {
			return err
		}
	}
	if len(d.TopApps) > 0 {
		if err := e.emitScalar("top_apps", d.TopApps); err != nil {
			return err
		}
	}
	if len(d.TopHosts) > 0 {
		if err := e.emitScalar("top_hosts", d.TopHosts); err != nil {
			return err
		}
	}
	if d.TempFiles != nil {
		if err := e.emitScalar("temp_files", d.TempFiles); err != nil {
			return err
		}
	}
	if d.Locks != nil {
		if err := e.emitScalar("locks", d.Locks); err != nil {
			return err
		}
	}
	if d.SlowestRun != nil {
		if err := e.emitScalar("slowest_run", d.SlowestRun); err != nil {
			return err
		}
	}
	if d.Plan != "" {
		if err := e.emitScalar("plan", d.Plan); err != nil {
			return err
		}
	}

	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte('}')
	return nil
}

// StreamSection emits a LocksJSON to bw item-by-item for the big Events
// array. Implements sectionStreamer.
func (l LocksJSON) StreamSection(bw *bufio.Writer, prefix, indent string, compact bool) error {
	inner := prefix + indent
	e := &fieldEmitter{bw: bw, inner: inner, compact: compact, first: true}

	bw.WriteByte('{')

	if err := e.emitScalar("total_events", l.TotalEvents); err != nil {
		return err
	}
	if err := e.emitScalar("waiting_events", l.WaitingEvents); err != nil {
		return err
	}
	if err := e.emitScalar("acquired_events", l.AcquiredEvents); err != nil {
		return err
	}
	if l.DeadlockEvents > 0 {
		if err := e.emitScalar("deadlock_events", l.DeadlockEvents); err != nil {
			return err
		}
	}
	if err := e.emitScalar("total_wait_time", l.TotalWaitTime); err != nil {
		return err
	}
	if err := e.emitScalar("avg_wait_time", l.AvgWaitTime); err != nil {
		return err
	}
	if err := e.emitScalar("lock_type_stats", l.LockTypeStats); err != nil {
		return err
	}
	if err := e.emitScalar("resource_type_stats", l.ResourceTypeStats); err != nil {
		return err
	}
	if len(l.RelationStats) > 0 {
		if err := e.emitScalar("relation_stats", l.RelationStats); err != nil {
			return err
		}
	}
	// Big array — stream item by item.
	e.writeKey("events")
	streamLockEventsJSON(bw, l.Events.events, inner, indent, compact)

	if len(l.Queries) > 0 {
		if err := e.emitScalar("queries", l.Queries); err != nil {
			return err
		}
	}

	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte('}')
	return nil
}

// StreamSection emits a TempFilesJSON to bw item-by-item for the big
// Events array. Implements sectionStreamer.
func (t TempFilesJSON) StreamSection(bw *bufio.Writer, prefix, indent string, compact bool) error {
	inner := prefix + indent
	e := &fieldEmitter{bw: bw, inner: inner, compact: compact, first: true}

	bw.WriteByte('{')

	if err := e.emitScalar("total_messages", t.TotalMessages); err != nil {
		return err
	}
	if err := e.emitScalar("total_size", t.TotalSize); err != nil {
		return err
	}
	if err := e.emitScalar("avg_size", t.AvgSize); err != nil {
		return err
	}
	if err := e.emitScalar("max_size", t.MaxSize); err != nil {
		return err
	}
	// Big array — stream item by item.
	e.writeKey("events")
	streamTempFileEventsJSON(bw, t.Events.events, inner, indent, compact)

	if len(t.Queries) > 0 {
		if err := e.emitScalar("queries", t.Queries); err != nil {
			return err
		}
	}

	if !compact {
		bw.WriteByte('\n')
		bw.WriteString(prefix)
	}
	bw.WriteByte('}')
	return nil
}

// streamTopLevel writes a sectionStreamer as a top-level JSON document
// to w. Same trailing newline as json.Encoder.Encode.
func streamTopLevel(w io.Writer, s sectionStreamer, compact bool) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	if err := s.StreamSection(bw, "", "  ", compact); err != nil {
		return err
	}
	bw.WriteByte('\n')
	return nil
}

type ClientsJSON struct {
	UniqueDatabases int `json:"unique_databases"`
	UniqueUsers     int `json:"unique_users"`
	UniqueApps      int `json:"unique_apps"`
	UniqueHosts     int `json:"unique_hosts"`
}

type ClientEntityJSON struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type EventJSON struct {
	Type       string  `json:"type"`
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
}

// nonErrorSeverity reports whether a severity sits below the error classes
// (LOG/INFO/DEBUG/NOTICE). The --errors section drops these; --events keeps
// every severity. Shared by the text and JSON renderers so the two never drift.
func nonErrorSeverity(s string) bool {
	switch s {
	case "LOG", "INFO", "DEBUG", "NOTICE":
		return true
	default:
		return false
	}
}

type EventStatJSON struct {
	// ID is the stable short handle (e.g. "wa-aBc1") used as the CLI
	// selector for `--event-detail` and as the click-target id in the
	// HTML modal. Generated from severity + normalized message.
	ID            string `json:"id,omitempty"`
	Message       string `json:"message"`
	Count         int    `json:"count"`
	Severity      string `json:"severity"`
	Example       string `json:"example"`
	SQLStateClass string `json:"sql_state_class,omitempty"`
	// Timestamps lists every occurrence as Unix milliseconds. Consumed by
	// the HTML report's per-event modal to render an occurrences-over-time
	// sparkline. Omitted when empty.
	Timestamps []int64 `json:"timestamps,omitempty"`
	// TriggeringQueries exposes the Pareto view of which normalized
	// queries fired this pattern. The IDs are stable across the report
	// (shared with sql_performance.queries[].id) so the HTML modal can
	// cross-link to the Query Detail panel.
	TriggeringQueries []TriggeringQueryJSON `json:"triggering_queries,omitempty"`
}

// TriggeringQueryJSON exposes one (queryID, normalized_query, count)
// row of an EventStat's triggering-queries table. The same ID is
// emitted under sql_performance.queries[].id when the query was also
// timed by log_min_duration_statement; consumers can join the two
// sections on this field to enrich the event view with full query
// metrics.
type TriggeringQueryJSON struct {
	ID              string `json:"id"`
	NormalizedQuery string `json:"normalized_query"`
	Count           int    `json:"count"`
}

type ErrorClassJSON struct {
	ClassCode   string `json:"class_code"`
	Description string `json:"description"`
	Count       int    `json:"count"`
}

// ExportJSON brings together all metrics into one composite structure,
// converts it into an indented JSON string, and outputs the result.
// Only sections with data are included in the output.
// When full is true, includes sql_overview and enriched sql_performance sections.
// When compact is true, outputs JSON without indentation (smaller, lower memory).
func ExportJSON(w io.Writer, m analysis.AggregatedMetrics, sections []string, full bool, compact bool) {
	data := buildJSONData(m, sections, full)
	if err := encodeMapStreaming(w, data, compact); err != nil {
		fmt.Fprintf(w, "[ERROR] Failed to export JSON: %v\n", err)
	}
}

// encodeMapStreaming writes a map[string]interface{} as a JSON object
// **section by section** to w, instead of letting json.Encoder buffer
// the entire output internally before flushing. The encoder's hidden
// buffer was the dominant peak-RSS contributor during marshal phase
// on big logs (~800 MB on J.log, all sections' marshaled bytes
// coexisting until the final flush). Here each section's bytes are
// written and released before the next section is marshaled, so peak
// during marshal = max single section, not the sum.
//
// Top-level keys are emitted in sorted order to match json.Marshal's
// default behavior on maps (the goldens depend on this).
func encodeMapStreaming(w io.Writer, data map[string]interface{}, compact bool) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	bw.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			bw.WriteByte(',')
		}
		if !compact {
			bw.WriteString("\n  ")
		}
		// Marshal the key (handles escaping properly).
		kb, err := json.Marshal(k)
		if err != nil {
			return err
		}
		bw.Write(kb)
		if compact {
			bw.WriteByte(':')
		} else {
			bw.WriteString(": ")
		}
		// Sections that contain big arrays implement sectionStreamer
		// to write themselves item-by-item to the writer, bypassing the
		// MarshalJSON contract that forces a complete []byte buffer per
		// section (~365 MB for connections on J.log). This avoids the
		// last per-section buffer peak.
		if streamer, ok := data[k].(sectionStreamer); ok {
			if err := streamer.StreamSection(bw, "  ", "  ", compact); err != nil {
				return err
			}
		} else {
			// Marshal small sections normally — their per-section buffer
			// is tiny (kilobytes) so no point streaming.
			var vb []byte
			if compact {
				vb, err = json.Marshal(data[k])
			} else {
				vb, err = json.MarshalIndent(data[k], "  ", "  ")
			}
			if err != nil {
				return err
			}
			bw.Write(vb)
		}
	}
	if !compact {
		bw.WriteByte('\n')
	}
	bw.WriteByte('}')
	// Always trailing newline — matches json.Encoder.Encode behavior
	// regardless of indent mode, so existing consumers (the wasm
	// MetaInfo path among others) see byte-identical output.
	bw.WriteByte('\n')
	return nil
}

// sectionStreamer lets a section value emit its JSON form directly to a
// writer instead of returning a full []byte through MarshalJSON. The
// caller (encodeMapStreaming) provides the indent context: prefix is
// the leading whitespace before the section's outer brace (matches
// MarshalIndent's prefix arg), indent is the per-level indent unit.
//
// Used to avoid the big per-section buffer when the section contains
// arrays of millions of items (sql_performance.executions,
// connections.{connections,session_events}).
type sectionStreamer interface {
	StreamSection(w *bufio.Writer, prefix, indent string, compact bool) error
}

// ExportJSONString returns the JSON export as a string instead of printing.
// This is useful for WASM and other contexts where stdout is not available.
func ExportJSONString(m analysis.AggregatedMetrics, sections []string) (string, error) {
	return ExportJSONStringWithMeta(m, sections, false, nil, false)
}

// MetaInfo contains optional metadata about the parsing process.
type MetaInfo struct {
	Format       string `json:"format,omitempty"`
	Entries      int    `json:"entries,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	ParseTime    string `json:"parse_time,omitempty"`
	ProcessingMs int64  `json:"processing_ms,omitempty"`
}

// ExportJSONStringWithMeta returns the JSON export with optional metadata.
// Uses streaming encoder to avoid triple buffering (map + []byte + string).
// When compact is true, outputs JSON without indentation (smaller, lower memory).
func ExportJSONStringWithMeta(m analysis.AggregatedMetrics, sections []string, full bool, meta *MetaInfo, compact bool) (string, error) {
	data := buildJSONData(m, sections, full)
	if meta != nil {
		data["meta"] = meta
	}

	// Section-by-section streaming into a buffer (same path as ExportJSON).
	var buf bytes.Buffer
	if err := encodeMapStreaming(&buf, data, compact); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// buildJSONData constructs the JSON data structure from metrics.
// This is the shared implementation used by both ExportJSON and ExportJSONString.
// When full is true, sql_overview and enriched sql_performance are added at the end.
func buildJSONData(m analysis.AggregatedMetrics, sections []string, full bool) map[string]interface{} {
	data := make(map[string]interface{})

	has := func(name string) bool {
		for _, s := range sections {
			if s == name || s == "all" {
				return true
			}
		}
		return false
	}

	if has("summary") {
		data["summary"] = convertSummary(m)
	}

	// --events and --errors select the same underlying data (severity
	// distribution + top signatures). The text renderer shows them
	// slightly differently (--errors focuses on ERROR/FATAL/PANIC); in
	// JSON we emit the full structure for both so downstream callers
	// always see the same shape.
	if (has("events") || has("errors")) && len(m.EventSummaries) > 0 {
		// --errors restricts the section to the error classes (drop
		// LOG/INFO/DEBUG/NOTICE), matching the text and markdown renderers;
		// --events keeps every severity. Without this, --errors --json/--yaml
		// silently emitted the full events list, identical to --events.
		onlyErrors := has("errors") && !has("events")
		events := make([]EventJSON, 0, len(m.EventSummaries))
		for _, ev := range m.EventSummaries {
			if onlyErrors && nonErrorSeverity(ev.Type) {
				continue
			}
			events = append(events, EventJSON{
				Type:       ev.Type,
				Count:      ev.Count,
				Percentage: ev.Percentage,
			})
		}
		data["events"] = events

		if len(m.TopEvents) > 0 {
			topEvents := make([]EventStatJSON, 0, len(m.TopEvents))
			for _, e := range m.TopEvents {
				if onlyErrors && nonErrorSeverity(e.Severity) {
					continue
				}
				topEvents = append(topEvents, EventStatJSON{
					ID:                e.ID,
					Message:           e.Message,
					Count:             e.Count,
					Severity:          e.Severity,
					Example:           e.Example,
					SQLStateClass:     e.SQLStateClass,
					Timestamps:        e.Timestamps,
					TriggeringQueries: triggeringQueriesJSON(e.TriggeringQueries),
				})
			}
			if len(topEvents) > 0 {
				data["top_events"] = topEvents
			}
		}
	}

	// SQL summary (basic) - skip if full mode (enriched version added at the end)
	if !full && has("sql_summary") && m.SQL.TotalQueries > 0 {
		data["sql_performance"] = convertSQLPerformance(m.SQL)
	}

	// Note: sql_overview is NOT included in default "all" mode.
	// It's only shown when --sql-overview flag is used (handled via early return in execute.go)
	// or when --full flag is used (added at the end of this function).

	if has("tempfiles") && m.TempFiles.Count > 0 {
		tf := TempFilesJSON{
			TotalMessages: m.TempFiles.Count,
			TotalSize:     FormatBytes(m.TempFiles.TotalSize),
			AvgSize:       FormatBytes(m.TempFiles.TotalSize / int64(m.TempFiles.Count)),
			MaxSize:       FormatBytes(m.TempFiles.MaxSize),
			// Lazy wrapper — no intermediate []TempFileEventJSON slice.
			Events:  lazyTempFileEvents{events: m.TempFiles.Events},
			Queries: []TempFileQueryStatJSON{},
		}
		for _, stat := range m.TempFiles.QueryStats {
			avg := int64(0)
			if stat.Count > 0 {
				avg = stat.TotalSize / int64(stat.Count)
			}
			tf.Queries = append(tf.Queries, TempFileQueryStatJSON{
				ID:              stat.ID,
				NormalizedQuery: stat.NormalizedQuery,
				RawQuery:        stat.RawQuery,
				Count:           stat.Count,
				TotalSize:       FormatBytes(stat.TotalSize),
				MinSize:         FormatBytes(stat.MinSize),
				MaxSize:         FormatBytes(stat.MaxSize),
				AvgSize:         FormatBytes(avg),
			})
		}
		sort.Slice(tf.Queries, func(i, j int) bool {
			return tf.Queries[i].ID < tf.Queries[j].ID
		})
		data["temp_files"] = tf
	}

	if has("locks") && m.Locks.TotalEvents > 0 {
		data["locks"] = convertLocks(m.Locks)
	}

	if has("maintenance") && (m.Vacuum.VacuumCount > 0 || m.Vacuum.AnalyzeCount > 0) {
		data["maintenance"] = buildMaintenanceJSON(m.Vacuum)
	}

	// Replication is gated on the server section: the --replication flag
	// is gone and --server covers both scopes (the CLI/MD renderers fold
	// replication into SERVER as a sub-zone). The JSON keys stay separate
	// so downstream consumers can target either side independently.
	if has("server") && m.Replication.HasAny {
		data["replication"] = buildReplicationJSON(m.Replication)
	}

	if has("checkpoints") && (m.Checkpoints.CompleteCount > 0 || m.Checkpoints.WarningCount > 0) {
		cp := CheckpointsJSON{
			TotalCheckpoints: m.Checkpoints.CompleteCount,
		}
		if m.Checkpoints.CompleteCount > 0 {
			cp.AvgCheckpointTime = formatSeconds(m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount))
			cp.MaxCheckpointTime = formatSeconds(m.Checkpoints.MaxWriteTimeSeconds)
		}
		for _, t := range m.Checkpoints.Events {
			cp.Events = append(cp.Events, t.Format("2006-01-02 15:04:05"))
		}
		if len(m.Checkpoints.TypeCounts) > 0 {
			cp.Types = make(map[string]CheckpointTypeJSON)
			duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
			durationHours := duration.Hours()
			for cpType, count := range m.Checkpoints.TypeCounts {
				percentage := float64(count) / float64(m.Checkpoints.CompleteCount) * 100
				rate := 0.0
				if durationHours > 0 {
					rate = float64(count) / durationHours
				}
				typeJSON := CheckpointTypeJSON{
					Count:      count,
					Percentage: percentage,
					Rate:       rate,
				}
				if events, ok := m.Checkpoints.TypeEvents[cpType]; ok {
					for _, t := range events {
						typeJSON.Events = append(typeJSON.Events, t.Format("2006-01-02 15:04:05"))
					}
				}
				cp.Types[cpType] = typeJSON
			}
		}
		if len(m.Checkpoints.WALDistances) > 0 && m.Checkpoints.CompleteCount > 0 {
			avgKB := float64(m.Checkpoints.TotalDistanceKB) / float64(m.Checkpoints.CompleteCount)
			cp.AvgWALDistance = FormatBytes(int64(avgKB) * 1024)
			cp.MaxWALDistance = FormatBytes(m.Checkpoints.MaxDistanceKB * 1024)
			for _, w := range m.Checkpoints.WALDistances {
				cp.WALDistances = append(cp.WALDistances, WALDistanceJSON{
					Timestamp:  w.Timestamp.Format("2006-01-02 15:04:05"),
					DistanceKB: w.DistanceKB,
					EstimateKB: w.EstimateKB,
				})
			}
		}
		// I/O rates
		if m.Checkpoints.TotalBuffersWritten > 0 {
			cp.TotalBuffersWritten = m.Checkpoints.TotalBuffersWritten
		}
		duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
		if duration.Seconds() > 0 && m.Checkpoints.TotalDistanceKB > 0 {
			walRateBytesPerSec := float64(m.Checkpoints.TotalDistanceKB*1024) / duration.Seconds()
			cp.WALRate = formatRate(walRateBytesPerSec)
		}
		if m.Checkpoints.TotalWriteTimeSeconds > 0 && m.Checkpoints.TotalBuffersWritten > 0 {
			flushRateBytesPerSec := float64(m.Checkpoints.TotalBuffersWritten*8192) / m.Checkpoints.TotalWriteTimeSeconds
			cp.FlushRate = formatRate(flushRateBytesPerSec)
		}
		if m.Checkpoints.WarningCount > 0 {
			cp.WarningCount = m.Checkpoints.WarningCount
			cp.WarningMinIntervalSeconds = m.Checkpoints.WarningMinIntervalSeconds
			cp.WarningMaxIntervalSeconds = m.Checkpoints.WarningMaxIntervalSeconds
			for _, t := range m.Checkpoints.WarningEvents {
				cp.WarningEvents = append(cp.WarningEvents, t.Format("2006-01-02 15:04:05"))
			}
		}
		data["checkpoints"] = cp
	}

	if has("connections") && (m.Connections.ConnectionReceivedCount > 0 || m.Connections.DisconnectionCount > 0) {
		duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
		durationHours := duration.Hours()
		if durationHours == 0 {
			durationHours = 1
		}
		conn := ConnectionsJSON{
			ConnectionCount:       m.Connections.ConnectionReceivedCount,
			AvgConnectionsPerHour: fmt.Sprintf("%.2f", float64(m.Connections.ConnectionReceivedCount)/durationHours),
			DisconnectionCount:    m.Connections.DisconnectionCount,
			AvgSessionTime: func() string {
				if m.Connections.DisconnectionCount > 0 {
					return (m.Connections.TotalSessionTime / time.Duration(m.Connections.DisconnectionCount)).String()
				}
				return ""
			}(),
			Connections: lazyConnections{metrics: &m.Connections},
		}
		if m.Connections.SessionStats.Count > 0 {
			stats := m.Connections.SessionStats
			conn.SessionStats = &SessionStatsJSON{
				Count:     stats.Count,
				Min:       stats.Min.String(),
				Max:       stats.Max.String(),
				Avg:       stats.Avg.String(),
				Median:    stats.Median.String(),
				Cumulated: m.Connections.SessionCumulated.String(),
			}
			conn.SessionDistribution = m.Connections.SessionDistribution
		}
		if len(m.Connections.SessionsByUser) > 0 {
			conn.SessionsByUser = make(map[string]SessionStatsJSON)
			for user, s := range m.Connections.SessionsByUser {
				stats := s.Stats()
				conn.SessionsByUser[user] = SessionStatsJSON{
					Count: stats.Count, Min: stats.Min.String(), Max: stats.Max.String(),
					Avg: stats.Avg.String(), Median: stats.Median.String(), Cumulated: s.Cumulated().String(),
				}
			}
		}
		if len(m.Connections.SessionsByDatabase) > 0 {
			conn.SessionsByDatabase = make(map[string]SessionStatsJSON)
			for db, s := range m.Connections.SessionsByDatabase {
				stats := s.Stats()
				conn.SessionsByDatabase[db] = SessionStatsJSON{
					Count: stats.Count, Min: stats.Min.String(), Max: stats.Max.String(),
					Avg: stats.Avg.String(), Median: stats.Median.String(), Cumulated: s.Cumulated().String(),
				}
			}
		}
		if len(m.Connections.SessionsByHost) > 0 {
			conn.SessionsByHost = make(map[string]SessionStatsJSON)
			for host, s := range m.Connections.SessionsByHost {
				stats := s.Stats()
				conn.SessionsByHost[host] = SessionStatsJSON{
					Count: stats.Count, Min: stats.Min.String(), Max: stats.Max.String(),
					Avg: stats.Avg.String(), Median: stats.Median.String(), Cumulated: s.Cumulated().String(),
				}
			}
		}
		if m.Connections.PeakConcurrentSessions > 0 {
			conn.PeakConcurrent = m.Connections.PeakConcurrentSessions
			conn.PeakConcurrentTime = m.Connections.PeakConcurrentTimestamp.Format("2006-01-02 15:04:05")
		}
		// Export session events for client-side sweep-line — lazy wrapper
		// avoids the per-event []SessionEventJSON intermediate slice.
		conn.SessionEvents = lazySessionEvents{metrics: &m.Connections}
		data["connections"] = conn
	}

	if has("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0 || m.UniqueEntities.UniqueHosts > 0) {
		data["clients"] = ClientsJSON{
			UniqueDatabases: m.UniqueEntities.UniqueDbs,
			UniqueUsers:     m.UniqueEntities.UniqueUsers,
			UniqueApps:      m.UniqueEntities.UniqueApps,
			UniqueHosts:     m.UniqueEntities.UniqueHosts,
		}
		if m.UniqueEntities.UniqueUsers > 0 && m.UniqueEntities.UserCounts != nil && !(len(m.UniqueEntities.Users) == 1 && m.UniqueEntities.Users[0] == "UNKNOWN") {
			sortedUsers := analysis.SortByCount(m.UniqueEntities.UserCounts)
			users := make([]ClientEntityJSON, len(sortedUsers))
			for i, item := range sortedUsers {
				users[i] = ClientEntityJSON{Name: item.Name, Count: item.Count}
			}
			data["users"] = users
		}
		if m.UniqueEntities.UniqueApps > 0 && m.UniqueEntities.AppCounts != nil && !(len(m.UniqueEntities.Apps) == 1 && m.UniqueEntities.Apps[0] == "UNKNOWN") {
			sortedApps := analysis.SortByCount(m.UniqueEntities.AppCounts)
			apps := make([]ClientEntityJSON, len(sortedApps))
			for i, item := range sortedApps {
				apps[i] = ClientEntityJSON{Name: item.Name, Count: item.Count}
			}
			data["apps"] = apps
		}
		if m.UniqueEntities.UniqueDbs > 0 && m.UniqueEntities.DBCounts != nil && !(len(m.UniqueEntities.DBs) == 1 && m.UniqueEntities.DBs[0] == "UNKNOWN") {
			sortedDBs := analysis.SortByCount(m.UniqueEntities.DBCounts)
			databases := make([]ClientEntityJSON, len(sortedDBs))
			for i, item := range sortedDBs {
				databases[i] = ClientEntityJSON{Name: item.Name, Count: item.Count}
			}
			data["databases"] = databases
		}
		if m.UniqueEntities.UniqueHosts > 0 && m.UniqueEntities.HostCounts != nil && !(len(m.UniqueEntities.Hosts) == 1 && m.UniqueEntities.Hosts[0] == "UNKNOWN") {
			sortedHosts := analysis.SortByCount(m.UniqueEntities.HostCounts)
			hosts := make([]ClientEntityJSON, len(sortedHosts))
			for i, item := range sortedHosts {
				hosts[i] = ClientEntityJSON{Name: item.Name, Count: item.Count}
			}
			data["hosts"] = hosts
		}
	}

	if has("server") && m.Server.HasAny() {
		data["server"] = buildServerJSON(m.Server)
	}

	// Full mode: add sql_overview and enriched sql_performance at the end
	if full && m.SQL.TotalQueries > 0 {
		// SQL overview (categories, types, dimensional breakdowns)
		data["sql_overview"] = buildSQLOverviewData(m.SQL)

		// Enriched SQL performance (basic stats + histograms + top queries)
		data["sql_performance"] = buildFullSQLPerformance(m.SQL)
	}

	return data
}

// buildServerJSON folds the analyzer's server-lifecycle metrics into
// the JSON struct used by --json output. Every counter is emitted (a
// zero is meaningful — "no reload happened"); slice and map fields
// stay omitempty so the JSON shape adapts to what the log actually
// carried.
func buildServerJSON(s analysis.ServerMetrics) ServerJSON {
	const tsFmt = "2006-01-02 15:04:05"
	j := ServerJSON{
		Starts:             s.StartCount,
		Reloads:            s.ReloadCount,
		ShutdownsFast:      s.ShutdownFastCount,
		ShutdownsImmediate: s.ShutdownImmediateCount,
		ShutdownsSmart:     s.ShutdownSmartCount,
		ShutdownCompleted:  s.ShutDownCompletedCount,
		CrashRecoveries:    s.CrashRecoveryCount,
		Interrupted:        s.InterruptedCount,
		BackendCrashes:     s.BackendCrashCount,
		AuxProcessExits:    s.AuxProcessExitCount,
	}
	for _, t := range s.StartTimes {
		j.StartTimes = append(j.StartTimes, t.Format(tsFmt))
	}
	for _, t := range s.ReloadTimes {
		j.ReloadTimes = append(j.ReloadTimes, t.Format(tsFmt))
	}
	if len(s.SignalCounts) > 0 {
		j.SignalCounts = make(map[string]int, len(s.SignalCounts))
		for k, v := range s.SignalCounts {
			j.SignalCounts[k] = v
		}
	}
	for _, c := range s.ParameterChanges {
		j.ParameterChanges = append(j.ParameterChanges, ServerParameterChangeJSON{
			Parameter: c.Parameter,
			Old:       c.Old,
			New:       c.New,
			Timestamp: c.Timestamp.Format(tsFmt),
		})
	}
	for _, ev := range s.Timeline {
		j.Timeline = append(j.Timeline, ServerTimelineEventJSON{
			Timestamp: ev.Timestamp.Format(tsFmt),
			Kind:      ev.Kind,
			Detail:    ev.Detail,
		})
	}
	return j
}

// buildSQLOverviewData builds SQL overview data for JSON export.
func buildSQLOverviewData(m analysis.SQLMetrics) SQLOverviewJSON {
	overview := SQLOverviewJSON{
		TotalQueries: m.TotalQueries,
	}

	categoryStats := make(map[string]struct {
		count     int
		totalTime float64
	})
	for _, stat := range m.QueryTypeStats {
		cs := categoryStats[stat.Category]
		cs.count += stat.Count
		cs.totalTime += stat.TotalTime
		categoryStats[stat.Category] = cs
	}

	for cat, cs := range categoryStats {
		overview.Categories = append(overview.Categories, CategoryStatJSON{
			Category:   cat,
			Count:      cs.count,
			Percentage: float64(cs.count) / float64(m.TotalQueries) * 100,
			TotalTime:  formatQueryDuration(cs.totalTime),
		})
	}
	sort.Slice(overview.Categories, func(i, j int) bool {
		if overview.Categories[i].Count != overview.Categories[j].Count {
			return overview.Categories[i].Count > overview.Categories[j].Count
		}
		return overview.Categories[i].Category < overview.Categories[j].Category
	})

	for qtype, stat := range m.QueryTypeStats {
		overview.Types = append(overview.Types, TypeStatJSON{
			Type:       qtype,
			Category:   stat.Category,
			Count:      stat.Count,
			Percentage: float64(stat.Count) / float64(m.TotalQueries) * 100,
			TotalTime:  formatQueryDuration(stat.TotalTime),
			AvgTime:    formatQueryDuration(stat.AvgTime),
			MaxTime:    formatQueryDuration(stat.MaxTime),
		})
	}
	sort.Slice(overview.Types, func(i, j int) bool {
		if overview.Types[i].Count != overview.Types[j].Count {
			return overview.Types[i].Count > overview.Types[j].Count
		}
		return overview.Types[i].Type < overview.Types[j].Type
	})

	overview.ByDatabase = convertDimensionBreakdown(m.QueryTypesByDatabase)
	overview.ByUser = convertDimensionBreakdown(m.QueryTypesByUser)
	overview.ByHost = convertDimensionBreakdown(m.QueryTypesByHost)
	overview.ByApp = convertDimensionBreakdown(m.QueryTypesByApp)

	return overview
}

// buildFullSQLPerformance builds enriched SQL performance data for --full mode.
// Includes basic stats, duration distribution histogram, and top queries lists.
func buildFullSQLPerformance(m analysis.SQLMetrics) SQLPerformanceDetailJSON {
	// Top 1% slow computation — count events whose duration exceeds the
	// P99 threshold. Goes through the compact storage helper to avoid
	// expanding 40M QueryExecution structs just to read the duration.
	top1Slow := 0
	if m.ExecutionCount() > 0 {
		top1Slow = m.ExecutionsCountAbove(m.P99QueryDuration)
	}

	perf := SQLPerformanceDetailJSON{
		TotalQueryDuration:  formatQueryDuration(m.SumQueryDuration),
		TotalQueriesParsed:  m.TotalQueries,
		TotalUniqueQueries:  m.UniqueQueries,
		Top1PercentSlow:     top1Slow,
		QueryMaxDuration:    formatQueryDuration(m.MaxQueryDuration),
		QueryMinDuration:    formatQueryDuration(m.MinQueryDuration),
		QueryMedianDuration: formatQueryDuration(m.MedianQueryDuration),
		Query99thPercentile: formatQueryDuration(m.P99QueryDuration),
	}

	// Duration distribution histogram
	buckets := []struct {
		label     string
		threshold float64
	}{
		{"< 1 ms", 1},
		{"< 10 ms", 10},
		{"< 100 ms", 100},
		{"< 1 s", 1000},
		{"< 10 s", 10000},
		{">= 10 s", -1},
	}

	bucketCounts := make([]int, len(buckets))
	m.IterateExecutions(func(exec analysis.QueryExecution) bool {
		for i, b := range buckets {
			if b.threshold < 0 || exec.Duration < b.threshold {
				bucketCounts[i]++
				break
			}
		}
		return true
	})

	for i, b := range buckets {
		perf.DurationDistribution = append(perf.DurationDistribution, DurationBucketJSON{
			Bucket: b.label,
			Count:  bucketCounts[i],
		})
	}

	// Convert QueryStats to slice for sorting
	type queryStat struct {
		id    string
		query string
		stat  *analysis.QueryStat
	}
	var stats []queryStat
	for _, s := range m.QueryStats {
		stats = append(stats, queryStat{s.ID, s.NormalizedQuery, s})
	}

	// Slowest queries (by max duration)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].stat.MaxTime != stats[j].stat.MaxTime {
			return stats[i].stat.MaxTime > stats[j].stat.MaxTime
		}
		return stats[i].id < stats[j].id
	})
	limit := 10
	if len(stats) < limit {
		limit = len(stats)
	}
	for i := 0; i < limit; i++ {
		s := stats[i]
		perf.SlowestQueries = append(perf.SlowestQueries, QueryRankJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			Count:           s.stat.Count,
			TotalTime:       formatQueryDuration(s.stat.TotalTime),
			AvgTime:         formatQueryDuration(s.stat.AvgTime),
			MaxTime:         formatQueryDuration(s.stat.MaxTime),
		})
	}

	// Most frequent queries (by count)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].stat.Count != stats[j].stat.Count {
			return stats[i].stat.Count > stats[j].stat.Count
		}
		return stats[i].id < stats[j].id
	})
	limit = 15
	if len(stats) < limit {
		limit = len(stats)
	}
	for i := 0; i < limit; i++ {
		s := stats[i]
		perf.MostFrequentQueries = append(perf.MostFrequentQueries, QueryRankJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			Count:           s.stat.Count,
			TotalTime:       formatQueryDuration(s.stat.TotalTime),
			AvgTime:         formatQueryDuration(s.stat.AvgTime),
			MaxTime:         formatQueryDuration(s.stat.MaxTime),
		})
	}

	// Most time consuming queries (by total time)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].stat.TotalTime != stats[j].stat.TotalTime {
			return stats[i].stat.TotalTime > stats[j].stat.TotalTime
		}
		return stats[i].id < stats[j].id
	})
	limit = 10
	if len(stats) < limit {
		limit = len(stats)
	}
	for i := 0; i < limit; i++ {
		s := stats[i]
		perf.MostTimeConsuming = append(perf.MostTimeConsuming, QueryRankJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			Count:           s.stat.Count,
			TotalTime:       formatQueryDuration(s.stat.TotalTime),
			AvgTime:         formatQueryDuration(s.stat.AvgTime),
			MaxTime:         formatQueryDuration(s.stat.MaxTime),
		})
	}

	// Full queries data for HTML viewer (all queries, sorted by total time)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].stat.TotalTime != stats[j].stat.TotalTime {
			return stats[i].stat.TotalTime > stats[j].stat.TotalTime
		}
		return stats[i].id < stats[j].id
	})
	dimsByQuery := m.TopDimensionsByQuery(5)
	for _, s := range stats {
		row := QueryStatJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			RawQuery:        s.stat.RawQuery,
			Type:            analysis.QueryTypeFromID(s.id),
			Count:           s.stat.Count,
			TotalTime:       roundMicro(s.stat.TotalTime),
			AvgTime:         roundMicro(s.stat.AvgTime),
			MaxTime:         s.stat.MaxTime,
			PreparedNames:   s.stat.PreparedNames,
			SlowestRun:      slowestRunJSON(s.stat),
			Plan:            s.stat.LastPlan,
		}
		if dims, ok := dimsByQuery[s.id]; ok {
			row.TopDatabases = dimensionCountsJSON(dims.Databases)
			row.TopUsers = dimensionCountsJSON(dims.Users)
			row.TopApps = dimensionCountsJSON(dims.Apps)
			row.TopHosts = dimensionCountsJSON(dims.Hosts)
		}
		perf.Queries = append(perf.Queries, row)
	}

	// Executions for time charts — lazy wrapper, T separator for the
	// detail format (RFC3339-ish) consumed by the HTML viewer.
	perf.Executions = lazyExecutions{metrics: &m, tsFormat: "2006-01-02T15:04:05"}

	return perf
}

// formatSeconds formats float64 seconds into "X.XX s" or "Y ms" without using time.Duration
func formatSeconds(s float64) string {
	if s >= 1.0 {
		return fmt.Sprintf("%.2f s", s)
	}
	return fmt.Sprintf("%d ms", int(s*1000))
}

// Helper function to format the vacuum space recovered for each table.
func formatVacuumSpaceRecovered(space map[string]int64) map[string]string {
	formatted := make(map[string]string, len(space))
	for table, size := range space {
		formatted[table] = FormatBytes(size)
	}
	return formatted
}

// triggeringQueriesJSON converts the in-memory TriggeringQuery slice
// into its JSON-friendly form. Returns nil (not an empty slice) when
// the stat carries no triggering queries so the wire shape stays
// "triggering_queries": absent rather than "triggering_queries": [].
func triggeringQueriesJSON(qs []analysis.TriggeringQuery) []TriggeringQueryJSON {
	if len(qs) == 0 {
		return nil
	}
	out := make([]TriggeringQueryJSON, len(qs))
	for i, q := range qs {
		out[i] = TriggeringQueryJSON{
			ID:              q.ID,
			NormalizedQuery: q.NormalizedQuery,
			Count:           q.Count,
		}
	}
	return out
}

// buildMaintenanceJSON folds the analyzer's vacuum metrics — including
// the newly parsed continuation-line aggregates — into the JSON struct.
// The continuation fields are emitted with omitempty so logs that never
// carry them keep the same JSON shape they had before this commit.
func buildMaintenanceJSON(v analysis.VacuumMetrics) MaintenanceJSON {
	j := MaintenanceJSON{
		VacuumCount:                v.VacuumCount,
		AggressiveVacuumCount:      v.AggressiveVacuumCount,
		AnalyzeCount:               v.AnalyzeCount,
		VacuumTableCounts:          v.VacuumTableCounts,
		AnalyzeTableCounts:         v.AnalyzeTableCounts,
		VacuumSpaceRecovered:       formatVacuumSpaceRecovered(v.VacuumSpaceRecovered),
		TotalVacuumElapsedSeconds:  v.TotalVacuumElapsedSeconds,
		TotalTuplesRemoved:         v.TotalTuplesRemoved,
		TotalTuplesNotYetRemovable: v.TotalTuplesNotYetRemovable,
		TotalBufferHits:            v.TotalBufferHits,
		TotalBufferMisses:          v.TotalBufferMisses,
		TotalBufferDirtied:         v.TotalBufferDirtied,
		TotalBufferWritten:         v.TotalBufferWritten,
		TotalWALRecords:            v.TotalWALRecords,
		TotalWALBytes:              v.TotalWALBytes,
	}
	if len(v.TopVacuumTables) > 0 {
		j.TopVacuumTables = make([]VacuumTableStatJSON, len(v.TopVacuumTables))
		for i, t := range v.TopVacuumTables {
			j.TopVacuumTables[i] = vacuumTableStatJSON(t)
		}
	}
	if len(v.XminBlockedTables) > 0 {
		j.XminBlockedTables = make([]VacuumTableStatJSON, len(v.XminBlockedTables))
		for i, t := range v.XminBlockedTables {
			j.XminBlockedTables[i] = vacuumTableStatJSON(t)
		}
	}
	if v.SlowestVacuum != nil {
		s := v.SlowestVacuum
		j.SlowestVacuum = &VacuumSampleJSON{
			Table:                 s.Table,
			ElapsedSeconds:        s.ElapsedSeconds,
			PagesRemoved:          s.PagesRemoved,
			TuplesRemoved:         s.TuplesRemoved,
			TuplesNotYetRemovable: s.TuplesNotYetRemovable,
		}
		if !s.Timestamp.IsZero() {
			j.SlowestVacuum.Timestamp = s.Timestamp.Format("2006-01-02 15:04:05")
		}
	}
	j.TotalAnalyzeElapsedSeconds = v.TotalAnalyzeElapsedSeconds
	if len(v.TopAnalyzeTablesByElapsed) > 0 {
		j.TopAnalyzeTablesByElapsed = make([]VacuumTableStatJSON, len(v.TopAnalyzeTablesByElapsed))
		for i, t := range v.TopAnalyzeTablesByElapsed {
			j.TopAnalyzeTablesByElapsed[i] = vacuumTableStatJSON(t)
		}
	}
	return j
}

func vacuumTableStatJSON(t analysis.VacuumTableStat) VacuumTableStatJSON {
	return VacuumTableStatJSON{
		Table:                 t.Table,
		VacuumCount:           t.VacuumCount,
		TotalElapsedSeconds:   t.TotalElapsedSeconds,
		MaxElapsedSeconds:     t.MaxElapsedSeconds,
		TuplesRemoved:         t.TuplesRemoved,
		TuplesNotYetRemovable: t.TuplesNotYetRemovable,
		BufferHits:            t.BufferHits,
		BufferMisses:          t.BufferMisses,
		BufferDirtied:         t.BufferDirtied,
		BufferWritten:         t.BufferWritten,
		WALRecords:            t.WALRecords,
		WALBytes:              t.WALBytes,
	}
}

// buildReplicationJSON folds the analyzer's replication metrics into
// the JSON struct. Only present in the output map when HasAny is true
// (see buildJSONData), so the JSON shape for logs without replication
// markers stays unchanged.
func buildReplicationJSON(r analysis.ReplicationMetrics) ReplicationJSON {
	out := ReplicationJSON{
		Markers:                 r.Markers,
		StreamReconnects:        r.Markers["stream_started"],
		RecoveryPauses:          r.Markers["recovery_paused"],
		RecoveryResumes:         r.Markers["recovery_resuming"],
		ConflictsWithRecovery:   r.Markers["conflict_terminate"] + r.Markers["conflict_cancel"],
		InvalidatedSlots:        r.Markers["slot_invalidated"],
		ReplicationTerminations: r.Markers["replication_term"] + r.Markers["wal_receive_failed"] + r.Markers["walsender_timeout"] + r.Markers["unexpected_eof"],
		PeakHourLabel:           r.PeakHourLabel,
		PeakHourCount:           r.PeakHourCount,
	}
	for _, c := range r.Markers {
		out.TotalEvents += c
	}
	if !r.LastTermination.IsZero() {
		out.LastTermination = r.LastTermination.Format("2006-01-02 15:04:05")
	}
	if len(r.HourCounts) > 0 {
		out.HourCounts = r.HourCounts
	}
	if len(r.ConflictQueries) > 0 {
		qs := make([]ReplicationConflictQueryJSON, 0, len(r.ConflictQueries))
		for _, s := range r.ConflictQueries {
			qs = append(qs, ReplicationConflictQueryJSON{
				ID:              s.ID,
				NormalizedQuery: s.NormalizedQuery,
				RawQuery:        s.RawQuery,
				Count:           s.Count,
			})
		}
		// Sort by count desc, then ID asc, for deterministic output.
		sort.Slice(qs, func(i, j int) bool {
			if qs[i].Count != qs[j].Count {
				return qs[i].Count > qs[j].Count
			}
			return qs[i].ID < qs[j].ID
		})
		out.ConflictQueries = qs
	}
	if len(r.Events) > 0 {
		evs := make([]ReplicationEventJSON, len(r.Events))
		for i, e := range r.Events {
			ev := ReplicationEventJSON{
				Marker:   e.Marker,
				Severity: e.Severity,
			}
			if !e.Timestamp.IsZero() {
				ev.Timestamp = e.Timestamp.Format("2006-01-02 15:04:05")
			}
			evs[i] = ev
		}
		out.Events = evs
	}
	return out
}

// convertSummary aggregates global metrics into a JSON-friendly format.
// It calculates the total duration between the first and last log entry
// and computes the throughput (logs per second).
func convertSummary(m analysis.AggregatedMetrics) SummaryJSON {
	duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
	throughput := 0.0
	if duration.Seconds() > 0 {
		throughput = float64(m.Global.Count) / duration.Seconds()
	}
	return SummaryJSON{
		StartDate:    m.Global.MinTimestamp.Format("2006-01-02 15:04:05"),
		EndDate:      m.Global.MaxTimestamp.Format("2006-01-02 15:04:05"),
		Duration:     duration.String(),
		TotalLogs:    m.Global.Count,
		Throughput:   fmt.Sprintf("%.2f entries/s", throughput),
		ErrorCount:   m.Global.ErrorCount,
		FatalCount:   m.Global.FatalCount,
		PanicCount:   m.Global.PanicCount,
		WarningCount: m.Global.WarningCount,
		LogCount:     m.Global.LogCount,
	}
}

// convertSQLPerformance processes SQL metrics to create a JSON structure.
// It calculates additional information like counting the number of slow queries
// (those that exceed the 99th percentile threshold) and formats various durations.
func convertSQLPerformance(m analysis.SQLMetrics) SQLPerformanceJSON {

	// Top 1% slow computation — count events whose duration exceeds the
	// P99 threshold. Goes through the compact storage helper to avoid
	// expanding 40M QueryExecution structs just to read the duration.
	top1Slow := 0
	if m.ExecutionCount() > 0 {
		top1Slow = m.ExecutionsCountAbove(m.P99QueryDuration)
	}

	// SQL duration data for each statement — lazy wrapper avoids the
	// per-execution []QueryExecutionJSON intermediate slice (was the
	// dominant per-row cost on big logs).
	executionsLazy := lazyExecutions{metrics: &m}

	// Per-query top-5 dimensions in one pass — feeds the HTML modal's
	// DIMENSIONS block. Cheap (~O(N) once vs O(N × K) for a per-query
	// re-walk) and cleanly nilable when the log carried no prefix.
	dimsByQuery := m.TopDimensionsByQuery(5)

	// Export all query stats (sorted by ID for deterministic output)
	queriesJSON := make([]QueryStatJSON, 0, len(m.QueryStats))
	for _, stat := range m.QueryStats {
		row := QueryStatJSON{
			ID:              stat.ID,
			NormalizedQuery: stat.NormalizedQuery,
			RawQuery:        stat.RawQuery,
			Count:           stat.Count,
			TotalTime:       roundMicro(stat.TotalTime),
			AvgTime:         roundMicro(stat.AvgTime),
			MaxTime:         stat.MaxTime,
			PreparedNames:   stat.PreparedNames,
			SlowestRun:      slowestRunJSON(stat),
			Plan:            stat.LastPlan,
		}
		if dims, ok := dimsByQuery[stat.ID]; ok {
			row.TopDatabases = dimensionCountsJSON(dims.Databases)
			row.TopUsers = dimensionCountsJSON(dims.Users)
			row.TopApps = dimensionCountsJSON(dims.Apps)
			row.TopHosts = dimensionCountsJSON(dims.Hosts)
		}
		queriesJSON = append(queriesJSON, row)
	}
	// Sort by ID for deterministic JSON output
	sort.Slice(queriesJSON, func(i, j int) bool {
		return queriesJSON[i].ID < queriesJSON[j].ID
	})

	return SQLPerformanceJSON{
		TotalQueryDuration:     formatQueryDuration(m.SumQueryDuration),
		TotalQueriesParsed:     m.TotalQueries,
		TotalUniqueQueries:     m.UniqueQueries,
		Top1PercentSlowQueries: top1Slow,
		QueryMaxDuration:       formatQueryDuration(m.MaxQueryDuration),
		QueryMinDuration:       formatQueryDuration(m.MinQueryDuration),
		QueryMedianDuration:    formatQueryDuration(m.MedianQueryDuration),
		Query99thPercentile:    formatQueryDuration(m.P99QueryDuration),
		Executions:             executionsLazy,
		Queries:                queriesJSON,
	}
}

// convertLocks processes lock metrics to create a JSON structure.
func convertLocks(m analysis.LockMetrics) LocksJSON {
	// Calculate average wait time
	avgWaitTime := "0 ms"
	if m.WaitingEvents+m.AcquiredEvents > 0 {
		avg := m.TotalWaitTime / float64(m.WaitingEvents+m.AcquiredEvents)
		avgWaitTime = formatQueryDuration(avg)
	}

	// Format total wait time
	totalWaitTime := formatQueryDuration(m.TotalWaitTime)

	// Lazy wrapper — no intermediate []LockEventJSON slice. The
	// streamLockEventsJSON helper formats each event in place.

	// Export all query stats (sorted by ID for deterministic output)
	queriesJSON := make([]LockQueryStatJSON, 0, len(m.QueryStats))
	for _, stat := range m.QueryStats {
		queriesJSON = append(queriesJSON, LockQueryStatJSON{
			ID:                stat.ID,
			NormalizedQuery:   stat.NormalizedQuery,
			RawQuery:          stat.RawQuery,
			AcquiredCount:     stat.AcquiredCount,
			AcquiredWaitTime:  formatQueryDuration(stat.AcquiredWaitTime),
			StillWaitingCount: stat.StillWaitingCount,
			StillWaitingTime:  formatQueryDuration(stat.StillWaitingTime),
			TotalWaitTime:     formatQueryDuration(stat.TotalWaitTime),
		})
	}
	// Sort by ID for deterministic JSON output
	sort.Slice(queriesJSON, func(i, j int) bool {
		return queriesJSON[i].ID < queriesJSON[j].ID
	})

	return LocksJSON{
		TotalEvents:       m.TotalEvents,
		WaitingEvents:     m.WaitingEvents,
		AcquiredEvents:    m.AcquiredEvents,
		DeadlockEvents:    m.DeadlockEvents,
		TotalWaitTime:     totalWaitTime,
		AvgWaitTime:       avgWaitTime,
		LockTypeStats:     m.LockTypeStats,
		ResourceTypeStats: m.ResourceTypeStats,
		RelationStats:     m.RelationStats,
		Events:            lazyLockEvents{events: m.Events},
		Queries:           queriesJSON,
	}
}

// ExportSQLOverviewJSON exports SQL overview data as JSON.
func ExportSQLOverviewJSON(w io.Writer, m analysis.SQLMetrics) {
	if m.TotalQueries == 0 {
		fmt.Fprintln(w, "{}")
		return
	}

	overview := SQLOverviewJSON{
		TotalQueries: m.TotalQueries,
	}

	// Build category statistics
	categoryStats := make(map[string]struct {
		count     int
		totalTime float64
	})
	for _, stat := range m.QueryTypeStats {
		cs := categoryStats[stat.Category]
		cs.count += stat.Count
		cs.totalTime += stat.TotalTime
		categoryStats[stat.Category] = cs
	}

	// Convert to sorted slice
	for cat, cs := range categoryStats {
		overview.Categories = append(overview.Categories, CategoryStatJSON{
			Category:   cat,
			Count:      cs.count,
			Percentage: float64(cs.count) / float64(m.TotalQueries) * 100,
			TotalTime:  formatQueryDuration(cs.totalTime),
		})
	}
	sort.Slice(overview.Categories, func(i, j int) bool {
		if overview.Categories[i].Count != overview.Categories[j].Count {
			return overview.Categories[i].Count > overview.Categories[j].Count
		}
		return overview.Categories[i].Category < overview.Categories[j].Category
	})

	// Build type statistics
	for qtype, stat := range m.QueryTypeStats {
		overview.Types = append(overview.Types, TypeStatJSON{
			Type:       qtype,
			Category:   stat.Category,
			Count:      stat.Count,
			Percentage: float64(stat.Count) / float64(m.TotalQueries) * 100,
			TotalTime:  formatQueryDuration(stat.TotalTime),
			AvgTime:    formatQueryDuration(stat.AvgTime),
			MaxTime:    formatQueryDuration(stat.MaxTime),
		})
	}
	sort.Slice(overview.Types, func(i, j int) bool {
		if overview.Types[i].Count != overview.Types[j].Count {
			return overview.Types[i].Count > overview.Types[j].Count
		}
		return overview.Types[i].Type < overview.Types[j].Type
	})

	// Build dimensional breakdowns
	overview.ByDatabase = convertDimensionBreakdown(m.QueryTypesByDatabase)
	overview.ByUser = convertDimensionBreakdown(m.QueryTypesByUser)
	overview.ByHost = convertDimensionBreakdown(m.QueryTypesByHost)
	overview.ByApp = convertDimensionBreakdown(m.QueryTypesByApp)

	// Stream the overview as a top-level document.
	if err := streamTopLevel(w, overview, false); err != nil {
		fmt.Fprintf(w, "[ERROR] Failed to export JSON: %v\n", err)
	}
}

// ExportSQLPerformanceJSON exports detailed SQL performance data as JSON.
func ExportSQLPerformanceJSON(w io.Writer, m analysis.SQLMetrics) {
	if m.TotalQueries == 0 {
		fmt.Fprintln(w, "{}")
		return
	}

	// Top 1% slow computation — count events whose duration exceeds the
	// P99 threshold. Goes through the compact storage helper to avoid
	// expanding 40M QueryExecution structs just to read the duration.
	top1Slow := 0
	if m.ExecutionCount() > 0 {
		top1Slow = m.ExecutionsCountAbove(m.P99QueryDuration)
	}

	perf := SQLPerformanceDetailJSON{
		TotalQueryDuration:  formatQueryDuration(m.SumQueryDuration),
		TotalQueriesParsed:  m.TotalQueries,
		TotalUniqueQueries:  m.UniqueQueries,
		Top1PercentSlow:     top1Slow,
		QueryMaxDuration:    formatQueryDuration(m.MaxQueryDuration),
		QueryMinDuration:    formatQueryDuration(m.MinQueryDuration),
		QueryMedianDuration: formatQueryDuration(m.MedianQueryDuration),
		Query99thPercentile: formatQueryDuration(m.P99QueryDuration),
	}

	// Duration distribution histogram
	buckets := []struct {
		label     string
		threshold float64
	}{
		{"< 1 ms", 1},
		{"< 10 ms", 10},
		{"< 100 ms", 100},
		{"< 1 s", 1000},
		{"< 10 s", 10000},
		{">= 10 s", -1},
	}

	bucketCounts := make([]int, len(buckets))
	m.IterateExecutions(func(exec analysis.QueryExecution) bool {
		for i, b := range buckets {
			if b.threshold < 0 || exec.Duration < b.threshold {
				bucketCounts[i]++
				break
			}
		}
		return true
	})

	for i, b := range buckets {
		perf.DurationDistribution = append(perf.DurationDistribution, DurationBucketJSON{
			Bucket: b.label,
			Count:  bucketCounts[i],
		})
	}

	// Convert QueryStats to slice for sorting
	type queryStat struct {
		id    string
		query string
		stat  *analysis.QueryStat
	}
	var stats []queryStat
	for _, s := range m.QueryStats {
		stats = append(stats, queryStat{s.ID, s.NormalizedQuery, s})
	}

	// Slowest queries (by max duration)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].stat.MaxTime != stats[j].stat.MaxTime {
			return stats[i].stat.MaxTime > stats[j].stat.MaxTime
		}
		return stats[i].id < stats[j].id
	})
	limit := 10
	if len(stats) < limit {
		limit = len(stats)
	}
	for i := 0; i < limit; i++ {
		s := stats[i]
		perf.SlowestQueries = append(perf.SlowestQueries, QueryRankJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			Count:           s.stat.Count,
			TotalTime:       formatQueryDuration(s.stat.TotalTime),
			AvgTime:         formatQueryDuration(s.stat.AvgTime),
			MaxTime:         formatQueryDuration(s.stat.MaxTime),
		})
	}

	// Most frequent queries (by count)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].stat.Count != stats[j].stat.Count {
			return stats[i].stat.Count > stats[j].stat.Count
		}
		return stats[i].id < stats[j].id
	})
	limit = 15
	if len(stats) < limit {
		limit = len(stats)
	}
	for i := 0; i < limit; i++ {
		s := stats[i]
		perf.MostFrequentQueries = append(perf.MostFrequentQueries, QueryRankJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			Count:           s.stat.Count,
			TotalTime:       formatQueryDuration(s.stat.TotalTime),
			AvgTime:         formatQueryDuration(s.stat.AvgTime),
			MaxTime:         formatQueryDuration(s.stat.MaxTime),
		})
	}

	// Most time consuming queries (by total time)
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].stat.TotalTime != stats[j].stat.TotalTime {
			return stats[i].stat.TotalTime > stats[j].stat.TotalTime
		}
		return stats[i].id < stats[j].id
	})
	limit = 10
	if len(stats) < limit {
		limit = len(stats)
	}
	for i := 0; i < limit; i++ {
		s := stats[i]
		perf.MostTimeConsuming = append(perf.MostTimeConsuming, QueryRankJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			Count:           s.stat.Count,
			TotalTime:       formatQueryDuration(s.stat.TotalTime),
			AvgTime:         formatQueryDuration(s.stat.AvgTime),
			MaxTime:         formatQueryDuration(s.stat.MaxTime),
		})
	}

	// Stream the perf as a top-level document. The Executions field is
	// not populated by this function (caller --sql-performance --json
	// only wants the aggregated stats and top queries), so the stream
	// helper skips it via the omitempty equivalent inside StreamSection.
	if err := streamTopLevel(w, perf, false); err != nil {
		fmt.Fprintf(w, "[ERROR] Failed to export JSON: %v\n", err)
	}
}

// convertDimensionBreakdown converts a dimension breakdown map to JSON format.
func convertDimensionBreakdown(breakdown map[string]map[string]*analysis.QueryTypeCount) []DimensionBreakdownJSON {
	if len(breakdown) == 0 {
		return nil
	}

	var result []DimensionBreakdownJSON
	for dimName, types := range breakdown {
		var totalCount int
		var totalTime float64
		var queryTypes []QueryTypeCountJSON

		for typeName, tc := range types {
			totalCount += tc.Count
			totalTime += tc.TotalTime
			queryTypes = append(queryTypes, QueryTypeCountJSON{
				Type:      typeName,
				Count:     tc.Count,
				TotalTime: formatQueryDuration(tc.TotalTime),
			})
		}

		// Sort query types by count descending
		sort.Slice(queryTypes, func(i, j int) bool {
			if queryTypes[i].Count != queryTypes[j].Count {
				return queryTypes[i].Count > queryTypes[j].Count
			}
			return queryTypes[i].Type < queryTypes[j].Type
		})

		result = append(result, DimensionBreakdownJSON{
			Name:       dimName,
			Count:      totalCount,
			TotalTime:  formatQueryDuration(totalTime),
			QueryTypes: queryTypes,
		})
	}

	// Sort dimensions by count descending
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Name < result[j].Name
	})

	return result
}

// ExportSQLDetailJSON exports SQL query details as JSON.
func ExportSQLDetailJSON(w io.Writer, m analysis.AggregatedMetrics, queryIDs []string) {
	var details []SQLDetailJSON

	for _, queryID := range queryIDs {
		detail := SQLDetailJSON{
			ID: queryID,
		}

		// Find query in SQL stats by ID (iterate since map is keyed by normalized query hash)
		var foundStat *analysis.QueryStat
		for _, stat := range m.SQL.QueryStats {
			if stat.ID == queryID {
				foundStat = stat
				break
			}
		}

		if foundStat != nil {
			detail.NormalizedQuery = foundStat.NormalizedQuery
			detail.RawQuery = foundStat.RawQuery
			detail.Type = analysis.QueryTypeFromID(queryID)
			detail.Category = analysis.QueryCategory(detail.Type)
			detail.PreparedNames = foundStat.PreparedNames
			detail.SlowestRun = slowestRunJSON(foundStat)
			detail.Statistics = &QueryDetailStatsJSON{
				Count:     foundStat.Count,
				TotalTime: formatQueryDuration(foundStat.TotalTime),
				AvgTime:   formatQueryDuration(foundStat.AvgTime),
				MaxTime:   formatQueryDuration(foundStat.MaxTime),
			}

			if foundStat.LastPlan != "" {
				detail.Plan = foundStat.LastPlan
			}

			// Top-5 dimensions (db/user/app/host) that ran this query.
			// Same data the CLI/markdown/HTML renderers expose; computed
			// here at output time from the compact executions storage.
			dims := m.SQL.TopDimensionsForID(queryID, 5)
			detail.TopDatabases = dimensionCountsJSON(dims.Databases)
			detail.TopUsers = dimensionCountsJSON(dims.Users)
			detail.TopApps = dimensionCountsJSON(dims.Apps)
			detail.TopHosts = dimensionCountsJSON(dims.Hosts)

			// Filter executions for this query into a slice, then wrap
			// into lazyExecutions so the StreamSection emits each item
			// directly to the writer (avoids the per-row JSON struct
			// intermediate even on hot queries with millions of rows).
			var filtered []analysis.QueryExecution
			m.SQL.IterateExecutionsForID(queryID, func(exec analysis.QueryExecution) bool {
				filtered = append(filtered, exec)
				return true
			})
			if len(filtered) > 0 {
				detail.Executions = lazyExecutions{executions: filtered}
			}
		} else {
			// Query not found in SQL stats, might be from locks or tempfiles only
			detail.Type = analysis.QueryTypeFromID(queryID)
			detail.Category = analysis.QueryCategory(detail.Type)
		}

		// Find in temp files by ID
		var foundTfStat *analysis.TempFileQueryStat
		for _, tfStat := range m.TempFiles.QueryStats {
			if tfStat.ID == queryID {
				foundTfStat = tfStat
				break
			}
		}
		if foundTfStat != nil {
			if detail.NormalizedQuery == "" {
				detail.NormalizedQuery = foundTfStat.NormalizedQuery
				detail.RawQuery = foundTfStat.RawQuery
			}
			detail.TempFiles = &QueryTempFilesJSON{
				Count:     foundTfStat.Count,
				TotalSize: FormatBytes(foundTfStat.TotalSize),
			}
		}

		// Find in locks by ID
		var foundLockStat *analysis.LockQueryStat
		for _, lockStat := range m.Locks.QueryStats {
			if lockStat.ID == queryID {
				foundLockStat = lockStat
				break
			}
		}
		if foundLockStat != nil {
			if detail.NormalizedQuery == "" {
				detail.NormalizedQuery = foundLockStat.NormalizedQuery
				detail.RawQuery = foundLockStat.RawQuery
			}
			detail.Locks = &QueryLocksJSON{
				AcquiredCount:    foundLockStat.AcquiredCount,
				AcquiredWaitTime: formatQueryDuration(foundLockStat.AcquiredWaitTime),
				WaitingCount:     foundLockStat.StillWaitingCount,
				WaitingTime:      formatQueryDuration(foundLockStat.StillWaitingTime),
				TotalWaitTime:    formatQueryDuration(foundLockStat.TotalWaitTime),
			}
		}

		// EVENTS rollup — every event pattern that has triggered this
		// query. Sorted by per-query trigger count desc with a tie-break
		// on the total event count, same logic as the text and markdown
		// renderers. The relation is materialised here at output time
		// rather than during analysis so the SQL data stays free of an
		// events back-reference.
		var events []SQLDetailEventJSON
		for i := range m.TopEvents {
			for _, tq := range m.TopEvents[i].TriggeringQueries {
				if tq.ID != queryID {
					continue
				}
				events = append(events, SQLDetailEventJSON{
					EventID:      m.TopEvents[i].ID,
					Severity:     m.TopEvents[i].Severity,
					Message:      m.TopEvents[i].Message,
					TriggerCount: tq.Count,
				})
				break
			}
		}
		if len(events) > 1 {
			sort.Slice(events, func(i, j int) bool {
				if events[i].TriggerCount != events[j].TriggerCount {
					return events[i].TriggerCount > events[j].TriggerCount
				}
				return events[i].EventID < events[j].EventID
			})
		}
		if len(events) > 0 {
			detail.Events = events
		}

		// Only add if we found something
		if detail.NormalizedQuery != "" || detail.TempFiles != nil || detail.Locks != nil || len(detail.Events) > 0 {
			details = append(details, detail)
		}
	}

	// Stream the array of details. Each detail emits its Executions
	// item-by-item via StreamSection.
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	bw.WriteByte('[')
	for i, d := range details {
		if i > 0 {
			bw.WriteByte(',')
		}
		bw.WriteString("\n  ")
		if err := d.StreamSection(bw, "  ", "  ", false); err != nil {
			fmt.Fprintf(w, "[ERROR] Failed to export JSON: %v\n", err)
			return
		}
	}
	if len(details) > 0 {
		bw.WriteByte('\n')
	}
	bw.WriteByte(']')
	bw.WriteByte('\n')
}

// roundMicro rounds a millisecond value to the microsecond. Summed
// durations carry float-association noise in their low bits (shard-
// order-dependent since the SQL analyzer went parallel); source log
// durations are microsecond-precise, so anything below 1 µs is noise,
// and rounding keeps the JSON byte-stable regardless of fold order.
func roundMicro(ms float64) float64 {
	return math.Round(ms*1e3) / 1e3
}
