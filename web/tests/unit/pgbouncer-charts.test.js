// Unit tests for the pgBouncer bar charts' rules: bucketing, the bucket
// size, the axis ticks, the event markers and their label layout.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { pgbBucketize, pgbBucketSize, pgbAxisFmt, pgbMarkers, pgbPlaceLabels } from '../../js/charts.js';

const snap = (t, o = {}) => ({ t, xacts_per_sec: 0, queries_per_sec: 0, in_bytes_per_sec: 0, out_bytes_per_sec: 0, xact_us: 0, query_us: 0, wait_us: 0, ...o });

test('a grouped bar shows the worst snapshot for times and waits, the mean for rates', () => {
    const pts = [
        snap(10, { xacts_per_sec: 10, queries_per_sec: 20, wait_us: 1000, query_us: 100 }),
        snap(20, { xacts_per_sec: 30, queries_per_sec: 40, wait_us: 9000, query_us: 300 }),
    ];
    const [b] = pgbBucketize(pts, 10, 60);
    assert.equal(b.n, 2);
    assert.equal(b.queries_per_sec, 30, 'rates are a mean');
    assert.equal(b.wait_us, 9000, 'the wait is the worst snapshot');
    assert.equal(b.query_us, 300, 'the query time is the worst snapshot');
    assert.equal(b.wait_us_avg, (1000 * 10 + 9000 * 30) / 40, 'the weighted mean stays for the tooltip');
});

test('one bar per snapshot up to 60, a round bucket beyond, whole days for long captures', () => {
    const series = (n, step) => Array.from({ length: n }, (_, i) => snap((i + 1) * step));
    assert.equal(pgbBucketSize(series(60, 10), 10, 0), 0);
    assert.equal(pgbBucketSize(series(61, 10), 10, 0), 30);
    assert.equal(pgbBucketSize(series(24 * 90, 3600), 3600, 0), 2 * 86400, '90 days: two days a bar');
    assert.equal(pgbBucketSize(series(10, 10), 10, 5), 0, 'a bucket within the period is per snapshot');
});

test('a rate tick keeps its decimal', () => {
    const fmt = pgbAxisFmt('rate');
    assert.equal(fmt(2.5), '2.5/s');
    assert.equal(fmt(3), '3/s');
    assert.equal(fmt(15000), '15k/s');
    assert.equal(fmt(1500), '1.5k/s');
});

test('events become spans and lines', () => {
    const ev = (s, kind, detail = '') => ({ timestamp: new Date(Date.UTC(2026, 8, 25, 9, 0, s)).toISOString(), kind, detail });
    const marks = pgbMarkers([
        ev(0, 'start', 'PgBouncer 1.25.2'), ev(10, 'pause', "PAUSE 'db'"), ev(20, 'resume'),
        ev(30, 'reload', 'RELOAD'), ev(40, 'shutdown', 'SIGINT'), ev(41, 'start'), ev(50, 'shutdown'),
    ]);
    assert.deepEqual(marks.map(m => m.label), ['paused', 'reload', 'restart', 'shutdown'], 'the first start is not drawn');
    assert.equal(marks[0].b - marks[0].a, 10, 'a pause spans until its resume');
    assert.equal(marks[1].kind, 'command');
    assert.equal(marks[3].b, Infinity, 'a shutdown with no start after it runs to the end');
});

test('a marker label starts at its line, turns left at the edge, moves down, or is left out', () => {
    const w = 30, gap = 3;
    const [a, b] = pgbPlaceLabels([{ x: 10, w }, { x: 100, w }], 1000, gap);
    assert.deepEqual([a.l, a.row, b.l, b.row], [13, 0, 103, 0], 'room on the first row, right of the line');
    const [edge] = pgbPlaceLabels([{ x: 990, w }], 1000, gap);
    assert.equal(edge.l, 990 - gap - w, 'left of the line where the plot ends');
    const crowded = pgbPlaceLabels([{ x: 10, w }, { x: 20, w }], 1000, gap);
    assert.equal(crowded[0].row, 1, 'a label that would cross the next line moves down');
    const full = pgbPlaceLabels(Array.from({ length: 5 }, (_, i) => ({ x: 10 + i, w })), 1000, gap);
    assert.equal(full.filter(s => s === null).length > 0, true, 'with no row free, a label is left out');
});

test('a bar spans the period the payload gives it', () => {
    const t0 = Date.UTC(2026, 8, 25, 9, 0, 0) / 1000;
    // A 60 s run started at 09:00:13, then a 15 s run after a restart.
    const pts = [[13, 73], [73, 133], [198, 213], [213, 228]].map(([a, t]) => ({ ...snap(t0 + t), a: t0 + a }));
    assert.deepEqual(pgbBucketize(pts, 60, 0).map(b => [b.a - t0, b.b - t0]), [[13, 73], [73, 133], [198, 213], [213, 228]]);
});
