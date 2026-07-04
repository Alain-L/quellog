// Unit tests for web/js/binning.js — pure time-bucketing helpers moved out
// of charts.js in Phase 2. All inputs use explicit synthetic timestamps so
// results are deterministic and timezone-independent.
//
// Shared geometry used below: minT=0, maxT=100 (seconds), interval=20
// → range 100, 5 buckets of 20 s, bucket centers at [10, 30, 50, 70, 90].

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import {
    computeAutoInterval, computeBuckets,
    binTimestamps, binDurations, binCombinedData,
    binTempFilesData, binConcurrentSessions, binCheckpointsByType
} from '../../js/binning.js';

const CENTERS = [10, 30, 50, 70, 90];

function arr(typed) {
    return Array.from(typed);
}

describe('computeAutoInterval', () => {
    it('picks the interval tier from the range', () => {
        assert.equal(computeAutoInterval(0), 60);
        assert.equal(computeAutoInterval(3599), 60);          // < 1h → 1 min
        assert.equal(computeAutoInterval(3600), 300);         // < 6h → 5 min
        assert.equal(computeAutoInterval(6 * 3600 - 1), 300);
        assert.equal(computeAutoInterval(6 * 3600), 900);     // < 24h → 15 min
        assert.equal(computeAutoInterval(24 * 3600 - 1), 900);
        assert.equal(computeAutoInterval(24 * 3600), 3600);   // >= 24h → 1h
        assert.equal(computeAutoInterval(30 * 24 * 3600), 3600);
    });
});

describe('computeBuckets', () => {
    it('derives the bucket count from range / interval', () => {
        assert.equal(computeBuckets(3600, 300), 12);
        assert.equal(computeBuckets(3601, 300), 13);          // ceil
    });

    it('resolves interval 0 through computeAutoInterval', () => {
        assert.equal(computeBuckets(3600, 0), 12);            // auto → 300 s
        assert.equal(computeBuckets(1800, 0), 30);            // auto → 60 s
    });

    it('floors at 5 buckets', () => {
        assert.equal(computeBuckets(100, 60), 5);             // ceil(1.67) = 2 → 5
        assert.equal(computeBuckets(0, 60), 5);
    });

    it('caps at 200 buckets', () => {
        assert.equal(computeBuckets(1_000_000, 60), 200);
    });
});

describe('binTimestamps', () => {
    it('counts timestamps per bucket and centers the x axis', () => {
        const times = [0, 5, 19.9, 20, 50, 99, 100];
        const { xData, yData, median, buckets } = binTimestamps(times, 0, 100, 20);
        assert.equal(buckets, 5);
        assert.deepEqual(arr(xData), CENTERS);
        // 100 lands exactly on maxT and is clamped into the last bucket.
        assert.deepEqual(arr(yData), [3, 1, 1, 0, 2]);
        // Non-empty counts sorted: [1,1,2,3] → element at floor(4/2) = 2.
        assert.equal(median, 2);
    });

    it('ignores timestamps outside [minT, maxT]', () => {
        const { yData } = binTimestamps([-1, 50, 101], 0, 100, 20);
        assert.deepEqual(arr(yData), [0, 0, 1, 0, 0]);
    });

    it('handles empty input', () => {
        const { yData, median, buckets } = binTimestamps([], 0, 100, 20);
        assert.equal(buckets, 5);
        assert.deepEqual(arr(yData), [0, 0, 0, 0, 0]);
        assert.equal(median, 0);
    });

    it('handles a single point with zero span (range falls back to 1)', () => {
        const { yData, median, buckets } = binTimestamps([42], 42, 42, 20);
        assert.equal(buckets, 5);
        assert.deepEqual(arr(yData), [1, 0, 0, 0, 0]);
        assert.equal(median, 1);
    });

    it('uses the auto interval when interval is 0', () => {
        // range 50 → auto 60 s → ceil(50/60) = 1 → floored to 5 buckets
        const { buckets } = binTimestamps([10], 0, 50, 0);
        assert.equal(buckets, 5);
    });
});

describe('binDurations', () => {
    it('sums durations per bucket, converted from ms to seconds', () => {
        const execs = [
            { t: 5, d: 2000 },
            { t: 10, d: 1000 },
            { t: 50, d: 500 }
        ];
        const { xData, yData, median } = binDurations(execs, 0, 100, 20);
        assert.deepEqual(arr(xData), CENTERS);
        assert.deepEqual(arr(yData), [3, 0, 0.5, 0, 0]);
        // Non-empty sums sorted: [0.5, 3] → element at floor(2/2) = 1.
        assert.equal(median, 3);
    });

    it('ignores executions outside the range', () => {
        const { yData } = binDurations([{ t: -5, d: 1000 }, { t: 200, d: 1000 }], 0, 100, 20);
        assert.deepEqual(arr(yData), [0, 0, 0, 0, 0]);
    });
});

describe('binCombinedData', () => {
    it('bins counts and duration sums on the same x axis', () => {
        const times = [5, 25];
        const execs = [{ t: 5, d: 1000 }];
        const { xData, countData, durationData, medianCount } =
            binCombinedData(times, execs, 0, 100, 20);
        assert.deepEqual(arr(xData), CENTERS);
        assert.deepEqual(arr(countData), [1, 1, 0, 0, 0]);
        assert.deepEqual(arr(durationData), [1, 0, 0, 0, 0]);
        assert.equal(medianCount, 1);
    });
});

describe('binTempFilesData', () => {
    it('accumulates count and byte size per bucket', () => {
        const events = [
            { ts: 5, size: 100 },
            { ts: 5, size: 50 },
            { ts: 90, size: 200 }
        ];
        const { countData, sizeData, medianCount } = binTempFilesData(events, 0, 100, 20);
        assert.deepEqual(arr(countData), [2, 0, 0, 0, 1]);
        assert.deepEqual(arr(sizeData), [150, 0, 0, 0, 200]);
        // Non-empty counts sorted: [1, 2] → element at floor(2/2) = 1.
        assert.equal(medianCount, 2);
    });
});

describe('binConcurrentSessions', () => {
    // Events carry time in MILLISECONDS; minT/maxT are in seconds.
    it('tracks the per-bucket concurrency peak (sweep-line)', () => {
        const events = [
            { time: 5000, delta: 1 },
            { time: 10000, delta: 1 },   // overlap → peak 2 in bucket 0
            { time: 15000, delta: -1 },
            { time: 18000, delta: -1 },
            { time: 45000, delta: 1 },
            { time: 55000, delta: -1 }
        ];
        const { yData, yPre, median } = binConcurrentSessions(events, 0, 100, 20);
        assert.deepEqual(arr(yData), [2, 0, 1, 0, 0]);
        assert.equal(yPre, null);        // no pre-existing sessions
        assert.equal(median, 2);         // sorted non-empty [1,2] → index 1
    });

    it('carries sessions still open across empty buckets', () => {
        const events = [
            { time: 5000, delta: 1 }     // opens in bucket 0, never closes
        ];
        const { yData } = binConcurrentSessions(events, 0, 100, 20);
        assert.deepEqual(arr(yData), [1, 1, 1, 1, 1]);
    });

    it('counts events before minT as carried-in concurrency', () => {
        const events = [
            { time: 5000, delta: 1 },    // before minT=10 s → carry-in
            { time: 30000, delta: -1 }
        ];
        const { yData } = binConcurrentSessions(events, 10, 110, 20);
        assert.deepEqual(arr(yData), [1, 1, 0, 0, 0]);
    });

    it('splits the peak into pre-existing and new when pre events exist', () => {
        const events = [
            { time: 0, delta: 2, pre: true },
            { time: 25000, delta: 1, pre: false },
            { time: 45000, delta: -1, pre: true }
        ];
        const { yData, yPre, median } = binConcurrentSessions(events, 0, 100, 20);
        assert.deepEqual(arr(yData), [2, 3, 3, 2, 2]);
        assert.deepEqual(arr(yPre), [2, 2, 2, 1, 1]);
        assert.equal(median, 2);         // sorted [2,2,2,3,3] → index 2
    });
});

describe('binCheckpointsByType', () => {
    it('bins each checkpoint type into its own series', () => {
        const typeData = {
            time: [5, 25],
            wal: [50],
            other: [99]
        };
        const { xData, series, buckets } = binCheckpointsByType(typeData, 0, 100, 20);
        assert.equal(buckets, 5);
        assert.deepEqual(arr(xData), CENTERS);
        assert.deepEqual(arr(series.time), [1, 1, 0, 0, 0]);
        assert.deepEqual(arr(series.wal), [0, 0, 1, 0, 0]);
        assert.deepEqual(arr(series.other), [0, 0, 0, 0, 1]);
    });

    it('treats missing type arrays as empty', () => {
        const { series } = binCheckpointsByType({ time: [5] }, 0, 100, 20);
        assert.deepEqual(arr(series.time), [1, 0, 0, 0, 0]);
        assert.deepEqual(arr(series.wal), [0, 0, 0, 0, 0]);
        assert.deepEqual(arr(series.other), [0, 0, 0, 0, 0]);
    });
});
