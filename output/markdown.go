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

					if shouldPrintHeader {
						classHeader := classCode
						if classCode != "Unclassified" {
							desc := analysis.GetErrorClassDescription(classCode)
							classHeader = fmt.Sprintf("%s - %s", classCode, desc)
						}
						b.WriteString(fmt.Sprintf("  - **%s**\n", classHeader))
					}
					// Events at the same indent as the class header —
					// pattern IDs in the left margin, flat under the
					// class label.
					indent := "  "

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

						idLead := ""
						if e.ID != "" {
							// Lead with the handle in italic — left
							// margin label, mirrors the italic-grey
							// column position used in text output.
							idLead = "*" + e.ID + "* "
						}
						b.WriteString(fmt.Sprintf("%s- %s`%s` (%d) [%.1f%%]\n",
							indent, idLead, msg, e.Count, localPct))
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
		b.WriteString(fmt.Sprintf("- **Cumulative temp file size**: %s\n", FormatBytes(m.TempFiles.TotalSize)))
		b.WriteString(fmt.Sprintf("- **Average temp file size**: %s\n", FormatBytes(avgSize)))
		b.WriteString(fmt.Sprintf("- **Max temp file size**: %s\n\n", FormatBytes(m.TempFiles.MaxSize)))

		// Queries generating temp files (in detailed/full mode)
		if (full || !has("all")) && len(m.TempFiles.QueryStats) > 0 {
			b.WriteString("### Queries Generating Temp Files\n\n")
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
			rows := make([][]string, 0, limit)
			for i := 0; i < limit; i++ {
				s := queries[i].stat
				rows = append(rows, []string{s.ID, truncateQuery(s.NormalizedQuery, 50), fmt.Sprintf("%d", s.Count), FormatBytes(s.TotalSize)})
			}
			mdTable(&b, []string{"SQLID", "Query", "Count", "Total Size"}, "llrr", rows)
			b.WriteString("\n")
		}
	}

	// ============================================================================
	// LOCKS
	// ============================================================================
	if has("locks") && m.Locks.TotalEvents > 0 {
		b.WriteString("## LOCKS\n\n")

		avgWaitTime := 0.0
		if m.Locks.TotalEvents > 0 {
			avgWaitTime = m.Locks.TotalWaitTime / float64(m.Locks.TotalEvents)
		}

		b.WriteString(fmt.Sprintf("- **Total lock events**: %d\n", m.Locks.TotalEvents))
		b.WriteString(fmt.Sprintf("- **Still waiting**: %d\n", m.Locks.WaitingEvents))
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

		// Relation distribution
		if len(m.Locks.RelationStats) > 0 {
			b.WriteString("### Relations\n\n")
			b.WriteString("| Relation | Count | Percentage |\n")
			b.WriteString("|---|---:|---:|\n")
			printLockStatsMarkdown(&b, m.Locks.RelationStats, m.Locks.TotalEvents)
			b.WriteString("\n")
		}

		// Waiting queries
		if len(m.Locks.QueryStats) > 0 {
			type queryPair struct{ stat *analysis.LockQueryStat }
			var pairs []queryPair
			for _, stat := range m.Locks.QueryStats {
				if stat.AcquiredCount > 0 || stat.StillWaitingCount > 0 {
					pairs = append(pairs, queryPair{stat})
				}
			}
			if len(pairs) > 0 {
				sort.Slice(pairs, func(i, j int) bool {
					return pairs[i].stat.TotalWaitTime > pairs[j].stat.TotalWaitTime
				})
				limit := 10
				if limit > len(pairs) {
					limit = len(pairs)
				}

				b.WriteString("### Waiting Queries\n\n")
				rows := make([][]string, 0, limit)
				for i := 0; i < limit; i++ {
					s := pairs[i].stat
					rows = append(rows, []string{s.ID, truncateQuery(s.NormalizedQuery, 60), fmt.Sprintf("%d", s.AcquiredCount), fmt.Sprintf("%d", s.StillWaitingCount), formatQueryDuration(s.TotalWaitTime)})
				}
				mdTable(&b, []string{"SQLID", "Query", "Acquired", "Waiting", "Total Wait"}, "llrrr", rows)
				b.WriteString("\n")
			}
		}

		// Blocking queries
		if len(m.Locks.Events) > 0 {
			type blockerStat struct {
				queryID    string
				query      string
				blockCount int
				totalWait  float64
			}
			blockers := make(map[string]*blockerStat)
			for _, e := range m.Locks.Events {
				if e.BlockingQueryID == "" || e.EventType == "deadlock" {
					continue
				}
				bs, ok := blockers[e.BlockingQueryID]
				if !ok {
					bs = &blockerStat{queryID: e.BlockingQueryID}
					blockers[e.BlockingQueryID] = bs
				}
				bs.blockCount++
				bs.totalWait += e.WaitTime
			}
			if len(blockers) > 0 {
				for _, e := range m.Locks.Events {
					if e.BlockingQueryID == "" || e.BlockingQuery == "" {
						continue
					}
					if bs, ok := blockers[e.BlockingQueryID]; ok && bs.query == "" {
						bs.query = e.BlockingQuery
					}
				}

				type blockerPair struct{ stat *blockerStat }
				var pairs []blockerPair
				for _, bs := range blockers {
					pairs = append(pairs, blockerPair{bs})
				}
				sort.Slice(pairs, func(i, j int) bool {
					return pairs[i].stat.totalWait > pairs[j].stat.totalWait
				})

				limit := 10
				if limit > len(pairs) {
					limit = len(pairs)
				}

				b.WriteString("### Blocking Queries\n\n")
				rows := make([][]string, 0, limit)
				for i := 0; i < limit; i++ {
					bs := pairs[i].stat
					query := bs.query
					if query == "" {
						query = "(unknown)"
					}
					avgWait := bs.totalWait / float64(bs.blockCount)
					rows = append(rows, []string{bs.queryID, truncateQuery(query, 60), fmt.Sprintf("%d", bs.blockCount), formatQueryDuration(avgWait), formatQueryDuration(bs.totalWait)})
				}
				mdTable(&b, []string{"SQLID", "Query", "Blocked", "Avg Wait", "Total Wait"}, "llrrr", rows)
				b.WriteString("\n")
			}
		}
	}

	// ============================================================================
	// MAINTENANCE — split into AUTOVACUUM / AUTOANALYZE sibling sections
	// to mirror the CLI text layout. Each section emits its own header
	// k:v block followed by purpose-driven top-tables panels.
	// ============================================================================
	if has("maintenance") {
		if m.Vacuum.VacuumCount > 0 {
			writeAutovacuumSectionMarkdown(&b, m.Vacuum)
		}
		if m.Vacuum.AnalyzeCount > 0 {
			writeAutoanalyzeSectionMarkdown(&b, m.Vacuum)
		}
	}

	// ============================================================================
	// CHECKPOINTS
	// ============================================================================
	if has("checkpoints") && (m.Checkpoints.CompleteCount > 0 || m.Checkpoints.WarningCount > 0) {
		b.WriteString("## CHECKPOINTS\n\n")

		if m.Checkpoints.CompleteCount > 0 {
			avgWriteSeconds := m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)
			avgDuration := time.Duration(avgWriteSeconds * float64(time.Second)).Truncate(time.Second)
			maxDuration := time.Duration(m.Checkpoints.MaxWriteTimeSeconds * float64(time.Second)).Truncate(time.Second)

			hist, _, scale := computeCheckpointHistogram(m.Checkpoints)
			printHistogramMarkdown(&b, hist, "Checkpoint distribution", "", scale, nil)

			b.WriteString(fmt.Sprintf("- **Checkpoint count**: %d\n", m.Checkpoints.CompleteCount))
			b.WriteString(fmt.Sprintf("- **Avg checkpoint write time**: %s\n", avgDuration))
			b.WriteString(fmt.Sprintf("- **Max checkpoint write time**: %s\n", maxDuration))

			if len(m.Checkpoints.WALDistances) > 0 {
				avgDistMB := float64(m.Checkpoints.TotalDistanceKB) / float64(m.Checkpoints.CompleteCount) / 1024.0
				maxDistMB := float64(m.Checkpoints.MaxDistanceKB) / 1024.0
				b.WriteString(fmt.Sprintf("- **WAL per checkpoint (avg)**: %.1f MB\n", avgDistMB))
				b.WriteString(fmt.Sprintf("- **WAL per checkpoint (max)**: %.1f MB\n", maxDistMB))
			}
		}

		if m.Checkpoints.WarningCount > 0 {
			if m.Checkpoints.WarningMinIntervalSeconds == m.Checkpoints.WarningMaxIntervalSeconds {
				b.WriteString(fmt.Sprintf("- **Too frequent warnings**: %d (%d s apart)\n",
					m.Checkpoints.WarningCount, m.Checkpoints.WarningMinIntervalSeconds))
			} else {
				b.WriteString(fmt.Sprintf("- **Too frequent warnings**: %d (%d-%d s apart)\n",
					m.Checkpoints.WarningCount,
					m.Checkpoints.WarningMinIntervalSeconds, m.Checkpoints.WarningMaxIntervalSeconds))
			}
		}
		b.WriteString("\n")

		// Display checkpoint types
		if len(m.Checkpoints.TypeCounts) > 0 {
			// Build a slice to sort types by count (descending).
			type typePair struct {
				Name  string
				Count int
			}
			var pairs []typePair
			for cpType, count := range m.Checkpoints.TypeCounts {
				pairs = append(pairs, typePair{Name: cpType, Count: count})
			}

			// Sort by count descending, then alphabetically.
			sort.Slice(pairs, func(i, j int) bool {
				if pairs[i].Count != pairs[j].Count {
					return pairs[i].Count > pairs[j].Count
				}
				return pairs[i].Name < pairs[j].Name
			})

			// Compute total duration for rate calculation.
			duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
			durationHours := duration.Hours()

			b.WriteString("### Checkpoint types\n\n")
			b.WriteString("|  |  |  |  |\n")
			b.WriteString("|------|------:|--:|-----:|\n")

			// Display each type with count, percentage and rate.
			for _, pair := range pairs {
				percentage := float64(pair.Count) / float64(m.Checkpoints.CompleteCount) * 100

				// Compute rate (checkpoints per hour) for this type.
				rate := 0.0
				if durationHours > 0 {
					rate = float64(pair.Count) / durationHours
				}

				b.WriteString(fmt.Sprintf("| %s | %d | %.1f%% | %.2f/h |\n",
					pair.Name, pair.Count, percentage, rate))
			}
			b.WriteString("\n")
		}

		// WAL distance vs estimate
		if walBuckets := computeWALDistanceHistogram(m.Checkpoints); len(walBuckets) > 0 {
			printWALDistanceMarkdown(&b, walBuckets)
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
		if m.Connections.SessionEventsCount() > 0 && !m.Global.MinTimestamp.IsZero() && !m.Global.MaxTimestamp.IsZero() {
			numBuckets := 6
			if isDetailedMode {
				numBuckets = 12
			}
			concurrentHist, labels, concurrentScale, peakTimes := computeConcurrentHistogram(
				m.Connections.IterateSessionEvents,
				m.Connections.SessionEventsCount(),
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
			hist, _, scale := computeConnectionsHistogram(m.Connections.IterateConnections, m.Connections.ConnectionsCount(), m.Global.MinTimestamp, m.Global.MaxTimestamp)
			printHistogramMarkdown(&b, hist, "Connection distribution", "", scale, nil)
		}

		b.WriteString(fmt.Sprintf("- **Connection count**: %d\n", m.Connections.ConnectionReceivedCount))
		if duration.Hours() > 0 {
			avgConnPerHour := float64(m.Connections.ConnectionReceivedCount) / duration.Hours()
			b.WriteString(fmt.Sprintf("- **Avg connections per hour**: %.2f\n", avgConnPerHour))
		}
		b.WriteString(fmt.Sprintf("- **Disconnection count**: %d\n", m.Connections.DisconnectionCount))

		if m.Connections.SessionStats.Count > 0 {
			// Average
			avgSessionTime := time.Duration(float64(m.Connections.TotalSessionTime) / float64(m.Connections.DisconnectionCount))
			b.WriteString(fmt.Sprintf("- **Avg session time**: %s\n", formatSessionDuration(avgSessionTime)))
			// Median (P²-estimated; <5% error after 50 samples)
			b.WriteString(fmt.Sprintf("- **Median session time**: %s\n", formatSessionDuration(m.Connections.SessionStats.Median)))
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
		if m.Connections.SessionStats.Count > 0 {
			stats := m.Connections.SessionStats

			b.WriteString("### Session Duration Statistics\n\n")
			b.WriteString(fmt.Sprintf("- **Count**: %d\n", stats.Count))
			b.WriteString(fmt.Sprintf("- **Min**: %s\n", stats.Min.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Max**: %s\n", stats.Max.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Avg**: %s\n", stats.Avg.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Median**: %s\n", stats.Median.Round(time.Second)))
			b.WriteString(fmt.Sprintf("- **Cumulated**: %s\n\n", m.Connections.SessionCumulated.Round(time.Second)))

			// Session duration distribution
			dist := m.Connections.SessionDistribution
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

		writeSessionTable := func(label string, src map[string]*analysis.StreamingDurationStats) {
			if len(src) == 0 {
				return
			}
			b.WriteString("### Session Duration by " + label + "\n\n")
			type row struct {
				key       string
				stats     analysis.DurationStats
				cumulated time.Duration
			}
			var sorted []row
			for k, s := range src {
				sorted = append(sorted, row{key: k, stats: s.Stats(), cumulated: s.Cumulated()})
			}
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].stats.Count > sorted[j].stats.Count })
			end := len(sorted)
			if end > 10 {
				end = 10
			}
			rows := make([][]string, 0, end)
			for _, r := range sorted[:end] {
				rows = append(rows, []string{
					r.key,
					fmt.Sprintf("%d", r.stats.Count),
					r.stats.Min.Round(time.Second).String(),
					r.stats.Max.Round(time.Second).String(),
					r.stats.Avg.Round(time.Second).String(),
					r.stats.Median.Round(time.Second).String(),
					r.cumulated.Round(time.Second).String(),
				})
			}
			mdTable(&b, []string{label, "Sessions", "Min", "Max", "Avg", "Median", "Cumulated"}, "lrrrrrr", rows)
			b.WriteString("\n")
		}
		writeSessionTable("User", m.Connections.SessionsByUser)
		writeSessionTable("Database", m.Connections.SessionsByDatabase)
		writeSessionTable("Host", m.Connections.SessionsByHost)
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

		writeEntityTable := func(title, colName string, counts map[string]int) {
			if len(counts) == 0 {
				return
			}
			b.WriteString("### " + title + "\n\n")
			sorted := analysis.SortByCount(counts)
			rows := make([][]string, 0, len(sorted))
			for _, item := range sorted {
				pct := float64(item.Count) * 100.0 / float64(totalLogs)
				rows = append(rows, []string{item.Name, fmt.Sprintf("%d", item.Count), fmt.Sprintf("%.1f%%", pct)})
			}
			mdTable(&b, []string{colName, "Count", "%"}, "lrr", rows)
			b.WriteString("\n")
		}
		writeComboTable := func(title, leftCol, rightCol string, counts map[string]int) {
			if len(counts) == 0 {
				return
			}
			b.WriteString("### " + title + "\n\n")
			sorted := analysis.SortByCount(counts)
			rows := make([][]string, 0, len(sorted))
			for _, item := range sorted {
				parts := strings.SplitN(item.Name, "|", 2)
				if len(parts) != 2 {
					continue
				}
				pct := float64(item.Count) * 100.0 / float64(totalLogs)
				rows = append(rows, []string{parts[0], parts[1], fmt.Sprintf("%d", item.Count), fmt.Sprintf("%.1f%%", pct)})
			}
			mdTable(&b, []string{leftCol, rightCol, "Count", "%"}, "llrr", rows)
			b.WriteString("\n")
		}
		if m.UniqueEntities.UniqueUsers > 0 {
			writeEntityTable("USERS", "User", m.UniqueEntities.UserCounts)
		}
		if m.UniqueEntities.UniqueApps > 0 {
			writeEntityTable("APPS", "App", m.UniqueEntities.AppCounts)
		}
		if m.UniqueEntities.UniqueDbs > 0 {
			writeEntityTable("DATABASES", "Database", m.UniqueEntities.DBCounts)
		}
		if m.UniqueEntities.UniqueHosts > 0 {
			writeEntityTable("HOSTS", "Host", m.UniqueEntities.HostCounts)
		}
		writeComboTable("USER × DATABASE", "User", "Database", m.UniqueEntities.UserDbCombos)
		writeComboTable("USER × HOST", "User", "Host", m.UniqueEntities.UserHostCombos)
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
		exportSQLSummaryMarkdownTo(&b, m.SQL, analysis.TempFileMetrics{}, analysis.LockMetrics{})
	}

	fmt.Fprintln(w, b.String())
}

// ============================================================================
// MARKDOWN-SPECIFIC HELPERS
// ============================================================================

// printHistogramMarkdown renders a histogram as ASCII art in a code block
func printWALDistanceMarkdown(b *strings.Builder, buckets []WALDistanceBucket) {
	maxMB := 0.0
	for _, bk := range buckets {
		if bk.AvgDistMB > maxMB {
			maxMB = bk.AvgDistMB
		}
		if bk.AvgEstMB > maxMB {
			maxMB = bk.AvgEstMB
		}
	}
	if maxMB == 0 {
		return
	}

	barWidth := 50
	scaleMB := maxMB / float64(barWidth)
	if scaleMB <= 0 {
		scaleMB = 1
	}

	scaleLabel := scaleMB
	scaleUnit := "MB"
	if scaleLabel < 1.0 {
		scaleLabel *= 1024
		scaleUnit = "kB"
	}

	b.WriteString("### WAL per checkpoint (avg)\n\n")
	b.WriteString(fmt.Sprintf("■ = %.0f %s, □ = estimate margin\n\n", scaleLabel, scaleUnit))
	b.WriteString("```\n")

	for _, bk := range buckets {
		if bk.Count == 0 {
			b.WriteString(fmt.Sprintf("%-13s  -\n", bk.Label))
			continue
		}
		dist := bk.AvgDistMB
		est := bk.AvgEstMB
		distChars := int(dist / scaleMB)
		if distChars > barWidth {
			distChars = barWidth
		}
		marginChars := 0
		if est > dist {
			estChars := int(est / scaleMB)
			if estChars > barWidth {
				estChars = barWidth
			}
			marginChars = estChars - distChars
			if marginChars < 0 {
				marginChars = 0
			}
		}
		bar := strings.Repeat("■", distChars) + strings.Repeat("□", marginChars)
		b.WriteString(fmt.Sprintf("%-13s %s  %.0f MB\n", bk.Label, bar, dist))
	}
	b.WriteString("```\n\n")
}

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
		// Sort by time if labels are time ranges. When two buckets share
		// the same start minute (e.g. 30-second buckets at 04:00:00 and
		// 04:00:30 both format to "04:00"), ti.Before(tj) is false both
		// ways, which makes the sort unstable and the output order
		// non-deterministic across runs. Fall back to the full label
		// string as a tiebreaker — it is always unique (contains the
		// bucket end time).
		sort.Slice(labels, func(i, j int) bool {
			pi := strings.Split(labels[i], " - ")
			pj := strings.Split(labels[j], " - ")
			if len(pi) == 2 && len(pj) == 2 {
				ti, err1 := time.Parse("15:04", pi[0])
				tj, err2 := time.Parse("15:04", pj[0])
				if err1 == nil && err2 == nil {
					if !ti.Equal(tj) {
						return ti.Before(tj)
					}
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
					if !ti.Equal(tj) {
						return ti.Before(tj)
					}
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
// writeAutovacuumSectionMarkdown renders the AUTOVACUUM section,
// mirroring the CLI layout: header k:v block + three purpose-driven
// top-tables panels (by elapsed, by rows-not-yet-removable, by count).
// Each line and panel is suppressed when its source metric is zero so
// older PostgreSQL versions emitting no continuation lines degrade
// cleanly to a terse output.
func writeAutovacuumSectionMarkdown(b *strings.Builder, v analysis.VacuumMetrics) {
	b.WriteString("## AUTOVACUUM\n\n")
	b.WriteString(fmt.Sprintf("- **Vacuum count**: %d\n", v.VacuumCount))
	if v.AggressiveVacuumCount > 0 {
		b.WriteString(fmt.Sprintf("  - *of which aggressive*: %d\n", v.AggressiveVacuumCount))
	}
	if v.TotalVacuumElapsedSeconds > 0 {
		dur := time.Duration(v.TotalVacuumElapsedSeconds * float64(time.Second)).Truncate(time.Second)
		b.WriteString(fmt.Sprintf("- **Cumulated time**: %s\n", dur))
	}
	if v.TotalTuplesRemoved > 0 {
		b.WriteString(fmt.Sprintf("- **Tuples removed**: %d\n", v.TotalTuplesRemoved))
	}
	if total := sumSpaceRecoveredMD(v.VacuumSpaceRecovered); total > 0 {
		b.WriteString(fmt.Sprintf("- **Space recovered**: %s\n", FormatBytes(total)))
	}
	if v.TotalTuplesNotYetRemovable > 0 {
		b.WriteString(fmt.Sprintf("- **Dead, not yet removable**: %d\n", v.TotalTuplesNotYetRemovable))
	}
	if v.TotalBufferHits+v.TotalBufferMisses > 0 {
		b.WriteString(fmt.Sprintf("- **Buffer usage**: hits=%d misses=%d dirtied=%d written=%d\n",
			v.TotalBufferHits, v.TotalBufferMisses, v.TotalBufferDirtied, v.TotalBufferWritten))
	}
	if v.TotalWALRecords > 0 || v.TotalWALBytes > 0 {
		b.WriteString(fmt.Sprintf("- **WAL usage**: %d records, %s\n",
			v.TotalWALRecords, FormatBytes(v.TotalWALBytes)))
	}
	if v.SlowestVacuum != nil && v.SlowestVacuum.ElapsedSeconds > 0 {
		dur := time.Duration(v.SlowestVacuum.ElapsedSeconds * float64(time.Second)).Truncate(time.Second)
		b.WriteString(fmt.Sprintf("- **Slowest single run**: %s on `%s`\n", dur, v.SlowestVacuum.Table))
	}
	b.WriteString("\n")

	if len(v.TopVacuumTables) > 0 {
		b.WriteString("### Top tables by autovacuum elapsed time\n\n")
		rows := make([][]string, 0, len(v.TopVacuumTables))
		for _, t := range v.TopVacuumTables {
			dur := time.Duration(t.TotalElapsedSeconds * float64(time.Second)).Truncate(time.Second)
			rec := ""
			if r := v.VacuumSpaceRecovered[t.Table]; r > 0 {
				rec = FormatBytes(r)
			}
			rows = append(rows, []string{"`" + t.Table + "`", fmt.Sprintf("%d", t.VacuumCount), dur.String(), rec})
		}
		mdTable(b, []string{"Table", "Vacuum count", "Elapsed", "Recovered"}, "lrrr", rows)
		b.WriteString("\n")
	}

	if len(v.XminBlockedTables) > 0 {
		b.WriteString("### Tables with rows not yet removable\n\n")
		rows := make([][]string, 0, len(v.XminBlockedTables))
		for _, t := range v.XminBlockedTables {
			rows = append(rows, []string{"`" + t.Table + "`", fmt.Sprintf("%d", t.TuplesNotYetRemovable), fmt.Sprintf("%d", t.VacuumCount)})
		}
		mdTable(b, []string{"Table", "Dead rows", "Vacuum count"}, "lrr", rows)
		b.WriteString("\n")
	}

	if len(v.VacuumTableCounts) > 0 {
		b.WriteString("### Top tables by autovacuum count\n\n")
		b.WriteString(printTopTablesMarkdown(v.VacuumTableCounts, v.VacuumCount, v.VacuumSpaceRecovered))
		b.WriteString("\n")
	}
}

// writeAutoanalyzeSectionMarkdown renders the AUTOANALYZE sibling
// section. Slimmer than autovacuum because PostgreSQL's analyze blocks
// only carry system-usage (elapsed) — no buffer, no WAL, no tuples.
func writeAutoanalyzeSectionMarkdown(b *strings.Builder, v analysis.VacuumMetrics) {
	b.WriteString("## AUTOANALYZE\n\n")
	b.WriteString(fmt.Sprintf("- **Analyze count**: %d\n", v.AnalyzeCount))
	if v.TotalAnalyzeElapsedSeconds > 0 {
		dur := time.Duration(v.TotalAnalyzeElapsedSeconds * float64(time.Second)).Truncate(time.Second)
		b.WriteString(fmt.Sprintf("- **Cumulated time**: %s\n", dur))
	}
	b.WriteString("\n")

	if len(v.TopAnalyzeTablesByElapsed) > 0 {
		b.WriteString("### Top tables by autoanalyze elapsed time\n\n")
		// Cap at 10 rows in the markdown for readability — the JSON
		// keeps the full list when consumers want more.
		end := len(v.TopAnalyzeTablesByElapsed)
		if end > 10 {
			end = 10
		}
		rows := make([][]string, 0, end)
		for _, t := range v.TopAnalyzeTablesByElapsed[:end] {
			dur := time.Duration(t.TotalElapsedSeconds * float64(time.Second)).Truncate(time.Second)
			rows = append(rows, []string{"`" + t.Table + "`", fmt.Sprintf("%d", t.VacuumCount), dur.String()})
		}
		mdTable(b, []string{"Table", "Analyze count", "Elapsed"}, "lrr", rows)
		b.WriteString("\n")
	}

	if len(v.AnalyzeTableCounts) > 0 {
		b.WriteString("### Top tables by autoanalyze count\n\n")
		b.WriteString(printTopTablesMarkdown(v.AnalyzeTableCounts, v.AnalyzeCount, nil))
		b.WriteString("\n")
	}
}

// sumSpaceRecoveredMD totals the per-table reclaimed bytes — mirrors
// the helper in output/text.go so the AUTOVACUUM header can show one
// cluster-wide "Space recovered" line above the per-table breakdown.
func sumSpaceRecoveredMD(m map[string]int64) int64 {
	var total int64
	for _, v := range m {
		total += v
	}
	return total
}

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
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		return pairs[i].Name < pairs[j].Name
	})

	var rows [][]string
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
		rows = append(rows, []string{
			p.Name,
			fmt.Sprintf("%d", p.Count),
			fmt.Sprintf("%.2f%%", percentage),
			FormatBytes(p.Recovered),
		})
		if total > 0 && float64(cum)/float64(total)*100 >= 80 {
			break
		}
	}
	var sb strings.Builder
	mdTable(&sb, []string{"Table", "Count", "% of total", "Recovered"}, "lrrr", rows)
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
		// Use the pre-computed ID instead of recalculating.
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
	emit := func(title string, sortFn func(i, j int) bool, headers []string, rowFn func(qinfo) []string) {
		sort.Slice(list, sortFn)
		b.WriteString("**" + title + "**\n\n")
		end := len(list)
		if end > 10 {
			end = 10
		}
		rows := make([][]string, 0, end)
		for _, q := range list[:end] {
			rows = append(rows, rowFn(q))
		}
		mdTable(b, headers, "lrrrl", rows)
		b.WriteString("\n")
	}
	emit("Slowest queries (top 10)",
		func(i, j int) bool { return list[i].MaxTime > list[j].MaxTime },
		[]string{"SQLID", "Max", "Avg", "Count", "Query"},
		func(q qinfo) []string {
			return []string{q.ID, formatQueryDuration(q.MaxTime), formatQueryDuration(q.AvgTime), fmt.Sprintf("%d", q.Count), truncateQuery(q.Query, 80)}
		})
	emit("Most frequent queries (top 10)",
		func(i, j int) bool { return list[i].Count > list[j].Count },
		[]string{"SQLID", "Count", "Avg", "Max", "Query"},
		func(q qinfo) []string {
			return []string{q.ID, fmt.Sprintf("%d", q.Count), formatQueryDuration(q.AvgTime), formatQueryDuration(q.MaxTime), truncateQuery(q.Query, 80)}
		})
	emit("Most time consuming queries (top 10)",
		func(i, j int) bool { return list[i].TotalTime > list[j].TotalTime },
		[]string{"SQLID", "Total", "Avg", "Count", "Query"},
		func(q qinfo) []string {
			return []string{q.ID, formatQueryDuration(q.TotalTime), formatQueryDuration(q.AvgTime), fmt.Sprintf("%d", q.Count), truncateQuery(q.Query, 80)}
		})
}

// countSlowQueries returns the count of queries in the top 1% (P99)
func countSlowQueries(sql analysis.SQLMetrics) int {
	if sql.ExecutionCount() == 0 {
		return 0
	}
	return sql.ExecutionsCountAbove(sql.P99QueryDuration)
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
	type pair struct{ stat *analysis.LockQueryStat }
	var pairs []pair
	for _, s := range queryStats {
		if s.AcquiredCount > 0 {
			pairs = append(pairs, pair{s})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].stat.AcquiredWaitTime > pairs[j].stat.AcquiredWaitTime })
	if limit > len(pairs) {
		limit = len(pairs)
	}
	rows := make([][]string, 0, limit)
	for i := 0; i < limit; i++ {
		s := pairs[i].stat
		avgWait := s.AcquiredWaitTime / float64(s.AcquiredCount)
		rows = append(rows, []string{s.ID, truncateQuery(s.NormalizedQuery, 60), fmt.Sprintf("%d", s.AcquiredCount), fmt.Sprintf("%.2f", avgWait), fmt.Sprintf("%.2f", s.AcquiredWaitTime)})
	}
	mdTable(b, lockQueryHeaders, "llrrr", rows)
}

// printStillWaitingLockQueriesMarkdown prints queries with locks still waiting in markdown table format.
func printStillWaitingLockQueriesMarkdown(b *strings.Builder, queryStats map[string]*analysis.LockQueryStat, limit int) {
	type pair struct{ stat *analysis.LockQueryStat }
	var pairs []pair
	for _, s := range queryStats {
		if s.StillWaitingCount > 0 {
			pairs = append(pairs, pair{s})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].stat.StillWaitingTime > pairs[j].stat.StillWaitingTime })
	if limit > len(pairs) {
		limit = len(pairs)
	}
	rows := make([][]string, 0, limit)
	for i := 0; i < limit; i++ {
		s := pairs[i].stat
		avgWait := s.StillWaitingTime / float64(s.StillWaitingCount)
		rows = append(rows, []string{s.ID, truncateQuery(s.NormalizedQuery, 60), fmt.Sprintf("%d", s.StillWaitingCount), fmt.Sprintf("%.2f", avgWait), fmt.Sprintf("%.2f", s.StillWaitingTime)})
	}
	mdTable(b, lockQueryHeaders, "llrrr", rows)
}

// printMostFrequentWaitingQueriesMarkdown prints all queries that experienced lock waits in markdown table format.
func printMostFrequentWaitingQueriesMarkdown(b *strings.Builder, queryStats map[string]*analysis.LockQueryStat, limit int) {
	type pair struct {
		stat       *analysis.LockQueryStat
		totalLocks int
		totalWait  float64
	}
	var pairs []pair
	for _, s := range queryStats {
		totalLocks := s.AcquiredCount + s.StillWaitingCount
		if totalLocks > 0 {
			pairs = append(pairs, pair{stat: s, totalLocks: totalLocks, totalWait: s.AcquiredWaitTime + s.StillWaitingTime})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].totalLocks > pairs[j].totalLocks })
	if limit > len(pairs) {
		limit = len(pairs)
	}
	rows := make([][]string, 0, limit)
	for i := 0; i < limit; i++ {
		p := pairs[i]
		avgWait := p.totalWait / float64(p.totalLocks)
		rows = append(rows, []string{p.stat.ID, truncateQuery(p.stat.NormalizedQuery, 60), fmt.Sprintf("%d", p.totalLocks), fmt.Sprintf("%.2f", avgWait), fmt.Sprintf("%.2f", p.totalWait)})
	}
	mdTable(b, lockQueryHeaders, "llrrr", rows)
}

// lockQueryHeaders is the shared 5-column header used by the three
// lock-query renderers — keeping the constant out of the functions
// guarantees they stay in sync.
var lockQueryHeaders = []string{"SQLID", "Normalized Query", "Locks", "Avg Wait (ms)", "Total Wait (ms)"}

// ExportSQLSummaryMarkdown produces a markdown report for --sql-summary
func ExportSQLSummaryMarkdown(w io.Writer, m analysis.SQLMetrics, tempFiles analysis.TempFileMetrics, locks analysis.LockMetrics) {
	var b strings.Builder

	// ... (content) ...
	// I'll be more specific to avoid error

	// Compute top 1% slowest queries
	top1Slow := 0
	if m.ExecutionCount() > 0 {
		top1Slow = m.ExecutionsCountAbove(m.P99QueryDuration)
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
		limit := 10
		if len(queries) < limit {
			limit = len(queries)
		}
		rows := make([][]string, 0, limit)
		for i := 0; i < limit; i++ {
			s := queries[i].stat
			rows = append(rows, []string{s.ID, truncateQuery(s.NormalizedQuery, 60), fmt.Sprintf("%d", s.Count), FormatBytes(s.TotalSize)})
		}
		mdTable(&b, []string{"SQLID", "Normalized Query", "Count", "Total Size"}, "llrr", rows)
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
			printMostFrequentWaitingQueriesMarkdown(&b, locks.QueryStats, 10)
			b.WriteString("\n")
		}
	}

	fmt.Fprintln(w, b.String())
}

// queryEventLinkMD is the markdown variant of queryEventLink — same
// shape, kept local so the two output packages do not need a shared
// view type.
type queryEventLinkMD struct {
	event      analysis.EventStat
	triggerCnt int
}

// findEventsTriggeredByQueryMD mirrors the text-side helper but keeps
// the local struct out of the public API surface.
func findEventsTriggeredByQueryMD(events []analysis.EventStat, queryID string) []queryEventLinkMD {
	if queryID == "" {
		return nil
	}
	var out []queryEventLinkMD
	for i := range events {
		for _, tq := range events[i].TriggeringQueries {
			if tq.ID == queryID {
				out = append(out, queryEventLinkMD{event: events[i], triggerCnt: tq.Count})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].triggerCnt != out[j].triggerCnt {
			return out[i].triggerCnt > out[j].triggerCnt
		}
		return out[i].event.Count > out[j].event.Count
	})
	return out
}

// ExportSQLDetailMarkdown produces a markdown report for --sql-detail
func ExportSQLDetailMarkdown(w io.Writer, m analysis.AggregatedMetrics, queryIDs []string) {
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
			if len(sqlStat.PreparedNames) > 0 {
				b.WriteString(fmt.Sprintf("- **Prepared as**: %s\n", formatPreparedNames(sqlStat.PreparedNames)))
			}
		}
		b.WriteString("\n")

		// EVENTS section — same early position as the text renderer:
		// straight after Query Info so the operational signal is the
		// first thing a DBA reads. Rows are sorted by trigger count
		// descending; "#" and "Event total" are dropped to keep the
		// table focused on the "this query caused N of these" answer.
		eventsForMD := findEventsTriggeredByQueryMD(m.TopEvents, qid)
		if len(eventsForMD) > 0 {
			b.WriteString("### EVENTS\n\n")
			b.WriteString("| Event ID | Severity | Message | Triggered |\n")
			b.WriteString("|---|---|---|---:|\n")
			for _, r := range eventsForMD {
				msg := r.event.Message
				if len(msg) > 90 {
					msg = msg[:89] + "…"
				}
				b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %d |\n",
					r.event.ID, r.event.Severity, msg, r.triggerCnt))
			}
			b.WriteString("\n")
		}

		// Execution histogram (if > 1 execution)
		if sqlStat != nil && sqlStat.Count > 1 {
			execHist, execUnit, execScale := computeSingleQueryExecutionHistogram(m.SQL, qid)
			if execHist != nil {
				printHistogramMarkdown(&b, execHist, "Query count", execUnit, execScale, nil)
			}
		}

		// TIME section
		if sqlStat != nil {
			b.WriteString("### TIME\n\n")

			// Cumulative time histogram (if > 1 execution)
			if sqlStat.Count > 1 {
				timeHist, timeUnit, timeScale := computeSingleQueryTimeHistogram(m.SQL, qid)
				if timeHist != nil {
					printHistogramMarkdown(&b, timeHist, "Cumulative time", timeUnit, timeScale, nil)
				}
			}

			// Duration distribution histogram (if > 1 execution)
			if sqlStat.Count > 1 {
				durationHist, durationUnit, durationScale, durationLabels := computeSingleQueryDurationDistribution(m.SQL, qid)
				if durationHist != nil {
					printHistogramMarkdown(&b, durationHist, "Query duration distribution", durationUnit, durationScale, durationLabels)
				}
			}

			// Calculate min duration
			minDuration := sqlStat.MaxTime
			m.SQL.IterateExecutionsForID(qid, func(exec analysis.QueryExecution) bool {
				if exec.Duration < minDuration {
					minDuration = exec.Duration
				}
				return true
			})

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

			avgSize := tempStat.TotalSize / int64(tempStat.Count)

			b.WriteString(fmt.Sprintf("- **Temp Files count**: %d\n", tempStat.Count))
			b.WriteString(fmt.Sprintf("- **Temp File min size**: %s\n", FormatBytes(tempStat.MinSize)))
			b.WriteString(fmt.Sprintf("- **Temp File max size**: %s\n", FormatBytes(tempStat.MaxSize)))
			b.WriteString(fmt.Sprintf("- **Temp File avg size**: %s\n", FormatBytes(avgSize)))
			b.WriteString(fmt.Sprintf("- **Temp Files size**: %s\n\n", FormatBytes(tempStat.TotalSize)))
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

		// Example or slowest run — when DETAIL params are available we
		// substitute them into the placeholders so the result is directly
		// copy-pastable into psql.
		if rawQuery != "" {
			if sqlStat != nil && sqlStat.SlowestRun != nil {
				sr := sqlStat.SlowestRun
				b.WriteString(fmt.Sprintf("### Slowest Run — %s, %s, pid=%s\n\n",
					formatQueryDuration(sr.DurationMs),
					sr.Timestamp.Format("2006-01-02 15:04:05"),
					sr.PID,
				))
				text, truncated, full := truncateForDisplay(SubstituteParameters(rawQuery, sr.Parameters), slowestRunDisplayCap)
				b.WriteString("```sql\n")
				b.WriteString(text)
				if truncated {
					b.WriteString("[…]")
					b.WriteString(truncationHint(len(text), full))
				}
				b.WriteString("\n```\n\n")
			} else {
				b.WriteString("### Example Query\n\n")
				b.WriteString("```sql\n")
				b.WriteString(rawQuery)
				b.WriteString("\n```\n\n")
			}
		}

		// Execution plan (from auto_explain)
		if sqlStat != nil && sqlStat.LastPlan != "" {
			b.WriteString("### Execution Plan\n\n")
			b.WriteString("```\n")
			b.WriteString(sqlStat.LastPlan)
			b.WriteString("\n```\n\n")
		}
	}

	fmt.Fprintln(w, b.String())
}

// ExportSQLOverviewMarkdown exports SQL query type overview statistics in Markdown format.
// This provides query type statistics with dimensional breakdowns (SELECT, INSERT, UPDATE, DELETE, etc.)
func ExportSQLOverviewMarkdown(w io.Writer, m analysis.SQLMetrics) {
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
func exportSQLOverviewMarkdownTo(b *strings.Builder, m analysis.SQLMetrics) {
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

// exportSQLSummaryMarkdownTo writes SQL performance content to a strings.Builder.
// Used by ExportMarkdown in full mode.
func exportSQLSummaryMarkdownTo(b *strings.Builder, m analysis.SQLMetrics, tempFiles analysis.TempFileMetrics, locks analysis.LockMetrics) {
	// Compute top 1% slowest queries via the compact storage helper.
	top1Slow := 0
	if m.ExecutionCount() > 0 {
		top1Slow = m.ExecutionsCountAbove(m.P99QueryDuration)
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
				FormatBytes(stat.TotalSize)))
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
			printMostFrequentWaitingQueriesMarkdown(b, locks.QueryStats, 5)
			b.WriteString("\n")
		}
	}
}

// mdTable writes a markdown table whose pipes align in raw view.
// alignments is a per-column string using 'r' for right-aligned cells,
// anything else (typically 'l') for left. Cell values are escaped
// pre-emission: any embedded "|" becomes "\|" so it stays part of the
// cell rather than starting a new column — a real footgun in this
// codebase since query texts and error messages happily carry pipes.
// The padding itself is purely cosmetic for a renderer but lets a
// reader scan the raw file without the columns sliding row to row.
func mdTable(b *strings.Builder, headers []string, alignments string, rows [][]string) {
	escapeCells := func(cells []string) []string {
		out := make([]string, len(cells))
		for i, c := range cells {
			out[i] = strings.ReplaceAll(c, "|", `\|`)
		}
		return out
	}
	hdr := escapeCells(headers)
	esc := make([][]string, len(rows))
	for i, r := range rows {
		esc[i] = escapeCells(r)
	}
	widths := make([]int, len(headers))
	for i, h := range hdr {
		widths[i] = len(h)
	}
	for _, row := range esc {
		for i := 0; i < len(widths) && i < len(row); i++ {
			if l := len(row[i]); l > widths[i] {
				widths[i] = l
			}
		}
	}
	right := func(i int) bool { return i < len(alignments) && alignments[i] == 'r' }
	writeRow := func(cells []string) {
		for i, c := range cells {
			b.WriteByte('|')
			b.WriteByte(' ')
			pad := widths[i] - len(c)
			if right(i) {
				b.WriteString(strings.Repeat(" ", pad))
				b.WriteString(c)
			} else {
				b.WriteString(c)
				b.WriteString(strings.Repeat(" ", pad))
			}
			b.WriteByte(' ')
		}
		b.WriteString("|\n")
	}
	writeRow(hdr)
	for i, w := range widths {
		b.WriteByte('|')
		if right(i) {
			b.WriteString(strings.Repeat("-", w+1))
			b.WriteByte(':')
		} else {
			b.WriteString(strings.Repeat("-", w+2))
		}
	}
	b.WriteString("|\n")
	for _, row := range esc {
		writeRow(row)
	}
}
