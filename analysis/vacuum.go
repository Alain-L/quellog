// analysis/vacuum.go
package analysis

import (
	"strings"

	"dalibo/quellog/parser"
)

// AnalyzeVacuum retourne les entrées de log liées aux opérations VACUUM.
func AnalyzeVacuum(entries []parser.LogEntry) []parser.LogEntry {
	var results []parser.LogEntry
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Message), "vacuum") {
			results = append(results, entry)
		}
	}
	return results
}
