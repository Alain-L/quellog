// Regression tests for the blind-audit fixes in web/js/report-filter.js:
//   #2  — an orphan session (never disconnected) must NOT be counted as a
//         disconnect, even when its last-seen timestamp is < summary.end_date.
//   #16 — a genuine disconnect at the log's final second (e == end_date) MUST
//         be counted; the old `e >= end_date` heuristic dropped it.
//   #7  — the temp-file total must sum each event's exact `size_bytes` integer
//         (not the round-tripped 2-decimal display string) so a full-range
//         filter is a byte-exact no-op on the total.
//   #14 — the peak-concurrent sweep must break ties connect-before-disconnect
//         (matching analysis/connections.go), so two sessions touching on the
//         same second count as overlapping.
//
// DOM-free (node --test). Data is installed via setOriginalReportData(data);
// the filter is applied with applyReportTimeFilter(beginStr, endStr).

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import {
    setOriginalReportData,
    applyReportTimeFilter,
} from '../../js/report-filter.js';
import { fmtBytesFull, parseSizeToBytesStrict } from '../../js/format.js';

// applyReportTimeFilter logs progress/warnings; keep node --test output clean.
function quiet(fn) {
    const log = console.log, warn = console.warn;
    console.log = () => {}; console.warn = () => {};
    try { return fn(); } finally { console.log = log; console.warn = warn; }
}

describe('#2 — orphan sessions are excluded from the disconnection count', () => {
    it('an orphan (orphan:true, last-seen < end_date) is NOT counted', () => {
        const END_DATE = '2025-01-01 12:00:00';
        setOriginalReportData({
            summary: { start_date: '2025-01-01 09:00:00', end_date: END_DATE, duration: '3h0m0s' },
            connections: {
                connection_count: 3, connections: [],
                disconnection_count: 2, // backend: only the 2 real disconnects
                session_events: [
                    { s: '2025-01-01 09:30:00', e: '2025-01-01 10:00:00', d: 1800000, u: 0, db: 0, h: 0 },
                    { s: '2025-01-01 10:00:00', e: '2025-01-01 10:30:00', d: 1800000, u: 0, db: 0, h: 0 },
                    // ORPHAN: last-seen 11:00 < end_date 12:00, flushed at Finalize.
                    { s: '2025-01-01 09:00:00', e: '2025-01-01 11:00:00', d: 7200000, orphan: true, u: 0, db: 0, h: 0 },
                ],
            },
        });

        const out = quiet(() => applyReportTimeFilter('2025-01-01 09:00:00', END_DATE));
        // Fixed: matches the backend (orphan excluded), not inflated to 3.
        assert.equal(out.connections.disconnection_count, 2);
        assert.equal(out.connections.session_stats.count, 2);
    });
});

describe('#16 — a genuine final-second disconnect is counted', () => {
    it('a real disconnect at e == end_date (no orphan flag) is kept', () => {
        const END_DATE = '2025-01-01 12:00:00';
        setOriginalReportData({
            summary: { start_date: '2025-01-01 11:00:00', end_date: END_DATE, duration: '1h0m0s' },
            connections: {
                connection_count: 2, connections: [],
                disconnection_count: 2, // backend: both are real disconnects
                session_events: [
                    { s: '2025-01-01 11:00:00', e: '2025-01-01 11:30:00', d: 1800000, u: 0, db: 0, h: 0 },
                    // GENUINE disconnect at the final second (e == end_date), NOT an orphan.
                    { s: '2025-01-01 11:15:00', e: END_DATE, d: 2700000, u: 0, db: 0, h: 0 },
                ],
            },
        });

        const out = quiet(() => applyReportTimeFilter('2025-01-01 11:00:00', END_DATE));
        // Fixed: the final-second disconnect is no longer dropped.
        assert.equal(out.connections.disconnection_count, 2);
        assert.equal(out.connections.session_stats.count, 2);
    });

    it('an orphan at e == end_date is still excluded (flag wins over timestamp)', () => {
        const END_DATE = '2025-01-01 12:00:00';
        setOriginalReportData({
            summary: { start_date: '2025-01-01 11:00:00', end_date: END_DATE, duration: '1h0m0s' },
            connections: {
                connection_count: 2, connections: [],
                disconnection_count: 1,
                session_events: [
                    { s: '2025-01-01 11:00:00', e: '2025-01-01 11:30:00', d: 1800000, u: 0, db: 0, h: 0 },
                    // Orphan flushed exactly at end_date — the flag, not the ts, decides.
                    { s: '2025-01-01 11:15:00', e: END_DATE, d: 2700000, orphan: true, u: 0, db: 0, h: 0 },
                ],
            },
        });

        const out = quiet(() => applyReportTimeFilter('2025-01-01 11:00:00', END_DATE));
        assert.equal(out.connections.disconnection_count, 1);
    });
});

describe('#7 — temp-file total sums exact size_bytes, not the display string', () => {
    it('a full-range filter is a byte-exact no-op on total_size', () => {
        const N = 200;
        const TRUE_BYTES = 1029; // 1029/1024 = 1.0048.. -> FormatBytes -> "1.00 KB"
        const displayed = fmtBytesFull(TRUE_BYTES);         // "1.00 KB"
        const reparsed = parseSizeToBytesStrict(displayed); // 1024 (lossy)
        assert.notEqual(reparsed, TRUE_BYTES);              // guard: the drift exists

        const backendExact = fmtBytesFull(TRUE_BYTES * N);  // byte-exact header
        const roundTripped = fmtBytesFull(reparsed * N);    // what the old code produced

        const events = [];
        for (let i = 0; i < N; i++) {
            const p = n => String(n).padStart(2, '0');
            events.push({
                timestamp: `2025-01-01 10:${p(Math.floor(i / 4))}:${p((i % 4) * 15)}`,
                size: displayed,
                size_bytes: TRUE_BYTES,
            });
        }

        setOriginalReportData({
            summary: { start_date: '2025-01-01 09:00:00', end_date: '2025-01-01 12:00:00', duration: '3h0m0s' },
            temp_files: { total_messages: N, total_size: backendExact, avg_size: displayed, max_size: displayed, events },
        });

        const out = quiet(() => applyReportTimeFilter('2025-01-01 09:00:00', '2025-01-01 12:00:00'));
        // Fixed: total equals the byte-exact backend sum, not the drifted round-trip.
        assert.equal(out.temp_files.total_size, backendExact);
        assert.notEqual(out.temp_files.total_size, roundTripped);
    });

    it('falls back to the display string when size_bytes is absent (legacy payload)', () => {
        setOriginalReportData({
            summary: { start_date: '2025-01-01 09:00:00', end_date: '2025-01-01 12:00:00', duration: '3h0m0s' },
            temp_files: {
                total_messages: 2,
                events: [
                    { timestamp: '2025-01-01 10:00:00', size: '1 KB' },   // no size_bytes
                    { timestamp: '2025-01-01 10:30:00', size: '3072' },  // unitless -> bytes
                ],
            },
        });
        const out = quiet(() => applyReportTimeFilter('2025-01-01 09:00:00', '2025-01-01 12:00:00'));
        // 1024 + 3072 = 4096 B via the string fallback.
        assert.equal(out.temp_files.total_size, '4.00 KB');
    });
});

describe('#14 — peak-concurrent sweep ties break connect-before-disconnect', () => {
    it('two sessions touching on the same second count as overlapping (peak 2)', () => {
        const BEGIN = '2025-01-01 10:00:00', END = '2025-01-01 11:00:00';
        setOriginalReportData({
            summary: { start_date: BEGIN, end_date: END, duration: '1h0m0s' },
            connections: {
                connection_count: 0, connections: [],
                session_events: [
                    // A ends exactly when B begins (10:30:00).
                    { s: '2025-01-01 10:00:00', e: '2025-01-01 10:30:00', d: 1800000, u: 0, db: 0, h: 0 },
                    { s: '2025-01-01 10:30:00', e: '2025-01-01 11:00:00', d: 1800000, u: 0, db: 0, h: 0 },
                ],
            },
        });

        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        // Fixed: matches the backend's starts-before-ends peak (2), not 1.
        assert.equal(out.connections.peak_concurrent_sessions, 2);
    });
});
