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

// TempFileEvent stores the timestamp and size of a temp file event.
type TempFileEvent struct {
	Timestamp time.Time
	Size      float64 // in bytes (as float for consistency)
}

// ============================================================================
// VERSION STREAMING
// ============================================================================

// TempFileAnalyzer traite les temp files au fil de l'eau.
type TempFileAnalyzer struct {
	count     int
	totalSize int64
	events    []TempFileEvent
}

// NewTempFileAnalyzer crée un nouvel analyseur de temp files.
func NewTempFileAnalyzer() *TempFileAnalyzer {
	return &TempFileAnalyzer{
		events: make([]TempFileEvent, 0, 1000), // Pre-allocate for common case
	}
}

// Process traite une entrée de log pour détecter les temp files.
func (a *TempFileAnalyzer) Process(entry *parser.LogEntry) {
	if !strings.Contains(entry.Message, "temporary file") {
		return // Skip non-relevant entries immediately
	}

	a.count++

	// Extract size from last word (same logic as before)
	words := strings.Fields(entry.Message)
	if len(words) > 0 {
		sizeStr := words[len(words)-1]
		if size, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
			a.totalSize += size
			a.events = append(a.events, TempFileEvent{
				Timestamp: entry.Timestamp,
				Size:      float64(size),
			})
		}
	}
}

// Finalize retourne les métriques finales.
func (a *TempFileAnalyzer) Finalize() TempFileMetrics {
	return TempFileMetrics{
		Count:     a.count,
		TotalSize: a.totalSize,
		Events:    a.events,
	}
}

// ============================================================================
// ANCIENNE VERSION (garde pour compatibilité si besoin)
// ============================================================================

// CalculateTemporaryFileMetrics est l'ancienne version qui prend toutes les entrées.
// À supprimer une fois le refactoring terminé.
func CalculateTemporaryFileMetrics(entries *[]parser.LogEntry) TempFileMetrics {
	analyzer := NewTempFileAnalyzer()
	for i := range *entries {
		analyzer.Process(&(*entries)[i])
	}
	return analyzer.Finalize()
}
