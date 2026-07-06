// Checkpoints section: checkpoint stats, WAL-distance chart, and the
// one-line server-lifecycle summary shown under the Summary card header.

import { esc, buildNoDataMessage } from '../utils.js';
import { chartData, buildChartContainer } from '../charts.js';

export function buildCheckpointsSection(data) {
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

    // I/O rates from server-computed values

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
                // Every trigger type other than time/wal, so the chart's Other
                // series matches the Other stat card (total - timed - wal)
                // rather than only two hardcoded trigger strings.
                other: Object.entries(types)
                    .filter(([k]) => k !== 'time' && k !== 'wal')
                    .flatMap(([, v]) => v?.events || [])
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
                    ${cp.wal_rate ? `<div class="stat-card"><div class="stat-value">${cp.wal_rate}</div><div class="stat-label">WAL Rate</div></div>` : ''}
                    ${cp.flush_rate ? `<div class="stat-card"><div class="stat-value">${cp.flush_rate}</div><div class="stat-label">Flush Rate</div></div>` : ''}
                    ${cp.warning_count ? `<div class="stat-card stat-card--alert"><div class="stat-value">${cp.warning_count}</div><div class="stat-label">Too Frequent</div></div>` : ''}
                </div>
                ${hasEvents ? `
                    ${buildChartContainer('chart-checkpoints', 'Checkpoint Distribution', { tooltip: 'Checkpoint writes over time. Timed is normal, WAL indicates heavy write load.' })}
                    <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:8px;font-size:12px;">
                        <span><span style="display:inline-block;width:12px;height:12px;background:var(--chart-bar);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Timed</span>
                        <span><span style="display:inline-block;width:12px;height:12px;background:var(--accent);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>WAL</span>
                        <span><span style="display:inline-block;width:12px;height:12px;background:#909399;border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Other</span>
                    </div>
                ` : hasWarnings ? `
                    ${buildChartContainer('chart-checkpoints', 'Checkpoint Frequency Warnings', {})}
                ` : ''}
                ${hasWALDistances ? `
                    <div style="margin-top:-0.5rem;">
                    ${buildChartContainer('chart-wal-distance', 'WAL Distance vs Estimate', { showBucketControl: false, tooltip: 'WAL generated between checkpoints. The estimate is PostgreSQL prediction for the next cycle.' })}
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

// Builds the optional one-line SERVER summary that sits right
// under the Summary section's header. Each fragment is a single
// span styled by severity: neutral for benign counters (starts,
// shutdowns, reloads), warning for non-fatal anomalies (crash
// recoveries, walsender timeouts, WAL receive failures…), alert
// for the worst events (backend crashes, invalidated slots).
// Fragments stay terse on purpose; the diagnostic detail (which
// signals, which params changed, which side of the replication
// broke) lives in the fragment's title= tooltip so the line is
// scannable at a glance and explorable on hover. Returns ''
// when nothing is worth surfacing — the line just disappears on
// healthy steady-state logs.
export function buildServerSummaryLine(data) {
    const s = data.server || {};
    const r = data.replication || {};
    const frags = [];
    const push = (text, sev, tip, detail) => frags.push({ text, sev, tip, detail });
    const plural = (n, sing, plur) => (n > 1 ? (plur || sing + 's') : sing);

    // Server-lifecycle markers, ordered roughly chronologically
    // (start → reload+config → shutdown → recovery → crash) so
    // the line reads as a tiny narrative.
    const starts = s.starts || 0;
    if (starts > 0) push(`${starts} ${plural(starts, 'start')}`, 'info');
    const reloads = s.reloads || 0;
    if (reloads > 0) push(`${reloads} ${plural(reloads, 'reload')}`, 'info');
    // Parameter changes ride along the reload that carried them —
    // the tooltip lists the actual settings so a DBA sees "what
    // changed" without opening the raw log.
    const params = s.parameter_changes || [];
    if (params.length > 0) {
        const shown = params.slice(0, 6).map(p => `${p.parameter} → ${p.new}`);
        if (params.length > 6) shown.push(`… +${params.length - 6} more`);
        push(`${params.length} param ${plural(params.length, 'change')}`, 'info', shown.join('\n'));
    }
    const totalShutdowns = (s.shutdowns_fast || 0) + (s.shutdowns_immediate || 0) + (s.shutdowns_smart || 0);
    if (totalShutdowns > 0) {
        const kinds = [];
        if (s.shutdowns_fast) kinds.push(`${s.shutdowns_fast} fast`);
        if (s.shutdowns_immediate) kinds.push(`${s.shutdowns_immediate} immediate`);
        if (s.shutdowns_smart) kinds.push(`${s.shutdowns_smart} smart`);
        push(`${totalShutdowns} ${plural(totalShutdowns, 'shutdown')}`, 'info', kinds.join(', '));
    }
    const recoveries = s.crash_recoveries || 0;
    if (recoveries > 0) push(`${recoveries} ${plural(recoveries, 'crash recovery', 'crash recoveries')}`, 'warning', 'database system was not properly shut down — automatic recovery');
    const crashes = s.backend_crashes || 0;
    if (crashes > 0) {
        const sigNames = { '1':'SIGHUP','2':'SIGINT','3':'SIGQUIT','6':'SIGABRT','9':'SIGKILL','11':'SIGSEGV','13':'SIGPIPE','14':'SIGALRM','15':'SIGTERM' };
        const sigCounts = s.signal_counts || {};
        const sigKeys = Object.keys(sigCounts).sort((a, b) => Number(a) - Number(b));
        const sigShort = sigKeys.map(k => sigNames[k] || 'signal ' + k).join(', ');
        const sigDetail = sigKeys.map(k => `${sigNames[k] || 'signal ' + k} ×${sigCounts[k]}`).join(', ');
        push(`${crashes} backend ${plural(crashes, 'crash', 'crashes')}`, 'alert', sigDetail, sigShort);
    }
    const auxExits = s.auxiliary_process_exits || 0;
    if (auxExits > 0) push(`${auxExits} aux ${plural(auxExits, 'exit')}`, 'warning', 'auxiliary process (bgwriter, walwriter, …) exited abnormally');

    // Replication markers — invalidated slots are the most
    // operationally severe, then per-cause termination markers
    // (named after the PG marker, diagnostic hint in tooltip),
    // then conflicts, then plain reconnects. The LastTermination
    // timestamp is appended to the last fired termination so the
    // line still gives a window even when several causes coexist.
    const slots = r.invalidated_slots || 0;
    if (slots > 0) push(`${slots} invalidated ${plural(slots, 'slot')}`, 'alert', 'replication slot dropped — standby must be rebuilt or resynced');
    const markers = r.markers || {};
    const termRows = [
        ['wal_receive_failed', 'WAL receive failure', 'replica lost primary'],
        ['walsender_timeout',  'walsender timeout',   'primary side — replica too slow'],
        ['replication_term',   'replication termination', 'primary closed walsender'],
        ['unexpected_eof',     'unexpected EOF',      'abrupt walsender disconnect'],
    ];
    const firedTerms = termRows.filter(([k]) => (markers[k] || 0) > 0);
    firedTerms.forEach(([k, label, hint], i) => {
        const n = markers[k];
        const isLast = i === firedTerms.length - 1;
        const lastT = isLast && r.last_termination
            ? `last ${r.last_termination.split(' ')[1] || r.last_termination}`
            : '';
        push(`${n} ${plural(n, label)}`, 'warning', hint, lastT);
    });
    const conflicts = r.conflicts_with_recovery || 0;
    if (conflicts > 0) push(`${conflicts} ${plural(conflicts, 'conflict')} w/ recovery`, 'warning', 'queries killed/cancelled because they blocked WAL replay');
    const reconnects = r.stream_reconnects || 0;
    if (reconnects > 0) push(`${reconnects} stream ${plural(reconnects, 'reconnect')}`, 'info');
    const pauses = r.recovery_pauses || 0;
    if (pauses > 0) push(`${pauses} recovery ${plural(pauses, 'pause')}`, 'info');

    // The health zone is a permanent part of the Summary card —
    // when nothing fired it shows an explicit "no server events"
    // so an absence reads as a positive signal (steady cluster)
    // rather than missing data, and the card keeps the same
    // structure whatever the log contains.
    if (frags.length === 0) {
        push('no server incidents', 'empty');
        frags[0].tip = 'no start / shutdown / crash / replication marker in this log';
    }
    // Fixed two-column grid whatever the fragment count, so the
    // zone has the same geometry on every report: one message
    // sits top-left, two split left/right, more fill column-
    // major (read down the left column first — same narrative
    // order as the CLI). Label left, optional muted detail
    // (signal names, last-termination time) as a plain suffix —
    // no parentheses.
    const parts = frags.map(f => {
        const tip = f.tip ? ` title="${esc(f.tip)}"` : '';
        const detail = f.detail ? `<span class="summary-server-detail">${esc(f.detail)}</span>` : '';
        return `<div class="summary-server-row"><span class="summary-server-frag summary-server-${f.sev}"${tip}>${esc(f.text)}</span>${detail}</div>`;
    }).join('');
    const rows = Math.max(1, Math.ceil(frags.length / 2));
    return `<div class="summary-separator summary-separator--tight"></div>
        <div class="summary-server-line" style="grid-template-rows: repeat(${rows}, auto)">${parts}</div>`;
}
