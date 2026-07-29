package parser

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// A minimal stderr body whose lines carry a real timestamp + severity, so
// matchesLogPattern accepts them once any leading literal is stripped.
const prefixOffsetBody = `2026-01-12 00:50:00 CET p=1 [a.b - 1] u - LOG:  connection received
2026-01-12 00:50:01 CET p=1 [a.b - 2] u - LOG:  connection authorized
2026-01-12 00:50:02 CET p=2 [c.d - 1] u - ERROR:  boom
2026-01-12 00:50:03 CET p=2 [c.d - 2] u - LOG:  disconnection
2026-01-12 00:50:04 CET p=3 [e.f - 1] u - LOG:  checkpoint starting
`

func withLinePrefix(body, prefix string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestDetectLeadingPrefix(t *testing.T) {
	cases := []struct {
		name   string
		sample string
		want   int
	}{
		{"no prefix (standard log)", prefixOffsetBody, 0},
		{"single-char literal (fdj T)", withLinePrefix(prefixOffsetBody, "T"), 1},
		{"multi-char literal", withLinePrefix(prefixOffsetBody, "pg1: "), len("pg1: ")},
		{"angle-bracket literal", withLinePrefix(prefixOffsetBody, "<c1> "), len("<c1> ")},
		{
			// Prose with a couple of stray dates must not pass the consensus.
			name:   "prose with stray dates",
			sample: "see 2026-01-12 00:00:00 in the report\nanother note about 2026-01-12 00:00:01 here\njust text\n",
			want:   0,
		},
		{
			// A timestamp pushed past the 16-byte cap is not a log_line_prefix.
			name:   "prefix beyond cap",
			sample: withLinePrefix(prefixOffsetBody, "this-prefix-is-way-too-long "),
			want:   0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectLeadingPrefix(c.sample); got != c.want {
				t.Errorf("detectLeadingPrefix = %d, want %d", got, c.want)
			}
		})
	}
}

// TestParsePrefixedEquivalence locks the end-to-end contract: a log carrying a
// constant literal before the timestamp parses to the same entry count as the
// same log without it. This is the fdj_postgresql.log case.
func TestParsePrefixedEquivalence(t *testing.T) {
	base := count(t, write(t, "base.log", []byte(prefixOffsetBody)))
	if base == 0 {
		t.Fatal("baseline parsed 0 entries")
	}
	// "<c1> " starts with '<', which hasTimestampBytes treats as an entry
	// start — the case that would have mis-driven the parallel segment path
	// had Parse not skipped it when prefixLen > 0.
	for _, prefix := range []string{"T", "pg1: ", "<c1> "} {
		prefix := prefix
		t.Run("prefix="+prefix, func(t *testing.T) {
			data := []byte(withLinePrefix(prefixOffsetBody, prefix))
			got := count(t, write(t, "prefixed.log", data))
			if got != base {
				t.Errorf("prefixed (%q) parsed %d entries, want %d", prefix, got, base)
			}
		})
	}
}

// TestParsePrefixedCompressed locks the propagation of the detected prefix
// through the decompression wrappers: a prefixed log delivered as .gz/.zst
// must parse to the same count as its plain form, not silently to zero.
func TestParsePrefixedCompressed(t *testing.T) {
	base := count(t, write(t, "base.log", []byte(prefixOffsetBody)))
	if base == 0 {
		t.Fatal("baseline parsed 0 entries")
	}
	data := []byte(withLinePrefix(prefixOffsetBody, "T"))
	t.Run("gz", func(t *testing.T) {
		if got := count(t, write(t, "prefixed.log.gz", gzipBytes(t, data))); got != base {
			t.Errorf("gz prefixed parsed %d entries, want %d", got, base)
		}
	})
	t.Run("zst", func(t *testing.T) {
		if got := count(t, write(t, "prefixed.log.zst", zstdBytes(t, data))); got != base {
			t.Errorf("zst prefixed parsed %d entries, want %d", got, base)
		}
	})
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zstdBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	out := enc.EncodeAll(data, nil)
	_ = enc.Close()
	return out
}

// TestStripLeadingPrefix guards the byte-shape check: strip only when a
// timestamp actually sits at the offset, leave everything else untouched.
func TestStripLeadingPrefix(t *testing.T) {
	p := &StderrParser{prefixLen: 1}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"strips when timestamp follows", "T2026-01-12 00:50:00 CET LOG:  x", "2026-01-12 00:50:00 CET LOG:  x"},
		{"leaves continuation (tab) intact", "\tSTATEMENT:  select 1", "\tSTATEMENT:  select 1"},
		{"leaves non-timestamp line intact", "Trandom text without a date", "Trandom text without a date"},
		{"leaves too-short line intact", "T2026", "T2026"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(p.stripLeadingPrefix([]byte(c.in))); got != c.want {
				t.Errorf("stripLeadingPrefix(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestStripLeadingPrefixDisabled: with prefixLen 0 the fast paths must not touch
// the entry at all (the normal, overwhelmingly common case).
func TestStripLeadingPrefixDisabled(t *testing.T) {
	p := &StderrParser{prefixLen: 0}
	in := "T2026-01-12 00:50:00 CET LOG:  x"
	if got := string(p.stripLeadingPrefix([]byte(in))); got != in {
		t.Errorf("stripLeadingPrefix with prefixLen=0 = %q, want unchanged", got)
	}
}
