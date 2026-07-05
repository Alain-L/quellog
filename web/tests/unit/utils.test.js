// Unit tests for web/js/utils.js — pure (DOM-free) exported functions only.
// esc() and escForJsAttr() need document.createElement and are deliberately
// NOT tested here (no fake DOM in Phase 0).
//
// Locale/Intl-dependent expectations are computed with the same APIs the
// module uses (toLocaleString, Intl.DurationFormat) so tests pass under any
// system locale / ICU build.

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import {
    fmt, fmtDuration, fmtDurationCoarse, fmtBytes, fmtCompact,
    fmtMs, fmtDur, parseDurToMs, safeMax, safeMin, escAttr, truncQuery
} from '../../js/utils.js';

// Same formatter the module uses internally.
const DF = new Intl.DurationFormat('en', { style: 'narrow' });

describe('fmt', () => {
    it('formats numbers with locale separators', () => {
        assert.equal(fmt(0), '0');
        assert.equal(fmt(42), (42).toLocaleString());
        assert.equal(fmt(1234567), (1234567).toLocaleString());
        assert.equal(fmt(-42), (-42).toLocaleString());
    });

    it('returns "0" for null and undefined', () => {
        assert.equal(fmt(null), '0');
        assert.equal(fmt(undefined), '0');
    });
});

describe('fmtDuration', () => {
    it('returns 0ms for zero, negative, null', () => {
        assert.equal(fmtDuration(0), '0ms');
        assert.equal(fmtDuration(-100), '0ms');
        assert.equal(fmtDuration(null), '0ms');
        assert.equal(fmtDuration(undefined), '0ms');
    });

    it('shows raw milliseconds below one second', () => {
        assert.equal(fmtDuration(1), '1ms');
        assert.equal(fmtDuration(500), '500ms');
        assert.equal(fmtDuration(999), '999ms');
        assert.equal(fmtDuration(999.4), '999ms');
    });

    it('rounds 999.6ms up to "1000ms" instead of promoting to 1s (quirk)', () => {
        // totalSeconds is floored (0) but the ms remainder is rounded (1000).
        assert.equal(fmtDuration(999.6), '1000ms');
    });

    it('drops the ms remainder once above one second', () => {
        assert.equal(fmtDuration(1000), DF.format({ seconds: 1 }));
        assert.equal(fmtDuration(1999), DF.format({ seconds: 1 }));
    });

    it('formats m/s and h/m/s combinations', () => {
        assert.equal(fmtDuration(61000), DF.format({ minutes: 1, seconds: 1 }));
        assert.equal(fmtDuration(60000), DF.format({ minutes: 1 }));
        assert.equal(fmtDuration(3600000), DF.format({ hours: 1 }));
        assert.equal(fmtDuration(3661000), DF.format({ hours: 1, minutes: 1, seconds: 1 }));
    });

    it('never converts hours to days (unlike fmtDurationCoarse)', () => {
        // 1d 1h 1m 1s worth of ms stays expressed in hours.
        assert.equal(fmtDuration(90061000), DF.format({ hours: 25, minutes: 1, seconds: 1 }));
    });
});

describe('fmtDurationCoarse', () => {
    it('returns 0ms for zero, negative, null', () => {
        assert.equal(fmtDurationCoarse(0), '0ms');
        assert.equal(fmtDurationCoarse(-1), '0ms');
        assert.equal(fmtDurationCoarse(null), '0ms');
    });

    it('shows raw milliseconds below one second', () => {
        assert.equal(fmtDurationCoarse(500), '500ms');
    });

    it('keeps only the two most significant units', () => {
        assert.equal(fmtDurationCoarse(45000), DF.format({ seconds: 45 }));
        assert.equal(fmtDurationCoarse(5 * 60000 + 17000), DF.format({ minutes: 5, seconds: 17 }));
        // 3h 59m 17s -> 3h 59m (seconds dropped past an hour)
        assert.equal(
            fmtDurationCoarse(3 * 3600000 + 59 * 60000 + 17000),
            DF.format({ hours: 3, minutes: 59 })
        );
        // 1d 1h 1m 1s -> 1d 1h (minutes dropped past a day)
        assert.equal(fmtDurationCoarse(90061000), DF.format({ days: 1, hours: 1 }));
    });

    it('omits a zero second unit', () => {
        assert.equal(fmtDurationCoarse(3600000), DF.format({ hours: 1 }));
        assert.equal(fmtDurationCoarse(86400000), DF.format({ days: 1 }));
    });
});

describe('fmtBytes', () => {
    it('formats bytes below 1 KiB', () => {
        assert.equal(fmtBytes(0), '0 B');
        assert.equal(fmtBytes(1), '1 B');
        assert.equal(fmtBytes(1023), '1023 B');
    });

    it('formats KB and MB with rounding', () => {
        assert.equal(fmtBytes(1024), '1 KB');
        assert.equal(fmtBytes(1536), '2 KB');       // Math.round(1.5) = 2
        assert.equal(fmtBytes(1048576), '1 MB');
        assert.equal(fmtBytes(5 * 1048576), '5 MB');
    });

    it('rounds up to "1024 KB" just under the MB boundary (quirk)', () => {
        // 1048575 B rounds to 1024 KB rather than switching to 1 MB.
        assert.equal(fmtBytes(1048575), '1024 KB');
        assert.equal(fmtBytes(1073741823), '1024 MB');
    });

    it('formats GB with one decimal, stripping trailing .0', () => {
        assert.equal(fmtBytes(1073741824), '1 GB');
        assert.equal(fmtBytes(1.5 * 1073741824), '1.5 GB');
        assert.equal(fmtBytes(2 * 1073741824), '2 GB');
    });
});

describe('fmtCompact', () => {
    it('returns "-" for null and negative', () => {
        assert.equal(fmtCompact(null), '-');
        assert.equal(fmtCompact(undefined), '-');
        assert.equal(fmtCompact(-5), '-');
    });

    it('passes small integers through', () => {
        assert.equal(fmtCompact(0), '0');
        assert.equal(fmtCompact(999), '999');
    });

    it('formats k / M / G with one decimal below 10 units', () => {
        assert.equal(fmtCompact(1000), '1.0k');
        assert.equal(fmtCompact(1500), '1.5k');
        assert.equal(fmtCompact(9999), '10.0k');
        assert.equal(fmtCompact(10000), '10k');
        assert.equal(fmtCompact(1_000_000), '1.0M');
        assert.equal(fmtCompact(2_500_000), '2.5M');
        assert.equal(fmtCompact(10_000_000), '10M');
        assert.equal(fmtCompact(1_000_000_000), '1.0G');
        assert.equal(fmtCompact(9_999_999_999), '10.0G');
        assert.equal(fmtCompact(15_000_000_000), '15G');
    });

    it('rounds up to "1000k"/"1000M" just under the next unit (quirk)', () => {
        assert.equal(fmtCompact(999_999), '1000k');
        assert.equal(fmtCompact(999_999_999), '1000M');
    });
});

describe('fmtMs', () => {
    it('returns "-" for null/undefined', () => {
        assert.equal(fmtMs(null), '-');
        assert.equal(fmtMs(undefined), '-');
    });

    it('shows "<1ms" below 1, including exactly 0 (quirk)', () => {
        assert.equal(fmtMs(0), '<1ms');
        assert.equal(fmtMs(0.5), '<1ms');
    });

    it('formats ms, s and m ranges', () => {
        assert.equal(fmtMs(1), '1.0ms');
        assert.equal(fmtMs(999.94), '999.9ms');
        assert.equal(fmtMs(1000), '1.00s');
        assert.equal(fmtMs(60000), '1.0m');
        assert.equal(fmtMs(90000), '1.5m');
    });

    it('shows "60.00s" just under the minute boundary (quirk)', () => {
        assert.equal(fmtMs(59999), '60.00s');
    });

    it('delegates string input to fmtDur', () => {
        assert.equal(fmtMs('500ms'), '500.0ms');
        assert.equal(fmtMs('1h30m'), '1h 30m');
    });
});

describe('fmtDur (Go duration strings)', () => {
    it('returns "-" for empty or dash', () => {
        assert.equal(fmtDur(''), '-');
        assert.equal(fmtDur(null), '-');
        assert.equal(fmtDur('-'), '-');
    });

    it('stringifies non-string input', () => {
        assert.equal(fmtDur(5), '5');
    });

    it('cleans up fractional-second Go durations', () => {
        // Seconds are Math.round()ed when combined with larger units,
        // as documented in the fmtDur doc-comment.
        assert.equal(fmtDur('2m7.663353305s'), '2m 8s');
        assert.equal(fmtDur('1.5s'), '1.50s');
    });

    it('formats pure milliseconds', () => {
        assert.equal(fmtDur('123.456ms'), '123.5ms');
        assert.equal(fmtDur('500ms'), '500.0ms');
    });

    it('formats h/m combinations and converts >=24h to days', () => {
        assert.equal(fmtDur('1h30m'), '1h 30m');
        assert.equal(fmtDur('25h30m'), '1d 1h 30m');
        assert.equal(fmtDur('48h'), '2d');
    });

    it('drops a trailing ms component after minutes (quirk)', () => {
        // Not a shape Go itself emits (Go would say "1m0.5s"), but the parser
        // silently loses the 500ms today.
        assert.equal(fmtDur('1m500ms'), '1m');
    });

    it('passes through zero and unparseable strings', () => {
        assert.equal(fmtDur('0s'), '0s');
        assert.equal(fmtDur('garbage'), 'garbage');
    });
});

describe('parseDurToMs', () => {
    it('returns 0 for empty, dash, null', () => {
        assert.equal(parseDurToMs(''), 0);
        assert.equal(parseDurToMs('-'), 0);
        assert.equal(parseDurToMs(null), 0);
    });

    it('coerces non-string numbers', () => {
        assert.equal(parseDurToMs(5000), 5000);
        assert.equal(parseDurToMs(NaN), 0);
    });

    it('parses individual and combined units', () => {
        assert.equal(parseDurToMs('2h'), 7200000);
        assert.equal(parseDurToMs('1h 30m'), 5400000);
        assert.equal(parseDurToMs('1m 30s'), 90000);
        assert.equal(parseDurToMs('45s'), 45000);
        assert.equal(parseDurToMs('250ms'), 250);
        assert.equal(parseDurToMs('123.5ms'), 123.5);
        assert.equal(parseDurToMs('1.5s'), 1500);
    });

    it('round-trips fmtDuration output', () => {
        for (const ms of [500, 45000, 90000, 3600000, 5405000]) {
            assert.equal(parseDurToMs(fmtDuration(ms)), ms, `round-trip ${ms}ms`);
        }
    });

    it('round-trips fmtDur output', () => {
        assert.equal(parseDurToMs(fmtDur('2m7.663353305s')), 128000); // "2m 8s"
        assert.equal(parseDurToMs(fmtDur('1h30m')), 5400000);
        assert.equal(parseDurToMs(fmtDur('48h')), 0, 'day unit "2d" is not parsed back (no d handling)');
    });
});

describe('safeMax / safeMin', () => {
    it('return 0 for empty arrays', () => {
        assert.equal(safeMax([]), 0);
        assert.equal(safeMin([]), 0);
    });

    it('handle single elements and negatives', () => {
        assert.equal(safeMax([7]), 7);
        assert.equal(safeMin([7]), 7);
        assert.equal(safeMax([-5, -3, -9]), -3);
        assert.equal(safeMin([-5, -3, -9]), -9);
        assert.equal(safeMax([3, 1, 2]), 3);
        assert.equal(safeMin([3, 1, 2]), 1);
    });

    it('handle large arrays without call-stack issues', () => {
        const big = Array.from({ length: 500000 }, (_, i) => i);
        assert.equal(safeMax(big), 499999);
        assert.equal(safeMin(big), 0);
    });
});

describe('escAttr', () => {
    it('returns empty string for null/undefined', () => {
        assert.equal(escAttr(null), '');
        assert.equal(escAttr(undefined), '');
    });

    it('escapes all five HTML attribute specials', () => {
        assert.equal(
            escAttr(`<a href="x" onmouseover='y'>&z</a>`),
            '&lt;a href=&quot;x&quot; onmouseover=&#39;y&#39;&gt;&amp;z&lt;/a&gt;'
        );
    });

    it('stringifies non-string input and keeps plain text intact', () => {
        assert.equal(escAttr(42), '42');
        assert.equal(escAttr('plain text'), 'plain text');
        assert.equal(escAttr(''), '');
    });
});

describe('truncQuery', () => {
    it('passes through falsy and short strings unchanged', () => {
        assert.equal(truncQuery(null), null);
        assert.equal(truncQuery(''), '');
        assert.equal(truncQuery('short'), 'short');
    });

    it('keeps a string exactly at the limit', () => {
        const s = 'x'.repeat(120);
        assert.equal(truncQuery(s), s);
    });

    it('truncates above the limit and appends an ellipsis', () => {
        const s = 'a'.repeat(121);
        const out = truncQuery(s);
        assert.equal(out, 'a'.repeat(120) + '…');
        assert.equal(out.length, 121);
    });

    it('honors a custom max', () => {
        assert.equal(truncQuery('abcdefgh', 5), 'abcde…');
        assert.equal(truncQuery('abcde', 5), 'abcde');
    });
});
