//go:build js && wasm

// Package main provides the WASM entry point for quellog.
// It exposes the parser, analysis, and output packages to JavaScript.
package main

import (
	"encoding/json"
	"strconv"
	"syscall/js"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"
)

const version = "0.2.0-wasm"

// JSFilters is the JSON structure for filters from JavaScript
type JSFilters struct {
	Begin       string   `json:"begin"`       // ISO datetime string
	End         string   `json:"end"`         // ISO datetime string
	Database    []string `json:"database"`    // Database names
	User        []string `json:"user"`        // User names
	Application []string `json:"application"` // Application names
}

var perf = js.Global().Get("performance")

func now() float64 {
	return perf.Call("now").Float()
}

// convertFilters converts JS filters to parser.LogFilters
func convertFilters(jsf JSFilters) parser.LogFilters {
	var f parser.LogFilters

	// Parse time range (JS sends ISO format like "2024-06-05T00:00")
	if jsf.Begin != "" {
		if t, err := time.Parse("2006-01-02T15:04", jsf.Begin); err == nil {
			f.BeginT = t
		}
	}
	if jsf.End != "" {
		if t, err := time.Parse("2006-01-02T15:04", jsf.End); err == nil {
			f.EndT = t
		}
	}

	f.DbFilter = jsf.Database
	f.UserFilter = jsf.User
	f.AppFilter = jsf.Application

	return f
}

func formatDuration(ms int64) string {
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	// Format seconds with 2 decimal places
	secs := float64(ms) / 1000
	return strconv.FormatFloat(secs, 'f', 2, 64) + "s"
}

func main() {
	js.Global().Set("quellogParse", js.FuncOf(parseLog))
	js.Global().Set("quellogVersion", js.FuncOf(getVersion))
	select {}
}

func getVersion(this js.Value, args []js.Value) interface{} {
	return version
}

func parseLog(this js.Value, args []js.Value) interface{} {
	t0 := now()

	if len(args) < 1 {
		return `{"error": "No input provided"}`
	}

	content := args[0].String()
	if len(content) == 0 {
		return `{"error": "Empty input"}`
	}

	// Parse optional filters (second argument)
	var filters parser.LogFilters
	if len(args) >= 2 && !args[1].IsNull() && !args[1].IsUndefined() {
		filtersJSON := args[1].String()
		if filtersJSON != "" {
			var jsFilters JSFilters
			if err := json.Unmarshal([]byte(filtersJSON), &jsFilters); err == nil {
				filters = convertFilters(jsFilters)
			}
		}
	}

	// Convert to bytes for optimized parsing
	data := []byte(content)

	// Detect format
	sampleSize := 32 * 1024
	if len(data) < sampleSize {
		sampleSize = len(data)
	}
	format := parser.DetectFormatFromContent(string(data[:sampleSize]))
	if format == "" {
		format = "stderr"
	}

	// Parse using optimized bytes path
	entries, parseErr := parser.ParseFromBytesSync(data, format)
	if parseErr != nil {
		return `{"error": "Parse error: ` + parseErr.Error() + `"}`
	}

	// Analysis
	tempAnalyzer := analysis.NewTempFileAnalyzer()
	vacAnalyzer := analysis.NewVacuumAnalyzer()
	chkAnalyzer := analysis.NewCheckpointAnalyzer()
	connAnalyzer := analysis.NewConnectionAnalyzer()
	lockAnalyzer := analysis.NewLockAnalyzer()
	evtAnalyzer := analysis.NewEventAnalyzer()
	errAnalyzer := analysis.NewErrorClassAnalyzer()
	uniAnalyzer := analysis.NewUniqueEntityAnalyzer()
	sqlAnalyzer := analysis.NewSQLAnalyzer()

	var globalMetrics analysis.GlobalMetrics
	var filteredCount int
	for i := range entries {
		entry := &entries[i]

		// Apply filters
		if !parser.PassesFilters(*entry, filters) {
			continue
		}
		filteredCount++

		tempAnalyzer.Process(entry)
		vacAnalyzer.Process(entry)
		chkAnalyzer.Process(entry)
		connAnalyzer.Process(entry)
		lockAnalyzer.Process(entry)
		evtAnalyzer.Process(entry)
		errAnalyzer.Process(entry)
		uniAnalyzer.Process(entry)
		sqlAnalyzer.Process(entry)

		if !entry.IsContinuation {
			globalMetrics.Count++
		}
		if globalMetrics.MinTimestamp.IsZero() || entry.Timestamp.Before(globalMetrics.MinTimestamp) {
			globalMetrics.MinTimestamp = entry.Timestamp
		}
		if globalMetrics.MaxTimestamp.IsZero() || entry.Timestamp.After(globalMetrics.MaxTimestamp) {
			globalMetrics.MaxTimestamp = entry.Timestamp
		}
	}

	tempMetrics := tempAnalyzer.Finalize()
	vacMetrics := vacAnalyzer.Finalize()
	chkMetrics := chkAnalyzer.Finalize()
	connMetrics := connAnalyzer.Finalize()
	lockMetrics := lockAnalyzer.Finalize()
	evtMetrics := evtAnalyzer.Finalize()
	errMetrics := errAnalyzer.Finalize()
	uniMetrics := uniAnalyzer.Finalize()
	sqlMetrics := sqlAnalyzer.Finalize()

	analysis.CollectQueriesWithoutDuration(&sqlMetrics, &lockMetrics, &tempMetrics)

	// Assemble metrics
	metrics := analysis.AggregatedMetrics{
		Global:         globalMetrics,
		TempFiles:      tempMetrics,
		Vacuum:         vacMetrics,
		Checkpoints:    chkMetrics,
		Connections:    connMetrics,
		Locks:          lockMetrics,
		EventSummaries: evtMetrics,
		ErrorClasses:   errMetrics,
		UniqueEntities: uniMetrics,
		SQL:            sqlMetrics,
	}

	// JSON Export
	sections := []string{"all"}
	processingMs := int64(now() - t0)
	meta := &output.MetaInfo{
		Format:    format,
		Entries:   metrics.Global.Count,
		Bytes:     int64(len(content)),
		ParseTime: formatDuration(processingMs),
	}
	jsonStr, err := output.ExportJSONStringWithMeta(metrics, sections, meta)
	if err != nil {
		return `{"error": "JSON export error: ` + err.Error() + `"}`
	}

	return jsonStr
}
