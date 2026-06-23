package cmd

import (
	"testing"
	"time"
)

func TestParseDurationUnits(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30m", 30 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"1d", 24 * time.Hour},
		{"2w", 2 * 7 * 24 * time.Hour},
		{"1y", 365 * 24 * time.Hour},
		{"292y", 292 * 365 * 24 * time.Hour}, // largest year value that still fits
	}
	for _, c := range cases {
		got, err := parseDuration(c.in)
		if err != nil {
			t.Errorf("parseDuration(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestParseDurationOverflow locks the monkey-test finding: large N with d/w/y
// must error, not silently wrap around int64 nanoseconds (e.g. "9999y" used to
// become ~55y, feeding --split/--last/--window a wrong window).
func TestParseDurationOverflow(t *testing.T) {
	for _, in := range []string{"9999y", "100000y", "1000000y", "100000000d", "10000000w"} {
		if d, err := parseDuration(in); err == nil {
			t.Errorf("parseDuration(%q) = %v, want overflow error", in, d)
		}
	}
}
