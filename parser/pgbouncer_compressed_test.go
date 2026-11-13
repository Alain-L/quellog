//go:build !js

package parser

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// pgbouncerSample is a minimal pgBouncer log: the prefix shape plus a
// pgBouncer-only marker, which is what detection keys on.
const pgbouncerSample = "2026-09-09 20:58:58.651 UTC [1] LOG stats: 1 xacts/s, 2 queries/s, " +
	"in 3 B/s, out 4 B/s, xact 5 us, query 6 us, wait 7 us\n" +
	"2026-09-09 20:58:59.651 UTC [1] LOG C-0x55f1: app/user@10.0.0.1:5432 login attempt: db=app user=app\n"

// TestWrapCompressedParserNeverReturnsNilPair locks the failure mode behind
// the pgBouncer .gz panic: the router used to log an error and return a nil
// parser with a NIL ERROR for any format it did not know, which ParseFile
// then dereferenced (SIGSEGV). Every format detection can produce must wrap,
// and anything else must come back as an error.
func TestWrapCompressedParserNeverReturnsNilPair(t *testing.T) {
	detectable := []struct {
		name string
		p    LogParser
	}{
		{"stderr", &StderrParser{}},
		{"csv", &CsvParser{}},
		{"json", &JsonParser{}},
		{"pgbouncer", &PgBouncerParser{}},
	}
	for _, codec := range []compressionCodec{gzipCodec, zstdCodec} {
		for _, d := range detectable {
			wrapped, err := wrapCompressedParser(d.p, codec)
			if err != nil {
				t.Errorf("wrapCompressedParser(%s, %s): unexpected error: %v", d.name, codec.name, err)
			}
			if wrapped == nil {
				t.Errorf("wrapCompressedParser(%s, %s): nil parser", d.name, codec.name)
			}
		}
	}

	// A parser the router cannot stream must be reported, never handed back
	// as a nil parser with a nil error.
	wrapped, err := wrapCompressedParser(&TarParser{}, gzipCodec)
	if wrapped != nil {
		t.Errorf("wrapCompressedParser(TarParser): got a parser, want nil")
	}
	if !errors.Is(err, ErrUnsupportedCompressedParser) {
		t.Errorf("wrapCompressedParser(TarParser): err = %v, want ErrUnsupportedCompressedParser", err)
	}
}

// TestDetectFileFormatCompressedPgBouncer locks the routing contract the cmd
// layer relies on: a pgBouncer log keeps its format through gzip and through
// a tar archive. Reported as "stderr", its lines were fed to the PostgreSQL
// analyzer instead of the pooler one.
func TestDetectFileFormatCompressedPgBouncer(t *testing.T) {
	dir := t.TempDir()

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(pgbouncerSample)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	var tgz bytes.Buffer
	tzw := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(tzw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "pgbouncer.log",
		Mode:     0o644,
		Size:     int64(len(pgbouncerSample)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(pgbouncerSample)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := tzw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	cases := []struct {
		name string
		data []byte
	}{
		{"pgbouncer.log", []byte(pgbouncerSample)},
		{"pgbouncer.log.gz", gz.Bytes()},
		{"pgbouncer.tgz", tgz.Bytes()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, c.name)
			if err := os.WriteFile(path, c.data, 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			if got := DetectFileFormat(path); got != "pgbouncer" {
				t.Errorf("DetectFileFormat(%s) = %q, want \"pgbouncer\"", c.name, got)
			}
		})
	}
}

// TestKeepMember: each track reads its own members and nothing else; the
// default reads everything, as a run without pooler files always has.
func TestKeepMember(t *testing.T) {
	for _, c := range []struct {
		track        ArchiveTrack
		pooler, want bool
	}{
		{AllMembers, true, true}, {AllMembers, false, true},
		{PostgresMembers, true, false}, {PostgresMembers, false, true},
		{PoolerMembers, true, true}, {PoolerMembers, false, false},
	} {
		if got := keepMember(c.track, c.pooler); got != c.want {
			t.Errorf("keepMember(%v, pooler=%v) = %v, want %v", c.track, c.pooler, got, c.want)
		}
	}
}
