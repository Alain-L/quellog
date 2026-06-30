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
			formatThousands(int64(m.Global.Count)),
			humanDate(m.Global.MinTimestamp),
			humanDate(m.Global.MaxTimestamp),
			humanDuration(duration),
		))
	}

	// ============================================================================
	// SERVER (right after SUMMARY so the cluster lifecycle frames every
	// section below it; auto-hides on steady-state logs). Replication
	// markers are folded in as a sub-zone so a server-less log that
	// carries walreceiver/walsender events still gets a place to surface
	// them.
	// ============================================================================
	if has("server") && (m.Server.HasAny() || m.Replication.HasAny) {
		writeServerSectionMarkdown(&b, m.Server, m.Replication)
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
		writeSQLKeyMetricsMarkdown(&b, m.SQL)

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
				if queries[i].stat.TotalSize != queries[j].stat.TotalSize {
					return queries[i].stat.TotalSize > queries[j].stat.TotalSize
				}
				return queries[i].stat.ID < queries[j].stat.ID
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
					if pairs[i].stat.TotalWaitTime != pairs[j].stat.TotalWaitTime {
						return pairs[i].stat.TotalWaitTime > pairs[j].stat.TotalWaitTime
					}
					return pairs[i].stat.ID < pairs[j].stat.ID
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
					if pairs[i].stat.totalWait != pairs[j].stat.totalWait {
						return pairs[i].stat.totalWait > pairs[j].stat.totalWait
					}
					return pairs[i].stat.queryID < pairs[j].stat.queryID
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
			sort.Slice(sorted, func(i, j int) bool {
				if sorted[i].stats.Count != sorted[j].stats.Count {
					return sorted[i].stats.Count > sorted[j].stats.Count
				}
				return sorted[i].key < sorted[j].key
			})
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

// writeServerSectionMarkdown emits the SERVER section: counters as a
// bullet list, then sub-sections for parameter changes, timeline, and
// the folded Replication block (when any marker fired). Lines for
// counters that stayed at zero are omitted so a log carrying only
// replication markers still renders a clean section.
func writeServerSectionMarkdown(b *strings.Builder, s analysis.ServerMetrics, r analysis.ReplicationMetrics) {
	b.WriteString("## SERVER\n\n")

	if s.StartCount > 0 {
		b.WriteString(fmt.Sprintf("- **Starts**: %d\n", s.StartCount))
	}
	if s.ReloadCount > 0 {
		b.WriteString(fmt.Sprintf("- **Reloads (SIGHUP)**: %d\n", s.ReloadCount))
	}
	totalShutdowns := s.ShutdownFastCount + s.ShutdownImmediateCount + s.ShutdownSmartCount
	if totalShutdowns > 0 {
		b.WriteString(fmt.Sprintf("- **Shutdowns**: %d fast, %d immediate, %d smart\n",
			s.ShutdownFastCount, s.ShutdownImmediateCount, s.ShutdownSmartCount))
	}
	if s.CrashRecoveryCount > 0 {
		b.WriteString(fmt.Sprintf("- **Crash recoveries**: %d (\"not properly shut down\")\n", s.CrashRecoveryCount))
	}
	if s.BackendCrashCount > 0 {
		b.WriteString(fmt.Sprintf("- **Backend crashes**: %d %s\n", s.BackendCrashCount, formatSignalCounts(s.SignalCounts)))
	}
	if s.AuxProcessExitCount > 0 {
		b.WriteString(fmt.Sprintf("- **Auxiliary process exits**: %d\n", s.AuxProcessExitCount))
	}
	b.WriteString("\n")

	if len(s.ParameterChanges) > 0 {
		b.WriteString("### Config parameter changes\n\n")
		rows := make([][]string, 0, len(s.ParameterChanges))
		for _, c := range s.ParameterChanges {
			old := c.Old
			if old == "" {
				old = "-"
			}
			rows = append(rows, []string{
				c.Parameter,
				old,
				c.New,
				c.Timestamp.Format("2006-01-02 15:04:05"),
			})
		}
		mdTable(b, []string{"Parameter", "Old", "New", "When"}, "llll", rows)
		b.WriteString("\n")
	}

	if len(s.Timeline) > 0 {
		b.WriteString("### Timeline\n\n")
		rows := make([][]string, 0, len(s.Timeline))
		for _, ev := range s.Timeline {
			rows = append(rows, []string{
				ev.Timestamp.Format("2006-01-02 15:04:05"),
				ev.Kind,
				ev.Detail,
			})
		}
		mdTable(b, []string{"Time", "Event", "Detail"}, "lll", rows)
		b.WriteString("\n")
	}

	if r.HasAny {
		writeReplicationSubZoneMarkdown(b, r)
	}
}

// writeReplicationSubZoneMarkdown emits the folded "Replication" block:
// a bullet list of categories that actually fired, with the most-recent
// termination timestamp inlined so a DBA can jump to the right log
// window without crawling the whole section.
func writeReplicationSubZoneMarkdown(b *strings.Builder, r analysis.ReplicationMetrics) {
	b.WriteString("### Replication\n\n")

	if v := r.Markers["stream_started"]; v > 0 {
		line := fmt.Sprintf("- **Stream reconnects**: %d", v)
		if r.PeakHourLabel != "" && r.PeakHourCount > 1 {
			line += fmt.Sprintf(" _(peak %d× in %s)_", r.PeakHourCount, r.PeakHourLabel)
		}
		b.WriteString(line + "\n")
	}
	if v := r.Markers["recovery_paused"]; v > 0 {
		b.WriteString(fmt.Sprintf("- **Recovery pauses**: %d\n", v))
	}
	if v := r.Markers["conflict_terminate"] + r.Markers["conflict_cancel"]; v > 0 {
		line := fmt.Sprintf("- **Conflicts with recovery**: %d", v)
		if n := len(r.ConflictQueries); n > 0 {
			line += fmt.Sprintf(" _(%d unique queries terminated)_", n)
		}
		b.WriteString(line + "\n")
	}
	if v := r.Markers["slot_invalidated"]; v > 0 {
		b.WriteString(fmt.Sprintf("- **Invalidated slots**: %d\n", v))
	}
	// One bullet per termination cause — the four PG markers map to
	// diametrically opposite scenarios (replica side vs primary side),
	// so collapsing them would hide the diagnostic. Each bullet carries
	// a short hint naming the side at fault; the LastTermination
	// timestamp is appended to the last fired bullet only.
	type termRow struct{ key, label, hint string }
	termRows := []termRow{
		{"wal_receive_failed", "WAL receive failures", "replica lost primary"},
		{"walsender_timeout", "Walsender timeouts", "primary side — replica too slow"},
		{"replication_term", "Replication terminations", "primary closed walsender"},
		{"unexpected_eof", "Unexpected EOFs", "abrupt walsender disconnect"},
	}
	fired := make([]termRow, 0, len(termRows))
	for _, t := range termRows {
		if r.Markers[t.key] > 0 {
			fired = append(fired, t)
		}
	}
	for i, t := range fired {
		line := fmt.Sprintf("- **%s**: %d", t.label, r.Markers[t.key])
		suffix := t.hint
		if i == len(fired)-1 && !r.LastTermination.IsZero() {
			suffix += ", last " + r.LastTermination.Format("2006-01-02 15:04:05")
		}
		line += fmt.Sprintf(" _(%s)_", suffix)
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
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

	// Pad labels to the widest one so the "|", bars and counts line
	// up vertically in the code block — without this, variable label
	// widths (e.g. "< 1s" vs "1min - 30min") slide everything around.
	labelW := 0
	for _, label := range labels {
		if l := len(label); l > labelW {
			labelW = l
		}
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
		b.WriteString(fmt.Sprintf("%-*s | %s %s\n", labelW, label, bar, valueStr))
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

	labelW := 0
	for _, label := range labels {
		if l := len(label); l > labelW {
			labelW = l
		}
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
			b.WriteString(fmt.Sprintf("%-*s | -\n", labelW, label))
		} else {
			peakStr := ""
			if pt, ok := peakTimes[label]; ok && !pt.IsZero() {
				peakStr = fmt.Sprintf("(%02d:%02d)", pt.Hour(), pt.Minute())
			}
			b.WriteString(fmt.Sprintf("%-*s | %s %d %s\n", labelW, label, bar, v, peakStr))
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
	if total := sumSpaceRecovered(v.VacuumSpaceRecovered); total > 0 {
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
		func(i, j int) bool {
			if list[i].MaxTime != list[j].MaxTime {
				return list[i].MaxTime > list[j].MaxTime
			}
			return list[i].ID < list[j].ID
		},
		[]string{"SQLID", "Max", "Avg", "Count", "Query"},
		func(q qinfo) []string {
			return []string{q.ID, formatQueryDuration(q.MaxTime), formatQueryDuration(q.AvgTime), fmt.Sprintf("%d", q.Count), truncateQuery(q.Query, 80)}
		})
	emit("Most frequent queries (top 10)",
		func(i, j int) bool {
			if list[i].Count != list[j].Count {
				return list[i].Count > list[j].Count
			}
			return list[i].ID < list[j].ID
		},
		[]string{"SQLID", "Count", "Avg", "Max", "Query"},
		func(q qinfo) []string {
			return []string{q.ID, fmt.Sprintf("%d", q.Count), formatQueryDuration(q.AvgTime), formatQueryDuration(q.MaxTime), truncateQuery(q.Query, 80)}
		})
	emit("Most time consuming queries (top 10)",
		func(i, j int) bool {
			if list[i].TotalTime != list[j].TotalTime {
				return list[i].TotalTime > list[j].TotalTime
			}
			return list[i].ID < list[j].ID
		},
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

// writeDimensionsMarkdownRow appends one line of the Dimensions
// sub-section in --sql-detail markdown. Each entry reads "<name>
// <count>" with the count italicised — same sobre convention as the
// CLI (muted italic). Skips silently when the row has no data so
// empty axes never appear at all.
func writeDimensionsMarkdownRow(b *strings.Builder, label string, rows []analysis.DimensionCount) {
	if len(rows) == 0 {
		return
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s *%s*", r.Name, formatThousands(int64(r.Count))))
	}
	b.WriteString(fmt.Sprintf("- **%s**: %s\n", label, strings.Join(parts, ", ")))
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
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
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
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].stat.AcquiredWaitTime != pairs[j].stat.AcquiredWaitTime {
			return pairs[i].stat.AcquiredWaitTime > pairs[j].stat.AcquiredWaitTime
		}
		return pairs[i].stat.ID < pairs[j].stat.ID
	})
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
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].stat.StillWaitingTime != pairs[j].stat.StillWaitingTime {
			return pairs[i].stat.StillWaitingTime > pairs[j].stat.StillWaitingTime
		}
		return pairs[i].stat.ID < pairs[j].stat.ID
	})
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
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].totalLocks != pairs[j].totalLocks {
			return pairs[i].totalLocks > pairs[j].totalLocks
		}
		return pairs[i].stat.ID < pairs[j].stat.ID
	})
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
	// SQL PERFORMANCE section
	b.WriteString("## SQL PERFORMANCE\n\n")

	// Query load histogram
	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		queryLoad, unit, scale := computeQueryLoadHistogram(m)
		printHistogramMarkdown(&b, queryLoad, "Query load distribution", unit, scale, nil)
	}

	// Key metrics table
	writeSQLKeyMetricsMarkdown(&b, m)

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
			if queries[i].stat.TotalSize != queries[j].stat.TotalSize {
				return queries[i].stat.TotalSize > queries[j].stat.TotalSize
			}
			return queries[i].stat.ID < queries[j].stat.ID
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
			// Dimensions inlined into the Query Info block — same reason
			// as the CLI: kept next to "who ran this how many times"
			// instead of an extra sub-section header.
			dims := m.SQL.TopDimensionsForID(qid, 5)
			if !dims.IsEmpty() {
				writeDimensionsMarkdownRow(&b, "Databases", dims.Databases)
				writeDimensionsMarkdownRow(&b, "Users", dims.Users)
				writeDimensionsMarkdownRow(&b, "Apps", dims.Apps)
				writeDimensionsMarkdownRow(&b, "Hosts", dims.Hosts)
			}
		}
		b.WriteString("\n")

		// EVENTS section — same early position as the text renderer:
		// straight after Query Info so the operational signal is the
		// first thing a DBA reads. Rows are sorted by trigger count
		// descending; "#" and "Event total" are dropped to keep the
		// table focused on the "this query caused N of these" answer.
		eventsForMD := findEventsTriggeredByQuery(m.TopEvents, qid)
		if len(eventsForMD) > 0 {
			b.WriteString("### EVENTS\n\n")
			b.WriteString("| Event ID | Severity | Message | Triggered |\n")
			b.WriteString("|---|---|---|---:|\n")
			for _, r := range eventsForMD {
				msg := r.Event.Message
				if len(msg) > 90 {
					msg = msg[:89] + "…"
				}
				b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %d |\n",
					r.Event.ID, r.Event.Severity, msg, r.TriggerCnt))
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
			b.WriteString(fmt.Sprintf("- **Avg Duration**: %s\n", formatQueryDuration(sqlStat.AvgTime)))
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
				b.WriteString(fmt.Sprintf("### Slowest Run — %s, %s, pid=%s%s\n\n",
					formatQueryDuration(sr.DurationMs),
					sr.Timestamp.Format("2006-01-02 15:04:05"),
					sr.PID,
					formatSlowestRunDimensions(sr),
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
	writeSQLOverviewGlobalStatsMarkdown(&b, m)

	if len(m.QueryTypeStats) > 0 {
		b.WriteString("## Query Category Summary\n\n")
		writeQueryCategorySummaryMarkdown(&b, m.QueryTypeStats, m.TotalQueries)
		b.WriteString("## Query Type Distribution\n\n")
		writeQueryTypeDistributionMarkdown(&b, m.QueryTypeStats, m.TotalQueries)
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
		if dimensions[i].count != dimensions[j].count {
			return dimensions[i].count > dimensions[j].count
		}
		return dimensions[i].name < dimensions[j].name
	})
	for _, dim := range dimensions {
		b.WriteString(fmt.Sprintf("### %s (%d queries, %s)\n\n", dim.name, dim.count, formatQueryDuration(dim.totalTime)))
		types := breakdown[dim.name]
		type entry struct {
			name      string
			count     int
			totalTime float64
		}
		typeList := make([]entry, 0, len(types))
		for typeName, tc := range types {
			typeList = append(typeList, entry{typeName, tc.Count, tc.TotalTime})
		}
		sort.Slice(typeList, func(i, j int) bool {
			if typeList[i].count != typeList[j].count {
				return typeList[i].count > typeList[j].count
			}
			return typeList[i].name < typeList[j].name
		})
		rows := make([][]string, 0, len(typeList))
		for _, t := range typeList {
			rows = append(rows, []string{t.name, fmt.Sprintf("%d", t.count), formatQueryDuration(t.totalTime)})
		}
		mdTable(b, []string{"Query Type", "Count", "Total Time"}, "lrr", rows)
		b.WriteString("\n")
	}
}

// writeQueryCategorySummaryMarkdown renders the aggregated per-category
// (DML / DDL / TCL / UTILITY) view. Called from both the standalone
// sql-overview export and the in-line variant under SQL OVERVIEW.
func writeQueryCategorySummaryMarkdown(b *strings.Builder, qts map[string]*analysis.QueryTypeStat, totalQueries int) {
	if len(qts) == 0 {
		return
	}
	type catAgg struct {
		Count     int
		TotalTime float64
	}
	cats := make(map[string]*catAgg)
	for _, ts := range qts {
		if _, ok := cats[ts.Category]; !ok {
			cats[ts.Category] = &catAgg{}
		}
		cats[ts.Category].Count += ts.Count
		cats[ts.Category].TotalTime += ts.TotalTime
	}
	names := make([]string, 0, len(cats))
	for n := range cats {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if cats[names[i]].Count != cats[names[j]].Count {
			return cats[names[i]].Count > cats[names[j]].Count
		}
		return names[i] < names[j]
	})
	rows := make([][]string, 0, len(names))
	for _, n := range names {
		c := cats[n]
		pct := 0.0
		if totalQueries > 0 {
			pct = float64(c.Count) / float64(totalQueries) * 100
		}
		rows = append(rows, []string{n, fmt.Sprintf("%d", c.Count), fmt.Sprintf("%.1f%%", pct), formatQueryDuration(c.TotalTime)})
	}
	mdTable(b, []string{"Category", "Count", "%", "Total Time"}, "lrrr", rows)
	b.WriteString("\n")
}

// writeQueryTypeDistributionMarkdown renders the per-type table
// (SELECT / INSERT / UPDATE / …) shown right under the category
// summary. Same dual-call-site relationship.
func writeQueryTypeDistributionMarkdown(b *strings.Builder, qts map[string]*analysis.QueryTypeStat, totalQueries int) {
	if len(qts) == 0 {
		return
	}
	types := make([]*analysis.QueryTypeStat, 0, len(qts))
	for _, ts := range qts {
		types = append(types, ts)
	}
	sort.Slice(types, func(i, j int) bool {
		if types[i].Count != types[j].Count {
			return types[i].Count > types[j].Count
		}
		return types[i].Type < types[j].Type
	})
	rows := make([][]string, 0, len(types))
	for _, ts := range types {
		pct := 0.0
		if totalQueries > 0 {
			pct = float64(ts.Count) / float64(totalQueries) * 100
		}
		rows = append(rows, []string{
			ts.Type,
			fmt.Sprintf("%d", ts.Count),
			fmt.Sprintf("%.1f%%", pct),
			formatQueryDuration(ts.TotalTime),
			formatQueryDuration(ts.AvgTime),
			formatQueryDuration(ts.MaxTime),
		})
	}
	mdTable(b, []string{"Type", "Count", "%", "Total Time", "Avg Time", "Max Time"}, "lrrrrr", rows)
	b.WriteString("\n")
}

// exportSQLOverviewMarkdownTo writes SQL overview content to a strings.Builder.
// Used by ExportMarkdown in full mode.
func exportSQLOverviewMarkdownTo(b *strings.Builder, m analysis.SQLMetrics) {
	// Global statistics
	b.WriteString("### Global Statistics\n\n")
	writeSQLOverviewGlobalStatsMarkdown(b, m)

	if len(m.QueryTypeStats) > 0 {
		b.WriteString("### Query Category Summary\n\n")
		writeQueryCategorySummaryMarkdown(b, m.QueryTypeStats, m.TotalQueries)
		b.WriteString("### Query Type Distribution\n\n")
		writeQueryTypeDistributionMarkdown(b, m.QueryTypeStats, m.TotalQueries)
	}

	exportQueryTypeBreakdownMarkdown(b, "Per Database", m.QueryTypesByDatabase)
	exportQueryTypeBreakdownMarkdown(b, "Per User", m.QueryTypesByUser)
	exportQueryTypeBreakdownMarkdown(b, "Per Host", m.QueryTypesByHost)
	exportQueryTypeBreakdownMarkdown(b, "Per Application", m.QueryTypesByApp)
}

// exportSQLSummaryMarkdownTo writes SQL performance content to a strings.Builder.
// Used by ExportMarkdown in full mode.
func exportSQLSummaryMarkdownTo(b *strings.Builder, m analysis.SQLMetrics, tempFiles analysis.TempFileMetrics, locks analysis.LockMetrics) {
	// Query load histogram
	if !m.StartTimestamp.IsZero() && !m.EndTimestamp.IsZero() {
		queryLoad, unit, scale := computeQueryLoadHistogram(m)
		printHistogramMarkdown(b, queryLoad, "Query load distribution", unit, scale, nil)
	}

	// Key metrics table
	writeSQLKeyMetricsMarkdown(b, m)

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
			if queries[i].stat.TotalSize != queries[j].stat.TotalSize {
				return queries[i].stat.TotalSize > queries[j].stat.TotalSize
			}
			return queries[i].stat.ID < queries[j].stat.ID
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

// writeKVPairsTableMarkdown writes the SQL summary / overview key
// metrics as a flat Markdown bullet list rather than a table — the
// "|  |  |  |  |" empty-header pattern read worse in raw view than
// labeled bullets. Labels get a trailing space-pad so the colons line
// up vertically in raw view (renderers collapse the run, but the raw
// file reads like a table of contents). Each row in rows is two
// (label, value) pairs side-by-side; empty labels are skipped so the
// dangling "99th percentile" / "" cell in the overview disappears
// cleanly rather than emitting a "**:**" bullet.
func writeKVPairsTableMarkdown(b *strings.Builder, rows [][4]string) {
	type pair struct{ label, value string }
	var pairs []pair
	for _, r := range rows {
		if r[0] != "" {
			pairs = append(pairs, pair{r[0], r[1]})
		}
		if r[2] != "" {
			pairs = append(pairs, pair{r[2], r[3]})
		}
	}
	if len(pairs) == 0 {
		return
	}
	maxW := 0
	for _, p := range pairs {
		if l := len(p.label); l > maxW {
			maxW = l
		}
	}
	for _, p := range pairs {
		b.WriteString(fmt.Sprintf("- **%s**%s : %s\n",
			p.label, strings.Repeat(" ", maxW-len(p.label)), p.value))
	}
	b.WriteString("\n")
}

// writeSQLKeyMetricsMarkdown emits the "Total query duration / Total
// queries parsed / …" 4-row block — the one shown in the default
// report, the --sql-summary export and the inline SQL PERFORMANCE
// variant under the full report. Same content across all three.
func writeSQLKeyMetricsMarkdown(b *strings.Builder, m analysis.SQLMetrics) {
	top1Slow := countSlowQueries(m)
	writeKVPairsTableMarkdown(b, [][4]string{
		{"Total query duration", formatQueryDuration(m.SumQueryDuration), "Total queries parsed", fmt.Sprintf("%d", m.TotalQueries)},
		{"Total unique queries", fmt.Sprintf("%d", m.UniqueQueries), "Top 1% slow queries", fmt.Sprintf("%d", top1Slow)},
		{"Query max duration", formatQueryDuration(m.MaxQueryDuration), "Query min duration", formatQueryDuration(m.MinQueryDuration)},
		{"Query median duration", formatQueryDuration(m.MedianQueryDuration), "Query 99% max duration", formatQueryDuration(m.P99QueryDuration)},
	})
}

// writeSQLOverviewGlobalStatsMarkdown emits the Global Statistics
// block at the top of --sql-overview (standalone and inline).
func writeSQLOverviewGlobalStatsMarkdown(b *strings.Builder, m analysis.SQLMetrics) {
	writeKVPairsTableMarkdown(b, [][4]string{
		{"Total queries", fmt.Sprintf("%d", m.TotalQueries), "Unique queries", fmt.Sprintf("%d", m.UniqueQueries)},
		{"Total duration", formatQueryDuration(m.SumQueryDuration), "Median duration", formatQueryDuration(m.MedianQueryDuration)},
		{"Min duration", formatQueryDuration(m.MinQueryDuration), "Max duration", formatQueryDuration(m.MaxQueryDuration)},
		{"99th percentile", formatQueryDuration(m.P99QueryDuration), "", ""},
	})
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
