package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Alain-L/quellog/analysis"
)

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
