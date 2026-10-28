package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstPostgresArg(t *testing.T) {
	dir := t.TempDir()
	patroni := filepath.Join(dir, "patroni.log")
	pg := filepath.Join(dir, "postgresql.csv")
	os.WriteFile(patroni, []byte("2026-09-24 13:43:50,360 INFO: Lock owner: None; I am pg1\n2026-09-24 13:43:52,779 INFO: no action. I am (pg1), the leader with the lock\n"), 0o644)
	os.WriteFile(pg, []byte("x\n"), 0o644)
	if got := firstPostgresArg([]string{patroni, pg}); got != pg {
		t.Errorf("firstPostgresArg = %s, want the PostgreSQL log %s", got, pg)
	}
}
