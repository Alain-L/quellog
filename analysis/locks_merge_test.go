package analysis

import (
	"bufio"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// lockRunSingle processes every entry through one analyzer.
func lockRunSingle(entries []parser.LogEntry) LockMetrics {
	a := NewLockAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// lockRunSharded shards entries by PID across n analyzers, processes each
// shard, folds shards 1..n-1 into shard 0 via Merge (repeated pairwise), then
// finalizes shard 0. Returns the metrics and the number of shards that
// actually received entries.
func lockRunSharded(entries []parser.LogEntry, n int) (LockMetrics, int) {
	shards := make([]*LockAnalyzer, n)
	for i := range shards {
		shards[i] = NewLockAnalyzer()
	}
	seen := make(map[int]struct{})
	for i := range entries {
		s := shardForPID(entries[i].PID, n)
		seen[s] = struct{}{}
		shards[s].Process(&entries[i])
	}
	for i := 1; i < n; i++ {
		shards[0].Merge(shards[i])
	}
	return shards[0].Finalize(), len(seen)
}

// lockAssertMetricsEqual compares two LockMetrics for parity. TotalWaitTime and
// the per-query wait-time sums are accumulated floats summed in a different
// order by the sharded path, so they are compared with almostEqual and then
// zeroed before the structural reflect.DeepEqual covers the remaining
// (integer / bit-exact / string) fields.
func lockAssertMetricsEqual(t *testing.T, label string, want, got LockMetrics) {
	t.Helper()

	if !almostEqual(want.TotalWaitTime, got.TotalWaitTime) {
		t.Errorf("%s: TotalWaitTime: want=%v got=%v", label, want.TotalWaitTime, got.TotalWaitTime)
	}
	want.TotalWaitTime, got.TotalWaitTime = 0, 0

	// Per-occurrence event WaitTime is carried verbatim from Process (not
	// re-summed), so it is bit-exact; only the per-query aggregate sums need
	// almostEqual treatment.
	lockNormalizeQueryStats(t, label, want.QueryStats, got.QueryStats)

	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s: metrics mismatch\n single  = %+v\n sharded = %+v", label, want, got)
	}
}

// lockNormalizeQueryStats checks the accumulated-float wait-time fields inside
// the per-query stat maps with almostEqual, then zeroes them in place so the
// caller's reflect.DeepEqual can compare the rest exactly.
func lockNormalizeQueryStats(t *testing.T, label string, want, got map[string]*LockQueryStat) {
	t.Helper()
	for k, av := range want {
		bv, ok := got[k]
		if !ok {
			continue // structural compare will report the missing key
		}
		if !almostEqual(av.AcquiredWaitTime, bv.AcquiredWaitTime) {
			t.Errorf("%s: query %q AcquiredWaitTime: %v vs %v", label, k, av.AcquiredWaitTime, bv.AcquiredWaitTime)
		}
		if !almostEqual(av.StillWaitingTime, bv.StillWaitingTime) {
			t.Errorf("%s: query %q StillWaitingTime: %v vs %v", label, k, av.StillWaitingTime, bv.StillWaitingTime)
		}
		if !almostEqual(av.TotalWaitTime, bv.TotalWaitTime) {
			t.Errorf("%s: query %q TotalWaitTime: %v vs %v", label, k, av.TotalWaitTime, bv.TotalWaitTime)
		}
		av.AcquiredWaitTime, bv.AcquiredWaitTime = 0, 0
		av.StillWaitingTime, bv.StillWaitingTime = 0, 0
		av.TotalWaitTime, bv.TotalWaitTime = 0, 0
	}
}

// TestLockShardMergeParity_Synthetic exercises the hard cases: many backends
// (PIDs spreading across shards), several complete still-waiting -> acquired
// pairs on distinct PIDs, a deadlock, a cross-PID blocking edge (DETAIL names a
// backend that may live on another shard), and some waits still pending at
// end-of-stream. Timestamps are strictly ascending so the timeline order is
// unambiguous under merge-sort.
func TestLockShardMergeParity_Synthetic(t *testing.T) {
	base := time.Date(2025, 11, 30, 21, 10, 0, 0, time.UTC)
	type line struct {
		pid string // backend PID emitting the line (prefix [pid])
		msg string // message body (no prefix)
		// cont marks DETAIL/CONTEXT/STATEMENT continuation lines.
		cont bool
	}

	// Backends 100,200,300,400,500,600 are the "waiting" backends; their
	// blocking counterparts (101,201,...) hold the lock and run a query, so the
	// blocking-query back-fill is exercised across shards.
	lines := []line{
		// --- backend 101 holds a lock, runs a query (its query is cached). ---
		{"101", `LOG:  duration: 1.0 ms  statement: UPDATE users SET name = 'a' WHERE id = 1`, false},

		// --- backend 100 waits on relation, then acquires. Blocked by 101. ---
		{"100", `LOG:  process 100 still waiting for ShareLock on relation 123 of database 5 after 100.5 ms`, false},
		{"100", `DETAIL:  Process holding the lock: 101. Wait queue: 100.`, true},
		{"100", `STATEMENT:  SELECT * FROM users WHERE id = 1`, true},
		{"100", `LOG:  process 100 acquired ShareLock on relation 123 of database 5 after 250.25 ms`, false},

		// --- backend 201 holds a lock, runs a query. ---
		{"201", `LOG:  duration: 2.0 ms  statement: DELETE FROM orders WHERE id = 9`, false},

		// --- backend 200 waits then acquires. Blocked by 201. ---
		{"200", `LOG:  process 200 still waiting for ExclusiveLock on transaction 789 after 50.0 ms`, false},
		{"200", `DETAIL:  Process holding the lock: 201. Wait queue: 200.`, true},
		{"200", `STATEMENT:  SELECT * FROM orders WHERE id = 9`, true},
		{"200", `LOG:  process 200 acquired ExclusiveLock on transaction 789 after 75.0 ms`, false},

		// --- backend 300 waits then acquires (no blocking detail). ---
		{"300", `LOG:  process 300 still waiting for AccessExclusiveLock on relation 456 of database 5 after 10.0 ms`, false},
		{"300", `STATEMENT:  ALTER TABLE events ADD COLUMN x int`, true},
		{"300", `LOG:  process 300 acquired AccessExclusiveLock on relation 456 of database 5 after 20.0 ms`, false},

		// --- backend 400: a deadlock. The waiting event is promoted. ---
		{"400", `LOG:  process 400 still waiting for ShareLock on transaction 999 after 5000.0 ms`, false},
		{"400", `STATEMENT:  UPDATE accounts SET bal = bal - 1 WHERE id = 7`, true},
		{"400", `ERROR:  deadlock detected`, false},

		// --- backend 500: still waiting at end of stream (never acquired). ---
		{"500", `LOG:  process 500 still waiting for ShareLock on relation 123 of database 5 after 333.0 ms`, false},
		{"500", `STATEMENT:  SELECT * FROM users WHERE id = 1`, true},

		// --- backend 600: fast acquisition with no prior "still waiting". ---
		{"600", `LOG:  process 600 acquired AccessShareLock on relation 789 of database 5 after 1.5 ms`, false},
		{"600", `STATEMENT:  SELECT count(*) FROM big`, true},
	}

	entries := make([]parser.LogEntry, 0, len(lines))
	for i, l := range lines {
		full := "[" + l.pid + "] " + l.msg
		ts := base.Add(time.Duration(i) * time.Second)
		entries = append(entries, parser.NewLogEntry(ts, full, l.cont))
	}
	for i := range entries {
		if entries[i].PID == "" {
			t.Fatalf("entry %d: PID not extracted from %q", i, entries[i].Message)
		}
	}

	// Sanity: the waiting backends must spread across >=2 shards, otherwise the
	// cross-shard merge is not actually exercised.
	const probe = 4
	waitPIDs := []string{"100", "200", "300", "400", "500", "600"}
	seen := map[int]bool{}
	for _, pid := range waitPIDs {
		seen[shardForPID(pid, probe)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("test PIDs collapsed onto %d shard(s); pick PIDs that spread", len(seen))
	}

	single := lockRunSingle(entries)

	// The fixture must produce non-degenerate metrics.
	if single.TotalEvents == 0 || single.AcquiredEvents == 0 || single.DeadlockEvents == 0 {
		t.Fatalf("synthetic fixture produced degenerate metrics: %+v", single)
	}
	if single.WaitingEvents == 0 {
		t.Fatalf("expected at least one still-waiting event, got: %+v", single)
	}

	for _, n := range []int{2, 3, 4} {
		// Recompute single per iteration: the parity comparison zeroes
		// accumulated-float fields in place, which would corrupt a shared
		// single across iterations.
		singleN := lockRunSingle(entries)
		sharded, spread := lockRunSharded(entries, n)
		if n >= 2 && spread < 2 {
			t.Fatalf("n=%d: PIDs did not spread across >=2 shards (spread=%d)", n, spread)
		}
		lockAssertMetricsEqual(t, "synthetic n="+itoa(n), singleN, sharded)
	}
}

// TestLockShardMergeParity_Fixtures proves parity on any real corpus that
// carries lock lines, across several shard counts. Skips gracefully if none.
//
// Precondition. PID-sharding only reproduces the single pass when entry.PID is
// the BACKEND PID, so a backend's "still waiting" and matching "acquired" lines
// land on the same shard (their lockKey then dedupes in-shard before Merge).
// Some syslog fixtures violate this: their prefix leads with the syslog message
// sequence marker (e.g. "[10-1]", "[11-1]") rather than the postgres "[1352]"
// backend bracket, so the parser's ExtractPID returns the per-line counter, not
// the backend. Those fixtures cannot be PID-sharded by ANY pairing analyzer and
// are detected + skipped by lockFixtureHasStablePID below — this is a parser
// PID-extraction limitation, not a Merge defect (stderr.log and syslog_bsd.log,
// whose prefixes lead with the real backend bracket, exercise the cross-shard
// merge and pass).
func TestLockShardMergeParity_Fixtures(t *testing.T) {
	candidates := []string{
		"../test/testdata/stderr.log",
		"../test/testdata/test_summary.log",
		"../test/testdata/syslog.log",
		"../test/testdata/syslog_bsd.log",
		"../test/testdata/syslog_rfc5424.log",
		"../test/testdata/sql_extended.log",
	}

	ranAny := false
	for _, path := range candidates {
		if !fixtureHasLocks(path) {
			continue
		}
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			continue
		}
		single := lockRunSingle(entries)
		if single.TotalEvents == 0 {
			continue
		}
		if !lockFixtureHasStablePID(entries) {
			t.Logf("%s: entry.PID is not the backend PID (syslog sequence marker); "+
				"PID-sharding precondition not met, skipping", path)
			continue
		}
		ranAny = true
		for _, n := range []int{2, 4, 8} {
			singleN := lockRunSingle(entries)
			sharded, _ := lockRunSharded(entries, n)
			lockAssertMetricsEqual(t, path+" n="+itoa(n), singleN, sharded)
		}
	}

	if !ranAny {
		t.Skip("no fixture with lock metrics carrying a backend PID found")
	}
}

// lockFixtureHasStablePID reports whether, on every lock-event line, the
// entry.PID extracted by the parser equals the backend PID named in the message
// ("process <pid> ..."). When they differ (syslog sequence-marker prefixes),
// the PID-sharding precondition is violated and the fixture must be skipped.
func lockFixtureHasStablePID(entries []parser.LogEntry) bool {
	for i := range entries {
		msg := entries[i].Message
		isWaiting := strings.Contains(msg, lockStillWaiting)
		if !isWaiting && !strings.Contains(msg, lockAcquired) {
			continue
		}
		backendPID, _, _, _, _, ok := parseLockEvent(msg, isWaiting)
		if !ok {
			continue
		}
		if entries[i].PID != backendPID {
			return false
		}
	}
	return true
}

// fixtureHasLocks reports whether the file exists and contains any lock marker,
// without fully parsing it.
func fixtureHasLocks(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, lockStillWaiting) ||
			strings.Contains(line, lockAcquired) ||
			strings.Contains(line, lockDeadlock) {
			return true
		}
	}
	return false
}
