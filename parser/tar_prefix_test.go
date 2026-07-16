//go:build !js

package parser

import "testing"

// TestTarPrefixSalvage locks bug #12: a stderr log carrying a constant literal
// before its timestamp (a custom log_line_prefix ahead of %t/%m) parses fine as
// a plain or single-compressed file — the salvage in autodetect.go — but used to
// yield ZERO entries when the same bytes lived inside a tar, because the tar
// ".log" / rotated ".log.<date>" paths built a StderrParser with prefixLen 0 and
// never ran detectLeadingPrefix. The fix samples the member head and applies the
// same salvage, so a tar member parses to the same count as its plain form.
func TestTarPrefixSalvage(t *testing.T) {
	// Baseline: the un-prefixed body as a plain file.
	base := count(t, write(t, "base.log", []byte(prefixOffsetBody)))
	if base == 0 {
		t.Fatal("baseline parsed 0 entries")
	}

	// A 6-byte literal prefix before every timestamp (e.g. "NODE1 " ahead of %t).
	prefixed := []byte(withLinePrefix(prefixOffsetBody, "NODE1 "))

	// Sanity: the plain/compressed path already salvages this today.
	if got := count(t, write(t, "prefixed.log", prefixed)); got != base {
		t.Fatalf("plain prefixed .log parsed %d entries, want %d (salvage baseline)", got, base)
	}

	cases := []struct {
		name   string
		member string
	}{
		{"plain .log member", "postgresql.log"},
		{"rotated .log.<date> member", "postgresql.log.2026-03-23-10"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			tarPath := writeTar(t, map[string][]byte{c.member: prefixed})
			got := count(t, tarPath)
			if got == 0 {
				t.Fatalf("prefixed member %q inside tar parsed 0 entries (bug #12 regression)", c.member)
			}
			if got != base {
				t.Errorf("prefixed member %q parsed %d entries, want %d", c.member, got, base)
			}
		})
	}

	// No-prefix common case must not regress: a plain .log member with a normal
	// timestamp at column 0 still parses to the baseline (prefixLen stays 0).
	t.Run("no-prefix member unaffected", func(t *testing.T) {
		tarPath := writeTar(t, map[string][]byte{"postgresql.log": []byte(prefixOffsetBody)})
		if got := count(t, tarPath); got != base {
			t.Errorf("no-prefix member parsed %d entries, want %d", got, base)
		}
	})
}
