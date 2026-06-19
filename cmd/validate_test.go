package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Alain-L/quellog/parser"
)

// TestIsDetectionError makes sure detection failures (already logged by the
// parser layer) are recognised — even when wrapped — while raw parse-stage
// errors (e.g. a truncated stream) are not, so the latter get a partial-output
// warning instead of being swallowed.
func TestIsDetectionError(t *testing.T) {
	detection := []error{
		parser.ErrFileEmpty,
		parser.ErrBinaryFile,
		parser.ErrInvalidFormat,
		parser.ErrUnknownFormat,
		parser.ErrCompressionFailed,
		fmt.Errorf("file.gz: %w", parser.ErrCompressionFailed), // wrapped, as ParseFile returns
	}
	for _, err := range detection {
		if !isDetectionError(err) {
			t.Errorf("isDetectionError(%v) = false, want true", err)
		}
	}
	for _, err := range []error{errors.New("unexpected EOF"), nil} {
		if isDetectionError(err) {
			t.Errorf("isDetectionError(%v) = true, want false", err)
		}
	}
}

// resetFlagState zeroes every flag validateFlagCombinations reads, so each
// table case starts clean regardless of order.
func resetFlagState() {
	jsonFlag = false
	jsonCompactFlag = false
	yamlFlag = false
	mdFlag = false
	htmlFlag = false
	openFlag = false
	followFlag = false
	splitFlag = ""
	outputFlag = ""
	sqlDetailFlag = nil
	eventDetailFlag = nil
	sqlPerformanceFlag = false
	sqlOverviewFlag = false
}

// TestValidateFlagCombinations locks the monkey-test finding: invalid combos
// must be rejected by validateFlagCombinations (called once before follow mode)
// so they fail cleanly instead of spinning in the follow loop.
func TestValidateFlagCombinations(t *testing.T) {
	cases := []struct {
		name    string
		set     func()
		wantErr bool
	}{
		{"clean: nothing set", func() {}, false},
		{"clean: html + open", func() { htmlFlag = true; openFlag = true }, false},
		{"clean: split + html", func() { htmlFlag = true; splitFlag = "1d" }, false},
		{"open without html", func() { openFlag = true }, true},
		{"open with follow", func() { htmlFlag = true; openFlag = true; followFlag = true }, true},
		{"split without html", func() { splitFlag = "1d" }, true},
		{"split with follow", func() { htmlFlag = true; splitFlag = "1d"; followFlag = true }, true},
		{"json + json-compact", func() { jsonFlag = true; jsonCompactFlag = true }, true},
		{"multi-format + output", func() { jsonFlag = true; htmlFlag = true; outputFlag = "x" }, true},
		{"split + sql-detail", func() { htmlFlag = true; splitFlag = "1d"; sqlDetailFlag = []string{"q1"} }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetFlagState()
			defer resetFlagState()
			c.set()
			err := validateFlagCombinations()
			if (err != nil) != c.wantErr {
				t.Errorf("validateFlagCombinations() err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}
