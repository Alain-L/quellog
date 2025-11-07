# Optimization Baseline Report

**Date**: 2025-11-08
**File**: B.log (54 MB)
**Branch**: perf
**Commit**: (baseline before optimizations)

## Baseline Performance

### Execution Time (3 runs)
- Run 1 (cold cache): 0.55s real, 0.50s user, 0.12s sys
- Run 2: 0.34s real, 0.49s user, 0.09s sys
- Run 3: 0.34s real, 0.49s user, 0.09s sys
- **Average (warm cache)**: 0.34s real, 0.49s user

### Memory Usage
- Maximum RSS: ~113 MB
- Peak footprint: ~55 MB

### Allocations
- **Total allocated**: 37.4 MB
- **Total objects**: 123,029

### Allocation Hotspots
1. `parseStderrLineBytes`: 27.7 MB (73.93%) - 100,004 objects (81.28%)
2. `ConnectionAnalyzer.Process`: 1.7 MB (4.60%) - 10,923 objects via strings.Split
3. `NewTempFileAnalyzer`: 1.0 MB (2.78%)
4. Various runtime: ~6 MB (16%)

## Target Optimizations

### #1: parseStderrLineBytes - Direct byte parsing
**Target**: Eliminate 100,004 string allocations (27.7 MB)
**Expected gain**: -70% allocations, -20-30% CPU time

### #2: parsePostgreSQLDuration - Manual parsing
**Target**: Eliminate 10,923 strings.Split allocations (1.7 MB)
**Expected gain**: -4.6% allocations

### #3: strings.Contains → IndexByte
**Target**: Reduce CPU time in analyzers
**Expected gain**: -5-10% CPU time

### #4: Channel buffer reduction
**Target**: Reduce buffer from 24576 to 4096
**Expected gain**: -3 MB memory, better cache locality

## Success Criteria
- Optimization applied only if measurable gain
- All tests must pass (go test ./...)
- Each optimization committed separately
