// ES Module imports
import { fmtBytes } from './js/utils.js';
import {
    wasmReady, currentFileContent, currentFileName, currentFileSize,
    charts, currentFilters,
    setAnalysisData, setCurrentFileContent, setCurrentFileName, setCurrentFileSize,
    setOriginalDimensions, setAppliedFilters, clearAllCharts
} from './js/state.js';
import { initTheme, toggleTheme } from './js/theme.js';
import { unzstd, prepareContent } from './js/compression.js';
import './js/period-nav.js'; // shared period navigator (split reports + WASM)
import {
    initFilterBar, closeAllDropdowns,
    updateAllDropdownTriggers, updateApplyButton, wireTimeFilter,
    buildFiltersObject, resetTimeInputs, clearFilterSelections,
    setupFilterEventListeners, exposeFilterGlobals
} from './js/filters.js';
import {
    MAX_FILE_SIZE, setProgress, initWasmInstance, loadWasm,
    setupDragDrop, showLoading, hideLoading, showDropZone
} from './js/file-handler.js';
import {
    chartData, clearChartData, createTimeChart, createDurationChart, createCombinedSQLChart,
    createConcurrentChart, createCheckpointChart, createWALDistanceChart, createCombinedTempFilesChart,
    closeChartModal, updateModalInterval, resetModalZoom, exportChartPNG,
    resetChartZoom, openChartModal, updateChartInterval, toggleCombinedSeries, exportChartById,
    createCostMapChart, resetCostMapZoom, openCostMapModal
} from './js/charts.js';
import {
    setOriginalReportData, getOriginalReportData, applyReportTimeFilter, resetReportTimeFilter
} from './js/report-filter.js';

// Web Components (self-registering)
import './js/components/ql-tabs.js';
import './js/components/ql-modal.js';
import './js/components/ql-tooltip.js';
import './js/components/ql-dropdown.js';

// Section builders — each module owns its render* functions and any
// section-private state (see the module doc-comments for details).
import { buildSummarySection } from './js/sections/summary.js';
import { buildEventsSection } from './js/sections/events.js';
import { buildConnectionsSection, buildClientsSection, toggleClientIO } from './js/sections/connections.js';
import { buildCheckpointsSection } from './js/sections/checkpoints.js';
import {
    buildMaintenanceSection, showVacuumMainSort, showVacuumBufferSort,
    showVacuumView, showAnalyzeView, showMaintRibbon, showAnalyzeSort
} from './js/sections/maintenance.js';
import { buildLocksSection } from './js/sections/locks.js';
import { buildTempFilesSection } from './js/sections/tempfiles.js';
import {
    buildSQLOverviewSection, buildSQLPerformanceSection, showSqlOvView,
    setSqlOverviewData, highlightQuery
} from './js/sections/sql.js';
import {
    showQueryModal, showEventDetail, navigateToQuery, navigateToEvent,
    modalBack, visualizePlan, visualizePlanFor
} from './js/sections/modals.js';

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
    clearChartData();
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
    charts.forEach(c => { c._ro?.disconnect(); c.destroy(); });
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
    html += buildSummarySection(data, currentFileInfo);
    html += buildEventsSection(data);
    html += buildClientsSection(data);
    html += '</div>';

    // Connections (full width)
    html += buildConnectionsSection(data);

    // SQL Overview (full width)
    setSqlOverviewData(data.sql_overview);
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
    requestAnimationFrame(buildAllCharts);

}

// Build (or rebuild) every inline chart from the chartData registry.
// Runs after renderResults populates the DOM, and again on theme
// toggle: chart colors are resolved at build time, so a palette change
// needs a rebuild (each builder destroys its previous instance first).
function buildAllCharts() {
    chartData.forEach((data, chartId) => {
        const accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
        const color = chartId.includes('tempfiles') ? accentColor : null;
        // Check data type: checkpoints (stacked), sessions (sweep-line), duration, combined, tempfiles, costmap, or timestamps
        if (data?.type === 'wal-distance') {
            createWALDistanceChart(chartId, data);
        } else if (data?.type === 'checkpoints') {
            createCheckpointChart(chartId, data);
        } else if (data?.type === 'sessions') {
            createConcurrentChart(chartId, data.data, { color: color || 'var(--accent)', logStart: data.logStart, logEnd: data.logEnd });
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
}

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
window.showAnalyzeView = showAnalyzeView;
window.toggleClientIO = toggleClientIO;
window.showMaintRibbon = showMaintRibbon;
window.showAnalyzeSort = showAnalyzeSort;
window.showEventDetail = showEventDetail;
window.navigateToQuery = navigateToQuery;
window.navigateToEvent = navigateToEvent;
window.modalBack = modalBack;
window.toggleTheme = () => {
    toggleTheme();
    // Chart colors are resolved at build time; rebuild the inline
    // charts so they pick up the new theme palette. Modal charts are
    // rebuilt on open, so they need no special handling here.
    if (chartData.size > 0) requestAnimationFrame(buildAllCharts);
};
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

