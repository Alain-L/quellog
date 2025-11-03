package output

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// ExportMarkdown produces a comprehensive markdown report.
// Reuses histogram computation from text.go.
func ExportMarkdown(m analysis.AggregatedMetrics, sections []string) {
	has := func(name string) bool {
		for _, s := range sections {
			if s == name || s == "all" {
				return true
			}
		}
		return false
	}

	var b strings.Builder
	duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)

	// ============================================================================
	// SUMMARY
	// ============================================================================
	if has("summary") {
		b.WriteString("## SUMMARY\n\n")
		b.WriteString(fmt.Sprintf("This _quellog_ report summarizes **%s** log entries collected between %s — %s, spanning %s of activity.\n\n",
			formatIntWithCommas(int64(m.Global.Count)),
			humanDate(m.Global.MinTimestamp),
			humanDate(m.Global.MaxTimestamp),
			humanDuration(duration),
		))
	}

	// ============================================================================
	// SQL PERFORMANCE
	// ============================================================================
	if has("sql_performance") && m.SQL.TotalQueries > 0 {
		b.WriteString("## SQL PERFORMANCE\n\n")

		// Query load histogram
		if !m.SQL.StartTimestamp.IsZero() && !m.SQL.EndTimestamp.IsZero() {
			queryLoad, unit, scale := computeQueryLoadHistogram(m.SQL)
			printHistogramMarkdown(&b, queryLoad, "Query load distribution", unit, scale, nil)
		}

		// Key metrics table
		top1Slow := countSlowQueries(m.SQL)
		b.WriteString("|  |  |  |  |\n")
		b.WriteString("|---|---:|---|---:|\n")
		b.WriteString(fmt.Sprintf("| Total query duration | %s | Total queries parsed | %d |\n",
			formatQueryDuration(m.SQL.SumQueryDuration), m.SQL.TotalQueries))
		b.WriteString(fmt.Sprintf("| Total unique queries | %d | Top 1%% slow queries | %d |\n",
			m.SQL.UniqueQueries, top1Slow))
		b.WriteString(fmt.Sprintf("| Query max duration | %s | Query min duration | %s |\n",
			formatQueryDuration(m.SQL.MaxQueryDuration), formatQueryDuration(m.SQL.MinQueryDuration)))
		b.WriteString(fmt.Sprintf("| Query median duration | %s | Query 99%% max duration | %s |\n\n",
			formatQueryDuration(m.SQL.MedianQueryDuration), formatQueryDuration(m.SQL.P99QueryDuration)))

		// Duration histogram
		if !m.SQL.StartTimestamp.IsZero() && !m.SQL.EndTimestamp.IsZero() {
			hist, unit, scale := computeQueryDurationHistogram(m.SQL)
			printHistogramMarkdown(&b, hist, "Query duration distribution", unit, scale,
				[]string{"  < 1 ms", " < 10 ms", "< 100 ms", "   < 1 s", "  < 10 s", " >= 10 s"})
		}

		// Query stats tables
		b.WriteString("### Query Statistics\n\n")
		printQueryStatsMarkdown(&b, m.SQL.QueryStats)
	}

	// ============================================================================
	// EVENTS
	// ============================================================================
	if has("events") && len(m.EventSummaries) > 0 {
		b.WriteString("## EVENTS\n\n")
		b.WriteString("|  |  |\n")
		b.WriteString("|---|---:|\n")
		for _, ev := range m.EventSummaries {
			if ev.Count == 0 {
				b.WriteString(fmt.Sprintf("| %s | - |\n", ev.Type))
			} else {
				b.WriteString(fmt.Sprintf("| %s | %d |\n", ev.Type, ev.Count))
			}
		}
		b.WriteString("\n")
	}

	// ============================================================================
	// TEMP FILES
	// ============================================================================
	if has("tempfiles") && m.TempFiles.Count > 0 {
		b.WriteString("## TEMP FILES\n\n")

		hist, unit, scale := computeTempFileHistogram(m.TempFiles)
		printHistogramMarkdown(&b, hist, "Temp file distribution", unit, scale, nil)

		avgSize := int64(0)
		if m.TempFiles.Count > 0 {
			avgSize = m.TempFiles.TotalSize / int64(m.TempFiles.Count)
		}

		b.WriteString(fmt.Sprintf("- **Temp file messages**: %d\n", m.TempFiles.Count))
		b.WriteString(fmt.Sprintf("- **Cumulative temp file size**: %s\n", formatBytes(m.TempFiles.TotalSize)))
		b.WriteString(fmt.Sprintf("- **Average temp file size**: %s\n\n", formatBytes(avgSize)))
	}

	// ============================================================================
	// MAINTENANCE
	// ============================================================================
	if has("maintenance") && (m.Vacuum.VacuumCount > 0 || m.Vacuum.AnalyzeCount > 0) {
		b.WriteString("## MAINTENANCE\n\n")
		b.WriteString(fmt.Sprintf("- **Automatic vacuum count**: %d\n", m.Vacuum.VacuumCount))
		b.WriteString(fmt.Sprintf("- **Automatic analyze count**: %d\n\n", m.Vacuum.AnalyzeCount))

		if m.Vacuum.VacuumCount > 0 {
			b.WriteString("### Top automatic vacuum operations per table\n\n")
			b.WriteString(printTopTablesMarkdown(m.Vacuum.VacuumTableCounts, m.Vacuum.VacuumCount, m.Vacuum.VacuumSpaceRecovered))
			b.WriteString("\n")
		}

		if m.Vacuum.AnalyzeCount > 0 {
			b.WriteString("### Top automatic analyze operations per table\n\n")
			b.WriteString(printTopTablesMarkdown(m.Vacuum.AnalyzeTableCounts, m.Vacuum.AnalyzeCount, nil))
			b.WriteString("\n")
		}
	}

	// ============================================================================
	// CHECKPOINTS
	// ============================================================================
	if has("checkpoints") && m.Checkpoints.CompleteCount > 0 {
		avgWriteSeconds := m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)
		avgDuration := time.Duration(avgWriteSeconds * float64(time.Second)).Truncate(time.Second)
		maxDuration := time.Duration(m.Checkpoints.MaxWriteTimeSeconds * float64(time.Second)).Truncate(time.Second)

		b.WriteString("## CHECKPOINTS\n\n")

		hist, _, scale := computeCheckpointHistogram(m.Checkpoints)
		printHistogramMarkdown(&b, hist, "Checkpoint distribution", "", scale, nil)

		b.WriteString(fmt.Sprintf("- **Checkpoint count**: %d\n", m.Checkpoints.CompleteCount))
		b.WriteString(fmt.Sprintf("- **Avg checkpoint write time**: %s\n", avgDuration))
		b.WriteString(fmt.Sprintf("- **Max checkpoint write time**: %s\n\n", maxDuration))

		// Affichage des types de checkpoints
		if len(m.Checkpoints.TypeCounts) > 0 {
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

			// Calculer la durée totale pour le taux
			duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
			durationHours := duration.Hours()

			b.WriteString("### Checkpoint types\n\n")
			b.WriteString("|  |  |  |  |\n")
			b.WriteString("|------|------:|--:|-----:|\n")

			// Afficher chaque type
			for _, pair := range pairs {
				percentage := float64(pair.Count) / float64(m.Checkpoints.CompleteCount) * 100

				// Calculer le taux (checkpoints par heure) pour ce type
				rate := 0.0
				if durationHours > 0 {
					rate = float64(pair.Count) / durationHours
				}

				b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% | %.2f/h |\n",
					pair.Name, pair.Count, percentage, rate))
			}
			b.WriteString("\n")
		}
	}

	// ============================================================================
	// CONNECTIONS
	// ============================================================================
	if has("connections") && m.Connections.ConnectionReceivedCount > 0 {
		b.WriteString("## CONNECTIONS & SESSIONS\n\n")

		hist, _, scale := computeConnectionsHistogram(m.Connections.Connections)
		printHistogramMarkdown(&b, hist, "Connection distribution", "", scale, nil)

		b.WriteString(fmt.Sprintf("- **Connection count**: %d\n", m.Connections.ConnectionReceivedCount))
		if duration.Hours() > 0 {
			avgConnPerHour := float64(m.Connections.ConnectionReceivedCount) / duration.Hours()
			b.WriteString(fmt.Sprintf("- **Avg connections per hour**: %.2f\n", avgConnPerHour))
		}
		b.WriteString(fmt.Sprintf("- **Disconnection count**: %d\n", m.Connections.DisconnectionCount))

		if m.Connections.DisconnectionCount > 0 {
			avgSessionTime := time.Duration(float64(m.Connections.TotalSessionTime) / float64(m.Connections.DisconnectionCount))
			b.WriteString(fmt.Sprintf("- **Avg session time**: %s\n\n", avgSessionTime.Round(time.Second)))
		} else {
			b.WriteString("- **Avg session time**: N/A\n\n")
		}
	}

	// ============================================================================
	// CLIENTS
	// ============================================================================
	if has("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0) {
		b.WriteString("## CLIENTS\n\n")
		b.WriteString(fmt.Sprintf("- **Unique DBs**: %d\n", m.UniqueEntities.UniqueDbs))
		b.WriteString(fmt.Sprintf("- **Unique Users**: %d\n", m.UniqueEntities.UniqueUsers))
		b.WriteString(fmt.Sprintf("- **Unique Apps**: %d\n\n", m.UniqueEntities.UniqueApps))

		if m.UniqueEntities.UniqueUsers > 0 {
			b.WriteString("### USERS\n\n")
			for _, user := range m.UniqueEntities.Users {
				b.WriteString(fmt.Sprintf("- %s\n", user))
			}
			b.WriteString("\n")
		}

		if m.UniqueEntities.UniqueApps > 0 {
			b.WriteString("### APPS\n\n")
			for _, app := range m.UniqueEntities.Apps {
				b.WriteString(fmt.Sprintf("- %s\n", app))
			}
			b.WriteString("\n")
		}

		if m.UniqueEntities.UniqueDbs > 0 {
			b.WriteString("### DATABASES\n\n")
			for _, db := range m.UniqueEntities.DBs {
				b.WriteString(fmt.Sprintf("- %s\n", db))
			}
			b.WriteString("\n")
		}
	}

	fmt.Println(b.String())
}

// ============================================================================
// MARKDOWN-SPECIFIC HELPERS
// ============================================================================

// printHistogramMarkdown renders a histogram as ASCII art in a code block
func printHistogramMarkdown(b *strings.Builder, data map[string]int, title, unit string, scaleFactor int, orderedLabels []string) {
	if len(data) == 0 {
		b.WriteString("(No data available)\n\n")
		return
	}

	var labels []string
	if len(orderedLabels) > 0 {
		labels = orderedLabels
	} else {
		for k := range data {
			labels = append(labels, k)
		}
		// Sort by time if labels are time ranges
		sort.Slice(labels, func(i, j int) bool {
			pi := strings.Split(labels[i], " - ")
			pj := strings.Split(labels[j], " - ")
			if len(pi) == 2 && len(pj) == 2 {
				ti, err1 := time.Parse("15:04", pi[0])
				tj, err2 := time.Parse("15:04", pj[0])
				if err1 == nil && err2 == nil {
					return ti.Before(tj)
				}
			}
			return labels[i] < labels[j]
		})
	}

	if scaleFactor <= 0 {
		scaleFactor = 1
	}

	b.WriteString(fmt.Sprintf("### %s\n\n```\n", title))
	for _, label := range labels {
		v := data[label]
		barLen := v / scaleFactor
		if barLen < 0 {
			barLen = 0
		}
		bar := strings.Repeat("■", barLen)

		valueStr := fmt.Sprintf("%d %s", v, unit)
		if v == 0 {
			valueStr = "-"
		}
		b.WriteString(fmt.Sprintf("%s | %s %s\n", label, bar, valueStr))
	}
	b.WriteString("```\n\n")
}

// printTopTablesMarkdown produces a markdown table for vacuum/analyze operations
func printTopTablesMarkdown(tableCounts map[string]int, total int, spaceRecovered map[string]int64) string {
	if len(tableCounts) == 0 {
		return "(No tables)\n"
	}

	type pair struct {
		Name      string
		Count     int
		Recovered int64
	}

	var pairs []pair
	for name, c := range tableCounts {
		p := pair{Name: name, Count: c}
		if spaceRecovered != nil {
			p.Recovered = spaceRecovered[name]
		}
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Count > pairs[j].Count })

	var sb strings.Builder
	sb.WriteString("| Table | Count | % of total | Recovered |\n")
	sb.WriteString("|---|---:|---:|---:|\n")

	cum := 0
	for i, p := range pairs {
		if i >= 10 {
			break
		}

		percentage := 0.0
		if total > 0 {
			percentage = float64(p.Count) / float64(total) * 100
		}
		cum += p.Count

		sb.WriteString(fmt.Sprintf("| %s | %d | %.2f%% | %s |\n",
			p.Name, p.Count, percentage, formatBytes(p.Recovered)))

		// Stop at 80% cumulative or 10 rows
		cumPerc := 0.0
		if total > 0 {
			cumPerc = float64(cum) / float64(total) * 100
		}
		if cumPerc >= 80 {
			break
		}
	}
	return sb.String()
}

// printQueryStatsMarkdown generates three tables: slowest, most frequent, time consuming
func printQueryStatsMarkdown(b *strings.Builder, stats map[string]*analysis.QueryStat) {
	if len(stats) == 0 {
		b.WriteString("(No query stats)\n\n")
		return
	}

	type qinfo struct {
		ID        string
		Query     string
		Count     int
		TotalTime float64
		AvgTime   float64
		MaxTime   float64
		TotalTempSize int64
	}

	var list []qinfo
	for _, s := range stats {
		// ✅ Utilise l'ID déjà calculé au lieu de le recalculer
		list = append(list, qinfo{
			ID:        s.ID,
			Query:     s.NormalizedQuery,
			Count:     s.Count,
			TotalTime: s.TotalTime,
			AvgTime:   s.AvgTime,
			MaxTime:   s.MaxTime,
			TotalTempSize: s.TotalTempSize,
		})
	}

	// Slowest queries
	sort.Slice(list, func(i, j int) bool { return list[i].MaxTime > list[j].MaxTime })
	b.WriteString("**Slowest queries (top 10)**\n\n")
	b.WriteString("| SQLID | Max | Avg | Count | Query |\n")
	b.WriteString("|---|---:|---:|---:|---|\n")
	for i, q := range list {
		if i >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %s |\n",
			q.ID, formatQueryDuration(q.MaxTime), formatQueryDuration(q.AvgTime),
			q.Count, truncateQuery(q.Query, 80)))
	}
	b.WriteString("\n")

	// Most frequent queries
	sort.Slice(list, func(i, j int) bool { return list[i].Count > list[j].Count })
	b.WriteString("**Most frequent queries (top 10)**\n\n")
	b.WriteString("| SQLID | Count | Avg | Max | Query |\n")
	b.WriteString("|---|---:|---:|---:|---|\n")
	for i, q := range list {
		if i >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s |\n",
			q.ID, q.Count, formatQueryDuration(q.AvgTime), formatQueryDuration(q.MaxTime),
			truncateQuery(q.Query, 80)))
	}
	b.WriteString("\n")

	// Time consuming queries
	sort.Slice(list, func(i, j int) bool { return list[i].TotalTime > list[j].TotalTime })
	b.WriteString("**Most time consuming queries (top 10)**\n\n")
	b.WriteString("| SQLID | Total | Avg | Count | Query |\n")
	b.WriteString("|---|---:|---:|---:|---|\n")
	for i, q := range list {
		if i >= 10 {
			break
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %s |\n",
			q.ID, formatQueryDuration(q.TotalTime), formatQueryDuration(q.AvgTime),
			q.Count, truncateQuery(q.Query, 80)))
	}
	b.WriteString("\n")

	// Temp file consuming queries
	sort.Slice(list, func(i, j int) bool { return list[i].TotalTempSize > list[j].TotalTempSize })
	b.WriteString("**Most temporary file consuming queries (top 10)**\n\n")
	b.WriteString("| SQLID | Total Temp Size | Count | Query |\n")
	b.WriteString("|---|---:|---:|---|\n")
	for i, q := range list {
		if i >= 10 {
			break
		}
		if q.TotalTempSize == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n",
			q.ID, formatBytes(q.TotalTempSize), q.Count,
			truncateQuery(q.Query, 80)))
	}
	b.WriteString("\n")
}

// countSlowQueries returns the count of queries in the top 1% (P99)
func countSlowQueries(sql analysis.SqlMetrics) int {
	if len(sql.Executions) == 0 {
		return 0
	}
	threshold := sql.P99QueryDuration
	count := 0
	for _, exec := range sql.Executions {
		if exec.Duration >= threshold {
			count++
		}
	}
	return count
}

// ============================================================================
// FORMATTING HELPERS (reused from text.go)
// ============================================================================

// formatIntWithCommas formats an integer with thousands separators
func formatIntWithCommas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	res := strings.Join(parts, ",")
	if n < 0 {
		res = "-" + res
	}
	return res
}

// humanDate returns a compact, human-friendly date/time string
func humanDate(t time.Time) string {
	if t.IsZero() {
		return "N/A"
	}
	return t.Format("2 Jan 2006, 15:04 (MST)")
}

// humanDuration formats a duration in a human-readable way
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	s := int64(d / time.Second)
	days := s / 86400
	s -= days * 86400
	hours := s / 3600
	s -= hours * 3600
	minutes := s / 60
	secs := s - minutes*60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if secs > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}
	return strings.Join(parts, " ")
}
