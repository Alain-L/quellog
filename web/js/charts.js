// Chart creation and management for quellog web app
// Uses uPlot library for interactive time-series charts

import { safeMax, safeMin, fmt } from './utils.js';
import {
    charts, modalCharts, modalChartsData, chartIntervalMap, defaultInterval,
    incrementModalChartCounter
} from './state.js';

// Store chart data for re-creation and modal expansion
export const chartData = new Map();

// Shared x-axis tick formatting for time series. Shows HH:MM, and adds a short
// date ("3 Jan") on the first tick of each day — but only when the visible span
// is multi-day, so single-day charts are unchanged. timeAxisSize reserves the
// extra height for the date line in that case. Both read the live x-scale so
// they stay correct on zoom.
function timeAxisMultiDay(u) {
    const xs = u && u.scales && u.scales.x;
    return !!(xs && xs.max != null && xs.min != null && (xs.max - xs.min) > 86400);
}
function timeAxisValues(u, vals) {
    const multiDay = timeAxisMultiDay(u);
    return vals.map((v, i) => {
        const d = new Date(v * 1000);
        const time = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
        if (!multiDay) return time;
        const prevDay = i > 0 ? new Date(vals[i - 1] * 1000).getDate() : -1;
        if (i === 0 || d.getDate() !== prevDay) {
            return time + '\n' + d.toLocaleDateString('en-US', { day: 'numeric', month: 'short' });
        }
        return time;
    });
}
function timeAxisSize(u) { return timeAxisMultiDay(u) ? 36 : 20; }

// Modal state (local to charts module)
let modalChart = null;
let modalChartId = null;
let modalInterval = 0;  // 0 = Auto

// Bind double-click on a chart's overlay to a reset callback. uPlot's built-in
// dblclick calls setScale('x', { min: null, max: null }), which auto-fits to
// the *current* data — but our charts re-bin the data on zoom, so the data
// extent equals the zoomed range and the built-in "reset" stays zoomed. We
// install our handler in capture phase and stop propagation so uPlot's never
// runs.
function bindDblclickReset(chart, resetFn) {
    chart.over.addEventListener('dblclick', (e) => {
        e.preventDefault();
        e.stopImmediatePropagation();
        resetFn();
    }, true);
}

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

// Format interval for display
export function formatInterval(seconds) {
    if (seconds === 0) return 'Auto';
    if (seconds < 60) return seconds + 's';
    if (seconds < 3600) return (seconds / 60) + ' min';
    return (seconds / 3600) + 'h';
}

// Global tooltip plugin for uPlot charts
export function tooltipPlugin() {
    let tooltip = null;
    return {
        hooks: {
            init: u => {
                tooltip = document.createElement('div');
                tooltip.className = 'chart-tooltip';
                tooltip.style.display = 'none';
                u.over.appendChild(tooltip);
            },
            setCursor: u => {
                // Skip during re-sampling
                if (u._resampling) { tooltip.style.display = 'none'; return; }
                const { idx } = u.cursor;
                const data0 = u.data[0];
                const data1 = u.data[1];
                if (idx == null || !data0 || idx < 0 || idx >= data0.length) {
                    tooltip.style.display = 'none';
                    return;
                }
                const x = data0[idx];
                const y = data1[idx];
                if (x === undefined || y === undefined || !Number.isFinite(x)) {
                    tooltip.style.display = 'none';
                    return;
                }
                const d = new Date(x * 1000);
                const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                tooltip.innerHTML = `${timeStr} · ${y} events`;
                const left = u.valToPos(x, 'x');
                const top = u.valToPos(y, 'y');
                tooltip.style.display = 'block';
                tooltip.style.left = Math.min(left, u.over.clientWidth - 100) + 'px';
                tooltip.style.top = Math.max(0, top - 40) + 'px';
            }
        }
    };
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

// Create stacked bar chart for checkpoints (time=blue, xlog=orange, other=gray)
export function createCheckpointChart(containerId, data, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !data?.all || data.all.length === 0) return null;

    // Clear previous chart
    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    // Parse all timestamps for range calculation
    const allTimes = data.all.map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)).sort((a, b) => a - b);
    if (allTimes.length === 0) return null;

    // Parse type-specific timestamps
    const typeData = {
        time: (data.types?.time || []).map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)),
        wal: (data.types?.wal || []).map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)),
        other: (data.types?.other || []).map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t))
    };

    const minT = allTimes[0];
    const maxT = allTimes[allTimes.length - 1];
    const interval = options.interval !== undefined ? options.interval : (chartIntervalMap.get(containerId) ?? defaultInterval);

    // Initial binning
    const { xData, series } = binCheckpointsByType(typeData, minT, maxT, interval);

    // Colors for checkpoint types
    const colors = {
        time: getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5',
        wal: getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#f47920',
        other: '#909399' // gray
    };

    // Tooltip plugin for stacked bars
    const tooltip = document.createElement('div');
    tooltip.className = 'chart-tooltip';
    tooltip.style.cssText = 'position:absolute;display:none;padding:6px 10px;background:var(--bg);border:1px solid var(--border);border-radius:4px;font-size:12px;pointer-events:none;z-index:100;white-space:nowrap;box-shadow:0 2px 8px rgba(0,0,0,0.15);';

    const checkpointTooltipPlugin = () => ({
        hooks: {
            init: u => {
                u.root.querySelector('.u-over').appendChild(tooltip);
            },
            setCursor: u => {
                if (u._resampling) { tooltip.style.display = 'none'; return; }
                const { idx, left, top } = u.cursor;
                const data0 = u.data[0];
                if (idx == null || !data0 || idx < 0 || idx >= data0.length) {
                    tooltip.style.display = 'none';
                    return;
                }
                const x = data0[idx];
                if (x === undefined || !Number.isFinite(x)) {
                    tooltip.style.display = 'none';
                    return;
                }
                const timeVal = u.data[1][idx] || 0;
                const xlogVal = u.data[2][idx] || 0;
                const otherVal = u.data[3][idx] || 0;
                const total = timeVal + xlogVal + otherVal;
                if (total === 0) {
                    tooltip.style.display = 'none';
                    return;
                }
                const d = new Date(x * 1000);
                const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                let parts = [];
                if (timeVal > 0) parts.push(`<span style="color:${colors.time}">${timeVal} timed</span>`);
                if (xlogVal > 0) parts.push(`<span style="color:${colors.wal}">${xlogVal} WAL</span>`);
                const otherLabel = data.warningsOnly ? 'warning' : 'other';
                if (otherVal > 0) parts.push(`<span style="color:${colors.other}">${otherVal} ${otherLabel}</span>`);
                tooltip.innerHTML = `<strong>${timeStr}</strong><br>${parts.join(' · ')}`;
                tooltip.style.display = 'block';
                const ttWidth = tooltip.offsetWidth;
                const ttHeight = tooltip.offsetHeight;
                const chartWidth = u.bbox.width;
                let ttLeft = left - ttWidth / 2;
                if (ttLeft < 0) ttLeft = 0;
                if (ttLeft + ttWidth > chartWidth) ttLeft = chartWidth - ttWidth;
                tooltip.style.left = ttLeft + 'px';
                tooltip.style.top = (top - ttHeight - 10) + 'px';
            }
        }
    });

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 120,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: (u, min, max) => [0, Math.max(Math.ceil(max), 2)] }
        },
        axes: [
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                values: timeAxisValues,
                size: timeAxisSize,
                font: '10px system-ui'
            },
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                size: 30,
                font: '10px system-ui',
                incrs: [1, 2, 5, 10, 20, 50, 100],
                values: (u, vals) => vals.map(v => Number.isInteger(v) ? v : '')
            }
        ],
        series: [
            {},
            { fill: 'transparent', stroke: 'transparent', width: 0, points: { show: false }, paths: () => null },
            { fill: 'transparent', stroke: 'transparent', width: 0, points: { show: false }, paths: () => null },
            { fill: 'transparent', stroke: 'transparent', width: 0, points: { show: false }, paths: () => null }
        ],
        plugins: [checkpointTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                const xd = u.data[0];
                const timeSeries = u.data[1];
                const xlogSeries = u.data[2];
                const otherSeries = u.data[3];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(3, barWidth / 3);
                const y0 = u.valToPos(0, 'y', true);

                // Draw stacked bars: other (bottom), xlog (middle), time (top)
                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    let yBottom = y0;

                    // Draw other (bottom)
                    const otherVal = otherSeries[i] || 0;
                    if (otherVal > 0) {
                        const yTop = u.valToPos(otherVal, 'y', true);
                        const h = yBottom - yTop;
                        ctx.fillStyle = colors.other;
                        ctx.fillRect(x - barWidth/2, yTop, barWidth, h);
                        yBottom = yTop;
                    }

                    // Draw xlog (middle)
                    const xlogVal = xlogSeries[i] || 0;
                    if (xlogVal > 0) {
                        const yTop = yBottom - (y0 - u.valToPos(xlogVal, 'y', true));
                        const h = yBottom - yTop;
                        ctx.fillStyle = colors.wal;
                        ctx.fillRect(x - barWidth/2, yTop, barWidth, h);
                        yBottom = yTop;
                    }

                    // Draw time (top) with rounded corners
                    const timeVal = timeSeries[i] || 0;
                    if (timeVal > 0) {
                        const yTop = yBottom - (y0 - u.valToPos(timeVal, 'y', true));
                        const h = yBottom - yTop;
                        ctx.fillStyle = colors.time;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, yBottom);
                        ctx.lineTo(x - barWidth/2, yTop + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, yTop, x - barWidth/2 + radius, yTop);
                        ctx.lineTo(x + barWidth/2 - radius, yTop);
                        ctx.quadraticCurveTo(x + barWidth/2, yTop, x + barWidth/2, yTop + radius);
                        ctx.lineTo(x + barWidth/2, yBottom);
                        ctx.closePath();
                        ctx.fill();
                    } else if (xlogVal > 0 || otherVal > 0) {
                        // Round top of highest visible segment
                        // Already drawn with fillRect, so add rounded corners
                    }
                }
            }],
            setScale: [u => {
                if (!u._typeData || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;
                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, series: newSeries } = binCheckpointsByType(u._typeData, newMin, newMax, u._interval);
                    u._resampling = true;
                    u.setData([newX, newSeries.time, newSeries.wal, newSeries.other], false);
                    u._resampling = false;
                    // Force batch/commit cycle to reset cursor state
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, series.time, series.wal, series.other], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    // Store data for re-sampling
    chart._typeData = typeData;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });

    // Handle resize
    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) {
            chart.setSize({ width: container.clientWidth, height: opts.height });
        }
    });
    resizeObserver.observe(container);

    return chart;
}

// Create WAL distance vs estimate chart (bars + dashed line)
export function createWALDistanceChart(containerId, data, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !data?.distances || data.distances.length === 0) return null;

    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    // Parse data: timestamps, distance (MB), estimate (MB)
    const points = data.distances
        .map(d => ({
            t: new Date(d.timestamp).getTime() / 1000,
            dist: d.distance_kb / 1024,
            est: d.estimate_kb / 1024
        }))
        .filter(p => !isNaN(p.t))
        .sort((a, b) => a.t - b.t);

    if (points.length === 0) return null;

    const xData = new Float64Array(points.map(p => p.t));
    const distData = new Float64Array(points.map(p => p.dist));
    const estData = new Float64Array(points.map(p => p.est));

    const barColor = getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5';
    const estColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#f47920';

    // Parse warning timestamps for pink background bands
    const warningTimes = (data.warnings || [])
        .map(t => new Date(t).getTime() / 1000)
        .filter(t => !isNaN(t))
        .sort((a, b) => a - b);

    // Tooltip
    const tooltip = document.createElement('div');
    tooltip.className = 'chart-tooltip';

    const tooltipPlugin = () => ({
        hooks: {
            init: u => { u.root.querySelector('.u-over').appendChild(tooltip); },
            setCursor: u => {
                const { idx, left, top } = u.cursor;
                if (idx == null || idx < 0 || idx >= u.data[0].length) {
                    tooltip.style.display = 'none';
                    return;
                }
                const dist = u.data[1][idx] || 0;
                const est = u.data[2][idx] || 0;
                if (dist === 0 && est === 0) {
                    tooltip.style.display = 'none';
                    return;
                }
                const d = new Date(u.data[0][idx] * 1000);
                const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                tooltip.innerHTML = `<strong>${timeStr}</strong><br>` +
                    `<span style="color:${barColor}">distance: ${dist.toFixed(1)} MB</span><br>` +
                    `<span style="color:${estColor}">estimate: ${est.toFixed(1)} MB</span>`;
                tooltip.style.display = 'block';
                const ttWidth = tooltip.offsetWidth;
                const chartWidth = u.bbox.width;
                let ttLeft = left - ttWidth / 2;
                if (ttLeft < 0) ttLeft = 0;
                if (ttLeft + ttWidth > chartWidth) ttLeft = chartWidth - ttWidth;
                tooltip.style.left = ttLeft + 'px';
                tooltip.style.top = (top - tooltip.offsetHeight - 10) + 'px';
            }
        }
    });

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 200,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: (u, min, max) => [0, max * 1.1] }
        },
        axes: [
            { stroke: '#888', grid: { stroke: '#8881' }, ticks: { show: false }, gap: 2, size: timeAxisSize,
              values: timeAxisValues
            },
            {
                stroke: '#888',
                grid: { stroke: '#8881' },
                size: 30,
                ticks: { show: false },
                gap: 2,
                values: (u, vals) => vals.map(v => v >= 1024 ? (v/1024).toFixed(0) + 'G' : v.toFixed(0))
            }
        ],
        series: [
            {},
            // Distance: invisible series (drawn as bars in hook)
            { fill: 'transparent', stroke: 'transparent', width: 0, points: { show: false }, paths: () => null },
            // Estimate: dashed line
            { stroke: estColor, width: 2, dash: [6, 4], points: { show: false } }
        ],
        plugins: [tooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                const xd = u.data[0];
                const dist = u.data[1];
                const barW = Math.max(4, (u.bbox.width / xd.length) * 0.6);
                const radius = Math.min(3, barW / 3);
                const y0 = u.valToPos(0, 'y', true);
                const yMax = u.valToPos(u.scales.y.max, 'y', true);

                // Draw pink background bands for "too frequent" warning periods
                if (warningTimes.length > 0) {
                    ctx.fillStyle = 'rgba(220, 53, 69, 0.25)';
                    // Group warnings within 60s into clusters
                    let clusterStart = warningTimes[0];
                    let clusterEnd = warningTimes[0];
                    for (let i = 1; i <= warningTimes.length; i++) {
                        if (i < warningTimes.length && warningTimes[i] - clusterEnd < 120) {
                            clusterEnd = warningTimes[i];
                        } else {
                            // Draw this cluster with some padding
                            const pad = Math.max(30, (clusterEnd - clusterStart) * 0.1);
                            const x1 = u.valToPos(clusterStart - pad, 'x', true);
                            const x2 = u.valToPos(clusterEnd + pad, 'x', true);
                            ctx.fillRect(x1, yMax, x2 - x1, y0 - yMax);
                            if (i < warningTimes.length) {
                                clusterStart = warningTimes[i];
                                clusterEnd = warningTimes[i];
                            }
                        }
                    }
                }

                ctx.fillStyle = barColor;
                for (let i = 0; i < xd.length; i++) {
                    const v = dist[i] || 0;
                    if (v <= 0) continue;
                    const x = u.valToPos(xd[i], 'x', true);
                    const yTop = u.valToPos(v, 'y', true);
                    const h = y0 - yTop;

                    ctx.beginPath();
                    ctx.moveTo(x - barW/2, y0);
                    ctx.lineTo(x - barW/2, yTop + radius);
                    ctx.quadraticCurveTo(x - barW/2, yTop, x - barW/2 + radius, yTop);
                    ctx.lineTo(x + barW/2 - radius, yTop);
                    ctx.quadraticCurveTo(x + barW/2, yTop, x + barW/2, yTop + radius);
                    ctx.lineTo(x + barW/2, y0);
                    ctx.closePath();
                    ctx.fill();
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, distData, estData], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    const minT = xData[0];
    const maxT = xData[xData.length - 1];
    chart._originalXRange = [minT, maxT];

    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) {
            chart.setSize({ width: container.clientWidth, height: opts.height });
        }
    });
    resizeObserver.observe(container);

    return chart;
}

// Create interactive time chart with uPlot
export function createTimeChart(containerId, timestamps, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !timestamps || timestamps.length === 0) return null;

    // Clear previous chart
    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    // Parse timestamps
    const times = timestamps.map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)).sort((a, b) => a - b);
    if (times.length === 0) return null;

    const minT = times[0];
    const maxT = times[times.length - 1];
    const interval = options.interval !== undefined ? options.interval : (chartIntervalMap.get(containerId) ?? defaultInterval);

    // Initial binning
    const { xData, yData, median } = binTimestamps(times, minT, maxT, interval);

    // Colors for gradient (light to dark based on intensity)
    const baseColor = options.color || getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5';

    // Range selection callback for filtering
    const onSelect = options.onSelect || null;

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 120,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] }
        },
        axes: [
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                values: timeAxisValues,
                size: timeAxisSize,
                font: '10px system-ui'
            },
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                size: 30,
                font: '10px system-ui'
            }
        ],
        series: [
            {},
            {
                fill: 'transparent',
                stroke: 'transparent',
                width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [tooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0];
                const yd = u.data[1];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(3, barWidth / 3);

                // Draw median line first (behind bars)
                const currentMedian = u._median || 0;
                if (currentMedian > 0) {
                    const yMed = u.valToPos(currentMedian, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim();
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, yMed);
                    ctx.lineTo(left + width, yMed);
                    ctx.stroke();
                    ctx.setLineDash([]);
                }

                // Draw bars with gradient colors
                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y = u.valToPos(yd[i], 'y', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const h = y0 - y;
                    if (h > 0) {
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, y0);
                        ctx.lineTo(x - barWidth/2, y + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                        ctx.lineTo(x + barWidth/2 - radius, y);
                        ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                        ctx.lineTo(x + barWidth/2, y0);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
                ctx.restore();
            }],
            setSelect: [u => {
                if (onSelect && u.select.width > 10) {
                    const minX = u.posToVal(u.select.left, 'x');
                    const maxX = u.posToVal(u.select.left + u.select.width, 'x');
                    onSelect(new Date(minX * 1000), new Date(maxX * 1000));
                }
            }],
            setScale: [u => {
                // Re-sample on zoom
                if (!u._times || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                // Check if zoomed (not at original range)
                const [origMin, origMax] = u._originalXRange;
                const isZoomed = Math.abs(newMin - origMin) > 1 || Math.abs(newMax - origMax) > 1;
                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    // Re-bin for the visible range
                    const { xData: newX, yData: newY, median: newMedian } = binTimestamps(u._times, newMin, newMax, u._interval);
                    u._median = newMedian;
                    u._resampling = true;
                    u.setData([newX, newY], false);
                    u._resampling = false;
                    // Force batch/commit cycle to reset cursor state
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    // Store data for re-sampling
    chart._times = times;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._median = median;

    // Handle resize
    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) {
            chart.setSize({ width: container.clientWidth, height: opts.height });
        }
    });
    resizeObserver.observe(container);

    return chart;
}

// Create duration distribution chart (sum of query durations per time bucket)
export function createDurationChart(containerId, executions, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !executions || executions.length === 0) return null;

    // Clear previous chart
    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    // Sort by timestamp
    const sorted = [...executions].sort((a, b) => a.t - b.t);
    if (sorted.length === 0) return null;

    const minT = sorted[0].t;
    const maxT = sorted[sorted.length - 1].t;
    const interval = options.interval !== undefined ? options.interval : (chartIntervalMap.get(containerId) ?? defaultInterval);

    // Initial binning (sums durations in seconds)
    const { xData, yData, median } = binDurations(sorted, minT, maxT, interval);

    const baseColor = options.color || getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5';

    // Tooltip plugin for duration (shows seconds)
    function durationTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    if (u._resampling) { tooltip.style.display = 'none'; return; }
                    const { idx } = u.cursor;
                    const data0 = u.data[0];
                    const data1 = u.data[1];
                    if (idx == null || !data0 || idx < 0 || idx >= data0.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = data0[idx];
                    const y = data1[idx];
                    if (x === undefined || y === undefined || !Number.isFinite(x)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    // Format duration nicely
                    const durStr = y >= 60 ? `${(y/60).toFixed(1)}m` : `${y.toFixed(1)}s`;
                    tooltip.innerHTML = `${timeStr} · ${durStr}`;
                    const left = u.valToPos(x, 'x');
                    const top = u.valToPos(y, 'y');
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 100) + 'px';
                    tooltip.style.top = Math.max(0, top - 40) + 'px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 120,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] }
        },
        axes: [
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                values: timeAxisValues,
                size: timeAxisSize,
                font: '10px system-ui'
            },
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                size: 40,
                font: '10px system-ui',
                values: (u, vals) => vals.map(v => v >= 60 ? `${(v/60).toFixed(0)}m` : `${v.toFixed(0)}s`)
            }
        ],
        series: [
            {},
            {
                fill: 'transparent',
                stroke: 'transparent',
                width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [durationTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0];
                const yd = u.data[1];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(3, barWidth / 3);

                // Draw median line first (behind bars)
                const currentMedian = u._median || 0;
                if (currentMedian > 0) {
                    const yMed = u.valToPos(currentMedian, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim();
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, yMed);
                    ctx.lineTo(left + width, yMed);
                    ctx.stroke();
                    ctx.setLineDash([]);
                }

                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y = u.valToPos(yd[i], 'y', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const h = y0 - y;
                    if (h > 0) {
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, y0);
                        ctx.lineTo(x - barWidth/2, y + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                        ctx.lineTo(x + barWidth/2 - radius, y);
                        ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                        ctx.lineTo(x + barWidth/2, y0);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                if (!u._executions || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, yData: newY, median: newMedian } = binDurations(u._executions, newMin, newMax, u._interval);
                    u._median = newMedian;
                    u._resampling = true;
                    u.setData([newX, newY], false);
                    u._resampling = false;
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    chart._executions = sorted;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._median = median;

    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) {
            chart.setSize({ width: container.clientWidth, height: opts.height });
        }
    });
    resizeObserver.observe(container);

    return chart;
}

// Create combined SQL chart with grouped bars (count + duration side by side)
export function createCombinedSQLChart(containerId, rawData, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !rawData) return null;

    const { times, executions } = rawData;
    if (!times || times.length === 0) return null;

    // Clear previous chart
    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    const minT = times[0];
    const maxT = times[times.length - 1];
    const interval = options.interval !== undefined ? options.interval : (chartIntervalMap.get(containerId) ?? defaultInterval);

    // Initial binning
    const { xData, countData, durationData, medianCount } = binCombinedData(times, executions, minT, maxT, interval);

    const countColor = getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5';
    const durationColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#f5a623';
    const textColor = getComputedStyle(document.documentElement).getPropertyValue('--text').trim();
    const mutedColor = getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim();

    // Series visibility state
    const seriesVisible = { count: true, duration: true };

    // Custom tooltip for combined chart
    function combinedTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    if (u._resampling) { tooltip.style.display = 'none'; return; }
                    const { idx } = u.cursor;
                    const xd = u.data[0];
                    const countY = u.data[1];
                    const durY = u.data[2];
                    if (idx == null || !xd || idx < 0 || idx >= xd.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = xd[idx];
                    const count = countY[idx];
                    const dur = durY[idx];
                    if (x === undefined || !Number.isFinite(x)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    const durStr = dur >= 60 ? `${(dur/60).toFixed(1)}m` : `${dur.toFixed(1)}s`;
                    let parts = [timeStr];
                    if (u._seriesVisible.count) parts.push(`${fmt(count)} queries`);
                    if (u._seriesVisible.duration) parts.push(durStr);
                    tooltip.innerHTML = parts.join(' · ');
                    const left = u.valToPos(x, 'x');
                    const topCount = u._seriesVisible.count ? u.valToPos(count, 'y', true) : u.bbox.top + u.bbox.height;
                    const topDur = u._seriesVisible.duration ? u.valToPos(dur, 'duration', true) : u.bbox.top + u.bbox.height;
                    const top = Math.min(topCount, topDur);
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 140) + 'px';
                    tooltip.style.top = Math.max(0, top - 40) + 'px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 150,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] },
            duration: { range: [0, null] }
        },
        axes: [
            {
                stroke: textColor,
                grid: { show: false },
                ticks: { show: false },
                values: timeAxisValues,
                size: timeAxisSize,
                font: '10px system-ui'
            },
            {
                stroke: countColor,
                grid: { show: false },
                ticks: { show: false },
                size: 35,
                font: '10px system-ui',
                side: 3  // left
            },
            {
                scale: 'duration',
                stroke: durationColor,
                grid: { show: false },
                ticks: { show: false },
                size: 40,
                font: '10px system-ui',
                side: 1,  // right
                values: (u, vals) => vals.map(v => v >= 60 ? `${(v/60).toFixed(0)}m` : `${v.toFixed(0)}s`)
            }
        ],
        series: [
            {},
            { label: 'Count', scale: 'y', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null },
            { label: 'Duration', scale: 'duration', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null }
        ],
        plugins: [combinedTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0];
                const countY = u.data[1];
                const durY = u.data[2];
                const bothVisible = u._seriesVisible.count && u._seriesVisible.duration;
                const totalBarWidth = Math.max(4, (u.bbox.width / xd.length) * 0.7);
                const barWidth = bothVisible ? totalBarWidth / 2 - 1 : totalBarWidth;
                const radius = Math.min(2, barWidth / 4);

                // Draw median line first (behind bars)
                if (u._seriesVisible.count) {
                    const currentMedian = u._medianCount || 0;
                    if (currentMedian > 0) {
                        const yMed = u.valToPos(currentMedian, 'y', true);
                        const { left, width } = u.bbox;
                        ctx.strokeStyle = mutedColor;
                        ctx.lineWidth = 1;
                        ctx.setLineDash([4, 4]);
                        ctx.beginPath();
                        ctx.moveTo(left, yMed);
                        ctx.lineTo(left + width, yMed);
                        ctx.stroke();
                        ctx.setLineDash([]);
                    }
                }

                for (let i = 0; i < xd.length; i++) {
                    const xCenter = u.valToPos(xd[i], 'x', true);
                    const y0Count = u.valToPos(0, 'y', true);
                    const y0Dur = u.valToPos(0, 'duration', true);

                    // Draw count bar (left side if both visible)
                    if (u._seriesVisible.count && countY[i] > 0) {
                        const x = bothVisible ? xCenter - barWidth/2 - 0.5 : xCenter;
                        const y = u.valToPos(countY[i], 'y', true);
                        const h = y0Count - y;
                        if (h > 0) {
                            ctx.fillStyle = countColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Count);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Count);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }

                    // Draw duration bar (right side if both visible)
                    if (u._seriesVisible.duration && durY[i] > 0) {
                        const x = bothVisible ? xCenter + barWidth/2 + 0.5 : xCenter;
                        const y = u.valToPos(durY[i], 'duration', true);
                        const h = y0Dur - y;
                        if (h > 0) {
                            ctx.fillStyle = durationColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Dur);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Dur);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                if (!u._rawData || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, countData: newCount, durationData: newDur, medianCount: newMed } = binCombinedData(u._rawData.times, u._rawData.executions, newMin, newMax, u._interval);
                    u._medianCount = newMed;
                    u._resampling = true;
                    u.setData([newX, newCount, newDur], false);
                    u._resampling = false;
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, countData, durationData], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    chart._rawData = rawData;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._medianCount = medianCount;
    chart._seriesVisible = seriesVisible;
    chart._containerId = containerId;

    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) {
            chart.setSize({ width: container.clientWidth, height: opts.height });
        }
    });
    resizeObserver.observe(container);

    return chart;
}

// Toggle series visibility for combined chart
export function toggleCombinedSeries(chartId, series) {
    const chart = charts.get(chartId);
    if (!chart || !chart._seriesVisible) return;
    chart._seriesVisible[series] = !chart._seriesVisible[series];
    // Update legend UI
    const legendItem = document.querySelector(`[data-chart="${chartId}"][data-series="${series}"]`);
    if (legendItem) {
        legendItem.classList.toggle('disabled', !chart._seriesVisible[series]);
    }
    chart.redraw();
}

// Create chart from pre-aggregated histogram data (e.g., concurrent sessions)
export function createHistogramChart(containerId, histogram, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !histogram || histogram.length === 0) return null;

    // Clear previous chart
    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    // Parse histogram labels to get timestamps
    // Labels are like "21:10 - 21:12" or "12/10 21:10 - 12/10 21:12"
    const xData = [];
    const yData = [];
    const peakTimes = [];

    // Get reference date from analysisData
    const refDate = analysisData?.summary?.start_date ? new Date(analysisData.summary.start_date) : new Date();

    histogram.forEach((h, i) => {
        // Parse label to get start time
        const parts = h.label.split(' - ');
        if (parts.length >= 1) {
            let timeStr = parts[0].trim();
            let date = new Date(refDate);

            // Check if it includes date (MM/DD HH:MM format)
            const dateMatch = timeStr.match(/(\d+)\/(\d+)\s+(\d+):(\d+)/);
            const timeMatch = timeStr.match(/^(\d+):(\d+)$/);

            if (dateMatch) {
                date.setMonth(parseInt(dateMatch[1]) - 1);
                date.setDate(parseInt(dateMatch[2]));
                date.setHours(parseInt(dateMatch[3]), parseInt(dateMatch[4]), 0, 0);
            } else if (timeMatch) {
                date.setHours(parseInt(timeMatch[1]), parseInt(timeMatch[2]), 0, 0);
            }

            xData.push(date.getTime() / 1000);
            yData.push(h.count || 0);
            peakTimes.push(h.peak_time || '');
        }
    });

    if (xData.length === 0) return null;

    const baseColor = options.color || getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#5a9bd5';

    // Calculate median for reference line
    const sortedY = [...yData].filter(v => v > 0).sort((a, b) => a - b);
    const median = sortedY.length > 0 ? sortedY[Math.floor(sortedY.length / 2)] : 0;
    const maxY = Math.max(...yData) || 1;

    // Tooltip plugin for histogram - use stored histogram reference
    function tooltipPlugin(histRef) {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    const { idx } = u.cursor;
                    const data0 = u.data[0];
                    const data1 = u.data[1];
                    if (idx == null || !data0 || idx < 0 || idx >= data0.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = data0[idx];
                    const y = data1[idx];
                    if (x === undefined || y === undefined || !Number.isFinite(x)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    // Format time from x value
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    // Try to find matching histogram entry for peak_time
                    const h = histRef[idx];
                    const peakInfo = h?.peak_time ? ` (peak: ${h.peak_time})` : '';
                    tooltip.innerHTML = `${timeStr} · ${y} sessions${peakInfo}`;
                    const left = u.valToPos(x, 'x');
                    const top = u.valToPos(y, 'y');
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 120) + 'px';
                    tooltip.style.top = Math.max(0, top - 50) + 'px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 120,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] }
        },
        axes: [
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                values: timeAxisValues,
                size: timeAxisSize,
                font: '10px system-ui'
            },
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false },
                ticks: { show: false },
                size: 30,
                font: '10px system-ui'
            }
        ],
        series: [
            {},
            {
                fill: 'transparent',
                stroke: 'transparent',
                width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [tooltipPlugin(histogram)],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0];
                const yd = u.data[1];
                const barWidth = Math.max(4, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(3, barWidth / 3);

                // Draw median line first (behind bars)
                if (median > 0) {
                    const yMed = u.valToPos(median, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim();
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, yMed);
                    ctx.lineTo(left + width, yMed);
                    ctx.stroke();
                    ctx.setLineDash([]);
                }

                // Draw bars (stacked: grey pre-log, orange new)
                const preData = u._yPre;
                const preColor = getComputedStyle(document.documentElement).getPropertyValue('--text-muted')?.trim() || '#999';
                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const total = yd[i];
                    const pre = preData ? preData[i] : 0;
                    const newVal = total - pre;

                    if (pre > 0) {
                        const yPre = u.valToPos(pre, 'y', true);
                        ctx.fillStyle = preColor;
                        ctx.beginPath();
                        if (newVal > 0) {
                            ctx.rect(x - barWidth/2, yPre, barWidth, y0 - yPre);
                        } else {
                            ctx.moveTo(x - barWidth/2, y0);
                            ctx.lineTo(x - barWidth/2, yPre + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, yPre, x - barWidth/2 + radius, yPre);
                            ctx.lineTo(x + barWidth/2 - radius, yPre);
                            ctx.quadraticCurveTo(x + barWidth/2, yPre, x + barWidth/2, yPre + radius);
                            ctx.lineTo(x + barWidth/2, y0);
                            ctx.closePath();
                        }
                        ctx.fill();
                    }

                    if (newVal > 0) {
                        const yTop = u.valToPos(total, 'y', true);
                        const yBottom = pre > 0 ? u.valToPos(pre, 'y', true) : y0;
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, yBottom);
                        ctx.lineTo(x - barWidth/2, yTop + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, yTop, x - barWidth/2 + radius, yTop);
                        ctx.lineTo(x + barWidth/2 - radius, yTop);
                        ctx.quadraticCurveTo(x + barWidth/2, yTop, x + barWidth/2, yTop + radius);
                        ctx.lineTo(x + barWidth/2, yBottom);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
                ctx.restore();
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    // Store original range for reset
    chart._originalXRange = [xData[0], xData[xData.length - 1]];
    chart._lastRange = null;
    chart.setScale('x', { min: xData[0], max: xData[xData.length - 1] });
    chart._median = median;

    // Handle resize
    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) {
            chart.setSize({ width: container.clientWidth, height: opts.height });
        }
    });
    resizeObserver.observe(container);

    return chart;
}

// Create concurrent sessions chart using sweep-line algorithm
export function createConcurrentChart(containerId, sessions, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !sessions || sessions.length === 0) return null;

    // Clear previous chart
    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    // Parse session events first to get time range
    const logStartT = options.logStart ? new Date(options.logStart).getTime() : null;
    const events = [];
    sessions.forEach(s => {
        const start = new Date(s.s).getTime();
        const end = new Date(s.e).getTime();
        if (!isNaN(start) && !isNaN(end)) {
            const pre = logStartT ? (start < logStartT) : false;
            events.push({ time: start, delta: 1, pre });
            events.push({ time: end, delta: -1, pre });
        }
    });
    if (events.length === 0) return null;

    // Sort events: by time, then starts before ends
    events.sort((a, b) => a.time - b.time || b.delta - a.delta);

    // Find time range
    const minT = events[0].time / 1000;
    const maxT = events[events.length - 1].time / 1000;
    const interval = options.interval !== undefined ? options.interval : (chartIntervalMap.get(containerId) ?? defaultInterval);

    // Initial binning
    const { xData, yData, yPre, median } = binConcurrentSessions(events, minT, maxT, interval);

    // Resolve CSS variable to actual color for canvas
    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#f47920';
        }
        return c;
    };
    const baseColor = resolveColor(options.color) || getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#f47920';

    // Tooltip for sessions - no _resampling check, rely on data validation
    function sessionsTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    const { idx } = u.cursor;
                    if (idx == null) { tooltip.style.display = 'none'; return; }
                    const data0 = u.data?.[0];
                    const data1 = u.data?.[1];
                    if (!data0 || !data1 || idx < 0 || idx >= data0.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = data0[idx];
                    const y = data1[idx];
                    if (!Number.isFinite(x) || !Number.isFinite(y)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const left = u.valToPos(x, 'x');
                    const top = u.valToPos(y, 'y');
                    if (!Number.isFinite(left) || !Number.isFinite(top)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    tooltip.innerHTML = `${timeStr} · ${Math.round(y)} sessions`;
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 100) + 'px';
                    tooltip.style.top = Math.max(0, top - 40) + 'px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 120,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        legend: { show: false },
        scales: { x: { time: true }, y: { range: [0, null] } },
        axes: [
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false }, ticks: { show: false },
                values: (u, vals) => {
                    const multiDay = (maxT - minT) > 86400;
                    return vals.map((v, i) => {
                        const d = new Date(v * 1000);
                        const time = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        if (!multiDay) return time;
                        const prevDay = i > 0 ? new Date(vals[i-1] * 1000).getDate() : -1;
                        if (i === 0 || d.getDate() !== prevDay) {
                            return time + '\n' + d.toLocaleDateString('en-US', { day: 'numeric', month: 'short' });
                        }
                        return time;
                    });
                },
                size: (maxT - minT) > 86400 ? 36 : 20, font: '10px system-ui'
            },
            {
                stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                grid: { show: false }, ticks: { show: false },
                size: 30, font: '10px system-ui'
            }
        ],
        series: [
            {},
            {
                fill: 'transparent', stroke: 'transparent', width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [sessionsTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0], yd = u.data[1];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(3, barWidth / 3);

                // Draw median line first (behind bars)
                const currentMedian = u._median || 0;
                if (currentMedian > 0) {
                    const yMed = u.valToPos(currentMedian, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim();
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, yMed);
                    ctx.lineTo(left + width, yMed);
                    ctx.stroke();
                    ctx.setLineDash([]);
                }

                // Draw bars (stacked: grey pre-log, orange new)
                const preData = u._yPre;
                const preColor = getComputedStyle(document.documentElement).getPropertyValue('--text-muted')?.trim() || '#999';
                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const total = yd[i];
                    const pre = preData ? preData[i] : 0;
                    const newVal = total - pre;

                    if (pre > 0) {
                        const yPre = u.valToPos(pre, 'y', true);
                        ctx.fillStyle = preColor;
                        ctx.beginPath();
                        if (newVal > 0) {
                            ctx.rect(x - barWidth/2, yPre, barWidth, y0 - yPre);
                        } else {
                            ctx.moveTo(x - barWidth/2, y0);
                            ctx.lineTo(x - barWidth/2, yPre + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, yPre, x - barWidth/2 + radius, yPre);
                            ctx.lineTo(x + barWidth/2 - radius, yPre);
                            ctx.quadraticCurveTo(x + barWidth/2, yPre, x + barWidth/2, yPre + radius);
                            ctx.lineTo(x + barWidth/2, y0);
                            ctx.closePath();
                        }
                        ctx.fill();
                    }

                    if (newVal > 0) {
                        const yTop = u.valToPos(total, 'y', true);
                        const yBottom = pre > 0 ? u.valToPos(pre, 'y', true) : y0;
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, yBottom);
                        ctx.lineTo(x - barWidth/2, yTop + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, yTop, x - barWidth/2 + radius, yTop);
                        ctx.lineTo(x + barWidth/2 - radius, yTop);
                        ctx.quadraticCurveTo(x + barWidth/2, yTop, x + barWidth/2, yTop + radius);
                        ctx.lineTo(x + barWidth/2, yBottom);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                // Re-sample on zoom
                if (!u._events || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, yData: newY, yPre: newYPre, median: newMedian } = binConcurrentSessions(u._events, newMin, newMax, u._interval);
                    u._median = newMedian;
                    u._yPre = newYPre;
                    u._resampling = true;
                    u.setData([newX, newY], false);
                    u._resampling = false;
                    // Force batch/commit cycle to reset internal state
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    // Store data for re-sampling
    chart._events = events;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._median = median;
    chart._yPre = yPre;

    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) chart.setSize({ width: container.clientWidth, height: opts.height });
    });
    resizeObserver.observe(container);

    return chart;
}

// Create combined temp files chart (count + size)
export function createCombinedTempFilesChart(containerId, events, options = {}) {
    const container = document.getElementById(containerId);
    if (!container || !events || events.length === 0) return null;

    // Parse events: extract timestamp and size
    const parsedEvents = events.map(e => ({
        ts: new Date(e.timestamp).getTime() / 1000,
        size: parseSizeToBytes(e.size)
    })).filter(x => !isNaN(x.ts)).sort((a, b) => a.ts - b.ts);

    if (parsedEvents.length === 0) return null;

    // Clear previous chart
    if (charts.has(containerId)) {
        charts.get(containerId).destroy();
        charts.delete(containerId);
    }
    container.innerHTML = '';

    const minT = parsedEvents[0].ts;
    const maxT = parsedEvents[parsedEvents.length - 1].ts;
    const interval = options.interval !== undefined ? options.interval : (chartIntervalMap.get(containerId) ?? defaultInterval);

    // Initial binning
    const { xData, countData, sizeData, medianCount } = binTempFilesData(parsedEvents, minT, maxT, interval);

    const countColor = getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5';
    const sizeColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#f5a623';
    const textColor = getComputedStyle(document.documentElement).getPropertyValue('--text').trim();
    const mutedColor = getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim();

    // Series visibility state
    const seriesVisible = { count: true, size: true };

    // Custom tooltip
    function tempFilesTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    if (u._resampling) { tooltip.style.display = 'none'; return; }
                    const { idx } = u.cursor;
                    const xd = u.data[0];
                    const countY = u.data[1];
                    const sizeY = u.data[2];
                    if (idx == null || !xd || idx < 0 || idx >= xd.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = xd[idx];
                    const count = countY[idx];
                    const size = sizeY[idx];
                    if (x === undefined || !Number.isFinite(x)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    let parts = [timeStr];
                    if (u._seriesVisible.count) parts.push(`${Math.round(count)} files`);
                    if (u._seriesVisible.size) parts.push(fmtBytes(size));
                    tooltip.innerHTML = parts.join(' · ');
                    const left = u.valToPos(x, 'x');
                    const topCount = u._seriesVisible.count ? u.valToPos(count, 'y', true) : u.bbox.top + u.bbox.height;
                    const topSize = u._seriesVisible.size ? u.valToPos(size, 'size', true) : u.bbox.top + u.bbox.height;
                    const top = Math.min(topCount, topSize);
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 140) + 'px';
                    tooltip.style.top = Math.max(0, top - 40) + 'px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 300,
        height: options.height || 150,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] },
            size: { range: [0, null] }
        },
        axes: [
            {
                stroke: textColor,
                grid: { show: false },
                ticks: { show: false },
                values: timeAxisValues,
                size: timeAxisSize,
                font: '10px system-ui'
            },
            {
                stroke: countColor,
                grid: { show: false },
                ticks: { show: false },
                size: 35,
                font: '10px system-ui',
                side: 3  // left
            },
            {
                scale: 'size',
                stroke: sizeColor,
                grid: { show: false },
                ticks: { show: false },
                size: 50,
                font: '10px system-ui',
                side: 1,  // right
                values: (u, vals) => vals.map(v => fmtBytesShort(v))
            }
        ],
        series: [
            {},
            { label: 'Count', scale: 'y', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null },
            { label: 'Size', scale: 'size', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null }
        ],
        plugins: [tempFilesTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0];
                const countY = u.data[1];
                const sizeY = u.data[2];
                const bothVisible = u._seriesVisible.count && u._seriesVisible.size;
                const totalBarWidth = Math.max(4, (u.bbox.width / xd.length) * 0.7);
                const barWidth = bothVisible ? totalBarWidth / 2 - 1 : totalBarWidth;
                const radius = Math.min(2, barWidth / 4);

                // Draw median line (behind bars)
                if (u._seriesVisible.count) {
                    const currentMedian = u._medianCount || 0;
                    if (currentMedian > 0) {
                        const yMed = u.valToPos(currentMedian, 'y', true);
                        const { left, width } = u.bbox;
                        ctx.strokeStyle = mutedColor;
                        ctx.lineWidth = 1;
                        ctx.setLineDash([4, 4]);
                        ctx.beginPath();
                        ctx.moveTo(left, yMed);
                        ctx.lineTo(left + width, yMed);
                        ctx.stroke();
                        ctx.setLineDash([]);
                    }
                }

                for (let i = 0; i < xd.length; i++) {
                    const xCenter = u.valToPos(xd[i], 'x', true);
                    const y0Count = u.valToPos(0, 'y', true);
                    const y0Size = u.valToPos(0, 'size', true);

                    // Draw count bar (left side if both visible)
                    if (u._seriesVisible.count && countY[i] > 0) {
                        const x = bothVisible ? xCenter - barWidth/2 - 0.5 : xCenter;
                        const y = u.valToPos(countY[i], 'y', true);
                        const h = y0Count - y;
                        if (h > 0) {
                            ctx.fillStyle = countColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Count);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Count);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }

                    // Draw size bar (right side if both visible)
                    if (u._seriesVisible.size && sizeY[i] > 0) {
                        const x = bothVisible ? xCenter + barWidth/2 + 0.5 : xCenter;
                        const y = u.valToPos(sizeY[i], 'size', true);
                        const h = y0Size - y;
                        if (h > 0) {
                            ctx.fillStyle = sizeColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Size);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Size);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                if (u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin != null && newMax != null && (newMax - newMin) > 1) {
                    const { xData: newX, countData: newCount, sizeData: newSize, medianCount: newMed } = binTempFilesData(u._events, newMin, newMax, u._interval);
                    u._medianCount = newMed;
                    u._resampling = true;
                    u.setData([newX, newCount, newSize], false);
                    u._resampling = false;
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, countData, sizeData], container);
    charts.set(containerId, chart);
    bindDblclickReset(chart, () => resetChartZoom(containerId));

    chart._events = parsedEvents;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._medianCount = medianCount;
    chart._seriesVisible = seriesVisible;
    chart._containerId = containerId;

    const resizeObserver = new ResizeObserver(() => {
        if (container.clientWidth > 0) {
            chart.setSize({ width: container.clientWidth, height: opts.height });
        }
    });
    resizeObserver.observe(container);

    return chart;
}

// Helper: parse size string to bytes
function parseSizeToBytes(size) {
    if (!size || typeof size !== 'string') return 0;
    const match = size.match(/([\d.]+)\s*(KB|MB|GB|TB|B)/i);
    if (!match) return parseFloat(size) || 0;
    const val = parseFloat(match[1]);
    const unit = match[2].toUpperCase();
    if (unit === 'TB') return val * 1024 * 1024 * 1024 * 1024;
    if (unit === 'GB') return val * 1024 * 1024 * 1024;
    if (unit === 'MB') return val * 1024 * 1024;
    if (unit === 'KB') return val * 1024;
    return val;
}

// Helper: format bytes for display
function fmtBytes(b) {
    if (b == null || isNaN(b)) return '-';
    if (b < 1024) return b.toFixed(0) + ' B';
    if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
    if (b < 1024 * 1024 * 1024) return (b / (1024 * 1024)).toFixed(1) + ' MB';
    return (b / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
}

// Helper: format bytes short (for Y axis)
function fmtBytesShort(b) {
    if (b == null || isNaN(b) || b === 0) return '0';
    if (b < 1024) return b.toFixed(0) + 'B';
    if (b < 1024 * 1024) return (b / 1024).toFixed(0) + 'K';
    if (b < 1024 * 1024 * 1024) return (b / (1024 * 1024)).toFixed(0) + 'M';
    return (b / (1024 * 1024 * 1024)).toFixed(1) + 'G';
}

// Build chart container HTML with controls
export function buildChartContainer(id, title, options = {}) {
    const showIntervalControl = options.showBucketControl !== false;
    const currentInterval = chartIntervalMap.get(id) ?? defaultInterval;
    const tooltip = options.tooltip || '';
    const infoIcon = tooltip ? `<ql-tooltip text="${tooltip}">i</ql-tooltip>` : '';
    return `
        <div class="chart-container">
            <div class="chart-controls">
                <span class="subsection-title" style="margin: 0; font-size: 0.7rem;">${title}${infoIcon}</span>
                <div style="display: flex; gap: 0.5rem; align-items: center;">
                    <span class="zoom-hint">drag to zoom</span>
                    ${showIntervalControl ? `
                        <select onchange="updateChartInterval('${id}', this.value)">
                            <option value="0" ${currentInterval === 0 ? 'selected' : ''}>Auto</option>
                            <option value="60" ${currentInterval === 60 ? 'selected' : ''}>1 min</option>
                            <option value="300" ${currentInterval === 300 ? 'selected' : ''}>5 min</option>
                            <option value="900" ${currentInterval === 900 ? 'selected' : ''}>15 min</option>
                            <option value="3600" ${currentInterval === 3600 ? 'selected' : ''}>1h</option>
                        </select>
                    ` : ''}
                    <button onclick="resetChartZoom('${id}')">Reset</button>
                    <button class="btn-expand" onclick="openChartModal('${id}', '${title.replace(/'/g, "\\'")}')" title="Expand chart">⛶</button>
                </div>
            </div>
            <div id="${id}" style="min-height: 120px;"></div>
        </div>
    `;
}

// Update chart with new interval
export function updateChartInterval(chartId, intervalValue) {
    const interval = parseInt(intervalValue);
    chartIntervalMap.set(chartId, interval);

    // Recreate only this specific chart
    const data = chartData.get(chartId);
    if (data) {
        const accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
        const color = chartId.includes('tempfiles') ? accentColor : null;
        if (data?.type === 'sessions') {
            createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)', interval, logStart: data.logStart, logEnd: data.logEnd });
        } else if (data?.type === 'histogram') {
            // Pre-computed histogram can't change interval
        } else if (data?.type === 'duration') {
            createDurationChart(chartId, data.data, { color: accentColor, interval });
        } else if (data?.type === 'combined') {
            createCombinedSQLChart(chartId, data.data, { interval });
        } else if (data?.type === 'checkpoints') {
            createCheckpointChart(chartId, data, { interval });
        } else if (data?.type === 'combined-tempfiles') {
            createCombinedTempFilesChart(chartId, data.events, { interval });
        } else {
            createTimeChart(chartId, data, { color, interval });
        }
    }
}

// Reset chart zoom and re-sample to original range
export function resetChartZoom(chartId) {
    const chart = charts.get(chartId);
    if (chart && chart._originalXRange) {
        const [min, max] = chart._originalXRange;
        // Clear lastRange to force re-sampling
        chart._lastRange = null;
        chart.setScale('x', { min, max });
    }
}

// Store chart data for re-creation
// chartData declared at module level

// Modal state
// modalChart declared at module level
// modalChartId declared at module level
// modalInterval declared at module level  // 0 = Auto

// Open chart in modal
export function openChartModal(chartId, title) {
    const data = chartData.get(chartId);
    if (!data) return;

    modalChartId = chartId;
    modalInterval = chartIntervalMap.get(chartId) ?? defaultInterval;

    document.getElementById('modalChartTitle').textContent = title;
    document.getElementById('modalBucketSelect').value = modalInterval;
    document.getElementById('chartModal').classList.add('active');
    document.body.style.overflow = 'hidden';

    // Hide interval select for pre-computed histograms
    const intervalSelect = document.getElementById('modalBucketSelect');
    intervalSelect.style.display = (data?.type === 'histogram' || data?.type === 'wal-distance') ? 'none' : '';

    // Create expanded chart
    setTimeout(() => renderModalChart(), 50);
}

// Render chart in modal
export function renderModalChart() {
    const container = document.getElementById('modal-chart-container');
    container.innerHTML = '';

    const data = chartData.get(modalChartId);
    if (!data) return;

    const accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
    const color = modalChartId.includes('tempfiles') ? accentColor : null;

    // Create larger chart
    if (data?.type === 'wal-distance') {
        modalChart = createWALDistanceChart('modal-chart-container', data, { height: 500 });
    } else if (data?.type === 'checkpoints') {
        modalChart = createCheckpointChartLarge(container, data, {
            interval: modalInterval,
            height: 500
        });
    } else if (data?.type === 'sessions') {
        modalChart = createConcurrentChartLarge(container, data.data, {
            color: color || 'var(--accent)',
            interval: modalInterval,
            height: 500,
            logStart: data.logStart,
            logEnd: data.logEnd
        });
    } else if (data?.type === 'histogram') {
        modalChart = createHistogramChartLarge(container, data.data, {
            color: color || 'var(--accent)',
            height: 500
        });
    } else if (data?.type === 'duration') {
        modalChart = createDurationChartLarge(container, data.data, {
            color: accentColor,
            interval: modalInterval,
            height: 500
        });
    } else if (data?.type === 'combined') {
        modalChart = createCombinedSQLChartLarge(container, data.data, {
            interval: modalInterval,
            height: 500
        });
    } else if (data?.type === 'combined-tempfiles') {
        modalChart = createCombinedTempFilesChartLarge(container, data.events, {
            interval: modalInterval,
            height: 500
        });
    } else {
        modalChart = createTimeChartLarge(container, data, {
            color,
            interval: modalInterval,
            height: 500
        });
    }

    if (modalChart) bindDblclickReset(modalChart, resetModalZoom);

    // Add modal legend based on chart type
    let legendEl = document.getElementById('modal-chart-legend');
    if (!legendEl) {
        legendEl = document.createElement('div');
        legendEl.id = 'modal-chart-legend';
        container.parentNode.appendChild(legendEl);
    }
    legendEl.style.cssText = 'display:flex;gap:16px;justify-content:center;margin-top:8px;font-size:13px;';
    const dot = (color) => `<span style="display:inline-block;width:12px;height:12px;background:${color};border-radius:2px;vertical-align:middle;margin-right:4px;"></span>`;
    const dash = (color) => `<span style="display:inline-block;width:16px;height:0;border-top:2px dashed ${color};vertical-align:middle;margin-right:4px;"></span>`;
    const legends = {
        'sessions': `<span>${dot('var(--accent)')}Sessions</span><span>${dot('var(--text-muted)')}Pre-log</span><span>${dash('var(--text-muted)')}Median</span>`,
        'checkpoints': `<span>${dot('var(--chart-bar)')}Timed</span><span>${dot('var(--accent)')}WAL</span><span>${dot('#909399')}Other</span>`,
        'wal-distance': `<span>${dot('var(--chart-bar)')}Distance</span><span>${dash('var(--accent)')}Estimate</span>`,
        'combined': `<span>${dot('var(--chart-bar)')}Count</span><span>${dot('var(--accent)')}Duration</span><span>${dash('var(--text-muted)')}Median</span>`,
        'combined-tempfiles': `<span>${dot('var(--chart-bar)')}Count</span><span>${dot('var(--accent)')}Size</span><span>${dash('var(--text-muted)')}Median</span>`,
    };
    const defaultLegend = `<span>${dot('var(--chart-bar)')}Connections</span><span>${dash('var(--text-muted)')}Median</span>`;
    legendEl.innerHTML = legends[data?.type] || defaultLegend;
}

// Cost map (uPlot port): log-log scatter of queries (count × avg duration).
// uPlot's native log distr (distr:3) silently aborts the first draw in this
// build, so instead the data is fed already log10-transformed onto LINEAR
// scales — same visual, and uPlot still owns the grid, 2D drag-zoom, reset and
// the expand modal. Axis ticks are placed at integer decades and formatted back
// to real units. A draw hook paints the iso-cumulative + Pareto diagonals and
// the colour-bucketed points (radius adapts to point count); a 2-point dummy
// series only anchors the scale ranges — nothing of it is drawn.
export function createCostMapChart(containerId, queries, options = {}) {
    const container = typeof containerId === 'string' ? document.getElementById(containerId) : containerId;
    if (!container) return null;

    const pts = (queries || [])
        .filter(q => (q.count || 0) > 0 && (q.avg_time_ms || 0) > 0)
        .map(q => ({ id: q.id, type: q.type || q.query_type || '', count: q.count, avg: q.avg_time_ms, total: q.total_time_ms || q.count * q.avg_time_ms }));
    if (pts.length === 0) return null;

    // Log domains padded to half-decades, then the shorter axis is expanded so
    // X and Y carry the same number of decades — with a square plot that keeps
    // the iso-cumulative diagonals at 45°.
    const xs = pts.map(p => Math.log10(p.count));
    const ys = pts.map(p => Math.log10(p.avg));
    const hf = v => Math.floor(v * 2) / 2, hc = v => Math.ceil(v * 2) / 2;
    let logXMin = hf(safeMin(xs)), logXMax = Math.max(hc(safeMax(xs)), logXMin + 1);
    let logYMin = hf(safeMin(ys)), logYMax = Math.max(hc(safeMax(ys)), logYMin + 1);
    const range = Math.max(logXMax - logXMin, logYMax - logYMin);
    if (logXMax - logXMin < range) { const p = (range - (logXMax - logXMin)) / 2; logXMin -= p; logXMax += p; }
    if (logYMax - logYMin < range) { const p = (range - (logYMax - logYMin)) / 2; logYMin -= p; logYMax += p; }
    // Counts are integers >= 1, so the X axis must not drop below 10^0 = 1
    // (that would print meaningless sub-1 "0" ticks). Shift the whole X window
    // up if padding pushed it negative; count=1 then sits on the left edge and
    // the draw hook gives points an expanded clip so they aren't cut in half.
    if (logXMin < 0) { logXMax -= logXMin; logXMin = 0; }

    const css = v => getComputedStyle(document.documentElement).getPropertyValue(v).trim();
    const BUCKETS = [
        { upTo: 0.25, color: css('--primary') },
        { upTo: 0.50, color: css('--warning') },
        { upTo: 0.75, color: css('--danger') },
        { upTo: 1.01, color: css('--purple') },
    ];
    const tlogs = pts.map(p => Math.log10(p.total));
    const logTMin = safeMin(tlogs), logTMax = safeMax(tlogs);
    const colorFor = total => {
        const t = logTMax === logTMin ? 0 : (Math.log10(total) - logTMin) / (logTMax - logTMin);
        return (BUCKETS.find(b => t <= b.upTo) || BUCKETS[3]).color;
    };

    const density = Math.min(1, Math.max(0, (Math.log10(pts.length) - 1) / (Math.log10(2000) - 1)));
    const baseR = 4 - density * 2; // CSS px: ~4 (sparse) down to ~2 (dense)
    let hoverId = null;

    const fmtCount = v => v >= 1e6 ? (v / 1e6).toFixed(v >= 1e7 ? 0 : 1).replace(/\.0$/, '') + 'M'
        : v >= 1e3 ? (v / 1e3).toFixed(v >= 1e4 ? 0 : 1).replace(/\.0$/, '') + 'k' : String(Math.round(v));
    const fmtMs = v => v >= 3600000 ? (v / 3600000).toFixed(0) + 'h'
        : v >= 60000 ? (v / 60000).toFixed(0) + 'min'
            : v >= 1000 ? (v / 1000).toFixed(0) + 's'
                : v >= 1 ? v.toFixed(0) + 'ms' : v.toFixed(1) + 'ms';

    // Pareto thresholds: smallest per-query total T whose heavier queries sum
    // to frac of the grand total.
    const sortedTotals = pts.map(p => p.total).sort((a, b) => b - a);
    const grandTotal = sortedTotals.reduce((a, b) => a + b, 0);
    const topShare = frac => { let acc = 0; for (const t of sortedTotals) { acc += t; if (acc >= frac * grandTotal) return t; } return sortedTotals[sortedTotals.length - 1]; };

    // Coordinates: scales are linear over log10 values, so real value v maps via
    // log10(v) through valToPos. Scale min/max are in log space.
    const drawCostMap = u => {
        const ctx = u.ctx, b = u.bbox;
        // u.bbox and valToPos(...,true) are in device pixels; uPlot does not
        // scale the context, so every hard-coded size (radius, line width,
        // font) must be multiplied by pxRatio to render at the intended CSS
        // size — without this, points were ~half size on retina.
        const dpr = u.pxRatio || window.devicePixelRatio || 1;
        const X = v => u.valToPos(Math.log10(v), 'x', true);
        const Y = v => u.valToPos(Math.log10(v), 'y', true);
        const xMin = Math.pow(10, u.scales.x.min), xMax = Math.pow(10, u.scales.x.max);
        const yMax = Math.pow(10, u.scales.y.max);
        ctx.save();
        ctx.beginPath();
        ctx.rect(b.left, b.top, b.width, b.height);
        ctx.clip();

        const diagonal = (T, stroke, width, dash, opacity, label, labelColor) => {
            ctx.save();
            ctx.globalAlpha = opacity;
            ctx.strokeStyle = stroke;
            ctx.lineWidth = width * dpr;
            ctx.setLineDash(dash.map(d => d * dpr));
            ctx.beginPath();
            ctx.moveTo(X(xMin), Y(T / xMin));
            ctx.lineTo(X(xMax), Y(T / xMax));
            ctx.stroke();
            ctx.setLineDash([]);
            if (label) {
                let lx = xMax, ly = T / xMax;
                if (Math.log10(ly) > Math.log10(yMax)) { ly = yMax; lx = T / yMax; }
                ctx.globalAlpha = 1;
                ctx.fillStyle = labelColor;
                ctx.font = 'italic ' + (10 * dpr) + 'px system-ui';
                ctx.textAlign = 'right';
                ctx.fillText(label, X(lx) - 5 * dpr, Y(ly) - 4 * dpr);
            }
            ctx.restore();
        };
        const muted = css('--text-muted'), success = css('--success');
        [[1000, '1s'], [60000, '1min'], [3600000, '1h'], [86400000, '1d']].forEach(([T, l]) =>
            diagonal(T, muted, 0.7, [4, 3], 0.5, l, muted));
        if (grandTotal > 0 && pts.length > 1) {
            const t1 = topShare(0.01), t10 = topShare(0.10);
            diagonal(t1, success, 1.1, [], 0.75, 'top 1%', success);
            if (Math.abs(Math.log10(t10) - Math.log10(t1)) > 0.05) diagonal(t10, success, 1.1, [6, 2], 0.75, 'top 10%', success);
        }

        ctx.restore(); // end diagonal clip (strict bbox)

        // Points get a clip expanded by their radius so boundary points (e.g.
        // count=1 on the left edge) render whole instead of being sliced.
        const m = (baseR * 1.8 + 2) * dpr;
        ctx.save();
        ctx.beginPath();
        ctx.rect(b.left - m, b.top - m, b.width + 2 * m, b.height + 2 * m);
        ctx.clip();
        const bg = css('--bg'), textc = css('--text');
        for (const p of pts) {
            const hovered = p.id === hoverId;
            ctx.beginPath();
            ctx.arc(X(p.count), Y(p.avg), (hovered ? baseR * 1.8 : baseR) * dpr, 0, Math.PI * 2);
            ctx.fillStyle = colorFor(p.total);
            ctx.globalAlpha = hovered ? 1 : 0.85;
            ctx.fill();
            ctx.globalAlpha = 1;
            ctx.lineWidth = (hovered ? 1.5 : 0.5) * dpr;
            ctx.strokeStyle = hovered ? textc : bg;
            ctx.stroke();
        }
        ctx.restore();
    };

    // Decade ticks: integer log values within the visible range.
    const decadeSplits = (u, axisIdx, scaleMin, scaleMax) => {
        const out = [];
        for (let d = Math.ceil(scaleMin); d <= Math.floor(scaleMax); d++) out.push(d);
        return out;
    };
    const text = css('--text');
    const initW = container.clientWidth || 320;
    const opts = {
        width: initW,
        height: options.height || initW,
        cursor: { drag: { x: true, y: true, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { range: (u, min, max) => [min, max] },
            y: { range: (u, min, max) => [min, max] },
        },
        axes: [
            { scale: 'x', stroke: text, font: '10px system-ui', size: 28, grid: { stroke: css('--border'), width: 0.5 }, ticks: { show: false }, splits: decadeSplits, values: (u, vals) => vals.map(v => fmtCount(Math.pow(10, v))) },
            { scale: 'y', stroke: text, font: '10px system-ui', size: 40, grid: { stroke: css('--border'), width: 0.5 }, ticks: { show: false }, splits: decadeSplits, values: (u, vals) => vals.map(v => fmtMs(Math.pow(10, v))) },
        ],
        series: [
            {},
            { scale: 'y', paths: () => null, points: { show: false } },
        ],
        hooks: { draw: [drawCostMap] },
    };

    const id = typeof containerId === 'string' ? containerId : container.id;
    const chart = new uPlot(opts, [[logXMin, logXMax], [logYMin, logYMax]], container);
    charts.set(id, chart);
    chart._cmXRange = [logXMin, logXMax];
    chart._cmYRange = [logYMin, logYMax];
    bindDblclickReset(chart, () => resetCostMapZoom(id));

    // Cross-highlight bridge (inline chart only — the modal has no table next
    // to it). highlightQuery (app.js) calls this to enlarge the matching point
    // when a table row is hovered; the point's own hover calls highlightQuery
    // the other way. costMapHighlight never calls back, so there is no loop.
    const bridge = id === 'chart-costmap';
    if (bridge) {
        window.costMapHighlight = (qid, on) => {
            const v = on ? qid : null;
            if (hoverId !== v) { hoverId = v; chart.redraw(); }
        };
    }

    // Keep the iso-cumulative diagonals at a true 45°: X and Y already span the
    // same number of decades, so the plot AREA must be square. Resize the chart
    // so bbox is square (fits the smaller available dimension) and centre it —
    // this matters most in the wide expand modal.
    chart.root.style.margin = '0 auto';
    const squarePlot = () => {
        const availW = container.clientWidth || initW;
        const availH = options.height || availW;
        const dpr = chart.pxRatio || window.devicePixelRatio || 1;
        const gx = chart.width - chart.bbox.width / dpr;  // left+right gutters (CSS px)
        const gy = chart.height - chart.bbox.height / dpr; // top+bottom gutters
        const side = Math.max(80, Math.min(availW - gx, availH - gy));
        const w = Math.round(side + gx), h = Math.round(side + gy);
        if (w !== chart.width || h !== chart.height) chart.setSize({ width: w, height: h });
    };
    squarePlot();

    // Click a point to open its detail modal (canvas hit-test, nearest point).
    chart.over.addEventListener('click', e => {
        const r = chart.over.getBoundingClientRect();
        const mx = e.clientX - r.left, my = e.clientY - r.top;
        let best = null, bd = Infinity;
        for (const p of pts) {
            const dx = chart.valToPos(Math.log10(p.count), 'x', false) - mx;
            const dy = chart.valToPos(Math.log10(p.avg), 'y', false) - my;
            const d = dx * dx + dy * dy;
            if (d < bd) { bd = d; best = p; }
        }
        if (best && Math.sqrt(bd) <= baseR + 6 && window.showQueryModal) window.showQueryModal(best.id);
    });
    // Hover: enlarge the nearest point, show a tooltip, switch the cursor.
    if (getComputedStyle(container).position === 'static') container.style.position = 'relative';
    const tip = document.createElement('div');
    tip.style.cssText = 'position:absolute;pointer-events:none;display:none;background:var(--bg);border:1px solid var(--border);border-radius:4px;padding:3px 6px;font:11px system-ui;color:var(--text);box-shadow:0 2px 8px rgba(0,0,0,.18);z-index:5;white-space:nowrap;';
    container.appendChild(tip);
    const nearest = (mx, my) => {
        let best = null, bd = (baseR + 6) * (baseR + 6);
        for (const p of pts) {
            const dx = chart.valToPos(Math.log10(p.count), 'x', false) - mx;
            const dy = chart.valToPos(Math.log10(p.avg), 'y', false) - my;
            const d = dx * dx + dy * dy;
            if (d <= bd) { bd = d; best = p; }
        }
        return best;
    };
    let lastNid = null;
    const setHover = (nid, scroll) => {
        if (nid === lastNid) return;
        if (bridge && window.highlightQuery) {
            // Route through highlightQuery so the matching table row highlights
            // (and scrolls into view); it calls back costMapHighlight to enlarge
            // the point. Guarded by lastNid so it fires only on change.
            if (lastNid) window.highlightQuery(lastNid, false);
            if (nid) window.highlightQuery(nid, true, scroll);
        } else if (hoverId !== nid) {
            hoverId = nid; chart.redraw();
        }
        lastNid = nid;
    };
    chart.over.addEventListener('mousemove', e => {
        const r = chart.over.getBoundingClientRect();
        const mx = e.clientX - r.left, my = e.clientY - r.top;
        const p = nearest(mx, my);
        chart.over.style.cursor = p ? 'pointer' : 'default';
        setHover(p ? p.id : null, true);
        if (p) {
            tip.innerHTML = (p.type ? p.type + ' · ' : '') + p.count + '× · avg ' + fmtMs(p.avg) + ' · cumulated ' + fmtMs(p.total);
            tip.style.display = 'block';
            let left = chart.over.offsetLeft + mx + 12;
            let top = chart.over.offsetTop + my + 12;
            if (left + tip.offsetWidth > container.clientWidth) left = container.clientWidth - tip.offsetWidth - 2;
            tip.style.left = left + 'px';
            tip.style.top = top + 'px';
        } else {
            tip.style.display = 'none';
        }
    });
    chart.over.addEventListener('mouseleave', () => {
        tip.style.display = 'none';
        setHover(null, false);
    });

    const ro = new ResizeObserver(() => { if (container.clientWidth > 0) squarePlot(); });
    ro.observe(container);
    return chart;
}

// Reset a cost-map chart to its auto-fit (log-space) domain on both axes.
export function resetCostMapZoom(id) {
    const chart = charts.get(id);
    if (!chart || !chart._cmXRange) return;
    chart.setScale('x', { min: chart._cmXRange[0], max: chart._cmXRange[1] });
    chart.setScale('y', { min: chart._cmYRange[0], max: chart._cmYRange[1] });
}

// Create large time chart for modal
export function createTimeChartLarge(container, timestamps, options = {}) {
    if (!timestamps?.length) return null;

    const height = options.height || 350;
    // Parse and store times in seconds for consistency
    const times = timestamps.map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)).sort((a, b) => a - b);
    if (times.length === 0) return null;

    const minT = times[0], maxT = times[times.length - 1];
    const interval = options.interval !== undefined ? options.interval : modalInterval;

    // Initial binning
    const { xData, yData, median } = binTimestamps(times, minT, maxT, interval);

    // Resolve CSS variable to actual color for canvas
    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
        }
        return c || '#5a9bd5';
    };
    const baseColor = resolveColor(options.color) || resolveColor('var(--chart-bar)');
    const textColor = resolveColor('var(--text)');
    const borderColor = resolveColor('var(--border)');

    // Add padding to prevent bars from being cut off
    const xPadding = (xData[xData.length - 1] - xData[0]) / (xData.length * 2);
    const xMin = xData[0] - xPadding;
    const xMax = xData[xData.length - 1] + xPadding;

    const opts = {
        width: container.clientWidth || 1100,
        height: height,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] }
        },
        axes: [
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } },
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } }
        ],
        series: [
            {},
            {
                fill: 'transparent',
                stroke: 'transparent',
                width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [tooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0], yd = u.data[1];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(4, barWidth / 3);

                // Draw median line first (behind bars)
                const currentMedian = u._median || 0;
                if (currentMedian > 0) {
                    const yMed = u.valToPos(currentMedian, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = textColor;
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, yMed);
                    ctx.lineTo(left + width, yMed);
                    ctx.stroke();
                    ctx.setLineDash([]);
                }

                // Draw bars (stacked: grey pre-log, orange new)
                const preData = u._yPre;
                const preColor = getComputedStyle(document.documentElement).getPropertyValue('--text-muted')?.trim() || '#999';
                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const total = yd[i];
                    const pre = preData ? preData[i] : 0;
                    const newVal = total - pre;

                    if (pre > 0) {
                        const yPre = u.valToPos(pre, 'y', true);
                        ctx.fillStyle = preColor;
                        ctx.beginPath();
                        if (newVal > 0) {
                            ctx.rect(x - barWidth/2, yPre, barWidth, y0 - yPre);
                        } else {
                            ctx.moveTo(x - barWidth/2, y0);
                            ctx.lineTo(x - barWidth/2, yPre + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, yPre, x - barWidth/2 + radius, yPre);
                            ctx.lineTo(x + barWidth/2 - radius, yPre);
                            ctx.quadraticCurveTo(x + barWidth/2, yPre, x + barWidth/2, yPre + radius);
                            ctx.lineTo(x + barWidth/2, y0);
                            ctx.closePath();
                        }
                        ctx.fill();
                    }

                    if (newVal > 0) {
                        const yTop = u.valToPos(total, 'y', true);
                        const yBottom = pre > 0 ? u.valToPos(pre, 'y', true) : y0;
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, yBottom);
                        ctx.lineTo(x - barWidth/2, yTop + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, yTop, x - barWidth/2 + radius, yTop);
                        ctx.lineTo(x + barWidth/2 - radius, yTop);
                        ctx.quadraticCurveTo(x + barWidth/2, yTop, x + barWidth/2, yTop + radius);
                        ctx.lineTo(x + barWidth/2, yBottom);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                // Re-sample on zoom
                if (!u._times || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, yData: newY, median: newMedian } = binTimestamps(u._times, newMin, newMax, u._interval);
                    u._median = newMedian;
                    u._resampling = true;
                    u.setData([newX, newY], false);
                    u._resampling = false;
                    // Force batch/commit cycle to reset cursor state
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);

    // Store data for re-sampling
    chart._times = times;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._median = median;
    return chart;
}

// Create large duration chart for modal
export function createDurationChartLarge(container, executions, options = {}) {
    if (!executions?.length) return null;

    const sorted = [...executions].sort((a, b) => a.t - b.t);
    const minT = sorted[0].t;
    const maxT = sorted[sorted.length - 1].t;
    const interval = options.interval ?? 0;
    const height = options.height || 350;

    const { xData, yData, median } = binDurations(sorted, minT, maxT, interval);

    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
        }
        return c;
    };
    const baseColor = resolveColor(options.color) || getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5';
    const textColor = resolveColor('var(--text)');
    const borderColor = resolveColor('var(--border)');

    function durationTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    const { idx } = u.cursor;
                    if (idx == null) { tooltip.style.display = 'none'; return; }
                    const data0 = u.data?.[0];
                    const data1 = u.data?.[1];
                    if (!data0 || !data1 || idx < 0 || idx >= data0.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = data0[idx];
                    const y = data1[idx];
                    if (!Number.isFinite(x) || !Number.isFinite(y)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const left = u.valToPos(x, 'x');
                    const top = u.valToPos(y, 'y');
                    if (!Number.isFinite(left) || !Number.isFinite(top)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    const durStr = y >= 60 ? `${(y/60).toFixed(1)}m` : `${y.toFixed(1)}s`;
                    tooltip.innerHTML = `${timeStr} · ${durStr}`;
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 100) + 'px';
                    tooltip.style.top = Math.max(0, top - 40) + 'px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 1100,
        height: height,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] }
        },
        axes: [
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } },
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor },
              values: (u, vals) => vals.map(v => v >= 60 ? `${(v/60).toFixed(0)}m` : `${v.toFixed(0)}s`) }
        ],
        series: [
            {},
            {
                fill: 'transparent',
                stroke: 'transparent',
                width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [durationTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0], yd = u.data[1];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(4, barWidth / 3);

                // Draw median line first (behind bars)
                const currentMedian = u._median || 0;
                if (currentMedian > 0) {
                    const yMed = u.valToPos(currentMedian, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = resolveColor('var(--text-muted)');
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, yMed);
                    ctx.lineTo(left + width, yMed);
                    ctx.stroke();
                    ctx.setLineDash([]);
                }

                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y = u.valToPos(yd[i], 'y', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const h = y0 - y;
                    if (h > 0) {
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, y0);
                        ctx.lineTo(x - barWidth/2, y + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                        ctx.lineTo(x + barWidth/2 - radius, y);
                        ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                        ctx.lineTo(x + barWidth/2, y0);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                if (!u._executions || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, yData: newY, median: newMedian } = binDurations(u._executions, newMin, newMax, u._interval);
                    u._median = newMedian;
                    u._resampling = true;
                    u.setData([newX, newY], false);
                    u._resampling = false;
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);

    chart._executions = sorted;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._median = median;
    return chart;
}

// Create large combined SQL chart for modal (grouped bars)
export function createCombinedSQLChartLarge(container, rawData, options = {}) {
    if (!rawData?.times?.length) return null;

    const { times, executions } = rawData;
    const minT = times[0];
    const maxT = times[times.length - 1];
    const interval = options.interval ?? 0;
    const height = options.height || 350;

    const { xData, countData, durationData, medianCount } = binCombinedData(times, executions, minT, maxT, interval);

    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
        }
        return c;
    };
    const countColor = resolveColor('var(--chart-bar)');
    const durationColor = resolveColor('var(--accent)');
    const textColor = resolveColor('var(--text)');
    const borderColor = resolveColor('var(--border)');
    const mutedColor = resolveColor('var(--text-muted)');

    // Series visibility state (always both visible in modal for now)
    const seriesVisible = { count: true, duration: true };

    function combinedTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    if (u._resampling) { tooltip.style.display = 'none'; return; }
                    const { idx } = u.cursor;
                    const xd = u.data[0];
                    const countY = u.data[1];
                    const durY = u.data[2];
                    if (idx == null || !xd || idx < 0 || idx >= xd.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = xd[idx];
                    const count = countY[idx];
                    const dur = durY[idx];
                    if (x === undefined || !Number.isFinite(x)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    const durStr = dur >= 60 ? `${(dur/60).toFixed(1)}m` : `${dur.toFixed(1)}s`;
                    tooltip.innerHTML = `${timeStr} · ${fmt(count)} queries · ${durStr}`;
                    const left = u.valToPos(x, 'x');
                    const topCount = u.valToPos(count, 'y', true);
                    const topDur = u.valToPos(dur, 'duration', true);
                    const top = Math.min(topCount, topDur);
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 140) + 'px';
                    tooltip.style.top = Math.max(0, top - 40) + 'px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 1100,
        height: height,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] },
            duration: { range: [0, null] }
        },
        axes: [
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } },
            { stroke: countColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor }, side: 3 },
            { scale: 'duration', stroke: durationColor, grid: { show: false }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor }, side: 1,
              values: (u, vals) => vals.map(v => v >= 60 ? `${(v/60).toFixed(0)}m` : `${v.toFixed(0)}s`) }
        ],
        series: [
            {},
            { label: 'Count', scale: 'y', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null },
            { label: 'Duration', scale: 'duration', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null }
        ],
        plugins: [combinedTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0];
                const countY = u.data[1];
                const durY = u.data[2];
                const bothVisible = u._seriesVisible.count && u._seriesVisible.duration;
                const totalBarWidth = Math.max(4, (u.bbox.width / xd.length) * 0.7);
                const barWidth = bothVisible ? totalBarWidth / 2 - 1 : totalBarWidth;
                const radius = Math.min(3, barWidth / 4);

                // Draw median line first (behind bars)
                if (u._seriesVisible.count) {
                    const currentMedian = u._medianCount || 0;
                    if (currentMedian > 0) {
                        const yMed = u.valToPos(currentMedian, 'y', true);
                        const { left, width } = u.bbox;
                        ctx.strokeStyle = mutedColor;
                        ctx.lineWidth = 1;
                        ctx.setLineDash([4, 4]);
                        ctx.beginPath();
                        ctx.moveTo(left, yMed);
                        ctx.lineTo(left + width, yMed);
                        ctx.stroke();
                        ctx.setLineDash([]);
                    }
                }

                for (let i = 0; i < xd.length; i++) {
                    const xCenter = u.valToPos(xd[i], 'x', true);
                    const y0Count = u.valToPos(0, 'y', true);
                    const y0Dur = u.valToPos(0, 'duration', true);

                    // Draw count bar (left side)
                    if (u._seriesVisible.count && countY[i] > 0) {
                        const x = bothVisible ? xCenter - barWidth/2 - 0.5 : xCenter;
                        const y = u.valToPos(countY[i], 'y', true);
                        const h = y0Count - y;
                        if (h > 0) {
                            ctx.fillStyle = countColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Count);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Count);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }

                    // Draw duration bar (right side)
                    if (u._seriesVisible.duration && durY[i] > 0) {
                        const x = bothVisible ? xCenter + barWidth/2 + 0.5 : xCenter;
                        const y = u.valToPos(durY[i], 'duration', true);
                        const h = y0Dur - y;
                        if (h > 0) {
                            ctx.fillStyle = durationColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Dur);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Dur);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                if (!u._rawData || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, countData: newCount, durationData: newDur, medianCount: newMed } = binCombinedData(u._rawData.times, u._rawData.executions, newMin, newMax, u._interval);
                    u._medianCount = newMed;
                    u._resampling = true;
                    u.setData([newX, newCount, newDur], false);
                    u._resampling = false;
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, countData, durationData], container);

    chart._rawData = rawData;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._medianCount = medianCount;
    chart._seriesVisible = seriesVisible;
    return chart;
}

// Create large combined temp files chart for modal
export function createCombinedTempFilesChartLarge(container, events, options = {}) {
    if (!events || events.length === 0) return null;

    // Parse events
    const parsedEvents = events.map(e => ({
        ts: new Date(e.timestamp).getTime() / 1000,
        size: parseSizeToBytes(e.size)
    })).filter(x => !isNaN(x.ts)).sort((a, b) => a.ts - b.ts);

    if (parsedEvents.length === 0) return null;

    const minT = parsedEvents[0].ts;
    const maxT = parsedEvents[parsedEvents.length - 1].ts;
    const interval = options.interval !== undefined ? options.interval : 0;

    const { xData, countData, sizeData, medianCount } = binTempFilesData(parsedEvents, minT, maxT, interval);

    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
        }
        return c;
    };
    const countColor = resolveColor('var(--chart-bar)');
    const sizeColor = resolveColor('var(--accent)');
    const textColor = resolveColor('var(--text)');
    const borderColor = resolveColor('var(--border)');
    const mutedColor = resolveColor('var(--text-muted)');

    const seriesVisible = { count: true, size: true };

    function tempFilesTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    if (u._resampling) { tooltip.style.display = 'none'; return; }
                    const { idx } = u.cursor;
                    const xd = u.data[0];
                    const countY = u.data[1];
                    const sizeY = u.data[2];
                    if (idx == null || !xd || idx < 0 || idx >= xd.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = xd[idx];
                    const count = countY[idx];
                    const size = sizeY[idx];
                    if (x === undefined || !Number.isFinite(x)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    let parts = [timeStr];
                    if (u._seriesVisible.count) parts.push(`${Math.round(count)} files`);
                    if (u._seriesVisible.size) parts.push(fmtBytes(size));
                    tooltip.innerHTML = parts.join(' · ');
                    const left = u.valToPos(x, 'x');
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 140) + 'px';
                    tooltip.style.top = '10px';
                }
            }
        };
    }

    const opts = {
        width: container.clientWidth || 600,
        height: options.height || 350,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] },
            size: { range: [0, null] }
        },
        axes: [
            {
                stroke: textColor,
                grid: { stroke: borderColor, width: 1 },
                ticks: { stroke: borderColor },
                values: timeAxisValues,
                size: timeAxisSize,
                font: '11px system-ui'
            },
            {
                stroke: countColor,
                grid: { stroke: borderColor, width: 1, dash: [4, 4] },
                ticks: { stroke: borderColor },
                size: 50,
                font: '11px system-ui',
                side: 3,
                label: 'Count',
                labelSize: 14,
                labelFont: '11px system-ui'
            },
            {
                scale: 'size',
                stroke: sizeColor,
                grid: { show: false },
                ticks: { stroke: borderColor },
                size: 60,
                font: '11px system-ui',
                side: 1,
                label: 'Size',
                labelSize: 14,
                labelFont: '11px system-ui',
                values: (u, vals) => vals.map(v => fmtBytesShort(v))
            }
        ],
        series: [
            {},
            { label: 'Count', scale: 'y', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null },
            { label: 'Size', scale: 'size', stroke: 'transparent', fill: 'transparent', points: { show: false }, paths: () => null }
        ],
        plugins: [tempFilesTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0];
                const countY = u.data[1];
                const sizeY = u.data[2];
                const bothVisible = u._seriesVisible.count && u._seriesVisible.size;
                const totalBarWidth = Math.max(6, (u.bbox.width / xd.length) * 0.7);
                const barWidth = bothVisible ? totalBarWidth / 2 - 1 : totalBarWidth;
                const radius = Math.min(3, barWidth / 4);

                // Draw median line
                if (u._seriesVisible.count) {
                    const currentMedian = u._medianCount || 0;
                    if (currentMedian > 0) {
                        const yMed = u.valToPos(currentMedian, 'y', true);
                        const { left, width } = u.bbox;
                        ctx.strokeStyle = mutedColor;
                        ctx.lineWidth = 1;
                        ctx.setLineDash([4, 4]);
                        ctx.beginPath();
                        ctx.moveTo(left, yMed);
                        ctx.lineTo(left + width, yMed);
                        ctx.stroke();
                        ctx.setLineDash([]);
                    }
                }

                for (let i = 0; i < xd.length; i++) {
                    const xCenter = u.valToPos(xd[i], 'x', true);
                    const y0Count = u.valToPos(0, 'y', true);
                    const y0Size = u.valToPos(0, 'size', true);

                    if (u._seriesVisible.count && countY[i] > 0) {
                        const x = bothVisible ? xCenter - barWidth/2 - 0.5 : xCenter;
                        const y = u.valToPos(countY[i], 'y', true);
                        const h = y0Count - y;
                        if (h > 0) {
                            ctx.fillStyle = countColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Count);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Count);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }

                    if (u._seriesVisible.size && sizeY[i] > 0) {
                        const x = bothVisible ? xCenter + barWidth/2 + 0.5 : xCenter;
                        const y = u.valToPos(sizeY[i], 'size', true);
                        const h = y0Size - y;
                        if (h > 0) {
                            ctx.fillStyle = sizeColor;
                            ctx.beginPath();
                            ctx.moveTo(x - barWidth/2, y0Size);
                            ctx.lineTo(x - barWidth/2, y + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                            ctx.lineTo(x + barWidth/2 - radius, y);
                            ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                            ctx.lineTo(x + barWidth/2, y0Size);
                            ctx.closePath();
                            ctx.fill();
                        }
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                if (u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin != null && newMax != null && (newMax - newMin) > 1) {
                    const { xData: newX, countData: newCount, sizeData: newSize, medianCount: newMed } = binTempFilesData(u._events, newMin, newMax, u._interval);
                    u._medianCount = newMed;
                    u._resampling = true;
                    u.setData([newX, newCount, newSize], false);
                    u._resampling = false;
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, countData, sizeData], container);

    chart._events = parsedEvents;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._medianCount = medianCount;
    chart._seriesVisible = seriesVisible;
    return chart;
}

// Create large concurrent chart for modal
export function createConcurrentChartLarge(container, sessions, options = {}) {
    const logStartT = options.logStart ? new Date(options.logStart).getTime() : null;
    const events = [];
    sessions.forEach(s => {
        const start = new Date(s.s).getTime();
        const end = new Date(s.e).getTime();
        if (!isNaN(start) && !isNaN(end)) {
            const pre = logStartT ? (start < logStartT) : false;
            events.push({ time: start, delta: 1, pre });
            events.push({ time: end, delta: -1, pre });
        }
    });
    if (events.length === 0) return null;

    events.sort((a, b) => a.time - b.time || b.delta - a.delta);

    const minT = events[0].time / 1000;
    const maxT = events[events.length - 1].time / 1000;
    const interval = options.interval !== undefined ? options.interval : modalInterval;

    // Initial binning
    const { xData, yData, yPre, median } = binConcurrentSessions(events, minT, maxT, interval);

    // Resolve CSS variable to actual color for canvas
    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
        }
        return c || '#5a9bd5';
    };
    const baseColor = resolveColor(options.color) || resolveColor('var(--accent)');
    const textColor = resolveColor('var(--text)');
    const borderColor = resolveColor('var(--border)');
    const height = options.height || 350;

    // Tooltip for sessions (modal) - no _resampling check
    function sessionsTooltipPlugin() {
        let tooltip = null;
        return {
            hooks: {
                init: u => {
                    tooltip = document.createElement('div');
                    tooltip.className = 'chart-tooltip';
                    tooltip.style.display = 'none';
                    u.over.appendChild(tooltip);
                },
                setCursor: u => {
                    const { idx } = u.cursor;
                    if (idx == null) { tooltip.style.display = 'none'; return; }
                    const data0 = u.data?.[0];
                    const data1 = u.data?.[1];
                    if (!data0 || !data1 || idx < 0 || idx >= data0.length) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const x = data0[idx];
                    const y = data1[idx];
                    if (!Number.isFinite(x) || !Number.isFinite(y)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const left = u.valToPos(x, 'x');
                    const top = u.valToPos(y, 'y');
                    if (!Number.isFinite(left) || !Number.isFinite(top)) {
                        tooltip.style.display = 'none';
                        return;
                    }
                    const d = new Date(x * 1000);
                    const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    tooltip.innerHTML = `${timeStr} · ${Math.round(y)} sessions`;
                    tooltip.style.display = 'block';
                    tooltip.style.left = Math.min(left, u.over.clientWidth - 100) + 'px';
                    tooltip.style.top = Math.max(0, top - 40) + 'px';
                }
            }
        };
    }

    // Add padding to prevent bars from being cut off
    const xPadding = (xData[xData.length - 1] - xData[0]) / (xData.length * 2);
    const xMin = xData[0] - xPadding;
    const xMax = xData[xData.length - 1] + xPadding;

    const multiDay = (maxT - minT) > 86400;
    const opts = {
        width: container.clientWidth || 1100,
        height: height,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] }
        },
        axes: [
            {
                stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: multiDay ? 60 : 50, font: '12px sans-serif', ticks: { stroke: borderColor },
                values: (u, vals) => vals.map((v, i) => {
                    const d = new Date(v * 1000);
                    const time = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                    if (!multiDay) return time;
                    const prevDay = i > 0 ? new Date(vals[i-1] * 1000).getDate() : -1;
                    if (i === 0 || d.getDate() !== prevDay) {
                        return time + '\n' + d.toLocaleDateString('en-US', { day: 'numeric', month: 'short' });
                    }
                    return time;
                })
            },
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } }
        ],
        series: [
            {},
            {
                fill: 'transparent',
                stroke: 'transparent',
                width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [sessionsTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0], yd = u.data[1];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(4, barWidth / 3);

                // Draw median line first (behind bars)
                const currentMedian = u._median || 0;
                if (currentMedian > 0) {
                    const yMed = u.valToPos(currentMedian, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = textColor;
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, yMed);
                    ctx.lineTo(left + width, yMed);
                    ctx.stroke();
                    ctx.setLineDash([]);
                }

                // Draw bars (stacked: grey pre-log, orange new)
                const preData = u._yPre;
                const preColor = getComputedStyle(document.documentElement).getPropertyValue('--text-muted')?.trim() || '#999';
                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const total = yd[i];
                    const pre = preData ? preData[i] : 0;
                    const newVal = total - pre;

                    if (pre > 0) {
                        const yPre = u.valToPos(pre, 'y', true);
                        ctx.fillStyle = preColor;
                        ctx.beginPath();
                        if (newVal > 0) {
                            ctx.rect(x - barWidth/2, yPre, barWidth, y0 - yPre);
                        } else {
                            ctx.moveTo(x - barWidth/2, y0);
                            ctx.lineTo(x - barWidth/2, yPre + radius);
                            ctx.quadraticCurveTo(x - barWidth/2, yPre, x - barWidth/2 + radius, yPre);
                            ctx.lineTo(x + barWidth/2 - radius, yPre);
                            ctx.quadraticCurveTo(x + barWidth/2, yPre, x + barWidth/2, yPre + radius);
                            ctx.lineTo(x + barWidth/2, y0);
                            ctx.closePath();
                        }
                        ctx.fill();
                    }

                    if (newVal > 0) {
                        const yTop = u.valToPos(total, 'y', true);
                        const yBottom = pre > 0 ? u.valToPos(pre, 'y', true) : y0;
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, yBottom);
                        ctx.lineTo(x - barWidth/2, yTop + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, yTop, x - barWidth/2 + radius, yTop);
                        ctx.lineTo(x + barWidth/2 - radius, yTop);
                        ctx.quadraticCurveTo(x + barWidth/2, yTop, x + barWidth/2, yTop + radius);
                        ctx.lineTo(x + barWidth/2, yBottom);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
                ctx.restore();
            }],
            setScale: [u => {
                // Re-sample on zoom
                if (!u._events || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, yData: newY, yPre: newYPre, median: newMedian } = binConcurrentSessions(u._events, newMin, newMax, u._interval);
                    u._median = newMedian;
                    u._yPre = newYPre;
                    u._resampling = true;
                    u.setData([newX, newY], false);
                    u._resampling = false;
                    // Force batch/commit cycle to reset internal state
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);

    // Store data for re-sampling
    chart._events = events;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });
    chart._median = median;
    chart._yPre = yPre;
    if (options.logStart) {
        const t = new Date(options.logStart).getTime() / 1000;
        if (!isNaN(t)) chart._logStart = t;
    }
    if (options.logEnd) {
        const t = new Date(options.logEnd).getTime() / 1000;
        if (!isNaN(t)) chart._logEnd = t;
    }
    return chart;
}

// Create large histogram chart for modal (pre-computed data)
export function createHistogramChartLarge(container, histData, options = {}) {
    if (!histData?.length) return null;

    const xData = new Float64Array(histData.length);
    const yData = new Float64Array(histData.length);
    for (let i = 0; i < histData.length; i++) {
        xData[i] = new Date(histData[i].time).getTime() / 1000;
        yData[i] = histData[i].value;
    }

    // Calculate median and max for styling
    const sortedY = [...yData].filter(v => v > 0).sort((a, b) => a - b);
    const median = sortedY.length > 0 ? sortedY[Math.floor(sortedY.length / 2)] : 0;
    const maxY = Math.max(...yData) || 1;

    // Resolve CSS variable to actual color for canvas
    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
        }
        return c || '#5a9bd5';
    };
    const baseColor = resolveColor(options.color) || resolveColor('var(--chart-bar)');
    const textColor = resolveColor('var(--text)');
    const borderColor = resolveColor('var(--border)');
    const height = options.height || 350;

    // Add padding to prevent bars from being cut off
    const xPadding = (xData[xData.length - 1] - xData[0]) / (xData.length * 2);
    const xMin = xData[0] - xPadding;
    const xMax = xData[xData.length - 1] + xPadding;

    const opts = {
        width: container.clientWidth || 1100,
        height: height,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: [0, null] }
        },
        axes: [
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } },
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } }
        ],
        series: [
            {},
            {
                fill: 'transparent',
                stroke: 'transparent',
                width: 0,
                points: { show: false },
                paths: () => null
            }
        ],
        plugins: [tooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                ctx.save();
                const xd = u.data[0], yd = u.data[1];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(4, barWidth / 3);

                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    const y = u.valToPos(yd[i], 'y', true);
                    const y0 = u.valToPos(0, 'y', true);
                    const h = y0 - y;
                    if (h > 0) {
                        ctx.fillStyle = baseColor;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, y0);
                        ctx.lineTo(x - barWidth/2, y + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, y, x - barWidth/2 + radius, y);
                        ctx.lineTo(x + barWidth/2 - radius, y);
                        ctx.quadraticCurveTo(x + barWidth/2, y, x + barWidth/2, y + radius);
                        ctx.lineTo(x + barWidth/2, y0);
                        ctx.closePath();
                        ctx.fill();
                    }
                }

                if (median > 0) {
                    const y = u.valToPos(median, 'y', true);
                    const { left, width } = u.bbox;
                    ctx.strokeStyle = textColor;
                    ctx.lineWidth = 1;
                    ctx.setLineDash([4, 4]);
                    ctx.beginPath();
                    ctx.moveTo(left, y);
                    ctx.lineTo(left + width, y);
                    ctx.stroke();
                }
                ctx.restore();
            }]
        }
    };

    const chart = new uPlot(opts, [xData, yData], container);
    chart._originalXRange = [xMin, xMax];
    chart._lastRange = null;
    chart.setScale('x', { min: xMin, max: xMax });
    chart._median = median;
    return chart;
}

// Create large stacked checkpoint chart for modal
export function createCheckpointChartLarge(container, data, options = {}) {
    if (!data?.all || data.all.length === 0) return null;

    const height = options.height || 350;

    // Parse all timestamps for range calculation
    const allTimes = data.all.map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)).sort((a, b) => a - b);
    if (allTimes.length === 0) return null;

    // Parse type-specific timestamps
    const typeData = {
        time: (data.types?.time || []).map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)),
        wal: (data.types?.wal || []).map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t)),
        other: (data.types?.other || []).map(t => new Date(t).getTime() / 1000).filter(t => !isNaN(t))
    };

    const minT = allTimes[0];
    const maxT = allTimes[allTimes.length - 1];
    const interval = options.interval !== undefined ? options.interval : modalInterval;

    // Initial binning
    const { xData, series } = binCheckpointsByType(typeData, minT, maxT, interval);

    // Resolve CSS variable to actual color for canvas
    const resolveColor = (c) => {
        if (c && c.startsWith('var(')) {
            const varName = c.slice(4, -1);
            return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
        }
        return c || '#5a9bd5';
    };

    // Colors for checkpoint types
    const colors = {
        time: resolveColor('var(--chart-bar)'),
        wal: getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#f47920',
        other: '#909399' // gray
    };
    const textColor = resolveColor('var(--text)');
    const borderColor = resolveColor('var(--border)');

    // Tooltip for modal
    const tooltip = document.createElement('div');
    tooltip.className = 'chart-tooltip';
    tooltip.style.cssText = 'position:absolute;display:none;padding:6px 10px;background:var(--bg);border:1px solid var(--border);border-radius:4px;font-size:12px;pointer-events:none;z-index:100;white-space:nowrap;box-shadow:0 2px 8px rgba(0,0,0,0.15);';

    const checkpointTooltipPlugin = () => ({
        hooks: {
            init: u => {
                u.root.querySelector('.u-over').appendChild(tooltip);
            },
            setCursor: u => {
                if (u._resampling) { tooltip.style.display = 'none'; return; }
                const { idx, left, top } = u.cursor;
                const data0 = u.data[0];
                if (idx == null || !data0 || idx < 0 || idx >= data0.length) {
                    tooltip.style.display = 'none';
                    return;
                }
                const x = data0[idx];
                if (x === undefined || !Number.isFinite(x)) {
                    tooltip.style.display = 'none';
                    return;
                }
                const timeVal = u.data[1][idx] || 0;
                const xlogVal = u.data[2][idx] || 0;
                const otherVal = u.data[3][idx] || 0;
                const total = timeVal + xlogVal + otherVal;
                if (total === 0) {
                    tooltip.style.display = 'none';
                    return;
                }
                const d = new Date(x * 1000);
                const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                let parts = [];
                if (timeVal > 0) parts.push(`<span style="color:${colors.time}">${timeVal} timed</span>`);
                if (xlogVal > 0) parts.push(`<span style="color:${colors.wal}">${xlogVal} WAL</span>`);
                const otherLabel = data.warningsOnly ? 'warning' : 'other';
                if (otherVal > 0) parts.push(`<span style="color:${colors.other}">${otherVal} ${otherLabel}</span>`);
                tooltip.innerHTML = `<strong>${timeStr}</strong><br>${parts.join(' · ')}`;
                tooltip.style.display = 'block';
                const ttWidth = tooltip.offsetWidth;
                const ttHeight = tooltip.offsetHeight;
                const chartWidth = u.bbox.width;
                let ttLeft = left - ttWidth / 2;
                if (ttLeft < 0) ttLeft = 0;
                if (ttLeft + ttWidth > chartWidth) ttLeft = chartWidth - ttWidth;
                tooltip.style.left = ttLeft + 'px';
                tooltip.style.top = (top - ttHeight - 10) + 'px';
            }
        }
    });

    // Add padding to prevent bars from being cut off
    const xPadding = (xData[xData.length - 1] - xData[0]) / (xData.length * 2);
    const xMin = xData[0] - xPadding;
    const xMax = xData[xData.length - 1] + xPadding;

    const opts = {
        width: container.clientWidth || 1100,
        height: height,
        cursor: { drag: { x: true, y: false, setScale: true }, bind: { dblclick: () => null } },
        select: { show: true },
        legend: { show: false },
        scales: {
            x: { time: true },
            y: { range: (u, min, max) => [0, Math.max(Math.ceil(max), 2)] }
        },
        axes: [
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor } },
            { stroke: textColor, grid: { stroke: borderColor, width: 1 }, size: 50, font: '12px sans-serif', ticks: { stroke: borderColor }, incrs: [1, 2, 5, 10, 20, 50, 100], values: (u, vals) => vals.map(v => Number.isInteger(v) ? v : '') }
        ],
        series: [
            {},
            { fill: 'transparent', stroke: 'transparent', width: 0, points: { show: false }, paths: () => null },
            { fill: 'transparent', stroke: 'transparent', width: 0, points: { show: false }, paths: () => null },
            { fill: 'transparent', stroke: 'transparent', width: 0, points: { show: false }, paths: () => null }
        ],
        plugins: [checkpointTooltipPlugin()],
        hooks: {
            draw: [u => {
                const ctx = u.ctx;
                const xd = u.data[0];
                const timeSeries = u.data[1];
                const xlogSeries = u.data[2];
                const otherSeries = u.data[3];
                const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.75);
                const radius = Math.min(4, barWidth / 3);
                const y0 = u.valToPos(0, 'y', true);

                // Draw stacked bars: other (bottom), xlog (middle), time (top)
                for (let i = 0; i < xd.length; i++) {
                    const x = u.valToPos(xd[i], 'x', true);
                    let yBottom = y0;

                    // Draw other (bottom)
                    const otherVal = otherSeries[i] || 0;
                    if (otherVal > 0) {
                        const yTop = u.valToPos(otherVal, 'y', true);
                        const h = yBottom - yTop;
                        ctx.fillStyle = colors.other;
                        ctx.fillRect(x - barWidth/2, yTop, barWidth, h);
                        yBottom = yTop;
                    }

                    // Draw xlog (middle)
                    const xlogVal = xlogSeries[i] || 0;
                    if (xlogVal > 0) {
                        const yTop = yBottom - (y0 - u.valToPos(xlogVal, 'y', true));
                        const h = yBottom - yTop;
                        ctx.fillStyle = colors.wal;
                        ctx.fillRect(x - barWidth/2, yTop, barWidth, h);
                        yBottom = yTop;
                    }

                    // Draw time (top) with rounded corners
                    const timeVal = timeSeries[i] || 0;
                    if (timeVal > 0) {
                        const yTop = yBottom - (y0 - u.valToPos(timeVal, 'y', true));
                        ctx.fillStyle = colors.time;
                        ctx.beginPath();
                        ctx.moveTo(x - barWidth/2, yBottom);
                        ctx.lineTo(x - barWidth/2, yTop + radius);
                        ctx.quadraticCurveTo(x - barWidth/2, yTop, x - barWidth/2 + radius, yTop);
                        ctx.lineTo(x + barWidth/2 - radius, yTop);
                        ctx.quadraticCurveTo(x + barWidth/2, yTop, x + barWidth/2, yTop + radius);
                        ctx.lineTo(x + barWidth/2, yBottom);
                        ctx.closePath();
                        ctx.fill();
                    }
                }
            }],
            setScale: [u => {
                if (!u._typeData || u._resampling) return;
                const xScale = u.scales.x;
                const newMin = xScale.min;
                const newMax = xScale.max;
                if (newMin == null || newMax == null) return;

                const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;
                if (rangeChanged) {
                    u._lastRange = [newMin, newMax];
                    const { xData: newX, series: newSeries } = binCheckpointsByType(u._typeData, newMin, newMax, u._interval);
                    u._resampling = true;
                    u.setData([newX, newSeries.time, newSeries.wal, newSeries.other], false);
                    u._resampling = false;
                    // Force batch/commit cycle to reset cursor state
                    u.batch(() => {
                        u.setScale('x', { min: newMin, max: newMax });
                    });
                }
            }]
        }
    };

    const chart = new uPlot(opts, [xData, series.time, series.wal, series.other], container);

    // Store data for re-sampling
    chart._typeData = typeData;
    chart._interval = interval;
    chart._originalXRange = [minT, maxT];
    chart._lastRange = null;
    chart.setScale('x', { min: minT, max: maxT });

    return chart;
}

// Close modal
export function closeChartModal() {
    document.getElementById('chartModal').classList.remove('active');
    document.body.style.overflow = '';
    // Undo cost-map-specific modal tweaks so the next chart opens normally.
    const content = document.querySelector('#chartModal .chart-modal-content');
    if (content) content.classList.remove('cm-square');
    const sel = document.getElementById('modalBucketSelect');
    if (sel) sel.style.display = '';
    if (modalChart) {
        modalChart.destroy();
        modalChart = null;
    }
    modalChartId = null;
}

// Update modal interval
export function updateModalInterval(intervalValue) {
    modalInterval = parseInt(intervalValue);
    renderModalChart();
}

// Reset modal zoom and re-sample to original range
export function resetModalZoom() {
    if (modalChart && modalChart._cmXRange) {
        // Cost map: reset both log axes to the auto-fit domain.
        modalChart.setScale('x', { min: modalChart._cmXRange[0], max: modalChart._cmXRange[1] });
        modalChart.setScale('y', { min: modalChart._cmYRange[0], max: modalChart._cmYRange[1] });
        return;
    }
    if (modalChart && modalChart._originalXRange) {
        const [min, max] = modalChart._originalXRange;
        // Clear lastRange to force re-sampling
        modalChart._lastRange = null;
        modalChart.setScale('x', { min, max });
    }
}

// Open the cost map in the shared chart modal, sized square (the plot is
// square, so a square window wastes no space). Sets modalChart so the modal's
// Reset/Export/close act on it.
export function openCostMapModal(srcId, title) {
    const data = chartData.get(srcId);
    if (!data) return;
    document.getElementById('modalChartTitle').textContent = title;
    const sel = document.getElementById('modalBucketSelect');
    if (sel) sel.style.display = 'none';
    const content = document.querySelector('#chartModal .chart-modal-content');
    if (content) content.classList.add('cm-square');
    document.getElementById('chartModal').classList.add('active');
    document.body.style.overflow = 'hidden';
    setTimeout(() => {
        const c = document.getElementById('modal-chart-container');
        c.innerHTML = '';
        // Leave room for the modal header + paddings (~200px) so the chart's
        // own bottom axis labels stay inside the body instead of being clipped.
        const side = Math.min(c.clientWidth || 480, Math.round(window.innerHeight - 200));
        modalChart = createCostMapChart('modal-chart-container', data.queries, { height: side });
    }, 50);
}

// Export chart as PNG (for modal chart)
export function exportChartPNG() {
    if (!modalChart) return;
    exportChartToPNG(modalChart, document.getElementById('modalChartTitle').textContent);
}

// Export any chart by ID
export function exportChartById(chartId, title) {
    const chart = charts.get(chartId);
    if (!chart) return;
    exportChartToPNG(chart, title);
}

// Common PNG export logic
function exportChartToPNG(chart, title) {
    const canvas = chart.root.querySelector('canvas');
    if (!canvas) return;
    const padding = 20;
    const titleHeight = 45;
    const bottomPadding = 40;

    // Create a new canvas with title on top, watermark at bottom
    const exportCanvas = document.createElement('canvas');
    const ctx = exportCanvas.getContext('2d');
    exportCanvas.width = canvas.width + padding * 2;
    exportCanvas.height = canvas.height + titleHeight + padding + bottomPadding;

    // Fill background based on theme
    const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
    ctx.fillStyle = isDark ? '#21262d' : '#ffffff';
    ctx.fillRect(0, 0, exportCanvas.width, exportCanvas.height);

    // Draw title (top center)
    ctx.fillStyle = isDark ? '#e6edf3' : '#1f2328';
    ctx.font = 'bold 18px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(title, exportCanvas.width / 2, padding + 25);

    // Draw the chart
    ctx.drawImage(canvas, padding, titleHeight + padding);

    // Watermark (bottom right)
    ctx.fillStyle = isDark ? '#8b949e' : '#656d76';
    ctx.font = '600 16px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
    ctx.textAlign = 'right';
    ctx.fillText('quellog', exportCanvas.width - padding, exportCanvas.height - 15);

    // Download
    const link = document.createElement('a');
    const filename = title.replace(/[^a-z0-9]/gi, '_');
    link.download = `quellog_${filename}_${new Date().toISOString().slice(0,10)}.png`;
    link.href = exportCanvas.toDataURL('image/png');
    link.click();
}

// Keyboard handler for modal
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && document.getElementById('chartModal').classList.contains('active')) {
        closeChartModal();
    }
});
