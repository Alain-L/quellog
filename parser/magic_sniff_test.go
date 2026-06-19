//go:build !js

package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSniffCompressionMagic(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		wantName string // "" => no codec
	}{
		{"gzip magic", []byte{0x1F, 0x8B, 0x08, 0x00}, "gzip"},
		{"zstd magic", []byte{0x28, 0xB5, 0x2F, 0xFD, 0x00}, "zstd"},
		{"plain text", []byte("2026-02-04 09:00:00 CET LOG: x"), ""},
		{"too short", []byte{0x1F}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "f")
			if err := os.WriteFile(p, c.data, 0o644); err != nil {
				t.Fatal(err)
			}
			codec, ok := sniffCompressionMagic(p)
			if (c.wantName != "") != ok {
				t.Fatalf("ok=%v, want %v", ok, c.wantName != "")
			}
			if ok && codec.name != c.wantName {
				t.Errorf("codec=%q, want %q", codec.name, c.wantName)
			}
		})
	}
}

// TestParseMislabeledCompressed locks the monkey-test finding: a gzip/zstd
// stream with a plain/wrong extension must be decompressed via magic sniffing,
// not rejected as binary. Counts must match the uncompressed fixture.
func TestParseMislabeledCompressed(t *testing.T) {
	plain := count(t, "../test/testdata/stderr.log")
	if plain == 0 {
		t.Fatal("baseline parsed 0 entries")
	}
	for _, src := range []string{"../test/testdata/stderr.log.gz", "../test/testdata/stderr.log.zst"} {
		src := src
		t.Run(filepath.Base(src), func(t *testing.T) {
			data, err := os.ReadFile(src)
			if err != nil {
				t.Fatalf("read %s: %v", src, err)
			}
			// Hand it a deliberately misleading .log extension.
			got := count(t, write(t, "mislabeled.log", data))
			if got != plain {
				t.Errorf("%s as .log parsed %d entries, want %d", src, got, plain)
			}
		})
	}
}
