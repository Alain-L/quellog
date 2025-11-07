# Optimization Reports

This directory contains detailed reports from the performance optimization campaign on the `perf` branch.

## Quick Summary

Read **[FINAL_OPTIMIZATIONS_SUMMARY.md](FINAL_OPTIMIZATIONS_SUMMARY.md)** for the complete overview.

**Key results**:
- **B.log (54 MB)**: -11% CPU, -27% RSS, -58% peak memory
- **I1.log (1 GB)**: -8% CPU, stable memory

## Report Structure

### Phase 1: Memory Optimizations

1. **[optimization_baseline.md](optimization_baseline.md)** - Initial memory profile
2. **[optimization_01_rejected.md](optimization_01_rejected.md)** - Why parseStderrLineBytes failed (+4% allocations)
3. **[optimization_02_accepted.md](optimization_02_accepted.md)** ✅ parsePostgreSQLDuration (-5% allocations)
4. **[optimization_03_rejected.md](optimization_03_rejected.md)** - Why strings.Contains pre-check failed
5. **[optimization_04_accepted.md](optimization_04_accepted.md)** ✅ Channel buffers (-27% RSS, -58% peak)
6. **[optimization_final_report.md](optimization_final_report.md)** - Phase 1 complete analysis

### Phase 2: CPU Optimizations

7. **[cpu_baseline.md](cpu_baseline.md)** - CPU profiling analysis
8. **[cpu_optimizations_report.md](cpu_optimizations_report.md)** - All 3 CPU optimizations (-8% to -11% CPU)

### Final Report

9. **[FINAL_OPTIMIZATIONS_SUMMARY.md](FINAL_OPTIMIZATIONS_SUMMARY.md)** ⭐ **START HERE** - Complete summary with methodology and results

## Methodology

All optimizations followed a rigorous process:
1. **Profile** with pprof and /usr/bin/time -l
2. **Identify** hotspots and optimization opportunities
3. **Implement** targeted changes
4. **Benchmark** with multiple runs on B.log (54MB) and I1.log (1GB)
5. **Test** with go test ./...
6. **Accept** only if measurable improvement
7. **Document** both successes and failures

## Benchmarking Commands

### Memory profiling
```bash
MEMPROFILE=/tmp/mem.prof bin/quellog I1.log > /dev/null
go tool pprof -top -alloc_space /tmp/mem.prof
go tool pprof -top -alloc_objects /tmp/mem.prof
```

### CPU profiling
```bash
CPUPROFILE=/tmp/cpu.prof bin/quellog I1.log > /dev/null
go tool pprof -top /tmp/cpu.prof
go tool pprof -top -cum /tmp/cpu.prof
```

### Time & memory usage
```bash
/usr/bin/time -l bin/quellog I1.log > /dev/null
```

## Optimizations Applied

### Memory (2 accepted, 2 rejected)
- ✅ parsePostgreSQLDuration: Manual parsing instead of strings.Split
- ✅ Channel buffers: 24576 → 4096 entries
- ❌ parseStderrLineBytes: Increased allocations (counterproductive)
- ❌ strings.Contains pre-check: No measurable gain

### CPU (3 accepted, all effective)
- ✅ LockAnalyzer: Single Index call instead of dual Contains
- ✅ EventAnalyzer: Check only first 50 chars for event types
- ✅ ConnectionAnalyzer: Single-pass scan for connection patterns

## Test Files

- **B.log**: 54 MB, ~100k log entries - Small file representative
- **I1.log**: 1 GB, ~1.8M log entries - Large file for profiling

## Key Learnings

1. **Rejected optimizations teach too**: Document failures to avoid repeating them
2. **Profile before optimizing**: Don't guess hotspots
3. **Measure each change**: Intuition can be wrong
4. **Domain knowledge helps**: Event types at start → prefix matching
5. **Single-pass wins**: One scan beats multiple scans

## Contact

For questions about these optimizations, see the commit history on branch `perf`:
```bash
git log --oneline --graph perf
```

Or read the detailed reports linked above.
