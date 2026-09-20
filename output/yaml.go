package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Alain-L/quellog/analysis"
	"gopkg.in/yaml.v3"
)

// jsonToYAML converts JSON-encoded data to YAML, preserving json struct tag keys.
func jsonToYAML(w io.Writer, jsonBytes []byte) {
	var data interface{}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		fmt.Fprintf(w, "# ERROR: Failed to parse data: %v\n", err)
		return
	}

	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(data); err != nil {
		fmt.Fprintf(w, "# ERROR: Failed to export YAML: %v\n", err)
	}
	enc.Close()
}

// ExportYAML writes the full analysis results as YAML.
func ExportYAML(w io.Writer, m analysis.AggregatedMetrics, sections []string, full bool) {
	ExportYAMLCombined(w, m, nil, sections, full, false)
}

// ExportYAMLCombined writes the YAML report for the same three shapes
// ExportJSONCombined produces — PostgreSQL only, PostgreSQL with a
// "pgbouncer" key, and the flat pooler document (pgbOnly) — reusing it as
// the source document.
func ExportYAMLCombined(w io.Writer, m analysis.AggregatedMetrics, pgb *analysis.PgBouncerMetrics, sections []string, full, pgbOnly bool) {
	var buf bytes.Buffer
	ExportJSONCombined(&buf, m, pgb, sections, full, true, pgbOnly)
	jsonToYAML(w, buf.Bytes())
}

// ExportSQLPerformanceYAML writes SQL performance metrics as YAML.
func ExportSQLPerformanceYAML(w io.Writer, m analysis.SQLMetrics) {
	var buf bytes.Buffer
	ExportSQLPerformanceJSON(&buf, m)
	jsonToYAML(w, buf.Bytes())
}

// ExportSQLOverviewYAML writes SQL overview metrics as YAML.
func ExportSQLOverviewYAML(w io.Writer, m analysis.SQLMetrics) {
	var buf bytes.Buffer
	ExportSQLOverviewJSON(&buf, m)
	jsonToYAML(w, buf.Bytes())
}

// ExportSQLDetailYAML writes SQL detail for specific query IDs as YAML.
func ExportSQLDetailYAML(w io.Writer, m analysis.AggregatedMetrics, queryIDs []string) {
	var buf bytes.Buffer
	ExportSQLDetailJSON(&buf, m, queryIDs)
	jsonToYAML(w, buf.Bytes())
}
