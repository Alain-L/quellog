// pgBouncer section: pooler telemetry rendered alongside the PostgreSQL
// sections. Reads data.pgbouncer (the PgBouncerMetrics JSON) and shows
// the window it was drawn from, summary cards, three time-series charts
// (throughput, network I/O, query/pool-wait timing), a session-time
// histogram, per-database/user/host breakdowns, and the ranked error list.

import { fmt, esc, fmtDurationCoarse, buildNoDataMessage, wholeLogBadge} from '../utils.js';
import { pgbValueFmt } from '../charts.js';
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

// TOP_N is how many rows a ranked breakdown shows before collapsing the
// rest into the report's "… and N more" line. A pooler fronting a
// multi-tenant estate has thousands of users, and ten rows adding up to
// 12% read as the whole truth unless the cut is stated.
const TOP_N = 10;

// moreRow renders that line for a bar list, muted so it reads as a note
// on the table rather than another row of data.
function moreRow(hidden) {
    if (hidden <= 0) return '';
    return `<div class="list-item" style="color: var(--text-muted); font-size: 11px;">… and ${fmt(hidden)} more</div>`;
}

// barList renders name→count rows as the report's shared bar-list (the
// same .scroll-list / .list-item / .bar-fill markup the Clients section
// uses), so the pgBouncer breakdowns look identical to the rest.
// `rows` is an array of [name, count]; when `ordered` is false they are
// sorted by count descending, clipped to TOP_N, and the clipped tail is
// declared under the list.
function barList(title, rows, total, ordered) {
    let hidden = 0;
    if (ordered) {
        rows = rows.filter(([, c]) => c > 0);
    } else {
        const ranked = rows.slice()
            .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
            .filter(([, c]) => c > 0);
        hidden = Math.max(0, ranked.length - TOP_N);
        rows = ranked.slice(0, TOP_N);
    }
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
                ${moreRow(hidden)}
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

// errorsTable renders the ranked errors as a standard report table (the
// bare <table> in a .table-container that Locks/Temp Files use: uppercase
// header, .num columns, no inline styling), so long error messages wrap
// and stay readable rather than being truncated like the bar lists.
function errorsTable(m, total) {
    if (!m) return '';
    const ranked = Object.entries(m).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
    const rows = ranked.slice(0, TOP_N);
    const hidden = ranked.length - rows.length;
    if (rows.length === 0) return '';
    return `
            <div class="table-container" style="max-height: 220px;">
                <table>
                    <thead><tr>
                        <th>Error</th>
                        <th class="num">Count</th>
                        <th class="num">%</th>
                    </tr></thead>
                    <tbody>
                        ${rows.map(([name, count]) => `
                            <tr>
                                <td style="overflow-wrap:anywhere">${esc(name)}</td>
                                <td class="num">${fmt(count)}</td>
                                <td class="num">${total ? (count / total * 100).toFixed(1) : '0.0'}%</td>
                            </tr>
                        `).join('')}
                        ${hidden > 0 ? `<tr><td colspan="3" style="color: var(--text-muted)">… and ${fmt(hidden)} more</td></tr>` : ''}
                    </tbody>
                </table>
            </div>
    `;
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
    if (!p.min_timestamp || !p.max_timestamp) return null;
    const from = new Date(p.min_timestamp);
    const to = new Date(p.max_timestamp);
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

// FIGURE_CARD_MIN_PX widens the shared .stat-grid minimum for this
// section. Its labels are longer than the report's others, so at the
// default they wrap to three lines and the fourteen figures read as one
// dense wall; this breaks them into even rows instead.
const FIGURE_CARD_MIN_PX = 150;

// statCard renders one card of the shared grid. `variant` takes the
// stat-card modifier classes (warning / alert) the section already uses.
function statCard(value, label, variant) {
    return `<div class="stat-card${variant ? ' ' + variant : ''}"><div class="stat-value">${value}</div><div class="stat-label">${label}</div></div>`;
}

// sessionTime formats a mean session age. TotalSessionSeconds is counted
// in whole seconds, so anything under a minute is reported to the second
// exactly as the text report does; longer means go through the shared
// coarse formatter.
function sessionTime(seconds) {
    if (seconds < 60) return `${Math.round(seconds)}s`;
    return fmtDurationCoarse(seconds * 1000);
}

// readCards is the line accounting: what was read, what became an entry,
// and what did not. The three loss counters appear only when they are
// non-zero, as in the text report -- a row of zeroes teaches nothing and
// pushes the figures that matter off the first screen. A non-zero
// unrecognized count is the one that changes how the rest should be
// read, so it carries the warning colour.
function readCards(p, w) {
    const cards = [];
    // Rounded to the second, as the coverage caveat rounds it: the same
    // window stated as 36 s here and 37 s there reads as two windows.
    if (w) cards.push(statCard(fmtDurationCoarse(Math.round((w.to - w.from) / 1000) * 1000), 'Duration'));
    if (p.lines_read > 0) cards.push(statCard(fmt(p.lines_read), 'Log lines read'));
    cards.push(statCard(fmt(p.lines || 0), 'Log entries'));
    if (p.lines_unrecognized > 0) {
        cards.push(statCard(fmt(p.lines_unrecognized), 'Unrecognized lines', 'stat-card--warning'));
    }
    const filtered = (p.lines_read || 0) - (p.lines_unrecognized || 0) - (p.lines || 0);
    if (filtered > 0) cards.push(statCard(fmt(filtered), 'Filtered-out lines'));
    if (p.debug_lines > 0) cards.push(statCard(fmt(p.debug_lines), 'Debug/noise entries'));
    cards.push(statCard(fmt(p.stats_sample_count || 0), 'Stats snapshots'));
    // Content-width and centred, as the Summary card's own tile row is.
    // Provenance opens the section, as it opens the text report, but
    // cards stretched across the report would read as its headline.
    // auto-fit, as the telemetry row: a fifth card (verbose=2, or any
    // loss counter) does not fit a phone, and a fixed count clips the row
    // instead of wrapping it.
    return `<div class="stat-grid" style="grid-template-columns: repeat(auto-fit, minmax(110px, auto)); justify-content: center;">${cards.join('')}</div>`;
}

// telemetryCards states the figures the stats lines carry, in the order
// and the wording the text report uses for its REQUEST THROUGHPUT, QUERY
// TIMING and POOL WAIT sections. The charts plot the same series, but a
// curve is not a number: the mean a reader quotes has to be readable
// without hovering, and on a saturated pooler the pool wait is the one
// figure that names the problem.
//
// They join the section's single figure grid rather than forming a row of
// their own: three stacked rows of different widths read as a staircase,
// and they would split Request throughput across two of them.
function telemetryCards(p) {
    const micros = pgbValueFmt('micros');
    const rate = (v) => (v == null ? '' : v.toFixed(1) + '/s');
    const bytes = pgbValueFmt('bytes');
    const cards = [
        [rate(p.avg_xacts_per_sec), 'Transactions'],
        [micros(p.avg_xact_us), 'Avg transaction time'],
        [micros(p.avg_query_us), 'Avg query time'],
        [micros(p.avg_wait_us), 'Avg pool wait time'],
        [bytes(p.avg_in_bytes_per_sec), 'Network in'],
        [bytes(p.avg_out_bytes_per_sec), 'Network out'],
    ].map(([v, l]) => statCard(v, l));
    return cards.join('');
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

const SETUP_DOC = 'docs/pgbouncer-setup.md';

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
    const window = (new Date(p.max_timestamp) - new Date(p.min_timestamp)) / 1000;
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

const CONNECTIONS_HINT = '<code>log_connections = 1</code> in <code>pgbouncer.ini</code>; see ' + SETUP_DOC;
const SESSIONS_HINT = '<code>log_disconnections = 1</code> in <code>pgbouncer.ini</code>; see ' + SETUP_DOC;
const STATS_HINT = '<code>stats_period</code> in <code>pgbouncer.ini</code>; see ' + SETUP_DOC;
const CONNECTIONS_CONTRADICTION = 'Traffic over this window, but no client connection logged in it: the clients may predate the window, or the lines may be off.';
const SESSIONS_CONTRADICTION = 'Traffic over this window, but no session close logged in it: the sessions may still be open, or the lines may be off.';
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
    const window = Math.round((new Date(p.max_timestamp) - new Date(p.min_timestamp)) / 1000);
    const covered = Math.round(statsPeriodSeconds(p.stats_series) * p.stats_sample_count);
    const lead = covered > 0
        ? `${fmt(p.stats_sample_count)} stats snapshots cover ${fmt(covered)}\u00a0s of this ${fmt(window)}\u00a0s window`
        : `A single stats snapshot for this ${fmt(window)}\u00a0s window, of unknown extent`;
    return `${lead}: pgBouncer logs nothing between two snapshots, so the figures here describe what was sampled, not the window. Check <code>stats_period</code> in <code>pgbouncer.ini</code>; see ${SETUP_DOC}.`;
}

export function buildPgBouncerSection(data) {
    // The pooler track is analyzed on its own and its figures carry no
    // per-item timestamps to re-scope, so a time filter leaves them whole
    // — annotate rather than present stale numbers as scoped.
    const wholeLogNote = data._wholeLog?.pgbouncer ? wholeLogBadge() : '';
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
    const hasSeries = series.length >= MIN_CHART_POINTS;
    if (hasSeries) {
        const t = series.map(s => Math.round(new Date(s.time).getTime() / 1000));
        chartData.set('chart-pgb-throughput', {
            type: 'pgb-series', t,
            series: [
                { label: 'transactions/s', key: 'xacts_per_sec', color: 'var(--accent)' },
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
                { label: 'transaction', key: 'xact_us', color: 'var(--accent)', fmt: 'micros' },
                { label: 'query', key: 'query_us', color: 'var(--purple)', fmt: 'micros' },
                { label: 'pool wait', key: 'wait_us', color: 'var(--danger)', fmt: 'micros' },
            ],
            values: series, fmt: 'micros',
        });
    }

    const hasStats = p.stats_sample_count > 0;
    const capture = captureWindow(p);
    const hasSessions = p.sessions > 0;
    const hasConns = p.login_attempts > 0;
    const hasErrors = p.total_errors > 0;
    const missingClients = clientLinesMissing(p);

    // The closing row pairs the session histogram with the error table.
    // Either can be absent -- a pooler with log_disconnections off has no
    // histogram, a healthy one has no errors -- and a two-column grid
    // holding one block leaves half the report blank, so the survivor
    // takes the full width.
    const closing = [
        hasSessions ? `<div class="subsection-title" style="margin-top:0">Session Time</div>${sessionHistogram(p.session_distribution, p.sessions)}` : '',
        hasErrors ? `<div class="subsection-title" style="margin-top:0">Most Frequent Errors</div>${errorsTable(p.errors, p.total_errors)}` : '',
    ].filter(Boolean);

    return `
        <div class="section" id="pgbouncer">
            <div class="section-header">pgBouncer${wholeLogNote}</div>
            <div class="section-body">
                ${captureHeader(capture)}
                ${readCards(p, capture)}
                <div class="stat-grid" style="grid-template-columns: repeat(auto-fit, minmax(${FIGURE_CARD_MIN_PX}px, 1fr));">
                    ${hasStats ? `
                    <div class="stat-card" title="Mean of the per-snapshot query rates pgBouncer logged, not a rate measured over the whole capture"><div class="stat-value">${p.avg_queries_per_sec != null ? p.avg_queries_per_sec.toFixed(1) : '0'}/s</div><div class="stat-label">Avg queries/s</div></div>
                    <div class="stat-card" title="Highest queries/s seen on a single stats snapshot. pgBouncer logs one average per stats_period, so this is the busiest snapshot average, not the busiest instant."><div class="stat-value">${fmt(p.max_queries_per_sec || 0)}/s</div><div class="stat-label">Max snapshot avg queries/s</div></div>
                    ${telemetryCards(p)}` : ''}
                    <div class="stat-card"><div class="stat-value">${fmt(p.login_attempts || 0)}</div><div class="stat-label">Login attempts</div></div>
                    <div class="stat-card"><div class="stat-value">${fmt(p.sessions || 0)}</div><div class="stat-label">Completed sessions</div></div>
                    ${hasSessions ? statCard(sessionTime((p.total_session_seconds || 0) / p.sessions), 'Average session time') : ''}
                    <div class="stat-card" title="Server connections opened over the capture, not the number held at any moment: a pool capped at five can open many more over time."><div class="stat-value">${fmt(p.server_connections || 0)}</div><div class="stat-label">Server connections opened</div></div>
                    ${p.reserve_pool_activations ? `<div class="stat-card stat-card--warning"><div class="stat-value">${fmt(p.reserve_pool_activations)}</div><div class="stat-label">Reserve pool activations</div></div>` : ''}
                    <div class="stat-card${hasErrors ? ' stat-card--alert' : ''}"><div class="stat-value">${fmt(p.total_errors || 0)}</div><div class="stat-label">Errors</div></div>
                </div>
                ${hasStats && statsUnderCovered(p) ? note(coverageText(p), true) : ''}
                ${!hasStats ? emptyBlock(STATS_HINT, '') + note(NO_STATS_TEXT, false) : ''}
                ${hasStats && !hasSeries ? note(oneSnapshotText(series.length), false) : ''}
                ${hasSeries ? `
                    <div class="grid grid-2" style="margin-top:0.5rem;">
                        <div>
                            ${buildChartContainer('chart-pgb-throughput', 'Request Throughput', { tooltip: 'Transactions and queries per second. Each point is one stats-line average over the elapsed stats_period, not an instantaneous rate.', showBucketControl: false })}
                            ${chartLegend([['transactions/s', 'var(--accent)'], ['queries/s', 'var(--purple)']])}
                        </div>
                        <div>
                            ${buildChartContainer('chart-pgb-io', 'Network I/O', { tooltip: 'Bytes received and sent per second, averaged over each stats_period snapshot.', showBucketControl: false })}
                            ${chartLegend([['in', 'var(--accent)'], ['out', 'var(--success)']])}
                        </div>
                    </div>
                    <div style="margin-top:0.5rem;">
                        ${buildChartContainer('chart-pgb-timing', 'Transaction / Query / Pool Wait', { tooltip: 'Transaction and query time measure work on the server; pool wait is how long clients queued for a server connection — a pool sizing signal, not a query one. pgBouncer logs one average per snapshot, never a single operation, so each point is an interval average. What pool wait is averaged over depends on pool_mode (one wait per session, per transaction or per statement), which pgBouncer logs nowhere.', showBucketControl: false })}
                        ${chartLegend([['transaction', 'var(--accent)'], ['query', 'var(--purple)'], ['pool wait', 'var(--danger)']])}
                    </div>
                ` : ''}
                <div class="subsection">
                        <div class="grid grid-2">
                            <div><div class="subsection-title" style="margin-top:0">Sessions by</div>${hasSessions ? dimTabs([
                                { label: 'Database', map: p.sessions_per_database },
                                { label: 'User', map: p.sessions_per_user },
                                { label: 'Host', map: p.sessions_per_host },
                            ], p.sessions) : emptyBlock(SESSIONS_HINT, missingClients ? SESSIONS_CONTRADICTION : '')}</div>
                            <div><div class="subsection-title" style="margin-top:0">Connections by</div>${hasConns ? dimTabs([
                                { label: 'Database', map: p.connections_per_database },
                                { label: 'User', map: p.connections_per_user },
                                { label: 'Host', map: p.connections_per_host },
                            ], p.login_attempts) : emptyBlock(CONNECTIONS_HINT, missingClients ? CONNECTIONS_CONTRADICTION : '')}</div>
                        </div>
                    </div>
                ${closing.length > 0 ? `
                    <div class="subsection">
                        ${closing.length > 1
                            ? `<div class="grid grid-2">${closing.map(b => `<div>${b}</div>`).join('')}</div>`
                            : closing[0]}
                    </div>
                ` : ''}
            </div>
        </div>
    `;
}
