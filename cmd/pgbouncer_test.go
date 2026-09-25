package cmd

import "testing"

// TestReportFormat: the HTML badge names the PostgreSQL input's format even
// when the run lists its pooler log first; the first-argument rule, its
// neighbour, answers "pgbouncer" on the first case.
func TestReportFormat(t *testing.T) {
	const pooler, server = "../test/testdata/pgbouncer/P1/p1.pgbouncer.log", "../test/testdata/pgbouncer/P1/p1.postgresql.log"
	for _, c := range []struct {
		args       []string
		withPooler bool
		want       string
	}{
		{[]string{pooler, server}, true, "stderr"},
		{[]string{server, pooler}, true, "stderr"},
		{[]string{pooler}, true, ""},
		{[]string{server}, false, "stderr"},
	} {
		if got := reportFormat(c.args, c.withPooler); got != c.want {
			t.Errorf("reportFormat(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}
