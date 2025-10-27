package output

import (
	"fmt"
	"strings"

	"github.com/Alain-L/quellog/analysis"
)

// FormatErrorClassSummary returns a formatted string representing the error class summary as an elegant table.
func FormatErrorClassSummary(summaries []analysis.ErrorClassSummary) string {
	// Define the column headers.
	headers := []string{"Class", "Description", "Count"}

	// Determine maximum widths for each column.
	widthCode := len(headers[0])
	widthDesc := len(headers[1])
	widthCount := len(headers[2])
	for _, s := range summaries {
		if len(s.ClassCode) > widthCode {
			widthCode = len(s.ClassCode)
		}
		if len(s.Description) > widthDesc {
			widthDesc = len(s.Description)
		}
		countStr := fmt.Sprintf("%d", s.Count)
		if len(countStr) > widthCount {
			widthCount = len(countStr)
		}
	}

	// Compute the total table width.
	totalWidth := (widthCode + 2) + (widthDesc + 2) + (widthCount + 2) + 4

	// Build the border lines.
	topLine := fmt.Sprintf("┌%s┐", strings.Repeat("─", totalWidth-2))
	headerSep := fmt.Sprintf("├%s┼%s┼%s┤",
		strings.Repeat("─", widthCode+2),
		strings.Repeat("─", widthDesc+2),
		strings.Repeat("─", widthCount+2),
	)
	bottomLine := fmt.Sprintf("└%s┘", strings.Repeat("─", totalWidth-2))

	// Build the header row.
	headerRow := fmt.Sprintf("│ %-*s │ %-*s │ %-*s │",
		widthCode, headers[0],
		widthDesc, headers[1],
		widthCount, headers[2],
	)

	// Assemble data rows.
	var sb strings.Builder
	sb.WriteString(topLine + "\n")
	sb.WriteString(headerRow + "\n")
	sb.WriteString(headerSep + "\n")
	for _, s := range summaries {
		sb.WriteString(fmt.Sprintf("│ %-*s │ %-*s │ %*d │\n",
			widthCode, s.ClassCode,
			widthDesc, s.Description,
			widthCount, s.Count,
		))
	}
	sb.WriteString(bottomLine)
	return sb.String()

}
