// Patroni section, in cards: the figures and the leader lock over time;
// the leader changes, with what PostgreSQL logged around them, beside the
// warnings and errors; the timeline.

import { fmt, fmtDur, esc, escAttr, wholeLogBadge } from '../utils.js';

const KIND_LABEL = {
    bootstrap: 'Bootstrap', replica_created: 'Replica created', switchover_request: 'Switchover request', switchover_seen: 'Switchover seen',
    demote: 'Demote', promote: 'Promote', failover_key: 'Switchover key cleared', rewind: 'pg_rewind',
    rewind_done: 'pg_rewind done', rewind_failed: 'pg_rewind failed', reinit: 'Reinit', crash_recovery: 'Crash recovery', lag_refused: 'Refused for lag', sync_standby: 'Sync standby',
    pause: 'Pause', resume: 'Resume', dcs_lost: 'DCS lost', dcs_restored: 'DCS back',
};

export function buildPatroniSection(data) {
    const p = data.patroni;
    if (!p) return '';
    const oneDay = p.start_date.slice(0, 10) === p.end_date.slice(0, 10);
    const clock = ts => (oneDay ? ts.slice(11) : ts);
    const dcsIncidents = (p.dcs_incidents || []).length;
    const lagRefused = (p.event_counts && p.event_counts.lag_refused) || 0;
    const withPg = !!data.summary; // PostgreSQL logs were part of the run
    const missing = p.members_without_log || [];
    const whole = data._wholeLog?.patroni ? wholeLogBadge() : '';
    const LEVEL = { alert: 'stat-card--alert', warning: 'stat-card--warning' };
    const card = (value, label, level = '') =>
        `<div class="stat-card ${LEVEL[level] || ''}"><div class="stat-value">${value}</div><div class="stat-label">${label}</div></div>`;
    return `
        <div class="section" id="patroni">
            <div class="section-header">Patroni${whole}</div>
            <div class="section-body">
                <div class="stat-grid">
                    ${card(p.leader ? esc(p.leader) : 'none', p.leader ? `Leader since ${clock(p.leader_since)}${missing.includes(p.leader) ? ', as seen by the others' : ''}` : 'Leader at end', p.leader ? '' : 'alert')}
                    ${card(p.switchovers, 'Switchovers')}
                    ${card(p.failovers, 'Failovers', p.failovers ? 'alert' : '')}
                    ${card(fmtDur(p.leaderless_time), 'Without leader', p.failovers ? 'warning' : '')}
                    ${card(dcsIncidents, 'DCS incidents', dcsIncidents ? 'warning' : '')}
                    ${card(lagRefused, 'Refused for lag', lagRefused ? 'warning' : '')}
                    ${card(fmt(p.error_count), 'Errors', p.error_count ? 'alert' : '')}
                </div>
                ${missing.length ? `<div class="pt-note">No log from ${esc(missing.join(', '))}: seen only by the members whose log was given.</div>` : ''}
                ${leadershipStrip(p, clock)}
            </div>
        </div>
        <div class="grid grid-2">
            <div class="section" id="patroni-changes">
                <div class="section-header">Patroni Leader Changes${whole}</div>
                <div class="section-body">${changesTable(p, clock, withPg, missing)}</div>
            </div>
            <div class="section" id="patroni-events">
                <div class="section-header">Patroni Events${whole}</div>
                <div class="section-body events-section">${eventsCard(p, clock)}</div>
            </div>
        </div>
        ${p.events ? `
        <div class="section" id="patroni-timeline">
            <div class="section-header">Patroni Timeline${whole}</div>
            <div class="section-body">
                <div class="table-container" style="max-height: 360px;">
                    <table>
                        <thead><tr><th>Time</th><th>Member</th><th>Event</th><th>Detail</th></tr></thead>
                        <tbody>${p.events.map(e => `
                            <tr><td>${clock(e.timestamp)}</td><td>${esc(e.member)}</td><td>${KIND_LABEL[e.kind] || esc(e.kind)}</td><td class="pt-msg">${esc(e.detail || '')}</td></tr>`).join('')}
                        </tbody>
                    </table>
                </div>
            </div>
        </div>` : ''}`;
}

// changesTable lists the leader changes, each with what the PostgreSQL logs
// given logged around it.
function changesTable(p, clock, withPg, missing) {
    if (!p.changes) return '<div class="pt-none">No leader change in the log</div>';
    return `${withPg ? '<div class="pt-sub">With what the PostgreSQL logs given logged around each change, on any node.</div>' : ''}
        <div class="table-container" style="max-height: none;">
            <table>
                <thead><tr><th>Time</th><th>Kind</th><th>Change</th><th class="num">Without leader</th></tr></thead>
                <tbody>${p.changes.map(c => `
                    <tr>
                        <td>${clock(c.timestamp)}</td>
                        <td><span class="pt-kind" data-kind="${c.kind}">${c.kind}</span></td>
                        <td class="pt-nowrap">${esc(c.from)} → ${esc(c.to)}</td>
                        <td class="num">${fmtDur(c.leaderless)}${missing.includes(c.from) || missing.includes(c.to) ? ', seen by the others' : ''}</td>
                    </tr>${withPg ? `
                    <tr class="pt-pg"><td colspan="4">${pgList(c.postgres, clock)}</td></tr>` : ''}`).join('')}
                </tbody>
            </table>
        </div>`;
}

// pgList lists a change's PostgreSQL events; nodes are not told apart, so
// the same event logged by several of them reads "×n".
function pgList(events, clock) {
    const count = new Map();
    for (const e of events || []) {
        const key = `${clock(e.timestamp)}\u0000${e.detail}`;
        count.set(key, (count.get(key) || 0) + 1);
    }
    if (!count.size) return '<div class="pt-pgl"><span></span><span>nothing logged</span></div>';
    return `<div class="pt-pgl">${[...count].map(([key, n]) => {
        const [t, detail] = key.split('\u0000');
        return `<span class="t">${t}</span><span>${esc(detail)}${n > 1 ? ` ×${n}` : ''}</span>`;
    }).join('')}</div>`;
}

// eventsCard lays the warnings and errors out as the PostgreSQL Events
// card: a tab per level, the most frequent first.
function eventsCard(p, clock) {
    const of = level => (p.messages || []).filter(m => (level === 'WARNING' ? m.level === 'WARNING' : m.level !== 'WARNING'));
    const total = level => of(level).reduce((n, m) => n + m.count, 0);
    const rows = level => {
        const items = of(level);
        if (!items.length) return `<div class="pt-none">No ${level} events recorded</div>`;
        return `<div class="table-container" style="max-height: 420px;"><table class="data-table" style="width: 100%;">${items.map(m => `<tr>
            <td class="num" style="width: 60px; vertical-align: top; padding: 0.25rem 0.5rem; font-weight: 600;">${fmt(m.count)}</td>
            <td class="pt-msg" style="vertical-align: top; padding: 0.25rem 0.5rem;">${esc(m.message)}</td>
            <td class="pt-when">${m.first_date ? clock(m.first_date) + (m.last_date && m.last_date !== m.first_date ? ' → ' + clock(m.last_date) : '') : ''}</td>
        </tr>`).join('')}</table></div>`;
    };
    const errors = total('ERROR');
    return `<ql-tabs>
        <ql-tab class="error"${errors ? ' selected' : ''}>ERROR<ql-badge>${fmt(errors)}</ql-badge></ql-tab>
        <ql-tab class="warning"${errors ? '' : ' selected'}>WARNING<ql-badge>${fmt(total('WARNING'))}</ql-badge></ql-tab>
        <ql-panel>${rows('ERROR')}</ql-panel>
        <ql-panel>${rows('WARNING')}</ql-panel>
    </ql-tabs>`;
}

// leadershipStrip draws one track per member over the log span: a filled
// bar while the member held the leader lock, a tick at each change, the
// member's figures at the end of its track, and a DCS track for the spans
// some member could not reach it.
function leadershipStrip(p, clock) {
    const t0 = Date.parse(p.start_date.replace(' ', 'T'));
    const span = Date.parse(p.end_date.replace(' ', 'T')) - t0 || 1;
    const pct = ts => (100 * (Date.parse(ts.replace(' ', 'T')) - t0) / span).toFixed(2);
    const members = new Map(p.members.map(m => [m.name, m]));
    const names = [...new Set([...p.members.map(m => m.name), ...(p.leadership || []).map(t => t.member)])];
    const ticks = (p.changes || []).map(c =>
        `<span class="pt-tick" data-kind="${c.kind}" style="left:${pct(c.timestamp)}%" title="${escAttr(`${c.kind} ${c.from} → ${c.to} at ${clock(c.timestamp)}`)}"></span>`).join('');
    const meta = name => {
        const m = members.get(name);
        return m ? `${esc(m.role)} · <b>${fmtDur(m.leader_time)}</b> as leader · ${fmt(m.total_logs)} entries` : 'no log';
    };
    const metaText = name => {
        const m = members.get(name);
        return m ? `${m.role} · ${fmtDur(m.leader_time)} as leader · ${fmt(m.total_logs)} entries` : 'no log';
    };
    // Five times on the axis; the narrow layout keeps the first, middle and last.
    const axis = [0, 0.25, 0.5, 0.75, 1].map(f => {
        const t = new Date(t0 + span * f);
        const hms = [t.getHours(), t.getMinutes(), t.getSeconds()].map(n => String(n).padStart(2, '0')).join(':');
        return `<span style="left:${f * 100}%">${hms}</span>`;
    }).join('');
    const term = (t, cls, title) => `<span class="pt-term${cls}" style="left:${pct(t.start)}%;width:${Math.max(0.4, pct(t.end) - pct(t.start))}%" title="${escAttr(title)}"></span>`;
    return `
        <div class="subsection">
            <div class="subsection-title">Leader lock</div>
            <div class="pt-strip">
                ${names.map(n => `
                <div class="pt-row">
                    <span class="pt-name" title="${escAttr(`${n}: ${metaText(n)}`)}">${esc(n)}</span>
                    <span class="pt-track">${(p.leadership || []).filter(t => t.member === n).map(t =>
                        term(t, '', `${n} leader ${clock(t.start)} → ${clock(t.end)}, ${fmtDur(t.duration)}`)).join('')}${ticks}</span>
                    <span class="pt-meta">${meta(n)}</span>
                </div>`).join('')}
                ${p.dcs_incidents ? `
                <div class="pt-row">
                    <span class="pt-name">DCS</span>
                    <span class="pt-track">${p.dcs_incidents.map(i =>
                        term(i, ' pt-term--dcs', `DCS unreachable from ${i.members.join(', ')}, ${clock(i.start)} → ${clock(i.end)}, ${fmtDur(i.duration)}`)).join('')}</span>
                    <span class="pt-meta"></span>
                </div>` : ''}
                <div class="pt-row"><span class="pt-name"></span><span class="pt-axis">${axis}</span><span class="pt-meta"></span></div>
            </div>
            <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:8px;font-size:12px;">
                ${swatch('var(--primary-light)', 'Leader lock', 12)}
                ${p.dcs_incidents ? swatch('var(--warning)', 'DCS unreachable', 12) : ''}
                ${p.failovers ? swatch('var(--danger)', 'Failover', 3) : ''}
                ${p.switchovers || p.lock_retaken ? swatch('var(--text-muted)', 'Switchover or retaken', 3) : ''}
            </div>
        </div>`;
}

// swatch is one legend item, in the markup of the chart legends elsewhere.
function swatch(color, label, width) {
    return `<span><span style="display:inline-block;width:${width}px;height:12px;background:${color};border-radius:2px;vertical-align:middle;margin-right:4px;"></span>${label}</span>`;
}
