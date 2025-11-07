# Optimization #1: parseStderrLineBytes Direct Parsing - REJECTED

**Status**: ❌ Rejected (worse performance)
**Date**: 2025-11-08

## Objective
Eliminate 100,004 string allocations in parseStderrLineBytes by parsing directly from bytes.

## Implementation
Modified `parser/mmap_parser.go` to:
- Parse timestamp and message separately from bytes
- Convert only timestamp portion: `string(line[:tzEnd])`
- Convert only message portion: `string(line[i:])`

## Results

### Baseline
- Allocations: 37.4 MB total
- Objects: 123,029
- parseStderrLineBytes: 27.7 MB (100,004 objects)

### After Optimization #1
- Allocations: 38.9 MB total (+1.5 MB, **+4%**)
- Objects: 122,939 (-90, negligible)
- parseStderrFormatBytes: 29.2 MB (110,176 objects) **+10k objects**

### Performance
- Time: 0.34s (same as baseline)
- Max RSS: ~109 MB (similar to baseline)

## Analysis
**Why it failed:**
- Splitting timestamp and message creates **2 allocations** per line instead of 1
- `string(line[:tzEnd])` + `string(line[i:])` > `string(line)`
- Go's string() conversion copies bytes, so we're copying more data overall

**Root cause:**
- `time.Parse()` requires a string, making timestamp conversion unavoidable
- Message must also be a string for LogEntry
- No way to avoid these allocations without changing Go's stdlib

## Conclusion
This optimization is **counterproductive** and has been reverted. The original code with a single `string(line)` conversion is optimal.

## Lesson Learned
When strings are required by APIs (time.Parse, struct fields), splitting byte slices into multiple strings increases allocations rather than reducing them.
