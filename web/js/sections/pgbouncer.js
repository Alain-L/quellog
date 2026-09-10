// pgBouncer section: pooler telemetry rendered alongside the PostgreSQL
// sections. Reads data.pgbouncer (the PgBouncerMetrics JSON) and shows
// summary cards, three time-series charts (throughput, network I/O,
// query/wait timing), a session-time histogram, per-database/user/host
// breakdowns, and the ranked error list.

import { fmt, esc, buildNoDataMessage } from '../utils.js';
import { chartData, buildChartContainer } from '../charts.js';

const SESSION_BUCKETS = ['< 1s', '1s - 1min', '1min - 30min', '30min - 2h', '2h - 5h', '> 5h'];

// breakdownTable renders a top-10 name/count/% table.
function breakdownTable(title, m, total) {
    if (!m) return '';
    const rows = Object.entries(m).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, 10);
    if (rows.length === 0) return '';
    return `
        <div>
            <div class="subsection-title">${esc(title)}</div>
            <div class="table-container" style="max-height:200px;">
                <table>
                    <tbody>
                        ${rows.map(([name, count]) => `
                            <tr>
                                <td>${esc(name)}</td>
                                <td class="num">${fmt(count)}</td>
                                <td class="num">${total ? (count / total * 100).toFixed(1) : '0.0'}%</td>
                            </tr>
                        `).join('')}
                    </tbody>
                </table>
            </div>
        </div>
    `;
}

// sessionHistogram renders the fixed-bucket session-time distribution as
// inline proportional bars (no uPlot — categorical, like the PostgreSQL
// session distribution).
function sessionHistogram(dist, total) {
    if (!dist) return '';
    const max = Math.max(1, ...SESSION_BUCKETS.map(b => dist[b] || 0));
    const rows = SESSION_BUCKETS.filter(b => (dist[b] || 0) > 0).map(b => {
        const c = dist[b] || 0;
        const w = Math.round(c / max * 100);
        return `
            <div style="display:flex;align-items:center;gap:8px;margin:2px 0;font-size:12px;">
                <span style="width:90px;color:var(--text-muted);">${esc(b)}</span>
                <span style="flex:1;background:var(--bg-alt);border-radius:2px;overflow:hidden;">
                    <span style="display:block;height:10px;width:${w}%;background:var(--chart-bar);"></span>
                </span>
                <span style="width:44px;text-align:right;">${fmt(c)}</span>
                <span style="width:48px;text-align:right;color:var(--text-muted);">${total ? (c / total * 100).toFixed(1) : '0.0'}%</span>
            </div>
        `;
    }).join('');
    return `<div class="subsection"><div class="subsection-title">Session time</div>${rows}</div>`;
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
                        <div>${buildChartContainer('chart-pgb-throughput', 'Request Throughput', { tooltip: 'Transactions and queries per second, sampled from pgBouncer stats lines.', showBucketControl: false })}</div>
                        <div>${buildChartContainer('chart-pgb-io', 'Network I/O', { tooltip: 'Bytes received and sent per second.', showBucketControl: false })}</div>
                    </div>
                    <div style="margin-top:0.5rem;">${buildChartContainer('chart-pgb-timing', 'Transaction / Query / Wait Time', { tooltip: 'Average transaction, query, and pool-wait time (microsecond resolution).', showBucketControl: false })}</div>
                ` : ''}
                ${hasSessions ? `
                    ${sessionHistogram(p.session_distribution, p.sessions)}
                    <div class="grid grid-2" style="margin-top:0.5rem;">
                        ${breakdownTable('Sessions per database', p.sessions_per_database, p.sessions)}
                        ${breakdownTable('Sessions per user', p.sessions_per_user, p.sessions)}
                    </div>
                    <div style="margin-top:0.5rem;">${breakdownTable('Sessions per host', p.sessions_per_host, p.sessions)}</div>
                ` : ''}
                ${hasConns ? `
                    <div class="grid grid-2" style="margin-top:0.5rem;">
                        ${breakdownTable('Connections per database', p.connections_per_database, p.login_attempts)}
                        ${breakdownTable('Connections per user', p.connections_per_user, p.login_attempts)}
                    </div>
                ` : ''}
                ${hasErrors ? `<div style="margin-top:0.5rem;">${breakdownTable('Most frequent errors', p.errors, p.total_errors)}</div>` : ''}
            </div>
        </div>
    `;
}
