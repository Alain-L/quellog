// Package cmd implements the command-line interface for quellog.
// It uses the Cobra library to handle commands, flags, and execution.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	lastFlag   string // --last: Analyze last N duration (e.g., 1h, 30m)

	// Attribute filtering flags
	dbFilter    []string // --dbname: Filter by database name(s)
	appFilter   []string // --appname: Filter by application name(s)
	userFilter  []string // --dbuser: Filter by database user(s)
	excludeUser []string // --exclude-user: Exclude specific user(s)

	// SQL analysis flags
	sqlPerformanceFlag bool     // --sql-performance: Display detailed SQL performance report
	sqlOverviewFlag    bool     // --sql-overview: Display query type overview with dimensional breakdown
	sqlDetailFlag      []string // --sql-detail: Show details for specific SQL IDs
	eventDetailFlag    []string // --event-detail: Show details for specific event pattern IDs (e.g. wa-aBc1)

	// Section selection flags (print only specific sections)
	summaryFlag     bool // --summary: Print only summary section
	eventsFlag      bool // --events: Print only events section
	errorsFlag      bool // --errors: Print only error classes section
	sqlSummaryFlag  bool // --sql-summary: Print only SQL summary section
	tempfilesFlag   bool // --tempfiles: Print only temporary files section
	locksFlag       bool // --locks: Print only locks section
	maintenanceFlag bool // --maintenance: Print only maintenance section
	checkpointsFlag bool // --checkpoints: Print only checkpoints section
	connectionsFlag bool // --connections: Print only connections section
	clientsFlag     bool // --clients: Print only clients section
	serverFlag      bool // --server: Print only server lifecycle section
	replicationFlag bool // --replication: Print only replication section

	// Output format flags
	jsonFlag        bool // --json: Export results in JSON format
	jsonCompactFlag bool // --json-compact: Export JSON without indentation (smaller output)
	yamlFlag        bool // --yaml: Export results in YAML format
	mdFlag          bool // --md: Export results in Markdown format
	htmlFlag        bool // --html: Export results as standalone HTML report
	openFlag        bool // --open: Open the generated HTML report in the default browser

	// Report completeness flag
	fullFlag bool // --full: Display comprehensive report with all sections and detailed SQL analysis

	// Follow mode flags
	followFlag   bool          // --follow: Continuous monitoring mode
	intervalFlag time.Duration // --interval: Refresh interval for follow mode
	outputFlag   string        // --output: Output file path (mandatory for follow mode with JSON/HTML)

	// Logging verbosity
	quietFlag bool // --quiet: suppress INFO logs, keep WARN and above
)

// completionCmd generates shell autocompletion scripts for bash, zsh,
// fish and powershell. Cobra implements all four — we just expose the
// subcommand. Install instructions are printed in the long help.
var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion script",
	Long: `Output a shell completion script to stdout.

Examples:
  # bash (one-shot for the current shell)
  source <(quellog completion bash)

  # bash (persistent, system-wide on macOS with brew bash-completion)
  quellog completion bash > $(brew --prefix)/etc/bash_completion.d/quellog

  # zsh (persistent, with compinit already enabled in your .zshrc)
  quellog completion zsh > "${fpath[1]}/_quellog"

  # fish
  quellog completion fish > ~/.config/fish/completions/quellog.fish

  # PowerShell
  quellog completion powershell | Out-String | Invoke-Expression`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return cmd.Root().GenBashCompletion(os.Stdout)
		case "zsh":
			return cmd.Root().GenZshCompletion(os.Stdout)
		case "fish":
			return cmd.Root().GenFishCompletion(os.Stdout, true)
		case "powershell":
			return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
		}
		return nil
	},
}

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
	RunE:          executeParsing,
	Args:          cobra.ArbitraryArgs, // file paths, glob patterns, "-" for stdin
	SilenceErrors: true,                // we surface errors via slog in Execute()
	SilenceUsage:  true,                // do not print usage on runtime errors
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Apply --quiet now that flags have been parsed.
		if quietFlag {
			setLogLevel(slog.LevelWarn)
		}
	},
}

// Execute runs the root command.
// This is called by main.go to start the CLI application.
//
// A root context is installed with signal.NotifyContext so SIGINT and
// SIGTERM cancel any in-flight pipeline (parsing, filtering, analysis)
// without leaking goroutines. The cancellation reaches the orchestration
// layer immediately; per-file parsers complete the file they are reading
// before exiting.
func Execute(v, c, d string) {
	version = v
	commit = c
	date = d
	rootCmd.Version = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)

	initLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		slog.Error("command failed", "err", err)
		os.Exit(1)
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
		"Time window duration (e.g., 30m, 2h, 1d, 1w, 1y). Adjusts --begin or --end accordingly")
	rootCmd.PersistentFlags().StringVarP(&lastFlag, "last", "L", "",
		"Analyze last N duration from now (e.g., 1h, 30m, 1d, 1w, 5y)")

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
	rootCmd.PersistentFlags().BoolVar(&sqlPerformanceFlag, "sql-performance", false,
		"Display detailed SQL performance analysis with metrics and percentiles")
	rootCmd.PersistentFlags().BoolVar(&sqlOverviewFlag, "sql-overview", false,
		"Display query type overview with breakdown by dimension")
	rootCmd.PersistentFlags().StringSliceVarP(&sqlDetailFlag, "sql-detail", "Q", nil,
		"Show details for specific SQL ID(s). Can be specified multiple times")
	rootCmd.PersistentFlags().StringSliceVarP(&eventDetailFlag, "event-detail", "E", nil,
		"Show details for specific event pattern ID(s) (e.g. wa-aBc1, er-Qr5p). Can be specified multiple times")

	// Section selection flags
	rootCmd.Flags().BoolVar(&summaryFlag, "summary", false,
		"Print only the summary section")
	rootCmd.Flags().BoolVar(&eventsFlag, "events", false,
		"Print only the events section")
	rootCmd.Flags().BoolVar(&errorsFlag, "errors", false,
		"Print only the error classes section")
	rootCmd.Flags().BoolVar(&sqlSummaryFlag, "sql-summary", false,
		"Print only the SQL summary section")
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
	rootCmd.Flags().BoolVar(&serverFlag, "server", false,
		"Print only the server lifecycle section")
	rootCmd.Flags().BoolVar(&replicationFlag, "replication", false,
		"Print only the replication section")

	// Output format flags
	rootCmd.PersistentFlags().BoolVarP(&jsonFlag, "json", "J", false,
		"Export results in JSON format")
	rootCmd.PersistentFlags().BoolVar(&jsonCompactFlag, "json-compact", false,
		"Export JSON without indentation (smaller output, lower memory)")
	rootCmd.PersistentFlags().BoolVarP(&yamlFlag, "yaml", "Y", false,
		"Export results in YAML format (gomplate compatible)")
	rootCmd.PersistentFlags().BoolVarP(&mdFlag, "md", "", false,
		"Export results in Markdown format")
	rootCmd.PersistentFlags().BoolVarP(&htmlFlag, "html", "H", false,
		"Export results as standalone HTML report")
	rootCmd.PersistentFlags().BoolVar(&openFlag, "open", false,
		"Open the generated HTML report in the default browser (requires --html)")

	// Report completeness flag
	rootCmd.PersistentFlags().BoolVarP(&fullFlag, "full", "F", false,
		"Display comprehensive report with all sections and detailed SQL analysis")

	// Follow mode flags
	rootCmd.PersistentFlags().BoolVar(&followFlag, "follow", false,
		"Continuous monitoring mode (re-parse and refresh report periodically)")
	rootCmd.PersistentFlags().DurationVar(&intervalFlag, "interval", 30*time.Second,
		"Refresh interval for follow mode (e.g., 10s, 1m)")
	rootCmd.PersistentFlags().StringVarP(&outputFlag, "output", "o", "",
		"Output file path (recommended for follow mode with JSON or HTML formats)")

	// Verbosity
	rootCmd.PersistentFlags().BoolVarP(&quietFlag, "quiet", "q", false,
		"Suppress INFO logs (keep WARN and ERROR)")

	// Subcommands
	rootCmd.AddCommand(completionCmd)
}
