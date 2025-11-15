package output

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"

	"golang.org/x/term"
)

// TextFormatter formats the report in plain text.
type TextFormatter struct{}

// PrintMetrics displays the aggregated metrics.
func PrintMetrics(m analysis.AggregatedMetrics, sections []string) {

	// Check flags
	has := func(name string) bool {
		for _, s := range sections {
			if s == name || s == "all" {
				return true
			}
		}
		return false
	}

	// Calculate total duration from min and max timestamps.
	duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)

	// ANSI style for bold text.
	bold := "\033[1m"
	reset := "\033[0m"

	// General summary header.
	if has("summary") {
		fmt.Println(bold + "\nSUMMARY\n" + reset)
		fmt.Printf("  %-25s : %s\n", "Start date", m.Global.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
		fmt.Printf("  %-25s : %s\n", "End date", m.Global.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
		fmt.Printf("  %-25s : %s\n", "Duration", duration)
		fmt.Printf("  %-25s : %d\n", "Total entries", m.Global.Count)
		if duration > 0 {
			rate := float64(m.Global.Count) / duration.Seconds()
			fmt.Printf("  %-25s : %.2f entries/s\n", "Throughput", rate)
		}
	}

	// SQL performance section
	if has("sql_performance") && m.SQL.TotalQueries > 0 {
		PrintSQLSummary(m.SQL, true)
	}

	// Events
	if has("events") && len(m.EventSummaries) > 0 {
		PrintEventsReport(m.EventSummaries)
	}

	// Error Classes
	if has("errors") && len(m.ErrorClasses) > 0 {
		fmt.Println(bold + "\nERROR CLASSES\n" + reset)

		// Determine the longest description for alignment
		maxDescLength := 0
		for _, ec := range m.ErrorClasses {
			descWithCode := fmt.Sprintf("%s – %s", ec.ClassCode, ec.Description)
			if len(descWithCode) > maxDescLength {
				maxDescLength = len(descWithCode)
			}
		}

		// Print error classes with aligned counts
		for _, ec := range m.ErrorClasses {
			fmt.Printf("  %s – %-*s : %d\n",
				ec.ClassCode,
				maxDescLength - len(ec.ClassCode) - 3, // -3 for " – "
				ec.Description,
				ec.Count)
		}
	}

	// Temp Files section.
	if has("tempfiles") && m.TempFiles.Count > 0 {

		fmt.Println(bold + "\nTEMP FILES\n" + reset)

		// Histogram
		hist, unit, scaleFactor := computeTempFileHistogram(m.TempFiles)
		PrintHistogram(hist, "Temp file distribution", unit, scaleFactor, nil)

		fmt.Printf("  %-25s : %d\n", "Temp file messages", m.TempFiles.Count)
		fmt.Printf("  %-25s : %s\n", "Cumulative temp file size", formatBytes(m.TempFiles.TotalSize))
		avgSize := int64(0)
		if m.TempFiles.Count > 0 {
			avgSize = m.TempFiles.TotalSize / int64(m.TempFiles.Count)
		}
		fmt.Printf("  %-25s : %s\n", "Average temp file size", formatBytes(avgSize))

		// Queries generating temp files (only shown with --tempfiles flag, not in default report)
		if !has("all") && len(m.TempFiles.QueryStats) > 0 {
			termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				termWidth = 120
			}

			fmt.Println(bold + "\nQueries generating temp files:" + reset)

			// Sort queries by total size descending
			type queryWithSize struct {
				stat *analysis.TempFileQueryStat
			}
			queries := make([]queryWithSize, 0, len(m.TempFiles.QueryStats))
			for _, stat := range m.TempFiles.QueryStats {
				queries = append(queries, queryWithSize{stat: stat})
			}
			sort.Slice(queries, func(i, j int) bool {
				return queries[i].stat.TotalSize > queries[j].stat.TotalSize
			})

			// Display top 10
			limit := 10
			if len(queries) < limit {
				limit = len(queries)
			}

			if termWidth >= 120 {
				// Wide mode: show full query
				// Calculate consistent table width (90% of terminal)
				tableWidth := int(float64(termWidth) * 0.9)
				if tableWidth > termWidth-10 {
					tableWidth = termWidth - 10
				}

				// Fixed columns: SQLID(9) + Count(10) + Total Size(12) = 31
				// Spacing: 3 fixed columns * 2 spaces = 6
				fixedWidth := 31
				spacingWidth := 6
				queryWidth := tableWidth - fixedWidth - spacingWidth
				if queryWidth < 40 {
					queryWidth = 40
				}

				fmt.Printf("%s%-9s  %-*s  %10s  %12s%s\n",
					bold, "SQLID", queryWidth, "Query", "Count", "Total Size", reset)
				fmt.Println(strings.Repeat("-", tableWidth))

				for i := 0; i < limit; i++ {
					stat := queries[i].stat
					truncatedQuery := truncateQuery(stat.NormalizedQuery, queryWidth)
					fmt.Printf("%-9s  %-*s  %10d  %12s\n",
						stat.ID,
						queryWidth, truncatedQuery,
						stat.Count,
						formatBytes(stat.TotalSize))
				}
			} else {
				// Compact mode: show type only
				header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s\n", "SQLID", "Type", "Count", "Total Size")
				fmt.Print(bold + header + reset)
				fmt.Println(strings.Repeat("-", 80))
				for i := 0; i < limit; i++ {
					stat := queries[i].stat
					qType := analysis.QueryTypeFromID(stat.ID)
					fmt.Printf("%-8s  %-10s  %-10d  %-12s\n",
						stat.ID,
						qType,
						stat.Count,
						formatBytes(stat.TotalSize))
				}
			}
		}
	}

	// Locks section
	if has("locks") && m.Locks.TotalEvents > 0 {
		fmt.Println(bold + "\nLOCKS\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Total lock events", m.Locks.TotalEvents)
		fmt.Printf("  %-25s : %d\n", "Waiting events", m.Locks.WaitingEvents)
		fmt.Printf("  %-25s : %d\n", "Acquired events", m.Locks.AcquiredEvents)
		if m.Locks.DeadlockEvents > 0 {
			fmt.Printf("  %-25s : %d\n", "Deadlock events", m.Locks.DeadlockEvents)
		}
		if m.Locks.TotalWaitTime > 0 {
			avgWaitTime := m.Locks.TotalWaitTime / float64(m.Locks.WaitingEvents+m.Locks.AcquiredEvents)
			fmt.Printf("  %-25s : %s\n", "Avg wait time", formatQueryDuration(avgWaitTime))
			fmt.Printf("  %-25s : %s\n", "Total wait time", formatQueryDuration(m.Locks.TotalWaitTime))
		}

		// Lock types distribution
		if len(m.Locks.LockTypeStats) > 0 {
			fmt.Println("  Lock types:")
			printLockStats(m.Locks.LockTypeStats, m.Locks.TotalEvents)
		}

		// Resource types distribution
		if len(m.Locks.ResourceTypeStats) > 0 {
			fmt.Println("  Resource types:")
			printLockStats(m.Locks.ResourceTypeStats, m.Locks.TotalEvents)
		}

		// Acquired locks by query (only shown with --locks flag, not in default report)
		if !has("all") && len(m.Locks.QueryStats) > 0 {
			hasAcquired := false
			for _, stat := range m.Locks.QueryStats {
				if stat.AcquiredCount > 0 {
					hasAcquired = true
					break
				}
			}
			if hasAcquired {
				termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					termWidth = 120
				}

				fmt.Println(bold + "\nAcquired locks by query:" + reset)
				printAcquiredLockQueries(m.Locks.QueryStats, 10, termWidth)
			}
		}

		// Locks still waiting by query (only shown with --locks flag, not in default report)
		if !has("all") && len(m.Locks.QueryStats) > 0 {
			hasStillWaiting := false
			for _, stat := range m.Locks.QueryStats {
				if stat.StillWaitingCount > 0 {
					hasStillWaiting = true
					break
				}
			}
			if hasStillWaiting {
				termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					termWidth = 120
				}

				fmt.Println(bold + "\nLocks still waiting by query:" + reset)
				printStillWaitingLockQueries(m.Locks.QueryStats, 10, termWidth)
			}
		}

		// Most frequent waiting queries (only shown with --locks flag, not in default report)
		if !has("all") && len(m.Locks.QueryStats) > 0 {
			hasWaiting := false
			for _, stat := range m.Locks.QueryStats {
				if stat.AcquiredCount > 0 || stat.StillWaitingCount > 0 {
					hasWaiting = true
					break
				}
			}
			if hasWaiting {
				termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					termWidth = 120
				}

				fmt.Println(bold + "\nMost frequent waiting queries:" + reset)
				printMostFrequentWaitingQueries(m.Locks.QueryStats, 10, termWidth)
			}
		}
	}

	// Maintenance Metrics section.
	if has("maintenance") && (m.Vacuum.VacuumCount > 0 || m.Vacuum.AnalyzeCount > 0) {
		fmt.Println(bold + "\nMAINTENANCE\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Automatic vacuum count", m.Vacuum.VacuumCount)
		fmt.Printf("  %-25s : %d\n", "Automatic analyze count", m.Vacuum.AnalyzeCount)
		fmt.Println("  Top automatic vacuum operations per table:")
		printTopTables(m.Vacuum.VacuumTableCounts, m.Vacuum.VacuumCount, m.Vacuum.VacuumSpaceRecovered)
		fmt.Println("  Top automatic analyze operations per table:")
		printTopTables(m.Vacuum.AnalyzeTableCounts, m.Vacuum.AnalyzeCount, nil)
	}

	// Checkpoints section
	if has("checkpoints") && m.Checkpoints.CompleteCount > 0 {
		avgWriteSeconds := m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)
		avgDuration := time.Duration(avgWriteSeconds * float64(time.Second)).Truncate(time.Second)
		maxDuration := time.Duration(m.Checkpoints.MaxWriteTimeSeconds * float64(time.Second)).Truncate(time.Second)

		fmt.Println(bold + "\nCHECKPOINTS\n" + reset)

		// Histogram
		hist, _, scaleFactor := computeCheckpointHistogram(m.Checkpoints)
		PrintHistogram(hist, "Checkpoints", "", scaleFactor, nil)

		fmt.Printf("  %-25s : %d\n", "Checkpoint count", m.Checkpoints.CompleteCount)
		fmt.Printf("  %-25s : %s\n", "Avg checkpoint write time", avgDuration)
		fmt.Printf("  %-25s : %s\n", "Max checkpoint write time", maxDuration)

		// Affichage des types de checkpoints
		if len(m.Checkpoints.TypeCounts) > 0 {
			fmt.Println("  Checkpoint types:")

			// Créer une slice pour trier les types par count (décroissant)
			type typePair struct {
				Name  string
				Count int
			}
			var pairs []typePair
			for cpType, count := range m.Checkpoints.TypeCounts {
				pairs = append(pairs, typePair{Name: cpType, Count: count})
			}

			// Trier par count décroissant, puis par nom alphabétique
			sort.Slice(pairs, func(i, j int) bool {
				if pairs[i].Count != pairs[j].Count {
					return pairs[i].Count > pairs[j].Count
				}
				return pairs[i].Name < pairs[j].Name
			})

			// Calculer la durée totale pour les pourcentages et le taux
			duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
			durationHours := duration.Hours()

			// Déterminer la largeur max pour les noms de types
			maxTypeLen := 0
			for _, pair := range pairs {
				if len(pair.Name) > maxTypeLen {
					maxTypeLen = len(pair.Name)
				}
			}
			if maxTypeLen < 10 {
				maxTypeLen = 10
			}

			// Afficher chaque type avec son count, pourcentage et taux
			for _, pair := range pairs {
				percentage := float64(pair.Count) / float64(m.Checkpoints.CompleteCount) * 100

				// Calculer le taux (checkpoints par heure) pour ce type
				rate := 0.0
				if durationHours > 0 {
					rate = float64(pair.Count) / durationHours
				}

				// Format: type (left-aligned), count (right, 3 digits), percentage (right, 6 chars), rate (right, 8 chars)
				fmt.Printf("    %-*s  %3d  %5.1f%%  (%.2f/h)\n",
					maxTypeLen, pair.Name, pair.Count, percentage, rate)
			}
		}
	}

	// Connections & Sessions Metrics section.
	if has("connections") && m.Connections.ConnectionReceivedCount > 0 {
		fmt.Println(bold + "\nCONNECTIONS & SESSIONS\n" + reset)

		// Histogram
		hist, _, scaleFactor := computeConnectionsHistogram(m.Connections.Connections)
		PrintHistogram(hist, "Connection distribution", "", scaleFactor, nil)

		fmt.Printf("  %-25s : %d\n", "Connection count", m.Connections.ConnectionReceivedCount)
		if duration.Hours() > 0 {
			avgConnPerHour := float64(m.Connections.ConnectionReceivedCount) / duration.Hours()
			fmt.Printf("  %-25s : %.2f\n", "Avg connections per hour", avgConnPerHour) // Ensure float formatting
		}
		fmt.Printf("  %-25s : %d\n", "Disconnection count", m.Connections.DisconnectionCount)
		if m.Connections.DisconnectionCount > 0 {
			avgSessionTime := time.Duration(float64(m.Connections.TotalSessionTime) / float64(m.Connections.DisconnectionCount))
			fmt.Printf("  %-25s : %s\n", "Avg session time", avgSessionTime.Round(time.Second))
		} else {
			fmt.Printf("  %-25s : %s\n", "Avg session time", "N/A")
		}
	}

	// Unique Clients section.
	if has("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0 || m.UniqueEntities.UniqueHosts > 0) {
		fmt.Println(bold + "\nCLIENTS\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Unique DBs", m.UniqueEntities.UniqueDbs)
		fmt.Printf("  %-25s : %d\n", "Unique Users", m.UniqueEntities.UniqueUsers)
		fmt.Printf("  %-25s : %d\n", "Unique Apps", m.UniqueEntities.UniqueApps)
		fmt.Printf("  %-25s : %d\n", "Unique Hosts", m.UniqueEntities.UniqueHosts)

		// Display lists.
		if m.UniqueEntities.UniqueUsers > 0 {
			fmt.Println(bold + "\nUSERS\n" + reset)
			for _, user := range m.UniqueEntities.Users {
				fmt.Printf("    %s\n", user)
			}
		}
		if m.UniqueEntities.UniqueApps > 0 {
			fmt.Println(bold + "\nAPPS\n" + reset)
			for _, app := range m.UniqueEntities.Apps {
				fmt.Printf("    %s\n", app)
			}
		}
		if m.UniqueEntities.UniqueDbs > 0 {
			fmt.Println(bold + "\nDATABASES\n" + reset)
			for _, db := range m.UniqueEntities.DBs {
				fmt.Printf("    %s\n", db)
			}
		}
		if m.UniqueEntities.UniqueHosts > 0 {
			fmt.Println(bold + "\nHOSTS\n" + reset)
			for _, host := range m.UniqueEntities.Hosts {
				fmt.Printf("    %s\n", host)
			}
		}
	}
	fmt.Println()
}

// printTopTables prints the top tables for a given operation (vacuum or analyze).
// It stops when the cumulative count reaches at least 80% of the total, unless fewer than 10 tables are available.
func printTopTables(tableCounts map[string]int, total int, spaceRecovered map[string]int64) {
	// Convert the map into a slice of pairs.
	type tablePair struct {
		Name      string
		Count     int
		Recovered int64 // in bytes.
	}
	var pairs []tablePair
	for name, count := range tableCounts {
		p := tablePair{
			Name:  name,
			Count: count,
		}
		if spaceRecovered != nil {
			p.Recovered = spaceRecovered[name]
		}
		pairs = append(pairs, p)
	}

	// Sort by count in descending order, then by name alphabetically.
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		return pairs[i].Name < pairs[j].Name
	})

	// Determine maximum width for table names.
	tableLen := 0
	for _, p := range pairs {
		if l := len(p.Name); l > tableLen {
			tableLen = l
		}
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		if tableLen > int(float64(w)*0.4) {
			tableLen = int(float64(w) * 0.4)
		}
	}

	cum := 0
	n := 0
	for _, pair := range pairs {
		percentage := float64(pair.Count) / float64(total) * 100
		cum += pair.Count
		n++
		cumPercentage := float64(cum) / float64(total) * 100

		// Fixed alignment: table name (left, width = tableLen), count (right, width 6), percentage (right, width 6, 2 decimals).
		if spaceRecovered != nil && pair.Recovered > 0 {
			fmt.Printf("    %-*s %6d %6.2f%%  %12s removed\n",
				tableLen, pair.Name, pair.Count, percentage, formatBytes(pair.Recovered))
		} else {
			fmt.Printf("    %-*s %6d %6.2f%%\n",
				tableLen, pair.Name, pair.Count, percentage)
		}

		if cumPercentage >= 80 || n >= 10 {
			break
		}
	}
}

// PrintSQLSummary displays an SQL performance report in the CLI.
// The report uses ANSI bold formatting for better readability. The query text is truncated based on terminal width.
func PrintSQLSummary(m analysis.SqlMetrics, indicatorsOnly bool) {
	PrintSQLSummaryWithContext(m, analysis.TempFileMetrics{}, analysis.LockMetrics{}, indicatorsOnly)
}

// PrintSQLSummaryWithContext displays SQL performance with optional tempfiles and locks context.
func PrintSQLSummaryWithContext(m analysis.SqlMetrics, tempFiles analysis.TempFileMetrics, locks analysis.LockMetrics, indicatorsOnly bool) {
	// Get terminal width, defaulting to 80.
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width = w
	}

	// Define histogram width dynamically
	histogramWidth := width / 2 // Reserve space for labels (e.g., "00:00 - 04:00  ")
	if histogramWidth < 20 {
		histogramWidth = 20 // Ensure it is readable
	}

	// ANSI styles.
	bold := "\033[1m"
	reset := "\033[0m"

	// Compute top 1% slowest queries.
	top1Slow := 0
	if len(m.Executions) > 0 {
		threshold := m.P99QueryDuration
		for _, exec := range m.Executions {
			if exec.Duration >= threshold {
				top1Slow++
			}
		}
	}

	// ** SQL Summary Header **
	fmt.Println(bold + "\nSQL PERFORMANCE" + reset)
	fmt.Println()

	// ** Query Load Histogram **
	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		queryLoad, unit, scale := computeQueryLoadHistogram(m)
		PrintHistogram(queryLoad, "Query load distribution", unit, scale, nil)
	}

	fmt.Printf("  %-25s : %-20s\n", "Total query duration", formatQueryDuration(m.SumQueryDuration))
	fmt.Printf("  %-25s : %-20d\n", "Total queries parsed", m.TotalQueries)
	fmt.Printf("  %-25s : %-20d\n", "Total unique query", m.UniqueQueries)
	fmt.Printf("  %-25s : %-20d\n", "Top 1% slow queries", top1Slow)
	fmt.Println()
	fmt.Printf("  %-25s : %-20s\n", "Query max duration", formatQueryDuration(m.MaxQueryDuration))
	fmt.Printf("  %-25s : %-20s\n", "Query min duration", formatQueryDuration(m.MinQueryDuration))
	fmt.Printf("  %-25s : %-20s\n", "Query median duration", formatQueryDuration(m.MedianQueryDuration))
	fmt.Printf("  %-25s : %-20s\n", "Query 99% max duration", formatQueryDuration(m.P99QueryDuration))
	fmt.Println()

	if !indicatorsOnly {

		// Définition de l'ordre des labels pour l'histogramme des durées de requêtes.
		queryDurationOrder := []string{
			"< 1 ms",
			"< 10 ms",
			"< 100 ms",
			"< 1 s",
			"< 10 s",
			">= 10 s",
		}

		// ** Query Time Histogram **
		if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
			hist, unit, scale := computeQueryDurationHistogram(m)
			PrintHistogram(hist, "Query duration distribution", unit, scale, queryDurationOrder)
		}

		PrintQueryTableWithTitle("Slowest individual queries:", m.QueryStats, QueryTableConfig{
			Columns: []QueryTableColumn{
				ColumnSQLID(),
				ColumnQuery(),
				ColumnDuration(),
			},
			SortFunc:          SortByMaxTime,
			Limit:             10,
			ShowQueryText:     true,
			TableWidthPercent: 70,
		})

		PrintQueryTableWithTitle("Most Frequent Individual Queries:", m.QueryStats, QueryTableConfig{
			Columns: []QueryTableColumn{
				ColumnSQLID(),
				ColumnQuery(),
				ColumnCount(),
			},
			SortFunc: SortByCount,
			FilterFunc: func(row QueryRow) bool {
				return row.Count > 1
			},
			Limit:             15,
			ShowQueryText:     true,
			TableWidthPercent: 70,
		})

		PrintQueryTableWithTitle("Most time consuming queries:", m.QueryStats, QueryTableConfig{
			Columns: []QueryTableColumn{
				ColumnSQLID(),
				ColumnQuery(),
				ColumnCount(),
				ColumnMaxTime(),
				ColumnAvgTime(),
				ColumnTotalTime(),
			},
			SortFunc:      SortByTotalTime,
			Limit:         10,
			ShowQueryText: true,
		})

		// Display tempfiles and locks query tables to show queries without duration metrics
		termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			termWidth = 120
		}

		// Tempfiles queries
		if len(tempFiles.QueryStats) > 0 {
			fmt.Println(bold + "\nTEMP FILES" + reset)
			fmt.Println()

			// Sort queries by total size descending
			type queryWithSize struct {
				stat *analysis.TempFileQueryStat
			}
			queries := make([]queryWithSize, 0, len(tempFiles.QueryStats))
			for _, stat := range tempFiles.QueryStats {
				queries = append(queries, queryWithSize{stat: stat})
			}
			sort.Slice(queries, func(i, j int) bool {
				return queries[i].stat.TotalSize > queries[j].stat.TotalSize
			})

			// Display top 10
			limit := 10
			if len(queries) < limit {
				limit = len(queries)
			}

			if termWidth >= 120 {
				// Wide mode: show full query
				// Calculate consistent table width (90% of terminal)
				tableWidth := int(float64(termWidth) * 0.9)
				if tableWidth > termWidth-10 {
					tableWidth = termWidth - 10
				}

				// Fixed columns: SQLID(9) + Count(10) + Total Size(12) = 31
				// Spacing: 3 fixed columns * 2 spaces = 6
				fixedWidth := 31
				spacingWidth := 6
				queryWidth := tableWidth - fixedWidth - spacingWidth
				if queryWidth < 40 {
					queryWidth = 40
				}

				fmt.Printf("%s%-9s  %-*s  %10s  %12s%s\n",
					bold, "SQLID", queryWidth, "Query", "Count", "Total Size", reset)
				fmt.Println(strings.Repeat("-", tableWidth))

				for i := 0; i < limit; i++ {
					stat := queries[i].stat
					truncatedQuery := truncateQuery(stat.NormalizedQuery, queryWidth)
					fmt.Printf("%-9s  %-*s  %10d  %12s\n",
						stat.ID,
						queryWidth, truncatedQuery,
						stat.Count,
						formatBytes(stat.TotalSize))
				}
			} else {
				// Compact mode: show type only
				header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s\n", "SQLID", "Type", "Count", "Total Size")
				fmt.Print(bold + header + reset)
				fmt.Println(strings.Repeat("-", 80))
				for i := 0; i < limit; i++ {
					stat := queries[i].stat
					qType := analysis.QueryTypeFromID(stat.ID)
					fmt.Printf("%-8s  %-10s  %-10d  %-12s\n",
						stat.ID,
						qType,
						stat.Count,
						formatBytes(stat.TotalSize))
				}
			}
			fmt.Println()
		}

		// Locks section header
		if len(locks.QueryStats) > 0 {
			fmt.Println(bold + "\nLOCKS" + reset)
		}

		// Locks queries - Acquired locks by query
		if len(locks.QueryStats) > 0 {
			hasAcquired := false
			for _, stat := range locks.QueryStats {
				if stat.AcquiredCount > 0 {
					hasAcquired = true
					break
				}
			}
			if hasAcquired {
				fmt.Println(bold + "\nAcquired locks by query:" + reset)
				printAcquiredLockQueries(locks.QueryStats, 10, termWidth)
				fmt.Println()
			}
		}

		// Locks still waiting by query
		if len(locks.QueryStats) > 0 {
			hasStillWaiting := false
			for _, stat := range locks.QueryStats {
				if stat.StillWaitingCount > 0 {
					hasStillWaiting = true
					break
				}
			}
			if hasStillWaiting {
				fmt.Println(bold + "\nLocks still waiting by query:" + reset)
				printStillWaitingLockQueries(locks.QueryStats, 10, termWidth)
				fmt.Println()
			}
		}

		// Most frequent waiting queries (all locks that waited, acquired or not)
		if len(locks.QueryStats) > 0 {
			hasWaiting := false
			for _, stat := range locks.QueryStats {
				if stat.AcquiredCount > 0 || stat.StillWaitingCount > 0 {
					hasWaiting = true
					break
				}
			}
			if hasWaiting {
				fmt.Println(bold + "\nMost frequent waiting queries:" + reset)
				printMostFrequentWaitingQueries(locks.QueryStats, 10, termWidth)
			}
		}
	}
}

// PrintTimeConsumingQueries sorts and displays the top 10 queries based on total execution time.
// The display adapts to the terminal width, switching between full and simplified modes.
// PrintTimeConsumingQueries displays queries sorted by total time consumed.
// Returns true if any data was printed.
func PrintTimeConsumingQueries(queryStats map[string]*analysis.QueryStat) bool {
	return PrintQueryTable(queryStats, QueryTableConfig{
		Columns: []QueryTableColumn{
			ColumnSQLID(),
			ColumnQuery(),
			ColumnCount(),
			ColumnMaxTime(),
			ColumnAvgTime(),
			ColumnTotalTime(),
		},
		SortFunc:      SortByTotalTime,
		Limit:         10,
		ShowQueryText: true,
	})
}

// PrintSlowestQueries displays the top 10 slowest individual queries,
// showing three columns: SQLID, truncated Query, and Duration.
// Returns true if any data was printed.
func PrintSlowestQueries(queryStats map[string]*analysis.QueryStat) bool {
	return PrintQueryTable(queryStats, QueryTableConfig{
		Columns: []QueryTableColumn{
			ColumnSQLID(),
			ColumnQuery(),
			ColumnDuration(),
		},
		SortFunc:          SortByMaxTime,
		Limit:             10,
		ShowQueryText:     true,
		TableWidthPercent: 70, // Narrower table for simple 3-column layout
	})
}

// PrintMostFrequentQueries displays the top queries by frequency (sorted descending by count).
// The display stops if a query was executed only once or if the execution count drops by more than a factor of 10.
// Returns true if any data was printed.
func PrintMostFrequentQueries(queryStats map[string]*analysis.QueryStat) bool {
	return PrintQueryTable(queryStats, QueryTableConfig{
		Columns: []QueryTableColumn{
			ColumnSQLID(),
			ColumnQuery(),
			ColumnCount(),
		},
		SortFunc: SortByCount,
		FilterFunc: func(row QueryRow) bool {
			// Don't show queries executed only once
			return row.Count > 1
		},
		Limit:             15,
		ShowQueryText:     true,
		TableWidthPercent: 70, // Narrower table for simple 3-column layout
	})
}

// PrintSqlDetails iterates over the QueryStats and displays details for each query
// whose SQLID matches one of the provided queryDetails.
func PrintSqlDetails(m analysis.AggregatedMetrics, queryDetails []string) {
	for _, qid := range queryDetails {
		found := false

		// First, try to find in SQL metrics (queries with duration)
		for _, qs := range m.SQL.QueryStats {
			if qs.ID == qid {
				fmt.Printf("\nDetails for SQLID: %s\n", qs.ID)
				fmt.Println("SQL Query Details:")
				fmt.Printf("  SQLID            : %s\n", qs.ID)
				fmt.Printf("  Query Type       : %s\n", analysis.QueryTypeFromID(qs.ID))
				fmt.Printf("  Raw Query        : %s\n", qs.RawQuery)
				fmt.Printf("  Normalized Query : %s\n", qs.NormalizedQuery)
				fmt.Printf("  Executed         : %d\n", qs.Count)
				fmt.Printf("  Total Time       : %s\n", formatQueryDuration(qs.TotalTime))
				fmt.Printf("  Median Time      : %s\n", formatQueryDuration(qs.AvgTime))
				fmt.Printf("  Max Time         : %s\n", formatQueryDuration(qs.MaxTime))
				found = true
				break
			}
		}

		if found {
			continue
		}

		// If not found in SQL metrics, try locks
		for _, ls := range m.Locks.QueryStats {
			if ls.ID == qid {
				fmt.Printf("\nDetails for SQLID: %s\n", ls.ID)
				fmt.Println("Query Details (from lock events):")
				fmt.Printf("  SQLID                 : %s\n", ls.ID)
				fmt.Printf("  Query Type            : %s\n", analysis.QueryTypeFromID(ls.ID))
				fmt.Printf("  Raw Query             : %s\n", ls.RawQuery)
				fmt.Printf("  Normalized Query      : %s\n", ls.NormalizedQuery)
				fmt.Printf("  Acquired Locks        : %d\n", ls.AcquiredCount)
				fmt.Printf("  Acquired Wait Time    : %s\n", formatQueryDuration(ls.AcquiredWaitTime))
				fmt.Printf("  Still Waiting Locks   : %d\n", ls.StillWaitingCount)
				fmt.Printf("  Still Waiting Time    : %s\n", formatQueryDuration(ls.StillWaitingTime))
				fmt.Printf("  Total Wait Time       : %s\n", formatQueryDuration(ls.TotalWaitTime))
				fmt.Println("\nNote: This query was identified from lock events and has no duration metrics.")
				found = true
				break
			}
		}

		if found {
			continue
		}

		// If not found in locks, try tempfiles
		for _, ts := range m.TempFiles.QueryStats {
			if ts.ID == qid {
				fmt.Printf("\nDetails for SQLID: %s\n", ts.ID)
				fmt.Println("Query Details (from tempfile events):")
				fmt.Printf("  SQLID            : %s\n", ts.ID)
				fmt.Printf("  Query Type       : %s\n", analysis.QueryTypeFromID(ts.ID))
				fmt.Printf("  Raw Query        : %s\n", ts.RawQuery)
				fmt.Printf("  Normalized Query : %s\n", ts.NormalizedQuery)
				fmt.Printf("  Tempfile Count   : %d\n", ts.Count)
				fmt.Printf("  Total Size       : %s\n", formatBytes(ts.TotalSize))
				fmt.Println("\nNote: This query was identified from tempfile events and has no duration metrics.")
				found = true
				break
			}
		}

		if !found {
			fmt.Printf("\nQuery ID '%s' not found.\n", qid)
		}
	}
}

// Helpers

// truncateQuery truncates the query string to the specified length, appending "..." if necessary.
func truncateQuery(query string, length int) string {
	if len(query) > length {
		return query[:length-3] + "..."
	}
	return query
}

func formatQueryDuration(ms float64) string {
	d := time.Duration(ms * float64(time.Millisecond))
	if d < time.Second {
		return fmt.Sprintf("%d ms", d/time.Millisecond)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.2f s", d.Seconds())
	}
	if d < time.Hour {
		minutes := int(d / time.Minute)
		seconds := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	if d < 24*time.Hour {
		hours := int(d / time.Hour)
		minutes := int((d % time.Hour) / time.Minute)
		seconds := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dh %02dm %02ds", hours, minutes, seconds)
	}
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	minutes := int((d % time.Hour) / time.Minute)
	return fmt.Sprintf("%dd %dh %02dm", days, hours, minutes)
}

// NewTextFormatter returns a new instance of TextFormatter.
func NewTextFormatter() *TextFormatter {
	return &TextFormatter{}
}

// Format returns a formatted string with the report data.
func (tf *TextFormatter) Format(report AnalysisReport) string {
	return fmt.Sprintf(`Log Analysis Report:
Start date: %s
End date:   %s
Total duration: %s

Number of VACUUM events: %d
Number of checkpoints: %d
Temp files: %d
Temp file size: %s
Number of SQL queries: %d`,
		report.StartDate.Format("2006-01-02 15:04:05"),
		report.EndDate.Format("2006-01-02 15:04:05"),
		report.Duration,
		report.VacuumCount,
		report.CheckpointsCount,
		report.TempFiles,
		formatBytes(report.TempFileSize),
		report.SQLCount,
	)
}

// PrintEventsReport prints a clean, simple event summary with aligned labels.
func PrintEventsReport(summaries []analysis.EventSummary) {
	// ANSI style for bold text.
	bold := "\033[1m"
	reset := "\033[0m"

	// Print title in bold.
	fmt.Println(bold + "\nEVENTS\n" + reset)

	// Determine the longest event type for alignment.
	maxTypeLength := 0
	for _, summary := range summaries {
		if len(summary.Type) > maxTypeLength {
			maxTypeLength = len(summary.Type)
		}
	}

	// Print event counts with aligned labels.
	for _, summary := range summaries {
		if summary.Count == 0 {
			fmt.Printf("  %-*s : -\n", maxTypeLength, summary.Type)
		} else {
			fmt.Printf("  %-*s : %d\n", maxTypeLength, summary.Type, summary.Count)
		}
	}
}

// PrintHistogram affiche l'histogramme en triant les plages horaires par ordre chronologique.
// La largeur du terminal est récupérée automatiquement pour adapter la largeur de la barre.
func PrintHistogram(data map[string]int, title string, unit string, scaleFactor int, orderedLabels []string) {
	if len(data) == 0 {
		fmt.Printf("\n  (No data available)\n")
		return
	}

	// Récupération de la largeur du terminal.
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || termWidth <= 0 {
		termWidth = 80 // valeur par défaut
	}

	// Définition des largeurs réservées : 20 pour l'étiquette et 5 pour la valeur.
	labelWidth := 20
	valueWidth := 5
	spacing := 4 // Espaces entre le label et la valeur.
	barWidth := termWidth - labelWidth - spacing - valueWidth
	if barWidth < 10 {
		barWidth = 10
	}

	// Gestion des labels (ordre fixe ou tri automatique basé sur l'heure).
	labels := make([]string, 0, len(data))
	if len(orderedLabels) > 0 {
		// Utilisation de l'ordre défini explicitement.
		labels = orderedLabels
	} else {
		// Tri automatique basé sur l'heure "HH:MM - HH:MM".
		for label := range data {
			labels = append(labels, label)
		}
		sort.Slice(labels, func(i, j int) bool {
			partsI := strings.Split(labels[i], " - ")
			partsJ := strings.Split(labels[j], " - ")
			t1, _ := time.Parse("15:04", partsI[0])
			t2, _ := time.Parse("15:04", partsJ[0])
			return t1.Before(t2)
		})
	}

	// Détermination de la valeur maximale pour normaliser l'affichage.
	maxValue := 0
	for _, value := range data {
		if value > maxValue {
			maxValue = value
		}
	}

	// Calcul dynamique du scale factor si non fourni.
	if scaleFactor <= 0 {
		scaleFactor = int(math.Ceil(float64(maxValue) / float64(barWidth)))
		if scaleFactor < 1 {
			scaleFactor = 1
		}
	}

	// Affichage de l'en-tête.
	fmt.Printf("  %s | ■ = %d %s\n\n", title, scaleFactor, unit)

	// Affichage des lignes de l'histogramme.
	for _, label := range labels {
		value := data[label]
		barLength := value / scaleFactor
		if barLength > barWidth {
			barLength = barWidth
		}
		bar := strings.Repeat("■", barLength)

		// Déterminer si on affiche la valeur ou un `-`
		valueStr := fmt.Sprintf("%d %s", value, unit)
		if value == 0 {
			valueStr = " -"
		}

		// Ajustement du formatage : si pas de barres, on aligne à gauche sans indentation.
		if barLength > 0 {
			fmt.Printf("  %-13s  %-s %s\n", label, bar, valueStr)
		} else {
			fmt.Printf("  %-14s %s\n", label, valueStr)
		}
	}
	fmt.Println()
}

// computeQueryLoadHistogram répartit les durées des requêtes SQL en 6 intervalles égaux,
// et retourne l'histogramme, l'unité (ms, s ou min) et le scale factor.
// La durée de chaque tranche est convertie pour être au moins d'une unité d'échelle.

// computeQueryDurationHistogram renvoie un histogramme sous forme de map associant des étiquettes de buckets à leur nombre de requêtes.
// computeQueryDurationHistogram calcule un histogramme basé sur la durée des requêtes (exprimée en millisecondes)
// et retourne :
// - une map associant une étiquette de bucket au nombre de requêtes
// - une chaîne "req" indiquant que les valeurs sont en nombre de requêtes,
// - un scaleFactor permettant d’afficher des barres proportionnelles sur une largeur maximale de 40 caractères.


// computeCheckpointHistogram agrège les événements de checkpoints en 6 tranches d'une journée complète.
// On considère que tous les événements se situent dans la même journée.
// Le résultat est un histogramme (map label -> nombre de checkpoints),
// l'unité ("checkpoints") et un scale factor calculé pour limiter la largeur à 35 caractères.

// computeConnectionsHistogram agrège les timestamps d'événements dans un histogramme réparti sur numBuckets.
// Les buckets sont calculés sur la journée complète (00:00 - 24:00) basée sur la première occurrence.

// printLockStats prints lock type or resource type statistics.
func printLockStats(stats map[string]int, total int) {
	// Sort by count descending
	type statPair struct {
		name  string
		count int
	}
	var pairs []statPair
	for name, count := range stats {
		pairs = append(pairs, statPair{name, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].count > pairs[j].count
	})

	// Print top entries
	for _, p := range pairs {
		percentage := (float64(p.count) / float64(total)) * 100
		fmt.Printf("    %-25s %6d  %5.1f%%\n", p.name, p.count, percentage)
	}
}

// formatLockCount formats a lock count, displaying "-" for 0.
func formatLockCount(count int) string {
	if count == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", count)
}

// printAcquiredLockQueries prints queries with acquired locks, sorted by total wait time.
func printAcquiredLockQueries(queryStats map[string]*analysis.LockQueryStat, limit int, termWidth int) {
	// Convert map to slice and filter/sort by acquired wait time
	type queryPair struct {
		stat *analysis.LockQueryStat
	}
	var pairs []queryPair
	for _, stat := range queryStats {
		if stat.AcquiredCount > 0 {
			pairs = append(pairs, queryPair{stat})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].stat.AcquiredWaitTime > pairs[j].stat.AcquiredWaitTime
	})

	// Print top queries
	if limit > len(pairs) {
		limit = len(pairs)
	}

	bold := "\033[1m"
	reset := "\033[0m"

	if termWidth >= 120 {
		// Wide mode: show full query
		// Calculate consistent table width (90% of terminal)
		tableWidth := int(float64(termWidth) * 0.9)
		if tableWidth > termWidth-10 {
			tableWidth = termWidth - 10
		}

		// Fixed columns: SQLID(9) + Locks(10) + Avg Wait(15) + Total Wait(15) = 49
		// Spacing: 4 fixed columns * 2 spaces = 8
		fixedWidth := 49
		spacingWidth := 8
		queryWidth := tableWidth - fixedWidth - spacingWidth
		if queryWidth < 40 {
			queryWidth = 40
		}

		fmt.Printf("%s%-9s  %-*s  %10s  %15s  %15s%s\n",
			bold, "SQLID", queryWidth, "Query", "Locks", "Avg Wait", "Total Wait", reset)
		fmt.Println(strings.Repeat("-", tableWidth))

		for i := 0; i < limit; i++ {
			stat := pairs[i].stat
			truncatedQuery := truncateQuery(stat.NormalizedQuery, queryWidth)
			avgWait := stat.AcquiredWaitTime / float64(stat.AcquiredCount)
			fmt.Printf("%-9s  %-*s  %10d  %15s  %15s\n",
				stat.ID,
				queryWidth, truncatedQuery,
				stat.AcquiredCount,
				formatQueryDuration(avgWait),
				formatQueryDuration(stat.AcquiredWaitTime))
		}
	} else {
		// Compact mode: show type only
		header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s  %-12s\n", "SQLID", "Type", "Locks", "Avg Wait", "Total Wait")
		fmt.Print(bold + header + reset)
		fmt.Println(strings.Repeat("-", 80))

		for i := 0; i < limit; i++ {
			stat := pairs[i].stat
			qType := analysis.QueryTypeFromID(stat.ID)
			avgWait := stat.AcquiredWaitTime / float64(stat.AcquiredCount)
			fmt.Printf("%-8s  %-10s  %-10d  %-12s  %-12s\n",
				stat.ID,
				qType,
				stat.AcquiredCount,
				formatQueryDuration(avgWait),
				formatQueryDuration(stat.AcquiredWaitTime))
		}
	}
}

// printStillWaitingLockQueries prints queries with locks still waiting, sorted by total wait time.
func printStillWaitingLockQueries(queryStats map[string]*analysis.LockQueryStat, limit int, termWidth int) {
	// Convert map to slice and filter/sort by still waiting time
	type queryPair struct {
		stat *analysis.LockQueryStat
	}
	var pairs []queryPair
	for _, stat := range queryStats {
		if stat.StillWaitingCount > 0 {
			pairs = append(pairs, queryPair{stat})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].stat.StillWaitingTime > pairs[j].stat.StillWaitingTime
	})

	// Print top queries
	if limit > len(pairs) {
		limit = len(pairs)
	}

	bold := "\033[1m"
	reset := "\033[0m"

	if termWidth >= 120 {
		// Wide mode: show full query
		// Calculate consistent table width (90% of terminal)
		tableWidth := int(float64(termWidth) * 0.9)
		if tableWidth > termWidth-10 {
			tableWidth = termWidth - 10
		}

		// Fixed columns: SQLID(9) + Locks(10) + Avg Wait(15) + Total Wait(15) = 49
		// Spacing: 4 fixed columns * 2 spaces = 8
		fixedWidth := 49
		spacingWidth := 8
		queryWidth := tableWidth - fixedWidth - spacingWidth
		if queryWidth < 40 {
			queryWidth = 40
		}

		fmt.Printf("%s%-9s  %-*s  %10s  %15s  %15s%s\n",
			bold, "SQLID", queryWidth, "Query", "Locks", "Avg Wait", "Total Wait", reset)
		fmt.Println(strings.Repeat("-", tableWidth))

		for i := 0; i < limit; i++ {
			stat := pairs[i].stat
			truncatedQuery := truncateQuery(stat.NormalizedQuery, queryWidth)
			avgWait := stat.StillWaitingTime / float64(stat.StillWaitingCount)
			fmt.Printf("%-9s  %-*s  %10d  %15s  %15s\n",
				stat.ID,
				queryWidth, truncatedQuery,
				stat.StillWaitingCount,
				formatQueryDuration(avgWait),
				formatQueryDuration(stat.StillWaitingTime))
		}
	} else {
		// Compact mode: show type only
		header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s  %-12s\n", "SQLID", "Type", "Locks", "Avg Wait", "Total Wait")
		fmt.Print(bold + header + reset)
		fmt.Println(strings.Repeat("-", 80))

		for i := 0; i < limit; i++ {
			stat := pairs[i].stat
			qType := analysis.QueryTypeFromID(stat.ID)
			avgWait := stat.StillWaitingTime / float64(stat.StillWaitingCount)
			fmt.Printf("%-8s  %-10s  %-10d  %-12s  %-12s\n",
				stat.ID,
				qType,
				stat.StillWaitingCount,
				formatQueryDuration(avgWait),
				formatQueryDuration(stat.StillWaitingTime))
		}
	}
}

// printMostFrequentWaitingQueries prints all queries that experienced lock waits,
// sorted by the number of unique locks that waited (acquired or not).
func printMostFrequentWaitingQueries(queryStats map[string]*analysis.LockQueryStat, limit int, termWidth int) {
	// Convert map to slice and filter/sort by total number of locks that waited
	type queryPair struct {
		stat       *analysis.LockQueryStat
		totalLocks int
		totalWait  float64
	}
	var pairs []queryPair
	for _, stat := range queryStats {
		totalLocks := stat.AcquiredCount + stat.StillWaitingCount
		if totalLocks > 0 {
			totalWait := stat.AcquiredWaitTime + stat.StillWaitingTime
			pairs = append(pairs, queryPair{
				stat:       stat,
				totalLocks: totalLocks,
				totalWait:  totalWait,
			})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		// Sort by total locks (descending), then by ID (ascending) for deterministic ordering
		if pairs[i].totalLocks != pairs[j].totalLocks {
			return pairs[i].totalLocks > pairs[j].totalLocks
		}
		return pairs[i].stat.ID < pairs[j].stat.ID
	})

	// Print top queries
	if limit > len(pairs) {
		limit = len(pairs)
	}

	bold := "\033[1m"
	reset := "\033[0m"

	if termWidth >= 120 {
		// Wide mode: show full query
		// Calculate consistent table width (90% of terminal)
		tableWidth := int(float64(termWidth) * 0.9)
		if tableWidth > termWidth-10 {
			tableWidth = termWidth - 10
		}

		// Fixed columns: SQLID(9) + Locks(10) + Avg Wait(15) + Total Wait(15) = 49
		// Spacing: 4 fixed columns * 2 spaces = 8
		fixedWidth := 49
		spacingWidth := 8
		queryWidth := tableWidth - fixedWidth - spacingWidth
		if queryWidth < 40 {
			queryWidth = 40
		}

		fmt.Printf("%s%-9s  %-*s  %10s  %15s  %15s%s\n",
			bold, "SQLID", queryWidth, "Query", "Locks", "Avg Wait", "Total Wait", reset)
		fmt.Println(strings.Repeat("-", tableWidth))

		for i := 0; i < limit; i++ {
			pair := pairs[i]
			truncatedQuery := truncateQuery(pair.stat.NormalizedQuery, queryWidth)
			avgWait := pair.totalWait / float64(pair.totalLocks)
			fmt.Printf("%-9s  %-*s  %10d  %15s  %15s\n",
				pair.stat.ID,
				queryWidth, truncatedQuery,
				pair.totalLocks,
				formatQueryDuration(avgWait),
				formatQueryDuration(pair.totalWait))
		}
	} else {
		// Compact mode: show type only
		header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s  %-12s\n", "SQLID", "Type", "Locks", "Avg Wait", "Total Wait")
		fmt.Print(bold + header + reset)
		fmt.Println(strings.Repeat("-", 80))

		for i := 0; i < limit; i++ {
			pair := pairs[i]
			qType := analysis.QueryTypeFromID(pair.stat.ID)
			avgWait := pair.totalWait / float64(pair.totalLocks)
			fmt.Printf("%-8s  %-10s  %-10d  %-12s  %-12s\n",
				pair.stat.ID,
				qType,
				pair.totalLocks,
				formatQueryDuration(avgWait),
				formatQueryDuration(pair.totalWait))
		}
	}
}
