//go:build !js

package parser

import (
	"bytes"
	"compress/gzip"
	"os"
	"testing"
)

// TestIsRotatedLogFile_Dateext pins the rotated-name matcher: logrotate
// "dateext" naming (base + '-' + date) must be recognized alongside the
// classic numeric/date-dot ('.' + digit) forms, without matching non-logs.
func TestIsRotatedLogFile_Dateext(t *testing.T) {
	keep := []string{
		"postgresql.log.1", "postgresql.log.2.gz", "postgresql-16-main.log.1",
		"postgresql.log.2026-03-23-10", "postgresql.log-20260320",
		"postgresql.log-20260320.gz", "postgresql.log-2026-03-20.zst",
		"pg.csv-20260320.gz", "app.csv.5",
	}
	drop := []string{
		"postgresql.log", "app.csv", "readme.txt", "notes",
		"my.logging.txt", "foo.log-bar", "catalog.data",
	}
	for _, n := range keep {
		if !isRotatedLogFile(n) {
			t.Errorf("isRotatedLogFile(%q) = false, want true", n)
		}
	}
	for _, n := range drop {
		if isRotatedLogFile(n) {
			t.Errorf("isRotatedLogFile(%q) = true, want false", n)
		}
	}
}

// TestTarDateextCompressed is the end-to-end regression: a tar holding a
// logrotate dateext member compressed with gzip (postgresql.log-20260320.gz)
// must be decompressed and parsed, not dropped as unsupported. The name was
// rejected by both the entry gate (".log." only) and the internal dispatch,
// so the whole rotated history vanished; both now accept the '-' separator.
func TestTarDateextCompressed(t *testing.T) {
	log, err := os.ReadFile("../test/testdata/stderr.log")
	if err != nil {
		t.Fatal(err)
	}
	plain := count(t, "../test/testdata/stderr.log")
	if plain == 0 {
		t.Fatal("baseline parsed 0 entries")
	}

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(log); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	tarPath := writeTar(t, map[string][]byte{
		"postgresql.log-20260320.gz": gzBuf.Bytes(),
	})
	if got := count(t, tarPath); got != plain {
		t.Errorf("dateext-compressed member parsed %d entries, want %d (rotated history dropped?)", got, plain)
	}
}
