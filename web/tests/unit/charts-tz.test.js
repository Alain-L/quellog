// FIX #4 (MED) — event modal First/Last-seen (UTC) must agree with the modal's
// own sparkline. The cards render via utils.js utcDateTime (log's zone-less
// clock); the chart time axis/tooltips used to format the SAME epoch array with
// toLocaleTime/DateString and NO timeZone option, i.e. the VIEWER's local zone,
// so cards and chart disagreed by the viewer offset (Paris: cards 02:00, chart
// 03:00). charts.js now formats its time axis in UTC via utcTimeHM/utcDateShort.
//
// This asserts the chart axis formatter yields the SAME wall-clock as
// utcDateTime for a given epoch, and stays stable across viewer timezones.
// Run under a non-UTC zone to actually exercise the divergence, e.g.:
//   TZ=America/New_York node --test charts-tz.test.js
//   TZ=Europe/Paris     node --test charts-tz.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { utcTimeHM, utcDateShort } from '../../js/charts.js';
import { utcDateTime } from '../../js/utils.js';

// Backend emits occurrence timestamps as epoch-ms of the log's own
// zone-normalized-to-UTC wall-clock. Pick 02:00 UTC on 2025-01-01 — a value
// that lands on the previous day (21:00) for a New York viewer and an hour off
// (03:00) for a Paris viewer when formatted local.
const EPOCH_MS = Date.UTC(2025, 0, 1, 2, 0, 0);

test('chart time axis (utcTimeHM) matches the First/Last-seen card (utcDateTime)', () => {
    // The card shows "2025-01-01 02:00:00"; its HH:MM slice is "02:00".
    const cardHM = utcDateTime(new Date(EPOCH_MS)).slice(11, 16);
    const chartHM = utcTimeHM(new Date(EPOCH_MS));
    console.log(`  TZ=${process.env.TZ || '(system)'}  card="${cardHM}"  chart="${chartHM}"`);
    assert.equal(chartHM, cardHM, 'chart axis wall-clock equals card wall-clock');
    assert.equal(chartHM, '02:00', 'both show the log clock 02:00 regardless of viewer TZ');
});

test('chart short date (utcDateShort) is the log-clock date, not the viewer-local date', () => {
    // Under America/New_York this epoch is on 2024-12-31 local, but the report
    // clock (UTC) is 2025-01-01, so the axis date must read "Jan 1".
    assert.equal(utcDateShort(new Date(EPOCH_MS)), 'Jan 1');
});

test('helpers are viewer-timezone-invariant (would drift if they used local time)', () => {
    // A pure local-zone formatter, kept here only as the negative control: it is
    // exactly what charts.js used to do. It must NOT equal our UTC helper under a
    // non-UTC runtime, proving the fix removes the viewer-offset dependency.
    const localHM = new Date(EPOCH_MS).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
    const offsetMin = new Date(EPOCH_MS).getTimezoneOffset(); // 0 only when runtime is UTC
    if (offsetMin === 0) {
        console.log('  runtime TZ is UTC — local and UTC coincide; re-run with TZ=Europe/Paris to see the drift');
        assert.equal(utcTimeHM(new Date(EPOCH_MS)), localHM);
    } else {
        console.log(`  offset ${-offsetMin} min  ->  local="${localHM}"  utc="${utcTimeHM(new Date(EPOCH_MS))}"`);
        assert.notEqual(utcTimeHM(new Date(EPOCH_MS)), localHM, 'UTC helper does not track the viewer zone');
    }
});
