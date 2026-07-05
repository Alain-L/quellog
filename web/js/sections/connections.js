// Connections section: connection/session stats, client I/O failures panel,
// session distribution + tables, and the sibling Clients section
// (databases/users/apps/hosts breakdown).

import { fmt, fmtDur, esc, parseDurToMs, buildNoDataMessage } from '../utils.js';
import { chartData, buildChartContainer } from '../charts.js';

export function buildConnectionsSection(data) {
    const c = data.connections;
    if (!c || (c.connection_count === 0 && !c.client_io_failures)) {
        return `
            <div class="section" id="connections">
                <div class="section-header muted">Connections</div>
                <div class="section-body">
                    ${buildNoDataMessage('<code>log_connections = on</code>')}
                </div>
            </div>
        `;
    }
    // No connection logging, but client I/O failures were still seen:
    // render a minimal section carrying just the failures panel.
    if (c.connection_count === 0 && c.client_io_failures) {
        return `
            <div class="section" id="connections">
                <div class="section-header">Connections</div>
                <div class="section-body">
                    ${renderClientIOFailures(c.client_io_failures)}
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
                ${c.client_io_failures ? renderClientIOFailures(c.client_io_failures) : ''}
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

// ---- Client I/O failures panel (collapsible amber banner + detail) ----
function cioSum(m) { let s = 0; for (const k in m) s += m[k]; return s; }
function cioDirTotal(byReason) { let s = 0; for (const r in byReason) s += cioSum(byReason[r]); return s; }

// renderIODirBlock renders one direction's reasons as a single
// two-column grid (reason | count) — no direction header (that lives in
// the banner; left column is receiving, right is sending, matching the
// banner order). Per-database sub-rows appear only when a reason spans
// more than one database.
function renderIODirBlock(byReason) {
    const reasons = Object.keys(byReason).map(r => ({ name: r, dbs: byReason[r], total: cioSum(byReason[r]) }));
    if (!reasons.length) return '<div></div>';
    reasons.sort((a, b) => b.total - a.total || a.name.localeCompare(b.name));
    let cells = '';
    for (const rz of reasons) {
        cells += `<div class="cio-rsn">${esc(rz.name)}</div><div class="cio-v">${fmt(rz.total)}</div>`;
        const dbs = Object.keys(rz.dbs);
        if (dbs.length > 1) {
            dbs.map(d => ({ d, c: rz.dbs[d] }))
               .sort((a, b) => b.c - a.c || a.d.localeCompare(b.d))
               .forEach(x => { cells += `<div class="cio-db">${esc(x.d)}</div><div class="cio-v cio-sub">${fmt(x.c)}</div>`; });
        }
    }
    return `<div class="cio-block">${cells}</div>`;
}

function renderClientIOFailures(cio) {
    const dirs = [
        ['receiving from client', cio.receiving_from_client || {}],
        ['sending to client', cio.sending_to_client || {}],
    ].filter(([, m]) => cioDirTotal(m) > 0);
    // Collapsed: the split is inline on one line. Expanded: that inline
    // is hidden and the same directions reappear as an aligned header
    // grid row atop the reason blocks, so each column sits under its
    // header. Never both at once — one-line compact, aligned detail,
    // direction label shown once.
    const inline = dirs.map(([label, m]) => `${label} <b>${fmt(cioDirTotal(m))}</b>`)
        .join(' <span class="cio-dot">·</span> ');
    const heads = dirs.map(([label, m]) =>
        `<div class="cio-dir">${esc(label)}<span class="cio-tot">${fmt(cioDirTotal(m))}</span></div>`).join('');
    const blocks = dirs.map(([, m]) => renderIODirBlock(m)).join('');
    return `
        <div class="cio">
            <div class="cio-banner" onclick="toggleClientIO(this)">
                <span class="cio-lead">⚠ ${fmt(cio.total)} client I/O failures</span>
                <span class="cio-inline">${inline}</span>
                <span class="cio-arrow">▾</span>
            </div>
            <div class="cio-detail" hidden>
                <div class="cio-grid cio-heads">${heads}</div>
                <div class="cio-grid">${blocks}</div>
            </div>
        </div>`;
}

export function toggleClientIO(banner) {
    const w = banner.parentElement;
    w.classList.toggle('open');
    const d = w.querySelector('.cio-detail');
    if (d) d.hidden = !d.hidden;
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

export function buildClientsSection(data) {
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
