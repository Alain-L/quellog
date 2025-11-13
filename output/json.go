package output

import (
	"github.com/Alain-L/quellog/analysis"
	"encoding/json"
	"fmt"
	"time"
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
	StartDate  string `json:"start_date"`
	EndDate    string `json:"end_date"`
	Duration   string `json:"duration"`
	TotalLogs  int    `json:"total_logs"`
	Throughput string `json:"throughput"`
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
}

type QueryExecutionJSON struct {
	Timestamp string `json:"timestamp"`
	Duration  string `json:"duration"`
}

type TempFilesJSON struct {
	TotalMessages int                 `json:"total_messages"`
	TotalSize     string              `json:"total_size"`
	AvgSize       string              `json:"avg_size"`
	Events        []TempFileEventJSON `json:"events"`
}

type TempFileEventJSON struct {
	Timestamp string `json:"timestamp"`
	Size      string `json:"size"`
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
	TopQueries        []LockQueryStatJSON `json:"top_queries,omitempty"`
}

type LockEventJSON struct {
	Timestamp    string `json:"timestamp"`
	EventType    string `json:"event_type"`
	LockType     string `json:"lock_type,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	WaitTime     string `json:"wait_time,omitempty"`
	ProcessID    string `json:"process_id"`
}

type LockQueryStatJSON struct {
	ID                  string `json:"id"`
	NormalizedQuery     string `json:"normalized_query"`
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

type ConnectionsJSON struct {
	ConnectionCount       int      `json:"connection_count"`
	AvgConnectionsPerHour string   `json:"avg_connections_per_hour"`
	DisconnectionCount    int      `json:"disconnection_count"`
	AvgSessionTime        string   `json:"avg_session_time"`
	Connections           []string `json:"connections"`
}

type ClientsJSON struct {
	UniqueDatabases int `json:"unique_databases"`
	UniqueUsers     int `json:"unique_users"`
	UniqueApps      int `json:"unique_apps"`
	UniqueHosts     int `json:"unique_hosts"`
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
		events := make(map[string]int)
		for _, ev := range m.EventSummaries {
			events[ev.Type] = ev.Count
		}
		data["events"] = events
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
		}
		for _, event := range m.TempFiles.Events {
			tf.Events = append(tf.Events, TempFileEventJSON{
				Timestamp: event.Timestamp.Format("2006-01-02 15:04:05"),
				Size:      formatBytes(int64(event.Size)),
			})
		}
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
		conn := ConnectionsJSON{
			ConnectionCount:       m.Connections.ConnectionReceivedCount,
			AvgConnectionsPerHour: fmt.Sprintf("%.2f", float64(m.Connections.ConnectionReceivedCount)/24.0),
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
		StartDate:  m.Global.MinTimestamp.Format("2006-01-02 15:04:05"),
		EndDate:    m.Global.MaxTimestamp.Format("2006-01-02 15:04:05"),
		Duration:   duration.String(),
		TotalLogs:  m.Global.Count,
		Throughput: fmt.Sprintf("%.2f entries/s", throughput),
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
		}
	}

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
		}
	}

	// Convert top queries (sorted by total wait time)
	type queryPair struct {
		stat *analysis.LockQueryStat
	}
	var pairs []queryPair
	for _, stat := range m.QueryStats {
		pairs = append(pairs, queryPair{stat})
	}
	// Sort by total wait time descending, then by ID ascending for deterministic ordering
	for i := 0; i < len(pairs)-1; i++ {
		for j := i + 1; j < len(pairs); j++ {
			// Primary: sort by total wait time (descending)
			if pairs[j].stat.TotalWaitTime > pairs[i].stat.TotalWaitTime {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			} else if pairs[j].stat.TotalWaitTime == pairs[i].stat.TotalWaitTime {
				// Secondary: sort by ID (ascending) for stable output
				if pairs[j].stat.ID < pairs[i].stat.ID {
					pairs[i], pairs[j] = pairs[j], pairs[i]
				}
			}
		}
	}

	topQueries := make([]LockQueryStatJSON, 0, 10)
	limit := 10
	if len(pairs) < limit {
		limit = len(pairs)
	}
	for i := 0; i < limit; i++ {
		stat := pairs[i].stat
		topQueries = append(topQueries, LockQueryStatJSON{
			ID:                stat.ID,
			NormalizedQuery:   stat.NormalizedQuery,
			AcquiredCount:     stat.AcquiredCount,
			AcquiredWaitTime:  fmt.Sprintf("%.2f ms", stat.AcquiredWaitTime),
			StillWaitingCount: stat.StillWaitingCount,
			StillWaitingTime:  fmt.Sprintf("%.2f ms", stat.StillWaitingTime),
			TotalWaitTime:     fmt.Sprintf("%.2f ms", stat.TotalWaitTime),
		})
	}

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
		TopQueries:        topQueries,
	}
}
