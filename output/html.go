package output

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Alain-L/quellog/analysis"
	"github.com/klauspost/compress/zstd"
)

//go:embed report_template.html
var reportTemplate string

// HTMLReportInfo contains metadata about the report generation.
type HTMLReportInfo struct {
	Filename    string
	FileSize    int64
	ProcessTime float64 // in milliseconds
	Format      string  // detected log format (csv, json, stderr)
}

// ExportHTML exports metrics as a standalone HTML report with embedded data.
// The HTML file includes all necessary CSS, JavaScript, and the data itself,
// making it fully self-contained and openable in any modern browser.
// The JSON data is zstd-compressed and base64-encoded to reduce file size.
func ExportHTML(w io.Writer, metrics analysis.AggregatedMetrics, info HTMLReportInfo) error {
	// Build full JSON data structure (same as JSON export with all sections)
	sections := []string{"all"}
	data := buildJSONData(metrics, sections, true)

	// Add meta section required by the web UI
	format := info.Format
	if format == "" {
		format = "stderr" // default
	}
	data["meta"] = map[string]interface{}{
		"format":        format,
		"entries":       metrics.Global.Count,
		"filename":      info.Filename,
		"filesize":      info.FileSize,
		"parse_time_ms": info.ProcessTime,
	}

	// Marshal to JSON
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal metrics to JSON: %w", err)
	}

	// Compress JSON with zstd (level 19 for best compression)
	var zstdBuf bytes.Buffer
	zstdWriter, err := zstd.NewWriter(&zstdBuf, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return fmt.Errorf("failed to create zstd writer: %w", err)
	}
	if _, err := zstdWriter.Write(jsonBytes); err != nil {
		return fmt.Errorf("failed to compress JSON: %w", err)
	}
	if err := zstdWriter.Close(); err != nil {
		return fmt.Errorf("failed to close zstd writer: %w", err)
	}

	// Encode to base64
	compressed := base64.StdEncoding.EncodeToString(zstdBuf.Bytes())

	// Inject compressed data into template
	html := strings.Replace(reportTemplate, "{{REPORT_JSON_DATA}}", compressed, 1)

	// Write to output
	if _, err := w.Write([]byte(html)); err != nil {
		return fmt.Errorf("failed to write HTML: %w", err)
	}

	return nil
}
