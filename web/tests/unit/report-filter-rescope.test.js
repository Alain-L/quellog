// Regression for R3 (MEDIUM cluster): applyReportTimeFilter used to re-scope
// only sql_performance / temp_files(header) / checkpoints / connections, leaving
// several sections at FULL-LOG next to the scoped cards. This asserts the FIXED
// behaviour: every section with a re-scopable per-item timestamp now re-scopes
// under a filter, and the sections that genuinely cannot (no per-item timestamp
// in the payload) are flagged whole-log via `_wholeLog` so the renderers can
// annotate them (see web/js/sections/*.js wholeLogBadge() calls).
//
// DOM-free (node --test). Signature is applyReportTimeFilter(beginStr, endStr);
// the payload is installed via setOriginalReportData(data). top_events carry
// Unix-ms occurrence timestamps (UTC wall-clock, per the backend) so they are
// built with Date.UTC and the module filters them against UTC bounds — the
// whole test is timezone-stable.

import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert/strict';

import {
    setOriginalReportData,
    applyReportTimeFilter,
} from '../../js/report-filter.js';

// applyReportTimeFilter logs progress/warnings; keep node --test output clean.
function quiet(fn) {
    const log = console.log, warn = console.warn;
    console.log = () => {}; console.warn = () => {};
    try { return fn(); } finally { console.log = log; console.warn = warn; }
}

// Full log spans 12h (00:00 -> 12:00); we filter to the FIRST HALF so exactly
// half of each timestamped activity is dropped.
const FULL_BEGIN = '2025-01-01 00:00:00';
const FULL_END   = '2025-01-01 12:00:00';
const MID        = '2025-01-01 06:00:00';

// UTC epoch-ms for a "YYYY-MM-DD HH:00:00" wall-clock on 2025-01-01.
const utc = (h, m = 0) => Date.UTC(2025, 0, 1, h, m, 0);

function makeDataset() {
    return {
        summary: { start_date: FULL_BEGIN, end_date: FULL_END, duration: '12h0m0s' },

        // Severity distribution: population counts, NO per-item timestamps.
        events: [
            { type: 'LOG', count: 100, percentage: 95.24 },
            { type: 'ERROR', count: 5, percentage: 4.76 },
        ],

        // Two top events. The ERROR straddles the split; the WARNING is entirely
        // in the second half so it must be dropped under a first-half filter.
        top_events: [
            { id: 'er-1', message: 'deadlock detected', severity: 'ERROR', count: 4,
              timestamps: [utc(1), utc(2), utc(8), utc(9)] },
            { id: 'wa-1', message: 'slow fsync', severity: 'WARNING', count: 2,
              timestamps: [utc(7), utc(8)] },
        ],

        // Headline query mix — categories + types re-scopable via executions;
        // by_database is whole-log (no per-execution dimension).
        sql_overview: {
            total_queries: 8,
            categories: [{ category: 'DML', count: 8, percentage: 100, total_time: '3.60 s' }],
            types: [
                { type: 'SELECT', category: 'DML', count: 6, percentage: 75,
                  total_time: '2.40 s', avg_time: '400 ms', max_time: '700 ms' },
                { type: 'INSERT', category: 'DML', count: 2, percentage: 25,
                  total_time: '1.20 s', avg_time: '600 ms', max_time: '800 ms' },
            ],
            by_database: [{ name: 'app', count: 8, total_time: '3.60 s',
                query_types: [{ type: 'SELECT', count: 6, total_time: '2.40 s' }] }],
        },

        sql_performance: {
            total_queries_parsed: 8,
            total_unique_queries: 3,
            executions: [
                { timestamp: '2025-01-01T01:00:00', duration_ms: 100, query_id: 'q1' }, // 1st SELECT
                { timestamp: '2025-01-01T02:00:00', duration_ms: 200, query_id: 'q1' }, // 1st SELECT
                { timestamp: '2025-01-01T03:00:00', duration_ms: 300, query_id: 'q2' }, // 1st SELECT
                { timestamp: '2025-01-01T04:00:00', duration_ms: 400, query_id: 'i1' }, // 1st INSERT
                { timestamp: '2025-01-01T07:00:00', duration_ms: 500, query_id: 'q1' }, // 2nd
                { timestamp: '2025-01-01T08:00:00', duration_ms: 600, query_id: 'q1' }, // 2nd
                { timestamp: '2025-01-01T09:00:00', duration_ms: 700, query_id: 'q2' }, // 2nd
                { timestamp: '2025-01-01T10:00:00', duration_ms: 800, query_id: 'i1' }, // 2nd
            ],
            queries: [
                { id: 'q1', normalized_query: 'SELECT 1', type: 'SELECT', count: 4, total_time_ms: 1400, avg_time_ms: 350, max_time_ms: 600 },
                { id: 'q2', normalized_query: 'SELECT 2', type: 'SELECT', count: 2, total_time_ms: 1000, avg_time_ms: 500, max_time_ms: 700 },
                { id: 'i1', normalized_query: 'INSERT x', type: 'INSERT', count: 2, total_time_ms: 1200, avg_time_ms: 600, max_time_ms: 800 },
            ],
        },

        temp_files: {
            total_messages: 4,
            total_size: '6.00 KB',
            avg_size: '1.50 KB',
            max_size: '2.00 KB',
            events: [
                { timestamp: '2025-01-01 01:30:00', size: '1 KB', query_id: 't1' }, // 1st
                { timestamp: '2025-01-01 02:30:00', size: '1 KB', query_id: 't1' }, // 1st
                { timestamp: '2025-01-01 07:30:00', size: '2 KB', query_id: 't2' }, // 2nd
                { timestamp: '2025-01-01 08:30:00', size: '2 KB', query_id: 't2' }, // 2nd
            ],
            queries: [
                { id: 't1', normalized_query: 'SELECT big', raw_query: 'SELECT big',
                  count: 2, total_size: '2.00 KB', min_size: '1.00 KB', max_size: '1.00 KB', avg_size: '1.00 KB' },
                { id: 't2', normalized_query: 'SELECT huge', raw_query: 'SELECT huge',
                  count: 2, total_size: '4.00 KB', min_size: '2.00 KB', max_size: '2.00 KB', avg_size: '2.00 KB' },
            ],
        },

        // Not re-scopable (no clean per-item timestamp / needs backend dedup).
        locks: {
            total_events: 6, waiting_events: 2, acquired_events: 4, deadlock_events: 0,
            total_wait_time: '3.00 s', avg_wait_time: '500 ms', lock_type_stats: { relation: 6 },
        },
        maintenance: {
            vacuum_count: 4, aggressive_vacuum_count: 1, analyze_count: 2,
            total_vacuum_elapsed_seconds: 40, total_tuples_removed: 1000,
        },
    };
}

describe('R3 fix — sections re-scope or are flagged whole-log under a time filter', () => {
    let original;
    beforeEach(() => {
        setOriginalReportData(makeDataset());
        original = makeDataset();
    });

    // ---- temp_files.queries: the Top-Queries table now re-scopes ----------- //
    it('temp_files.queries re-scopes (t2 drops, t1 count halves, header agrees)', () => {
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, MID));
        assert.equal(out.temp_files.total_messages, 2);      // header re-scoped
        assert.equal(out.temp_files.queries.length, 1);      // t2 (all 2nd half) dropped
        const t1 = out.temp_files.queries[0];
        assert.equal(t1.id, 't1');
        assert.equal(t1.count, 2);
        assert.equal(t1.total_size, '2.00 KB');
        assert.equal(t1.avg_size, '1.00 KB');
        // Table total now matches the re-scoped header (was the R3 contradiction).
        const tableTotal = out.temp_files.queries.reduce((s, q) => s + q.count, 0);
        assert.equal(tableTotal, out.temp_files.total_messages);
    });

    // ---- top_events: per-occurrence timestamps drive the re-scope ---------- //
    it('top_events re-scopes (WARNING dropped, ERROR count 4 -> 2, timestamps clipped)', () => {
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, MID));
        assert.equal(out.top_events.length, 1);              // wa-1 fully outside -> dropped
        const er = out.top_events[0];
        assert.equal(er.id, 'er-1');
        assert.equal(er.count, 2);                           // was 4
        assert.equal(er.timestamps.length, 2);
        assert.deepEqual(er.timestamps, [utc(1), utc(2)]);   // only in-window occurrences
    });

    // ---- sql_overview headline mix (categories + types) re-scopes ---------- //
    it('sql_overview categories/types/total re-scope from filtered executions', () => {
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, MID));
        const ov = out.sql_overview;
        assert.equal(ov.total_queries, 4);                   // 8 -> 4
        assert.equal(ov.categories[0].category, 'DML');
        assert.equal(ov.categories[0].count, 4);
        assert.equal(ov.categories[0].percentage, 100);
        const byType = Object.fromEntries(ov.types.map(t => [t.type, t]));
        assert.equal(byType.SELECT.count, 3);                // q1(2) + q2(1)
        assert.equal(byType.INSERT.count, 1);                // i1(1)
        assert.equal(byType.SELECT.percentage, 75);
        assert.equal(byType.INSERT.percentage, 25);
        // Contradiction with SQL Performance is gone: both now report 4.
        assert.equal(out.sql_performance.total_queries_parsed, ov.total_queries);
    });

    it('sql_overview by_database stays whole-log and is flagged for annotation', () => {
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, MID));
        assert.deepEqual(out.sql_overview.by_database, original.sql_overview.by_database);
        assert.equal(out._wholeLog.sql_dimensions, true);
    });

    // ---- annotation fallback: whole-log sections are flagged --------------- //
    it('events / locks / maintenance stay whole-log AND are flagged in _wholeLog', () => {
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, MID));
        assert.deepEqual(out.events, original.events);
        assert.deepEqual(out.locks, original.locks);
        assert.deepEqual(out.maintenance, original.maintenance);
        assert.equal(out._wholeLog.events, true);
        assert.equal(out._wholeLog.locks, true);
        assert.equal(out._wholeLog.maintenance, true);
    });

    it('filter markers are set and consistent with the annotations', () => {
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, MID));
        assert.equal(out._timeFiltered, true);
        assert.deepEqual(out._filterRange, { begin: FULL_BEGIN, end: MID });
        // Every flagged section is genuinely still full-log (honest annotation).
        assert.deepEqual(out.locks, original.locks);
        assert.deepEqual(out.maintenance, original.maintenance);
    });

    // ---- unfiltered payload carries no whole-log flags (no badges) --------- //
    it('a full-window filter still marks whole-log sections but the data is unchanged', () => {
        // A filter over the whole span keeps everything, yet the whole-log
        // sections remain flagged (they are structurally un-scopable, not
        // "happened to match"): the annotation is about method, not values.
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, FULL_END));
        assert.equal(out.top_events.length, 2);              // both events survive
        assert.equal(out.top_events[0].count, 4);
        assert.equal(out.sql_overview.total_queries, 8);
        assert.equal(out._wholeLog.locks, true);
    });
});
