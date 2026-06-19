package parser

import (
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
