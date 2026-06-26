// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
//
// Returns an error so cobra (and ultimately Execute()) can decide how to
// surface the failure. In follow mode, per-cycle errors are logged but
// do not stop the loop — the only thing that exits the loop is a signal
// (delivered via cmd.Context() cancellation, set up in Execute()).
func executeParsing(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Flag-combination constraints are static (flag-only), so validate them
	// once here. In follow mode the per-cycle error is logged and the loop
	// continues; if these checks lived only inside the cycle, an invalid combo
	// (e.g. --open --follow, --follow --split) would be re-reported every tick
	// forever instead of failing cleanly with a non-zero exit.
	if err := validateFlagCombinations(); err != nil {
		return err
	}

	if !followFlag {
		return runAnalysisCycle(ctx, args)
	}

	// Apply default time window for follow mode if none specified
	if lastFlag == "" && beginTime == "" && endTime == "" && windowFlag == "" {
		lastFlag = "24h"
		slog.Info("no time filter specified for follow mode, defaulting to --last 24h")
	}

	// Follow mode implementation
	slog.Info("entering follow mode", "interval", intervalFlag)

	ticker := time.NewTicker(intervalFlag)
	defer ticker.Stop()

	// Run first cycle immediately. In follow mode, errors are non-fatal —
	// log them and wait for the next tick so transient failures (file
	// rotated, no entries in the window, disk briefly full) do not kill
	// the long-running process.
	if err := runAnalysisCycle(ctx, args); err != nil {
		slog.Error("analysis cycle failed", "err", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := runAnalysisCycle(ctx, args); err != nil {
				slog.Error("analysis cycle failed", "err", err)
			}
		case <-ctx.Done():
			slog.Info("stopping follow mode")
			return nil
		}
	}
}

// runAnalysisCycle executes a single parsing and analysis pass.
func runAnalysisCycle(ctx context.Context, args []string) error {
	// Investigation hook: when QUELLOG_HEAP_SNAPSHOTS is set, dump heap
	// pprof + MemStats at fixed intervals so we can audit memory growth
	// trajectory through the parse. No-op otherwise.
	defer startHeapSnapshots()()

	startTime := time.Now()

	// Step 1: Collect log files from arguments
	allFiles := collectFiles(args)
	if len(allFiles) == 0 {
		slog.Info("no log files found, exiting")
		return nil
	}

	// Pre-validate stdin usage so we fail fast (and never from inside a
	// goroutine) if the user mixed "-" with regular files.
	if err := validateStdinUsage(allFiles); err != nil {
		return err
	}

	// Calculate total file size for throughput reporting
	totalFileSize := calculateTotalFileSize(allFiles)

	// Step 2: Validate and parse time filter options
	if err := validateTimeFilters(); err != nil {
		return err
	}

	var beginT, endT time.Time
	var err error
	if lastFlag != "" {
		// --last takes precedence and sets both begin and end
		beginT, endT, err = parseLast(lastFlag)
		if err != nil {
			return err
		}
	} else {
		// Parse --begin and --end normally
		beginT, endT, err = parseDateTimes(beginTime, endTime)
		if err != nil {
			return err
		}
		windowDur, err := parseWindow(windowFlag)
		if err != nil {
			return err
		}
		beginT, endT = applyTimeWindow(beginT, endT, windowDur)
	}

	// Step 3: Set up streaming pipeline
	rawLogs := make(chan []parser.LogEntry, 64)

	// Track whether at least one input parsed successfully. The async
	// parsers are fire-and-forget: errors get logged inside, and if
	// nothing comes out we surface a single clean error here at the end.
	var parsedAny atomic.Bool

	// Optional progress indicator on stderr (large input + TTY + text
	// output). nil here is a no-op for all bar methods. Reset the
	// parser-side counters so a follow-mode cycle doesn't show
	// cumulative numbers from a previous run.
	parser.ResetParsedEntries()
	parser.ResetCurrentFileProgress()
	pb := newProgressBar(totalFileSize)
	pb.Start()
	defer pb.Finish()

	// Launch parallel file parsing
	go parseFilesAsync(ctx, allFiles, rawLogs, &parsedAny, pb)

	// Step 4: Apply filters (skip channel hop when no filters are active)
	filters := buildLogFilters(beginT, endT)
	var analyzeInput <-chan []parser.LogEntry
	if filters.IsEmpty() {
		analyzeInput = rawLogs
	} else {
		filteredLogs := make(chan []parser.LogEntry, 64)
		go parser.FilterStream(ctx, rawLogs, filteredLogs, filters)
		analyzeInput = filteredLogs
	}

	// Step 5: Process and output results based on flags
	if err := processAndOutput(ctx, analyzeInput, startTime, totalFileSize, args, pb); err != nil {
		return err
	}

	// A file that parsed partially before erroring (e.g. a truncated gzip
	// stream) still produced usable entries; ParsedEntries() catches that case
	// so we don't claim "no files could be parsed" after already emitting an
	// analysis for the recovered data.
	if !parsedAny.Load() && parser.ParsedEntries() == 0 {
		return fmt.Errorf("no files could be parsed: check that files exist, are readable, and in a supported format")
	}
	return nil
}

// isDetectionError reports whether err is a format-detection failure the parser
// layer already logged with specifics, so parseFilesAsync doesn't double-log it.
func isDetectionError(err error) bool {
	return errors.Is(err, parser.ErrFileEmpty) ||
		errors.Is(err, parser.ErrBinaryFile) ||
		errors.Is(err, parser.ErrInvalidFormat) ||
		errors.Is(err, parser.ErrUnknownFormat) ||
		errors.Is(err, parser.ErrCompressionFailed)
}

// validateStdinUsage rejects mixing "-" (stdin) with regular file arguments.
func validateStdinUsage(files []string) error {
	hasStdin := false
	hasRegular := false
	for _, f := range files {
		if f == "-" {
			hasStdin = true
		} else {
			hasRegular = true
		}
	}
	if hasStdin && hasRegular {
		return fmt.Errorf("cannot mix stdin (-) with file arguments")
	}
	return nil
}

// parseFilesAsync reads log files in parallel and sends entries to the channel.
// It determines the optimal number of workers based on file count and CPU cores.
// On per-file failure, errors are logged via slog (the autodetect/parser layer
// already logs specific details). The caller observes overall success through
// the parsedAny flag and the channel closing.
//
// Special handling: if "-" is in the files list, it reads from stdin. The
// caller must ensure stdin is not mixed with regular files (see
// validateStdinUsage).
func parseFilesAsync(ctx context.Context, files []string, out chan<- []parser.LogEntry, parsedAny *atomic.Bool, pb *progressBar) {
	defer close(out)

	// Special case: stdin (caller has validated it is not mixed)
	if len(files) == 1 && files[0] == "-" {
		if err := parser.ParseStdin(out); err != nil {
			slog.Error("failed to parse from stdin", "err", err)
			return
		}
		parsedAny.Store(true)
		return
	}

	numWorkers := determineWorkerCount(files)

	// fileSize is captured per file so the progress bar can advance after
	// each ParseFile completes — the parser layer doesn't expose a
	// finer-grained cursor today, so multi-file rotations get smooth
	// progress while a single huge file jumps from 0% to 100% at the end.
	fileSize := func(path string) int64 {
		st, err := os.Stat(path)
		if err != nil {
			return 0
		}
		return st.Size()
	}

	if numWorkers == 1 {
		// Single file: no need for worker pool
		for _, file := range files {
			if ctx.Err() != nil {
				return
			}
			parser.ResetCurrentFileProgress()
			if err := parser.ParseFile(file, out); err != nil {
				// Detection failures are already logged with specifics; surface
				// parse-stage failures (e.g. a stream that truncated mid-file)
				// since those leave partial output.
				if !isDetectionError(err) {
					slog.Warn("file parsing ended early; output may be partial", "file", file, "err", err)
				}
				continue
			}
			// Reset the in-flight cursor to 0 before crediting the
			// completed file size to bytesDone — otherwise the bar
			// renders done = bytesDone + lingering cursor (= 2x file).
			parser.ResetCurrentFileProgress()
			pb.AddBytes(fileSize(file))
			parsedAny.Store(true)
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
				if ctx.Err() != nil {
					return
				}
				if err := parser.ParseFile(file, out); err != nil {
					// Detection failures are already logged; surface parse-stage
					// failures (partial output) as a warning.
					if !isDetectionError(err) {
						slog.Warn("file parsing ended early; output may be partial", "file", file, "err", err)
					}
					continue
				}
				pb.AddBytes(fileSize(file))
				parsedAny.Store(true)
			}
		}()
	}
	wg.Wait()
}

// buildLogFilters creates a LogFilters struct from command-line flags.
//
// Time bounds are projected onto the wall-clock timeline so a naive
// --begin/--end ("2026-02-04 09:00:00", no zone) and the now-relative
// --last/--window bounds (machine-local) compare consistently against log
// entries in any timezone. See parser.WallClock and PassesFilters.
func buildLogFilters(beginT, endT time.Time) parser.LogFilters {
	return parser.LogFilters{
		BeginT:      parser.WallClock(beginT),
		EndT:        parser.WallClock(endT),
		DbFilter:    dbFilter,
		UserFilter:  userFilter,
		ExcludeUser: excludeUser,
		AppFilter:   appFilter,
	}
}

// exportFormatCount counts how many distinct export formats are requested.
func exportFormatCount() int {
	n := 0
	if jsonFlag || jsonCompactFlag {
		n++
	}
	if yamlFlag {
		n++
	}
	if mdFlag {
		n++
	}
	if htmlFlag {
		n++
	}
	return n
}

// validateFlagCombinations checks flag-combination constraints that depend only
// on the flags, not on the input. Called once before follow mode starts so an
// invalid combo aborts with a non-zero exit rather than being logged and
// retried on every tick.
func validateFlagCombinations() error {
	formatCount := exportFormatCount()
	if jsonFlag && jsonCompactFlag {
		return fmt.Errorf("--json and --json-compact are mutually exclusive")
	}
	if formatCount > 1 && outputFlag != "" {
		return fmt.Errorf("-o/--output is not compatible with multiple export formats (each format writes to its own default file)")
	}
	if formatCount > 1 && (len(sqlDetailFlag) > 0 || len(eventDetailFlag) > 0 || sqlPerformanceFlag || sqlOverviewFlag) {
		return fmt.Errorf("multiple export formats are only supported for the full report (not with --sql-detail, --event-detail, --sql-performance, --sql-overview)")
	}
	if openFlag && !htmlFlag {
		return fmt.Errorf("--open requires --html (nothing to open without an HTML report)")
	}
	if openFlag && formatCount > 1 {
		return fmt.Errorf("--open is not supported with multiple export formats (ambiguous in batch context)")
	}
	if openFlag && followFlag {
		return fmt.Errorf("--open is not supported with --follow (would re-open the browser every cycle)")
	}
	if splitFlag != "" {
		if !htmlFlag {
			return fmt.Errorf("--split requires --html")
		}
		if followFlag {
			return fmt.Errorf("--split is not supported with --follow (it would regenerate a multi-period report every cycle)")
		}
		if formatCount > 1 {
			return fmt.Errorf("--split is only supported with --html (not alongside other export formats)")
		}
		if len(sqlDetailFlag) > 0 || len(eventDetailFlag) > 0 || sqlPerformanceFlag || sqlOverviewFlag {
			return fmt.Errorf("--split is only supported for the full HTML report (not with --sql-detail, --event-detail, --sql-performance, --sql-overview)")
		}
	}
	return nil
}

// processAndOutput analyzes filtered logs and outputs results in the requested format.
func processAndOutput(ctx context.Context, filteredLogs <-chan []parser.LogEntry, startTime time.Time, totalFileSize int64, inputArgs []string, pb *progressBar) error {
	// Flag-combination constraints are validated once up front (see
	// validateFlagCombinations, called from executeParsing). Here we only need
	// the format count for dispatch and the --split short-circuit.
	formatCount := exportFormatCount()
	if splitFlag != "" {
		return runSplitHTML(ctx, filteredLogs, startTime, totalFileSize, inputArgs, pb)
	}

	// Analysis PID-shard count: > 1 only for large, uncompressed plain
	// stderr (where LogEntry.PID is the backend PID). Computed once and
	// threaded into every metrics build.
	workers := shardWorkers(inputArgs)

	// Special case: SQL query details (single query analysis)
	if len(sqlDetailFlag) > 0 {
		metrics, processingDuration, err := requireMetrics(ctx, filteredLogs, totalFileSize, startTime, pb, workers)
		if err != nil {
			return err
		}
		w, closer, err := createOutputWriter(outputFlag)
		if err != nil {
			return err
		}
		defer closer()

		if jsonFlag {
			output.ExportSQLDetailJSON(w, metrics, sqlDetailFlag)
		} else if yamlFlag {
			output.ExportSQLDetailYAML(w, metrics, sqlDetailFlag)
		} else if mdFlag {
			output.ExportSQLDetailMarkdown(w, metrics, sqlDetailFlag)
		} else {
			PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
			output.PrintSQLDetails(metrics, sqlDetailFlag)
		}
		return closer()
	}

	// Special case: event pattern details (lookup by ID like wa-aBc1)
	if len(eventDetailFlag) > 0 {
		metrics, processingDuration, err := requireMetrics(ctx, filteredLogs, totalFileSize, startTime, pb, workers)
		if err != nil {
			return err
		}
		w, closer, err := createOutputWriter(outputFlag)
		if err != nil {
			return err
		}
		defer closer()

		if jsonFlag {
			output.ExportEventDetailJSON(w, metrics, eventDetailFlag)
		} else if yamlFlag {
			output.ExportEventDetailYAML(w, metrics, eventDetailFlag)
		} else if mdFlag {
			output.ExportEventDetailMarkdown(w, metrics, eventDetailFlag)
		} else {
			PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
			output.PrintEventDetails(metrics, eventDetailFlag)
		}
		return closer()
	}

	// Special case: SQL performance (detailed aggregated query statistics)
	// Skip if --full is set (will be included in full report)
	if sqlPerformanceFlag && !fullFlag {
		metrics, processingDuration, err := requireMetrics(ctx, filteredLogs, totalFileSize, startTime, pb, workers)
		if err != nil {
			return err
		}
		w, closer, err := createOutputWriter(outputFlag)
		if err != nil {
			return err
		}
		defer closer()

		if jsonFlag {
			output.ExportSQLPerformanceJSON(w, metrics.SQL)
		} else if yamlFlag {
			output.ExportSQLPerformanceYAML(w, metrics.SQL)
		} else if mdFlag {
			output.ExportSQLSummaryMarkdown(w, metrics.SQL, metrics.TempFiles, metrics.Locks)
		} else {
			PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
			output.PrintSQLSummaryWithContext(metrics.SQL, metrics.TempFiles, metrics.Locks, false)
		}
		return closer()
	}

	// Special case: SQL overview (query type statistics with dimensional breakdown)
	// Skip if --full is set (will be included in full report)
	if sqlOverviewFlag && !fullFlag {
		metrics, processingDuration, err := requireMetrics(ctx, filteredLogs, totalFileSize, startTime, pb, workers)
		if err != nil {
			return err
		}
		w, closer, err := createOutputWriter(outputFlag)
		if err != nil {
			return err
		}
		defer closer()

		if jsonFlag {
			output.ExportSQLOverviewJSON(w, metrics.SQL)
		} else if yamlFlag {
			output.ExportSQLOverviewYAML(w, metrics.SQL)
		} else if mdFlag {
			output.ExportSQLOverviewMarkdown(w, metrics.SQL)
		} else {
			PrintProcessingSummary(metrics.SQL.TotalQueries, processingDuration, totalFileSize)
			output.PrintSQLOverview(metrics.SQL)
		}
		return closer()
	}

	// Default: full analysis with all metrics
	metrics := analysis.AggregateMetricsWithWorkers(ctx, filteredLogs, workers)
	// Aggregation drained the input — parse is done. Clear the bar
	// before any subsequent stderr/stdout write (PrintProcessingSummary
	// and the section renderers below). The defer in runAnalysisCycle
	// would otherwise fire too late, leaving the bar stuck next to the
	// summary line.
	pb.Finish()
	processingDuration := time.Since(startTime)

	// Check if any log entries were successfully parsed
	if metrics.Global.Count == 0 {
		return fmt.Errorf("no log entries could be parsed: check that files are readable and in a supported format")
	}

	// Validate that we have a valid time range
	// Note: MaxTimestamp can be equal to MinTimestamp if there's only one log entry
	if metrics.Global.MaxTimestamp.IsZero() || metrics.Global.MaxTimestamp.Before(metrics.Global.MinTimestamp) {
		return fmt.Errorf("invalid time range: MinTimestamp=%v, MaxTimestamp=%v",
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

	// Multi-format export: render each selected format to its default file
	if formatCount > 1 {
		return renderMultipleFormats(metrics, sections, inputArgs, totalFileSize, processingDuration)
	}

	// Output in requested format
	if jsonFlag || jsonCompactFlag {
		w, closer, err := createOutputWriter(outputFlag)
		if err != nil {
			return err
		}
		defer closer()
		output.ExportJSON(w, metrics, sections, fullFlag, jsonCompactFlag)
		return closer()
	}

	if yamlFlag {
		w, closer, err := createOutputWriter(outputFlag)
		if err != nil {
			return err
		}
		defer closer()
		output.ExportYAML(w, metrics, sections, fullFlag)
		return closer()
	}

	if mdFlag {
		w, closer, err := createOutputWriter(outputFlag)
		if err != nil {
			return err
		}
		defer closer()
		output.ExportMarkdown(w, metrics, sections, fullFlag)
		return closer()
	}

	if htmlFlag {
		// Generate output filename based on input or flag
		outputName := outputFlag
		if outputName == "" {
			outputName = generateHTMLFilename(inputArgs)
		}

		w, closer, err := createOutputWriter(outputName)
		if err != nil {
			return err
		}
		defer closer()

		// Detect format from first input file
		detectedFormat := ""
		if len(inputArgs) > 0 {
			detectedFormat = parser.DetectFileFormat(inputArgs[0])
		}

		// Build report info with filename and processing stats
		reportInfo := output.HTMLReportInfo{
			Filename:    generateInputDescription(inputArgs),
			FileSize:    totalFileSize,
			ProcessTime: float64(processingDuration.Milliseconds()),
			Format:      detectedFormat,
			Version:     version,
		}

		if err := output.ExportHTML(w, metrics, reportInfo, sections); err != nil {
			return fmt.Errorf("failed to write HTML report: %w", err)
		}

		// Flush and close before announcing or opening the file so the
		// browser never reads a truncated report (and disk-full errors
		// surface instead of a silent partial write).
		if err := closer(); err != nil {
			return err
		}

		// In follow mode, be less verbose about saved files
		if !followFlag {
			fmt.Printf("Report saved to %s\n", outputName)
		}
		if openFlag {
			openInBrowser(outputName)
		}
		return nil
	}

	// Default: text output
	PrintProcessingSummary(metrics.Global.Count, processingDuration, totalFileSize)
	output.PrintMetrics(metrics, sections, fullFlag)
	return nil
}

// runSplitHTML consumes the stream, aggregates it into per-interval periods and
// writes a single HTML report with a period selector. Used for --split --html.
func runSplitHTML(ctx context.Context, filteredLogs <-chan []parser.LogEntry, startTime time.Time, totalFileSize int64, inputArgs []string, pb *progressBar) error {
	interval, err := parseDuration(splitFlag)
	if err != nil || interval <= 0 {
		return fmt.Errorf("invalid --split interval %q (use e.g. 1d, 3h, 5m): %v", splitFlag, err)
	}

	buckets, err := analysis.AggregateMetricsBySplit(ctx, filteredLogs, interval)
	pb.Finish()
	if err != nil {
		return err
	}
	if len(buckets) == 0 {
		return fmt.Errorf("no timestamped log entries to split into periods")
	}
	processingDuration := time.Since(startTime)

	outputName := outputFlag
	if outputName == "" {
		outputName = generateHTMLFilename(inputArgs)
	}
	w, closer, err := createOutputWriter(outputName)
	if err != nil {
		return err
	}
	defer closer()

	detectedFormat := ""
	if len(inputArgs) > 0 {
		detectedFormat = parser.DetectFileFormat(inputArgs[0])
	}
	reportInfo := output.HTMLReportInfo{
		Filename:    generateInputDescription(inputArgs),
		FileSize:    totalFileSize,
		ProcessTime: float64(processingDuration.Milliseconds()),
		Format:      detectedFormat,
		Version:     version,
	}
	if err := output.ExportHTMLSplit(w, buckets, reportInfo, buildSectionList()); err != nil {
		return fmt.Errorf("failed to write split HTML report: %w", err)
	}
	if err := closer(); err != nil {
		return err
	}
	if !followFlag {
		fmt.Printf("Report saved to %s (%d periods)\n", outputName, len(buckets))
	}
	if openFlag {
		openInBrowser(outputName)
	}
	return nil
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

// defaultExportName builds the default output filename for multi-format export.
// Single input  -> "quellog-<stem>.<ext>" (stem strips the last extension only).
// Multi / stdin -> "quellog.<ext>".
func defaultExportName(ext string, inputArgs []string) string {
	if len(inputArgs) != 1 || inputArgs[0] == "-" {
		return "quellog." + ext
	}
	base := filepath.Base(inputArgs[0])
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	if base == "" {
		return "quellog." + ext
	}
	return "quellog-" + base + "." + ext
}

// renderMultipleFormats writes the full report to one file per selected format,
// using default filenames. Called only when 2+ format flags are set.
func renderMultipleFormats(metrics analysis.AggregatedMetrics, sections []string, inputArgs []string, totalFileSize int64, processingDuration time.Duration) error {
	write := func(ext string, render func(io.Writer) error) error {
		name := defaultExportName(ext, inputArgs)
		w, closer, err := createOutputWriter(name)
		if err != nil {
			return err
		}
		defer closer()
		if err := render(w); err != nil {
			return fmt.Errorf("failed to write %s report: %w", ext, err)
		}
		if err := closer(); err != nil {
			return err
		}
		if !followFlag {
			fmt.Printf("Report saved to %s\n", name)
		}
		return nil
	}

	if jsonFlag || jsonCompactFlag {
		if err := write("json", func(w io.Writer) error {
			output.ExportJSON(w, metrics, sections, fullFlag, jsonCompactFlag)
			return nil
		}); err != nil {
			return err
		}
	}
	if yamlFlag {
		if err := write("yaml", func(w io.Writer) error {
			output.ExportYAML(w, metrics, sections, fullFlag)
			return nil
		}); err != nil {
			return err
		}
	}
	if mdFlag {
		if err := write("md", func(w io.Writer) error {
			output.ExportMarkdown(w, metrics, sections, fullFlag)
			return nil
		}); err != nil {
			return err
		}
	}
	if htmlFlag {
		detectedFormat := ""
		if len(inputArgs) > 0 {
			detectedFormat = parser.DetectFileFormat(inputArgs[0])
		}
		reportInfo := output.HTMLReportInfo{
			Filename:    generateInputDescription(inputArgs),
			FileSize:    totalFileSize,
			ProcessTime: float64(processingDuration.Milliseconds()),
			Format:      detectedFormat,
			Version:     version,
		}
		if err := write("html", func(w io.Writer) error {
			return output.ExportHTML(w, metrics, reportInfo, sections)
		}); err != nil {
			return err
		}
	}
	return nil
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
	if serverFlag {
		sections = append(sections, "server")
	}

	// If no specific sections selected, show all
	if len(sections) == 0 {
		sections = []string{"all"}
	}

	return sections
}

// validateTimeFilters checks that time filter flags are compatible.
func validateTimeFilters() error {
	if beginTime != "" && endTime != "" && windowFlag != "" {
		return fmt.Errorf("--begin, --end, and --window cannot all be used together")
	}

	// --last cannot be used with other time filters
	if lastFlag != "" {
		if beginTime != "" || endTime != "" || windowFlag != "" {
			return fmt.Errorf("--last cannot be used with --begin, --end, or --window")
		}
	}
	return nil
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
		slog.Warn("--window specified but neither --begin nor --end is set; ignoring --window")
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
		numEntries, duration.Seconds(), output.FormatBytes(fileSize))
}

// createOutputWriter returns an io.Writer for the given output path and a
// closer that MUST be checked on the success path.
//
// If path is empty, it returns os.Stdout with a no-op closer. Otherwise it
// creates the file and wraps it in a bufio.Writer: the interposed buffer is
// what makes error reporting reliable. The exporters wrap w in their own
// buffered writer and swallow its Flush error; by writing into our bufio,
// that swallowed flush becomes an in-memory copy that cannot fail, and the
// real I/O happens at our checked Flush (catching disk-full / quota) and
// Close (catching deferred errors on networked filesystems). Without this,
// a report written to a full disk would silently truncate and quellog
// would still exit 0.
func createOutputWriter(path string) (io.Writer, func() error, error) {
	if path == "" {
		return os.Stdout, func() error { return nil }, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create output file %q: %w", path, err)
	}
	bw := bufio.NewWriter(f)
	return bw, func() error {
		if ferr := bw.Flush(); ferr != nil {
			f.Close()
			return fmt.Errorf("failed to write output file %q: %w", path, ferr)
		}
		if cerr := f.Close(); cerr != nil {
			return fmt.Errorf("failed to close output file %q: %w", path, cerr)
		}
		return nil
	}, nil
}

// shardWorkers picks the analysis PID-shard count for an input set. It
// returns > 1 only for large, uncompressed plain-stderr inputs where
// LogEntry.PID is the PostgreSQL backend PID (the data-parallel fan-out's
// precondition); everything else stays single-shard (always correct).
//
// QUELLOG_SHARD_WORKERS overrides the count for benchmarking — it bypasses
// the format/size gate, so only point it at plain stderr.
func shardWorkers(inputArgs []string) int {
	if v := os.Getenv("QUELLOG_SHARD_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	if len(inputArgs) == 0 {
		return 1
	}
	// Sharding pays off above ~256 MB of DECOMPRESSED content, since analysis
	// work scales with decompressed size — not the on-disk size. Compressed
	// inputs are scaled up by a conservative log-expansion factor first, so a
	// 245 MB .gz (≈ 4.5 GB decompressed) shards while a small one does not.
	const minSize = 256 << 20
	if estimatedDecompressedSize(inputArgs) < minSize {
		return 1
	}
	for _, f := range inputArgs {
		if !parser.SupportsPIDSharding(f) {
			return 1
		}
	}
	n := runtime.NumCPU() - 2
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	return n
}

// logExpansionFactor is a conservative estimate of how much a compressed
// PostgreSQL log expands when decompressed. Measured ratios are ~14-18x (gzip
// 18x, zstd 14x on a 4.5 GB log); 8 is a deliberate floor so we never
// over-estimate — at worst a poorly-compressing input shards slightly below
// the 256 MB target, which only costs the bounded fan-out setup.
const logExpansionFactor = 8

// estimatedDecompressedSize sums the byte volume the analysis stage will
// process: compressed inputs scaled up by logExpansionFactor, plain inputs
// (including uncompressed tar) counted at their on-disk size.
func estimatedDecompressedSize(inputArgs []string) int64 {
	var total int64
	for _, f := range inputArgs {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		sz := fi.Size()
		if isCompressedInput(f) {
			sz *= logExpansionFactor
		}
		total += sz
	}
	return total
}

// isCompressedInput reports whether a file is gzip/zstd-compressed (including
// compressed tar) and therefore expands when decompressed. A plain .tar is a
// 1:1 container and is not counted as compressed.
func isCompressedInput(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".gz", ".zst", ".zstd", ".tgz", ".tzst"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// requireMetrics aggregates metrics and returns an error if no log entries
// were parsed.
func requireMetrics(ctx context.Context, filteredLogs <-chan []parser.LogEntry, totalFileSize int64, startTime time.Time, pb *progressBar, workers int) (analysis.AggregatedMetrics, time.Duration, error) {
	metrics := analysis.AggregateMetricsWithWorkers(ctx, filteredLogs, workers)
	// Aggregation has drained the input channel — parsing is fully
	// done. Clear the progress bar before any subsequent stderr write
	// (PrintProcessingSummary, slog warnings, …) so the redrawn line
	// doesn't clobber them.
	pb.Finish()
	processingDuration := time.Since(startTime)
	if metrics.Global.Count == 0 {
		return analysis.AggregatedMetrics{}, 0, fmt.Errorf("no log entries could be parsed: check that files are readable and in a supported format")
	}
	return metrics, processingDuration, nil
}
