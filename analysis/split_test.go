package analysis

import (
	"context"
	"testing"
	"time"

	"github.com/Alain-L/quellog/parser"
)

// A fixed non-UTC zone so the tests lock the wall-clock-local alignment
// (a real CET log must bucket on its own clock, not UTC).
var testCET = time.FixedZone("CET", 3600)

func TestBucketStartFor(t *testing.T) {
	mk := func(h, m int) time.Time { return time.Date(2026, 2, 4, h, m, 0, 0, testCET) }
	cases := []struct {
		name         string
		t            time.Time
		interval     time.Duration
		wantH, wantM int
	}{
		{"1h floors to the hour", mk(9, 37), time.Hour, 9, 0},
		{"1h exact boundary", mk(9, 0), time.Hour, 9, 0},
		{"3h floors to 0/3/6/9", mk(10, 15), 3 * time.Hour, 9, 0},
		{"3h at 06:00", mk(6, 0), 3 * time.Hour, 6, 0},
		{"5m floors", mk(9, 47), 5 * time.Minute, 9, 45},
		{"1d floors to local midnight", mk(15, 21), 24 * time.Hour, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bucketStartFor(c.t, c.interval)
			if got.Hour() != c.wantH || got.Minute() != c.wantM {
				t.Errorf("bucketStartFor(%s, %v) = %02d:%02d, want %02d:%02d",
					c.t.Format("15:04"), c.interval, got.Hour(), got.Minute(), c.wantH, c.wantM)
			}
			// The bucket start must stay on the input's local clock, not UTC.
			if _, off := got.Zone(); off != 3600 {
				t.Errorf("bucket start lost the +01:00 zone: offset=%d", off)
			}
		})
	}
}

func TestSplitLabel(t *testing.T) {
	d := time.Date(2026, 2, 4, 9, 0, 0, 0, testCET)
	if got := splitLabel(d, time.Hour); got != "2026-02-04 09:00" {
		t.Errorf("intraday label = %q, want %q", got, "2026-02-04 09:00")
	}
	if got := splitLabel(d, 24*time.Hour); got != "2026-02-04" {
		t.Errorf("daily label = %q, want %q", got, "2026-02-04")
	}
}

func TestAggregateMetricsBySplit(t *testing.T) {
	mk := func(h, m int) parser.LogEntry {
		return parser.NewLogEntry(time.Date(2026, 2, 4, h, m, 0, 0, testCET), "LOG:  statement: select 1", false)
	}
	entries := []parser.LogEntry{
		mk(8, 5), mk(8, 50), // 08:00 -> 2
		mk(9, 0), mk(9, 30), mk(9, 59), // 09:00 -> 3
		mk(11, 10), // 11:00 -> 1 (10:00 has none -> no bucket)
		parser.NewLogEntry(time.Time{}, "LOG:  no ts", false), // zero timestamp -> skipped
	}
	in := make(chan []parser.LogEntry, 1)
	in <- entries
	close(in)

	buckets, err := AggregateMetricsBySplit(context.Background(), in, time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []struct {
		label string
		count int
	}{
		{"2026-02-04 08:00", 2},
		{"2026-02-04 09:00", 3},
		{"2026-02-04 11:00", 1},
	}
	if len(buckets) != len(want) {
		t.Fatalf("got %d buckets, want %d", len(buckets), len(want))
	}
	sum := 0
	for i, w := range want {
		if buckets[i].Label != w.label {
			t.Errorf("bucket %d label = %q, want %q", i, buckets[i].Label, w.label)
		}
		if got := buckets[i].Metrics.Global.Count; got != w.count {
			t.Errorf("bucket %q count = %d, want %d", w.label, got, w.count)
		}
		sum += buckets[i].Metrics.Global.Count
	}
	// Partition invariant: the per-period counts sum to the timestamped
	// entries (the zero-timestamp one is skipped, not double-counted).
	if sum != 6 {
		t.Errorf("sum of period counts = %d, want 6", sum)
	}
}

func TestAggregateMetricsBySplitOverflow(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, testCET)
	entries := make([]parser.LogEntry, 0, maxSplitBuckets+1)
	for i := 0; i <= maxSplitBuckets; i++ { // one more distinct hourly bucket than the cap
		entries = append(entries, parser.NewLogEntry(base.Add(time.Duration(i)*time.Hour), "LOG:  x", false))
	}
	in := make(chan []parser.LogEntry, 1)
	in <- entries
	close(in)

	if _, err := AggregateMetricsBySplit(context.Background(), in, time.Hour); err == nil {
		t.Fatalf("expected overflow error for >%d periods, got nil", maxSplitBuckets)
	}
}
