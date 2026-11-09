// Unit tests for the pgBouncer section renderer.
//
// A pooler log carries traffic figures only when it holds "LOG stats:"
// lines, which a short capture at the default stats_period=60 never has.
// The text and Markdown reports drop the whole throughput block in that
// case; the HTML section must do the same rather than publish a 0.0/s
// that reads as a measurement.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { buildPgBouncerSection as buildSections } from '../../js/sections/pgbouncer.js';

// The payloads below each describe one pooler; the section takes the list.
const buildPgBouncerSection = (d) => buildSections({ ...d, pgbouncer: [d.pgbouncer] });

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

    assert.ok(!html.includes('>Queries<'), 'the average-rate card rendered without a stats snapshot');
    assert.ok(!html.includes('/s</div>'), 'a per-second value is shown without a stats snapshot');

    // The counters that do not come from the stats lines stay.
    assert.ok(html.includes('Completed sessions'));
    assert.ok(html.includes('Warnings'));
});

test('with stats snapshots: the throughput cards are shown', () => {
    const html = buildPgBouncerSection({
        pgbouncer: { ...statlessPayload, stats_sample_count: 2, avg_queries_per_sec: 10.5, max_queries_per_sec: 21 },
    });

    assert.ok(html.includes('>Queries<'));
    assert.ok(html.includes('10.5/s'));
    // The busiest snapshot is read on the chart, not on a card.
    assert.ok(!html.includes('21/s'), 'a max-snapshot card is back');
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
    start_date: '2026-09-20 13:59:04',
    end_date: '2026-09-20 13:59:50',
    stats_sample_count: 3,
    stats_series: [
        { time: '2026-09-20 13:59:19' },
        { time: '2026-09-20 13:59:34' },
        { time: '2026-09-20 13:59:49' },
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

    assert.ok(html.includes('pgBouncer Sessions'), 'the Sessions card vanished instead of saying why it is empty');
    assert.ok(html.includes('pgBouncer Connections'), 'the Connections card vanished instead of saying why it is empty');
    assert.ok(html.includes('log_connections = 1'));
    assert.ok(html.includes('log_disconnections = 1'));
    // The note must distinguish this from a pooler nobody used: it says
    // the window carried traffic, without claiming to know why the
    // client lines are absent.
    assert.ok(html.includes('no client connection was logged in it'),
        'the placeholder no longer distinguishes a silenced log from an idle one');
});

test('an idle pooler is not told its connection logging is off', () => {
    const html = buildPgBouncerSection({ pgbouncer: idlePayload });

    assert.ok(!html.includes('Traffic over this window, but no'),
        'an idle pooler is accused of losing lines it never had to write');
    // It still gets the placeholder and the settings to check.
    assert.ok(html.includes('pgBouncer Connections'));
    assert.ok(html.includes('log_connections = 1'));
});

test('a single stats snapshot: the figures are qualified, not asserted', () => {
    const html = buildPgBouncerSection({
        pgbouncer: {
            ...silencedPayload,
            login_attempts: 182,
            sessions: 181,
            stats_sample_count: 1,
            stats_series: [{ time: '2026-09-20 13:39:47' }],
            start_date: '2026-09-20 13:39:27',
            end_date: '2026-09-20 13:40:04',
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
            start_date: '2026-09-09 20:58:59',
            end_date: '2026-09-09 20:59:04',
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
    start_date: '2026-09-20 13:39:27',
    end_date: '2026-09-20 13:40:04',
    stats_sample_count: 1,
    stats_series: [{ time: '2026-09-20 13:39:47', queries_per_sec: 0 }],
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
                { time: '2026-09-20 13:39:47' },
                { time: '2026-09-20 13:39:57' },
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
// The cards pair up two by two. A card left alone on its row, the events
// of a pooler with no timeline, takes the full width instead of leaving
// half the report blank.

test('a row holding one card does not keep an empty column', () => {
    const errors = [{ id: 'wa-AAAA', level: 'WARNING', message: 'query_wait_timeout', count: 45, first: '2026-09-20 13:57:58', last: '2026-09-20 13:58:40' }];
    const eventsOnly = buildPgBouncerSection({ pgbouncer: { ...oneSnapshotPayload, total_errors: 45, errors } });
    assert.equal((eventsOnly.match(/grid grid-2/g) || []).length, 1,
        'the events card is held in a two-column grid with nothing beside it');

    const both = buildPgBouncerSection({
        pgbouncer: { ...oneSnapshotPayload, total_errors: 45, errors, events: [{ timestamp: '2026-09-20 13:57:00', kind: 'start', detail: 'PgBouncer 1.25.2' }] },
    });
    assert.equal((both.match(/grid grid-2/g) || []).length, 2,
        'the timeline and the events lost their two columns');
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

// ---- time filter ---------------------------------------------------
//
// The pooler track is analyzed separately and carries no per-item
// timestamps, so the client-side slider cannot re-scope it. The web data
// contract requires such a section to be flagged rather than left to
// present whole-log numbers as if they were scoped.
test('the section is flagged whole-log under a time filter', () => {
    const p = { ...silencedPayload, login_attempts: 69, sessions: 68 };
    assert.ok(
        !buildPgBouncerSection({ pgbouncer: p }).includes('whole-log-badge'),
        'an unfiltered report should not carry the badge',
    );
    assert.ok(
        buildPgBouncerSection({ pgbouncer: p, _wholeLog: { pgbouncer: true } }).includes('whole-log-badge'),
        'a filtered report presents whole-log figures and must say so',
    );
});

// Two poolers are two sections: each needs its own section and chart ids
// (a shared id draws both series into the first chart), and its name in
// the header so the reader knows which pooler a figure belongs to.
test('each pooler gets its own section, ids and name', () => {
    const series = [{ time: '2026-09-25 09:00:00', queries_per_sec: 1 }, { time: '2026-09-25 09:00:10', queries_per_sec: 2 }];
    const pooler = (name) => ({
        ...statlessPayload, name, files: [name + '.log'], stats_sample_count: 2, stats_series: series,
        start_date: '2026-09-25 09:00:00', end_date: '2026-09-25 09:00:10',
        events: [{ timestamp: '2026-09-25 09:00:00', kind: 'start', detail: 'PgBouncer 1.25.2' }],
    });
    const html = buildSections({ pgbouncer: [pooler('127.0.0.1:6432'), pooler('<b>6433</b>')] });
    assert.ok(html.includes('id="pgbouncer"') && html.includes('id="pgbouncer-2"'));
    assert.ok(html.includes('chart-pgb-timing"') && html.includes('chart-pgb-timing-2"'));
    assert.ok(html.includes('pgBouncer · 127.0.0.1:6432'));
    assert.ok(html.includes('&lt;b&gt;6433&lt;/b&gt;') && !html.includes('<b>6433</b>'));

    const one = buildSections({ pgbouncer: [pooler('127.0.0.1:6432')] });
    assert.ok(!one.includes('pgBouncer · '), 'a single pooler keeps the plain header');
});

// Before 1.23 the wait field is a queue depth: the section must say so,
// name it as clients queued, and keep it off the chart drawn in µs. The
// release boundary is what separates the two readings.
test('a pre-1.23 wait is shown as clients queued', () => {
    const series = [{ time: '2026-09-25 09:24:02', wait_us: 0 }, { time: '2026-09-25 09:24:12', wait_us: 5500000 }];
    const pooler = (version) => buildPgBouncerSection({ pgbouncer: {
        ...statlessPayload, version, stats_sample_count: 2, stats_series: series, avg_wait_us: 5500000,
        start_date: '2026-09-25 09:24:02', end_date: '2026-09-25 09:24:12',
    } });
    const old = pooler('1.22.1');
    assert.ok(old.includes('5.5 clients') && old.includes('Avg clients queued') && old.includes('predates 1.23'));
    assert.ok(!old.includes('Avg pool wait time'));
    const current = pooler('1.23.0');
    assert.ok(current.includes('Avg pool wait time') && !current.includes('Avg clients queued'));
});

// Error messages, close reasons, database names in commands and file
// names all come from the log, some from a client that has not even
// authenticated: every cell that shows one must be escaped.
test('log-borne text is escaped in every new cell', () => {
    const evil = '<img src=x onerror=alert(1)>';
    const html = buildPgBouncerSection({ pgbouncer: {
        ...statlessPayload, name: evil, version: evil, files: [evil],
        start_date: '2026-09-25 09:00:00', end_date: '2026-09-25 09:01:00',
        total_errors: 1, errors: [{ message: evil, count: 1, first: '2026-09-25 09:00:00', last: '2026-09-25 09:00:00' }],
        session_reasons: [{ reason: evil, count: 181, total_seconds: 0 }],
        events: [{ timestamp: '2026-09-25 09:00:00', kind: evil, detail: evil }],
    } });
    assert.ok(!html.includes(evil), 'raw log text reached the page');
    assert.ok(html.includes('&lt;img src=x onerror=alert(1)&gt;'));

    // A file name lands in an attribute too, where a quote closes it.
    const quoted = buildPgBouncerSection({ pgbouncer: { ...statlessPayload, name: 'p', files: ['/logs/x" onmouseover="alert(1).log'] } });
    assert.ok(quoted.includes('title="/logs/x&quot; onmouseover=&quot;alert(1).log"'), 'a quote in a file name broke out of its attribute');
});

// Who ended the sessions: "client_idle_timeout" is the pooler's doing,
// only a reason starting "client " is the client's, as on the Go side.
test('sessions are split by who ended them', () => {
    const html = buildPgBouncerSection({ pgbouncer: { ...statlessPayload, sessions: 183, session_reasons: [
        { reason: 'client close request', count: 160, total_seconds: 0 },
        { reason: 'client_idle_timeout', count: 1, total_seconds: 20 },
        { reason: 'query_wait_timeout', count: 22, total_seconds: 66 },
    ] } });
    assert.ok(html.includes('Ended by the client: 160 · by the pooler: 23'));
    assert.ok(/query_<wbr>wait_<wbr>timeout<\/td><td class="num">22<\/td><td class="num">12\.0%<\/td><td class="num">3s</.test(html),
        'a close reason carries its count, share and average age');
});

// Pooler times read on the log's own clock, like the PostgreSQL sections:
// the payload carries the log's digits without a zone, and they are the
// digits shown, whatever the viewer's time zone.
test('pooler times keep the digits of the log', () => {
    const html = buildPgBouncerSection({ pgbouncer: {
        ...statlessPayload,
        start_date: '2026-09-25 09:05:29', end_date: '2026-09-25 09:14:29',
        events: [{ timestamp: '2026-09-25 09:11:32', kind: 'shutdown', detail: 'SIGINT' }],
        total_errors: 1, errors: [{ message: 'boom', count: 1, first: '2026-09-25 09:10:59', last: '2026-09-25 09:10:59' }],
    } });
    for (const t of ['09:05:29', '09:14:29', '09:11:32', '09:10:59']) assert.ok(html.includes(t), t);
});

// A narrow table breaks identifiers between their words, and a run of
// thirty characters without a break every thirty, never inside a time.
test('table cells break between words, not mid-word', () => {
    const html = buildPgBouncerSection({ pgbouncer: {
        ...statlessPayload, start_date: '2026-09-25 09:00:00', end_date: '2026-09-25 09:01:00',
        total_errors: 2, errors: [
            { message: 'pooler error: query_wait_timeout', count: 1, first: '2026-09-25 09:00:01', last: '2026-09-25 09:00:05' },
            { message: 'x'.repeat(60), count: 1, first: '2026-09-25 09:00:02', last: '2026-09-25 09:00:02' },
        ],
        events: [{ timestamp: '2026-09-25 09:00:00', kind: 'start', detail: 'PgBouncer 1.25.2' }],
    } });
    assert.ok(html.includes('query_<wbr>wait_<wbr>timeout'));
    assert.ok(html.includes('x'.repeat(30) + '<wbr>' + 'x'.repeat(30) + '<wbr>'));
    assert.ok(html.includes('>09:00:00<'), 'a time cell must stay whole');
    assert.ok(html.includes('09:00:01 → 09:00:05'), 'an error reads from its first occurrence to its last');
});

// Queueing stretches are listed with their own peak, in the section's
// unit: a pre-1.23 wait is a number of clients, a later one a duration.
test('queueing stretches are listed in the wait unit', () => {
    const ep = [{ start: '2026-09-25 09:08:09', end: '2026-09-25 09:09:09', peak_wait_us: 2571000, peak_timestamp: '2026-09-25 09:08:39' }];
    const html = buildPgBouncerSection({ pgbouncer: { ...statlessPayload, version: '1.25.2', wait_episodes: ep } });
    assert.ok(html.includes('Stretches with waits of 100 ms or more') && html.includes('2.57\u00a0s') && html.includes('>09:08:39<'));
    assert.ok(html.includes('09:08:09 → 09:09:09'), 'a stretch reads from its start to its end');
    const old = buildPgBouncerSection({ pgbouncer: { ...statlessPayload, version: '1.18.0', wait_episodes: ep } });
    assert.ok(old.includes('0.1 client or more queued') && old.includes('2.6\u00a0clients'));
});

// The page recomputes the Go verdict on stats coverage; this is the case
// analysis.TestPgBouncerStatsCoverageEdge holds: five snapshots ten
// seconds apart cover 50 s, exactly half of a 100 s window (not flagged)
// and less than half of a 101 s one (flagged).
test('the coverage caveat fires below half the window, as on the Go side', () => {
    const series = [0, 10, 20, 30, 40].map(s => ({ time: `2026-09-25 09:00:${String(s).padStart(2, '0')}` }));
    const section = (end) => buildPgBouncerSection({ pgbouncer: {
        ...statlessPayload, stats_sample_count: 5, stats_series: series,
        start_date: '2026-09-25 09:00:00', end_date: end,
    } });
    assert.ok(!section('2026-09-25 09:01:40').includes('pgBouncer logs nothing between two snapshots'));
    assert.ok(section('2026-09-25 09:01:41').includes('pgBouncer logs nothing between two snapshots'));
});

// The error and close-reason tables show twenty rows and state the rest,
// as the text report does.
test('the error table stops at twenty rows and says how many it left out', () => {
    const errors = Array.from({ length: 21 }, (_, i) => ({ message: `error ${i}`, count: 21 - i, first: '2026-09-25 09:00:00', last: '2026-09-25 09:00:00' }));
    const html = buildPgBouncerSection({ pgbouncer: { ...statlessPayload, total_errors: 231, errors } });
    assert.ok(html.includes('>error 19<') && !html.includes('>error 20<'));
    assert.ok(html.includes('… and 1 more'));
});
