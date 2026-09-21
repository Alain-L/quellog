package quellog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Three pgBouncer settings decide how much of a pooler report can exist
// at all: log_connections / log_disconnections gate the client lines,
// stats_period gates the telemetry lines, and pool_mode -- logged
// nowhere -- decides what the wait figure is averaged over. A report
// that drops a section, or publishes a rate computed from one startup
// snapshot, without saying which of the three is responsible leaves the
// reader unable to tell "nothing happened" from "your pooler does not
// log it". These tests freeze that distinction.
//
// Every probe below is built from lines copied verbatim out of real
// pgbouncer captures (03-material of the pooler audit, and the fixtures
// under testdata/regressions/pgbouncer/); only the selection is ours.

// singleStatsProbe is pgb-07-oldversion-transaction.log reduced to its
// shape: a pooler started at 13:39:27, one startup "stats:" line twenty
// seconds later reporting zeros because no client had connected yet,
// and then the real traffic -- which no later snapshot ever summarises,
// the capture ending before the next period elapsed. The traffic
// figures are therefore a measurement of the idle period, published as
// if they measured the log.
const singleStatsProbe = `2026-09-20 13:39:27.855 CEST [13942] LOG listening on 127.0.0.1:6432
2026-09-20 13:39:27.855 CEST [13942] LOG process up: PgBouncer 1.18.0, libevent 2.1.13-stable (epoll), adns: evdns2, tls: OpenSSL 3.5.7 9 Jun 2026
2026-09-20 13:39:47.855 CEST [13942] LOG stats: 0 xacts/s, 0 queries/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
2026-09-20 13:40:03.346 CEST [13942] LOG C-0x55db1ade3c10: benchdb/bench@127.0.0.1:36842 login attempt: db=benchdb user=bench tls=no
2026-09-20 13:40:03.348 CEST [13942] LOG S-0x55db1adeb3b0: benchdb/bench@127.0.0.1:5544 new connection to server (from 127.0.0.1:53670)
2026-09-20 13:40:03.458 CEST [13942] LOG C-0x55db1ade3c10: benchdb/bench@127.0.0.1:38528 closing because: client close request (age=0s)
2026-09-20 13:40:03.458 CEST [13942] LOG C-0x55db1ade3c10: benchdb/bench@127.0.0.1:38538 login attempt: db=benchdb user=bench tls=no
2026-09-20 13:40:03.459 CEST [13942] LOG C-0x55db1ade3c10: benchdb/bench@127.0.0.1:38538 closing because: client close request (age=0s)
2026-09-20 13:40:04.480 CEST [13942] LOG C-0x55db1ade3c10: pgbouncer/admin@127.0.0.1:38550 login attempt: db=pgbouncer user=admin tls=no
2026-09-20 13:40:04.481 CEST [13942] LOG SHUTDOWN command issued
`

// idleProbe is the false positive to avoid: a pooler configured exactly
// as the setup page prescribes -- connections logged, a one-second
// stats_period covering the window end to end -- that simply had no
// client. Lines copied from testdata/regressions/pgbouncer/pgbouncer.log,
// whose opening snapshots are all zeros for that same reason. Nothing
// here may be reported as a configuration gap.
const idleProbe = `2026-09-09 20:58:58.651 UTC [1] LOG listening on 0.0.0.0:6432
2026-09-09 20:58:59.655 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, 0 client parses/s, 0 server parses/s, 0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
2026-09-09 20:59:00.655 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, 0 client parses/s, 0 server parses/s, 0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
2026-09-09 20:59:01.655 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, 0 client parses/s, 0 server parses/s, 0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
2026-09-09 20:59:02.655 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, 0 client parses/s, 0 server parses/s, 0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
2026-09-09 20:59:03.655 UTC [1] LOG stats: 0 xacts/s, 0 queries/s, 0 client parses/s, 0 server parses/s, 0 binds/s, in 0 B/s, out 0 B/s, xact 0 us, query 0 us, wait 0 us
`

// writeProbe drops a probe in a temp dir and returns its path.
func writeProbe(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	return p
}

// TestPgBouncerNamesTheMissingConnectionLines freezes COR-39: with
// log_connections = 0 the Sessions and Connections sections used to
// vanish without a word, on a page that went on reporting 48 queries/s
// and 45 errors. Both sections must now exist and name the setting.
func TestPgBouncerNamesTheMissingConnectionLines(t *testing.T) {
	fixture := filepath.Join("testdata", "regressions", "pgbouncer", "pgbouncer_no_conn_log.log")

	for _, tc := range []struct {
		format string
		args   []string
		// section is how the format spells the two headings.
		sessions, connections string
	}{
		{"text", nil, "SESSIONS", "CONNECTIONS"},
		{"markdown", []string{"--md"}, "## Sessions", "## Connections"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			out := string(runHarness(t, false, append([]string{fixture}, tc.args...)...))
			out = ansiEscapeRe.ReplaceAllString(out, "")

			if !strings.Contains(out, tc.sessions) {
				t.Errorf("no Sessions section: with log_connections = 0 it disappears silently\n%s", out)
			}
			if !strings.Contains(out, tc.connections) {
				t.Errorf("no Connections section: with log_connections = 0 it disappears silently\n%s", out)
			}
			if !strings.Contains(out, "log_connections") {
				t.Errorf("the report never names log_connections, so the reader cannot tell an idle pooler from an unlogged one\n%s", out)
			}
			if !strings.Contains(out, "log_disconnections") {
				t.Errorf("the report never names log_disconnections\n%s", out)
			}
			if !strings.Contains(out, "pgbouncer-setup") {
				t.Errorf("the report does not point at the setup page\n%s", out)
			}
		})
	}
}

// TestPgBouncerSaysTrafficIsNotMissing is the other half of COR-39: the
// sentence that names log_connections must state that the pooler did
// report traffic over the same window, which is what distinguishes a
// silenced log from an idle one.
func TestPgBouncerSaysTrafficIsNotMissing(t *testing.T) {
	fixture := filepath.Join("testdata", "regressions", "pgbouncer", "pgbouncer_no_conn_log.log")
	out := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, fixture)), "")

	if !strings.Contains(out, "The pooler reported traffic over this window") {
		t.Errorf("the empty sections do not say the traffic itself was reported\n%s", out)
	}
}

// TestPgBouncerFlagsSingleSnapshotCoverage freezes COR-40: one startup
// snapshot must not be published as a measurement of the whole capture
// without saying so.
func TestPgBouncerFlagsSingleSnapshotCoverage(t *testing.T) {
	probe := writeProbe(t, "single-stats.log", singleStatsProbe)
	out := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, probe)), "")

	if !strings.Contains(out, "stats snapshot") {
		t.Errorf("the report publishes 0.0/s from a single startup snapshot without qualifying it\n%s", out)
	}
	if !strings.Contains(out, "stats_period") {
		t.Errorf("the report never names stats_period, the setting that decides the coverage\n%s", out)
	}
	if !strings.Contains(out, "pgbouncer-setup") {
		t.Errorf("the coverage note does not point at the setup page\n%s", out)
	}
}

// TestPgBouncerFlagsAbsentTelemetry covers the other end of the same
// setting: a capture shorter than one stats_period holds no "stats:"
// line at all, and the three sections fed by those lines used to vanish
// without a word.
func TestPgBouncerFlagsAbsentTelemetry(t *testing.T) {
	fixture := filepath.Join("testdata", "regressions", "pgbouncer", "pgbouncer_verbose.log")
	out := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, fixture)), "")

	if !strings.Contains(out, "REQUEST THROUGHPUT") {
		t.Errorf("the throughput section vanishes instead of saying why it is empty\n%s", out)
	}
	if !strings.Contains(out, "stats_period") {
		t.Errorf("the report never names stats_period\n%s", out)
	}
	// A short capture is not a misconfiguration; the note must not read
	// as an accusation.
	if strings.Contains(out, "The pooler reported traffic over this window") {
		t.Errorf("an absent-telemetry note borrowed the unlogged-connections wording\n%s", out)
	}
}

// TestPgBouncerDoesNotAccuseAnIdlePooler is the calibration test: a
// correctly configured pooler that simply had no client must get no
// configuration warning at all. A false positive here would be worse
// than no warning.
func TestPgBouncerDoesNotAccuseAnIdlePooler(t *testing.T) {
	probe := writeProbe(t, "idle.log", idleProbe)
	out := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, probe)), "")

	if strings.Contains(out, "The pooler reported traffic over this window") {
		t.Errorf("an idle pooler is told its connection logging is off\n%s", out)
	}
	if strings.Contains(out, "stats snapshot") && strings.Contains(out, "snapshots cover") {
		t.Errorf("an idle pooler with end-to-end telemetry is told its coverage is short\n%s", out)
	}
	// It still gets the plain placeholder, with the settings to check.
	if !strings.Contains(out, "CONNECTIONS") || !strings.Contains(out, "log_connections") {
		t.Errorf("the idle report drops the Connections section instead of showing it empty\n%s", out)
	}
}

// TestPgBouncerStatesPoolModeIsUnlogged freezes COR-41. pool_mode
// changes what the wait figure is averaged over (per session, per
// transaction, per statement) and pgBouncer logs it nowhere, so the
// report must say the denominator is not in the log rather than let the
// reader assume one.
func TestPgBouncerStatesPoolModeIsUnlogged(t *testing.T) {
	fixture := filepath.Join("testdata", "regressions", "pgbouncer", "pgbouncer.log")

	for _, args := range [][]string{nil, {"--md"}} {
		out := ansiEscapeRe.ReplaceAllString(string(runHarness(t, false, append([]string{fixture}, args...)...)), "")
		if !strings.Contains(out, "pool_mode") {
			t.Errorf("args=%v: the pool wait figure never mentions pool_mode, which sets its denominator\n%s", args, out)
		}
	}
}
