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
	// Ajoutez d'autres classes au besoin...
}

// errorCodeRegex is used to find the SQLSTATE error code in a log message.
// On s'attend à une mention du type "SQLSTATE = 'XXXXX'" (où X est un chiffre ou une lettre).
var errorCodeRegex = regexp.MustCompile(`SQLSTATE\s*=\s*'([0-9A-Z]{5})'`)

// SummarizeErrorClasses processes a slice of log entries, extracts SQLSTATE codes (if présents)
// et regroupe les erreurs par classe (les deux premiers caractères du code).
func SummarizeErrorClasses(entries []parser.LogEntry) []ErrorClassSummary {
	counts := make(map[string]int)
	for _, entry := range entries {
		msg := strings.ToUpper(entry.Message)
		// On recherche le code SQLSTATE dans le message.
		match := errorCodeRegex.FindStringSubmatch(msg)
		if len(match) == 2 {
			sqlstate := match[1]
			// La classe d'erreur est constituée des deux premiers caractères.
			classCode := sqlstate[:2]
			counts[classCode]++
		}
	}

	// Transforme la map en slice de résumés.
	var summaries []ErrorClassSummary
	for classCode, count := range counts {
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
