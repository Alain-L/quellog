// Unit tests for the pgBouncer section renderer.
//
// A pooler log carries traffic figures only when it holds "LOG stats:"
// lines, which a short capture at the default stats_period=60 never has.
// The text and Markdown reports drop the whole throughput block in that
// case; the HTML section must do the same rather than publish a 0.0/s
// that reads as a measurement.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { buildPgBouncerSection } from '../../js/sections/pgbouncer.js';

// statlessPayload mirrors pgb-05 run over a window shorter than one
// stats_period: real traffic, no stats line.
const statlessPayload = {
    lines: 373,
    stats_sample_count: 0,
    avg_queries_per_sec: 0,
    max_queries_per_sec: 0,
    login_attempts: 182,
    server_connections: 4,
    sessions: 181,
    total_errors: 0,
};

test('no stats snapshot: the section publishes no throughput figure', () => {
    const html = buildPgBouncerSection({ pgbouncer: statlessPayload });

    assert.ok(!html.includes('Avg queries/s'), 'the average-rate card rendered without a stats snapshot');
    assert.ok(!html.includes('Max snapshot avg'), 'the max-rate card rendered without a stats snapshot');
    assert.ok(!html.includes('/s</div>'), 'a per-second value is shown without a stats snapshot');

    // The counters that do not come from the stats lines stay.
    assert.ok(html.includes('Login attempts'));
    assert.ok(html.includes('Completed sessions'));
});

test('with stats snapshots: the throughput cards are shown', () => {
    const html = buildPgBouncerSection({
        pgbouncer: { ...statlessPayload, stats_sample_count: 2, avg_queries_per_sec: 10.5, max_queries_per_sec: 21 },
    });

    assert.ok(html.includes('Avg queries/s'));
    assert.ok(html.includes('10.5/s'));
    assert.ok(html.includes('Max snapshot avg'));
    assert.ok(html.includes('21/s'));
});

// ---- configuration gaps -------------------------------------------
//
// Three settings decide how much of the section can exist at all. When
// one of them empties a block, the block must say so: a block that just
// disappears reads the same as "nothing happened".

// silencedPayload is pgb-04-transaction-noconnlog-errors.log: the same
// traffic and the same errors as its sibling capture, with
// log_connections = 0 / log_disconnections = 0 removing every client
// line.
const silencedPayload = {
    lines: 56,
    min_timestamp: '2026-09-20T13:59:04.379+02:00',
    max_timestamp: '2026-09-20T13:59:50.169+02:00',
    stats_sample_count: 3,
    stats_series: [
        { time: '2026-09-20T13:59:19.379+02:00' },
        { time: '2026-09-20T13:59:34.382+02:00' },
        { time: '2026-09-20T13:59:49.385+02:00' },
    ],
    avg_queries_per_sec: 48.3,
    max_queries_per_sec: 58,
    max_xacts_per_sec: 11,
    login_attempts: 0,
    server_connections: 0,
    sessions: 0,
    total_errors: 45,
};

// idlePayload is the false positive to avoid: the same shape, but with
// nothing in the window that contradicts a pooler nobody used.
const idlePayload = {
    ...silencedPayload,
    avg_queries_per_sec: 0,
    max_queries_per_sec: 0,
    max_xacts_per_sec: 0,
    total_errors: 0,
};

test('log_connections = 0: the empty blocks name the setting', () => {
    const html = buildPgBouncerSection({ pgbouncer: silencedPayload });

    assert.ok(html.includes('Sessions by'), 'the Sessions block vanished instead of saying why it is empty');
    assert.ok(html.includes('Connections by'), 'the Connections block vanished instead of saying why it is empty');
    assert.ok(html.includes('log_connections = 1'));
    assert.ok(html.includes('log_disconnections = 1'));
    assert.ok(html.includes('docs/pgbouncer-setup.md'));
    assert.ok(html.includes('logged no client connection'),
        'the placeholder does not say the traffic itself was reported');
});

test('an idle pooler is not told its connection logging is off', () => {
    const html = buildPgBouncerSection({ pgbouncer: idlePayload });

    assert.ok(!html.includes('The pooler reported traffic over this window'),
        'an idle pooler is accused of losing lines it never had to write');
    // It still gets the placeholder and the settings to check.
    assert.ok(html.includes('Connections by'));
    assert.ok(html.includes('log_connections = 1'));
});

test('a single stats snapshot: the figures are qualified, not asserted', () => {
    const html = buildPgBouncerSection({
        pgbouncer: {
            ...silencedPayload,
            login_attempts: 182,
            sessions: 181,
            stats_sample_count: 1,
            stats_series: [{ time: '2026-09-20T13:39:47.855+02:00' }],
            min_timestamp: '2026-09-20T13:39:27.855+02:00',
            max_timestamp: '2026-09-20T13:40:04.481+02:00',
            avg_queries_per_sec: 0,
            max_queries_per_sec: 0,
            max_xacts_per_sec: 0,
        },
    });

    assert.ok(html.includes('A single stats snapshot'),
        'a startup snapshot is published as a measurement of the whole window');
    assert.ok(html.includes('stats_period'));
});

test('telemetry covering the window end to end is not flagged', () => {
    const series = [];
    for (let i = 0; i < 5; i++) {
        series.push({ time: new Date(Date.UTC(2026, 8, 9, 20, 59, i)).toISOString() });
    }
    const html = buildPgBouncerSection({
        pgbouncer: {
            ...idlePayload,
            login_attempts: 129,
            sessions: 129,
            stats_sample_count: 5,
            stats_series: series,
            min_timestamp: '2026-09-09T20:58:59.000Z',
            max_timestamp: '2026-09-09T20:59:04.000Z',
        },
    });

    assert.ok(!html.includes('stats snapshots cover'),
        'a capture sampled every second is told its coverage is short');
    assert.ok(!html.includes('A single stats snapshot'));
});

test('the timing chart says pool_mode sets the wait denominator', () => {
    const html = buildPgBouncerSection({
        pgbouncer: { ...silencedPayload, login_attempts: 129, sessions: 129 },
    });
    assert.ok(html.includes('pool_mode'),
        'nothing tells the reader what the pool-wait figure is averaged over');
});
