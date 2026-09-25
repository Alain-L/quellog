package quellog_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// TestPgBouncerCompressedInputs locks the compressed and archived reading
// paths for pgBouncer logs. A pgBouncer log must produce the exact same
// report whether it is read plain, gzip/zstd compressed, or out of a tar
// or zip archive — the nominal production shape, where logrotate hands us
// pgbouncer.log.1.gz next to pgbouncer.log.
//
// The fixture is the corpus one, so the expected output is its existing
// JSON golden: compression must be transparent, never a different report.
// Only the file the pooler was read from differs.
// Before the parseReader extraction, .gz/.zst panicked on a nil parser and
// archives were silently parsed as PostgreSQL.
func TestPgBouncerCompressedInputs(t *testing.T) {
	const fixture = "testdata/regressions/pgbouncer/pgbouncer.log"

	want, err := os.ReadFile(goldenPath(fixture, "json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	plain, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	dir := t.TempDir()
	variants := []struct {
		name  string
		build func(t *testing.T, plain []byte) []byte
	}{
		{"pgbouncer.log.gz", gzipFixture},
		{"pgbouncer.log.zst", zstdFixture},
		{"pgbouncer.tgz", tgzFixture},
		{"pgbouncer.zip", zipFixture},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			path := filepath.Join(dir, v.name)
			if err := os.WriteFile(path, v.build(t, plain), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			got := withoutSource(t, runHarness(t, false, path, "--json"))
			if want := withoutSource(t, want); !bytes.Equal(got, want) {
				t.Errorf("%s output diverges from the plain-input golden\n--- want ---\n%s\n--- got ---\n%s",
					v.name, want, got)
			}
		})
	}
}

// gzipFixture returns plain compressed as a standalone gzip stream.
func gzipFixture(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// zstdFixture returns plain compressed as a standalone zstd frame.
func zstdFixture(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

// tgzFixture returns a gzip-compressed tar holding plain as pgbouncer.log.
func tgzFixture(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	hdr := &tar.Header{
		Name:     "pgbouncer.log",
		Mode:     0o644,
		Size:     int64(len(plain)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(plain); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// zipFixture returns a zip archive holding plain as pgbouncer.log.
func zipFixture(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("pgbouncer.log")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// withoutSource drops what names the input file from every pooler of a
// --json document: its files, and its name when the log gives no listen
// address to name it by.
func withoutSource(t *testing.T, doc []byte) []byte {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	poolers, _ := d["pgbouncer"].([]any)
	for _, p := range poolers {
		delete(p.(map[string]any), "files")
		delete(p.(map[string]any), "name")
	}
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

// TestMixedArchiveIsSplit is a refutation test. An archive holding a
// PostgreSQL log and a pooler log -- a support bundle -- used to be routed
// whole by its first member: pooler lines polluted the PostgreSQL report,
// or the PostgreSQL report vanished, in silence. Each member must reach its
// own analyzer, whatever the order, exactly as the two files passed apart.
// Routing by the first member agrees with this rule on every archive that
// holds one kind of log, which is every other test in this file.
func TestMixedArchiveIsSplit(t *testing.T) {
	const pg, pooler = "testdata/pgbouncer/P1/p1.postgresql.log", "testdata/pgbouncer/P1/p1.pgbouncer.log"
	want := withoutSource(t, withoutMeta(t, runHarness(t, false, pg, pooler, "--json")))
	members := map[string][]byte{}
	for _, f := range []string{pg, pooler} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		members[filepath.Base(f)] = b
	}
	dir := t.TempDir()
	for name, order := range map[string][]string{
		"pg-first.tgz":     {"p1.postgresql.log", "p1.pgbouncer.log"},
		"pooler-first.tgz": {"p1.pgbouncer.log", "p1.postgresql.log"},
		"pooler-first.zip": {"p1.pgbouncer.log", "p1.postgresql.log"},
	} {
		var buf bytes.Buffer
		if filepath.Ext(name) == ".zip" {
			zw := zip.NewWriter(&buf)
			for _, m := range order {
				w, _ := zw.Create(m)
				w.Write(members[m])
			}
			zw.Close()
		} else {
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			for _, m := range order {
				tw.WriteHeader(&tar.Header{Name: m, Mode: 0o644, Size: int64(len(members[m])), Typeflag: tar.TypeReg})
				tw.Write(members[m])
			}
			tw.Close()
			gz.Close()
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := withoutSource(t, withoutMeta(t, runHarness(t, false, path, "--json"))); !bytes.Equal(got, want) {
			t.Errorf("%s: the archive's report diverges from its two files read apart\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
		}
	}
}

// withoutMeta drops the run metadata (input names and sizes) from a --json
// document.
func withoutMeta(t *testing.T, doc []byte) []byte {
	t.Helper()
	var d map[string]json.RawMessage
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	delete(d, "meta")
	out, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
