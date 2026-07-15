// Cross-modal navigation (Event <-> Query) plus the Event Detail and Query
// Detail modal bodies, the explain.dalibo.com EXPLAIN-visualize flow, and
// the modal-local bar charts (duration/temp-files histograms). Owns the
// module-private modalStack/_suppressStackClear pair that lets either
// modal's "<- Back" button unwind a chain of cross-modal navigations.

import {
    fmt, esc, escForJsAttr, truncQuery, safeMax, safeMin, fmtMs, fmtBytes, fmtMsLong,
    qdDurationBuckets
} from '../utils.js';
import { parseSizeToBytes } from '../format.js';
import { analysisData, modalCharts, modalChartsData, incrementModalChartCounter } from '../state.js';
import { createTimeChart, createCombinedSQLChart } from '../charts.js';

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

export function navigateToQuery(queryId, fromKind, fromId) {
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

export function navigateToEvent(eventIndex, fromKind, fromId) {
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

export function modalBack() {
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

// Reconcile one triggering-query row against the event's own occurrence total.
// The per-query counts are whole-log (no per-query timestamps to re-scope), so
// under a stale/edge payload they can exceed a time-scoped event total and yield
// an impossible percentage. Belt-and-suspenders: cap the displayed count at the
// event total and clamp the percentage to 100 % so the modal can never render a
// count > the event's occurrences or a share > 100 %. report-filter.js already
// drops triggering_queries under a narrowing filter; this guards every other
// path (direct/unfiltered render, legacy payloads).
export function triggerRowStat(triggerCount, eventTotal) {
    const raw = Number(triggerCount) || 0;
    const total = Number(eventTotal) || 0;
    const count = total > 0 ? Math.min(raw, total) : raw;
    const pct = total > 0 ? Math.min(100, count / total * 100).toFixed(1) : '0.0';
    return { count, pct };
}

// Triggering-queries table for the event modal. Click a row →
// navigateToQuery which pushes the current event onto the modal
// stack so the user can hit "← Back" to return. Same column
// shape as buildQueryTable (no rank column; rows are pre-sorted
// desc by count). data-flash-id labels each row by its queryID
// so a return navigation can scroll + flash this row.
export function buildEventTriggeringTable(triggers, eventTotal, eventId) {
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
                        const { count, pct } = triggerRowStat(t.count, eventTotal);
                        const qid = esc(t.id);
                        const eid = esc(eventId || '');
                        return `
                        <tr onclick="navigateToQuery('${qid}', 'event', '${eid}')" data-flash-id="${qid}" style="cursor:pointer;" title="Click for query details">
                            <td class="query-cell">${esc(truncQuery(t.normalized_query))}</td>
                            <td class="num">${fmt(count)}</td>
                            <td class="num">${pct}%</td>
                            <td class="num">
                                <div class="duration-bar">
                                    <div class="bar"><div class="bar-fill" style="width: ${Math.min(100, count/maxCount*100)}%"></div></div>
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

// Event detail modal — full message + occurrences-over-time sparkline
export function showEventDetail(index, opts = {}) {
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
        ` : (analysisData?._timeFiltered ? `
            <div class="qd-section-title">Triggering Queries <span class="qd-meta">not available under time filter</span></div>
            <div class="empty">Per-query counts are whole-log and can't be re-scoped to the selected window.</div>
        ` : '')}
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


export function showQueryModal(queryId, opts = {}) {
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
        if (lockQ.acquired_wait_time) {
            html += '<div class="qd-stat"><div class="qd-stat-label">Acquired Wait</div><div class="qd-stat-value">' + lockQ.acquired_wait_time + '</div></div>';
        }
        if (lockQ.still_waiting_time) {
            html += '<div class="qd-stat"><div class="qd-stat-label">Still Waiting Time</div><div class="qd-stat-value">' + lockQ.still_waiting_time + '</div></div>';
        }
        html += '</div>';
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

export function visualizePlan() {
    const dataEl = document.getElementById('plan-data');
    if (!dataEl) return;
    const data = JSON.parse(dataEl.textContent);
    explainDaliboFlow(data.plan, data.sql, data.id);
}

// Triggered from the per-row eye button in the query table — looks
// up the query by id in analysisData and reuses the same flow.
export function visualizePlanFor(queryId) {
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
    const dist = qdDurationBuckets(execs);
    if (dist.every(b => b.count === 0)) return '';
    const counts = dist.map(b => b.count);
    const maxVal = Math.max(...counts);
    let html = '<div style="font-size: 0.7rem; color: var(--text-muted); margin: 0.75rem 0 0.25rem;">Duration distribution</div>';
    html += '<div style="display: flex; flex-direction: column; gap: 4px;">';
    for (let i = 0; i < dist.length; i++) {
        const pct = maxVal > 0 ? (counts[i] / maxVal * 100) : 0;
        html += '<div style="display: flex; align-items: center; gap: 8px; font-size: 0.75rem;">';
        html += '<span style="width: 60px; text-align: right; color: var(--text-muted);">' + dist[i].label + '</span>';
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

// Modal lifecycle listeners. Guarded so the module can be imported in a
// DOM-free environment (node --test) without a TypeError at load; in the
// browser the modal elements always exist and these are always registered.
if (typeof document !== 'undefined' && document.getElementById('queryModal')) {
    // Cleanup modal charts when the query modal closes.
    document.getElementById('queryModal').addEventListener('modal-close', () => {
        modalCharts.forEach(c => c.destroy());
        modalCharts.length = 0;
    });

    // Modal navigation stack lifecycle — clear the trail whenever the user
    // closes a modal "for real" (Escape, backdrop click, the × button).
    // Programmatic closes triggered by our own navigateToX/modalBack set
    // _suppressStackClear first so the stack survives the close event.
    document.getElementById('queryModal').addEventListener('modal-close', () => {
        if (!_suppressStackClear) modalStack = [];
    });
    document.getElementById('eventModal').addEventListener('modal-close', () => {
        if (!_suppressStackClear) modalStack = [];
    });
}
