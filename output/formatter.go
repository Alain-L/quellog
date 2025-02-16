// output/formatter.go
package output

import (
	"fmt"
	"time"
)

// AnalysisReport rassemble les données issues de l'analyse des logs.
type AnalysisReport struct {
	StartDate        time.Time
	EndDate          time.Time
	Duration         time.Duration
	VacuumCount      int
	CheckpointsCount int
	TempFiles        int
	TempFileSize     int64
	SQLCount         int
}

// formatBytes convertit une taille (en bytes) en une chaîne lisible (GB, MB, KB ou B).
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(GB))
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// Formatter définit l'interface pour formater le rapport.
type Formatter interface {
	Format(report AnalysisReport) string
}
