//go:build js && wasm

// Package main provides the WASM entry point for quellog.
// It exposes the parser, analysis, and output packages to JavaScript.
package main

import (
	"strconv"
	"syscall/js"

	"github.com/Alain-L/quellog/analysis"
	"github.com/Alain-L/quellog/output"
	"github.com/Alain-L/quellog/parser"
)

const version = "0.2.0-wasm"

var perf = js.Global().Get("performance")

func now() float64 {
	return perf.Call("now").Float()
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
	for i := range entries {
		entry := &entries[i]
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
