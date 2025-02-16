// analysis/checkpoints.go
package analysis

import (
	"strings"

	"dalibo/quellog/parser"
)

// AnalyzeCheckpoints retourne les entrées de log liées aux checkpoints.
func AnalyzeCheckpoints(entries []parser.LogEntry) []parser.LogEntry {
	var results []parser.LogEntry
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Message), "checkpoint") {
			results = append(results, entry)
		}
	}
	return results
}
