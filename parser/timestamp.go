package parser

import "time"

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

// parseTime wraps time.Parse and normalizes zero-offset zones to UTC.
// All parsers should use this helper instead of time.Parse directly.
func parseTime(layout, value string) (time.Time, error) {
	t, err := time.Parse(layout, value)
	if err != nil {
		return t, err
	}
	return normalizeZone(t), nil
}
