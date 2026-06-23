// web/js/period-nav.js
//
// Shared period navigator for split reports (--split HTML) and the WASM split
// mode. It is driven entirely by globals so both environments reuse it verbatim:
//   - window.REPORT_PERIODS = [{ label, entries, errors, data }]  (data = the
//     zstd+base64 per-period blob produced by Go's compressReportJSON)
//   - window.decompressData(blob) -> Promise<dataset>   (provided per env)
//   - window.renderResults / window.setAnalysisData     (exposed by app.js)
//
// It injects a compact heatmap into the Summary tile: one square per period,
// colour intensity = entry volume, the selected period in colour. Datasets are
// decompressed lazily and cached. harmonizeSummary() (app.js) skips the date
// title + timeline when window.QL_SPLIT is set, leaving them to this navigator.

let _periodCache = {};
let _curPeriod = 0;
let _maxEntries = 1;

const _fmtInt = (n) => (n || 0).toLocaleString();

// "2026-02-04 00:00" -> "4 Feb 2026 · 00:00" (matches the non-split date style).
function formatPeriodLabel(lbl) {
    const sp = lbl.indexOf(' ');
    const datePart = sp > 0 ? lbl.slice(0, sp) : lbl;
    const timePart = sp > 0 ? lbl.slice(sp + 1) : '';
    const d = new Date(datePart + 'T00:00:00');
    if (isNaN(d.getTime())) return lbl;
    const human = d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
    return timePart ? human + ' · ' + timePart : human;
}

async function showPeriod(idx) {
    const periods = window.REPORT_PERIODS || [];
    if (idx < 0 || idx >= periods.length) return;
    _curPeriod = idx;
    const p = periods[idx];
    let data = _periodCache[idx];
    if (!data) {
        try { data = await window.decompressData(p.data); _periodCache[idx] = data; }
        catch (err) { console.error('Failed to load period', idx, err); alert('Failed to load period: ' + err.message); return; }
    }
    window.setAnalysisData(data);
    data._parseTimeMs = (data.meta && data.meta.parse_time_ms) || 0;
    window.renderResults(data, (data.meta && data.meta.filename) || p.label, (data.meta && data.meta.filesize) || 0, true);
    injectPeriodNav();
    window.scrollTo(0, 0);
}

function injectPeriodNav() {
    const periods = window.REPORT_PERIODS || [];
    const host = document.querySelector('#summary .summary-body');
    if (!host || host.querySelector('.summary-periods')) return;
    // The eyebrow (size fold + filename tooltip) is handled by harmonizeSummary.

    const box = document.createElement('div');
    box.className = 'summary-periods';

    const head = document.createElement('div');
    head.className = 'summary-periods-head';
    const spacer = document.createElement('span'); // balances the centered nav
    const nav = document.createElement('div');
    nav.className = 'summary-periods-nav';
    const prev = document.createElement('button');
    prev.className = 'summary-periods-btn'; prev.textContent = '‹';
    prev.title = 'Previous period'; prev.disabled = _curPeriod === 0;
    prev.onclick = () => showPeriod(_curPeriod - 1);
    const label = document.createElement('span');
    label.className = 'summary-periods-label';
    label.textContent = formatPeriodLabel(periods[_curPeriod].label);
    const next = document.createElement('button');
    next.className = 'summary-periods-btn'; next.textContent = '›';
    next.title = 'Next period'; next.disabled = _curPeriod === periods.length - 1;
    next.onclick = () => showPeriod(_curPeriod + 1);
    nav.append(prev, label, next);
    head.append(spacer, nav);

    const grid = document.createElement('div');
    grid.className = 'summary-periods-grid';
    periods.forEach((p, i) => {
        const seg = document.createElement('button');
        seg.className = 'ql-seg' + (p.errors ? ' has-err' : '') + (i === _curPeriod ? ' active' : '');
        seg.title = p.label + ' · ' + _fmtInt(p.entries) + ' entries' +
            (p.errors ? ' · ' + _fmtInt(p.errors) + ' errors' : '');
        const fill = document.createElement('i');
        fill.style.setProperty('--t', 12 + Math.round((p.entries || 0) / _maxEntries * 88));
        seg.appendChild(fill);
        seg.onclick = () => showPeriod(i);
        grid.appendChild(seg);
    });

    // Bounds under the heatmap (first..last period): time-of-day for intraday
    // splits, the humanized date for daily+ splits (matches the title style).
    const shortBound = (lbl) => { const i = lbl.indexOf(' '); return i > 0 ? lbl.slice(i + 1) : formatPeriodLabel(lbl); };
    const bounds = document.createElement('div');
    bounds.className = 'summary-periods-bounds';
    const b0 = document.createElement('span');
    b0.textContent = shortBound(periods[0].label);
    const b1 = document.createElement('span');
    b1.textContent = shortBound(periods[periods.length - 1].label);
    bounds.append(b0, b1);

    box.append(head, grid, bounds);
    const anchor = host.querySelector('.stat-grid') || host.querySelector('.summary-header');
    if (anchor) anchor.after(box);
    else host.insertBefore(box, host.firstChild);
    const active = grid.children[_curPeriod];
    if (active) active.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

function onKey(e) {
    const t = e.target;
    if (t && (t.tagName === 'INPUT' || t.tagName === 'SELECT' || t.tagName === 'TEXTAREA')) return;
    if (e.key === 'ArrowLeft') showPeriod(_curPeriod - 1);
    else if (e.key === 'ArrowRight') showPeriod(_curPeriod + 1);
}

// startPeriodNav switches the Summary tile into period mode and shows the first
// period. Call after window.REPORT_PERIODS and window.decompressData are set.
// It resets all per-session state (notably the decoded-period cache, which is
// index-keyed and would otherwise return a previous split's datasets).
export function startPeriodNav() {
    const periods = window.REPORT_PERIODS || [];
    if (!periods.length) return;
    _periodCache = {};
    _curPeriod = 0;
    _maxEntries = periods.reduce((m, p) => Math.max(m, p.entries || 0), 1);
    window.QL_SPLIT = true; // harmonizeSummary leaves the title + strip to us
    document.documentElement.classList.add('ql-split');
    document.removeEventListener('keydown', onKey);
    document.addEventListener('keydown', onKey);
    showPeriod(0);
}

// stopPeriodNav tears split mode down: drop the cache, the keyboard handler, the
// QL_SPLIT flag and the .ql-split class so the next single render is normal.
export function stopPeriodNav() {
    _periodCache = {};
    _curPeriod = 0;
    document.removeEventListener('keydown', onKey);
    window.QL_SPLIT = false;
    document.documentElement.classList.remove('ql-split');
}

window.startPeriodNav = startPeriodNav;
window.stopPeriodNav = stopPeriodNav;
window.showPeriod = showPeriod;
