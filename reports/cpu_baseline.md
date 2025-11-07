# CPU Optimization Baseline

**Date**: 2025-11-08
**Branch**: perf (after memory optimizations)

## Test Files

### B.log (54 MB, ~100k entries)
- **Time**: 0.35s real, 0.51s user
- **Max RSS**: 83 MB
- **Peak footprint**: 23 MB

### I1.log (1 GB, ~1.8M entries)
- **Time**: 5.09s real, 7.91s user
- **Max RSS**: 1.36 GB
- **Peak footprint**: 285 MB

## CPU Profile Analysis (I1.log)

Total CPU time: 9.07s (170% - multi-core)

### Top CPU Consumers (self time)

1. **Goroutine synchronization**: 4.71s (52%)
   - `runtime.usleep`: 2.92s (32%)
   - `runtime.pthread_cond_signal`: 1.76s (19%)
   - `runtime.pthread_cond_wait`: 1.03s (11%)

2. **String operations**: 1.56s (17%)
   - `internal/bytealg.IndexByteString`: 1.13s (12%)
   - `internal/stringslite.Index`: 0.43s (4.7%)

3. **Parsing**: 0.51s (5.6%)
   - `parseMmapDataOptimized`: 0.51s

4. **Analysis operations**:
   - `normalizeQuery`: 0.21s (2.3%)
   - `extractDurationAndQuery`: 0.27s (3%)
   - `extractValueAt`: 0.08s (0.9%)

### Per-Analyzer CPU Time (cumulative)

1. **StreamingAnalyzer.Process**: 2.24s (24.7%)
2. **SQLAnalyzer.Process**: 0.58s (6.4%)
3. **LockAnalyzer.Process**: 0.40s (4.4%)
4. **TempFileAnalyzer.Process**: 0.27s (3%)
5. **EventAnalyzer.Process**: 0.23s (2.5%)
6. **ConnectionAnalyzer.Process**: 0.22s (2.4%)
7. **VacuumAnalyzer.Process**: 0.20s (2.2%)
8. **UniqueEntityAnalyzer.Process**: 0.18s (2%)

### strings.Contains Hotspots

**Total**: ~1.11s (12.2% of total CPU)

1. **LockAnalyzer.Process**: ~400ms
   - Line 225: `!strings.Contains(msg, "process ") && !strings.Contains(msg, "deadlock")`
   - Called on EVERY log entry before first lock is seen

2. **EventAnalyzer.Process**: ~230ms
   - Line 96: Loop calling `strings.Contains(msg, eventType)` for each event type
   - Checks 11 event types per entry

3. **ConnectionAnalyzer.Process**: ~220ms
   - Line 78: `strings.Contains(msg, connectionReceived)` (100ms)
   - Line 85: `strings.Contains(msg, disconnection)` (120ms)

4. **CheckpointAnalyzer.Process**: ~80ms
   - Line 107: `strings.Index(msg, "checkpoint")`

5. **ErrorClassAnalyzer.Process**: ~80ms
   - Line 203-204: Manual loop looking for "SQLSTATE"

## Optimization Opportunities

### High Impact (> 100ms potential gain)

1. **LockAnalyzer early filtering** (400ms)
   - Current: Calls Contains on every entry
   - Solution: More selective pre-filter or check message prefix

2. **EventAnalyzer loop** (230ms)
   - Current: Loops through 11 event types with Contains
   - Solution: Check message prefix (events are at start: "ERROR:", "WARNING:", etc.)

3. **ConnectionAnalyzer** (220ms)
   - Current: Two Contains calls per entry
   - Solution: Check distinctive characters or prefixes

### Medium Impact (50-100ms potential gain)

4. **normalizeQuery** (210ms)
   - Called for every SQL query
   - Solution: Cache normalized queries

5. **CheckpointAnalyzer** (80ms)
   - Solution: Check prefix instead of full string search

### Low Impact (< 50ms)

6. **Goroutine synchronization** (4.71s)
   - This is inherent overhead of parallel processing
   - Hard to optimize without major architecture changes

## Target

Reduce user CPU time on I1.log from **7.91s to < 7.0s** (-12% target)
