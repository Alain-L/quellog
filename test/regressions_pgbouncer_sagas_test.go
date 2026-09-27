package quellog_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPgBouncerSagas freezes the JSON, Markdown and text reports of the lab sagas under
// testdata/pgbouncer: real poolers and a real PostgreSQL taken through
// restarts, reloads, pauses, saturation and log rotation, with the ground
// truth in each MANIFEST.md. Every pooler log of a saga is passed along
// with its PostgreSQL log, as a DBA collecting a host's logs would.
func TestPgBouncerSagas(t *testing.T) {
	var runs []string
	entries, _ := os.ReadDir("testdata/pgbouncer")
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "P") {
			runs = append(runs, filepath.Join("testdata/pgbouncer", e.Name()))
		}
	}
	if len(runs) == 0 {
		t.Fatal("no saga under testdata/pgbouncer")
	}
	for _, run := range runs {
		t.Run(filepath.Base(run), func(t *testing.T) {
			pooler, _ := filepath.Glob(filepath.Join(run, "*.pgbouncer.log*"))
			server, _ := filepath.Glob(filepath.Join(run, "*.postgresql.log"))
			args := append(pooler, server...)
			for _, format := range []string{"json", "md"} {
				got := runHarness(t, false, append(args, "--"+format)...)
				compareOrUpdateGolden(t, got, goldenPath(run, format))
			}
			// The text report opens on a processing time that varies from
			// run to run; everything after that first line is frozen.
			text := runHarness(t, false, args...)
			if i := bytes.IndexByte(text, '\n'); i >= 0 {
				text = text[i+1:]
			}
			compareOrUpdateGolden(t, text, goldenPath(run, "txt"))
		})
	}
}

// TestPgBouncerSplitFileMergesBack cuts a real capture in two, as a log
// rotation does, and expects the report of the whole file: the two parts
// share the pooler's PID and follow each other, so they are one pooler,
// and every figure must add up to what the single file gives.
func TestPgBouncerSplitFileMergesBack(t *testing.T) {
	const fixture = "testdata/regressions/pgbouncer/pgbouncer.log"
	plain, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	lines := strings.SplitAfter(string(plain), "\n")
	dir := t.TempDir()
	first, second := filepath.Join(dir, "pgbouncer.log.1"), filepath.Join(dir, "pgbouncer.log")
	half := len(lines) / 2
	if err := os.WriteFile(first, []byte(strings.Join(lines[:half], "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(strings.Join(lines[half:], "")), 0o644); err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile(goldenPath(fixture, "json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	got := withoutSource(t, runHarness(t, false, first, second, "--json"))
	if want := withoutSource(t, want); !bytes.Equal(got, want) {
		t.Errorf("a capture split in two diverges from the whole\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}
