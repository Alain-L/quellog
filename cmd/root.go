package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"dalibo/quellog/analysis"
	"dalibo/quellog/output"
	"dalibo/quellog/parser"

	"github.com/spf13/cobra"
)

// Global Flags
var (
	beginTime  string // --begin
	endTime    string // --end
	windowFlag string // --window

	dbFilter    []string // --dbname
	appFilter   []string // --appname
	userFilter  []string // --dbuser
	excludeUser []string // --exclude-user

	summaryFlag    bool     // --summary
	sqlSummaryFlag bool     // --sql-summary
	sqlDetailFlag  []string // --sql-detail
	// Removed explodeFlag as the option has been deprecated.
	grepExpr []string // --grep
)

// rootCmd is the main command.
var rootCmd = &cobra.Command{
	Use:   "quellog [files or dirs]",
	Short: "quellog is a PostgreSQL log parser CLI",
	Long: `quellog is a CLI tool to parse and filter PostgreSQL logs.
It can show a summary or filter lines based on various criteria.
Specify files or directories as arguments, and combine them with flags.`,
	Run: executeParsing,
}

// Execute is called from main.go to run the CLI.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

// init sets up all the flags.
func init() {
	// Time Filters
	rootCmd.PersistentFlags().StringVarP(&beginTime, "begin", "b", "",
		"Filter entries after this datetime (YYYY-MM-DD HH:MM:SS)")
	rootCmd.PersistentFlags().StringVarP(&endTime, "end", "e", "",
		"Filter entries before this datetime (YYYY-MM-DD HH:MM:SS)")
	rootCmd.PersistentFlags().StringVarP(&windowFlag, "window", "W", "",
		"Specify a duration (e.g., 30m, 2h) to limit the analysis window. If --begin or --end is set, it adjusts the other bound accordingly.")

	// Attribute Filters
	rootCmd.PersistentFlags().StringSliceVarP(&dbFilter, "dbname", "d", nil,
		"Only report on entries for the given database(s)")
	rootCmd.PersistentFlags().StringSliceVarP(&userFilter, "dbuser", "u", nil,
		"Only report on entries for the specified user(s)")
	rootCmd.PersistentFlags().StringSliceVarP(&excludeUser, "exclude-user", "U", nil,
		"Exclude entries for the specified user(s)")
	rootCmd.PersistentFlags().StringSliceVarP(&appFilter, "appname", "N", nil,
		"Only report on entries for the given application names")

	// SQL Query Options
	rootCmd.PersistentFlags().BoolVarP(&sqlSummaryFlag, "sql-summary", "", false,
		"Display a global SQL summary including performance metrics and percentiles")
	rootCmd.PersistentFlags().StringSliceVarP(&sqlDetailFlag, "query-detail", "Q", nil,
		"Show details for specific SQL IDs (repeat the flag for multiple IDs)")

	// General Output Options
	rootCmd.PersistentFlags().BoolVarP(&summaryFlag, "summary", "S", false,
		"Display a global summary instead of printing individual log lines")
	rootCmd.PersistentFlags().StringSliceVarP(&grepExpr, "grep", "g", nil,
		"Filter the final lines by a substring match (can be specified multiple times)")
}

// executeParsing is the main run function (streaming version).
func executeParsing(cmd *cobra.Command, args []string) {
	// 1) Collect files.
	allFiles := collectFiles(args)
	if len(allFiles) == 0 {
		fmt.Println("[INFO] No log files found. Exiting.")
		os.Exit(0)
	}

	// 2) Check compatibility of --begin/--end/--window.
	if beginTime != "" && endTime != "" && windowFlag != "" {
		log.Fatalf("Options --begin, --end, and --window cannot all be used together.")
	}

	// 3) Convert dates and window.
	bT, eT := parseDateTimes(beginTime, endTime)
	windowDur := parseWindow(windowFlag)

	// Complete the missing date if windowDur > 0.
	if windowDur > 0 {
		switch {
		case !bT.IsZero() && eT.IsZero():
			eT = bT.Add(windowDur)
		case bT.IsZero() && !eT.IsZero():
			bT = eT.Add(-windowDur)
		default:
			if bT.IsZero() && eT.IsZero() {
				fmt.Println("[WARN] --window specified but neither --begin nor --end. Ignoring --window.")
			}
		}
	}

	// 4) Create the channel for raw logs (unfiltered).
	rawLogs := make(chan parser.LogEntry, 100)

	// 5) Launch file reading and streaming parsing (autodetect + parse).
	go func() {
		parser.ParseAllFiles(allFiles, rawLogs)
		close(rawLogs) // Close the channel once parsing is finished.
	}()

	// 6) Create the channel for filtered logs.
	filteredLogs := make(chan parser.LogEntry)

	// 7) Build the filter structure.
	filters := parser.LogFilters{
		BeginT:      bT,
		EndT:        eT,
		DbFilter:    dbFilter,
		UserFilter:  userFilter,
		ExcludeUser: excludeUser,
		AppFilter:   appFilter,
		GrepExpr:    grepExpr,
	}

	// 8) Apply streaming filtering.
	go parser.FilterStream(rawLogs, filteredLogs, filters)

	// 9) Process SQL query details if specified.
	if len(sqlDetailFlag) > 0 {
		sqlMetrics := analysis.RunSQLSummary(filteredLogs)
		output.PrintSqlDetails(sqlMetrics, sqlDetailFlag)
		return
	}

	// 10) Process the filtered logs based on activated options.
	if sqlSummaryFlag {
		// SQL summary processing: consume the channel for SQL reporting.
		sqlMetrics := analysis.RunSQLSummary(filteredLogs)
		output.PrintSQLSummary(sqlMetrics, false)
		return
	} else if summaryFlag {
		// General aggregation reporting.
		metrics := analysis.AggregateMetrics(filteredLogs)
		if metrics.Global.MaxTimestamp.IsZero() || !metrics.Global.MaxTimestamp.After(metrics.Global.MinTimestamp) {
			log.Fatalf("Error: the computed duration is 0 (MinTimestamp: %v, MaxTimestamp: %v)", metrics.Global.MinTimestamp, metrics.Global.MaxTimestamp)
		}
		output.PrintMetrics(metrics)
	} else {
		// Print each log line.
		for e := range filteredLogs {
			fmt.Println(e.Message)
		}
	}
}

// HELPERS

// collectFiles gathers files based on the provided arguments.
func collectFiles(args []string) []string {
	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err == nil && info.IsDir() {
			dirFiles, _ := gatherLogFiles(arg)
			files = append(files, dirFiles...)
		} else {
			matches, err := filepath.Glob(arg)
			if err != nil {
				log.Printf("[WARN] Error in pattern %s: %v\n", arg, err)
				continue
			}
			files = append(files, matches...)
		}
	}
	return files
}

// gatherLogFiles scans a directory for .log files (non-recursive).
func gatherLogFiles(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	entries, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}
	var logFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".log" {
			logFiles = append(logFiles, filepath.Join(dir, e.Name()))
		}
	}
	return logFiles, nil
}

// parseDateTimes parses the begin/end datetimes in the format "2006-01-02 15:04:05".
func parseDateTimes(bStr, eStr string) (time.Time, time.Time) {
	var bT, eT time.Time
	if bStr != "" {
		tmp, err := time.Parse("2006-01-02 15:04:05", bStr)
		if err != nil {
			log.Fatalf("Invalid --begin datetime: %v\n", err)
		}
		bT = tmp
	}
	if eStr != "" {
		tmp, err := time.Parse("2006-01-02 15:04:05", eStr)
		if err != nil {
			log.Fatalf("Invalid --end datetime: %v\n", err)
		}
		eT = tmp
	}
	return bT, eT
}

// parseWindow converts windowFlag to time.Duration if set.
func parseWindow(wStr string) time.Duration {
	if wStr == "" {
		return 0
	}
	d, err := time.ParseDuration(wStr)
	if err != nil {
		log.Fatalf("Invalid --window duration: %v\n", err)
	}
	return d
}
