package parser

import (
	"sync"
	"sync/atomic"
	"time"
)

// normalizeZone ensures UTC-offset timestamps carry time.UTC as their
// Location rather than a FixedZone("", 0). Go's time.Parse maps the
// zero offset to one or the other depending on the input format and
// platform (e.g. "+00:00" on macOS yields FixedZone; on Linux it can
// yield time.UTC). Downstream formatters that use the "MST" directive
// then render "+0000" vs "UTC" inconsistently. Calling this helper
// right after time.Parse keeps all zero-offset timestamps as time.UTC
// so output is deterministic.
func normalizeZone(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	if _, offset := t.Zone(); offset == 0 {
		return t.UTC()
	}
	return t
}

// WallClock projects a timestamp onto its own wall clock, expressed as the
// equivalent civil instant in UTC: a value whose UTC fields read exactly what
// the original displayed in its zone (e.g. 09:00 CET -> 09:00 UTC). This lets
// time-range filters compare a naive --begin/--end (which carries no zone) to
// log entries regardless of the log's or the machine's timezone — the same
// civil-time alignment the --split bucketing uses. Zero times pass through.
func WallClock(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	_, off := t.Zone()
	return t.Add(time.Duration(off) * time.Second)
}

// parseTime wraps time.Parse and normalizes zero-offset zones to UTC.
// All parsers should use this helper instead of time.Parse directly.
func parseTime(layout, value string) (time.Time, error) {
	t, err := time.Parse(layout, value)
	if err != nil {
		return t, err
	}
	return normalizeZone(t), nil
}

// zoneCache memoizes timezone-name → *time.Location resolutions for
// the fast path. A log file carries one or two distinct zone names
// (standard + DST variants), so the map stays tiny and lock-free
// reads dominate.
var zoneCache sync.Map // string → *time.Location

// zoneEntry is the single-slot hot cache for the most recent zone
// name seen by fastParsePGTimestamp.
type zoneEntry struct {
	name string
	loc  *time.Location
}

var lastZone atomic.Pointer[zoneEntry]

// resolveZoneName maps a zone abbreviation from a PG log prefix to a
// Location, reproducing exactly what time.Parse("… MST") + the
// normalizeZone helper would produce:
//   - "UTC"/"GMT" → time.UTC
//   - the machine's local zone names (standard AND daylight) →
//     time.Local, so time.Date resolves the per-date DST offset just
//     like time.Parse does
//   - any other abbreviation → FixedZone(name, 0), which
//     normalizeZone then folds to UTC — same as the slow path
func resolveZoneName(name string) *time.Location {
	if v, ok := zoneCache.Load(name); ok {
		return v.(*time.Location)
	}
	var loc *time.Location
	if name == "UTC" || name == "GMT" {
		loc = time.UTC
	} else {
		winter, _ := time.Date(time.Now().Year(), 1, 15, 12, 0, 0, 0, time.Local).Zone()
		summer, _ := time.Date(time.Now().Year(), 7, 15, 12, 0, 0, 0, time.Local).Zone()
		if name == winter || name == summer {
			loc = time.Local
		} else {
			loc = time.FixedZone(name, 0)
		}
	}
	zoneCache.Store(name, loc)
	return loc
}

// fastParsePGTimestamp decodes the canonical PostgreSQL log_line_prefix
// timestamp "YYYY-MM-DD HH:MM:SS[.fff] TZ" directly from the input —
// no time.Parse, no intermediate string allocation. Generic over
// string and []byte so both the bytes hot path and the string-based
// syslog/normalized-entry path share one implementation. The caller
// has already validated the separator positions ('-', '-', ' ', ':',
// ':'); this function validates the digits, the optional fractional
// part, and resolves the zone via resolveZoneName. ok=false on any
// deviation so callers fall back to the slow parseTime path, keeping
// behavior strictly identical on exotic formats (numeric offsets,
// localized names, …).
func fastParsePGTimestamp[T string | []byte](b T, tzStart, tzEnd int) (time.Time, bool) {
	// Digits of the fixed date/time part.
	year, ok := atoi4(b, 0)
	if !ok {
		return time.Time{}, false
	}
	month, ok := atoi2(b, 5)
	if !ok || month < 1 || month > 12 {
		return time.Time{}, false
	}
	day, ok := atoi2(b, 8)
	if !ok || day < 1 || day > 31 {
		return time.Time{}, false
	}
	hour, ok := atoi2(b, 11)
	if !ok || hour > 23 {
		return time.Time{}, false
	}
	minute, ok := atoi2(b, 14)
	if !ok || minute > 59 {
		return time.Time{}, false
	}
	sec, ok := atoi2(b, 17)
	if !ok || sec > 59 { // time.Parse rejects :60 (no leap-second leniency); stay in parity
		return time.Time{}, false
	}

	// Optional fractional seconds: ".” followed by 1-9 digits, ending
	// right where the timezone token starts (minus its leading space).
	nanos := 0
	fracEnd := 19
	if 19 < len(b) && b[19] == '.' {
		i := 20
		mult := 100_000_000
		for i < tzStart-1 && i < len(b) {
			c := b[i]
			if c < '0' || c > '9' {
				return time.Time{}, false
			}
			if mult > 0 {
				nanos += int(c-'0') * mult
				mult /= 10
			}
			i++
		}
		if i == 20 { // bare "." with no digits
			return time.Time{}, false
		}
		fracEnd = i
	}
	// The byte right before tzStart must be the separating space and
	// the fraction (or seconds) must end exactly there.
	if fracEnd != tzStart-1 || b[fracEnd] != ' ' {
		return time.Time{}, false
	}

	// Timezone token: alphabetic abbreviations only — anything else
	// (numeric offsets like "+02:00") falls back to the slow path.
	for i := tzStart; i < tzEnd; i++ {
		c := b[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return time.Time{}, false
		}
	}
	// Single-entry hot cache in front of zoneCache: a log file almost
	// always carries one zone name, and the string(bytes) == string
	// comparison below is allocation-free (compiler-optimized), so the
	// per-line string conversion only happens on a zone change.
	var loc *time.Location
	if e := lastZone.Load(); e != nil && string(b[tzStart:tzEnd]) == e.name {
		loc = e.loc
	} else {
		name := string(b[tzStart:tzEnd])
		loc = resolveZoneName(name)
		lastZone.Store(&zoneEntry{name: name, loc: loc})
	}

	t := time.Date(year, time.Month(month), day, hour, minute, sec, nanos, loc)
	return normalizeZone(t), true
}

// atoi2 converts two ASCII digits at b[i], b[i+1]. ok=false on any
// non-digit.
func atoi2[T string | []byte](b T, i int) (int, bool) {
	if b[i] < '0' || b[i] > '9' || b[i+1] < '0' || b[i+1] > '9' {
		return 0, false
	}
	return int(b[i]-'0')*10 + int(b[i+1]-'0'), true
}

// atoi4 converts four ASCII digits at b[i..i+3]. ok=false on any
// non-digit.
func atoi4[T string | []byte](b T, i int) (int, bool) {
	hi, ok := atoi2(b, i)
	if !ok {
		return 0, false
	}
	lo, ok := atoi2(b, i+2)
	if !ok {
		return 0, false
	}
	return hi*100 + lo, true
}
