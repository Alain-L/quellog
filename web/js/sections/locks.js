// Locks section: deadlock/waiting/acquired stats, lock/resource/relation
// type breakdowns, and the waiting-queries + blocking-queries tables.

import { fmt, fmtDur, fmtDuration, parseDurToMs, truncQuery, esc, buildNoDataMessage } from '../utils.js';

export function buildLocksSection(data) {
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
