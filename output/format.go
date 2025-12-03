// Package output provides shared formatting functions.
package output

import (
	"fmt"
	"time"
)

// formatQueryDuration formats a duration in milliseconds to a human-readable string.
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
