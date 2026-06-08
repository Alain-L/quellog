// ES Module imports
import { fmt, fmtDuration, fmtBytes, fmtMs, fmtDur, parseDurToMs, esc, escForJsAttr, truncQuery, safeMax, safeMin } from './js/utils.js';
import {
    wasmModule, wasmReady, analysisData, currentFileContent, currentFileName, currentFileSize, originalDimensions,
    charts, modalCharts, modalChartsData, modalChartCounter, chartIntervalMap, defaultInterval,
    currentFilters, appliedFilters,
    setWasmModule, setWasmReady, setAnalysisData, setCurrentFileContent, setCurrentFileName, setCurrentFileSize,
    setOriginalDimensions, incrementModalChartCounter, setAppliedFilters, clearAllCharts
} from './js/state.js';
import { initTheme, toggleTheme } from './js/theme.js';
import { gunzipBuffer, unzstd, detectFormat, decompress, extractTar, prepareContent } from './js/compression.js';
import {
    showFilterBar, hideFilterBar, initFilterBar, closeAllDropdowns,
    updateAllDropdownTriggers, updateApplyButton, updateTimeSlider,
    buildFiltersObject, resetTimeInputs, clearFilterSelections,
    setupFilterEventListeners, exposeFilterGlobals
} from './js/filters.js';
import {
    MAX_FILE_SIZE, setProgress, initWasmInstance, loadWasm,
    setupDragDrop, showLoading, hideLoading, showDropZone
} from './js/file-handler.js';
import {
    chartData, createTimeChart, createDurationChart, createCombinedSQLChart,
    createConcurrentChart, createHistogramChart, createCheckpointChart, createWALDistanceChart, createCombinedTempFilesChart,
    buildChartContainer, closeChartModal, updateModalInterval, resetModalZoom, exportChartPNG,
    resetChartZoom, openChartModal, updateChartInterval, toggleCombinedSeries, exportChartById
} from './js/charts.js';
import {
    setOriginalReportData, getOriginalReportData, applyReportTimeFilter, resetReportTimeFilter
} from './js/report-filter.js';

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

        async function processFile(file) {
            // Check both module state and window (standalone mode uses window.wasmReady)
            if (!wasmReady && !window.wasmReady) { alert('WASM not ready'); return; }

            // Check file size limit
            if (file.size > MAX_FILE_SIZE) {
                alert(`File too large (${fmtBytes(file.size)}). Maximum size: ${fmtBytes(MAX_FILE_SIZE)}.\n\nFor larger files, use the command-line version:\n  quellog ${file.name}`);
                return;
            }

            showLoading(dropZone, loading, results);
            clearAllCharts();
            setProgress(5, 'Initializing...');

            try {
                // Reinitialize WASM to free previous memory (gc=leaking workaround)
                await initWasmInstance();

                console.log(`[quellog] Parsing: ${file.name} (${fmtBytes(file.size)})`);
                setProgress(10, 'Reading file...');

                // Handle compressed files and tar archives
                const content = await prepareContent(file);

                // Single static "Crunching log entries…" message during the
                // WASM parse. Cycling phrases were tried (CSS-only opacity
                // keyframes, clip-path wipe, transform slides) but none
                // animated reliably across the JS-thread freeze in our
                // tinygo wasm setup. Spinner + static label is the honest
                // fallback — at least the user knows something is running.
                setProgress(50, 'Crunching log entries…');

                // Store for re-filtering
                setCurrentFileContent(content);
                setCurrentFileName(file.name);
                setCurrentFileSize(file.size);
                setOriginalDimensions(null);  // Reset for new file

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
            // JSON uses vacuum_count, analyze_count (not autovacuum/autoanalyze)
            // vacuum_table_counts and analyze_table_counts are objects {table: count}
            // vacuum_space_recovered is {table: "XX KB"} for tables that recovered space
            const spaceRecovered = m.vacuum_space_recovered || {};
            const vacTables = m.vacuum_table_counts ? Object.entries(m.vacuum_table_counts).map(([t, c]) => ({table: t, count: c, removed: spaceRecovered[t]})).sort((a,b) => b.count - a.count) : [];
            const anaTables = m.analyze_table_counts ? Object.entries(m.analyze_table_counts).map(([t, c]) => ({table: t, count: c})).sort((a,b) => b.count - a.count) : [];
            const hasVacTables = vacTables.length > 0;
            const hasAnaTables = anaTables.length > 0;
            const maxVac = vacTables[0]?.count || 1;
            const maxAna = anaTables[0]?.count || 1;
            // Calculate total space recovered
            const totalRecovered = Object.values(spaceRecovered).reduce((sum, size) => sum + parseSizeToBytes(size), 0);
            const elapsedTotalSec = m.total_vacuum_elapsed_seconds || 0;
            const elapsedStr = elapsedTotalSec > 0 ? fmtDuration(elapsedTotalSec * 1000) : '';
            const xminTotal = m.total_tuples_not_yet_removable || 0;
            const slowest = m.slowest_vacuum;
            const topVacTables = m.top_vacuum_tables || [];
            const xminTables = m.xmin_blocked_tables || [];
            return `
                <div class="section" id="maintenance">
                    <div class="section-header">Maintenance</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${m.vacuum_count || 0}</div><div class="stat-label">Vacuum</div></div>
                            ${(m.aggressive_vacuum_count || 0) > 0 ? `<div class="stat-card stat-card--warning"><div class="stat-value">${m.aggressive_vacuum_count}</div><div class="stat-label">Aggressive</div></div>` : ''}
                            ${totalRecovered > 0 ? `<div class="stat-card"><div class="stat-value">${fmtBytes(totalRecovered)}</div><div class="stat-label">Recovered</div></div>` : ''}
                            <div class="stat-card"><div class="stat-value">${m.analyze_count || 0}</div><div class="stat-label">Analyze</div></div>
                            ${elapsedStr ? `<div class="stat-card"><div class="stat-value">${elapsedStr}</div><div class="stat-label">Vacuum Time</div></div>` : ''}
                            ${xminTotal > 0 ? `<div class="stat-card stat-card--alert" title="Dead tuples vacuum could not yet remove — a long-running transaction is holding back the xmin horizon."><div class="stat-value">${fmt(xminTotal)}</div><div class="stat-label">Xmin-blocked</div></div>` : ''}
                        </div>
                        ${slowest ? `
                            <div class="subsection">
                                <div class="subsection-title">Slowest single vacuum</div>
                                <div style="font-size:0.85rem;color:var(--text-muted);">
                                    <strong style="color:var(--text);">${fmtDuration(slowest.elapsed_seconds * 1000)}</strong>
                                    on <code>${esc(slowest.table)}</code>${slowest.timestamp ? ` <span style="color:var(--text-muted);">at ${esc(slowest.timestamp)}</span>` : ''}
                                    ${slowest.tuples_removed ? ` &middot; ${fmt(slowest.tuples_removed)} tuples removed` : ''}
                                </div>
                            </div>
                        ` : ''}
                        ${hasVacTables ? `
                            <div class="subsection">
                                <div class="subsection-title">Top Vacuum Tables</div>
                                <div class="scroll-list scroll-list--maintenance">
                                    ${vacTables.slice(0, 5).map(t => `
                                        <div class="list-item">
                                            <span class="name">${esc(t.table)}</span>
                                            <div class="bar"><div class="bar-fill" style="width: ${t.count/maxVac*100}%"></div></div>
                                            <span class="removed">${t.removed ? t.removed + ' removed' : ''}</span>
                                            <span class="value">${fmt(t.count)}</span>
                                        </div>
                                    `).join('')}
                                </div>
                            </div>
                        ` : ''}
                        ${hasAnaTables ? `
                            <div class="subsection">
                                <div class="subsection-title">Top Analyze Tables</div>
                                <div class="scroll-list scroll-list--maintenance">
                                    ${anaTables.slice(0, 5).map(t => `
                                        <div class="list-item">
                                            <span class="name">${esc(t.table)}</span>
                                            <div class="bar"><div class="bar-fill" style="width: ${t.count/maxAna*100}%"></div></div>
                                            <span class="removed"></span>
                                            <span class="value">${fmt(t.count)}</span>
                                        </div>
                                    `).join('')}
                                </div>
                            </div>
                        ` : ''}
                        ${topVacTables.length > 0 ? `
                            <div class="subsection">
                                <div class="subsection-title">Top Tables by Vacuum Time</div>
                                <div class="scroll-list scroll-list--maintenance">
                                    ${topVacTables.slice(0, 5).map(t => `
                                        <div class="list-item">
                                            <span class="name">${esc(t.table)}</span>
                                            <div class="bar"><div class="bar-fill" style="width: ${t.total_elapsed_seconds/(topVacTables[0].total_elapsed_seconds||1)*100}%"></div></div>
                                            <span class="removed">${t.vacuum_count}×</span>
                                            <span class="value">${fmtDuration(t.total_elapsed_seconds * 1000)}</span>
                                        </div>
                                    `).join('')}
                                </div>
                            </div>
                        ` : ''}
                        ${xminTables.length > 0 ? `
                            <div class="subsection">
                                <div class="subsection-title" title="Long-running transactions are blocking vacuum from removing these tuples — a stuck xmin horizon eventually leads to wraparound emergencies.">Tables blocked by stuck xmin horizon</div>
                                <div class="scroll-list scroll-list--maintenance">
                                    ${xminTables.slice(0, 5).map(t => `
                                        <div class="list-item">
                                            <span class="name">${esc(t.table)}</span>
                                            <div class="bar"><div class="bar-fill" style="width: ${t.tuples_not_yet_removable/(xminTables[0].tuples_not_yet_removable||1)*100}%; background: var(--danger);"></div></div>
                                            <span class="removed">${t.vacuum_count}×</span>
                                            <span class="value">${fmt(t.tuples_not_yet_removable)} rows</span>
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
                            <div class="stat-card"><div class="stat-value">${fmtDur(l.total_wait_time) || '-'}</div><div class="stat-label">Total</div></div>
                        </div>
                        ${hasLockTypes || hasResTypes || hasRelations ? `
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
                                ${hasRelations ? `
                                    <div style="flex: 1; min-width: 120px;">
                                        <div class="subsection-title" style="margin-top: 0;">Relations</div>
                                        <div class="query-types">
                                            ${relations.map(t => `
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
                                                const wa = parseFloat(a.total_wait_time) || 0;
                                                const wb = parseFloat(b.total_wait_time) || 0;
                                                return wb - wa;
                                            }).slice(0, 10).map(q => `
                                                <tr>
                                                    <td class="query-cell" onclick="showQueryModal('${esc(q.id)}')">${esc(truncQuery(q.normalized_query))}</td>
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
                                                    <td class="num">${fmtMs(b.totalWaitMs / b.count)}</td>
                                                    <td class="num">${fmtMs(b.totalWaitMs)}</td>
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
                    <td><span class="query-type"><span class="name">${t.type}</span></span></td>
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

            // Cost-map data: every query with a real avg duration. Drawn
            // inline in the left column rather than in a modal.
            const costMapQueries = queries.filter(q => (q.count || 0) > 0 && (q.avg_time_ms || 0) > 0);

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
                                ${costMapQueries.length > 1 ? `
                                    <div class="chart-container">
                                        <div class="chart-controls">
                                            <span class="subsection-title" style="margin: 0; font-size: 0.7rem;">Cost Map<ql-tooltip text="Each dot is a normalized query, positioned by execution count (X) and average duration (Y) on log-log scales. The 45° iso-curves mark constant cumulative time (count × avg). Click a dot to open its details.">i</ql-tooltip></span>
                                        </div>
                                        ${buildCostMapSvg(costMapQueries)}
                                    </div>
                                ` : ''}
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
            document.getElementById('queryModal').open();
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
            // The cost-map dot for a cluster stores a space-separated id
            // list, so the dot still matches when any of its members is
            // hovered from the table side — the [attr~="value"] selector
            // semantics give us that for free.
            document.querySelectorAll('[data-q-id~="' + id + '"]').forEach(el => {
                if (el.tagName === 'circle') {
                    if (on) {
                        if (!el.hasAttribute('data-r-orig')) el.setAttribute('data-r-orig', el.getAttribute('r'));
                        el.setAttribute('r', '7');
                        el.setAttribute('stroke', 'var(--text)');
                        el.setAttribute('stroke-width', '1.5');
                        el.setAttribute('opacity', '1');
                    } else {
                        el.setAttribute('r', el.getAttribute('data-r-orig') || '3');
                        el.setAttribute('stroke', 'var(--bg)');
                        el.setAttribute('stroke-width', '0.5');
                        el.setAttribute('opacity', '0.85');
                    }
                } else if (el.tagName === 'TR') {
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
                }
            });
        }

        function buildCostMapSvg(queries) {
            // Symmetric padding so the plot area is a perfect square (W == H).
            // Axis tick labels still fit (~36 px on the left for "100ms"-class
            // labels). No axis titles — the section's tooltip covers them.
            const SIZE = 600;
            const PAD_L = 40, PAD_R = 18, PAD_T = 18, PAD_B = 40;
            const W = SIZE - PAD_L - PAD_R; // 542
            const H = SIZE - PAD_T - PAD_B; // 542

            const pts = queries.map(q => ({
                id: q.id,
                type: q.type || q.query_type || '',
                count: q.count,
                avg: q.avg_time_ms,
                total: q.total_time_ms || (q.count * q.avg_time_ms),
            }));

            // Log-log domain padded to half-decade boundaries. We then expand
            // the shorter axis so X and Y carry the same number of decades:
            // with W = H, that forces every iso-cumulative line (slope -1 in
            // log space) to render at exactly 45° on screen.
            const xs = pts.map(p => Math.log10(p.count));
            const ys = pts.map(p => Math.log10(p.avg));
            const halfFloor = v => Math.floor(v * 2) / 2;
            const halfCeil  = v => Math.ceil(v * 2) / 2;
            let logXMin = halfFloor(Math.min(...xs));
            let logXMax = Math.max(halfCeil(Math.max(...xs)), logXMin + 1);
            let logYMin = halfFloor(Math.min(...ys));
            let logYMax = Math.max(halfCeil(Math.max(...ys)), logYMin + 1);
            const xRange = logXMax - logXMin;
            const yRange = logYMax - logYMin;
            const range = Math.max(xRange, yRange);
            if (xRange < range) {
                const pad = (range - xRange) / 2;
                logXMin -= pad;
                logXMax += pad;
            }
            if (yRange < range) {
                const pad = (range - yRange) / 2;
                logYMin -= pad;
                logYMax += pad;
            }
            // Counts are integers >= 1, so the X axis cannot legitimately drop
            // below 10^0 = 1. If padding pushed it below 0, shift the whole
            // X range upward so the minimum lands exactly at 1 — the range
            // length (and thus the 45° iso-curves) stays intact.
            if (logXMin < 0) {
                logXMax -= logXMin;
                logXMin = 0;
            }
            const x = v => PAD_L + ((Math.log10(v) - logXMin) / (logXMax - logXMin)) * W;
            const y = v => PAD_T + H - ((Math.log10(v) - logYMin) / (logYMax - logYMin)) * H;

            // Discrete 4-bucket coloring drawn from quellog's standard palette
            // (--primary, --warning, --danger, --purple). Each point falls in
            // one of four equal-width log-cumulative buckets — no gradient.
            // Bucket thresholds will be tunable once the visual is validated.
            const BUCKETS = [
                { upTo: 0.25, color: 'var(--primary)' },
                { upTo: 0.50, color: 'var(--warning)' },
                { upTo: 0.75, color: 'var(--danger)' },
                { upTo: 1.01, color: 'var(--purple)' },
            ];
            const ts = pts.map(p => Math.log10(p.total));
            const logTMin = Math.min(...ts);
            const logTMax = Math.max(...ts);
            const colorFor = total => {
                const t = logTMax === logTMin ? 0
                    : (Math.log10(total) - logTMin) / (logTMax - logTMin);
                return BUCKETS.find(b => t <= b.upTo).color;
            };

            const fmtAxisCount = v => {
                if (v >= 1e6) return (v / 1e6).toFixed(v >= 1e7 ? 0 : 1).replace(/\.0$/, '') + 'M';
                if (v >= 1e3) return (v / 1e3).toFixed(v >= 1e4 ? 0 : 1).replace(/\.0$/, '') + 'k';
                return String(v);
            };
            const fmtAxisMs = v => {
                if (v >= 3600000) return (v / 3600000).toFixed(0) + 'h';
                if (v >= 60000) return (v / 60000).toFixed(0) + 'min';
                if (v >= 1000) return (v / 1000).toFixed(0) + 's';
                if (v >= 1) return v.toFixed(0) + 'ms';
                return v.toFixed(1) + 'ms';
            };

            let svg = `<svg viewBox="0 0 ${SIZE} ${SIZE}" xmlns="http://www.w3.org/2000/svg" style="width:100%;max-width:${SIZE}px;display:block;margin:0 auto;">`;
            svg += `<rect x="${PAD_L}" y="${PAD_T}" width="${W}" height="${H}" fill="var(--bg-alt)"/>`;

            // Grid + axis ticks at every decade
            for (let d = Math.ceil(logXMin); d <= Math.floor(logXMax); d++) {
                const v = Math.pow(10, d);
                const xv = x(v);
                svg += `<line x1="${xv}" y1="${PAD_T}" x2="${xv}" y2="${PAD_T + H}" stroke="var(--border)" stroke-width="0.5" stroke-dasharray="2,2"/>`;
                svg += `<text x="${xv}" y="${PAD_T + H + 14}" text-anchor="middle" font-size="10" fill="var(--text-muted)">${fmtAxisCount(v)}</text>`;
            }
            for (let d = Math.ceil(logYMin); d <= Math.floor(logYMax); d++) {
                const v = Math.pow(10, d);
                const yv = y(v);
                svg += `<line x1="${PAD_L}" y1="${yv}" x2="${PAD_L + W}" y2="${yv}" stroke="var(--border)" stroke-width="0.5" stroke-dasharray="2,2"/>`;
                svg += `<text x="${PAD_L - 6}" y="${yv + 3}" text-anchor="end" font-size="10" fill="var(--text-muted)">${fmtAxisMs(v)}</text>`;
            }

            // Each diagonal we draw is an iso-cumulative-time line:
            //   avg = T / count → in log-log a straight 45° line.
            // The set of "ISO" anchors are decorative time landmarks; the
            // two "PCT" diagonals (top 90% / top 99%) are computed from the
            // data and surface where the long tail starts to add up.
            const inRange = (l, lo, hi) => l >= lo - 1e-9 && l <= hi + 1e-9;
            const clipDiagonal = T => {
                const hits = [];
                let yv = T / Math.pow(10, logXMin);
                if (inRange(Math.log10(yv), logYMin, logYMax)) hits.push([Math.pow(10, logXMin), yv]);
                yv = T / Math.pow(10, logXMax);
                if (inRange(Math.log10(yv), logYMin, logYMax)) hits.push([Math.pow(10, logXMax), yv]);
                let xv = T / Math.pow(10, logYMax);
                if (inRange(Math.log10(xv), logXMin, logXMax)) hits.push([xv, Math.pow(10, logYMax)]);
                xv = T / Math.pow(10, logYMin);
                if (inRange(Math.log10(xv), logXMin, logXMax)) hits.push([xv, Math.pow(10, logYMin)]);
                return hits;
            };
            const drawDiagonal = (T, label, opts) => {
                const hits = clipDiagonal(T);
                if (hits.length < 2) return;
                const [p1, p2] = hits;
                svg += `<line x1="${x(p1[0])}" y1="${y(p1[1])}" x2="${x(p2[0])}" y2="${y(p2[1])}" stroke="${opts.stroke}" stroke-width="${opts.width}" stroke-dasharray="${opts.dasharray}" opacity="${opts.opacity}"/>`;
                const labelPt = hits.reduce((acc, p) => p[0] > acc[0] ? p : acc, hits[0]);
                svg += `<text x="${x(labelPt[0]) - 6}" y="${y(labelPt[1]) - 4}" text-anchor="end" font-size="10" font-style="italic" fill="${opts.label}">${label}</text>`;
            };

            // Decorative cumulative-time landmarks
            const ISO = [
                { ms: 1000,     label: '1s' },
                { ms: 60000,    label: '1min' },
                { ms: 3600000,  label: '1h' },
                { ms: 86400000, label: '1d' },
            ];
            ISO.forEach(iso => drawDiagonal(iso.ms, iso.label, {
                stroke: 'var(--text-muted)', width: 0.7, dasharray: '4,3', opacity: 0.5,
                label: 'var(--text-muted)',
            }));

            // "Top X%" diagonals — Pareto thresholds. The smallest per-query
            // total T such that queries whose total exceeds T together sum
            // to X% of the grand total. Above-and-right of the line = those
            // heavy hitters; below-and-left = the remaining (100-X)% of
            // cumulated time. We draw the 10% line ("top 10%": below-left
            // contains 90% of time) and the 1% line ("top 1%": below-left
            // contains 99%).
            const sortedTotals = pts.map(p => p.total).sort((a, b) => b - a);
            const grandTotal = sortedTotals.reduce((a, b) => a + b, 0);
            const topShareThreshold = (frac) => {
                let acc = 0;
                for (let i = 0; i < sortedTotals.length; i++) {
                    acc += sortedTotals[i];
                    if (acc >= frac * grandTotal) return sortedTotals[i];
                }
                return sortedTotals[sortedTotals.length - 1];
            };
            if (grandTotal > 0 && pts.length > 1) {
                const Ttop10 = topShareThreshold(0.10);
                const Ttop1 = topShareThreshold(0.01);
                // Solid green to contrast with the muted gray dashed ISOs.
                drawDiagonal(Ttop1, 'top 1%', {
                    stroke: 'var(--success)', width: 1.1, dasharray: 'none', opacity: 0.75,
                    label: 'var(--success)',
                });
                if (Math.abs(Math.log10(Ttop10) - Math.log10(Ttop1)) > 0.05) {
                    drawDiagonal(Ttop10, 'top 10%', {
                        stroke: 'var(--success)', width: 1.1, dasharray: '6,2', opacity: 0.75,
                        label: 'var(--success)',
                    });
                }
            }

            // Axis frame (no titles — the section tooltip describes the axes).
            svg += `<line x1="${PAD_L}" y1="${PAD_T}" x2="${PAD_L}" y2="${PAD_T + H}" stroke="var(--text)" stroke-width="1"/>`;
            svg += `<line x1="${PAD_L}" y1="${PAD_T + H}" x2="${PAD_L + W}" y2="${PAD_T + H}" stroke="var(--text)" stroke-width="1"/>`;

            // Points are drawn small by default so the cloud feels light and
            // overlapping queries don't visually merge into one blob; the
            // hover handler bumps them up to a clearly-readable size.
            pts.forEach(p => {
                const head = p.type ? `${p.type} · ` : '';
                const tip = `${head}${p.count}× · avg ${fmtAxisMs(p.avg)} · cumulated ${fmtAxisMs(p.total)}`;
                const id = esc(p.id);
                svg += `<circle data-q-id="${id}" cx="${x(p.count).toFixed(1)}" cy="${y(p.avg).toFixed(1)}" r="3" fill="${colorFor(p.total)}" stroke="var(--bg)" stroke-width="0.5" opacity="0.85" onclick="showQueryModal('${id}')" onmouseenter="highlightQuery('${id}', true, true)" onmouseleave="highlightQuery('${id}', false)" style="cursor:pointer"><title>${esc(tip)}</title></circle>`;
            });

            svg += `</svg>`;

            // Legend below the chart — uses the shared .chart-legend styles
            // (same look as Query Activity's legend underneath). Compact gap
            // and short labels so it stays on a single line in the 1/3 column.
            const dot = (bg) => `<span class="chart-legend-bar" style="background:${bg};border-radius:50%;width:8px;height:8px;"></span>`;
            const dash = (color, solid) => `<span style="display:inline-block;width:14px;height:0;border-top:${solid ? '1.5px solid' : '1px dashed'} ${color};vertical-align:middle;"></span>`;
            let legend = '<div class="chart-legend" style="gap:0.6rem;">';
            legend += `<span class="chart-legend-item">${dot('var(--primary)')}low</span>`;
            legend += `<span class="chart-legend-item">${dot('var(--warning)')}med</span>`;
            legend += `<span class="chart-legend-item">${dot('var(--danger)')}high</span>`;
            legend += `<span class="chart-legend-item">${dot('var(--purple)')}extreme</span>`;
            legend += `<span class="chart-legend-item">${dash('var(--text-muted)', false)}iso</span>`;
            legend += `<span class="chart-legend-item">${dash('var(--success)', true)}top 1% / 10%</span>`;
            legend += '</div>';

            return svg + legend;
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
                html += '<script type="application/json" id="plan-data">' + JSON.stringify({plan: q.plan, sql: q.normalized_query || '', id: q.id || ''}) + '<\/script>';
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

            const containerId = 'modal-chart-' + incrementModalChartCounter();
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

            const containerId = 'modal-chart-' + incrementModalChartCounter();
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
        window.showQueryModal = showQueryModal;
        window.highlightQuery = highlightQuery;
        window.visualizePlan = visualizePlan;
        window.visualizePlanFor = visualizePlanFor;
        window.showSqlOvView = showSqlOvView;
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

