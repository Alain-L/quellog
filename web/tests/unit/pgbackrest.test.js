// Unit tests for the pgBackRest section renderer (js/sections/pgbackrest.js).
//
// Small hand-made stanzas pin each rule; the JSON goldens of the lab runs
// (test/testdata/pgbackrest/S*/pgbackrest_logs.json.golden) check the
// section on real data.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { buildPgBackRestSection } from '../../js/sections/pgbackrest.js';
import { chartData } from '../../js/charts.js';

// esc() needs document.createElement. node --test runs each file in its
// own process, so a minimal stub, escaping & < > exactly like
// textContent->innerHTML, is safe here.
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

const here = dirname(fileURLToPath(import.meta.url));
const golden = name => JSON.parse(readFileSync(join(here, '../../../test/testdata/pgbackrest', name, 'pgbackrest_logs.json.golden'), 'utf8')).pgbackrest;
const render = (pbr, extra = {}) => buildPgBackRestSection({ pgbackrest: pbr, ...extra });
// The text of the HTML, tags dropped, for wording checks.
const text = html => html.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ');
// Asserts that s holds every part.
const hasAll = (s, ...parts) => { for (const p of parts) assert.ok(s.includes(p), p); };

const stats = (o = {}) => ({ logged: true, async_logged: false, pushed: 0, pushed_async: 0, found: 0, found_async: 0, not_found: 0, not_found_async: 0, dropped: 0, failed: 0, failed_async: 0, timeouts: 0, ...o });

// A minimal nominal stanza: one full backup and one failure, WAL pushed.
const nominal = () => ({
    stanza: 'main',
    files: ['logs/main-backup.log', 'logs/main-archive-push.log'],
    lines_read: 154,
    versions: ['2.59.3'],
    start_date: '2026-10-10 13:04:55',
    end_date: '2026-10-10 13:14:11',
    has_timestamps: true, saw_begin_end: true, backups_logged: true,
    backups: [
        { label: '20261010-130516F', type: 'full', repo: 'repo1', start_date: '2026-10-10 13:05:16', end_date: '2026-10-10 13:05:38', duration_ms: 22000, size: '29.4MB', size_bytes: 30828134, succeeded: true },
        { type: 'incr', repo: 'repo1', start_date: '2026-10-10 13:11:41', end_date: '2026-10-10 13:11:41', duration_ms: 3, succeeded: false, error_code: 56, error_line: 'unable to find primary cluster - cannot proceed', error_label: 'unable to find primary cluster' },
    ],
    last_full: { label: '20261010-130516F', repo: 'repo1', end_date: '2026-10-10 13:05:38', age_seconds: 513 },
    archive_push: stats({ pushed: 3 }),
    archive_get: stats({ logged: false }),
    push_dates: ['2026-10-10 13:05:00', '2026-10-10 13:06:00', '2026-10-10 13:07:00'],
    operations: [{ command: 'stanza-create', date: '2026-10-10 13:04:55', summary: "stanza-create for stanza 'main' on repo1", detail: 'on repo1', succeeded: true, has_outcome: true, duration_ms: 48 }],
    errors: [{ id: 'er-AbCd', code: 56, label: 'unable to find primary cluster', message: 'unable to find primary cluster - cannot proceed', example: 'unable to find primary cluster - cannot proceed', count: 1, first_date: '2026-10-10 13:11:41', last_date: '2026-10-10 13:11:41', commands: ['backup'], dates: ['2026-10-10 13:11:41'] }],
    warnings: [{ id: 'wa-EfGh', label: 'repo1-retention-full not set', message: "option 'repo1-retention-full' is not set", count: 2, commands: ['backup'] }],
    pg_retry_warnings: 0, pg_restore_failures: 0,
});

test('no pgBackRest data: the section publishes the shared no-data placeholder', () => {
    hasAll(buildPgBackRestSection({}), 'id="pgbackrest"', 'No data available', 'pgBackRest log file');
});

test('a stanza with lines_read: 0 and nothing else is filtered out', () => {
    assert.ok(render([{ stanza: 'ghost', lines_read: 0 }]).includes('No data available'));
});

test('lines_read: 0 does not hide a stanza drawn from PostgreSQL archive_command failures', () => {
    const html = render([{ stanza: 'demo', lines_read: 0, pg_archive_failures: [{ segment: '000000010000000000000010', exit_code: 103, date: '2026-01-14 13:07:00' }] }]);
    assert.ok(html.includes('id="pgbackrest-checks"'));
});

test('the section is three rows: one card, then two pairs of cards', () => {
    const html = render([nominal()]);
    hasAll(html, ...['pgbackrest', 'pgbackrest-checks', 'pgbackrest-events', 'pgbackrest-backups', 'pgbackrest-wal'].map(id => `id="${id}"`));
    assert.equal(html.split('class="grid grid-2"').length - 1, 2);
});

test('the header gives the period, the version and what was read, not the stanzas', () => {
    hasAll(text(render([nominal()])), '10 Oct 2026 13:04:55 → 13:14:11', 'pgBackRest 2.59.3', '154 lines read in 2 files');
});

test('the --begin/--end window is stated only in a pgBackRest-only report', () => {
    const st = { ...nominal(), window: { begin: '2026-10-10 13:10:00', cut_before: true, cut_after: false } };
    assert.ok(!text(render([st])).includes('Window'));
    assert.ok(text(render([st], { pgbackrest_only: true })).includes('Window from 2026-10-10 13:10:00 (lines before it left out)'));
});

test('a setting that hid information is named in an amber banner, with pgbackrest.conf', () => {
    const st = { ...nominal(), missing_info: ['1 invocation logged activity (warnings/errors) but no command begin/end lines of their own'] };
    const html = render([st]);
    hasAll(html, '⚠ Missing information', '<code>log-level-file = info</code>');
    assert.ok(text(html).includes('in pgbackrest.conf'));
    const ts = render([{ ...nominal(), missing_info: ['log-timestamp=n: x.log carries no timestamps'] }]);
    assert.ok(ts.includes('<code>log-timestamp = y</code>'));
    assert.ok(!render([nominal()]).includes('Missing information'));
});

test('a stanza card is never "Healthy" when run outcomes are not in the log', () => {
    const ok = { ...nominal(), backups: [nominal().backups[0]], errors: [] };
    assert.ok(text(render([ok])).includes('Healthy'));
    const hidden = { ...ok, missing_info: ['1 invocation logged activity but no command begin/end lines'] };
    assert.ok(text(render([hidden])).includes('Some outcomes not logged'));
});

test('a stanza card reads its state: recovered only by a later success, else needs attention', () => {
    // The last backup failed: nothing recovered it.
    assert.ok(text(render([nominal()])).includes('Needs attention'));
    const later = nominal();
    later.backups.push({ label: '20261010-131300F', type: 'full', start_date: '2026-10-10 13:13:00', end_date: '2026-10-10 13:13:20', duration_ms: 20000, succeeded: true });
    assert.ok(text(render([later])).includes('Recovered from failures'));
    const lost = { ...later, archive_push: stats({ pushed: 3, dropped_wal: [{ segment: '000000010000000000000013', date: '2026-10-10 13:10:00' }] }) };
    hasAll(text(render([lost])), 'Needs attention', '1 WAL lost');
});

test('archive_command failures only PostgreSQL counted: no ✓, stated on the card', () => {
    const st = { ...nominal(), archive_push: stats({ pushed: 3, last_push: { segment: 'A', date: '2026-10-10 13:07:00', mode: 'sync' } }),
        pg_archive_failures: [{ segment: 'B', exit_code: 103, date: '2026-10-10 13:08:00' }] };
    const html = render([st]);
    hasAll(text(html), 'Needs attention', 'PostgreSQL: 1 archive_command failures');
    for (const check of ['Archiving never stopped', 'Archiving works at the end of the log']) {
        assert.ok(new RegExp(`${check}</td><td class="pgbr-center"><span class="pgbr-no">`).test(html), check);
    }
});

test('"WAL never expired" only when no expire run removed WAL', () => {
    const st = { ...nominal(), retention_warnings: [{ text: "option 'repo1-retention-archive' is not set - archive logs will not be expired", count: 1 }] };
    assert.ok(text(render([st])).includes('WAL never expired'));
    st.expire_runs = [{ date: '2026-10-10 13:11:20', repo: 'repo1', archive_removed: 1, succeeded: true }];
    assert.ok(!text(render([st])).includes('WAL never expired'));
});

test('backups not logged (a PostgreSQL log alone) read "not logged", never 0', () => {
    const pgOnly = { stanza: 'main', lines_read: 20, archive_push: stats({ pushed: 4 }), push_dates: ['2026-10-10 13:05:00'], start_date: '2026-10-10 13:05:00', end_date: '2026-10-10 13:06:00' };
    const html = render([pgOnly]);
    const t = text(html);
    hasAll(t, 'Backups not logged', 'backups are not in this log');
    assert.ok(!t.includes('0 ok'));
    // The checks the log cannot answer read "–".
    assert.ok(html.includes('title="not in this log">–'));
});

test('the last full is named under its age, with its repository when the backups use several', () => {
    assert.ok(text(render([nominal()])).includes('since the last full, 20261010-130516F, ended'));
    const two = nominal();
    two.backups.push({ label: '20261010-131000F', type: 'full', repo: 'repo2', start_date: '2026-10-10 13:10:00', end_date: '2026-10-10 13:10:20', duration_ms: 20000, size: '29.4MB', size_bytes: 1, succeeded: true });
    two.last_full = { label: '20261010-131000F', repo: 'repo2', end_date: '2026-10-10 13:10:20' };
    assert.ok(text(render([two])).includes('since the last full, 20261010-131000F on repo2, ended'));
});

test('outages are worded as the text report: count, outcome, longest', () => {
    const st = { ...nominal(), archive_push: stats({ pushed: 3, failures: [
        { segment: 'A', code: 103, attempts: 3, first_date: '2026-10-10 13:06:00', last_date: '2026-10-10 13:07:19', recovery: 'recovered', cause: 'archive.info permission denied' },
        { segment: 'B', code: 103, attempts: 2, first_date: '2026-10-10 13:08:00', last_date: '2026-10-10 13:08:02', recovery: 'recovered', cause: 'archive.info missing' },
    ] }) };
    assert.ok(text(render([st])).includes('2 outages, all recovered, longest 1m19s'));
});

test('the Backups tab chains each diff and incr to its full, the expired ones struck', () => {
    const st = nominal();
    st.backups.push({ label: '20261010-130516F_20261010-130828I', type: 'incr', start_date: '2026-10-10 13:08:28', end_date: '2026-10-10 13:08:30', duration_ms: 2000, size: '8.3KB', succeeded: true });
    st.expire_runs = [{ date: '2026-10-10 13:11:20', repo: 'repo1', expired: ['20261010-130516F_20261010-130828I'], archive_removed: 0, succeeded: true }];
    const html = render([st]);
    hasAll(html, 'class="pgbr-chain"', 'pgbr-chip gone');
    assert.ok(text(html).includes('1 expired (retention)'));
});

test('the Repositories tab gives each repository its latest backup and its errors', () => {
    const st = nominal();
    st.errors.push({ id: 'er-X', code: 104, label: 'repo2 archive.info permission denied', message: 'archive-push command encountered error(s):', repo_line: 'repo2: [FileOpenError] unable to load info file', count: 21, commands: ['archive-push'] });
    hasAll(text(render([st])), 'repo2', '21 errors naming it');
});

test('the events card opens on OPERATIONS and counts each tab as the PostgreSQL card does', () => {
    const html = render([nominal()]);
    assert.ok(/<ql-tab selected>OPERATIONS<ql-badge>1<\/ql-badge>/.test(html));
    // An error row: its code, its pgBackRest name, the label from the JSON.
    hasAll(html, '<ql-tab class="error">ERROR<ql-badge>1</ql-badge>', '<ql-tab class="warning">WARNING<ql-badge>2</ql-badge>',
        'DbConnectError', 'onclick="showPgBackRestIssue(0)"');
});

test('the --event-detail ID is the CLI\'s: never shown in the HTML', () => {
    const html = render([nominal()]);
    assert.ok(!html.includes('er-AbCd') && !html.includes('wa-EfGh'));
});

test('an operation of a log-timestamp=n file is listed last, at "?"', () => {
    const st = nominal();
    st.operations.push({ command: 'check', summary: 'x', detail: 'WAL 000000010000000000000007 archived on repo1', succeeded: true, has_outcome: true });
    const html = render([st]);
    const ops = html.slice(html.indexOf('pgbr-op-time'));
    assert.ok(ops.indexOf('>?</td>') > ops.indexOf('>13:04:55</td>'));
});

test('the failed backups table words the cause from the JSON, and a killed run', () => {
    const st = nominal();
    st.backups.push({ type: 'diff', start_date: '2026-10-10 13:07:59', last_line_date: '2026-10-10 13:07:59', succeeded: false, crashed: true, signal: 'SIGTERM' });
    hasAll(text(render([st])), 'Failed backups', 'unable to find primary cluster failed [056]', 'terminated by SIGTERM, last line at 13:07:59 killed');
});

test('the backups and WAL charts are registered with what they draw', () => {
    chartData.clear();
    render([nominal()]);
    const b = chartData.get('chart-pgbr-backups'), w = chartData.get('chart-pgbr-wal');
    assert.equal(b.type, 'pgbr-backups');
    assert.deepEqual(b.backups.map(x => x.kind), ['ok', 'failed']);
    assert.equal(w.type, 'pgbr-wal');
    assert.equal(w.series[0].dates.length, 3);
});

test('a report whose lines named no stanza is named "(from PostgreSQL log)"', () => {
    assert.ok(text(render([{ ...nominal(), stanza: '' }])).includes('(from PostgreSQL log)'));
});

// ---- the lab runs ----------------------------------------------------------------

test('S2: the WAL chart has one date per WAL counted, and the outages table names each one', () => {
    const pbr = golden('S2');
    for (const s of pbr) {
        assert.equal((s.push_dates || []).length, s.archive_push.pushed + s.archive_push.pushed_async, s.stanza);
    }
    hasAll(text(render(pbr)), 'Outages and WAL lost', 'archive.info permission denied');
});

test('S3: lock errors read the label, killed backups are listed, operations include stop and start', () => {
    const t = text(render(golden('S3')));
    hasAll(t, 'lock held by another command', 'LockAcquireError', 'killed');
    assert.ok(/ stop /.test(t));
    assert.ok(/ start /.test(t));
});

test('S5: two repositories, each named on the failed backups and the last full', () => {
    const html = render(golden('S5'));
    assert.ok(html.includes('<th>Repo</th>'));
    assert.ok(text(html).includes('on repo2, ended'));
});

test('S1: restores are listed in OPERATIONS with their destination and target', () => {
    const t = text(render(golden('S1')));
    assert.ok(/into \/var\/lib\/pg\/restore\d, (latest|to \d\d:\d\d:\d\d)/.test(t));
});

// ---- archive-get and PostgreSQL's side ----------------------------------------

test('S7: archive-get puts a Restore tab beside Archiving, with its chart and table', () => {
    chartData.clear();
    const html = render(golden('S7'));
    hasAll(html, 'pgBackRest WAL<', '<ql-tab selected>Archiving</ql-tab><ql-tab>Restore</ql-tab>');
    const r = chartData.get('chart-pgbr-restore');
    assert.equal(r.type, 'pgbr-wal');
    assert.equal(r.verb, 'restored');
    assert.deepEqual(r.series.map(s => s.name), ['restore2', 'standby', 'restore3', 'restore4']);
    assert.equal(r.lost.length, 2);
    hasAll(text(html), '54 WAL restored (15 async)', 'Outages and missing WAL', 'pg1-path is not the data directory',
        'not in the archive, pushed at 20:40:06 WAL missing');
});

test('S7: the stanza card and the checks read the missing WAL, the restores their recovery', () => {
    const html = render(golden('S7'));
    hasAll(text(html), 'Needs attention', '1 WAL missing');
    hasAll(html, '<div class="pgbr-op-detail bad">recovery: archive-get failed [032]</div>',
        '<div class="pgbr-op-detail bad">recovery: WAL 00000001000000000000001C missing</div>');
    for (const check of ['No WAL missing from the repository', 'Every recovery got its WAL']) {
        assert.ok(new RegExp(`${check}</td><td class="pgbr-center"><span class="pgbr-no">`).test(html), check);
    }
});

test('no archive-get logged: no Restore tab, the archive-get checks read "–"', () => {
    const html = render([nominal()]);
    assert.ok(html.includes('pgBackRest WAL Archiving<'));
    assert.ok(!html.includes('<ql-tab>Restore</ql-tab>'));
    assert.ok(/No WAL missing from the repository<\/td><td class="pgbr-center"><span class="pgbr-na"/.test(html));
});

test('PostgreSQL gave up archiving: a fact above the chart, a check, a column per outage', () => {
    const st = { ...nominal(), pg_retry_warnings: 2, pg_retry_dates: ['2026-10-10 13:06:30', '2026-10-10 13:06:50'],
        pg_archive_failures: [{ segment: 'A', exit_code: 103, date: '2026-10-10 13:06:10' }, { segment: 'A', exit_code: 103, date: '2026-10-10 13:06:40' }],
        archive_push: stats({ pushed: 3, failures: [{ segment: 'A', code: 103, attempts: 2, first_date: '2026-10-10 13:06:00', last_date: '2026-10-10 13:07:00', recovery: 'recovered', cause: 'archive.info missing' }] }) };
    const html = render([st]);
    hasAll(text(html), 'PostgreSQL: 2 archive_command failures (exit 103) gave up 2 times', '2 failed, gave up 2×');
    assert.ok(/PostgreSQL never gave up archiving<\/td><td class="pgbr-center"><span class="pgbr-no">/.test(html));
    assert.ok(html.includes('<th class="num">PostgreSQL</th>'));
    // A pgBackRest-only report has no PostgreSQL log to tell.
    assert.ok(/PostgreSQL never gave up archiving<\/td><td class="pgbr-center"><span class="pgbr-na"/.test(render([st], { pgbackrest_only: true })));
});

test('the async worker\'s share of the pushes is stated above the chart', () => {
    const st = { ...nominal(), archive_push: stats({ pushed: 20, pushed_async: 4, failed_async: 6 }) };
    assert.ok(text(render([st])).includes('24 pushed, 4 through the async worker async worker: 6 failed'));
});
