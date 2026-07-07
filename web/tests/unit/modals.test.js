// Unit tests for the pure (DOM-free) helpers behind the query-detail and
// event-detail modals — the fixes that shipped without test exercise (the
// visual harness never opens a modal). See docs/web-data-contract.md.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';

// The helpers are homed in utils.js (DOM-free) precisely so they are testable —
// importing modals.js in node fails (it pulls charts.js, which registers a
// document keydown listener at module load).
import { qdDurationBuckets, utcDateTime } from '../../js/utils.js';

test('qdDurationBuckets reads duration_ms and buckets like the backend', () => {
    const execs = [
        { duration_ms: 0.5 },    // < 1 ms
        { duration_ms: 5 },      // < 10 ms
        { duration_ms: 5 },      // < 10 ms
        { duration_ms: 100 },    // < 1 s  (100 is not < 100)
        { duration_ms: 15000 },  // >= 10 s
        { duration: '5 ms' },    // old (non-existent) field -> ignored
        { duration_ms: null },   // ignored
    ];
    const dist = qdDurationBuckets(execs);
    const count = (label) => dist.find(b => b.label === label).count;
    assert.equal(count('< 1 ms'), 1);
    assert.equal(count('< 10 ms'), 2);
    assert.equal(count('< 100 ms'), 0);
    assert.equal(count('< 1 s'), 1);
    assert.equal(count('< 10 s'), 0);
    assert.equal(count('>= 10 s'), 1);
    // Only the 5 numeric duration_ms values count; the `duration` string and the
    // null are ignored (the bug: reading `duration` made every value 0, so the
    // whole block filtered out and never rendered).
    assert.equal(dist.reduce((s, b) => s + b.count, 0), 5);
});

test('utcDateTime formats the epoch-ms UTC wall-clock (zero-padded)', () => {
    // The modal feeds `new Date(epochMs)`; utcDateTime reads the UTC components
    // so it reproduces the log's own zone-less wall-clock (the rest of the
    // report shows those strings). Date.UTC builds the absolute instant.
    assert.equal(utcDateTime(new Date(Date.UTC(2026, 0, 1, 23, 30, 15))), '2026-01-01 23:30:15');
    assert.equal(utcDateTime(new Date(Date.UTC(2026, 8, 5, 8, 5, 9))), '2026-09-05 08:05:09'); // zero-padding
});

test('utcDateTime is timezone-stable — same output under UTC and America/New_York', () => {
    // Regression for R4 finding 12: the modal's First/Last-seen used the
    // viewer-local getters, so the SAME instant rendered as different
    // wall-clocks per viewer TZ (23:30 UTC / 18:30 New York / 08:30+1d Tokyo),
    // disagreeing with the section tables. utcDateTime pins it to UTC.
    //
    // process.env.TZ is honored only at Date-subsystem init, so re-derive in a
    // child process per TZ and compare the two renders of a fixed epoch-ms.
    const url = new URL('../../js/utils.js', import.meta.url).href;
    const epochMs = Date.UTC(2026, 0, 15, 23, 30, 0); // UTC log wrote 23:30:00
    const prog =
        `import('${url}').then(m => process.stdout.write(m.utcDateTime(new Date(${epochMs}))))`;
    const run = (tz) => execFileSync(process.execPath, ['--input-type=module', '-e', prog],
        { env: { ...process.env, TZ: tz }, encoding: 'utf8' });

    const utc = run('UTC');
    const ny = run('America/New_York');
    const tokyo = run('Asia/Tokyo');
    assert.equal(utc, '2026-01-15 23:30:00');
    assert.equal(ny, utc, 'New York viewer must render the same wall-clock as UTC');
    assert.equal(tokyo, utc, 'Tokyo viewer must render the same wall-clock as UTC');
});
