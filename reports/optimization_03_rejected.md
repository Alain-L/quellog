# Optimization #3: Replace strings.Contains with IndexByte - REJECTED

**Status**: ❌ Rejected (no measurable gain, potentially worse)
**Date**: 2025-11-08

## Objective
Reduce CPU time in analyzers by checking for a single character with IndexByte before calling strings.Contains.

## Implementation Attempted
Modified `analysis/connections.go`:
```go
// Before
if strings.Contains(msg, "connection received") { ... }

// After (attempted)
if strings.IndexByte(msg, 'c') >= 0 && strings.Contains(msg, "connection received") { ... }
```

## Results
- **Time**: 0.34-0.35s (same as baseline)
- **No measurable improvement**

## Analysis
**Why it failed:**

1. **Double work for common characters**:
   - Characters like 'c' and 'd' appear frequently in log messages
   - If IndexByte(msg, 'c') finds 'c' but Contains(..., "connection received") fails, we've done two searches instead of one
   - Contains() already has optimized Boyer-Moore-like algorithms for longer patterns

2. **strings.Contains is already optimized**:
   - Go's stdlib uses efficient algorithms for substring search
   - Adding an extra check only helps if the early-reject rate is very high
   - Single-character checks don't provide enough selectivity

3. **Best case is minimal**:
   - Even if IndexByte saves work, it's a minor optimization compared to other operations
   - The parsing and analysis logic dominates CPU time, not pattern matching

## Conclusion
This optimization is **not beneficial** and has been rejected. The original code with direct `strings.Contains` calls is already well-optimized.

## Lesson Learned
Micro-optimizations like adding character checks before Contains() only help when:
1. The checked character has high selectivity (rare in the input)
2. The full Contains() call is expensive relative to the check
3. The pattern appears in hot paths with millions of calls

For connection/disconnection events (~10k-20k calls on B.log), the overhead of an extra check likely outweighs any benefit.
