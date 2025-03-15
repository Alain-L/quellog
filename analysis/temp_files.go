// analysis/temp_files.go
package analysis

import (
	"dalibo/quellog/parser"
	"strconv"
	"strings"
	"time"
)

// TempFileMetrics aggregates statistics related to temporary files.
type TempFileMetrics struct {
	Count     int             // Number of temp file events
	TotalSize int64           // Cumulative temp file size in bytes
	Events    []TempFileEvent // for each timestamp and tempfile size
}

// For Histogram
// QueryExecution stores the timestamp and duration of a single SQL query.
type TempFileEvent struct {
	Timestamp time.Time
	Size      float64 // in milliseconds
}

func CalculateTemporaryFileMetrics(entries *[]parser.LogEntry) TempFileMetrics {
	var metrics TempFileMetrics
	for _, entry := range *entries {
		if strings.Contains(entry.Message, "temporary file") {
			metrics.Count++
			// On suppose que le dernier mot de la ligne contient la taille du fichier.
			words := strings.Fields(entry.Message)
			if len(words) > 0 {
				sizeStr := words[len(words)-1]
				size, err := strconv.ParseInt(sizeStr, 10, 64)
				if err == nil {
					metrics.TotalSize += size
					// Ajout d'un événement avec le timestamp et la taille (convertie en float64)
					metrics.Events = append(metrics.Events, TempFileEvent{
						Timestamp: entry.Timestamp,
						Size:      float64(size),
					})
				}
			}
		}
	}
	return metrics
}
