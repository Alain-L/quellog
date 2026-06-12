package analysis

import (
	"fmt"
	"testing"
	"time"
)

// TestCompactExecutions_DimensionRoundTrip exercises the basic
// dictionary path: append a handful of events under a few distinct
// (db/user/app/host) combos and verify that ForEach reads back the
// exact strings.
func TestCompactExecutions_DimensionRoundTrip(t *testing.T) {
	ce := newCompactExecutions(16)
	ts := time.Unix(1700000000, 0).UTC()

	type row struct{ id, db, user, app, host string }
	rows := []row{
		{"se-aaa", "appdb", "alice", "webapp", "10.0.0.1"},
		{"se-aaa", "appdb", "alice", "webapp", "10.0.0.1"},
		{"in-bbb", "writes", "bob", "worker", "10.0.0.2"},
		{"se-aaa", "appdb", "alice", "webapp", "10.0.0.1"},
		{"se-aaa", "", "alice", "", "10.0.0.1"}, // partially empty
	}
	for i, r := range rows {
		ce.append(ts.Add(time.Duration(i)*time.Second), float64(i+1), r.id, r.db, r.user, r.app, r.host)
	}

	if got := ce.Len(); got != len(rows) {
		t.Fatalf("Len = %d, want %d", got, len(rows))
	}

	got := make([]row, 0, ce.Len())
	ce.ForEach(func(e QueryExecution) bool {
		got = append(got, row{e.QueryID, e.Database, e.User, e.App, e.Host})
		return true
	})
	for i, want := range rows {
		if got[i] != want {
			t.Errorf("row %d: got %+v, want %+v", i, got[i], want)
		}
	}
}

// TestCompactExecutions_DimensionWidening forces a dictionary past
// 255 entries to trigger the uint8 → uint16 promotion and verifies
// that all previously stored values still read back correctly.
func TestCompactExecutions_DimensionWidening(t *testing.T) {
	ce := newCompactExecutions(1024)
	ts := time.Unix(1700000000, 0).UTC()

	// Append 300 events with distinct database names so the db
	// dictionary crosses the 255 → 256 → 257 boundary. Other
	// dimensions stay at low cardinality.
	for i := 0; i < 300; i++ {
		db := fmt.Sprintf("db%03d", i)
		ce.append(ts.Add(time.Duration(i)*time.Millisecond), 1.0, "qid", db, "user", "app", "host")
	}

	if !ce.dbWide {
		t.Fatalf("expected db dictionary to be widened to uint16 after 300 entries")
	}
	if ce.userWide || ce.appWide || ce.hostWide {
		t.Errorf("low-cardinality dimensions should NOT have been widened: user=%v app=%v host=%v",
			ce.userWide, ce.appWide, ce.hostWide)
	}

	// Spot-check: the first event should read back its original db
	// (db000) even after the slice was reallocated.
	first := ce.At(0)
	if first.Database != "db000" {
		t.Errorf("ce.At(0).Database = %q, want %q (pre-promotion event lost)", first.Database, "db000")
	}
	last := ce.At(299)
	if last.Database != "db299" {
		t.Errorf("ce.At(299).Database = %q, want %q", last.Database, "db299")
	}
	// All 300 db names should be present in the dictionary (1 +
	// "" + 300).
	if len(ce.databases) != 301 {
		t.Errorf("databases dict size = %d, want 301 (1 reserved + 300 unique)", len(ce.databases))
	}
}

// TestSQLMetrics_TopDimensionsForID asserts that the per-query
// top-N breakdown ranks by count desc then name asc, and that
// "unknown" (empty) prefix values are skipped.
func TestSQLMetrics_TopDimensionsForID(t *testing.T) {
	ce := newCompactExecutions(16)
	ts := time.Unix(1700000000, 0).UTC()

	// queryA: 3× appdb, 1× webdb, 1× "" (skipped)
	for _, db := range []string{"appdb", "appdb", "appdb", "webdb", ""} {
		ce.append(ts, 1, "se-A", db, "u", "a", "h")
	}
	// queryB: 1× otherdb
	ce.append(ts, 1, "se-B", "otherdb", "u", "a", "h")

	m := SQLMetrics{executions: ce}
	dims := m.TopDimensionsForID("se-A", 5)
	if len(dims.Databases) != 2 {
		t.Fatalf("queryA: want 2 databases (appdb,webdb; empty skipped), got %d", len(dims.Databases))
	}
	if dims.Databases[0].Name != "appdb" || dims.Databases[0].Count != 3 {
		t.Errorf("queryA top db = %+v, want {appdb, 3}", dims.Databases[0])
	}
	if dims.Databases[1].Name != "webdb" || dims.Databases[1].Count != 1 {
		t.Errorf("queryA second db = %+v, want {webdb, 1}", dims.Databases[1])
	}
	// Unknown query id should return empty result.
	none := m.TopDimensionsForID("not-a-query", 5)
	if !none.IsEmpty() {
		t.Errorf("TopDimensionsForID for unknown id should be empty, got %+v", none)
	}
}
