// Package quellog_test contains end-to-end tests that build the quellog
// binary and exercise it against fixture log files.
//
// This file provides a small harness (TestMain + a few helpers) that the
// regression-corpus tests share. Existing tests in this package each build
// their own binary; that's left untouched here.
package quellog_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// updateGoldens is set via `go test -update`. When true, golden files are
// rewritten with the current output instead of being compared. Use this
// after an intentional output change, then commit the updated goldens.
var updateGoldens = flag.Bool("update", false, "rewrite golden files instead of comparing")

// harnessBinary is the path to the quellog binary built once for the entire
// test package by TestMain. Empty if TestMain has not yet built it.
var harnessBinary string

// TestMain builds the quellog binary once for all tests in the package and
// removes it on exit. Existing tests that build their own binary continue
// to work — they are independent and we do not interfere with their path.
func TestMain(m *testing.M) {
	flag.Parse()

	tmpDir, err := os.MkdirTemp("", "quellog-harness-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness: mkdir tmp: %v\n", err)
		os.Exit(2)
	}
	binPath := filepath.Join(tmpDir, "quellog")

	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "harness: build failed: %v\n%s\n", err, out)
		os.RemoveAll(tmpDir)
		os.Exit(2)
	}
	harnessBinary = binPath

	code := m.Run()

	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// runHarness runs the harness binary with the given arguments and returns
// its stdout. Test fails on non-zero exit unless allowFailure is true.
func runHarness(t *testing.T, allowFailure bool, args ...string) []byte {
	t.Helper()
	if harnessBinary == "" {
		t.Fatal("harness binary not built (TestMain did not run?)")
	}

	cmd := exec.Command(harnessBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil && !allowFailure {
		t.Fatalf("quellog %v failed: %v\nstderr: %s", args, err, stderr.String())
	}
	return stdout.Bytes()
}

// goldenPath returns the path to the golden file for the given fixture
// and format (e.g. "json", "md").
func goldenPath(fixturePath, format string) string {
	return fixturePath + "." + format + ".golden"
}

// compareOrUpdateGolden compares got against the contents of goldenPath.
// When the -update flag is set, it rewrites the golden file instead.
func compareOrUpdateGolden(t *testing.T, got []byte, goldenPath string) {
	t.Helper()

	if *updateGoldens {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("update golden %s: %v", goldenPath, err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test -update` to create it)", goldenPath, err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("output diverges from golden %s\n--- want ---\n%s\n--- got ---\n%s",
			goldenPath, want, got)
	}
}
