package output

import (
	"dalibo/quellog/analysis"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"
)

// TextFormatter formats the report in plain text.
type TextFormatter struct{}

// PrintMetrics displays the aggregated metrics.
func PrintMetrics(m analysis.Metrics) {
	// Calculate total duration from min and max timestamps.
	duration := m.MaxTimestamp.Sub(m.MinTimestamp)

	// ANSI style for bold text.
	bold := "\033[1m"
	reset := "\033[0m"

	// General summary header.
	fmt.Println(bold + "\nSUMMARY" + reset)
	fmt.Printf("\n  %-25s : %s\n", "Start date", m.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "End date", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "Duration", duration)
	fmt.Printf("  %-25s : %d\n", "Total entries", m.Count)
	if duration > 0 {
		rate := float64(m.Count) / duration.Seconds()
		fmt.Printf("  %-25s : %.2f entries/s\n", "Throughput", rate)
	}
	fmt.Printf("  %-25s : %d\n", "Entries with 'ERROR'", m.ErrorCount)
	fmt.Printf("  %-25s : %d\n", "Entries with 'FATAL'", m.FatalCount)
	fmt.Printf("  %-25s : %d\n", "Entries with 'PANIC'", m.PanicCount)
	fmt.Printf("  %-25s : %d\n", "Entries with 'WARNING'", m.WarningCount)
	fmt.Printf("  %-25s : %d\n", "Log entries ('LOG:')", m.LogCount)

	// Temp Files section.
	fmt.Println(bold + "\nTemp files" + reset)
	fmt.Printf("  %-25s : %d\n", "Temp file messages", m.TempFileCount)
	fmt.Printf("  %-25s : %s\n", "Cumulative temp file size", formatBytes(m.TempFileTotalSize))
	avgSize := int64(0)
	if m.TempFileCount > 0 {
		avgSize = m.TempFileTotalSize / int64(m.TempFileCount)
	}
	fmt.Printf("  %-25s : %s\n", "Average temp file size", formatBytes(avgSize))

	// Maintenance Metrics section.
	fmt.Println(bold + "\nMaintenance" + reset)
	fmt.Printf("  %-25s : %d\n", "Automatic vacuum count", m.VacuumCount)
	fmt.Printf("  %-25s : %d\n", "Automatic analyze count", m.AnalyzeCount)
	fmt.Println("  Top automatic vacuum operations per table:")
	printTopTables(m.VacuumTableCounts, m.VacuumCount, m.VacuumSpaceRecovered)
	fmt.Println("  Top automatic analyze operations per table:")
	printTopTables(m.AnalyzeTableCounts, m.AnalyzeCount, nil)

	// Checkpoints section (if available).
	if m.CheckpointCompleteCount > 0 {
		avgWriteSeconds := m.TotalCheckpointWriteSeconds / float64(m.CheckpointCompleteCount)
		avgDuration := time.Duration(avgWriteSeconds * float64(time.Second)).Truncate(time.Second)
		fmt.Println(bold + "\nCHECKPOINTS" + reset)
		fmt.Printf("  %-25s : %d\n", "Checkpoint count", m.CheckpointCompleteCount)
		fmt.Printf("  %-25s : %s\n", "Avg checkpoint write time", avgDuration)
	}

	// Connections & Sessions Metrics section.
	fmt.Println(bold + "\nConnections & Sessions" + reset)
	fmt.Printf("  %-25s : %d\n", "Connection count", m.ConnectionReceivedCount)
	if duration.Hours() > 0 {
		avgConnPerHour := float64(m.ConnectionReceivedCount) / duration.Hours()
		fmt.Printf("  %-25s : %d\n", "Avg connections per hour", int(avgConnPerHour))
	}
	fmt.Printf("  %-25s : %d\n", "Disconnection count", m.DisconnectionCount)
	if m.DisconnectionCount > 0 {
		avgSessionTime := m.TotalSessionTime / time.Duration(m.DisconnectionCount)
		fmt.Printf("  %-25s : %s\n", "Avg session time", avgSessionTime.Truncate(time.Second))
	}

	// Unique Clients section.
	fmt.Println(bold + "\nCLIENTS" + reset)
	fmt.Printf("  %-25s : %d\n", "Unique DBs", m.UniqueDbs)
	fmt.Printf("  %-25s : %d\n", "Unique Users", m.UniqueUsers)
	fmt.Printf("  %-25s : %d\n", "Unique Apps", m.UniqueApps)

	// Display lists.
	fmt.Println(bold + "\nDATABASES" + reset)
	for _, db := range m.DBs {
		fmt.Printf("    %s\n", db)
	}
	fmt.Println(bold + "\nUSERS" + reset)
	for _, user := range m.Users {
		fmt.Printf("    %s\n", user)
	}
	fmt.Println(bold + "\nAPPS" + reset)
	for _, app := range m.Apps {
		fmt.Printf("    %s\n", app)
	}
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

	// Sort by count in descending order.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Count > pairs[j].Count
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
func PrintSQLSummary(m analysis.SqlMetrics) {
	// Get terminal width, defaulting to 80.
	width := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width = w
	}
	// Allocate approximately 60% of the terminal width for the query.
	queryWidth := int(float64(width) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	// ANSI styles.
	bold := "\033[1m"
	reset := "\033[0m"

	// Calculate total duration and average load.
	totalDuration := m.EndTimestamp.Sub(m.StartTimestamp)
	avgLoad := float64(m.TotalQueries) / totalDuration.Seconds()

	// General header.
	fmt.Println("Total log duration: ", formatQueryDuration(float64(totalDuration.Milliseconds())))
	fmt.Println(bold + "\nSQL PERFORMANCE REPORT" + reset)
	fmt.Println()
	fmt.Printf("  %-25s : %-20s  %-25s : %s\n",
		"Total query duration", formatQueryDuration(m.SumQueryDuration),
		"Query max duration", formatQueryDuration(m.MaxQueryDuration))
	fmt.Printf("  %-25s : %-20d  %-25s : %s\n",
		"Total query parsed", m.TotalQueries,
		"Query min duration", formatQueryDuration(m.MinQueryDuration))
	fmt.Printf("  %-25s : %-20d  %-25s : %s\n",
		"Total individual query", m.UniqueQueries,
		"Query median duration", formatQueryDuration(m.MedianQueryDuration))
	fmt.Printf("  %-25s : %-20s  %-25s : %s\n",
		"Average load", formatAverageLoad(avgLoad),
		"Query 99th percentile", formatQueryDuration(m.P99QueryDuration))
	fmt.Println()

	// Display various SQL query reports.
	fmt.Println(bold + "Slowest individual queries:" + reset)
	PrintSlowestQueries(m.QueryStats)
	fmt.Println()

	fmt.Println(bold + "Most Frequent Individual Queries:" + reset)
	PrintMostFrequentQueries(m.QueryStats)
	fmt.Println()

	fmt.Println(bold + "Most time consuming queries:" + reset)
	PrintTimeConsumingQueries(m.QueryStats)
	fmt.Println()
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

	sort.Slice(queries, func(i, j int) bool { return queries[i].TotalTime > queries[j].TotalTime })

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
		return queries[i].MaxTime > queries[j].MaxTime
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
		return queries[i].Count > queries[j].Count
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

	var maxCount int
	var prevCount int
	for i, q := range queries {
		if i == 0 {
			maxCount = q.Count
			prevCount = q.Count
		} else {
			if q.Count == 1 {
				break
			}
			if q.Count < prevCount/10 {
				break
			}
			if q.Count <= (maxCount/2)-2 {
				break
			}
			if i == 15 {
				break
			}
			prevCount = q.Count
		}

		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Printf("%-9s  %-*s  %12d\n",
			q.ID,
			queryWidth, displayQuery,
			q.Count)
	}
}

// PrintSqlDetails iterates over the QueryStats and displays details for each query
// whose SQLID matches one of the provided queryDetails.
func PrintSqlDetails(m analysis.SqlMetrics, queryDetails []string) {
	for _, qs := range m.QueryStats {
		for _, qid := range queryDetails {
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
			}
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

func formatAverageLoad(load float64) string {
	if load >= 1.0 {
		return fmt.Sprintf("%.2f queries/s", load)
	}
	perMin := load * 60.0
	if perMin >= 1.0 {
		return fmt.Sprintf("%.0f queries/min", perMin)
	}
	perHour := load * 3600.0
	return fmt.Sprintf("%.0f queries/h", perHour)
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

// FormatEventSummary returns a formatted string representing the event summary as an elegant table.
// It includes a merged, centered bold title row and a TOTAL row at the end.
func (tf *TextFormatter) FormatEventSummary(summaries []analysis.EventSummary) string {
	// Define column headers.
	headers := []string{"Type", "Count", "Percentage"}

	// Determine maximum widths for each column.
	widthType := len(headers[0])
	widthCount := len(headers[1])
	widthPercentage := len(headers[2])
	for _, summary := range summaries {
		if len(summary.Type) > widthType {
			widthType = len(summary.Type)
		}
		countStr := fmt.Sprintf("%d", summary.Count)
		if len(countStr) > widthCount {
			widthCount = len(countStr)
		}
		percStr := fmt.Sprintf("%.2f%%", summary.Percentage)
		if len(percStr) > widthPercentage {
			widthPercentage = len(percStr)
		}
	}

	totalTableWidth := (widthType + 2) + (widthCount + 2) + (widthPercentage + 2) + 4

	topLine := fmt.Sprintf("┌%s┐", strings.Repeat("─", totalTableWidth-2))
	mergedTitleBorder := fmt.Sprintf("├%s┤", strings.Repeat("─", totalTableWidth-2))
	upperBottomLine := fmt.Sprintf("├%s┘", strings.Repeat("─", totalTableWidth-2))

	headerSep := fmt.Sprintf("├%s┼%s┼%s┤",
		strings.Repeat("─", widthType+2),
		strings.Repeat("─", widthCount+2),
		strings.Repeat("─", widthPercentage+2),
	)

	// Build the merged title row.
	titleText := "EVENTS SUMMARY"
	boldTitle := "\033[1m" + titleText + "\033[0m"
	availWidth := totalTableWidth - 2
	padTotal := availWidth - len(titleText)
	if padTotal < 0 {
		padTotal = 0
	}
	leftPad := padTotal / 2
	rightPad := padTotal - leftPad
	titleRow := fmt.Sprintf("│%s%s%s│", strings.Repeat(" ", leftPad), boldTitle, strings.Repeat(" ", rightPad))

	// Build the header row.
	headerRow := fmt.Sprintf("│ %-*s │ %-*s │ %-*s │",
		widthType, headers[0],
		widthCount, headers[1],
		widthPercentage, headers[2],
	)

	var dataRows strings.Builder
	for _, summary := range summaries {
		dataRows.WriteString(fmt.Sprintf("│ %-*s │ %*d │ %*s │\n",
			widthType, summary.Type,
			widthCount, summary.Count,
			widthPercentage, fmt.Sprintf("%.2f%%", summary.Percentage),
		))
	}

	totalCount := 0
	for _, summary := range summaries {
		totalCount += summary.Count
	}

	totalCountStr := fmt.Sprintf("%d", totalCount)
	rightCellWidth := widthCount
	if len(totalCountStr) > rightCellWidth {
		rightCellWidth = len(totalCountStr)
	}
	totalTwoColWidth := (widthType + 2) + (rightCellWidth + 2) + 1

	totalRow := fmt.Sprintf("│ \033[1m%-*s\033[0m │ \033[1m%*s\033[0m │",
		widthType, "TOTAL",
		rightCellWidth, totalCountStr,
	)

	finalLine := fmt.Sprintf("└%s┘", strings.Repeat("─", totalTwoColWidth))

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(topLine + "\n")
	sb.WriteString(titleRow + "\n")
	sb.WriteString(mergedTitleBorder + "\n")
	sb.WriteString(headerRow + "\n")
	sb.WriteString(headerSep + "\n")
	sb.WriteString(dataRows.String())
	sb.WriteString(upperBottomLine + "\n")
	sb.WriteString(totalRow + "\n")
	sb.WriteString(finalLine)
	sb.WriteString("\n")

	return sb.String()
}
