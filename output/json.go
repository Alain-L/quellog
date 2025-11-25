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
	PeakConcurrent     int    `json:"peak_concurrent_sessions,omitempty"`
	PeakConcurrentTime string `json:"peak_concurrent_timestamp,omitempty"`

	// Raw events
	Connections []string `json:"connections"`
}

type ClientsJSON struct {
	UniqueDatabases int `json:"unique_databases"`
	UniqueUsers     int `json:"unique_users"`
	UniqueApps      int `json:"unique_apps"`
	UniqueHosts     int `json:"unique_hosts"`
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
func ExportJSON(m analysis.AggregatedMetrics, sections []string) {
	// Build dynamic JSON structure
	data := make(map[string]interface{})

	// Check flags
	has := func(name string) bool {
		for _, s := range sections {
			if s == name || s == "all" {
				return true
			}
		}
		return false
	}

	// summary
	if has("summary") {
		data["summary"] = convertSummary(m)
	}

	// Events summary
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

	// Error classes
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

	// Conditionally include SQL performance
	if has("sql_performance") && m.SQL.TotalQueries > 0 {
		data["sql_performance"] = convertSQLPerformance(m.SQL)
	}

	// Conditionally include temp files
	if has("tempfiles") && m.TempFiles.Count > 0 {
		tf := TempFilesJSON{
			TotalMessages: m.TempFiles.Count,
			TotalSize:     formatBytes(m.TempFiles.TotalSize),
			AvgSize:       formatBytes(m.TempFiles.TotalSize / int64(m.TempFiles.Count)),
			Events:        []TempFileEventJSON{},
			Queries:       []TempFileQueryStatJSON{},
		}
		// Export events with QueryID
		for _, event := range m.TempFiles.Events {
			tf.Events = append(tf.Events, TempFileEventJSON{
				Timestamp: event.Timestamp.Format("2006-01-02 15:04:05"),
				Size:      formatBytes(int64(event.Size)),
				QueryID:   event.QueryID,
			})
		}
		// Export query stats (sorted by ID for deterministic output)
		for _, stat := range m.TempFiles.QueryStats {
			tf.Queries = append(tf.Queries, TempFileQueryStatJSON{
				ID:              stat.ID,
				NormalizedQuery: stat.NormalizedQuery,
				RawQuery:        stat.RawQuery,
				Count:           stat.Count,
				TotalSize:       formatBytes(stat.TotalSize),
			})
		}
		// Sort by ID for deterministic JSON output
		sort.Slice(tf.Queries, func(i, j int) bool {
			return tf.Queries[i].ID < tf.Queries[j].ID
		})
		data["temp_files"] = tf
	}

	// Conditionally include locks
	if has("locks") && m.Locks.TotalEvents > 0 {
		data["locks"] = convertLocks(m.Locks)
	}

	// Conditionally include maintenance
	if has("maintenance") && (m.Vacuum.VacuumCount > 0 || m.Vacuum.AnalyzeCount > 0) {
		data["maintenance"] = MaintenanceJSON{
			VacuumCount:          m.Vacuum.VacuumCount,
			AnalyzeCount:         m.Vacuum.AnalyzeCount,
			VacuumTableCounts:    m.Vacuum.VacuumTableCounts,
			AnalyzeTableCounts:   m.Vacuum.AnalyzeTableCounts,
			VacuumSpaceRecovered: formatVacuumSpaceRecovered(m.Vacuum.VacuumSpaceRecovered),
		}
	}

	// Conditionally include checkpoints
	if has("checkpoints") && m.Checkpoints.CompleteCount > 0 {
		cp := CheckpointsJSON{
			TotalCheckpoints:  m.Checkpoints.CompleteCount,
			AvgCheckpointTime: formatSeconds(m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)),
			MaxCheckpointTime: formatSeconds(m.Checkpoints.MaxWriteTimeSeconds),
		}

		// Ajouter les événements globaux
		for _, t := range m.Checkpoints.Events {
			cp.Events = append(cp.Events, t.Format("2006-01-02 15:04:05"))
		}

		// Ajouter les types de checkpoints si disponibles
		if len(m.Checkpoints.TypeCounts) > 0 {
			cp.Types = make(map[string]CheckpointTypeJSON)

			// Calculer la durée totale pour le taux
			duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
			durationHours := duration.Hours()

			for cpType, count := range m.Checkpoints.TypeCounts {
				percentage := float64(count) / float64(m.Checkpoints.CompleteCount) * 100

				// Calculer le taux (checkpoints par heure) pour ce type
				rate := 0.0
				if durationHours > 0 {
					rate = float64(count) / durationHours
				}

				typeJSON := CheckpointTypeJSON{
					Count:      count,
					Percentage: percentage,
					Rate:       rate,
				}

				// Ajouter les événements pour ce type
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

	// Conditionally include connections
	if has("connections") && (m.Connections.ConnectionReceivedCount > 0 || m.Connections.DisconnectionCount > 0) {
		// Calculate duration for avg connections per hour
		duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
		durationHours := duration.Hours()
		if durationHours == 0 {
			durationHours = 1 // Avoid division by zero
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

		// Add connection timestamps
		for _, t := range m.Connections.Connections {
			conn.Connections = append(conn.Connections, t.Format("2006-01-02 15:04:05"))
		}

		// Add global session statistics if we have session data
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

			// Add session duration distribution
			conn.SessionDistribution = analysis.CalculateDurationDistribution(m.Connections.SessionDurations)
		}

		// Add sessions by user
		if len(m.Connections.SessionsByUser) > 0 {
			conn.SessionsByUser = make(map[string]SessionStatsJSON)
			for user, durations := range m.Connections.SessionsByUser {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				conn.SessionsByUser[user] = SessionStatsJSON{
					Count:     stats.Count,
					Min:       stats.Min.String(),
					Max:       stats.Max.String(),
					Avg:       stats.Avg.String(),
					Median:    stats.Median.String(),
					Cumulated: cumulated.String(),
				}
			}
		}

		// Add sessions by database
		if len(m.Connections.SessionsByDatabase) > 0 {
			conn.SessionsByDatabase = make(map[string]SessionStatsJSON)
			for db, durations := range m.Connections.SessionsByDatabase {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				conn.SessionsByDatabase[db] = SessionStatsJSON{
					Count:     stats.Count,
					Min:       stats.Min.String(),
					Max:       stats.Max.String(),
					Avg:       stats.Avg.String(),
					Median:    stats.Median.String(),
					Cumulated: cumulated.String(),
				}
			}
		}

		// Add sessions by host
		if len(m.Connections.SessionsByHost) > 0 {
			conn.SessionsByHost = make(map[string]SessionStatsJSON)
			for host, durations := range m.Connections.SessionsByHost {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				conn.SessionsByHost[host] = SessionStatsJSON{
					Count:     stats.Count,
					Min:       stats.Min.String(),
					Max:       stats.Max.String(),
					Avg:       stats.Avg.String(),
					Median:    stats.Median.String(),
					Cumulated: cumulated.String(),
				}
			}
		}

		// Add peak concurrent sessions
		if m.Connections.PeakConcurrentSessions > 0 {
			conn.PeakConcurrent = m.Connections.PeakConcurrentSessions
			conn.PeakConcurrentTime = m.Connections.PeakConcurrentTimestamp.Format("2006-01-02 15:04:05")
		}

		data["connections"] = conn
	}

	// Clients and detailed lists
	if has("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0 || m.UniqueEntities.UniqueHosts > 0) {
		// Basic counts
		data["clients"] = ClientsJSON{
			UniqueDatabases: m.UniqueEntities.UniqueDbs,
			UniqueUsers:     m.UniqueEntities.UniqueUsers,
			UniqueApps:      m.UniqueEntities.UniqueApps,
			UniqueHosts:     m.UniqueEntities.UniqueHosts,
		}
		// Detailed lists, excluding sole UNKNOWN entries
		if m.UniqueEntities.UniqueUsers > 0 && !(len(m.UniqueEntities.Users) == 1 && m.UniqueEntities.Users[0] == "UNKNOWN") {
			data["users"] = m.UniqueEntities.Users
		}
		if m.UniqueEntities.UniqueApps > 0 && !(len(m.UniqueEntities.Apps) == 1 && m.UniqueEntities.Apps[0] == "UNKNOWN") {
			data["apps"] = m.UniqueEntities.Apps
		}
		if m.UniqueEntities.UniqueDbs > 0 && !(len(m.UniqueEntities.DBs) == 1 && m.UniqueEntities.DBs[0] == "UNKNOWN") {
			data["databases"] = m.UniqueEntities.DBs
		}
		if m.UniqueEntities.UniqueHosts > 0 && !(len(m.UniqueEntities.Hosts) == 1 && m.UniqueEntities.Hosts[0] == "UNKNOWN") {
			data["hosts"] = m.UniqueEntities.Hosts
		}
	}

	// Marshal and output
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Println("[ERROR] Failed to export JSON:", err)
		return
	}
	fmt.Println(string(jsonData))
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
