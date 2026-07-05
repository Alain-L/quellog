// Unit tests for web/js/report-filter.js — black-box through its 4 exports.
// The synthetic dataset mirrors the shape consumed by applyReportTimeFilter:
// summary, sql_performance.executions, temp_files.events, checkpoints.events
// (+ types / wal_distances / warning_events), connections.connections
// (+ session_events with s/e fields).
//
// All timestamps use the Go text format "YYYY-MM-DD HH:MM:SS"; the module
// parses both filter bounds and event timestamps with the same
// new Date('...T...') local-time path, so results are timezone-independent.

import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert/strict';

import {
    setOriginalReportData,
    getOriginalReportData,
    applyReportTimeFilter,
    resetReportTimeFilter
} from '../../js/report-filter.js';

// applyReportTimeFilter logs progress/warnings; keep the test output clean.
function quiet(fn) {
    const origLog = console.log;
    const origWarn = console.warn;
    console.log = () => {};
    console.warn = () => {};
    try {
        return fn();
    } finally {
        console.log = origLog;
        console.warn = origWarn;
    }
}

function makeDataset() {
    return {
        summary: {
            start_date: '2025-01-01 09:00:00',
            end_date: '2025-01-01 23:59:00',
            duration: '14h59m0s'
        },
        sql_performance: {
            total_queries_parsed: 5,
            total_unique_queries: 3,
            executions: [
                { timestamp: '2025-01-01 10:15:00', duration_ms: 100, query_id: 'q1' },
                { timestamp: '2025-01-01 10:30:00', duration_ms: 300, query_id: 'q1' },
                { timestamp: '2025-01-01 10:45:00', duration_ms: 500, query_id: 'q2' },
                { timestamp: '2025-01-01 10:50:00', duration_ms: 200 }, // no query_id
                { timestamp: '2025-01-01 12:00:00', duration_ms: 9999, query_id: 'q3' } // outside
            ],
            queries: [
                { id: 'q1', query: 'SELECT 1', count: 2, total_time_ms: 400, avg_time_ms: 200, max_time_ms: 300 },
                { id: 'q2', query: 'SELECT 2', count: 1, total_time_ms: 500, avg_time_ms: 500, max_time_ms: 500 },
                { id: 'q3', query: 'SELECT 3', count: 1, total_time_ms: 9999, avg_time_ms: 9999, max_time_ms: 9999 }
            ]
        },
        temp_files: {
            total_messages: 3,
            events: [
                { timestamp: '2025-01-01 10:10:00', size: '1 KB' },
                { timestamp: '2025-01-01 10:20:00', size: '3072' }, // unitless -> bytes
                { timestamp: '2025-01-01 23:00:00', size: '1 GB' }  // outside
            ]
        },
        checkpoints: {
            total_checkpoints: 3,
            events: [
                '2025-01-01 10:05:00',
                '2025-01-01 10:35:00',
                '2025-01-01 11:30:00' // outside
            ],
            types: {
                time: { count: 2, events: ['2025-01-01 10:05:00', '2025-01-01 11:30:00'] },
                wal: { count: 1, events: ['2025-01-01 10:35:00'] },
                force: { count: 1, events: ['2025-01-01 23:00:00'] }
            },
            wal_distances: [
                { timestamp: '2025-01-01 10:05:00', mb: 12 },
                { timestamp: '2025-01-01 12:00:00', mb: 5 } // outside
            ],
            warning_events: [
                { timestamp: '2025-01-01 10:40:00', message: 'checkpoints too frequent' },
                { timestamp: '2025-01-01 09:00:00', message: 'early' } // outside
            ]
        },
        connections: {
            connection_count: 4,
            connections: [
                '2025-01-01 10:00:00', // exactly at begin (inclusive)
                '2025-01-01 10:30:00',
                '2025-01-01 11:00:00', // exactly at end (inclusive)
                '2025-01-01 11:00:01'  // one second past
            ],
            session_events: [
                { s: '2025-01-01 09:00:00', e: '2025-01-01 09:30:00' }, // ends before window
                { s: '2025-01-01 09:30:00', e: '2025-01-01 10:30:00' }, // overlaps start
                { s: '2025-01-01 10:15:00', e: '2025-01-01 10:45:00' }, // fully inside
                { s: '2025-01-01 09:00:00', e: '2025-01-01 12:00:00' }, // spans the window
                { s: '2025-01-01 11:30:00', e: '2025-01-01 12:00:00' }, // starts after window
                { s: '2025-01-01 10:20:00', e: null }                    // missing end
            ]
        },
        // A section the filter does not know about must pass through untouched.
        locks: { total_waits: 7 }
    };
}

const BEGIN = '2025-01-01 10:00:00';
const END = '2025-01-01 11:00:00';

describe('before any data is stored', () => {
    it('applyReportTimeFilter returns null and getOriginalReportData is null', () => {
        // Runs first: module-level originalData has not been set yet.
        assert.equal(getOriginalReportData(), null);
        assert.equal(quiet(() => applyReportTimeFilter(BEGIN, END)), null);
        assert.equal(resetReportTimeFilter(), null);
    });
});

describe('setOriginalReportData / getOriginalReportData', () => {
    it('deep-clones the input (later caller mutations are invisible)', () => {
        const data = makeDataset();
        setOriginalReportData(data);
        data.summary.start_date = 'MUTATED';
        data.checkpoints.events.push('MUTATED');
        const stored = getOriginalReportData();
        assert.equal(stored.summary.start_date, '2025-01-01 09:00:00');
        assert.equal(stored.checkpoints.events.length, 3);
        assert.notEqual(stored, data);
    });

    it('resetReportTimeFilter returns the stored original object', () => {
        setOriginalReportData(makeDataset());
        assert.equal(resetReportTimeFilter(), getOriginalReportData());
    });
});

describe('applyReportTimeFilter', () => {
    beforeEach(() => {
        setOriginalReportData(makeDataset());
    });

    it('returns the original data on an invalid range', () => {
        const out = quiet(() => applyReportTimeFilter('', END));
        assert.equal(out, getOriginalReportData());
        const out2 = quiet(() => applyReportTimeFilter(BEGIN, null));
        assert.equal(out2, getOriginalReportData());
    });

    it('does not mutate the stored original', () => {
        quiet(() => applyReportTimeFilter(BEGIN, END));
        assert.deepEqual(getOriginalReportData(), makeDataset());
    });

    it('updates the summary window and recomputed duration', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        assert.equal(out.summary.start_date, BEGIN);
        assert.equal(out.summary.end_date, END);
        assert.equal(out.summary.duration, '1h0m0s');
        assert.equal(out._timeFiltered, true);
        assert.deepEqual(out._filterRange, { begin: BEGIN, end: END });
    });

    it('leaves unknown sections untouched', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        assert.deepEqual(out.locks, { total_waits: 7 });
    });

    it('slices sql executions and re-aggregates the stats', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const sql = out.sql_performance;

        // 4 of 5 executions are inside [10:00, 11:00]; q3 (12:00) is dropped.
        assert.equal(sql.executions.length, 4);
        assert.equal(sql.total_queries_parsed, 4);
        // The no-query_id execution counts in totals but not unique queries.
        assert.equal(sql.total_unique_queries, 2);

        // durations kept: [100, 300, 500, 200] -> sorted [100, 200, 300, 500].
        // Formatted with fmtQueryDuration (backend parity): sub-second -> "N ms".
        assert.equal(sql.query_min_duration, '100 ms');
        assert.equal(sql.query_max_duration, '500 ms');
        assert.equal(sql.query_median_duration, '250 ms'); // (200+300)/2
        assert.equal(sql.query_99th_percentile, '500 ms'); // idx floor(4*0.99)=3
        assert.equal(sql.top_1_percent_slow_queries, 1);    // only the 500ms one

        // stats.total is in ms; 1100ms -> "1.10 s" (fmtQueryDuration second tier).
        assert.equal(sql.total_query_duration, '1.10 s');
    });

    it('re-aggregates per-query stats and drops out-of-range queries', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const queries = out.sql_performance.queries;
        assert.equal(queries.length, 2); // q3 has count 0 -> filtered out

        const q1 = queries.find(q => q.id === 'q1');
        assert.deepEqual(
            { count: q1.count, total: q1.total_time_ms, avg: q1.avg_time_ms, max: q1.max_time_ms },
            { count: 2, total: 400, avg: 200, max: 300 }
        );
        assert.equal(q1.query, 'SELECT 1'); // untouched fields preserved

        const q2 = queries.find(q => q.id === 'q2');
        assert.deepEqual(
            { count: q2.count, total: q2.total_time_ms, avg: q2.avg_time_ms, max: q2.max_time_ms },
            { count: 1, total: 500, avg: 500, max: 500 }
        );
    });

    it('slices temp file events and recomputes sizes', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const tf = out.temp_files;
        assert.equal(tf.events.length, 2); // the 1 GB event at 23:00 is out
        assert.equal(tf.total_messages, 2);
        // 1 KB (1024 B) + "3072" (unitless -> 3072 B) = 4096 B. fmtBytesFull
        // (backend parity) keeps 2 decimals; max is recomputed (was left stale).
        assert.equal(tf.total_size, '4.00 KB');
        assert.equal(tf.avg_size, '2.00 KB');
        assert.equal(tf.max_size, '3.00 KB'); // max(1024, 3072) B
    });

    it('reports 0 B totals when no temp file event survives', () => {
        const out = quiet(() =>
            applyReportTimeFilter('2025-01-01 20:00:00', '2025-01-01 21:00:00'));
        const tf = out.temp_files;
        assert.equal(tf.total_messages, 0);
        assert.equal(tf.total_size, '0 B');
        assert.equal(tf.avg_size, '0 B');
    });

    it('slices checkpoint events (plain timestamp strings)', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const cp = out.checkpoints;
        assert.deepEqual(cp.events, ['2025-01-01 10:05:00', '2025-01-01 10:35:00']);
        assert.equal(cp.total_checkpoints, 2);
    });

    it('re-aggregates checkpoint types and drops empty ones', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const types = out.checkpoints.types;

        // 'force' has no event in the window -> removed entirely.
        assert.deepEqual(Object.keys(types).sort(), ['time', 'wal']);

        // Window is exactly 1h; 2 checkpoints total in the window.
        assert.equal(types.time.count, 1);
        assert.equal(types.time.percentage, 50);
        assert.equal(types.time.rate_per_hour, 1);
        assert.deepEqual(types.time.events, ['2025-01-01 10:05:00']);

        assert.equal(types.wal.count, 1);
        assert.equal(types.wal.percentage, 50);
        assert.equal(types.wal.rate_per_hour, 1);
    });

    it('filters wal_distances and warning_events by timestamp', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const cp = out.checkpoints;
        assert.equal(cp.wal_distances.length, 1);
        assert.equal(cp.wal_distances[0].mb, 12);
        assert.equal(cp.warning_events.length, 1);
        assert.equal(cp.warning_events[0].message, 'checkpoints too frequent');
        // warning_count is recomputed to match the filtered events (was stale).
        assert.equal(cp.warning_count, 1);
    });

    it('slices connections with inclusive bounds and recomputes the rate', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const cn = out.connections;
        // 10:00:00 and 11:00:00 are kept (>= begin, <= end); 11:00:01 dropped.
        assert.deepEqual(cn.connections, [
            '2025-01-01 10:00:00',
            '2025-01-01 10:30:00',
            '2025-01-01 11:00:00'
        ]);
        assert.equal(cn.connection_count, 3);
        assert.equal(cn.avg_connections_per_hour, '3.00');
    });

    it('keeps session events overlapping the window, drops the rest', () => {
        const out = quiet(() => applyReportTimeFilter(BEGIN, END));
        const kept = out.connections.session_events;
        // Overlap rule: start <= end-of-window AND end >= start-of-window.
        assert.equal(kept.length, 3);
        assert.deepEqual(kept.map(ev => ev.s), [
            '2025-01-01 09:30:00', // overlaps start
            '2025-01-01 10:15:00', // fully inside
            '2025-01-01 09:00:00'  // spans the whole window
        ]);
        // The session with e: null was dropped (both bounds required).
    });

    it('handles a dataset with only a summary section', () => {
        setOriginalReportData({
            summary: {
                start_date: '2025-01-01 00:00:00',
                end_date: '2025-01-02 00:00:00',
                duration: '24h0m0s'
            }
        });
        const out = quiet(() =>
            applyReportTimeFilter('2025-01-01 06:00:00', '2025-01-01 07:30:15'));
        assert.equal(out.summary.duration, '1h30m15s');
        assert.equal(out._timeFiltered, true);
        assert.equal(out.sql_performance, undefined);
    });
});
