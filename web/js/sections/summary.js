// Summary section: header stat grid, date range, and interactive time slider.

import { fmt, fmtBytes, fmtDur, esc } from '../utils.js';
import { computeDayAxis, MAX_CANVAS_DAYS, offsetToTs } from '../filters.js';
import { timeFilterStartTs, timeFilterDurationMins } from '../state.js';
import { buildServerSummaryLine } from './checkpoints.js';

export function buildSummarySection(data, fileInfo) {
    const s = data.summary;
    const f = fileInfo || {};

    // Format parse time nicely
    const parseTime = f.parseTimeMs || 0;
    const parseTimeStr = parseTime < 1000 ? `${parseTime}ms` : `${(parseTime/1000).toFixed(2)}s`;

    // Format duration: d:h / h:m / m:s with rollover. Handles the "d" unit
    // that fmtDur introduces for spans >= 24h (e.g. "1d", "2d3h").
    const formatDuration = (durStr) => {
        if (!durStr || durStr === '-') return '-';
        // Parse "2d3h", "12h 30m 11s", "5m 23s" or "45s"
        let d = parseInt(durStr.match(/(\d+)d/)?.[1] || 0);
        let h = parseInt(durStr.match(/(\d+)h/)?.[1] || 0);
        let m = parseInt(durStr.match(/(\d+)m/)?.[1] || 0);
        let sec = parseInt(durStr.match(/(\d+)s/)?.[1] || 0);
        // Rollover
        if (sec >= 60) { m += Math.floor(sec / 60); sec = sec % 60; }
        if (m >= 60) { h += Math.floor(m / 60); m = m % 60; }
        if (h >= 24) { d += Math.floor(h / 24); h = h % 24; }
        if (d > 0) return h > 0 ? `${d}d${h}h` : `${d}d`;
        if (h > 0) return `${h}h${m.toString().padStart(2, '0')}`;
        if (m > 0) return `${m}m${sec.toString().padStart(2, '0')}s`;
        return `${sec}s`;
    };

    // Format date as human readable
    const formatDateHuman = (dateStr) => {
        if (!dateStr) return '';
        const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
        const parts = dateStr.split('-');
        if (parts.length !== 3) return dateStr;
        const day = parseInt(parts[2]);
        const month = months[parseInt(parts[1]) - 1] || parts[1];
        const year = parts[0];
        return `${day} ${month} ${year}`;
    };

    // Parse dates for timeline
    const startDate = s.start_date || '';
    const endDate = s.end_date || '';
    const startDay = startDate.split(' ')[0] || '';
    const endDay = endDate.split(' ')[0] || '';
    const startTime = startDate.split(' ')[1] || '';
    const endTime = endDate.split(' ')[1] || '';
    const sameDay = startDay === endDay;

    // Human readable date for header, collapsing the month/year shared by
    // both bounds: "1 → 2 Jan 2026", "1 Jan → 2 Feb 2026", or the full
    // "1 Jan 2026 → 2 Jan 2027" when the years differ.
    const formatDateRange = (sd, ed) => {
        const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
        const sp = sd.split('-');
        const ep = ed.split('-');
        if (sp.length !== 3 || ep.length !== 3) {
            return `${formatDateHuman(sd)} → ${formatDateHuman(ed)}`;
        }
        const sDay = parseInt(sp[2]), eDay = parseInt(ep[2]);
        const sMon = months[parseInt(sp[1]) - 1] || sp[1];
        const eMon = months[parseInt(ep[1]) - 1] || ep[1];
        const sYear = sp[0], eYear = ep[0];
        if (sYear === eYear && sp[1] === ep[1]) {
            return `${sDay} → ${eDay} ${eMon} ${eYear}`;
        }
        if (sYear === eYear) {
            return `${sDay} ${sMon} → ${eDay} ${eMon} ${eYear}`;
        }
        return `${sDay} ${sMon} ${sYear} → ${eDay} ${eMon} ${eYear}`;
    };
    // Derive the header date(s) from the same guarded day axis as the
    // slider, so a folded near-empty day doesn't show in the header either.
    const tsToDayStr = (ts) => {
        const d = new Date(ts);
        const p = (n) => String(n).padStart(2, '0');
        return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
    };
    // Canvas axis from the slider's STATE (set once at initial load), not
    // from data.summary which carries the *filtered* bounds after a drag —
    // otherwise the day canvas/header would reshape on every filter.
    let axis = null;
    if (timeFilterStartTs && timeFilterDurationMins > 0) {
        axis = { axisStart: timeFilterStartTs, nDays: Math.max(1, Math.round(timeFilterDurationMins / 1440)) };
    } else if (startDate && endDate) {
        axis = computeDayAxis(startDate, endDate);
    }
    let dateDisplay;
    if (axis) {
        const firstDay = tsToDayStr(axis.axisStart);
        const lastDay = tsToDayStr(offsetToTs(axis.axisStart, (axis.nDays - 1) * 1440));
        dateDisplay = axis.nDays === 1 ? formatDateHuman(firstDay) : formatDateRange(firstDay, lastDay);
    } else {
        dateDisplay = sameDay ? formatDateHuman(startDay) : formatDateRange(startDay, endDay);
    }

    // Time range label. The header
    // already carries the full dates ("13 Feb 2026 → 14 Feb
    // 2026"), so the multi-day form reuses the same short
    // vocabulary and drops seconds — "13 Feb 11:59 → 14 Feb
    // 00:00" instead of repeating two full timestamps.
    const shortDay = (dateStr) => formatDateHuman(dateStr).replace(/ \d{4}$/, '');
    const timeRangeLabel = sameDay
        ? `${startTime.slice(0, 5)} – ${endTime.slice(0, 5)}`
        : `${shortDay(startDay)} ${startTime.slice(0, 5)} → ${shortDay(endDay)} ${endTime.slice(0, 5)}`;

    // Multi-day: the track is each touched calendar day as a full 24h of
    // EQUAL width. Overlay a grey date (day/month) centred on each day and
    // a thin divider at each midnight. The filled bar (default selection =
    // data extent) then shows how far the log reaches into each day.
    // Midnight dividers for any multi-day span (up to ~3 months, beyond
    // which they'd be too dense); per-day date labels only while they fit
    // (<= MAX_CANVAS_DAYS), otherwise the start/end dates sit at the bounds.
    const hasDayLabels = !!(axis && axis.nDays > 1 && axis.nDays <= MAX_CANVAS_DAYS);
    let dayMarkers = '';
    if (axis && axis.nDays > 1 && axis.nDays <= 92) {
        const monthsAbbr = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
        const labels = [];
        const dividers = [];
        for (let k = 0; k < axis.nDays; k++) {
            if (hasDayLabels) {
                const dd = new Date(offsetToTs(axis.axisStart, k * 1440));
                labels.push(`<span class="summary-time-day-label" style="left:${(k + 0.5) / axis.nDays * 100}%">${dd.getDate()} ${monthsAbbr[dd.getMonth()]}</span>`);
            }
            if (k > 0) dividers.push(`<i class="summary-time-day-divider" style="left:${k / axis.nDays * 100}%"></i>`);
        }
        dayMarkers = labels.join('') + dividers.join('');
    }

    // Bound labels: midnight-to-midnight by default, but for a span too
    // wide for per-day labels, show the start/end dates at the ends instead.
    let boundLeft = '00:00', boundRight = '24:00';
    if (axis && axis.nDays > MAX_CANVAS_DAYS) {
        const monthsAbbr = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
        const fmtDay = (ts) => { const d = new Date(ts); return `${d.getDate()} ${monthsAbbr[d.getMonth()]}`; };
        boundLeft = fmtDay(axis.axisStart);
        boundRight = fmtDay(offsetToTs(axis.axisStart, (axis.nDays - 1) * 1440));
    }

    return `
        <div class="section" id="summary">
            <div class="section-header">Summary</div>
            <div class="section-body summary-body">
                <div class="summary-header">
                    <div class="summary-date">${dateDisplay}</div>
                    <div class="summary-meta">
                        <span class="summary-filename">${esc(f.fileName || 'Unknown')}</span>
                        <span class="summary-parsetime">parsed in ${parseTimeStr}</span>
                    </div>
                </div>
                <div class="summary-separator"></div>
                <div class="stat-grid" style="grid-template-columns: repeat(4, auto); justify-content: center; margin-bottom: 1.2rem;">
                    <div class="stat-card">
                        <div class="stat-value">${(f.format || '?').toUpperCase()}</div>
                        <div class="stat-label">format</div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-value">${fmtBytes(f.fileSize || 0)}</div>
                        <div class="stat-label">size</div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-value">${fmt(s.total_logs)}</div>
                        <div class="stat-label">entries</div>
                    </div>
                    <div class="stat-card">
                        <div class="stat-value">${formatDuration(fmtDur(s.duration))}</div>
                        <div class="stat-label">duration</div>
                    </div>
                </div>
                <div class="summary-time${hasDayLabels ? ' summary-time--multiday' : ''}" id="summaryTime">
                    <!-- Slider mode (single day, <= 24h): interactive replacement
                         for the old read-only timeline. Same IDs as the former Time
                         dropdown so initTimeFilter()/updateTimeSlider() keep working. -->
                    <div class="filter-time-slider" id="filterTimeSlider">
                        <div class="summary-time-row">
                            <span class="summary-time-bound">${boundLeft}</span>
                            <div class="filter-time-slider-track">
                                <input type="range" id="filterTimeMin" min="0" max="1440" value="0" step="1">
                                <input type="range" id="filterTimeMax" min="0" max="1440" value="1440" step="1">
                                <div class="filter-time-slider-range" id="filterTimeRange"></div>
                                ${dayMarkers}
                            </div>
                            <span class="summary-time-bound">${boundRight}</span>
                        </div>
                        <div class="filter-time-slider-label" id="filterTimeLabel">${timeRangeLabel}</div>
                    </div>
                </div>
                ${buildServerSummaryLine(data)}
            </div>
        </div>
    `;
}
