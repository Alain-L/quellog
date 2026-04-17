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
	data := buildJSONData(m, sections, full)
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintf(w, "# ERROR: Failed to marshal data: %v\n", err)
		return
	}
	jsonToYAML(w, jsonBytes)
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
