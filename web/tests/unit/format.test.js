// Unit tests for web/js/format.js — byte-size formatting/parsing helpers.
// These lock in the exact historical output of the three formatter variants
// and the two parser variants that Phase 2 consolidated behind one unit
// table. Any assertion change here means a rendered string changed.

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import {
    fmtBytes, fmtBytesPrecise, fmtBytesShort, fmtBytesFull,
    parseSizeToBytes, parseSizeToBytesStrict
} from '../../js/format.js';

const KB = 1024;
const MB = 1024 ** 2;
const GB = 1024 ** 3;
const TB = 1024 ** 4;

describe('fmtBytes (rounded variant)', () => {
    it('formats bytes below 1 KiB verbatim', () => {
        assert.equal(fmtBytes(0), '0 B');
        assert.equal(fmtBytes(1), '1 B');
        assert.equal(fmtBytes(1000), '1000 B');   // 1024-based, not 1000-based
        assert.equal(fmtBytes(1023), '1023 B');
        assert.equal(fmtBytes(512.5), '512.5 B'); // raw concatenation, no rounding
    });

    it('rounds KB and MB to integers', () => {
        assert.equal(fmtBytes(KB), '1 KB');
        assert.equal(fmtBytes(1536), '2 KB');         // Math.round(1.5) = 2
        assert.equal(fmtBytes(999000), '976 KB');     // Math.round(975.6)
        assert.equal(fmtBytes(MB), '1 MB');
        assert.equal(fmtBytes(1023 * MB), '1023 MB');
    });

    it('keeps the "1024 KB" quirk just under the MB boundary', () => {
        assert.equal(fmtBytes(MB - 1), '1024 KB');
        assert.equal(fmtBytes(GB - 1), '1024 MB');
    });

    it('formats GB with one decimal, stripping trailing .0, and caps at GB', () => {
        assert.equal(fmtBytes(GB), '1 GB');
        assert.equal(fmtBytes(1.5 * GB), '1.5 GB');
        assert.equal(fmtBytes(2 * GB), '2 GB');
        assert.equal(fmtBytes(2 * TB), '2048 GB');    // no TB display tier
    });

    it('passes negatives through the B tier (historical behavior)', () => {
        assert.equal(fmtBytes(-5), '-5 B');
    });

    it('has no null guard (historical behavior)', () => {
        assert.equal(fmtBytes(null), 'null B');   // null < 1024 → B tier
        assert.equal(fmtBytes(NaN), 'NaN GB');    // all comparisons false → GB tier
    });
});

describe('fmtBytesPrecise (tooltip variant)', () => {
    it('returns "-" for null/undefined/NaN', () => {
        assert.equal(fmtBytesPrecise(null), '-');
        assert.equal(fmtBytesPrecise(undefined), '-');
        assert.equal(fmtBytesPrecise(NaN), '-');
    });

    it('formats B with 0 decimals', () => {
        assert.equal(fmtBytesPrecise(0), '0 B');
        assert.equal(fmtBytesPrecise(1023), '1023 B');
        assert.equal(fmtBytesPrecise(512.4), '512 B');
        assert.equal(fmtBytesPrecise(-5), '-5 B');
    });

    it('formats KB/MB with 1 decimal (kept, not stripped)', () => {
        assert.equal(fmtBytesPrecise(KB), '1.0 KB');
        assert.equal(fmtBytesPrecise(1536), '1.5 KB');
        assert.equal(fmtBytesPrecise(MB - 1), '1024.0 KB');
        assert.equal(fmtBytesPrecise(MB), '1.0 MB');
        assert.equal(fmtBytesPrecise(1.5 * MB), '1.5 MB');
    });

    it('formats GB with 2 decimals and caps at GB', () => {
        assert.equal(fmtBytesPrecise(GB), '1.00 GB');
        assert.equal(fmtBytesPrecise(1.5 * GB), '1.50 GB');
        assert.equal(fmtBytesPrecise(2 * TB), '2048.00 GB');
    });
});

describe('fmtBytesShort (axis variant)', () => {
    it('returns "0" for null/NaN/zero', () => {
        assert.equal(fmtBytesShort(null), '0');
        assert.equal(fmtBytesShort(undefined), '0');
        assert.equal(fmtBytesShort(NaN), '0');
        assert.equal(fmtBytesShort(0), '0');
    });

    it('uses single-letter units without spaces', () => {
        assert.equal(fmtBytesShort(1), '1B');
        assert.equal(fmtBytesShort(1023), '1023B');
        assert.equal(fmtBytesShort(KB), '1K');
        assert.equal(fmtBytesShort(1536), '2K');      // (1.5).toFixed(0) = "2"
        assert.equal(fmtBytesShort(10 * MB), '10M');
    });

    it('formats GB with 1 decimal and caps at G', () => {
        assert.equal(fmtBytesShort(GB), '1.0G');
        assert.equal(fmtBytesShort(1.5 * GB), '1.5G');
        assert.equal(fmtBytesShort(2 * TB), '2048.0G');
    });
});

describe('parseSizeToBytes (loose variant)', () => {
    it('returns 0 for empty / non-string input', () => {
        assert.equal(parseSizeToBytes(null), 0);
        assert.equal(parseSizeToBytes(undefined), 0);
        assert.equal(parseSizeToBytes(''), 0);
        assert.equal(parseSizeToBytes(1024), 0);   // numbers are not parsed
    });

    it('parses all units, case-insensitively', () => {
        assert.equal(parseSizeToBytes('10 B'), 10);
        assert.equal(parseSizeToBytes('1 KB'), 1024);
        assert.equal(parseSizeToBytes('1 kb'), 1024);
        assert.equal(parseSizeToBytes('1KB'), 1024);   // no space
        assert.equal(parseSizeToBytes('1 MB'), MB);
        assert.equal(parseSizeToBytes('1 GB'), GB);
        assert.equal(parseSizeToBytes('1 TB'), TB);
    });

    it('keeps fractional results (no rounding)', () => {
        assert.equal(parseSizeToBytes('1.5 KB'), 1536);
        assert.equal(parseSizeToBytes('0.5 B'), 0.5);
        assert.equal(parseSizeToBytes('2.5 GB'), 2.5 * GB);
    });

    it('falls back to parseFloat for unit-less input', () => {
        assert.equal(parseSizeToBytes('512'), 512);
        assert.equal(parseSizeToBytes('999000'), 999000);
        assert.equal(parseSizeToBytes('12.5'), 12.5);
        assert.equal(parseSizeToBytes('abc'), 0);
    });

    it('matches anywhere in the string (loose)', () => {
        assert.equal(parseSizeToBytes('50 MB extra'), 50 * MB);
        assert.equal(parseSizeToBytes('size: 3 KB'), 3 * KB);
        assert.equal(parseSizeToBytes('-5 KB'), 5 * KB);  // sign is outside the match
    });

    it('round-trips through fmtBytes for exact unit values', () => {
        assert.equal(fmtBytes(parseSizeToBytes('2 KB')), '2 KB');
        assert.equal(fmtBytes(parseSizeToBytes('768 MB')), '768 MB');
        assert.equal(fmtBytes(parseSizeToBytes('1.5 GB')), '1.5 GB');
    });
});

describe('parseSizeToBytesStrict (report-filter variant)', () => {
    it('returns 0 for empty / non-string input', () => {
        assert.equal(parseSizeToBytesStrict(null), 0);
        assert.equal(parseSizeToBytesStrict(undefined), 0);
        assert.equal(parseSizeToBytesStrict(''), 0);
        assert.equal(parseSizeToBytesStrict(1024), 0);
    });

    it('parses all units, case-insensitively, unit defaulting to B', () => {
        assert.equal(parseSizeToBytesStrict('10 B'), 10);
        assert.equal(parseSizeToBytesStrict('1 KB'), 1024);
        assert.equal(parseSizeToBytesStrict('1 tb'), TB);
        assert.equal(parseSizeToBytesStrict('50 MB'), 50 * MB);
        assert.equal(parseSizeToBytesStrict('512'), 512);      // unit optional
        assert.equal(parseSizeToBytesStrict('999000'), 999000);
    });

    it('rounds the result to an integer', () => {
        assert.equal(parseSizeToBytesStrict('1.5 KB'), 1536);
        assert.equal(parseSizeToBytesStrict('0.5 B'), 1);      // Math.round(0.5) = 1
        assert.equal(parseSizeToBytesStrict('12.5'), 13);
        assert.equal(parseSizeToBytesStrict('2.5 GB'), Math.round(2.5 * GB));
    });

    it('rejects anything but a whole number+unit string (anchored)', () => {
        assert.equal(parseSizeToBytesStrict('50 MB extra'), 0);
        assert.equal(parseSizeToBytesStrict('size: 3 KB'), 0);
        assert.equal(parseSizeToBytesStrict(' 1 KB'), 0);      // leading space fails ^
        assert.equal(parseSizeToBytesStrict('-5 KB'), 0);
        assert.equal(parseSizeToBytesStrict('abc'), 0);
    });
});

describe('fmtBytesFull (backend FormatBytes parity)', () => {
    it('formats bytes below 1 KiB as an integer', () => {
        assert.equal(fmtBytesFull(0), '0 B');
        assert.equal(fmtBytesFull(512), '512 B');
        assert.equal(fmtBytesFull(1023), '1023 B');
    });
    it('formats KB/MB/GB/TB with two decimals (1024-based)', () => {
        assert.equal(fmtBytesFull(1024), '1.00 KB');
        assert.equal(fmtBytesFull(1536), '1.50 KB');
        assert.equal(fmtBytesFull(1024 ** 2), '1.00 MB');
        assert.equal(fmtBytesFull(1024 ** 3), '1.00 GB');
        assert.equal(fmtBytesFull(1024 ** 4), '1.00 TB');
    });
    it('keeps a TB tier (unlike the GB-capped formatters)', () => {
        assert.equal(fmtBytesFull(2.5 * 1024 ** 4), '2.50 TB');
    });
});
