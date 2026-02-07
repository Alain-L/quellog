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

const version = "0.3.0-wasm"

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

// parseFilterTime tries multiple formats for filter time inputs
func parseFilterTime(s string) (time.Time, bool) {
	formats := []string{
		"2006-01-02 15:04:05", // Go output format (space with seconds)
		"2006-01-02T15:04:05", // ISO with seconds
		"2006-01-02T15:04",    // ISO without seconds
		"2006-01-02 15:04",    // Space without seconds
	}
	for _, fmt := range formats {
		if t, err := time.Parse(fmt, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// convertFilters converts JS filters to parser.LogFilters
func convertFilters(jsf JSFilters) parser.LogFilters {
	var f parser.LogFilters

	// Parse time range (supports multiple formats)
	if jsf.Begin != "" {
		if t, ok := parseFilterTime(jsf.Begin); ok {
			f.BeginT = t
		}
	}
	if jsf.End != "" {
		if t, ok := parseFilterTime(jsf.End); ok {
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

// parseAndAnalyze is the core parsing logic shared by parse methods.
func parseAndAnalyze(data []byte, filters parser.LogFilters) string {
	t0 := now()

	// Detect format
	sampleSize := 32 * 1024
	if len(data) < sampleSize {
		sampleSize = len(data)
	}
	format := parser.DetectFormatFromContent(string(data[:sampleSize]))
	if format == "" {
		format = "stderr"
	}

	// Parse
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
	uniAnalyzer := analysis.NewUniqueEntityAnalyzer()
	sqlAnalyzer := analysis.NewSQLAnalyzerWithSize(int64(len(data)))

	var globalMetrics analysis.GlobalMetrics
	for i := range entries {
		entry := &entries[i]
		if !parser.PassesFilters(*entry, filters) {
			continue
		}
		tempAnalyzer.Process(entry)
		vacAnalyzer.Process(entry)
		chkAnalyzer.Process(entry)
		connAnalyzer.Process(entry)
		lockAnalyzer.Process(entry)
		evtAnalyzer.Process(entry)
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
	evtSummaries, topEvents := evtAnalyzer.Finalize()
	uniMetrics := uniAnalyzer.Finalize()
	sqlMetrics := sqlAnalyzer.Finalize()

	analysis.CollectQueriesWithoutDuration(&sqlMetrics, &lockMetrics, &tempMetrics)

	metrics := analysis.AggregatedMetrics{
		Global:         globalMetrics,
		TempFiles:      tempMetrics,
		Vacuum:         vacMetrics,
		Checkpoints:    chkMetrics,
		Connections:    connMetrics,
		Locks:          lockMetrics,
		EventSummaries: evtSummaries,
		TopEvents:      topEvents,
		UniqueEntities: uniMetrics,
		SQL:            sqlMetrics,
	}

	sections := []string{"all"}
	full := true
	processingMs := int64(now() - t0)
	meta := &output.MetaInfo{
		Format:    format,
		Entries:   metrics.Global.Count,
		Bytes:     int64(len(data)),
		ParseTime: formatDuration(processingMs),
	}
	// Use compact JSON in WASM to reduce memory usage (no indentation overhead)
	jsonStr, err := output.ExportJSONStringWithMeta(metrics, sections, full, meta, true)
	if err != nil {
		return `{"error": "JSON export error: ` + err.Error() + `"}`
	}

	return jsonStr
}

func main() {
	js.Global().Set("quellogParse", js.FuncOf(parseLog))
	js.Global().Set("quellogParseBytes", js.FuncOf(parseLogBytes)) // Accepts Uint8Array (faster)
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
	uniAnalyzer := analysis.NewUniqueEntityAnalyzer()
	sqlAnalyzer := analysis.NewSQLAnalyzerWithSize(int64(len(content)))

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
	evtSummaries, topEvents := evtAnalyzer.Finalize()
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
		EventSummaries: evtSummaries,
		TopEvents:      topEvents,
		UniqueEntities: uniMetrics,
		SQL:            sqlMetrics,
	}

	// JSON Export (full=true to include all SQL analysis sections)
	sections := []string{"all"}
	full := true
	processingMs := int64(now() - t0)
	meta := &output.MetaInfo{
		Format:    format,
		Entries:   metrics.Global.Count,
		Bytes:     int64(len(content)),
		ParseTime: formatDuration(processingMs),
	}
	// Use compact JSON in WASM to reduce memory usage
	jsonStr, err := output.ExportJSONStringWithMeta(metrics, sections, full, meta, true)
	if err != nil {
		return `{"error": "JSON export error: ` + err.Error() + `"}`
	}

	return jsonStr
}

// parseLogBytes accepts a Uint8Array directly instead of a string.
// This reduces memory usage by avoiding the JS string → Go string conversion.
// Usage from JS: quellogParseBytes(uint8Array, filtersJson)
func parseLogBytes(this js.Value, args []js.Value) interface{} {
	t0 := now()

	if len(args) < 1 {
		return `{"error": "No input provided"}`
	}

	jsArray := args[0]
	if jsArray.IsNull() || jsArray.IsUndefined() {
		return `{"error": "Input is null or undefined"}`
	}

	// Get length from Uint8Array
	length := jsArray.Get("length").Int()
	if length == 0 {
		return `{"error": "Empty input"}`
	}

	// Allocate buffer and copy directly from JS Uint8Array
	// This is 1 copy instead of 2 (no string intermediate)
	data := make([]byte, length)
	js.CopyBytesToGo(data, jsArray)

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

	// Detect format
	sampleSize := 32 * 1024
	if length < sampleSize {
		sampleSize = length
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
	uniAnalyzer := analysis.NewUniqueEntityAnalyzer()
	sqlAnalyzer := analysis.NewSQLAnalyzerWithSize(int64(length))

	var globalMetrics analysis.GlobalMetrics
	for i := range entries {
		entry := &entries[i]

		// Apply filters
		if !parser.PassesFilters(*entry, filters) {
			continue
		}

		tempAnalyzer.Process(entry)
		vacAnalyzer.Process(entry)
		chkAnalyzer.Process(entry)
		connAnalyzer.Process(entry)
		lockAnalyzer.Process(entry)
		evtAnalyzer.Process(entry)
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
	evtSummaries, topEvents := evtAnalyzer.Finalize()
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
		EventSummaries: evtSummaries,
		TopEvents:      topEvents,
		UniqueEntities: uniMetrics,
		SQL:            sqlMetrics,
	}

	// JSON Export
	sections := []string{"all"}
	full := true
	processingMs := int64(now() - t0)
	meta := &output.MetaInfo{
		Format:    format,
		Entries:   metrics.Global.Count,
		Bytes:     int64(length),
		ParseTime: formatDuration(processingMs),
	}
	// Use compact JSON in WASM to reduce memory usage
	jsonStr, err := output.ExportJSONStringWithMeta(metrics, sections, full, meta, true)
	if err != nil {
		return `{"error": "JSON export error: ` + err.Error() + `"}`
	}

	return jsonStr
}