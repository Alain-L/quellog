package output

import "testing"

// TestFormatBytes covers every unit boundary. The TB case is the regression
// guard: the TB branch previously divided by GB, rendering values >= 1 TB
// 1024x too large.
func TestFormatBytes(t *testing.T) {
	const (
		kb int64 = 1024
		mb       = 1024 * kb
		gb       = 1024 * mb
		tb       = 1024 * gb
	)
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{kb, "1.00 KB"},
		{kb + kb/2, "1.50 KB"},
		{mb, "1.00 MB"},
		{gb, "1.00 GB"},
		{tb, "1.00 TB"},     // exactly 1 TB — would have printed "1024.00 TB"
		{2 * tb, "2.00 TB"}, // would have printed "2048.00 TB"
		{5 * tb, "5.00 TB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.in); got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
