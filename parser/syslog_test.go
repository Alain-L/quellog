package parser

import "testing"

// TestDetectSyslogFormat covers the format sniffer, including the
// regression guard for lines that match the ISO prefix but are too short
// to carry the seconds field. Before the len>=17 guard, a line truncated
// right after the minute ("2026-06-13T14:30") matched the first four
// byte checks and then panicked reading line[16].
func TestDetectSyslogFormat(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want SyslogFormat
	}{
		{"iso full", "2025-11-30T21:10:20+00:00 host app: msg\n", SyslogISO},
		{"bsd full", "Nov 30 21:10:20 host app: msg\n", SyslogBSD},
		{"rfc5424", "<134>1 2025-11-30T21:10:20+00:00 host app - - - msg\n", SyslogRFC5424},
		{"plain stderr", "2025-11-30 21:10:20.101 UTC [55] LOG:  ready\n", SyslogNone},
		{"too short", "short line\n", SyslogNone},
		// Regression: 16-byte ISO-looking line truncated after the minute.
		{"iso truncated to minute", "2026-06-13T14:30\n", SyslogNone},
		// Regression: 15-byte ISO-looking line (no newline content beyond).
		{"iso truncated to hour-min", "2026-06-13T14:3\n", SyslogNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectSyslogFormat([]byte(c.in)); got != c.want {
				t.Errorf("detectSyslogFormat(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
