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

// vacuumRunSingle processes every entry through one analyzer.
func vacuumRunSingle(entries []parser.LogEntry) VacuumMetrics {
	a := NewVacuumAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// vacuumRunSharded shards entries by PID across n analyzers, processes each
// shard, folds shards 1..n-1 into shard 0 via Merge (repeated pairwise),
// then finalizes shard 0. Returns the metrics and the number of shards that
// actually received entries.
func vacuumRunSharded(entries []parser.LogEntry, n int) (VacuumMetrics, int) {
	shards := make([]*VacuumAnalyzer, n)
	for i := range shards {
		shards[i] = NewVacuumAnalyzer()
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

// vacuumAssertMetricsEqual compares two VacuumMetrics for parity. Per-table
// elapsed sums and the global elapsed totals are accumulated floats; they are
// summed in a different order by the sharded path, so they are compared with
// almostEqual and then zeroed before the structural reflect.DeepEqual covers
// the remaining (integer / bit-exact) fields.
func vacuumAssertMetricsEqual(t *testing.T, label string, want, got VacuumMetrics) {
	t.Helper()
	if !almostEqual(want.TotalVacuumElapsedSeconds, got.TotalVacuumElapsedSeconds) {
		t.Errorf("%s: TotalVacuumElapsedSeconds: want=%v got=%v", label, want.TotalVacuumElapsedSeconds, got.TotalVacuumElapsedSeconds)
	}
	if !almostEqual(want.TotalAnalyzeElapsedSeconds, got.TotalAnalyzeElapsedSeconds) {
		t.Errorf("%s: TotalAnalyzeElapsedSeconds: want=%v got=%v", label, want.TotalAnalyzeElapsedSeconds, got.TotalAnalyzeElapsedSeconds)
	}
	want.TotalVacuumElapsedSeconds, got.TotalVacuumElapsedSeconds = 0, 0
	want.TotalAnalyzeElapsedSeconds, got.TotalAnalyzeElapsedSeconds = 0, 0
	// Per-table elapsed floats live in the *VacuumTableStat maps and the
	// derived top-N slices; normalize them with almostEqual then zero so the
	// structural compare stays exact.
	vacuumNormalizeFloatStats(t, label, want, got)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s: metrics mismatch\n single  = %+v\n sharded = %+v", label, want, got)
	}
}

// vacuumNormalizeFloatStats checks the accumulated-float elapsed fields inside
// the per-table stat maps and derived slices with almostEqual, then zeroes
// them in place so the caller's reflect.DeepEqual can compare the rest exactly.
func vacuumNormalizeFloatStats(t *testing.T, label string, want, got VacuumMetrics) {
	t.Helper()
	normMap := func(a, b map[string]*VacuumTableStat) {
		for k, av := range a {
			if bv, ok := b[k]; ok {
				if !almostEqual(av.TotalElapsedSeconds, bv.TotalElapsedSeconds) {
					t.Errorf("%s: table %q TotalElapsedSeconds: %v vs %v", label, k, av.TotalElapsedSeconds, bv.TotalElapsedSeconds)
				}
				if !almostEqual(av.MaxElapsedSeconds, bv.MaxElapsedSeconds) {
					t.Errorf("%s: table %q MaxElapsedSeconds: %v vs %v", label, k, av.MaxElapsedSeconds, bv.MaxElapsedSeconds)
				}
				av.TotalElapsedSeconds, bv.TotalElapsedSeconds = 0, 0
				av.MaxElapsedSeconds, bv.MaxElapsedSeconds = 0, 0
			}
		}
	}
	normMap(want.VacuumTableStats, got.VacuumTableStats)
	normSlice := func(a, b []VacuumTableStat) {
		for i := range a {
			if i < len(b) {
				a[i].TotalElapsedSeconds, b[i].TotalElapsedSeconds = 0, 0
				a[i].MaxElapsedSeconds, b[i].MaxElapsedSeconds = 0, 0
			}
		}
	}
	normSlice(want.TopVacuumTables, got.TopVacuumTables)
	normSlice(want.XminBlockedTables, got.XminBlockedTables)
	normSlice(want.TopAnalyzeTablesByElapsed, got.TopAnalyzeTablesByElapsed)
	if want.SlowestVacuum != nil && got.SlowestVacuum != nil {
		if !almostEqual(want.SlowestVacuum.ElapsedSeconds, got.SlowestVacuum.ElapsedSeconds) {
			t.Errorf("%s: SlowestVacuum.ElapsedSeconds: %v vs %v", label, want.SlowestVacuum.ElapsedSeconds, got.SlowestVacuum.ElapsedSeconds)
		}
		want.SlowestVacuum.ElapsedSeconds, got.SlowestVacuum.ElapsedSeconds = 0, 0
	}
}

func TestVacuumShardMergeParity_Synthetic(t *testing.T) {
	base := time.Date(2025, 11, 30, 21, 10, 0, 0, time.UTC)
	type line struct {
		pid string
		msg string
	}
	lines := []line{
		{"100", `LOG:  automatic vacuum of table "app.public.users": index scans: 1, pages: 10 removed, 90 remain`},
		{"200", `LOG:  automatic analyze of table "app.public.orders" system usage: CPU: 0.01s`},
		{"100", `LOG:  automatic vacuum of table "app.public.users": index scans: 1, pages: 5 removed, 95 remain`},
		{"300", `LOG:  automatic aggressive vacuum of table "app.public.orders": index scans: 2, pages: 20 removed, 80 remain`},
		{"200", `LOG:  automatic vacuum of table "app.public.users": index scans: 0, pages: 3 removed, 97 remain`},
		{"400", `LOG:  automatic analyze of table "app.public.events" system usage: CPU: 0.02s`},
		{"300", `LOG:  automatic vacuum of table "app.public.events": index scans: 1, pages: 7 removed, 93 remain`},
		{"100", `LOG:  automatic analyze of table "app.public.users" system usage: CPU: 0.03s`},
		{"200", `LOG:  automatic vacuum of table "app.public.audit": index scans: 0`},
		{"400", `LOG:  automatic aggressive vacuum of table "app.public.users": index scans: 3, pages: 12 removed, 88 remain`},
	}

	entries := make([]parser.LogEntry, 0, len(lines))
	for i, l := range lines {
		full := "[" + l.pid + "] " + l.msg
		ts := base.Add(time.Duration(i) * time.Second)
		entries = append(entries, parser.NewLogEntry(ts, full, false))
	}

	for i := range entries {
		if entries[i].PID == "" {
			t.Fatalf("entry %d: PID not extracted from %q", i, entries[i].Message)
		}
	}

	single := vacuumRunSingle(entries)

	for _, n := range []int{2, 3, 4} {
		// Recompute single per iteration: the parity comparison normalizes
		// (zeroes) accumulated-float fields in place, which would otherwise
		// corrupt a shared single across iterations.
		singleN := vacuumRunSingle(entries)
		sharded, spread := vacuumRunSharded(entries, n)
		if n >= 2 && spread < 2 {
			t.Fatalf("n=%d: PIDs did not spread across >=2 shards (spread=%d)", n, spread)
		}
		vacuumAssertMetricsEqual(t, "synthetic n="+itoa(n), singleN, sharded)
	}

	if single.VacuumCount == 0 || single.AnalyzeCount == 0 || single.AggressiveVacuumCount == 0 {
		t.Fatalf("synthetic fixture produced degenerate metrics: %+v", single)
	}
	if got := single.VacuumSpaceRecovered["app.public.users"]; got != (10+5+3+12)*pageSize {
		t.Errorf("space recovered for app.public.users = %d, want %d", got, (10+5+3+12)*pageSize)
	}
	if got := single.VacuumTableCounts["app.public.users"]; got == 0 {
		t.Errorf("vacuum table count for app.public.users should be > 0")
	}
}

func TestVacuumShardMergeParity_Fixtures(t *testing.T) {
	candidates := []string{
		"../test/testdata/stderr.log",
		"../test/testdata/syslog.log",
		"../test/testdata/syslog_bsd.log",
		"../test/testdata/syslog_rfc5424.log",
	}

	ranAny := false
	for _, path := range candidates {
		if !fixtureHasVacuum(path) {
			continue
		}
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			continue
		}

		single := vacuumRunSingle(entries)
		if single.VacuumCount == 0 && single.AnalyzeCount == 0 {
			continue
		}
		ranAny = true

		for _, n := range []int{2, 4, 8} {
			singleN := vacuumRunSingle(entries)
			sharded, _ := vacuumRunSharded(entries, n)
			vacuumAssertMetricsEqual(t, path+" n="+itoa(n), singleN, sharded)
		}
	}

	if !ranAny {
		t.Skip("no fixture with vacuum/analyze metrics found")
	}
}

// fixtureHasVacuum reports whether the file exists and contains any
// vacuum/analyze marker, without fully parsing it.
func fixtureHasVacuum(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, autoVacuumMarker) ||
			strings.Contains(line, autoAnalyzeMarker) ||
			strings.Contains(line, autoAggressiveVacuumMarker) {
			return true
		}
	}
	return false
}

// itoa is a tiny local int formatter for test labels.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
