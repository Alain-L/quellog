package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Alain-L/quellog/analysis"
)

// ExportPgBouncerJSON writes the pgBouncer metrics as a JSON object. The
// struct carries its own json tags, so this is a thin marshaling wrapper;
// compact toggles indentation for machine consumption.
func ExportPgBouncerJSON(w io.Writer, m analysis.PgBouncerMetrics, compact bool) {
	enc := json.NewEncoder(w)
	if !compact {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(m); err != nil {
		fmt.Fprintf(w, "[ERROR] Failed to export pgBouncer JSON: %v\n", err)
	}
}
