// SQL Overview + SQL Performance sections: query-type breakdowns (global
// and per-dimension), duration distribution, cost-map + activity charts,
// the query tables (by total/max/count/TCL), and the cost-map <-> table
// row cross-highlight. Owns the module-private sqlOverviewData cache
// (written by the app.js orchestrator via setSqlOverviewData) and the
// query-types-table id counter.

import {
    fmt, esc, fmtDur, fmtMs, fmtMsLong, parseDurToMs, safeMax, truncQuery, buildNoDataMessage
} from '../utils.js';
import { chartData, buildChartContainer } from '../charts.js';

export function buildSQLOverviewSection(data) {
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

export function setSqlOverviewData(data) { sqlOverviewData = data; }

export function showSqlOvView(btn, view) {
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

export function buildSQLPerformanceSection(data) {
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
export function highlightQuery(id, on, scroll) {
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
