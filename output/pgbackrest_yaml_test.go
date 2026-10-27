package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Alain-L/quellog/analysis"
)

// TestExportYAMLWithPgBackRest pins that the YAML export carries the
// "pgbackrest" key next to the PostgreSQL report, with the cause lines
// of a failure, and leaves it out for an empty pbr; and that a
// pgBackRest-only run holds that key alone.
func TestExportYAMLWithPgBackRest(t *testing.T) {
	pbr := []analysis.PgBackRestMetrics{{
		Stanza: "ghost", LinesRead: 40,
		Backups: []analysis.PgBackRestBackup{{Type: "incr", ErrorCode: 55, ErrorLine: "unable to load info file",
			ErrorDetail: []string{"HINT: has a stanza-create been performed?"}}},
		ExpireRuns: []analysis.PgBackRestExpireRun{{Repo: "all", Succeeded: true}},
	}}

	var with bytes.Buffer
	ExportYAMLWithPgBackRest(&with, analysis.AggregatedMetrics{}, pbr, []string{"all"}, false)
	out := with.String()
	for _, want := range []string{"pgbackrest:", "stanza: ghost", "lines_read: 40", "error_code: 55",
		"error_detail:", "has a stanza-create been performed?", "repo: all", "summary:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	var without bytes.Buffer
	ExportYAMLWithPgBackRest(&without, analysis.AggregatedMetrics{}, nil, []string{"all"}, false)
	if strings.Contains(without.String(), "pgbackrest") {
		t.Errorf("empty pbr: pgbackrest key present:\n%s", without.String())
	}

	var only bytes.Buffer
	ExportPgBackRestOnlyYAML(&only, pbr)
	if !strings.HasPrefix(only.String(), "pgbackrest:") || strings.Contains(only.String(), "summary:") {
		t.Errorf("pgBackRest-only YAML should hold the pgbackrest key alone:\n%s", only.String())
	}
}
