/**
 * Pure time-bucketing / aggregation helpers for the chart builders
 * (DOM-free, no uPlot access). charts.js imports these to bin raw event
 * streams into fixed-width buckets before rendering.
 * @module binning
 */

// Compute optimal interval based on time range
export function computeAutoInterval(rangeSeconds) {
    if (rangeSeconds < 3600) return 60;         // < 1h → 1 min
    if (rangeSeconds < 6 * 3600) return 300;    // < 6h → 5 min
    if (rangeSeconds < 24 * 3600) return 900;   // < 24h → 15 min
    return 3600;                                 // >= 24h → 1h
}

// Compute bucket count from interval and range
export function computeBuckets(rangeSeconds, intervalSeconds) {
    if (intervalSeconds === 0) {
        intervalSeconds = computeAutoInterval(rangeSeconds);
    }
    const buckets = Math.max(5, Math.ceil(rangeSeconds / intervalSeconds));
    return Math.min(buckets, 200);  // Cap at 200 buckets max
}

// Helper: bin timestamps into histogram data
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

// Helper: bin executions by time and sum durations (for Query Time Distribution)
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

// Helper: bin combined count and duration data for dual-axis chart
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

// Helper: bin temp files data into count and size per bucket
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

// Helper: bin concurrent sessions using sweep-line algorithm
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

// Helper: bin checkpoints by type (for stacked bar chart)
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
