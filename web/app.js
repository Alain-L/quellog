// ES Module imports
import { fmt, fmtDuration, fmtBytes, fmtMs, fmtDur, esc, safeMax, safeMin } from './js/utils.js';
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
                setProgress(50, 'Parsing log entries...');

                // Store for re-filtering
                setCurrentFileContent(content);
                setCurrentFileName(file.name);
                setCurrentFileSize(file.size);
                setOriginalDimensions(null);  // Reset for new file

                // Time the actual parsing
                const parseStart = performance.now();
                const resultJson = quellogParse(content);
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
                    // Check data type: checkpoints (stacked), sessions (sweep-line), histogram (pre-computed), duration, combined, tempfiles, or timestamps
                    if (data?.type === 'wal-distance') {
                        createWALDistanceChart(chartId, data);
                    } else if (data?.type === 'checkpoints') {
                        createCheckpointChart(chartId, data);
                    } else if (data?.type === 'sessions') {
                        createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)' });
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
                chartData.set('chart-concurrent', { type: 'sessions', data: c.session_events });
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

            // Compute throughput from wal_distances: distance_kb / write_time per checkpoint
            // We need per-checkpoint write times, but we only have aggregates.
            // Use avg and max from formatted strings: parse "263.22 s" → float
            const parseSeconds = s => { const m = s?.match?.(/([\d.]+)\s*s/); return m ? parseFloat(m[1]) : 0; };
            const parseMB = s => { const m = s?.match?.(/([\d.]+)\s*MB/); if (m) return parseFloat(m[1]); const g = s?.match?.(/([\d.]+)\s*GB/); return g ? parseFloat(g[1]) * 1024 : 0; };
            const avgWriteS = parseSeconds(cp.avg_checkpoint_time);
            const maxWriteS = parseSeconds(cp.max_checkpoint_time);
            const avgDistMB = parseMB(cp.avg_wal_distance);
            const maxDistMB = parseMB(cp.max_wal_distance);
            const avgThroughput = avgWriteS > 0 ? avgDistMB / avgWriteS : 0;
            // Peak throughput: max distance / avg write time (conservative estimate)
            const peakThroughput = avgWriteS > 0 ? maxDistMB / avgWriteS : 0;
            const fmtThroughput = v => {
                if (v <= 0) return '-';
                if (v >= 1024) return (v / 1024).toFixed(1) + ' GB/s';
                if (v >= 1) return v.toFixed(1) + ' MB/s';
                return (v * 1024).toFixed(0) + ' kB/s';
            };

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
                            <div class="stat-card"><div class="stat-value">${fmtThroughput(avgThroughput)}</div><div class="stat-label">Avg Throughput</div></div>
                            <div class="stat-card"><div class="stat-value">${fmtThroughput(peakThroughput)}</div><div class="stat-label">Peak Throughput</div></div>
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
            return `
                <div class="section" id="maintenance">
                    <div class="section-header">Maintenance</div>
                    <div class="section-body">
                        <div class="stat-grid">
                            <div class="stat-card"><div class="stat-value">${m.vacuum_count || 0}</div><div class="stat-label">Vacuum</div></div>
                            ${totalRecovered > 0 ? `<div class="stat-card"><div class="stat-value">${fmtBytes(totalRecovered)}</div><div class="stat-label">Recovered</div></div>` : ''}
                            <div class="stat-card"><div class="stat-value">${m.analyze_count || 0}</div><div class="stat-label">Analyze</div></div>
                        </div>
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
            return `
                <div class="section" id="locks">
                    <div class="section-header ${deadlocks > 0 ? 'danger' : ''}">Locks</div>
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
                        </div>
                        ${hasEvents ? buildChartContainer('chart-tempfiles', 'Temp File Activity', { showFilterBtn: true, tooltip: 'Temp file count and cumulative size over time. Created when queries exceed work_mem.' }) : ''}
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

            // Build duration distribution from queries
            const durationDist = buildDurationDistribution(queries);

            return `
                <div class="section" id="sql_performance">
                    <div class="section-header">SQL Performance</div>
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

                        <ql-tabs style="margin-top: 1rem;">
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
            document.getElementById('queryModal').open();
        }

        function copyQuery(index) {
            const q = analysisData.sql_performance.queries[index];
            navigator.clipboard.writeText(q.full_query || q.normalized_query);
            alert('Query copied to clipboard');
        }

        function closeModal() {
            document.getElementById('queryModal').close();
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
                document.getElementById('queryModal').open();
                return;
            }

            // Get executions and temp events
            const allExecs = analysisData.sql_performance?.executions || [];
            const execs = q ? allExecs.filter(e => e.query_id === q.id) : [];
            const allTempEvents = analysisData.temp_files?.events || [];
            const tempEvents = q ? allTempEvents.filter(e => e.query_id === q.id) : [];

            // Build detailed view with all available data
            document.getElementById('queryModalBody').innerHTML = buildQueryDetailHTML(q, execs, tempEvents, lockQ, tempQ);
            document.getElementById('queryModal').open();

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
                    html += '<div class="qd-chart-header"><span class="qd-chart-title">Execution Over Time</span><button class="btn-export-png" onclick="exportChartById(\'qd-chart-combined\', \'Query Execution Over Time\')" title="Export as PNG">⬇ PNG</button></div>';
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

                // Time the parsing
                const parseStart = performance.now();
                const resultJson = quellogParse(currentFileContent, filtersJson);
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
        window.showSqlOvView = showSqlOvView;
        window.copyQuery = copyQuery;
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

