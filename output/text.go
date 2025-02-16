// output/text.go
package output

import (
	"dalibo/quellog/analysis"
	"fmt"
	"strings"
)

// TextFormatter formate le rapport au format texte.
type TextFormatter struct{}

// NewTextFormatter retourne une instance de TextFormatter.
func NewTextFormatter() *TextFormatter {
	return &TextFormatter{}
}

// Format retourne une chaîne formatée avec les données du rapport.
func (tf *TextFormatter) Format(report AnalysisReport) string {
	return fmt.Sprintf(`Log Analysis Report:
Start date: %s
End date:   %s
Total duration: %s

Number of VACUUM events: %d
Number of checkpoints: %d
Temp files: %d
Temp file size: %s
Number of SQL queries: %d`,
		report.StartDate.Format("2006-01-02 15:04:05"),
		report.EndDate.Format("2006-01-02 15:04:05"),
		report.Duration,
		report.VacuumCount,
		report.CheckpointsCount,
		report.TempFiles,
		formatBytes(report.TempFileSize),
		report.SQLCount,
	)
}

// FormatEventSummary returns a formatted string representing the event summary as an elegant table
// with a merged, centered bold title row spanning the entire table width. After the event rows,
// a separate TOTAL row is added (displayed in its own two-cell box that merges the Count and Percentage columns).
func (tf *TextFormatter) FormatEventSummary(summaries []analysis.EventSummary) string {
	// Define column headers.
	headers := []string{"Type", "Count", "Percentage"}

	// Determine maximum widths for each column based on headers and data.
	widthType := len(headers[0])
	widthCount := len(headers[1])
	widthPercentage := len(headers[2])
	for _, summary := range summaries {
		if len(summary.Type) > widthType {
			widthType = len(summary.Type)
		}
		countStr := fmt.Sprintf("%d", summary.Count)
		if len(countStr) > widthCount {
			widthCount = len(countStr)
		}
		percStr := fmt.Sprintf("%.2f%%", summary.Percentage)
		if len(percStr) > widthPercentage {
			widthPercentage = len(percStr)
		}
	}

	// Compute the total table width for the three-column section.
	// Each column gets 2 spaces of padding (one on each side).
	// There are 4 extra characters for the outer borders.
	totalTableWidth := (widthType + 2) + (widthCount + 2) + (widthPercentage + 2) + 4

	// Build continuous border lines spanning the entire three-column table width.
	topLine := fmt.Sprintf("┌%s┐", strings.Repeat("─", totalTableWidth-2))
	mergedTitleBorder := fmt.Sprintf("├%s┤", strings.Repeat("─", totalTableWidth-2))
	// The lower border for the data section uses a left junction "├" and a right corner "┘".
	upperBottomLine := fmt.Sprintf("├%s┘", strings.Repeat("─", totalTableWidth-2))

	// Build the header separator with column splits.
	headerSep := fmt.Sprintf("├%s┼%s┼%s┤",
		strings.Repeat("─", widthType+2),
		strings.Repeat("─", widthCount+2),
		strings.Repeat("─", widthPercentage+2),
	)

	// Prepare the merged title row.
	titleText := "EVENTS SUMMARY"
	boldTitle := "\033[1m" + titleText + "\033[0m" // Bold the title using ANSI escape codes.
	availWidth := totalTableWidth - 2              // Available width inside the outer borders.
	padTotal := availWidth - len(titleText)
	if padTotal < 0 {
		padTotal = 0
	}
	leftPad := padTotal / 2
	rightPad := padTotal - leftPad
	titleRow := fmt.Sprintf("│%s%s%s│", strings.Repeat(" ", leftPad), boldTitle, strings.Repeat(" ", rightPad))

	// Build the column header row.
	headerRow := fmt.Sprintf("│ %-*s │ %-*s │ %-*s │",
		widthType, headers[0],
		widthCount, headers[1],
		widthPercentage, headers[2],
	)

	// Build the data rows.
	var dataRows strings.Builder
	for _, summary := range summaries {
		dataRows.WriteString(fmt.Sprintf("│ %-*s │ %*d │ %*s │\n",
			widthType, summary.Type,
			widthCount, summary.Count,
			widthPercentage, fmt.Sprintf("%.2f%%", summary.Percentage),
		))
	}

	// Calculate the total count across all event types.
	totalCount := 0
	for _, summary := range summaries {
		totalCount += summary.Count
	}

	// Build the TOTAL row in its own two-cell box (merging the Count and Percentage columns).
	// First, compute the width for the second cell.
	totalCountStr := fmt.Sprintf("%d", totalCount)
	rightCellWidth := widthCount
	if len(totalCountStr) > rightCellWidth {
		rightCellWidth = len(totalCountStr)
	}
	// The total box has two columns: left cell width = widthType+2, right cell width = rightCellWidth+2,
	// plus one vertical separator.
	totalTwoColWidth := (widthType + 2) + (rightCellWidth + 2) + 1

	// Build the TOTAL row (with both label and total printed in bold).
	totalRow := fmt.Sprintf("│ \033[1m%-*s\033[0m │ \033[1m%*s\033[0m │",
		widthType, "TOTAL",
		rightCellWidth, totalCountStr,
	)

	// Build the bottom border for the TOTAL row.
	finalLine := fmt.Sprintf("└%s┘", strings.Repeat("─", totalTwoColWidth))

	// Assemble the final table.
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(topLine + "\n")
	sb.WriteString(titleRow + "\n")
	sb.WriteString(mergedTitleBorder + "\n")
	sb.WriteString(headerRow + "\n")
	sb.WriteString(headerSep + "\n")
	sb.WriteString(dataRows.String())
	sb.WriteString(upperBottomLine + "\n")
	sb.WriteString(totalRow + "\n")
	sb.WriteString(finalLine)
	sb.WriteString("\n")

	return sb.String()
}
