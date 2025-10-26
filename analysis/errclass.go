package analysis

import (
	"regexp"
	"strings"

	"dalibo/quellog/parser"
)

// ErrorClassSummary represents a summary for a specific error class.
type ErrorClassSummary struct {
	// ClassCode is the two-character SQLSTATE error class code.
	ClassCode string
	// Description is the human-readable description of the error class.
	Description string
	// Count is the number of errors in this class.
	Count int
}

// errorClassDescriptions maps error class codes (the first two characters of SQLSTATE)
// to their PostgreSQL-defined descriptions.
var errorClassDescriptions = map[string]string{
	"00": "Successful Completion",
	"01": "Warning",
	"02": "No Data",
	"03": "SQL Statement Not Yet Complete",
	"08": "Connection Exception",
	"09": "Triggered Action Exception",
	"0A": "Feature Not Supported",
	"0B": "Invalid Transaction Initiation",
	"0F": "Locator Exception",
	"0L": "Invalid Grantor",
	"0P": "Invalid Role Specification",
	"20": "Case Not Found",
	"21": "Cardinality Violation",
	"22": "Data Exception",
	"23": "Integrity Constraint Violation",
	"24": "Invalid Cursor State",
	"25": "Invalid Transaction State",
	"26": "Invalid SQL Statement Name",
	"27": "Triggered Data Change Violation",
	"28": "Invalid Authorization Specification",
	"2B": "Dependent Privilege Descriptors Still Exist",
	"2D": "Invalid Transaction Termination",
	"2F": "SQL Routine Exception",
	"34": "Invalid Cursor Name",
	"38": "External Routine Exception",
	"39": "External Routine Invocation Exception",
	"3B": "Savepoint Exception",
	"3D": "Invalid Catalog Name",
	"3F": "Invalid Schema Name",
	"40": "Transaction Rollback",
}

// errorCodeRegex is used to find the SQLSTATE error code in a log message.
var errorCodeRegex = regexp.MustCompile(`SQLSTATE\s*=\s*'([0-9A-Z]{5})'`)

// ============================================================================
// VERSION STREAMING
// ============================================================================

// ErrorClassAnalyzer traite les error classes au fil de l'eau.
type ErrorClassAnalyzer struct {
	counts map[string]int
}

// NewErrorClassAnalyzer crée un nouvel analyseur d'error classes.
func NewErrorClassAnalyzer() *ErrorClassAnalyzer {
	return &ErrorClassAnalyzer{
		counts: make(map[string]int, 50),
	}
}

// Process traite une entrée de log pour détecter les SQLSTATE codes.
func (a *ErrorClassAnalyzer) Process(entry *parser.LogEntry) {
	// Early return si pas de SQLSTATE dans le message
	if !strings.Contains(entry.Message, "SQLSTATE") {
		return
	}

	msg := strings.ToUpper(entry.Message)
	match := errorCodeRegex.FindStringSubmatch(msg)
	if len(match) == 2 {
		sqlstate := match[1]
		classCode := sqlstate[:2]
		a.counts[classCode]++
	}
}

// Finalize retourne les métriques finales.
func (a *ErrorClassAnalyzer) Finalize() []ErrorClassSummary {
	summaries := make([]ErrorClassSummary, 0, len(a.counts))

	for classCode, count := range a.counts {
		description := errorClassDescriptions[classCode]
		if description == "" {
			description = "Unknown"
		}
		summaries = append(summaries, ErrorClassSummary{
			ClassCode:   classCode,
			Description: description,
			Count:       count,
		})
	}

	return summaries
}

// ============================================================================
// ANCIENNE VERSION (compatibilité backwards)
// ============================================================================

// SummarizeErrorClasses processes a slice of log entries, extracts SQLSTATE codes.
// À supprimer une fois le refactoring terminé.
func SummarizeErrorClasses(entries []parser.LogEntry) []ErrorClassSummary {
	analyzer := NewErrorClassAnalyzer()
	for i := range entries {
		analyzer.Process(&entries[i])
	}
	return analyzer.Finalize()
}
