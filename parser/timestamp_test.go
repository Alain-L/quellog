package parser

import (
	"strings"
	"testing"
	"time"
)

// slowParsePG reproduces the pre-fast-path behavior: layout-based
// time.Parse with the fractional layout first, then the plain one,
// both normalized. Used as the parity oracle.
func slowParsePG(ts string) (time.Time, bool) {
	t, err := parseTime("2006-01-02 15:04:05.999 MST", ts)
	if err != nil {
		t, err = parseTime("2006-01-02 15:04:05 MST", ts)
		if err != nil {
			return time.Time{}, false
		}
	}
	return t, true
}

// TestFastParsePGTimestamp_ParityWithTimeParse feeds the fast path and
// the layout-based slow path the same canonical PG timestamps and
// requires identical instants and rendered zones. Covers: no fraction,
// millisecond fraction, UTC/GMT, an unknown abbreviation (folded to
// UTC by normalizeZone on both paths), and both generic instantiations
// (string and []byte).
func TestFastParsePGTimestamp_ParityWithTimeParse(t *testing.T) {
	cases := []string{
		"2026-02-13 12:11:59.123 UTC",
		"2026-02-13 12:11:59 UTC",
		"2025-12-31 23:59:60 GMT", // leap second — rejected by both paths
		"2026-07-14 08:00:01.7 UTC",
		"2026-01-02 03:04:05.999 KST", // unknown abbrev → FixedZone(0) → UTC
		"2024-02-29 00:00:00.000 UTC", // leap day
	}
	for _, ts := range cases {
		// The timezone token starts after the last space.
		tzStart := strings.LastIndexByte(ts, ' ') + 1
		want, wantOK := slowParsePG(ts)
		got, gotOK := fastParsePGTimestamp(ts, tzStart, len(ts))
		if gotOK != wantOK {
			t.Errorf("%q: ok=%v, want %v", ts, gotOK, wantOK)
			continue
		}
		if !wantOK {
			continue
		}
		if !got.Equal(want) {
			t.Errorf("%q: instant %v, want %v", ts, got, want)
		}
		if got.Format("2006-01-02 15:04:05.999 MST") != want.Format("2006-01-02 15:04:05.999 MST") {
			t.Errorf("%q: rendered %q, want %q", ts,
				got.Format("2006-01-02 15:04:05.999 MST"),
				want.Format("2006-01-02 15:04:05.999 MST"))
		}

		// Same through the []byte instantiation.
		gotB, okB := fastParsePGTimestamp([]byte(ts), tzStart, len(ts))
		if !okB || !gotB.Equal(want) {
			t.Errorf("%q ([]byte): got %v ok=%v, want %v", ts, gotB, okB, want)
		}
	}
}

// TestFastParsePGTimestamp_FallsBackOnExotic ensures the fast path
// rejects (ok=false) anything it is not strictly built for, so the
// caller's slow path keeps handling those — numeric offsets, malformed
// digits, missing fraction digits, misplaced timezone token.
func TestFastParsePGTimestamp_FallsBackOnExotic(t *testing.T) {
	cases := []string{
		"2026-02-13 12:11:59 +02",     // numeric offset
		"2026-02-13 12:11:59 +02:00",  // numeric offset, colon form
		"2026-02-13 12:11:5X UTC",     // bad digit
		"2026-02-13 12:11:59. UTC",    // bare dot, no fraction digits
		"2026-13-13 12:11:59 UTC",     // month out of range
		"2026-02-13 25:11:59 UTC",     // hour out of range
		"2026-02-13 12:11:59.12X UTC", // non-digit in fraction
	}
	for _, ts := range cases {
		tzStart := strings.LastIndexByte(ts, ' ') + 1
		if _, ok := fastParsePGTimestamp(ts, tzStart, len(ts)); ok {
			t.Errorf("%q: fast path accepted, want fallback", ts)
		}
	}
}

// TestFastParsePGTimestamp_LocalZoneDST checks that the machine's own
// zone abbreviations resolve through time.Local so DST offsets follow
// the parsed date — same as time.Parse. Skipped when the local zone is
// UTC (no abbreviation distinct from the fast-path UTC special case).
func TestFastParsePGTimestamp_LocalZoneDST(t *testing.T) {
	winterName, _ := time.Date(time.Now().Year(), 1, 15, 12, 0, 0, 0, time.Local).Zone()
	if winterName == "UTC" {
		t.Skip("local zone is UTC; nothing to assert beyond the UTC case")
	}
	ts := "2026-01-15 10:00:00.000 " + winterName
	tzStart := strings.LastIndexByte(ts, ' ') + 1
	want, wantOK := slowParsePG(ts)
	got, gotOK := fastParsePGTimestamp(ts, tzStart, len(ts))
	if gotOK != wantOK {
		t.Fatalf("ok=%v, want %v", gotOK, wantOK)
	}
	if wantOK && !got.Equal(want) {
		t.Errorf("instant %v, want %v", got, want)
	}
}
