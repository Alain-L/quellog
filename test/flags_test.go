// test/flags_test.go
package quellog_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// testFile is a small test file that exists for flag testing
const testFile = "testdata/test_summary.log"

// runQuellog executes the binary with given flags and returns stdout, stderr, and exit code
func runQuellog(t *testing.T, binary string, args ...string) (stdout, stderr string, exitCode int) {
	cmd := exec.Command(binary, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("Failed to run command: %v", err)
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// TestFlagCompatibility tests the compatibility matrix of all flags
func TestFlagCompatibility(t *testing.T) {
	// Build the binary once
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	binary := "../quellog_test"

	// =========================================================================
	// VALID COMBINATIONS
	// =========================================================================

	t.Run("ValidCombinations", func(t *testing.T) {
		validCases := []struct {
			name string
			args []string
		}{
			// Output formats (each alone)
			{"default_output", []string{testFile}},
			{"json_output", []string{testFile, "--json"}},
			{"md_output", []string{testFile, "--md"}},

			// Section selectors (single)
			{"section_summary", []string{testFile, "--summary"}},
			{"section_checkpoints", []string{testFile, "--checkpoints"}},
			{"section_events", []string{testFile, "--events"}},
			{"section_errors", []string{testFile, "--errors"}},
			{"section_sql_performance", []string{testFile, "--sql-performance"}},
			{"section_tempfiles", []string{testFile, "--tempfiles"}},
			{"section_locks", []string{testFile, "--locks"}},
			{"section_maintenance", []string{testFile, "--maintenance"}},
			{"section_connections", []string{testFile, "--connections"}},
			{"section_clients", []string{testFile, "--clients"}},

			// Section selectors (multiple)
			{"sections_summary_events", []string{testFile, "--summary", "--events"}},
			{"sections_all_three", []string{testFile, "--summary", "--checkpoints", "--connections"}},

			// Section + output format
			{"section_json", []string{testFile, "--summary", "--json"}},
			{"section_md", []string{testFile, "--summary", "--md"}},
			{"multiple_sections_json", []string{testFile, "--summary", "--events", "--json"}},

			// SQL flags
			{"sql_summary", []string{testFile, "--sql-summary"}},
			{"sql_summary_md", []string{testFile, "--sql-summary", "--md"}},

			// Time filters (valid combinations) - using dates that match test_summary.log (2025-01-01)
			{"begin_only", []string{testFile, "--begin", "2025-01-01 00:00:00"}},
			{"end_only", []string{testFile, "--end", "2025-01-02 00:00:00"}},
			{"begin_end", []string{testFile, "--begin", "2025-01-01 00:00:00", "--end", "2025-01-02 00:00:00"}},
			{"begin_window", []string{testFile, "--begin", "2025-01-01 00:00:00", "--window", "24h"}},
			{"end_window", []string{testFile, "--end", "2025-01-02 00:00:00", "--window", "24h"}},

			// Attribute filters (use values that don't filter out everything)
			{"dbuser_filter", []string{testFile, "--dbuser", "postgres"}},
			{"appname_filter", []string{testFile, "--appname", "psql"}},
			{"exclude_user", []string{testFile, "--exclude-user", "nonexistent_user"}},
		}

		for _, tc := range validCases {
			t.Run(tc.name, func(t *testing.T) {
				_, stderr, exitCode := runQuellog(t, binary, tc.args...)
				if exitCode != 0 {
					t.Errorf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
				}
			})
		}
	})

	// =========================================================================
	// INVALID COMBINATIONS (should fail with clear error)
	// =========================================================================

	t.Run("InvalidCombinations", func(t *testing.T) {
		invalidCases := []struct {
			name        string
			args        []string
			errContains string // expected substring in stderr
		}{
			// Output format conflicts
			{
				name:        "json_and_md",
				args:        []string{testFile, "--json", "--md"},
				errContains: "mutually exclusive",
			},

			// Time filter conflicts
			{
				name:        "begin_end_window",
				args:        []string{testFile, "--begin", "2024-01-01 00:00:00", "--end", "2024-12-31 23:59:59", "--window", "1h"},
				errContains: "cannot all be used together",
			},
			{
				name:        "last_with_begin",
				args:        []string{testFile, "--last", "1h", "--begin", "2024-01-01 00:00:00"},
				errContains: "--last cannot be used with",
			},
			{
				name:        "last_with_end",
				args:        []string{testFile, "--last", "1h", "--end", "2024-12-31 23:59:59"},
				errContains: "--last cannot be used with",
			},
			{
				name:        "last_with_window",
				args:        []string{testFile, "--last", "1h", "--window", "30m"},
				errContains: "--last cannot be used with",
			},

			// SQL flag conflicts
			{
				name:        "json_with_sql_summary",
				args:        []string{testFile, "--json", "--sql-summary"},
				errContains: "--json is not compatible with --sql-summary",
			},
			{
				name:        "json_with_sql_detail",
				args:        []string{testFile, "--json", "--sql-detail", "se-123456"},
				errContains: "--json is not compatible with",
			},
		}

		for _, tc := range invalidCases {
			t.Run(tc.name, func(t *testing.T) {
				_, stderr, exitCode := runQuellog(t, binary, tc.args...)

				if exitCode == 0 {
					t.Errorf("Expected non-zero exit code for invalid combination")
				}
				if !strings.Contains(stderr, tc.errContains) {
					t.Errorf("Expected stderr to contain %q, got: %s", tc.errContains, stderr)
				}
			})
		}
	})

	// =========================================================================
	// EDGE CASES
	// =========================================================================

	t.Run("EdgeCases", func(t *testing.T) {
		edgeCases := []struct {
			name        string
			args        []string
			shouldPass  bool
			errContains string
		}{
			// No input file
			{
				name:       "no_input",
				args:       []string{},
				shouldPass: true, // exits with info message, not error
			},

			// Non-existent file (exits with 0 and warning, not error)
			{
				name:       "nonexistent_file",
				args:       []string{"testdata/nonexistent_file_xyz.log"},
				shouldPass: true, // Program shows warning but exits 0
			},

			// Help flag
			{
				name:       "help_flag",
				args:       []string{"--help"},
				shouldPass: true,
			},

			// Version flag
			{
				name:       "version_flag",
				args:       []string{"--version"},
				shouldPass: true,
			},

			// Invalid time format
			{
				name:        "invalid_begin_format",
				args:        []string{testFile, "--begin", "not-a-date"},
				shouldPass:  false,
				errContains: "", // error message varies
			},

			// Invalid window format
			{
				name:        "invalid_window_format",
				args:        []string{testFile, "--window", "not-a-duration"},
				shouldPass:  false,
				errContains: "", // error message varies
			},
		}

		for _, tc := range edgeCases {
			t.Run(tc.name, func(t *testing.T) {
				_, stderr, exitCode := runQuellog(t, binary, tc.args...)

				if tc.shouldPass {
					if exitCode != 0 {
						t.Errorf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
					}
				} else {
					if exitCode == 0 {
						t.Errorf("Expected non-zero exit code")
					}
					if tc.errContains != "" && !strings.Contains(stderr, tc.errContains) {
						t.Errorf("Expected stderr to contain %q, got: %s", tc.errContains, stderr)
					}
				}
			})
		}
	})
}

// TestOutputFormats verifies that each output format produces valid output
func TestOutputFormats(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	binary := "../quellog_test"

	t.Run("TextOutput", func(t *testing.T) {
		stdout, _, exitCode := runQuellog(t, binary, testFile)
		if exitCode != 0 {
			t.Fatalf("Command failed with exit code %d", exitCode)
		}
		// Text output should contain SUMMARY section
		if !strings.Contains(stdout, "SUMMARY") {
			t.Errorf("Text output missing SUMMARY section")
		}
	})

	t.Run("JSONOutput", func(t *testing.T) {
		stdout, _, exitCode := runQuellog(t, binary, testFile, "--json")
		if exitCode != 0 {
			t.Fatalf("Command failed with exit code %d", exitCode)
		}
		// JSON should start with {
		stdout = strings.TrimSpace(stdout)
		if !strings.HasPrefix(stdout, "{") {
			t.Errorf("JSON output should start with '{', got: %s...", stdout[:min(50, len(stdout))])
		}
		if !strings.HasSuffix(stdout, "}") {
			t.Errorf("JSON output should end with '}'")
		}
	})

	t.Run("MarkdownOutput", func(t *testing.T) {
		stdout, _, exitCode := runQuellog(t, binary, testFile, "--md")
		if exitCode != 0 {
			t.Fatalf("Command failed with exit code %d", exitCode)
		}
		// Markdown should contain headers
		if !strings.Contains(stdout, "#") {
			t.Errorf("Markdown output missing headers")
		}
	})
}

// TestSectionFilters verifies that section flags correctly filter output
func TestSectionFilters(t *testing.T) {
	buildCmd := exec.Command("go", "build", "-o", "quellog_test", ".")
	buildCmd.Dir = ".."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	binary := "../quellog_test"

	sections := []struct {
		flag     string
		expected string // string that should appear in output
		excluded string // string that should NOT appear (from another section)
	}{
		{"--summary", "Start date", "CHECKPOINTS"},
		{"--checkpoints", "CHECKPOINTS", "CONNECTIONS"},
		{"--events", "EVENTS", "MAINTENANCE"},
		{"--connections", "CONNECTIONS", "LOCKS"},
		{"--maintenance", "MAINTENANCE", "TEMPORARY"},
	}

	for _, sec := range sections {
		t.Run(sec.flag, func(t *testing.T) {
			stdout, _, exitCode := runQuellog(t, binary, testFile, sec.flag)
			if exitCode != 0 {
				t.Fatalf("Command failed with exit code %d", exitCode)
			}

			if !strings.Contains(stdout, sec.expected) {
				t.Errorf("Output with %s should contain %q", sec.flag, sec.expected)
			}

			// When filtering to one section, others should not appear
			// (unless the section is empty or they share content)
			if sec.excluded != "" && strings.Contains(stdout, sec.excluded) {
				t.Errorf("Output with %s should NOT contain %q", sec.flag, sec.excluded)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
