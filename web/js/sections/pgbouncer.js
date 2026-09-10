// pgBouncer section: pooler telemetry rendered alongside the PostgreSQL
// sections. Reads data.pgbouncer (the PgBouncerMetrics JSON) and shows
// summary cards, three time-series charts (throughput, network I/O,
// query/wait timing), a session-time histogram, per-database/user/host
// breakdowns, and the ranked error list.

import { fmt, esc, buildNoDataMessage } from '../utils.js';
import { chartData, buildChartContainer } from '../charts.js';

const SESSION_BUCKETS = ['< 1s', '1s - 1min', '1min - 30min', '30min - 2h', '2h - 5h', '> 5h'];

// chartLegend renders an external dotted legend below a chart, matching
// the Temp Files / Connections legend style (uPlot's own legend is hidden).
function chartLegend(items) {
    const dot = (c) => `<span style="display:inline-block;width:12px;height:12px;background:${c};border-radius:2px;vertical-align:middle;margin-right:4px;"></span>`;
    return `<div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:4px;font-size:12px;">`
        + items.map(([label, c]) => `<span>${dot(c)}${label}</span>`).join('')
        + `</div>`;
}

// barList renders name→count rows as the report's shared bar-list (the
// same .scroll-list / .list-item / .bar-fill markup the Clients section
// uses), so the pgBouncer breakdowns look identical to the rest.
// `rows` is an array of [name, count]; when `ordered` is false they are
// sorted by count descending and clipped to the top 10.
function barList(title, rows, total, ordered) {
    if (!ordered) {
        rows = rows.slice().sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 10);
    }
    rows = rows.filter(([, c]) => c > 0);
    if (rows.length === 0) return '';
    const max = Math.max(1, ...rows.map(([, c]) => c));
    return `
        <div>
            ${title ? `<div class="subsection-title">${esc(title)}</div>` : ''}
            <div class="scroll-list">
                ${rows.map(([name, count]) => {
                    const pct = total > 0 ? (count / total * 100).toFixed(1) : '0.0';
                    return `
                    <div class="list-item">
                        <span class="name">${esc(name)}</span>
                        <div class="bar-container"><div class="bar"><div class="bar-fill" style="width: ${count / max * 100}%"></div></div></div>
                        <span class="value">${fmt(count)} <small style="color: var(--text-muted)">(${pct}%)</small></span>
                    </div>`;
                }).join('')}
            </div>
        </div>
    `;
}

// dimTabs groups per-database / per-user / per-host breakdowns into a
// single tabbed widget (ql-tabs), the same affordance the Clients section
// uses. `dims` is [{label, map}]; each becomes a tab (with a unique-count
// badge) and a panel holding the shared bar-list.
function dimTabs(dims, total) {
    const shown = dims.filter(d => d.map && Object.keys(d.map).length > 0);
    if (shown.length === 0) return '';
    const tabs = shown.map((d, i) =>
        `<ql-tab${i === 0 ? ' selected' : ''}>${d.label} <ql-badge>${Object.keys(d.map).length}</ql-badge></ql-tab>`
    ).join('');
    const panels = shown.map(d =>
        `<ql-panel>${barList('', Object.entries(d.map), total, false)}</ql-panel>`
    ).join('');
    return `<ql-tabs>${tabs}${panels}</ql-tabs>`;
}

// errorsTable renders the ranked errors as a data-table (the Events
// section's markup), so long error messages wrap and stay readable
// rather than being truncated like the short-label bar lists.
function errorsTable(m, total) {
    if (!m) return '';
    const rows = Object.entries(m).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 10);
    if (rows.length === 0) return '';
    return `
        <div class="subsection">
            <div class="subsection-title">Most frequent errors</div>
            <div class="table-container" style="max-height: 220px;">
                <table class="data-table" style="width: 100%;">
                    <tbody>
                        ${rows.map(([name, count]) => `
                            <tr>
                                <td style="padding: 0.25rem 0.5rem;">${esc(name)}</td>
                                <td class="num" style="width: 60px; padding: 0.25rem 0.5rem; font-weight: 600;">${fmt(count)}</td>
                                <td class="num" style="width: 60px; padding: 0.25rem 0.5rem; color: var(--text-muted);">${total ? (count / total * 100).toFixed(1) : '0.0'}%</td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            </div>
        </div>
    `;
}

// sessionHistogram renders the fixed-bucket session-time distribution
// using the same bar-list markup, in bucket order.
function sessionHistogram(dist, total) {
    if (!dist) return '';
    return barList('Session time', SESSION_BUCKETS.map(b => [b, dist[b] || 0]), total, true);
}

export function buildPgBouncerSection(data) {
    const p = data.pgbouncer;
    if (!p || !p.lines) {
        return `
            <div class="section" id="pgbouncer">
                <div class="section-header muted">pgBouncer</div>
                <div class="section-body">${buildNoDataMessage('a pgBouncer log file')}</div>
            </div>
        `;
    }

    // Register the three time-series charts (drawn after DOM insertion).
    const series = p.stats_series || [];
    if (series.length > 0) {
        const t = series.map(s => Math.round(new Date(s.time).getTime() / 1000));
        chartData.set('chart-pgb-throughput', {
            type: 'pgb-series', t,
            series: [
                { label: 'xacts/s', key: 'xacts_per_sec', color: 'var(--accent)' },
                { label: 'queries/s', key: 'queries_per_sec', color: 'var(--purple)' },
            ],
            values: series,
        });
        chartData.set('chart-pgb-io', {
            type: 'pgb-series', t,
            series: [
                { label: 'in', key: 'in_bytes_per_sec', color: 'var(--accent)', fmt: 'bytes' },
                { label: 'out', key: 'out_bytes_per_sec', color: 'var(--success)', fmt: 'bytes' },
            ],
            values: series, fmt: 'bytes',
        });
        chartData.set('chart-pgb-timing', {
            type: 'pgb-series', t,
            series: [
                { label: 'xact', key: 'xact_us', color: 'var(--accent)', fmt: 'micros' },
                { label: 'query', key: 'query_us', color: 'var(--purple)', fmt: 'micros' },
                { label: 'wait', key: 'wait_us', color: 'var(--danger)', fmt: 'micros' },
            ],
            values: series, fmt: 'micros',
        });
    }

    const hasStats = p.stats_sample_count > 0;
    const hasSessions = p.sessions > 0;
    const hasConns = p.login_attempts > 0;
    const hasErrors = p.total_errors > 0;

    return `
        <div class="section" id="pgbouncer">
            <div class="section-header">pgBouncer</div>
            <div class="section-body">
                <div class="stat-grid">
                    <div class="stat-card"><div class="stat-value">${p.avg_queries_per_sec != null ? p.avg_queries_per_sec.toFixed(1) : '0'}/s</div><div class="stat-label">Avg Queries</div></div>
                    <div class="stat-card"><div class="stat-value">${fmt(p.max_queries_per_sec || 0)}/s</div><div class="stat-label">Peak Queries</div></div>
                    <div class="stat-card"><div class="stat-value">${fmt(p.login_attempts || 0)}</div><div class="stat-label">Login Attempts</div></div>
                    <div class="stat-card"><div class="stat-value">${fmt(p.sessions || 0)}</div><div class="stat-label">Sessions</div></div>
                    <div class="stat-card"><div class="stat-value">${fmt(p.server_connections || 0)}</div><div class="stat-label">Server Conns</div></div>
                    ${p.reserve_pool_activations ? `<div class="stat-card stat-card--warning"><div class="stat-value">${fmt(p.reserve_pool_activations)}</div><div class="stat-label">Reserve Pool</div></div>` : ''}
                    <div class="stat-card${hasErrors ? ' stat-card--alert' : ''}"><div class="stat-value">${fmt(p.total_errors || 0)}</div><div class="stat-label">Errors</div></div>
                </div>
                ${hasStats ? `
                    <div class="grid grid-2" style="margin-top:0.5rem;">
                        <div>
                            ${buildChartContainer('chart-pgb-throughput', 'Request Throughput', { tooltip: 'Transactions and queries per second, sampled from pgBouncer stats lines.', showBucketControl: false })}
                            ${chartLegend([['xacts/s', 'var(--accent)'], ['queries/s', 'var(--purple)']])}
                        </div>
                        <div>
                            ${buildChartContainer('chart-pgb-io', 'Network I/O', { tooltip: 'Bytes received and sent per second.', showBucketControl: false })}
                            ${chartLegend([['in', 'var(--accent)'], ['out', 'var(--success)']])}
                        </div>
                    </div>
                    <div style="margin-top:0.5rem;">
                        ${buildChartContainer('chart-pgb-timing', 'Transaction / Query / Wait Time', { tooltip: 'Average transaction, query, and pool-wait time (microsecond resolution).', showBucketControl: false })}
                        ${chartLegend([['transaction', 'var(--accent)'], ['query', 'var(--purple)'], ['wait', 'var(--danger)']])}
                    </div>
                ` : ''}
                ${hasSessions ? `
                    ${sessionHistogram(p.session_distribution, p.sessions)}
                    <div class="subsection">
                        <div class="subsection-title">Sessions by</div>
                        ${dimTabs([
                            { label: 'Database', map: p.sessions_per_database },
                            { label: 'User', map: p.sessions_per_user },
                            { label: 'Host', map: p.sessions_per_host },
                        ], p.sessions)}
                    </div>
                ` : ''}
                ${hasConns ? `
                    <div class="subsection">
                        <div class="subsection-title">Connections by</div>
                        ${dimTabs([
                            { label: 'Database', map: p.connections_per_database },
                            { label: 'User', map: p.connections_per_user },
                            { label: 'Host', map: p.connections_per_host },
                        ], p.login_attempts)}
                    </div>
                ` : ''}
                ${hasErrors ? `<div style="margin-top:0.5rem;">${errorsTable(p.errors, p.total_errors)}</div>` : ''}
            </div>
        </div>
    `;
}
