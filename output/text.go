package output

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"

	"golang.org/x/term"
)

type textPrinter struct {
	out     io.Writer
	width   int
	boldOn  string
	boldOff string
}

func newStdoutPrinter() textPrinter {
	return newTextPrinter(os.Stdout)
}

func newTextPrinter(w io.Writer) textPrinter {
	pr := textPrinter{
		out:   w,
		width: 80,
	}

	if file, ok := w.(*os.File); ok {
		if term.IsTerminal(int(file.Fd())) {
			pr.boldOn = "\033[1m"
			pr.boldOff = "\033[0m"
		}

		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 {
			pr.width = width
		}
	}

	return pr
}

func (p textPrinter) bold(text string) string {
	if p.boldOn == "" {
		return text
	}
	return p.boldOn + text + p.boldOff
}

type sectionFilter struct {
	all   bool
	names map[string]struct{}
}

func newSectionFilter(sections []string) sectionFilter {
	filter := sectionFilter{
		names: make(map[string]struct{}, len(sections)),
	}

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		if section == "all" {
			filter.all = true
			return filter
		}
		filter.names[section] = struct{}{}
	}

	return filter
}

func (f sectionFilter) includes(name string) bool {
	if f.all {
		return true
	}
	_, ok := f.names[name]
	return ok
}

// TextFormatter formats the report in plain text.
type TextFormatter struct{}

// PrintMetrics displays the aggregated metrics.
func PrintMetrics(m analysis.AggregatedMetrics, sections []string) {
	pr := newStdoutPrinter()
	renderMetrics(pr, m, sections)
}

func renderMetrics(pr textPrinter, m analysis.AggregatedMetrics, sections []string) {
	filter := newSectionFilter(sections)
	duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)

	if filter.includes("summary") {
		printSummarySection(pr, m, duration)
	}

	if filter.includes("sql_performance") && m.SQL.TotalQueries > 0 {
		renderSQLSummary(pr, m.SQL, true)
	}

	if filter.includes("events") && len(m.EventSummaries) > 0 {
		renderEventsReport(pr, m.EventSummaries)
	}

	if filter.includes("tempfiles") && m.TempFiles.Count > 0 {
		printTempFilesSection(pr, m)
	}

	if filter.includes("maintenance") && (m.Vacuum.VacuumCount > 0 || m.Vacuum.AnalyzeCount > 0) {
		printMaintenanceSection(pr, m)
	}

	if filter.includes("checkpoints") && m.Checkpoints.CompleteCount > 0 {
		printCheckpointsSection(pr, m, duration)
	}

	if filter.includes("connections") && m.Connections.ConnectionReceivedCount > 0 {
		printConnectionsSection(pr, m, duration)
	}

	if filter.includes("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0) {
		printClientsSection(pr, m)
	}

	fmt.Fprintln(pr.out)
}

const timeFormat = "2006-01-02 15:04:05 MST"

func printSummarySection(pr textPrinter, m analysis.AggregatedMetrics, duration time.Duration) {
	fmt.Fprintln(pr.out, pr.bold("\nSUMMARY\n"))
	fmt.Fprintf(pr.out, "  %-25s : %s\n", "Start date", m.Global.MinTimestamp.Format(timeFormat))
	fmt.Fprintf(pr.out, "  %-25s : %s\n", "End date", m.Global.MaxTimestamp.Format(timeFormat))
	fmt.Fprintf(pr.out, "  %-25s : %s\n", "Duration", duration)
	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Total entries", m.Global.Count)
	if duration > 0 {
		rate := float64(m.Global.Count) / duration.Seconds()
		fmt.Fprintf(pr.out, "  %-25s : %.2f entries/s\n", "Throughput", rate)
	}
}

func printTempFilesSection(pr textPrinter, m analysis.AggregatedMetrics) {
	fmt.Fprintln(pr.out, pr.bold("\nTEMP FILES\n"))

	hist, unit, scaleFactor := computeTempFileHistogram(m.TempFiles)
	renderHistogram(pr, hist, "Temp file distribution", unit, scaleFactor, nil)

	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Temp file messages", m.TempFiles.Count)
	fmt.Fprintf(pr.out, "  %-25s : %s\n", "Cumulative temp file size", formatBytes(m.TempFiles.TotalSize))

	avgSize := int64(0)
	if m.TempFiles.Count > 0 {
		avgSize = m.TempFiles.TotalSize / int64(m.TempFiles.Count)
	}
	fmt.Fprintf(pr.out, "  %-25s : %s\n", "Average temp file size", formatBytes(avgSize))
}

func printMaintenanceSection(pr textPrinter, m analysis.AggregatedMetrics) {
	fmt.Fprintln(pr.out, pr.bold("\nMAINTENANCE\n"))
	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Automatic vacuum count", m.Vacuum.VacuumCount)
	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Automatic analyze count", m.Vacuum.AnalyzeCount)

	fmt.Fprintln(pr.out, "  Top automatic vacuum operations per table:")
	printTopTables(pr, m.Vacuum.VacuumTableCounts, m.Vacuum.VacuumCount, m.Vacuum.VacuumSpaceRecovered)

	fmt.Fprintln(pr.out, "  Top automatic analyze operations per table:")
	printTopTables(pr, m.Vacuum.AnalyzeTableCounts, m.Vacuum.AnalyzeCount, nil)
}

func printCheckpointsSection(pr textPrinter, m analysis.AggregatedMetrics, duration time.Duration) {
	avgWriteSeconds := m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)
	avgDuration := time.Duration(avgWriteSeconds * float64(time.Second)).Truncate(time.Second)
	maxDuration := time.Duration(m.Checkpoints.MaxWriteTimeSeconds * float64(time.Second)).Truncate(time.Second)

	fmt.Fprintln(pr.out, pr.bold("\nCHECKPOINTS\n"))

	hist, _, scaleFactor := computeCheckpointHistogram(m.Checkpoints)
	renderHistogram(pr, hist, "Checkpoints", "", scaleFactor, nil)

	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Checkpoint count", m.Checkpoints.CompleteCount)
	fmt.Fprintf(pr.out, "  %-25s : %s\n", "Avg checkpoint write time", avgDuration)
	fmt.Fprintf(pr.out, "  %-25s : %s\n", "Max checkpoint write time", maxDuration)

	if len(m.Checkpoints.TypeCounts) == 0 {
		return
	}

	fmt.Fprintln(pr.out, "  Checkpoint types:")

	type typePair struct {
		Name  string
		Count int
	}

	var pairs []typePair
	for cpType, count := range m.Checkpoints.TypeCounts {
		pairs = append(pairs, typePair{Name: cpType, Count: count})
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		return pairs[i].Name < pairs[j].Name
	})

	durationHours := duration.Hours()

	maxTypeLen := 10
	for _, pair := range pairs {
		if len(pair.Name) > maxTypeLen {
			maxTypeLen = len(pair.Name)
		}
	}

	for _, pair := range pairs {
		percentage := float64(pair.Count) / float64(m.Checkpoints.CompleteCount) * 100
		rate := 0.0
		if durationHours > 0 {
			rate = float64(pair.Count) / durationHours
		}

		fmt.Fprintf(pr.out, "    %-*s  %3d  %5.1f%%  (%.2f/h)\n",
			maxTypeLen, pair.Name, pair.Count, percentage, rate)
	}
}

func printConnectionsSection(pr textPrinter, m analysis.AggregatedMetrics, duration time.Duration) {
	fmt.Fprintln(pr.out, pr.bold("\nCONNECTIONS & SESSIONS\n"))

	hist, _, scaleFactor := computeConnectionsHistogram(m.Connections.Connections)
	renderHistogram(pr, hist, "Connection distribution", "", scaleFactor, nil)

	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Connection count", m.Connections.ConnectionReceivedCount)
	if duration.Hours() > 0 {
		avgConnPerHour := float64(m.Connections.ConnectionReceivedCount) / duration.Hours()
		fmt.Fprintf(pr.out, "  %-25s : %.2f\n", "Avg connections per hour", avgConnPerHour)
	}
	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Disconnection count", m.Connections.DisconnectionCount)
	if m.Connections.DisconnectionCount > 0 {
		avgSessionTime := time.Duration(float64(m.Connections.TotalSessionTime) / float64(m.Connections.DisconnectionCount))
		fmt.Fprintf(pr.out, "  %-25s : %s\n", "Avg session time", avgSessionTime.Round(time.Second))
	} else {
		fmt.Fprintf(pr.out, "  %-25s : %s\n", "Avg session time", "N/A")
	}
}

func printClientsSection(pr textPrinter, m analysis.AggregatedMetrics) {
	fmt.Fprintln(pr.out, pr.bold("\nCLIENTS\n"))
	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Unique DBs", m.UniqueEntities.UniqueDbs)
	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Unique Users", m.UniqueEntities.UniqueUsers)
	fmt.Fprintf(pr.out, "  %-25s : %d\n", "Unique Apps", m.UniqueEntities.UniqueApps)

	if m.UniqueEntities.UniqueUsers > 0 {
		fmt.Fprintln(pr.out, pr.bold("\nUSERS\n"))
		for _, user := range m.UniqueEntities.Users {
			fmt.Fprintf(pr.out, "    %s\n", user)
		}
	}

	if m.UniqueEntities.UniqueApps > 0 {
		fmt.Fprintln(pr.out, pr.bold("\nAPPS\n"))
		for _, app := range m.UniqueEntities.Apps {
			fmt.Fprintf(pr.out, "    %s\n", app)
		}
	}

	if m.UniqueEntities.UniqueDbs > 0 {
		fmt.Fprintln(pr.out, pr.bold("\nDATABASES\n"))
		for _, db := range m.UniqueEntities.DBs {
			fmt.Fprintf(pr.out, "    %s\n", db)
		}
	}
}

// printTopTables prints the top tables for a given operation (vacuum or analyze).
// It stops when the cumulative count reaches at least 80% of the total, unless fewer than 10 tables are available.
func printTopTables(pr textPrinter, tableCounts map[string]int, total int, spaceRecovered map[string]int64) {
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
	if pr.width > 0 {
		maxAllowed := int(float64(pr.width) * 0.4)
		if maxAllowed > 0 && tableLen > maxAllowed {
			tableLen = maxAllowed
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
			fmt.Fprintf(pr.out, "    %-*s %6d %6.2f%%  %12s removed\n",
				tableLen, pair.Name, pair.Count, percentage, formatBytes(pair.Recovered))
		} else {
			fmt.Fprintf(pr.out, "    %-*s %6d %6.2f%%\n",
				tableLen, pair.Name, pair.Count, percentage)
		}

		if cumPercentage >= 80 || n >= 10 {
			break
		}
	}
}

// PrintSQLSummary displays an SQL performance report in the CLI.
// The public wrapper uses stdout; internal rendering accepts any writer.
func PrintSQLSummary(m analysis.SqlMetrics, indicatorsOnly bool) {
	renderSQLSummary(newStdoutPrinter(), m, indicatorsOnly)
}

func renderSQLSummary(pr textPrinter, m analysis.SqlMetrics, indicatorsOnly bool) {
	fmt.Fprintln(pr.out, pr.bold("\nSQL PERFORMANCE\n"))

	top1Slow := 0
	if len(m.Executions) > 0 {
		threshold := m.P99QueryDuration
		for _, exec := range m.Executions {
			if exec.Duration >= threshold {
				top1Slow++
			}
		}
	}

	fmt.Fprintln(pr.out)

	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		queryLoad, unit, scale := computeQueryLoadHistogram(m)
		renderHistogram(pr, queryLoad, "Query load distribution", unit, scale, nil)
	}

	fmt.Fprintf(pr.out, "  %-25s : %-20s\n", "Total query duration", formatQueryDuration(m.SumQueryDuration))
	fmt.Fprintf(pr.out, "  %-25s : %-20d\n", "Total queries parsed", m.TotalQueries)
	fmt.Fprintf(pr.out, "  %-25s : %-20d\n", "Total unique query", m.UniqueQueries)
	fmt.Fprintf(pr.out, "  %-25s : %-20d\n", "Top 1% slow queries", top1Slow)
	fmt.Fprintln(pr.out)
	fmt.Fprintf(pr.out, "  %-25s : %-20s\n", "Query max duration", formatQueryDuration(m.MaxQueryDuration))
	fmt.Fprintf(pr.out, "  %-25s : %-20s\n", "Query min duration", formatQueryDuration(m.MinQueryDuration))
	fmt.Fprintf(pr.out, "  %-25s : %-20s\n", "Query median duration", formatQueryDuration(m.MedianQueryDuration))
	fmt.Fprintf(pr.out, "  %-25s : %-20s\n", "Query 99% max duration", formatQueryDuration(m.P99QueryDuration))
	fmt.Fprintln(pr.out)

	if indicatorsOnly {
		return
	}

	queryDurationOrder := []string{
		"< 1 ms",
		"< 10 ms",
		"< 100 ms",
		"< 1 s",
		"< 10 s",
		">= 10 s",
	}

	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		hist, unit, scale := computeQueryDurationHistogram(m)
		renderHistogram(pr, hist, "Query duration distribution", unit, scale, queryDurationOrder)
	}

	fmt.Fprintln(pr.out, pr.bold("Slowest individual queries:"))
	renderSlowestQueries(pr, m.QueryStats)
	fmt.Fprintln(pr.out)

	fmt.Fprintln(pr.out, pr.bold("Most Frequent Individual Queries:"))
	renderMostFrequentQueries(pr, m.QueryStats)
	fmt.Fprintln(pr.out)

	fmt.Fprintln(pr.out, pr.bold("Most time consuming queries:"))
	renderTimeConsumingQueries(pr, m.QueryStats)
	fmt.Fprintln(pr.out)

	fmt.Fprintln(pr.out, pr.bold("Top Queries by Temporary File Size:"))
	renderTempFileQueries(pr, m.QueryStats)
	fmt.Fprintln(pr.out)
}

// PrintTimeConsumingQueries sorts and displays the top 10 queries based on total execution time.
func PrintTimeConsumingQueries(queryStats map[string]*analysis.QueryStat) {
	renderTimeConsumingQueries(newStdoutPrinter(), queryStats)
}

func renderTimeConsumingQueries(pr textPrinter, queryStats map[string]*analysis.QueryStat) {
	type queryInfo struct {
		Query     string
		ID        string
		Count     int
		TotalTime float64
		AvgTime   float64
		MaxTime   float64
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

	termWidth := pr.width
	if termWidth <= 0 {
		termWidth = 120
	}

	if termWidth >= 120 {
		queryWidth := int(float64(termWidth) * 0.6)
		if queryWidth < 40 {
			queryWidth = 40
		}

		header := fmt.Sprintf("%-9s  %-*s  %10s  %12s  %12s  %12s",
			"SQLID", queryWidth, "Query", "Executed", "Max", "Avg", "Total")
		fmt.Fprintln(pr.out, pr.bold(header))
		totalWidth := 9 + 2 + queryWidth + 2 + 10 + 2 + 12 + 2 + 12 + 2 + 12
		fmt.Fprintln(pr.out, strings.Repeat("-", totalWidth))

		for i, q := range queries {
			if i >= 10 {
				break
			}
			displayQuery := truncateQuery(q.Query, queryWidth)
			fmt.Fprintf(pr.out, "%-9s  %-*s  %10d  %12s  %12s  %12s\n",
				q.ID,
				queryWidth, displayQuery,
				q.Count,
				formatQueryDuration(q.MaxTime),
				formatQueryDuration(q.AvgTime),
				formatQueryDuration(q.TotalTime))
		}
		return
	}

	header := fmt.Sprintf("%-9s  %-10s  %-10s  %-12s  %-12s  %-12s", "SQLID", "Type", "Executed", "Max", "Avg", "Total")
	fmt.Fprintln(pr.out, pr.bold(header))
	fmt.Fprintln(pr.out, strings.Repeat("-", len(header)))
	for i, q := range queries {
		if i >= 10 {
			break
		}
		qType := analysis.QueryTypeFromID(q.ID)
		fmt.Fprintf(pr.out, "%-9s  %-10s  %-10d  %-12s  %-12s  %-12s\n",
			q.ID,
			qType,
			q.Count,
			formatQueryDuration(q.MaxTime),
			formatQueryDuration(q.AvgTime),
			formatQueryDuration(q.TotalTime))
	}
}

func renderTempFileQueries(pr textPrinter, queryStats map[string]*analysis.QueryStat) {
	type queryInfo struct {
		Query         string
		ID            string
		Count         int
		TotalTempSize int64
	}

	var queries []queryInfo
	for normalized, stats := range queryStats {
		if stats.TotalTempSize > 0 {
			id, _ := analysis.GenerateQueryID(stats.RawQuery, normalized)
			queries = append(queries, queryInfo{
				Query:         normalized,
				ID:            id,
				Count:         stats.Count,
				TotalTempSize: stats.TotalTempSize,
			})
		}
	}

	if len(queries) == 0 {
		return
	}

	sort.Slice(queries, func(i, j int) bool {
		if queries[i].TotalTempSize != queries[j].TotalTempSize {
			return queries[i].TotalTempSize > queries[j].TotalTempSize
		}
		return queries[i].Query < queries[j].Query
	})

	termWidth := pr.width
	if termWidth <= 0 {
		termWidth = 120
	}

	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	header := fmt.Sprintf("%-9s  %-*s  %10s  %15s",
		"SQLID", queryWidth, "Query", "Executed", "Total Temp Size")
	fmt.Fprintln(pr.out, pr.bold(header))
	totalWidth := 9 + 2 + queryWidth + 2 + 10 + 2 + 15
	fmt.Fprintln(pr.out, strings.Repeat("-", totalWidth))

	for i, q := range queries {
		if i >= 10 {
			break
		}
		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Fprintf(pr.out, "%-9s  %-*s  %10d  %15s\n",
			q.ID,
			queryWidth, displayQuery,
			q.Count,
			formatBytes(q.TotalTempSize))
	}
	fmt.Fprintln(pr.out)
}

// PrintSlowestQueries displays the top 10 slowest individual queries,
// showing three columns: SQLID, truncated Query, and Duration.
func PrintSlowestQueries(queryStats map[string]*analysis.QueryStat) {
	renderSlowestQueries(newStdoutPrinter(), queryStats)
}

func renderSlowestQueries(pr textPrinter, queryStats map[string]*analysis.QueryStat) {
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

	termWidth := pr.width
	if termWidth <= 0 {
		termWidth = 120
	}
	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	headerFormat := fmt.Sprintf("%%-9s  %%-%ds  %%12s", queryWidth)
	header := fmt.Sprintf(headerFormat, "SQLID", "Query", "Duration")
	fmt.Fprintln(pr.out, pr.bold(header))
	totalWidth := 9 + 2 + queryWidth + 2 + 12
	fmt.Fprintln(pr.out, strings.Repeat("-", totalWidth))

	for _, q := range queries {
		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Fprintf(pr.out, "%-9s  %-*s  %12s\n",
			q.ID,
			queryWidth, displayQuery,
			formatQueryDuration(q.MaxTime))
	}
}

// PrintMostFrequentQueries displays the top queries by frequency (sorted descending by count).
// The display stops if a query was executed only once or if the execution count drops by more than a factor of 10.
func PrintMostFrequentQueries(queryStats map[string]*analysis.QueryStat) {
	renderMostFrequentQueries(newStdoutPrinter(), queryStats)
}

func renderMostFrequentQueries(pr textPrinter, queryStats map[string]*analysis.QueryStat) {
	type queryInfo struct {
		ID      string
		Query   string
		Count   int
		AvgTime float64
	}
	var queries []queryInfo
	for normalized, stats := range queryStats {
		id, _ := analysis.GenerateQueryID(stats.RawQuery, normalized)
		queries = append(queries, queryInfo{
			ID:      id,
			Query:   normalized,
			Count:   stats.Count,
			AvgTime: stats.AvgTime,
		})
	}

	sort.Slice(queries, func(i, j int) bool {
		if queries[i].Count != queries[j].Count {
			return queries[i].Count > queries[j].Count
		}
		return queries[i].Query < queries[j].Query
	})

	termWidth := pr.width
	if termWidth <= 0 {
		termWidth = 120
	}
	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	header := fmt.Sprintf("%-9s  %-*s  %-12s  %-12s", "SQLID", queryWidth, "Query", "Executed", "Avg time")
	fmt.Fprintln(pr.out, pr.bold(header))
	fmt.Fprintln(pr.out, strings.Repeat("-", 9+2+queryWidth+2+12+2+12))

	prevCount := -1
	for i, q := range queries {
		if i >= 15 {
			break
		}
		if q.Count <= 1 {
			break
		}
		if prevCount > 0 && q.Count*10 < prevCount {
			break
		}
		prevCount = q.Count

		displayQuery := truncateQuery(q.Query, queryWidth)
		fmt.Fprintf(pr.out, "%-9s  %-*s  %-12d  %-12s\n",
			q.ID,
			queryWidth, displayQuery,
			q.Count,
			formatQueryDuration(q.AvgTime))
	}
}

// PrintSqlDetails iterates over the QueryStats and displays details for each query
// whose SQLID matches one of the provided queryDetails.
func PrintSqlDetails(m analysis.SqlMetrics, queryDetails []string) {
	renderSQLDetails(newStdoutPrinter(), m, queryDetails)
}

func renderSQLDetails(pr textPrinter, m analysis.SqlMetrics, queryDetails []string) {
	if len(queryDetails) == 0 {
		return
	}

	targets := make(map[string]struct{}, len(queryDetails))
	for _, id := range queryDetails {
		targets[id] = struct{}{}
	}

	for _, qs := range m.QueryStats {
		if _, ok := targets[qs.ID]; !ok {
			continue
		}

		fmt.Fprintf(pr.out, "\nDetails for SQLID: %s\n", qs.ID)
		fmt.Fprintln(pr.out, "SQL Query Details:")
		fmt.Fprintf(pr.out, "  SQLID            : %s\n", qs.ID)
		fmt.Fprintf(pr.out, "  Query Type       : %s\n", analysis.QueryTypeFromID(qs.ID))
		fmt.Fprintf(pr.out, "  Raw Query        : %s\n", qs.RawQuery)
		fmt.Fprintf(pr.out, "  Normalized Query : %s\n", qs.NormalizedQuery)
		fmt.Fprintf(pr.out, "  Executed         : %d\n", qs.Count)
		fmt.Fprintf(pr.out, "  Total Time       : %s\n", formatQueryDuration(qs.TotalTime))
		fmt.Fprintf(pr.out, "  Average Time     : %s\n", formatQueryDuration(qs.AvgTime))
		fmt.Fprintf(pr.out, "  Max Time         : %s\n", formatQueryDuration(qs.MaxTime))
	}
}

// Helpers

// truncateQuery truncates the query string to the specified length, appending "..." if necessary.
func truncateQuery(query string, length int) string {
	if length <= 3 {
		return query
	}
	runes := []rune(query)
	if len(runes) <= length {
		return query
	}
	return string(runes[:length-3]) + "..."
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
	renderEventsReport(newStdoutPrinter(), summaries)
}

func renderEventsReport(pr textPrinter, summaries []analysis.EventSummary) {
	if len(summaries) == 0 {
		return
	}

	fmt.Fprintln(pr.out, pr.bold("\nEVENTS\n"))

	maxTypeLength := 0
	for _, summary := range summaries {
		if len(summary.Type) > maxTypeLength {
			maxTypeLength = len(summary.Type)
		}
	}

	for _, summary := range summaries {
		if summary.Count == 0 {
			fmt.Fprintf(pr.out, "  %-*s : -\n", maxTypeLength, summary.Type)
			continue
		}
		fmt.Fprintf(pr.out, "  %-*s : %d\n", maxTypeLength, summary.Type, summary.Count)
	}
}

// PrintHistogram affiche l'histogramme en triant les plages horaires par ordre chronologique.
// La largeur du terminal est récupérée automatiquement pour adapter la largeur de la barre.
func PrintHistogram(data map[string]int, title string, unit string, scaleFactor int, orderedLabels []string) {
	renderHistogram(newStdoutPrinter(), data, title, unit, scaleFactor, orderedLabels)
}

func renderHistogram(pr textPrinter, data map[string]int, title string, unit string, scaleFactor int, orderedLabels []string) {
	if len(data) == 0 {
		fmt.Fprintln(pr.out, "\n  (No data available)")
		return
	}

	termWidth := pr.width
	if termWidth <= 0 {
		termWidth = 80
	}

	const labelPadWidth = 20
	const valueWidth = 5
	const spacing = 4
	barWidth := termWidth - labelPadWidth - spacing - valueWidth
	if barWidth < 10 {
		barWidth = 10
	}

	labels := make([]string, 0, len(data))
	if len(orderedLabels) > 0 {
		labels = orderedLabels
	} else {
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

	maxValue := 0
	for _, value := range data {
		if value > maxValue {
			maxValue = value
		}
	}

	if scaleFactor <= 0 {
		scaleFactor = int(math.Ceil(float64(maxValue) / float64(barWidth)))
		if scaleFactor < 1 {
			scaleFactor = 1
		}
	}

	fmt.Fprintf(pr.out, "  %s | ■ = %d %s\n\n", title, scaleFactor, unit)

	for _, label := range labels {
		value := data[label]
		barLength := value / scaleFactor
		if barLength > barWidth {
			barLength = barWidth
		}
		bar := strings.Repeat("■", barLength)

		valueStr := fmt.Sprintf("%d %s", value, unit)
		if value == 0 {
			valueStr = " -"
		}

		if barLength > 0 {
			fmt.Fprintf(pr.out, "  %-13s  %-s %s\n", label, bar, valueStr)
		} else {
			fmt.Fprintf(pr.out, "  %-14s %s\n", label, valueStr)
		}
	}
	fmt.Fprintln(pr.out)
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
