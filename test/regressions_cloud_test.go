package quellog_test

import (
	"testing"
)

// Cloud-provider format fixtures (RDS, Aurora, Azure, GCP) embed metadata
// inline in the prefix, e.g. `2025-11-23 11:04:24 UTC:[local]:user@db:[pid]:LOG:`
// instead of `2026-01-01 00:00:01 CET [pid]: user=foo db=bar LOG:`.
//
// They are parsed by parseRDSFormat / parseAzureFormat in
// parser/cloud_providers.go. A regression in the prefix-aware normalizer
// (introduced when the mmap path was dropped in aa0e769) was stripping
// the timestamp from these lines, causing partial or total parse failure.
//
// These tests pin one assertion per cloud format so any regression on the
// timestamp-extraction path fails loud.

func TestRegression_AWSRDSFormat(t *testing.T) {
	got := runFixtureJSON(t, "testdata/cloud/rds_postgres.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 53 {
		t.Errorf("summary.total_logs = %v, want 53 (RDS prefix normalization stripped early entries?)", total)
	}
	if start, _ := summary["start_date"].(string); start != "2024-01-15 08:00:01" {
		t.Errorf("summary.start_date = %q, want %q (first RDS line dropped?)",
			start, "2024-01-15 08:00:01")
	}
}

// rds_sample.log uses `[local]` as a placeholder host instead of an
// IP(port) tuple. This is the variant that was returning *zero* entries
// on HEAD before the fix — the all-placeholder prefix slipped past the
// `@`-detection guards.
func TestRegression_AWSRDSLocalPlaceholder(t *testing.T) {
	got := runFixtureJSON(t, "testdata/cloud/rds_sample.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 76 {
		t.Errorf("summary.total_logs = %v, want 76 (every RDS line dropped — normalize stripped timestamp?)", total)
	}
	if start, _ := summary["start_date"].(string); start != "2025-11-23 11:04:24" {
		t.Errorf("summary.start_date = %q, want %q", start, "2025-11-23 11:04:24")
	}
}

func TestRegression_AWSAuroraFormat(t *testing.T) {
	got := runFixtureJSON(t, "testdata/cloud/aurora_postgres.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 48 {
		t.Errorf("summary.total_logs = %v, want 48", total)
	}
	if start, _ := summary["start_date"].(string); start != "2024-01-15 10:00:01" {
		t.Errorf("summary.start_date = %q, want %q", start, "2024-01-15 10:00:01")
	}
}

func TestRegression_AzurePostgresFormat(t *testing.T) {
	got := runFixtureJSON(t, "testdata/cloud/azure_postgres.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 51 {
		t.Errorf("summary.total_logs = %v, want 51", total)
	}
	if start, _ := summary["start_date"].(string); start != "2024-01-15 10:00:01" {
		t.Errorf("summary.start_date = %q, want %q", start, "2024-01-15 10:00:01")
	}
}

// rds_overnight is the bulk fixture — 2 582 entries spanning 8 hours.
// The first line is at 00:00:00 sharp; if the normalizer drops it, the
// summary start_date jumps forward.
func TestRegression_AWSRDSOvernightBulk(t *testing.T) {
	got := runFixtureJSON(t, "testdata/cloud/rds_overnight_2025-11-23.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 2582 {
		t.Errorf("summary.total_logs = %v, want 2582", total)
	}
	if start, _ := summary["start_date"].(string); start != "2025-11-23 00:00:00" {
		t.Errorf("summary.start_date = %q, want %q (first overnight line dropped?)",
			start, "2025-11-23 00:00:00")
	}
}

// GCP Cloud SQL stderr-style log uses a regular stderr prefix with an
// epoch timestamp. It does not exercise the cloud-format normalization
// path, but pinning it here keeps the cloud corpus self-contained.
func TestRegression_GCPCloudSQLFormat(t *testing.T) {
	got := runFixtureJSON(t, "testdata/cloud/gcp_overnight_2025-11-23.log")
	summary := got["summary"].(map[string]any)
	if total, _ := summary["total_logs"].(float64); total != 2579 {
		t.Errorf("summary.total_logs = %v, want 2579", total)
	}
}
