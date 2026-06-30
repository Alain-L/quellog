//go:build !js

package output

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Alain-L/quellog/analysis"

	"golang.org/x/term"
)

// PrintMetrics displays the aggregated metrics.
// If full is true, displays extended analysis sections.
func PrintMetrics(m analysis.AggregatedMetrics, sections []string, full bool) {

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
	bold := ansiBold
	reset := ansiReset

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

	// Server lifecycle (incl. replication sub-zone): placed right after
	// SUMMARY so the cluster context (crash at 14h, reload at 08h, lost
	// walreceiver at 16h, etc.) frames every other section below it.
	// Auto-hides on steady-state logs via HasAny on either side.
	if has("server") && (m.Server.HasAny() || m.Replication.HasAny) {
		printServerSection(m.Server, m.Replication, bold, reset)
	}

	// SQL summary section (skip in full mode — enriched version added at the end)
	if !full && has("sql_summary") && m.SQL.TotalQueries > 0 {
		PrintSQLSummary(m.SQL, true)
	}

	// Events
	if has("events") && len(m.EventSummaries) > 0 {
		PrintEventsReport(m.EventSummaries, m.TopEvents, false)
	} else if has("errors") && len(m.EventSummaries) > 0 {
		PrintEventsReport(m.EventSummaries, m.TopEvents, true)
	}

	// Temp Files section.
	if has("tempfiles") && m.TempFiles.Count > 0 {

		fmt.Println(bold + "\nTEMP FILES\n" + reset)

		// Size histogram (always shown)
		hist, unit, scaleFactor := computeTempFileHistogram(m.TempFiles)
		PrintHistogram(hist, "Temp file size", unit, scaleFactor, nil)

		// Count histogram (shown with --tempfiles or --full)
		if full || !has("all") {
			countHist, countUnit, countScale := computeTempFileCountHistogram(m.TempFiles)
			PrintHistogram(countHist, "Temp file count", countUnit, countScale, nil)
		}

		fmt.Printf("  %-25s : %d\n", "Temp file messages", m.TempFiles.Count)
		fmt.Printf("  %-25s : %s\n", "Cumulative temp file size", FormatBytes(m.TempFiles.TotalSize))
		avgSize := int64(0)
		if m.TempFiles.Count > 0 {
			avgSize = m.TempFiles.TotalSize / int64(m.TempFiles.Count)
		}
		fmt.Printf("  %-25s : %s\n", "Average temp file size", FormatBytes(avgSize))
		fmt.Printf("  %-25s : %s\n", "Max temp file size", FormatBytes(m.TempFiles.MaxSize))

		// Queries generating temp files (shown with --tempfiles or --full)
		if (full || !has("all")) && len(m.TempFiles.QueryStats) > 0 {
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
						FormatBytes(stat.TotalSize))
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
						FormatBytes(stat.TotalSize))
				}
			}
		}
	}

	// Locks section
	if has("locks") && m.Locks.TotalEvents > 0 {
		fmt.Println(bold + "\nLOCKS\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Total lock events", m.Locks.TotalEvents)
		fmt.Printf("  %-25s : %d\n", "Still waiting", m.Locks.WaitingEvents)
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

		// Relation distribution
		if len(m.Locks.RelationStats) > 0 {
			fmt.Println()
			fmt.Println("  Relations:")
			printLockStats(m.Locks.RelationStats, m.Locks.TotalEvents)
		}

		// Waiting queries (shown with --locks or --full)
		if (full || !has("all")) && len(m.Locks.QueryStats) > 0 {
			termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				termWidth = 120
			}

			// Sort by total wait time descending
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

				fmt.Println(bold + "\nWaiting queries:" + reset)

				if termWidth >= 120 {
					tableWidth := int(float64(termWidth) * 0.9)
					if tableWidth > termWidth-10 {
						tableWidth = termWidth - 10
					}
					// SQLID(9) + Acquired(10) + Waiting(10) + Total Wait(15) = 44
					fixedWidth := 44
					spacingWidth := 8
					queryWidth := tableWidth - fixedWidth - spacingWidth
					if queryWidth < 40 {
						queryWidth = 40
					}

					fmt.Printf("%s%-9s  %-*s  %10s  %10s  %15s%s\n",
						bold, "SQLID", queryWidth, "Query", "Acquired", "Waiting", "Total Wait", reset)
					fmt.Println(strings.Repeat("-", tableWidth))

					for i := 0; i < limit; i++ {
						stat := pairs[i].stat
						truncatedQuery := truncateQuery(stat.NormalizedQuery, queryWidth)
						fmt.Printf("%-9s  %-*s  %10d  %10d  %15s\n",
							stat.ID,
							queryWidth, truncatedQuery,
							stat.AcquiredCount,
							stat.StillWaitingCount,
							formatQueryDuration(stat.TotalWaitTime))
					}
				} else {
					fmt.Printf("%s%-9s  %10s  %10s  %15s%s\n",
						bold, "SQLID", "Acquired", "Waiting", "Total Wait", reset)
					fmt.Println(strings.Repeat("-", 60))

					for i := 0; i < limit; i++ {
						stat := pairs[i].stat
						fmt.Printf("%-9s  %10d  %10d  %15s\n",
							stat.ID,
							stat.AcquiredCount,
							stat.StillWaitingCount,
							formatQueryDuration(stat.TotalWaitTime))
					}
				}
			}
		}

		// Blocking queries (shown with --locks or --full)
		if (full || !has("all")) && len(m.Locks.Events) > 0 {
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

				termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					termWidth = 120
				}

				limit := 10
				if limit > len(pairs) {
					limit = len(pairs)
				}

				fmt.Println(bold + "\nBlocking queries:" + reset)

				if termWidth >= 120 {
					tableWidth := int(float64(termWidth) * 0.9)
					if tableWidth > termWidth-10 {
						tableWidth = termWidth - 10
					}
					fixedWidth := 49
					spacingWidth := 8
					queryWidth := tableWidth - fixedWidth - spacingWidth
					if queryWidth < 40 {
						queryWidth = 40
					}

					fmt.Printf("%s%-9s  %-*s  %10s  %15s  %15s%s\n",
						bold, "SQLID", queryWidth, "Query", "Blocked", "Avg Wait", "Total Wait", reset)
					fmt.Println(strings.Repeat("-", tableWidth))

					for i := 0; i < limit; i++ {
						bs := pairs[i].stat
						truncatedQuery := truncateQuery(bs.query, queryWidth)
						if truncatedQuery == "" {
							truncatedQuery = "(unknown)"
						}
						avgWait := bs.totalWait / float64(bs.blockCount)
						fmt.Printf("%-9s  %-*s  %10d  %15s  %15s\n",
							bs.queryID,
							queryWidth, truncatedQuery,
							bs.blockCount,
							formatQueryDuration(avgWait),
							formatQueryDuration(bs.totalWait))
					}
				} else {
					header := fmt.Sprintf("%-8s  %-10s  %-10s  %-12s  %-12s\n", "SQLID", "Type", "Blocked", "Avg Wait", "Total Wait")
					fmt.Print(bold + header + reset)
					fmt.Println(strings.Repeat("-", 80))

					for i := 0; i < limit; i++ {
						bs := pairs[i].stat
						qType := analysis.QueryTypeFromID(bs.queryID)
						avgWait := bs.totalWait / float64(bs.blockCount)
						fmt.Printf("%-8s  %-10s  %-10d  %-12s  %-12s\n",
							bs.queryID,
							qType,
							bs.blockCount,
							formatQueryDuration(avgWait),
							formatQueryDuration(bs.totalWait))
					}
				}
			}
		}
	}

	// Maintenance Metrics: split into two sibling sections so the
	// reader doesn't have to mentally separate vacuum from analyze
	// inside one wall of text.
	if has("maintenance") {
		if m.Vacuum.VacuumCount > 0 {
			printAutovacuumSection(m.Vacuum)
		}
		if m.Vacuum.AnalyzeCount > 0 {
			printAutoanalyzeSection(m.Vacuum)
		}
	}

	// Checkpoints section
	if has("checkpoints") && (m.Checkpoints.CompleteCount > 0 || m.Checkpoints.WarningCount > 0) {
		fmt.Println(bold + "\nCHECKPOINTS\n" + reset)

		if m.Checkpoints.CompleteCount > 0 {
			avgWriteSeconds := m.Checkpoints.TotalWriteTimeSeconds / float64(m.Checkpoints.CompleteCount)
			avgDuration := time.Duration(avgWriteSeconds * float64(time.Second)).Truncate(time.Second)
			maxDuration := time.Duration(m.Checkpoints.MaxWriteTimeSeconds * float64(time.Second)).Truncate(time.Second)

			// Histogram with frequency (4-hour buckets)
			hist, _, scaleFactor := computeCheckpointHistogram(m.Checkpoints)
			PrintCheckpointHistogram(hist, "Checkpoints", scaleFactor, 4.0)

			fmt.Printf("  %-25s : %d\n", "Checkpoint count", m.Checkpoints.CompleteCount)
			fmt.Printf("  %-25s : %s\n", "Avg checkpoint write time", avgDuration)
			fmt.Printf("  %-25s : %s\n", "Max checkpoint write time", maxDuration)

			if len(m.Checkpoints.WALDistances) > 0 {
				avgDistMB := float64(m.Checkpoints.TotalDistanceKB) / float64(m.Checkpoints.CompleteCount) / 1024.0
				maxDistMB := float64(m.Checkpoints.MaxDistanceKB) / 1024.0
				fmt.Printf("  %-25s : %.1f MB\n", "WAL per checkpoint (avg)", avgDistMB)
				fmt.Printf("  %-25s : %.1f MB\n", "WAL per checkpoint (max)", maxDistMB)
			}
		}

		if m.Checkpoints.WarningCount > 0 {
			italic := ansiItalic
			if m.Checkpoints.WarningMinIntervalSeconds == m.Checkpoints.WarningMaxIntervalSeconds {
				fmt.Printf("  "+bold+"%-25s : %d"+reset+"   "+italic+"%ds apart"+reset+"\n",
					"Too frequent warnings", m.Checkpoints.WarningCount,
					m.Checkpoints.WarningMinIntervalSeconds)
			} else {
				fmt.Printf("  "+bold+"%-25s : %d"+reset+"   "+italic+"%d-%ds apart"+reset+"\n",
					"Too frequent warnings", m.Checkpoints.WarningCount,
					m.Checkpoints.WarningMinIntervalSeconds, m.Checkpoints.WarningMaxIntervalSeconds)
			}
		}

		// Display checkpoint types
		if len(m.Checkpoints.TypeCounts) > 0 {
			fmt.Println()
			fmt.Println("  Checkpoint types:")

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

			// Compute total duration for percentages and rate.
			duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
			durationHours := duration.Hours()

			// Determine max width for type names.
			maxTypeLen := 0
			for _, pair := range pairs {
				if len(pair.Name) > maxTypeLen {
					maxTypeLen = len(pair.Name)
				}
			}
			if maxTypeLen < 10 {
				maxTypeLen = 10
			}

			// Display each type with count, percentage and rate.
			muted := ansiMutedItalic
			reset := ansiReset

			for _, pair := range pairs {
				percentage := float64(pair.Count) / float64(m.Checkpoints.CompleteCount) * 100

				// Compute rate (checkpoints per hour) for this type.
				rate := 0.0
				if durationHours > 0 {
					rate = float64(pair.Count) / durationHours
				}

				// Format: type (left-aligned), count (right, 3 digits), percentage (right, 6 chars), rate (muted)
				fmt.Printf("    %-*s  %3d  %5.1f%%   %s%.2f/h%s\n",
					maxTypeLen, pair.Name, pair.Count, percentage, muted, rate, reset)
			}
		}

		// WAL distance vs estimate histogram
		if walBuckets := computeWALDistanceHistogram(m.Checkpoints); len(walBuckets) > 0 {
			PrintWALDistanceHistogram(walBuckets)
		}
	}

	// Connections & Sessions Metrics section.
	if has("connections") && m.Connections.ConnectionReceivedCount > 0 {
		fmt.Println(bold + "\nCONNECTIONS & SESSIONS\n" + reset)

		// Detailed mode: --connections explicit or --full
		isDetailedMode := full || !has("all")

		// Concurrent sessions histogram (always shown, more buckets in detailed mode)
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
				PrintConcurrentHistogramWithTZ(concurrentHist, "Concurrent sessions", concurrentScale, labels, peakTimes, &m.Global.MinTimestamp)
			}
		}

		// Connection distribution histogram (only in detailed mode)
		if isDetailedMode {
			hist, _, scaleFactor := computeConnectionsHistogram(m.Connections.IterateConnections, m.Connections.ConnectionsCount(), m.Global.MinTimestamp, m.Global.MaxTimestamp, 12)
			PrintHistogram(hist, "Connection distribution", "", scaleFactor, nil)
		}

		fmt.Printf("  %-25s : %d\n", "Connection count", m.Connections.ConnectionReceivedCount)
		fmt.Printf("  %-25s : %d\n", "Disconnection count", m.Connections.DisconnectionCount)
		if duration.Hours() > 0 {
			avgConnPerHour := float64(m.Connections.ConnectionReceivedCount) / duration.Hours()
			fmt.Printf("  %-25s : %.2f\n", "Avg connections per hour", avgConnPerHour)
		}
		if m.Connections.SessionStats.Count > 0 {
			// Average
			avgSessionTime := time.Duration(float64(m.Connections.TotalSessionTime) / float64(m.Connections.DisconnectionCount))
			fmt.Printf("  %-25s : %s\n", "Avg session time", formatSessionDuration(avgSessionTime))
			// Median (P²-estimated; <5% error after 50 samples)
			fmt.Printf("  %-25s : %s\n", "Median session time", formatSessionDuration(m.Connections.SessionStats.Median))
		} else if m.Connections.DisconnectionCount > 0 {
			fmt.Printf("  %-25s : %s\n", "Avg session time", "N/A")
		}

		// Average concurrent (always shown if data available)
		if len(m.Connections.Connections) > 0 && m.Connections.DisconnectionCount > 0 {
			duration := m.Global.MaxTimestamp.Sub(m.Global.MinTimestamp)
			if duration > 0 {
				avgConcurrent := float64(m.Connections.TotalSessionTime) / float64(duration)
				fmt.Printf("  %-25s : %.1f\n", "Avg concurrent", avgConcurrent)
			}
		}

		// Peak concurrent sessions (always shown)
		if m.Connections.PeakConcurrentSessions > 0 {
			fmt.Printf("  %-25s : %d", "Maximum simultaneous", m.Connections.PeakConcurrentSessions)
			if !m.Connections.PeakConcurrentTimestamp.IsZero() {
				fmt.Printf("     (at %s)", m.Connections.PeakConcurrentTimestamp.Format("2006-01-02 15:04:05"))
			}
			fmt.Println()
		}

		// Detailed mode: --connections explicit or --full
		isExplicit := full || !has("all")
		if isExplicit && m.Connections.SessionStats.Count > 0 {
			printDetailedConnectionStats(m, bold, reset, true)
		}
	}

	// Unique Clients section.
	if has("clients") && (m.UniqueEntities.UniqueDbs > 0 || m.UniqueEntities.UniqueUsers > 0 || m.UniqueEntities.UniqueApps > 0 || m.UniqueEntities.UniqueHosts > 0) {
		fmt.Println(bold + "\nCLIENTS\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Unique DBs", m.UniqueEntities.UniqueDbs)
		fmt.Printf("  %-25s : %d\n", "Unique Users", m.UniqueEntities.UniqueUsers)
		fmt.Printf("  %-25s : %d\n", "Unique Apps", m.UniqueEntities.UniqueApps)
		fmt.Printf("  %-25s : %d\n", "Unique Hosts", m.UniqueEntities.UniqueHosts)

		// Calculate total logs for percentage calculation
		totalLogs := m.Global.Count

		// Detailed mode: --clients explicit or --full
		isExplicit := full || !has("all")
		topPrefix := ""
		topLimit := 0 // 0 means no limit
		if !isExplicit {
			topPrefix = "TOP "
			topLimit = 10 // Limit to top 10 when not explicit
		}

		// Display users with counts and percentages
		if m.UniqueEntities.UniqueUsers > 0 && m.UniqueEntities.UserCounts != nil {
			fmt.Println(bold + "\n" + topPrefix + "USERS\n" + reset)
			sortedUsers := analysis.SortByCount(m.UniqueEntities.UserCounts)
			limit := len(sortedUsers)
			remaining := 0
			if topLimit > 0 && topLimit < limit {
				remaining = limit - topLimit
				limit = topLimit
			}
			for i := 0; i < limit; i++ {
				item := sortedUsers[i]
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				fmt.Printf("  %-25s %6d  %5.1f%%\n", item.Name, item.Count, percentage)
			}
			if remaining > 0 {
				fmt.Printf("  [%d more...]\n", remaining)
			}
		}

		// Display apps with counts and percentages
		if m.UniqueEntities.UniqueApps > 0 && m.UniqueEntities.AppCounts != nil {
			fmt.Println(bold + "\n" + topPrefix + "APPS\n" + reset)
			sortedApps := analysis.SortByCount(m.UniqueEntities.AppCounts)
			limit := len(sortedApps)
			remaining := 0
			if topLimit > 0 && topLimit < limit {
				remaining = limit - topLimit
				limit = topLimit
			}
			for i := 0; i < limit; i++ {
				item := sortedApps[i]
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				fmt.Printf("  %-25s %6d  %5.1f%%\n", item.Name, item.Count, percentage)
			}
			if remaining > 0 {
				fmt.Printf("  [%d more...]\n", remaining)
			}
		}

		// Display databases with counts and percentages
		if m.UniqueEntities.UniqueDbs > 0 && m.UniqueEntities.DBCounts != nil {
			fmt.Println(bold + "\n" + topPrefix + "DATABASES\n" + reset)
			sortedDBs := analysis.SortByCount(m.UniqueEntities.DBCounts)
			limit := len(sortedDBs)
			remaining := 0
			if topLimit > 0 && topLimit < limit {
				remaining = limit - topLimit
				limit = topLimit
			}
			for i := 0; i < limit; i++ {
				item := sortedDBs[i]
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				fmt.Printf("  %-25s %6d  %5.1f%%\n", item.Name, item.Count, percentage)
			}
			if remaining > 0 {
				fmt.Printf("  [%d more...]\n", remaining)
			}
		}

		// Display hosts with counts and percentages
		if m.UniqueEntities.UniqueHosts > 0 && m.UniqueEntities.HostCounts != nil {
			fmt.Println(bold + "\n" + topPrefix + "HOSTS\n" + reset)
			sortedHosts := analysis.SortByCount(m.UniqueEntities.HostCounts)
			limit := len(sortedHosts)
			remaining := 0
			if topLimit > 0 && topLimit < limit {
				remaining = limit - topLimit
				limit = topLimit
			}
			for i := 0; i < limit; i++ {
				item := sortedHosts[i]
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				fmt.Printf("  %-25s %6d  %5.1f%%\n", item.Name, item.Count, percentage)
			}
			if remaining > 0 {
				fmt.Printf("  [%d more...]\n", remaining)
			}
		}

		// Display user×database combinations
		if len(m.UniqueEntities.UserDbCombos) > 0 {
			fmt.Println(bold + "\n" + topPrefix + "USER × DATABASE\n" + reset)
			sortedCombos := analysis.SortByCount(m.UniqueEntities.UserDbCombos)
			limit := len(sortedCombos)
			remaining := 0
			if topLimit > 0 && topLimit < limit {
				remaining = limit - topLimit
				limit = topLimit
			}
			for i := 0; i < limit; i++ {
				item := sortedCombos[i]
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				// Split the combo key "user|database"
				parts := strings.SplitN(item.Name, "|", 2)
				if len(parts) == 2 {
					fmt.Printf("  %-25s × %-25s %6d  %5.1f%%\n", parts[0], parts[1], item.Count, percentage)
				}
			}
			if remaining > 0 {
				fmt.Printf("  [%d more...]\n", remaining)
			}
		}

		// Display user×host combinations
		if len(m.UniqueEntities.UserHostCombos) > 0 {
			fmt.Println(bold + "\n" + topPrefix + "USER × HOST\n" + reset)
			sortedCombos := analysis.SortByCount(m.UniqueEntities.UserHostCombos)
			limit := len(sortedCombos)
			remaining := 0
			if topLimit > 0 && topLimit < limit {
				remaining = limit - topLimit
				limit = topLimit
			}
			for i := 0; i < limit; i++ {
				item := sortedCombos[i]
				percentage := float64(item.Count) * 100.0 / float64(totalLogs)
				// Split the combo key "user|host"
				parts := strings.SplitN(item.Name, "|", 2)
				if len(parts) == 2 {
					fmt.Printf("  %-25s × %-25s %6d  %5.1f%%\n", parts[0], parts[1], item.Count, percentage)
				}
			}
			if remaining > 0 {
				fmt.Printf("  [%d more...]\n", remaining)
			}
		}
	}
	fmt.Println()

	// Full mode: SQL OVERVIEW + SQL PERFORMANCE at the end. TempFiles
	// and Locks are zeroed because they were already shown above as
	// their own sections — passing them again would duplicate output.
	if full && m.SQL.TotalQueries > 0 {
		PrintSQLOverview(m.SQL)
		PrintSQLSummaryWithContext(m.SQL, analysis.TempFileMetrics{}, analysis.LockMetrics{}, false)
	}
}

// printServerSection renders the SERVER lifecycle section: counters
// for starts / reloads / shutdowns / crashes, a "config parameter
// changes" mini-table (when present), a compact timeline, and a
// "Replication" sub-zone aggregating walreceiver/walsender health.
// Nothing is emitted when neither side captured a marker — checked by
// the caller.
func printServerSection(s analysis.ServerMetrics, r analysis.ReplicationMetrics, bold, reset string) {
	fmt.Println(bold + "\nSERVER\n" + reset)

	// Starts — hidden when zero so logs that only carry replication
	// markers do not show a misleading "Starts: 0" line.
	if s.StartCount > 0 {
		startDetail := ""
		if len(s.StartTimes) > 0 {
			startDetail = "   " + ansiMutedItalic + "(first: " + s.StartTimes[0].Format("2006-01-02 15:04:05") + ")" + reset
		}
		fmt.Printf("  %-25s : %d%s\n", "Starts", s.StartCount, startDetail)
	}

	// Reloads — same auto-hide behavior.
	if s.ReloadCount > 0 {
		reloadDetail := ""
		if len(s.ReloadTimes) > 0 {
			reloadDetail = "   " + ansiMutedItalic + "(last: " + s.ReloadTimes[len(s.ReloadTimes)-1].Format("15:04:05") + ")" + reset
		}
		fmt.Printf("  %-25s : %d%s\n", "Reloads (SIGHUP)", s.ReloadCount, reloadDetail)
	}

	// Shutdowns.
	if s.ShutdownFastCount+s.ShutdownImmediateCount+s.ShutdownSmartCount > 0 {
		fmt.Printf("  %-25s : %d fast, %d immediate, %d smart\n",
			"Shutdowns",
			s.ShutdownFastCount, s.ShutdownImmediateCount, s.ShutdownSmartCount)
	}

	// Crash recoveries.
	if s.CrashRecoveryCount > 0 {
		fmt.Printf("  %-25s : %d   "+ansiMutedItalic+"(\"not properly shut down\")"+reset+"\n",
			"Crash recoveries", s.CrashRecoveryCount)
	}

	// Backend crashes — render signal breakdown.
	if s.BackendCrashCount > 0 {
		fmt.Printf("  %-25s : %d   "+ansiMutedItalic+"%s"+reset+"\n",
			"Backend crashes", s.BackendCrashCount, formatSignalCounts(s.SignalCounts))
	}

	// Auxiliary process exits.
	if s.AuxProcessExitCount > 0 {
		fmt.Printf("  %-25s : %d\n", "Auxiliary process exits", s.AuxProcessExitCount)
	}

	// Config parameter changes table.
	if len(s.ParameterChanges) > 0 {
		printParameterChangesTable(s.ParameterChanges)
	}

	// Timeline.
	if len(s.Timeline) > 0 {
		printServerTimeline(s.Timeline)
	}

	// Replication sub-zone — folded into SERVER because on real-world
	// logs it rarely fires more than one or two markers, and the rhythm
	// reads better next to the cluster lifecycle than in its own header.
	if r.HasAny {
		printReplicationSubZone(r, s.HasAny())
	}
}

// printReplicationSubZone renders the compact "Replication" block
// inside the SERVER section: one indented line per category that
// actually fired. Termination markers are summed under one headline
// with the most-recent timestamp inlined so a DBA jumps straight to
// the right window in the raw logs. The blank-line separator is
// suppressed when the parent SERVER section had no content of its own
// (server-less log) so we don't pile two blank lines under the header.
func printReplicationSubZone(r analysis.ReplicationMetrics, serverHasContent bool) {
	muted := ansiMutedItalic
	reset := ansiReset

	if serverHasContent {
		fmt.Println()
	}
	fmt.Println("  Replication:")

	if v := r.Markers["stream_started"]; v > 0 {
		line := fmt.Sprintf("    %-24s : %d", "Stream reconnects", v)
		if r.PeakHourLabel != "" && r.PeakHourCount > 1 {
			line += fmt.Sprintf("   %s(peak %d× in %s)%s", muted, r.PeakHourCount, r.PeakHourLabel, reset)
		}
		fmt.Println(line)
	}
	if v := r.Markers["recovery_paused"]; v > 0 {
		fmt.Printf("    %-24s : %d\n", "Recovery pauses", v)
	}
	if v := r.Markers["conflict_terminate"] + r.Markers["conflict_cancel"]; v > 0 {
		line := fmt.Sprintf("    %-24s : %d", "Conflicts with recovery", v)
		if n := len(r.ConflictQueries); n > 0 {
			line += fmt.Sprintf("   %s(%d unique queries terminated)%s", muted, n, reset)
		}
		fmt.Println(line)
	}
	if v := r.Markers["slot_invalidated"]; v > 0 {
		fmt.Printf("    %-24s : %d\n", "Invalidated slots", v)
	}
	// One line per termination cause — the four PG markers map to four
	// diametrically opposite scenarios (replica side vs primary side),
	// so collapsing them would hide the diagnostic. Each row carries a
	// short hint naming the side of the cluster at fault; the
	// LastTermination timestamp is appended to the last fired row only
	// so a DBA jumps to the right window without us repeating it on
	// every line.
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
		line := fmt.Sprintf("    %-24s : %d", t.label, r.Markers[t.key])
		suffix := t.hint
		if i == len(fired)-1 && !r.LastTermination.IsZero() {
			suffix += ", last " + r.LastTermination.Format("15:04:05")
		}
		line += fmt.Sprintf("   %s(%s)%s", muted, suffix, reset)
		fmt.Println(line)
	}
}

// signalName maps the POSIX signal numbers PostgreSQL backends are
// commonly terminated with to their canonical names. Unknown numbers
// fall through to "signal N" so the renderer never lies — but in
// practice 6/9/11/15 cover essentially every real backend crash log.
var signalName = map[string]string{
	"1":  "SIGHUP",
	"2":  "SIGINT",
	"3":  "SIGQUIT",
	"6":  "SIGABRT", // assertion failure, abort()
	"9":  "SIGKILL", // OOM killer, kill -9
	"11": "SIGSEGV", // segfault
	"13": "SIGPIPE",
	"14": "SIGALRM",
	"15": "SIGTERM", // pg_ctl stop, systemd
}

// formatSignalCounts renders the SignalCounts map as a human-readable
// "(SIGKILL ×1, SIGSEGV ×2)" string. Sorted by signal number so the
// output is stable across runs.
func formatSignalCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	sigs := make([]string, 0, len(counts))
	for sig := range counts {
		sigs = append(sigs, sig)
	}
	sort.Slice(sigs, func(i, j int) bool {
		ai, _ := strconv.Atoi(sigs[i])
		aj, _ := strconv.Atoi(sigs[j])
		if ai != aj {
			return ai < aj
		}
		return sigs[i] < sigs[j]
	})
	var b strings.Builder
	b.WriteByte('(')
	for i, sig := range sigs {
		if i > 0 {
			b.WriteString(", ")
		}
		name, ok := signalName[sig]
		if !ok {
			name = "signal " + sig
		}
		fmt.Fprintf(&b, "%s ×%d", name, counts[sig])
	}
	b.WriteByte(')')
	return b.String()
}

// printParameterChangesTable emits the "Parameter / Old / New / When"
// mini-table. Old is always empty for SIGHUP-driven changes (PG does
// not log the previous value); the column is kept so the layout
// matches a future "diff" enrichment without breaking consumers.
func printParameterChangesTable(changes []analysis.ServerParameterChange) {
	fmt.Println()
	fmt.Println("  Config parameter changes:")
	// Compute column widths.
	paramW := len("Parameter")
	oldW := len("Old")
	newW := len("New")
	for _, c := range changes {
		if len(c.Parameter) > paramW {
			paramW = len(c.Parameter)
		}
		if len(c.Old) > oldW {
			oldW = len(c.Old)
		}
		if len(c.New) > newW {
			newW = len(c.New)
		}
	}
	if oldW < 3 {
		oldW = 3
	}
	if newW < 3 {
		newW = 3
	}
	fmt.Printf("    %-*s  %-*s  %-*s  %s\n", paramW, "Parameter", oldW, "Old", newW, "New", "When")
	for _, c := range changes {
		old := c.Old
		if old == "" {
			old = "-"
		}
		fmt.Printf("    %-*s  %-*s  %-*s  %s\n",
			paramW, c.Parameter,
			oldW, old,
			newW, c.New,
			c.Timestamp.Format("15:04:05"))
	}
}

// printServerTimeline emits the compact timeline at the bottom of the
// section: one line per event, "HH:MM:SS  event-tag  detail".
func printServerTimeline(events []analysis.ServerTimelineEvent) {
	fmt.Println()
	fmt.Println("  Timeline:")
	for _, ev := range events {
		fmt.Printf("    %s  %-10s  %s\n",
			ev.Timestamp.Format("15:04:05"),
			ev.Kind,
			ev.Detail)
	}
}

// printDetailedConnectionStats displays detailed connection and session statistics.
// This is shown only when --connections is explicitly used (not as part of "all").
func printDetailedConnectionStats(m analysis.AggregatedMetrics, bold, reset string, showAll bool) {
	// 1. SESSION TIME DISTRIBUTION (with histogram bars)
	if m.Connections.SessionStats.Count > 0 {
		fmt.Println()
		dist := m.Connections.SessionDistribution

		// Define bucket order for consistent display
		orderedBuckets := []string{"< 1s", "1s - 1min", "1min - 30min", "30min - 2h", "2h - 5h", "> 5h"}

		// Calculate scale factor to limit histogram width to 40 chars (like connections histogram)
		maxValue := 0
		for _, count := range dist {
			if count > maxValue {
				maxValue = count
			}
		}
		histogramWidth := 40
		scaleFactor := int(math.Ceil(float64(maxValue) / float64(histogramWidth)))
		if scaleFactor < 1 {
			scaleFactor = 1
		}

		// Use PrintHistogram like for connections
		PrintHistogram(dist, "Session time distribution", "", scaleFactor, orderedBuckets)
	}

	// 2. SESSION DURATION BY USER (tables at the end, more readable)
	if len(m.Connections.SessionsByUser) > 0 {
		fmt.Println(bold + "\nSESSION DURATION BY USER\n" + reset)

		// Sort by total session count (descending)
		type userStats struct {
			user      string
			stats     analysis.DurationStats
			cumulated time.Duration
		}
		var sortedUsers []userStats
		for user, s := range m.Connections.SessionsByUser {
			stats := s.Stats()
			sortedUsers = append(sortedUsers, userStats{user: user, stats: stats, cumulated: s.Cumulated()})
		}
		sort.Slice(sortedUsers, func(i, j int) bool {
			if sortedUsers[i].stats.Count != sortedUsers[j].stats.Count {
				return sortedUsers[i].stats.Count > sortedUsers[j].stats.Count
			}
			return sortedUsers[i].user < sortedUsers[j].user
		})

		// Display header
		fmt.Printf("  %-25s  %8s  %8s  %8s  %8s  %8s  %10s\n",
			"User", "Sessions", "Min", "Max", "Avg", "Median", "Cumulated")
		fmt.Println("  " + strings.Repeat("-", 89))

		limit := len(sortedUsers)
		if limit > 10 && !showAll {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			u := sortedUsers[i]
			fmt.Printf("  %-25s  %8d  %8s  %8s  %8s  %8s  %10s\n",
				u.user,
				u.stats.Count,
				formatSessionDuration(u.stats.Min),
				formatSessionDuration(u.stats.Max),
				formatSessionDuration(u.stats.Avg),
				formatSessionDuration(u.stats.Median),
				formatSessionDuration(u.cumulated))
		}
	}

	// 3. SESSION DURATION BY DATABASE (tables at the end, more readable)
	if len(m.Connections.SessionsByDatabase) > 0 {
		fmt.Println(bold + "\nSESSION DURATION BY DATABASE\n" + reset)

		// Sort by total session count (descending)
		type dbStats struct {
			database  string
			stats     analysis.DurationStats
			cumulated time.Duration
		}
		var sortedDBs []dbStats
		for db, s := range m.Connections.SessionsByDatabase {
			stats := s.Stats()
			sortedDBs = append(sortedDBs, dbStats{database: db, stats: stats, cumulated: s.Cumulated()})
		}
		sort.Slice(sortedDBs, func(i, j int) bool {
			if sortedDBs[i].stats.Count != sortedDBs[j].stats.Count {
				return sortedDBs[i].stats.Count > sortedDBs[j].stats.Count
			}
			return sortedDBs[i].database < sortedDBs[j].database
		})

		// Display header
		fmt.Printf("  %-25s  %8s  %8s  %8s  %8s  %8s  %10s\n",
			"Database", "Sessions", "Min", "Max", "Avg", "Median", "Cumulated")
		fmt.Println("  " + strings.Repeat("-", 89))

		limit := len(sortedDBs)
		if limit > 10 && !showAll {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			d := sortedDBs[i]
			fmt.Printf("  %-25s  %8d  %8s  %8s  %8s  %8s  %10s\n",
				d.database,
				d.stats.Count,
				formatSessionDuration(d.stats.Min),
				formatSessionDuration(d.stats.Max),
				formatSessionDuration(d.stats.Avg),
				formatSessionDuration(d.stats.Median),
				formatSessionDuration(d.cumulated))
		}
	}

	// 4. SESSION DURATION BY HOST
	if len(m.Connections.SessionsByHost) > 0 {
		fmt.Println(bold + "\nSESSION DURATION BY HOST\n" + reset)

		// Sort by total session count (descending)
		type hostStats struct {
			host      string
			stats     analysis.DurationStats
			cumulated time.Duration
		}
		var sortedHosts []hostStats
		for host, s := range m.Connections.SessionsByHost {
			stats := s.Stats()
			sortedHosts = append(sortedHosts, hostStats{host: host, stats: stats, cumulated: s.Cumulated()})
		}
		sort.Slice(sortedHosts, func(i, j int) bool {
			if sortedHosts[i].stats.Count != sortedHosts[j].stats.Count {
				return sortedHosts[i].stats.Count > sortedHosts[j].stats.Count
			}
			return sortedHosts[i].host < sortedHosts[j].host
		})

		// Display header
		fmt.Printf("  %-25s  %8s  %8s  %8s  %8s  %8s  %10s\n",
			"Host", "Sessions", "Min", "Max", "Avg", "Median", "Cumulated")
		fmt.Println("  " + strings.Repeat("-", 89))

		limit := len(sortedHosts)
		if limit > 10 && !showAll {
			limit = 10
		}
		for i := 0; i < limit; i++ {
			h := sortedHosts[i]
			fmt.Printf("  %-25s  %8d  %8s  %8s  %8s  %8s  %10s\n",
				h.host,
				h.stats.Count,
				formatSessionDuration(h.stats.Min),
				formatSessionDuration(h.stats.Max),
				formatSessionDuration(h.stats.Avg),
				formatSessionDuration(h.stats.Median),
				formatSessionDuration(h.cumulated))
		}
	}

	// Note: SESSION DURATION BY APPLICATION not implemented
	// App info is in log_line_prefix, not in disconnection message body
}

// formatSessionDuration formats a time.Duration for human-readable display.
// Examples: "0s", "2s", "5m30s", "1h45m", "2d 3h15m"
func formatSessionDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		if secs > 0 {
			return fmt.Sprintf("%dm%ds", mins, secs)
		}
		return fmt.Sprintf("%dm", mins)
	}
	totalHours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if totalHours >= 24 {
		days := totalHours / 24
		hours := totalHours % 24
		if hours > 0 && mins > 0 {
			return fmt.Sprintf("%dd %dh%dm", days, hours, mins)
		}
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if mins > 0 {
		return fmt.Sprintf("%dh%dm", totalHours, mins)
	}
	return fmt.Sprintf("%dh", totalHours)
}

// printAutovacuumSection renders the AUTOVACUUM panel: header k:v
// block (count, cumulated time, tuples, dead-not-removable, buffer/WAL
// usage, slowest single run) followed by three purpose-driven panels —
// top tables by elapsed, tables blocked by xmin horizon, top tables by
// count. Each line/panel is suppressed when its source metric is zero,
// so older PG versions emitting no continuation lines degrade cleanly.
func printAutovacuumSection(v analysis.VacuumMetrics) {
	fmt.Println(ansiBold + "\nAUTOVACUUM\n" + ansiReset)

	fmt.Printf("  %-25s : %s\n", "Vacuum count", formatThousands(int64(v.VacuumCount)))
	if v.AggressiveVacuumCount > 0 {
		fmt.Printf("  %-25s : %s\n", "  of which aggressive", formatThousands(int64(v.AggressiveVacuumCount)))
	}
	if v.TotalVacuumElapsedSeconds > 0 {
		dur := time.Duration(v.TotalVacuumElapsedSeconds * float64(time.Second)).Truncate(time.Second)
		fmt.Printf("  %-25s : %s\n", "Cumulated time", dur)
	}
	if v.TotalTuplesRemoved > 0 {
		fmt.Printf("  %-25s : %s\n", "Tuples removed", formatThousands(v.TotalTuplesRemoved))
	}
	if total := sumSpaceRecovered(v.VacuumSpaceRecovered); total > 0 {
		fmt.Printf("  %-25s : %s\n", "Space recovered", FormatBytes(total))
	}
	if v.TotalTuplesNotYetRemovable > 0 {
		fmt.Printf("  %-25s : %s\n", "Dead, not yet removable", formatThousands(v.TotalTuplesNotYetRemovable))
	}
	if v.TotalBufferHits+v.TotalBufferMisses > 0 {
		fmt.Printf("  %-25s : hits %s  misses %s  dirtied %s  written %s\n",
			"Buffer usage",
			formatCompact(v.TotalBufferHits),
			formatCompact(v.TotalBufferMisses),
			formatCompact(v.TotalBufferDirtied),
			formatCompact(v.TotalBufferWritten),
		)
	}
	if v.TotalWALRecords > 0 || v.TotalWALBytes > 0 {
		fmt.Printf("  %-25s : %s records  %s\n",
			"WAL usage",
			formatCompact(v.TotalWALRecords),
			FormatBytes(v.TotalWALBytes),
		)
	}
	if v.SlowestVacuum != nil && v.SlowestVacuum.ElapsedSeconds > 0 {
		dur := time.Duration(v.SlowestVacuum.ElapsedSeconds * float64(time.Second)).Truncate(time.Second)
		fmt.Printf("  %-25s : %s on %s\n", "Slowest single run", dur, v.SlowestVacuum.Table)
	}

	if len(v.TopVacuumTables) > 0 {
		printTopVacuumElapsedTable(v.TopVacuumTables, v.VacuumSpaceRecovered)
	}
	if len(v.XminBlockedTables) > 0 {
		printTopXminTable(v.XminBlockedTables)
	}
	if len(v.VacuumTableCounts) > 0 {
		printTopCountTable("Top tables by count:", v.VacuumTableCounts, v.VacuumCount)
	}
}

// printAutoanalyzeSection renders the AUTOANALYZE sibling panel.
// Currently slimmer than autovacuum because PG's analyze blocks only
// carry system-usage (elapsed) — no buffer, no WAL, no tuples.
func printAutoanalyzeSection(v analysis.VacuumMetrics) {
	fmt.Println(ansiBold + "\nAUTOANALYZE\n" + ansiReset)

	fmt.Printf("  %-25s : %s\n", "Analyze count", formatThousands(int64(v.AnalyzeCount)))
	if v.TotalAnalyzeElapsedSeconds > 0 {
		dur := time.Duration(v.TotalAnalyzeElapsedSeconds * float64(time.Second)).Truncate(time.Second)
		fmt.Printf("  %-25s : %s\n", "Cumulated time", dur)
	}

	if len(v.TopAnalyzeTablesByElapsed) > 0 {
		printTopElapsedTable("Top tables by elapsed time:", v.TopAnalyzeTablesByElapsed)
	}
	if len(v.AnalyzeTableCounts) > 0 {
		printTopCountTable("Top tables by count:", v.AnalyzeTableCounts, v.AnalyzeCount)
	}
}

// printTopElapsedTable renders one maintenance target per row, table
// name first so the eye scans the identifier column without parsing
// numerics. The VacuumCount field is reused for analyze rows too —
// semantically it is the per-table operation count, regardless of
// which branch (vacuum or analyze) populated it.
func printTopElapsedTable(title string, rows []analysis.VacuumTableStat) {
	fmt.Println("\n  " + title)
	durs := make([]string, len(rows))
	maxDurW, maxNameW := 0, 0
	for i, t := range rows {
		durs[i] = time.Duration(t.TotalElapsedSeconds * float64(time.Second)).Truncate(time.Second).String()
		if len(durs[i]) > maxDurW {
			maxDurW = len(durs[i])
		}
		if len(t.Table) > maxNameW {
			maxNameW = len(t.Table)
		}
	}
	for i, t := range rows {
		fmt.Printf("    %-*s  %3d×  %*s\n", maxNameW, t.Table, t.VacuumCount, maxDurW, durs[i])
	}
}

// printTopVacuumElapsedTable is the autovacuum-specific variant of
// printTopElapsedTable: same name-first layout (table → count → elapsed),
// with the per-table bytes reclaimed appended in muted italic when
// known. Suffixing rather than column-aligning keeps the trio clean on
// xmin-blocked workloads where most rows reclaim nothing — only the
// productive lines wear the trailing tag.
func printTopVacuumElapsedTable(rows []analysis.VacuumTableStat, recovered map[string]int64) {
	fmt.Println("\n  Top tables by elapsed time:")
	durs := make([]string, len(rows))
	maxDurW, maxNameW := 0, 0
	for i, t := range rows {
		durs[i] = time.Duration(t.TotalElapsedSeconds * float64(time.Second)).Truncate(time.Second).String()
		if len(durs[i]) > maxDurW {
			maxDurW = len(durs[i])
		}
		if len(t.Table) > maxNameW {
			maxNameW = len(t.Table)
		}
	}
	for i, t := range rows {
		if r := recovered[t.Table]; r > 0 {
			fmt.Printf("    %-*s  %3d×  %*s  %s%s recovered%s\n",
				maxNameW, t.Table, t.VacuumCount, maxDurW, durs[i],
				ansiMutedItalic, FormatBytes(r), ansiReset)
		} else {
			fmt.Printf("    %-*s  %3d×  %*s\n",
				maxNameW, t.Table, t.VacuumCount, maxDurW, durs[i])
		}
	}
}

// sumSpaceRecovered totals the per-table reclaimed bytes so the
// AUTOVACUUM header can carry a single global figure alongside
// "Tuples removed". Returns 0 when no table reclaimed space — that's
// the signal a renderer uses to skip the line entirely.
func sumSpaceRecovered(m map[string]int64) int64 {
	var total int64
	for _, v := range m {
		total += v
	}
	return total
}

// printTopXminTable lists tables where autovacuum saw dead tuples it
// could not remove yet — same wording PostgreSQL uses in its own log
// ("are dead but not yet removable") so a DBA seeing the report
// recognises the term instantly. Read this list as "where the
// freeze-pressure debt is accumulating".
func printTopXminTable(rows []analysis.VacuumTableStat) {
	fmt.Println("\n  Tables with rows not yet removable:")
	nums := make([]string, len(rows))
	maxNumW, maxNameW := 0, 0
	for i, t := range rows {
		nums[i] = formatThousands(t.TuplesNotYetRemovable)
		if len(nums[i]) > maxNumW {
			maxNumW = len(nums[i])
		}
		if len(t.Table) > maxNameW {
			maxNameW = len(t.Table)
		}
	}
	for i, t := range rows {
		fmt.Printf("    %-*s  %*s rows\n", maxNameW, t.Table, maxNumW, nums[i])
	}
}

// printTopCountTable ranks tables by their raw vacuum or analyze count.
// Stops once the cumulative share crosses 80% (or 10 entries, whichever
// comes first) so the panel doesn't bury the signal under a long tail
// on workloads that touch hundreds of tables.
func printTopCountTable(title string, counts map[string]int, total int) {
	type pair struct {
		Name  string
		Count int
	}
	pairs := make([]pair, 0, len(counts))
	for n, c := range counts {
		pairs = append(pairs, pair{n, c})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		return pairs[i].Name < pairs[j].Name
	})
	// Pre-walk to find the kept rows and their max name width before
	// printing, so the table column aligns on the longest displayed
	// table rather than the longest in the entire input.
	cum, kept := 0, 0
	maxNameW := 0
	for i, p := range pairs {
		cum += p.Count
		kept = i + 1
		if len(p.Name) > maxNameW {
			maxNameW = len(p.Name)
		}
		if i >= 9 {
			break
		}
		if float64(cum)/float64(total)*100 >= 80 && i >= 4 {
			break
		}
	}
	fmt.Println("\n  " + title)
	for i := 0; i < kept; i++ {
		p := pairs[i]
		pct := float64(p.Count) / float64(total) * 100
		fmt.Printf("    %-*s  %6d  %5.1f%%\n", maxNameW, p.Name, p.Count, pct)
	}
}

// formatCompact renders large integer counts with SI-style suffixes
// (1.5M, 370M, 4.5G). Used in the maintenance summary lines where
// thousand-separated forms (e.g. "367,452,793") add noise without
// telling the reader the order of magnitude any faster.
func formatCompact(n int64) string {
	switch {
	case n < 0:
		return "-"
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	case n < 10_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n < 10_000_000_000:
		return fmt.Sprintf("%.1fG", float64(n)/1_000_000_000)
	default:
		return fmt.Sprintf("%dG", n/1_000_000_000)
	}
}

// formatThousands turns an int64 into a thousands-separated string.
// Negative values are unsupported (the analyzer only emits >= 0 here).
func formatThousands(v int64) string {
	s := strconv.FormatInt(v, 10)
	n := len(s)
	if n <= 3 {
		return s
	}
	out := make([]byte, 0, n+(n-1)/3)
	pre := n % 3
	if pre > 0 {
		out = append(out, s[:pre]...)
		if n > pre {
			out = append(out, ',')
		}
	}
	for i := pre; i < n; i += 3 {
		out = append(out, s[i:i+3]...)
		if i+3 < n {
			out = append(out, ',')
		}
	}
	return string(out)
}

// PrintSQLSummary displays an SQL performance report in the CLI.
// The report uses ANSI bold formatting for better readability. The query text is truncated based on terminal width.
func PrintSQLSummary(m analysis.SQLMetrics, indicatorsOnly bool) {
	PrintSQLSummaryWithContext(m, analysis.TempFileMetrics{}, analysis.LockMetrics{}, indicatorsOnly)
}

// PrintSQLSummaryWithContext displays SQL performance with optional tempfiles and locks context.
func PrintSQLSummaryWithContext(m analysis.SQLMetrics, tempFiles analysis.TempFileMetrics, locks analysis.LockMetrics, indicatorsOnly bool) {
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
	bold := ansiBold
	reset := ansiReset

	// Compute top 1% slowest queries via the compact storage helper.
	top1Slow := 0
	if m.ExecutionCount() > 0 {
		top1Slow = m.ExecutionsCountAbove(m.P99QueryDuration)
	}

	// ** SQL Summary Header **
	fmt.Println(bold + "\nSQL SUMMARY" + reset)
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

		// Define label order for the query duration histogram.
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
						FormatBytes(stat.TotalSize))
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
						FormatBytes(stat.TotalSize))
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

// PrintSQLDetails iterates over the QueryStats and displays details for each query
// whose SQLID matches one of the provided queryDetails.
// It consolidates metrics from SQL performance, tempfiles, and locks into a unified view.
func PrintSQLDetails(m analysis.AggregatedMetrics, queryDetails []string) {
	bold := ansiBold
	reset := ansiReset

	for _, qid := range queryDetails {
		// Collect all metrics for this query ID
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

		// Search in tempfiles
		for _, ts := range m.TempFiles.QueryStats {
			if ts.ID == qid {
				tempStat = ts
				break
			}
		}

		// Search in locks
		for _, ls := range m.Locks.QueryStats {
			if ls.ID == qid {
				lockStat = ls
				break
			}
		}

		// Fallback: a triggering-query entry inside top_events. The
		// query was never timed by log_min_duration_statement but it
		// still showed up as the STATEMENT continuation of one or more
		// error patterns. We synthesise a minimal QueryStat so the
		// detail view can still render the normalised form and the
		// EVENTS section below picks up.
		var triggerOnlyNormalized string
		if sqlStat == nil && tempStat == nil && lockStat == nil {
			for i := range m.TopEvents {
				for _, tq := range m.TopEvents[i].TriggeringQueries {
					if tq.ID == qid {
						triggerOnlyNormalized = tq.NormalizedQuery
						break
					}
				}
				if triggerOnlyNormalized != "" {
					break
				}
			}
			if triggerOnlyNormalized == "" {
				fmt.Printf("\nQuery ID '%s' not found.\n", qid)
				continue
			}
		}

		// Get query type and normalized query (from any available source)
		var queryType string
		var normalizedQuery string
		var rawQuery string

		if sqlStat != nil {
			queryType = analysis.QueryTypeFromID(sqlStat.ID)
			normalizedQuery = sqlStat.NormalizedQuery
			rawQuery = sqlStat.RawQuery
		} else if lockStat != nil {
			queryType = analysis.QueryTypeFromID(lockStat.ID)
			normalizedQuery = lockStat.NormalizedQuery
			rawQuery = lockStat.RawQuery
		} else if tempStat != nil {
			queryType = analysis.QueryTypeFromID(tempStat.ID)
			normalizedQuery = tempStat.NormalizedQuery
			rawQuery = tempStat.RawQuery
		} else if triggerOnlyNormalized != "" {
			queryType = analysis.QueryTypeFromID(qid)
			normalizedQuery = triggerOnlyNormalized
			// No raw form — the only thing we have is the normalised
			// signature from the STATEMENT continuation.
		}

		// SQL DETAILS section
		fmt.Println(bold + "\nSQL DETAILS" + reset)
		fmt.Println()

		// Execution histogram (if multiple executions)
		if sqlStat != nil && sqlStat.Count > 1 {
			execHist, execUnit, execScale := computeSingleQueryExecutionHistogram(m.SQL, qid)
			if execHist != nil {
				PrintHistogram(execHist, "Query count", execUnit, execScale, nil)
			}
		}

		fmt.Printf("  Id                   : %s\n", qid)
		fmt.Printf("  Query Type           : %s\n", queryType)
		if sqlStat != nil {
			fmt.Printf("  Count                : %d\n", sqlStat.Count)
			if len(sqlStat.PreparedNames) > 0 {
				fmt.Printf("  Prepared as          : %s\n", formatPreparedNames(sqlStat.PreparedNames))
			}
			// Dimensions inlined into the Query Info block so the top-N
			// db/user/app/host stay next to "who ran this how many
			// times" without an extra header. Rows auto-hide when the
			// axis is empty (e.g. logs without %a / %h prefixes).
			dims := m.SQL.TopDimensionsForID(qid, 5)
			if !dims.IsEmpty() {
				printDimensionsRow("Databases", dims.Databases)
				printDimensionsRow("Users", dims.Users)
				printDimensionsRow("Apps", dims.Apps)
				printDimensionsRow("Hosts", dims.Hosts)
			}
		}

		// EVENTS section — surfaced right after the Query Info block so
		// the operational signal ("this query triggers this error N
		// times") is the first thing a DBA sees, before the time /
		// tempfiles / locks drill-down.
		eventsFor := findEventsTriggeredByQuery(m.TopEvents, qid)
		if len(eventsFor) > 0 {
			fmt.Println()
			fmt.Println(bold + "EVENTS" + reset)
			fmt.Println()
			printQueryEvents(eventsFor)
		}

		// TIME section (if SQL metrics available)
		if sqlStat != nil {
			fmt.Println()
			fmt.Println(bold + "TIME" + reset)
			fmt.Println()

			// Time histogram (if multiple executions)
			if sqlStat.Count > 1 {
				timeHist, timeUnit, timeScale := computeSingleQueryTimeHistogram(m.SQL, qid)
				if timeHist != nil {
					PrintHistogram(timeHist, "Cumulative time", timeUnit, timeScale, nil)
				}
			}

			// Duration distribution histogram (if multiple executions)
			if sqlStat.Count > 1 {
				durationHist, durationUnit, durationScale, durationLabels := computeSingleQueryDurationDistribution(m.SQL, qid)
				if durationHist != nil {
					PrintHistogram(durationHist, "Query duration distribution", durationUnit, durationScale, durationLabels)
				}
			}

			// Calculate min duration from executions
			minDuration := sqlStat.MaxTime
			m.SQL.IterateExecutionsForID(qid, func(exec analysis.QueryExecution) bool {
				if exec.Duration < minDuration {
					minDuration = exec.Duration
				}
				return true
			})

			fmt.Printf("  Total Duration       : %s\n", formatQueryDuration(sqlStat.TotalTime))
			fmt.Printf("  Min Duration         : %s\n", formatQueryDuration(minDuration))
			fmt.Printf("  Avg Duration         : %s\n", formatQueryDuration(sqlStat.AvgTime))
			fmt.Printf("  Max Duration         : %s\n", formatQueryDuration(sqlStat.MaxTime))
		}

		// TEMP FILES section (if tempfiles metrics available)
		if tempStat != nil {
			fmt.Println()
			fmt.Println(bold + "TEMP FILES" + reset)
			fmt.Println()

			// Tempfiles size histogram (if multiple events)
			if tempStat.Count > 1 {
				tempSizeHist, tempSizeUnit, tempSizeScale := computeSingleQueryTempFileHistogram(m.TempFiles.Events, qid)
				if tempSizeHist != nil {
					PrintHistogram(tempSizeHist, "Temp files size", tempSizeUnit, tempSizeScale, nil)
				}
			}

			// Tempfiles count histogram (if multiple events)
			if tempStat.Count > 1 {
				tempCountHist, tempCountUnit, tempCountScale := computeSingleQueryTempFileCountHistogram(m.TempFiles.Events, qid)
				if tempCountHist != nil {
					PrintHistogram(tempCountHist, "Temp files count", tempCountUnit, tempCountScale, nil)
				}
			}

			avgSize := tempStat.TotalSize / int64(tempStat.Count)

			fmt.Printf("  Temp Files count     : %d\n", tempStat.Count)
			fmt.Printf("  Temp File min size   : %s\n", FormatBytes(tempStat.MinSize))
			fmt.Printf("  Temp File max size   : %s\n", FormatBytes(tempStat.MaxSize))
			fmt.Printf("  Temp File avg size   : %s\n", FormatBytes(avgSize))
			fmt.Printf("  Temp Files size      : %s\n", FormatBytes(tempStat.TotalSize))
		}

		// LOCKS section (if locks metrics available)
		if lockStat != nil {
			fmt.Println()
			fmt.Println(bold + "LOCKS" + reset)
			fmt.Println()

			// Always show acquired locks/time (even if 0)
			fmt.Printf("  Acquired Locks       : %d\n", lockStat.AcquiredCount)
			fmt.Printf("  Acquired Wait Time   : %s\n", formatQueryDuration(lockStat.AcquiredWaitTime))
			fmt.Printf("  Still Waiting Locks  : %d\n", lockStat.StillWaitingCount)
			fmt.Printf("  Still Waiting Time   : %s\n", formatQueryDuration(lockStat.StillWaitingTime))
			fmt.Printf("  Total Wait Time      : %s\n", formatQueryDuration(lockStat.TotalWaitTime))
		}

		// Display normalized query
		fmt.Println()
		fmt.Println("Normalized Query:")
		fmt.Println()
		// Indent each line of the formatted query
		formatted := formatSQL(normalizedQuery)
		for _, line := range strings.Split(formatted, "\n") {
			fmt.Println(" " + line)
		}
		fmt.Println()

		// Show one concrete execution. When we have parameter values (extended
		// protocol + DETAIL pairing), display the slowest run with $N
		// substituted; otherwise fall back to the placeholder form.
		if rawQuery != "" {
			if sqlStat != nil && sqlStat.SlowestRun != nil {
				sr := sqlStat.SlowestRun
				fmt.Printf("Slowest Run %s%s, %s, pid=%s%s%s\n",
					ansiMutedItalic,
					formatQueryDuration(sr.DurationMs),
					sr.Timestamp.Format("2006-01-02 15:04:05"),
					sr.PID,
					formatSlowestRunDimensions(sr),
					ansiReset,
				)
				fmt.Println()
				text, truncated, full := truncateForDisplay(SubstituteParameters(rawQuery, sr.Parameters), slowestRunDisplayCap)
				if truncated {
					fmt.Printf("%s[…]%s%s%s\n", text, ansiMutedItalic, truncationHint(len(text), full), ansiReset)
				} else {
					fmt.Println(text)
				}
			} else {
				fmt.Println("Example Query:")
				fmt.Println()
				fmt.Println(rawQuery)
			}
		}

		// Display execution plan if available (from auto_explain)
		if sqlStat != nil && sqlStat.LastPlan != "" {
			fmt.Println()
			fmt.Println(bold + "EXECUTION PLAN" + reset)
			fmt.Println()
			for _, line := range strings.Split(sqlStat.LastPlan, "\n") {
				fmt.Printf("  %s\n", line)
			}
		}
	}
}

// Helpers

// queryEventLink pairs an EventStat with the count of times the query
// under inspection triggered it, derived at render time from the
// EventStat.TriggeringQueries slice.
type queryEventLink struct {
	Event      analysis.EventStat
	TriggerCnt int
}

// findEventsTriggeredByQuery walks events looking for any pattern
// whose TriggeringQueries list mentions queryID. Results are sorted by
// per-query trigger count descending, with a tie-breaker on the total
// event count so the most pressing pattern surfaces first.
func findEventsTriggeredByQuery(events []analysis.EventStat, queryID string) []queryEventLink {
	if queryID == "" {
		return nil
	}
	var out []queryEventLink
	for i := range events {
		for _, tq := range events[i].TriggeringQueries {
			if tq.ID == queryID {
				out = append(out, queryEventLink{Event: events[i], TriggerCnt: tq.Count})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TriggerCnt != out[j].TriggerCnt {
			return out[i].TriggerCnt > out[j].TriggerCnt
		}
		return out[i].Event.Count > out[j].Event.Count
	})
	return out
}

// printQueryEvents renders the EVENTS section table for --sql-detail.
// Same column shape ideas as printTopTables / printQueryTable so the
// section sits visually next to TEMP FILES and LOCKS. Rows are already
// sorted by trigger count desc, which doubles as the implicit ranking
// (no "#" column).
func printQueryEvents(rows []queryEventLink) {
	const msgWidth = 60
	fmt.Printf("  %-10s  %-8s  %-*s  %9s\n", "EventID", "SEVERITY", msgWidth, "MESSAGE", "TRIGGERED")
	for _, r := range rows {
		msg := r.Event.Message
		if len(msg) > msgWidth {
			msg = msg[:msgWidth-1] + "…"
		}
		fmt.Printf("  %-10s  %-8s  %-*s  %9d\n",
			r.Event.ID, r.Event.Severity, msgWidth, msg, r.TriggerCnt)
	}
}

// formatSlowestRunDimensions appends a ", db=X, user=Y, app=Z, host=W"
// suffix to the slowest-run header line. Empty fields are skipped so
// noisy logs without all four prefix values still produce readable
// output. Returns "" when none of the dimensions are populated.
func formatSlowestRunDimensions(sr *analysis.SlowestRun) string {
	if sr == nil {
		return ""
	}
	var parts []string
	if sr.Database != "" {
		parts = append(parts, "db="+sr.Database)
	}
	if sr.User != "" {
		parts = append(parts, "user="+sr.User)
	}
	if sr.App != "" {
		parts = append(parts, "app="+sr.App)
	}
	if sr.Host != "" {
		parts = append(parts, "host="+sr.Host)
	}
	if len(parts) == 0 {
		return ""
	}
	return ", " + strings.Join(parts, ", ")
}

// printDimensionsRow renders one line of the Dimensions sub-block
// under --sql-detail. Skips silently when the row has no data so
// empty axes never appear at all. Each entry reads "<name> <count>"
// where <count> is muted italic — same convention as the inline
// timestamps in SERVER / Replication. Label width is aligned with
// the other Query Info lines (e.g. "Total Duration       :").
func printDimensionsRow(label string, rows []analysis.DimensionCount) {
	if len(rows) == 0 {
		return
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("%s %s%s%s", r.Name, ansiMutedItalic, formatThousands(int64(r.Count)), ansiReset))
	}
	fmt.Printf("  %-21s: %s\n", label, strings.Join(parts, ", ")) // aligned with Query Info ":" column
}

// formatPreparedNames renders the set of distinct prepared-statement names
// observed for a query: a single name is shown as-is; multiple names are
// joined with ", " and suffixed by their count.
func formatPreparedNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return fmt.Sprintf("%s (%d names seen)", strings.Join(names, ", "), len(names))
	}
}

// truncateQuery truncates the query string to the specified length, appending "..." if necessary.
func truncateQuery(query string, length int) string {
	if len(query) > length {
		return query[:length-3] + "..."
	}
	return query
}

// PrintEventsReport prints a consolidated event report including summary and top events.
func PrintEventsReport(summaries []analysis.EventSummary, topEvents []analysis.EventStat, onlyErrors bool) {
	// ANSI styles.
	bold := ansiBold
	reset := ansiReset

	// Print title in bold.
	fmt.Println(bold + "\nEVENTS\n" + reset)

	// Constants for layout
	severityLabelWidth := 25

	for _, blk := range groupEventsBySeverityAndClass(summaries, topEvents, onlyErrors) {
		// Print Severity Main Line
		fmt.Printf("  %-*s : %d (%.1f%%)\n",
			severityLabelWidth, blk.Summary.Type,
			blk.Summary.Count, blk.Summary.Percentage)

		if len(blk.Classes) == 0 {
			continue
		}

		// Find max message width for consistent alignment within this
		// severity block.
		msgWidth := 0
		for _, c := range blk.Classes {
			for _, e := range c.Events {
				if len(e.Message) > msgWidth {
					msgWidth = len(e.Message)
				}
			}
		}
		if msgWidth < 30 {
			msgWidth = 30
		}
		if msgWidth > 60 {
			msgWidth = 60
		}

		for _, c := range blk.Classes {
			// If ALL events are unclassified (common for LOG), skip the
			// "Unclassified" header to keep the section a flat list.
			if !(c.Code == "Unclassified" && len(blk.Classes) == 1) {
				fmt.Printf("    %s\n", c.Header)
			}

			// Print messages at the same indent as the class header
			// (4 spaces). Pattern IDs sit in the left margin instead of
			// nested deeper — the third indent level crowded the layout
			// and pushed the count column past the 80-col mark on long
			// messages.
			indent := "    "

			for _, e := range c.Events {
				msg := e.Message
				if len(msg) > msgWidth {
					msg = msg[:msgWidth-3] + "..."
				}

				localPct := 0.0
				if blk.Summary.Count > 0 {
					localPct = (float64(e.Count) / float64(blk.Summary.Count)) * 100
				}

				// Lead the row with the stable handle as a left-margin
				// label, italic-grey to keep it secondary to the message.
				// 7-char IDs (XX-XXXX) give a stable column. When no ID
				// (severity not tracked as a pattern), pad with spaces so
				// the message column stays aligned across rows.
				idCol := strings.Repeat(" ", 7)
				if e.ID != "" {
					idCol = ansiMutedItalic + e.ID + ansiReset
				}
				fmt.Printf("%s%s  %-*s  %6d  %6.2f%%\n",
					indent,
					idCol,
					msgWidth, msg,
					e.Count, localPct)
			}
		}
	}
	fmt.Println()
}

// PrintHistogram displays a histogram with time ranges sorted chronologically.
// Terminal width is detected automatically to adapt bar width.
func PrintHistogram(data map[string]int, title string, unit string, scaleFactor int, orderedLabels []string) {
	if len(data) == 0 {
		fmt.Printf("\n  (No data available)\n")
		return
	}

	// Get terminal width.
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || termWidth <= 0 {
		termWidth = 80 // default
	}

	// Reserved widths: 20 for label, 5 for value.
	labelWidth := 20
	valueWidth := 5
	spacing := 4
	barWidth := termWidth - labelWidth - spacing - valueWidth
	if barWidth < 10 {
		barWidth = 10
	}
	// Cap the bar width even on very wide terminals — past ~40 chars
	// the bars stop conveying ratio at a glance and just turn into a
	// wall of blocks. The histogram is meant to be a quick visual cue,
	// not a precise scale.
	if barWidth > 80 {
		barWidth = 40
	}

	// Label ordering: use explicit order if provided, otherwise sort by time.
	labels := make([]string, 0, len(data))
	if len(orderedLabels) > 0 {
		labels = orderedLabels
	} else {
		// Auto-sort by time "HH:MM - HH:MM".
		for label := range data {
			labels = append(labels, label)
		}
		sort.Slice(labels, func(i, j int) bool {
			partsI := strings.Split(labels[i], " - ")
			partsJ := strings.Split(labels[j], " - ")
			t1, _ := time.Parse("15:04", partsI[0])
			t2, _ := time.Parse("15:04", partsJ[0])
			if !t1.Equal(t2) {
				return t1.Before(t2)
			}
			// Tiebreaker for buckets sharing the same start minute
			// (keeps output deterministic across runs).
			return labels[i] < labels[j]
		})
	}

	// Find the maximum value for display normalization.
	maxValue := 0
	for _, value := range data {
		if value > maxValue {
			maxValue = value
		}
	}

	// Compute scale factor dynamically if not provided.
	if scaleFactor <= 0 {
		scaleFactor = int(math.Ceil(float64(maxValue) / float64(barWidth)))
		if scaleFactor < 1 {
			scaleFactor = 1
		}
	}

	// Print header.
	fmt.Printf("  %s | ■ = %d %s\n\n", title, scaleFactor, unit)

	// Print histogram rows.
	for _, label := range labels {
		value := data[label]
		barLength := value / scaleFactor
		if barLength > barWidth {
			barLength = barWidth
		}
		bar := strings.Repeat("■", barLength)

		// Show value or `-` for zero.
		valueStr := fmt.Sprintf("%d %s", value, unit)
		if value == 0 {
			valueStr = " -"
		}

		// Keep value alignment consistent.
		if barLength > 0 {
			fmt.Printf("  %-13s  %-s %s\n", label, bar, valueStr)
		} else {
			fmt.Printf("  %-13s  %s\n", label, valueStr)
		}
	}
	fmt.Println()
}

// PrintCheckpointHistogram displays a checkpoint histogram with frequency per bucket.
// bucketHours is the duration of each bucket in hours (e.g., 4 for 4-hour buckets).
func PrintCheckpointHistogram(data map[string]int, title string, scaleFactor int, bucketHours float64) {
	if len(data) == 0 {
		fmt.Printf("\n  (No data available)\n")
		return
	}

	// Terminal width
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || termWidth <= 0 {
		termWidth = 80
	}

	// Layout widths
	labelWidth := 20
	valueWidth := 5
	freqWidth := 10 // space for "(X.XX/h)" or "(X.XX/m)"
	spacing := 4
	barWidth := termWidth - labelWidth - spacing - valueWidth - freqWidth
	if barWidth < 10 {
		barWidth = 10
	}
	// Cap on very wide terminals — past ~40 chars the bars stop
	// conveying ratio at a glance. Same rationale as PrintHistogram.
	if barWidth > 80 {
		barWidth = 40
	}

	// Sort labels by time
	labels := make([]string, 0, len(data))
	for label := range data {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		partsI := strings.Split(labels[i], " - ")
		partsJ := strings.Split(labels[j], " - ")
		t1, _ := time.Parse("15:04", partsI[0])
		t2, _ := time.Parse("15:04", partsJ[0])
		if !t1.Equal(t2) {
			return t1.Before(t2)
		}
		return labels[i] < labels[j]
	})

	// Find max value
	maxValue := 0
	for _, value := range data {
		if value > maxValue {
			maxValue = value
		}
	}

	// Calculate scale factor if not provided
	if scaleFactor <= 0 {
		scaleFactor = int(math.Ceil(float64(maxValue) / float64(barWidth)))
		if scaleFactor < 1 {
			scaleFactor = 1
		}
	}

	// Header
	fmt.Printf("  %s | ■ = %d \n\n", title, scaleFactor)

	// Print each row
	for _, label := range labels {
		value := data[label]
		barLength := value / scaleFactor
		if barLength > barWidth {
			barLength = barWidth
		}
		bar := strings.Repeat("■", barLength)

		// Calculate frequency (italic + medium gray)
		muted := ansiMutedItalic
		reset := ansiReset
		var freqStr string
		if value == 0 {
			freqStr = ""
		} else {
			freq := float64(value) / bucketHours
			if freq >= 1.0 {
				freqStr = fmt.Sprintf("%s%.1f/h%s", muted, freq, reset)
			} else {
				// Convert to per minute
				freqPerMin := freq * 60
				freqStr = fmt.Sprintf("%s%.1f/m%s", muted, freqPerMin, reset)
			}
		}

		// Format output
		if value == 0 {
			fmt.Printf("  %-13s   -\n", label)
		} else if barLength > 0 {
			fmt.Printf("  %-13s  %-s %d  %s\n", label, bar, value, freqStr)
		} else {
			fmt.Printf("  %-13s  %d  %s\n", label, value, freqStr)
		}
	}
	fmt.Println()
}

// PrintWALDistanceHistogram displays a WAL distance vs estimate histogram.
// Uses ◼ for distance within estimate, ◻ for estimate margin, ■ for overshoot.
func PrintWALDistanceHistogram(buckets []WALDistanceBucket) {
	// Find max value (max of distance and estimate across all buckets)
	maxMB := 0.0
	for _, b := range buckets {
		if b.AvgDistMB > maxMB {
			maxMB = b.AvgDistMB
		}
		if b.AvgEstMB > maxMB {
			maxMB = b.AvgEstMB
		}
	}
	if maxMB == 0 {
		return
	}

	barWidth := 40

	// Scale: MB per character
	scaleMB := maxMB / float64(barWidth)
	if scaleMB <= 0 {
		scaleMB = 1
	}

	// Round scale for legend
	scaleLabel := scaleMB
	scaleUnit := "MB"
	if scaleLabel < 1.0 {
		scaleLabel *= 1024
		scaleUnit = "kB"
	}

	muted := ansiMuted
	muteReset := ansiReset

	fmt.Printf("\n  WAL per checkpoint (avg) | ■ = %.0f %s  %s□%s = estimate margin\n\n", scaleLabel, scaleUnit, muted, muteReset)

	for _, b := range buckets {
		if b.Count == 0 {
			fmt.Printf("  %-13s   -\n", b.Label)
			continue
		}

		dist := b.AvgDistMB
		est := b.AvgEstMB

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
		margin := ""
		if marginChars > 0 {
			margin = muted + strings.Repeat("□", marginChars) + muteReset
		}
		bar := strings.Repeat("■", distChars) + margin

		fmt.Printf("  %-13s  %s  %.0f MB\n", b.Label, bar, dist)
	}
	fmt.Println()
}

func PrintConcurrentHistogramWithTZ(data map[string]int, title string, scaleFactor int, orderedLabels []string, peakTimes map[string]time.Time, refTime *time.Time) {
	if len(data) == 0 {
		fmt.Printf("\n  (No data available)\n")
		return
	}

	// Get terminal width.
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || termWidth <= 0 {
		termWidth = 80 // default
	}

	// Column widths.
	labelWidth := 15
	peakTimeWidth := 8 // " (HH:MM)"
	valueWidth := 5
	spacing := 4
	barWidth := termWidth - labelWidth - spacing - valueWidth - peakTimeWidth
	if barWidth < 10 {
		barWidth = 10
	}
	// Cap on very wide terminals — past ~40 chars the bars stop
	// conveying ratio at a glance. Same rationale as PrintHistogram.
	if barWidth > 80 {
		barWidth = 40
	}

	// Label ordering.
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
			if !t1.Equal(t2) {
				return t1.Before(t2)
			}
			// Tiebreaker for buckets sharing the same start minute
			// (keeps output deterministic across runs).
			return labels[i] < labels[j]
		})
	}

	// Find the maximum value.
	maxValue := 0
	for _, value := range data {
		if value > maxValue {
			maxValue = value
		}
	}

	// Compute scale factor dynamically if not provided.
	if scaleFactor <= 0 {
		scaleFactor = int(math.Ceil(float64(maxValue) / float64(barWidth)))
		if scaleFactor < 1 {
			scaleFactor = 1
		}
	}

	// Print header.
	fmt.Printf("  %s | ■ = %d \n\n", title, scaleFactor)

	// Print histogram rows.
	for _, label := range labels {
		value := data[label]
		barLength := value / scaleFactor
		if barLength > barWidth {
			barLength = barWidth
		}
		bar := strings.Repeat("■", barLength)

		// Format value and peak time
		if value == 0 {
			fmt.Printf("  %-13s  %s\n", label, " -")
		} else {
			muted := ansiMutedItalic
			muteReset := ansiReset
			peakStr := ""
			if pt, ok := peakTimes[label]; ok && !pt.IsZero() {
				// Normalize peak time to reference timezone if provided
				displayTime := pt
				if refTime != nil {
					displayTime = pt.In(refTime.Location())
				}
				peakStr = fmt.Sprintf("%s%02d:%02d%s", muted, displayTime.Hour(), displayTime.Minute(), muteReset)
			}
			if barLength > 0 {
				fmt.Printf("  %-13s  %-s %d  %s\n", label, bar, value, peakStr)
			} else {
				fmt.Printf("  %-13s  %d  %s\n", label, value, peakStr)
			}
		}
	}
	fmt.Println()
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
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		// Total tie-break on name: Go map iteration order is randomized, so
		// without a secondary key equal-count entries (e.g. the "Relations"
		// list) print in a non-deterministic order, differing run-to-run and
		// between single-pass and PID-sharded runs.
		return pairs[i].name < pairs[j].name
	})

	// Print top entries
	for _, p := range pairs {
		percentage := (float64(p.count) / float64(total)) * 100
		fmt.Printf("    %-25s %6d  %5.1f%%\n", p.name, p.count, percentage)
	}
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
		if pairs[i].stat.AcquiredWaitTime != pairs[j].stat.AcquiredWaitTime {
			return pairs[i].stat.AcquiredWaitTime > pairs[j].stat.AcquiredWaitTime
		}
		return pairs[i].stat.ID < pairs[j].stat.ID
	})

	// Print top queries
	if limit > len(pairs) {
		limit = len(pairs)
	}

	bold := ansiBold
	reset := ansiReset

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
		if pairs[i].stat.StillWaitingTime != pairs[j].stat.StillWaitingTime {
			return pairs[i].stat.StillWaitingTime > pairs[j].stat.StillWaitingTime
		}
		return pairs[i].stat.ID < pairs[j].stat.ID
	})

	// Print top queries
	if limit > len(pairs) {
		limit = len(pairs)
	}

	bold := ansiBold
	reset := ansiReset

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

	bold := ansiBold
	reset := ansiReset

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

// PrintSQLOverview displays SQL query type overview with dimensional breakdowns.
// This shows statistics grouped by query type (SELECT, INSERT, UPDATE, DELETE, etc.)
// with counts, times, and percentages, broken down by database, user, host, and application.
func PrintSQLOverview(m analysis.SQLMetrics) {
	bold := ansiBold
	reset := ansiReset

	fmt.Println(bold + "\nSQL QUERY OVERVIEW" + reset)
	fmt.Println()

	if m.TotalQueries == 0 {
		fmt.Println("  No SQL queries found in the logs.")
		return
	}

	// Group by category (DML, DDL, TCL, etc.) - EN PREMIER
	categoryStats := make(map[string]struct {
		count     int
		totalTime float64
	})
	for _, stat := range m.QueryTypeStats {
		cat := stat.Category
		cs := categoryStats[cat]
		cs.count += stat.Count
		cs.totalTime += stat.TotalTime
		categoryStats[cat] = cs
	}

	// Sort categories by count
	type catStatPair struct {
		category  string
		count     int
		totalTime float64
	}
	var catPairs []catStatPair
	for cat, cs := range categoryStats {
		catPairs = append(catPairs, catStatPair{cat, cs.count, cs.totalTime})
	}
	sort.Slice(catPairs, func(i, j int) bool {
		if catPairs[i].count != catPairs[j].count {
			return catPairs[i].count > catPairs[j].count
		}
		return catPairs[i].category < catPairs[j].category
	})

	fmt.Println(bold + "  Query Category Summary" + reset)
	fmt.Println()
	fmt.Printf("  %-10s  %10s  %8s  %12s\n", "Category", "Count", "%", "Total Time")
	fmt.Println("  " + strings.Repeat("-", 46))

	for _, pair := range catPairs {
		pct := float64(pair.count) / float64(m.TotalQueries) * 100
		fmt.Printf("  %-10s  %10d  %7.1f%%  %12s\n",
			pair.category,
			pair.count,
			pct,
			formatQueryDuration(pair.totalTime))
	}
	fmt.Println()

	// Sort query types by count (descending)
	type typeStatPair struct {
		qtype string
		stat  *analysis.QueryTypeStat
	}
	var pairs []typeStatPair
	for qtype, stat := range m.QueryTypeStats {
		pairs = append(pairs, typeStatPair{qtype, stat})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].stat.Count != pairs[j].stat.Count {
			return pairs[i].stat.Count > pairs[j].stat.Count
		}
		return pairs[i].qtype < pairs[j].qtype
	})

	// Print query type distribution - EN SECOND
	fmt.Println(bold + "  Query Type Distribution" + reset)
	fmt.Println()
	fmt.Printf("  %-12s  %10s  %8s  %12s  %12s  %10s\n", "Type", "Count", "%", "Total Time", "Avg Time", "Max Time")
	fmt.Println("  " + strings.Repeat("-", 72))

	for _, pair := range pairs {
		stat := pair.stat
		pct := float64(stat.Count) / float64(m.TotalQueries) * 100
		fmt.Printf("  %-12s  %10d  %7.1f%%  %12s  %12s  %10s\n",
			pair.qtype,
			stat.Count,
			pct,
			formatQueryDuration(stat.TotalTime),
			formatQueryDuration(stat.AvgTime),
			formatQueryDuration(stat.MaxTime))
	}
	fmt.Println()

	// Breakdowns by dimension
	printQueryTypeBreakdown("Per Database", m.QueryTypesByDatabase, bold, reset)
	printQueryTypeBreakdown("Per User", m.QueryTypesByUser, bold, reset)
	printQueryTypeBreakdown("Per Host", m.QueryTypesByHost, bold, reset)
	printQueryTypeBreakdown("Per Application", m.QueryTypesByApp, bold, reset)
}

// printQueryTypeBreakdown displays query type statistics grouped by a dimension (database, user, host, app)
func printQueryTypeBreakdown(title string, breakdown map[string]map[string]*analysis.QueryTypeCount, bold, reset string) {
	if len(breakdown) == 0 {
		return
	}

	fmt.Println(bold + "  " + title + reset)
	fmt.Println()

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
		if dimensions[i].count != dimensions[j].count {
			return dimensions[i].count > dimensions[j].count
		}
		return dimensions[i].name < dimensions[j].name
	})

	// Print each dimension with its query types
	italic := ansiItalic
	for _, dim := range dimensions {
		fmt.Printf("  %s%s (%d queries, %s)%s\n",
			italic,
			dim.name,
			dim.count,
			formatQueryDuration(dim.totalTime),
			reset)

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
			if typeList[i].count != typeList[j].count {
				return typeList[i].count > typeList[j].count
			}
			return typeList[i].name < typeList[j].name
		})

		// Print query types
		for _, t := range typeList {
			fmt.Printf("    %-12s  %6d  %12s\n",
				t.name,
				t.count,
				formatQueryDuration(t.totalTime))
		}
		fmt.Println()
	}
}
