// Package cmd implements the command-line interface for quellog.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

// startHeapSnapshots writes a heap pprof + a runtime.MemStats text dump
// to the directory in QUELLOG_HEAP_SNAPSHOTS at fixed intervals (default
// 10s, override via QUELLOG_HEAP_SNAPSHOTS_INTERVAL). Returns a stop
// function that takes a final snapshot and stops the goroutine. If the
// env var is unset, returns a no-op.
//
// Investigation-only: keep out of CI/release paths. Activate via:
//
//	QUELLOG_HEAP_SNAPSHOTS=/tmp/heap_snaps \
//	QUELLOG_HEAP_SNAPSHOTS_INTERVAL=5s \
//	bin/quellog _samples/Z/*.tar.gz --json > /dev/null
//
// Snapshots are named snap_NNNNs.{prof,stats}. Use
//
//	go tool pprof -base snap_T0.prof snap_T1.prof
//
// to see incremental allocations between two timestamps.
func startHeapSnapshots() func() {
	dir := os.Getenv("QUELLOG_HEAP_SNAPSHOTS")
	if dir == "" {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "heap snapshot: cannot create %s: %v\n", dir, err)
		return func() {}
	}

	interval := 10 * time.Second
	if s := os.Getenv("QUELLOG_HEAP_SNAPSHOTS_INTERVAL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			interval = d
		}
	}

	start := time.Now()
	writeSnapshot(dir, 0)

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case t := <-ticker.C:
				writeSnapshot(dir, int(t.Sub(start).Seconds()))
			}
		}
	}()

	return func() {
		close(stopCh)
		<-doneCh
		writeSnapshot(dir, int(time.Since(start).Seconds()))
	}
}

// writeSnapshot dumps a heap pprof (post-GC for inuse accuracy) plus
// a sibling .stats text file with runtime.MemStats counters.
func writeSnapshot(dir string, elapsedS int) {
	runtime.GC()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	base := filepath.Join(dir, fmt.Sprintf("snap_%04ds", elapsedS))

	if f, err := os.Create(base + ".prof"); err == nil {
		_ = pprof.WriteHeapProfile(f)
		_ = f.Close()
	}

	if f, err := os.Create(base + ".stats"); err == nil {
		fmt.Fprintf(f, "elapsed_s=%d\n", elapsedS)
		fmt.Fprintf(f, "heap_inuse=%d\n", ms.HeapInuse)
		fmt.Fprintf(f, "heap_alloc=%d\n", ms.HeapAlloc)
		fmt.Fprintf(f, "heap_sys=%d\n", ms.HeapSys)
		fmt.Fprintf(f, "heap_objects=%d\n", ms.HeapObjects)
		fmt.Fprintf(f, "stack_inuse=%d\n", ms.StackInuse)
		fmt.Fprintf(f, "sys=%d\n", ms.Sys)
		fmt.Fprintf(f, "num_gc=%d\n", ms.NumGC)
		fmt.Fprintf(f, "alloc_total=%d\n", ms.TotalAlloc)
		fmt.Fprintf(f, "frees=%d\n", ms.Frees)
		fmt.Fprintf(f, "mallocs=%d\n", ms.Mallocs)
		_ = f.Close()
	}
}
