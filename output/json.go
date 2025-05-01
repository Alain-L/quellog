package output

import (
	"dalibo/quellog/analysis"
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

type CheckpointsJSON struct {
	TotalCheckpoints  int      `json:"total_checkpoints"`
	AvgCheckpointTime string   `json:"avg_checkpoint_time"`
	MaxCheckpointTime string   `json:"max_checkpoint_time"`
	Events            []string `json:"events"`
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

// ExportJSON brings together all metrics into one composite structure,
// converts it into an indented JSON string, and outputs the result.
// It processes sections for summary, SQL performance, temporary files,
// maintenance tasks, checkpoints, connections, and client information.
func ExportJSON(m analysis.AggregatedMetrics) {
	// Calculate average session time

	avgSessionTime := time.Duration(0)
	if m.Connections.DisconnectionCount > 0 {
		avgSessionTime = m.Connections.TotalSessionTime / time.Duration(m.Connections.DisconnectionCount)
	}

	// Calculate average temp file size with a check to avoid division by zero.
	var avgTempFileSize int64
	if m.TempFiles.Count > 0 {
		avgTempFileSize = m.TempFiles.TotalSize / int64(m.TempFiles.Count)
	} else {
		avgTempFileSize = 0
	}

	// Calculate average checkpoint time only if there is at least one complete checkpoint.
	var avgCheckpointTime float64

	if m.Checkpoints.CompleteCount > 0 {
		fmt.Printf("[DEBUG] m.Checkpoints.TotalWriteTimeSeconds: %f\n", m.Checkpoints.TotalWriteTimeSeconds)
		fmt.Printf("[DEBUG] m.Checkpoints.CompleteCount: %d\n", m.Checkpoints.CompleteCount)
		avgCheckpointTime = m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)
	} else {
		avgCheckpointTime = 0
	}

	// Calculate average connections per hour (dividing by 24.0 is safe as 24.0 is constant).
	avgConnectionsPerHour := float64(m.Connections.ConnectionReceivedCount) / 24.0

	// Convert the slice of time.Time to a slice of formatted strings

	// for checkpoints
	events := make([]string, len(m.Checkpoints.Events))
	for i, t := range m.Checkpoints.Events {
		events[i] = t.Format("2006-01-02 15:04:05")
	}

	// for connections
	connections := make([]string, len(m.Connections.Connections))
	for i, t := range m.Connections.Connections {
		connections[i] = t.Format("2006-01-02 15:04:05")
	}

	// for temp files, with size info added
	tempFileEvents := make([]TempFileEventJSON, len(m.TempFiles.Events))
	for i, event := range m.TempFiles.Events {
		tempFileEvents[i] = TempFileEventJSON{
			Timestamp: event.Timestamp.Format("2006-01-02 15:04:05"),
			Size:      formatBytes(int64(event.Size)),
		}
	}

	data := struct {
		Summary     SummaryJSON        `json:"summary"`
		SQL         SQLPerformanceJSON `json:"sql_performance"`
		TempFiles   TempFilesJSON      `json:"temp_files"`
		Maintenance MaintenanceJSON    `json:"maintenance"`
		Checkpoints CheckpointsJSON    `json:"checkpoints"`
		Connections ConnectionsJSON    `json:"connections"`
		Clients     ClientsJSON        `json:"clients"`
	}{
		Summary: convertSummary(m),
		SQL:     convertSQLPerformance(m.SQL),
		TempFiles: TempFilesJSON{
			TotalMessages: m.TempFiles.Count,
			TotalSize:     formatBytes(m.TempFiles.TotalSize),
			AvgSize:       formatBytes(avgTempFileSize),
			Events:        tempFileEvents,
		},
		Maintenance: MaintenanceJSON{
			VacuumCount:          m.Vacuum.VacuumCount,
			AnalyzeCount:         m.Vacuum.AnalyzeCount,
			VacuumTableCounts:    m.Vacuum.VacuumTableCounts,
			AnalyzeTableCounts:   m.Vacuum.AnalyzeTableCounts,
			VacuumSpaceRecovered: formatVacuumSpaceRecovered(m.Vacuum.VacuumSpaceRecovered),
		},
		Checkpoints: CheckpointsJSON{
			TotalCheckpoints:  m.Checkpoints.CompleteCount,
			AvgCheckpointTime: formatQueryDuration(1000 * avgCheckpointTime),
			MaxCheckpointTime: formatQueryDuration(1000 * float64(m.Checkpoints.MaxWriteTimeSeconds)),
			Events:            events,
		},
		Connections: ConnectionsJSON{
			ConnectionCount:       m.Connections.ConnectionReceivedCount,
			AvgConnectionsPerHour: fmt.Sprintf("%.2f", avgConnectionsPerHour),
			DisconnectionCount:    m.Connections.DisconnectionCount,
			AvgSessionTime:        avgSessionTime.String(),
			Connections:           connections,
		},
		Clients: ClientsJSON{
			UniqueDatabases: m.UniqueEntities.UniqueDbs,
			UniqueUsers:     m.UniqueEntities.UniqueUsers,
			UniqueApps:      m.UniqueEntities.UniqueApps,
		},
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Println("[ERROR] Failed to export JSON:", err)
		return
	}
	fmt.Println(string(jsonData))
}

// Helper function to format the vacuum space recovered for each table.
func formatVacuumSpaceRecovered(space map[string]int64) map[string]string {
	formatted := make(map[string]string, len(space))
	for table, size := range space {
		formatted[table] = formatBytes(size)
	}
	return formatted
}
