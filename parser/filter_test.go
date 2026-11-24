package parser

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPassesFiltersWallClock locks the wall-clock semantics of --begin/--end:
// a naive bound ("09:00:00", no zone, parsed as UTC) must match the moment a
// non-UTC log entry's own clock reads 09:00 — not 09:00 UTC. This is the bug
// the --split parity check surfaced (entries kept their zone while the bound
// was UTC, so the window was off by the zone offset).
func TestPassesFiltersWallClock(t *testing.T) {
	cet := time.FixedZone("CET", 3600) // +01:00, like a real European server log
	// --begin "2026-02-04 09:00:00" arrives zoneless, i.e. UTC, exactly as
	// time.Parse(DateTimeFormat, ...) would produce it.
	begin := time.Date(2026, 2, 4, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 2, 4, 10, 0, 0, 0, time.UTC)
	filters := LogFilters{BeginT: WallClock(begin), EndT: WallClock(end)}

	entryAt := func(h, m int) LogEntry {
		return NewLogEntry(time.Date(2026, 2, 4, h, m, 0, 0, cet), "LOG:  x", false)
	}
	cases := []struct {
		name string
		h, m int
		keep bool
	}{
		{"just before begin (08:59 CET)", 8, 59, false},
		{"exactly at begin (09:00 CET)", 9, 0, true},
		{"inside window (09:30 CET)", 9, 30, true},
		{"exactly at end (10:00 CET)", 10, 0, true},
		{"just after end (10:01 CET)", 10, 1, false},
		// The pre-fix bug: 09:30 CET == 08:30 UTC would be wrongly dropped as
		// "before 09:00 UTC". Wall-clock semantics keep it.
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PassesFilters(entryAt(c.h, c.m), filters); got != c.keep {
				t.Errorf("PassesFilters(%02d:%02d CET) = %v, want %v", c.h, c.m, got, c.keep)
			}
		})
	}
}

// TestWallClock checks the civil-time projection across zones, including DST-ish
// fixed offsets and zero times.
func TestWallClock(t *testing.T) {
	cet := time.FixedZone("CET", 3600)
	minus5 := time.FixedZone("EST", -5*3600)
	cases := []struct {
		name           string
		in             time.Time
		wantH, wantMin int
	}{
		{"CET 09:00 -> civil 09:00 UTC", time.Date(2026, 2, 4, 9, 0, 0, 0, cet), 9, 0},
		{"EST 23:30 -> civil 23:30 UTC", time.Date(2026, 2, 4, 23, 30, 0, 0, minus5), 23, 30},
		{"UTC 14:15 -> unchanged", time.Date(2026, 2, 4, 14, 15, 0, 0, time.UTC), 14, 15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WallClock(c.in).UTC()
			if got.Hour() != c.wantH || got.Minute() != c.wantMin {
				t.Errorf("WallClock = %02d:%02d UTC, want %02d:%02d", got.Hour(), got.Minute(), c.wantH, c.wantMin)
			}
		})
	}
	if !WallClock(time.Time{}).IsZero() {
		t.Error("WallClock(zero) must stay zero")
	}
}

// TestFilterStreamHeldConnection covers #7: "connection received" is logged
// before PostgreSQL knows the database (db=[unknown]), so --dbname dropped it
// and the connection report went empty. FilterStream now holds that line per
// PID and releases it ahead of the session's first line that passes, even
// across batches; the line of a session that never passes stays dropped.
// With --appname, the application is only known after the connection
// lines, so the session waits for its first line naming it.
func TestFilterStreamHeldConnection(t *testing.T) {
	ts := time.Date(2026, 7, 15, 9, 56, 45, 0, time.UTC)
	entryApp := func(pid, db, app, body string) LogEntry {
		return NewLogEntry(ts, "["+pid+"]: db="+db+",user=u,app="+app+",client=[local], LOG:  "+body, false)
	}
	entry := func(pid, db, body string) LogEntry { return entryApp(pid, db, "psql", body) }
	received := func(pid string) LogEntry {
		return entryApp(pid, "[unknown]", "[unknown]", "connection received: host=[local]")
	}
	authorized := func(pid, db string) LogEntry {
		return entryApp(pid, db, "[unknown]", "connection authorized: user=u database="+db)
	}
	disconnect := func(pid, db string) LogEntry {
		return entry(pid, db, "disconnection: session time: 0:00:00.006 user=u database="+db+" host=[local]")
	}
	statement := func(pid, app string) LogEntry { return entryApp(pid, "app", app, "duration: 1.000 ms") }
	byDB := LogFilters{DbFilter: []string{"app"}}

	cases := []struct {
		name    string
		filters LogFilters
		batches [][]LogEntry
		want    []string // messages kept, in order
	}{
		{
			name:    "one batch",
			filters: byDB,
			batches: [][]LogEntry{{
				received("1"), authorized("1", "app"), disconnect("1", "app"),
				received("2"), authorized("2", "other"), disconnect("2", "other"),
			}},
			want: []string{
				received("1").Message, authorized("1", "app").Message, disconnect("1", "app").Message,
			},
		},
		{
			name:    "connection line at the end of a batch",
			filters: byDB,
			batches: [][]LogEntry{
				{authorized("3", "app"), received("1")},
				{authorized("1", "app"), disconnect("1", "app")},
			},
			want: []string{
				authorized("3", "app").Message,
				received("1").Message, authorized("1", "app").Message, disconnect("1", "app").Message,
			},
		},
		{
			name:    "dropped session, then the PID is reused",
			filters: byDB,
			batches: [][]LogEntry{{
				received("1"), authorized("1", "other"), disconnect("1", "other"),
				authorized("1", "app"),
			}},
			want: []string{authorized("1", "app").Message},
		},
		{
			name:    "--appname: held until the application is known",
			filters: LogFilters{AppFilter: []string{"psql"}},
			batches: [][]LogEntry{{
				received("1"), authorized("1", "app"), statement("1", "psql"),
				received("2"), authorized("2", "app"), statement("2", "pgbench"), statement("2", "psql"),
			}},
			want: []string{received("1").Message, statement("1", "psql").Message, statement("2", "psql").Message},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := make(chan []LogEntry, len(c.batches))
			for _, b := range c.batches {
				in <- b
			}
			close(in)
			out := make(chan []LogEntry, len(c.batches))
			FilterStream(context.Background(), in, out, c.filters)
			var got []string
			for b := range out {
				for _, e := range b {
					got = append(got, e.Message)
				}
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("kept:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(c.want, "\n  "))
			}
		})
	}
}
