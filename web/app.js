// ES Module imports
import { fmt, fmtDuration, fmtDurationCoarse, fmtBytes, fmtCompact, fmtMs, fmtDur, parseDurToMs, esc, escForJsAttr, truncQuery, safeMax, safeMin } from './js/utils.js';
import {
    wasmModule, wasmReady, analysisData, currentFileContent, currentFileName, currentFileSize, originalDimensions,
    charts, modalCharts, modalChartsData, modalChartCounter, chartIntervalMap, defaultInterval,
    currentFilters, appliedFilters,
    setWasmModule, setWasmReady, setAnalysisData, setCurrentFileContent, setCurrentFileName, setCurrentFileSize,
    setOriginalDimensions, incrementModalChartCounter, setAppliedFilters, clearAllCharts
} from './js/state.js';
import { initTheme, toggleTheme } from './js/theme.js';
import { unzstd, decompress, prepareContent } from './js/compression.js';
import './js/period-nav.js'; // shared period navigator (split reports + WASM)
import {
    showFilterBar, hideFilterBar, initFilterBar, closeAllDropdowns,
    updateAllDropdownTriggers, updateApplyButton, updateTimeSlider, wireTimeFilter,
    buildFiltersObject, resetTimeInputs, clearFilterSelections,
    setupFilterEventListeners, exposeFilterGlobals, computeDayAxis, MAX_CANVAS_DAYS
} from './js/filters.js';
import {
    MAX_FILE_SIZE, setProgress, initWasmInstance, loadWasm,
    setupDragDrop, showLoading, hideLoading, showDropZone
} from './js/file-handler.js';
import {
    chartData, createTimeChart, createDurationChart, createCombinedSQLChart,
    createConcurrentChart, createHistogramChart, createCheckpointChart, createWALDistanceChart, createCombinedTempFilesChart,
    buildChartContainer, closeChartModal, updateModalInterval, resetModalZoom, exportChartPNG,
    resetChartZoom, openChartModal, updateChartInterval, toggleCombinedSeries, exportChartById,
    createCostMapChart, resetCostMapZoom, openCostMapModal
} from './js/charts.js';
import {
    setOriginalReportData, getOriginalReportData, applyReportTimeFilter, resetReportTimeFilter
} from './js/report-filter.js';
import { timeFilterStartTs, timeFilterDurationMins } from './js/state.js';

// Web Components (self-registering)
import './js/components/ql-tabs.js';
import './js/components/ql-modal.js';
import './js/components/ql-tooltip.js';
import './js/components/ql-dropdown.js';

        // Load WASM module on startup
        loadWasm();

        // DOM elements
        const dropZone = document.getElementById('dropZone');
        const fileInput = document.getElementById('fileInput');
        const loading = document.getElementById('loading');
        const results = document.getElementById('results');
        const main = document.getElementById('main');

        // Setup drag and drop with processFile callback
        setupDragDrop(dropZone, fileInput, processFile);

        // Wire the bundled-demo entry points (standalone build only). The sample
        // log is injected by web/standalone.go into window.DEMO_LOG_ZST_B64; when
        // it is absent (unbundled dev page) the button stays hidden. The ?demo
        // URL param auto-loads the example once the wasm is ready, for shareable
        // links.
        const demoBtn = document.getElementById('demoBtn');
        if (demoBtn && window.DEMO_LOG_ZST_B64) {
            demoBtn.style.display = '';
            if (/[?&]demo\b/.test(location.search)) {
                (async function () {
                    while (!window.wasmReady) await new Promise(r => setTimeout(r, 50));
                    loadDemo();
                })();
            }
        }

        async function processFile(file) {
            // Check both module state and window (standalone mode uses window.wasmReady)
            if (!wasmReady && !window.wasmReady) { alert('WASM not ready'); return; }

            // Check file size limit
            if (file.size > MAX_FILE_SIZE) {
                alert(`File too large (${fmtBytes(file.size)}). Maximum size: ${fmtBytes(MAX_FILE_SIZE)}.\n\nFor larger files, use the command-line version:\n  quellog ${file.name}`);
                return;
            }

            await runAnalysis(() => prepareContent(file), file.name, file.size);
        }

        // Load the bundled example log — the standalone "See example report"
        // button and the ?demo URL param. The sample ships zstd-compressed +
        // base64 in window.DEMO_LOG_ZST_B64 (injected by web/standalone.go);
        // decompress it with the same fzstd path used for the wasm payload, then
        // run the normal analysis pipeline so the demo behaves exactly like a
        // dropped file. Absent in the unbundled dev page → the button stays hidden.
        async function loadDemo() {
            if (!wasmReady && !window.wasmReady) { alert('WASM not ready'); return; }
            const b64 = window.DEMO_LOG_ZST_B64;
            if (!b64) { alert('No example log is bundled in this build.'); return; }
            const name = window.DEMO_LOG_NAME || 'demo.log';
            await runAnalysis(() => {
                const zst = Uint8Array.from(atob(b64), c => c.charCodeAt(0));
                return unzstd(zst.buffer);
            }, name, 0);
        }

        // Shared analysis pipeline for both a dropped/selected File and the
        // bundled demo. getContent is an (async) producer returning either a
        // Uint8Array (fast quellogParseBytes path) or a string (archive path);
        // size is the on-disk byte count, or 0 to derive it from the content.
        async function runAnalysis(getContent, name, size) {
            showLoading(dropZone, loading, results);
            clearAllCharts();
            setProgress(5, 'Initializing...');

            try {
                // Reinitialize WASM to free previous memory (gc=leaking workaround)
                await initWasmInstance();

                setProgress(10, 'Reading file...');

                // Handle compressed files / archives / the embedded demo blob.
                const content = await getContent();

                // Single static "Crunching log entries…" message during the
                // WASM parse. Cycling phrases were tried (CSS-only opacity
                // keyframes, clip-path wipe, transform slides) but none
                // animated reliably across the JS-thread freeze in our
                // tinygo wasm setup. Spinner + static label is the honest
                // fallback — at least the user knows something is running.
                setProgress(50, 'Crunching log entries…');

                // Store for re-filtering
                setCurrentFileContent(content);
                setCurrentFileName(name);
                setCurrentFileSize(size || (content instanceof Uint8Array ? content.byteLength : content.length));
                setOriginalDimensions(null);  // Reset for new file

                console.log(`[quellog] Parsing: ${name} (${fmtBytes(currentFileSize)})`);

                // Yield once with rAF so the label paints before the
                // wasm call freezes the main thread.
                await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));

                // Time the actual parsing. Use quellogParseBytes when
                // content is a Uint8Array (plain logs, the new default
                // path) — saves the JS-string-to-Go-[]byte double copy
                // that was eating ~700 MB of wasm linear memory on big
                // logs. Archive paths still pass a string.
                const parseStart = performance.now();
                const resultJson = (content instanceof Uint8Array)
                    ? quellogParseBytes(content)
                    : quellogParse(content);
                const parseEnd = performance.now();
                const parseTimeMs = Math.round(parseEnd - parseStart);

                const data = JSON.parse(resultJson);

                if (data.error) throw new Error(data.error);

                // Store parse time for display
                data._parseTimeMs = parseTimeMs;

                // Store as base data for time filtering
                setOriginalReportData(data);

                setProgress(90, 'Rendering...');
                setAnalysisData(data);
                renderResults(data, currentFileName, currentFileSize);
                setProgress(100, 'Done');
                console.log(`[quellog] Complete: ${data.meta?.entries || 0} entries in ${parseTimeMs}ms`);
            } catch (err) {
                console.error('Analysis failed:', err);
                alert('Analysis failed: ' + err.message);
                showDropZone(dropZone);
            } finally {
                hideLoading(loading);
            }
        }

        // harmonizeSummary folds the source + size + parse time into a one-line
        // eyebrow and gives the filename a tooltip, after each render. Shared by
        // the standalone reports and the live WASM tool.
        function harmonizeSummary() {
            const body = document.querySelector('#summary .summary-body');
            if (!body) return;
            const meta = body.querySelector('.summary-meta');
            const pt = meta && meta.querySelector('.summary-parsetime');
            const szVal = body.querySelector('.stat-grid .stat-card:nth-child(2) .stat-value');
            if (pt && szVal && pt.textContent.indexOf(szVal.textContent) === -1) {
                pt.textContent = szVal.textContent + ' ' + pt.textContent;
            }
            const fn = meta && meta.querySelector('.summary-filename');
            if (fn && !fn.title) fn.title = fn.textContent;
        }

        function renderResults(data, fileName, fileSize, isInitial = true) {
            results.classList.add('active');

            // Clear previous chart data
            chartData.clear();
            charts.forEach(c => c.destroy());
            charts.clear();

            // In report mode, store original data for client-side filtering
            if (window.REPORT_MODE && isInitial) {
                setOriginalReportData(data);
            }

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
            // Server lifecycle is folded into the Events section as a
            // SERVER tab — on real-world logs it carries only a handful
            // of events and a full-width panel reads as wasted space.
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
            html += buildSQLPerformanceSection(data);

            // Row 5: Checkpoints | Temp Files
            html += '<div class="grid grid-2">';
            html += buildCheckpointsSection(data);
            html += buildTempFilesSection(data);
            html += '</div>';

            // Row 6: Locks | Maintenance
            html += '<div class="grid grid-2">';
            html += buildLocksSection(data);
            html += buildMaintenanceSection(data);
            html += '</div>';

            results.innerHTML = html;

            harmonizeSummary();

            // The time control lives in the freshly-rebuilt Summary card; bind it.
            wireTimeFilter();

            // Create uPlot charts after DOM is ready
            requestAnimationFrame(() => {
                chartData.forEach((data, chartId) => {
                    const accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
                    const color = chartId.includes('tempfiles') ? accentColor : null;
                    // Check data type: checkpoints (stacked), sessions (sweep-line), histogram (pre-computed), duration, combined, tempfiles, or timestamps
                    if (data?.type === 'wal-distance') {
                        createWALDistanceChart(chartId, data);
                    } else if (data?.type === 'checkpoints') {
                        createCheckpointChart(chartId, data);
                    } else if (data?.type === 'sessions') {
                        createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)', logStart: data.logStart, logEnd: data.logEnd });
                    } else if (data?.type === 'histogram') {
                        createHistogramChart(chartId, data.data, { color: color || 'var(--accent)' });
                    } else if (data?.type === 'duration') {
                        createDurationChart(chartId, data.data, { color: accentColor });
                    } else if (data?.type === 'combined') {
                        createCombinedSQLChart(chartId, data.data);
                    } else if (data?.type === 'combined-tempfiles') {
                        createCombinedTempFilesChart(chartId, data.events);
                    } else if (data?.type === 'costmap') {
                        createCostMapChart(chartId, data.queries);
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

            // Format duration: d:h / h:m / m:s with rollover. Handles the "d" unit
            // that fmtDur introduces for spans >= 24h (e.g. "1d", "2d3h").
            const formatDuration = (durStr) => {
                if (!durStr || durStr === '-') return '-';
                // Parse "2d3h", "12h 30m 11s", "5m 23s" or "45s"
                let d = parseInt(durStr.match(/(\d+)d/)?.[1] || 0);
                let h = parseInt(durStr.match(/(\d+)h/)?.[1] || 0);
                let m = parseInt(durStr.match(/(\d+)m/)?.[1] || 0);
                let sec = parseInt(durStr.match(/(\d+)s/)?.[1] || 0);
                // Rollover
                if (sec >= 60) { m += Math.floor(sec / 60); sec = sec % 60; }
                if (m >= 60) { h += Math.floor(m / 60); m = m % 60; }
                if (h >= 24) { d += Math.floor(h / 24); h = h % 24; }
                if (d > 0) return h > 0 ? `${d}d${h}h` : `${d}d`;
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

            // Human readable date for header, collapsing the month/year shared by
            // both bounds: "1 → 2 Jan 2026", "1 Jan → 2 Feb 2026", or the full
            // "1 Jan 2026 → 2 Jan 2027" when the years differ.
            const formatDateRange = (sd, ed) => {
                const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
                const sp = sd.split('-');
                const ep = ed.split('-');
                if (sp.length !== 3 || ep.length !== 3) {
                    return `${formatDateHuman(sd)} → ${formatDateHuman(ed)}`;
                }
                const sDay = parseInt(sp[2]), eDay = parseInt(ep[2]);
                const sMon = months[parseInt(sp[1]) - 1] || sp[1];
                const eMon = months[parseInt(ep[1]) - 1] || ep[1];
                const sYear = sp[0], eYear = ep[0];
                if (sYear === eYear && sp[1] === ep[1]) {
                    return `${sDay} → ${eDay} ${eMon} ${eYear}`;
                }
                if (sYear === eYear) {
                    return `${sDay} ${sMon} → ${eDay} ${eMon} ${eYear}`;
                }
                return `${sDay} ${sMon} ${sYear} → ${eDay} ${eMon} ${eYear}`;
            };
            // Derive the header date(s) from the same guarded day axis as the
            // slider, so a folded near-empty day doesn't show in the header either.
            const tsToDayStr = (ts) => {
                const d = new Date(ts);
                const p = (n) => String(n).padStart(2, '0');
                return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
            };
            // Canvas axis from the slider's STATE (set once at initial load), not
            // from data.summary which carries the *filtered* bounds after a drag —
            // otherwise the day canvas/header would reshape on every filter.
            let axis = null;
            if (timeFilterStartTs && timeFilterDurationMins > 0) {
                axis = { axisStart: timeFilterStartTs, nDays: Math.max(1, Math.round(timeFilterDurationMins / 1440)) };
            } else if (startDate && endDate) {
                axis = computeDayAxis(startDate, endDate);
            }
            let dateDisplay;
            if (axis) {
                const firstDay = tsToDayStr(axis.axisStart);
                const lastDay = tsToDayStr(axis.axisStart + (axis.nDays - 1) * 86400000);
                dateDisplay = axis.nDays === 1 ? formatDateHuman(firstDay) : formatDateRange(firstDay, lastDay);
            } else {
                dateDisplay = sameDay ? formatDateHuman(startDay) : formatDateRange(startDay, endDay);
            }

            // Time range label. The header
            // already carries the full dates ("13 Feb 2026 → 14 Feb
            // 2026"), so the multi-day form reuses the same short
            // vocabulary and drops seconds — "13 Feb 11:59 → 14 Feb
            // 00:00" instead of repeating two full timestamps.
            const shortDay = (dateStr) => formatDateHuman(dateStr).replace(/ \d{4}$/, '');
            const timeRangeLabel = sameDay
                ? `${startTime.slice(0, 5)} – ${endTime.slice(0, 5)}`
                : `${shortDay(startDay)} ${startTime.slice(0, 5)} → ${shortDay(endDay)} ${endTime.slice(0, 5)}`;

            // Multi-day: the track is each touched calendar day as a full 24h of
            // EQUAL width. Overlay a grey date (day/month) centred on each day and
            // a thin divider at each midnight. The filled bar (default selection =
            // data extent) then shows how far the log reaches into each day.
            // Midnight dividers for any multi-day span (up to ~3 months, beyond
            // which they'd be too dense); per-day date labels only while they fit
            // (<= MAX_CANVAS_DAYS), otherwise the start/end dates sit at the bounds.
            const hasDayLabels = !!(axis && axis.nDays > 1 && axis.nDays <= MAX_CANVAS_DAYS);
            let dayMarkers = '';
            if (axis && axis.nDays > 1 && axis.nDays <= 92) {
                const monthsAbbr = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
                const labels = [];
                const dividers = [];
                for (let k = 0; k < axis.nDays; k++) {
                    if (hasDayLabels) {
                        const dd = new Date(axis.axisStart + k * 86400000);
                        labels.push(`<span class="summary-time-day-label" style="left:${(k + 0.5) / axis.nDays * 100}%">${dd.getDate()} ${monthsAbbr[dd.getMonth()]}</span>`);
                    }
                    if (k > 0) dividers.push(`<i class="summary-time-day-divider" style="left:${k / axis.nDays * 100}%"></i>`);
                }
                dayMarkers = labels.join('') + dividers.join('');
            }

            // Bound labels: midnight-to-midnight by default, but for a span too
            // wide for per-day labels, show the start/end dates at the ends instead.
            let boundLeft = '00:00', boundRight = '24:00';
            if (axis && axis.nDays > MAX_CANVAS_DAYS) {
                const monthsAbbr = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
                const fmtDay = (ts) => { const d = new Date(ts); return `${d.getDate()} ${monthsAbbr[d.getMonth()]}`; };
                boundLeft = fmtDay(axis.axisStart);
                boundRight = fmtDay(axis.axisStart + (axis.nDays - 1) * 86400000);
            }

            return `
                <div class="section" id="summary">
                    <div class="section-header">Summary</div>
                    <div class="section-body summary-body">
                        <div class="summary-header">
                            <div class="summary-date">${dateDisplay}</div>
                            <div class="summary-meta">
                                <span class="summary-filename">${esc(f.fileName || 'Unknown')}</span>
                                <span class="summary-parsetime">parsed in ${parseTimeStr}</span>
                            </div>
                        </div>
                        <div class="summary-separator"></div>
                        <div class="stat-grid" style="grid-template-columns: repeat(4, auto); justify-content: center; margin-bottom: 1.2rem;">
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
                        <div class="summary-time${hasDayLabels ? ' summary-time--multiday' : ''}" id="summaryTime">
                            <!-- Slider mode (single day, <= 24h): interactive replacement
                                 for the old read-only timeline. Same IDs as the former Time
                                 dropdown so initTimeFilter()/updateTimeSlider() keep working. -->
                            <div class="filter-time-slider" id="filterTimeSlider">
                                <div class="summary-time-row">
                                    <span class="summary-time-bound">${boundLeft}</span>
                                    <div class="filter-time-slider-track">
                                        <input type="range" id="filterTimeMin" min="0" max="1440" value="0" step="1">
                                        <input type="range" id="filterTimeMax" min="0" max="1440" value="1440" step="1">
                                        <div class="filter-time-slider-range" id="filterTimeRange"></div>
                                        ${dayMarkers}
                                    </div>
                                    <span class="summary-time-bound">${boundRight}</span>
                                </div>
                                <div class="filter-time-slider-label" id="filterTimeLabel">${timeRangeLabel}</div>
                            </div>
                        </div>
                        ${buildServerSummaryLine(data)}
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

	// Determine default active tab
	let defaultTab = 'ERROR';
	if (summaryMap['ERROR'] > 0) defaultTab = 'ERROR';
	else if (summaryMap['FATAL'] > 0) defaultTab = 'FATAL';
	else if (summaryMap['PANIC'] > 0) defaultTab = 'PANIC';
	else if (summaryMap['WARNING'] > 0) defaultTab = 'WARNING';

	// Generate Tabs HTML
	let tabsHtml = criticalSeverities.map(sev => {
		const count = summaryMap[sev] || 0;
		const isSelected = sev === defaultTab ? 'selected' : '';
		const cls = sev.toLowerCase();
		return `<ql-tab class="${cls}" ${isSelected}>${sev}<ql-badge>${fmt(count)}</ql-badge></ql-tab>`;
	}).join('');

	// Generate Panels HTML
	let panelsHtml = criticalSeverities.map(sev => {
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
					// Resolve original index in data.top_events so the modal
					// can grab the full Example + timestamps array.
					const origIdx = topEvents.indexOf(e);
					rows += `
					<tr class="event-row" onclick="showEventDetail(${origIdx})" style="cursor:pointer;" title="Click for details">
						<td style="width: 50px; vertical-align: top; padding: 0.25rem 0.5rem;">
							${code ? `<span class="event-class-badge" style="border-color:${sevColor}; color:${sevColor};">${code}</span>` : ''}
						</td>
						<td style="vertical-align: top; padding: 0.25rem 0.5rem;">
							${desc ? `<div style="font-size: 0.6rem; font-weight: 600; color: var(--text-muted); margin-bottom: 2px;">${esc(desc)}</div>` : ''}
							<div class="event-msg-text">${esc(e.message)}</div>
						</td>
						<td class="num" style="width: 60px; vertical-align: top; padding: 0.25rem 0.5rem; font-weight: 600;">${fmt(e.count)}</td>
					</tr>`;
				});
			});

			innerContent = `
			<div class="table-container" style="max-height: 200px;">
				<table class="data-table" style="width: 100%;">
					${rows}
				</table>
			</div>`;
		}

		return `<ql-panel>${innerContent}</ql-panel>`;
	}).join('');

	// Generate Indicators HTML (Right)
	let indicatorsHtml = '';
	if (!onlyErrors) {
		indicatorsHtml = '<div class="events-indicators"><div class="tabs indicators-group">';
		noiseSeverities.forEach(sev => {
			const count = summaryMap[sev] || 0;
			const cls = sev.toLowerCase();
			indicatorsHtml += `<div class="tab indicator ${cls}">${sev}<span class="tab-badge">${fmt(count)}</span></div>`;
		});
		indicatorsHtml += '</div></div>';
	}

	return `
	<div class="section" id="events">
		<div class="section-header">Events</div>
		<div class="section-body events-section">
			${indicatorsHtml}
			<ql-tabs>
				${tabsHtml}
				${panelsHtml}
			</ql-tabs>
		</div>
	</div>
	`;
}

        function buildConnectionsSection(data) {
            const c = data.connections;
            if (!c || c.connection_count === 0) {
                return `
                    <div class="section" id="connections">
                        <div class="section-header muted">Connections</div>
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
                chartData.set('chart-concurrent', {
                    type: 'sessions',
                    data: c.session_events,
                    logStart: data.summary?.start_date,
                    logEnd: data.summary?.end_date
                });
            }

            return `
                <div class="section" id="connections">
                    <div class="section-header">Connections</div>
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
                            ${c.session_events?.length > 0 ? `
                                <div>
                                ${buildChartContainer('chart-concurrent', 'Concurrent Sessions', { showFilterBtn: false, tooltip: 'Number of active database connections at a given time. High values indicate more database activity.' })}
                                <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:4px;font-size:12px;">
                                    <span><span style="display:inline-block;width:12px;height:12px;background:var(--accent);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Sessions</span>
                                    <span><span style="display:inline-block;width:12px;height:12px;background:var(--text-muted);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Pre-log</span>
                                    <span><span style="display:inline-block;width:16px;height:0;border-top:2px dashed var(--text-muted);vertical-align:middle;margin-right:4px;"></span>Median</span>
                                </div>
                                </div>
                            ` : ''}
                            ${hasConnections ? `
                                <div>
                                ${buildChartContainer('chart-connections', 'Connection Distribution', { showFilterBtn: true, tooltip: 'Timeline of connection events. High values indicate heavy traffic.' })}
                                <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:4px;font-size:12px;">
                                    <span><span style="display:inline-block;width:12px;height:12px;background:var(--chart-bar);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Connections</span>
                                    <span><span style="display:inline-block;width:16px;height:0;border-top:2px dashed var(--text-muted);vertical-align:middle;margin-right:4px;"></span>Median</span>
                                </div>
                                </div>
                            ` : ''}
                        </div>
                        ${hasSessionDist ? `
                            <div class="subsection">
                                <div class="subsection-title">Session Duration Distribution</div>
                                ${buildSessionDistributionChart(c.session_distribution)}
                            </div>
                        ` : ''}
                        ${hasSessions ? `
                            <ql-tabs style="margin-top: 1rem;">
                                <ql-tab selected>By User</ql-tab>
                                <ql-tab>By Database</ql-tab>
                                <ql-tab>By Host</ql-tab>
                                <ql-panel>${buildSessionTable(c.sessions_by_user, 'User')}</ql-panel>
                                <ql-panel>${buildSessionTable(c.sessions_by_database, 'Database')}</ql-panel>
                                <ql-panel>${buildSessionTable(c.sessions_by_host, 'Host')}</ql-panel>
                            </ql-tabs>
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
                            return `<span class="duration-stack-item"><span class="duration-stack-dot" style="background: ${colors[e.idx]};"></span>${e.label} ${pct > 0 && pct < 1 ? '< 1' : pct.toFixed(0)}%</span>`;
                        }).join('')}
                    </div>
                </div>
            `;
        }

        function buildSessionTable(sessions, label) {
            if (!sessions || Object.keys(sessions).length === 0) return '<div class="empty">No session data</div>';
            const tableId = 'session-table-' + label.toLowerCase().replace(/\s+/g, '-');
            const rows = Object.entries(sessions).map(([name, s]) => ({
                name,
                count: s.count,
                min: fmtDur(s.min_duration),   minMs: parseDurToMs(s.min_duration),
                avg: fmtDur(s.avg_duration),   avgMs: parseDurToMs(s.avg_duration),
                median: fmtDur(s.median_duration), medianMs: parseDurToMs(s.median_duration),
                max: fmtDur(s.max_duration),   maxMs: parseDurToMs(s.max_duration),
                cumulated: fmtDur(s.cumulated_duration), cumulatedMs: parseDurToMs(s.cumulated_duration)
            }));
            window['_sessionRows_' + tableId] = rows;

            const cols = [
                { key: 'count', label: 'Sessions', sort: 'count' },
                { key: 'min', label: 'Min', sort: 'minMs' },
                { key: 'avg', label: 'Avg', sort: 'avgMs' },
                { key: 'median', label: 'Median', sort: 'medianMs' },
                { key: 'max', label: 'Max', sort: 'maxMs' },
                { key: 'cumulated', label: 'Cumulated', sort: 'cumulatedMs' },
            ];
            const thStyle = 'text-align:right;cursor:pointer;user-select:none';
            const needsScroll = rows.length > 12;
            return `
                <div class="table-scroll-wrapper${needsScroll ? ' has-overflow' : ''}" style="${needsScroll ? 'max-height: 350px; overflow-y: auto;' : ''}">
                <table class="data-table" id="${tableId}" style="font-size: 0.75rem;">
                    <thead><tr>
                        <th>${label}</th>
                        ${cols.map(c => `<th style="${thStyle}" onclick="sortSessionTable('${tableId}','${c.sort}',this)" data-sort="${c.sort}">${c.label}${c.sort === 'count' ? ' ▼' : ''}</th>`).join('')}
                    </tr></thead>
                    <tbody>
                        ${renderSessionRows(rows, 'count')}
                    </tbody>
                </table>
                </div>
            `;
        }

        function renderSessionRows(rows, sortKey, asc) {
            const sorted = [...rows].sort((a, b) => asc ? a[sortKey] - b[sortKey] : b[sortKey] - a[sortKey]);
            return sorted.map(s => `
                <tr>
                    <td>${esc(s.name)}</td>
                    <td style="text-align:right">${fmt(s.count)}</td>
                    <td style="text-align:right">${s.min || '-'}</td>
                    <td style="text-align:right">${s.avg || '-'}</td>
                    <td style="text-align:right">${s.median || '-'}</td>
                    <td style="text-align:right">${s.max || '-'}</td>
                    <td style="text-align:right">${s.cumulated || '-'}</td>
                </tr>
            `).join('');
        }

        window.sortSessionTable = function(tableId, sortKey, th) {
            const table = document.getElementById(tableId);
            if (!table) return;
            const rows = window['_sessionRows_' + tableId];
            if (!rows) return;
            const wasAsc = th.dataset.dir === 'asc';
            const asc = !wasAsc;
            th.dataset.dir = asc ? 'asc' : 'desc';
            table.querySelectorAll('th[data-sort]').forEach(h => {
                const arrow = h === th ? (asc ? ' ▲' : ' ▼') : '';
                h.textContent = h.textContent.replace(/ [▲▼]$/, '') + arrow;
            });
            table.querySelector('tbody').innerHTML = renderSessionRows(rows, sortKey, asc);
        };

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
                        <div class="section-header muted">Clients</div>
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
                    <div class="section-header">Clients</div>
                    <div class="section-body">
                        <ql-tabs>
                            <ql-tab selected>Databases <ql-badge>${c.unique_databases}</ql-badge></ql-tab>
                            <ql-tab>Users <ql-badge>${c.unique_users}</ql-badge></ql-tab>
                            <ql-tab>Apps <ql-badge>${c.unique_apps}</ql-badge></ql-tab>
                            <ql-tab>Hosts <ql-badge>${c.unique_hosts}</ql-badge></ql-tab>
                            <ql-panel>${buildClientList(databases, maxDb)}</ql-panel>
                            <ql-panel>${buildClientList(users, maxUser)}</ql-panel>
                            <ql-panel>${buildClientList(apps, maxApp)}</ql-panel>
                            <ql-panel>${buildClientList(hosts, maxHost)}</ql-panel>
                        </ql-tabs>
                    </div>
                </div>
            `;
        }


        function buildCheckpointsSection(data) {
            const cp = data.checkpoints;
            if (!cp || (!cp.total_checkpoints && !cp.warning_count)) {
                return `
                    <div class="section" id="checkpoints">
                        <div class="section-header muted">Checkpoints</div>
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
            const req = (types['shutdown immediate']?.count || 0) + (types['immediate force wait']?.count || 0);
            const hasEvents = cp.events?.length > 0;
            const hasWarnings = cp.warning_events?.length > 0;
            const other = cp.total_checkpoints - timed - wal;

            // I/O rates from server-computed values

            const hasWALDistances = cp.wal_distances?.length > 0;

            // Store WAL distance data for distance vs estimate chart
            if (hasWALDistances) {
                chartData.set('chart-wal-distance', {
                    type: 'wal-distance',
                    distances: cp.wal_distances,
                    warnings: cp.warning_events || []
                });
            }

            // Store checkpoint data by type for multi-series chart
            if (hasEvents) {
                chartData.set('chart-checkpoints', {
                    type: 'checkpoints',
                    all: cp.events,
                    types: {
                        time: types.time?.events || [],
                        wal: types.wal?.events || [],
                        other: [
                            ...(types['shutdown immediate']?.events || []),
                            ...(types['immediate force wait']?.events || [])
                        ]
                    }
                });
            } else if (hasWarnings) {
                // Warnings only (log_checkpoints = off): show warnings as the sole series
                chartData.set('chart-checkpoints', {
                    type: 'checkpoints',
                    warningsOnly: true,
                    all: cp.warning_events,
                    types: {
                        time: [],
                        wal: [],
                        other: cp.warning_events
                    }
                });
            }

            return `
                <div class="section" id="checkpoints">
                    <div class="section-header">Checkpoints</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${timed}</div><div class="stat-label">Timed</div></div>
                            <div class="stat-card"><div class="stat-value">${wal}</div><div class="stat-label">WAL</div></div>
                            ${other > 0 ? `<div class="stat-card"><div class="stat-value">${other}</div><div class="stat-label">Other</div></div>` : ''}
                            ${cp.wal_rate ? `<div class="stat-card"><div class="stat-value">${cp.wal_rate}</div><div class="stat-label">WAL Rate</div></div>` : ''}
                            ${cp.flush_rate ? `<div class="stat-card"><div class="stat-value">${cp.flush_rate}</div><div class="stat-label">Flush Rate</div></div>` : ''}
                            ${cp.warning_count ? `<div class="stat-card stat-card--alert"><div class="stat-value">${cp.warning_count}</div><div class="stat-label">Too Frequent</div></div>` : ''}
                        </div>
                        ${hasEvents ? `
                            ${buildChartContainer('chart-checkpoints', 'Checkpoint Distribution', { showFilterBtn: false, tooltip: 'Checkpoint writes over time. Timed is normal, WAL indicates heavy write load.' })}
                            <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:8px;font-size:12px;">
                                <span><span style="display:inline-block;width:12px;height:12px;background:var(--chart-bar);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Timed</span>
                                <span><span style="display:inline-block;width:12px;height:12px;background:var(--accent);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>WAL</span>
                                <span><span style="display:inline-block;width:12px;height:12px;background:#909399;border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Other</span>
                            </div>
                        ` : hasWarnings ? `
                            ${buildChartContainer('chart-checkpoints', 'Checkpoint Frequency Warnings', { showFilterBtn: false })}
                        ` : ''}
                        ${hasWALDistances ? `
                            <div style="margin-top:-0.5rem;">
                            ${buildChartContainer('chart-wal-distance', 'WAL Distance vs Estimate', { showFilterBtn: false, showBucketControl: false, tooltip: 'WAL generated between checkpoints. The estimate is PostgreSQL prediction for the next cycle.' })}
                            <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:4px;font-size:12px;">
                                <span><span style="display:inline-block;width:12px;height:12px;background:var(--chart-bar);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Distance</span>
                                <span><span style="display:inline-block;width:16px;height:0;border-top:2px dashed var(--accent);vertical-align:middle;margin-right:4px;"></span>Estimate</span>
                                ${cp.warning_count ? `<span><span style="display:inline-block;width:12px;height:12px;background:rgba(220,53,69,0.25);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Too frequent</span>` : ''}
                            </div>
                            </div>
                        ` : ''}
                    </div>
                </div>
            `;
        }

        // Builds the optional one-line SERVER summary that sits right
        // under the Summary section's header. Each fragment is a single
        // span styled by severity: neutral for benign counters (starts,
        // shutdowns, reloads), warning for non-fatal anomalies (crash
        // recoveries, walsender timeouts, WAL receive failures…), alert
        // for the worst events (backend crashes, invalidated slots).
        // Fragments stay terse on purpose; the diagnostic detail (which
        // signals, which params changed, which side of the replication
        // broke) lives in the fragment's title= tooltip so the line is
        // scannable at a glance and explorable on hover. Returns ''
        // when nothing is worth surfacing — the line just disappears on
        // healthy steady-state logs.
        function buildServerSummaryLine(data) {
            const s = data.server || {};
            const r = data.replication || {};
            const frags = [];
            const push = (text, sev, tip, detail) => frags.push({ text, sev, tip, detail });
            const plural = (n, sing, plur) => (n > 1 ? (plur || sing + 's') : sing);

            // Server-lifecycle markers, ordered roughly chronologically
            // (start → reload+config → shutdown → recovery → crash) so
            // the line reads as a tiny narrative.
            const starts = s.starts || 0;
            if (starts > 0) push(`${starts} ${plural(starts, 'start')}`, 'info');
            const reloads = s.reloads || 0;
            if (reloads > 0) push(`${reloads} ${plural(reloads, 'reload')}`, 'info');
            // Parameter changes ride along the reload that carried them —
            // the tooltip lists the actual settings so a DBA sees "what
            // changed" without opening the raw log.
            const params = s.parameter_changes || [];
            if (params.length > 0) {
                const shown = params.slice(0, 6).map(p => `${p.parameter} → ${p.new}`);
                if (params.length > 6) shown.push(`… +${params.length - 6} more`);
                push(`${params.length} param ${plural(params.length, 'change')}`, 'info', shown.join('\n'));
            }
            const totalShutdowns = (s.shutdowns_fast || 0) + (s.shutdowns_immediate || 0) + (s.shutdowns_smart || 0);
            if (totalShutdowns > 0) {
                const kinds = [];
                if (s.shutdowns_fast) kinds.push(`${s.shutdowns_fast} fast`);
                if (s.shutdowns_immediate) kinds.push(`${s.shutdowns_immediate} immediate`);
                if (s.shutdowns_smart) kinds.push(`${s.shutdowns_smart} smart`);
                push(`${totalShutdowns} ${plural(totalShutdowns, 'shutdown')}`, 'info', kinds.join(', '));
            }
            const recoveries = s.crash_recoveries || 0;
            if (recoveries > 0) push(`${recoveries} ${plural(recoveries, 'crash recovery', 'crash recoveries')}`, 'warning', 'database system was not properly shut down — automatic recovery');
            const crashes = s.backend_crashes || 0;
            if (crashes > 0) {
                const sigNames = { '1':'SIGHUP','2':'SIGINT','3':'SIGQUIT','6':'SIGABRT','9':'SIGKILL','11':'SIGSEGV','13':'SIGPIPE','14':'SIGALRM','15':'SIGTERM' };
                const sigCounts = s.signal_counts || {};
                const sigKeys = Object.keys(sigCounts).sort((a, b) => Number(a) - Number(b));
                const sigShort = sigKeys.map(k => sigNames[k] || 'signal ' + k).join(', ');
                const sigDetail = sigKeys.map(k => `${sigNames[k] || 'signal ' + k} ×${sigCounts[k]}`).join(', ');
                push(`${crashes} backend ${plural(crashes, 'crash', 'crashes')}`, 'alert', sigDetail, sigShort);
            }
            const auxExits = s.auxiliary_process_exits || 0;
            if (auxExits > 0) push(`${auxExits} aux ${plural(auxExits, 'exit')}`, 'warning', 'auxiliary process (bgwriter, walwriter, …) exited abnormally');

            // Replication markers — invalidated slots are the most
            // operationally severe, then per-cause termination markers
            // (named after the PG marker, diagnostic hint in tooltip),
            // then conflicts, then plain reconnects. The LastTermination
            // timestamp is appended to the last fired termination so the
            // line still gives a window even when several causes coexist.
            const slots = r.invalidated_slots || 0;
            if (slots > 0) push(`${slots} invalidated ${plural(slots, 'slot')}`, 'alert', 'replication slot dropped — standby must be rebuilt or resynced');
            const markers = r.markers || {};
            const termRows = [
                ['wal_receive_failed', 'WAL receive failure', 'replica lost primary'],
                ['walsender_timeout',  'walsender timeout',   'primary side — replica too slow'],
                ['replication_term',   'replication termination', 'primary closed walsender'],
                ['unexpected_eof',     'unexpected EOF',      'abrupt walsender disconnect'],
            ];
            const firedTerms = termRows.filter(([k]) => (markers[k] || 0) > 0);
            firedTerms.forEach(([k, label, hint], i) => {
                const n = markers[k];
                const isLast = i === firedTerms.length - 1;
                const lastT = isLast && r.last_termination
                    ? `last ${r.last_termination.split(' ')[1] || r.last_termination}`
                    : '';
                push(`${n} ${plural(n, label)}`, 'warning', hint, lastT);
            });
            const conflicts = r.conflicts_with_recovery || 0;
            if (conflicts > 0) push(`${conflicts} ${plural(conflicts, 'conflict')} w/ recovery`, 'warning', 'queries killed/cancelled because they blocked WAL replay');
            const reconnects = r.stream_reconnects || 0;
            if (reconnects > 0) push(`${reconnects} stream ${plural(reconnects, 'reconnect')}`, 'info');
            const pauses = r.recovery_pauses || 0;
            if (pauses > 0) push(`${pauses} recovery ${plural(pauses, 'pause')}`, 'info');

            // The health zone is a permanent part of the Summary card —
            // when nothing fired it shows an explicit "no server events"
            // so an absence reads as a positive signal (steady cluster)
            // rather than missing data, and the card keeps the same
            // structure whatever the log contains.
            if (frags.length === 0) {
                push('no server incidents', 'empty');
                frags[0].tip = 'no start / shutdown / crash / replication marker in this log';
            }
            // Fixed two-column grid whatever the fragment count, so the
            // zone has the same geometry on every report: one message
            // sits top-left, two split left/right, more fill column-
            // major (read down the left column first — same narrative
            // order as the CLI). Label left, optional muted detail
            // (signal names, last-termination time) as a plain suffix —
            // no parentheses.
            const parts = frags.map(f => {
                const tip = f.tip ? ` title="${esc(f.tip)}"` : '';
                const detail = f.detail ? `<span class="summary-server-detail">${esc(f.detail)}</span>` : '';
                return `<div class="summary-server-row"><span class="summary-server-frag summary-server-${f.sev}"${tip}>${esc(f.text)}</span>${detail}</div>`;
            }).join('');
            const rows = Math.max(1, Math.ceil(frags.length / 2));
            return `<div class="summary-separator summary-separator--tight"></div>
                <div class="summary-server-line" style="grid-template-rows: repeat(${rows}, auto)">${parts}</div>`;
        }

        function buildMaintenanceSection(data) {
            const m = data.maintenance;
            if (!m || ((m.vacuum_count || 0) + (m.analyze_count || 0)) === 0) {
                return `
                    <div class="section" id="maintenance">
                        <div class="section-header muted">Maintenance</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_autovacuum_min_duration = 0</code>')}
                        </div>
                    </div>
                `;
            }
            // Flat layout: one consolidated stat-grid at the top covers
            // every headline (vacuum + analyze + buffer/WAL chips), then
            // two sibling subsections each carry only their tab-switchable
            // top-tables widget. Avoids the "two stat-grids stacked" look
            // and keeps the eye moving downward through the panels rather
            // than jumping between sibling blocks of headline numbers.
            const spaceRecovered = m.vacuum_space_recovered || {};
            const totalRecovered = Object.values(spaceRecovered).reduce((s, sz) => s + parseSizeToBytes(sz), 0);
            primeMaintenanceCaches(m, spaceRecovered);

            const hasVac = (m.vacuum_count || 0) > 0;
            const hasAna = (m.analyze_count || 0) > 0;

            return `
                <div class="section" id="maintenance">
                    <div class="section-header">Maintenance</div>
                    <div class="section-body">
                        ${buildMaintenanceStatGrid(m, totalRecovered)}
                        ${hasVac ? buildAutovacuumPanel(m) : ''}
                        ${hasAna ? buildAutoanalyzePanel(m) : ''}
                    </div>
                </div>
            `;
        }

        // Cache the live maintenance metrics so the tab switcher can
        // re-render the table without re-walking analysisData each click.
        let _vacTabsData = null;
        let _anaTabsData = null;

        function primeMaintenanceCaches(m, spaceRecovered) {
            const topVacTables = m.top_vacuum_tables || [];
            const xminTables = m.xmin_blocked_tables || [];
            const vacTables = m.vacuum_table_counts
                ? Object.entries(m.vacuum_table_counts)
                    .map(([t, c]) => ({ table: t, count: c }))
                    .sort((a, b) => b.count - a.count)
                : [];
            _vacTabsData = { topElapsed: topVacTables, xmin: xminTables, byCount: vacTables, spaceRecovered, vacuumCount: m.vacuum_count || 0 };

            const topAnaTables = m.top_analyze_tables_by_elapsed || [];
            const anaTables = m.analyze_table_counts
                ? Object.entries(m.analyze_table_counts)
                    .map(([t, c]) => ({ table: t, count: c }))
                    .sort((a, b) => b.count - a.count)
                : [];
            _anaTabsData = { topElapsed: topAnaTables, byCount: anaTables, analyzeCount: m.analyze_count || 0 };
        }

        function buildMaintenanceStatGrid(m, _totalRecovered) {
            // Trimmed to the metrics a DBA acts on first; per-table
            // tuples removed / space recovered live in the table panel
            // below since they're per-row anyway. Durations use the
            // coarse formatter (drops seconds past 1h) so the headline
            // reads at a glance.
            const vacElapsedStr = (m.total_vacuum_elapsed_seconds || 0) > 0
                ? fmtDurationCoarse(m.total_vacuum_elapsed_seconds * 1000) : '';
            const anaElapsedStr = (m.total_analyze_elapsed_seconds || 0) > 0
                ? fmtDurationCoarse(m.total_analyze_elapsed_seconds * 1000) : '';
            const slowest = m.slowest_vacuum;
            return `
                <div class="stat-grid">
                    <div class="stat-card"><div class="stat-value">${fmt(m.vacuum_count || 0)}</div><div class="stat-label">Vacuum count</div></div>
                    ${(m.aggressive_vacuum_count || 0) > 0 ? `<div class="stat-card stat-card--warning"><div class="stat-value">${fmt(m.aggressive_vacuum_count)}</div><div class="stat-label">Aggressive</div></div>` : ''}
                    ${vacElapsedStr ? `<div class="stat-card"><div class="stat-value">${vacElapsedStr}</div><div class="stat-label">Vacuum time</div></div>` : ''}
                    ${slowest && slowest.elapsed_seconds > 0 ? `<div class="stat-card" title="${esc(slowest.table)}"><div class="stat-value">${fmtDurationCoarse(slowest.elapsed_seconds * 1000)}</div><div class="stat-label">Slowest single run</div></div>` : ''}
                    <div class="stat-card"><div class="stat-value">${fmt(m.analyze_count || 0)}</div><div class="stat-label">Analyze count</div></div>
                    ${anaElapsedStr ? `<div class="stat-card"><div class="stat-value">${anaElapsedStr}</div><div class="stat-label">Analyze time</div></div>` : ''}
                </div>
            `;
        }

        function buildMaintenanceMetricLines(m) {
            // Buffer-usage totals are intentionally not shown here
            // anymore — the per-table "By buffer" tab carries the same
            // breakdown (cluster-wide is just the column sum). WAL has
            // no dedicated tab so we keep it as a one-line chip.
            const walTotal = (m.total_wal_records || 0) + (m.total_wal_bytes || 0);
            if (walTotal === 0) return '';
            return `
                <div class="metric-line">
                    <span class="metric-line-label">WAL usage</span>
                    <span class="metric-line-val"><strong>${fmtCompact(m.total_wal_records || 0)}</strong> records</span>
                    <span class="metric-line-val"><strong>${fmtBytes(m.total_wal_bytes || 0)}</strong></span>
                </div>
            `;
        }

        function buildAutovacuumPanel(m) {
            const topVacTables = _vacTabsData?.topElapsed || [];
            const hasBufferData = topVacTables.some(t => (t.buffer_hits || 0) + (t.buffer_misses || 0) > 0);
            return `
                <div class="subsection">
                    <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.5rem;">
                        <div class="subsection-title" style="margin: 0;">Autovacuum</div>
                        ${hasBufferData ? `
                            <div class="tabs" style="margin: 0;">
                                <button class="tab active" onclick="showVacuumView(this, 'main')">Top tables</button>
                                <button class="tab" onclick="showVacuumView(this, 'buffer')">Buffer usage</button>
                            </div>
                        ` : ''}
                    </div>
                    ${buildMaintenanceMetricLines(m)}
                    <div id="vacuum-table-container">
                        ${renderVacuumMainTable()}
                    </div>
                </div>
            `;
        }

        function showVacuumView(btn, view) {
            btn.parentElement.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            btn.classList.add('active');
            const container = document.getElementById('vacuum-table-container');
            if (!container) return;
            container.innerHTML = view === 'buffer' ? renderVacuumBufferTable() : renderVacuumMainTable();
        }

        function buildAutoanalyzePanel(m) {
            const topAnaTables = _anaTabsData?.topElapsed || [];
            const anaTables = _anaTabsData?.byCount || [];
            if (topAnaTables.length === 0 && anaTables.length === 0) return '';
            return `
                <div class="subsection">
                    <div style="margin-bottom: 0.5rem;">
                        <div class="subsection-title" style="margin: 0;">Autoanalyze</div>
                    </div>
                    <div id="analyze-table-container">
                        ${renderAnalyzeTable()}
                    </div>
                </div>
            `;
        }

        // Rendering primitives — same scroll-list shape the maintenance
        // section has always used (name + bar + secondary + value) so
        // the visual rhythm is preserved across tab switches. The cap
        // is generous so the list scrolls (max-height + overflow-y on
        // .scroll-list) rather than truncating: the user keeps the long
        // tail one wheel-flick away. CLI keeps a tighter cut.
        const MAINT_TOP_N = 20;

        // Maintenance list-items use a wider 5-slot layout
        // (name / wide bar / extra / removed / value): the bar takes the
        // remaining flex space so progress reads at a glance even on
        // dense reports, and the .extra slot reserves a fixed width so
        // the bar never shifts horizontally when only some rows carry a
        // "X KB recovered" annotation.

        // Autovacuum: two stacked sortable tables — a main panel
        // (elapsed / vacuums / dead rows / recovered) and a buffer
        // panel (hits / misses / dirtied / written). Each has its own
        // sort state so the user can rank tables independently on
        // either side.
        let _vacMainSortKey = 'elapsed';   // 'table' | 'elapsed' | 'count' | 'dead' | 'recovered'
        let _vacMainSortDir = 'desc';
        let _vacBufSortKey = 'misses';     // 'table' | 'hits' | 'misses' | 'dirtied' | 'written'
        let _vacBufSortDir = 'desc';

        function renderVacuumMainTable() {
            const d = _vacTabsData;
            if (!d) return '';
            // Per-table metrics: count (from vacuum_table_counts, all
            // tables), elapsed/dead (from top_vacuum_tables, capped
            // higher now), recovered (from spaceRecovered map).
            const elapsedByTable = {};
            const deadByTable = {};
            (d.topElapsed || []).forEach(t => {
                elapsedByTable[t.table] = t.total_elapsed_seconds || 0;
                deadByTable[t.table] = t.tuples_not_yet_removable || 0;
            });
            const parseRecov = s => s ? parseSizeToBytes(s) : 0;
            const rows = (d.byCount || []).map(t => ({
                table: t.table,
                count: t.count,
                elapsed: elapsedByTable[t.table] || 0,
                dead: deadByTable[t.table] || 0,
                recovered: parseRecov(d.spaceRecovered[t.table]),
            }));
            if (!rows.length) return '<div class="empty">No vacuum operations recorded.</div>';
            const factor = _vacMainSortDir === 'desc' ? -1 : 1;
            rows.sort((a, b) => {
                const va = a[_vacMainSortKey], vb = b[_vacMainSortKey];
                if (typeof va === 'string') return va.localeCompare(vb) * factor;
                return (va - vb) * factor;
            });
            const limited = rows.slice(0, MAINT_TOP_N);
            const barKey = _vacMainSortKey === 'table' ? 'elapsed' : _vacMainSortKey;
            const maxBar = Math.max(...limited.map(r => r[barKey] || 0)) || 1;
            const arrow = key => _vacMainSortKey === key ? `<span class="sort-arrow">${_vacMainSortDir === 'desc' ? '▼' : '▲'}</span>` : '';
            const sortable = (key, label) => `<span data-sort onclick="showVacuumMainSort('${key}')">${label}${arrow(key)}</span>`;
            return `<div class="scroll-list scroll-list--maintenance scroll-list--maintenance-vac-main">
                <div class="list-header list-header--sortable">
                    <span class="name">${sortable('table', 'Table')}</span>
                    <div class="bar"></div>
                    <span class="extra">${sortable('recovered', 'Recovered')}</span>
                    <span class="dead-col">${sortable('dead', 'Dead rows')}</span>
                    <span class="removed">${sortable('count', 'Vacuums')}</span>
                    <span class="value">${sortable('elapsed', 'Elapsed')}</span>
                </div>
                ${limited.map(r => `<div class="list-item">
                    ${maintName(r.table)}
                    <div class="bar"><div class="bar-fill" style="width: ${(r[barKey]||0)/maxBar*100}%${_vacMainSortKey === 'dead' ? '; background: var(--danger);' : ''}"></div></div>
                    <span class="extra">${r.recovered > 0 ? fmtBytes(r.recovered) : '-'}</span>
                    <span class="dead-col">${r.dead > 0 ? fmt(r.dead) : '-'}</span>
                    <span class="removed">${r.count}×</span>
                    <span class="value">${r.elapsed > 0 ? fmtDuration(r.elapsed * 1000) : '-'}</span>
                </div>`).join('')}
            </div>`;
        }

        function renderVacuumBufferTable() {
            const d = _vacTabsData;
            if (!d) return '';
            const rows = (d.topElapsed || [])
                .filter(t => (t.buffer_hits || 0) + (t.buffer_misses || 0) > 0)
                .map(t => ({
                    table: t.table,
                    hits: t.buffer_hits || 0,
                    misses: t.buffer_misses || 0,
                    dirtied: t.buffer_dirtied || 0,
                    written: t.buffer_written || 0,
                }));
            if (!rows.length) return '<div class="empty">No buffer data.</div>';
            const factor = _vacBufSortDir === 'desc' ? -1 : 1;
            rows.sort((a, b) => {
                const va = a[_vacBufSortKey], vb = b[_vacBufSortKey];
                if (typeof va === 'string') return va.localeCompare(vb) * factor;
                return (va - vb) * factor;
            });
            const limited = rows.slice(0, MAINT_TOP_N);
            const barKey = _vacBufSortKey === 'table' ? 'misses' : _vacBufSortKey;
            const maxBar = Math.max(...limited.map(r => r[barKey] || 0)) || 1;
            const arrow = key => _vacBufSortKey === key ? `<span class="sort-arrow">${_vacBufSortDir === 'desc' ? '▼' : '▲'}</span>` : '';
            const sortable = (key, label) => `<span data-sort onclick="showVacuumBufferSort('${key}')">${label}${arrow(key)}</span>`;
            const cell = n => (n || 0) > 0 ? fmtCompact(n) : '-';
            return `<div class="scroll-list scroll-list--maintenance scroll-list--maintenance-buffer">
                <div class="list-header list-header--sortable list-header--buffer">
                    <span class="name">${sortable('table', 'Table')}</span>
                    <div class="bar"></div>
                    <span class="extra"><span class="buf-cells"><span>${sortable('hits', 'Hits')}</span><span>${sortable('misses', 'Misses')}</span><span>${sortable('dirtied', 'Dirtied')}</span><span>${sortable('written', 'Written')}</span></span></span>
                </div>
                ${limited.map(r => `<div class="list-item">
                    ${maintName(r.table)}
                    <div class="bar"><div class="bar-fill" style="width: ${(r[barKey]||0)/maxBar*100}%"></div></div>
                    <span class="extra"><span class="buf-cells"><span>${cell(r.hits)}</span><span>${cell(r.misses)}</span><span>${cell(r.dirtied)}</span><span>${cell(r.written)}</span></span></span>
                </div>`).join('')}
            </div>`;
        }

        function showVacuumMainSort(key) {
            if (_vacMainSortKey === key) _vacMainSortDir = _vacMainSortDir === 'desc' ? 'asc' : 'desc';
            else { _vacMainSortKey = key; _vacMainSortDir = 'desc'; }
            const c = document.getElementById('vacuum-table-container');
            if (c) c.innerHTML = renderVacuumMainTable();
        }

        function showVacuumBufferSort(key) {
            if (_vacBufSortKey === key) _vacBufSortDir = _vacBufSortDir === 'desc' ? 'asc' : 'desc';
            else { _vacBufSortKey = key; _vacBufSortDir = 'desc'; }
            const c = document.getElementById('vacuum-table-container');
            if (c) c.innerHTML = renderVacuumBufferTable();
        }

        // maintName renders a table-name cell with a native hover
        // tooltip carrying the full identifier plus a click handler
        // that inserts a one-line copy ribbon directly above the row
        // — auto-selected, ready for Cmd+C. The cell itself keeps the
        // truncated form so the row layout never reflows.
        function maintName(table) {
            const safe = esc(table);
            return `<span class="name" title="${safe}" onclick="showMaintRibbon(this)"><span class="name-inner">${safe}</span></span>`;
        }

        function showMaintRibbon(el) {
            // Toggle off when reclicking the same expanded cell.
            if (el.classList.contains('expanded')) {
                el.classList.remove('expanded');
                window.getSelection().removeAllRanges();
                return;
            }
            // Only one cell expanded at a time.
            document.querySelectorAll('.scroll-list--maintenance .name.expanded')
                .forEach(n => n.classList.remove('expanded'));
            el.classList.add('expanded');
            // Pre-select the inner span so the next keystroke is Cmd+C.
            const inner = el.querySelector('.name-inner');
            if (inner) {
                const range = document.createRange();
                range.selectNodeContents(inner);
                const sel = window.getSelection();
                sel.removeAllRanges();
                sel.addRange(range);
            }
            // Re-truncate as soon as the selection leaves the cell —
            // a click anywhere else, a Tab, ESC, etc. The setTimeout
            // skips the initial selection event the opener just fired.
            setTimeout(() => {
                const onSelChange = () => {
                    const s = window.getSelection();
                    if (!s.anchorNode || !el.contains(s.anchorNode)) {
                        el.classList.remove('expanded');
                        document.removeEventListener('selectionchange', onSelChange);
                    }
                };
                document.addEventListener('selectionchange', onSelChange);
            }, 0);
        }

        // Autoanalyze uses a single unified table with sortable column
        // headers (no tab toggle) — every row carries both elapsed and
        // count, the user sorts on whichever dimension is most relevant
        // at the moment. Bar is proportional to the currently-sorted
        // numeric column, so the visual ranking always matches the sort.
        let _anaSortKey = 'elapsed';   // 'table' | 'elapsed' | 'count' | 'share'
        let _anaSortDir = 'desc';      // 'desc' | 'asc'

        function renderAnalyzeTable() {
            const d = _anaTabsData;
            if (!d) return '';
            // Merge per-table count (covers all tables that ran an
            // analyze) with elapsed data (covers all tables PG emitted
            // a system-usage line for — should be all on PG 13+ with
            // log_autovacuum_min_duration). Tables missing elapsed get
            // 0, which sorts to the bottom on desc.
            const elapsedByTable = {};
            (d.topElapsed || []).forEach(t => { elapsedByTable[t.table] = t.total_elapsed_seconds; });
            const rows = (d.byCount || []).map(t => ({
                table: t.table,
                count: t.count,
                elapsed: elapsedByTable[t.table] || 0,
                share: d.analyzeCount > 0 ? (t.count / d.analyzeCount * 100) : 0,
            }));
            if (!rows.length) return '<div class="empty">No analyze operations recorded.</div>';
            const factor = _anaSortDir === 'desc' ? -1 : 1;
            rows.sort((a, b) => {
                const va = a[_anaSortKey], vb = b[_anaSortKey];
                if (typeof va === 'string') return va.localeCompare(vb) * factor;
                return (va - vb) * factor;
            });
            const limited = rows.slice(0, MAINT_TOP_N);
            const barKey = _anaSortKey === 'table' ? 'elapsed' : _anaSortKey;
            const maxBar = Math.max(...limited.map(r => r[barKey] || 0)) || 1;
            const arrow = key => _anaSortKey === key ? `<span class="sort-arrow">${_anaSortDir === 'desc' ? '▼' : '▲'}</span>` : '';
            const sortable = (key, label) => `<span data-sort onclick="showAnalyzeSort('${key}')">${label}${arrow(key)}</span>`;
            return `<div class="scroll-list scroll-list--maintenance">
                <div class="list-header list-header--sortable">
                    <span class="name">${sortable('table', 'Table')}</span>
                    <div class="bar"></div>
                    <span class="extra">${sortable('elapsed', 'Elapsed')}</span>
                    <span class="removed">${sortable('count', 'Analyzes')}</span>
                    <span class="value">${sortable('share', 'Share')}</span>
                </div>
                ${limited.map(r => `<div class="list-item">
                    ${maintName(r.table)}
                    <div class="bar"><div class="bar-fill" style="width: ${(r[barKey] || 0) / maxBar * 100}%"></div></div>
                    <span class="extra">${r.elapsed > 0 ? fmtDuration(r.elapsed * 1000) : '-'}</span>
                    <span class="removed">${r.count}×</span>
                    <span class="value">${r.share.toFixed(1)}%</span>
                </div>`).join('')}
            </div>`;
        }

        function showAnalyzeSort(key) {
            if (_anaSortKey === key) {
                _anaSortDir = _anaSortDir === 'desc' ? 'asc' : 'desc';
            } else {
                _anaSortKey = key;
                _anaSortDir = 'desc';
            }
            const container = document.getElementById('analyze-table-container');
            if (container) container.innerHTML = renderAnalyzeTable();
        }

        function buildLocksSection(data) {
            const l = data.locks;
            if (!l || ((l.deadlock_events || 0) + (l.waiting_events || 0) + (l.acquired_events || 0)) === 0) {
                return `
                    <div class="section" id="locks">
                        <div class="section-header muted">Locks</div>
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
            const relations = l.relation_stats ? Object.entries(l.relation_stats).map(([t, c]) => ({type: t, count: c})).sort((a,b) => b.count - a.count) : [];
            const hasRelations = relations.length > 0;
            return `
                <div class="section" id="locks">
                    <div class="section-header">Locks</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card ${deadlocks > 0 ? 'stat-card--alert' : ''}"><div class="stat-value">${deadlocks}</div><div class="stat-label">Deadlocks</div></div>
                            <div class="stat-card"><div class="stat-value">${l.waiting_events || 0}</div><div class="stat-label">Still Waiting</div></div>
                            <div class="stat-card"><div class="stat-value">${l.acquired_events || 0}</div><div class="stat-label">Acquired</div></div>
                            <div class="stat-card"><div class="stat-value">${fmtDur(l.avg_wait_time) || '-'}</div><div class="stat-label">Avg</div></div>
                            <div class="stat-card"><div class="stat-value">${l.total_wait_time || '-'}</div><div class="stat-label">Total</div></div>
                        </div>
                        ${hasLockTypes || hasResTypes || hasRelations ? `
                            <div class="subsection" style="display: flex; gap: 1rem; flex-wrap: wrap;">
                                ${hasLockTypes ? `
                                    <div style="flex: 1; min-width: 120px;">
                                        <div class="subsection-title" style="margin-top: 0;">Lock Types</div>
                                        <div class="query-types">
                                            ${lockTypes.map(t => `
                                                <span class="query-type">
                                                    <span class="name">${esc(t.type)}</span>
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
                                                    <span class="name">${esc(t.type)}</span>
                                                    <span class="count">${fmt(t.count)}</span>
                                                </span>
                                            `).join('')}
                                        </div>
                                    </div>
                                ` : ''}
                                ${hasRelations ? `
                                    <div style="flex: 1; min-width: 120px;">
                                        <div class="subsection-title" style="margin-top: 0;">Relations</div>
                                        <div class="query-types">
                                            ${relations.map(t => `
                                                <span class="query-type">
                                                    <span class="name">${esc(t.type)}</span>
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
                                <div class="subsection-title">Waiting Queries</div>
                                <div class="table-container" style="max-height: 180px;">
                                    <table>
                                        <thead><tr>
                                            <th>Query</th>
                                            <th class="num">Acquired</th>
                                            <th class="num">Waiting</th>
                                            <th class="num">Total Wait</th>
                                        </tr></thead>
                                        <tbody>
                                            ${[...l.queries].sort((a, b) => {
                                                // parseDurToMs, not parseFloat: total_wait_time is a
                                                // formatted duration ("1h 04m 17s"); parseFloat would
                                                // read only the leading number and rank a 1h wait (→1)
                                                // below a 25s wait (→25).
                                                const wa = parseDurToMs(a.total_wait_time) || 0;
                                                const wb = parseDurToMs(b.total_wait_time) || 0;
                                                return wb - wa;
                                            }).slice(0, 10).map(q => `
                                                <tr>
                                                    <td class="query-cell" onclick="showQueryModal('${esc(q.id)}')">${esc(truncQuery(q.normalized_query))}</td>
                                                    <td class="num">${q.acquired_count || 0}</td>
                                                    <td class="num">${q.still_waiting_count || 0}</td>
                                                    <td class="num">${q.total_wait_time || '-'}</td>
                                                </tr>
                                            `).join('')}
                                        </tbody>
                                    </table>
                                </div>
                            </div>
                        ` : ''}
                        ${(() => {
                            // PG emits one "still waiting" every deadlock_timeout (default 1s)
                            // plus one final "acquired" for each blocked transaction, so a single
                            // real wait surfaces as 2–5 entries in `events[]`. Aggregating the
                            // raw stream triple-counts "Blocked" and inflates "Total Wait" by
                            // the sum of the intermediate still-waiting values.
                            // Fold to one entry per unique wait — keyed by (process_id,
                            // blocking_pid, lock_type) — preferring the final `acquired`
                            // event when present (carries the true end-to-end wait time),
                            // falling back to the latest `waiting` otherwise.
                            const uniqueWaits = new Map();
                            (l.events || []).forEach(e => {
                                if (!e.blocking_query_id || e.event_type === 'deadlock') return;
                                const key = `${e.process_id}|${e.blocking_pid}|${e.lock_type}`;
                                const prev = uniqueWaits.get(key);
                                if (!prev || e.event_type === 'acquired') {
                                    uniqueWaits.set(key, e);
                                }
                            });
                            const blockers = {};
                            uniqueWaits.forEach(e => {
                                if (!blockers[e.blocking_query_id]) {
                                    blockers[e.blocking_query_id] = { id: e.blocking_query_id, query: e.blocking_query || '', count: 0, totalWaitMs: 0 };
                                }
                                blockers[e.blocking_query_id].count++;
                                // Parse "1.00 s" or "2m 30s" wait_time string to ms
                                const wt = e.wait_time || '';
                                const sMatch = wt.match(/([\d.]+)\s*s/);
                                const mMatch = wt.match(/([\d.]+)\s*m/);
                                let ms = 0;
                                if (mMatch) ms += parseFloat(mMatch[1]) * 60000;
                                if (sMatch) ms += parseFloat(sMatch[1]) * 1000;
                                blockers[e.blocking_query_id].totalWaitMs += ms;
                            });
                            const sorted = Object.values(blockers).sort((a, b) => b.totalWaitMs - a.totalWaitMs).slice(0, 10);
                            if (sorted.length === 0) return '';
                            return `
                            <div class="subsection">
                                <div class="subsection-title">Blocking Queries</div>
                                <div class="table-container" style="max-height: 180px;">
                                    <table>
                                        <thead><tr>
                                            <th>Query</th>
                                            <th class="num">Blocked</th>
                                            <th class="num">Avg Wait</th>
                                            <th class="num">Total Wait</th>
                                        </tr></thead>
                                        <tbody>
                                            ${sorted.map(b => `
                                                <tr>
                                                    <td class="query-cell" onclick="showQueryModal('${esc(b.id)}')">${esc(b.query || b.id)}</td>
                                                    <td class="num">${b.count}</td>
                                                    <td class="num">${fmtDuration(b.totalWaitMs / b.count)}</td>
                                                    <td class="num">${fmtDuration(b.totalWaitMs)}</td>
                                                </tr>
                                            `).join('')}
                                        </tbody>
                                    </table>
                                </div>
                            </div>`;
                        })()}
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
                        <div class="section-header muted">Temp Files</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_temp_files = 0</code>')}
                        </div>
                    </div>
                `;
            }
            const hasQueries = tf.queries?.length > 0;
            const hasEvents = tf.events?.length > 0;
            // Store full events for combined chart (count + size)
            if (hasEvents) {
                chartData.set('chart-tempfiles', { type: 'combined-tempfiles', events: tf.events });
            }
            return `
                <div class="section" id="temp_files">
                    <div class="section-header">Temp Files</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${fmt(tf.total_messages)}</div><div class="stat-label">Count</div></div>
                            <div class="stat-card"><div class="stat-value">${tf.total_size}</div><div class="stat-label">Total</div></div>
                            <div class="stat-card"><div class="stat-value">${tf.avg_size}</div><div class="stat-label">Avg</div></div>
                            <div class="stat-card"><div class="stat-value">${tf.max_size || '-'}</div><div class="stat-label">Max</div></div>
                        </div>
                        ${hasEvents ? `
                            ${buildChartContainer('chart-tempfiles', 'Temp File Activity', { showFilterBtn: true, tooltip: 'Temp file count and cumulative size over time. Created when queries exceed work_mem.' })}
                            <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:4px;font-size:12px;">
                                <span><span style="display:inline-block;width:12px;height:12px;background:var(--chart-bar);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Count</span>
                                <span><span style="display:inline-block;width:12px;height:12px;background:var(--accent);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Size</span>
                                <span><span style="display:inline-block;width:16px;height:0;border-top:2px dashed var(--text-muted);vertical-align:middle;margin-right:4px;"></span>Median</span>
                            </div>
                        ` : ''}
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
                                                    <td class="query-cell" onclick="showQueryModal('${esc(q.id || '')}')">${esc(truncQuery(q.normalized_query || ''))}</td>
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

        function buildSQLOverviewSection(data) {
            const ov = data.sql_overview;
            // Check if we have any SQL data
            const hasData = ov && ov.categories && ov.categories.some(c => c.count > 0);
            if (!hasData) {
                return `
                    <div class="section" id="sql_overview">
                        <div class="section-header muted">SQL Overview</div>
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
                    <div class="section-header">SQL Overview</div>
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

        let _queryTypesTableCounter = 0;
        function buildQueryTypesTable(types) {
            if (!types?.length) return '<div class="empty">No query type data</div>';
            const tableId = 'sql-types-table-' + (_queryTypesTableCounter++);
            const rows = types.map(t => ({
                type: t.type,
                count: t.count,
                pct: t.percentage || 0,
                avg: fmtDur(t.avg_time), avgMs: parseDurToMs(t.avg_time),
                max: fmtDur(t.max_time), maxMs: parseDurToMs(t.max_time),
                total: fmtDur(t.total_time), totalMs: parseDurToMs(t.total_time)
            }));
            window['_queryTypesRows_' + tableId] = rows;

            const thStyle = 'cursor:pointer;user-select:none';
            const cols = [
                { key: 'count', label: 'Count', sort: 'count' },
                { key: 'pct', label: '%', sort: 'pct' },
                { key: 'avg', label: 'Avg', sort: 'avgMs' },
                { key: 'max', label: 'Max', sort: 'maxMs' },
                { key: 'total', label: 'Total', sort: 'totalMs' },
            ];
            const needsScroll = rows.length > 8;
            return `
                <div class="table-scroll-wrapper${needsScroll ? ' has-overflow' : ''}" style="${needsScroll ? 'max-height: 250px; overflow-y: auto;' : ''}">
                    <table id="${tableId}">
                        <thead><tr>
                            <th>Type</th>
                            ${cols.map(c => `<th class="num" style="${thStyle}" onclick="sortQueryTypesTable('${tableId}','${c.sort}',this)" data-sort="${c.sort}">${c.label}${c.sort === 'count' ? ' ▼' : ''}</th>`).join('')}
                        </tr></thead>
                        <tbody>
                            ${renderQueryTypesRows(rows, 'count')}
                        </tbody>
                    </table>
                </div>
            `;
        }

        function renderQueryTypesRows(rows, sortKey, asc) {
            const sorted = [...rows].sort((a, b) => asc ? a[sortKey] - b[sortKey] : b[sortKey] - a[sortKey]);
            return sorted.map(t => `
                <tr>
                    <td><span class="query-type"><span class="name">${esc(t.type)}</span></span></td>
                    <td class="num">${fmt(t.count)}</td>
                    <td class="num">${t.pct.toFixed(1)}%</td>
                    <td class="num">${t.avg || '-'}</td>
                    <td class="num">${t.max || '-'}</td>
                    <td class="num">${t.total || '-'}</td>
                </tr>
            `).join('');
        }

        window.sortQueryTypesTable = function(tableId, sortKey, th) {
            const table = document.getElementById(tableId);
            if (!table) return;
            const rows = window['_queryTypesRows_' + tableId];
            if (!rows) return;
            const wasAsc = th.dataset.dir === 'asc';
            const asc = !wasAsc;
            th.dataset.dir = asc ? 'asc' : 'desc';
            table.querySelectorAll('th[data-sort]').forEach(h => {
                const arrow = h === th ? (asc ? ' ▲' : ' ▼') : '';
                h.textContent = h.textContent.replace(/ [▲▼]$/, '') + arrow;
            });
            table.querySelector('tbody').innerHTML = renderQueryTypesRows(rows, sortKey, asc);
        };

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
                                                    <span class="name">${esc(t.type)}</span>
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

        const TCL_TYPES = new Set([
            'BEGIN', 'COMMIT', 'ROLLBACK', 'SAVEPOINT', 'RELEASE',
            'START', 'END', 'ABORT', 'PREPARE', 'DEALLOCATE'
        ]);

        function buildTCLTable(tclQueries) {
            if (!tclQueries?.length) return '';

            // Aggregate by type
            const byType = new Map();
            for (const q of tclQueries) {
                const type = q.type;
                if (!byType.has(type)) {
                    byType.set(type, { type, count: 0, totalTime: 0, maxTime: 0 });
                }
                const agg = byType.get(type);
                agg.count += q.count;
                agg.totalTime += q.total_time_ms;
                agg.maxTime = Math.max(agg.maxTime, q.max_time_ms);
            }

            // Sort by total time descending
            const rows = [...byType.values()].sort((a, b) => b.totalTime - a.totalTime);

            return `
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th>Type</th>
                                <th class="num">Count</th>
                                <th class="num">Total Time</th>
                                <th class="num">Avg</th>
                                <th class="num">Max</th>
                            </tr>
                        </thead>
                        <tbody>
                            ${rows.map(r => `
                                <tr>
                                    <td>${r.type}</td>
                                    <td class="num">${fmt(r.count)}</td>
                                    <td class="num">${fmtMs(r.totalTime)}</td>
                                    <td class="num">${fmtMs(r.count > 0 ? r.totalTime / r.count : 0)}</td>
                                    <td class="num">${fmtMs(r.maxTime)}</td>
                                </tr>
                            `).join('')}
                        </tbody>
                    </table>
                </div>
            `;
        }

        function buildSQLPerformanceSection(data) {
            const sql = data.sql_performance;
            if (!sql || !sql.queries || sql.queries.length === 0) {
                return `
                    <div class="section" id="sql_performance">
                        <div class="section-header muted">SQL Performance</div>
                        <div class="section-body">
                            ${buildNoDataMessage('<code>log_min_duration_statement = 0</code>')}
                        </div>
                    </div>
                `;
            }
            const queries = sql.queries || [];
            const executions = sql.executions || [];

            // Separate TCL queries from regular queries
            const regularQueries = queries.filter(q => !TCL_TYPES.has(q.type));
            const tclQueries = queries.filter(q => TCL_TYPES.has(q.type));

            const maxTime = safeMax(regularQueries.map(q => q.total_time_ms)) || 1;

            // Create sorted copies for each tab (regular queries only)
            const byTotal = [...regularQueries].sort((a, b) => b.total_time_ms - a.total_time_ms);
            const bySlowest = [...regularQueries].sort((a, b) => b.max_time_ms - a.max_time_ms);
            const byFrequent = [...regularQueries].sort((a, b) => b.count - a.count);

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

            // Slim duration distribution stacked-bar — same data as the
            // previous "Duration Distribution" subsection, but rendered
            // inline (no title, no border) so it just fills a thin band
            // between the activity chart and the tabs without competing
            // with the cost map for attention.
            const durationDist = buildDurationDistribution(queries);

            // Cost-map data: regular queries with a real avg duration. TCL
            // (COMMIT/BEGIN/ROLLBACK…) is excluded — like the main query tables,
            // which keep it in their own tab — so a high-volume COMMIT can't
            // dominate and stretch the X axis, and every point cross-references
            // a visible (non-TCL) table row.
            const costMapQueries = regularQueries.filter(q => (q.count || 0) > 0 && (q.avg_time_ms || 0) > 0);

            return `
                <div class="section" id="sql_performance">
                    <div class="section-header">SQL Performance</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${fmt(sql.total_queries_parsed)}</div><div class="stat-label">Queries</div></div>
                            <div class="stat-card"><div class="stat-value">${fmt(sql.total_unique_queries)}</div><div class="stat-label">Unique</div></div>
                            ${(sql.top_1_percent_slow_queries || 0) > 0 ? `<div class="stat-card stat-card--alert"><div class="stat-value">${sql.top_1_percent_slow_queries}</div><div class="stat-label">Top 1%</div></div>` : ''}
                            <div class="stat-card"><div class="stat-value">${fmtDur(sql.query_min_duration) || '-'}</div><div class="stat-label">Min</div></div>
                            <div class="stat-card"><div class="stat-value">${fmtDur(sql.query_median_duration) || '-'}</div><div class="stat-label">Median</div></div>
                            <div class="stat-card stat-card--alert"><div class="stat-value">${fmtDur(sql.query_99th_percentile) || '-'}</div><div class="stat-label">P99</div></div>
                            <div class="stat-card stat-card--alert"><div class="stat-value">${fmtDur(sql.query_max_duration) || '-'}</div><div class="stat-label">Max</div></div>
                        </div>
                        <div class="sql-perf-grid">
                            <div class="sql-perf-col-left">
                                ${costMapQueries.length > 1 ? (() => {
                                    chartData.set('chart-costmap', { type: 'costmap', queries: costMapQueries });
                                    return `
                                    <div class="chart-container">
                                        <div class="chart-controls">
                                            <span class="subsection-title" style="margin: 0; font-size: 0.7rem;">Cost Map<ql-tooltip text="Each dot is a normalized query, positioned by execution count (X) and average duration (Y) on log-log scales. The 45° iso-curves mark constant cumulative time (count × avg). Drag to zoom.">i</ql-tooltip></span>
                                            <div style="display: flex; gap: 0.5rem; align-items: center;">
                                                <span class="zoom-hint">drag to zoom</span>
                                                <button onclick="resetCostMapZoom('chart-costmap')">Reset</button>
                                                <button class="btn-expand" onclick="openCostMapModal('chart-costmap', 'Cost Map')" title="Expand chart">⛶</button>
                                            </div>
                                        </div>
                                        <div id="chart-costmap" style="min-height: 120px;"></div>
                                    </div>
                                    `;
                                })() : ''}
                            </div>
                            <div class="sql-perf-col-right">
                                ${hasExecutions ? `
                                    <div>
                                        ${buildChartContainer('chart-sql-combined', 'Query Activity', { showFilterBtn: true, tooltip: 'Query count and cumulated duration over time.' })}
                                        <div class="chart-legend">
                                            <span class="chart-legend-item" data-chart="chart-sql-combined" data-series="count" onclick="toggleCombinedSeries('chart-sql-combined', 'count')"><span class="chart-legend-bar chart-legend-bar--count"></span>Count</span>
                                            <span class="chart-legend-item" data-chart="chart-sql-combined" data-series="duration" onclick="toggleCombinedSeries('chart-sql-combined', 'duration')"><span class="chart-legend-bar chart-legend-bar--duration"></span>Duration</span>
                                            <span><span style="display:inline-block;width:16px;height:0;border-top:2px dashed var(--text-muted);vertical-align:middle;margin-right:4px;"></span>Median</span>
                                        </div>
                                    </div>
                                ` : ''}
                                ${durationDist.some(d => d.count > 0) ? `<div style="margin-top:0.5rem;">${buildCompactDurationDist(durationDist)}</div>` : ''}
                                <ql-tabs style="margin-top: 0.75rem;">
                                    <ql-tab selected>By Total Time</ql-tab>
                                    <ql-tab>Slowest (Max)</ql-tab>
                                    <ql-tab>Most Frequent</ql-tab>
                                    ${tclQueries.length > 0 ? '<ql-tab>TCL</ql-tab>' : ''}
                                    <ql-panel>${buildQueryTable(byTotal, maxTime, 'total')}</ql-panel>
                                    <ql-panel>${buildQueryTable(bySlowest, maxTime, 'max')}</ql-panel>
                                    <ql-panel>${buildQueryTable(byFrequent, maxTime, 'count')}</ql-panel>
                                    ${tclQueries.length > 0 ? `<ql-panel>${buildTCLTable(tclQueries)}</ql-panel>` : ''}
                                </ql-tabs>
                            </div>
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
                            return `<span class="duration-stack-item"><span class="duration-stack-dot" style="background: ${colors[d.idx]};"></span>${d.label} ${pct > 0 && pct < 1 ? '< 1' : pct.toFixed(0)}%</span>`;
                        }).join('')}
                    </div>
                </div>
            `;
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
                                <th></th>
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
                                const qid = esc(q.id);
                                return `
                                <tr data-q-id="${qid}" onmouseenter="highlightQuery('${qid}', true)" onmouseleave="highlightQuery('${qid}', false)">
                                    <td>${i + 1}</td>
                                    <td class="cell-plan-action">${q.plan ? `<button class="btn-explain" onclick="event.stopPropagation(); visualizePlanFor('${qid}')" title="Visualize plan on explain.dalibo.com"><svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><rect x="9" y="2" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="16" y="16" width="6" height="6" rx="1"/><path d="M12 8 v4 M5 12 h14 M5 12 v4 M19 12 v4" fill="none" stroke="currentColor" stroke-width="1.6"/></svg></button>` : ''}</td>
                                    <td class="query-cell" onclick="showQueryModal('${qid}')" title="Click for details">${esc(truncQuery(q.normalized_query))}</td>
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

        // --- Modal navigation (Event ↔ Query) -----------------------
        // modalStack tracks the trail of cross-modal navigations so the
        // "← Back" button in either modal can return to the previous
        // one. Each entry is {kind:'event'|'query', id, childId} — the
        // childId points to the row inside this modal that the user
        // clicked to descend, so on the way back we can scroll + flash
        // that row to anchor the user visually.
        let modalStack = [];
        // _suppressStackClear is set while we deliberately close a
        // modal during in-flight navigation (so the modal-close
        // listener below does not wipe the stack we just pushed).
        let _suppressStackClear = false;

        function navigateToQuery(queryId, fromKind, fromId) {
            if (fromKind && fromId) {
                const top = modalStack[modalStack.length - 1];
                if (!top || top.kind !== fromKind || top.id !== fromId) {
                    modalStack.push({ kind: fromKind, id: fromId, childId: queryId });
                }
            }
            if (fromKind === 'event') {
                _suppressStackClear = true;
                document.getElementById('eventModal').close();
                _suppressStackClear = false;
            }
            showQueryModal(queryId);
        }

        function navigateToEvent(eventIndex, fromKind, fromId) {
            const ev = analysisData?.top_events?.[eventIndex];
            const eid = ev?.id;
            if (fromKind && fromId && eid) {
                const top = modalStack[modalStack.length - 1];
                if (!top || top.kind !== fromKind || top.id !== fromId) {
                    modalStack.push({ kind: fromKind, id: fromId, childId: eid });
                }
            }
            if (fromKind === 'query') {
                _suppressStackClear = true;
                document.getElementById('queryModal').close();
                _suppressStackClear = false;
            }
            showEventDetail(eventIndex);
        }

        function modalBack() {
            const prev = modalStack.pop();
            if (!prev) return;
            _suppressStackClear = true;
            document.getElementById('queryModal').close();
            document.getElementById('eventModal').close();
            _suppressStackClear = false;
            if (prev.kind === 'event') {
                const idx = (analysisData?.top_events || []).findIndex(e => e.id === prev.id);
                if (idx >= 0) showEventDetail(idx, { flashId: prev.childId });
            } else if (prev.kind === 'query') {
                showQueryModal(prev.id, { flashId: prev.childId });
            }
        }

        // Returns the inline back-button bar to inject at the top of a
        // modal body, or '' when the stack is empty (i.e. this modal was
        // opened directly, not through a cross-modal navigation).
        function renderBackBar() {
            if (modalStack.length === 0) return '';
            const prev = modalStack[modalStack.length - 1];
            const label = prev.kind === 'event' ? 'event' : 'query';
            return '<div class="modal-back-bar"><button class="modal-back-btn" onclick="modalBack()">← Back to ' + label + '</button></div>';
        }

        // Scroll the row whose data-flash-id matches flashId into view
        // and flash a brief highlight on it so the user can anchor the
        // navigation visually.
        function flashAndScroll(flashId) {
            if (!flashId) return;
            requestAnimationFrame(() => {
                const row = document.querySelector('[data-flash-id="' + (window.CSS?.escape ? CSS.escape(flashId) : flashId) + '"]');
                if (!row) return;
                row.scrollIntoView({ behavior: 'smooth', block: 'center' });
                row.classList.add('modal-flash');
                setTimeout(() => row.classList.remove('modal-flash'), 1800);
            });
        }

        // Triggering-queries table for the event modal. Click a row →
        // navigateToQuery which pushes the current event onto the modal
        // stack so the user can hit "← Back" to return. Same column
        // shape as buildQueryTable (no rank column; rows are pre-sorted
        // desc by count). data-flash-id labels each row by its queryID
        // so a return navigation can scroll + flash this row.
        function buildEventTriggeringTable(triggers, eventTotal, eventId) {
            if (!triggers?.length) return '<div class="empty">No triggering queries</div>';
            const maxCount = triggers[0]?.count || 1;
            return `
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th>Query</th>
                                <th class="num">Count</th>
                                <th class="num">%</th>
                                <th class="num"></th>
                            </tr>
                        </thead>
                        <tbody>
                            ${triggers.slice(0, 50).map(t => {
                                const pct = eventTotal > 0 ? (t.count / eventTotal * 100).toFixed(1) : 0;
                                const qid = esc(t.id);
                                const eid = esc(eventId || '');
                                return `
                                <tr onclick="navigateToQuery('${qid}', 'event', '${eid}')" data-flash-id="${qid}" style="cursor:pointer;" title="Click for query details">
                                    <td class="query-cell">${esc(truncQuery(t.normalized_query))}</td>
                                    <td class="num">${fmt(t.count)}</td>
                                    <td class="num">${pct}%</td>
                                    <td class="num">
                                        <div class="duration-bar">
                                            <div class="bar"><div class="bar-fill" style="width: ${t.count/maxCount*100}%"></div></div>
                                        </div>
                                    </td>
                                </tr>
                            `}).join('')}
                        </tbody>
                    </table>
                </div>
            `;
        }

        // Walk top_events and collect the events whose
        // triggering_queries entry includes queryId. Each result is the
        // pair (event, triggerCount) so the renderer can show both the
        // global event count and the share attributed to this query.
        function findEventsTriggeredBy(queryId) {
            if (!queryId || !analysisData?.top_events) return [];
            const out = [];
            for (const ev of analysisData.top_events) {
                if (!ev.triggering_queries) continue;
                const tq = ev.triggering_queries.find(t => t.id === queryId);
                if (tq) out.push({ event: ev, triggerCount: tq.count });
            }
            // Sort by trigger count desc, tie-break on event count desc.
            out.sort((a, b) => {
                if (a.triggerCount !== b.triggerCount) return b.triggerCount - a.triggerCount;
                return (b.event.count || 0) - (a.event.count || 0);
            });
            return out;
        }

        // severityColorVar maps a PostgreSQL severity string to the CSS
        // variable used elsewhere in the report (event modal sparkline,
        // event rows colouring) so the same colour palette is reused
        // consistently when we tag severity cells.
        function severityColorVar(sev) {
            switch (sev) {
                case 'ERROR':            return 'var(--danger)';
                case 'FATAL':
                case 'PANIC':            return 'var(--purple)';
                case 'WARNING':          return 'var(--warning)';
                default:                 return 'var(--text-muted)';
            }
        }

        // "Events triggered" table for the Query Detail modal — the
        // mirror image of buildEventTriggeringTable. Each row pivots from
        // "this query → these events" and clicking it opens the matching
        // event modal so the user can dive into samples/timeline.
        // Rows are already sorted by trigger count desc upstream; no
        // ranking column is displayed and the event ID stays implicit
        // (the row click is the only thing the reader needs). The
        // severity cell is bold + coloured so the row tells you what
        // class of error this is at a glance.
        function buildQueryEventsTable(rows, queryId) {
            if (!rows.length) return '';
            const qid = esc(queryId || '');
            return `
                <div class="table-container">
                    <table>
                        <thead>
                            <tr>
                                <th>Severity</th>
                                <th>Message</th>
                                <th class="num">Triggered</th>
                            </tr>
                        </thead>
                        <tbody>
                            ${rows.slice(0, 50).map(r => {
                                const idx = analysisData.top_events.indexOf(r.event);
                                return `
                                <tr onclick="navigateToEvent(${idx}, 'query', '${qid}')" data-flash-id="${esc(r.event.id)}" style="cursor:pointer;" title="Click for event details">
                                    <td style="color: ${severityColorVar(r.event.severity)}; font-weight: 700;">${esc(r.event.severity)}</td>
                                    <td class="query-cell">${esc(truncQuery(r.event.message))}</td>
                                    <td class="num">${fmt(r.triggerCount)}</td>
                                </tr>
                            `}).join('')}
                        </tbody>
                    </table>
                </div>
            `;
        }

        function copyQuery(index) {
            const q = analysisData.sql_performance.queries[index];
            navigator.clipboard.writeText(q.full_query || q.normalized_query);
            alert('Query copied to clipboard');
        }

        // Event detail modal — full message + occurrences-over-time sparkline
        function showEventDetail(index, opts = {}) {
            const e = analysisData.top_events?.[index];
            if (!e) return;

            const sevColor = e.severity === 'ERROR' ? 'var(--danger)'
                : (e.severity === 'FATAL' || e.severity === 'PANIC') ? 'var(--purple)'
                : e.severity === 'WARNING' ? 'var(--warning)' : 'var(--text-muted)';

            const ts = e.timestamps || [];
            let firstStr = '-', lastStr = '-', freqStr = '-';
            if (ts.length > 0) {
                const first = new Date(ts[0]);
                const last = new Date(ts[ts.length - 1]);
                firstStr = first.toISOString().slice(0, 19).replace('T', ' ');
                lastStr = last.toISOString().slice(0, 19).replace('T', ' ');
                const spanMin = Math.max(1, (last - first) / 60000);
                freqStr = (ts.length / spanMin).toFixed(2) + ' /min';
            }

            const sqlClass = e.sql_state_class || '';
            const sqlBadge = sqlClass ? `<span class="event-class-badge" style="border-color:${sevColor};color:${sevColor};margin-right:0.5rem;">${esc(sqlClass)}</span>` : '';

            const chartTitle = `Event – ${e.severity}${sqlClass ? ' ' + sqlClass : ''}`;
            // Copy-button helper: same inline pattern as SQL detail modal —
            // shows "Copied!" feedback, reverts to "Copy" after 1.5 s.
            // escForJsAttr handles all 4 escape layers so a message
            // containing " or ' or \n doesn't break the attribute / JS string.
            const copyBtn = (text) => `<button class="copy-btn-inline" onclick="navigator.clipboard.writeText('${escForJsAttr(text)}');this.textContent='Copied!';setTimeout(()=>this.textContent='Copy',1500)">Copy</button>`;

            document.getElementById('eventModalBody').innerHTML = `
                ${renderBackBar()}
                <div style="margin-bottom:1rem;">
                    <div style="display:flex;align-items:center;margin-bottom:0.5rem;">
                        ${sqlBadge}
                        <span style="font-weight:600;color:${sevColor};">${esc(e.severity)}</span>
                    </div>
                </div>
                <div class="qd-chart-container" style="margin-bottom:1rem;">
                    <div class="qd-chart-header">
                        <span class="qd-chart-title">Occurrences Over Time</span>
                        <button class="btn-export-png" onclick="exportChartById('eventModalChart', '${chartTitle.replace(/'/g, "\\'")}')" title="Export as PNG">⬇ PNG</button>
                    </div>
                    <div id="eventModalChart" style="height:180px;"></div>
                </div>
                <div class="detail-stats" style="margin-bottom:1rem;">
                    <div class="detail-stat"><div class="value">${fmt(e.count)}</div><div class="label">Occurrences</div></div>
                    <div class="detail-stat"><div class="value">${esc(firstStr)}</div><div class="label">First seen</div></div>
                    <div class="detail-stat"><div class="value">${esc(lastStr)}</div><div class="label">Last seen</div></div>
                    <div class="detail-stat"><div class="value">${esc(freqStr)}</div><div class="label">Frequency</div></div>
                </div>
                <div class="qd-section-title" style="display:flex;justify-content:space-between;align-items:center;">Normalized Pattern${copyBtn(e.message)}</div>
                <div class="query-detail-sql" style="margin-bottom:0.75rem;">${esc(e.message)}</div>
                <div class="qd-section-title" style="display:flex;justify-content:space-between;align-items:center;">Example (raw message)${copyBtn(e.example || e.message)}</div>
                <div class="query-detail-sql" style="margin-bottom:0.75rem;">${esc(e.example || e.message)}</div>
                ${(e.triggering_queries && e.triggering_queries.length > 0) ? `
                    <div class="qd-section-title">Triggering Queries <span class="qd-meta">${e.triggering_queries.length} distinct</span></div>
                    ${buildEventTriggeringTable(e.triggering_queries, e.count, e.id)}
                ` : ''}
            `;
            document.getElementById('eventModal').open();

            // Render the chart after the modal is on screen so the container
            // width is known to uPlot. Reuse createTimeChart for visual parity
            // with the other timestamp-based charts (median line, drag-zoom,
            // tooltip plugin) — feeds it the per-event timestamps array.
            const sevColorResolved = e.severity === 'ERROR' ? getComputedStyle(document.documentElement).getPropertyValue('--danger').trim()
                : (e.severity === 'FATAL' || e.severity === 'PANIC') ? getComputedStyle(document.documentElement).getPropertyValue('--purple').trim()
                : e.severity === 'WARNING' ? getComputedStyle(document.documentElement).getPropertyValue('--warning').trim()
                : getComputedStyle(document.documentElement).getPropertyValue('--chart-bar').trim();
            requestAnimationFrame(() => createTimeChart('eventModalChart', ts, { color: sevColorResolved, height: 180 }));
            if (opts.flashId) flashAndScroll(opts.flashId);
        }

        function closeModal() {
            document.getElementById('queryModal').close();
        }

        // Cost map: log-log scatter of every normalized query, positioned by
        // execution count (X) × avg duration (Y). The diagonals of constant
        // cumulative time (count * avg) render as 45° iso-lines (axes are
        // forced to share a decade range so the geometry is exact). Each
        // point is coloured by which bucket of cumulative time it falls in.
        // Cross-highlight between cost-map dots and query table rows. Both
        // sides tag their element with data-q-id="<query.id>"; this handler
        // is wired through inline onmouseenter/onmouseleave to avoid having
        // to re-attach listeners every time the SQL Performance section is
        // re-rendered. The optional `scroll` flag (passed only by the dots)
        // nudges the matching row into the visible area of its scrollable
        // container — when the row is already in view it is a no-op.
        function highlightQuery(id, on, scroll) {
            // Query table rows tag themselves with data-q-id. Toggle the row's
            // highlight and, when the trigger is a cost-map dot, scroll it into
            // view. The cost-map point itself is enlarged via costMapHighlight.
            document.querySelectorAll('tr[data-q-id~="' + id + '"]').forEach(el => {
                el.classList.toggle('q-row-hover', on);
                if (on && scroll) {
                    const c = el.closest('.table-container');
                    if (c) {
                        const rowTop = el.offsetTop;
                        const rowBot = rowTop + el.offsetHeight;
                        if (rowTop < c.scrollTop || rowBot > c.scrollTop + c.clientHeight) {
                            c.scrollTo({
                                top: rowTop - (c.clientHeight - el.offsetHeight) / 2,
                                behavior: 'smooth',
                            });
                        }
                    }
                }
            });
            // Mirror the highlight onto the uPlot cost map (enlarge its point).
            if (window.costMapHighlight) window.costMapHighlight(id, on);
        }

        function showQueryModal(queryId, opts = {}) {
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

            // 4. Fallback: a triggering-query entry inside top_events.
            //    We promote it to a minimal q object so the detail view can
            //    still render its normalized form and the cross-link to the
            //    "Events triggered" section will fire even when the query
            //    was never timed (no log_min_duration_statement on it).
            if (!q && !lockQ && !tempQ) {
                const topEvents = analysisData.top_events || [];
                let tq = null;
                for (const ev of topEvents) {
                    if (!ev.triggering_queries) continue;
                    tq = ev.triggering_queries.find(t => t.id === queryId);
                    if (tq) break;
                }
                if (tq) {
                    q = {
                        id: tq.id,
                        normalized_query: tq.normalized_query,
                        // Synthesised so the existing detail renderer
                        // shows a sensible header. Real metrics stay
                        // absent so the modal does not pretend it has
                        // information it does not.
                        count: 0,
                        type: '',
                        _triggerOnly: true,
                    };
                }
            }

            // If nothing found, show just the text
            if (!q && !lockQ && !tempQ) {
                document.getElementById('queryModalBody').innerHTML = renderBackBar() + '<div class="query-detail-sql">' + esc(queryId) + '</div>';
                document.getElementById('queryModal').open();
                if (opts.flashId) flashAndScroll(opts.flashId);
                return;
            }

            // Get executions and temp events
            const allExecs = analysisData.sql_performance?.executions || [];
            const execs = q ? allExecs.filter(e => e.query_id === q.id) : [];
            const allTempEvents = analysisData.temp_files?.events || [];
            const tempEvents = q ? allTempEvents.filter(e => e.query_id === q.id) : [];

            // Build detailed view with all available data
            document.getElementById('queryModalBody').innerHTML = renderBackBar() + buildQueryDetailHTML(q, execs, tempEvents, lockQ, tempQ);
            document.getElementById('queryModal').open();
            if (opts.flashId) flashAndScroll(opts.flashId);

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
                // Dimensions inlined into the same flex row as
                // TYPE / COUNT / TOTAL / AVG / MAX so the "who ran
                // this" answer sits right next to the duration stats.
                // Placed BEFORE Prepared as so the wide prepared-names
                // grid (which forces a row break) doesn't push the
                // dimensions below it.
                const dimAxes = [
                    ['Databases', q.top_databases],
                    ['Users',     q.top_users],
                    ['Apps',      q.top_apps],
                    ['Hosts',     q.top_hosts],
                ];
                dimAxes.forEach(([label, rows]) => {
                    if (!Array.isArray(rows) || rows.length === 0) return;
                    const parts = rows.map(r =>
                        '<span class="qd-dim-inline">'
                        + '<span class="qd-dim-name">' + esc(r.name) + '</span>'
                        + '<span class="qd-dim-count">' + fmt(r.count) + '</span>'
                        + '</span>'
                    ).join('<span class="qd-dim-sep">·</span>');
                    html += '<div class="qd-stat qd-stat-dim"><div class="qd-stat-label">' + label + '</div><div class="qd-stat-value qd-stat-value-dims">' + parts + '</div></div>';
                });
                if (q.prepared_names && q.prepared_names.length > 0) {
                    const names = q.prepared_names;
                    const single = names.length === 1;
                    const labelMeta = single
                        ? ''
                        : '<span class="qd-meta">' + names.length + ' names</span>';
                    let namesHtml;
                    if (single) {
                        namesHtml = esc(names[0]);
                    } else {
                        // Compact grid: 1 row when ≤8 names, 2 rows for 9-16,
                        // capped at 8 columns beyond. Each cell gets a 3-tone
                        // class so its 4 neighbours always differ visually.
                        const n = names.length;
                        const cols = n <= 8 ? n : Math.min(8, Math.ceil(n / 2));
                        const cellClass = (i) => {
                            const r = Math.floor(i / cols), c = i % cols;
                            // even row: A B A B ... ; odd row: B C B C ...
                            if (r % 2 === 0) return c % 2 === 0 ? 'qd-cell-a' : 'qd-cell-b';
                            return c % 2 === 0 ? 'qd-cell-b' : 'qd-cell-c';
                        };
                        const cells = names.map((nm, i) =>
                            '<span class="' + cellClass(i) + '">' + esc(nm) + '</span>'
                        ).join('');
                        namesHtml = '<div class="qd-names-grid" style="grid-template-columns: repeat(' + cols + ', 1fr)">' + cells + '</div>';
                    }
                    const cls = single ? 'qd-stat' : 'qd-stat qd-stat-wide';
                    html += '<div class="' + cls + '"><div class="qd-stat-label">Prepared as' + labelMeta + '</div><div class="qd-stat-value">' + namesHtml + '</div></div>';
                }
                html += '</div>';
                if (execs.length > 0) {
                    html += '<div class="qd-chart-container">';
                    html += '<div class="qd-chart-header"><span class="qd-chart-title">Execution Over Time</span><button class="btn-export-png" onclick="exportChartById(\'qd-chart-combined\', \'Query Execution Over Time\')" title="Export as PNG">⬇ PNG</button></div>';
                    html += '<div id="qd-chart-combined" style="height: 180px;"></div>';
                    html += '<div class="chart-legend">';
                    html += '<span class="chart-legend-item" data-chart="qd-chart-combined" data-series="count" onclick="toggleCombinedSeries(\'qd-chart-combined\', \'count\')"><span class="chart-legend-bar chart-legend-bar--count"></span>Count</span>';
                    html += '<span class="chart-legend-item" data-chart="qd-chart-combined" data-series="duration" onclick="toggleCombinedSeries(\'qd-chart-combined\', \'duration\')"><span class="chart-legend-bar chart-legend-bar--duration"></span>Duration</span>';
                    html += '<span><span style="display:inline-block;width:16px;height:0;border-top:2px dashed var(--text-muted);vertical-align:middle;margin-right:4px;"></span>Median</span>';
                    html += '</div>';
                    html += '</div>';
                    html += buildQdDurationDistribution(execs);
                }
                html += '</div>';
            }

            // EVENTS section — promoted to right after Query Info so
            // the operational signal ("this query triggers these
            // errors") is the first thing a reader sees, before the
            // LOCKS / TEMP FILES / Normalized Query / Slowest Run
            // blocks. Same content as the old position below; just
            // moved up.
            const eventsForThisQueryEarly = findEventsTriggeredBy(q?.id);
            if (eventsForThisQueryEarly.length > 0) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title">Events triggered <span class="qd-meta">' + eventsForThisQueryEarly.length + ' distinct</span></div>';
                html += buildQueryEventsTable(eventsForThisQueryEarly, q?.id);
                html += '</div>';
            }

            // LOCKS section (from locks.queries)
            if (lockQ) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="color: var(--warning)">Lock Waits</div>';
                html += '<div class="qd-stats">';
                html += '<div class="qd-stat"><div class="qd-stat-label">Acquired</div><div class="qd-stat-value">' + fmt(lockQ.acquired_count || 0) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Still Waiting</div><div class="qd-stat-value">' + fmt(lockQ.still_waiting_count || 0) + '</div></div>';
                html += '<div class="qd-stat"><div class="qd-stat-label">Total Wait</div><div class="qd-stat-value">' + (lockQ.total_wait_time || '-') + '</div></div>';
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
                html += '<div class="qd-section-title" style="display: flex; justify-content: space-between; align-items: center;">Normalized Query<button class="copy-btn-inline" onclick="navigator.clipboard.writeText(\'' + escForJsAttr(queryText) + '\');this.textContent=\'Copied!\';setTimeout(()=>this.textContent=\'Copy\',1500)">Copy</button></div>';
                html += '<div class="query-detail-sql">';
                html += formatSQL(queryText);
                html += '</div>';
                html += '</div>';
            }

            // SLOWEST RUN section (params substituted) or RAW QUERY fallback
            if (q?.slowest_run?.query_with_params) {
                const sr = q.slowest_run;
                const ts = sr.timestamp || '';
                const pid = sr.pid || '';
                const dur = (typeof sr.duration_ms === 'number')
                    ? fmtMs(sr.duration_ms)
                    : '';
                const parts = [];
                if (dur) parts.push(dur);
                if (ts) parts.push(ts);
                if (pid) parts.push('pid=' + pid);
                if (sr.database) parts.push('db=' + sr.database);
                if (sr.user) parts.push('user=' + sr.user);
                if (sr.app) parts.push('app=' + sr.app);
                if (sr.host) parts.push('host=' + sr.host);
                const meta = parts.length
                    ? '<span class="qd-meta">' + esc(parts.join(', ')) + '</span>'
                    : '';
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="display: flex; justify-content: space-between; align-items: center;"><span>Slowest Run' + meta + '</span><button class="copy-btn-inline" onclick="navigator.clipboard.writeText(\'' + escForJsAttr(sr.query_with_params) + '\');this.textContent=\'Copied!\';setTimeout(()=>this.textContent=\'Copy\',1500)">Copy</button></div>';
                html += '<div class="query-detail-sql">';
                html += esc(sr.query_with_params);
                html += '</div>';
                html += '</div>';
            } else if (q?.raw_query && q.raw_query !== q.normalized_query) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="display: flex; justify-content: space-between; align-items: center;">Example Query<button class="copy-btn-inline" onclick="navigator.clipboard.writeText(\'' + escForJsAttr(q.raw_query) + '\');this.textContent=\'Copied!\';setTimeout(()=>this.textContent=\'Copy\',1500)">Copy</button></div>';
                html += '<div class="query-detail-sql">';
                html += esc(q.raw_query);
                html += '</div>';
                html += '</div>';
            }

            // EXECUTION PLAN section (from auto_explain)
            if (q?.plan) {
                html += '<div class="qd-section">';
                html += '<div class="qd-section-title" style="display: flex; align-items: center; gap: 4px;">Execution Plan <ql-tooltip text="Captured by auto_explain. Shows the last observed plan for this query signature.">i</ql-tooltip>';
                html += '<button style="margin-left: auto;" class="btn-visualize" onclick="visualizePlan()">Visualize</button>';
                html += '<button class="copy-btn-inline" onclick="navigator.clipboard.writeText(document.getElementById(\'plan-text\').textContent);this.textContent=\'Copied!\';setTimeout(()=>this.textContent=\'Copy\',1500)">Copy</button>';
                html += '</div>';
                html += '<div id="plan-text" class="query-detail-sql" style="white-space:pre;font-size:0.75rem;">';
                html += esc(q.plan);
                html += '</div>';
                // Escape <, >, & in the embedded JSON so log-derived plan text
                // containing "</script>" can't terminate the element early and
                // inject HTML. JSON.parse decodes </>/& back.
                const planJSON = JSON.stringify({plan: q.plan, sql: q.normalized_query || '', id: q.id || ''})
                    .replace(/&/g, '\\u0026').replace(/</g, '\\u003c').replace(/>/g, '\\u003e');
                html += '<script type="application/json" id="plan-data">' + planJSON + '<\/script>';
                html += '</div>';
            }

            return html;
        }

        // Render modal charts after DOM update
        // Shared "Open on explain.dalibo.com?" confirmation + POST flow.
        // Used both by the Visualize button inside the Query Detail modal
        // (visualizePlan) and by the per-row eye button in the query table
        // (visualizePlanFor) — only the plan/sql/title source differs.
        function explainDaliboFlow(plan, sql, idForTitle) {
            if (!plan) return;
            let overlay = document.getElementById('visualize-confirm');
            if (!overlay) {
                overlay = document.createElement('div');
                overlay.id = 'visualize-confirm';
                overlay.className = 'modal-overlay';
                overlay.innerHTML = `
                    <div style="max-width:440px;padding:24px;text-align:center;background:var(--bg);border-radius:8px;box-shadow:0 4px 24px rgba(0,0,0,0.3);margin:auto;">
                        <p style="margin:0 0 8px;font-weight:600;font-size:1rem;">Open on explain.dalibo.com?</p>
                        <p style="margin:0 0 20px;font-size:0.85rem;color:var(--text-muted);">The execution plan and query will be sent to <a href="https://explain.dalibo.com/about" target="_blank" style="color:var(--primary);">explain.dalibo.com</a> for visualization.</p>
                        <div style="display:flex;gap:8px;justify-content:center;">
                            <button id="visualize-cancel" style="padding:6px 20px;border:1px solid var(--border);color:var(--text-muted);background:transparent;border-radius:4px;cursor:pointer;font-weight:600;font-size:0.75rem;text-transform:uppercase;">Cancel</button>
                            <button id="visualize-ok" style="padding:6px 20px;background:var(--accent);color:#fff;border:none;border-radius:4px;cursor:pointer;font-weight:600;font-size:0.75rem;text-transform:uppercase;">Open</button>
                        </div>
                    </div>`;
                document.body.appendChild(overlay);
            }
            overlay.classList.add('active');

            const cancel = document.getElementById('visualize-cancel');
            const ok = document.getElementById('visualize-ok');
            const cleanup = () => { overlay.classList.remove('active'); };
            cancel.onclick = cleanup;
            overlay.onclick = (e) => { if (e.target === overlay) cleanup(); };

            ok.onclick = () => {
                cleanup();
                const form = document.createElement('form');
                form.method = 'POST';
                form.action = 'https://explain.dalibo.com/new';
                form.target = '_blank';
                const planInput = document.createElement('input');
                planInput.type = 'hidden';
                planInput.name = 'plan';
                planInput.value = plan;
                form.appendChild(planInput);
                if (sql) {
                    const sqlInput = document.createElement('input');
                    sqlInput.type = 'hidden';
                    sqlInput.name = 'sql';
                    sqlInput.value = sql;
                    form.appendChild(sqlInput);
                }
                if (idForTitle) {
                    const titleInput = document.createElement('input');
                    titleInput.type = 'hidden';
                    titleInput.name = 'title';
                    titleInput.value = 'quellog_' + idForTitle;
                    form.appendChild(titleInput);
                }
                document.body.appendChild(form);
                form.submit();
                document.body.removeChild(form);
            };
        }

        function visualizePlan() {
            const dataEl = document.getElementById('plan-data');
            if (!dataEl) return;
            const data = JSON.parse(dataEl.textContent);
            explainDaliboFlow(data.plan, data.sql, data.id);
        }

        // Triggered from the per-row eye button in the query table — looks
        // up the query by id in analysisData and reuses the same flow.
        function visualizePlanFor(queryId) {
            const q = analysisData?.sql_performance?.queries?.find(x => x.id === queryId);
            if (!q || !q.plan) return;
            explainDaliboFlow(q.plan, q.normalized_query || '', queryId);
        }

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
            const maxY = safeMax(yData) || 1; // safeMax: yData (occurrence sparkline) can be large

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
                html += '<div style="flex: 1; height: 18px; border-radius: 4px; overflow: hidden;">';
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
            const sizeContainerId = 'modal-chart-' + incrementModalChartCounter();
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
            const countContainerId = 'modal-chart-' + incrementModalChartCounter();
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

        // Cleanup modal charts when modal closes
        document.getElementById('queryModal').addEventListener('modal-close', () => {
            modalCharts.forEach(c => c.destroy());
            modalCharts.length = 0;
        });

        // Modal navigation stack lifecycle — clear the trail whenever
        // the user closes a modal "for real" (Escape, backdrop click,
        // the × button). Programmatic closes triggered by our own
        // navigateToX/modalBack set _suppressStackClear first so the
        // stack survives the close event.
        document.getElementById('queryModal').addEventListener('modal-close', () => {
            if (!_suppressStackClear) modalStack = [];
        });
        document.getElementById('eventModal').addEventListener('modal-close', () => {
            if (!_suppressStackClear) modalStack = [];
        });

        // Local state for file info display
        let currentFileInfo = null;

        window.resetAnalysis = function() {
            // Reset state
            clearFilterSelections();
            setAppliedFilters({});
            currentFileInfo = null;
            fileInput.value = '';
            // Open file picker directly
            fileInput.click();
        };

        // Filter state initialization
        setupFilterEventListeners();
        exposeFilterGlobals();

        window.applyFilters = async function() {
            // A filter / Apply / Clear renders a single report, so leave split
            // mode first (drop split state + reset the Split control to Off).
            if (window.QL_SPLIT) {
                window.stopPeriodNav();
                delete window.REPORT_PERIODS;
                const sc = document.getElementById('splitCount');
                if (sc) sc.textContent = '';
                document.querySelectorAll('#splitControl .filter-dropdown-item')
                    .forEach((it, i) => it.classList.toggle('selected', i === 0));
            }

            // Build filters object from current UI state
            const filters = buildFiltersObject();

            // Separate dimension filters from time filters
            const { begin, end, ...dimensionFilters } = filters;
            const hasDimensionFilters = Object.keys(dimensionFilters).length > 0;
            const hasTimeFilter = begin || end;

            // Store what we're applying for comparison
            const newApplied = {};
            for (const key of Object.keys(currentFilters)) {
                newApplied[key] = [...currentFilters[key]];
            }
            if (begin) newApplied._begin = begin;
            if (end) newApplied._end = end;
            setAppliedFilters(newApplied);

            // Close dropdowns
            closeAllDropdowns();

            // Check if only time filter changed (dimension filters unchanged)
            const originalData = getOriginalReportData();
            const canUseClientSideTimeFilter = originalData && !hasDimensionFilters;

            // Use client-side time filtering when possible (faster, no re-parse)
            if (canUseClientSideTimeFilter || window.REPORT_MODE) {
                try {
                    let data;
                    if (hasTimeFilter) {
                        const filterBegin = begin || originalData?.summary?.start_date;
                        const filterEnd = end || originalData?.summary?.end_date;
                        data = applyReportTimeFilter(filterBegin, filterEnd);
                    } else {
                        data = resetReportTimeFilter();
                    }

                    if (data) {
                        setAnalysisData(data);
                        const filename = window.REPORT_MODE ? (data.meta?.filename || 'Report') : currentFileName;
                        const filesize = window.REPORT_MODE ? (data.meta?.filesize || 0) : currentFileSize;
                        renderResults(data, filename, filesize, false);
                        console.log('[quellog] Time filtered (client-side)');
                        // Ephemeral pulse on the freshly-rendered selection to
                        // signal the report just refreshed.
                        requestAnimationFrame(() => {
                            const r = document.getElementById('filterTimeRange');
                            if (r) { r.classList.remove('pulse'); void r.offsetWidth; r.classList.add('pulse'); }
                        });
                    }
                } catch (err) {
                    console.error('Client-side filter failed:', err);
                } finally {
                    updateApplyButton();
                }
                return;
            }

            // WASM mode: re-parse with filters (dimension filters or first parse)
            if (!currentFileContent) return;

            // Show filtering indicator
            const filterStatus = document.getElementById('filterStatus');
            filterStatus?.classList.add('active');

            await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));

            try {
                // Only pass dimension filters to WASM, time filter will be applied client-side
                const wasmFilters = hasDimensionFilters ? dimensionFilters : null;
                const filtersJson = wasmFilters ? JSON.stringify(wasmFilters) : null;

                // Reinitialize WASM to reset memory (gc=leaking accumulates)
                if (typeof reinitWasm === 'function') {
                    await reinitWasm();
                }

                // Time the parsing — same Uint8Array fast-path as the
                // initial drop, see processFile.
                const parseStart = performance.now();
                const resultJson = (currentFileContent instanceof Uint8Array)
                    ? quellogParseBytes(currentFileContent, filtersJson)
                    : quellogParse(currentFileContent, filtersJson);
                const parseEnd = performance.now();
                const parseTimeMs = Math.round(parseEnd - parseStart);

                let data = JSON.parse(resultJson);

                if (data.error) throw new Error(data.error);

                // Store parse time for display
                data._parseTimeMs = parseTimeMs;

                // Store as base data for subsequent time filtering
                setOriginalReportData(data);

                // Apply time filter client-side if needed
                if (hasTimeFilter) {
                    const filterBegin = begin || data.summary?.start_date;
                    const filterEnd = end || data.summary?.end_date;
                    data = applyReportTimeFilter(filterBegin, filterEnd);
                }

                setAnalysisData(data);
                renderResults(data, currentFileName, currentFileSize, false);
                console.log(`[quellog] Filtered: ${data.meta?.entries || 0} entries in ${parseTimeMs}ms`);
            } catch (err) {
                console.error('Filter failed:', err);
            } finally {
                filterStatus?.classList.remove('active');
                updateApplyButton();
            }
        };

        // applySplit re-runs the parse splitting the stream by intervalSec and
        // drives the shared period navigator with the resulting blobs. 0 = Off:
        // leave split mode and re-render a single report.
        window.applySplit = async function(intervalSec) {
            if (!currentFileContent) return;
            if (!intervalSec) {
                window.stopPeriodNav();
                delete window.REPORT_PERIODS;
                window.applyFilters();
                return;
            }
            const filterStatus = document.getElementById('filterStatus');
            filterStatus?.classList.add('active');
            await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
            try {
                if (typeof reinitWasm === 'function') await reinitWasm();
                const filters = buildFiltersObject();
                const filtersJson = JSON.stringify(filters);
                const bytes = (currentFileContent instanceof Uint8Array)
                    ? currentFileContent : new TextEncoder().encode(currentFileContent);
                const resultJson = quellogSplitBytes(bytes, intervalSec, currentFileName, filtersJson);
                const r = JSON.parse(resultJson);
                if (r.error) throw new Error(r.error);
                window.REPORT_PERIODS = r;
                window.startPeriodNav();
                console.log(`[quellog] Split into ${r.length} periods`);
            } catch (err) {
                console.error('Split failed:', err);
                alert('Split failed: ' + err.message);
            } finally {
                filterStatus?.classList.remove('active');
            }
        };

        // selectSplit handles a click on a Split menu item: mark it selected,
        // show the interval in the trigger badge, close the menu, apply.
        window.selectSplit = function(sec, el) {
            const menu = el.closest('.filter-dropdown-menu');
            if (menu) menu.querySelectorAll('.filter-dropdown-item').forEach(i => i.classList.remove('selected'));
            el.classList.add('selected');
            const count = document.getElementById('splitCount');
            if (count) count.textContent = sec ? el.textContent.trim() : '';
            document.querySelector('.filter-dropdown[data-category="split"]')?.classList.remove('open');
            window.applySplit(sec);
        };

        window.clearAllFilters = function() {
            resetTimeInputs();
            clearFilterSelections();
            updateAllDropdownTriggers();

            // Apply immediately (clear = apply with no filters)
            if (currentFileContent || window.REPORT_MODE) {
                window.applyFilters();
            }
        };

        // Helpers (fmt, fmtDuration, fmtBytes, fmtMs, fmtDur, esc, safeMax, safeMin imported from js/utils.js)

        // Build no-data message for sections without data
        function buildNoDataMessage(hint) {
            return `
                <div class="no-data-message">
                    <div class="no-data-text">No data available</div>
                    <div class="no-data-hint">Check: ${hint}</div>
                </div>
            `;
        }

        // Initialize theme (from theme.js module)
        initTheme();

        // Update footer version from WASM (or report mode)
        const versionEl = document.getElementById('quellog-version');
        if (versionEl && typeof window.quellogVersion === 'function') {
            const v = window.quellogVersion();
            if (v) versionEl.textContent = 'quellog ' + v;
        }

        // Expose functions for inline onclick handlers and report mode
        window.renderResults = renderResults;
        window.setAnalysisData = setAnalysisData;
        // Default per-period blob decoder used by the period navigator in the
        // WASM tool. The standalone --split report overrides this with its own
        // lazy fzstd loader (fzstd is bundled eagerly here).
        if (!window.decompressData) {
            window.decompressData = async function (b64) {
                const bin = atob(b64);
                const bytes = new Uint8Array(bin.length);
                for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
                return JSON.parse(new TextDecoder().decode(unzstd(bytes)));
            };
        }
        window.showQueryModal = showQueryModal;
        window.highlightQuery = highlightQuery;
        window.visualizePlan = visualizePlan;
        window.visualizePlanFor = visualizePlanFor;
        window.showSqlOvView = showSqlOvView;
        window.showVacuumMainSort = showVacuumMainSort;
        window.showVacuumBufferSort = showVacuumBufferSort;
        window.showVacuumView = showVacuumView;
        window.showMaintRibbon = showMaintRibbon;
        window.showAnalyzeSort = showAnalyzeSort;
        window.copyQuery = copyQuery;
        window.showEventDetail = showEventDetail;
        window.navigateToQuery = navigateToQuery;
        window.navigateToEvent = navigateToEvent;
        window.modalBack = modalBack;
        window.closeModal = closeModal;
        window.toggleTheme = toggleTheme;
        window.closeChartModal = closeChartModal;
        window.updateModalInterval = updateModalInterval;
        window.resetModalZoom = resetModalZoom;
        window.exportChartPNG = exportChartPNG;
        window.exportChartById = exportChartById;
        window.resetChartZoom = resetChartZoom;
        window.openChartModal = openChartModal;
        window.updateChartInterval = updateChartInterval;
        window.toggleCombinedSeries = toggleCombinedSeries;
        window.resetCostMapZoom = resetCostMapZoom;
        window.openCostMapModal = openCostMapModal;
        window.loadDemo = loadDemo;

