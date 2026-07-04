// Maintenance section: autovacuum + autoanalyze panels with sortable
// top-tables/buffer-usage/skipped tabs. Owns the module-private caches
// (_vacTabsData/_anaTabsData) and per-table sort state so tab/sort
// switches can re-render without re-walking the full analysis payload.

import {
    fmt, fmtDuration, fmtDurationCoarse, fmtBytes, fmtCompact, esc, buildNoDataMessage
} from '../utils.js';
import { parseSizeToBytes } from '../format.js';

export function buildMaintenanceSection(data) {
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
    _vacTabsData = { topElapsed: topVacTables, xmin: xminTables, byCount: vacTables, spaceRecovered, vacuumCount: m.vacuum_count || 0, skipped: m.skipped_vacuum_tables || [] };

    const topAnaTables = m.top_analyze_tables_by_elapsed || [];
    const anaTables = m.analyze_table_counts
        ? Object.entries(m.analyze_table_counts)
            .map(([t, c]) => ({ table: t, count: c }))
            .sort((a, b) => b.count - a.count)
        : [];
    _anaTabsData = { topElapsed: topAnaTables, byCount: anaTables, analyzeCount: m.analyze_count || 0, skipped: m.skipped_analyze_tables || [] };
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
    const hasSkipped = (_vacTabsData?.skipped || []).length > 0;
    const hasMain = (_vacTabsData?.byCount || []).length > 0;
    // Default to the skipped view only when there is nothing else to
    // show (autovacuum fully lock-blocked: zero completed runs).
    const skippedOnly = hasSkipped && !hasMain;
    const showTabs = hasBufferData || hasSkipped;
    return `
        <div class="subsection">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.5rem;">
                <div class="subsection-title" style="margin: 0;">Autovacuum</div>
                ${showTabs ? `
                    <div class="tabs" style="margin: 0;">
                        <button class="tab${skippedOnly ? '' : ' active'}" onclick="showVacuumView(this, 'main')">Top tables</button>
                        ${hasBufferData ? `<button class="tab" onclick="showVacuumView(this, 'buffer')">Buffer usage</button>` : ''}
                        ${hasSkipped ? `<button class="tab skipped${skippedOnly ? ' active' : ''}" onclick="showVacuumView(this, 'skipped')">Skipped <span class="tab-badge">${fmt(m.skipped_vacuum_count || 0)}</span></button>` : ''}
                    </div>
                ` : ''}
            </div>
            ${buildMaintenanceMetricLines(m)}
            <div id="vacuum-table-container">
                ${skippedOnly ? renderVacuumSkippedTable() : renderVacuumMainTable()}
            </div>
        </div>
    `;
}

export function showVacuumView(btn, view) {
    btn.parentElement.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    btn.classList.add('active');
    const container = document.getElementById('vacuum-table-container');
    if (!container) return;
    container.innerHTML = view === 'buffer' ? renderVacuumBufferTable()
        : view === 'skipped' ? renderVacuumSkippedTable()
        : renderVacuumMainTable();
}

function buildAutoanalyzePanel(m) {
    const topAnaTables = _anaTabsData?.topElapsed || [];
    const anaTables = _anaTabsData?.byCount || [];
    const hasMain = topAnaTables.length > 0 || anaTables.length > 0;
    const hasSkipped = (_anaTabsData?.skipped || []).length > 0;
    if (!hasMain && !hasSkipped) return '';
    // Autoanalyze had no tab bar before; introduce one whenever there
    // are skips (mirrors autovacuum so the warning badge always shows).
    const showTabs = hasSkipped;
    const skippedOnly = hasSkipped && !hasMain;
    return `
        <div class="subsection">
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.5rem;">
                <div class="subsection-title" style="margin: 0;">Autoanalyze</div>
                ${showTabs ? `
                    <div class="tabs" style="margin: 0;">
                        <button class="tab${skippedOnly ? '' : ' active'}" onclick="showAnalyzeView(this, 'main')">Top tables</button>
                        <button class="tab skipped${skippedOnly ? ' active' : ''}" onclick="showAnalyzeView(this, 'skipped')">Skipped <span class="tab-badge">${fmt(m.skipped_analyze_count || 0)}</span></button>
                    </div>
                ` : ''}
            </div>
            <div id="analyze-table-container">
                ${skippedOnly ? renderAnalyzeSkippedTable() : renderAnalyzeTable()}
            </div>
        </div>
    `;
}

export function showAnalyzeView(btn, view) {
    btn.parentElement.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    btn.classList.add('active');
    const container = document.getElementById('analyze-table-container');
    if (!container) return;
    container.innerHTML = view === 'skipped' ? renderAnalyzeSkippedTable() : renderAnalyzeTable();
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

export function showVacuumMainSort(key) {
    if (_vacMainSortKey === key) _vacMainSortDir = _vacMainSortDir === 'desc' ? 'asc' : 'desc';
    else { _vacMainSortKey = key; _vacMainSortDir = 'desc'; }
    const c = document.getElementById('vacuum-table-container');
    if (c) c.innerHTML = renderVacuumMainTable();
}

export function showVacuumBufferSort(key) {
    if (_vacBufSortKey === key) _vacBufSortDir = _vacBufSortDir === 'desc' ? 'asc' : 'desc';
    else { _vacBufSortKey = key; _vacBufSortDir = 'desc'; }
    const c = document.getElementById('vacuum-table-container');
    if (c) c.innerHTML = renderVacuumBufferTable();
}

// Skipped tables: a simple count-ranked list (relation / red bar /
// count), shared by autovacuum and autoanalyze. The reason is shown
// only when it deviates from the universal "lock not available"
// default — matching the CLI, which suppresses that noise.
const SKIP_REASON_DEFAULT = 'lock not available';

function renderSkippedTable(skips) {
    if (!skips || !skips.length) return '<div class="empty">No skipped operations.</div>';
    const rows = skips.slice().sort((a, b) => (b.count - a.count) || a.table.localeCompare(b.table));
    const limited = rows.slice(0, MAINT_TOP_N);
    const maxBar = Math.max(...limited.map(r => r.count || 0)) || 1;
    return `<div class="scroll-list scroll-list--maintenance">
        <div class="list-header">
            <span class="name">Table</span>
            <div class="bar"></div>
            <span class="extra"></span>
            <span class="value">Skipped</span>
        </div>
        ${limited.map(r => {
            const reason = (r.reason && r.reason !== SKIP_REASON_DEFAULT) ? esc(r.reason) : '';
            return `<div class="list-item">
                ${maintName(r.table)}
                <div class="bar"><div class="bar-fill" style="width: ${(r.count || 0) / maxBar * 100}%; background: var(--danger);"></div></div>
                <span class="extra">${reason ? `<span style="color: var(--danger);">${reason}</span>` : ''}</span>
                <span class="value">${r.count}×</span>
            </div>`;
        }).join('')}
    </div>`;
}

function renderVacuumSkippedTable() { return renderSkippedTable(_vacTabsData?.skipped); }
function renderAnalyzeSkippedTable() { return renderSkippedTable(_anaTabsData?.skipped); }

// maintName renders a table-name cell with a native hover
// tooltip carrying the full identifier plus a click handler
// that inserts a one-line copy ribbon directly above the row
// — auto-selected, ready for Cmd+C. The cell itself keeps the
// truncated form so the row layout never reflows.
function maintName(table) {
    const safe = esc(table);
    return `<span class="name" title="${safe}" onclick="showMaintRibbon(this)"><span class="name-inner">${safe}</span></span>`;
}

export function showMaintRibbon(el) {
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

export function showAnalyzeSort(key) {
    if (_anaSortKey === key) {
        _anaSortDir = _anaSortDir === 'desc' ? 'asc' : 'desc';
    } else {
        _anaSortKey = key;
        _anaSortDir = 'desc';
    }
    const container = document.getElementById('analyze-table-container');
    if (container) container.innerHTML = renderAnalyzeTable();
}
