// pgBackRest section. Three rows, in the report's grammar:
//   1. one card: the settings that hid information (amber banner), the
//      period, and tabs of cards per stanza, per repository and of the
//      backups still present;
//   2. the checks per stanza beside the operations, errors and warnings,
//      laid out as the PostgreSQL Events card (a click opens Event Detail);
//   3. the backups' durations and sizes beside the WAL, pushed (Archiving)
//      and restored (Restore, when archive-get is logged), each chart with
//      the table of what went wrong under it.
// The short labels (an error's cause, an operation's detail) come from the
// JSON, worded once in Go for the text report and this one alike.

import { fmt, esc, escAttr, buildNoDataMessage, wholeLogBadge } from '../utils.js';
import { chartData, buildChartContainer, createTimeChart } from '../charts.js';

// wallDate reads pgBackRest's zoneless wall clock as UTC milliseconds, so
// that arithmetic and formatting never depend on the viewer's zone.
export function wallDate(ts) {
    return ts ? Date.parse(ts.replace(' ', 'T') + 'Z') : NaN;
}

const pad = (n, w = 2) => String(n).padStart(w, '0');
const code3 = c => pad(c, 3);
const plural = (n, w) => `${fmt(n)} ${w}${n === 1 ? '' : 's'}`;
const UNNAMED_STANZA = '(from PostgreSQL log)';
const stanzaName = s => s.stanza || UNNAMED_STANZA;
const hms = t => (isNaN(t) ? '?' : new Date(t).toISOString().slice(11, 19));
const wallString = t => new Date(t).toISOString().slice(0, 19).replace('T', ' ');

function dur(sec) {
    const s = Math.round(sec);
    if (s < 60) return `${s}s`;
    if (s < 3600) return `${Math.floor(s / 60)}m${pad(s % 60)}s`;
    return `${Math.floor(s / 3600)}h${pad(Math.floor((s % 3600) / 60))}m`;
}
const runDur = ms => (ms < 1000 ? `${ms}ms` : dur(ms / 1000));
const size = s => (s || '').replace(/^([\d.]+)([KMGTP]?B)$/, '$1 $2');

// pgBackRest's error names, from its build/error.yaml.
const NAMES = { 26: 'ChecksumError', 27: 'ConfigError', 28: 'FileInvalidError', 29: 'FormatError', 30: 'CommandRequiredError', 31: 'OptionInvalidError', 32: 'OptionInvalidValueError', 37: 'OptionRequiredError', 38: 'PgRunningError', 39: 'ProtocolError', 40: 'PathNotEmptyError', 41: 'FileOpenError', 42: 'FileReadError', 43: 'ParamRequiredError', 44: 'ArchiveMismatchError', 45: 'ArchiveDuplicateError', 46: 'VersionNotSupportedError', 47: 'PathCreateError', 48: 'CommandInvalidError', 49: 'HostConnectError', 50: 'LockAcquireError', 51: 'BackupMismatchError', 52: 'FileSyncError', 53: 'PathOpenError', 54: 'PathSyncError', 55: 'FileMissingError', 56: 'DbConnectError', 57: 'DbQueryError', 58: 'DbMismatchError', 60: 'FileRemoveError', 61: 'PathRemoveError', 62: 'StopError', 63: 'TermError', 64: 'FileWriteError', 67: 'FeatureNotSupportedError', 68: 'ArchiveCommandInvalidError', 69: 'LinkExpectedError', 70: 'LinkDestinationError', 72: 'HostInvalidError', 73: 'PathMissingError', 74: 'FileMoveError', 75: 'BackupSetInvalidError', 76: 'TablespaceMapError', 78: 'LinkMapError', 79: 'FileCloseError', 80: 'DbMissingError', 81: 'DbInvalidError', 82: 'ArchiveTimeoutError', 83: 'FileModeError', 87: 'ArchiveDisabledError', 88: 'FileOwnerError', 89: 'UserMissingError', 93: 'FileExistsError', 95: 'CryptoError', 96: 'ParamInvalidError', 97: 'PathCloseError', 98: 'FileInfoError', 99: 'JsonFormatError', 100: 'KernelError', 101: 'ServiceError', 102: 'ExecuteError', 103: 'RepoInvalidError', 104: 'CommandError', 105: 'AccessError', 106: 'ClockError', 122: 'RuntimeError', 124: 'UnhandledError', 125: 'UnknownError' };

const OUTCOMES = { recovered: 'recovered', still_failing: 'still failing', no_later_push: 'pushed elsewhere', unknown: 'outcome unknown' };

const backupsLogged = s => !!(s.backups_logged || (s.backups || []).length);
const pushesLogged = s => !!(s.archive_push?.logged || s.archive_push?.async_logged);
const getsLogged = s => !!(s.archive_get?.logged || s.archive_get?.async_logged);
const restored = s => (s.archive_get?.found || 0) + (s.archive_get?.found_async || 0);
// hasRestore: archive-get logged something worth its own tab.
const hasRestore = s => getsLogged(s) && !!(restored(s) || (s.archive_get?.failures || []).length || (s.missing_wal || []).length);
const missingSegments = s => new Set((s.missing_wal || []).map(w => w.segment)).size;
// pgUnlogged: PostgreSQL counted archive_command failures pgBackRest did
// not log as runs (their begin was not in the log); pgFailingAtEnd: the
// last of them came after the last push.
const pgUnlogged = s => (s.pg_archive_failures || []).length > (s.archive_push?.failed || 0);
function pgFailingAtEnd(s) {
    if (!pgUnlogged(s)) return false;
    const last = Math.max(...(s.pg_archive_failures || []).map(f => wallDate(f.date)).filter(t => !isNaN(t)));
    const push = wallDate(s.archive_push?.last_push?.date);
    return isFinite(last) && (isNaN(push) || last > push);
}
const base = p => (p || '').replace(/\/+$/, '').split('/').pop() || p || '';
const cutWindow = s => !!(s.window && (s.window.cut_before || s.window.cut_after));
// A note of missing_info other than log-timestamp=n hides run outcomes.
const hidesOutcomes = s => (s.missing_info || []).some(n => !n.startsWith('log-timestamp=n'));
const multiRepo = s => new Set((s.backups || []).map(b => b.repo).filter(Boolean)).size > 1;

// ---- what happened, per stanza -----------------------------------------------

// events lists a stanza's dated facts, as the text report's timeline: t is
// NaN for a run of a log-timestamp=n file, kept for the lists only.
function events(s) {
    const st = stanzaName(s), ev = [];
    const add = e => ev.push({ stanza: st, src: s, ...e });
    for (const b of s.backups || []) {
        const kind = b.succeeded || b.in_progress ? 'ok' : b.crashed ? 'killed' : 'failed';
        const result = b.succeeded ? 'ok' : b.in_progress ? 'in progress' : b.crashed ? 'killed' : b.error_code ? `failed [${code3(b.error_code)}]` : 'failed';
        const detail = b.succeeded ? b.label : b.crashed
            ? [b.signal ? `terminated by ${b.signal}` : 'no end line', b.last_line_date ? `last line at ${b.last_line_date.slice(11)}` : ''].filter(Boolean).join(', ')
            : b.error_label || '';
        add({ cat: 'backup', kind, b, t: wallDate(b.start_date), end: wallDate(b.end_date || b.last_line_date), event: 'backup ' + b.type, result, detail, du: b.duration_ms });
    }
    for (const f of s.archive_push?.failures || []) {
        add({ cat: 'outage', kind: f.recovery === 'still_failing' ? 'open' : 'outage', f, t: wallDate(f.first_date), end: wallDate(f.last_date), event: 'archiving outage',
            result: OUTCOMES[f.recovery] || f.recovery, detail: `${f.segment}, ${plural(f.attempts, 'attempt')}, ${f.code ? `[${code3(f.code)}] ` : ''}${f.cause || f.first_line || ''}` });
    }
    for (const f of s.archive_get?.failures || []) {
        add({ cat: 'getout', kind: f.recovery === 'still_failing' ? 'open' : 'outage', f, t: wallDate(f.first_date), end: wallDate(f.last_date), event: 'WAL restore outage',
            result: OUTCOMES[f.recovery] || f.recovery, detail: `${f.segment || '?'}, ${plural(f.attempts, 'attempt')}, ${f.code ? `[${code3(f.code)}] ` : ''}${f.cause || f.first_line || ''}${f.path ? ', ' + f.path : ''}` });
    }
    for (const w of s.missing_wal || []) {
        add({ cat: 'missing', kind: 'lost', w, t: wallDate(w.date), event: 'missing WAL', result: 'not found',
            detail: `${w.segment}, pushed at ${(w.pushed_date || '').slice(11)}${w.path ? ', ' + w.path : ''}` });
    }
    for (const d of s.archive_push?.dropped_wal || []) {
        add({ cat: 'lost', kind: 'lost', t: wallDate(d.date), event: 'WAL lost', result: 'dropped', detail: `${d.segment}, archive queue full`, segment: d.segment });
    }
    for (const o of s.operations || []) {
        const result = o.succeeded ? 'ok' : !o.has_outcome ? 'no outcome' : o.error_code ? `failed [${code3(o.error_code)}]` : 'failed';
        add({ cat: 'op', kind: o.succeeded || !o.has_outcome ? 'ok' : 'failed', t: wallDate(o.date), event: o.command, result, detail: o.detail || '', du: o.duration_ms, recovery: o.recovery });
    }
    for (const r of s.expire_runs || []) {
        const removed = [(r.expired || []).join(', '), r.archive_removed ? plural(r.archive_removed, 'WAL range') : ''].filter(Boolean).join('; ');
        if (r.succeeded && !removed) continue;
        const repo = r.repo === 'all' ? 'all repositories' : r.repo || '';
        add({ cat: 'op', kind: r.succeeded ? 'ok' : 'failed', t: wallDate(r.date), event: 'expire', result: r.succeeded ? 'ok' : (r.error_code ? `failed [${code3(r.error_code)}]` : 'failed'),
            detail: removed ? `removed ${removed}${repo ? ' on ' + repo : ''}` : repo ? 'on ' + repo : '' });
    }
    return ev;
}
const tipOf = e => `${hms(e.t)} · ${e.stanza} · ${e.event} · ${e.result}${e.du ? ' · ' + runDur(e.du) : ''}${e.detail ? ' · ' + e.detail : ''}`;

// ---- row 1: banner, header, cards ----------------------------------------------

// missingBanner names the pgBackRest settings that hid part of the activity,
// in the amber banner Connections uses for client I/O failures.
function missingBanner(pbr) {
    const hints = new Map();
    for (const s of pbr) for (const n of s.missing_info || []) {
        const [setting, why] = n.startsWith('log-timestamp=n')
            ? ['log-timestamp = y', 'a file was written without timestamps: its runs have no date nor duration']
            : ['log-level-file = info', 'command begin/end lines are missing: whether runs succeeded cannot be told. A synchronous archive-push or archive-get writes its own log file only with --log-level-file on its command line or in its environment, not from pgbackrest.conf'];
        if (!hints.has(setting)) hints.set(setting, { why, stanzas: [] });
        hints.get(setting).stanzas.push(stanzaName(s));
    }
    if (!hints.size) return '';
    const tip = [...hints].map(([st, h]) => `${st}: ${h.why}${pbr.length > 1 ? ` (${h.stanzas.join(', ')})` : ''}`).join('\n');
    return `<div class="cio pgbr-missing"><div class="cio-banner" title="${escAttr(tip)}"><span class="cio-lead">⚠ Missing information</span><span class="cio-inline">check ${[...hints.keys()].map(k => `<code>${esc(k)}</code>`).join(' <span class="cio-dot">·</span> ')} in pgbackrest.conf</span></div></div>`;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const day = d => `${+d.slice(8, 10)} ${MONTHS[+d.slice(5, 7) - 1]} ${d.slice(0, 4)}`;

// header is the period in bold and, on its line, the version and what was
// read. A --begin/--end window is stated only in a pgBackRest-only report:
// otherwise the Summary card shows the period analyzed.
function header(pbr, pgbrOnly) {
    const starts = pbr.map(s => s.start_date).filter(Boolean).sort(), ends = pbr.map(s => s.end_date).filter(Boolean).sort();
    const from = starts[0], to = ends[ends.length - 1];
    const period = !from ? 'Undated' : from.slice(0, 10) === to.slice(0, 10)
        ? `${day(from)} ${from.slice(11, 19)} → ${to.slice(11, 19)}`
        : `${day(from)} ${from.slice(11, 19)} → ${day(to)} ${to.slice(11, 19)}`;
    const versions = [...new Set(pbr.flatMap(s => s.versions || []))].sort();
    const files = [...new Set(pbr.flatMap(s => s.files || []))];
    const lines = pbr.reduce((n, s) => n + (s.lines_read || 0), 0);
    const parts = [
        versions.length ? `pgBackRest ${esc(versions.join(', '))}` : '',
        // The browser demo reads one input whose name quellog does not know.
        `<b>${fmt(lines)}</b> lines read${files.length ? ` in <span class="pgbr-files" title="${escAttr(files.join('\n'))}">${plural(files.length, 'file')}</span>` : ''}`,
    ];
    const w = pbr.find(s => s.window)?.window;
    if (w && pgbrOnly) {
        const range = !w.begin ? `until ${esc(w.end)}` : !w.end ? `from ${esc(w.begin)}` : `${esc(w.begin)} → ${esc(w.end)}`;
        const cut = w.cut_before && w.cut_after ? ' (lines before and after it left out)' : w.cut_before ? ' (lines before it left out)' : w.cut_after ? ' (lines after it left out)' : '';
        parts.push(`Window ${range}${cut}`);
    }
    return `<div class="summary-header pgbr-head"><div class="summary-date">${period}</div><div class="pgbr-meta">${parts.filter(Boolean).join(' · ')}</div></div>`;
}

// frame is the period every gauge and chart shares, with a 1% margin so a
// mark on the first or last second stays whole.
function frame(pbr) {
    const ts = pbr.flatMap(s => [wallDate(s.start_date), wallDate(s.end_date)]).filter(t => !isNaN(t));
    if (!ts.length) return null;
    const a = Math.min(...ts), b = Math.max(...ts), m = Math.max(1000, b - a) / 100;
    const t0 = a - m, t1 = b + m;
    return { t0, t1, x: t => Math.max(0, Math.min(100, ((t - t0) / (t1 - t0)) * 100)) };
}

// gauge is a stanza's activity over the period: each event fills it for as
// long as it lasted, outages under the backups, instants on top.
function gauge(f, ev) {
    if (!f) return '';
    const order = { outage: 0, open: 0, ok: 1, failed: 1, killed: 1, lost: 2 };
    const marks = ev.filter(e => !isNaN(e.t) && e.cat !== 'op').sort((a, b) => order[a.kind] - order[b.kind]).map(e => {
        const l = f.x(e.t), w = isNaN(e.end) ? 0 : Math.max(f.x(e.end) - l, 0);
        return `<span class="pgbr-seg is-${e.kind}" style="left:${l}%;width:${w}%" title="${escAttr(tipOf(e))}"></span>`;
    }).join('');
    return `<div class="pgbr-gauge">${marks}</div>`;
}

// outageFact words how the outages ended, as the text report's line.
function outageFact(outages) {
    const n = outages.length;
    if (!n) return '<span><b>0</b> outages</span>';
    const spans = outages.map(e => (e.end - e.t) / 1000).filter(x => !isNaN(x));
    const words = [...new Set(outages.map(e => e.result))];
    const cls = outages.some(e => e.kind === 'open') ? 'bad' : 'warn';
    if (n === 1) return `<span class="${cls}"><b>1</b> outage${spans.length ? ` (${dur(spans[0])})` : ''}, ${esc(words[0])}</span>`;
    const state = words.length === 1 ? `all ${words[0]}` : words.map(w => `${outages.filter(e => e.result === w).length} ${w}`).join(', ');
    return `<span class="${cls}"><b>${n}</b> outages, ${esc(state)}${spans.length ? `, longest ${dur(Math.max(...spans))}` : ''}</span>`;
}

function stanzaCards(pbr, f) {
    const endT = Math.max(...pbr.map(s => wallDate(s.end_date)).filter(t => !isNaN(t)));
    return `<div class="pgbr-cards">${pbr.map(s => {
        const ev = events(s);
        const bs = ev.filter(e => e.cat === 'backup');
        // Killed runs are named as such, as the text report and the failed
        // backups table name them, not counted as failures.
        const ok = bs.filter(e => e.b.succeeded).length, failed = bs.filter(e => e.kind === 'failed').length,
            killed = bs.filter(e => e.kind === 'killed').length;
        const outages = ev.filter(e => e.cat === 'outage'), lost = ev.filter(e => e.cat === 'lost').length;
        const logged = backupsLogged(s);
        const missing = missingSegments(s);
        // A failure is recovered only when a later backup succeeded.
        const done = bs.filter(e => !e.b.in_progress && !isNaN(e.t)).sort((a, b) => a.t - b.t);
        const lastFailed = done.length > 0 && !done[done.length - 1].b.succeeded;
        const state = outages.some(e => e.kind === 'open') || lost || missing || lastFailed || pgFailingAtEnd(s) ? 'bad'
            : failed || killed || outages.length || pgUnlogged(s) ? 'warn'
            : hidesOutcomes(s) ? 'partial' : ok ? 'good' : logged ? 'none' : 'unlogged';
        const word = { bad: 'Needs attention', warn: 'Recovered from failures', good: 'Healthy', partial: 'Some outcomes not logged', none: 'No backup in the log', unlogged: 'Backups not logged' }[state];
        const lf = s.last_full;
        const sub = lf ? `since the last full, ${esc(lf.label)}${multiRepo(s) && lf.repo ? ' on ' + esc(lf.repo) : ''}, ended`
            : !logged ? 'backups are not in this log' : cutWindow(s) ? 'no full backup in this window' : 'no full backup in the log';
        const ap = s.archive_push || {};
        return `<div class="pgbr-card is-${state}">
            <div class="pgbr-card-head"><span class="pgbr-card-name">${esc(stanzaName(s))}</span><span class="pgbr-card-state">${word}</span></div>
            <div class="pgbr-card-big">${lf && isFinite(endT) ? dur((endT - wallDate(lf.end_date)) / 1000) : '–'}<small>${sub}</small></div>
            ${gauge(f, ev)}
            <div class="pgbr-card-facts">${logged ? `<span><b>${ok}</b> ok</span><span class="${failed ? 'bad' : ''}"><b>${failed}</b> failed</span>${killed ? `<span class="warn"><b>${killed}</b> killed</span>` : ''}` : '<span>backups not logged</span>'}${pushesLogged(s) ? `<span><b>${fmt((ap.pushed || 0) + (ap.pushed_async || 0))}</b> WAL pushed</span>` : '<span>WAL pushes not logged</span>'}${outageFact(outages)}${pgUnlogged(s) ? `<span class="${pgFailingAtEnd(s) ? 'bad' : 'warn'}">PostgreSQL: <b>${fmt(s.pg_archive_failures.length)}</b> archive_command failures</span>` : ''}${lost ? `<span class="bad"><b>${lost}</b> WAL lost</span>` : ''}${hasRestore(s) ? `<span><b>${fmt(restored(s))}</b> WAL restored</span>` : ''}${missing ? `<span class="bad"><b>${missing}</b> WAL missing</span>` : ''}</div>
        </div>`;
    }).join('')}</div>`;
}

function repoCards(pbr) {
    const repos = new Map();
    const get = r => { if (!repos.has(r)) repos.set(r, { ok: 0, failed: 0, killed: 0, last: null, errors: 0, causes: new Set() }); return repos.get(r); };
    for (const s of pbr) {
        for (const b of s.backups || []) {
            if (!b.repo) continue;
            const r = get(b.repo);
            if (b.succeeded) { r.ok++; if (!r.last || b.end_date > r.last.end_date) r.last = b; } else if (b.crashed) r.killed++; else if (!b.in_progress) r.failed++;
        }
        for (const e of s.errors || []) {
            const m = (e.repo_line || '').match(/^(repo\d+):/);
            if (m) { const r = get(m[1]); r.errors += e.count; r.causes.add(e.label); }
        }
    }
    if (!repos.size) return '<div class="pgbr-empty">No repository named in the log.</div>';
    return `<div class="pgbr-cards">${[...repos].sort().map(([n, r]) => `<div class="pgbr-card ${r.errors || r.failed || r.killed ? 'is-warn' : ''}">
        <div class="pgbr-card-head"><span class="pgbr-card-name">${esc(n)}</span><span class="pgbr-card-state">${r.errors ? 'errors logged' : 'no error'}</span></div>
        <div class="pgbr-card-big pgbr-card-label">${r.last ? esc(r.last.label) : '–'}<small>latest successful backup on it</small></div>
        <div class="pgbr-card-facts"><span><b>${r.ok}</b> ok</span><span class="${r.failed ? 'bad' : ''}"><b>${r.failed}</b> failed</span>${r.killed ? `<span class="warn"><b>${r.killed}</b> killed</span>` : ''}<span class="${r.errors ? 'bad' : ''}"><b>${r.errors}</b> errors naming it</span></div>
        ${r.causes.size ? `<div class="pgbr-card-note">${esc([...r.causes].join('; '))}</div>` : ''}
    </div>`).join('')}</div>`;
}

// backupCards shows, per stanza, the backups still present as chains (each
// diff and incr after the full it depends on), the expired ones struck.
function backupCards(pbr) {
    const parent = l => (l || '').split('_')[0];
    const cards = pbr.map(s => {
        const ok = (s.backups || []).filter(b => b.succeeded && b.label);
        const why = new Map();
        for (const r of s.expire_runs || []) for (const l of r.expired || []) why.set(l, `expired at ${(r.date || '').slice(11)}${r.set_label ? ' (asked for)' : ' (retention)'}`);
        const kept = ok.filter(b => !why.has(b.label)), gone = ok.filter(b => why.has(b.label));
        const fulls = ok.filter(b => b.type === 'full');
        const showRepo = multiRepo(s);
        const chip = b => `<span class="pgbr-chip${why.has(b.label) ? ' gone' : ''}" title="${escAttr(`${b.label} · ${b.type}${b.repo ? ' · ' + b.repo : ''} · ${size(b.size)} · ended ${(b.end_date || '').slice(11)} · ${why.get(b.label) || 'kept'}`)}"><i class="t-${b.type}"></i>${esc((b.end_date || '').slice(11, 16))}${showRepo && b.repo ? ` <small>${esc(b.repo)}</small>` : ''}<small>${esc(size(b.size))}</small></span>`;
        const chains = fulls.map(fb => [fb, ...ok.filter(b => b.type !== 'full' && parent(b.label) === fb.label)]);
        const orphans = ok.filter(b => b.type !== 'full' && !fulls.some(f => f.label === parent(b.label)));
        if (orphans.length) chains.push(orphans);
        const keptChains = fulls.filter(f => !why.has(f.label)).length;
        const reasons = [...new Set(gone.map(b => why.get(b.label).replace(/^expired at \S+ /, '')))];
        // An expire run given a retention still removes WAL.
        const noWalExpiry = (s.retention_warnings || []).some(w => /retention-archive/.test(w.text)) && !(s.expire_runs || []).some(r => r.archive_removed);
        return `<div class="pgbr-card">
            <div class="pgbr-card-head"><span class="pgbr-card-name">${esc(stanzaName(s))}</span>${gone.length ? `<span class="pgbr-card-state warn">${gone.length} expired</span>` : `<span class="pgbr-card-state">${ok.length ? 'all kept' : 'none'}</span>`}</div>
            <div class="pgbr-card-big">${kept.length}<small>${kept.length === 1 ? 'backup' : 'backups'} still present${fulls.length ? `, in ${plural(keptChains, 'chain')}` : ''}</small></div>
            <div class="pgbr-chains">${chains.length ? chains.map(c => `<div class="pgbr-chain">${c.map(chip).join('<span class="pgbr-link"></span>')}</div>`).join('')
                : `<div class="pgbr-empty">${backupsLogged(s) ? 'No successful backup in the log.' : 'Backups are not in this log.'}</div>`}</div>
            <div class="pgbr-card-facts"><span><b>${kept.length}</b> kept</span>${gone.length ? `<span class="warn"><b>${gone.length}</b> expired${reasons.length ? ' ' + esc(reasons.join(', ')) : ''}</span>` : ''}${noWalExpiry ? '<span class="warn">WAL never expired</span>' : ''}</div>
        </div>`;
    }).join('');
    return `<div class="pgbr-cards">${cards}</div>
        <div class="chart-legend pgbr-legend"><span><i class="pgbr-key t-full"></i>Full</span><span><i class="pgbr-key t-diff"></i>Diff</span><span><i class="pgbr-key t-incr"></i>Incr</span><span><s>struck</s>&nbsp;expired since</span></div>`;
}

// ---- row 2: checks, events --------------------------------------------------------

function checksTable(pbr, pgbrOnly) {
    // null: this log cannot tell (backups, pushes or gets not logged here,
    // no PostgreSQL log read).
    const restores = s => (s.operations || []).filter(o => o.command === 'restore' && o.path);
    const checks = [
        ['A full backup is still present', s => (backupsLogged(s) ? !!s.last_full : null)],
        ['No backup failed', s => (backupsLogged(s) ? !(s.backups || []).some(b => !b.succeeded && !b.in_progress) : null)],
        ['Archiving never stopped', s => (pushesLogged(s) ? !((s.archive_push?.failures || []).length || pgUnlogged(s)) : null)],
        ['Archiving works at the end of the log', s => (pushesLogged(s) ? !((s.archive_push?.failures || []).some(x => x.recovery === 'still_failing') || pgFailingAtEnd(s)) : null)],
        ['No WAL dropped', s => (pushesLogged(s) ? !(s.archive_push?.dropped_wal || []).length : null)],
        ['PostgreSQL never gave up archiving', s => (pgbrOnly ? null : !s.pg_retry_warnings)],
        ['No WAL missing from the repository', s => (getsLogged(s) ? !(s.missing_wal || []).length : null)],
        ['Every recovery got its WAL', s => (getsLogged(s) && restores(s).length ? !restores(s).some(o => o.recovery) : null)],
        ['No retention warning', s => !(s.retention_warnings || []).length],
        ['Run outcomes in the log', s => !hidesOutcomes(s)],
        ['Timestamps in the log', s => !(s.missing_info || []).some(n => n.startsWith('log-timestamp=n'))],
    ];
    const mark = v => (v === null ? '<span class="pgbr-na" title="not in this log">–</span>' : v ? '<span class="pgbr-yes">✓</span>' : '<span class="pgbr-no">✗</span>');
    return `<div class="table-container" style="max-height: none;"><table>
        <thead><tr><th>Check</th>${pbr.map(s => `<th class="pgbr-center">${esc(stanzaName(s))}</th>`).join('')}</tr></thead>
        <tbody>${checks.map(([l, fn]) => `<tr><td>${esc(l)}</td>${pbr.map(s => `<td class="pgbr-center">${mark(fn(s))}</td>`).join('')}</tr>`).join('')}</tbody>
    </table></div><div class="pgbr-note">Facts from the log, a yes or a no per stanza; – when the log cannot tell.</div>`;
}

// The errors and warnings the Event Detail modal opens, by index.
const ISSUES = [];

function issueRows(pbr, key) {
    const items = pbr.flatMap(s => (s[key] || []).map(e => ({ s, e })));
    const sev = key === 'errors' ? 'ERROR' : 'WARNING';
    if (!items.length) return `<div class="pgbr-none">No ${sev} events recorded</div>`;
    const color = key === 'errors' ? 'var(--danger)' : 'var(--warning)';
    const groups = new Map();
    for (const it of items) {
        const k = key === 'errors' ? it.e.code || 0 : 0;
        if (!groups.has(k)) groups.set(k, []);
        groups.get(k).push(it);
    }
    const sum = k => groups.get(k).reduce((n, x) => n + x.e.count, 0);
    const several = pbr.length > 1;
    let rows = '';
    for (const k of [...groups.keys()].sort((a, b) => sum(b) - sum(a))) {
        groups.get(k).sort((a, b) => b.e.count - a.e.count).forEach((it, i) => {
            ISSUES.push({ e: it.e, sev, stanza: stanzaName(it.s) });
            rows += `<tr onclick="showPgBackRestIssue(${ISSUES.length - 1})" style="cursor: pointer;" title="Click for details">
                <td style="width: 50px; vertical-align: top; padding: 0.25rem 0.5rem;">${k && i === 0 ? `<span style="color: ${color};">${code3(k)}</span>` : ''}</td>
                <td style="vertical-align: top; padding: 0.25rem 0.5rem;">${k && i === 0 && NAMES[k] ? `<div class="pgbr-codename">${NAMES[k]}</div>` : ''}<div>${esc(it.e.label || it.e.message)}${several ? ` <span class="pgbr-muted">· ${esc(stanzaName(it.s))}</span>` : ''}</div></td>
                <td class="num" style="width: 60px; vertical-align: top; padding: 0.25rem 0.5rem; font-weight: 600;">${fmt(it.e.count)}</td>
            </tr>`;
        });
    }
    return `<div class="table-container" style="max-height: 320px;"><table class="data-table" style="width: 100%;">${rows}</table></div>`;
}

function operationRows(pbr) {
    const several = pbr.length > 1;
    const ops = pbr.flatMap(s => events(s).filter(e => e.cat === 'op')).sort((a, b) => isNaN(a.t) - isNaN(b.t) || a.t - b.t);
    if (!ops.length) return '<div class="pgbr-none">No other operation recorded</div>';
    return `<div class="table-container" style="max-height: 320px;"><table class="data-table" style="width: 100%;">${ops.map(e => `<tr>
        <td class="pgbr-op-time">${hms(e.t)}</td>
        <td style="vertical-align: top; padding: 0.25rem 0.5rem;"><div>${esc(e.event)}${several ? ` <span class="pgbr-muted">· ${esc(e.stanza)}</span>` : ''}</div>${e.detail ? `<div class="pgbr-op-detail">${esc(e.detail)}</div>` : ''}${e.recovery ? `<div class="pgbr-op-detail bad">recovery: ${esc(e.recovery)}</div>` : ''}</td>
        <td class="num pgbr-op-dur">${e.du ? runDur(e.du) : ''}</td>
        <td class="pgbr-op-result${e.kind === 'failed' ? ' bad' : ''}">${esc(e.result)}</td>
    </tr>`).join('')}</table></div>`;
}

// eventsCard lays the operations, errors and warnings out as the PostgreSQL
// Events card. OPERATIONS opens first: the errors are also read below the
// charts (failed backups, outages).
function eventsCard(pbr) {
    ISSUES.length = 0;
    const count = key => pbr.reduce((n, s) => n + (s[key] || []).reduce((m, e) => m + e.count, 0), 0);
    const nOps = pbr.reduce((n, s) => n + events(s).filter(e => e.cat === 'op').length, 0);
    const nErr = count('errors'), nWarn = count('warnings');
    const sel = nOps ? 0 : nErr ? 1 : nWarn ? 2 : 0;
    return `<ql-tabs>
        <ql-tab${sel === 0 ? ' selected' : ''}>OPERATIONS<ql-badge>${fmt(nOps)}</ql-badge></ql-tab>
        <ql-tab class="error"${sel === 1 ? ' selected' : ''}>ERROR<ql-badge>${fmt(nErr)}</ql-badge></ql-tab>
        <ql-tab class="warning"${sel === 2 ? ' selected' : ''}>WARNING<ql-badge>${fmt(nWarn)}</ql-badge></ql-tab>
        <ql-panel>${operationRows(pbr)}</ql-panel>
        <ql-panel>${issueRows(pbr, 'errors')}</ql-panel>
        <ql-panel>${issueRows(pbr, 'warnings')}</ql-panel>
    </ql-tabs>`;
}

// showPgBackRestIssue opens the Event Detail modal on a pgBackRest error or
// warning: the HTML counterpart of --event-detail. Its ID is the CLI's
// handle and is not shown here.
export function showPgBackRestIssue(i) {
    const it = ISSUES[i];
    if (!it) return;
    const { e, sev, stanza } = it;
    const color = sev === 'ERROR' ? 'var(--danger)' : 'var(--warning)';
    const dates = (e.dates || []).map(wallDate).filter(t => !isNaN(t)).sort((a, b) => a - b);
    const freq = dates.length > 1 ? (dates.length / Math.max(1, (dates[dates.length - 1] - dates[0]) / 60000)).toFixed(2) + ' /min' : '-';
    const under = (e.detail || []).filter(l => l !== e.repo_line);
    const pattern = e.message + (under[0] ? ' ' + under[0].replace(/'\/[^' ]*\/([^/' ]+\/[^/' ]+)'/g, "'.../$1'") : '');
    const example = [e.example || e.message, ...under].join('\n');
    const copy = text => `<button class="copy-btn-inline" onclick="navigator.clipboard.writeText(${escAttr(JSON.stringify(text))});this.textContent='Copied!';setTimeout(()=>this.textContent='Copy',1500)">Copy</button>`;
    document.getElementById('eventModalBody').innerHTML = `
        <div style="margin-bottom: 1rem;">
            <div style="display: flex; align-items: center; margin-bottom: 0.5rem;">
                ${e.code ? `<span style="border: 1px solid ${color}; color: ${color}; border-radius: 3px; padding: 0 4px; margin-right: 0.5rem;">${code3(e.code)}</span>` : ''}
                <span style="font-weight: 600; color: ${color};">${sev}</span>
                ${e.code && NAMES[e.code] ? `<span style="color: var(--text-muted); margin-left: 0.5rem;">${NAMES[e.code]}</span>` : ''}
            </div>
            <div style="color: var(--text-muted); font-size: 0.8rem;">pgBackRest · stanza ${esc(stanza)}${(e.commands || []).length ? ' · ' + esc(e.commands.join(', ')) : ''}</div>
        </div>
        <div class="qd-chart-container" style="margin-bottom: 1rem;">
            <div class="qd-chart-header"><span class="qd-chart-title">Occurrences Over Time</span></div>
            <div id="eventModalChart" style="height: 180px;"></div>
        </div>
        <div class="detail-stats" style="margin-bottom: 1rem;">
            <div class="detail-stat"><div class="value">${fmt(e.count)}</div><div class="label">Occurrences</div></div>
            <div class="detail-stat"><div class="value">${esc(e.first_date || '-')}</div><div class="label">First seen</div></div>
            <div class="detail-stat"><div class="value">${esc(e.last_date || '-')}</div><div class="label">Last seen</div></div>
            <div class="detail-stat"><div class="value">${freq}</div><div class="label">Frequency</div></div>
        </div>
        <div class="qd-section-title" style="display: flex; justify-content: space-between; align-items: center;">Normalized Pattern${copy(pattern)}</div>
        <div class="query-detail-sql" style="margin-bottom: 0.75rem;">${esc(pattern)}</div>
        <div class="qd-section-title" style="display: flex; justify-content: space-between; align-items: center;">Example (raw message)${copy(example)}</div>
        <div class="query-detail-sql" style="margin-bottom: 0.75rem; white-space: pre-wrap;">${esc(example)}</div>`;
    document.getElementById('eventModal').open();
    const resolved = getComputedStyle(document.documentElement).getPropertyValue(sev === 'ERROR' ? '--danger' : '--warning').trim();
    requestAnimationFrame(() => createTimeChart('eventModalChart', dates, { color: resolved, height: 180, utc: true }));
}
if (typeof window !== 'undefined') window.showPgBackRestIssue = showPgBackRestIssue;

// ---- row 3: backups and WAL charts with their tables --------------------------------

const legendItem = (cls, label) => `<span><i class="pgbr-key ${cls}"></i>${label}</span>`;

function backupsCard(pbr, f) {
    // A backup still running at the --end bound has neither a duration nor
    // an outcome to draw.
    const bs = pbr.flatMap(s => events(s).filter(e => e.cat === 'backup' && !isNaN(e.t) && !e.b.in_progress));
    if (!bs.length) return pbr.some(backupsLogged) ? '<div class="pgbr-none">No backup in the log</div>' : buildNoDataMessage('the pgBackRest log files, where backups are logged');
    const hasSize = bs.some(e => e.b.succeeded && e.b.size_bytes);
    const legend = legendItem('t-full', 'Duration') + (hasSize ? legendItem('is-size', 'Size (right scale)') : '')
        + (bs.some(e => e.kind === 'failed') ? legendItem('is-failband', 'Failed') : '') + (bs.some(e => e.kind === 'killed') ? legendItem('is-killband', 'Killed') : '');
    chartData.set('chart-pgbr-backups', {
        type: 'pgbr-backups', legend, t0: wallString(f.t0), t1: wallString(f.t1),
        backups: bs.map(e => ({ start: e.b.start_date, end: e.b.end_date || e.b.last_line_date, durMs: e.b.duration_ms || 0, size: e.b.succeeded ? e.b.size_bytes || 0 : 0, kind: e.kind,
            tip: tipOf(e) + (e.b.succeeded && e.b.size ? ' · ' + size(e.b.size) : '') })),
    });
    const several = pbr.length > 1, repos = pbr.some(multiRepo);
    const bad = bs.filter(e => e.kind !== 'ok').sort((a, b) => a.t - b.t);
    const table = bad.length ? `<div class="subsection"><div class="subsection-title">Failed backups</div>
        <div class="table-container" style="max-height: 180px;"><table><thead><tr><th>Start</th><th class="num">Lasted</th>${several ? '<th>Stanza</th>' : ''}<th>Type</th>${repos ? '<th>Repo</th>' : ''}<th>Cause</th><th>Outcome</th></tr></thead><tbody>
        ${bad.map(e => `<tr><td class="pgbr-nowrap">${hms(e.t)}</td><td class="num">${e.du ? runDur(e.du) : '–'}</td>${several ? `<td>${esc(e.stanza)}</td>` : ''}
            <td>${esc(e.b.type)}${e.b.changed_to_full && e.b.requested_type ? ` <span class="pgbr-muted">(asked ${esc(e.b.requested_type)})</span>` : ''}</td>${repos ? `<td>${esc(e.b.repo || '')}</td>` : ''}
            <td>${esc(e.detail)}</td><td class="pgbr-nowrap ${e.kind === 'killed' ? 'pgbr-muted' : 'bad'}">${esc(e.result)}</td></tr>`).join('')}
        </tbody></table></div></div>` : '';
    return buildChartContainer('chart-pgbr-backups', 'Backup Durations and Sizes', { showBucketControl: false, tooltip: 'Each backup that succeeded: its duration (log scale, left) and the size pgBackRest copied (right), not the space used in the repository. A failed or killed backup is a band over the time it ran.' })
        + `<div class="chart-legend pgbr-legend">${legend}</div>` + table;
}

function walCard(pbr, f) {
    const several = pbr.length > 1;
    const series = pbr.filter(s => (s.push_dates || []).length).map(s => ({ name: stanzaName(s), dates: s.push_dates }));
    const ev = pbr.flatMap(events);
    const outages = ev.filter(e => e.cat === 'outage' && !isNaN(e.t));
    const lost = ev.filter(e => e.cat === 'lost' && !isNaN(e.t));
    const failedPushes = pbr.flatMap(s => (s.errors || []).filter(e => (e.commands || []).some(c => c.startsWith('archive-push')))
        .flatMap(e => (e.dates || []).map(d => ({ date: d, tip: `${d.slice(11)} · ${stanzaName(s)} · push failed · ${e.label}` }))));
    const pgFailures = pbr.flatMap(s => (s.pg_archive_failures || []).filter(x => x.date).map(x => ({ date: x.date,
        tip: `${x.date.slice(11)} · ${stanzaName(s)} · PostgreSQL: archive_command failed${x.exit_code ? ` (exit ${x.exit_code})` : x.signal ? ` (signal ${x.signal})` : ''}${x.segment ? ' · ' + x.segment : ''}` })));
    let chart;
    if (series.length) {
        const legend = series.map((s, i) => `<span><i class="pgbr-key pgbr-line s${i % 4}"></i>${esc(s.name)}</span>`).join('')
            + (failedPushes.length ? legendItem('is-dot', 'Failed push') : '') + (outages.length ? legendItem('is-outage', 'Archiving outage') : '')
            + (lost.length ? legendItem('is-lostline', 'WAL lost') : '') + (pgFailures.length ? legendItem('is-pgtick', 'PostgreSQL: archive_command failed') : '');
        chartData.set('chart-pgbr-wal', {
            type: 'pgbr-wal', legend, t0: wallString(f.t0), t1: wallString(f.t1), series,
            outages: outages.map(e => ({ start: e.f.first_date, end: e.f.last_date, open: e.kind === 'open', tip: `${hms(e.t)} → ${hms(e.end)} (${dur((e.end - e.t) / 1000)}) · ${e.stanza} · archiving outage · ${e.detail} · ${e.result}` })),
            lost: lost.map(e => ({ date: wallString(e.t), tip: `${hms(e.t)} · ${e.stanza} · WAL lost · ${e.detail} · reported archived to PostgreSQL` })),
            failedPushes, pgFailures,
        });
        chart = buildChartContainer('chart-pgbr-wal', 'WAL Pushed', { showBucketControl: false, tooltip: 'Segments pushed, cumulated per stanza: a flat stretch is a stall, no segment left PostgreSQL. Bands are archiving outages.' })
            + `<div class="chart-legend pgbr-legend">${legend}</div>`;
    } else {
        chart = pbr.some(pushesLogged) ? '<div class="pgbr-none">No WAL pushed in the log</div>' : buildNoDataMessage('the pgBackRest log files, where WAL pushes are logged');
    }
    const rows = ev.filter(e => e.cat === 'outage' || e.cat === 'lost').sort((a, b) => isNaN(a.t) - isNaN(b.t) || a.t - b.t);
    // PostgreSQL's side of each outage: its archive_command failures of the
    // segment, and the times it gave up for a while during the outage.
    const pgCol = pbr.some(s => (s.pg_archive_failures || []).length);
    const pgCell = e => {
        if (e.cat !== 'outage') return '<td class="num">–</td>';
        const n = (e.src.pg_archive_failures || []).filter(x => x.segment && x.segment === e.f.segment).length;
        const g = (e.src.pg_retry_dates || []).map(wallDate).filter(t => t >= e.t && t <= e.end + 999).length;
        return `<td class="num pgbr-nowrap">${n ? `${fmt(n)} failed` : '–'}${g ? `, gave up ${g}×` : ''}</td>`;
    };
    const table = rows.length ? `<div class="subsection"><div class="subsection-title">Outages and WAL lost</div>
        <div class="table-container" style="max-height: 180px;"><table><thead><tr><th>Start</th><th class="num">Lasted</th>${several ? '<th>Stanza</th>' : ''}<th>Segment</th><th class="num">Tries</th><th>Cause</th>${pgCol ? '<th class="num">PostgreSQL</th>' : ''}<th>Outcome</th></tr></thead><tbody>
        ${rows.map(e => e.cat === 'lost'
            ? `<tr><td class="pgbr-nowrap">${hms(e.t)}</td><td class="num">–</td>${several ? `<td>${esc(e.stanza)}</td>` : ''}<td class="pgbr-segment">${esc(e.segment)}</td><td class="num">–</td><td>archive queue full</td>${pgCol ? pgCell(e) : ''}<td class="pgbr-nowrap bad">WAL lost</td></tr>`
            : `<tr><td class="pgbr-nowrap">${hms(e.t)}</td><td class="num">${isNaN(e.end) ? '–' : dur((e.end - e.t) / 1000)}</td>${several ? `<td>${esc(e.stanza)}</td>` : ''}<td class="pgbr-segment">${esc(e.f.segment)}</td><td class="num">${fmt(e.f.attempts)}</td>
                <td>${e.f.code ? `[${code3(e.f.code)}] ` : ''}${esc(e.f.cause || e.f.first_line || '')}</td>${pgCol ? pgCell(e) : ''}<td class="pgbr-nowrap ${e.kind === 'open' ? 'bad' : 'warn'}">${esc(e.result)}</td></tr>`).join('')}
        </tbody></table></div></div>` : '';
    return archivingFacts(pbr) + chart + table;
}

// archivingFacts states, above the WAL Pushed chart, the async worker's
// share and PostgreSQL's own count of archive_command failures.
function archivingFacts(pbr) {
    const several = pbr.length > 1;
    const lines = pbr.map(s => {
        const ap = s.archive_push || {}, pf = s.pg_archive_failures || [], bits = [];
        if (ap.pushed_async || ap.failed_async) {
            bits.push(`<span><b>${fmt((ap.pushed || 0) + (ap.pushed_async || 0))}</b> pushed, <b>${fmt(ap.pushed_async || 0)}</b> through the async worker</span>`);
            bits.push(`<span class="${ap.failed_async ? 'bad' : ''}">async worker: <b>${fmt(ap.failed_async || 0)}</b> failed</span>`);
        }
        if (pf.length) {
            const codes = [...new Set(pf.map(x => (x.exit_code ? `exit ${x.exit_code}` : x.signal ? `signal ${x.signal}` : '')).filter(Boolean))];
            bits.push(`<span>PostgreSQL: <b>${fmt(pf.length)}</b> archive_command ${pf.length === 1 ? 'failure' : 'failures'}${codes.length ? ` (${codes.join(', ')})` : ''}</span>`);
            if (s.pg_retry_warnings) bits.push(`<span class="bad">gave up <b>${fmt(s.pg_retry_warnings)}</b> ${s.pg_retry_warnings === 1 ? 'time' : 'times'}</span>`);
        }
        return bits.length ? (several ? `<span class="pgbr-muted">${esc(stanzaName(s))}</span>` : '') + bits.join('') : '';
    }).filter(Boolean);
    return lines.map(l => `<div class="pgbr-facts">${l}</div>`).join('');
}

// restoreCard is archive-get's side, in the WAL Pushed grammar: the
// segments found per data directory (--pg1-path), its outages and the
// WAL the repository no longer had, listed under the chart.
function restoreCard(pbr, f) {
    const several = pbr.length > 1;
    const gets = pbr.filter(hasRestore);
    const facts = gets.map(s => {
        const ag = s.archive_get, failed = (ag.failed || 0) + (ag.failed_async || 0), missing = missingSegments(s);
        return `<div class="pgbr-facts">${several ? `<span class="pgbr-muted">${esc(stanzaName(s))}</span>` : ''}<span><b>${fmt(restored(s))}</b> WAL restored${ag.found_async ? ` (${fmt(ag.found_async)} async)` : ''}</span><span class="${failed ? 'bad' : ''}"><b>${fmt(failed)}</b> failed</span>${missing ? `<span class="bad"><b>${missing}</b> WAL missing from the repository</span>` : ''}</div>`;
    }).join('');
    const name = (s, p) => (several ? `${stanzaName(s)} ` : '') + (base(p) || '?');
    const series = gets.flatMap(s => (s.restore_dates || []).map(r => ({ name: name(s, r.path), dates: r.dates })));
    const ev = gets.flatMap(events);
    const outages = ev.filter(e => e.cat === 'getout' && !isNaN(e.t)), missing = ev.filter(e => e.cat === 'missing' && !isNaN(e.t));
    let chart;
    if (series.length) {
        const legend = series.map((s, i) => `<span><i class="pgbr-key pgbr-line s${i % 4}"></i>${esc(s.name)}</span>`).join('')
            + (outages.some(e => e.kind === 'outage') ? legendItem('is-outage', 'Restore outage') : '')
            + (outages.some(e => e.kind === 'open') ? legendItem('is-failband', 'Still failing') : '')
            + (missing.length ? legendItem('is-lostline', 'WAL missing') : '');
        chartData.set('chart-pgbr-restore', {
            type: 'pgbr-wal', legend, verb: 'restored', t0: wallString(f.t0), t1: wallString(f.t1), series,
            outages: outages.map(e => ({ start: e.f.first_date, end: e.f.last_date, open: e.kind === 'open', tip: `${hms(e.t)} → ${hms(e.end)} (${dur((e.end - e.t) / 1000)}) · ${name(e.src, e.f.path)} · WAL restore outage · ${e.detail} · ${e.result}` })),
            lost: missing.map(e => ({ date: e.w.date, tip: `${hms(e.t)} · ${name(e.src, e.w.path)} · ${e.w.segment} not in the archive, pushed at ${(e.w.pushed_date || '').slice(11)}` })),
            failedPushes: [], pgFailures: [],
        });
        chart = buildChartContainer('chart-pgbr-restore', 'WAL Restored', { showBucketControl: false, tooltip: 'Segments archive-get handed to PostgreSQL, cumulated per data directory: a restored instance or a standby. Bands are its outages, a red line a WAL the repository no longer had.' })
            + `<div class="chart-legend pgbr-legend">${legend}</div>`;
    } else {
        chart = '<div class="pgbr-none">No WAL restored in the log</div>';
    }
    const rows = [...outages, ...missing].sort((a, b) => a.t - b.t);
    const table = rows.length ? `<div class="subsection"><div class="subsection-title">Outages and missing WAL</div>
        <div class="table-container" style="max-height: 180px;"><table><thead><tr><th>Start</th><th class="num">Lasted</th>${several ? '<th>Stanza</th>' : ''}<th>Data directory</th><th>Segment</th><th class="num">Tries</th><th>Cause</th><th>Outcome</th></tr></thead><tbody>
        ${rows.map(e => e.cat === 'missing'
            ? `<tr><td class="pgbr-nowrap">${hms(e.t)}</td><td class="num">–</td>${several ? `<td>${esc(e.stanza)}</td>` : ''}<td title="${escAttr(e.w.path || '')}">${esc(base(e.w.path))}</td><td class="pgbr-segment">${esc(e.w.segment)}</td><td class="num">–</td><td>not in the archive, pushed at ${esc((e.w.pushed_date || '').slice(11))}</td><td class="pgbr-nowrap bad">WAL missing</td></tr>`
            : `<tr><td class="pgbr-nowrap">${hms(e.t)}</td><td class="num">${isNaN(e.end) ? '–' : dur((e.end - e.t) / 1000)}</td>${several ? `<td>${esc(e.stanza)}</td>` : ''}<td title="${escAttr(e.f.path || '')}">${esc(base(e.f.path))}</td><td class="pgbr-segment">${esc(e.f.segment || '')}</td><td class="num">${fmt(e.f.attempts)}</td>
                <td>${e.f.code ? `[${code3(e.f.code)}] ` : ''}${esc(e.f.cause || e.f.first_line || '')}</td><td class="pgbr-nowrap ${e.kind === 'open' ? 'bad' : 'warn'}">${esc(e.result)}</td></tr>`).join('')}
        </tbody></table></div></div>` : '';
    return facts + chart + table;
}

// ---- the section ----------------------------------------------------------------------

// hasContent reports whether a stanza has anything to show; lines_read
// alone is not enough, since a stanza drawn only from PostgreSQL's own
// archive_command failure lines reads no pgBackRest line.
function anyNonZero(obj) {
    return Object.values(obj || {}).some(v => (Array.isArray(v) ? v.length > 0 : !!v));
}
function hasContent(st) {
    return !!(st.lines_read || (st.backups || []).length || (st.commands || []).length || (st.operations || []).length
        || (st.expire_runs || []).length || (st.errors || []).length || (st.warnings || []).length
        || (st.pg_archive_failures || []).length || st.pg_retry_warnings || st.pg_restore_failures
        || anyNonZero(st.archive_push) || anyNonZero(st.archive_get));
}

export function buildPgBackRestSection(data) {
    const pbr = (data.pgbackrest || []).filter(st => st && hasContent(st));
    if (!pbr.length) {
        return `
            <div class="section" id="pgbackrest">
                <div class="section-header">pgBackRest</div>
                <div class="section-body">${buildNoDataMessage('a pgBackRest log file, or a PostgreSQL log whose archive_command/restore_command runs pgBackRest')}</div>
            </div>`;
    }
    // The time filter does not re-scope these figures: say so.
    const whole = data._wholeLog?.pgbackrest ? wholeLogBadge() : '';
    const f = frame(pbr);
    const undated = buildNoDataMessage('<code>log-timestamp = y</code> in pgbackrest.conf');
    return `
        <div class="section" id="pgbackrest">
            <div class="section-header">pgBackRest${whole}</div>
            <div class="section-body">
                ${missingBanner(pbr)}
                ${header(pbr, !!data.pgbackrest_only)}
                <ql-tabs>
                    <ql-tab selected>Stanzas</ql-tab><ql-tab>Repositories</ql-tab><ql-tab>Backups</ql-tab>
                    <ql-panel>${stanzaCards(pbr, f)}</ql-panel>
                    <ql-panel>${repoCards(pbr)}</ql-panel>
                    <ql-panel>${backupCards(pbr)}</ql-panel>
                </ql-tabs>
            </div>
        </div>
        <div class="grid grid-2">
            <div class="section" id="pgbackrest-checks">
                <div class="section-header">pgBackRest Checks${whole}</div>
                <div class="section-body">${checksTable(pbr, !!data.pgbackrest_only)}</div>
            </div>
            <div class="section" id="pgbackrest-events">
                <div class="section-header">pgBackRest Events${whole}</div>
                <div class="section-body events-section">${eventsCard(pbr)}</div>
            </div>
        </div>
        <div class="grid grid-2">
            <div class="section" id="pgbackrest-backups">
                <div class="section-header">pgBackRest Backups${whole}</div>
                <div class="section-body">${f ? backupsCard(pbr, f) : undated}</div>
            </div>
            <div class="section" id="pgbackrest-wal">
                <div class="section-header">${pbr.some(hasRestore) ? 'pgBackRest WAL' : 'pgBackRest WAL Archiving'}${whole}</div>
                <div class="section-body">${!f ? undated : pbr.some(hasRestore) ? `<ql-tabs class="pgbr-wal-tabs">
                    <ql-tab selected>Archiving</ql-tab><ql-tab>Restore</ql-tab>
                    <ql-panel>${walCard(pbr, f)}</ql-panel>
                    <ql-panel>${restoreCard(pbr, f)}</ql-panel>
                </ql-tabs>` : walCard(pbr, f)}</div>
            </div>
        </div>`;
}
