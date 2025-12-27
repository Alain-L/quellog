// Initialize WASM - cache compiled module for memory-safe reloading
        let wasmModule = null;  // Compiled WebAssembly.Module (reusable)
        let wasmReady = false;
        let analysisData = null;
        let currentFileContent = null;  // Keep file content for re-filtering
        let currentFileName = null;
        let currentFileSize = 0;
        let originalDimensions = null;  // Keep original dimensions for filter chips

        // Chart management
        const charts = new Map();  // Store chart instances by ID
        const modalCharts = [];    // Store modal chart instances
        const modalChartsData = new Map();  // Pending modal charts data
        let modalChartCounter = 0;
        const chartIntervalMap = new Map();  // Per-chart interval in seconds (0 = auto)
        const defaultInterval = 0;  // Auto

        // Compute optimal interval based on time range
        function computeAutoInterval(rangeSeconds) {
            if (rangeSeconds < 3600) return 60;         // < 1h → 1 min
            if (rangeSeconds < 6 * 3600) return 300;    // < 6h → 5 min
            if (rangeSeconds < 24 * 3600) return 900;   // < 24h → 15 min
            return 3600;                                 // >= 24h → 1h
        }

        // Compute bucket count from interval and range
        function computeBuckets(rangeSeconds, intervalSeconds) {
            if (intervalSeconds === 0) {
                intervalSeconds = computeAutoInterval(rangeSeconds);
            }
            const buckets = Math.max(5, Math.ceil(rangeSeconds / intervalSeconds));
            return Math.min(buckets, 200);  // Cap at 200 buckets max
        }

        // Format interval for display
        function formatInterval(seconds) {
            if (seconds === 0) return 'Auto';
            if (seconds < 60) return seconds + 's';
            if (seconds < 3600) return (seconds / 60) + ' min';
            return (seconds / 3600) + 'h';
        }

        // Global tooltip plugin for uPlot charts
        function tooltipPlugin() {
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
        function binTimestamps(times, minT, maxT, interval) {
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
        function binDurations(executions, minT, maxT, interval) {
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
        function binCombinedData(times, executions, minT, maxT, interval) {
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

        // Helper: bin concurrent sessions using sweep-line algorithm
        function binConcurrentSessions(events, minT, maxT, interval) {
            const range = maxT - minT || 1;
            const buckets = computeBuckets(range, interval);
            const bucketSize = range / buckets;

            const xData = new Float64Array(buckets);
            const yData = new Float64Array(buckets);
            for (let i = 0; i < buckets; i++) {
                xData[i] = minT + i * bucketSize + bucketSize / 2;
            }

            // Filter and find starting concurrent count before minT
            let current = 0;
            let eventIdx = 0;

            // Count events before minT to get starting concurrent count
            while (eventIdx < events.length && events[eventIdx].time / 1000 < minT) {
                current += events[eventIdx].delta;
                eventIdx++;
            }

            // Sweep-line for visible range
            for (let b = 0; b < buckets; b++) {
                const bucketStart = minT + b * bucketSize;
                const bucketEnd = minT + (b + 1) * bucketSize;
                let maxInBucket = current;

                while (eventIdx < events.length && events[eventIdx].time / 1000 < bucketEnd) {
                    const e = events[eventIdx];
                    const t = e.time / 1000;
                    if (t >= bucketStart) {
                        current += e.delta;
                        if (current > maxInBucket) maxInBucket = current;
                    } else {
                        current += e.delta;
                    }
                    eventIdx++;
                }
                yData[b] = Math.max(maxInBucket, 0);
            }

            const sortedY = [...yData].filter(v => v > 0).sort((a, b) => a - b);
            const median = sortedY.length > 0 ? sortedY[Math.floor(sortedY.length / 2)] : 0;

            return { xData, yData, median, buckets };
        }

        // Helper: bin checkpoints by type (for stacked bar chart)
        function binCheckpointsByType(typeData, minT, maxT, interval) {
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
        function createCheckpointChart(containerId, data, options = {}) {
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
                        if (otherVal > 0) parts.push(`<span style="color:${colors.other}">${otherVal} other</span>`);
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
                        values: (u, vals) => vals.map(v => {
                            const d = new Date(v * 1000);
                            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        }),
                        size: 20,
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

            // Store data for re-sampling
            chart._typeData = typeData;
            chart._interval = interval;
            chart._originalXRange = [minT, maxT];

            // Handle resize
            const resizeObserver = new ResizeObserver(() => {
                if (container.clientWidth > 0) {
                    chart.setSize({ width: container.clientWidth, height: opts.height });
                }
            });
            resizeObserver.observe(container);

            return chart;
        }

        // Create interactive time chart with uPlot
        function createTimeChart(containerId, timestamps, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
                        values: (u, vals) => vals.map(v => {
                            const d = new Date(v * 1000);
                            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        }),
                        size: 20,
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

            // Store data for re-sampling
            chart._times = times;
            chart._interval = interval;
            chart._originalXRange = [minT, maxT];
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
        function createDurationChart(containerId, executions, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
                        values: (u, vals) => vals.map(v => {
                            const d = new Date(v * 1000);
                            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        }),
                        size: 20,
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

            chart._executions = sorted;
            chart._interval = interval;
            chart._originalXRange = [minT, maxT];
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
        function createCombinedSQLChart(containerId, rawData, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
                        values: (u, vals) => vals.map(v => {
                            const d = new Date(v * 1000);
                            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        }),
                        size: 20,
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

            chart._rawData = rawData;
            chart._interval = interval;
            chart._originalXRange = [minT, maxT];
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
        function toggleCombinedSeries(chartId, series) {
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
        function createHistogramChart(containerId, histogram, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
                        values: (u, vals) => vals.map(v => {
                            const d = new Date(v * 1000);
                            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        }),
                        size: 20,
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

                        // Draw bars
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
                    }]
                }
            };

            const chart = new uPlot(opts, [xData, yData], container);
            charts.set(containerId, chart);

            // Store original range for reset
            chart._originalXRange = [xData[0], xData[xData.length - 1]];
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
        function createConcurrentChart(containerId, sessions, options = {}) {
            const container = document.getElementById(containerId);
            if (!container || !sessions || sessions.length === 0) return null;

            // Clear previous chart
            if (charts.has(containerId)) {
                charts.get(containerId).destroy();
                charts.delete(containerId);
            }
            container.innerHTML = '';

            // Parse session events first to get time range
            const events = [];
            sessions.forEach(s => {
                const start = new Date(s.s).getTime();
                const end = new Date(s.e).getTime();
                if (!isNaN(start) && !isNaN(end)) {
                    events.push({ time: start, delta: 1 });  // +1 at start
                    events.push({ time: end, delta: -1 });   // -1 at end
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
            const { xData, yData, median } = binConcurrentSessions(events, minT, maxT, interval);

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
                cursor: { drag: { x: true, y: false, setScale: true } },
                legend: { show: false },
                scales: { x: { time: true }, y: { range: [0, null] } },
                axes: [
                    {
                        stroke: getComputedStyle(document.documentElement).getPropertyValue('--text').trim(),
                        grid: { show: false }, ticks: { show: false },
                        values: (u, vals) => vals.map(v => new Date(v * 1000).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })),
                        size: 20, font: '10px system-ui'
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

                        // Draw bars
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
                        // Re-sample on zoom
                        if (!u._events || u._resampling) return;
                        const xScale = u.scales.x;
                        const newMin = xScale.min;
                        const newMax = xScale.max;
                        if (newMin == null || newMax == null) return;

                        const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                        if (rangeChanged) {
                            u._lastRange = [newMin, newMax];
                            const { xData: newX, yData: newY, median: newMedian } = binConcurrentSessions(u._events, newMin, newMax, u._interval);
                            u._median = newMedian;
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

            // Store data for re-sampling
            chart._events = events;
            chart._interval = interval;
            chart._originalXRange = [minT, maxT];
            chart._median = median;

            const resizeObserver = new ResizeObserver(() => {
                if (container.clientWidth > 0) chart.setSize({ width: container.clientWidth, height: opts.height });
            });
            resizeObserver.observe(container);

            return chart;
        }

        // Build chart container HTML with controls
        function buildChartContainer(id, title, options = {}) {
            const showIntervalControl = options.showBucketControl !== false;
            const currentInterval = chartIntervalMap.get(id) ?? defaultInterval;
            const tooltip = options.tooltip || '';
            const infoIcon = tooltip ? `<span class="info-icon">i<span class="info-tooltip">${tooltip}</span></span>` : '';
            return `
                <div class="chart-container">
                    <div class="chart-controls">
                        <span class="subsection-title" style="margin: 0; font-size: 0.7rem;">${title} ${infoIcon}</span>
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
                            <button class="btn-expand" onclick="openChartModal('${id}', '${title.replace(/'/g, "\\'")}')" title="Expand chart" aria-label="Expand chart">⛶</button>
                        </div>
                    </div>
                    <div id="${id}" style="min-height: 120px;"></div>
                </div>
            `;
        }

        // Update chart with new interval
        function updateChartInterval(chartId, intervalValue) {
            const interval = parseInt(intervalValue);
            chartIntervalMap.set(chartId, interval);

            // Recreate only this specific chart
            const data = chartData.get(chartId);
            if (data) {
                const accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
                const color = chartId.includes('tempfiles') ? accentColor : null;
                if (data?.type === 'sessions') {
                    createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)', interval });
                } else if (data?.type === 'histogram') {
                    // Pre-computed histogram can't change interval
                } else if (data?.type === 'duration') {
                    createDurationChart(chartId, data.data, { color: accentColor, interval });
                } else if (data?.type === 'combined') {
                    createCombinedSQLChart(chartId, data.data, { interval });
                } else {
                    createTimeChart(chartId, data, { color, interval });
                }
            }
        }

        // Reset chart zoom and re-sample to original range
        function resetChartZoom(chartId) {
            const chart = charts.get(chartId);
            if (chart && chart._originalXRange) {
                const [min, max] = chart._originalXRange;
                // Clear lastRange to force re-sampling
                chart._lastRange = null;
                chart.setScale('x', { min, max });
            }
        }

        // Store chart data for re-creation
        const chartData = new Map();

        // Modal state
        let modalChart = null;
        let modalChartId = null;
        let modalInterval = 0;  // 0 = Auto

        // Open chart in modal
        function openChartModal(chartId, title) {
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
            intervalSelect.style.display = data?.type === 'histogram' ? 'none' : '';

            // Create expanded chart
            setTimeout(() => renderModalChart(), 50);
        }

        // Render chart in modal
        function renderModalChart() {
            const container = document.getElementById('modal-chart-container');
            container.innerHTML = '';

            const data = chartData.get(modalChartId);
            if (!data) return;

            const accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
            const color = modalChartId.includes('tempfiles') ? accentColor : null;

            // Create larger chart
            if (data?.type === 'checkpoints') {
                modalChart = createCheckpointChartLarge(container, data, {
                    interval: modalInterval,
                    height: 350
                });
            } else if (data?.type === 'sessions') {
                modalChart = createConcurrentChartLarge(container, data.data, {
                    color: color || 'var(--accent)',
                    interval: modalInterval,
                    height: 350
                });
            } else if (data?.type === 'histogram') {
                modalChart = createHistogramChartLarge(container, data.data, {
                    color: color || 'var(--accent)',
                    height: 350
                });
            } else if (data?.type === 'duration') {
                modalChart = createDurationChartLarge(container, data.data, {
                    color: accentColor,
                    interval: modalInterval,
                    height: 350
                });
            } else if (data?.type === 'combined') {
                modalChart = createCombinedSQLChartLarge(container, data.data, {
                    interval: modalInterval,
                    height: 350
                });
            } else {
                modalChart = createTimeChartLarge(container, data, {
                    color,
                    interval: modalInterval,
                    height: 350
                });
            }
        }

        // Create large time chart for modal
        function createTimeChartLarge(container, timestamps, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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

                        // Draw bars
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
            chart._median = median;
            return chart;
        }

        // Create large duration chart for modal
        function createDurationChartLarge(container, executions, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
            chart._median = median;
            return chart;
        }

        // Create large combined SQL chart for modal (grouped bars)
        function createCombinedSQLChartLarge(container, rawData, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
            chart._medianCount = medianCount;
            chart._seriesVisible = seriesVisible;
            return chart;
        }

        // Create large concurrent chart for modal
        function createConcurrentChartLarge(container, sessions, options = {}) {
            const events = [];
            sessions.forEach(s => {
                const start = new Date(s.s).getTime();
                const end = new Date(s.e).getTime();
                if (!isNaN(start) && !isNaN(end)) {
                    events.push({ time: start, delta: 1 });
                    events.push({ time: end, delta: -1 });
                }
            });
            if (events.length === 0) return null;

            events.sort((a, b) => a.time - b.time || b.delta - a.delta);

            const minT = events[0].time / 1000;
            const maxT = events[events.length - 1].time / 1000;
            const interval = options.interval !== undefined ? options.interval : modalInterval;

            // Initial binning
            const { xData, yData, median } = binConcurrentSessions(events, minT, maxT, interval);

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

            const opts = {
                width: container.clientWidth || 1100,
                height: height,
                cursor: { drag: { x: true, y: false, setScale: true } },
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

                        // Draw bars
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
                        // Re-sample on zoom
                        if (!u._events || u._resampling) return;
                        const xScale = u.scales.x;
                        const newMin = xScale.min;
                        const newMax = xScale.max;
                        if (newMin == null || newMax == null) return;

                        const rangeChanged = !u._lastRange || Math.abs(u._lastRange[0] - newMin) > 1 || Math.abs(u._lastRange[1] - newMax) > 1;

                        if (rangeChanged) {
                            u._lastRange = [newMin, newMax];
                            const { xData: newX, yData: newY, median: newMedian } = binConcurrentSessions(u._events, newMin, newMax, u._interval);
                            u._median = newMedian;
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
            chart._median = median;
            return chart;
        }

        // Create large histogram chart for modal (pre-computed data)
        function createHistogramChartLarge(container, histData, options = {}) {
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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
            chart._median = median;
            return chart;
        }

        // Create large stacked checkpoint chart for modal
        function createCheckpointChartLarge(container, data, options = {}) {
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
                        if (otherVal > 0) parts.push(`<span style="color:${colors.other}">${otherVal} other</span>`);
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
                cursor: { drag: { x: true, y: false, setScale: true } },
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

            return chart;
        }

        // Close modal
        function closeChartModal() {
            document.getElementById('chartModal').classList.remove('active');
            document.body.style.overflow = '';
            if (modalChart) {
                modalChart.destroy();
                modalChart = null;
            }
            modalChartId = null;
        }

        // Update modal interval
        function updateModalInterval(intervalValue) {
            modalInterval = parseInt(intervalValue);
            renderModalChart();
        }

        // Reset modal zoom and re-sample to original range
        function resetModalZoom() {
            if (modalChart && modalChart._originalXRange) {
                const [min, max] = modalChart._originalXRange;
                // Clear lastRange to force re-sampling
                modalChart._lastRange = null;
                modalChart.setScale('x', { min, max });
            }
        }

        // Export chart as PNG
        function exportChartPNG() {
            if (!modalChart) return;

            const canvas = modalChart.root.querySelector('canvas');
            if (!canvas) return;

            const title = document.getElementById('modalChartTitle').textContent;
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

        // Compile and cache WASM module on startup
        fetch('quellog.wasm')
            .then(response => response.arrayBuffer())
            .then(buffer => WebAssembly.compile(buffer))
            .then(module => {
                wasmModule = module;
                return initWasmInstance();
            })
            .then(() => {
                wasmReady = true;
                console.log('quellog WASM ready:', quellogVersion());
            })
            .catch(err => {
                console.error('WASM load failed:', err);
                alert('Failed to load WASM module');
            });

        // Create fresh WASM instance (resets memory)
        async function initWasmInstance() {
            const go = new Go();
            const instance = await WebAssembly.instantiate(wasmModule, go.importObject);
            go.run(instance);
            // Wait for Go initialization
            await new Promise(r => setTimeout(r, 10));
        }

        // DOM elements
        const dropZone = document.getElementById('dropZone');
        const fileInput = document.getElementById('fileInput');
        const loading = document.getElementById('loading');
        const results = document.getElementById('results');
        const main = document.getElementById('main');

        // Drag and drop
        dropZone.addEventListener('dragover', e => { e.preventDefault(); dropZone.classList.add('drag-over'); });
        dropZone.addEventListener('dragleave', () => dropZone.classList.remove('drag-over'));
        dropZone.addEventListener('drop', e => {
            e.preventDefault();
            dropZone.classList.remove('drag-over');
            if (e.dataTransfer.files[0]) processFile(e.dataTransfer.files[0]);
        });
        fileInput.addEventListener('change', e => { if (e.target.files[0]) processFile(e.target.files[0]); });

        function setProgress(pct, text) {
            document.getElementById('progressBar').style.width = pct + '%';
            document.getElementById('loadingText').textContent = text;
        }

        // Compression/archive support
        async function gunzipBuffer(buffer) {
            const ds = new DecompressionStream('gzip');
            const writer = ds.writable.getWriter();
            writer.write(buffer);
            writer.close();
            const reader = ds.readable.getReader();
            const chunks = [];
            while (true) {
                const { done, value } = await reader.read();
                if (done) break;
                chunks.push(value);
            }
            const result = new Uint8Array(chunks.reduce((a, c) => a + c.length, 0));
            let offset = 0;
            for (const chunk of chunks) { result.set(chunk, offset); offset += chunk.length; }
            return result;
        }

        function unzstd(buffer) {
            // fzstd loaded in standalone, check if available
            if (typeof fzstd !== 'undefined') return fzstd.decompress(new Uint8Array(buffer));
            throw new Error('Zstd decompression not available');
        }

        function detectFormat(buffer) {
            const h = new Uint8Array(buffer.slice(0, 512));
            if (h[0] === 0x1f && h[1] === 0x8b) return 'gzip';
            if (h[0] === 0x28 && h[1] === 0xb5 && h[2] === 0x2f && h[3] === 0xfd) return 'zstd';
            // Tar: check for 'ustar' at offset 257
            if (h[257] === 0x75 && h[258] === 0x73 && h[259] === 0x74 && h[260] === 0x61 && h[261] === 0x72) return 'tar';
            return 'plain';
        }

        async function decompress(buffer, name) {
            let fmt = detectFormat(buffer);
            // Also check extension as fallback
            const lname = name.toLowerCase();
            if (fmt === 'plain' && (lname.endsWith('.gz') || lname.endsWith('.gzip'))) fmt = 'gzip';
            if (fmt === 'plain' && (lname.endsWith('.zst') || lname.endsWith('.zstd'))) fmt = 'zstd';

            if (fmt === 'gzip') return await gunzipBuffer(buffer);
            if (fmt === 'zstd') return unzstd(buffer);
            return new Uint8Array(buffer);
        }

        async function extractTar(buffer) {
            const data = new Uint8Array(buffer);
            const files = [];
            let offset = 0;

            while (offset + 512 <= data.length) {
                const header = data.slice(offset, offset + 512);
                // End of archive: all zeros
                if (header.every(b => b === 0)) break;

                const nameBytes = header.slice(0, 100);
                const name = new TextDecoder().decode(nameBytes).replace(/\0.*$/, '');
                const sizeStr = new TextDecoder().decode(header.slice(124, 136)).replace(/\0.*$/, '').trim();
                const size = parseInt(sizeStr, 8) || 0;
                const typeFlag = header[156];

                offset += 512;

                // Regular file (type '0' or '\0')
                if ((typeFlag === 48 || typeFlag === 0) && size > 0) {
                    let content = data.slice(offset, offset + size);
                    // Decompress nested files
                    const lname = name.toLowerCase();
                    if (lname.endsWith('.gz') || lname.endsWith('.gzip')) {
                        content = await gunzipBuffer(content.buffer);
                    } else if (lname.endsWith('.zst') || lname.endsWith('.zstd')) {
                        content = unzstd(content.buffer);
                    }
                    files.push({ name, content });
                }

                offset += Math.ceil(size / 512) * 512;
            }

            // Concatenate all file contents
            return files.map(f => new TextDecoder().decode(f.content)).join('\n');
        }

        async function prepareContent(file) {
            const buffer = await file.arrayBuffer();
            let data = await decompress(buffer, file.name);

            // Check if result is a tar archive
            const fmt = detectFormat(data.buffer);
            const lname = file.name.toLowerCase();
            if (fmt === 'tar' || lname.includes('.tar')) {
                return await extractTar(data.buffer);
            }

            return new TextDecoder().decode(data);
        }

        const MAX_FILE_SIZE = 1500 * 1024 * 1024; // 1.5GB limit for in-memory parsing

        async function processFile(file) {
            if (!wasmReady) { alert('WASM not ready'); return; }

            // Check file size limit
            if (file.size > MAX_FILE_SIZE) {
                alert(`File too large (${fmtBytes(file.size)}). Maximum size: ${fmtBytes(MAX_FILE_SIZE)}.\n\nFor larger files, use the command-line version:\n  quellog ${file.name}`);
                return;
            }

            dropZone.style.display = 'none';
            loading.classList.add('active');
            results.classList.remove('active');
            setProgress(5, 'Initializing...');

            try {
                // Reinitialize WASM to free previous memory (gc=leaking workaround)
                await initWasmInstance();

                console.log(`[quellog] Parsing: ${file.name} (${fmtBytes(file.size)})`);
                setProgress(10, 'Reading file...');

                // Handle compressed files and tar archives
                const content = await prepareContent(file);
                setProgress(50, 'Parsing log entries...');

                // Store for re-filtering
                currentFileContent = content;
                currentFileName = file.name;
                currentFileSize = file.size;
                originalDimensions = null;  // Reset for new file

                // Time the actual parsing
                const parseStart = performance.now();
                const resultJson = quellogParse(content);
                const parseEnd = performance.now();
                const parseTimeMs = Math.round(parseEnd - parseStart);

                const data = JSON.parse(resultJson);

                if (data.error) throw new Error(data.error);

                // Store parse time for display
                data._parseTimeMs = parseTimeMs;

                setProgress(90, 'Rendering...');
                analysisData = data;
                renderResults(data, currentFileName, currentFileSize);
                setProgress(100, 'Done');
                console.log(`[quellog] Complete: ${data.meta?.entries || 0} entries in ${parseTimeMs}ms`);
            } catch (err) {
                console.error('Analysis failed:', err);
                alert('Analysis failed: ' + err.message);
                dropZone.style.display = 'block';
            } finally {
                loading.classList.remove('active');
            }
        }

        function renderResults(data, fileName, fileSize, isInitial = true) {
            results.classList.add('active');

            // Clear previous chart data
            chartData.clear();
            charts.forEach(c => c.destroy());
            charts.clear();

            // Show action buttons in header
            document.getElementById('newFileBtn').style.display = 'inline-block';
            // Store file info for summary section
            currentFileInfo = {
                fileName,
                fileSize,
                format: data.meta.format,
                entries: data.meta.entries,
                parseTimeMs: data._parseTimeMs || 0
            };
            // Initialize filter bar
            initFilterBar(data, isInitial);

            // Build sections with new layout
            let html = '';

            // Row 1: Summary | Events | Error Classes | Clients (4 cols)
            html += `<div class="grid grid-top-row">`;
            html += buildSummarySection(data);
            html += buildEventsSection(data);
            html += buildClientsSection(data);
            html += '</div>';

            // Connections (full width)
            html += buildConnectionsSection(data);

            // SQL Overview (full width)
            sqlOverviewData = data.sql_overview;
            html += buildSQLOverviewSection(data);

            // SQL Performance (full width)
            if (data.sql_performance?.queries?.length > 0) {
                html += buildSQLPerformanceSection(data);
            }

            // Row 5: Locks | Temp Files
            html += '<div class="grid grid-2">';
            html += buildLocksSection(data);
            html += buildTempFilesSection(data);
            html += '</div>';

            // Row 6: Checkpoints | Maintenance
            html += '<div class="grid grid-2">';
            html += buildCheckpointsSection(data);
            html += buildMaintenanceSection(data);
            html += '</div>';

            results.innerHTML = html;

            // Create uPlot charts after DOM is ready
            requestAnimationFrame(() => {
                chartData.forEach((data, chartId) => {
                    const accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
                    const color = chartId.includes('tempfiles') ? accentColor : null;
                    // Check data type: checkpoints (stacked), sessions (sweep-line), histogram (pre-computed), duration, or timestamps
                    if (data?.type === 'checkpoints') {
                        createCheckpointChart(chartId, data);
                    } else if (data?.type === 'sessions') {
                        createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)' });
                    } else if (data?.type === 'histogram') {
                        createHistogramChart(chartId, data.data, { color: color || 'var(--accent)' });
                    } else if (data?.type === 'duration') {
                        createDurationChart(chartId, data.data, { color: accentColor });
                    } else if (data?.type === 'combined') {
                        createCombinedSQLChart(chartId, data.data);
                    } else {
                        createTimeChart(chartId, data, { color });
                    }
                });
            });

        }

        // Section builders
        function buildSummarySection(data) {
            const s = data.summary;
            const f = currentFileInfo || {};

            // Format parse time nicely
            const parseTime = f.parseTimeMs || 0;
            const parseTimeStr = parseTime < 1000 ? `${parseTime}ms` : `${(parseTime/1000).toFixed(2)}s`;

            // Format duration: h:m if >= 1h, m:s otherwise (with proper rollover)
            const formatDuration = (durStr) => {
                if (!durStr || durStr === '-') return '-';
                // Parse duration like "12h 30m 11s" or "5m 23s" or "45s"
                let h = parseInt(durStr.match(/(\d+)h/)?.[1] || 0);
                let m = parseInt(durStr.match(/(\d+)m/)?.[1] || 0);
                let sec = parseInt(durStr.match(/(\d+)s/)?.[1] || 0);
                // Rollover seconds to minutes
                if (sec >= 60) { m += Math.floor(sec / 60); sec = sec % 60; }
                if (m >= 60) { h += Math.floor(m / 60); m = m % 60; }
                if (h > 0) return `${h}h${m.toString().padStart(2, '0')}`;
                if (m > 0) return `${m}m${sec.toString().padStart(2, '0')}s`;
                return `${sec}s`;
            };

            // Format date as human readable
            const formatDateHuman = (dateStr) => {
                if (!dateStr) return '';
                const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
                const parts = dateStr.split('-');
                if (parts.length !== 3) return dateStr;
                const day = parseInt(parts[2]);
                const month = months[parseInt(parts[1]) - 1] || parts[1];
                const year = parts[0];
                return `${day} ${month} ${year}`;
            };

            // Parse dates for timeline
            const startDate = s.start_date || '';
            const endDate = s.end_date || '';
            const startDay = startDate.split(' ')[0] || '';
            const endDay = endDate.split(' ')[0] || '';
            const startTime = startDate.split(' ')[1] || '';
            const endTime = endDate.split(' ')[1] || '';
            const sameDay = startDay === endDay;

            // Human readable date for header
            const dateDisplay = sameDay
                ? formatDateHuman(startDay)
                : `${formatDateHuman(startDay)} → ${formatDateHuman(endDay)}`;

            // Calculate timeline position (percentage of day)
            const timeToPercent = (timeStr) => {
                if (!timeStr) return 0;
                const parts = timeStr.split(':');
                const h = parseInt(parts[0] || 0);
                const m = parseInt(parts[1] || 0);
                const sec = parseInt(parts[2] || 0);
                return ((h * 3600 + m * 60 + sec) / 86400) * 100;
            };
            const startPercent = sameDay ? timeToPercent(startTime) : 0;
            const endPercent = sameDay ? timeToPercent(endTime) : 100;
            const segmentWidth = Math.max(endPercent - startPercent, 1);
            const segmentCenter = startPercent + segmentWidth / 2;

            // Time range label (centered under segment)
            const timeRangeLabel = sameDay
                ? `${startTime.slice(0, 5)} – ${endTime.slice(0, 5)}`
                : `${startDate} → ${endDate}`;

            return `
                <div class="section" id="summary">
                    <h2 class="section-header">Summary</div>
                    <div class="section-body summary-body">
                        <div class="summary-header">
                            <div class="summary-date">${dateDisplay}</div>
                            <div class="summary-meta">
                                <span class="summary-filename">${esc(f.fileName || 'Unknown')}</span>
                                <span class="summary-parsetime">parsed in ${parseTimeStr}</span>
                            </div>
                        </div>
                        <div class="summary-separator"></div>
                        <div class="stat-grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 1.2rem;">
                            <div class="stat-card">
                                <div class="stat-value">${(f.format || '?').toUpperCase()}</div>
                                <div class="stat-label">format</div>
                            </div>
                            <div class="stat-card">
                                <div class="stat-value">${fmtBytes(f.fileSize || 0)}</div>
                                <div class="stat-label">size</div>
                            </div>
                            <div class="stat-card">
                                <div class="stat-value">${fmt(s.total_logs)}</div>
                                <div class="stat-label">entries</div>
                            </div>
                            <div class="stat-card">
                                <div class="stat-value">${formatDuration(fmtDur(s.duration))}</div>
                                <div class="stat-label">duration</div>
                            </div>
                        </div>
                        <div class="summary-timeline">
                            <div class="summary-timeline-row">
                                <span class="summary-timeline-bound">00:00</span>
                                <div class="summary-timeline-track">
                                    <div class="summary-timeline-segment" style="left: ${startPercent}%; width: ${segmentWidth}%;"></div>
                                </div>
                                <span class="summary-timeline-bound">24:00</span>
                            </div>
                            <div class="summary-timeline-labels">
                                <span class="summary-timeline-range" style="left: ${segmentCenter}%">${timeRangeLabel}</span>
                            </div>
                        </div>
                        <div class="summary-separator"></div>
                    </div>
                </div>
            `;
        }

function buildEventsSection(data) {
	// Filter logic
	const onlyErrors = data.meta?.sections && data.meta.sections.includes('errors') && !data.meta.sections.includes('events') && !data.meta.sections.includes('all');

	// Prepare data
	const topEvents = data.top_events || [];
	const bySeverity = {};
	topEvents.forEach(e => {
		if (!bySeverity[e.severity]) bySeverity[e.severity] = [];
		bySeverity[e.severity].push(e);
	});

	// Stats for bars
	const eventsArr = data.events || []; // {type, count}
	const summaryMap = {};
	eventsArr.forEach(e => { summaryMap[e.type] = e.count; });

	// Define Groups
	const criticalSeverities = ['PANIC', 'FATAL', 'ERROR', 'WARNING'];
	const noiseSeverities = ['NOTICE', 'LOG', 'INFO', 'DEBUG'];

	// Show all critical tabs even if empty (requested feature)
	const activeCriticals = criticalSeverities;

	// Determine default active tab
	let defaultTab = 'ERROR';
	if (summaryMap['ERROR'] > 0) defaultTab = 'ERROR';
	else if (summaryMap['FATAL'] > 0) defaultTab = 'FATAL';
	else if (summaryMap['PANIC'] > 0) defaultTab = 'PANIC';
	else if (summaryMap['WARNING'] > 0) defaultTab = 'WARNING';

	// Generate Tabs HTML (Left)
	let tabsHtml = '';
	if (activeCriticals.length > 0) {
		tabsHtml += '<div class="tabs">';
		activeCriticals.forEach(sev => {
			const count = summaryMap[sev] || 0;
			const isActive = sev === defaultTab ? 'active' : '';
			const cls = sev.toLowerCase();
			tabsHtml += `<button class="tab ${cls} ${isActive}" onclick="openTab(event, 'event-tab-${sev}')">${sev}<span class="tab-badge">${fmt(count)}</span></button>`;
		});
		tabsHtml += '</div>';
	} else {
		tabsHtml += '<div class="empty">No critical events found</div>';
	}

	// Generate Indicators HTML (Right)
	let noiseHtml = '<div class="tabs indicators-group">';
	noiseSeverities.forEach(sev => {
		if (onlyErrors) return;
		const count = summaryMap[sev] || 0;
		const cls = sev.toLowerCase();
		noiseHtml += `<div class="tab indicator ${cls}">
			${sev}<span class="tab-badge">${fmt(count)}</span>
		</div>`;
	});
	noiseHtml += '</div>';

	// Generate Content HTML
	let contentHtml = '';
	criticalSeverities.forEach(sev => {
		const isActive = sev === defaultTab ? 'block' : 'none';
		const count = summaryMap[sev] || 0;
		const sevEvents = bySeverity[sev] || [];
		
		let innerContent = '';

		if (count === 0 || sevEvents.length === 0) {
			innerContent = `
			<div style="padding: 2rem; text-align: center; color: var(--text-muted); font-style: italic;">
				No ${sev} events recorded
			</div>`;
		} else {
			// Group by Class
			const byClass = {};
			sevEvents.forEach(e => {
				const cls = e.sql_state_class || 'Unclassified';
				if (!byClass[cls]) byClass[cls] = [];
				byClass[cls].push(e);
			});
			const classes = Object.keys(byClass).sort();
			if (classes.includes('Unclassified')) {
				classes.splice(classes.indexOf('Unclassified'), 1);
				classes.push('Unclassified');
			}

			let rows = '';
			const sevColor = sev === 'ERROR' ? 'var(--danger)' : sev === 'FATAL' || sev === 'PANIC' ? 'var(--purple)' : sev === 'WARNING' ? 'var(--warning)' : 'var(--text-muted)';

			classes.forEach(cls => {
				const classEvents = byClass[cls];
				classEvents.sort((a, b) => b.count - a.count);

				let code = "";
				let desc = cls;
				if (cls === 'Unclassified') { code = ''; desc = ''; }
				else if (cls.match(/^\w{2} - /)) { code = cls.substring(0, 2); desc = cls.substring(5); }
				else if (cls.length === 2) { code = cls; desc = ''; }

				classEvents.forEach(e => {
					rows += `
					<tr class="event-row">
						<td style="width: 50px; vertical-align: top; padding: 0.35rem 0.5rem;">
							${code ? `<span class="event-class-badge" style="border-color:${sevColor}; color:${sevColor};">${code}</span>` : ''}
						</td>
						<td style="vertical-align: top; padding: 0.35rem 0.5rem;">
							${desc ? `<div style="font-size: 0.6rem; font-weight: 600; color: var(--text-muted); margin-bottom: 2px;">${esc(desc)}</div>` : ''}
							<div class="event-msg-text" title="${esc(e.message)}">${esc(e.message)}</div>
						</td>
						<td class="num" style="width: 60px; vertical-align: top; font-weight: 600;">${fmt(e.count)}</td>
					</tr>`;
				});
			});

			innerContent = `
			<div class="table-container" style="max-height: 220px;">
				<table class="data-table" style="width: 100%;">
					${rows}
				</table>
			</div>`;
		}

		contentHtml += `
		<div id="event-tab-${sev}" class="tab-content" style="display: ${isActive};">
			${innerContent}
		</div>`;
	});

	return `
	<div class="section" id="events">
		<h2 class="section-header">Events</div>
		<div class="section-body">
			<div class="events-toolbar">
				${tabsHtml}
				${noiseHtml}
			</div>
			${contentHtml}
		</div>
	</div>
	`;
}

        function buildConnectionsSection(data) {
            const c = data.connections;
            if (!c || c.connection_count === 0) {
                return `
                    <div class="section" id="connections">
                        <h2 class="section-header muted">Connections</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_connections = on</code>')}
                        </div>
                    </div>
                `;
            }
            const hasSessions = (c.sessions_by_user && Object.keys(c.sessions_by_user).length > 0) ||
                               (c.sessions_by_database && Object.keys(c.sessions_by_database).length > 0);
            // session_distribution is an object: {"< 1s": 123, ...}
            const hasSessionDist = c.session_distribution && Object.keys(c.session_distribution).length > 0;
            const hasConnections = c.connections?.length > 0;

            // Store connection timestamps for chart creation
            if (hasConnections) {
                chartData.set('chart-connections', c.connections);
            }
            // Store session events for client-side sweep-line (allows bucket adjustment)
            if (c.session_events?.length > 0) {
                chartData.set('chart-concurrent', { type: 'sessions', data: c.session_events });
            }

            return `
                <div class="section" id="connections">
                    <h2 class="section-header">Connections</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card">
                                <div class="stat-value">${fmt(c.connection_count)}</div>
                                <div class="stat-label">Connections</div>
                            </div>
                            <div class="stat-card">
                                <div class="stat-value">${fmt(c.disconnection_count)}</div>
                                <div class="stat-label">Disconnections</div>
                            </div>
                            <div class="stat-card">
                                <div class="stat-value">${fmtDur(c.avg_session_time) || '-'}</div>
                                <div class="stat-label">Avg Session</div>
                            </div>
                            <div class="stat-card">
                                <div class="stat-value">${fmtDur(c.session_stats?.median_duration) || '-'}</div>
                                <div class="stat-label">Median Session</div>
                            </div>
                            <div class="stat-card">
                                <div class="stat-value">${c.avg_connections_per_hour || '0'}/h</div>
                                <div class="stat-label">Rate</div>
                            </div>
                            ${c.peak_concurrent_sessions ? `
                                <div class="stat-card">
                                    <div class="stat-value">${c.peak_concurrent_sessions}</div>
                                    <div class="stat-label">Peak Concurrent</div>
                                </div>
                            ` : ''}
                        </div>
                        <div class="grid grid-2" style="margin-top: 0.5rem;">
                            ${c.session_events?.length > 0 ? buildChartContainer('chart-concurrent', 'Concurrent Sessions', { showFilterBtn: false, tooltip: 'Number of active database connections at a given time. High values indicate more database activity.' }) : ''}
                            ${hasConnections ? buildChartContainer('chart-connections', 'Connection Distribution', { showFilterBtn: true, tooltip: 'Timeline of connection events. High values indicate heavy traffic.' }) : ''}
                        </div>
                        ${hasSessionDist ? `
                            <div class="subsection">
                                <div class="subsection-title">Session Duration Distribution</div>
                                ${buildSessionDistributionChart(c.session_distribution)}
                            </div>
                        ` : ''}
                        ${hasSessions ? `
                            <div class="tabs" style="margin-top: 1rem;">
                                <button class="tab active" onclick="showConnTab(this, 'conn-by-user')">By User</button>
                                <button class="tab" onclick="showConnTab(this, 'conn-by-db')">By Database</button>
                                <button class="tab" onclick="showConnTab(this, 'conn-by-host')">By Host</button>
                            </div>
                            <div id="conn-by-user" class="tab-content" style="display: block;">
                                ${buildSessionTable(c.sessions_by_user, 'User')}
                            </div>
                            <div id="conn-by-db" class="tab-content" style="display: none;">
                                ${buildSessionTable(c.sessions_by_database, 'Database')}
                            </div>
                            <div id="conn-by-host" class="tab-content" style="display: none;">
                                ${buildSessionTable(c.sessions_by_host, 'Host')}
                            </div>
                        ` : ''}
                    </div>
                </div>
            `;
        }

        // Build session duration distribution as stacked bar (same style as SQL duration dist)
        function buildSessionDistributionChart(distribution) {
            // Standard buckets in order
            const orderedBuckets = ['< 1s', '1s - 1min', '1min - 30min', '30min - 2h', '2h - 5h', '> 5h'];
            const entries = orderedBuckets.map((label, idx) => ({
                label,
                count: distribution[label] || 0,
                idx
            }));
            const total = entries.reduce((sum, e) => sum + e.count, 0) || 1;
            const activeEntries = entries.filter(e => e.count > 0);

            if (activeEntries.length === 0) return '<div class="empty">No session data</div>';

            // Distinct colors with good contrast
            const colors = ['#3b82f6', '#10b981', '#8b5cf6', '#f59e0b', '#f97316', '#ef4444'];

            return `
                <div class="duration-stack">
                    <div class="duration-stack-bar">
                        ${activeEntries.map(e => {
                            const pct = (e.count / total) * 100;
                            return `<div class="duration-stack-segment" style="flex: ${pct}; min-width: 15px; background: ${colors[e.idx]};" title="${e.label}: ${fmt(e.count)} (${pct.toFixed(1)}%)"></div>`;
                        }).join('')}
                    </div>
                    <div class="duration-stack-legend">
                        ${activeEntries.map(e => {
                            const pct = (e.count / total) * 100;
                            return `<span class="duration-stack-item"><span class="duration-stack-dot" style="background: ${colors[e.idx]};"></span>${e.label} ${pct.toFixed(0)}%</span>`;
                        }).join('')}
                    </div>
                </div>
            `;
        }

        // Build concurrent sessions vertical bar chart
        function buildConcurrentSessionsChart(histogram) {
            if (!histogram || histogram.length === 0) return '';
            const max = Math.max(...histogram.map(h => h.count)) || 1;
            const firstLabel = histogram[0]?.label?.split(' - ')[0] || '';
            const lastLabel = histogram[histogram.length - 1]?.label?.split(' - ')[1] || '';
            return `
                <div class="histogram-container">
                    <div class="histogram">
                        ${histogram.map(h => `
                            <div class="histogram-bar" style="height: ${Math.max(3, h.count/max*100)}%; background: var(--accent);">
                                <div class="tooltip">${h.count} (${h.peak_time || h.label})</div>
                            </div>
                        `).join('')}
                    </div>
                    <div class="histogram-labels">
                        <span>${firstLabel}</span>
                        <span>${lastLabel}</span>
                    </div>
                </div>
            `;
        }

        function showConnTab(btn, id) {
            btn.parentElement.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            btn.classList.add('active');
            ['conn-by-user', 'conn-by-db', 'conn-by-host'].forEach(tid => {
                const el = document.getElementById(tid);
                if (el) el.style.display = tid === id ? 'block' : 'none';
            });
        }

        function buildSessionTable(sessions, label) {
            // sessions is an object: {name: {count, min_duration, avg_duration, ...}}
            if (!sessions || Object.keys(sessions).length === 0) return '<div class="empty">No session data</div>';
            const rows = Object.entries(sessions).map(([name, s]) => ({
                name,
                count: s.count,
                min: fmtDur(s.min_duration),
                avg: fmtDur(s.avg_duration),
                median: fmtDur(s.median_duration),
                max: fmtDur(s.max_duration),
                cumulated: fmtDur(s.cumulated_duration)
            })).sort((a, b) => b.count - a.count).slice(0, 10);
            return `
                <table class="data-table" style="font-size: 0.75rem;">
                    <thead><tr>
                        <th>${label}</th>
                        <th style="text-align:right">Sessions</th>
                        <th style="text-align:right">Min</th>
                        <th style="text-align:right">Avg</th>
                        <th style="text-align:right">Median</th>
                        <th style="text-align:right">Max</th>
                        <th style="text-align:right">Cumulated</th>
                    </tr></thead>
                    <tbody>
                        ${rows.map(s => `
                            <tr>
                                <td>${esc(s.name)}</td>
                                <td style="text-align:right">${fmt(s.count)}</td>
                                <td style="text-align:right">${s.min || '-'}</td>
                                <td style="text-align:right">${s.avg || '-'}</td>
                                <td style="text-align:right">${s.median || '-'}</td>
                                <td style="text-align:right">${s.max || '-'}</td>
                                <td style="text-align:right">${s.cumulated || '-'}</td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            `;
        }

        function buildClientsSection(data) {
            const c = data.clients || {};
            // databases, users, apps, hosts are at top level in JSON
            const databases = data.databases || [];
            const users = data.users || [];
            const apps = data.apps || [];
            const hosts = data.hosts || [];
            if (!c.unique_databases && databases.length === 0) {
                return `
                    <div class="section" id="clients">
                        <h2 class="section-header muted">Clients</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>%u</code>, <code>%d</code>, <code>%a</code> in <code>log_line_prefix</code>')}
                        </div>
                    </div>
                `;
            }
            const maxDb = databases[0]?.count || 1;
            const maxUser = users[0]?.count || 1;
            const maxApp = apps[0]?.count || 1;
            const maxHost = hosts[0]?.count || 1;
            const hasCombos = false; // combos not in quellog JSON

            function buildClientList(items, max) {
                if (!items?.length) return '';
                const total = items.reduce((sum, d) => sum + d.count, 0);
                return `
                    <div class="scroll-list">
                        ${items.slice(0, 10).map(d => {
                            const pct = total > 0 ? (d.count / total * 100).toFixed(1) : '0.0';
                            return `
                            <div class="list-item">
                                <span class="name">${esc(d.name)}</span>
                                <div class="bar-container"><div class="bar"><div class="bar-fill" style="width: ${d.count/max*100}%"></div></div></div>
                                <span class="value">${fmt(d.count)} <small style="color: var(--text-muted)">(${pct}%)</small></span>
                            </div>
                        `}).join('')}
                    </div>
                `;
            }

            return `
                <div class="section" id="clients">
                    <h2 class="section-header">Clients</div>
                    <div class="section-body">
                        <div class="tabs">
                            <button class="tab active" onclick="showClientTab(this, 'client-dbs')">Databases <span class="tab-badge">${c.unique_databases}</span></button>
                            <button class="tab" onclick="showClientTab(this, 'client-users')">Users <span class="tab-badge">${c.unique_users}</span></button>
                            <button class="tab" onclick="showClientTab(this, 'client-apps')">Apps <span class="tab-badge">${c.unique_apps}</span></button>
                            <button class="tab" onclick="showClientTab(this, 'client-hosts')">Hosts <span class="tab-badge">${c.unique_hosts}</span></button>
                        </div>
                        <div id="client-dbs" class="tab-content" style="display: block;">
                            ${buildClientList(databases, maxDb)}
                        </div>
                        <div id="client-users" class="tab-content" style="display: none;">
                            ${buildClientList(users, maxUser)}
                        </div>
                        <div id="client-apps" class="tab-content" style="display: none;">
                            ${buildClientList(apps, maxApp)}
                        </div>
                        <div id="client-hosts" class="tab-content" style="display: none;">
                            ${buildClientList(hosts, maxHost)}
                        </div>
                    </div>
                </div>
            `;
        }

        function showClientTab(btn, id) {
            btn.parentElement.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            btn.classList.add('active');
            ['client-dbs', 'client-users', 'client-apps', 'client-hosts', 'client-combos'].forEach(tid => {
                const el = document.getElementById(tid);
                if (el) el.style.display = tid === id ? 'block' : 'none';
            });
        }

        function buildCheckpointsSection(data) {
            const cp = data.checkpoints;
            if (!cp || cp.total_checkpoints === 0) {
                return `
                    <div class="section" id="checkpoints">
                        <h2 class="section-header muted">Checkpoints</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_checkpoints = on</code>')}
                        </div>
                    </div>
                `;
            }
            // types is an object: {"time": {count, ...}, "wal": {count, ...}, ...}
            const types = cp.types || {};
            const timed = types.time?.count || 0;
            const wal = types.wal?.count || 0;
            const req = (types.shutdown?.count || 0) + (types['immediate force wait']?.count || 0);
            const hasEvents = cp.events?.length > 0;

            // Store checkpoint data by type for multi-series chart
            if (hasEvents) {
                chartData.set('chart-checkpoints', {
                    type: 'checkpoints',
                    all: cp.events,
                    types: {
                        time: types.time?.events || [],
                        wal: types.wal?.events || [],
                        other: [
                            ...(types.shutdown?.events || []),
                            ...(types['immediate force wait']?.events || [])
                        ]
                    }
                });
            }

            return `
                <div class="section" id="checkpoints">
                    <h2 class="section-header">Checkpoints</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${fmt(cp.total_checkpoints)}</div><div class="stat-label">Total</div></div>
                            <div class="stat-card"><div class="stat-value">${timed}</div><div class="stat-label">Timed</div></div>
                            <div class="stat-card"><div class="stat-value">${wal}</div><div class="stat-label">WAL</div></div>
                            <div class="stat-card"><div class="stat-value">${req}</div><div class="stat-label">Req</div></div>
                            <div class="stat-card"><div class="stat-value">${cp.avg_checkpoint_time || '-'}</div><div class="stat-label">Avg</div></div>
                            <div class="stat-card"><div class="stat-value">${cp.max_checkpoint_time || '-'}</div><div class="stat-label">Max</div></div>
                        </div>
                        ${hasEvents ? `
                            ${buildChartContainer('chart-checkpoints', 'Checkpoint Distribution', { showFilterBtn: false, tooltip: 'Checkpoint writes over time. Timed is normal, WAL indicates heavy write load.' })}
                            <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:8px;font-size:12px;">
                                <span><span style="display:inline-block;width:12px;height:12px;background:var(--chart-bar);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Timed</span>
                                <span><span style="display:inline-block;width:12px;height:12px;background:var(--accent);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>WAL</span>
                                <span><span style="display:inline-block;width:12px;height:12px;background:#909399;border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Other</span>
                            </div>
                        ` : ''}
                    </div>
                </div>
            `;
        }

        function buildMaintenanceSection(data) {
            const m = data.maintenance;
            if (!m || ((m.vacuum_count || 0) + (m.analyze_count || 0)) === 0) {
                return `
                    <div class="section" id="maintenance">
                        <h2 class="section-header muted">Maintenance</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_autovacuum_min_duration = 0</code>')}
                        </div>
                    </div>
                `;
            }
            // JSON uses vacuum_count, analyze_count (not autovacuum/autoanalyze)
            // vacuum_table_counts and analyze_table_counts are objects {table: count}
            const vacTables = m.vacuum_table_counts ? Object.entries(m.vacuum_table_counts).map(([t, c]) => ({table: t, count: c})).sort((a,b) => b.count - a.count) : [];
            const anaTables = m.analyze_table_counts ? Object.entries(m.analyze_table_counts).map(([t, c]) => ({table: t, count: c})).sort((a,b) => b.count - a.count) : [];
            const hasVacTables = vacTables.length > 0;
            const hasAnaTables = anaTables.length > 0;
            const maxVac = vacTables[0]?.count || 1;
            const maxAna = anaTables[0]?.count || 1;
            return `
                <div class="section" id="maintenance">
                    <h2 class="section-header">Maintenance</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${m.vacuum_count || 0}</div><div class="stat-label">Vacuum</div></div>
                            <div class="stat-card"><div class="stat-value">${m.analyze_count || 0}</div><div class="stat-label">Analyze</div></div>
                        </div>
                        ${hasVacTables ? `
                            <div class="subsection">
                                <div class="subsection-title">Top Vacuum Tables</div>
                                <div class="scroll-list">
                                    ${vacTables.slice(0, 5).map(t => `
                                        <div class="list-item">
                                            <span class="name">${esc(t.table)}</span>
                                            <div class="bar"><div class="bar-fill" style="width: ${t.count/maxVac*100}%"></div></div>
                                            <span class="value">${fmt(t.count)}</span>
                                        </div>
                                    `).join('')}
                                </div>
                            </div>
                        ` : ''}
                        ${hasAnaTables ? `
                            <div class="subsection">
                                <div class="subsection-title">Top Analyze Tables</div>
                                <div class="scroll-list">
                                    ${anaTables.slice(0, 5).map(t => `
                                        <div class="list-item">
                                            <span class="name">${esc(t.table)}</span>
                                            <div class="bar"><div class="bar-fill" style="width: ${t.count/maxAna*100}%"></div></div>
                                            <span class="value">${fmt(t.count)}</span>
                                        </div>
                                    `).join('')}
                                </div>
                            </div>
                        ` : ''}
                    </div>
                </div>
            `;
        }

        function buildLocksSection(data) {
            const l = data.locks;
            if (!l || ((l.deadlock_events || 0) + (l.waiting_events || 0) + (l.acquired_events || 0)) === 0) {
                return `
                    <div class="section" id="locks">
                        <h2 class="section-header muted">Locks</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_lock_waits = on</code>')}
                        </div>
                    </div>
                `;
            }
            // JSON uses: deadlock_events, waiting_events, acquired_events
            // lock_type_stats and resource_type_stats are objects {type: count}
            const deadlocks = l.deadlock_events || 0;
            const lockTypes = l.lock_type_stats ? Object.entries(l.lock_type_stats).map(([t, c]) => ({type: t, count: c})).sort((a,b) => b.count - a.count) : [];
            const resTypes = l.resource_type_stats ? Object.entries(l.resource_type_stats).map(([t, c]) => ({type: t, count: c})).sort((a,b) => b.count - a.count) : [];
            const hasLockTypes = lockTypes.length > 0;
            const hasResTypes = resTypes.length > 0;
            const hasQueries = l.queries?.length > 0;
            return `
                <div class="section" id="locks">
                    <h2 class="section-header ${deadlocks > 0 ? 'danger' : ''}">Locks</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card ${deadlocks > 0 ? 'stat-card--alert' : ''}"><div class="stat-value">${deadlocks}</div><div class="stat-label">Deadlocks</div></div>
                            <div class="stat-card"><div class="stat-value">${l.waiting_events || 0}</div><div class="stat-label">Still Waiting</div></div>
                            <div class="stat-card"><div class="stat-value">${l.acquired_events || 0}</div><div class="stat-label">Acquired</div></div>
                            <div class="stat-card"><div class="stat-value">${fmtDur(l.avg_wait_time) || '-'}</div><div class="stat-label">Avg</div></div>
                            <div class="stat-card"><div class="stat-value">${fmtDur(l.total_wait_time) || '-'}</div><div class="stat-label">Total</div></div>
                        </div>
                        ${hasLockTypes || hasResTypes ? `
                            <div class="subsection" style="display: flex; gap: 1rem; flex-wrap: wrap;">
                                ${hasLockTypes ? `
                                    <div style="flex: 1; min-width: 120px;">
                                        <div class="subsection-title" style="margin-top: 0;">Lock Types</div>
                                        <div class="query-types">
                                            ${lockTypes.map(t => `
                                                <span class="query-type">
                                                    <span class="name">${t.type}</span>
                                                    <span class="count">${fmt(t.count)}</span>
                                                </span>
                                            `).join('')}
                                        </div>
                                    </div>
                                ` : ''}
                                ${hasResTypes ? `
                                    <div style="flex: 1; min-width: 120px;">
                                        <div class="subsection-title" style="margin-top: 0;">Resource Types</div>
                                        <div class="query-types">
                                            ${resTypes.map(t => `
                                                <span class="query-type">
                                                    <span class="name">${t.type}</span>
                                                    <span class="count">${fmt(t.count)}</span>
                                                </span>
                                            `).join('')}
                                        </div>
                                    </div>
                                ` : ''}
                            </div>
                        ` : ''}
                        ${hasQueries ? `
                            <div class="subsection">
                                <div class="subsection-title">Queries Involved in Lock Waits</div>
                                <div class="table-container" style="max-height: 180px;">
                                    <table>
                                        <thead><tr>
                                            <th>Query</th>
                                            <th class="num">Acquired</th>
                                            <th class="num">Waiting</th>
                                            <th class="num">Total Wait</th>
                                        </tr></thead>
                                        <tbody>
                                            ${l.queries.slice(0, 10).map(q => `
                                                <tr>
                                                    <td class="query-cell" onclick="showQueryModal('${esc(q.id)}')">${esc(q.normalized_query)}</td>
                                                    <td class="num">${q.acquired_count || 0}</td>
                                                    <td class="num">${q.still_waiting_count || 0}</td>
                                                    <td class="num">${fmtDur(q.total_wait_time) || '-'}</td>
                                                </tr>
                                            `).join('')}
                                        </tbody>
                                    </table>
                                </div>
                            </div>
                        ` : ''}
                    </div>
                </div>
            `;
        }

        function buildTempFilesSection(data) {
            const tf = data.temp_files;
            // JSON has: total_messages, total_size, avg_size, events (array with timestamps), queries (array)
            if (!tf || tf.total_messages === 0) {
                return `
                    <div class="section" id="temp_files">
                        <h2 class="section-header muted">Temp Files</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_temp_files = 0</code>')}
                        </div>
                    </div>
                `;
            }
            const hasQueries = tf.queries?.length > 0;
            const hasEvents = tf.events?.length > 0;
            // Store timestamps for chart creation
            if (hasEvents) {
                chartData.set('chart-tempfiles', tf.events.map(e => e.timestamp));
            }
            return `
                <div class="section" id="temp_files">
                    <h2 class="section-header">Temp Files</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${fmt(tf.total_messages)}</div><div class="stat-label">Count</div></div>
                            <div class="stat-card"><div class="stat-value">${tf.total_size}</div><div class="stat-label">Total</div></div>
                            <div class="stat-card"><div class="stat-value">${tf.avg_size}</div><div class="stat-label">Avg</div></div>
                        </div>
                        ${hasEvents ? buildChartContainer('chart-tempfiles', 'Temp File Activity', { showFilterBtn: true, tooltip: 'Temporary files created by queries exceeding work_mem.' }) : ''}
                        ${hasQueries ? `
                            <div class="subsection">
                                <div class="subsection-title">Top Queries</div>
                                <div class="table-container" style="max-height: 180px;">
                                    <table>
                                        <thead><tr>
                                            <th>Query</th>
                                            <th class="num">Count</th>
                                            <th class="num">Total Size</th>
                                        </tr></thead>
                                        <tbody>
                                            ${tf.queries.slice(0, 10).map(q => `
                                                <tr>
                                                    <td class="query-cell" onclick="showQueryModal('${esc(q.id || '')}')">${esc(q.normalized_query || '')}</td>
                                                    <td class="num">${fmt(q.count)}</td>
                                                    <td class="num">${q.total_size}</td>
                                                </tr>
                                            `).join('')}
                                        </tbody>
                                    </table>
                                </div>
                            </div>
                        ` : ''}
                    </div>
                </div>
            `;
        }

        // Build time histogram from array of timestamp strings
        function buildTimeHistogram(timestamps, buckets = 24) {
            if (!timestamps || timestamps.length === 0) return [];
            const times = timestamps.map(t => new Date(t).getTime()).filter(t => !isNaN(t));
            if (times.length === 0) return [];
            // Use reduce instead of spread to avoid "too many arguments" error
            const min = times.reduce((a, b) => a < b ? a : b, times[0]);
            const max = times.reduce((a, b) => a > b ? a : b, times[0]);
            const range = max - min || 1;
            const bucketSize = range / buckets;
            const hist = Array(buckets).fill(0);
            times.forEach(t => {
                const idx = Math.min(Math.floor((t - min) / bucketSize), buckets - 1);
                hist[idx]++;
            });
            const startDate = new Date(min);
            const endDate = new Date(max);
            return hist.map((count, i) => ({
                count,
                start: i === 0 ? formatTime(startDate) : '',
                end: i === buckets - 1 ? formatTime(endDate) : ''
            }));
        }

        function formatTime(d) {
            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
        }

        function buildHistogramHTML(histogram, colorVar = '--chart-bar') {
            const max = Math.max(...histogram.map(h => h.count)) || 1;
            return `
                <div class="histogram">
                    ${histogram.map(h => `
                        <div class="histogram-bar" style="height: ${Math.max(3, h.count/max*100)}%; background: var(${colorVar});">
                            <div class="tooltip">${h.count}</div>
                        </div>
                    `).join('')}
                </div>
                <div class="histogram-labels">
                    <span>${histogram[0]?.start || ''}</span>
                    <span>${histogram[histogram.length-1]?.end || ''}</span>
                </div>
            `;
        }

        function buildSQLOverviewSection(data) {
            const ov = data.sql_overview;
            // Check if we have any SQL data
            const hasData = ov && ov.categories && ov.categories.some(c => c.count > 0);
            if (!hasData) {
                return `
                    <div class="section" id="sql_overview">
                        <h2 class="section-header muted">SQL Overview</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_min_duration_statement = 0</code>')}
                        </div>
                    </div>
                `;
            }
            // Build dimension data for tab switching
            const globalTypes = ov.types || ov.query_types || [];
            const hasByDb = ov.by_database?.length > 0;
            const hasByUser = ov.by_user?.length > 0;
            const hasByHost = ov.by_host?.length > 0;
            const hasByApp = ov.by_app?.length > 0;

            // All possible categories in order
            const allCategories = ['DML', 'DDL', 'TCL', 'UTILITY', 'OTHER'];
            const catMap = {};
            (ov.categories || []).forEach(c => { catMap[c.category || c.name] = c; });

            return `
                <div class="section" id="sql_overview">
                    <h2 class="section-header">SQL Overview</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            ${allCategories.map(cat => {
                                const c = catMap[cat];
                                const hasData = c && c.count > 0;
                                return `
                                <div class="stat-card stat-card-compact${hasData ? '' : ' stat-card-muted'}">
                                    <div class="stat-value">${hasData ? fmt(c.count) : '—'}</div>
                                    <div class="stat-label">${cat}${hasData ? ` <span class="stat-pct">${c.percentage?.toFixed(1) || 0}%</span>` : ''}</div>
                                </div>`;
                            }).join('')}
                        </div>

                        <div class="subsection">
                            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.5rem;">
                                <div class="subsection-title" style="margin: 0;">Query Types</div>
                                <div class="tabs" style="margin: 0;">
                                    <button class="tab active" onclick="showSqlOvView(this, 'global')">Global</button>
                                    ${hasByDb ? '<button class="tab" onclick="showSqlOvView(this, \'database\')">By Database</button>' : ''}
                                    ${hasByUser ? '<button class="tab" onclick="showSqlOvView(this, \'user\')">By User</button>' : ''}
                                    ${hasByHost ? '<button class="tab" onclick="showSqlOvView(this, \'host\')">By Host</button>' : ''}
                                    ${hasByApp ? '<button class="tab" onclick="showSqlOvView(this, \'app\')">By App</button>' : ''}
                                </div>
                            </div>
                            <div id="sqlov-table-container">
                                ${buildQueryTypesTable(globalTypes)}
                            </div>
                        </div>
                    </div>
                </div>
            `;
        }

        // Store SQL overview data globally for tab switching
        let sqlOverviewData = null;

        function showSqlOvView(btn, view) {
            btn.parentElement.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            btn.classList.add('active');

            const container = document.getElementById('sqlov-table-container');
            if (!container || !sqlOverviewData) return;

            const ov = sqlOverviewData;
            if (view === 'global') {
                container.innerHTML = buildQueryTypesTable(ov.types || ov.query_types || []);
            } else {
                const dimKey = 'by_' + view;
                const items = ov[dimKey] || [];
                container.innerHTML = buildDimensionTable(items, view);
            }
        }

        function buildQueryTypesTable(types) {
            if (!types?.length) return '<div class="empty">No query type data</div>';
            return `
                <div class="table-container" style="max-height: 300px;">
                    <table>
                        <thead><tr>
                            <th>Type</th><th class="num">Count</th><th class="num">%</th><th class="num">Avg</th><th class="num">Max</th><th class="num">Total</th>
                        </tr></thead>
                        <tbody>
                            ${types.slice(0, 15).map(t => `
                                <tr>
                                    <td><span class="query-type"><span class="name">${t.type}</span></span></td>
                                    <td class="num">${fmt(t.count)}</td>
                                    <td class="num">${t.percentage?.toFixed(1) || 0}%</td>
                                    <td class="num">${fmtDur(t.avg_time) || '-'}</td>
                                    <td class="num">${fmtDur(t.max_time) || '-'}</td>
                                    <td class="num">${fmtDur(t.total_time) || '-'}</td>
                                </tr>
                            `).join('')}
                        </tbody>
                    </table>
                </div>
            `;
        }

        function buildDimensionTable(items, dimType) {
            if (!items?.length) return `<div class="empty">No ${dimType} data</div>`;
            const label = dimType.charAt(0).toUpperCase() + dimType.slice(1);
            return `
                <div class="table-container" style="max-height: 300px;">
                    <table>
                        <thead><tr>
                            <th>${label}</th><th class="num">Queries</th><th class="num">Total Time</th><th>Top Types</th>
                        </tr></thead>
                        <tbody>
                            ${items.slice(0, 15).map(d => `
                                <tr>
                                    <td style="font-weight: 500;">${esc(d.name)}</td>
                                    <td class="num">${fmt(d.count)}</td>
                                    <td class="num">${fmtDur(d.total_time) || '-'}</td>
                                    <td>
                                        <div class="query-types" style="justify-content: flex-start;">
                                            ${(d.query_types || []).slice(0, 4).map(t => `
                                                <span class="query-type" style="padding: 0.15rem 0.35rem; font-size: 0.65rem;">
                                                    <span class="name">${t.type}</span>
                                                    <span class="count">${fmt(t.count)}</span>
                                                </span>
                                            `).join('')}
                                        </div>
                                    </td>
                                </tr>
                            `).join('')}
                        </tbody>
                    </table>
                </div>
            `;
        }

        function buildSQLPerformanceSection(data) {
            const sql = data.sql_performance;
            const queries = sql.queries || [];
            const executions = sql.executions || [];
            const maxTime = safeMax(queries.map(q => q.total_time_ms)) || 1;

            // Create sorted copies for each tab
            const byTotal = [...queries].sort((a, b) => b.total_time_ms - a.total_time_ms);
            const bySlowest = [...queries].sort((a, b) => b.max_time_ms - a.max_time_ms);
            const byFrequent = [...queries].sort((a, b) => b.count - a.count);

            // Store executions for combined chart
            const hasExecutions = executions.length > 0;
            if (hasExecutions) {
                const times = executions.map(e => new Date(e.timestamp).getTime() / 1000).sort((a, b) => a - b);
                const execs = executions.map(e => ({
                    t: new Date(e.timestamp).getTime() / 1000,
                    d: e.duration_ms || 0
                }));
                chartData.set('chart-sql-combined', {
                    type: 'combined',
                    data: { times, executions: execs }
                });
            }

            // Build duration distribution from queries
            const durationDist = buildDurationDistribution(queries);

            return `
                <div class="section" id="sql_performance">
                    <h2 class="section-header">SQL Performance</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${fmt(sql.total_queries_parsed)}</div><div class="stat-label">Queries</div></div>
                            <div class="stat-card"><div class="stat-value">${fmt(sql.total_unique_queries)}</div><div class="stat-label">Unique</div></div>
                            <div class="stat-card"><div class="stat-value">${fmtDur(sql.query_min_duration) || '-'}</div><div class="stat-label">Min</div></div>
                            <div class="stat-card"><div class="stat-value">${fmtDur(sql.query_median_duration) || '-'}</div><div class="stat-label">Median</div></div>
                            <div class="stat-card stat-card--alert"><div class="stat-value">${fmtDur(sql.query_99th_percentile) || '-'}</div><div class="stat-label">P99</div></div>
                            <div class="stat-card stat-card--alert"><div class="stat-value">${fmtDur(sql.query_max_duration) || '-'}</div><div class="stat-label">Max</div></div>
                            ${(sql.top_1_percent_slow_queries || 0) > 0 ? `<div class="stat-card stat-card--alert"><div class="stat-value">${sql.top_1_percent_slow_queries}</div><div class="stat-label">Top 1%</div></div>` : ''}
                        </div>

                        ${hasExecutions ? `
                            <div style="margin-top: 0.75rem;">
                                ${buildChartContainer('chart-sql-combined', 'Query Activity', { showFilterBtn: true, tooltip: 'Query count and cumulated duration over time.' })}
                                <div class="chart-legend">
                                    <span class="chart-legend-item" data-chart="chart-sql-combined" data-series="count" onclick="toggleCombinedSeries('chart-sql-combined', 'count')"><span class="chart-legend-bar chart-legend-bar--count"></span>Count</span>
                                    <span class="chart-legend-item" data-chart="chart-sql-combined" data-series="duration" onclick="toggleCombinedSeries('chart-sql-combined', 'duration')"><span class="chart-legend-bar chart-legend-bar--duration"></span>Duration</span>
                                </div>
                            </div>
                        ` : ''}
                        ${durationDist.some(d => d.count > 0) ? `
                            <div class="subsection" style="margin-top: 0.75rem;">
                                <div class="subsection-title">Duration Distribution</div>
                                ${buildCompactDurationDist(durationDist)}
                            </div>
                        ` : ''}

                        <div class="tabs" style="margin-top: 1rem;">
                            <button class="tab active" onclick="showSqlTab(this, 'sql-total')">By Total Time</button>
                            <button class="tab" onclick="showSqlTab(this, 'sql-slowest')">Slowest (Max)</button>
                            <button class="tab" onclick="showSqlTab(this, 'sql-frequent')">Most Frequent</button>
                        </div>

                        <div id="sql-total" class="tab-content" style="display: block;">
                            ${buildQueryTable(byTotal, maxTime, 'total')}
                        </div>
                        <div id="sql-slowest" class="tab-content" style="display: none;">
                            ${buildQueryTable(bySlowest, maxTime, 'max')}
                        </div>
                        <div id="sql-frequent" class="tab-content" style="display: none;">
                            ${buildQueryTable(byFrequent, maxTime, 'count')}
                        </div>
                    </div>
                </div>
            `;
        }

        // Build duration distribution buckets from queries
        function buildDurationDistribution(queries) {
            if (!queries?.length) return [];
            // Duration buckets in ms
            const buckets = [
                { label: '< 1ms', max: 1 },
                { label: '1-10ms', max: 10 },
                { label: '10-100ms', max: 100 },
                { label: '100ms-1s', max: 1000 },
                { label: '1-10s', max: 10000 },
                { label: '> 10s', max: Infinity }
            ];
            const counts = buckets.map(() => 0);
            queries.forEach(q => {
                // Use avg_time_ms for distribution
                const ms = q.avg_time_ms || 0;
                for (let i = 0; i < buckets.length; i++) {
                    if (ms < buckets[i].max) {
                        counts[i] += q.count || 1;
                        break;
                    }
                }
            });
            // Return all buckets (including zeros for grayed display)
            return buckets.map((b, i) => ({ label: b.label, count: counts[i] }));
        }

        function buildDurationDistChart(dist) {
            const total = dist.reduce((sum, d) => sum + d.count, 0) || 1;

            // Calculate max and second max for truncation logic
            const counts = dist.map(d => d.count).filter(c => c > 0).sort((a, b) => b - a);
            const maxCount = counts[0] || 1;
            const secondMax = counts[1] || maxCount;
            const needsTruncation = maxCount > secondMax * 5 && secondMax > 0;
            const secondMaxWidth = needsTruncation ? 75 : 100;

            const getBarWidth = (count) => {
                if (count === 0) return 0;
                if (needsTruncation && count === maxCount) return 100;
                const scaleMax = needsTruncation ? secondMax : maxCount;
                return Math.max((count / scaleMax) * secondMaxWidth, 5);
            };

            return `
                <div class="sql-category-bars">
                    ${dist.map(d => {
                        const pct = ((d.count / total) * 100).toFixed(1);
                        const width = getBarWidth(d.count);
                        const isTruncated = needsTruncation && d.count === maxCount;
                        const hatchStart = secondMaxWidth;
                        return `
                            <div class="sql-category-bar${d.count === 0 ? ' disabled' : ''}">
                                <span class="label" style="width: 70px;">${d.label}</span>
                                <div class="bar-bg">
                                    <div class="bar${isTruncated ? ' truncated' : ''}"
                                         style="width: ${width}%;${isTruncated ? ` --hatch-start: ${hatchStart}%;` : ''}"></div>
                                </div>
                                <span class="count">${fmt(d.count)}</span>
                                <span class="pct">${pct}%</span>
                            </div>
                        `;
                    }).join('')}
                </div>
            `;
        }

        // Compact horizontal duration distribution
        function buildCompactDurationDist(dist) {
            const total = dist.reduce((sum, d) => sum + d.count, 0) || 1;
            // Filter to non-zero buckets only, keep original index for color
            const activeDist = dist.map((d, i) => ({ ...d, idx: i })).filter(d => d.count > 0);
            if (activeDist.length === 0) return '';

            // Distinct colors with good contrast
            const colors = ['#3b82f6', '#10b981', '#8b5cf6', '#f59e0b', '#f97316', '#ef4444'];

            return `
                <div class="duration-stack">
                    <div class="duration-stack-bar">
                        ${activeDist.map(d => {
                            const pct = (d.count / total) * 100;
                            return `<div class="duration-stack-segment" style="flex: ${pct}; min-width: 15px; background: ${colors[d.idx]};" title="${d.label}: ${fmt(d.count)} (${pct.toFixed(1)}%)"></div>`;
                        }).join('')}
                    </div>
                    <div class="duration-stack-legend">
                        ${activeDist.map(d => {
                            const pct = (d.count / total) * 100;
                            return `<span class="duration-stack-item"><span class="duration-stack-dot" style="background: ${colors[d.idx]};"></span>${d.label} ${pct.toFixed(0)}%</span>`;
                        }).join('')}
                    </div>
                </div>
            `;
        }

        function showSqlTab(btn, id) {
            btn.parentElement.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            btn.classList.add('active');
            ['sql-total', 'sql-slowest', 'sql-frequent'].forEach(tid => {
                const el = document.getElementById(tid);
                if (el) el.style.display = tid === id ? 'block' : 'none';
            });
        }

        function buildQueryTable(queries, maxTime, sortBy) {
            if (!queries?.length) return '<div class="empty">No queries</div>';
            // Calculate total for percentage
            const totalTime = queries.reduce((sum, q) => sum + (q.total_time_ms || 0), 0);
            return `
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th>#</th>
                                <th>Query</th>
                                <th class="num">Count</th>
                                <th class="num">Avg</th>
                                <th class="num">Max</th>
                                <th class="num">%</th>
                                <th class="num">Total</th>
                            </tr>
                        </thead>
                        <tbody>
                            ${queries.slice(0, 50).map((q, i) => {
                                const pct = totalTime > 0 ? (q.total_time_ms / totalTime * 100).toFixed(1) : 0;
                                return `
                                <tr>
                                    <td>${i + 1}</td>
                                    <td class="query-cell" onclick="showQueryModal('${esc(q.id)}')" title="Click for details">${esc(q.normalized_query)}</td>
                                    <td class="num">${fmt(q.count)}</td>
                                    <td class="num">${fmtMs(q.avg_time_ms)}</td>
                                    <td class="num">${fmtMs(q.max_time_ms)}</td>
                                    <td class="num">${pct}%</td>
                                    <td class="num">
                                        <div class="duration-bar">
                                            <div class="bar"><div class="bar-fill" style="width: ${q.total_time_ms/maxTime*100}%"></div></div>
                                            <span>${fmtMsLong(q.total_time_ms)}</span>
                                        </div>
                                    </td>
                                </tr>
                            `}).join('')}
                        </tbody>
                    </table>
                </div>
            `;
        }

        function buildHistogram(histogram) {
            if (!histogram || histogram.length === 0) return '';
            const max = Math.max(...histogram.map(h => h.count)) || 1;
            return `
                <div class="histogram-container">
                    <div class="histogram">
                        ${histogram.map(h => `
                            <div class="histogram-bar" style="height: ${Math.max(3, h.count/max*100)}%">
                                <div class="tooltip">${h.start}-${h.end}: ${fmt(h.count)}</div>
                            </div>
                        `).join('')}
                    </div>
                    <div class="histogram-labels">
                        <span>${histogram[0]?.start || ''}</span>
                        <span>${histogram[histogram.length-1]?.end || ''}</span>
                    </div>
                </div>
            `;
        }

        // Query detail modal
        function showQueryDetail(index) {
            const q = analysisData.sql_performance.queries[index];
            document.getElementById('queryModalBody').innerHTML = `
                <div class="query-detail-sql">
                    <button class="copy-btn" onclick="copyQuery(${index})">Copy</button>
                    ${esc(q.full_query || q.normalized_query)}
                </div>
                <div class="detail-stats">
                    <div class="detail-stat"><div class="value">${fmt(q.count)}</div><div class="label">Executions</div></div>
                    <div class="detail-stat"><div class="value">${fmtMs(q.total_time_ms)}</div><div class="label">Total Time</div></div>
                    <div class="detail-stat"><div class="value">${fmtMs(q.avg_time_ms)}</div><div class="label">Avg Time</div></div>
                    <div class="detail-stat"><div class="value">${fmtMs(q.min_time_ms)}</div><div class="label">Min Time</div></div>
                    <div class="detail-stat"><div class="value">${fmtMs(q.max_time_ms)}</div><div class="label">Max Time</div></div>
                    <div class="detail-stat"><div class="value">${q.percentage?.toFixed(2) || '-'}%</div><div class="label">% of Total</div></div>
                    <div class="detail-stat"><div class="value">${q.query_type || '-'}</div><div class="label">Type</div></div>
                    <div class="detail-stat"><div class="value">${q.category || '-'}</div><div class="label">Category</div></div>
                </div>
            `;
            document.getElementById('queryModal').classList.add('active');
        }

        function copyQuery(index) {
            const q = analysisData.sql_performance.queries[index];
            navigator.clipboard.writeText(q.full_query || q.normalized_query);
            alert('Query copied to clipboard');
        }

        function closeModal() {
            document.getElementById('queryModal').classList.remove('active');
            // Destroy modal charts
            modalCharts.forEach(c => c.destroy());
            modalCharts.length = 0;
        }

        function showQueryModal(queryId) {
            if (!analysisData) return;

            // 1. Search in sql_performance.queries
            const sqlQueries = analysisData.sql_performance?.queries || [];
            let q = sqlQueries.find(x => x.id === queryId);
            if (!q) {
                q = sqlQueries.find(x => x.normalized_query === queryId || x.raw_query === queryId);
            }

            // 2. Search in locks.queries
            const lockQueries = analysisData.locks?.queries || [];
            let lockQ = lockQueries.find(x => x.id === queryId);
            if (!lockQ) {
                lockQ = lockQueries.find(x => x.normalized_query === queryId);
            }

            // 3. Search in temp_files.queries
            const tempQueries = analysisData.temp_files?.queries || [];
            let tempQ = tempQueries.find(x => x.id === queryId);
            if (!tempQ) {
                tempQ = tempQueries.find(x => x.normalized_query === queryId);
            }

            // If nothing found, show just the text
            if (!q && !lockQ && !tempQ) {
                document.getElementById('queryModalBody').innerHTML = '<div class="query-detail-sql">' + esc(queryId) + '</div>';
                document.getElementById('queryModal').classList.add('active');
                return;
            }

            // Get executions and temp events
            const allExecs = analysisData.sql_performance?.executions || [];
            const execs = q ? allExecs.filter(e => e.query_id === q.id) : [];
            const allTempEvents = analysisData.temp_files?.events || [];
            const tempEvents = q ? allTempEvents.filter(e => e.query_id === q.id) : [];

            // Build detailed view with all available data
            document.getElementById('queryModalBody').innerHTML = buildQueryDetailHTML(q, execs, tempEvents, lockQ, tempQ);
            document.getElementById('queryModal').classList.add('active');

            // Render uPlot charts after DOM update and modal animation
            setTimeout(() => {
                renderModalCharts();
                // Create combined chart for query detail if we have executions
                if (q && execs.length > 0) {
                    const times = execs.map(e => new Date(e.timestamp).getTime() / 1000).sort((a, b) => a - b);
                    const execData = execs.map(e => ({
                        t: new Date(e.timestamp).getTime() / 1000,
                        d: e.duration_ms || 0
                    }));
                    createCombinedSQLChart('qd-chart-combined', { times, executions: execData }, { height: 180 });
                }
            }, 100);
        }

        function buildQueryDetailHTML(q, execs, tempEvents, lockQ, tempQ) {
            let html = '';
            // Use the best available query object
            const mainQuery = q || lockQ || tempQ;
            const queryText = mainQuery?.normalized_query || mainQuery?.raw_query || '';

            // QUERY INFO section (from sql_performance)
            if (q) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title">Query Info</div>';
                html += '<div class="qd-stats">';
                html += '<div class="qd-stat"><div class="qd-stat-label">Type</div><div class="qd-stat-value">' + (q.type || q.query_type || '-') + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Count</div><div class="qd-stat-value">' + fmt(q.count) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Total</div><div class="qd-stat-value">' + fmtMsLong(q.total_time_ms) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Avg</div><div class="qd-stat-value">' + fmtMsLong(q.avg_time_ms) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Max</div><div class="qd-stat-value">' + fmtMsLong(q.max_time_ms) + '</div></div>';
                if (q.min_time_ms != null) {
                    html += '<div class="qd-stat"><div class="qd-stat-label">Min</div><div class="qd-stat-value">' + fmtMsLong(q.min_time_ms) + '</div></div>';
                }
                html += '</div>';
                if (execs.length > 0) {
                    html += '<div class="qd-chart-container">';
                    html += '<div id="qd-chart-combined" style="height: 180px;"></div>';
                    html += '<div class="chart-legend">';
                    html += '<span class="chart-legend-item" data-chart="qd-chart-combined" data-series="count" onclick="toggleCombinedSeries(\'qd-chart-combined\', \'count\')"><span class="chart-legend-bar chart-legend-bar--count"></span>Count</span>';
                    html += '<span class="chart-legend-item" data-chart="qd-chart-combined" data-series="duration" onclick="toggleCombinedSeries(\'qd-chart-combined\', \'duration\')"><span class="chart-legend-bar chart-legend-bar--duration"></span>Duration</span>';
                    html += '</div>';
                    html += '</div>';
                    html += buildQdDurationDistribution(execs);
                }
                html += '</div>';
            }

            // LOCKS section (from locks.queries)
            if (lockQ) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="color: var(--warning)">Lock Waits</div>';
                html += '<div class="qd-stats">';
                html += '<div class="qd-stat"><div class="qd-stat-label">Acquired</div><div class="qd-stat-value">' + fmt(lockQ.acquired_count || 0) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Still Waiting</div><div class="qd-stat-value">' + fmt(lockQ.still_waiting_count || 0) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Total Wait</div><div class="qd-stat-value">' + fmtDur(lockQ.total_wait_time) + '</div></div>';
                if (lockQ.avg_wait_time) {
                    html += '<div class="qd-stat"><div class="qd-stat-label">Avg Wait</div><div class="qd-stat-value">' + fmtDur(lockQ.avg_wait_time) + '</div></div>';
                }
                if (lockQ.max_wait_time) {
                    html += '<div class="qd-stat"><div class="qd-stat-label">Max Wait</div><div class="qd-stat-value">' + fmtDur(lockQ.max_wait_time) + '</div></div>';
                }
                html += '</div>';
                // Lock types breakdown
                if (lockQ.lock_types && Object.keys(lockQ.lock_types).length > 0) {
                    html += '<div style="margin-top: 0.75rem;">';
                    html += '<div style="font-size: 0.7rem; color: var(--text-muted); margin-bottom: 0.3rem;">Lock Types</div>';
                    html += '<div class="query-types">';
                    for (const [type, count] of Object.entries(lockQ.lock_types)) {
                        html += '<span class="query-type"><span class="name">' + type + '</span><span class="count">' + fmt(count) + '</span></span>';
                    }
                    html += '</div></div>';
                }
                html += '</div>';
            }

            // TEMP FILES section (from temp_files.queries or events)
            if (tempQ) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="color: var(--accent)">Temp Files</div>';
                html += '<div class="qd-stats">';
                html += '<div class="qd-stat"><div class="qd-stat-label">Count</div><div class="qd-stat-value">' + fmt(tempQ.count) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Total Size</div><div class="qd-stat-value">' + (tempQ.total_size || '-') + '</div></div>';
                if (tempQ.avg_size) {
                    html += '<div class="qd-stat"><div class="qd-stat-label">Avg Size</div><div class="qd-stat-value">' + tempQ.avg_size + '</div></div>';
                }
                if (tempQ.min_size) {
                    html += '<div class="qd-stat"><div class="qd-stat-label">Min</div><div class="qd-stat-value">' + tempQ.min_size + '</div></div>';
                }
                if (tempQ.max_size) {
                    html += '<div class="qd-stat"><div class="qd-stat-label">Max</div><div class="qd-stat-value">' + tempQ.max_size + '</div></div>';
                }
                html += '</div>';
                html += '</div>';
            } else if (tempEvents && tempEvents.length > 0) {
                // Fallback to temp events from sql_performance cross-reference
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="color: var(--accent)">Temp Files</div>';
                const totalSize = tempEvents.reduce((sum, e) => sum + parseSizeToBytes(e.size), 0);
                const avgSize = totalSize / tempEvents.length;
                const sizes = tempEvents.map(e => parseSizeToBytes(e.size));
                const minSize = safeMin(sizes);
                const maxSize = safeMax(sizes);
                html += '<div class="qd-stats">';
                html += '<div class="qd-stat"><div class="qd-stat-label">Count</div><div class="qd-stat-value">' + fmt(tempEvents.length) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Total Size</div><div class="qd-stat-value">' + fmtBytes(totalSize) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Avg Size</div><div class="qd-stat-value">' + fmtBytes(avgSize) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Min</div><div class="qd-stat-value">' + fmtBytes(minSize) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Max</div><div class="qd-stat-value">' + fmtBytes(maxSize) + '</div></div>';
                html += '</div>';
                html += buildQdTempFilesHistogram(tempEvents);
                html += '</div>';
            }

            // NORMALIZED QUERY section
            if (queryText) {
                const copySource = q ? 'sql_performance.queries' : lockQ ? 'locks.queries' : 'temp_files.queries';
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="display: flex; justify-content: space-between; align-items: center;">Normalized Query<button class="copy-btn-inline" onclick="navigator.clipboard.writeText(\'' + esc(queryText).replace(/'/g, "\\'").replace(/\n/g, '\\n') + '\');this.textContent=\'Copied!\';setTimeout(()=>this.textContent=\'Copy\',1500)">Copy</button></div>';
                html += '<div class="query-detail-sql">';
                html += formatSQL(queryText);
                html += '</div>';
                html += '</div>';
            }

            // RAW QUERY section (if different and available)
            if (q?.raw_query && q.raw_query !== q.normalized_query) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="display: flex; justify-content: space-between; align-items: center;">Example Query<button class="copy-btn-inline" onclick="navigator.clipboard.writeText(\'' + esc(q.raw_query).replace(/'/g, "\\'").replace(/\n/g, '\\n') + '\');this.textContent=\'Copied!\';setTimeout(()=>this.textContent=\'Copy\',1500)">Copy</button></div>';
                html += '<div class="query-detail-sql">';
                html += esc(q.raw_query);
                html += '</div>';
                html += '</div>';
            }

            return html;
        }

        // Render modal charts after DOM update
        function renderModalCharts() {
            // Destroy previous modal charts
            modalCharts.forEach(c => c.destroy());
            modalCharts.length = 0;

            // Render each pending chart
            modalChartsData.forEach((data, containerId) => {
                const container = document.getElementById(containerId);
                if (!container) return;

                const chart = createModalBarChart(container, data.xData, data.yData, {
                    color: data.color,
                    height: data.height || 100,
                    valueFormatter: data.valueFormatter
                });
                if (chart) modalCharts.push(chart);
            });
            modalChartsData.clear();
        }

        // Create uPlot bar chart for modal
        function createModalBarChart(container, xData, yData, options = {}) {
            if (!xData || xData.length === 0) return null;

            const resolveColor = (c) => {
                if (c && c.startsWith('var(')) {
                    const varName = c.slice(4, -1);
                    return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
                }
                return c || '#5a9bd5';
            };

            const baseColor = resolveColor(options.color || 'var(--primary)');
            const textColor = resolveColor('var(--text)');
            const height = options.height || 100;

            // Calculate median and max for styling
            const sortedY = [...yData].filter(v => v > 0).sort((a, b) => a - b);
            const median = sortedY.length > 0 ? sortedY[Math.floor(sortedY.length / 2)] : 0;
            const maxY = Math.max(...yData) || 1;

            const opts = {
                width: container.clientWidth || 500,
                height: height,
                cursor: { show: true },
                select: { show: false },
                legend: { show: false },
                scales: {
                    x: { time: true },
                    y: { range: [0, null] }
                },
                axes: [
                    { stroke: textColor, grid: { show: false }, ticks: { show: false }, size: 25, font: '10px system-ui' },
                    { show: false }
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
                plugins: [modalTooltipPlugin(options.valueFormatter)],
                hooks: {
                    draw: [u => {
                        const ctx = u.ctx;
                        ctx.save();
                        const xd = u.data[0], yd = u.data[1];
                        const barWidth = Math.max(6, (u.bbox.width / xd.length) * 0.7);
                        const radius = Math.min(3, barWidth / 3);

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

            return new uPlot(opts, [xData, yData], container);
        }

        // Tooltip plugin for modal charts
        function modalTooltipPlugin(valueFormatter) {
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
                        if (idx == null || !data0 || idx < 0 || idx >= data0.length) { tooltip.style.display = 'none'; return; }
                        const x = data0[idx];
                        const y = data1[idx];
                        if (x === undefined || y === undefined || !Number.isFinite(x)) { tooltip.style.display = 'none'; return; }
                        const d = new Date(x * 1000);
                        const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        const valStr = valueFormatter ? valueFormatter(y) : y;
                        tooltip.innerHTML = `${timeStr} · ${valStr}`;
                        const left = u.valToPos(x, 'x');
                        const top = u.valToPos(y, 'y');
                        tooltip.style.display = 'block';
                        tooltip.style.left = Math.min(left, u.over.clientWidth - 80) + 'px';
                        tooltip.style.top = Math.max(0, top - 40) + 'px';
                    }
                }
            };
        }

        // Build time-based histogram container (renders with uPlot)
        function buildQdHistogram(timestamps, title, unit) {
            if (!timestamps || timestamps.length === 0) return '';
            const times = timestamps.map(t => new Date(t).getTime()).filter(t => !isNaN(t)).sort((a,b) => a - b);
            if (times.length === 0) return '';

            const buckets = 12;
            const min = times[0], max = times[times.length - 1];
            const range = (max - min) || 1;
            const bucketSize = range / buckets;
            const hist = Array(buckets).fill(0);
            times.forEach(t => {
                const idx = Math.min(Math.floor((t - min) / bucketSize), buckets - 1);
                hist[idx]++;
            });

            const xData = new Float64Array(buckets);
            const yData = new Float64Array(buckets);
            for (let i = 0; i < buckets; i++) {
                xData[i] = (min + (i + 0.5) * bucketSize) / 1000;
                yData[i] = hist[i];
            }

            const containerId = 'modal-chart-' + (++modalChartCounter);
            modalChartsData.set(containerId, {
                xData, yData,
                color: 'var(--primary)',
                height: 100,
                valueFormatter: v => v + ' queries'
            });

            return `<div id="${containerId}" style="min-height: 100px; margin-top: 0.5rem;"></div>`;
        }

        // Build cumulative time histogram container
        function buildQdCumulativeTimeHistogram(execs) {
            if (!execs || execs.length === 0) return '';
            const times = execs.map(e => ({ ts: new Date(e.timestamp).getTime(), dur: parseDurationToMs(e.duration) }))
                .filter(x => !isNaN(x.ts) && x.dur > 0).sort((a,b) => a.ts - b.ts);
            if (times.length === 0) return '';

            const buckets = 12;
            const min = times[0].ts, max = times[times.length - 1].ts;
            const range = (max - min) || 1;
            const bucketSize = range / buckets;
            const hist = Array(buckets).fill(0);
            times.forEach(t => {
                const idx = Math.min(Math.floor((t.ts - min) / bucketSize), buckets - 1);
                hist[idx] += t.dur;
            });

            const xData = new Float64Array(buckets);
            const yData = new Float64Array(buckets);
            for (let i = 0; i < buckets; i++) {
                xData[i] = (min + (i + 0.5) * bucketSize) / 1000;
                yData[i] = hist[i];
            }

            const containerId = 'modal-chart-' + (++modalChartCounter);
            modalChartsData.set(containerId, {
                xData, yData,
                color: 'var(--accent)',
                height: 100,
                valueFormatter: fmtMsLong
            });

            return `
                <div style="font-size: 0.7rem; color: var(--text-muted); margin: 0.75rem 0 0.25rem;">Cumulative time</div>
                <div id="${containerId}" style="min-height: 100px;"></div>
            `;
        }

        // Build duration distribution (horizontal bars - keep as HTML for categories)
        function buildQdDurationDistribution(execs) {
            const durations = execs.map(e => parseDurationToMs(e.duration)).filter(d => d > 0);
            if (durations.length === 0) return '';
            const buckets = [
                { label: '< 1 ms', max: 1 },
                { label: '< 10 ms', max: 10 },
                { label: '< 100 ms', max: 100 },
                { label: '< 1 s', max: 1000 },
                { label: '< 10 s', max: 10000 },
                { label: '>= 10 s', max: Infinity }
            ];
            const counts = buckets.map(() => 0);
            durations.forEach(d => {
                for (let i = 0; i < buckets.length; i++) {
                    if (d < buckets[i].max) { counts[i]++; break; }
                }
            });
            const maxVal = Math.max(...counts);
            let html = '<div style="font-size: 0.7rem; color: var(--text-muted); margin: 0.75rem 0 0.25rem;">Duration distribution</div>';
            html += '<div style="display: flex; flex-direction: column; gap: 4px;">';
            for (let i = 0; i < buckets.length; i++) {
                const pct = maxVal > 0 ? (counts[i] / maxVal * 100) : 0;
                html += '<div style="display: flex; align-items: center; gap: 8px; font-size: 0.75rem;">';
                html += '<span style="width: 60px; text-align: right; color: var(--text-muted);">' + buckets[i].label + '</span>';
                html += '<div style="flex: 1; height: 18px; background: var(--bg-tertiary); border-radius: 4px; overflow: hidden;">';
                html += '<div style="width: ' + pct + '%; height: 100%; background: var(--chart-bar); border-radius: 4px;"></div>';
                html += '</div>';
                html += '<span style="width: 70px; text-align: right;">' + (counts[i] > 0 ? fmt(counts[i]) + ' queries' : '-') + '</span>';
                html += '</div>';
            }
            html += '</div>';
            return html;
        }

        // Build temp files histograms
        function buildQdTempFilesHistogram(events) {
            if (!events || events.length === 0) return '';
            const data = events.map(e => ({ ts: new Date(e.timestamp).getTime(), size: parseSizeToBytes(e.size) }))
                .filter(x => !isNaN(x.ts)).sort((a,b) => a.ts - b.ts);
            if (data.length === 0) return '';

            const buckets = 12;
            const min = data[0].ts, max = data[data.length - 1].ts;
            const range = (max - min) || 1;
            const bucketSize = range / buckets;
            const sizeHist = Array(buckets).fill(0);
            const countHist = Array(buckets).fill(0);
            data.forEach(d => {
                const idx = Math.min(Math.floor((d.ts - min) / bucketSize), buckets - 1);
                sizeHist[idx] += d.size;
                countHist[idx]++;
            });

            // Size chart
            const sizeXData = new Float64Array(buckets);
            const sizeYData = new Float64Array(buckets);
            for (let i = 0; i < buckets; i++) {
                sizeXData[i] = (min + (i + 0.5) * bucketSize) / 1000;
                sizeYData[i] = sizeHist[i];
            }
            const sizeContainerId = 'modal-chart-' + (++modalChartCounter);
            modalChartsData.set(sizeContainerId, {
                xData: sizeXData, yData: sizeYData,
                color: 'var(--success)',
                height: 100,
                valueFormatter: fmtBytes
            });

            // Count chart
            const countXData = new Float64Array(buckets);
            const countYData = new Float64Array(buckets);
            for (let i = 0; i < buckets; i++) {
                countXData[i] = (min + (i + 0.5) * bucketSize) / 1000;
                countYData[i] = countHist[i];
            }
            const countContainerId = 'modal-chart-' + (++modalChartCounter);
            modalChartsData.set(countContainerId, {
                xData: countXData, yData: countYData,
                color: 'var(--success)',
                height: 100,
                valueFormatter: v => v + ' files'
            });

            return `
                <div style="font-size: 0.7rem; color: var(--text-muted); margin: 0.5rem 0 0.25rem;">Temp files size</div>
                <div id="${sizeContainerId}" style="min-height: 100px;"></div>
                <div style="font-size: 0.7rem; color: var(--text-muted); margin: 0.75rem 0 0.25rem;">Temp files count</div>
                <div id="${countContainerId}" style="min-height: 100px;"></div>
            `;
        }

        function formatTimeShort(d) {
            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
        }

        function fmtMsLong(ms) {
            if (ms == null || isNaN(ms)) return '-';
            if (ms < 1000) return ms.toFixed(0) + 'ms';
            if (ms < 60000) return (ms / 1000).toFixed(2) + 's';
            if (ms < 3600000) return Math.floor(ms / 60000) + 'm ' + Math.round((ms % 60000) / 1000) + 's';
            const h = Math.floor(ms / 3600000);
            const m = Math.floor((ms % 3600000) / 60000);
            const s = Math.round((ms % 60000) / 1000);
            if (h < 24) return h + 'h ' + m + 'm ' + s + 's';
            const d = Math.floor(h / 24);
            return d + 'd ' + (h % 24) + 'h ' + m + 'm';
        }

        function parseDurationToMs(dur) {
            if (!dur || typeof dur !== 'string') return 0;
            let ms = 0;
            const hMatch = dur.match(/(\d+)\s*h/);
            const mMatch = dur.match(/(\d+)\s*m(?!s)/);
            const sMatch = dur.match(/([\d.]+)\s*s(?![\d])/);
            const msMatch = dur.match(/([\d.]+)\s*ms/);
            if (hMatch) ms += parseInt(hMatch[1]) * 3600000;
            if (mMatch) ms += parseInt(mMatch[1]) * 60000;
            if (sMatch) ms += parseFloat(sMatch[1]) * 1000;
            if (msMatch) ms += parseFloat(msMatch[1]);
            return ms;
        }

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

        function formatSQL(sql) {
            if (!sql) return '';
            // Better SQL formatting with indentation
            var s = esc(sql);
            // Major clause keywords - new line, no indent
            var majorKW = ['SELECT', 'FROM', 'WHERE', 'GROUP BY', 'ORDER BY', 'HAVING', 'LIMIT', 'OFFSET', 'INSERT INTO', 'UPDATE', 'DELETE FROM', 'SET', 'VALUES', 'RETURNING'];
            // Sub-clause keywords - new line, with indent
            var subKW = ['AND', 'OR', 'LEFT JOIN', 'RIGHT JOIN', 'INNER JOIN', 'OUTER JOIN', 'CROSS JOIN', 'JOIN', 'ON', 'USING'];
            // Add newlines before major keywords
            majorKW.forEach(function(kw) {
                var re = new RegExp('\\s+(' + kw + ')\\b', 'gi');
                s = s.replace(re, '\n$1');
            });
            // Add newlines + indent before sub-clause keywords
            subKW.forEach(function(kw) {
                var re = new RegExp('\\s+(' + kw + ')\\b', 'gi');
                s = s.replace(re, '\n    $1');
            });
            // Clean up
            s = s.replace(/^\n/, '').replace(/\n\n+/g, '\n');
            // Highlight keywords in blue
            var allKW = majorKW.concat(subKW);
            allKW.forEach(function(kw) {
                var re = new RegExp('\\b(' + kw + ')\\b', 'gi');
                s = s.replace(re, '<span style="color:#569cd6;">$1</span>');
            });
            // Highlight placeholders ($1, $2, etc) in orange
            s = s.replace(/(\$\d+)/g, '<span style="color:#ce9178;">$1</span>');
            return s;
        }

        document.getElementById('queryModal').addEventListener('click', e => {
            if (e.target.id === 'queryModal') closeModal();
        });

        window.resetAnalysis = function() {
            // Reset state
            currentFilters = {};
            appliedFilters = {};
            currentFileInfo = null;
            fileInput.value = '';
            // Open file picker directly
            fileInput.click();
        };

        // State
        let currentFilters = {};
        let appliedFilters = {}; // Filters actually applied to the data
        let currentFileInfo = null;
        let availableDimensions = { databases: [], users: [], applications: [], hosts: [] };
        let openDropdown = null;

        // ===== Filter Bar Functions =====

        function showFilterBar() {
            document.getElementById('filterBar')?.classList.add('active');
        }

        function hideFilterBar() {
            document.getElementById('filterBar')?.classList.remove('active');
        }

        function initFilterBar(data, isInitial = false) {
            const extractNames = (arr) => (arr || []).map(item => item.name || item);

            if (isInitial || !originalDimensions) {
                originalDimensions = {
                    databases: extractNames(data.databases),
                    users: extractNames(data.users),
                    applications: extractNames(data.apps),
                    hosts: extractNames(data.hosts),
                    timeRange: data.summary?.time_range || null
                };
            }

            availableDimensions = originalDimensions;

            // Populate dropdowns
            populateDropdown('database', availableDimensions.databases);
            populateDropdown('user', availableDimensions.users);
            populateDropdown('application', availableDimensions.applications);
            populateDropdown('host', availableDimensions.hosts);

            // Set time range
            if (isInitial) {
                initTimeFilter(data.summary?.start_date, data.summary?.end_date);
            }

            updateAllDropdownTriggers();
            showFilterBar();
        }

        // Time filter: slider for ≤24h, pickers for >24h
        let timeFilterMode = 'slider'; // 'slider' or 'pickers'
        let timeFilterStartTs = null; // Start timestamp for slider offset conversion
        let timeFilterEndTs = null;   // End timestamp
        let timeFilterDurationMins = 0;

        function initTimeFilter(startDate, endDate) {
            const slider = document.getElementById('filterTimeSlider');
            const pickers = document.getElementById('filterTimePickers');
            const begin = document.getElementById('filterBegin');
            const end = document.getElementById('filterEnd');

            if (!startDate || !endDate) return;

            // Parse timestamps
            const startTs = new Date(startDate.replace(' ', 'T')).getTime();
            const endTs = new Date(endDate.replace(' ', 'T')).getTime();
            const durationMs = endTs - startTs;
            const durationHours = durationMs / (1000 * 60 * 60);

            if (durationHours <= 24) {
                // Slider mode - offset from start
                timeFilterMode = 'slider';
                timeFilterStartTs = startTs;
                timeFilterEndTs = endTs;
                timeFilterDurationMins = Math.ceil(durationMs / (1000 * 60));

                slider.style.display = 'block';
                pickers.style.display = 'none';

                // Set slider range
                const minSlider = document.getElementById('filterTimeMin');
                const maxSlider = document.getElementById('filterTimeMax');
                minSlider.min = 0;
                minSlider.max = timeFilterDurationMins;
                maxSlider.min = 0;
                maxSlider.max = timeFilterDurationMins;
                minSlider.value = 0;
                maxSlider.value = timeFilterDurationMins;
                minSlider.setAttribute('data-original', '0');
                maxSlider.setAttribute('data-original', String(timeFilterDurationMins));

                // Set day label
                const startDay = startDate.split(' ')[0];
                const endDay = endDate.split(' ')[0];
                if (startDay === endDay) {
                    document.getElementById('filterTimeDay').textContent = formatDateHuman(startDay);
                } else {
                    document.getElementById('filterTimeDay').textContent = formatDateHuman(startDay) + ' – ' + formatDateHuman(endDay);
                }

                // Update display
                updateTimeSlider();

                // Add event listeners
                minSlider.oninput = () => { enforceMinMax(); updateTimeSlider(); updateApplyButton(); updateTimeDropdownTrigger(); };
                maxSlider.oninput = () => { enforceMinMax(); updateTimeSlider(); updateApplyButton(); updateTimeDropdownTrigger(); };
            } else {
                // Pickers mode
                timeFilterMode = 'pickers';
                timeFilterStartTs = null;
                timeFilterEndTs = null;
                slider.style.display = 'none';
                pickers.style.display = 'grid';

                if (begin) {
                    begin.value = startDate.slice(0, 16).replace(' ', 'T');
                    begin.setAttribute('data-original', begin.value);
                }
                if (end) {
                    end.value = endDate.slice(0, 16).replace(' ', 'T');
                    end.setAttribute('data-original', end.value);
                }
            }
        }

        function timeToMinutes(timeStr) {
            const parts = timeStr.split(':');
            return parseInt(parts[0] || 0) * 60 + parseInt(parts[1] || 0);
        }

        function minutesToTime(mins) {
            const h = Math.floor(mins / 60);
            const m = mins % 60;
            return `${h.toString().padStart(2, '0')}:${m.toString().padStart(2, '0')}`;
        }

        function formatDateHuman(dateStr) {
            if (!dateStr) return '';
            const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
            const parts = dateStr.split('-');
            if (parts.length !== 3) return dateStr;
            const day = parseInt(parts[2]);
            const month = months[parseInt(parts[1]) - 1] || parts[1];
            const year = parts[0];
            return `${day} ${month} ${year}`;
        }

        function enforceMinMax() {
            const minSlider = document.getElementById('filterTimeMin');
            const maxSlider = document.getElementById('filterTimeMax');
            const minVal = parseInt(minSlider.value);
            const maxVal = parseInt(maxSlider.value);
            if (minVal > maxVal - 5) {
                minSlider.value = maxVal - 5;
            }
            if (maxVal < minVal + 5) {
                maxSlider.value = minVal + 5;
            }
        }

        function updateTimeSlider() {
            const minSlider = document.getElementById('filterTimeMin');
            const maxSlider = document.getElementById('filterTimeMax');
            const range = document.getElementById('filterTimeRange');
            const label = document.getElementById('filterTimeLabel');

            const minVal = parseInt(minSlider.value);
            const maxVal = parseInt(maxSlider.value);
            const maxRange = parseInt(maxSlider.max) || 1440;
            const minPercent = (minVal / maxRange) * 100;
            const maxPercent = (maxVal / maxRange) * 100;

            range.style.left = minPercent + '%';
            range.style.width = (maxPercent - minPercent) + '%';

            // Convert offset to actual time
            const startTime = offsetToTimeStr(minVal);
            const endTime = offsetToTimeStr(maxVal);
            label.textContent = startTime + ' – ' + endTime;
        }

        function offsetToTimeStr(offsetMins) {
            if (!timeFilterStartTs) return minutesToTime(offsetMins);
            const ts = new Date(timeFilterStartTs + offsetMins * 60 * 1000);
            return ts.getHours().toString().padStart(2, '0') + ':' + ts.getMinutes().toString().padStart(2, '0');
        }

        function offsetToDatetime(offsetMins) {
            if (!timeFilterStartTs) return null;
            const ts = new Date(timeFilterStartTs + offsetMins * 60 * 1000);
            const y = ts.getFullYear();
            const m = (ts.getMonth() + 1).toString().padStart(2, '0');
            const d = ts.getDate().toString().padStart(2, '0');
            const hh = ts.getHours().toString().padStart(2, '0');
            const mm = ts.getMinutes().toString().padStart(2, '0');
            // Go expects format "2006-01-02T15:04" (with T, no seconds)
            return `${y}-${m}-${d}T${hh}:${mm}`;
        }

        function populateDropdown(category, values) {
            const list = document.getElementById(`dropdownList-${category}`);
            if (!list) return;

            if (!values || values.length === 0) {
                list.innerHTML = '<div class="filter-dropdown-empty">No data</div>';
                return;
            }

            list.innerHTML = values.map(v => {
                const name = typeof v === 'object' ? v.name : v;
                const selected = currentFilters[category]?.includes(name) ? 'selected' : '';
                return `<div class="filter-dropdown-item ${selected}" data-value="${esc(name)}" onclick="toggleFilterValue('${category}', '${esc(name)}', this)">
                    <input type="checkbox" class="filter-item-checkbox" ${selected ? 'checked' : ''} tabindex="-1">
                    <span class="filter-dropdown-item-label" title="${esc(name)}">${esc(name)}</span>
                </div>`;
            }).join('');
            updateToggleAllCheckbox(category);
        }

        window.toggleDropdown = function(category) {
            const dropdown = document.querySelector(`.filter-dropdown[data-category="${category}"]`);
            if (!dropdown) return;

            const wasOpen = dropdown.classList.contains('open');

            // Close all dropdowns
            closeAllDropdowns();

            // Toggle this one
            if (!wasOpen) {
                dropdown.classList.add('open');
                openDropdown = category;
                // Focus search if present
                const search = dropdown.querySelector('.filter-dropdown-search');
                if (search) setTimeout(() => search.focus(), 10);
            }
        };

        function closeAllDropdowns() {
            document.querySelectorAll('.filter-dropdown.open').forEach(d => d.classList.remove('open'));
            // Clear search inputs
            document.querySelectorAll('.filter-dropdown-search').forEach(s => {
                s.value = '';
                const category = s.closest('.filter-dropdown')?.dataset.category;
                if (category) searchDropdown(category, '');
            });
            openDropdown = null;
        }

        window.searchDropdown = function(category, query) {
            const list = document.getElementById(`dropdownList-${category}`);
            if (!list) return;

            const items = list.querySelectorAll('.filter-dropdown-item');
            const q = query.toLowerCase();

            items.forEach(item => {
                const value = item.dataset.value.toLowerCase();
                item.style.display = value.includes(q) ? '' : 'none';
            });
        };

        window.toggleFilterValue = function(category, value, element) {
            // Toggle selection
            if (!currentFilters[category]) currentFilters[category] = [];

            const idx = currentFilters[category].indexOf(value);
            if (idx >= 0) {
                currentFilters[category].splice(idx, 1);
                if (currentFilters[category].length === 0) delete currentFilters[category];
                element?.classList.remove('selected');
                const cb = element?.querySelector('.filter-item-checkbox');
                if (cb) cb.checked = false;
            } else {
                currentFilters[category].push(value);
                element?.classList.add('selected');
                const cb = element?.querySelector('.filter-item-checkbox');
                if (cb) cb.checked = true;
            }

            updateDropdownTrigger(category);
            updateToggleAllCheckbox(category);
            updateApplyButton();
        };

        window.toggleAllFilterValues = function(category, checked) {
            const list = document.getElementById(`dropdownList-${category}`);
            if (!list) return;

            const items = list.querySelectorAll('.filter-dropdown-item');
            const values = Array.from(items).map(item => item.dataset.value);

            if (checked) {
                // Select all
                currentFilters[category] = [...values];
                items.forEach(item => {
                    item.classList.add('selected');
                    const cb = item.querySelector('.filter-item-checkbox');
                    if (cb) cb.checked = true;
                });
            } else {
                // Deselect all
                delete currentFilters[category];
                items.forEach(item => {
                    item.classList.remove('selected');
                    const cb = item.querySelector('.filter-item-checkbox');
                    if (cb) cb.checked = false;
                });
            }

            updateDropdownTrigger(category);
            updateApplyButton();
        };

        function updateToggleAllCheckbox(category) {
            const dropdown = document.querySelector(`.filter-dropdown[data-category="${category}"]`);
            const checkbox = dropdown?.querySelector('.filter-dropdown-toggle-all');
            const list = document.getElementById(`dropdownList-${category}`);
            if (!checkbox || !list) return;

            const items = list.querySelectorAll('.filter-dropdown-item');
            const selectedCount = currentFilters[category]?.length || 0;

            checkbox.checked = selectedCount === items.length && items.length > 0;
            checkbox.indeterminate = selectedCount > 0 && selectedCount < items.length;
        }

        window.applyTimeFilter = function() {
            closeAllDropdowns();
            updateApplyButton();
        };

        function updateDropdownTrigger(category) {
            const dropdown = document.querySelector(`.filter-dropdown[data-category="${category}"]`);
            if (!dropdown) return;

            const trigger = dropdown.querySelector('.filter-dropdown-trigger');
            const countEl = dropdown.querySelector('.filter-dropdown-count');
            const count = currentFilters[category]?.length || 0;

            if (count > 0) {
                dropdown.classList.add('has-selection');
                trigger.classList.add('has-selection');
                countEl.textContent = `(${count})`;
            } else {
                dropdown.classList.remove('has-selection');
                trigger.classList.remove('has-selection');
                countEl.textContent = '';
            }
        }

        window.clearCategoryFilter = function(category) {
            // Clear this category's filters
            delete currentFilters[category];

            // Update UI - unselect items in this dropdown
            const list = document.getElementById(`dropdownList-${category}`);
            if (list) {
                list.querySelectorAll('.filter-dropdown-item.selected').forEach(item => {
                    item.classList.remove('selected');
                });
            }

            updateDropdownTrigger(category);
            updateApplyButton();
        };

        function updateAllDropdownTriggers() {
            ['database', 'user', 'application', 'host'].forEach(updateDropdownTrigger);
            updateTimeDropdownTrigger();
            updateApplyButton();
        }

        function updateTimeDropdownTrigger() {
            const dropdown = document.querySelector('.filter-dropdown[data-category="time"]');
            if (!dropdown) return;

            const trigger = dropdown.querySelector('.filter-dropdown-trigger');
            const countEl = dropdown.querySelector('.filter-dropdown-count');
            const hasTimeFilter = hasTimeFilterChanged();

            if (hasTimeFilter) {
                trigger.classList.add('has-selection');
                countEl.textContent = '(1)';
            } else {
                trigger.classList.remove('has-selection');
                countEl.textContent = '';
            }
        }

        function hasTimeFilterChanged() {
            if (timeFilterMode === 'slider') {
                const minSlider = document.getElementById('filterTimeMin');
                const maxSlider = document.getElementById('filterTimeMax');
                const minOrig = minSlider?.getAttribute('data-original') || '0';
                const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
                return minSlider?.value !== minOrig || maxSlider?.value !== maxOrig;
            } else {
                const begin = document.getElementById('filterBegin');
                const end = document.getElementById('filterEnd');
                const beginOrig = begin?.getAttribute('data-original') || '';
                const endOrig = end?.getAttribute('data-original') || '';
                return (begin?.value || '') !== beginOrig || (end?.value || '') !== endOrig;
            }
        }

        function filtersHaveChanged() {
            // Check if currentFilters differ from appliedFilters
            const currentKeys = Object.keys(currentFilters);
            const appliedKeys = Object.keys(appliedFilters).filter(k => !k.startsWith('_'));

            // Check category filters
            if (currentKeys.length !== appliedKeys.length) return true;
            for (const key of currentKeys) {
                if (!appliedFilters[key]) return true;
                const curr = [...currentFilters[key]].sort();
                const appl = [...appliedFilters[key]].sort();
                if (curr.length !== appl.length) return true;
                for (let i = 0; i < curr.length; i++) {
                    if (curr[i] !== appl[i]) return true;
                }
            }

            // Check time filters
            let currBegin = null, currEnd = null;

            if (timeFilterMode === 'slider') {
                const minSlider = document.getElementById('filterTimeMin');
                const maxSlider = document.getElementById('filterTimeMax');
                const minOrig = minSlider?.getAttribute('data-original') || '0';
                const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
                const minVal = minSlider?.value || '0';
                const maxVal = maxSlider?.value || String(timeFilterDurationMins);

                if (minVal !== minOrig) {
                    currBegin = offsetToDatetime(parseInt(minVal));
                }
                if (maxVal !== maxOrig) {
                    currEnd = offsetToDatetime(parseInt(maxVal));
                }
            } else {
                const begin = document.getElementById('filterBegin');
                const end = document.getElementById('filterEnd');
                const beginOrig = begin?.getAttribute('data-original') || '';
                const endOrig = end?.getAttribute('data-original') || '';
                const beginVal = begin?.value || '';
                const endVal = end?.value || '';

                currBegin = beginVal !== beginOrig ? beginVal : null;
                currEnd = endVal !== endOrig ? endVal : null;
            }

            if (currBegin !== (appliedFilters._begin || null)) return true;
            if (currEnd !== (appliedFilters._end || null)) return true;

            return false;
        }

        function updateApplyButton() {
            const applyBtn = document.getElementById('filterApply');
            const clearBtn = document.getElementById('filterClear');
            if (!applyBtn) return;

            const hasChanges = filtersHaveChanged();
            applyBtn.classList.toggle('active', hasChanges);
            applyBtn.disabled = !hasChanges;

            // Also update clear button
            const hasAnyFilters = Object.keys(currentFilters).length > 0 || hasTimeFilterChanged();
            clearBtn?.classList.toggle('active', hasAnyFilters);
            if (clearBtn) clearBtn.disabled = !hasAnyFilters;
        }

        window.applyFilters = async function() {
            if (!currentFileContent) return;

            // Build filters object from current UI state
            const filters = { ...currentFilters };

            // Add time range based on mode
            let beginFilter = null, endFilter = null;

            if (timeFilterMode === 'slider') {
                const minSlider = document.getElementById('filterTimeMin');
                const maxSlider = document.getElementById('filterTimeMax');
                const minOrig = minSlider?.getAttribute('data-original') || '0';
                const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
                const minVal = minSlider?.value || '0';
                const maxVal = maxSlider?.value || String(timeFilterDurationMins);

                if (minVal !== minOrig) {
                    beginFilter = offsetToDatetime(parseInt(minVal));
                }
                if (maxVal !== maxOrig) {
                    endFilter = offsetToDatetime(parseInt(maxVal));
                }
            } else {
                const begin = document.getElementById('filterBegin')?.value;
                const end = document.getElementById('filterEnd')?.value;
                const beginOrig = document.getElementById('filterBegin')?.getAttribute('data-original') || '';
                const endOrig = document.getElementById('filterEnd')?.getAttribute('data-original') || '';

                if (begin && begin !== beginOrig) beginFilter = begin.replace('T', ' ');
                if (end && end !== endOrig) endFilter = end.replace('T', ' ');
            }

            if (beginFilter) filters.begin = beginFilter;
            if (endFilter) filters.end = endFilter;

            const hasFilters = Object.keys(filters).length > 0;

            // Store what we're applying for comparison (deep copy arrays)
            appliedFilters = {};
            for (const key of Object.keys(currentFilters)) {
                appliedFilters[key] = [...currentFilters[key]];
            }
            if (beginFilter) appliedFilters._begin = beginFilter;
            else delete appliedFilters._begin;
            if (endFilter) appliedFilters._end = endFilter;
            else delete appliedFilters._end;

            // Close dropdowns
            closeAllDropdowns();

            // Show filtering indicator
            const filterStatus = document.getElementById('filterStatus');
            filterStatus?.classList.add('active');

            await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));

            try {
                const filtersJson = hasFilters ? JSON.stringify(filters) : null;

                // Reinitialize WASM to reset memory (gc=leaking accumulates)
                if (typeof reinitWasm === 'function') {
                    await reinitWasm();
                }

                // Time the parsing
                const parseStart = performance.now();
                const resultJson = quellogParse(currentFileContent, filtersJson);
                const parseEnd = performance.now();
                const parseTimeMs = Math.round(parseEnd - parseStart);

                const data = JSON.parse(resultJson);

                if (data.error) throw new Error(data.error);

                // Store parse time for display
                data._parseTimeMs = parseTimeMs;

                analysisData = data;
                renderResults(data, currentFileName, currentFileSize, false);
                console.log(`[quellog] Filtered: ${data.meta?.entries || 0} entries in ${parseTimeMs}ms`);
            } catch (err) {
                console.error('Filter failed:', err);
            } finally {
                filterStatus?.classList.remove('active');
                updateApplyButton();
            }
        };

        window.clearAllFilters = function() {
            // Reset time inputs based on mode
            if (timeFilterMode === 'slider') {
                const minSlider = document.getElementById('filterTimeMin');
                const maxSlider = document.getElementById('filterTimeMax');
                if (minSlider) minSlider.value = minSlider.getAttribute('data-original') || '0';
                if (maxSlider) maxSlider.value = maxSlider.getAttribute('data-original') || String(timeFilterDurationMins);
                updateTimeSlider();
            } else {
                const begin = document.getElementById('filterBegin');
                const end = document.getElementById('filterEnd');
                if (begin) begin.value = begin.getAttribute('data-original') || '';
                if (end) end.value = end.getAttribute('data-original') || '';
            }

            // Clear selections
            currentFilters = {};

            // Update UI
            document.querySelectorAll('.filter-dropdown-item.selected').forEach(item => {
                item.classList.remove('selected');
            });

            updateAllDropdownTriggers();

            // Apply immediately (clear = apply with no filters)
            if (currentFileContent) {
                applyFilters();
            }
        };

        // Close dropdowns when clicking outside
        document.addEventListener('click', function(e) {
            if (!e.target.closest('.filter-dropdown')) {
                closeAllDropdowns();
            }
        });

        // Close dropdowns with Escape key
        document.addEventListener('keydown', function(e) {
            if (e.key === 'Escape') {
                closeAllDropdowns();
            }
        });

        // Helpers
        function fmt(n) { return n?.toLocaleString() || '0'; }

        // Build no-data message for sections without data
        function buildNoDataMessage(hint) {
            return `
                <div class="no-data-message">
                    <div class="no-data-text">No data available</div>
                    <div class="no-data-hint">Check: ${hint}</div>
                </div>
            `;
        }
        function fmtDuration(ms) {
            if (!ms) return '0ms';
            if (ms < 1000) return ms + 'ms';
            const s = ms / 1000;
            if (s < 60) return s.toFixed(1) + 's';
            const m = Math.floor(s / 60);
            const sec = Math.round(s % 60);
            return m + 'm ' + sec + 's';
        }
        // Safe min/max for large arrays (avoid "too many function arguments" error)
        function safeMax(arr) { return arr.length === 0 ? 0 : arr.reduce((a, b) => a > b ? a : b, arr[0]); }
        function safeMin(arr) { return arr.length === 0 ? 0 : arr.reduce((a, b) => a < b ? a : b, arr[0]); }
        function fmtBytes(b) {
            if (b < 1024) return b + ' B';
            if (b < 1024*1024) return (b/1024).toFixed(1) + ' KB';
            if (b < 1024*1024*1024) return (b/1024/1024).toFixed(1) + ' MB';
            return (b/1024/1024/1024).toFixed(2) + ' GB';
        }
        function fmtMs(ms) {
            if (ms == null) return '-';
            if (typeof ms === 'string') return fmtDur(ms);
            if (ms < 1) return '<1ms';
            if (ms < 1000) return ms.toFixed(1) + 'ms';
            if (ms < 60000) return (ms/1000).toFixed(2) + 's';
            return (ms/60000).toFixed(1) + 'm';
        }
        // Format/clean duration strings from backend (e.g. "2m7.663353305s" -> "2m 7s")
        function fmtDur(s) {
            if (!s || s === '-') return '-';
            if (typeof s !== 'string') return String(s);
            // Check for ms first (msIdx is position where 'ms' starts, so 'm' is at msIdx)
            var msIdx = s.indexOf('ms');
            if (msIdx > 0 && s.indexOf('h') < 0 && s.indexOf('m') === msIdx) {
                // Pure milliseconds (no separate 'm' for minutes)
                var msVal = parseFloat(s);
                return isNaN(msVal) ? s : msVal.toFixed(1) + 'ms';
            }
            // Parse h/m/s components
            var h = 0, m = 0, sec = 0;
            var hIdx = s.indexOf('h');
            var mIdx = s.indexOf('m');
            var sIdx = s.lastIndexOf('s');
            if (hIdx > 0) h = parseInt(s.substring(0, hIdx)) || 0;
            if (mIdx > 0 && mIdx !== msIdx) {
                // 'm' is for minutes only if it's not part of 'ms'
                var mStart = hIdx > 0 ? hIdx + 1 : 0;
                m = parseInt(s.substring(mStart, mIdx)) || 0;
            }
            if (sIdx > 0 && sIdx !== msIdx + 1) {
                // 's' is for seconds only if it's not the 's' in 'ms'
                var sStart = mIdx > 0 && mIdx !== msIdx ? mIdx + 1 : (hIdx > 0 ? hIdx + 1 : 0);
                sec = parseFloat(s.substring(sStart, sIdx)) || 0;
            }
            // Build output
            var parts = [];
            if (h > 0) parts.push(h + 'h');
            if (m > 0) parts.push(m + 'm');
            if (sec > 0) {
                if (parts.length > 0) {
                    parts.push(Math.round(sec) + 's');
                } else {
                    parts.push(sec.toFixed(2) + 's');
                }
            }
            // Handle pure milliseconds that weren't caught above (e.g., "0.5ms")
            if (parts.length === 0 && msIdx > 0) {
                var msVal = parseFloat(s);
                return isNaN(msVal) ? s : msVal.toFixed(1) + 'ms';
            }
            return parts.length > 0 ? parts.join(' ') : s;
        }
        function esc(s) {
            if (!s) return '';
            const d = document.createElement('div');
            d.textContent = s;
            return d.innerHTML;
        }

        // Theme management
        const iconSun = document.getElementById('iconSun');
        const iconMoon = document.getElementById('iconMoon');
        const htmlEl = document.documentElement;
        const THEME_KEY = 'quellog-theme';

        function getPreferredTheme() {
            const saved = localStorage.getItem(THEME_KEY);
            if (saved) return saved;
            return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
        }

        function setTheme(theme) {
            htmlEl.dataset.theme = theme;
            // Show sun in dark mode (click to switch to light), moon in light mode (click to switch to dark)
            iconSun.style.display = theme === 'dark' ? 'block' : 'none';
            iconMoon.style.display = theme === 'light' ? 'block' : 'none';
            localStorage.setItem(THEME_KEY, theme);
        }

        function toggleTheme() {
            const current = htmlEl.dataset.theme || 'light';
            setTheme(current === 'dark' ? 'light' : 'dark');
        }

        // Initialize theme
        setTheme(getPreferredTheme());

        // Listen for system theme changes
        window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', e => {
            if (!localStorage.getItem(THEME_KEY)) {
                setTheme(e.matches ? 'dark' : 'light');
            }
        });

        // Expose functions for inline onclick handlers
        window.showConnTab = showConnTab;
        window.showClientTab = showClientTab;
        window.showQueryModal = showQueryModal;
        window.showSqlOvView = showSqlOvView;
        window.showSqlTab = showSqlTab;
        window.copyQuery = copyQuery;
        window.closeModal = closeModal;
        window.toggleTheme = toggleTheme;
        window.closeChartModal = closeChartModal;
        window.updateModalInterval = updateModalInterval;
        window.resetModalZoom = resetModalZoom;
        window.exportChartPNG = exportChartPNG;
window.openTab = function(evt, tabName) {
    const btn = evt.currentTarget;
    const container = btn.closest('.section-body') || document;
    
    // Hide all tab content in this container
    const contents = container.querySelectorAll('.tab-content');
    for (let i = 0; i < contents.length; i++) {
        contents[i].style.display = 'none';
    }

    // Reset tabs (remove active)
    // We search in the button's parent (the toolbar/tabs container) to be safe
    const tabs = btn.parentElement.querySelectorAll('.tab');
    for (let i = 0; i < tabs.length; i++) {
        tabs[i].classList.remove('active');
    }

    // Activate target content
    const target = document.getElementById(tabName);
    if (target) target.style.display = 'block';

    // Activate button
    btn.classList.add('active');
}

