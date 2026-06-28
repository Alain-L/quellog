package analysis

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// runShardedUniqueEntity feeds entries through n PID-sharded
// UniqueEntityAnalyzers and merges them back into one, returning the merged
// Finalize output.
func runShardedUniqueEntity(entries []parser.LogEntry, n int) UniqueEntityMetrics {
	shards := make([]*UniqueEntityAnalyzer, n)
	for i := range shards {
		shards[i] = NewUniqueEntityAnalyzer()
	}
	for i := range entries {
		e := &entries[i]
		shards[shardForPID(e.PID, n)].Process(e)
	}
	merged := shards[0]
	for i := 1; i < n; i++ {
		merged.Merge(shards[i])
	}
	return merged.Finalize()
}

func runSingleUniqueEntity(entries []parser.LogEntry) UniqueEntityMetrics {
	a := NewUniqueEntityAnalyzer()
	for i := range entries {
		a.Process(&entries[i])
	}
	return a.Finalize()
}

// TestUniqueEntityShardMergeParity_Synthetic builds a multi-backend stream
// whose db/user/app/host values deliberately overlap AND diverge across
// PIDs, so the count maps both collide (forcing Merge to sum) and grow
// (forcing Merge to union new keys). Counts are integers, so reflect.DeepEqual
// is an exact check. The intra-PID db switch exercises the lastSeenCache
// transition both inside a shard and at the shard boundary that Merge flushes.
func TestUniqueEntityShardMergeParity_Synthetic(t *testing.T) {
	base := time.Unix(1700000000, 0).UTC()
	var entries []parser.LogEntry
	ts := 0
	add := func(pid, msg string) {
		entries = append(entries, parser.LogEntry{
			Timestamp:      base.Add(time.Duration(ts) * time.Second),
			Message:        msg,
			IsContinuation: false,
			PID:            pid,
		})
		ts++
	}

	type fields struct{ user, db, app, host string }
	plan := map[string][]fields{
		"101": {
			{"alice", "sales", "psql", "10.0.0.1"},
			{"alice", "sales", "psql", "10.0.0.1"},
			{"alice", "reports", "psql", "10.0.0.1"},
		},
		"102": {
			{"bob", "sales", "pgbench", "10.0.0.2"},
			{"bob", "sales", "pgbench", "10.0.0.2"},
		},
		"103": {
			{"alice", "sales", "dbeaver", "10.0.0.1"},
			{"carol", "reports", "psql", "10.0.0.9"},
		},
		"104": {
			{"carol", "reports", "psql", "10.0.0.9"},
			{"alice", "reports", "psql", "10.0.0.3"},
		},
		"105": {
			{"dave", "analytics", "etl", "10.0.0.4"},
		},
		"106": {
			{"bob", "sales", "pgbench", "10.0.0.2"},
			{"dave", "analytics", "etl", "10.0.0.4"},
		},
	}

	pids := []string{"101", "102", "103", "104", "105", "106"}
	maxRounds := 0
	for _, p := range pids {
		if len(plan[p]) > maxRounds {
			maxRounds = len(plan[p])
		}
	}
	for r := 0; r < maxRounds; r++ {
		for _, p := range pids {
			rows := plan[p]
			if r >= len(rows) {
				continue
			}
			f := rows[r]
			add(p, "LOG:  connection authorized: user="+f.user+" database="+f.db+
				" application_name="+f.app+" host="+f.host)
		}
	}

	const n = 4
	seen := map[int]bool{}
	for _, p := range pids {
		seen[shardForPID(p, n)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("test PIDs collapsed onto %d shard(s); pick PIDs that spread", len(seen))
	}

	single := runSingleUniqueEntity(entries)
	for _, nn := range []int{2, 3, 4, 8} {
		merged := runShardedUniqueEntity(entries, nn)
		if !reflect.DeepEqual(single, merged) {
			t.Errorf("n=%d: UniqueEntityMetrics mismatch:\n single=%+v\n shard =%+v", nn, single, merged)
		}
	}
}

// TestUniqueEntityShardMergeParity_Fixtures proves parity on real parsed
// corpora carrying db/user fields, across several shard counts. Skips
// gracefully when a fixture is absent or yields no extractable entities.
func TestUniqueEntityShardMergeParity_Fixtures(t *testing.T) {
	fixtures := []string{
		"stderr.log",
		"sql_extended.log",
		"test_summary.log",
		"repro_fatal.log",
	}
	checked := false
	for _, fx := range fixtures {
		path := filepath.Join("..", "test", "testdata", fx)
		entries := parseFixtureEntries(t, path)
		if len(entries) == 0 {
			continue
		}
		single := runSingleUniqueEntity(entries)
		if single.UniqueDbs == 0 && single.UniqueUsers == 0 &&
			single.UniqueApps == 0 && single.UniqueHosts == 0 {
			t.Logf("%s: no db/user/app/host fields, skipping", fx)
			continue
		}
		for _, n := range []int{2, 3, 4, 8} {
			merged := runShardedUniqueEntity(entries, n)
			if !reflect.DeepEqual(single, merged) {
				t.Errorf("%s n=%d: UniqueEntityMetrics mismatch:\n single=%+v\n shard =%+v",
					fx, n, single, merged)
			}
		}
		checked = true
	}
	if !checked {
		t.Skip("no fixture surfaced db/user/app/host entities; parity check vacuous")
	}
}
