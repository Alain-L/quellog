#!/usr/bin/env node
// Benchmark WASM memory usage with TinyGo
// Usage: node bench_wasm_memory.js <logfile> [string|bytes|both]

const fs = require('fs');
const path = require('path');

// Load TinyGo WASM runtime
require('./wasm_exec_tiny.js');

const logFile = process.argv[2];
const mode = process.argv[3] || 'both';

if (!logFile) {
    console.error('Usage: node bench_wasm_memory.js <logfile> [string|bytes|both]');
    process.exit(1);
}

function formatBytes(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(2) + ' KB';
    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(2) + ' MB';
    return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
}

function getMemoryUsage() {
    const usage = process.memoryUsage();
    return {
        heapUsed: usage.heapUsed,
        heapTotal: usage.heapTotal,
        external: usage.external,
        rss: usage.rss,
        arrayBuffers: usage.arrayBuffers || 0
    };
}

function printMemory(label, mem) {
    console.log(`  ${label}:`);
    console.log(`    Heap Used:     ${formatBytes(mem.heapUsed)}`);
    console.log(`    RSS:           ${formatBytes(mem.rss)}`);
}

function getWasmMemorySize(instance) {
    if (instance && instance.exports && instance.exports.memory) {
        return instance.exports.memory.buffer.byteLength;
    }
    return 0;
}

async function runBenchmark(wasmBuffer, logContentString, logContentBytes, fileSize, testMode) {
    if (global.gc) global.gc();
    await new Promise(r => setTimeout(r, 100));

    const memBefore = getMemoryUsage();

    // Load fresh WASM instance
    const go = new Go();
    const { instance } = await WebAssembly.instantiate(wasmBuffer, go.importObject);
    go.run(instance);

    const wasmMemBefore = getWasmMemorySize(instance);

    // Parse
    const t0 = Date.now();
    let result;

    if (testMode === 'string') {
        result = global.quellogParse(logContentString);
    } else {
        result = global.quellogParseBytes(logContentBytes);
    }

    const parseTime = Date.now() - t0;
    const memAfter = getMemoryUsage();
    const wasmMemAfter = getWasmMemorySize(instance);

    // Validate result
    let entries = 'N/A';
    try {
        const parsed = JSON.parse(result);
        entries = parsed.meta?.entries || 'N/A';
    } catch (e) {
        console.error('  Invalid JSON result');
    }

    return {
        mode: testMode,
        parseTime,
        entries,
        resultSize: result.length,
        heapGrowth: memAfter.heapUsed - memBefore.heapUsed,
        rssGrowth: memAfter.rss - memBefore.rss,
        wasmMemBefore,
        wasmMemAfter,
        wasmGrowth: wasmMemAfter - wasmMemBefore
    };
}

async function benchmark() {
    console.log('=== WASM Memory Benchmark ===\n');

    const stats = fs.statSync(logFile);
    console.log(`Log file: ${logFile}`);
    console.log(`File size: ${formatBytes(stats.size)}`);
    console.log(`Mode: ${mode}\n`);

    // Load WASM binary once
    const wasmPath = path.join(__dirname, 'quellog_tiny.wasm');
    const wasmBuffer = fs.readFileSync(wasmPath);
    console.log(`WASM binary: ${formatBytes(wasmBuffer.length)}`);

    // Read file in both formats
    console.log('Reading log file...');
    const logContentString = fs.readFileSync(logFile, 'utf-8');
    const logContentBytes = new Uint8Array(fs.readFileSync(logFile));
    console.log(`  String length: ${logContentString.length}`);
    console.log(`  Bytes length:  ${logContentBytes.length}\n`);

    const results = [];

    if (mode === 'string' || mode === 'both') {
        console.log('--- Testing quellogParse (string) ---');
        const r = await runBenchmark(wasmBuffer, logContentString, logContentBytes, stats.size, 'string');
        results.push(r);
        console.log(`  Parse time:    ${(r.parseTime / 1000).toFixed(2)}s`);
        console.log(`  Entries:       ${r.entries}`);
        console.log(`  WASM Memory:   ${formatBytes(r.wasmMemBefore)} → ${formatBytes(r.wasmMemAfter)}`);
        console.log(`  RSS growth:    +${formatBytes(r.rssGrowth)}`);
        console.log();
    }

    if (mode === 'bytes' || mode === 'both') {
        console.log('--- Testing quellogParseBytes (Uint8Array) ---');
        const r = await runBenchmark(wasmBuffer, logContentString, logContentBytes, stats.size, 'bytes');
        results.push(r);
        console.log(`  Parse time:    ${(r.parseTime / 1000).toFixed(2)}s`);
        console.log(`  Entries:       ${r.entries}`);
        console.log(`  WASM Memory:   ${formatBytes(r.wasmMemBefore)} → ${formatBytes(r.wasmMemAfter)}`);
        console.log(`  RSS growth:    +${formatBytes(r.rssGrowth)}`);
        console.log();
    }

    // Comparison
    if (results.length === 2) {
        console.log('=== Comparison ===');
        const [str, bytes] = results;
        const wasmSaved = str.wasmMemAfter - bytes.wasmMemAfter;
        const timeSaved = str.parseTime - bytes.parseTime;
        console.log(`  WASM Memory saved: ${formatBytes(wasmSaved)} (${((wasmSaved / str.wasmMemAfter) * 100).toFixed(1)}%)`);
        console.log(`  Time saved:        ${(timeSaved / 1000).toFixed(2)}s (${((timeSaved / str.parseTime) * 100).toFixed(1)}%)`);
    }
}

benchmark().catch(err => {
    console.error('Benchmark failed:', err);
    process.exit(1);
});
