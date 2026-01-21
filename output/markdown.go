//go:build !js

package output

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// ExportMarkdown produces a comprehensive markdown report.
// Reuses histogram computation from text.go.
// When full is true, sql_overview and sql_performance sections are added at the end.
func ExportMarkdown(w io.Writer, m analysis.AggregatedMetrics, sections []string, full bool) {
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
	// SQL SUMMARY (skip if full mode - enriched version added at the end)
	// ============================================================================
	if !full && has("sql_summary") && m.SQL.TotalQueries > 0 {
		b.WriteString("## SQL SUMMARY\n\n")

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
	if (has("events") || has("errors")) && len(m.EventSummaries) > 0 {
		b.WriteString("## EVENTS\n\n")

		onlyErrors := has("errors") && !has("events")

		// Re-sort summaries by severity order (PANIC -> FATAL -> ERROR ...)
		severityRank := make(map[string]int)
		for i, s := range analysis.PredefinedEventTypes {
			severityRank[s] = i
		}

		sort.Slice(m.EventSummaries, func(i, j int) bool {
			rankI, okI := severityRank[m.EventSummaries[i].Type]
			rankJ, okJ := severityRank[m.EventSummaries[j].Type]
			if okI && okJ {
				return rankI < rankJ
			}
			if okI {
				return true
			}
			if okJ {
				return false
			}
			return m.EventSummaries[i].Type < m.EventSummaries[j].Type
		})

		// Group top events by severity
		eventsBySeverity := make(map[string][]analysis.EventStat)
		for _, e := range m.TopEvents {
			eventsBySeverity[e.Severity] = append(eventsBySeverity[e.Severity], e)
		}

		for _, summary := range m.EventSummaries {
			if summary.Count == 0 {
				continue
			}

			// Filter non-error severities if requested
			if onlyErrors {
				s := summary.Type
				if s == "LOG" || s == "INFO" || s == "DEBUG" || s == "NOTICE" {
					continue
				}
			}

			// Level 1: Severity
			b.WriteString(fmt.Sprintf("- **%s**: %d (%.1f%%)\n",
				summary.Type, summary.Count, summary.Percentage))

			// Detailed events
			if events, ok := eventsBySeverity[summary.Type]; ok {
				// Group by Error Class
				byClass := make(map[string][]analysis.EventStat)
				for _, e := range events {
					class := e.SQLStateClass
					if class == "" || class == "00" {
						class = "Unclassified"
					}
					byClass[class] = append(byClass[class], e)
				}

				// Sort classes
				var classes []string
				for c := range byClass {
					classes = append(classes, c)
				}
				sort.Slice(classes, func(i, j int) bool {
					if classes[i] == "Unclassified" {
						return false
					}
					if classes[j] == "Unclassified" {
						return true
					}
					return classes[i] < classes[j]
				})

				for _, classCode := range classes {
					classEvents := byClass[classCode]

					// Level 2: Class
					shouldPrintHeader := (classCode != "Unclassified") || (classCode == "Unclassified" && len(classes) > 1)
					
					indent := "  "
					if shouldPrintHeader {
						classHeader := classCode
						if classCode != "Unclassified" {
							desc := analysis.GetErrorClassDescription(classCode)
							classHeader = fmt.Sprintf("%s - %s", classCode, desc)
						}
						b.WriteString(fmt.Sprintf("  - **%s**\n", classHeader))
						indent = "    "
					}

					// Sort events by count
					sort.Slice(classEvents, func(i, j int) bool {
						return classEvents[i].Count > classEvents[j].Count
					})

					// Level 3: Message
					for _, e := range classEvents {
						msg := e.Message
						if len(msg) > 80 {
							msg = msg[:77] + "..."
						}
						// Escape backticks in message for markdown code block
						msg = strings.ReplaceAll(msg, "`", "'")
						
						localPct := 0.0
						if summary.Count > 0 {
							localPct = (float64(e.Count) / float64(summary.Count)) * 100
						}

						b.WriteString(fmt.Sprintf("%s- `%s` (%d) [%.1f%%]\n",
							indent, msg, e.Count, localPct))
					}
				}
			}
			b.WriteString("\n")
		}
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

		// Queries generating temp files (in detailed/full mode)
		if (full || !has("all")) && len(m.TempFiles.QueryStats) > 0 {
			b.WriteString("### Queries Generating Temp Files\n\n")
			b.WriteString("| SQLID | Query | Count | Total Size |\n")
			b.WriteString("|---|---|---:|---:|\n")

			// Sort by total size descending
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

			limit := 10
			if len(queries) < limit {
				limit = len(queries)
			}
			for i := 0; i < limit; i++ {
				stat := queries[i].stat
				b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n",
					stat.ID,
					truncateQuery(stat.NormalizedQuery, 50),
					stat.Count,
					formatBytes(stat.TotalSize)))
			}
			b.WriteString("\n")
		}
	}

	// ============================================================================
	// LOCKS
	// ============================================================================
	if has("locks") && m.Locks.TotalEvents > 0 {
		b.WriteString("## LOCKS\n\n")

		avgWaitTime := 0.0
		if m.Locks.WaitingEvents+m.Locks.AcquiredEvents > 0 {
			avgWaitTime = m.Locks.TotalWaitTime / float64(m.Locks.WaitingEvents+m.Locks.AcquiredEvents)
		}

		b.WriteString(fmt.Sprintf("- **Total lock events**: %d\n", m.Locks.TotalEvents))
		b.WriteString(fmt.Sprintf("- **Waiting events**: %d\n", m.Locks.WaitingEvents))
		b.WriteString(fmt.Sprintf("- **Acquired events**: %d\n", m.Locks.AcquiredEvents))
		if m.Locks.DeadlockEvents > 0 {
			b.WriteString(fmt.Sprintf("- **Deadlock events**: %d\n", m.Locks.DeadlockEvents))
		}
		if m.Locks.TotalWaitTime > 0 {
			b.WriteString(fmt.Sprintf("- **Average wait time**: %.2f ms\n", avgWaitTime))
			b.WriteString(fmt.Sprintf("- **Total wait time**: %.2f s\n\n", m.Locks.TotalWaitTime/1000))
		} else {
			b.WriteString("\n")
		}

		// Lock types distribution
		if len(m.Locks.LockTypeStats) > 0 {
			b.WriteString("### Lock Types\n\n")
			b.WriteString("| Lock Type | Count | Percentage |\n")
			b.WriteString("|---|---:|---:|\n")
			printLockStatsMarkdown(&b, m.Locks.LockTypeStats, m.Locks.TotalEvents)
			b.WriteString("\n")
		}

		// Resource types distribution
		if len(m.Locks.ResourceTypeStats) > 0 {
			b.WriteString("### Resource Types\n\n")
			b.WriteString("| Resource Type | Count | Percentage |\n")
			b.WriteString("|---|---:|---:|\n")
			printLockStatsMarkdown(&b, m.Locks.ResourceTypeStats, m.Locks.TotalEvents)
			b.WriteString("\n")
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
				b.WriteString("### Acquired Locks by Query\n\n")
				b.WriteString("| SQLID | Normalized Query | Locks | Avg Wait (ms) | Total Wait (ms) |\n")
				b.WriteString("|---|---|---:|---:|---:|\n")
				printAcquiredLockQueriesMarkdown(&b, m.Locks.QueryStats, 10)
				b.WriteString("\n")
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
				b.WriteString("### Locks Still Waiting by Query\n\n")
				b.WriteString("| SQLID | Normalized Query | Locks | Avg Wait (ms) | Total Wait (ms) |\n")
				b.WriteString("|---|---|---:|---:|---:|\n")
				printStillWaitingLockQueriesMarkdown(&b, m.Locks.QueryStats, 10)
				b.WriteString("\n")
			}
		}

		// Most frequent waiting queries
		if len(m.Locks.QueryStats) > 0 {
			hasWaiting := false
			for _, stat := range m.Locks.QueryStats {
				if stat.AcquiredCount > 0 || stat.StillWaitingCount > 0 {
					hasWaiting = true
					break
				}
			}
			if hasWaiting {
				b.WriteString("### Most Frequent Waiting Queries\n\n")
				b.WriteString("| SQLID | Normalized Query | Locks | Avg Wait (ms) | Total Wait (ms) |\n")
				b.WriteString("|---|---|---:|---:|---:|\n")
				printMostFrequentWaitingQueriesMarkdown(&b, m.Locks.QueryStats, 10)
				b.WriteString("\n")
			}
		}
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

		// Determine if detailed mode (markdown always shows details like --md implies full export)
		isDetailedMode := true

		// Concurrent sessions histogram (always shown)
		if len(m.Connections.SessionEvents) > 0 && !m.Global.MinTimestamp.IsZero() && !m.Global.MaxTimestamp.IsZero() {
			numBuckets := 6
			if isDetailedMode {
				numBuckets = 12
			}
			concurrentHist, labels, concurrentScale, peakTimes := computeConcurrentHistogram(
				m.Connections.SessionEvents,
				m.Global.MinTimestamp,
				m.Global.MaxTimestamp,
				numBuckets,
			)
			if len(concurrentHist) > 0 {
				printConcurrentHistogramMarkdown(&b, concurrentHist, "Concurrent sessions", concurrentScale, labels, peakTimes)
			}
		}

		// Connection distribution histogram (in detailed mode)
		if isDetailedMode {
			hist, _, scale := computeConnectionsHistogram(m.Connections.Connections, m.Global.MinTimestamp, m.Global.MaxTimestamp)
			printHistogramMarkdown(&b, hist, "Connection distribution", "", scale, nil)
		}

		b.WriteString(fmt.Sprintf("- **Connection count**: %d\n", m.Connections.ConnectionReceivedCount))
		if duration.Hours() > 0 {
			avgConnPerHour := float64(m.Connections.ConnectionReceivedCount) / duration.Hours()
			b.WriteString(fmt.Sprintf("- **Avg connections per hour**: %.2f\n", avgConnPerHour))
		}
		b.WriteString(fmt.Sprintf("- **Disconnection count**: %d\n", m.Connections.DisconnectionCount))

		if len(m.Connections.SessionDurations) > 0 {
			// Average
			avgSessionTime := time.Duration(float64(m.Connections.TotalSessionTime) / float64(m.Connections.DisconnectionCount))
			b.WriteString(fmt.Sprintf("- **Avg session time**: %s\n", formatSessionDuration(avgSessionTime)))
			// Median (more representative for skewed distributions)
			sorted := make([]time.Duration, len(m.Connections.SessionDurations))
			copy(sorted, m.Connections.SessionDurations)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
			median := sorted[len(sorted)/2]
			b.WriteString(fmt.Sprintf("- **Median session time**: %s\n", formatSessionDuration(median)))
		} else if m.Connections.DisconnectionCount > 0 {
			b.WriteString("- **Avg session time**: N/A\n")
		}

		// Peak concurrent sessions
		if m.Connections.PeakConcurrentSessions > 0 {
			b.WriteString(fmt.Sprintf("- **Peak concurrent sessions**: %d (at %s)\n",
				m.Connections.PeakConcurrentSessions,
				m.Connections.PeakConcurrentTimestamp.Format("15:04:05")))
		}
		b.WriteString("\n")

		// Session statistics
		if len(m.Connections.SessionDurations) > 0 {
			stats := analysis.CalculateDurationStats(m.Connections.SessionDurations)
			var cumulated time.Duration
			for _, d := range m.Connections.SessionDurations {
				cumulated += d
			}

			b.WriteString("### Session Duration Statistics\n\n")
			b.WriteString(fmt.Sprintf("- **Count**: %d\n", stats.Count))
			b.WriteString(fmt.Sprintf("- **Min**: %s\n", stats.Min.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Max**: %s\n", stats.Max.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Avg**: %s\n", stats.Avg.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Median**: %s\n", stats.Median.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Cumulated**: %s\n\n", cumulated.Round(time.Second)))

			// Session duration distribution
			dist := analysis.CalculateDurationDistribution(m.Connections.SessionDurations)
			// Calculate proper scale factor for histogram
			maxVal := 0
			for _, v := range dist {
				if v > maxVal {
					maxVal = v
				}
			}
			scaleFactor := 1
			if maxVal > 40 {
				scaleFactor = (maxVal + 39) / 40 // Ceiling division to limit bars to ~40 chars
			}
			printHistogramMarkdown(&b, dist, "Session duration distribution", "sessions", scaleFactor,
				[]string{"< 1s", "1s - 1min", "1min - 30min", "30min - 2h", "2h - 5h", "> 5h"})
		}

		// Sessions by user
		if len(m.Connections.SessionsByUser) > 0 {
			b.WriteString("### Session Duration by User\n\n")
			b.WriteString("| User | Sessions | Min | Max | Avg | Median | Cumulated |\n")
			b.WriteString("|---|---:|---|---|---|---|---:|\n")

			type userStats struct {
				user      string
				stats     analysis.DurationStats
				cumulated time.Duration
			}
			var sortedUsers []userStats
			for user, durations := range m.Connections.SessionsByUser {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				sortedUsers = append(sortedUsers, userStats{
					user: user, stats: stats, cumulated: cumulated,
				})
			}
			sort.Slice(sortedUsers, func(i, j int) bool {
				return sortedUsers[i].stats.Count > sortedUsers[j].stats.Count
			})

			limit := 10
			for i := 0; i < limit && i < len(sortedUsers); i++ {
				u := sortedUsers[i]
				b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s | %s | %s |\n",
					u.user,
					u.stats.Count,
					u.stats.Min.Round(time.Second),
					u.stats.Max.Round(time.Second),
					u.stats.Avg.Round(time.Second),
					u.stats.Median.Round(time.Second),
					u.cumulated.Round(time.Second)))
			}
			b.WriteString("\n")
		}

		// Sessions by database
		if len(m.Connections.SessionsByDatabase) > 0 {
			b.WriteString("### Session Duration by Database\n\n")
			b.WriteString("| Database | Sessions | Min | Max | Avg | Median | Cumulated |\n")
			b.WriteString("|---|---:|---|---|---|---|---:|\n")

			type dbStats struct {
				database  string
				stats     analysis.DurationStats
				cumulated time.Duration
			}
			var sortedDBs []dbStats
			for db, durations := range m.Connections.SessionsByDatabase {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				sortedDBs = append(sortedDBs, dbStats{
					database: db, stats: stats, cumulated: cumulated,
				})
			}
			sort.Slice(sortedDBs, func(i, j int) bool {
				return sortedDBs[i].stats.Count > sortedDBs[j].stats.Count
			})

			limit := 10
			for i := 0; i < limit && i < len(sortedDBs); i++ {
				d := sortedDBs[i]
				b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s | %s | %s |\n",
					d.database,
					d.stats.Count,
					d.stats.Min.Round(time.Second),
					d.stats.Max.Round(time.Second),
					d.stats.Avg.Round(time.Second),
					d.stats.Median.Round(time.Second),
					d.cumulated.Round(time.Second)))
			}
			b.WriteString("\n")
		}

		// Sessions by host
		if len(m.Connections.SessionsByHost) > 0 {
			b.WriteString("### Session Duration by Host\n\n")
			b.WriteString("| Host | Sessions | Min | Max | Avg | Median | Cumulated |\n")
			b.WriteString("|---|---:|---|---|---|---|---:|\n")

			type hostStats struct {
				host      string
				stats     analysis.DurationStats
				cumulated time.Duration
			}
			var sortedHosts []hostStats
			for host, durations := range m.Connections.SessionsByHost {
				stats := analysis.CalculateDurationStats(durations)
				var cumulated time.Duration
				for _, d := range durations {
					cumulated += d
				}
				sortedHosts = append(sortedHosts, hostStats{
					host: host, stats: stats, cumulated: cumulated,
				})
			}
			sort.Slice(sortedHosts, func(i, j int) bool {
				return sortedHosts[i].stats.Count > sortedHosts[j].stats.Count
			})

			limit := 10
			for i := 0; i < limit && i < len(sortedHosts); i++ {
				h := sortedHosts[i]
				b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s | %s | %s |\n",
					h.host,
					h.stats.Count,
					h.stats.Min.Round(time.Second),
					h.stats.Max.Round(time.Second),
					h.stats.Avg.Round(time.Second),
					h.stats.Median.Round(time.Second),
					h.cumulated.Round(time.Second)))
			}
			b.WriteString("\n")
		}
	}

	// ============================================================================
	// CLIENTS
	// ============================================================================
	if has("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0 || m.UniqueEntities.UniqueHosts > 0) {
		b.WriteString("## CLIENTS\n\n")
		b.WriteString(fmt.Sprintf("- **Unique DBs**: %d\n", m.UniqueEntities.UniqueDbs))
		b.WriteString(fmt.Sprintf("- **Unique Users**: %d\n", m.UniqueEntities.UniqueUsers))
		b.WriteString(fmt.Sprintf("- **Unique Apps**: %d\n", m.UniqueEntities.UniqueApps))
		b.WriteString(fmt.Sprintf("- **Unique Hosts**: %d\n\n", m.UniqueEntities.UniqueHosts))

		totalLogs := m.Global.Count

		if m.UniqueEntities.UniqueUsers > 0 && m.UniqueEntities.UserCounts != nil {
			b.WriteString("### USERS\n\n")
			b.WriteString("| User | Count | % |\n")
			b.WriteString("|---|---:|---:|\n")
			sortedUsers := analysis.SortByCount(m.UniqueEntities.UserCounts)
			for _, item := range sortedUsers {
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% |\n", item.Name, item.Count, percentage))
			}
			b.WriteString("\n")
		}

		if m.UniqueEntities.UniqueApps > 0 && m.UniqueEntities.AppCounts != nil {
			b.WriteString("### APPS\n\n")
			b.WriteString("| App | Count | % |\n")
			b.WriteString("|---|---:|---:|\n")
			sortedApps := analysis.SortByCount(m.UniqueEntities.AppCounts)
			for _, item := range sortedApps {
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% |\n", item.Name, item.Count, percentage))
			}
			b.WriteString("\n")
		}

		if m.UniqueEntities.UniqueDbs > 0 && m.UniqueEntities.DBCounts != nil {
			b.WriteString("### DATABASES\n\n")
			b.WriteString("| Database | Count | % |\n")
			b.WriteString("|---|---:|---:|\n")
			sortedDBs := analysis.SortByCount(m.UniqueEntities.DBCounts)
			for _, item := range sortedDBs {
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% |\n", item.Name, item.Count, percentage))
			}
			b.WriteString("\n")
		}

		if m.UniqueEntities.UniqueHosts > 0 && m.UniqueEntities.HostCounts != nil {
			b.WriteString("### HOSTS\n\n")
			b.WriteString("| Host | Count | % |\n")
			b.WriteString("|---|---:|---:|\n")
			sortedHosts := analysis.SortByCount(m.UniqueEntities.HostCounts)
			for _, item := range sortedHosts {
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% |\n", item.Name, item.Count, percentage))
			}
			b.WriteString("\n")
		}

		if len(m.UniqueEntities.UserDbCombos) > 0 {
			b.WriteString("### USER × DATABASE\n\n")
			b.WriteString("| User | Database | Count | % |\n")
			b.WriteString("|---|---|---:|---:|\n")
			sortedCombos := analysis.SortByCount(m.UniqueEntities.UserDbCombos)
			for _, item := range sortedCombos {
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				parts := strings.SplitN(item.Name, "|", 2)
				if len(parts) == 2 {
					b.WriteString(fmt.Sprintf("| %s | %s | %d | %.1f%% |\n", parts[0], parts[1], item.Count, percentage))
				}
			}
			b.WriteString("\n")
		}

		if len(m.UniqueEntities.UserHostCombos) > 0 {
			b.WriteString("### USER × HOST\n\n")
			b.WriteString("| User | Host | Count | % |\n")
			b.WriteString("|---|---|---:|---:|\n")
			sortedCombos := analysis.SortByCount(m.UniqueEntities.UserHostCombos)
			for _, item := range sortedCombos {
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				parts := strings.SplitN(item.Name, "|", 2)
				if len(parts) == 2 {
					b.WriteString(fmt.Sprintf("| %s | %s | %d | %.1f%% |\n", parts[0], parts[1], item.Count, percentage))
				}
			}
			b.WriteString("\n")
		}
	}

	// ============================================================================
	// FULL MODE: SQL OVERVIEW and SQL PERFORMANCE at the end
	// ============================================================================
	if full && m.SQL.TotalQueries > 0 {
		// SQL Overview section
		b.WriteString("## SQL OVERVIEW\n\n")
		exportSQLOverviewMarkdownTo(&b, m.SQL)

		// SQL Performance section (enriched with top queries)
		// Don't include TempFiles/Locks here - they're already shown in their respective sections above
		b.WriteString("## SQL PERFORMANCE\n\n")
		exportSqlSummaryMarkdownTo(&b, m.SQL, analysis.TempFileMetrics{}, analysis.LockMetrics{})
	}

	fmt.Fprintln(w, b.String())
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

// printConcurrentHistogramMarkdown prints a histogram with peak times in markdown format.
func printConcurrentHistogramMarkdown(b *strings.Builder, data map[string]int, title string, scaleFactor int, orderedLabels []string, peakTimes map[string]time.Time) {
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

		if v == 0 {
			b.WriteString(fmt.Sprintf("%s | -\n", label))
		} else {
			peakStr := ""
			if pt, ok := peakTimes[label]; ok && !pt.IsZero() {
				peakStr = fmt.Sprintf("(%02d:%02d)", pt.Hour(), pt.Minute())
			}
			b.WriteString(fmt.Sprintf("%s | %s %d %s\n", label, bar, v, peakStr))
		}
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

// printLockStatsMarkdown prints lock type or resource type statistics in markdown table format.
func printLockStatsMarkdown(b *strings.Builder, stats map[string]int, total int) {
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

	// Print entries
	for _, p := range pairs {
		percentage := (float64(p.count) / float64(total)) * 100
		b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% |\n", p.name, p.count, percentage))
	}
}

// printAcquiredLockQueriesMarkdown prints queries with acquired locks in markdown table format.
func printAcquiredLockQueriesMarkdown(b *strings.Builder, queryStats map[string]*analysis.LockQueryStat, limit int) {
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
		truncatedQuery := truncateQuery(stat.NormalizedQuery, 60)
		avgWait := stat.AcquiredWaitTime / float64(stat.AcquiredCount)
		b.WriteString(fmt.Sprintf("| %s | %s | %d | %.2f | %.2f |\n",
			stat.ID,
			truncatedQuery,
			stat.AcquiredCount,
			avgWait,
			stat.AcquiredWaitTime))
	}
}

// printStillWaitingLockQueriesMarkdown prints queries with locks still waiting in markdown table format.
func printStillWaitingLockQueriesMarkdown(b *strings.Builder, queryStats map[string]*analysis.LockQueryStat, limit int) {
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
		truncatedQuery := truncateQuery(stat.NormalizedQuery, 60)
		avgWait := stat.StillWaitingTime / float64(stat.StillWaitingCount)
		b.WriteString(fmt.Sprintf("| %s | %s | %d | %.2f | %.2f |\n",
			stat.ID,
			truncatedQuery,
			stat.StillWaitingCount,
			avgWait,
			stat.StillWaitingTime))
	}
}

// printMostFrequentWaitingQueriesMarkdown prints all queries that experienced lock waits in markdown table format.
func printMostFrequentWaitingQueriesMarkdown(b *strings.Builder, queryStats map[string]*analysis.LockQueryStat, limit int) {
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
		truncatedQuery := truncateQuery(pair.stat.NormalizedQuery, 60)
		avgWait := pair.totalWait / float64(pair.totalLocks)
		b.WriteString(fmt.Sprintf("| %s | %s | %d | %.2f | %.2f |\n",
			pair.stat.ID,
			truncatedQuery,
			pair.totalLocks,
			avgWait,
			pair.totalWait))
	}
}

// ExportSqlSummaryMarkdown produces a markdown report for --sql-summary
func ExportSqlSummaryMarkdown(w io.Writer, m analysis.SqlMetrics, tempFiles analysis.TempFileMetrics, locks analysis.LockMetrics) {
	var b strings.Builder

	// ... (content) ...
	// I'll be more specific to avoid error
	
	// Compute top 1% slowest queries
	top1Slow := 0
	if len(m.Executions) > 0 {
		threshold := m.P99QueryDuration
		for _, exec := range m.Executions {
			if exec.Duration >= threshold {
				top1Slow++
			}
		}
	}

	// SQL PERFORMANCE section
	b.WriteString("## SQL PERFORMANCE\n\n")

	// Query load histogram
	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		queryLoad, unit, scale := computeQueryLoadHistogram(m)
		printHistogramMarkdown(&b, queryLoad, "Query load distribution", unit, scale, nil)
	}

	// Key metrics table
	b.WriteString("|  |  |  |  |\n")
	b.WriteString("|---|---:|---|---:|\n")
	b.WriteString(fmt.Sprintf("| Total query duration | %s | Total queries parsed | %d |\n",
		formatQueryDuration(m.SumQueryDuration), m.TotalQueries))
	b.WriteString(fmt.Sprintf("| Total unique queries | %d | Top 1%% slow queries | %d |\n",
		m.UniqueQueries, top1Slow))
	b.WriteString(fmt.Sprintf("| Query max duration | %s | Query min duration | %s |\n",
		formatQueryDuration(m.MaxQueryDuration), formatQueryDuration(m.MinQueryDuration)))
	b.WriteString(fmt.Sprintf("| Query median duration | %s | Query 99%% max duration | %s |\n\n",
		formatQueryDuration(m.MedianQueryDuration), formatQueryDuration(m.P99QueryDuration)))

	// Duration histogram
	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		hist, unit, scale := computeQueryDurationHistogram(m)
		printHistogramMarkdown(&b, hist, "Query duration distribution", unit, scale,
			[]string{"< 1 ms", "< 10 ms", "< 100 ms", "< 1 s", "< 10 s", ">= 10 s"})
	}

	// Query stats tables
	b.WriteString("### Query Statistics\n\n")
	printQueryStatsMarkdown(&b, m.QueryStats)

	// TEMP FILES section
	if len(tempFiles.QueryStats) > 0 {
		b.WriteString("## TEMP FILES\n\n")
		b.WriteString("| SQLID | Normalized Query | Count | Total Size |\n")
		b.WriteString("|---|---|---:|---:|\n")

		// Sort by total size descending
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
		for i := 0; i < limit; i++ {
			stat := queries[i].stat
			truncatedQuery := truncateQuery(stat.NormalizedQuery, 60)
			b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n",
				stat.ID,
				truncatedQuery,
				stat.Count,
				formatBytes(stat.TotalSize)))
		}
		b.WriteString("\n")
	}

	// LOCKS section
	if len(locks.QueryStats) > 0 {
		b.WriteString("## LOCKS\n\n")

		// Acquired locks by query
		hasAcquired := false
		for _, stat := range locks.QueryStats {
			if stat.AcquiredCount > 0 {
				hasAcquired = true
				break
			}
		}
		if hasAcquired {
			b.WriteString("### Acquired Locks by Query\n\n")
			b.WriteString("| SQLID | Normalized Query | Locks | Avg Wait (ms) | Total Wait (ms) |\n")
			b.WriteString("|---|---|---:|---:|---:|\n")
			printAcquiredLockQueriesMarkdown(&b, locks.QueryStats, 10)
			b.WriteString("\n")
		}

		// Locks still waiting by query
		hasStillWaiting := false
		for _, stat := range locks.QueryStats {
			if stat.StillWaitingCount > 0 {
				hasStillWaiting = true
				break
			}
		}
		if hasStillWaiting {
			b.WriteString("### Locks Still Waiting by Query\n\n")
			b.WriteString("| SQLID | Normalized Query | Locks | Avg Wait (ms) | Total Wait (ms) |\n")
			b.WriteString("|---|---|---:|---:|---:|\n")
			printStillWaitingLockQueriesMarkdown(&b, locks.QueryStats, 10)
			b.WriteString("\n")
		}

		// Most frequent waiting queries
		hasWaiting := false
		for _, stat := range locks.QueryStats {
			if stat.AcquiredCount > 0 || stat.StillWaitingCount > 0 {
				hasWaiting = true
				break
			}
		}
		if hasWaiting {
			b.WriteString("### Most Frequent Waiting Queries\n\n")
			b.WriteString("| SQLID | Normalized Query | Locks | Avg Wait (ms) | Total Wait (ms) |\n")
			b.WriteString("|---|---|---:|---:|---:|\n")
			printMostFrequentWaitingQueriesMarkdown(&b, locks.QueryStats, 10)
			b.WriteString("\n")
		}
	}

	fmt.Fprintln(w, b.String())
}

// ExportSqlDetailMarkdown produces a markdown report for --sql-detail
func ExportSqlDetailMarkdown(w io.Writer, m analysis.AggregatedMetrics, queryIDs []string) {
	var b strings.Builder

	for _, qid := range queryIDs {
		// ... (content) ...
		// I'll be more specific to avoid error
		
		// Collect metrics for this query ID
		var sqlStat *analysis.QueryStat
		var tempStat *analysis.TempFileQueryStat
		var lockStat *analysis.LockQueryStat

		// Search in SQL metrics
		for _, qs := range m.SQL.QueryStats {
			if qs.ID == qid {
				sqlStat = qs
				break
			}
		}

		// Search in tempfiles metrics
		for _, ts := range m.TempFiles.QueryStats {
			if ts.ID == qid {
				tempStat = ts
				break
			}
		}

		// Search in locks metrics
		for _, ls := range m.Locks.QueryStats {
			if ls.ID == qid {
				lockStat = ls
				break
			}
		}

		// If query not found anywhere, skip
		if sqlStat == nil && tempStat == nil && lockStat == nil {
			continue
		}

		// Get normalized query and type
		normalizedQuery := ""
		queryType := analysis.QueryTypeFromID(qid)
		rawQuery := ""

		if sqlStat != nil {
			normalizedQuery = sqlStat.NormalizedQuery
			rawQuery = sqlStat.RawQuery
		} else if tempStat != nil {
			normalizedQuery = tempStat.NormalizedQuery
		} else if lockStat != nil {
			normalizedQuery = lockStat.NormalizedQuery
		}

		// SQL DETAILS section
		b.WriteString(fmt.Sprintf("## SQL DETAILS: %s\n\n", qid))
		b.WriteString(fmt.Sprintf("- **Id**: %s\n", qid))
		b.WriteString(fmt.Sprintf("- **Query Type**: %s\n", queryType))
		if sqlStat != nil {
			b.WriteString(fmt.Sprintf("- **Count**: %d\n", sqlStat.Count))
		}
		b.WriteString("\n")

		// Execution histogram (if > 1 execution)
		if sqlStat != nil && sqlStat.Count > 1 {
			execHist, execUnit, execScale := computeSingleQueryExecutionHistogram(m.SQL.Executions, qid)
			if execHist != nil {
				printHistogramMarkdown(&b, execHist, "Query count", execUnit, execScale, nil)
			}
		}

		// TIME section
		if sqlStat != nil {
			b.WriteString("### TIME\n\n")

			// Cumulative time histogram (if > 1 execution)
			if sqlStat.Count > 1 {
				timeHist, timeUnit, timeScale := computeSingleQueryTimeHistogram(m.SQL.Executions, qid)
				if timeHist != nil {
					printHistogramMarkdown(&b, timeHist, "Cumulative time", timeUnit, timeScale, nil)
				}
			}

			// Duration distribution histogram (if > 1 execution)
			if sqlStat.Count > 1 {
				durationHist, durationUnit, durationScale, durationLabels := computeSingleQueryDurationDistribution(m.SQL.Executions, qid)
				if durationHist != nil {
					printHistogramMarkdown(&b, durationHist, "Query duration distribution", durationUnit, durationScale, durationLabels)
				}
			}

			// Calculate min duration
			minDuration := sqlStat.MaxTime
			for _, exec := range m.SQL.Executions {
				if exec.QueryID == qid && exec.Duration < minDuration {
					minDuration = exec.Duration
				}
			}

			b.WriteString(fmt.Sprintf("- **Total Duration**: %s\n", formatQueryDuration(sqlStat.TotalTime)))
			b.WriteString(fmt.Sprintf("- **Min Duration**: %s\n", formatQueryDuration(minDuration)))
			b.WriteString(fmt.Sprintf("- **Median Duration**: %s\n", formatQueryDuration(sqlStat.AvgTime)))
			b.WriteString(fmt.Sprintf("- **Max Duration**: %s\n\n", formatQueryDuration(sqlStat.MaxTime)))
		}

		// TEMP FILES section
		if tempStat != nil {
			b.WriteString("### TEMP FILES\n\n")

			// Size histogram (if > 1 event)
			if tempStat.Count > 1 {
				tempSizeHist, tempSizeUnit, tempSizeScale := computeSingleQueryTempFileHistogram(m.TempFiles.Events, qid)
				if tempSizeHist != nil {
					printHistogramMarkdown(&b, tempSizeHist, "Temp files size", tempSizeUnit, tempSizeScale, nil)
				}
			}

			// Count histogram (if > 1 event)
			if tempStat.Count > 1 {
				tempCountHist, tempCountUnit, tempCountScale := computeSingleQueryTempFileCountHistogram(m.TempFiles.Events, qid)
				if tempCountHist != nil {
					printHistogramMarkdown(&b, tempCountHist, "Temp files count", tempCountUnit, tempCountScale, nil)
				}
			}

			// Calculate min/max/avg sizes
			var minSize, maxSize int64
			minSize = 9223372036854775807 // MaxInt64
			for _, event := range m.TempFiles.Events {
				if event.QueryID == qid {
					size := int64(event.Size)
					if size < minSize {
						minSize = size
					}
					if size > maxSize {
						maxSize = size
					}
				}
			}
			avgSize := tempStat.TotalSize / int64(tempStat.Count)

			b.WriteString(fmt.Sprintf("- **Temp Files count**: %d\n", tempStat.Count))
			b.WriteString(fmt.Sprintf("- **Temp File min size**: %s\n", formatBytes(minSize)))
			b.WriteString(fmt.Sprintf("- **Temp File max size**: %s\n", formatBytes(maxSize)))
			b.WriteString(fmt.Sprintf("- **Temp File avg size**: %s\n", formatBytes(avgSize)))
			b.WriteString(fmt.Sprintf("- **Temp Files size**: %s\n\n", formatBytes(tempStat.TotalSize)))
		}

		// LOCKS section
		if lockStat != nil {
			b.WriteString("### LOCKS\n\n")
			b.WriteString(fmt.Sprintf("- **Acquired Locks**: %d\n", lockStat.AcquiredCount))
			b.WriteString(fmt.Sprintf("- **Acquired Wait Time**: %s\n", formatQueryDuration(lockStat.AcquiredWaitTime)))
			b.WriteString(fmt.Sprintf("- **Still Waiting Locks**: %d\n", lockStat.StillWaitingCount))
			b.WriteString(fmt.Sprintf("- **Still Waiting Time**: %s\n", formatQueryDuration(lockStat.StillWaitingTime)))
			b.WriteString(fmt.Sprintf("- **Total Wait Time**: %s\n\n", formatQueryDuration(lockStat.TotalWaitTime)))
		}

		// Normalized query
		if normalizedQuery != "" {
			b.WriteString("### Normalized Query\n\n")
			b.WriteString("```sql\n")
			b.WriteString(formatSQL(normalizedQuery))
			b.WriteString("\n```\n\n")
		}

		// Example query
		if rawQuery != "" {
			b.WriteString("### Example Query\n\n")
			b.WriteString("```sql\n")
			b.WriteString(rawQuery)
			b.WriteString("\n```\n\n")
		}
	}

	fmt.Fprintln(w, b.String())
}

// ExportSqlOverviewMarkdown exports SQL query type overview statistics in Markdown format.
// This provides query type statistics with dimensional breakdowns (SELECT, INSERT, UPDATE, DELETE, etc.)
func ExportSqlOverviewMarkdown(w io.Writer, m analysis.SqlMetrics) {
	var b strings.Builder

	b.WriteString("# SQL QUERY OVERVIEW\n\n")

	// ... (rest of logic) ...
	// Again, providing full body correctly to avoid corruption.
	
	// Global statistics
	b.WriteString("## Global Statistics\n\n")
	b.WriteString("|  |  |  |  |\n")
	b.WriteString("|---|---:|---|---:|\n")
	b.WriteString(fmt.Sprintf("| Total queries | %d | Unique queries | %d |\n",
		m.TotalQueries, m.UniqueQueries))
	b.WriteString(fmt.Sprintf("| Total duration | %s | Median duration | %s |\n",
		formatQueryDuration(m.SumQueryDuration), formatQueryDuration(m.MedianQueryDuration)))
	b.WriteString(fmt.Sprintf("| Min duration | %s | Max duration | %s |\n",
		formatQueryDuration(m.MinQueryDuration), formatQueryDuration(m.MaxQueryDuration)))
	b.WriteString(fmt.Sprintf("| 99th percentile | %s | | |\n\n",
		formatQueryDuration(m.P99QueryDuration)))

	// Query Category Summary - EN PREMIER
	if len(m.QueryTypeStats) > 0 {
		b.WriteString("## Query Category Summary\n\n")
		b.WriteString("| Category | Count | % | Total Time |\n")
		b.WriteString("|---|---:|---:|---:|\n")

		// Aggregate by category
		categoryStats := make(map[string]struct {
			Count     int
			TotalTime float64
		})
		for _, ts := range m.QueryTypeStats {
			cat := categoryStats[ts.Category]
			cat.Count += ts.Count
			cat.TotalTime += ts.TotalTime
			categoryStats[ts.Category] = cat
		}

		// Sort categories by count descending
		categories := make([]string, 0, len(categoryStats))
		for cat := range categoryStats {
			categories = append(categories, cat)
		}
		sort.Slice(categories, func(i, j int) bool {
			return categoryStats[categories[i]].Count > categoryStats[categories[j]].Count
		})

		for _, cat := range categories {
			stats := categoryStats[cat]
			pct := 0.0
			if m.TotalQueries > 0 {
				pct = float64(stats.Count) / float64(m.TotalQueries) * 100
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% | %s |\n",
				cat, stats.Count, pct, formatQueryDuration(stats.TotalTime)))
		}
		b.WriteString("\n")
	}

	// Query Type Distribution - EN SECOND
	if len(m.QueryTypeStats) > 0 {
		b.WriteString("## Query Type Distribution\n\n")
		b.WriteString("| Type | Count | % | Total Time | Avg Time | Max Time |\n")
		b.WriteString("|---|---:|---:|---:|---:|---:|\n")

		// Sort by count descending
		types := make([]*analysis.QueryTypeStat, 0, len(m.QueryTypeStats))
		for _, ts := range m.QueryTypeStats {
			types = append(types, ts)
		}
		sort.Slice(types, func(i, j int) bool {
			return types[i].Count > types[j].Count
		})

		for _, ts := range types {
			pct := 0.0
			if m.TotalQueries > 0 {
				pct = float64(ts.Count) / float64(m.TotalQueries) * 100
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% | %s | %s | %s |\n",
				ts.Type, ts.Count, pct,
				formatQueryDuration(ts.TotalTime),
				formatQueryDuration(ts.AvgTime),
				formatQueryDuration(ts.MaxTime)))
		}
		b.WriteString("\n")
	}

	// Breakdowns by dimension
	exportQueryTypeBreakdownMarkdown(&b, "Per Database", m.QueryTypesByDatabase)
	exportQueryTypeBreakdownMarkdown(&b, "Per User", m.QueryTypesByUser)
	exportQueryTypeBreakdownMarkdown(&b, "Per Host", m.QueryTypesByHost)
	exportQueryTypeBreakdownMarkdown(&b, "Per Application", m.QueryTypesByApp)

	fmt.Fprintln(w, b.String())
}

// exportQueryTypeBreakdownMarkdown writes query type breakdown for a dimension (database, user, host, app)
func exportQueryTypeBreakdownMarkdown(b *strings.Builder, title string, breakdown map[string]map[string]*analysis.QueryTypeCount) {
	if len(breakdown) == 0 {
		return
	}

	b.WriteString("## " + title + "\n\n")

	// Sort dimensions by total count (descending)
	type dimStats struct {
		name      string
		count     int
		totalTime float64
	}
	var dimensions []dimStats
	for dimName, types := range breakdown {
		var totalCount int
		var totalTime float64
		for _, tc := range types {
			totalCount += tc.Count
			totalTime += tc.TotalTime
		}
		dimensions = append(dimensions, dimStats{dimName, totalCount, totalTime})
	}
	sort.Slice(dimensions, func(i, j int) bool {
		return dimensions[i].count > dimensions[j].count
	})

	// Print each dimension with its query types
	for _, dim := range dimensions {
		b.WriteString(fmt.Sprintf("### %s (%d queries, %s)\n\n",
			dim.name,
			dim.count,
			formatQueryDuration(dim.totalTime)))

		b.WriteString("| Query Type | Count | Total Time |\n")
		b.WriteString("|---|---:|---:|\n")

		// Get all query types for this dimension
		types := breakdown[dim.name]
		var typeList []struct {
			name      string
			count     int
			totalTime float64
		}
		for typeName, tc := range types {
			typeList = append(typeList, struct {
				name      string
				count     int
				totalTime float64
			}{typeName, tc.Count, tc.TotalTime})
		}

		// Sort by count descending
		sort.Slice(typeList, func(i, j int) bool {
			return typeList[i].count > typeList[j].count
		})

		// Print query types
		for _, t := range typeList {
			b.WriteString(fmt.Sprintf("| %s | %d | %s |\n",
				t.name,
				t.count,
				formatQueryDuration(t.totalTime)))
		}
		b.WriteString("\n")
	}
}

// exportSQLOverviewMarkdownTo writes SQL overview content to a strings.Builder.
// Used by ExportMarkdown in full mode.
func exportSQLOverviewMarkdownTo(b *strings.Builder, m analysis.SqlMetrics) {
	// Global statistics
	b.WriteString("### Global Statistics\n\n")
	b.WriteString("|  |  |  |  |\n")
	b.WriteString("|---|---:|---|---:|\n")
	b.WriteString(fmt.Sprintf("| Total queries | %d | Unique queries | %d |\n",
		m.TotalQueries, m.UniqueQueries))
	b.WriteString(fmt.Sprintf("| Total duration | %s | Median duration | %s |\n",
		formatQueryDuration(m.SumQueryDuration), formatQueryDuration(m.MedianQueryDuration)))
	b.WriteString(fmt.Sprintf("| Min duration | %s | Max duration | %s |\n",
		formatQueryDuration(m.MinQueryDuration), formatQueryDuration(m.MaxQueryDuration)))
	b.WriteString(fmt.Sprintf("| 99th percentile | %s | | |\n\n",
		formatQueryDuration(m.P99QueryDuration)))

	// Query Category Summary
	if len(m.QueryTypeStats) > 0 {
		b.WriteString("### Query Category Summary\n\n")
		b.WriteString("| Category | Count | % | Total Time |\n")
		b.WriteString("|---|---:|---:|---:|\n")

		// Aggregate by category
		categoryStats := make(map[string]struct {
			Count     int
			TotalTime float64
		})
		for _, ts := range m.QueryTypeStats {
			cat := categoryStats[ts.Category]
			cat.Count += ts.Count
			cat.TotalTime += ts.TotalTime
			categoryStats[ts.Category] = cat
		}

		// Sort categories by count descending
		categories := make([]string, 0, len(categoryStats))
		for cat := range categoryStats {
			categories = append(categories, cat)
		}
		sort.Slice(categories, func(i, j int) bool {
			return categoryStats[categories[i]].Count > categoryStats[categories[j]].Count
		})

		for _, cat := range categories {
			stats := categoryStats[cat]
			pct := 0.0
			if m.TotalQueries > 0 {
				pct = float64(stats.Count) / float64(m.TotalQueries) * 100
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% | %s |\n",
				cat, stats.Count, pct, formatQueryDuration(stats.TotalTime)))
		}
		b.WriteString("\n")
	}

	// Query Type Distribution
	if len(m.QueryTypeStats) > 0 {
		b.WriteString("### Query Type Distribution\n\n")
		b.WriteString("| Type | Count | % | Total Time | Avg Time | Max Time |\n")
		b.WriteString("|---|---:|---:|---:|---:|---:|\n")

		// Sort by count descending
		types := make([]*analysis.QueryTypeStat, 0, len(m.QueryTypeStats))
		for _, ts := range m.QueryTypeStats {
			types = append(types, ts)
		}
		sort.Slice(types, func(i, j int) bool {
			return types[i].Count > types[j].Count
		})

		for _, ts := range types {
			pct := 0.0
			if m.TotalQueries > 0 {
				pct = float64(ts.Count) / float64(m.TotalQueries) * 100
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% | %s | %s | %s |\n",
				ts.Type, ts.Count, pct,
				formatQueryDuration(ts.TotalTime),
				formatQueryDuration(ts.AvgTime),
				formatQueryDuration(ts.MaxTime)))
		}
		b.WriteString("\n")
	}

	// Breakdowns by dimension
	exportQueryTypeBreakdownMarkdown(b, "Per Database", m.QueryTypesByDatabase)
	exportQueryTypeBreakdownMarkdown(b, "Per User", m.QueryTypesByUser)
	exportQueryTypeBreakdownMarkdown(b, "Per Host", m.QueryTypesByHost)
	exportQueryTypeBreakdownMarkdown(b, "Per Application", m.QueryTypesByApp)
}

// exportSqlSummaryMarkdownTo writes SQL performance content to a strings.Builder.
// Used by ExportMarkdown in full mode.
func exportSqlSummaryMarkdownTo(b *strings.Builder, m analysis.SqlMetrics, tempFiles analysis.TempFileMetrics, locks analysis.LockMetrics) {
	// Compute top 1% slowest queries
	top1Slow := 0
	if len(m.Executions) > 0 {
		threshold := m.P99QueryDuration
		for _, exec := range m.Executions {
			if exec.Duration >= threshold {
				top1Slow++
			}
		}
	}

	// Query load histogram
	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		queryLoad, unit, scale := computeQueryLoadHistogram(m)
		printHistogramMarkdown(b, queryLoad, "Query load distribution", unit, scale, nil)
	}

	// Key metrics table
	b.WriteString("|  |  |  |  |\n")
	b.WriteString("|---|---:|---|---:|\n")
	b.WriteString(fmt.Sprintf("| Total query duration | %s | Total queries parsed | %d |\n",
		formatQueryDuration(m.SumQueryDuration), m.TotalQueries))
	b.WriteString(fmt.Sprintf("| Total unique queries | %d | Top 1%% slow queries | %d |\n",
		m.UniqueQueries, top1Slow))
	b.WriteString(fmt.Sprintf("| Query max duration | %s | Query min duration | %s |\n",
		formatQueryDuration(m.MaxQueryDuration), formatQueryDuration(m.MinQueryDuration)))
	b.WriteString(fmt.Sprintf("| Query median duration | %s | Query 99%% max duration | %s |\n\n",
		formatQueryDuration(m.MedianQueryDuration), formatQueryDuration(m.P99QueryDuration)))

	// Duration histogram
	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		hist, unit, scale := computeQueryDurationHistogram(m)
		printHistogramMarkdown(b, hist, "Query duration distribution", unit, scale,
			[]string{"< 1 ms", "< 10 ms", "< 100 ms", "< 1 s", "< 10 s", ">= 10 s"})
	}

	// Query stats tables
	b.WriteString("### Query Statistics\n\n")
	printQueryStatsMarkdown(b, m.QueryStats)

	// TEMP FILES section
	if len(tempFiles.QueryStats) > 0 {
		b.WriteString("### Queries Generating Temp Files\n\n")
		b.WriteString("| SQLID | Normalized Query | Count | Total Size |\n")
		b.WriteString("|---|---|---:|---:|\n")

		// Sort by total size descending
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
		for i := 0; i < limit; i++ {
			stat := queries[i].stat
			truncatedQuery := truncateQuery(stat.NormalizedQuery, 60)
			b.WriteString(fmt.Sprintf("| %s | %s | %d | %s |\n",
				stat.ID,
				truncatedQuery,
				stat.Count,
				formatBytes(stat.TotalSize)))
		}
		b.WriteString("\n")
	}

	// LOCKS section
	if len(locks.QueryStats) > 0 {
		// Acquired locks by query
		hasAcquired := false
		for _, stat := range locks.QueryStats {
			if stat.AcquiredCount > 0 {
				hasAcquired = true
				break
			}
		}
		if hasAcquired {
			b.WriteString("### Acquired Locks by Query\n\n")
			b.WriteString("| SQLID | Query | Locks | Avg Wait | Total Wait |\n")
			b.WriteString("|---|---|---:|---:|---:|\n")
			printAcquiredLockQueriesMarkdown(b, locks.QueryStats, 5)
			b.WriteString("\n")
		}

		// Locks still waiting by query
		hasStillWaiting := false
		for _, stat := range locks.QueryStats {
			if stat.StillWaitingCount > 0 {
				hasStillWaiting = true
				break
			}
		}
		if hasStillWaiting {
			b.WriteString("### Locks Still Waiting by Query\n\n")
			b.WriteString("| SQLID | Query | Locks | Avg Wait | Total Wait |\n")
			b.WriteString("|---|---|---:|---:|---:|\n")
			printStillWaitingLockQueriesMarkdown(b, locks.QueryStats, 5)
			b.WriteString("\n")
		}

		// Most frequent lock waiting queries
		hasWaiting := false
		for _, stat := range locks.QueryStats {
			if stat.AcquiredCount > 0 || stat.StillWaitingCount > 0 {
				hasWaiting = true
				break
			}
		}
		if hasWaiting {
			b.WriteString("### Most Frequent Waiting Queries\n\n")
			b.WriteString("| SQLID | Query | Locks | Avg Wait | Total Wait |\n")
			b.WriteString("|---|---|---:|---:|---:|\n")
			printMostFrequentWaitingQueriesMarkdown(b, locks.QueryStats, 5)
			b.WriteString("\n")
		}
	}
}
