//go:build !js

package parser

import (
	"archive/tar"
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTar builds a tar at a temp path from name->content members.
func writeTar(t *testing.T, members map[string][]byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "archive.tar")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for name, content := range members {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTarContentSniff locks the monkey-test finding: a tar member that holds a
// valid log but carries no recognized extension must be parsed by content, not
// silently dropped; genuinely binary members are still skipped.
func TestTarContentSniff(t *testing.T) {
	log, err := os.ReadFile("../test/testdata/stderr.log")
	if err != nil {
		t.Fatal(err)
	}
	plain := count(t, "../test/testdata/stderr.log")
	if plain == 0 {
		t.Fatal("baseline parsed 0 entries")
	}

	t.Run("extensionless log member", func(t *testing.T) {
		tarPath := writeTar(t, map[string][]byte{"postgres_nolog": log})
		if got := count(t, tarPath); got != plain {
			t.Errorf("extensionless member parsed %d entries, want %d", got, plain)
		}
	})

	t.Run("binary member skipped, log member kept", func(t *testing.T) {
		tarPath := writeTar(t, map[string][]byte{
			"junk.bin":       {0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, 0x03},
			"postgres_nolog": log,
		})
		if got := count(t, tarPath); got != plain {
			t.Errorf("mixed archive parsed %d entries, want %d (binary skipped, log kept)", got, plain)
		}
	})

	// The PostgreSQL parser reads nothing from Patroni lines: the skip is
	// about saying so, instead of dropping the member silently.
	t.Run("Patroni member skipped with a warning, log member kept", func(t *testing.T) {
		tarPath := writeTar(t, map[string][]byte{
			"patroni.log":    []byte(patroniSample),
			"postgresql.log": log,
		})
		var logged bytes.Buffer
		defer slog.SetDefault(slog.Default())
		slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
		if got := count(t, tarPath); got != plain {
			t.Errorf("archive with a Patroni log parsed %d entries, want %d", got, plain)
		}
		if !strings.Contains(logged.String(), "Patroni log inside an archive") {
			t.Errorf("no warning about the skipped Patroni member; logged: %q", logged.String())
		}
	})
}
