package output

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// JSON structures define the format of the output data.
// Each structure corresponds to a section of metrics:
// - SummaryJSON: global metrics such as start/end dates, total logs, and throughput.
// - SQLPerformanceJSON: metrics related to SQL query performance, including durations and counts.
// - TempFilesJSON: statistics on temporary files (message count, total and average size).
// - MaintenanceJSON: counts of maintenance operations like VACUUM and ANALYZE.
// - CheckpointsJSON: information about checkpoint operations.
// - ConnectionsJSON: connection and session-related statistics.
// - ClientsJSON: counts of unique databases, users, and client applications.

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
	TotalQueryDuration     string               `json:"total_query_duration"`
	TotalQueriesParsed     int                  `json:"total_queries_parsed"`
	TotalUniqueQueries     int                  `json:"total_unique_queries"`
	Top1PercentSlowQueries int                  `json:"top_1_percent_slow_queries"`
	QueryMaxDuration       string               `json:"query_max_duration"`
	QueryMinDuration       string               `json:"query_min_duration"`
	QueryMedianDuration    string               `json:"query_median_duration"`
	Query99thPercentile    string               `json:"query_99th_percentile"`
	Executions             []QueryExecutionJSON `json:"executions"`
	Queries                []QueryStatJSON      `json:"queries"`
}

type QueryExecutionJSON struct {
	Timestamp string `json:"timestamp"`
	Duration  string `json:"duration"`
	QueryID   string `json:"query_id"`
}

type QueryStatJSON struct {
	ID              string  `json:"id"`
	NormalizedQuery string  `json:"normalized_query"`
	RawQuery        string  `json:"raw_query"`
	Count           int     `json:"count"`
	TotalTime       float64 `json:"total_time_ms"`
	AvgTime         float64 `json:"avg_time_ms"`
	MaxTime         float64 `json:"max_time_ms"`
}

// SQL Overview JSON structures (for --sql-overview --json)

type SQLOverviewJSON struct {
	TotalQueries int                       `json:"total_queries"`
	Categories   []CategoryStatJSON        `json:"categories"`
	Types        []TypeStatJSON            `json:"types"`
	ByDatabase   []DimensionBreakdownJSON  `json:"by_database,omitempty"`
	ByUser       []DimensionBreakdownJSON  `json:"by_user,omitempty"`
	ByHost       []DimensionBreakdownJSON  `json:"by_host,omitempty"`
	ByApp        []DimensionBreakdownJSON  `json:"by_app,omitempty"`
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
	Name       string              `json:"name"`
	Count      int                 `json:"count"`
	TotalTime  string              `json:"total_time"`
	QueryTypes []QueryTypeCountJSON `json:"query_types"`
}

type QueryTypeCountJSON struct {
	Type      string `json:"type"`
	Count     int    `json:"count"`
	TotalTime string `json:"total_time"`
}

// SQL Detail JSON structures (for --sql-detail --json)

type SQLDetailJSON struct {
	ID              string                   `json:"id"`
	NormalizedQuery string                   `json:"normalized_query"`
	RawQuery        string                   `json:"raw_query,omitempty"`
	Type            string                   `json:"type"`
	Category        string                   `json:"category"`
	Statistics      *QueryDetailStatsJSON    `json:"statistics,omitempty"`
	Executions      []QueryExecutionJSON     `json:"executions,omitempty"`
	TempFiles       *QueryTempFilesJSON      `json:"temp_files,omitempty"`
	Locks           *QueryLocksJSON          `json:"locks,omitempty"`
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
	SlowestQueries       []QueryRankJSON `json:"slowest_queries"`
	MostFrequentQueries  []QueryRankJSON `json:"most_frequent_queries"`
	MostTimeConsuming    []QueryRankJSON `json:"most_time_consuming"`

	// Full query data for HTML viewer
	Queries    []QueryStatJSON      `json:"queries,omitempty"`
	Executions []QueryExecutionJSON `json:"executions,omitempty"`
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
	TotalMessages int                      `json:"total_messages"`
	TotalSize     string                   `json:"total_size"`
	AvgSize       string                   `json:"avg_size"`
	Events        []TempFileEventJSON      `json:"events"`
	Queries       []TempFileQueryStatJSON  `json:"queries,omitempty"`
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
}

type MaintenanceJSON struct {
	VacuumCount          int               `json:"vacuum_count"`
	AnalyzeCount         int               `json:"analyze_count"`
	VacuumTableCounts    map[string]int    `json:"vacuum_table_counts"`
	AnalyzeTableCounts   map[string]int    `json:"analyze_table_counts"`
	VacuumSpaceRecovered map[string]string `json:"vacuum_space_recovered"`
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
	Events            []LockEventJSON     `json:"events"`
	Queries           []LockQueryStatJSON `json:"queries,omitempty"`
}

type LockEventJSON struct {
	Timestamp    string `json:"timestamp"`
	EventType    string `json:"event_type"`
	LockType     string `json:"lock_type,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	WaitTime     string `json:"wait_time,omitempty"`
	ProcessID    string `json:"process_id"`
	QueryID      string `json:"query_id,omitempty"`
}

type LockQueryStatJSON struct {
	ID                  string `json:"id"`
	NormalizedQuery     string `json:"normalized_query"`
	RawQuery            string `json:"raw_query"`
	AcquiredCount       int    `json:"acquired_count"`
	AcquiredWaitTime    string `json:"acquired_wait_time"`
	StillWaitingCount   int    `json:"still_waiting_count"`
	StillWaitingTime    string `json:"still_waiting_time"`
	TotalWaitTime       string `json:"total_wait_time"`
}

type CheckpointTypeJSON struct {
	Count      int      `json:"count"`
	Percentage float64  `json:"percentage"`
	Rate       float64  `json:"rate_per_hour"`
	Events     []string `json:"events"`
}

type CheckpointsJSON struct {
	TotalCheckpoints  int                           `json:"total_checkpoints"`
	AvgCheckpointTime string                        `json:"avg_checkpoint_time"`
	MaxCheckpointTime string                        `json:"max_checkpoint_time"`
	Events            []string                      `json:"events"`
	Types             map[string]CheckpointTypeJSON `json:"types,omitempty"`
}

type SessionStatsJSON struct {
	Count     int    `json:"count"`
	Min       string `json:"min_duration"`
	Max       string `json:"max_duration"`
	Avg       string `json:"avg_duration"`
	Median    string `json:"median_duration"`
	Cumulated string `json:"cumulated_duration"`
}

// ConcurrentBucketJSON represents one bucket in the concurrent sessions histogram.
type ConcurrentBucketJSON struct {
	Label    string `json:"label"`
	Count    int    `json:"count"`
	PeakTime string `json:"peak_time,omitempty"`
}

type ConnectionsJSON struct {
	ConnectionCount       int                         `json:"connection_count"`
	AvgConnectionsPerHour string                      `json:"avg_connections_per_hour"`
	DisconnectionCount    int                         `json:"disconnection_count"`
	AvgSessionTime        string                      `json:"avg_session_time"`

	// Session statistics
	SessionStats        *SessionStatsJSON           `json:"session_stats,omitempty"`
	SessionDistribution map[string]int              `json:"session_distribution,omitempty"`

	// Breakdown by entity
	SessionsByUser     map[string]SessionStatsJSON `json:"sessions_by_user,omitempty"`
	SessionsByDatabase map[string]SessionStatsJSON `json:"sessions_by_database,omitempty"`
	SessionsByHost     map[string]SessionStatsJSON `json:"sessions_by_host,omitempty"`

	// Concurrent sessions
	PeakConcurrent            int                    `json:"peak_concurrent_sessions,omitempty"`
	PeakConcurrentTime        string                 `json:"peak_concurrent_timestamp,omitempty"`
	ConcurrentSessionsHistory []ConcurrentBucketJSON `json:"concurrent_sessions_histogram,omitempty"`

	// Raw events
	Connections   []string           `json:"connections"`
	SessionEvents []SessionEventJSON `json:"session_events,omitempty"`
}

// SessionEventJSON represents a session with start and end times for client-side sweep-line.
type SessionEventJSON struct {
	Start string `json:"s"` // Short keys for smaller JSON
	End   string `json:"e"`
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

type ErrorClassJSON struct {
	ClassCode   string `json:"class_code"`
	Description string `json:"description"`
	Count       int    `json:"count"`
}

// ExportJSON brings together all metrics into one composite structure,
// converts it into an indented JSON string, and outputs the result.
// Only sections with data are included in the output.
// When full is true, includes sql_overview and enriched sql_performance sections.
func ExportJSON(m analysis.AggregatedMetrics, sections []string, full bool) {
	data := buildJSONData(m, sections, full)

	// Marshal and output
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Println("[ERROR] Failed to export JSON:", err)
		return
	}
	fmt.Println(string(jsonData))
}

// ExportJSONString returns the JSON export as a string instead of printing.
// This is useful for WASM and other contexts where stdout is not available.
func ExportJSONString(m analysis.AggregatedMetrics, sections []string) (string, error) {
	return ExportJSONStringWithMeta(m, sections, false, nil)
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
func ExportJSONStringWithMeta(m analysis.AggregatedMetrics, sections []string, full bool, meta *MetaInfo) (string, error) {
	data := buildJSONData(m, sections, full)
	if meta != nil {
		data["meta"] = meta
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
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

	if has("events") && len(m.EventSummaries) > 0 {
		events := make([]EventJSON, len(m.EventSummaries))
		for i, ev := range m.EventSummaries {
			events[i] = EventJSON{
				Type:       ev.Type,
				Count:      ev.Count,
				Percentage: ev.Percentage,
			}
		}
		data["events"] = events
	}

	if has("errors") && len(m.ErrorClasses) > 0 {
		errorClasses := make([]ErrorClassJSON, len(m.ErrorClasses))
		for i, ec := range m.ErrorClasses {
			errorClasses[i] = ErrorClassJSON{
				ClassCode:   ec.ClassCode,
				Description: ec.Description,
				Count:       ec.Count,
			}
		}
		data["error_classes"] = errorClasses
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
			TotalSize:     formatBytes(m.TempFiles.TotalSize),
			AvgSize:       formatBytes(m.TempFiles.TotalSize / int64(m.TempFiles.Count)),
			Events:        []TempFileEventJSON{},
			Queries:       []TempFileQueryStatJSON{},
		}
		for _, event := range m.TempFiles.Events {
			tf.Events = append(tf.Events, TempFileEventJSON{
				Timestamp: event.Timestamp.Format("2006-01-02 15:04:05"),
				Size:      formatBytes(int64(event.Size)),
				QueryID:   event.QueryID,
			})
		}
		for _, stat := range m.TempFiles.QueryStats {
			tf.Queries = append(tf.Queries, TempFileQueryStatJSON{
				ID:              stat.ID,
				NormalizedQuery: stat.NormalizedQuery,
				RawQuery:        stat.RawQuery,
				Count:           stat.Count,
				TotalSize:       formatBytes(stat.TotalSize),
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
		data["maintenance"] = MaintenanceJSON{
			VacuumCount:          m.Vacuum.VacuumCount,
			AnalyzeCount:         m.Vacuum.AnalyzeCount,
			VacuumTableCounts:    m.Vacuum.VacuumTableCounts,
			AnalyzeTableCounts:   m.Vacuum.AnalyzeTableCounts,
			VacuumSpaceRecovered: formatVacuumSpaceRecovered(m.Vacuum.VacuumSpaceRecovered),
		}
	}

	if has("checkpoints") && m.Checkpoints.CompleteCount > 0 {
		cp := CheckpointsJSON{
			TotalCheckpoints:  m.Checkpoints.CompleteCount,
			AvgCheckpointTime: formatSeconds(m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)),
			MaxCheckpointTime: formatSeconds(m.Checkpoints.MaxWriteTimeSeconds),
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
			Connections: []string{},
		}
		for _, t := range m.Connections.Connections {
			conn.Connections = append(conn.Connections, t.Format("2006-01-02 15:04:05"))
		}
		if len(m.Connections.SessionDurations) > 0 {
			stats := analysis.CalculateDurationStats(m.Connections.SessionDurations)
			var cumulated time.Duration
			for _, d := range m.Connections.SessionDurations {
				cumulated += d
			}
			conn.SessionStats = &SessionStatsJSON{
				Count:     stats.Count,
				Min:       stats.Min.String(),
				Max:       stats.Max.String(),
				Avg:       stats.Avg.String(),
				Median:    stats.Median.String(),
				Cumulated: cumulated.String(),
			}
			conn.SessionDistribution = analysis.CalculateDurationDistribution(m.Connections.SessionDurations)
		}
		if len(m.Connections.SessionsByUser) > 0 {
			conn.SessionsByUser = make(map[string]SessionStatsJSON)
			for user, durations := range m.Connections.SessionsByUser {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				conn.SessionsByUser[user] = SessionStatsJSON{
					Count: stats.Count, Min: stats.Min.String(), Max: stats.Max.String(),
					Avg: stats.Avg.String(), Median: stats.Median.String(), Cumulated: cumulated.String(),
				}
			}
		}
		if len(m.Connections.SessionsByDatabase) > 0 {
			conn.SessionsByDatabase = make(map[string]SessionStatsJSON)
			for db, durations := range m.Connections.SessionsByDatabase {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				conn.SessionsByDatabase[db] = SessionStatsJSON{
					Count: stats.Count, Min: stats.Min.String(), Max: stats.Max.String(),
					Avg: stats.Avg.String(), Median: stats.Median.String(), Cumulated: cumulated.String(),
				}
			}
		}
		if len(m.Connections.SessionsByHost) > 0 {
			conn.SessionsByHost = make(map[string]SessionStatsJSON)
			for host, durations := range m.Connections.SessionsByHost {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				conn.SessionsByHost[host] = SessionStatsJSON{
					Count: stats.Count, Min: stats.Min.String(), Max: stats.Max.String(),
					Avg: stats.Avg.String(), Median: stats.Median.String(), Cumulated: cumulated.String(),
				}
			}
		}
		if m.Connections.PeakConcurrentSessions > 0 {
			conn.PeakConcurrent = m.Connections.PeakConcurrentSessions
			conn.PeakConcurrentTime = m.Connections.PeakConcurrentTimestamp.Format("2006-01-02 15:04:05")
		}
		// Export session events for client-side sweep-line (allows bucket adjustment)
		if len(m.Connections.SessionEvents) > 0 {
			conn.SessionEvents = make([]SessionEventJSON, 0, len(m.Connections.SessionEvents))
			for _, se := range m.Connections.SessionEvents {
				if !se.StartTime.IsZero() && !se.EndTime.IsZero() {
					conn.SessionEvents = append(conn.SessionEvents, SessionEventJSON{
						Start: se.StartTime.Format("2006-01-02T15:04:05"),
						End:   se.EndTime.Format("2006-01-02T15:04:05"),
					})
				}
			}
		}
		// Pre-computed concurrent sessions histogram (12 buckets) as fallback
		if len(m.Connections.SessionEvents) > 0 && !m.Global.MinTimestamp.IsZero() && !m.Global.MaxTimestamp.IsZero() {
			hist, labels, _, peakTimes := computeConcurrentHistogram(
				m.Connections.SessionEvents,
				m.Global.MinTimestamp,
				m.Global.MaxTimestamp,
				12,
			)
			if len(labels) > 0 {
				conn.ConcurrentSessionsHistory = make([]ConcurrentBucketJSON, len(labels))
				for i, label := range labels {
					bucket := ConcurrentBucketJSON{Label: label, Count: hist[label]}
					if pt, ok := peakTimes[label]; ok && !pt.IsZero() {
						bucket.PeakTime = pt.Format("15:04:05")
					}
					conn.ConcurrentSessionsHistory[i] = bucket
				}
			}
		}
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

	// Full mode: add sql_overview and enriched sql_performance at the end
	if full && m.SQL.TotalQueries > 0 {
		// SQL overview (categories, types, dimensional breakdowns)
		data["sql_overview"] = buildSQLOverviewData(m.SQL)

		// Enriched SQL performance (basic stats + histograms + top queries)
		data["sql_performance"] = buildFullSQLPerformance(m.SQL)
	}

	return data
}

// buildSQLOverviewData builds SQL overview data for JSON export.
func buildSQLOverviewData(m analysis.SqlMetrics) SQLOverviewJSON {
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
		return overview.Categories[i].Count > overview.Categories[j].Count
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
		return overview.Types[i].Count > overview.Types[j].Count
	})

	overview.ByDatabase = convertDimensionBreakdown(m.QueryTypesByDatabase)
	overview.ByUser = convertDimensionBreakdown(m.QueryTypesByUser)
	overview.ByHost = convertDimensionBreakdown(m.QueryTypesByHost)
	overview.ByApp = convertDimensionBreakdown(m.QueryTypesByApp)

	return overview
}

// buildFullSQLPerformance builds enriched SQL performance data for --full mode.
// Includes basic stats, duration distribution histogram, and top queries lists.
func buildFullSQLPerformance(m analysis.SqlMetrics) SQLPerformanceDetailJSON {
	// Top 1% slow computation
	top1Slow := 0
	if len(m.Executions) > 0 {
		threshold := m.P99QueryDuration
		for _, exec := range m.Executions {
			if exec.Duration >= threshold {
				top1Slow++
			}
		}
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
	for _, exec := range m.Executions {
		for i, b := range buckets {
			if b.threshold < 0 || exec.Duration < b.threshold {
				bucketCounts[i]++
				break
			}
		}
	}

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
		return stats[i].stat.MaxTime > stats[j].stat.MaxTime
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
		return stats[i].stat.Count > stats[j].stat.Count
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
		return stats[i].stat.TotalTime > stats[j].stat.TotalTime
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
		return stats[i].stat.TotalTime > stats[j].stat.TotalTime
	})
	for _, s := range stats {
		perf.Queries = append(perf.Queries, QueryStatJSON{
			ID:              s.id,
			NormalizedQuery: s.query,
			RawQuery:        s.stat.RawQuery,
			Count:           s.stat.Count,
			TotalTime:       s.stat.TotalTime,
			AvgTime:         s.stat.AvgTime,
			MaxTime:         s.stat.MaxTime,
		})
	}

	// Executions for time charts
	for _, exec := range m.Executions {
		perf.Executions = append(perf.Executions, QueryExecutionJSON{
			Timestamp: exec.Timestamp.Format("2006-01-02T15:04:05"),
			Duration:  formatQueryDuration(exec.Duration),
			QueryID:   exec.QueryID,
		})
	}

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
		formatted[table] = formatBytes(size)
	}
	return formatted
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
func convertSQLPerformance(m analysis.SqlMetrics) SQLPerformanceJSON {

	// Top 1% slow computation
	top1Slow := 0
	if len(m.Executions) > 0 {
		threshold := m.P99QueryDuration
		for _, exec := range m.Executions {
			if exec.Duration >= threshold {
				top1Slow++
			}
		}
	}

	// SQL duration datas for each statement
	executionsJSON := make([]QueryExecutionJSON, len(m.Executions))
	for i, exec := range m.Executions {
		executionsJSON[i] = QueryExecutionJSON{
			Timestamp: exec.Timestamp.Format("2006-01-02 15:04:05"),
			Duration:  formatQueryDuration(exec.Duration),
			QueryID:   exec.QueryID,
		}
	}

	// Export all query stats (sorted by ID for deterministic output)
	queriesJSON := make([]QueryStatJSON, 0, len(m.QueryStats))
	for _, stat := range m.QueryStats {
		queriesJSON = append(queriesJSON, QueryStatJSON{
			ID:              stat.ID,
			NormalizedQuery: stat.NormalizedQuery,
			RawQuery:        stat.RawQuery,
			Count:           stat.Count,
			TotalTime:       stat.TotalTime,
			AvgTime:         stat.AvgTime,
			MaxTime:         stat.MaxTime,
		})
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
		Executions:             executionsJSON,
		Queries:                queriesJSON,
	}
}

// convertLocks processes lock metrics to create a JSON structure.
func convertLocks(m analysis.LockMetrics) LocksJSON {
	// Calculate average wait time
	avgWaitTime := "0 ms"
	if m.WaitingEvents+m.AcquiredEvents > 0 {
		avg := m.TotalWaitTime / float64(m.WaitingEvents+m.AcquiredEvents)
		avgWaitTime = fmt.Sprintf("%.2f ms", avg)
	}

	// Format total wait time
	totalWaitTime := fmt.Sprintf("%.2f s", m.TotalWaitTime/1000)

	// Convert events
	eventsJSON := make([]LockEventJSON, len(m.Events))
	for i, event := range m.Events {
		waitTime := ""
		if event.WaitTime > 0 {
			waitTime = fmt.Sprintf("%.2f ms", event.WaitTime)
		}
		eventsJSON[i] = LockEventJSON{
			Timestamp:    event.Timestamp.Format("2006-01-02 15:04:05"),
			EventType:    event.EventType,
			LockType:     event.LockType,
			ResourceType: event.ResourceType,
			WaitTime:     waitTime,
			ProcessID:    event.ProcessID,
			QueryID:      event.QueryID,
		}
	}

	// Export all query stats (sorted by ID for deterministic output)
	queriesJSON := make([]LockQueryStatJSON, 0, len(m.QueryStats))
	for _, stat := range m.QueryStats {
		queriesJSON = append(queriesJSON, LockQueryStatJSON{
			ID:                stat.ID,
			NormalizedQuery:   stat.NormalizedQuery,
			RawQuery:          stat.RawQuery,
			AcquiredCount:     stat.AcquiredCount,
			AcquiredWaitTime:  fmt.Sprintf("%.2f ms", stat.AcquiredWaitTime),
			StillWaitingCount: stat.StillWaitingCount,
			StillWaitingTime:  fmt.Sprintf("%.2f ms", stat.StillWaitingTime),
			TotalWaitTime:     fmt.Sprintf("%.2f ms", stat.TotalWaitTime),
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
		Events:            eventsJSON,
		Queries:           queriesJSON,
	}
}

// ExportSQLOverviewJSON exports SQL overview data as JSON.
func ExportSQLOverviewJSON(m analysis.SqlMetrics) {
	if m.TotalQueries == 0 {
		fmt.Println("{}")
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
		return overview.Categories[i].Count > overview.Categories[j].Count
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
		return overview.Types[i].Count > overview.Types[j].Count
	})

	// Build dimensional breakdowns
	overview.ByDatabase = convertDimensionBreakdown(m.QueryTypesByDatabase)
	overview.ByUser = convertDimensionBreakdown(m.QueryTypesByUser)
	overview.ByHost = convertDimensionBreakdown(m.QueryTypesByHost)
	overview.ByApp = convertDimensionBreakdown(m.QueryTypesByApp)

	// Marshal and output
	jsonData, err := json.MarshalIndent(overview, "", "  ")
	if err != nil {
		fmt.Println("[ERROR] Failed to export JSON:", err)
		return
	}
	fmt.Println(string(jsonData))
}

// ExportSQLPerformanceJSON exports detailed SQL performance data as JSON.
func ExportSQLPerformanceJSON(m analysis.SqlMetrics) {
	if m.TotalQueries == 0 {
		fmt.Println("{}")
		return
	}

	// Top 1% slow computation
	top1Slow := 0
	if len(m.Executions) > 0 {
		threshold := m.P99QueryDuration
		for _, exec := range m.Executions {
			if exec.Duration >= threshold {
				top1Slow++
			}
		}
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
	for _, exec := range m.Executions {
		for i, b := range buckets {
			if b.threshold < 0 || exec.Duration < b.threshold {
				bucketCounts[i]++
				break
			}
		}
	}

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
		return stats[i].stat.MaxTime > stats[j].stat.MaxTime
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
		return stats[i].stat.Count > stats[j].stat.Count
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
		return stats[i].stat.TotalTime > stats[j].stat.TotalTime
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

	// Marshal and output
	jsonData, err := json.MarshalIndent(perf, "", "  ")
	if err != nil {
		fmt.Println("[ERROR] Failed to export JSON:", err)
		return
	}
	fmt.Println(string(jsonData))
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
			return queryTypes[i].Count > queryTypes[j].Count
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
		return result[i].Count > result[j].Count
	})

	return result
}

// ExportSQLDetailJSON exports SQL query details as JSON.
func ExportSQLDetailJSON(m analysis.AggregatedMetrics, queryIDs []string) {
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
			detail.Statistics = &QueryDetailStatsJSON{
				Count:     foundStat.Count,
				TotalTime: formatQueryDuration(foundStat.TotalTime),
				AvgTime:   formatQueryDuration(foundStat.AvgTime),
				MaxTime:   formatQueryDuration(foundStat.MaxTime),
			}

			// Find executions for this query
			for _, exec := range m.SQL.Executions {
				if exec.QueryID == queryID {
					detail.Executions = append(detail.Executions, QueryExecutionJSON{
						Timestamp: exec.Timestamp.Format("2006-01-02 15:04:05"),
						Duration:  formatQueryDuration(exec.Duration),
						QueryID:   exec.QueryID,
					})
				}
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
				TotalSize: formatBytes(foundTfStat.TotalSize),
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
				AcquiredWaitTime: fmt.Sprintf("%.2f ms", foundLockStat.AcquiredWaitTime),
				WaitingCount:     foundLockStat.StillWaitingCount,
				WaitingTime:      fmt.Sprintf("%.2f ms", foundLockStat.StillWaitingTime),
				TotalWaitTime:    fmt.Sprintf("%.2f ms", foundLockStat.TotalWaitTime),
			}
		}

		// Only add if we found something
		if detail.NormalizedQuery != "" || detail.TempFiles != nil || detail.Locks != nil {
			details = append(details, detail)
		}
	}

	// Marshal and output
	jsonData, err := json.MarshalIndent(details, "", "  ")
	if err != nil {
		fmt.Println("[ERROR] Failed to export JSON:", err)
		return
	}
	fmt.Println(string(jsonData))
}
