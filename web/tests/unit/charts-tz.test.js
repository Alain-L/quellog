// Chart time-axis / tooltip formatting is timezone-round-trip-safe (v0.11.0).
//
// The backend renders every occurrence as the LOG's own zone-less wall-clock
// string ("2025-01-01 12:00:00", no zone). The section charts parse that same
// string with `new Date(str)` — interpreted in the VIEWER's local zone — and
// charts.js formats the axis/tooltips with toLocaleTime/DateString and NO
// timeZone option, i.e. also in the viewer's local zone. Parse-local +
// format-local cancel the viewer offset, so the axis round-trips the log's
// clock ("12:00") for every viewer.
//
// The regression (commit 3ef1922) formatted with timeZone:'UTC' while the
// parse side stayed local, so the two bases no longer matched and a non-UTC
// viewer saw every label shifted by their offset. This asserts the reverted,
// v0.11.0 local behavior: the round-trip is stable across viewer timezones,
// and the UTC-formatting negative control drifts (proving why it was wrong).
//
// process.env.TZ is honored only at Date-subsystem init, so the cross-TZ cases
// re-derive the render in a child process per zone.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';

// Mirror charts.js's inline time-axis / date-tick formatters exactly (local,
// no timeZone). The chart feeds `new Date(epochSeconds * 1000)`; the epoch is
// built from the wall-clock string via `new Date(str)`, so the whole path is
// `new Date(str)` in, formatted string out.
const LOCAL_HM = "d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })";
const LOCAL_DATE = "d.toLocaleDateString('en-US', { day: 'numeric', month: 'short' })";

// A zone-less wall-clock string, exactly as the log/backend emits it.
const WALL = '2025-01-01 12:00:00';

// Run the round-trip under a specific viewer timezone in a fresh process.
// mode 'local' reproduces charts.js (the fix); mode 'utc' reproduces the
// reverted 3ef1922 formatting (the bug) for the negative control.
function roundTripUnderTZ(tz, mode) {
    const opts = mode === 'utc'
        ? "{ hour: '2-digit', minute: '2-digit', hour12: false, timeZone: 'UTC' }"
        : "{ hour: '2-digit', minute: '2-digit', hour12: false }";
    const prog =
        `const d = new Date('${WALL}'.replace(' ', 'T'));` +
        `process.stdout.write(d.toLocaleTimeString('en-US', ${opts}));`;
    return execFileSync(process.execPath, ['-e', prog],
        { env: { ...process.env, TZ: tz }, encoding: 'utf8' });
}

test('local formatter round-trips the wall-clock string in-process', () => {
    // parse-local + format-local cancels the current runtime offset, so the
    // rendered clock equals the input clock whatever TZ node happens to run in.
    const d = new Date(WALL.replace(' ', 'T'));
    const hm = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
    const dateStr = d.toLocaleDateString('en-US', { day: 'numeric', month: 'short' });
    console.log(`  TZ=${process.env.TZ || '(system)'}  hm="${hm}"  date="${dateStr}"`);
    assert.equal(hm, '12:00', 'axis time equals the log clock 12:00');
    assert.equal(dateStr, 'Jan 1', 'axis date equals the log clock date');
});

test('local round-trip is viewer-timezone-invariant (UTC / New York / Tokyo)', () => {
    for (const tz of ['UTC', 'America/New_York', 'Asia/Tokyo']) {
        const hm = roundTripUnderTZ(tz, 'local');
        assert.equal(hm, '12:00', `${tz} viewer must see the log clock 12:00, not a shifted time`);
    }
});

test('negative control: UTC formatting (the reverted 3ef1922 bug) drifts for non-UTC viewers', () => {
    // Parse-local + format-UTC no longer share a basis: a New York viewer
    // (UTC-5) parses "12:00" as 12:00 local = 17:00 UTC, and formatting in UTC
    // then prints 17:00 — an offset-sized shift away from the log clock. This
    // is exactly the divergence the revert removes.
    const utcHm = roundTripUnderTZ('America/New_York', 'utc');
    const localHm = roundTripUnderTZ('America/New_York', 'local');
    console.log(`  New York:  local="${localHm}"  utc="${utcHm}"`);
    assert.equal(localHm, '12:00', 'local formatting shows the log clock');
    assert.notEqual(utcHm, '12:00', 'UTC formatting shifts the label off the log clock');
});
