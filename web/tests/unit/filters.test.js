// Unit tests for the pure (DOM-free) helpers of web/js/filters.js:
// computeDayAxis, initTimeFilter, minutesToTime, formatDateHuman,
// offsetToTimeStr, offsetToDatetime, MAX_CANVAS_DAYS.
//
// Everything else in filters.js touches document/window and is out of scope
// for Phase 0 (no fake DOM). offsetToDateTimeStr is not exported, so it is
// not tested directly.
//
// Timezone safety: computeDayAxis and the offset helpers work in local time
// (new Date(y, m, d) midnights). Expectations are computed with the same
// Date APIs instead of hard-coded epoch values, so the tests pass in any TZ.
// Fixture dates sit in mid-January, away from DST transitions.

import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert/strict';

import {
    computeDayAxis, initTimeFilter, minutesToTime, formatDateHuman,
    offsetToTimeStr, offsetToDatetime, MAX_CANVAS_DAYS
} from '../../js/filters.js';
import * as state from '../../js/state.js';

const DAY_MS = 86400000;

// Local-midnight helper mirroring the module's axis computation.
const midnight = (y, m, d) => new Date(y, m - 1, d).getTime();
const localTs = (s) => new Date(s.replace(' ', 'T')).getTime();

function resetTimeState() {
    state.setTimeFilterStartTs(null);
    state.setTimeFilterEndTs(null);
    state.setTimeFilterDurationMins(0);
    state.setTimeFilterSelMin(0);
    state.setTimeFilterSelMax(0);
    state.setTimeFilterDefMin(0);
    state.setTimeFilterDefMax(0);
}

describe('MAX_CANVAS_DAYS', () => {
    it('is the documented 8-day cutoff', () => {
        assert.equal(MAX_CANVAS_DAYS, 8);
    });
});

describe('computeDayAxis', () => {
    it('builds a single full-day axis for an intra-day span', () => {
        const a = computeDayAxis('2025-01-15 08:30:00', '2025-01-15 17:45:00');
        assert.equal(a.axisStart, midnight(2025, 1, 15));
        assert.equal(a.axisEnd, midnight(2025, 1, 16));
        assert.equal(a.nDays, 1);
        assert.equal(a.durMins, 1440);
        assert.equal(a.startTs, localTs('2025-01-15 08:30:00'));
        assert.equal(a.endTs, localTs('2025-01-15 17:45:00'));
        assert.equal(a.defMin, Math.round((a.startTs - a.axisStart) / 60000)); // 510 in a DST-free day
        assert.equal(a.defMax, Math.round((a.endTs - a.axisStart) / 60000));   // 1065
    });

    it('spans full calendar days for a multi-day range', () => {
        const a = computeDayAxis('2025-01-13 22:00:00', '2025-01-15 03:00:00');
        assert.equal(a.axisStart, midnight(2025, 1, 13));
        assert.equal(a.axisEnd, midnight(2025, 1, 16));
        assert.equal(a.nDays, 3);
        assert.equal(a.durMins, 3 * 1440);
        assert.equal(a.defMin, Math.round((a.startTs - a.axisStart) / 60000)); // 1320
        assert.equal(a.defMax, Math.round((a.endTs - a.axisStart) / 60000));   // 3060
    });

    it('folds away a negligible trailing day and clamps defMax', () => {
        // Ends 30s into day 15 (< 5 min coverage): day 15 is dropped.
        const a = computeDayAxis('2025-01-14 06:00:00', '2025-01-15 00:00:30');
        assert.equal(a.axisStart, midnight(2025, 1, 14));
        assert.equal(a.axisEnd, midnight(2025, 1, 15));
        assert.equal(a.nDays, 1);
        assert.equal(a.durMins, 1440);
        // Raw offset would be 1440.5 min -> rounded 1441, clamped to the axis.
        assert.equal(a.defMax, 1440);
    });

    it('folds away a negligible leading day and clamps defMin', () => {
        // Starts 2 min before midnight: day 14 is dropped.
        const a = computeDayAxis('2025-01-14 23:58:00', '2025-01-15 12:00:00');
        assert.equal(a.axisStart, midnight(2025, 1, 15));
        assert.equal(a.axisEnd, midnight(2025, 1, 16));
        assert.equal(a.nDays, 1);
        // Raw offset would be -2 min, clamped to 0.
        assert.equal(a.defMin, 0);
        assert.equal(a.defMax, Math.round((a.endTs - a.axisStart) / 60000)); // 720
    });

    it('never folds a single-day axis away', () => {
        // 90 seconds of data just after midnight: both fold guards require
        // the axis to stay > 1 day, so the lone day survives.
        const a = computeDayAxis('2025-01-15 00:00:10', '2025-01-15 00:01:40');
        assert.equal(a.nDays, 1);
        assert.equal(a.axisStart, midnight(2025, 1, 15));
        assert.equal(a.axisEnd, midnight(2025, 1, 16));
    });
});

describe('initTimeFilter', () => {
    beforeEach(resetTimeState);

    it('publishes the computed axis into module state', () => {
        const start = '2025-01-13 22:00:00';
        const end = '2025-01-15 03:00:00';
        const a = computeDayAxis(start, end);

        initTimeFilter(start, end);

        assert.equal(state.timeFilterStartTs, a.axisStart);
        assert.equal(state.timeFilterEndTs, a.axisEnd);
        assert.equal(state.timeFilterDurationMins, a.durMins);
        assert.equal(state.timeFilterDefMin, a.defMin);
        assert.equal(state.timeFilterDefMax, a.defMax);
        // Selection starts at the no-filter baseline.
        assert.equal(state.timeFilterSelMin, a.defMin);
        assert.equal(state.timeFilterSelMax, a.defMax);
    });

    it('is a no-op when either bound is missing', () => {
        initTimeFilter(null, '2025-01-15 03:00:00');
        initTimeFilter('2025-01-13 22:00:00', undefined);
        assert.equal(state.timeFilterStartTs, null);
        assert.equal(state.timeFilterDurationMins, 0);
        assert.equal(state.timeFilterSelMax, 0);
    });
});

describe('minutesToTime', () => {
    it('renders zero-padded HH:MM from a minute offset', () => {
        assert.equal(minutesToTime(0), '00:00');
        assert.equal(minutesToTime(5), '00:05');
        assert.equal(minutesToTime(75), '01:15');
        assert.equal(minutesToTime(1439), '23:59');
    });

    it('runs past 24h without wrapping (axis end label)', () => {
        assert.equal(minutesToTime(1440), '24:00');
        assert.equal(minutesToTime(2880), '48:00');
    });
});

describe('formatDateHuman', () => {
    it('formats YYYY-MM-DD as "D Mon YYYY"', () => {
        assert.equal(formatDateHuman('2025-01-05'), '5 Jan 2025');
        assert.equal(formatDateHuman('2025-12-31'), '31 Dec 2025');
    });

    it('returns empty string for falsy input', () => {
        assert.equal(formatDateHuman(''), '');
        assert.equal(formatDateHuman(null), '');
    });

    it('passes through strings that are not three dash-separated parts', () => {
        assert.equal(formatDateHuman('garbage'), 'garbage');
        assert.equal(formatDateHuman('2025-01'), '2025-01');
    });

    it('falls back to the raw month field when out of range', () => {
        assert.equal(formatDateHuman('2025-13-05'), '5 13 2025');
    });
});

describe('offsetToTimeStr', () => {
    beforeEach(resetTimeState);

    it('falls back to minutesToTime when no axis start is set', () => {
        assert.equal(offsetToTimeStr(75), '01:15');
        assert.equal(offsetToTimeStr(1440), '24:00');
    });

    it('renders wall-clock HH:MM from the axis start', () => {
        const base = midnight(2025, 1, 15);
        state.setTimeFilterStartTs(base);

        for (const offset of [0, 510, 1439, 1500]) {
            const d = new Date(base + offset * 60000);
            const expected =
                String(d.getHours()).padStart(2, '0') + ':' +
                String(d.getMinutes()).padStart(2, '0');
            assert.equal(offsetToTimeStr(offset), expected, `offset ${offset}`);
        }
        // Mid-January midnight base: no DST in play, so the literal holds too.
        assert.equal(offsetToTimeStr(510), '08:30');
    });
});

describe('offsetToDatetime', () => {
    beforeEach(resetTimeState);

    it('returns null when no axis start is set', () => {
        assert.equal(offsetToDatetime(510), null);
    });

    it('renders the Go-style "YYYY-MM-DD HH:MM:SS" local datetime', () => {
        const base = midnight(2025, 1, 15);
        state.setTimeFilterStartTs(base);

        const out = offsetToDatetime(510);
        assert.match(out, /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
        assert.equal(out, '2025-01-15 08:30:00');

        // Crossing into the next calendar day.
        const out2 = offsetToDatetime(1440 + 65);
        assert.equal(out2, '2025-01-16 01:05:00');
    });

    it('always renders :00 seconds (minute granularity input)', () => {
        state.setTimeFilterStartTs(midnight(2025, 1, 15));
        assert.match(offsetToDatetime(123), /:00$/);
    });
});

describe('computeDayAxis DST safety', () => {
    it('data-end offset lands on the calendar grid, not real elapsed minutes', () => {
        // Across a DST spring-forward/fall-back a calendar day is 23h/25h, so
        // real elapsed minutes drift from the uniform 1440-min-per-day grid the
        // labels use. The offset must be dayIndex*1440 + minutes-since-local-
        // midnight. Deterministic in any timezone: expected is computed the same
        // (calendar) way; in a DST zone the old (ts-axisStart)/60000 would differ.
        const a = computeDayAxis('2026-03-28 10:15:00', '2026-03-30 14:30:00');
        const startMidnight = new Date(2026, 2, 28).getTime();
        const endMidnight = new Date(2026, 2, 30).getTime();
        const dayIdx = Math.round((endMidnight - startMidnight) / 86400000);
        const expected = dayIdx * 1440 +
            Math.round((new Date(2026, 2, 30, 14, 30).getTime() - endMidnight) / 60000);
        assert.equal(a.defMax, expected);
    });
});
