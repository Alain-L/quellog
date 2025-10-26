package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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

	sqlSummaryFlag  bool     // --sql-summary
	queryDetailFlag []string // --query-detail

	summaryFlag        bool // --summary
	eventsFlag         bool // --events
	sqlPerformanceFlag bool // --sql-performance
	tempfilesFlag      bool // --tempfiles
	maintenanceFlag    bool // --maintenance
	checkpointsFlag    bool // --checkpoints
	connectionsFlag    bool // --connections
	clientsFlag        bool // --clients

	//grepExpr []string // --grep
	jsonFlag bool // --json
	mdFlag   bool // --md
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
	rootCmd.PersistentFlags().StringSliceVarP(&queryDetailFlag, "query-detail", "Q", nil,
		"Show details for specific SQL IDs (repeat the flag for multiple IDs)")

	// General Output Options
	rootCmd.Flags().BoolVar(&summaryFlag, "summary", false, "print only summary section")
	rootCmd.Flags().BoolVar(&eventsFlag, "events", false, "print only events section")
	rootCmd.Flags().BoolVar(&sqlPerformanceFlag, "sql-performance", false, "print only sql performance section")
	rootCmd.Flags().BoolVar(&tempfilesFlag, "tempfiles", false, "print only temporary files section")
	rootCmd.Flags().BoolVar(&maintenanceFlag, "maintenance", false, "print only maintenance files section")
	rootCmd.Flags().BoolVar(&checkpointsFlag, "checkpoints", false, "print only checkpoints section")
	rootCmd.Flags().BoolVar(&connectionsFlag, "connections", false, "print only connections section")
	rootCmd.Flags().BoolVar(&clientsFlag, "clients", false, "print only clients section")
	//rootCmd.PersistentFlags().StringSliceVarP(&grepExpr, "grep", "g", nil,
	//	"Filter the final lines by a substring match (can be specified multiple times)")
	rootCmd.PersistentFlags().BoolVarP(&jsonFlag, "json", "J", false, "Export results in JSON format")
	rootCmd.PersistentFlags().BoolVarP(&mdFlag, "md", "", false, "Export results in markdown format")

}

// executeParsing is the main run function (streaming version).
func executeParsing(cmd *cobra.Command, args []string) {
	// 0) Record the start time.
	startTime := time.Now()

	// 1) Collect files.
	allFiles := collectFiles(args)
	if len(allFiles) == 0 {
		fmt.Println("[INFO] No log files found. Exiting.")
		os.Exit(0)
	}

	// Calculate total file size.
	var totalFileSize int64 = 0
	for _, file := range allFiles {
		fi, err := os.Stat(file)
		if err == nil {
			totalFileSize += fi.Size()
		}
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
	// ✅ OPTIMISÉ: Buffer plus grand pour réduire la contention
	rawLogs := make(chan parser.LogEntry, 65536)

	// 5) ✅ OPTIMISÉ: Launch file reading avec contrôle du nombre de workers
	go func() {
		// Déterminer le nombre optimal de workers
		numWorkers := determineWorkerCount(len(allFiles))

		if numWorkers == 1 {
			// ✅ Cas spécial: un seul fichier, pas besoin de goroutines multiples
			for _, file := range allFiles {
				if err := parser.ParseFile(file, rawLogs); err != nil {
					log.Printf("Error parsing file %s: %v", file, err)
				}
			}
		} else {
			// ✅ Worker pool limité pour éviter la contention
			fileChan := make(chan string, len(allFiles))
			for _, file := range allFiles {
				fileChan <- file
			}
			close(fileChan)

			var wg sync.WaitGroup
			for i := 0; i < numWorkers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for file := range fileChan {
						if err := parser.ParseFile(file, rawLogs); err != nil {
							log.Printf("Error parsing file %s: %v", file, err)
						}
					}
				}()
			}
			wg.Wait()
		}
		close(rawLogs)
	}()

	// 6) Create the channel for filtered logs.
	// ✅ OPTIMISÉ: Buffer plus grand
	filteredLogs := make(chan parser.LogEntry, 65536)

	// 7) Build the filter structure.
	filters := parser.LogFilters{
		BeginT:      bT,
		EndT:        eT,
		DbFilter:    dbFilter,
		UserFilter:  userFilter,
		ExcludeUser: excludeUser,
		AppFilter:   appFilter,
		//GrepExpr:    grepExpr,
	}

	// 8) Apply streaming filtering.
	go parser.FilterStream(rawLogs, filteredLogs, filters)

	// 9) Process SQL query details if specified.
	if len(queryDetailFlag) > 0 {
		sqlMetrics := analysis.RunSQLSummary(filteredLogs)
		processingDuration := time.Since(startTime)
		PrintProcessingSummary(sqlMetrics.TotalQueries, processingDuration, totalFileSize)
		output.PrintSqlDetails(sqlMetrics, queryDetailFlag)
		return
	}

	// 10) Process SQL summary if specified.
	if sqlSummaryFlag {
		sqlMetrics := analysis.RunSQLSummary(filteredLogs)
		processingDuration := time.Since(startTime)
		PrintProcessingSummary(sqlMetrics.TotalQueries, processingDuration, totalFileSize)
		output.PrintSQLSummary(sqlMetrics, false)
		return
	}

	// 11) Default output: global aggregated metrics.
	metrics := analysis.AggregateMetrics(filteredLogs)

	processingDuration := time.Since(startTime)

	if metrics.Global.MaxTimestamp.IsZero() || !metrics.Global.MaxTimestamp.After(metrics.Global.MinTimestamp) {
		log.Fatalf("Error: the computed duration is 0 (MinTimestamp: %v, MaxTimestamp: %v)", metrics.Global.MinTimestamp, metrics.Global.MaxTimestamp)
	}

	// Build list of sections to report
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
	if maintenanceFlag {
		sections = append(sections, "maintenance")
	}
	if connectionsFlag {
		sections = append(sections, "connections")
	}
	if clientsFlag {
		sections = append(sections, "clients")
	}

	if len(sections) == 0 {
		sections = []string{"all"}
	}

	// Export JSON if requested
	if jsonFlag {
		output.ExportJSON(metrics, sections)
		return
	}

	// markdown export if requested
	if mdFlag {
		output.ExportMarkdown(metrics, sections)
		return
	}

	PrintProcessingSummary(metrics.Global.Count, processingDuration, totalFileSize)

	output.PrintMetrics(metrics, sections)
}

// ============================================================================
// ✅ NOUVELLE FONCTION: Détermine le nombre optimal de workers
// ============================================================================

// determineWorkerCount calcule le nombre optimal de workers en fonction du nombre de fichiers
func determineWorkerCount(numFiles int) int {
	if numFiles == 1 {
		return 1 // Un seul fichier, pas besoin de parallélisme
	}

	// Pour plusieurs fichiers, limite à NumCPU/2 pour éviter la contention
	maxWorkers := runtime.NumCPU() / 2
	if maxWorkers < 2 {
		maxWorkers = 2
	}
	if maxWorkers > 4 {
		maxWorkers = 4 // Max 4 workers pour éviter trop de contention
	}

	if numFiles < maxWorkers {
		return numFiles // Ne pas créer plus de workers que de fichiers
	}

	return maxWorkers
}

// ============================================================================
// HELPERS (inchangés)
// ============================================================================

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

// PrintProcessingSummary displays the summary line after processing logs.
func PrintProcessingSummary(numEntries int, duration time.Duration, fileSize int64) {
	fmt.Printf("quellog – %d entries processed in %.2f s (%s)\n",
		numEntries, duration.Seconds(), formatBytes(fileSize))
}

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
