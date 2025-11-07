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

		// Queries generating temp files (if available)
		if len(m.TempFiles.QueryStats) > 0 {
			fmt.Println("\n" + bold + "Queries generating temp files:" + reset + "\n")
			fmt.Printf("%s%-10s %-70s %10s %10s%s\n", bold, "SQLID", "Query", "Count", "Total Size", reset)
			fmt.Println(strings.Repeat("-", 103))

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
			for i := 0; i < limit; i++ {
				stat := queries[i].stat
				truncatedQuery := truncateQuery(stat.NormalizedQuery, 70)
				fmt.Printf("%-10s %-70s %10d %10s\n",
					stat.ID,
					truncatedQuery,
					stat.Count,
					formatBytes(stat.TotalSize))
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

		// Acquired locks by query
		if len(m.Locks.QueryStats) > 0 {
			hasAcquired := false
			for _, stat := range m.Locks.QueryStats {
				if stat.AcquiredCount > 0 {
					hasAcquired = true
					break
				}
			}
			if hasAcquired {
				fmt.Println("\n" + bold + "Acquired locks by query:" + reset + "\n")
				fmt.Printf("%-10s %-70s %10s %15s %15s\n", "SQLID", "Query", "Locks", "Avg Wait", "Total Wait")
				fmt.Println(strings.Repeat("-", 124))
				printAcquiredLockQueries(m.Locks.QueryStats, 10)
			}
		}

		// Locks still waiting by query
		if len(m.Locks.QueryStats) > 0 {
			hasStillWaiting := false
			for _, stat := range m.Locks.QueryStats {
				if stat.StillWaitingCount > 0 {
					hasStillWaiting = true
					break
				}
			}
			if hasStillWaiting {
				fmt.Println("\n" + bold + "Locks still waiting by query:" + reset + "\n")
				fmt.Printf("%-10s %-70s %10s %15s %15s\n", "SQLID", "Query", "Locks", "Avg Wait", "Total Wait")
				fmt.Println(strings.Repeat("-", 124))
				printStillWaitingLockQueries(m.Locks.QueryStats, 10)
			}
		}

		// Most frequent waiting queries (all locks that waited, acquired or not)
		if len(m.Locks.QueryStats) > 0 {
			hasWaiting := false
			for _, stat := range m.Locks.QueryStats {
				if stat.AcquiredCount > 0 || stat.StillWaitingCount > 0 {
					hasWaiting = true
					break
				}
			}
			if hasWaiting {
				fmt.Println("\n" + bold + "Most frequent waiting queries:" + reset + "\n")
				fmt.Printf("%-10s %-70s %10s %15s %15s\n", "SQLID", "Query", "Locks", "Avg Wait", "Total Wait")
				fmt.Println(strings.Repeat("-", 124))
				printMostFrequentWaitingQueries(m.Locks.QueryStats, 10)
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
	if has("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0) {
		fmt.Println(bold + "\nCLIENTS\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Unique DBs", m.UniqueEntities.UniqueDbs)
		fmt.Printf("  %-25s : %d\n", "Unique Users", m.UniqueEntities.UniqueUsers)
		fmt.Printf("  %-25s : %d\n", "Unique Apps", m.UniqueEntities.UniqueApps)

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

		fmt.Println(bold + "Slowest individual queries:" + reset)
		PrintSlowestQueries(m.QueryStats)
		fmt.Println()

		fmt.Println(bold + "Most Frequent Individual Queries:" + reset)
		PrintMostFrequentQueries(m.QueryStats)
		fmt.Println()

		fmt.Println(bold + "Most time consuming queries:" + reset)
		PrintTimeConsumingQueries(m.QueryStats)
		fmt.Println()

		// Display note about queries without duration metrics
		if m.QueriesWithoutDurationCount.Total > 0 {
			fmt.Println(strings.Repeat("─", 70))
			fmt.Printf("Note: %d queries without duration metrics were identified:\n", m.QueriesWithoutDurationCount.Total)
			if m.QueriesWithoutDurationCount.FromLocks > 0 {
				fmt.Printf("  • %d from lock events\n", m.QueriesWithoutDurationCount.FromLocks)
			}
			if m.QueriesWithoutDurationCount.FromTempfiles > 0 {
				fmt.Printf("  • %d from tempfile events\n", m.QueriesWithoutDurationCount.FromTempfiles)
			}
			fmt.Println()
		}
	}
}

// PrintTimeConsumingQueries sorts and displays the top 10 queries based on total execution time.
// The display adapts to the terminal width, switching between full and simplified modes.
func PrintTimeConsumingQueries(queryStats map[string]*analysis.QueryStat) {
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 120
	}

	type queryInfo struct {
		Query     string // Normalized query.
		ID        string // Friendly identifier.
		Count     int
		TotalTime float64 // in ms.
		AvgTime   float64 // in ms.
		MaxTime   float64 // in ms.
	}
	var queries []queryInfo
	for normalized, stats := range queryStats {
		id, _ := analysis.GenerateQueryID(stats.RawQuery, normalized)
		queries = append(queries, queryInfo{
			Query:     normalized,
			ID:        id,
			Count:     stats.Count,
			TotalTime: stats.TotalTime,
			AvgTime:   stats.AvgTime,
			MaxTime:   stats.MaxTime,
		})
	}

	sort.Slice(queries, func(i, j int) bool {
		if queries[i].TotalTime != queries[j].TotalTime {
			return queries[i].TotalTime > queries[j].TotalTime
		}
		return queries[i].Query < queries[j].Query
	})

	bold := "\033[1m"
	reset := "\033[0m"

	if termWidth >= 120 {
		queryWidth := int(float64(termWidth) * 0.6)
		if queryWidth < 40 {
			queryWidth = 40
		}

		fmt.Printf("%s%-9s  %-*s  %10s  %12s  %12s  %12s%s\n",
			bold, "SQLID", queryWidth, "Query", "Executed", "Max", "Avg", "Total", reset)
		totalWidth := 9 + 2 + queryWidth + 2 + 10 + 2 + 12 + 2 + 12 + 2 + 12
		fmt.Println(strings.Repeat("-", totalWidth))

		for i, q := range queries {
			if i >= 10 {
				break
			}
			displayQuery := truncateQuery(q.Query, queryWidth)
			fmt.Printf("%-8s  %-*s  %10d  %12s  %12s  %12s\n",
				q.ID,
				queryWidth, displayQuery,
				q.Count,
				formatQueryDuration(q.MaxTime),
				formatQueryDuration(q.AvgTime),
				formatQueryDuration(q.TotalTime))
		}
	} else {
		header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s  %-12s  %-12s\n", "SQLID", "Type", "Executed", "Max", "Avg", "Total")
		fmt.Print(bold + header + reset)
		fmt.Println(strings.Repeat("-", 80))
		for i, q := range queries {
			if i >= 10 {
				break
			}
			qType := analysis.QueryTypeFromID(q.ID)
			fmt.Printf("%-8s  %-10s  %-10d  %-12s  %-12s  %-12s\n",
				q.ID,
				qType,
				q.Count,
				formatQueryDuration(q.MaxTime),
				formatQueryDuration(q.AvgTime),
				formatQueryDuration(q.TotalTime))
		}
	}
}

// PrintSlowestQueries displays the top 10 slowest individual queries,
// showing three columns: SQLID, truncated Query, and Duration.
func PrintSlowestQueries(queryStats map[string]*analysis.QueryStat) {
	type queryInfo struct {
		ID      string
		Query   string
		MaxTime float64
	}
	var queries []queryInfo
	for normalized, stats := range queryStats {
		id, _ := analysis.GenerateQueryID(stats.RawQuery, normalized)
		queries = append(queries, queryInfo{
			ID:      id,
			Query:   normalized,
			MaxTime: stats.MaxTime,
		})
	}

	sort.Slice(queries, func(i, j int) bool {
		if queries[i].MaxTime != queries[j].MaxTime {
			return queries[i].MaxTime > queries[j].MaxTime
		}
		return queries[i].Query < queries[j].Query
	})

	if len(queries) > 10 {
		queries = queries[:10]
	}

	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 120
	}
	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	bold := "\033[1m"
	reset := "\033[0m"

	headerFormat := fmt.Sprintf("%%-9s  %%-%ds  %%12s\n", queryWidth)
	fmt.Printf("%s"+headerFormat+reset, bold, "SQLID", "Query", "Duration")
	totalWidth := 9 + 2 + queryWidth + 2 + 12
	fmt.Println(strings.Repeat("-", totalWidth))

	for _, q := range queries {
		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Printf("%-9s  %-*s  %12s\n",
			q.ID,
			queryWidth, displayQuery,
			formatQueryDuration(q.MaxTime))
	}
}

// PrintMostFrequentQueries displays the top queries by frequency (sorted descending by count).
// The display stops if a query was executed only once or if the execution count drops by more than a factor of 10.
func PrintMostFrequentQueries(queryStats map[string]*analysis.QueryStat) {
	type queryInfo struct {
		ID    string
		Query string
		Count int
	}
	var queries []queryInfo
	for normalized, stats := range queryStats {
		id, _ := analysis.GenerateQueryID(stats.RawQuery, normalized)
		queries = append(queries, queryInfo{
			ID:    id,
			Query: normalized,
			Count: stats.Count,
		})
	}

	sort.Slice(queries, func(i, j int) bool {
		if queries[i].Count != queries[j].Count {
			return queries[i].Count > queries[j].Count
		}
		return queries[i].Query < queries[j].Query
	})

	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 120
	}
	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	bold := "\033[1m"
	reset := "\033[0m"

	headerFormat := fmt.Sprintf("%%-9s  %%-%ds  %%12s\n", queryWidth)
	fmt.Printf("%s"+headerFormat+reset, bold, "SQLID", "Query", "Executed")
	totalWidth := 9 + 2 + queryWidth + 2 + 12
	fmt.Println(strings.Repeat("-", totalWidth))

	var prevCount int
	for i, q := range queries {
		// Stop conditions
		if i >= 15 {
			break // Max 15 queries
		}
		if q.Count == 1 {
			break // Don't show queries executed only once
		}
		if i > 0 && q.Count < prevCount/10 {
			break // Stop if frequency drops by 10x
		}

		prevCount = q.Count

		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Printf("%-9s  %-*s  %12d\n",
			q.ID,
			queryWidth, displayQuery,
			q.Count)
	}
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
func computeQueryLoadHistogram(m analysis.SqlMetrics) (map[string]int, string, int) {
	if m.StartTimestamp.IsZero() || m.EndTimestamp.IsZero() || len(m.Executions) == 0 {
		return nil, "", 0
	}

	// Division de l'intervalle total en 6 buckets égaux.
	totalDuration := m.EndTimestamp.Sub(m.StartTimestamp)
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Protection contre la division par zéro : si la durée totale est nulle ou très courte
	if bucketDuration <= 0 {
		bucketDuration = 1 * time.Nanosecond
	}

	// Préparation des buckets avec accumulation en millisecondes.
	histogramMs := make([]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		start := m.StartTimestamp.Add(time.Duration(i) * bucketDuration)
		end := m.StartTimestamp.Add(time.Duration(i+1) * bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", start.Format("15:04"), end.Format("15:04"))
	}

	// Répartition des durées (en ms) dans les buckets en fonction du timestamp de chaque exécution.
	for _, exec := range m.Executions {
		elapsed := exec.Timestamp.Sub(m.StartTimestamp)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		histogramMs[bucketIndex] += int(exec.Duration)
	}

	// Détermination de l'unité et du facteur de conversion en fonction de la charge maximale.
	maxBucketLoad := 0
	for _, load := range histogramMs {
		if load > maxBucketLoad {
			maxBucketLoad = load
		}
	}

	var unit string
	var conversion int
	if maxBucketLoad < 1000 {
		unit = "ms"
		conversion = 1
	} else if maxBucketLoad < 60000 {
		unit = "s"
		conversion = 1000
	} else {
		unit = "m"
		conversion = 60000
	}

	// Conversion et arrondi vers le haut pour que toute charge non nulle soit affichée au moins 1 unité.
	histogram := make(map[string]int, numBuckets)
	for i, load := range histogramMs {
		var value int
		if load > 0 {
			value = (load + conversion - 1) / conversion
		} else {
			value = 0
		}
		histogram[bucketLabels[i]] = value
	}

	// Calcul automatique du scale factor pour l'affichage.
	maxValue := 0
	for _, v := range histogram {
		if v > maxValue {
			maxValue = v
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, unit, scaleFactor
}

// computeQueryDurationHistogram renvoie un histogramme sous forme de map associant des étiquettes de buckets à leur nombre de requêtes.
// computeQueryDurationHistogram calcule un histogramme basé sur la durée des requêtes (exprimée en millisecondes)
// et retourne :
// - une map associant une étiquette de bucket au nombre de requêtes
// - une chaîne "req" indiquant que les valeurs sont en nombre de requêtes,
// - un scaleFactor permettant d’afficher des barres proportionnelles sur une largeur maximale de 40 caractères.
func computeQueryDurationHistogram(m analysis.SqlMetrics) (map[string]int, string, int) {
	// Définition des buckets fixes dans l'ordre souhaité.
	bucketDefinitions := []struct {
		label string
		lower float64 // Borne inférieure en ms (inclusive)
		upper float64 // Borne supérieure en ms (exclusive)
	}{
		{"< 1 ms", 0, 1},
		{"< 10 ms", 1, 10},
		{"< 100 ms", 10, 100},
		{"< 1 s", 100, 1000},
		{"< 10 s", 1000, 10000},
		{">= 10 s", 10000, math.Inf(1)},
	}

	// Initialisation de l'histogramme avec des valeurs à zéro pour garantir l'ordre d'affichage.
	histogram := make(map[string]int)
	for _, bucket := range bucketDefinitions {
		histogram[bucket.label] = 0
	}

	// Parcours des requêtes et répartition dans les buckets.
	for _, exec := range m.Executions {
		d := exec.Duration
		for _, bucket := range bucketDefinitions {
			if d >= bucket.lower && d < bucket.upper {
				histogram[bucket.label]++
				break
			}
		}
	}

	// Détermination du nombre maximal de requêtes dans un bucket.
	maxCount := 0
	for _, count := range histogram {
		if count > maxCount {
			maxCount = count
		}
	}

	// L'unité est "req" (nombre de requêtes).
	unit := "req"

	// Calcul du scale factor pour limiter la barre la plus longue à 40 caractères.
	scaleFactor := int(math.Ceil(float64(maxCount) / 40.0))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, unit, scaleFactor
}

func computeTempFileHistogram(m analysis.TempFileMetrics) (map[string]int, string, int) {
	// Si aucun événement, on ne peut pas construire l'histogramme.
	if len(m.Events) == 0 {
		return nil, "", 0
	}

	// Déterminer le début et la fin de la période à partir des événements.
	start := m.Events[0].Timestamp
	end := m.Events[0].Timestamp
	for _, event := range m.Events {
		if event.Timestamp.Before(start) {
			start = event.Timestamp
		}
		if event.Timestamp.After(end) {
			end = event.Timestamp
		}
	}
	totalDuration := end.Sub(start)

	// Si tous les événements ont le même timestamp (ou très proche),
	// on ne peut pas créer un histogramme temporel utile.
	// On met tout dans un seul bucket.
	if totalDuration < time.Second {
		totalDuration = time.Second
	}

	// On divise l'intervalle en 6 buckets égaux.
	numBuckets := 6
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Préparation des buckets.
	histogramSizes := make([]float64, numBuckets) // cumul des tailles en octets pour chaque bucket
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		bucketLabels[i] = fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
		histogramSizes[i] = 0
	}

	// Répartition des événements dans les buckets.
	for _, event := range m.Events {
		elapsed := event.Timestamp.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogramSizes[bucketIndex] += event.Size
	}

	// Détermination de la charge maximale.
	maxBucketLoad := 0.0
	for _, size := range histogramSizes {
		if size > maxBucketLoad {
			maxBucketLoad = size
		}
	}

	// Choix de l'unité et du facteur de conversion en fonction de la charge maximale.
	var unit string
	var conversion float64
	if maxBucketLoad < 1024 {
		unit = "B"
		conversion = 1
	} else if maxBucketLoad < 1024*1024 {
		unit = "KB"
		conversion = 1024
	} else if maxBucketLoad < 1024*1024*1024 {
		unit = "MB"
		conversion = 1024 * 1024
	} else {
		unit = "GB"
		conversion = 1024 * 1024 * 1024
	}

	// Conversion des valeurs en arrondissant vers le haut pour afficher au moins 1 unité.
	histogram := make(map[string]int, numBuckets)
	for i, raw := range histogramSizes {
		var value int
		if raw > 0 {
			value = int((raw + conversion - 1) / conversion)
		} else {
			value = 0
		}
		histogram[bucketLabels[i]] = value
	}

	// Calcul automatique du scale factor pour l'affichage (limite à 40 blocs max).
	histogramWidth := 40
	maxValue := 0
	for _, v := range histogram {
		if v > maxValue {
			maxValue = v
		}
	}

	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, unit, scaleFactor
}

// computeCheckpointHistogram agrège les événements de checkpoints en 6 tranches d'une journée complète.
// On considère que tous les événements se situent dans la même journée.
// Le résultat est un histogramme (map label -> nombre de checkpoints),
// l'unité ("checkpoints") et un scale factor calculé pour limiter la largeur à 35 caractères.
func computeCheckpointHistogram(m analysis.CheckpointMetrics) (map[string]int, string, int) {
	if len(m.Events) == 0 {
		return nil, "", 0
	}

	// On utilise le jour de la première occurrence pour fixer le début de la journée.
	day := m.Events[0]
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	end := start.Add(24 * time.Hour)
	numBuckets := 6
	bucketDuration := end.Sub(start) / time.Duration(numBuckets)

	histogram := make(map[string]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		label := fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
		histogram[label] = 0
		bucketLabels[i] = label
	}

	// Répartition des événements dans les buckets, en se basant sur l'heure de l'événement.
	for _, t := range m.Events {
		// Si l'événement n'est pas dans la journée, on le ramène dans l'intervalle [start, end).
		// On utilise l'heure uniquement.
		hour := t.Hour()
		bucketIndex := hour / 4 // 24h/6 = 4 heures par bucket.
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogram[bucketLabels[bucketIndex]]++
	}

	// Calcul du scale factor pour limiter la barre à 35 caractères.
	maxValue := 0
	for _, count := range histogram {
		if count > maxValue {
			maxValue = count
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	return histogram, "checkpoints", scaleFactor
}

// computeConnectionsHistogram agrège les timestamps d'événements dans un histogramme réparti sur numBuckets.
// Les buckets sont calculés sur la journée complète (00:00 - 24:00) basée sur la première occurrence.
func computeConnectionsHistogram(events []time.Time) (map[string]int, string, int) {
	if len(events) == 0 {
		return nil, "", 0
	}

	// Déterminer le début et la fin de la période à partir des événements.
	start := events[0]
	end := events[0]
	for _, t := range events {
		if t.Before(start) {
			start = t
		}
		if t.After(end) {
			end = t
		}
	}

	// On fixe le nombre de buckets à 6.
	numBuckets := 6
	totalDuration := end.Sub(start)
	if totalDuration <= 0 {
		totalDuration = time.Second // Durée minimale pour éviter la division par zéro.
	}
	bucketDuration := totalDuration / time.Duration(numBuckets)

	// Création des buckets avec leurs labels.
	histogram := make(map[string]int, numBuckets)
	bucketLabels := make([]string, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		label := fmt.Sprintf("%s - %s", bucketStart.Format("15:04"), bucketEnd.Format("15:04"))
		histogram[label] = 0
		bucketLabels[i] = label
	}

	// Répartition des événements dans les buckets.
	for _, t := range events {
		// On s'assure que l'événement se situe dans l'intervalle [start, end].
		if t.Before(start) || t.After(end) {
			continue
		}
		elapsed := t.Sub(start)
		bucketIndex := int(elapsed / bucketDuration)
		if bucketIndex >= numBuckets {
			bucketIndex = numBuckets - 1
		}
		histogram[bucketLabels[bucketIndex]]++
	}

	// Calcul du scale factor pour limiter la largeur de la barre à 35 caractères.
	maxValue := 0
	for _, count := range histogram {
		if count > maxValue {
			maxValue = count
		}
	}
	histogramWidth := 40
	scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
	if scaleFactor < 1 {
		scaleFactor = 1
	}

	// L'unité ici est "connections".
	return histogram, "connections", scaleFactor
}

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
func printAcquiredLockQueries(queryStats map[string]*analysis.LockQueryStat, limit int) {
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
	for i := 0; i < limit; i++ {
		stat := pairs[i].stat
		truncatedQuery := truncateQuery(stat.NormalizedQuery, 70)
		avgWait := stat.AcquiredWaitTime / float64(stat.AcquiredCount)
		fmt.Printf("%-10s %-70s %10d %15s %15s\n",
			stat.ID,
			truncatedQuery,
			stat.AcquiredCount,
			formatQueryDuration(avgWait),
			formatQueryDuration(stat.AcquiredWaitTime))
	}
}

// printStillWaitingLockQueries prints queries with locks still waiting, sorted by total wait time.
func printStillWaitingLockQueries(queryStats map[string]*analysis.LockQueryStat, limit int) {
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
	for i := 0; i < limit; i++ {
		stat := pairs[i].stat
		truncatedQuery := truncateQuery(stat.NormalizedQuery, 70)
		avgWait := stat.StillWaitingTime / float64(stat.StillWaitingCount)
		fmt.Printf("%-10s %-70s %10d %15s %15s\n",
			stat.ID,
			truncatedQuery,
			stat.StillWaitingCount,
			formatQueryDuration(avgWait),
			formatQueryDuration(stat.StillWaitingTime))
	}
}

// printMostFrequentWaitingQueries prints all queries that experienced lock waits,
// sorted by the number of unique locks that waited (acquired or not).
func printMostFrequentWaitingQueries(queryStats map[string]*analysis.LockQueryStat, limit int) {
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
		return pairs[i].totalLocks > pairs[j].totalLocks
	})

	// Print top queries
	if limit > len(pairs) {
		limit = len(pairs)
	}
	for i := 0; i < limit; i++ {
		pair := pairs[i]
		truncatedQuery := truncateQuery(pair.stat.NormalizedQuery, 70)
		avgWait := pair.totalWait / float64(pair.totalLocks)
		fmt.Printf("%-10s %-70s %10d %15s %15s\n",
			pair.stat.ID,
			truncatedQuery,
			pair.totalLocks,
			formatQueryDuration(avgWait),
			formatQueryDuration(pair.totalWait))
	}
}
