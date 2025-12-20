// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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

	var beginT, endT time.Time
	if lastFlag != "" {
		// --last takes precedence and sets both begin and end
		beginT, endT = parseLast(lastFlag)
	} else {
		// Parse --begin and --end normally
		beginT, endT = parseDateTimes(beginTime, endTime)
		windowDur := parseWindow(windowFlag)
		beginT, endT = applyTimeWindow(beginT, endT, windowDur)
	}

	// Step 3: Set up streaming pipeline
	rawLogs := make(chan parser.LogEntry, 24576)
	filteredLogs := make(chan parser.LogEntry, 24576)

	// Launch parallel file parsing
	go parseFilesAsync(allFiles, rawLogs)

	// Step 4: Apply filters to log entries
	filters := buildLogFilters(beginT, endT)
	go parser.FilterStream(rawLogs, filteredLogs, filters)

	// Step 5: Process and output results based on flags
	processAndOutput(filteredLogs, startTime, totalFileSize, args)
}

// parseFilesAsync reads log files in parallel and sends entries to the channel.
// It determines the optimal number of workers based on file count and CPU cores.
// If all files fail to parse, it exits immediately with a clear error.
// Special handling: if "-" is in the files list, it reads from stdin.
func parseFilesAsync(files []string, out chan<- parser.LogEntry) {
	defer close(out)

	// Special case: check if stdin is requested
	hasStdin := false
	regularFiles := []string{}
	for _, file := range files {
		if file == "-" {
			hasStdin = true
		} else {
			regularFiles = append(regularFiles, file)
		}
	}

	// If stdin is requested, it must be the only input
	if hasStdin {
		if len(regularFiles) > 0 {
			log.Fatalf("[ERROR] Cannot mix stdin (-) with file arguments")
		}
		// Parse from stdin
		if err := parser.ParseStdin(out); err != nil {
			log.Fatalf("[ERROR] Failed to parse from stdin: %v", err)
		}
		return
	}

	numWorkers := determineWorkerCount(len(regularFiles))
	successChan := make(chan bool, len(regularFiles))

	if numWorkers == 1 {
		// Single file: no need for worker pool
		for _, file := range regularFiles {
			if err := parser.ParseFile(file, out); err != nil {
				// Error already logged in detectParser with specific details
				successChan <- false
			} else {
				successChan <- true
			}
		}
	} else {
		// Multiple files: use worker pool
		fileChan := make(chan string, len(regularFiles))
		for _, file := range regularFiles {
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
						// Error already logged in detectParser with specific details
						successChan <- false
					} else {
						successChan <- true
					}
				}
			}()
		}
		wg.Wait()
	}
	close(successChan)

	// Check if at least one file was successfully parsed
	anySuccess := false
	for success := range successChan {
		if success {
			anySuccess = true
			break
		}
	}

	if !anySuccess {
		log.Fatalf("[ERROR] No files could be parsed. Check that files exist, are readable, and in a supported format.")
	}
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
func processAndOutput(filteredLogs <-chan parser.LogEntry, startTime time.Time, totalFileSize int64, inputArgs []string) {
	// Validate flag compatibility
	formatCount := 0
	if jsonFlag {
		formatCount++
	}
	if mdFlag {
		formatCount++
	}
	if htmlFlag {
		formatCount++
	}
	if formatCount > 1 {
		fmt.Fprintln(os.Stderr, "Error: --json, --md, and --html are mutually exclusive")
		os.Exit(1)
	}

	// Special case: SQL query details (single query analysis)
	if len(sqlDetailFlag) > 0 {
		// Run full analysis to collect queries from locks and tempfiles
		metrics := analysis.AggregateMetrics(filteredLogs, totalFileSize)
		processingDuration := time.Since(startTime)

		// Check if any log entries were successfully parsed
		if metrics.Global.Count == 0 {
			log.Fatalf("[ERROR] No log entries could be parsed. Check that files are readable and in a supported format.")
		}

		if jsonFlag {
			output.ExportSQLDetailJSON(metrics, sqlDetailFlag)
		} else if mdFlag {
			output.ExportSqlDetailMarkdown(metrics, sqlDetailFlag)
		} else {
			PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
			output.PrintSqlDetails(metrics, sqlDetailFlag)
		}
		return
	}

	// Special case: SQL performance (detailed aggregated query statistics)
	// Skip if --full is set (will be included in full report)
	if sqlPerformanceFlag && !fullFlag {
		// Run full analysis to collect queries from locks and tempfiles
		metrics := analysis.AggregateMetrics(filteredLogs, totalFileSize)
		processingDuration := time.Since(startTime)

		// Check if any log entries were successfully parsed
		if metrics.Global.Count == 0 {
			log.Fatalf("[ERROR] No log entries could be parsed. Check that files are readable and in a supported format.")
		}

		if jsonFlag {
			output.ExportSQLPerformanceJSON(metrics.SQL)
		} else if mdFlag {
			output.ExportSqlSummaryMarkdown(metrics.SQL, metrics.TempFiles, metrics.Locks)
		} else {
			PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
			output.PrintSQLSummaryWithContext(metrics.SQL, metrics.TempFiles, metrics.Locks, false)
		}
		return
	}

	// Special case: SQL overview (query type statistics with dimensional breakdown)
	// Skip if --full is set (will be included in full report)
	if sqlOverviewFlag && !fullFlag {
		metrics := analysis.AggregateMetrics(filteredLogs, totalFileSize)
		processingDuration := time.Since(startTime)

		// Check if any log entries were successfully parsed
		if metrics.Global.Count == 0 {
			log.Fatalf("[ERROR] No log entries could be parsed. Check that files are readable and in a supported format.")
		}

		if jsonFlag {
			output.ExportSQLOverviewJSON(metrics.SQL)
		} else if mdFlag {
			output.ExportSqlOverviewMarkdown(metrics.SQL)
		} else {
			PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
			output.PrintSQLOverview(metrics.SQL)
		}
		return
	}

	// Default: full analysis with all metrics
	metrics := analysis.AggregateMetrics(filteredLogs, totalFileSize)
	processingDuration := time.Since(startTime)

	// Check if any log entries were successfully parsed
	if metrics.Global.Count == 0 {
		log.Fatalf("[ERROR] No log entries could be parsed. Check that files are readable and in a supported format.")
	}

	// Validate that we have a valid time range
	// Note: MaxTimestamp can be equal to MinTimestamp if there's only one log entry
	if metrics.Global.MaxTimestamp.IsZero() || metrics.Global.MaxTimestamp.Before(metrics.Global.MinTimestamp) {
		log.Fatalf("[ERROR] Invalid time range: MinTimestamp=%v, MaxTimestamp=%v",
			metrics.Global.MinTimestamp, metrics.Global.MaxTimestamp)
	}

	// Determine which sections to display
	// --full forces all sections and ignores individual section flags
	var sections []string
	if fullFlag {
		sections = []string{"all"}
	} else {
		sections = buildSectionList()
	}

	// Output in requested format
	if jsonFlag {
		output.ExportJSON(metrics, sections, fullFlag)
		return
	}

	if mdFlag {
		output.ExportMarkdown(metrics, sections, fullFlag)
		return
	}

	if htmlFlag {
		// Generate output filename based on input
		outputName := generateHTMLFilename(inputArgs)
		f, err := os.Create(outputName)
		if err != nil {
			log.Fatalf("[ERROR] Failed to create HTML file: %v", err)
		}
		defer f.Close()

		// Build report info with filename and processing stats
		reportInfo := output.HTMLReportInfo{
			Filename:    generateInputDescription(inputArgs),
			FileSize:    totalFileSize,
			ProcessTime: float64(processingDuration.Milliseconds()),
		}

		if err := output.ExportHTML(f, metrics, reportInfo); err != nil {
			log.Fatalf("[ERROR] Failed to write HTML report: %v", err)
		}
		fmt.Printf("Report saved to %s\n", outputName)
		return
	}

	// Default: text output
	PrintProcessingSummary(metrics.Global.Count, processingDuration, totalFileSize)
	output.PrintMetrics(metrics, sections, fullFlag)
}

// generateInputDescription creates a human-readable description of input files.
// For a single file, returns its basename. For multiple files, returns "N files".
func generateInputDescription(args []string) string {
	if len(args) == 1 {
		return filepath.Base(args[0])
	}
	return fmt.Sprintf("%d files", len(args))
}

// generateHTMLFilename creates an output filename based on input arguments.
// If a single file is given, uses its basename with .html extension.
// Otherwise uses "quellog_report.html".
func generateHTMLFilename(args []string) string {
	if len(args) == 1 {
		// Single file: use its basename
		base := filepath.Base(args[0])
		// Remove extension(s) like .log, .csv, .log.gz, etc.
		for {
			ext := filepath.Ext(base)
			if ext == "" || (ext != ".log" && ext != ".csv" && ext != ".gz" && ext != ".zst" && ext != ".tar" && ext != ".tgz") {
				break
			}
			base = strings.TrimSuffix(base, ext)
		}
		if base == "" {
			base = "quellog_report"
		}
		return base + ".html"
	}
	return "quellog_report.html"
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
	if errorsFlag {
		sections = append(sections, "errors")
	}
	if sqlSummaryFlag {
		sections = append(sections, "sql_summary")
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

	// --last cannot be used with other time filters
	if lastFlag != "" {
		if beginTime != "" || endTime != "" || windowFlag != "" {
			log.Fatalf("[ERROR] --last cannot be used with --begin, --end, or --window")
		}
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
