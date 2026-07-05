/**
 * Byte-size formatting and parsing helpers (DOM-free, pure).
 *
 * All byte helpers share the single binary unit table below, but the three
 * formatters intentionally keep distinct output formats (rounding, decimals,
 * unit labels) because their call sites render differently:
 *   - fmtBytes         "512 B" / "12 KB" / "34 MB" / "1.5 GB"  (rounded, .0 stripped on GB)
 *   - fmtBytesPrecise  "512 B" / "12.3 KB" / "34.0 MB" / "1.50 GB"  (fixed decimals, '-' on null)
 *   - fmtBytesShort    "512B" / "12K" / "34M" / "1.5G"  (axis labels, '0' on null/0)
 * Same for the two parsers:
 *   - parseSizeToBytes        loose match anywhere in the string, fractional
 *     result, parseFloat fallback (chart/event aggregation)
 *   - parseSizeToBytesStrict  anchored match, optional unit, rounded result,
 *     0 on mismatch (report time-filter recompute)
 * Unifying any pair would change rendered output; keep them separate.
 * @module format
 */

/** Binary (1024-based) unit table shared by all byte helpers. */
const BYTE_UNITS = { B: 1, KB: 1024, MB: 1024 ** 2, GB: 1024 ** 3, TB: 1024 ** 4 };

/**
 * Pick the display tier for a byte count: unit label + divisor.
 * Display caps at GB (values >= 1 TB render as GB), matching all
 * historical formatters.
 * @param {number} b - Bytes
 * @returns {{unit: string, div: number}}
 */
function scaleBytes(b) {
    if (b < BYTE_UNITS.KB) return { unit: 'B', div: BYTE_UNITS.B };
    if (b < BYTE_UNITS.MB) return { unit: 'KB', div: BYTE_UNITS.KB };
    if (b < BYTE_UNITS.GB) return { unit: 'MB', div: BYTE_UNITS.MB };
    return { unit: 'GB', div: BYTE_UNITS.GB };
}

/**
 * Format bytes to human-readable size (rounded KB/MB, 1-decimal GB with
 * trailing ".0" stripped).
 * @param {number} b - Bytes
 * @returns {string}
 */
export function fmtBytes(b) {
    const { unit, div } = scaleBytes(b);
    if (unit === 'B') return b + ' B';
    if (unit === 'GB') return (b / div).toFixed(1).replace(/\.0$/, '') + ' GB';
    return Math.round(b / div) + ' ' + unit;
}

/**
 * Format bytes with fixed decimals (1 for KB/MB, 2 for GB); used in chart
 * tooltips where sub-unit precision matters. Returns '-' for null/NaN.
 * @param {number} b - Bytes
 * @returns {string}
 */
export function fmtBytesPrecise(b) {
    if (b == null || isNaN(b)) return '-';
    const { unit, div } = scaleBytes(b);
    if (unit === 'B') return b.toFixed(0) + ' B';
    if (unit === 'GB') return (b / div).toFixed(2) + ' GB';
    return (b / div).toFixed(1) + ' ' + unit;
}

/**
 * Format bytes compactly for axis labels (single-letter unit, no space).
 * Returns '0' for null/NaN/zero.
 * @param {number} b - Bytes
 * @returns {string}
 */
export function fmtBytesShort(b) {
    if (b == null || isNaN(b) || b === 0) return '0';
    const { unit, div } = scaleBytes(b);
    if (unit === 'B') return b.toFixed(0) + 'B';
    if (unit === 'GB') return (b / div).toFixed(1) + 'G';
    return (b / div).toFixed(0) + unit[0];
}

/**
 * Parse a size string to bytes, loosely: the number+unit may appear anywhere
 * in the string, the result keeps fractions, and unit-less input falls back
 * to parseFloat (e.g. "1.5 KB" -> 1536, "1234" -> 1234).
 * @param {string} size - Size string with unit
 * @returns {number} Size in bytes
 */
export function parseSizeToBytes(size) {
    if (!size || typeof size !== 'string') return 0;
    const match = size.match(/([\d.]+)\s*(KB|MB|GB|TB|B)/i);
    if (!match) return parseFloat(size) || 0;
    return parseFloat(match[1]) * BYTE_UNITS[match[2].toUpperCase()];
}

/**
 * Parse a size string to bytes, strictly: the whole string must be a number
 * with an optional unit (default B); the result is rounded to an integer and
 * any mismatch yields 0 (e.g. "50 MB" -> 52428800, "50 MB extra" -> 0).
 * @param {string} sizeStr - Size string with unit
 * @returns {number} Size in bytes
 */
export function parseSizeToBytesStrict(sizeStr) {
    if (!sizeStr || typeof sizeStr !== 'string') return 0;
    const match = sizeStr.match(/^([\d.]+)\s*(B|KB|MB|GB|TB)?$/i);
    if (!match) return 0;
    const value = parseFloat(match[1]);
    const unit = (match[2] || 'B').toUpperCase();
    return Math.round(value * (BYTE_UNITS[unit] || 1));
}
