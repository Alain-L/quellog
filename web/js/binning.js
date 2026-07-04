/**
 * Pure time-bucketing / aggregation helpers for the chart builders
 * (DOM-free, no uPlot access). charts.js imports these to bin raw event
 * streams into fixed-width buckets before rendering.
 * @module binning
 */

/**
 * One binned series: bucket-center X values plus per-bucket counts.
 * `xData[i]` is the center of bucket i (Unix seconds); `yData[i]` its value.
 * @typedef {Object} BinnedSeries
 * @property {Float64Array} xData - Bucket-center timestamps (Unix seconds)
 * @property {Float64Array} yData - Per-bucket aggregated value
 * @property {number} median - Median of the non-zero yData values
 * @property {number} buckets - Number of buckets actually used
 */

/**
 * Pick a bucket interval from the visible time range: 1 min under 1 h,
 * 5 min under 6 h, 15 min under 24 h, 1 h beyond.
 * @param {number} rangeSeconds - Visible time range in seconds
 * @returns {number} Interval in seconds
 */
export function computeAutoInterval(rangeSeconds) {
    if (rangeSeconds < 3600) return 60;         // < 1h → 1 min
    if (rangeSeconds < 6 * 3600) return 300;    // < 6h → 5 min
    if (rangeSeconds < 24 * 3600) return 900;   // < 24h → 15 min
    return 3600;                                 // >= 24h → 1h
}

/**
 * Compute the bucket count for a range/interval pair, clamped to [5, 200].
 * @param {number} rangeSeconds - Visible time range in seconds
 * @param {number} intervalSeconds - Desired bucket width in seconds; 0 means
 *   auto (falls back to computeAutoInterval)
 * @returns {number} Bucket count
 */
export function computeBuckets(rangeSeconds, intervalSeconds) {
    if (intervalSeconds === 0) {
        intervalSeconds = computeAutoInterval(rangeSeconds);
    }
    const buckets = Math.max(5, Math.ceil(rangeSeconds / intervalSeconds));
    return Math.min(buckets, 200);  // Cap at 200 buckets max
}

/**
 * Bin raw timestamps into per-bucket occurrence counts (histogram data).
 * Timestamps outside [minT, maxT] are dropped.
 * @param {number[]|Float64Array} times - Event timestamps (Unix seconds)
 * @param {number} minT - Range start (Unix seconds)
 * @param {number} maxT - Range end (Unix seconds)
 * @param {number} interval - Bucket width in seconds; 0 = auto
 * @returns {BinnedSeries}
 */
export function binTimestamps(times, minT, maxT, interval) {
    const range = maxT - minT || 1;
    const buckets = computeBuckets(range, interval);
    const bucketSize = range / buckets;

    const xData = new Float64Array(buckets);
    const yData = new Float64Array(buckets);
    for (let i = 0; i < buckets; i++) {
        xData[i] = minT + i * bucketSize + bucketSize / 2;
    }
    times.forEach(t => {
        if (t >= minT && t <= maxT) {
            const idx = Math.min(Math.floor((t - minT) / bucketSize), buckets - 1);
            yData[idx]++;
        }
    });

    const sortedY = [...yData].filter(v => v > 0).sort((a, b) => a - b);
    const median = sortedY.length > 0 ? sortedY[Math.floor(sortedY.length / 2)] : 0;

    return { xData, yData, median, buckets };
}

/**
 * One pre-shaped query execution as consumed by the chart builders
 * (charts.js maps the payload's `sql_performance.executions` into this).
 * @typedef {Object} ExecutionPoint
 * @property {number} t - Execution timestamp (Unix seconds)
 * @property {number} d - Duration in milliseconds
 */

/**
 * Bin query executions by time, summing durations per bucket (seconds).
 * Used for the Query Time Distribution chart.
 * @param {ExecutionPoint[]} executions - Pre-shaped executions
 * @param {number} minT - Range start (Unix seconds)
 * @param {number} maxT - Range end (Unix seconds)
 * @param {number} interval - Bucket width in seconds; 0 = auto
 * @returns {BinnedSeries} yData holds summed duration in seconds per bucket
 */
export function binDurations(executions, minT, maxT, interval) {
    const range = maxT - minT || 1;
    const buckets = computeBuckets(range, interval);
    const bucketSize = range / buckets;

    const xData = new Float64Array(buckets);
    const yData = new Float64Array(buckets);
    for (let i = 0; i < buckets; i++) {
        xData[i] = minT + i * bucketSize + bucketSize / 2;
    }
    executions.forEach(e => {
        const t = e.t;
        const dur = e.d;  // duration in ms
        if (t >= minT && t <= maxT) {
            const idx = Math.min(Math.floor((t - minT) / bucketSize), buckets - 1);
            yData[idx] += dur / 1000;  // convert to seconds for display
        }
    });

    const sortedY = [...yData].filter(v => v > 0).sort((a, b) => a - b);
    const median = sortedY.length > 0 ? sortedY[Math.floor(sortedY.length / 2)] : 0;

    return { xData, yData, median, buckets };
}

/**
 * Bin query counts and summed durations together for the dual-axis chart.
 * @param {number[]|Float64Array} times - Query timestamps (Unix seconds)
 * @param {ExecutionPoint[]} executions - Pre-shaped executions
 * @param {number} minT - Range start (Unix seconds)
 * @param {number} maxT - Range end (Unix seconds)
 * @param {number} interval - Bucket width in seconds; 0 = auto
 * @returns {{xData: Float64Array, countData: Float64Array,
 *   durationData: Float64Array, medianCount: number, buckets: number}}
 *   durationData is in seconds; medianCount is the median of non-zero counts
 */
export function binCombinedData(times, executions, minT, maxT, interval) {
    const range = maxT - minT || 1;
    const buckets = computeBuckets(range, interval);
    const bucketSize = range / buckets;

    const xData = new Float64Array(buckets);
    const countData = new Float64Array(buckets);
    const durationData = new Float64Array(buckets);
    for (let i = 0; i < buckets; i++) {
        xData[i] = minT + i * bucketSize + bucketSize / 2;
    }

    // Count queries per bucket
    times.forEach(t => {
        if (t >= minT && t <= maxT) {
            const idx = Math.min(Math.floor((t - minT) / bucketSize), buckets - 1);
            countData[idx]++;
        }
    });

    // Sum durations per bucket (in seconds)
    executions.forEach(e => {
        const t = e.t;
        const dur = e.d;
        if (t >= minT && t <= maxT) {
            const idx = Math.min(Math.floor((t - minT) / bucketSize), buckets - 1);
            durationData[idx] += dur / 1000;
        }
    });

    const sortedCount = [...countData].filter(v => v > 0).sort((a, b) => a - b);
    const medianCount = sortedCount.length > 0 ? sortedCount[Math.floor(sortedCount.length / 2)] : 0;

    return { xData, countData, durationData, medianCount, buckets };
}

/**
 * Bin temp-file events into per-bucket file counts and total bytes.
 * @param {Array<{ts: number, size: number}>} events - Pre-shaped temp-file
 *   events (ts in Unix seconds, size in bytes)
 * @param {number} minT - Range start (Unix seconds)
 * @param {number} maxT - Range end (Unix seconds)
 * @param {number} interval - Bucket width in seconds; 0 = auto
 * @returns {{xData: Float64Array, countData: Float64Array,
 *   sizeData: Float64Array, medianCount: number, buckets: number}}
 *   sizeData is in bytes
 */
export function binTempFilesData(events, minT, maxT, interval) {
    const range = maxT - minT || 1;
    const buckets = computeBuckets(range, interval);
    const bucketSize = range / buckets;

    const xData = new Float64Array(buckets);
    const countData = new Float64Array(buckets);
    const sizeData = new Float64Array(buckets);
    for (let i = 0; i < buckets; i++) {
        xData[i] = minT + i * bucketSize + bucketSize / 2;
    }

    // Aggregate events into buckets
    events.forEach(e => {
        const t = e.ts;
        if (t >= minT && t <= maxT) {
            const idx = Math.min(Math.floor((t - minT) / bucketSize), buckets - 1);
            countData[idx]++;
            sizeData[idx] += e.size;
        }
    });

    const sortedCount = [...countData].filter(v => v > 0).sort((a, b) => a - b);
    const medianCount = sortedCount.length > 0 ? sortedCount[Math.floor(sortedCount.length / 2)] : 0;

    return { xData, countData, sizeData, medianCount, buckets };
}

/**
 * One sweep-line delta event: +1 at session start, -1 at session end.
 * `pre` marks sessions already open before the log window (no connection
 * line seen); they are stacked separately when present.
 * @typedef {Object} SweepEvent
 * @property {number} time - Event time in Unix MILLIseconds (divided by
 *   1000 internally, unlike the other binners)
 * @property {number} delta - +1 (session opens) or -1 (session closes)
 * @property {boolean} [pre] - True for sessions opened before the window
 */

/**
 * Bin concurrent sessions with a sweep line: each bucket keeps the PEAK
 * concurrency reached inside it (not a sample), so short spikes survive
 * coarse bucketing. Events must be sorted by time.
 * @param {SweepEvent[]} events - Sorted sweep-line events
 * @param {number} minT - Range start (Unix seconds)
 * @param {number} maxT - Range end (Unix seconds)
 * @param {number} interval - Bucket width in seconds; 0 = auto
 * @returns {{xData: Float64Array, yData: Float64Array,
 *   yPre: Float64Array|null, median: number, buckets: number}}
 *   yData is total peak concurrency; yPre (present only when some events
 *   have pre=true) is the pre-existing-session share at that peak
 */
export function binConcurrentSessions(events, minT, maxT, interval) {
    const range = maxT - minT || 1;
    const buckets = computeBuckets(range, interval);
    const bucketSize = range / buckets;

    const hasPre = events.some(e => e.pre);
    const xData = new Float64Array(buckets);
    const yData = new Float64Array(buckets);
    const yPre = hasPre ? new Float64Array(buckets) : null;
    for (let i = 0; i < buckets; i++) {
        xData[i] = minT + i * bucketSize + bucketSize / 2;
    }

    let currentPre = 0, currentNew = 0;
    let eventIdx = 0;

    while (eventIdx < events.length && events[eventIdx].time / 1000 < minT) {
        if (events[eventIdx].pre) currentPre += events[eventIdx].delta;
        else currentNew += events[eventIdx].delta;
        eventIdx++;
    }

    for (let b = 0; b < buckets; b++) {
        const bucketStart = minT + b * bucketSize;
        const bucketEnd = minT + (b + 1) * bucketSize;
        let maxTotal = currentPre + currentNew;
        let preAtPeak = currentPre, newAtPeak = currentNew;

        while (eventIdx < events.length && events[eventIdx].time / 1000 < bucketEnd) {
            const e = events[eventIdx];
            if (e.pre) currentPre += e.delta;
            else currentNew += e.delta;
            const total = currentPre + currentNew;
            if (total > maxTotal) {
                maxTotal = total;
                preAtPeak = currentPre;
                newAtPeak = currentNew;
            }
            eventIdx++;
        }
        yData[b] = Math.max(maxTotal, 0);
        if (yPre) yPre[b] = Math.max(preAtPeak, 0);
    }

    const sortedY = [...yData].filter(v => v > 0).sort((a, b) => a - b);
    const median = sortedY.length > 0 ? sortedY[Math.floor(sortedY.length / 2)] : 0;

    return { xData, yData, yPre, median, buckets };
}

/**
 * Bin checkpoint timestamps into three stacked series (time / wal / other)
 * for the checkpoints-by-trigger stacked bar chart.
 * @param {{time?: number[], wal?: number[], other?: number[]}} typeData -
 *   Checkpoint timestamps (Unix seconds) grouped by trigger kind
 * @param {number} minT - Range start (Unix seconds)
 * @param {number} maxT - Range end (Unix seconds)
 * @param {number} interval - Bucket width in seconds; 0 = auto
 * @returns {{xData: Float64Array, series: {time: Float64Array,
 *   wal: Float64Array, other: Float64Array}, buckets: number}}
 */
export function binCheckpointsByType(typeData, minT, maxT, interval) {
    const range = maxT - minT || 1;
    const buckets = computeBuckets(range, interval);
    const bucketSize = range / buckets;

    const xData = new Float64Array(buckets);
    const series = {
        time: new Float64Array(buckets),
        wal: new Float64Array(buckets),
        other: new Float64Array(buckets)
    };

    for (let i = 0; i < buckets; i++) {
        xData[i] = minT + i * bucketSize + bucketSize / 2;
    }

    // Bin each type
    ['time', 'wal', 'other'].forEach(type => {
        (typeData[type] || []).forEach(t => {
            if (t >= minT && t <= maxT) {
                const idx = Math.min(Math.floor((t - minT) / bucketSize), buckets - 1);
                series[type][idx]++;
            }
        });
    });

    return { xData, series, buckets };
}
