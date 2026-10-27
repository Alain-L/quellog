package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Alain-L/quellog/analysis"
)

// ExportYAMLWithPgBackRest writes the full analysis results as YAML,
// adding, when pbr is non-empty, a "pgbackrest" key — derived from the
// same JSON document as ExportJSONWithPgBackRest, like the rest of this
// package's YAML. With an empty pbr it exports exactly the PostgreSQL
// report.
func ExportYAMLWithPgBackRest(w io.Writer, m analysis.AggregatedMetrics, pbr []analysis.PgBackRestMetrics, sections []string, full bool) {
	data := buildJSONData(m, sections, full)
	if len(pbr) > 0 {
		data["pgbackrest"] = pgBackRestJSONList(pbr)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintf(w, "# ERROR: Failed to marshal data: %v\n", err)
		return
	}
	jsonToYAML(w, jsonBytes)
}

// ExportPgBackRestOnlyYAML writes a document holding only the
// "pgbackrest" key, for a run with no PostgreSQL files at all.
func ExportPgBackRestOnlyYAML(w io.Writer, pbr []analysis.PgBackRestMetrics) {
	jsonBytes, err := json.Marshal(map[string]interface{}{"pgbackrest": pgBackRestJSONList(pbr)})
	if err != nil {
		fmt.Fprintf(w, "# ERROR: Failed to marshal data: %v\n", err)
		return
	}
	jsonToYAML(w, jsonBytes)
}
