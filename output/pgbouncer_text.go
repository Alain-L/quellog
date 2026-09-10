//go:build !js

package output

import (
	"fmt"
	"time"

	"github.com/Alain-L/quellog/analysis"
)

// sessionBucketOrder is the fixed display order for the session-time
// histogram, matching the PostgreSQL connection report's buckets.
var sessionBucketOrder = []string{"< 1s", "1s - 1min", "1min - 30min", "30min - 2h", "2h - 5h", "> 5h"}

// PrintPgBouncer renders the pgBouncer pooler report to stdout in the
// same visual language as the PostgreSQL text report (bold headers,
// two-space indented "label : value" lines, shared histogram helper).
func PrintPgBouncer(m analysis.PgBouncerMetrics) {
	bold := ansiBold
	reset := ansiReset
	duration := m.MaxTimestamp.Sub(m.MinTimestamp)

	fmt.Println(bold + "\nPGBOUNCER SUMMARY\n" + reset)
	fmt.Printf("  %-25s : %s\n", "Start date", m.MinTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "End date", m.MaxTimestamp.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  %-25s : %s\n", "Duration", duration)
	fmt.Printf("  %-25s : %d\n", "Log lines", m.Lines)
	fmt.Printf("  %-25s : %d\n", "Stats snapshots", m.StatsSampleCount)
	fmt.Printf("  %-25s : %d\n", "Login attempts", m.LoginAttempts)
	fmt.Printf("  %-25s : %d\n", "Server connections", m.ServerConnections)
	fmt.Printf("  %-25s : %d\n", "Completed sessions", m.Sessions)
	if m.ReservePoolActivations > 0 {
		fmt.Printf("  %-25s : %d\n", "Reserve pool activations", m.ReservePoolActivations)
	}
	fmt.Printf("  %-25s : %d\n", "Errors", m.TotalErrors)

	// Traffic (from the per-second stats snapshots).
	if m.StatsSampleCount > 0 {
		fmt.Println(bold + "\nREQUEST THROUGHPUT\n" + reset)
		fmt.Printf("  %-25s : %.1f /s  (max %d /s)\n", "Transactions", m.AvgXactsPerSec, m.MaxXactsPerSec)
		fmt.Printf("  %-25s : %.1f /s  (max %d /s)\n", "Queries", m.AvgQueriesPerSec, m.MaxQueriesPerSec)
		fmt.Printf("  %-25s : %s  (max %s)\n", "Network in", bytesPerSec(m.AvgInBytesPerSec), bytesPerSec(float64(m.MaxInBytesPerSec)))
		fmt.Printf("  %-25s : %s  (max %s)\n", "Network out", bytesPerSec(m.AvgOutBytesPerSec), bytesPerSec(float64(m.MaxOutBytesPerSec)))

		fmt.Println(bold + "\nQUERY TIMING\n" + reset)
		fmt.Printf("  %-25s : %s  (max %s)\n", "Avg transaction time", micros(m.AvgXactMicros), microsI(m.MaxXactMicros))
		fmt.Printf("  %-25s : %s  (max %s)\n", "Avg query time", micros(m.AvgQueryMicros), microsI(m.MaxQueryMicros))
		fmt.Printf("  %-25s : %s  (max %s)\n", "Avg wait time", micros(m.AvgWaitMicros), microsI(m.MaxWaitMicros))
	}

	// Sessions.
	if m.Sessions > 0 {
		fmt.Println(bold + "\nSESSIONS\n" + reset)
		PrintHistogram(m.SessionDistribution, "Session time", "", 1, sessionBucketOrder)
		fmt.Printf("  %-25s : %d\n", "Completed sessions", m.Sessions)
		fmt.Printf("  %-25s : %s\n", "Average session time", formatSessionDuration(time.Duration(m.AvgSessionSeconds()*float64(time.Second))))
		if len(m.SessionsPerDatabase) > 0 {
			printTopCountTable("Sessions per database:", m.SessionsPerDatabase, m.Sessions)
		}
		if len(m.SessionsPerUser) > 0 {
			printTopCountTable("Sessions per user:", m.SessionsPerUser, m.Sessions)
		}
		if len(m.SessionsPerHost) > 0 {
			printTopCountTable("Sessions per host:", m.SessionsPerHost, m.Sessions)
		}
	}

	// Connections (client login attempts).
	if m.LoginAttempts > 0 {
		fmt.Println(bold + "\nCONNECTIONS\n" + reset)
		fmt.Printf("  %-25s : %d\n", "Login attempts", m.LoginAttempts)
		fmt.Printf("  %-25s : %d\n", "Server connections", m.ServerConnections)
		if len(m.ConnectionsPerDatabase) > 0 {
			printTopCountTable("Connections per database:", m.ConnectionsPerDatabase, m.LoginAttempts)
		}
		if len(m.ConnectionsPerUser) > 0 {
			printTopCountTable("Connections per user:", m.ConnectionsPerUser, m.LoginAttempts)
		}
		if len(m.ConnectionsPerHost) > 0 {
			printTopCountTable("Connections per host:", m.ConnectionsPerHost, m.LoginAttempts)
		}
	}

	// Errors.
	if m.TotalErrors > 0 {
		fmt.Println(bold + "\nMOST FREQUENT ERRORS\n" + reset)
		printTopCountTable("Errors:", m.Errors, m.TotalErrors)
	}

	fmt.Println()
}

// bytesPerSec formats a per-second byte rate, e.g. "1.7 kB/s".
func bytesPerSec(v float64) string {
	return FormatBytes(int64(v+0.5)) + "/s"
}

// micros formats a microsecond value with unit-appropriate precision.
// pgBouncer reports timings in microseconds; keeping µs/ms resolution is
// a deliberate advantage over the reference analyzer, which rounds to
// milliseconds. Examples: 842 → "842 µs", 3425 → "3.4 ms", 2.4e6 → "2.4 s".
func micros(us float64) string {
	switch {
	case us < 1000:
		return fmt.Sprintf("%.0f µs", us)
	case us < 1_000_000:
		return fmt.Sprintf("%.1f ms", us/1000)
	default:
		return fmt.Sprintf("%.2f s", us/1_000_000)
	}
}

func microsI(us int64) string {
	return micros(float64(us))
}
