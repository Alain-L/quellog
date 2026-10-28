//go:build js && wasm

// Package main provides the WASM entry point for quellog.
// It exposes the parser, analysis, and output packages to JavaScript.
package main

import (
	"context"
	"encoding/json"
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
func parseAndAnalyze(files [][]byte, filters parser.LogFilters) string {
	t0 := time.Now()

	// Each file is detected on its own head, as on the CLI: Patroni logs
	// take their own track, one member per file, and the others feed the
	// PostgreSQL pipeline one after the other.
	var pgFiles, patroniFiles [][]byte
	var formats []string
	var size int
	for _, data := range files {
		size += len(data)
		format := parser.DetectFormatFromContent(string(data[:min(len(data), 32*1024)]))
		switch format {
		case "patroni":
			patroniFiles = append(patroniFiles, data)
			continue
		case "":
			format = "stderr"
		}
		pgFiles = append(pgFiles, data)
		formats = append(formats, format)
	}
	format := "stderr"
	if len(formats) > 0 {
		format = formats[0]
	}

	ctx := context.Background()

	// The data may be one file or the members of a dropped archive joined
	// together: the stderr parser carves the pgBackRest lines out of it,
	// as on the CLI, and the observer analyzes them. pgBackRest and Patroni
	// have no database, user or application: an attribute filter leaves
	// them out.
	inScope := analysis.PgBackRestInScope(filters, []string{"all"})
	var pgbr *analysis.PgBackRestAnalyzer
	if inScope {
		pgbr = analysis.NewPgBackRestAnalyzer()
		pgbr.SetTimeWindow(time.Time(filters.BeginT), time.Time(filters.EndT))
	}
	var patroni *analysis.PatroniMetrics
	if inScope && len(patroniFiles) > 0 {
		feeds := make([]func(chan<- []parser.LogEntry), len(patroniFiles))
		for i, data := range patroniFiles {
			feeds[i] = func(out chan<- []parser.LogEntry) {
				parser.ParseFromBytesStream(data, "patroni", out)
				close(out)
			}
		}
		patroni = analysis.AnalyzePatroniMembers(ctx, feeds, filters)
	}

	rawChan := make(chan []parser.LogEntry, 64)
	var parseErr error
	go func() {
		for i, data := range pgFiles {
			if err := parser.ParseFromBytesStream(data, formats[i], rawChan); err != nil && parseErr == nil {
				parseErr = err
			}
		}
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

	metrics := analysis.AggregateMetricsObserved(ctx, analyzeIn, 1, pgbr)
	if parseErr != nil {
		return `{"error": "Parse error: ` + parseErr.Error() + `"}`
	}
	analysis.CollectQueriesWithoutDuration(&metrics.SQL, &metrics.Locks, &metrics.TempFiles)

	var pbr []analysis.PgBackRestMetrics
	if pgbr != nil {
		pbr = pgbr.Finalize()
	}
	if metrics.Global.Count == 0 && len(pbr) > 0 && patroni == nil {
		return pgBackRestOnlyJSON(pbr, format, size, t0)
	}

	entries := metrics.Global.Count
	if patroni != nil {
		patroni.CorrelatePostgres(metrics.Server.Timeline)
		metrics.Patroni = patroni
		entries += patroni.Entries
	}
	processingMs := time.Since(t0).Milliseconds()
	meta := &output.MetaInfo{
		Format:    format,
		Entries:   entries,
		Bytes:     int64(size),
		ParseTime: output.FormatDurationMs(processingMs),
	}
	jsonStr, err := output.ExportJSONStringWithMeta(metrics, pbr, []string{"all"}, true, meta, true)
	if err != nil {
		return `{"error": "JSON export error: ` + err.Error() + `"}`
	}
	return jsonStr
}

// pgBackRestOnlyJSON returns the browser demo's pgBackRest-only report,
// for data that held no PostgreSQL entry.
func pgBackRestOnlyJSON(pbr []analysis.PgBackRestMetrics, format string, size int, t0 time.Time) string {
	meta := &output.MetaInfo{
		Format:    format,
		Entries:   analysis.PgBackRestLinesRead(pbr),
		Bytes:     int64(size),
		ParseTime: output.FormatDurationMs(time.Since(t0).Milliseconds()),
	}
	jsonStr, err := output.ExportPgBackRestOnlyJSONStringWithMeta(pbr, meta)
	if err != nil {
		return `{"error": "JSON export error: ` + err.Error() + `"}`
	}
	return jsonStr
}

// splitAndAnalyze runs the same parse pipeline as parseAndAnalyze but, instead
// of one aggregate, buckets the stream by interval (AggregateMetricsBySplit) and
// returns the period-navigator payload — a JSON array of per-period blobs,
// exactly the REPORT_PERIODS shape the standalone --split report embeds. On
// failure it returns a {"error": ...} object instead of an array.
func splitAndAnalyze(data []byte, filters parser.LogFilters, interval time.Duration, filename string) string {
	t0 := time.Now()

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

	buckets, err := analysis.AggregateMetricsBySplit(ctx, analyzeIn, interval)
	if parseErr != nil {
		return `{"error": "Parse error: ` + parseErr.Error() + `"}`
	}
	if err != nil {
		return `{"error": "` + err.Error() + `"}`
	}

	info := output.HTMLReportInfo{
		Filename:    filename,
		FileSize:    int64(len(data)),
		Format:      format,
		ProcessTime: float64(time.Since(t0).Milliseconds()),
	}
	periods, err := output.SplitPeriodsJSON(buckets, info, []string{"all"})
	if err != nil {
		return `{"error": "Split export error: ` + err.Error() + `"}`
	}
	return periods
}

func main() {
	js.Global().Set("quellogParse", js.FuncOf(parseLog))
	js.Global().Set("quellogParseBytes", js.FuncOf(parseLogBytes)) // Accepts Uint8Array (faster)
	js.Global().Set("quellogSplitBytes", js.FuncOf(splitLogBytes)) // Split into period blobs
	js.Global().Set("quellogVersion", js.FuncOf(getVersion))
	select {}
}

// splitLogBytes accepts a Uint8Array and produces per-period blobs.
// Usage from JS: quellogSplitBytes(uint8Array, intervalSeconds, filename, filtersJson)
func splitLogBytes(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return `{"error": "split needs data and interval"}`
	}
	jsArray := args[0]
	if jsArray.IsNull() || jsArray.IsUndefined() {
		return `{"error": "Input is null or undefined"}`
	}
	length := jsArray.Get("length").Int()
	if length == 0 {
		return `{"error": "Empty input"}`
	}
	intervalSec := args[1].Int()
	if intervalSec <= 0 {
		return `{"error": "Invalid split interval"}`
	}
	data := make([]byte, length)
	js.CopyBytesToGo(data, jsArray)
	filename := ""
	if len(args) > 2 && !args[2].IsNull() && !args[2].IsUndefined() {
		filename = args[2].String()
	}
	return splitAndAnalyze(data, parseFiltersArg(args, 3), time.Duration(intervalSec)*time.Second, filename)
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
	return parseAndAnalyze([][]byte{[]byte(content)}, parseFiltersArg(args, 1))
}

// parseLogBytes accepts a Uint8Array directly instead of a string.
// This reduces memory usage by avoiding the JS string → Go string conversion.
// Usage from JS: quellogParseBytes(uint8Array, filtersJson)
func parseLogBytes(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return `{"error": "No input provided"}`
	}
	input := args[0]
	if input.IsNull() || input.IsUndefined() {
		return `{"error": "Input is null or undefined"}`
	}
	// One Uint8Array, or an Array of them: one per file, as the CLI's
	// arguments.
	arrays := []js.Value{input}
	if js.Global().Get("Array").Call("isArray", input).Bool() {
		arrays = arrays[:0]
		for i := 0; i < input.Length(); i++ {
			arrays = append(arrays, input.Index(i))
		}
	}
	var files [][]byte
	for _, a := range arrays {
		if n := a.Get("length").Int(); n > 0 {
			data := make([]byte, n)
			js.CopyBytesToGo(data, a)
			files = append(files, data)
		}
	}
	if len(files) == 0 {
		return `{"error": "Empty input"}`
	}
	return parseAndAnalyze(files, parseFiltersArg(args, 1))
}
