/**
 * Utility functions for formatting and escaping.
 * @module utils
 */

/**
 * Format number with locale separators.
 * @param {number} n
 * @returns {string}
 */
export function fmt(n) {
    return n?.toLocaleString() ?? '0';
}

// Duration formatter (native Intl API when available). Older browsers lack
// Intl.DurationFormat; fall back to a small formatter approximating the
// narrow style ("1h 2m 3s") so module load doesn't throw.
const durationFmt = typeof Intl !== 'undefined' && typeof Intl.DurationFormat === 'function'
    ? new Intl.DurationFormat('en', { style: 'narrow' })
    : {
        format(d) {
            const parts = [];
            if (d.days) parts.push(d.days + 'd');
            if (d.hours) parts.push(d.hours + 'h');
            if (d.minutes) parts.push(d.minutes + 'm');
            if (d.seconds) parts.push(d.seconds + 's');
            return parts.join(' ');
        }
    };

/**
 * Format milliseconds to human-readable duration using native Intl.DurationFormat.
 * @param {number} ms
 * @returns {string}
 */
export function fmtDuration(ms) {
    if (!ms || ms < 0) return '0ms';

    const totalSeconds = Math.floor(ms / 1000);
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;
    const milliseconds = Math.round(ms % 1000);

    // For sub-second, show ms directly
    if (totalSeconds === 0) {
        return milliseconds + 'ms';
    }

    const duration = {};
    if (hours > 0) duration.hours = hours;
    if (minutes > 0) duration.minutes = minutes;
    if (seconds > 0) duration.seconds = seconds;

    return durationFmt.format(duration);
}

/**
 * Safe max for large arrays (avoids "too many arguments" error with Math.max).
 * @param {number[]} arr
 * @returns {number}
 */
export function safeMax(arr) {
    return arr.length === 0 ? 0 : arr.reduce((a, b) => a > b ? a : b, arr[0]);
}

/**
 * Safe min for large arrays (avoids "too many arguments" error with Math.min).
 * @param {number[]} arr
 * @returns {number}
 */
export function safeMin(arr) {
    return arr.length === 0 ? 0 : arr.reduce((a, b) => a < b ? a : b, arr[0]);
}

// Byte formatting lives in format.js (single unit table); re-exported here
// for back-compat so existing importers keep working unchanged.
export { fmtBytes } from './format.js';

/**
 * Coarser duration formatter that keeps only the two most significant
 * units (drops seconds once the value crosses an hour, drops minutes
 * once it crosses a day). Better for stat-cards where "3h 59m 17s"
 * adds noise to the headline reading — "3h 59m" lands faster.
 * @param {number} ms
 * @returns {string}
 */
export function fmtDurationCoarse(ms) {
    if (!ms || ms < 0) return '0ms';
    const totalSeconds = Math.floor(ms / 1000);
    if (totalSeconds === 0) return Math.round(ms % 1000) + 'ms';
    const days = Math.floor(totalSeconds / 86400);
    const hours = Math.floor((totalSeconds % 86400) / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;
    const duration = {};
    if (days > 0) {
        duration.days = days;
        if (hours > 0) duration.hours = hours;
    } else if (hours > 0) {
        duration.hours = hours;
        if (minutes > 0) duration.minutes = minutes;
    } else if (minutes > 0) {
        duration.minutes = minutes;
        if (seconds > 0) duration.seconds = seconds;
    } else {
        duration.seconds = seconds;
    }
    return durationFmt.format(duration);
}

/**
 * Format large integer counts with SI-style suffixes (1.5M, 370M, 4.5G).
 * Matches the CLI's formatCompact so the report's HTML and text outputs
 * use the same units for buffer / WAL aggregate counts.
 * @param {number} n
 * @returns {string}
 */
export function fmtCompact(n) {
    if (n == null || n < 0) return '-';
    if (n < 1000) return String(n);
    if (n < 10000) return (n / 1000).toFixed(1) + 'k';
    if (n < 1_000_000) return Math.round(n / 1000) + 'k';
    if (n < 10_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n < 1_000_000_000) return Math.round(n / 1_000_000) + 'M';
    if (n < 10_000_000_000) return (n / 1_000_000_000).toFixed(1) + 'G';
    return Math.round(n / 1_000_000_000) + 'G';
}

/**
 * Format milliseconds (numeric) to compact display string.
 * @param {number|string} ms
 * @returns {string}
 */
export function fmtMs(ms) {
    if (ms == null) return '-';
    if (typeof ms === 'string') return fmtDur(ms);
    if (ms < 1) return '<1ms';
    if (ms < 1000) return ms.toFixed(1) + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(2) + 's';
    return (ms / 60000).toFixed(1) + 'm';
}

/**
 * Format/clean duration strings from Go backend. Fractional seconds are
 * rounded to the nearest whole second when combined with larger units
 * (e.g. "2m7.663353305s" -> "2m 8s").
 * @param {string} s - Duration string in Go format
 * @returns {string}
 */
export function fmtDur(s) {
    if (!s || s === '-') return '-';
    if (typeof s !== 'string') return String(s);

    // Sub-millisecond units from Go's Duration.String() (e.g. session times):
    // "738µs" / "738ns". Keep them verbatim — the h/m/s parser below would read
    // the number as seconds (rendering "738µs" as "738.00s").
    if ((s.includes('µs') || s.includes('μs') || s.includes('ns')) && !s.includes('ms')) {
        return s;
    }

    // Check for ms first (msIdx is position where 'ms' starts)
    const msIdx = s.indexOf('ms');
    if (msIdx > 0 && s.indexOf('h') < 0 && s.indexOf('m') === msIdx) {
        // Pure milliseconds (no separate 'm' for minutes)
        const msVal = parseFloat(s);
        return isNaN(msVal) ? s : msVal.toFixed(1) + 'ms';
    }

    // Parse h/m/s components
    let h = 0, m = 0, sec = 0;
    const hIdx = s.indexOf('h');
    const mIdx = s.indexOf('m');
    const sIdx = s.lastIndexOf('s');

    if (hIdx > 0) h = parseInt(s.substring(0, hIdx)) || 0;
    if (mIdx > 0 && mIdx !== msIdx) {
        const mStart = hIdx > 0 ? hIdx + 1 : 0;
        m = parseInt(s.substring(mStart, mIdx)) || 0;
    }
    if (sIdx > 0 && sIdx !== msIdx + 1) {
        const sStart = mIdx > 0 && mIdx !== msIdx ? mIdx + 1 : (hIdx > 0 ? hIdx + 1 : 0);
        sec = parseFloat(s.substring(sStart, sIdx)) || 0;
    }

    // Build output — convert hours > 24 to days
    const parts = [];
    if (h >= 24) {
        const days = Math.floor(h / 24);
        const remH = h % 24;
        parts.push(days + 'd');
        if (remH > 0) parts.push(remH + 'h');
    } else if (h > 0) {
        parts.push(h + 'h');
    }
    if (m > 0) parts.push(m + 'm');
    if (sec > 0) {
        parts.push(parts.length > 0 ? Math.round(sec) + 's' : sec.toFixed(2) + 's');
    }

    // Handle pure milliseconds that weren't caught above
    if (parts.length === 0 && msIdx > 0) {
        const msVal = parseFloat(s);
        return isNaN(msVal) ? s : msVal.toFixed(1) + 'ms';
    }

    return parts.length > 0 ? parts.join(' ') : s;
}

/**
 * Parse a duration string (Go format or fmtDur output, e.g. "1h 2m 3s",
 * "2m7.66s", "153 ms") into milliseconds. Missing units contribute 0;
 * non-string input is coerced with Number().
 * @param {string|number} s - Duration string (or already-numeric ms)
 * @returns {number} Duration in milliseconds
 */
export function parseDurToMs(s) {
    if (!s || s === '-') return 0;
    if (typeof s !== 'string') return Number(s) || 0;
    let ms = 0;
    const hMatch = s.match(/(\d+)\s*h/);
    const mMatch = s.match(/(\d+)\s*m(?!s)/);
    const sMatch = s.match(/([\d.]+)\s*s(?!.*ms)/);
    const msMatch = s.match(/([\d.]+)\s*ms/);
    // Sub-millisecond units from Go's Duration.String() (µ is U+00B5; some
    // toolchains emit U+03BC). Without these, "738µs" parsed to 0 and broke
    // the session min/avg sort keys.
    const usMatch = s.match(/([\d.]+)\s*[µμ]s/);
    const nsMatch = s.match(/([\d.]+)\s*ns/);
    if (hMatch) ms += parseInt(hMatch[1]) * 3600000;
    if (mMatch) ms += parseInt(mMatch[1]) * 60000;
    if (sMatch) ms += parseFloat(sMatch[1]) * 1000;
    if (msMatch) ms += parseFloat(msMatch[1]);
    if (usMatch) ms += parseFloat(usMatch[1]) / 1000;
    if (nsMatch) ms += parseFloat(nsMatch[1]) / 1000000;
    return ms;
}

// Format a Date as local wall-clock "YYYY-MM-DD HH:MM:SS" — the same clock as
// the report's other sections, instead of UTC (toISOString shifted the day for
// non-UTC logs in the event-detail modal). Homed here (DOM-free) so it stays
// testable; the modal module pulls DOM-bound deps that node cannot import.
export function localDateTime(d) {
    const p = n => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ` +
        `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// Bucket a query's executions by duration into the backend's fixed distribution
// buckets. Reads duration_ms (a number); the query-detail modal used to read a
// non-existent `duration` string, so every value was 0 and the block never
// rendered.
export function qdDurationBuckets(execs) {
    const buckets = [
        { label: '< 1 ms', max: 1 },
        { label: '< 10 ms', max: 10 },
        { label: '< 100 ms', max: 100 },
        { label: '< 1 s', max: 1000 },
        { label: '< 10 s', max: 10000 },
        { label: '>= 10 s', max: Infinity }
    ];
    const counts = buckets.map(() => 0);
    for (const e of execs) {
        const d = e.duration_ms;
        if (typeof d !== 'number') continue;
        for (let i = 0; i < buckets.length; i++) {
            if (d < buckets[i].max) { counts[i]++; break; }
        }
    }
    return buckets.map((b, i) => ({ label: b.label, count: counts[i] }));
}

/**
 * Format a millisecond duration exactly like the Go backend's
 * formatQueryDuration (output/format.go): "512 ms" / "42.50 s" /
 * "42m 05s" / "1h 12m 50s" / "2d 3h 04m". Used by the report time-filter
 * re-aggregation so recomputed durations render identically to the
 * originals produced by the backend (the stat cards re-parse via fmtDur).
 * @param {number} ms - Duration in milliseconds
 * @returns {string}
 */
export function fmtQueryDuration(ms) {
    const S = 1000, M = 60 * S, H = 60 * M, D = 24 * H;
    if (ms < S) return `${Math.trunc(ms)} ms`;
    if (ms < M) return `${(ms / S).toFixed(2)} s`;
    const pad = n => String(n).padStart(2, '0');
    if (ms < H) {
        return `${Math.trunc(ms / M)}m ${pad(Math.trunc((ms % M) / S))}s`;
    }
    if (ms < D) {
        return `${Math.trunc(ms / H)}h ${pad(Math.trunc((ms % H) / M))}m ${pad(Math.trunc((ms % M) / S))}s`;
    }
    return `${Math.trunc(ms / D)}d ${Math.trunc((ms % D) / H)}h ${pad(Math.trunc((ms % H) / M))}m`;
}

/**
 * Escape HTML special characters.
 * @param {string} s
 * @returns {string}
 */
export function esc(s) {
    if (!s) return '';
    const d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
}

/**
 * Escape a string for safe interpolation inside a double-quoted HTML
 * attribute value. esc() (textContent→innerHTML) escapes & < > but NOT the
 * quote chars, so a log-derived value containing " could break out of an
 * attribute and inject a handler. escAttr handles the quotes too.
 * Pure string replacement — no DOM.
 * @param {*} s - Value to escape (stringified; null/undefined -> '')
 * @returns {string}
 */
export function escAttr(s) {
    if (s === null || s === undefined) return '';
    return String(s)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
}

// escForJsAttr escapes a string so it can be safely embedded as a JS string
// literal inside an HTML attribute (e.g. `onclick="...writeText('${x}')"`).
// Order matters:
//   1. esc() → neutralizes & < > for HTML
//   2. " → &quot; so a quote in the text doesn't close the attribute
//      (the bug that makes the leftover JS appear as button text when a
//      message contains a double quote)
//   3. ' → \' so the JS single-quoted string literal stays valid
//   4. \n → \\n so newlines in messages don't break the JS line
export function escForJsAttr(s) {
    if (!s) return '';
    return esc(s)
        .replace(/"/g, '&quot;')
        .replace(/'/g, "\\'")
        .replace(/\n/g, '\\n');
}

/**
 * Truncate a query string to `max` characters, appending an ellipsis.
 * @param {string} s - Query text
 * @param {number} [max=120] - Maximum length before truncation
 * @returns {string}
 */
export function truncQuery(s, max = 120) {
    if (!s || s.length <= max) return s;
    return s.slice(0, max) + '…';
}

/**
 * Longer-form millisecond duration formatter used by the query-detail
 * views (query table "Total" column, query detail modal stats). Unlike
 * fmtMs, always spells out unit suffixes down to seconds/minutes/hours
 * and never abbreviates to a single decimal beyond 1000ms.
 * @param {number} ms
 * @returns {string}
 */
export function fmtMsLong(ms) {
    if (ms == null || isNaN(ms)) return '-';
    if (ms < 1000) return ms.toFixed(0) + 'ms';
    if (ms < 60000) return (ms / 1000).toFixed(2) + 's';
    if (ms < 3600000) return Math.floor(ms / 60000) + 'm ' + Math.round((ms % 60000) / 1000) + 's';
    const h = Math.floor(ms / 3600000);
    const m = Math.floor((ms % 3600000) / 60000);
    const s = Math.round((ms % 60000) / 1000);
    if (h < 24) return h + 'h ' + m + 'm ' + s + 's';
    const d = Math.floor(h / 24);
    return d + 'd ' + (h % 24) + 'h ' + m + 'm';
}

/**
 * Build the shared "No data available" placeholder for a section whose
 * source data is absent (e.g. the relevant log_* setting is off).
 * @param {string} hint - HTML hint fragment naming the setting to check
 * @returns {string}
 */
export function buildNoDataMessage(hint) {
    return `
        <div class="no-data-message">
            <div class="no-data-text">No data available</div>
            <div class="no-data-hint">Check: ${hint}</div>
        </div>
    `;
}
