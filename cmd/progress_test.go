package cmd

import "testing"

// TestProgressBarSuppressed locks the bugfix: the progress bar must NOT be
// suppressed by the output format (--html and friends). It writes to stderr and
// clears before any output, so only too-small input or --quiet should turn it
// off; the terminal check lives separately in newProgressBar.
func TestProgressBarSuppressed(t *testing.T) {
	const big = progressMinSize
	const small = progressMinSize - 1

	cases := []struct {
		name string
		size int64
		set  func()
		want bool // true => suppressed (no bar)
	}{
		{"large, no flags", big, func() {}, false},
		{"large + html (the reported bug)", big, func() { htmlFlag = true }, false},
		{"large + json", big, func() { jsonFlag = true }, false},
		{"large + yaml", big, func() { yamlFlag = true }, false},
		{"large + md", big, func() { mdFlag = true }, false},
		{"large + quiet", big, func() { quietFlag = true }, true},
		{"small input", small, func() {}, true},
		{"small input + html", small, func() { htmlFlag = true }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetFlagState()
			quietFlag = false
			defer func() { resetFlagState(); quietFlag = false }()
			c.set()
			if got := progressBarSuppressed(c.size); got != c.want {
				t.Errorf("progressBarSuppressed(%d) = %v, want %v", c.size, got, c.want)
			}
		})
	}
}
