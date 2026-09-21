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
