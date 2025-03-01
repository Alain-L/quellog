// analysis/temp_files.go
package analysis

import (
	"dalibo/quellog/parser"
	"strconv"
	"strings"
)

// TempFileMetrics aggregates statistics related to temporary files.
type TempFileMetrics struct {
	Count     int   // Number of temp file events
	TotalSize int64 // Cumulative temp file size in bytes
}

func CalculateTemporaryFileMetrics(entries *[]parser.LogEntry) (count int, totalSize int64) {
	for _, entry := range *entries {
		if strings.Contains(entry.Message, "temporary file") {
			count++
			// Récupérer le dernier mot de la ligne qui est la taille du fichier
			words := strings.Fields(entry.Message)
			if len(words) > 0 {
				sizeStr := words[len(words)-1]
				size, err := strconv.ParseInt(sizeStr, 10, 64)
				if err == nil {
					totalSize += size
				}
			}
		}
	}
	return count, totalSize
}
