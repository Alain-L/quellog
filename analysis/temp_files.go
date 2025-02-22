// analysis/temp_files.go
package analysis

import (
	"dalibo/quellog/parser"
	"strings"
)

// TempFileMetrics aggregates statistics related to temporary files.
type TempFileMetrics struct {
	Count     int   // Number of temp file events
	TotalSize int64 // Cumulative temp file size in bytes
}

// CalculateTotalTemporaryFileSize returns the cumulative size (in bytes) of temporary file messages.
func CalculateTotalTemporaryFileSize(entries []parser.LogEntry) int64 {
	var totalSize int64
	for _, entry := range entries {
		// Exemple : on recherche dans le message un indice indiquant la taille d'un fichier temporaire.
		// Vous devrez adapter l'extraction en fonction du format de vos logs.
		if strings.Contains(strings.ToLower(entry.Message), "temp file") {
			// Ici, vous pouvez extraire la taille réelle.
			// Pour l'exemple, on suppose une taille fictive de 1000 octets par entrée.
			totalSize += 1000
		}
	}
	return totalSize
}

// CalculateTotalTemporaryFileCount returns the number of temporary file-related log entries.
func CalculateTotalTemporaryFileCount(entries []parser.LogEntry) int {
	count := 0
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Message), "temp file") {
			count++
		}
	}
	return count
}
