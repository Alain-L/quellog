//go:build js && wasm

// Package main provides the WASM entry point for quellog.
// It exposes the parser, analysis, and output packages to JavaScript.
package main

import (
	"context"
	"encoding/json"
	"strconv"
	"syscall/js"
	"time"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"
)

var version = "dev"

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
	secs := float64(ms) / 1000
	return strconv.FormatFloat(secs, 'f', 2, 64) + "s"
}

// parseFiltersArg extracts an optional JSFilters JSON string from args[idx].
func parseFiltersArg(args []js.Value, idx int) parser.LogFilters {
	var f parser.LogFilters
	if len(args) <= idx || args[idx].IsNull() || args[idx].IsUndefined() {
		return f
	}
	s := args[idx].String()
	if s == "" {
		return f
	}
	var jsFilters JSFilters
	if err := json.Unmarshal([]byte(s), &jsFilters); err == nil {
		f = convertFilters(jsFilters)
	}
	return f
}

// parseAndAnalyze runs the same pipeline as cmd/execute.go's
// runAnalysisCycle: format detection, streamed parse on a goroutine,
// optional FilterStream hop, then AggregateMetrics. Reusing the CLI's
// AggregateMetrics gives the WASM build the same parallel SQL analyzer
// activation (>200 MB), the same StreamingAnalyzer state machine, and
// frees us from maintaining a parallel analyzer-orchestration block.
func parseAndAnalyze(data []byte, filters parser.LogFilters) string {
	t0 := now()

	sampleSize := 32 * 1024
	if len(data) < sampleSize {
		sampleSize = len(data)
	}
	format := parser.DetectFormatFromContent(string(data[:sampleSize]))
	if format == "" {
		format = "stderr"
	}

	ctx := context.Background()
	rawChan := make(chan []parser.LogEntry, 64)
	var parseErr error
	go func() {
		parseErr = parser.ParseFromBytesStream(data, format, rawChan)
		close(rawChan)
	}()

	var analyzeIn <-chan []parser.LogEntry
	if filters.IsEmpty() {
		analyzeIn = rawChan
	} else {
		filteredChan := make(chan []parser.LogEntry, 64)
		go parser.FilterStream(ctx, rawChan, filteredChan, filters)
		analyzeIn = filteredChan
	}

	metrics := analysis.AggregateMetrics(ctx, analyzeIn, int64(len(data)))
	if parseErr != nil {
		return `{"error": "Parse error: ` + parseErr.Error() + `"}`
	}
	analysis.CollectQueriesWithoutDuration(&metrics.SQL, &metrics.Locks, &metrics.TempFiles)

	processingMs := int64(now() - t0)
	meta := &output.MetaInfo{
		Format:    format,
		Entries:   metrics.Global.Count,
		Bytes:     int64(len(data)),
		ParseTime: formatDuration(processingMs),
	}
	jsonStr, err := output.ExportJSONStringWithMeta(metrics, []string{"all"}, true, meta, true)
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

// parseLog accepts a JS string. Usage: quellogParse(content, filtersJson)
func parseLog(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return `{"error": "No input provided"}`
	}
	content := args[0].String()
	if len(content) == 0 {
		return `{"error": "Empty input"}`
	}
	return parseAndAnalyze([]byte(content), parseFiltersArg(args, 1))
}

// parseLogBytes accepts a Uint8Array directly instead of a string.
// This reduces memory usage by avoiding the JS string → Go string conversion.
// Usage from JS: quellogParseBytes(uint8Array, filtersJson)
func parseLogBytes(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return `{"error": "No input provided"}`
	}
	jsArray := args[0]
	if jsArray.IsNull() || jsArray.IsUndefined() {
		return `{"error": "Input is null or undefined"}`
	}
	length := jsArray.Get("length").Int()
	if length == 0 {
		return `{"error": "Empty input"}`
	}
	data := make([]byte, length)
	js.CopyBytesToGo(data, jsArray)
	return parseAndAnalyze(data, parseFiltersArg(args, 1))
}
