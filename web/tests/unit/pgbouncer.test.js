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

// esc() needs document.createElement. node --test runs each file in its own
// process, so the same minimal stub pgbouncer-labels.test.js uses — escaping
// & < > exactly like textContent->innerHTML — is safe here.
if (typeof globalThis.document === 'undefined') {
    globalThis.document = {
        createElement() {
            let text = '';
            return {
                set textContent(v) { text = v == null ? '' : String(v); },
                get innerHTML() {
                    return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
                },
            };
        },
    };
}

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
    // The note must distinguish this from a pooler nobody used: it says
    // the window carried traffic, without claiming to know why the
    // client lines are absent.
    assert.ok(html.includes('no client connection logged in it'),
        'the placeholder no longer distinguishes a silenced log from an idle one');
});

test('an idle pooler is not told its connection logging is off', () => {
    const html = buildPgBouncerSection({ pgbouncer: idlePayload });

    assert.ok(!html.includes('Traffic over this window, but no'),
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

// ---- a time series needs a time axis -------------------------------
//
// uPlot cannot scale an axis on a single point: given one snapshot it
// falls back to a default range and draws an empty plot under month
// labels taken from nowhere ("00:00 Dec 1" over a 37 s capture). A chart
// that invents its own axis is worse than no chart, so below two points
// the section says why it is not drawing one.

// oneSnapshotPayload is pgbouncer_oldversion.log: 37 s of capture, one
// "stats:" line, real client traffic around it.
const oneSnapshotPayload = {
    lines: 372,
    lines_read: 372,
    min_timestamp: '2026-09-20T13:39:27.855+02:00',
    max_timestamp: '2026-09-20T13:40:04.481+02:00',
    stats_sample_count: 1,
    stats_series: [{ time: '2026-09-20T13:39:47.855+02:00', queries_per_sec: 0 }],
    avg_queries_per_sec: 0,
    max_queries_per_sec: 0,
    login_attempts: 182,
    server_connections: 4,
    sessions: 181,
    session_distribution: { '< 1s': 181 },
    total_errors: 0,
};

test('a single snapshot draws no chart, and says why', () => {
    const html = buildPgBouncerSection({ pgbouncer: oneSnapshotPayload });

    for (const id of ['chart-pgb-throughput', 'chart-pgb-io', 'chart-pgb-timing']) {
        assert.ok(!html.includes(id), `${id} is drawn from a single point: its time axis is invented`);
    }
    assert.ok(html.includes('at least two'),
        'the charts are gone with nothing saying why');
    // The coverage caveat is about the figures, not the charts: it stays.
    assert.ok(html.includes('A single stats snapshot'));
});

test('two snapshots are enough to draw the charts', () => {
    const html = buildPgBouncerSection({
        pgbouncer: {
            ...oneSnapshotPayload,
            stats_sample_count: 2,
            stats_series: [
                { time: '2026-09-20T13:39:47.855+02:00' },
                { time: '2026-09-20T13:39:57.855+02:00' },
            ],
        },
    });
    for (const id of ['chart-pgb-throughput', 'chart-pgb-io', 'chart-pgb-timing']) {
        assert.ok(html.includes(id), `${id} is missing from a two-point series`);
    }
});

// ---- what was read, not only what was understood -------------------
//
// COR-87: the text report states the window, the lines read and the
// lines lost on the way to the figures. HTML is the most consulted
// format and stated none of it, so a report drawn from 100 of 120 lines
// read the same as one drawn from all of them.

const lossyPayload = {
    ...oneSnapshotPayload,
    lines: 100,
    lines_read: 120,
    lines_unrecognized: 20,
    debug_lines: 7,
    total_session_seconds: 362,
};

test('the summary states the window it was drawn from', () => {
    const html = buildPgBouncerSection({ pgbouncer: lossyPayload });
    assert.match(html, /class="summary-date">[^<]*→[^<]*</,
        'the HTML section never says which window it covers');
    assert.ok(html.includes('Duration'), 'the window duration is missing');
});

test('the summary publishes the lines it could not use', () => {
    const html = buildPgBouncerSection({ pgbouncer: lossyPayload });
    for (const label of ['Log lines read', 'Log entries', 'Unrecognized lines',
        'Debug/noise entries', 'Stats snapshots', 'Average session time']) {
        assert.ok(html.includes(label), `${label} is not published in HTML`);
    }
    assert.ok(html.includes('>120<'), 'the 120 lines read are not shown');
    assert.ok(html.includes('>20<'), 'the 20 unrecognized lines are not shown');
});

test('a clean capture is not told about losses it did not have', () => {
    const html = buildPgBouncerSection({ pgbouncer: oneSnapshotPayload });
    for (const label of ['Unrecognized lines', 'Filtered-out lines', 'Debug/noise entries']) {
        assert.ok(!html.includes(label), `${label} shown at zero, unlike the text report`);
    }
    assert.ok(html.includes('Log lines read'), 'the unconditional counters vanished too');
});

test('filtered-out lines are counted as the text report counts them', () => {
    const html = buildPgBouncerSection({
        pgbouncer: { ...oneSnapshotPayload, lines: 100, lines_read: 120, lines_unrecognized: 5 },
    });
    assert.ok(html.includes('Filtered-out lines'), 'the lines the filters dropped are not named');
    assert.ok(html.includes('>15<'), '120 read - 5 unrecognized - 100 entries = 15 filtered out');
});

// ---- grid balance --------------------------------------------------
//
// The closing row pairs the session histogram with the error table. With
// only one of the two, a two-column grid leaves half the report blank.

test('a row holding one block does not keep an empty column', () => {
    const errorsOnly = buildPgBouncerSection({
        pgbouncer: {
            ...oneSnapshotPayload,
            sessions: 0,
            session_distribution: undefined,
            total_errors: 45,
            errors: { 'pooler error: query_wait_timeout': 45 },
        },
    });
    const rows = (errorsOnly.match(/grid grid-2/g) || []).length;
    assert.equal(rows, 1, 'the errors table is held in a two-column grid with nothing beside it');

    const both = buildPgBouncerSection({
        pgbouncer: { ...oneSnapshotPayload, total_errors: 45, errors: { boom: 45 } },
    });
    assert.equal((both.match(/grid grid-2/g) || []).length, 2,
        'the paired row lost its two columns');
});

// ---- refutation: the two rules must not drift apart ----------------
//
// The Go side (analysis.PgBouncerMetrics.ClientLinesMissing) accepts
// only the snapshot counters as proof that clients were served. A
// server connection and a pooler error are deliberately not proof:
// min_pool_size opens backends with nobody connected, and a backend
// refusing the pooler errors out before any client exists.
//
// silencedPayload above carries traffic AND errors, idlePayload carries
// neither, so both rules answer the same on both -- which is why the
// drift went unnoticed. This payload is the one that separates them.
const idleWithServerNoisePayload = {
    ...silencedPayload,
    avg_queries_per_sec: 0,
    max_queries_per_sec: 0,
    max_xacts_per_sec: 0,
    server_connections: 4,
    total_errors: 7,
};

test('a backend error is not proof that clients were served', () => {
    const html = buildPgBouncerSection({ pgbouncer: idleWithServerNoisePayload });
    assert.ok(
        !html.includes('Traffic over this window, but no'),
        'the section claims traffic on a window whose snapshots are all zero',
    );
    // The block still appears, still names the setting that governs it:
    // the absence is stated, not explained away and not accused.
    assert.ok(
        html.includes('No data available') && html.includes('log_connections = 1'),
        'the empty block should still name the setting that governs it',
    );
});
