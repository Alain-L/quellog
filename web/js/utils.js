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

// Duration formatter (native Intl API)
const durationFmt = new Intl.DurationFormat('en', { style: 'narrow' });

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

/**
 * Format bytes to human-readable size.
 * @param {number} b - Bytes
 * @returns {string}
 */
export function fmtBytes(b) {
    const strip = v => v.replace(/\.0$/, '');
    if (b < 1024) return b + ' B';
    if (b < 1024 * 1024) return Math.round(b / 1024) + ' KB';
    if (b < 1024 * 1024 * 1024) return Math.round(b / 1024 / 1024) + ' MB';
    return strip((b / 1024 / 1024 / 1024).toFixed(1)) + ' GB';
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
 * Format/clean duration strings from Go backend (e.g. "2m7.663353305s" -> "2m 7s").
 * @param {string} s - Duration string in Go format
 * @returns {string}
 */
export function fmtDur(s) {
    if (!s || s === '-') return '-';
    if (typeof s !== 'string') return String(s);

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

export function parseDurToMs(s) {
    if (!s || s === '-') return 0;
    if (typeof s !== 'string') return Number(s) || 0;
    let ms = 0;
    const hMatch = s.match(/(\d+)\s*h/);
    const mMatch = s.match(/(\d+)\s*m(?!s)/);
    const sMatch = s.match(/([\d.]+)\s*s(?!.*ms)/);
    const msMatch = s.match(/([\d.]+)\s*ms/);
    if (hMatch) ms += parseInt(hMatch[1]) * 3600000;
    if (mMatch) ms += parseInt(mMatch[1]) * 60000;
    if (sMatch) ms += parseFloat(sMatch[1]) * 1000;
    if (msMatch) ms += parseFloat(msMatch[1]);
    return ms;
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

export function truncQuery(s, max = 120) {
    if (!s || s.length <= max) return s;
    return s.slice(0, max) + '…';
}
