// Package cmd implements the command-line interface for quellog.
// It uses the Cobra library to handle commands, flags, and execution.
package cmd

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
)

// Version information (passed from main)
var (
	version string
	commit  string
	date    string
)

// Flag variables for command-line options.
// These are package-level variables as required by Cobra's flag binding.
var (
	// Time filtering flags
	beginTime  string // --begin: Filter entries after this datetime
	endTime    string // --end: Filter entries before this datetime
	windowFlag string // --window: Time window duration (e.g., 30m, 2h)

	// Attribute filtering flags
	dbFilter    []string // --dbname: Filter by database name(s)
	appFilter   []string // --appname: Filter by application name(s)
	userFilter  []string // --dbuser: Filter by database user(s)
	excludeUser []string // --exclude-user: Exclude specific user(s)

	// SQL analysis flags
	sqlSummaryFlag bool     // --sql-summary: Display SQL performance summary
	sqlDetailFlag  []string // --sql-detail: Show details for specific SQL IDs

	// Section selection flags (print only specific sections)
	summaryFlag        bool // --summary: Print only summary section
	eventsFlag         bool // --events: Print only events section
	errorsFlag         bool // --errors: Print only error classes section
	sqlPerformanceFlag bool // --sql-performance: Print only SQL performance section
	tempfilesFlag      bool // --tempfiles: Print only temporary files section
	locksFlag          bool // --locks: Print only locks section
	maintenanceFlag    bool // --maintenance: Print only maintenance section
	checkpointsFlag    bool // --checkpoints: Print only checkpoints section
	connectionsFlag    bool // --connections: Print only connections section
	clientsFlag        bool // --clients: Print only clients section

	// Output format flags
	jsonFlag bool // --json: Export results in JSON format
	mdFlag   bool // --md: Export results in Markdown format
)

// rootCmd is the main command for the quellog CLI.
var rootCmd = &cobra.Command{
	Use:   "quellog [files or dirs]",
	Short: "PostgreSQL log parser and analyzer",
	Long: `quellog is a CLI tool to parse and analyze PostgreSQL logs.

It extracts insights about database operations including:
  - Query performance and SQL statistics
  - Connection patterns and session analysis
  - Checkpoint activity and database events
  - Temporary file usage and maintenance operations

Specify log files or directories as arguments, and use flags to filter
and customize the output.`,
	Run: executeParsing,
}

// Execute runs the root command.
// This is called by main.go to start the CLI application.
func Execute(v, c, d string) {
	version = v
	commit = c
	date = d
	rootCmd.Version = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)

	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

// init initializes all command-line flags.
func init() {
	// Time filter flags
	rootCmd.PersistentFlags().StringVarP(&beginTime, "begin", "b", "",
		"Filter entries after this datetime (format: YYYY-MM-DD HH:MM:SS)")
	rootCmd.PersistentFlags().StringVarP(&endTime, "end", "e", "",
		"Filter entries before this datetime (format: YYYY-MM-DD HH:MM:SS)")
	rootCmd.PersistentFlags().StringVarP(&windowFlag, "window", "W", "",
		"Time window duration (e.g., 30m, 2h). Adjusts --begin or --end accordingly")

	// Attribute filter flags
	rootCmd.PersistentFlags().StringSliceVarP(&dbFilter, "dbname", "d", nil,
		"Filter by database name(s). Can be specified multiple times")
	rootCmd.PersistentFlags().StringSliceVarP(&userFilter, "dbuser", "u", nil,
		"Filter by database user(s). Can be specified multiple times")
	rootCmd.PersistentFlags().StringSliceVarP(&excludeUser, "exclude-user", "U", nil,
		"Exclude entries from specified user(s)")
	rootCmd.PersistentFlags().StringSliceVarP(&appFilter, "appname", "N", nil,
		"Filter by application name(s)")

	// SQL analysis flags
	rootCmd.PersistentFlags().BoolVar(&sqlSummaryFlag, "sql-summary", false,
		"Display SQL performance summary with metrics and percentiles")
	rootCmd.PersistentFlags().StringSliceVarP(&sqlDetailFlag, "sql-detail", "Q", nil,
		"Show details for specific SQL ID(s). Can be specified multiple times")

	// Section selection flags
	rootCmd.Flags().BoolVar(&summaryFlag, "summary", false,
		"Print only the summary section")
	rootCmd.Flags().BoolVar(&eventsFlag, "events", false,
		"Print only the events section")
	rootCmd.Flags().BoolVar(&errorsFlag, "errors", false,
		"Print only the error classes section")
	rootCmd.Flags().BoolVar(&sqlPerformanceFlag, "sql-performance", false,
		"Print only the SQL performance section")
	rootCmd.Flags().BoolVar(&tempfilesFlag, "tempfiles", false,
		"Print only the temporary files section")
	rootCmd.Flags().BoolVar(&locksFlag, "locks", false,
		"Print only the locks section")
	rootCmd.Flags().BoolVar(&maintenanceFlag, "maintenance", false,
		"Print only the maintenance section")
	rootCmd.Flags().BoolVar(&checkpointsFlag, "checkpoints", false,
		"Print only the checkpoints section")
	rootCmd.Flags().BoolVar(&connectionsFlag, "connections", false,
		"Print only the connections section")
	rootCmd.Flags().BoolVar(&clientsFlag, "clients", false,
		"Print only the clients section")

	// Output format flags
	rootCmd.PersistentFlags().BoolVarP(&jsonFlag, "json", "J", false,
		"Export results in JSON format")
	rootCmd.PersistentFlags().BoolVarP(&mdFlag, "md", "", false,
		"Export results in Markdown format")
}
