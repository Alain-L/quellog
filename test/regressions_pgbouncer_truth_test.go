package quellog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rotationProbe is what a log rotation leaves behind: three orphaned
// tails of pgBouncer records (the timestamp/PID/severity prefix was cut
// away with the previous file) followed by three intact records. Every
// line is copied verbatim from testdata/regressions/pgbouncer/pgbouncer.log,
// the three first ones minus their prefix. Six lines in, three records
// out: the report must say so instead of publishing "parsed == parsed".
const rotationProbe = `app/app_user@10.89.0.4:35088 login attempt: db=app user=app_user tls=no replication=no
0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
3ad2f3c0: app/app_user@10.89.0.4:35088 closing because: server login failed: wrong password type (age=0s)
2026-09-09 20:58:59.655 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, 0 client parses/s, 0 server parses/s, 0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
2026-09-09 20:59:13.571 UTC [1] LOG C-0x7f3b3ad2f3c0: app/app_user@10.89.0.4:35088 login attempt: db=app user=app_user tls=no replication=no
2026-09-09 20:59:13.575 UTC [1] LOG C-0x7f3b3ad2f3c0: app/app_user@10.89.0.4:35088 closing because: server login failed: wrong password type (age=0s)
`

// TestPgBouncerReportsWhatItDropped freezes the reporting of lost input:
// the report must state the lines it read and the lines it could not
// decode, not only the records it understood.
func TestPgBouncerReportsWhatItDropped(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "rotated.log")
	if err := os.WriteFile(probe, []byte(rotationProbe), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	got := firstPooler(t, runHarness(t, false, probe, "--json"))
	for _, c := range []struct {
		key  string
		want float64
	}{
		{"lines_read", 6},
		{"lines", 3},
		{"lines_unrecognized", 3},
	} {
		v, ok := got[c.key].(float64)
		if !ok {
			t.Errorf("JSON key %q missing (got %v)", c.key, got[c.key])
			continue
		}
		if v != c.want {
			t.Errorf("%s = %v, want %v", c.key, v, c.want)
		}
	}

	text := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, probe)), "")
	for _, want := range []string{
		"Log lines read            : 6",
		"Log entries               : 3",
		"Unrecognized lines        : 3",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text report missing %q:\n%s", want, text)
		}
	}
}

// TestPgBouncerVentilatesDebugNoise freezes the signal/noise split of
// the headline record count. pgbouncer_verbose.log is a verbose=2
// capture: 1686 records, of which 1673 are DEBUG or NOISE.
func TestPgBouncerVentilatesDebugNoise(t *testing.T) {
	const fixture = "testdata/regressions/pgbouncer/pgbouncer_verbose.log"

	got := firstPooler(t, runHarness(t, false, fixture, "--json"))
	if v, ok := got["debug_lines"].(float64); !ok || v != 1673 {
		t.Errorf("debug_lines = %v, want 1673", got["debug_lines"])
	}
	if v, ok := got["lines"].(float64); !ok || v != 1686 {
		t.Errorf("lines = %v, want 1686", got["lines"])
	}

	text := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, fixture)), "")
	if !strings.Contains(text, "Debug/noise entries       : 1673") {
		t.Errorf("text report does not break the record count down by noise:\n%s", text)
	}
}

// TestPgBouncerTimingLabelsSaySnapshot freezes the timing labels: the
// column next to the averages is the highest snapshot average, never a
// peak operation, and the averages skip the snapshots that timed none —
// which is why they can rest on fewer snapshots than the summary counts.
func TestPgBouncerTimingLabelsSaySnapshot(t *testing.T) {
	const fixture = "testdata/regressions/pgbouncer/pgbouncer_stats_period.log"

	text := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, fixture)), "")
	for _, want := range []string{
		"(max snapshot avg ",
		"skip the snapshots that timed none",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text report missing %q:\n%s", want, text)
		}
	}

	md := string(runHarness(t, false, fixture, "--md"))
	if !strings.Contains(md, "| Metric | Weighted average | Max snapshot average |") {
		t.Errorf("Markdown query-timing table still labels the max column \"Peak\":\n%s", md)
	}
	if !strings.Contains(md, "skip the snapshots that timed none") {
		t.Errorf("Markdown report does not explain the timing denominator:\n%s", md)
	}
}

// firstPooler decodes a --json document and returns its first pooler.
func firstPooler(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	var got struct {
		PgBouncer []map[string]any `json:"pgbouncer"`
	}
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.PgBouncer) == 0 {
		t.Fatalf("no pgbouncer entry in:\n%s", doc)
	}
	return got.PgBouncer[0]
}
