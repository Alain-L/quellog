// pgBouncer sections: pooler telemetry rendered alongside the PostgreSQL
// sections, in cards per pooler. Reads data.pgbouncer (a list of
// PgBouncerMetrics) and shows, for each pooler, the window it was drawn
// from, summary cards, four bar charts (throughput, network I/O,
// transaction and query time, pool wait) with the pooler's events drawn
// on them, and the queueing stretches; the session time and close reasons
// beside the per-database/user/host connections; the warnings and errors.

import { fmt, esc, escAttr, fmtDurationCoarse, buildNoDataMessage, wholeLogBadge, barList } from '../utils.js';
import { pgbValueFmt, pgbPeriod, pgbMarkers } from '../charts.js';
import { chartData, buildChartContainer } from '../charts.js';

const SESSION_BUCKETS = ['< 1s', '1s - 1min', '1min - 30min', '30min - 2h', '2h - 5h', '> 5h'];

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

// LIST_MAX caps the close-reason and error tables, as the text report
// does. Unlike the breakdowns they are short, and the rare row is the one
// worth reading.
const LIST_MAX = 20;

// table renders rows as a standard report table (the bare <table> in a
// .table-container that Locks/Temp Files use: uppercase header, .num
// columns), so long messages wrap and stay readable rather than being
// truncated like the bar lists. `cols` is [[label, isNum]]; `rows` holds
// already-escaped cells.
function table(cols, rows, hidden) {
    if (rows.length === 0) return '';
    return `
            <div class="table-container" style="max-height: none;">
                <table>
                    <thead><tr>${cols.map(([l, num]) => `<th${num ? ' class="num"' : ''}>${l}</th>`).join('')}</tr></thead>
                    <tbody>
                        ${rows.map(r => `<tr>${r.map((c, i) => `<td${cols[i][1] ? ' class="num"' : i === 0 ? ' style="overflow-wrap:break-word"' : ''}>${c}</td>`).join('')}</tr>`).join('')}
                        ${hidden > 0 ? `<tr><td colspan="${cols.length}" style="color: var(--text-muted)">… and ${fmt(hidden)} more</td></tr>` : ''}
                    </tbody>
                </table>
            </div>
    `;
}

// breakable lets an escaped cell wrap after "_", "/" or ":" as well as at
// spaces, so query_wait_timeout breaks between its words on a phone
// instead of mid-word, and every 30 characters of a run with none of them:
// a client names its own database before it authenticates, and 200
// characters without a break would widen the table past the screen. The
// grid cells holding the tables take min-width 0, so a table that is still
// too wide scrolls in its container instead of growing past a section
// that clips.
function breakable(html) {
    return html.replace(/[^\s_/:&;<>]{30}/g, '$&<wbr>').replace(/[_/:]/g, '$&<wbr>');
}

const pct = (n, total) => (total ? (n / total * 100).toFixed(1) : '0.0') + '%';

// span renders a first-to-last pair as the text report does, one time
// when both read the same: four columns fit a phone, five did not.
function span(first, last) {
    return first === last ? first : `${first} → ${last}`;
}

// reasonsTable splits the completed sessions by close reason, with each
// reason's average age: a client that left and one the pooler killed in
// its queue are different populations.
function reasonsTable(reasons, total) {
    const list = reasons || [];
    return table([['Close reason'], ['Count', true], ['%', true], ['Avg age', true]],
        list.slice(0, LIST_MAX).map(r => [breakable(esc(r.reason)), fmt(r.count), pct(r.count, total), sessionTime(r.total_seconds / r.count)]),
        list.length - LIST_MAX);
}

// sessionsLine states the average session time and who ended the
// sessions, mirroring PgBouncerMetrics.SessionsEndedByClient: a reason
// starting with "client " is the client leaving; any other is the pooler
// refusing or killing it.
function sessionsLine(p) {
    const parts = [`Average session time: ${sessionTime((p.total_session_seconds || 0) / p.sessions)}`];
    if (p.session_reasons) {
        const byClient = p.session_reasons
            .filter(r => r.reason.startsWith('client '))
            .reduce((n, r) => n + r.count, 0);
        parts.push(`Ended by the client: ${fmt(byClient)}`, `by the pooler: ${fmt(p.sessions - byClient)}`);
    }
    return `<div style="font-size: 0.75rem; color: var(--text-muted); padding-bottom: 0.25rem;">${parts.join(' · ')}</div>`;
}

// episodesTable lists when clients queued, mirroring the text report's
// stretches: start, end, duration and the worst snapshot of each.
function episodesTable(p, at) {
    const depth = waitIsQueueDepth(p.version);
    const wait = depth ? (us) => (us / 1e6).toFixed(1) + ' clients' : pgbValueFmt('micros');
    const list = p.wait_episodes || [];
    return table([['When'], ['Duration', true], ['Peak', true], ['Peak at', true]],
        list.slice(0, LIST_MAX).map(e => [span(at(e.start), at(e.end)),
            sessionTime((new Date(e.end) - new Date(e.start)) / 1000), wait(e.peak_wait_us).replace(' ', '\u00a0'), at(e.peak_timestamp)]),
        list.length - LIST_MAX);
}

function episodesTitle(p) {
    return waitIsQueueDepth(p.version) ? 'Stretches with 0.1 client or more queued' : 'Stretches with waits of 100 ms or more';
}

// timelineTable is the pooler's own history: starts, shutdowns, reloads
// and console commands. A detail that only repeats the kind ("RELOAD")
// is left out, as in the text report.
function timelineTable(events, at) {
    return table([['Time'], ['Event'], ['Detail']],
        (events || []).map(e => [at(e.timestamp), esc(e.kind), esc((e.detail || '').toLowerCase() === e.kind ? '' : e.detail || '')]), 0);
}

// pgbLegend renders a chart's legend: with two series, each item hides or
// shows its series (togglePgbSeries), as on the combined SQL chart; with
// `id` null, the static legend of the expanded chart.
function pgbLegend(id, series) {
    const swatch = (c) => `<span class="chart-legend-bar" style="background:${c}"></span>`;
    if (!id || series.length < 2) {
        return `<div class="chart-legend">${series.map(s => `<span class="chart-legend-item" style="cursor:default">${swatch(s.color)}${esc(s.label)}</span>`).join('')}</div>`;
    }
    return `<div class="chart-legend">${series.map((s, i) => `<span class="chart-legend-item" data-chart="${id}" data-series="${i}" onclick="togglePgbSeries('${id}', ${i})">${swatch(s.color)}${esc(s.label)}</span>`).join('')}</div>`;
}

// markerKey names, above the charts, the event markers drawn on them
// between `from` and `to`: only the kinds that are there.
function markerKey(marks, from, to) {
    const on = marks.filter(m => m.b >= from && m.a <= to);
    if (!on.length) return '';
    const span = (tone) => `<span style="display:inline-block;width:12px;height:10px;border-left:2px solid var(${tone});border-right:2px solid var(${tone});background:color-mix(in srgb, var(${tone}) 20%, transparent)"></span>`;
    const line = `<span style="display:inline-block;width:0;height:10px;border-left:1px dashed var(--text-muted)"></span>`;
    const items = [];
    if (on.some(m => m.label === 'paused')) items.push([span('--warning'), 'pool paused (PAUSE … RESUME)']);
    if (on.some(m => m.label === 'restart')) items.push([span('--danger'), 'pooler restarted']);
    if (on.some(m => m.label === 'shutdown')) items.push([span('--danger'), 'pooler shut down']);
    if (on.some(m => m.label === 'reload')) items.push([line, 'configuration reloaded']);
    if (on.some(m => m.kind === 'command' && m.label !== 'reload')) items.push([line, 'console command, named on the chart']);
    return `<div class="chart-legend" style="justify-content:flex-start;gap:1rem;margin:0.5rem 0 0;">${items.map(([m, t]) => `<span style="display:inline-flex;align-items:center;gap:0.35rem">${m}${t}</span>`).join('')}</div>`;
}

// sessionHistogram renders the fixed-bucket session-time distribution
// using the same bar-list markup, in bucket order (title handled by the
// caller so it aligns with the other grouped subsection titles).
function sessionHistogram(dist, total) {
    if (!dist) return '';
    return barList('', SESSION_BUCKETS.map(b => [b, dist[b] || 0]), total, true);
}

// ---- what the figures were drawn from ------------------------------
//
// The text report opens on the window, the lines read and the lines that
// never reached a counter, so a report built from 100 of 120 lines reads
// differently from one built from all of them. HTML is the format most
// people read: it publishes the same accounting, in the same words, as a
// row of cards above the pooler's own figures.

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

// clock renders a local wall-clock time, and day the date that goes with
// it, in the report's existing short vocabulary ("20 Sep 2026").
function clock(d) {
    const p2 = (n) => String(n).padStart(2, '0');
    return `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`;
}

function day(d) {
    return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}`;
}

// captureWindow returns the capture bounds as Date objects, or null when the
// payload carries no usable timestamp (a section built from a fixture, a
// period with no entry).
function captureWindow(p) {
    if (!p.start_date || !p.end_date) return null;
    const from = new Date(p.start_date);
    const to = new Date(p.end_date);
    if (isNaN(from) || isNaN(to) || to < from) return null;
    return { from, to };
}

// captureHeader states which window the whole section describes. The
// second date is dropped when both ends fall on the same day, as the
// Summary card does.
function captureHeader(w) {
    if (!w) return '';
    const label = day(w.from) === day(w.to)
        ? `${day(w.from)} ${clock(w.from)} → ${clock(w.to)}`
        : `${day(w.from)} ${clock(w.from)} → ${day(w.to)} ${clock(w.to)}`;
    return `<div class="summary-header"><div class="summary-date">${label}</div></div>`;
}

// statCard renders one card of the shared grid. `variant` takes the
// stat-card modifier classes (warning / alert) the section already uses.
function statCard(value, label, variant) {
    const cls = variant ? `stat-card ${variant}` : 'stat-card';
    return `<div class="${cls}"><div class="stat-value">${value}</div><div class="stat-label">${label}</div></div>`;
}

// sessionTime formats a mean session age. TotalSessionSeconds is counted
// in whole seconds, so anything under a minute is reported to the second
// exactly as the text report does; longer means go through the shared
// coarse formatter.
function sessionTime(seconds) {
    if (seconds < 60) return `${Math.round(seconds)}s`;
    return fmtDurationCoarse(seconds * 1000);
}

// readLine is the line accounting: what was read, what became an entry,
// and what did not, written after the pooler's identity rather than as
// cards: it tells how far the figures below can be trusted, it is not
// one of them. The three loss counters appear only when they are
// non-zero, as in the text report -- a row of zeroes teaches nothing. A
// non-zero unrecognized count is the one that changes how the rest
// should be read, so it carries the warning colour.
function readLine(p) {
    const n = (v, label, warn) => `<span${warn ? ' style="color: var(--warning);"' : ''}>${label} <b>${fmt(v)}</b></span>`;
    const parts = [];
    if (p.lines_read > 0) parts.push(n(p.lines_read, 'Log lines read'));
    parts.push(n(p.lines || 0, 'Log entries'));
    if (p.lines_unrecognized > 0) parts.push(n(p.lines_unrecognized, 'Unrecognized lines', true));
    const filtered = (p.lines_read || 0) - (p.lines_unrecognized || 0) - (p.lines || 0);
    if (filtered > 0) parts.push(n(filtered, 'Filtered-out lines'));
    if (p.debug_lines > 0) parts.push(n(p.debug_lines, 'Debug/noise entries'));
    parts.push(n(p.stats_sample_count || 0, 'Stats snapshots'));
    return `<div style="text-align: center; font-size: 0.75rem; color: var(--text-muted); margin-bottom: 0.6rem;">${parts.join(' · ')}</div>`;
}

// waitIsQueueDepth mirrors PgBouncerMetrics.WaitIsQueueDepth: before
// pgBouncer 1.23 the wait field adds up the time every client spent
// queued per second, a queue depth rather than a duration.
function waitIsQueueDepth(version) {
    const [x, y] = (version || '').split('.').map(Number);
    return Number.isInteger(x) && Number.isInteger(y) && (x < 1 || (x === 1 && y < 23));
}

function queueDepthText(version) {
    return `PgBouncer ${esc(version)} predates 1.23: its wait field is the time all clients spent queued per second, shown here as the number of clients queued on average and left out of the timing chart. From 1.23 it is a wait time per server assignment.`;
}

// ---- configuration gaps -------------------------------------------
//
// Three pgBouncer settings decide how much of this section can exist:
// log_connections / log_disconnections gate the client lines,
// stats_period gates the telemetry lines, and pool_mode -- logged
// nowhere -- sets what the pool-wait figure is averaged over. A block
// that simply disappears leaves the reader unable to tell "nothing
// happened" from "your pooler does not log it", so each empty block
// says which of the three is responsible.
//
// The three predicates below mirror PgBouncerMetrics.StatsPeriod,
// .StatsUnderCovered and .ClientLinesMissing (analysis/pgbouncer.go);
// they are recomputed here because the payload carries the metrics, not
// the verdicts. Keep the two in step.

// statsPeriodSeconds infers stats_period from the median interval
// between consecutive snapshots. 0 when fewer than two leave nothing to
// measure.
function statsPeriodSeconds(series) {
    if (!series || series.length < 2) return 0;
    const gaps = [];
    for (let i = 1; i < series.length; i++) {
        const d = (new Date(series[i].time) - new Date(series[i - 1].time)) / 1000;
        if (d > 0) gaps.push(d);
    }
    if (gaps.length === 0) return 0;
    gaps.sort((a, b) => a - b);
    return gaps[Math.floor(gaps.length / 2)];
}

// statsUnderCovered reports whether the snapshots account for less than
// half of the reported window -- or for an unknown share of it, which a
// single snapshot always does.
function statsUnderCovered(p) {
    if (!p.stats_sample_count) return false;
    const window = (new Date(p.end_date) - new Date(p.start_date)) / 1000;
    if (!(window > 0)) return false;
    const covered = statsPeriodSeconds(p.stats_series) * p.stats_sample_count;
    if (!(covered > 0)) return true;
    return covered * 2 < window;
}

// clientLinesMissing reports whether the pooler demonstrably served
// clients over this window while logging none of them -- the only thing
// that separates a silenced log from an idle one.
function clientLinesMissing(p) {
    if (p.login_attempts > 0 || p.sessions > 0) return false;
    // Only the snapshot counters prove it, exactly as the Go side does.
    // A server connection opened and a pooler error are not proof:
    // min_pool_size opens backends with nobody connected, and a backend
    // refusing the pooler errors out before any client exists.
    return (p.max_xacts_per_sec > 0) || (p.max_queries_per_sec > 0);
}

// note renders a one-paragraph remark under a block, muted by default
// and in the warning colour when it qualifies figures that are shown.
function note(text, warn) {
    const color = warn ? 'var(--warning)' : 'var(--text-muted)';
    return `<div style="font-size: 0.75rem; color: ${color}; padding: 0.5rem 0; text-align: center;">${text}</div>`;
}

// emptyBlock is the shared no-data placeholder plus, when the window
// contradicts an idle pooler, the sentence that says so. Without that
// sentence the placeholder reads the same either way.
function emptyBlock(hint, contradiction) {
    return (contradiction ? note(contradiction, true) : '') + buildNoDataMessage(hint);
}

const CONNECTIONS_HINT = '<code>log_connections = 1</code> in <code>pgbouncer.ini</code>';
const SESSIONS_HINT = '<code>log_disconnections = 1</code> in <code>pgbouncer.ini</code>';
const STATS_HINT = '<code>stats_period</code> in <code>pgbouncer.ini</code>';
const CONNECTIONS_CONTRADICTION = 'Traffic shows over this window, yet no client connection was logged in it: the clients may predate the window, or <code>log_connections</code> is off.';
const SESSIONS_CONTRADICTION = 'Traffic shows over this window, yet no session close was logged in it: the sessions may still be open, or <code>log_disconnections</code> is off.';
const NO_STATS_TEXT = 'No <code>stats:</code> line in this window; throughput, network I/O and timing all come from those lines. pgBouncer writes one per <code>stats_period</code>, 60\u00a0s by default, so a shorter capture carries none.';

// MIN_CHART_POINTS is the number of snapshots a time series needs before
// it can be drawn. uPlot cannot derive a scale from a single point: it
// falls back to a default range and draws an empty plot under an axis
// taken from nowhere -- month labels over a capture of half a minute.
// An invented axis is read as a measured one, so below this the section
// says what it has instead of drawing it.
const MIN_CHART_POINTS = 2;

// oneSnapshotText explains the charts the section is not drawing. It
// stands on its own: the coverage caveat above it qualifies the figures,
// which is a different statement and does not always appear.
function oneSnapshotText(n) {
    const lead = n === 1
        ? 'This window holds a single <code>stats:</code> line'
        : `This window holds ${fmt(n)} <code>stats:</code> lines`;
    return `${lead}: throughput, network I/O and timing need at least two snapshots to be drawn against a time axis. pgBouncer writes one line per <code>stats_period</code>, 60\u00a0s by default, so a capture shorter than two periods carries a single point.`;
}

// coverageText qualifies figures drawn from snapshots that leave most
// of the window unmeasured: a pooler sampled once at startup otherwise
// reports 0.0 queries/s over a window holding hundreds of sessions, and
// states it as a measurement.
function coverageText(p) {
    const window = Math.round((new Date(p.end_date) - new Date(p.start_date)) / 1000);
    const covered = Math.round(statsPeriodSeconds(p.stats_series) * p.stats_sample_count);
    const lead = covered > 0
        ? `${fmt(p.stats_sample_count)} stats snapshots cover ${fmt(covered)}\u00a0s of this ${fmt(window)}\u00a0s window`
        : `A single stats snapshot for this ${fmt(window)}\u00a0s window, of unknown extent`;
    return `${lead}: pgBouncer logs nothing between two snapshots, so the figures here describe what was sampled, not the window. Check <code>stats_period</code> in <code>pgbouncer.ini</code>.`;
}

// poolerLine names the pooler a section describes: its address or file,
// its release, and the files it was read from -- by base name, the full
// path in the tooltip, so the line does not grow with the directory the
// logs sit in.
function poolerLine(p) {
    const parts = [esc(p.name || '')];
    if (p.version) parts.push('PgBouncer ' + esc(p.version));
    if (p.files && p.files.length) {
        parts.push(p.files.map(f => `<span title="${escAttr(f)}">${esc(f.split(/[\\/]/).pop())}</span>`).join(', '));
    }
    return `<div style="text-align: center; font-size: 0.75rem; color: var(--text-muted); overflow-wrap: anywhere;">${parts.join(' · ')}</div>`;
}

export function buildPgBouncerSection(data) {
    const poolers = (data.pgbouncer || []).filter(p => p && p.lines);
    if (poolers.length === 0) {
        return `
            <div class="section" id="pgbouncer">
                <div class="section-header">pgBouncer</div>
                <div class="section-body">${buildNoDataMessage('a pgBouncer log file')}</div>
            </div>
        `;
    }
    // The pooler track is analyzed on its own and its figures carry no
    // per-item timestamps to re-scope, so a time filter leaves them whole
    // — annotate rather than present stale numbers as scoped.
    const wholeLogNote = data._wholeLog?.pgbouncer ? wholeLogBadge() : '';
    return poolers.map((p, i) => poolerSection(p, i === 0 ? '' : '-' + (i + 1), poolers.length > 1, wholeLogNote)).join('');
}

// poolerSection renders one pooler, in cards: the figures, the charts and
// the queueing stretches; the sessions beside the connections; the
// warnings and errors, beside the timeline when no chart carries the
// events. `suffix` keeps the section and
// chart ids unique when there are several; `named` puts the pooler's name
// in the headers then.
function poolerSection(p, suffix, named, wholeLogNote) {
    const at = (ts) => {
        const d = new Date(ts);
        return isNaN(d) ? '' : clock(d);
    };
    const depth = waitIsQueueDepth(p.version);
    // Register the time-series charts (drawn after DOM insertion).
    const series = p.stats_series || [];
    const hasSeries = series.length >= MIN_CHART_POINTS;
    let charts = null;
    if (hasSeries) {
        const points = series.map(s => ({ ...s, t: Math.round(new Date(s.time).getTime() / 1000) }));
        const period = pgbPeriod(points.map(x => x.t));
        const events = p.events || [];
        // Two series that never differ would draw every bar twice: one bar,
        // named for both.
        const same = (a, b) => series.every(s => (s[a] || 0) === (s[b] || 0));
        const SPECS = {
            throughput: { title: 'Request Throughput', tooltip: 'Queries and transactions per second. Each bar is one stats line: an average over the stats_period it closes, not an instantaneous rate. Above 60 snapshots they are grouped by time bucket.',
                ...(same('xacts_per_sec', 'queries_per_sec')
                    ? { series: [{ key: 'xacts_per_sec', label: 'transactions/s = queries/s', color: 'var(--chart-bar)', fmt: 'rate' }] }
                    : { ownAxis: true, series: [{ key: 'queries_per_sec', label: 'queries/s', color: 'var(--chart-bar)', fmt: 'rate' }, { key: 'xacts_per_sec', label: 'transactions/s', color: 'var(--accent)', fmt: 'rate' }] }) },
            io: { title: 'Network I/O', tooltip: 'Bytes received (above the axis) and sent (below) per second, averaged over each stats_period.', mirror: true,
                series: [{ key: 'in_bytes_per_sec', label: 'in', color: 'var(--chart-bar)', fmt: 'bytes' }, { key: 'out_bytes_per_sec', label: 'out (below)', color: 'var(--accent)', fmt: 'bytes' }] },
            timing: { title: 'Transaction / Query Time', tooltip: 'Time spent on the server. pgBouncer logs one average per snapshot, never a single operation; a bar that groups snapshots shows the worst of them. On a log scale when a value is over a hundred times the median.', logAuto: true,
                ...(same('xact_us', 'query_us')
                    ? { series: [{ key: 'xact_us', label: 'transaction = query', color: 'var(--chart-bar)', fmt: 'micros' }] }
                    : { ownAxis: true, series: [{ key: 'xact_us', label: 'transaction', color: 'var(--chart-bar)', fmt: 'micros' }, { key: 'query_us', label: 'query', color: 'var(--accent)', fmt: 'micros' }] }) },
            ...(depth ? {} : { wait: { title: 'Pool Wait', tooltip: 'How long clients queued for a server connection: a pool sizing signal, not a query one. A bar that groups snapshots shows the worst of them. What it is averaged over depends on pool_mode, which pgBouncer logs nowhere.',
                series: [{ key: 'wait_us', label: 'pool wait', color: 'var(--danger)', fmt: 'micros' }] } }),
        };
        charts = Object.entries(SPECS).map(([name, spec]) => {
            const id = `chart-pgb-${name}${suffix}`;
            chartData.set(id, { type: 'pgb-bars', points, period, events, ...spec, legend: pgbLegend(null, spec.series) });
            return { id, spec };
        });
        charts.key = markerKey(pgbMarkers(events), points[0].t - period, points[points.length - 1].t);
    }

    const hasStats = p.stats_sample_count > 0;
    const hasSessions = p.sessions > 0;
    const hasConns = p.login_attempts > 0;
    const missingClients = clientLinesMissing(p);
    const name = named ? ' · ' + esc(p.name) : '';
    const card = (id, title, body) => `
        <div class="section" id="${id}${suffix}">
            <div class="section-header">${title}${name}${wholeLogNote}</div>
            <div class="section-body">${body}</div>
        </div>`;
    // A card alone on its row takes the full width.
    const row = (cards) => {
        const shown = cards.filter(Boolean);
        return shown.length > 1 ? `<div class="grid grid-2">${shown.join('')}</div>` : shown.join('');
    };

    const sessions = hasSessions
        ? `${sessionsLine(p)}${sessionHistogram(p.session_distribution, p.sessions)}
           ${p.session_reasons ? `<div class="subsection-title">Close Reasons</div>${reasonsTable(p.session_reasons, p.sessions)}` : ''}`
        : emptyBlock(SESSIONS_HINT, missingClients ? SESSIONS_CONTRADICTION : '');
    const connections = hasConns
        ? dimTabs([
            { label: 'Database', map: p.connections_per_database },
            { label: 'User', map: p.connections_per_user },
            { label: 'Host', map: p.connections_per_host },
        ], p.login_attempts)
        : emptyBlock(CONNECTIONS_HINT, missingClients ? CONNECTIONS_CONTRADICTION : '');

    return `
        ${card('pgbouncer', 'pgBouncer', `
                ${captureHeader(captureWindow(p))}
                ${poolerLine(p)}
                ${readLine(p)}
                <div class="stat-grid">${summaryCards(p)}</div>
                ${hasStats && statsUnderCovered(p) ? note(coverageText(p), true) : ''}
                ${hasStats && depth ? note(queueDepthText(p.version), true) : ''}
                ${!hasStats ? emptyBlock(STATS_HINT, '') + note(NO_STATS_TEXT, false) : ''}
                ${hasStats && !hasSeries ? note(oneSnapshotText(series.length), false) : ''}
                ${charts ? `
                    ${charts.key}
                    <div class="grid grid-2" style="margin-top:0.5rem;">
                        ${charts.map(({ id, spec }) => `<div${charts.length % 2 && id.startsWith('chart-pgb-timing') ? ' style="grid-column: 1 / -1"' : ''}>
                            ${buildChartContainer(id, spec.title, { tooltip: spec.tooltip })}
                            ${pgbLegend(id, spec.series)}
                        </div>`).join('')}
                    </div>
                ` : ''}
                ${p.wait_episodes ? `<div class="subsection"><div class="subsection-title">${episodesTitle(p)}</div>${episodesTable(p, at)}</div>` : ''}
        `)}
        ${row([
            card('pgbouncer-sessions', 'pgBouncer Sessions', sessions),
            card('pgbouncer-connections', 'pgBouncer Connections', connections),
        ])}
        ${row([
            // With charts, the events are drawn on them; without, they keep
            // their own table.
            p.events && !charts ? card('pgbouncer-timeline', 'pgBouncer Timeline', timelineTable(p.events, at)) : '',
            card('pgbouncer-events', 'pgBouncer Events', `<div class="events-section">${eventsTabs(p.errors, at)}</div>`),
        ])}
    `;
}

// summaryCards are the figures a DBA reads first: traffic, the three
// timings, the sessions, and the warnings and errors, as the text
// report's default view states them.
function summaryCards(p) {
    const micros = pgbValueFmt('micros');
    const rate = (v) => (v == null ? '' : v.toFixed(1) + '/s');
    const hasStats = p.stats_sample_count > 0;
    const { errors, warnings } = levelTotals(p.errors);
    return [
        hasStats ? statCard(rate(p.avg_xacts_per_sec), 'Transactions') : '',
        hasStats ? statCard(rate(p.avg_queries_per_sec), 'Queries') : '',
        hasStats ? statCard(micros(p.avg_xact_us), 'Avg transaction time') : '',
        hasStats ? statCard(micros(p.avg_query_us), 'Avg query time') : '',
        !hasStats ? '' : waitIsQueueDepth(p.version)
            ? statCard(((p.avg_wait_us || 0) / 1e6).toFixed(1) + ' clients', 'Avg clients queued')
            : statCard(micros(p.avg_wait_us), 'Avg pool wait time'),
        statCard(fmt(p.sessions || 0), 'Completed sessions'),
        statCard(fmt(warnings), 'Warnings', warnings ? 'stat-card--warning' : ''),
        statCard(fmt(errors), 'Errors', errors ? 'stat-card--alert' : ''),
    ].join('');
}

// levelTotals splits the occurrences into errors (ERROR, FATAL) and
// warnings, as the text report's two rows do.
function levelTotals(errors) {
    let e = 0, w = 0;
    for (const m of errors || []) {
        if (m.level === 'WARNING') w += m.count; else e += m.count;
    }
    return { errors: e, warnings: w };
}

// eventsTabs lays the warnings and errors out as the PostgreSQL Events
// card: a tab per level, the most frequent first, each with when it was
// first and last logged.
function eventsTabs(errors, at) {
    const of = (level) => (errors || []).filter(m => (level === 'WARNING' ? m.level === 'WARNING' : m.level !== 'WARNING'));
    const total = (level) => of(level).reduce((n, m) => n + m.count, 0);
    const rows = (level) => {
        const items = of(level);
        if (!items.length) return `<div class="pt-none">No ${level} events recorded</div>`;
        return table([['Message'], ['Count', true], ['When', true]],
            items.slice(0, LIST_MAX).map(m => [breakable(esc(m.message)), fmt(m.count), span(at(m.first), at(m.last))]),
            items.length - LIST_MAX);
    };
    const errs = total('ERROR');
    return `<ql-tabs>
        <ql-tab class="error"${errs ? ' selected' : ''}>ERROR<ql-badge>${fmt(errs)}</ql-badge></ql-tab>
        <ql-tab class="warning"${errs ? '' : ' selected'}>WARNING<ql-badge>${fmt(total('WARNING'))}</ql-badge></ql-tab>
        <ql-panel>${rows('ERROR')}</ql-panel>
        <ql-panel>${rows('WARNING')}</ql-panel>
    </ql-tabs>`;
}
