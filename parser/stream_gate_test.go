//go:build !js

package parser

import "testing"

// TestReachesStreamParallelSize pins the pure size gate that decides whether an
// stderr input takes the entry-sharded parallel engine or the sequential reader.
// It is the single knob both the parse router (wrapCompressedParser) and the
// fan-in window (UsesStreamParallelStderr) consult, so a regression here silently
// changes RSS/throughput on every compressed set.
func TestReachesStreamParallelSize(t *testing.T) {
	const mb = 1 << 20
	cases := []struct {
		name       string
		compressed bool
		size       int64
		want       bool
	}{
		// Compressed gate = size*8 >= 256 MB (streamParallelMinDecompressed).
		// The agirc-itesoft corpus (~8-13 MB .zst, 256 MB decompressed) must be
		// BELOW the gate so it returns to the sequential v0.11.0 path.
		{"compressed agirc-sized 11MB", true, 11 * mb, false},
		{"compressed 13MB largest-in-corpus", true, 13 * mb, false},
		{"compressed just below gate 31MB", true, 31 * mb, false},
		{"compressed at gate 32MB", true, 32 * mb, true},
		{"compressed large 64MB", true, 64 * mb, true},
		// Plain gate = size >= 64 MB (stderrParallelMinSize), matching
		// StderrParser.Parse's existing parseParallel routing.
		{"plain small 10MB", false, 10 * mb, false},
		{"plain just below gate 63MB", false, 63 * mb, false},
		{"plain at gate 64MB", false, 64 * mb, true},
		{"plain large 500MB", false, 500 * mb, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reachesStreamParallelSize(tc.compressed, tc.size); got != tc.want {
				t.Fatalf("reachesStreamParallelSize(%v, %d) = %v, want %v",
					tc.compressed, tc.size, got, tc.want)
			}
		})
	}
}

// TestUsesStreamParallelStderr_SizeGate verifies the unified predicate on a real
// compressed stderr fixture: it self-parallelizes only above the size gate. The
// fan-in window and the parse router both key off this, so a small compressed
// stderr set (below the gate) parses sequentially AND gets the wide window
// (v0.11.0 behavior), while a large one parses in parallel AND gets the small
// window (RSS bound). The size argument is passed explicitly (the predicate does
// not stat), so the same fixture exercises both branches.
func TestUsesStreamParallelStderr_SizeGate(t *testing.T) {
	const fixture = "../test/testdata/stderr.log.zst" // a small non-syslog stderr .zst

	// Real (tiny) size: below the compressed gate -> sequential -> wide window.
	if UsesStreamParallelStderr(fixture, 111558) {
		t.Fatalf("small compressed stderr (%d bytes) must NOT self-parallelize (want sequential + wide window)", 111558)
	}

	// Synthetic large size on the same stderr fixture: clears the gate ->
	// parseStreamParallel -> small window.
	if large := int64(40 << 20); !UsesStreamParallelStderr(fixture, large) {
		t.Fatalf("large compressed stderr (%d bytes) must self-parallelize (want parallel + small window)", large)
	}
}
