package cmd

import "testing"

// TestReportFormat: the HTML badge names the PostgreSQL input's format even
// when the run lists its pooler or Patroni log first; the first-argument
// rule, its neighbour, answers "pgbouncer" on the first case.
func TestReportFormat(t *testing.T) {
	const pooler, server = "../test/testdata/pgbouncer/P1/p1.pgbouncer.log", "../test/testdata/pgbouncer/P1/p1.postgresql.log"
	const patroni = "../test/testdata/patroni/A-p4-text-async/pg1.patroni.log"
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{pooler, server}, "stderr"},
		{[]string{server, pooler}, "stderr"},
		{[]string{patroni, server}, "stderr"},
		{[]string{pooler}, ""},
		{[]string{server}, "stderr"},
	} {
		if got := reportFormat(c.args); got != c.want {
			t.Errorf("reportFormat(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}
