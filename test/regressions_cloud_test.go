package quellog_test

import (
	"os"
	"path/filepath"
	"testing"
)

// Cloud-provider format fixtures (RDS, Aurora, Azure) embed metadata inline
// in the prefix, e.g. `2025-11-23 11:04:24 UTC:[local]:user@db:[pid]:LOG: ...`
// instead of `2026-01-01 00:00:01 CET [pid]: user=foo db=bar LOG: ...`.
//
// They are parsed by parseRDSFormat / parseAzureFormat in
// parser/cloud_providers.go. A regression in the prefix-aware normalizer
// (introduced when the mmap path was dropped in aa0e769) was stripping
// the timestamp from these lines, causing partial or total parse failure.
//
// The fixtures here are inline so the suite has zero external dependencies
// — the original test/testdata/cloud/ directory is gitignored as
// developer-local material. Keep the fixtures > 10 lines so they trigger
// StderrParser.detectPrefixStructure (the path the bug lives in).

// writeFixture writes content to a t.TempDir() file and returns the path.
func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

// rdsLog with IP(port) host, 12 entries — variant most users have.
const rdsLogIPPort = `2025-11-23 08:00:01 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  connection received: host=10.0.1.100 port=54321
2025-11-23 08:00:01 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  connection authorized: user=postgres database=mydb
2025-11-23 08:00:02 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  statement: SELECT * FROM users WHERE id = 1
2025-11-23 08:00:03 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  duration: 12.456 ms  statement: SELECT count(*) FROM orders
2025-11-23 08:00:04 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  duration: 8.123 ms  statement: SELECT id, name FROM customers
2025-11-23 08:00:05 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  duration: 234.567 ms  statement: SELECT * FROM large_table
2025-11-23 08:00:06 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  duration: 5.012 ms  statement: COMMIT
2025-11-23 08:00:07 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  duration: 3.456 ms  statement: BEGIN
2025-11-23 08:00:08 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  duration: 45.678 ms  statement: UPDATE orders SET status = 'shipped' WHERE id = 42
2025-11-23 08:00:09 UTC:10.0.1.100(54321):postgres@mydb:[1234]:ERROR:  duplicate key value violates unique constraint "orders_pkey"
2025-11-23 08:00:09 UTC:10.0.1.100(54321):postgres@mydb:[1234]:STATEMENT:  INSERT INTO orders (id, total) VALUES (42, 100)
2025-11-23 08:00:10 UTC:10.0.1.100(54321):postgres@mydb:[1234]:LOG:  disconnection: session time: 0:00:09.234 user=postgres database=mydb host=10.0.1.100 port=54321
`

// rdsLogLocalPlaceholder uses [local] for host/user/db — RDS instance-local
// connections (rdsadmin internal monitoring). This is the variant that
// returned ZERO entries on HEAD before the fix because every prefix
// component is a placeholder.
const rdsLogLocalPlaceholder = `2025-11-23 11:04:24 UTC:[local]:[unknown]@[unknown]:[17597]:LOG:  connection received: host=[local]
2025-11-23 11:04:24 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  connection authenticated: identity="rdsmon" method=peer
2025-11-23 11:04:24 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  connection authorized: user=rdsadmin database=rdsadmin
2025-11-23 11:04:39 UTC::@:[1337]:LOG:  checkpoint starting: time
2025-11-23 11:04:39 UTC::@:[1337]:LOG:  checkpoint complete: wrote 3 buffers (0.0%); 0 WAL file(s) added, 0 removed, 1 recycled
2025-11-23 11:04:40 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  duration: 1.234 ms  statement: SELECT 1
2025-11-23 11:04:41 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  duration: 2.345 ms  statement: SELECT pg_is_in_recovery()
2025-11-23 11:04:42 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  duration: 3.456 ms  statement: SELECT version()
2025-11-23 11:04:43 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  duration: 4.567 ms  statement: SHOW server_version
2025-11-23 11:04:44 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  duration: 5.678 ms  statement: SELECT current_database()
2025-11-23 11:04:45 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  duration: 6.789 ms  statement: SELECT now()
2025-11-23 11:04:46 UTC:[local]:rdsadmin@rdsadmin:[17597]:LOG:  disconnection: session time: 0:00:22.567 user=rdsadmin database=rdsadmin host=[local]
`

// azureLog uses '-' as the separator after the timezone — distinct from
// RDS/Aurora's ':' separator.
const azureLog = `2024-01-15 10:00:01 UTC-sess001-LOG: connection received: host=10.10.1.5 port=51234
2024-01-15 10:00:01 UTC-sess001-LOG: connection authorized: user=webapp database=production
2024-01-15 10:00:02 UTC-sess001-LOG: statement: SELECT id FROM users WHERE email = 'a@example.com'
2024-01-15 10:00:03 UTC-sess001-LOG: duration: 5.678 ms  statement: SELECT count(*) FROM orders
2024-01-15 10:00:04 UTC-sess001-LOG: duration: 12.345 ms  statement: SELECT * FROM products
2024-01-15 10:00:05 UTC-sess001-LOG: duration: 23.456 ms  statement: SELECT * FROM inventory
2024-01-15 10:00:06 UTC-sess001-LOG: duration: 8.901 ms  statement: COMMIT
2024-01-15 10:00:07 UTC-sess001-LOG: duration: 4.567 ms  statement: BEGIN
2024-01-15 10:00:08 UTC-sess001-LOG: duration: 56.789 ms  statement: UPDATE products SET stock = 0 WHERE id = 1
2024-01-15 10:00:09 UTC-sess001-LOG: duration: 7.890 ms  statement: COMMIT
2024-01-15 10:00:10 UTC-sess001-LOG: duration: 1.234 ms  statement: SELECT pg_sleep(0.001)
2024-01-15 10:00:11 UTC-sess001-LOG: disconnection: session time: 0:00:10.234
`

// TestRegression_AWSRDSFormatIPPort pins parsing of the IP(port) RDS variant.
// Before the fix every line was processed by the prefix-aware normalizer,
// which stripped the timestamp because metadata.Prefix matched starting at
// offset 0 — leaving an entry with no parseable timestamp that got dropped.
func TestRegression_AWSRDSFormatIPPort(t *testing.T) {
	path := writeFixture(t, "rds_ip.log", rdsLogIPPort)
	got := runFixtureJSON(t, path)
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 11 {
		t.Errorf("summary.total_logs = %v, want 11 (RDS prefix normalization stripped timestamps?)", total)
	}
	if start, _ := summary["start_date"].(string); start != "2025-11-23 08:00:01" {
		t.Errorf("summary.start_date = %q, want %q", start, "2025-11-23 08:00:01")
	}
}

// TestRegression_AWSRDSLocalPlaceholder is the variant that returned *zero*
// entries on HEAD before the fix — every host/user/db slot is a placeholder
// (`[local]`, `[unknown]`, `rdsadmin`), but the prefix structure still
// matches and triggers the broken normalize path.
func TestRegression_AWSRDSLocalPlaceholder(t *testing.T) {
	path := writeFixture(t, "rds_local.log", rdsLogLocalPlaceholder)
	got := runFixtureJSON(t, path)
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 12 {
		t.Errorf("summary.total_logs = %v, want 12 (every line dropped — normalize stripped timestamp?)", total)
	}
	if start, _ := summary["start_date"].(string); start != "2025-11-23 11:04:24" {
		t.Errorf("summary.start_date = %q, want %q", start, "2025-11-23 11:04:24")
	}
}

// TestRegression_AzurePostgresFormat uses the dash-separated Azure prefix.
// Pinning it here keeps the cloud-format guard in normalizeEntryBeforeParsing
// honest for both ':' and '-' separators.
func TestRegression_AzurePostgresFormat(t *testing.T) {
	path := writeFixture(t, "azure.log", azureLog)
	got := runFixtureJSON(t, path)
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 12 {
		t.Errorf("summary.total_logs = %v, want 12", total)
	}
	if start, _ := summary["start_date"].(string); start != "2024-01-15 10:00:01" {
		t.Errorf("summary.start_date = %q, want %q", start, "2024-01-15 10:00:01")
	}
}
