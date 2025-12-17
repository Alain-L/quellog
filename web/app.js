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
        const chartBucketsMap = new Map();  // Per-chart bucket count
        const defaultBuckets = 30;

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
                        const { idx } = u.cursor;
                        if (idx == null) {
                            tooltip.style.display = 'none';
                            return;
                        }
                        const x = u.data[0][idx];
                        const y = u.data[1][idx];
                        const d = new Date(x * 1000);
                        const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        tooltip.innerHTML = `<div class="tt-time">${timeStr}</div><div class="tt-value">${y} events</div>`;
                        const left = u.valToPos(x, 'x');
                        const top = u.valToPos(y, 'y');
                        tooltip.style.display = 'block';
                        tooltip.style.left = Math.min(left, u.over.clientWidth - 100) + 'px';
                        tooltip.style.top = Math.max(0, top - 40) + 'px';
                    }
                }
            };
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

            const buckets = options.buckets || chartBucketsMap.get(containerId) || defaultBuckets;
            const minT = times[0];
            const maxT = times[times.length - 1];
            const range = maxT - minT || 1;
            const bucketSize = range / buckets;

            // Build histogram data
            const xData = [];
            const yData = [];
            for (let i = 0; i < buckets; i++) {
                xData.push(minT + i * bucketSize + bucketSize / 2);
                yData.push(0);
            }
            times.forEach(t => {
                const idx = Math.min(Math.floor((t - minT) / bucketSize), buckets - 1);
                yData[idx]++;
            });

            // Color from CSS variable
            const color = options.color || getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim() || '#5a9bd5';

            // Range selection callback for filtering
            const onSelect = options.onSelect || null;

            const opts = {
                width: container.clientWidth || 300,
                height: options.height || 120,
                cursor: { drag: { x: true, y: false, setScale: true } },
                select: { show: true },
                scales: {
                    x: { time: true },
                    y: { range: [0, null] }
                },
                axes: [
                    {
                        stroke: getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim(),
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
                        show: false
                    }
                ],
                series: [
                    {},
                    {
                        fill: color,
                        stroke: color,
                        width: 0,
                        points: { show: false },
                        paths: (u, seriesIdx, idx0, idx1) => {
                            const { left, top, width, height } = u.bbox;
                            const d = u.data;
                            const xd = d[0];
                            const yd = d[seriesIdx];
                            const barWidth = Math.max(2, (width / xd.length) * 0.8);
                            const p = new Path2D();
                            for (let i = idx0; i <= idx1; i++) {
                                const x = u.valToPos(xd[i], 'x', true);
                                const y = u.valToPos(yd[i], 'y', true);
                                const y0 = u.valToPos(0, 'y', true);
                                p.rect(x - barWidth/2, y, barWidth, y0 - y);
                            }
                            return { fill: p };
                        }
                    }
                ],
                plugins: [tooltipPlugin()],
                hooks: {
                    setSelect: [u => {
                        if (onSelect && u.select.width > 10) {
                            const minX = u.posToVal(u.select.left, 'x');
                            const maxX = u.posToVal(u.select.left + u.select.width, 'x');
                            onSelect(new Date(minX * 1000), new Date(maxX * 1000));
                        }
                    }]
                }
            };

            const chart = new uPlot(opts, [xData, yData], container);
            charts.set(containerId, chart);

            // Store original range for reset
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

            const color = options.color || getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#5a9bd5';

            // Tooltip plugin for histogram
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
                            const { idx } = u.cursor;
                            if (idx == null || idx >= histogram.length) {
                                tooltip.style.display = 'none';
                                return;
                            }
                            const h = histogram[idx];
                            const y = u.data[1][idx];
                            tooltip.innerHTML = `<div class="tt-time">${h.label}</div><div class="tt-value">${y} sessions</div>${h.peak_time ? `<div class="tt-peak">peak: ${h.peak_time}</div>` : ''}`;
                            const left = u.valToPos(u.data[0][idx], 'x');
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
                scales: {
                    x: { time: true },
                    y: { range: [0, null] }
                },
                axes: [
                    {
                        stroke: getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim(),
                        grid: { show: false },
                        ticks: { show: false },
                        values: (u, vals) => vals.map(v => {
                            const d = new Date(v * 1000);
                            return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        }),
                        size: 20,
                        font: '10px system-ui'
                    },
                    { show: false }
                ],
                series: [
                    {},
                    {
                        fill: color,
                        stroke: color,
                        width: 0,
                        points: { show: false },
                        paths: (u, seriesIdx, idx0, idx1) => {
                            const { left, top, width, height } = u.bbox;
                            const d = u.data;
                            const xd = d[0];
                            const yd = d[seriesIdx];
                            const barWidth = Math.max(4, (width / xd.length) * 0.8);
                            const p = new Path2D();
                            for (let i = idx0; i <= idx1; i++) {
                                const x = u.valToPos(xd[i], 'x', true);
                                const y = u.valToPos(yd[i], 'y', true);
                                const y0 = u.valToPos(0, 'y', true);
                                p.rect(x - barWidth/2, y, barWidth, y0 - y);
                            }
                            return { fill: p };
                        }
                    }
                ],
                plugins: [tooltipPlugin()]
            };

            const chart = new uPlot(opts, [xData, yData], container);
            charts.set(containerId, chart);

            // Store original range for reset
            chart._originalXRange = [xData[0], xData[xData.length - 1]];

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

            const buckets = options.buckets || chartBucketsMap.get(containerId) || defaultBuckets;

            // Parse session events
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
            const range = maxT - minT || 1;

            // Limit buckets to ensure minimum 1 minute per bucket
            const minBucketDuration = 60; // 1 minute in seconds
            const maxBuckets = Math.max(1, Math.floor(range / minBucketDuration));
            const effectiveBuckets = Math.min(buckets, maxBuckets);
            const bucketSize = range / effectiveBuckets;

            // Sweep-line: compute max concurrent per bucket
            const xData = [];
            const yData = [];
            for (let i = 0; i < effectiveBuckets; i++) {
                xData.push(minT + i * bucketSize + bucketSize / 2);
                yData.push(0);
            }

            let current = 0;
            let eventIdx = 0;
            for (let b = 0; b < effectiveBuckets; b++) {
                const bucketStart = (minT + b * bucketSize) * 1000;
                const bucketEnd = (minT + (b + 1) * bucketSize) * 1000;
                let maxInBucket = current;

                while (eventIdx < events.length && events[eventIdx].time < bucketEnd) {
                    const e = events[eventIdx];
                    if (e.time < bucketStart) {
                        current += e.delta;
                    } else {
                        current += e.delta;
                        if (current > maxInBucket) maxInBucket = current;
                    }
                    eventIdx++;
                }
                yData[b] = Math.max(maxInBucket, 0);
            }

            const color = options.color || getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#5a9bd5';

            // Tooltip
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
                            const { idx } = u.cursor;
                            if (idx == null) { tooltip.style.display = 'none'; return; }
                            const x = u.data[0][idx];
                            const y = u.data[1][idx];
                            const d = new Date(x * 1000);
                            const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                            tooltip.innerHTML = `<div class="tt-time">${timeStr}</div><div class="tt-value">${y} sessions</div>`;
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
                scales: { x: { time: true }, y: { range: [0, null] } },
                axes: [
                    {
                        stroke: getComputedStyle(document.documentElement).getPropertyValue('--text-muted').trim(),
                        grid: { show: false }, ticks: { show: false },
                        values: (u, vals) => vals.map(v => new Date(v * 1000).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })),
                        size: 20, font: '10px system-ui'
                    },
                    { show: false }
                ],
                series: [
                    {},
                    {
                        fill: color, stroke: color, width: 0,
                        points: { show: false },
                        paths: (u, seriesIdx, idx0, idx1) => {
                            const xd = u.data[0], yd = u.data[seriesIdx];
                            const barWidth = Math.max(2, (u.bbox.width / xd.length) * 0.8);
                            const p = new Path2D();
                            for (let i = idx0; i <= idx1; i++) {
                                const x = u.valToPos(xd[i], 'x', true);
                                const y = u.valToPos(yd[i], 'y', true);
                                const y0 = u.valToPos(0, 'y', true);
                                p.rect(x - barWidth/2, y, barWidth, y0 - y);
                            }
                            return { fill: p };
                        }
                    }
                ],
                plugins: [tooltipPlugin()]
            };

            const chart = new uPlot(opts, [xData, yData], container);
            charts.set(containerId, chart);

            const resizeObserver = new ResizeObserver(() => {
                if (container.clientWidth > 0) chart.setSize({ width: container.clientWidth, height: opts.height });
            });
            resizeObserver.observe(container);

            return chart;
        }

        // Build chart container HTML with controls
        function buildChartContainer(id, title, options = {}) {
            const showBucketControl = options.showBucketControl !== false;
            const showFilterBtn = options.showFilterBtn === true;
            const currentBuckets = chartBucketsMap.get(id) || defaultBuckets;
            return `
                <div class="chart-container">
                    <div class="chart-controls">
                        <span class="subsection-title" style="margin: 0; font-size: 0.7rem;">${title}</span>
                        <div style="display: flex; gap: 0.5rem; align-items: center;">
                            <span class="zoom-hint">drag to zoom</span>
                            ${showBucketControl ? `
                                <select onchange="updateChartBuckets('${id}', this.value)">
                                    <option value="15" ${currentBuckets === 15 ? 'selected' : ''}>15 buckets</option>
                                    <option value="30" ${currentBuckets === 30 ? 'selected' : ''}>30 buckets</option>
                                    <option value="60" ${currentBuckets === 60 ? 'selected' : ''}>60 buckets</option>
                                    <option value="120" ${currentBuckets === 120 ? 'selected' : ''}>120 buckets</option>
                                </select>
                            ` : ''}
                            <button onclick="resetChartZoom('${id}')">Reset</button>
                            ${showFilterBtn ? `<button onclick="filterFromChart('${id}')" title="Filter data to selected range">Filter</button>` : ''}
                            <button class="btn-expand" onclick="openChartModal('${id}', '${title.replace(/'/g, "\\'")}')" title="Expand chart">⛶</button>
                        </div>
                    </div>
                    <div id="${id}" style="min-height: 120px;"></div>
                </div>
            `;
        }

        // Update chart with new bucket count
        function updateChartBuckets(chartId, buckets) {
            const numBuckets = parseInt(buckets);
            chartBucketsMap.set(chartId, numBuckets);

            // Recreate only this specific chart
            const data = chartData.get(chartId);
            if (data) {
                const color = chartId.includes('tempfiles') ? getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() : null;
                if (data?.type === 'sessions') {
                    createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)', buckets: numBuckets });
                } else if (data?.type === 'histogram') {
                    // Pre-computed histogram can't change buckets
                } else {
                    createTimeChart(chartId, data, { color, buckets: numBuckets });
                }
            }
        }

        // Reset chart zoom
        function resetChartZoom(chartId) {
            const chart = charts.get(chartId);
            if (chart && chart._originalXRange) {
                const [min, max] = chart._originalXRange;
                chart.setScale('x', { min, max });
            }
        }

        // Store chart data for re-creation
        const chartData = new Map();

        // Filter from chart selection
        function filterFromChart(chartId) {
            const chart = charts.get(chartId);
            if (!chart || chart.select.width < 10) return;

            const minX = chart.posToVal(chart.select.left, 'x');
            const maxX = chart.posToVal(chart.select.left + chart.select.width, 'x');

            // Set filter inputs
            const beginInput = document.getElementById('filterBegin');
            const endInput = document.getElementById('filterEnd');
            if (beginInput && endInput) {
                beginInput.value = new Date(minX * 1000).toISOString().slice(0, 16);
                endInput.value = new Date(maxX * 1000).toISOString().slice(0, 16);
                // Apply filters
                applyFilters();
            }
        }

        // Modal state
        let modalChart = null;
        let modalChartId = null;
        let modalBuckets = 30;

        // Open chart in modal
        function openChartModal(chartId, title) {
            const data = chartData.get(chartId);
            if (!data) return;

            modalChartId = chartId;
            modalBuckets = chartBucketsMap.get(chartId) || defaultBuckets;

            document.getElementById('modalChartTitle').textContent = title;
            document.getElementById('modalBucketSelect').value = modalBuckets;
            document.getElementById('chartModal').classList.add('active');
            document.body.style.overflow = 'hidden';

            // Hide bucket select for pre-computed histograms
            const bucketSelect = document.getElementById('modalBucketSelect');
            bucketSelect.style.display = data?.type === 'histogram' ? 'none' : '';

            // Create expanded chart
            setTimeout(() => renderModalChart(), 50);
        }

        // Render chart in modal
        function renderModalChart() {
            const container = document.getElementById('modal-chart-container');
            container.innerHTML = '';

            const data = chartData.get(modalChartId);
            if (!data) return;

            const color = modalChartId.includes('tempfiles')
                ? getComputedStyle(document.documentElement).getPropertyValue('--accent').trim()
                : null;

            // Create larger chart
            if (data?.type === 'sessions') {
                modalChart = createConcurrentChartLarge(container, data.data, {
                    color: color || 'var(--accent)',
                    buckets: modalBuckets,
                    height: 350
                });
            } else if (data?.type === 'histogram') {
                modalChart = createHistogramChartLarge(container, data.data, {
                    color: color || 'var(--accent)',
                    height: 350
                });
            } else {
                modalChart = createTimeChartLarge(container, data, {
                    color,
                    buckets: modalBuckets,
                    height: 350
                });
            }
        }

        // Create large time chart for modal
        function createTimeChartLarge(container, timestamps, options = {}) {
            if (!timestamps?.length) return null;

            const buckets = options.buckets || modalBuckets;
            const height = options.height || 350;
            const times = timestamps.map(t => new Date(t).getTime()).filter(t => !isNaN(t)).sort((a, b) => a - b);
            if (times.length === 0) return null;

            const min = times[0], max = times[times.length - 1];
            const range = max - min || 1;
            const bucketSize = range / buckets;

            const counts = new Array(buckets).fill(0);
            times.forEach(t => {
                const idx = Math.min(Math.floor((t - min) / bucketSize), buckets - 1);
                counts[idx]++;
            });

            const xData = new Float64Array(buckets);
            const yData = new Float64Array(buckets);
            for (let i = 0; i < buckets; i++) {
                xData[i] = (min + (i + 0.5) * bucketSize) / 1000;
                yData[i] = counts[i];
            }

            // Resolve CSS variable to actual color for canvas
            const resolveColor = (c) => {
                if (c && c.startsWith('var(')) {
                    const varName = c.slice(4, -1);
                    return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
                }
                return c || '#5a9bd5';
            };
            const barColor = resolveColor(options.color) || resolveColor('var(--chart-bar)');
            const textColor = resolveColor('var(--text-muted)');
            const borderColor = resolveColor('var(--border)');

            // Add padding to prevent bars from being cut off
            const xPadding = (xData[xData.length - 1] - xData[0]) / (buckets * 2);
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
                        fill: barColor,
                        stroke: barColor,
                        width: 0,
                        points: { show: false },
                        paths: (u, seriesIdx, idx0, idx1) => {
                            const { width } = u.bbox;
                            const xd = u.data[0], yd = u.data[seriesIdx];
                            const barWidth = Math.max(2, (width / xd.length) * 0.8);
                            const p = new Path2D();
                            for (let i = idx0; i <= idx1; i++) {
                                const x = u.valToPos(xd[i], 'x', true);
                                const y = u.valToPos(yd[i], 'y', true);
                                const y0 = u.valToPos(0, 'y', true);
                                p.rect(x - barWidth/2, y, barWidth, y0 - y);
                            }
                            return { fill: p };
                        }
                    }
                ],
                plugins: [tooltipPlugin()]
            };

            const chart = new uPlot(opts, [xData, yData], container);
            chart._originalXRange = [xMin, xMax];
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
            const points = [];
            let concurrent = 0;
            events.forEach(e => {
                concurrent += e.delta;
                points.push({ time: e.time, value: concurrent });
            });

            const min = points[0].time, max = points[points.length - 1].time;
            const range = (max - min) / 1000;
            const buckets = options.buckets || modalBuckets;
            const minBucketDuration = 60;
            const maxBuckets = Math.max(1, Math.floor(range / minBucketDuration));
            const effectiveBuckets = Math.min(buckets, maxBuckets);
            const bucketSize = (max - min) / effectiveBuckets;

            const maxValues = new Array(effectiveBuckets).fill(0);
            let pi = 0;
            for (let b = 0; b < effectiveBuckets; b++) {
                const bucketEnd = min + (b + 1) * bucketSize;
                while (pi < points.length && points[pi].time <= bucketEnd) {
                    maxValues[b] = Math.max(maxValues[b], points[pi].value);
                    pi++;
                }
                if (b > 0 && maxValues[b] === 0) maxValues[b] = maxValues[b - 1];
            }

            const xData = new Float64Array(effectiveBuckets);
            const yData = new Float64Array(effectiveBuckets);
            for (let i = 0; i < effectiveBuckets; i++) {
                xData[i] = (min + (i + 0.5) * bucketSize) / 1000;
                yData[i] = maxValues[i];
            }

            // Resolve CSS variable to actual color for canvas
            const resolveColor = (c) => {
                if (c && c.startsWith('var(')) {
                    const varName = c.slice(4, -1);
                    return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
                }
                return c || '#5a9bd5';
            };
            const barColor = resolveColor(options.color) || resolveColor('var(--chart-bar)');
            const textColor = resolveColor('var(--text-muted)');
            const borderColor = resolveColor('var(--border)');
            const height = options.height || 350;

            // Add padding to prevent bars from being cut off
            const xPadding = (xData[xData.length - 1] - xData[0]) / (effectiveBuckets * 2);
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
                        fill: barColor,
                        stroke: barColor,
                        width: 0,
                        points: { show: false },
                        paths: (u, seriesIdx, idx0, idx1) => {
                            const { width } = u.bbox;
                            const xd = u.data[0], yd = u.data[seriesIdx];
                            const barWidth = Math.max(2, (width / xd.length) * 0.8);
                            const p = new Path2D();
                            for (let i = idx0; i <= idx1; i++) {
                                const x = u.valToPos(xd[i], 'x', true);
                                const y = u.valToPos(yd[i], 'y', true);
                                const y0 = u.valToPos(0, 'y', true);
                                p.rect(x - barWidth/2, y, barWidth, y0 - y);
                            }
                            return { fill: p };
                        }
                    }
                ],
                plugins: [tooltipPlugin()]
            };

            const chart = new uPlot(opts, [xData, yData], container);
            chart._originalXRange = [xMin, xMax];
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

            // Resolve CSS variable to actual color for canvas
            const resolveColor = (c) => {
                if (c && c.startsWith('var(')) {
                    const varName = c.slice(4, -1);
                    return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || '#5a9bd5';
                }
                return c || '#5a9bd5';
            };
            const barColor = resolveColor(options.color) || resolveColor('var(--chart-bar)');
            const textColor = resolveColor('var(--text-muted)');
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
                        fill: barColor,
                        stroke: barColor,
                        width: 0,
                        points: { show: false },
                        paths: (u, seriesIdx, idx0, idx1) => {
                            const { width } = u.bbox;
                            const xd = u.data[0], yd = u.data[seriesIdx];
                            const barWidth = Math.max(2, (width / xd.length) * 0.8);
                            const p = new Path2D();
                            for (let i = idx0; i <= idx1; i++) {
                                const x = u.valToPos(xd[i], 'x', true);
                                const y = u.valToPos(yd[i], 'y', true);
                                const y0 = u.valToPos(0, 'y', true);
                                p.rect(x - barWidth/2, y, barWidth, y0 - y);
                            }
                            return { fill: p };
                        }
                    }
                ],
                plugins: [tooltipPlugin()]
            };

            const chart = new uPlot(opts, [xData, yData], container);
            chart._originalXRange = [xMin, xMax];
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

        // Update modal buckets
        function updateModalBuckets(buckets) {
            modalBuckets = parseInt(buckets);
            renderModalChart();
        }

        // Reset modal zoom
        function resetModalZoom() {
            if (modalChart && modalChart._originalXRange) {
                const [min, max] = modalChart._originalXRange;
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

            // Check if we have error classes for 4-column layout
            const hasErrors = data.error_classes?.length > 0;

            // Build sections with new layout
            let html = '';

            // Row 1: Summary | Events | Clients | Error Classes (3-4 cols)
            html += `<div class="grid grid-top-row">`;
            html += buildSummarySection(data);
            html += buildEventsSection(data);
            html += buildClientsSection(data);
            if (hasErrors) {
                html += buildErrorClassesSection(data);
            }
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
                    const color = chartId.includes('tempfiles') ? getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() : null;
                    // Check data type: sessions (sweep-line), histogram (pre-computed), or timestamps
                    if (data?.type === 'sessions') {
                        createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)' });
                    } else if (data?.type === 'histogram') {
                        createHistogramChart(chartId, data.data, { color: color || 'var(--accent)' });
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
                    <div class="section-header">Summary</div>
                    <div class="section-body summary-body">
                        <div class="summary-header">
                            <span class="summary-filename">${esc(f.fileName || 'Unknown')} · <span class="summary-date">${dateDisplay}</span></span>
                            <span class="summary-parsetime">parsed in ${parseTimeStr}</span>
                        </div>
                        <div class="summary-separator"></div>
                        <div class="summary-tiles">
                            <div class="summary-tile">
                                <div class="summary-tile-value">${(f.format || '?').toUpperCase()}</div>
                                <div class="summary-tile-label">format</div>
                            </div>
                            <div class="summary-tile">
                                <div class="summary-tile-value">${fmtBytes(f.fileSize || 0)}</div>
                                <div class="summary-tile-label">size</div>
                            </div>
                            <div class="summary-tile">
                                <div class="summary-tile-value">${fmt(s.total_logs)}</div>
                                <div class="summary-tile-label">entries</div>
                            </div>
                            <div class="summary-tile">
                                <div class="summary-tile-value">${formatDuration(fmtDur(s.duration))}</div>
                                <div class="summary-tile-label">duration</div>
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
            // data.events is an array: [{type: 'LOG', count: N, percentage: P}, ...]
            const eventsArr = data.events || [];
            const clsMap = { LOG: 'log', ERROR: 'error', WARNING: 'warning', FATAL: 'fatal', PANIC: 'fatal' };
            const events = eventsArr.map(e => ({
                name: e.type,
                count: e.count,
                cls: clsMap[e.type] || 'log'
            })).filter(x => x.count > 0);
            const max = Math.max(...events.map(x => x.count)) || 1;

            return `
                <div class="section" id="events">
                    <div class="section-header">Events</div>
                    <div class="section-body">
                        <div class="event-bars">
                            ${events.map(ev => `
                                <div class="event-bar">
                                    <span class="label">${ev.name}</span>
                                    <div class="bar-bg">
                                        <div class="bar ${ev.cls}" style="width: ${ev.count/max*100}%"></div>
                                    </div>
                                    <span class="count">${fmt(ev.count)}</span>
                                </div>
                            `).join('')}
                        </div>
                    </div>
                </div>
            `;
        }

        function buildConnectionsSection(data) {
            const c = data.connections;
            if (!c || c.connection_count === 0) {
                return `
                    <div class="section disabled" id="connections">
                        <div class="section-header">Connections</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('Enable connection logging', 'log_connections = on')}
                    </div>
                `;
            }
            const hasSessions = (c.sessions_by_user && Object.keys(c.sessions_by_user).length > 0) ||
                               (c.sessions_by_database && Object.keys(c.sessions_by_database).length > 0);
            // session_distribution is an object: {"< 1s": 123, ...}
            const distEntries = c.session_distribution ? Object.entries(c.session_distribution).filter(([,v]) => v > 0) : [];
            const hasConnections = c.connections?.length > 0;
            const hasConcurrentHist = c.concurrent_sessions_histogram?.length > 0;

            // Store connection timestamps for chart creation
            if (hasConnections) {
                chartData.set('chart-connections', c.connections);
            }
            // Store session events for client-side sweep-line (allows bucket adjustment)
            if (c.session_events?.length > 0) {
                chartData.set('chart-concurrent', { type: 'sessions', data: c.session_events });
            } else if (hasConcurrentHist) {
                // Fallback to pre-computed histogram
                chartData.set('chart-concurrent', { type: 'histogram', data: c.concurrent_sessions_histogram });
            }

            return `
                <div class="section" id="connections">
                    <div class="section-header">Connections</div>
                    <div class="section-body">
                        <div class="stats-row">
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
                                <div class="stat-card accent">
                                    <div class="stat-value">${c.peak_concurrent_sessions}</div>
                                    <div class="stat-label">Peak Concurrent</div>
                                </div>
                            ` : ''}
                        </div>
                        <div class="grid grid-2" style="margin-top: 0.5rem;">
                            ${(c.session_events?.length > 0 || hasConcurrentHist) ? buildChartContainer('chart-concurrent', 'Concurrent Sessions', { showFilterBtn: false }) : ''}
                            ${hasConnections ? buildChartContainer('chart-connections', 'Connection Distribution', { showFilterBtn: true }) : ''}
                        </div>
                        ${distEntries.length > 0 ? `
                            <div class="subsection">
                                <div class="subsection-title">Session Duration Distribution</div>
                                ${buildSessionDistributionChart(distEntries)}
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

        // Build session duration distribution as horizontal bar chart
        function buildSessionDistributionChart(entries) {
            const max = Math.max(...entries.map(([,v]) => v)) || 1;
            return `
                <div style="display: flex; flex-direction: column; gap: 4px; margin-top: 0.5rem;">
                    ${entries.map(([name, count]) => `
                        <div style="display: flex; align-items: center; gap: 8px; font-size: 0.75rem;">
                            <span style="width: 70px; text-align: right; color: var(--text-muted);">${name}</span>
                            <div style="flex: 1; height: 16px; background: var(--bg-tertiary); border-radius: 3px; overflow: hidden;">
                                <div style="width: ${count/max*100}%; height: 100%; background: var(--chart-bar); border-radius: 3px;"></div>
                            </div>
                            <span style="width: 50px; text-align: right;">${fmt(count)}</span>
                        </div>
                    `).join('')}
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
                        <th>${label}</th><th>Sessions</th><th>Min</th><th>Avg</th><th>Median</th><th>Max</th><th>Cumulated</th>
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
                    <div class="section disabled" id="clients">
                        <div class="section-header">Clients</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('No client info in log_line_prefix', '%u, %d, %a in log_line_prefix')}
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
                                <div class="bar"><div class="bar-fill" style="width: ${d.count/max*100}%"></div></div>
                                <span class="value">${fmt(d.count)} <small style="color: var(--text-muted)">(${pct}%)</small></span>
                            </div>
                        `}).join('')}
                    </div>
                `;
            }

            return `
                <div class="section" id="clients">
                    <div class="section-header">Clients</div>
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

        function buildErrorClassesSection(data) {
            const ec = data.error_classes || [];
            if (ec.length === 0) {
                return `
                    <div class="section disabled" id="error_classes">
                        <div class="section-header">Error Classes</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('Enable error classification', '%e in log_line_prefix')}
                    </div>
                `;
            }
            const maxCount = Math.max(...ec.map(e => e.count)) || 1;
            return `
                <div class="section" id="error_classes">
                    <div class="section-header danger">Error Classes <span class="badge">${ec.length} classes</span></div>
                    <div class="section-body">
                        <div class="scroll-list">
                            ${ec.map(e => {
                                const totalErrors = ec.reduce((sum, x) => sum + x.count, 0);
                                const pct = totalErrors > 0 ? (e.count / totalErrors * 100).toFixed(0) : 0;
                                return `
                                <div class="list-item">
                                    <span class="name" style="font-weight: 600; color: var(--danger); min-width: 40px;">-${e.class_code}</span>
                                    <span style="flex: 2; font-size: 0.8rem;">${esc(e.description)}</span>
                                    <div class="bar"><div class="bar-fill" style="width: ${e.count/maxCount*100}%; background: var(--danger);"></div></div>
                                    <span class="value">${fmt(e.count)}× <small style="color: var(--text-muted)">(${pct}%)</small></span>
                                </div>
                            `}).join('')}
                        </div>
                    </div>
                </div>
            `;
        }

        function buildCheckpointsSection(data) {
            const cp = data.checkpoints;
            if (!cp || cp.total_checkpoints === 0) {
                return `
                    <div class="section disabled" id="checkpoints">
                        <div class="section-header">Checkpoints</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('Enable checkpoint logging', 'log_checkpoints = on')}
                    </div>
                `;
            }
            // types is an object: {"time": {count, ...}, "wal": {count, ...}, ...}
            const types = cp.types || {};
            const timed = types.time?.count || 0;
            const wal = types.wal?.count || 0;
            const req = (types.shutdown?.count || 0) + (types['immediate force wait']?.count || 0);
            const hasEvents = cp.events?.length > 0;

            // Store checkpoint timestamps for chart creation
            if (hasEvents) {
                chartData.set('chart-checkpoints', cp.events);
            }

            return `
                <div class="section" id="checkpoints">
                    <div class="section-header">Checkpoints <span class="badge">${cp.total_checkpoints}</span></div>
                    <div class="section-body">
                        <div class="stats-row">
                            <div class="stat-card"><div class="stat-value">${timed}</div><div class="stat-label">Timed</div></div>
                            <div class="stat-card"><div class="stat-value">${wal}</div><div class="stat-label">WAL</div></div>
                            <div class="stat-card"><div class="stat-value">${req}</div><div class="stat-label">Req</div></div>
                        </div>
                        ${hasEvents ? buildChartContainer('chart-checkpoints', 'Checkpoint Distribution', { showFilterBtn: false }) : ''}
                        <div style="font-size: 0.8rem; color: var(--text-muted); margin-top: 0.5rem;">
                            Avg: ${cp.avg_checkpoint_time} | Max: ${cp.max_checkpoint_time}
                        </div>
                    </div>
                </div>
            `;
        }

        function buildMaintenanceSection(data) {
            const m = data.maintenance;
            if (!m || ((m.vacuum_count || 0) + (m.analyze_count || 0)) === 0) {
                return `
                    <div class="section disabled" id="maintenance">
                        <div class="section-header">Maintenance</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('Enable autovacuum logging', 'log_autovacuum_min_duration = 0')}
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
                    <div class="section-header">Maintenance</div>
                    <div class="section-body">
                        <div class="stats-row">
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
                    <div class="section disabled" id="locks">
                        <div class="section-header">Locks</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('Enable lock wait logging', 'log_lock_waits = on')}
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
                    <div class="section-header ${deadlocks > 0 ? 'danger' : ''}">Locks</div>
                    <div class="section-body">
                        <div class="stats-row">
                            <div class="stat-card ${deadlocks > 0 ? 'danger' : ''}"><div class="stat-value">${deadlocks}</div><div class="stat-label">Deadlocks</div></div>
                            <div class="stat-card"><div class="stat-value">${l.waiting_events || 0}</div><div class="stat-label">Still Waiting</div></div>
                            <div class="stat-card"><div class="stat-value">${l.acquired_events || 0}</div><div class="stat-label">Acquired</div></div>
                        </div>
                        <div style="font-size: 0.7rem; color: var(--text-muted); margin-top: 0.5rem;">
                            Avg: ${fmtDur(l.avg_wait_time) || '-'} | Total: ${fmtDur(l.total_wait_time) || '-'}
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
                    <div class="section disabled" id="temp_files">
                        <div class="section-header accent">Temp Files</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('Enable temp file logging', 'log_temp_files = 0')}
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
                    <div class="section-header accent">Temp Files <span class="badge">${tf.total_messages}</span></div>
                    <div class="section-body">
                        <div class="stats-row">
                            <div class="stat-card accent"><div class="stat-value">${tf.total_size}</div><div class="stat-label">Total</div></div>
                            <div class="stat-card"><div class="stat-value">${tf.avg_size}</div><div class="stat-label">Avg</div></div>
                        </div>
                        ${hasEvents ? buildChartContainer('chart-tempfiles', 'Temp File Activity', { showFilterBtn: true }) : ''}
                        ${hasQueries ? `
                            <div class="subsection">
                                <div class="subsection-title">Top Queries</div>
                                <div class="scroll-list" style="max-height: 150px;">
                                    ${tf.queries.slice(0, 5).map(q => `
                                        <div class="list-item" style="cursor: pointer;" onclick="showQueryModal('${esc(q.id || '')}')">
                                            <span class="name" style="flex: 2;">${esc(q.normalized_query || '')}</span>
                                            <span class="value">${fmt(q.count)}x - ${q.total_size}</span>
                                        </div>
                                    `).join('')}
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
                    <div class="section disabled" id="sql_overview">
                        <div class="section-header">SQL Overview</div>
                        <div class="section-body"></div>
                        ${buildDisabledOverlay('Enable query logging', 'log_min_duration_statement = 0')}
                    </div>
                `;
            }
            // Build dimension data for tab switching
            const globalTypes = ov.types || ov.query_types || [];
            const hasByDb = ov.by_database?.length > 0;
            const hasByUser = ov.by_user?.length > 0;
            const hasByHost = ov.by_host?.length > 0;
            const hasByApp = ov.by_app?.length > 0;

            return `
                <div class="section" id="sql_overview">
                    <div class="section-header">SQL Overview</div>
                    <div class="section-body">
                        <div class="categories">
                            ${ov.categories.map(c => `
                                <div class="category ${(c.category || c.name || '').toLowerCase()}">
                                    <div class="category-name">${c.category || c.name}</div>
                                    <div class="category-count">${fmt(c.count)}</div>
                                    <div class="category-pct">${c.percentage?.toFixed(1) || 0}%</div>
                                    ${c.total_time ? `<div class="category-pct" style="color: var(--primary); font-weight: 500;">${fmtDur(c.total_time)}</div>` : ''}
                                </div>
                            `).join('')}
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
                            <th>Type</th><th class="num">Count</th><th class="num">%</th><th class="num">Total</th><th class="num">Avg</th><th class="num">Max</th>
                        </tr></thead>
                        <tbody>
                            ${types.slice(0, 15).map(t => `
                                <tr>
                                    <td><span class="query-type"><span class="name">${t.type}</span></span></td>
                                    <td class="num">${fmt(t.count)}</td>
                                    <td class="num">${t.percentage?.toFixed(1) || 0}%</td>
                                    <td class="num">${fmtDur(t.total_time) || '-'}</td>
                                    <td class="num">${fmtDur(t.avg_time) || '-'}</td>
                                    <td class="num">${fmtDur(t.max_time) || '-'}</td>
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

            // Store executions for chart creation
            const hasExecutions = executions.length > 0;
            if (hasExecutions) {
                chartData.set('chart-sql-load', executions.map(e => e.timestamp));
            }

            // Build duration distribution from queries
            const durationDist = buildDurationDistribution(queries);

            return `
                <div class="section" id="sql_performance">
                    <div class="section-header accent">
                        SQL Performance
                        <span class="badge">${fmt(sql.total_queries_parsed)} queries | ${sql.total_unique_queries} unique</span>
                    </div>
                    <div class="section-body">
                        <div class="percentiles">
                            <div class="percentile"><div class="percentile-label">Min</div><div class="percentile-value">${fmtDur(sql.query_min_duration) || '-'}</div></div>
                            <div class="percentile"><div class="percentile-label">Median</div><div class="percentile-value">${fmtDur(sql.query_median_duration) || '-'}</div></div>
                            <div class="percentile danger"><div class="percentile-label">P99</div><div class="percentile-value">${fmtDur(sql.query_99th_percentile) || '-'}</div></div>
                            <div class="percentile danger"><div class="percentile-label">Max</div><div class="percentile-value">${fmtDur(sql.query_max_duration) || '-'}</div></div>
                            ${(sql.top_1_percent_slow_queries || 0) > 0 ? `<div class="percentile danger"><div class="percentile-label">Top 1%</div><div class="percentile-value">${sql.top_1_percent_slow_queries}</div></div>` : ''}
                        </div>

                        ${hasExecutions || durationDist.length > 0 ? `
                            <div class="grid grid-2" style="margin-top: 0.75rem;">
                                ${hasExecutions ? `<div>${buildChartContainer('chart-sql-load', 'Query Load Distribution', { showFilterBtn: true })}</div>` : ''}
                                ${durationDist.length > 0 ? `
                                    <div>
                                        <div class="subsection-title" style="font-size: 0.7rem;">Query Duration Distribution</div>
                                        ${buildDurationDistChart(durationDist)}
                                    </div>
                                ` : ''}
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
                { label: '<1ms', max: 1 },
                { label: '1-10ms', max: 10 },
                { label: '10-100ms', max: 100 },
                { label: '100ms-1s', max: 1000 },
                { label: '1-10s', max: 10000 },
                { label: '>10s', max: Infinity }
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
            return buckets.map((b, i) => ({ label: b.label, count: counts[i] })).filter(b => b.count > 0);
        }

        function buildDurationDistChart(dist) {
            const max = Math.max(...dist.map(d => d.count)) || 1;
            return `
                <div style="display: flex; flex-direction: column; gap: 3px;">
                    ${dist.map(d => `
                        <div class="event-bar">
                            <span class="label" style="width: 70px; font-size: 0.65rem;">${d.label}</span>
                            <div class="bar-bg">
                                <div class="bar" style="width: ${d.count/max*100}%; background: var(--chart-bar);"></div>
                            </div>
                            <span class="count" style="width: 50px;">${fmt(d.count)}</span>
                        </div>
                    `).join('')}
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
                                <th class="num">Total</th>
                                <th class="num">Avg</th>
                                <th class="num">Max</th>
                                <th class="num">%</th>
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
                                    <td class="num">
                                        <div class="duration-bar">
                                            <div class="bar"><div class="bar-fill" style="width: ${q.total_time_ms/maxTime*100}%"></div></div>
                                            <span>${fmtMs(q.total_time_ms)}</span>
                                        </div>
                                    </td>
                                    <td class="num">${fmtMs(q.avg_time_ms)}</td>
                                    <td class="num">${fmtMs(q.max_time_ms)}</td>
                                    <td class="num">${pct}%</td>
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
            setTimeout(() => renderModalCharts(), 100);
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
                html += '</div>';
                if (execs.length > 0) {
                    html += buildQdHistogram(execs.map(e => e.timestamp), 'Query count over time', '');
                }
                html += '</div>';

                // TIME section
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title">Time</div>';
                html += '<div class="qd-stats">';
                html += '<div class="qd-stat"><div class="qd-stat-label">Total</div><div class="qd-stat-value">' + fmtMsLong(q.total_time_ms) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Avg</div><div class="qd-stat-value">' + fmtMsLong(q.avg_time_ms) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Max</div><div class="qd-stat-value">' + fmtMsLong(q.max_time_ms) + '</div></div>';
                if (q.min_time_ms != null) {
                    html += '<div class="qd-stat"><div class="qd-stat-label">Min</div><div class="qd-stat-value">' + fmtMsLong(q.min_time_ms) + '</div></div>';
                }
                html += '</div>';
                if (execs.length > 0) {
                    html += buildQdCumulativeTimeHistogram(execs);
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

            const barColor = resolveColor(options.color || 'var(--primary)');
            const textColor = resolveColor('var(--text-muted)');
            const borderColor = resolveColor('var(--border)');
            const height = options.height || 100;

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
                        fill: barColor,
                        stroke: barColor,
                        width: 0,
                        points: { show: false },
                        paths: (u, seriesIdx, idx0, idx1) => {
                            const xd = u.data[0], yd = u.data[seriesIdx];
                            const barWidth = Math.max(6, (u.bbox.width / xd.length) * 0.7);
                            const p = new Path2D();
                            for (let i = idx0; i <= idx1; i++) {
                                const x = u.valToPos(xd[i], 'x', true);
                                const y = u.valToPos(yd[i], 'y', true);
                                const y0 = u.valToPos(0, 'y', true);
                                p.rect(x - barWidth/2, y, barWidth, y0 - y);
                            }
                            return { fill: p };
                        }
                    }
                ],
                plugins: [modalTooltipPlugin(options.valueFormatter)]
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
                        const { idx } = u.cursor;
                        if (idx == null) { tooltip.style.display = 'none'; return; }
                        const x = u.data[0][idx];
                        const y = u.data[1][idx];
                        const d = new Date(x * 1000);
                        const timeStr = d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
                        const valStr = valueFormatter ? valueFormatter(y) : y;
                        tooltip.innerHTML = `<div class="tt-time">${timeStr}</div><div class="tt-value">${valStr}</div>`;
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
                    <span class="filter-dropdown-item-label" title="${esc(name)}">${esc(name)}</span>
                </div>`;
            }).join('');
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
            } else {
                currentFilters[category].push(value);
                element?.classList.add('selected');
            }

            updateDropdownTrigger(category);
            updateApplyButton();
        };

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

                console.log('[Time Filter] mode=slider, minVal=', minVal, 'minOrig=', minOrig, 'maxVal=', maxVal, 'maxOrig=', maxOrig);
                console.log('[Time Filter] timeFilterStartTs=', timeFilterStartTs, 'durationMins=', timeFilterDurationMins);

                if (minVal !== minOrig) {
                    beginFilter = offsetToDatetime(parseInt(minVal));
                    console.log('[Time Filter] beginFilter=', beginFilter);
                }
                if (maxVal !== maxOrig) {
                    endFilter = offsetToDatetime(parseInt(maxVal));
                    console.log('[Time Filter] endFilter=', endFilter);
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
            console.log('Applying filters:', filters);

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

        // Build disabled overlay for sections without data
        function buildDisabledOverlay(message, param) {
            return `
                <div class="section-overlay">
                    <div class="section-overlay-icon">⚙️</div>
                    <div class="section-overlay-msg">${message}</div>
                    <code class="section-overlay-param">${param}</code>
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
            // Check for ms first
            var msIdx = s.indexOf('ms');
            if (msIdx > 0 && s.indexOf('h') < 0 && s.indexOf('m') === msIdx - 1) {
                // Pure milliseconds
                var msVal = parseFloat(s);
                return isNaN(msVal) ? s : msVal.toFixed(1) + 'ms';
            }
            // Parse h/m/s components
            var h = 0, m = 0, sec = 0;
            var hIdx = s.indexOf('h');
            var mIdx = s.indexOf('m');
            var sIdx = s.lastIndexOf('s');
            if (hIdx > 0) h = parseInt(s.substring(0, hIdx)) || 0;
            if (mIdx > 0 && mIdx !== msIdx - 1) {
                var mStart = hIdx > 0 ? hIdx + 1 : 0;
                m = parseInt(s.substring(mStart, mIdx)) || 0;
            }
            if (sIdx > 0 && sIdx !== msIdx) {
                var sStart = mIdx > 0 ? mIdx + 1 : (hIdx > 0 ? hIdx + 1 : 0);
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
        window.scrollToSection = scrollToSection;
        window.showConnTab = showConnTab;
        window.showClientTab = showClientTab;
        window.showQueryModal = showQueryModal;
        window.showSqlOvView = showSqlOvView;
        window.showSqlTab = showSqlTab;
        window.copyQuery = copyQuery;
        window.closeModal = closeModal;
        window.toggleTheme = toggleTheme;