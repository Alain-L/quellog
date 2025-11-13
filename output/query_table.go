// Package output provides query table formatting functionality.
package output

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Alain-L/quellog/analysis"
	"golang.org/x/term"
)

// QueryTableColumn represents a column in a query table.
type QueryTableColumn struct {
	// Header is the column header text
	Header string

	// Width is the column width (0 = auto-calculate)
	Width int

	// Alignment is the column alignment ("left", "right")
	Alignment string

	// ValueFunc extracts the column value from a QueryRow
	ValueFunc func(row QueryRow) string
}

// QueryRow represents a single row in the query table.
type QueryRow struct {
	ID            string
	Query         string
	QueryType     string
	Count         int
	TotalTime     float64 // ms
	AvgTime       float64 // ms
	MaxTime       float64 // ms
	TotalSize     int64   // bytes
	AcquiredCount int
	WaitingCount  int
	WaitTime      float64 // ms
}

// QueryTableConfig configures the query table display.
type QueryTableConfig struct {
	// Columns to display
	Columns []QueryTableColumn

	// SortFunc sorts the rows
	SortFunc func(rows []QueryRow)

	// FilterFunc filters rows (return true to include)
	FilterFunc func(row QueryRow) bool

	// Limit is the maximum number of rows to display (0 = no limit)
	Limit int

	// CompactMode forces compact display even on wide terminals
	CompactMode bool

	// ShowQueryText shows full query text in wide mode
	ShowQueryText bool
}

// PrintQueryTable prints a formatted table of queries.
func PrintQueryTable(queryStats map[string]*analysis.QueryStat, config QueryTableConfig) {
	if len(queryStats) == 0 {
		return
	}

	// Convert map to rows
	var rows []QueryRow
	for normalized, stats := range queryStats {
		id, _ := analysis.GenerateQueryID(stats.RawQuery, normalized)
		row := QueryRow{
			ID:        id,
			Query:     normalized,
			QueryType: analysis.QueryTypeFromID(id),
			Count:     stats.Count,
			TotalTime: stats.TotalTime,
			AvgTime:   stats.AvgTime,
			MaxTime:   stats.MaxTime,
		}

		// Apply filter
		if config.FilterFunc != nil && !config.FilterFunc(row) {
			continue
		}

		rows = append(rows, row)
	}

	// Sort rows
	if config.SortFunc != nil {
		config.SortFunc(rows)
	}

	// Apply limit
	if config.Limit > 0 && len(rows) > config.Limit {
		rows = rows[:config.Limit]
	}

	if len(rows) == 0 {
		return
	}

	// Detect terminal width
	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		termWidth = 120
	}

	// Choose display mode
	wideMode := termWidth >= 120 && !config.CompactMode && config.ShowQueryText

	bold := "\033[1m"
	reset := "\033[0m"

	if wideMode {
		// Wide mode: show full query text
		printWideQueryTable(rows, config, termWidth, bold, reset)
	} else {
		// Compact mode: show query type only
		printCompactQueryTable(rows, config, bold, reset)
	}
}

// printWideQueryTable prints the table with full query text.
func printWideQueryTable(rows []QueryRow, config QueryTableConfig, termWidth int, bold, reset string) {
	// Calculate query column width (60% of terminal width)
	queryWidth := int(float64(termWidth) * 0.6)
	if queryWidth < 40 {
		queryWidth = 40
	}

	// Build header
	var headerParts []string
	var widthParts []int

	for _, col := range config.Columns {
		if col.Header == "Query" {
			headerParts = append(headerParts, fmt.Sprintf("%-*s", queryWidth, col.Header))
			widthParts = append(widthParts, queryWidth)
		} else {
			width := col.Width
			if width == 0 {
				width = 12 // default width
			}
			if col.Alignment == "right" {
				headerParts = append(headerParts, fmt.Sprintf("%*s", width, col.Header))
			} else {
				headerParts = append(headerParts, fmt.Sprintf("%-*s", width, col.Header))
			}
			widthParts = append(widthParts, width)
		}
	}

	// Print header
	fmt.Print(bold)
	fmt.Println(strings.Join(headerParts, "  "))
	fmt.Print(reset)

	// Print separator
	totalWidth := 0
	for i, w := range widthParts {
		totalWidth += w
		if i < len(widthParts)-1 {
			totalWidth += 2 // spacing
		}
	}
	fmt.Println(strings.Repeat("-", totalWidth))

	// Print rows
	for _, row := range rows {
		var rowParts []string
		for _, col := range config.Columns {
			value := col.ValueFunc(row)
			if col.Header == "Query" {
				value = truncateQuery(value, queryWidth)
				rowParts = append(rowParts, fmt.Sprintf("%-*s", queryWidth, value))
			} else {
				width := col.Width
				if width == 0 {
					width = 12
				}
				if col.Alignment == "right" {
					rowParts = append(rowParts, fmt.Sprintf("%*s", width, value))
				} else {
					rowParts = append(rowParts, fmt.Sprintf("%-*s", width, value))
				}
			}
		}
		fmt.Println(strings.Join(rowParts, "  "))
	}
}

// printCompactQueryTable prints the table with query type only.
func printCompactQueryTable(rows []QueryRow, config QueryTableConfig, bold, reset string) {
	// Build header
	var headerParts []string
	var widthParts []int

	for _, col := range config.Columns {
		if col.Header == "Query" {
			// Replace with "Type" column
			headerParts = append(headerParts, fmt.Sprintf("%-10s", "Type"))
			widthParts = append(widthParts, 10)
		} else {
			width := col.Width
			if width == 0 {
				width = 12
			}
			if col.Alignment == "right" {
				headerParts = append(headerParts, fmt.Sprintf("%*s", width, col.Header))
			} else {
				headerParts = append(headerParts, fmt.Sprintf("%-*s", width, col.Header))
			}
			widthParts = append(widthParts, width)
		}
	}

	// Print header
	fmt.Print(bold)
	fmt.Println(strings.Join(headerParts, "  "))
	fmt.Print(reset)

	// Print separator
	fmt.Println(strings.Repeat("-", 80))

	// Print rows
	for _, row := range rows {
		var rowParts []string
		for _, col := range config.Columns {
			if col.Header == "Query" {
				// Show type instead
				rowParts = append(rowParts, fmt.Sprintf("%-10s", row.QueryType))
			} else {
				value := col.ValueFunc(row)
				width := col.Width
				if width == 0 {
					width = 12
				}
				if col.Alignment == "right" {
					rowParts = append(rowParts, fmt.Sprintf("%*s", width, value))
				} else {
					rowParts = append(rowParts, fmt.Sprintf("%-*s", width, value))
				}
			}
		}
		fmt.Println(strings.Join(rowParts, "  "))
	}
}

// Standard sort functions

// SortByMaxTime sorts query rows by maximum execution time (descending).
func SortByMaxTime(rows []QueryRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].MaxTime != rows[j].MaxTime {
			return rows[i].MaxTime > rows[j].MaxTime
		}
		return rows[i].Query < rows[j].Query
	})
}

// SortByTotalTime sorts query rows by total execution time (descending).
func SortByTotalTime(rows []QueryRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalTime != rows[j].TotalTime {
			return rows[i].TotalTime > rows[j].TotalTime
		}
		return rows[i].Query < rows[j].Query
	})
}

// SortByCount sorts query rows by execution count (descending).
func SortByCount(rows []QueryRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Query < rows[j].Query
	})
}

// SortByTotalSize sorts query rows by total size (descending).
func SortByTotalSize(rows []QueryRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalSize != rows[j].TotalSize {
			return rows[i].TotalSize > rows[j].TotalSize
		}
		return rows[i].Query < rows[j].Query
	})
}

// Standard column definitions

// ColumnSQLID returns the SQLID column.
func ColumnSQLID() QueryTableColumn {
	return QueryTableColumn{
		Header:    "SQLID",
		Width:     9,
		Alignment: "left",
		ValueFunc: func(row QueryRow) string { return row.ID },
	}
}

// ColumnQuery returns the Query column.
func ColumnQuery() QueryTableColumn {
	return QueryTableColumn{
		Header:    "Query",
		Width:     0, // auto-calculated
		Alignment: "left",
		ValueFunc: func(row QueryRow) string { return row.Query },
	}
}

// ColumnCount returns the execution count column.
func ColumnCount() QueryTableColumn {
	return QueryTableColumn{
		Header:    "Executed",
		Width:     10,
		Alignment: "right",
		ValueFunc: func(row QueryRow) string { return fmt.Sprintf("%d", row.Count) },
	}
}

// ColumnMaxTime returns the maximum duration column.
func ColumnMaxTime() QueryTableColumn {
	return QueryTableColumn{
		Header:    "Max",
		Width:     12,
		Alignment: "right",
		ValueFunc: func(row QueryRow) string { return formatQueryDuration(row.MaxTime) },
	}
}

// ColumnAvgTime returns the average duration column.
func ColumnAvgTime() QueryTableColumn {
	return QueryTableColumn{
		Header:    "Avg",
		Width:     12,
		Alignment: "right",
		ValueFunc: func(row QueryRow) string { return formatQueryDuration(row.AvgTime) },
	}
}

// ColumnTotalTime returns the total duration column.
func ColumnTotalTime() QueryTableColumn {
	return QueryTableColumn{
		Header:    "Total",
		Width:     12,
		Alignment: "right",
		ValueFunc: func(row QueryRow) string { return formatQueryDuration(row.TotalTime) },
	}
}

// ColumnDuration returns a simple duration column (for slowest queries).
func ColumnDuration() QueryTableColumn {
	return QueryTableColumn{
		Header:    "Duration",
		Width:     12,
		Alignment: "right",
		ValueFunc: func(row QueryRow) string { return formatQueryDuration(row.MaxTime) },
	}
}
