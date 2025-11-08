// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"

	"github.com/spf13/cobra"
)

// executeParsing is the main execution function for the root command.
// It orchestrates the entire log processing pipeline:
//  1. Collect input files
//  2. Parse time filters and validate options
//  3. Parse log files in parallel (streaming)
//  4. Filter log entries based on criteria
//  5. Analyze and output results
func executeParsing(cmd *cobra.Command, args []string) {
	startTime := time.Now()

	// Step 1: Collect log files from arguments
	allFiles := collectFiles(args)
	if len(allFiles) == 0 {
		fmt.Println("[INFO] No log files found. Exiting.")
		os.Exit(0)
	}

	// Calculate total file size for throughput reporting
	totalFileSize := calculateTotalFileSize(allFiles)

	// Step 2: Validate and parse time filter options
	validateTimeFilters()
	beginT, endT := parseDateTimes(beginTime, endTime)
	windowDur := parseWindow(windowFlag)
	beginT, endT = applyTimeWindow(beginT, endT, windowDur)

	// Step 3: Set up streaming pipeline
	rawLogs := make(chan parser.LogEntry, 24576)
	filteredLogs := make(chan parser.LogEntry, 24576)

	// Launch parallel file parsing
	go parseFilesAsync(allFiles, rawLogs)

	// Step 4: Apply filters to log entries
	filters := buildLogFilters(beginT, endT)
	go parser.FilterStream(rawLogs, filteredLogs, filters)

	// Step 5: Process and output results based on flags
	processAndOutput(filteredLogs, startTime, totalFileSize)
}

// parseFilesAsync reads log files in parallel and sends entries to the channel.
// It determines the optimal number of workers based on file count and CPU cores.
func parseFilesAsync(files []string, out chan<- parser.LogEntry) {
	defer close(out)

	numWorkers := determineWorkerCount(len(files))

	if numWorkers == 1 {
		// Single file: no need for worker pool
		for _, file := range files {
			if err := parser.ParseFile(file, out); err != nil {
				log.Printf("[ERROR] Failed to parse file %s: %v", file, err)
			}
		}
		return
	}

	// Multiple files: use worker pool
	fileChan := make(chan string, len(files))
	for _, file := range files {
		fileChan <- file
	}
	close(fileChan)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range fileChan {
				if err := parser.ParseFile(file, out); err != nil {
					log.Printf("[ERROR] Failed to parse file %s: %v", file, err)
				}
			}
		}()
	}
	wg.Wait()
}

// buildLogFilters creates a LogFilters struct from command-line flags.
func buildLogFilters(beginT, endT time.Time) parser.LogFilters {
	return parser.LogFilters{
		BeginT:      beginT,
		EndT:        endT,
		DbFilter:    dbFilter,
		UserFilter:  userFilter,
		ExcludeUser: excludeUser,
		AppFilter:   appFilter,
	}
}

// processAndOutput analyzes filtered logs and outputs results in the requested format.
func processAndOutput(filteredLogs <-chan parser.LogEntry, startTime time.Time, totalFileSize int64) {
	// Special case: SQL query details (single query analysis)
	if len(sqlDetailFlag) > 0 {
		// Run full analysis to collect queries from locks and tempfiles
		metrics := analysis.AggregateMetrics(filteredLogs, totalFileSize)
		processingDuration := time.Since(startTime)
		PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
		output.PrintSqlDetails(metrics, sqlDetailFlag)
		return
	}

	// Special case: SQL summary (aggregated query statistics)
	if sqlSummaryFlag {
		// Run full analysis to collect queries from locks and tempfiles
		metrics := analysis.AggregateMetrics(filteredLogs, totalFileSize)
		processingDuration := time.Since(startTime)
		PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
		output.PrintSQLSummary(metrics.SQL, false)
		return
	}

	// Default: full analysis with all metrics
	metrics := analysis.AggregateMetrics(filteredLogs, totalFileSize)
	processingDuration := time.Since(startTime)

	// Validate that we have a valid time range
	if metrics.Global.MaxTimestamp.IsZero() || !metrics.Global.MaxTimestamp.After(metrics.Global.MinTimestamp) {
		log.Fatalf("[ERROR] Invalid time range: MinTimestamp=%v, MaxTimestamp=%v",
			metrics.Global.MinTimestamp, metrics.Global.MaxTimestamp)
	}

	// Determine which sections to display
	sections := buildSectionList()

	// Output in requested format
	if jsonFlag {
		output.ExportJSON(metrics, sections)
		return
	}

	if mdFlag {
		output.ExportMarkdown(metrics, sections)
		return
	}

	// Default: text output
	PrintProcessingSummary(metrics.Global.Count, processingDuration, totalFileSize)
	output.PrintMetrics(metrics, sections)
}

// buildSectionList returns the list of sections to display based on flags.
// If no section flags are set, returns ["all"] to display everything.
func buildSectionList() []string {
	sections := []string{}

	if summaryFlag {
		sections = append(sections, "summary")
	}
	if checkpointsFlag {
		sections = append(sections, "checkpoints")
	}
	if eventsFlag {
		sections = append(sections, "events")
	}
	if sqlPerformanceFlag {
		sections = append(sections, "sql_performance")
	}
	if tempfilesFlag {
		sections = append(sections, "tempfiles")
	}
	if locksFlag {
		sections = append(sections, "locks")
	}
	if maintenanceFlag {
		sections = append(sections, "maintenance")
	}
	if connectionsFlag {
		sections = append(sections, "connections")
	}
	if clientsFlag {
		sections = append(sections, "clients")
	}

	// If no specific sections selected, show all
	if len(sections) == 0 {
		sections = []string{"all"}
	}

	return sections
}

// validateTimeFilters checks that time filter flags are compatible.
func validateTimeFilters() {
	if beginTime != "" && endTime != "" && windowFlag != "" {
		log.Fatalf("[ERROR] --begin, --end, and --window cannot all be used together")
	}
}

// applyTimeWindow applies the time window to the begin/end times.
// If window is specified and only one of begin/end is set, it calculates the other.
func applyTimeWindow(begin, end time.Time, window time.Duration) (time.Time, time.Time) {
	if window <= 0 {
		return begin, end
	}

	// If both begin and end are set, window is ignored
	if !begin.IsZero() && !end.IsZero() {
		return begin, end
	}

	// Calculate missing boundary
	if !begin.IsZero() && end.IsZero() {
		end = begin.Add(window)
	} else if begin.IsZero() && !end.IsZero() {
		begin = end.Add(-window)
	} else {
		// Neither begin nor end is set
		fmt.Println("[WARN] --window specified but neither --begin nor --end is set. Ignoring --window.")
	}

	return begin, end
}

// calculateTotalFileSize computes the total size of all input files.
func calculateTotalFileSize(files []string) int64 {
	var total int64
	for _, file := range files {
		if fi, err := os.Stat(file); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// PrintProcessingSummary displays a summary line showing processing statistics.
func PrintProcessingSummary(numEntries int, duration time.Duration, fileSize int64) {
	fmt.Printf("quellog – %d entries processed in %.2f s (%s)\n",
		numEntries, duration.Seconds(), formatBytes(fileSize))
}

// formatBytes converts a byte count to a human-readable string (KB, MB, GB, etc).
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}

	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "kMGTPE"[exp])
}
