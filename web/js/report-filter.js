// Report mode time filtering - client-side filtering and re-aggregation
// This module handles time range filtering for the HTML report export

import { fmtQueryDuration } from './utils.js';
import { parseSizeToBytesStrict, fmtBytesFull } from './format.js';

/**
 * One logged query execution from the payload's
 * `sql_performance.executions` (see docs/web-data-contract.md).
 * @typedef {Object} Execution
 * @property {string} timestamp - ISO timestamp ("YYYY-MM-DDTHH:MM:SS")
 * @property {number} duration_ms - Execution duration in milliseconds
 * @property {string} query_id - Stable query handle (e.g. "se-aehiJm")
 */

/**
 * One completed session from the payload's `connections.session_events`.
 * @typedef {Object} SessionEvent
 * @property {string} s - Session start (ISO timestamp)
 * @property {string} e - Session end (ISO timestamp)
 */

// Store original unfiltered data
let originalData = null;

/**
 * Store the original data for filtering
 * @param {Object} data - The original analysis data (decompressed payload)
 */
export function setOriginalReportData(data) {
    // Deep clone to avoid mutations
    originalData = JSON.parse(JSON.stringify(data));
}

/**
 * Get the original unfiltered data
 * @returns {Object|null} The original data (null before setOriginalReportData)
 */
export function getOriginalReportData() {
    return originalData;
}

/**
 * Parse a timestamp string to Date object
 * @param {string} ts - Timestamp string, display ("2025-01-01 12:00:00")
 *   or ISO ("2025-01-01T12:00:00") format
 * @returns {Date|null} Date object, or null for empty input
 */
function parseTimestamp(ts) {
    if (!ts) return null;
    // Handle both "2025-01-01 12:00:00" and ISO formats
    return new Date(ts.replace(' ', 'T'));
}

/**
 * Filter events by time range. Events may be bare timestamp strings
 * (e.g. `checkpoints.events`) or objects carrying a timestamp field.
 * @param {Array<string|Object>} events - Events to filter
 * @param {Date} beginDate - Start of time range (inclusive)
 * @param {Date} endDate - End of time range (inclusive)
 * @param {string} [tsField='timestamp'] - Timestamp field name for objects
 * @returns {Array<string|Object>} Filtered events (same element shape)
 */
function filterEventsByTime(events, beginDate, endDate, tsField = 'timestamp') {
    if (!events || !Array.isArray(events)) return [];

    return events.filter(event => {
        const ts = parseTimestamp(typeof event === 'string' ? event : event[tsField]);
        if (!ts) return false;
        return ts >= beginDate && ts <= endDate;
    });
}

/**
 * Calculate statistics from an array of durations
 * @param {number[]} durations - Duration values in milliseconds
 * @returns {{total: number, min: number, max: number, avg: number,
 *   median: number, p99: number}} All values in milliseconds
 */
function calculateDurationStats(durations) {
    if (!durations || durations.length === 0) {
        return { total: 0, min: 0, max: 0, avg: 0, median: 0, p99: 0 };
    }

    const sorted = [...durations].sort((a, b) => a - b);
    const total = durations.reduce((sum, d) => sum + d, 0);
    const min = sorted[0];
    const max = sorted[sorted.length - 1];
    const avg = total / durations.length;

    // Median
    const mid = Math.floor(sorted.length / 2);
    const median = sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;

    // P99
    const p99Index = Math.floor(sorted.length * 0.99);
    const p99 = sorted[Math.min(p99Index, sorted.length - 1)];

    return { total, min, max, avg, median, p99 };
}

/**
 * Re-aggregate SQL performance data from filtered executions: totals,
 * percentiles and per-query stats are recomputed; queries with no
 * execution left in range are dropped.
 * @param {Object} original - Original sql_performance object
 * @param {Execution[]} filteredExecutions - Executions kept by the filter
 * @returns {Object|null} Re-aggregated sql_performance (null if no original)
 */
function reaggregateSqlPerformance(original, filteredExecutions) {
    if (!original) return null;

    const result = { ...original };
    result.executions = filteredExecutions;

    // Recalculate query stats from filtered executions
    const queryStats = new Map();
    const durations = [];

    for (const exec of filteredExecutions) {
        durations.push(exec.duration_ms);

        if (exec.query_id) {
            if (!queryStats.has(exec.query_id)) {
                queryStats.set(exec.query_id, { count: 0, total: 0, max: 0 });
            }
            const stat = queryStats.get(exec.query_id);
            stat.count++;
            stat.total += exec.duration_ms;
            stat.max = Math.max(stat.max, exec.duration_ms);
        }
    }

    // Update aggregate metrics
    const stats = calculateDurationStats(durations);
    result.total_queries_parsed = filteredExecutions.length;
    result.total_unique_queries = queryStats.size;
    // Match the backend's formatQueryDuration so the re-aggregated stat cards
    // render identically to the unfiltered ones (no lost hour/day tiers).
    result.total_query_duration = fmtQueryDuration(stats.total);
    result.query_min_duration = fmtQueryDuration(stats.min);
    result.query_max_duration = fmtQueryDuration(stats.max);
    result.query_median_duration = fmtQueryDuration(stats.median);
    result.query_99th_percentile = fmtQueryDuration(stats.p99);

    // Top 1% slow queries
    const p99Threshold = stats.p99;
    result.top_1_percent_slow_queries = durations.filter(d => d >= p99Threshold).length;

    // Update per-query stats
    if (original.queries) {
        result.queries = original.queries.map(q => {
            const stat = queryStats.get(q.id);
            if (stat) {
                return {
                    ...q,
                    count: stat.count,
                    total_time_ms: stat.total,
                    avg_time_ms: stat.total / stat.count,
                    max_time_ms: stat.max
                };
            }
            // Query not in filtered range
            return { ...q, count: 0, total_time_ms: 0, avg_time_ms: 0, max_time_ms: 0 };
        }).filter(q => q.count > 0);
    }

    return result;
}

/**
 * Re-aggregate temp files data from filtered events (message count,
 * total and average size; sizes are re-parsed from their display strings).
 * @param {Object} original - Original temp_files object
 * @param {Array<{timestamp: string, size: string, query_id: string}>} filteredEvents - Filtered events
 * @returns {Object|null} Re-aggregated temp_files (null if no original)
 */
function reaggregateTempFiles(original, filteredEvents) {
    if (!original) return null;

    const result = { ...original };
    result.events = filteredEvents;

    // Recalculate totals (max is recomputed too — it was previously left stale).
    let totalBytes = 0, maxBytes = 0;
    for (const event of filteredEvents) {
        const b = parseSizeToBytesStrict(event.size);
        totalBytes += b;
        if (b > maxBytes) maxBytes = b;
    }

    result.total_messages = filteredEvents.length;
    // fmtBytesFull matches the backend FormatBytes (2 decimals) so the cards,
    // which render these raw, keep their format after filtering.
    result.total_size = fmtBytesFull(totalBytes);
    result.avg_size = filteredEvents.length > 0 ? fmtBytesFull(totalBytes / filteredEvents.length) : '0 B';
    result.max_size = filteredEvents.length > 0 ? fmtBytesFull(maxBytes) : '-';

    return result;
}

/**
 * Re-aggregate checkpoints data from filtered events: total count,
 * wal_distances, warning_events and the per-trigger `types` map (count,
 * percentage, rate_per_hour) are recomputed; empty types are dropped.
 * @param {Object} original - Original checkpoints object
 * @param {string[]} filteredEvents - Checkpoint timestamps kept in range
 * @param {Date} beginDate - Filter start
 * @param {Date} endDate - Filter end
 * @returns {Object|null} Re-aggregated checkpoints (null if no original)
 */
function reaggregateCheckpoints(original, filteredEvents, beginDate, endDate) {
    if (!original) return null;

    const result = { ...original };
    result.events = filteredEvents;
    result.total_checkpoints = filteredEvents.length;

    // Filter WAL distances by time range
    if (original.wal_distances) {
        result.wal_distances = filterEventsByTime(
            original.wal_distances, beginDate, endDate
        );
    }

    // Filter warning events by time range, and keep warning_count in step —
    // the "Too Frequent" card reads warning_count, which used to stay at the
    // full-log value while its events were filtered (contradictory card).
    if (original.warning_events) {
        result.warning_events = filterEventsByTime(
            original.warning_events, beginDate, endDate
        );
        result.warning_count = result.warning_events.length;
    }

    // Recalculate types from filtered events
    if (original.types) {
        const durationHours = (endDate - beginDate) / (1000 * 60 * 60);
        result.types = {};

        for (const [typeName, typeData] of Object.entries(original.types)) {
            const filteredTypeEvents = filterEventsByTime(
                typeData.events || [],
                beginDate,
                endDate
            );

            if (filteredTypeEvents.length > 0) {
                result.types[typeName] = {
                    ...typeData,
                    count: filteredTypeEvents.length,
                    percentage: filteredEvents.length > 0
                        ? (filteredTypeEvents.length / filteredEvents.length) * 100
                        : 0,
                    rate_per_hour: durationHours > 0
                        ? filteredTypeEvents.length / durationHours
                        : 0,
                    events: filteredTypeEvents
                };
            }
        }
    }

    return result;
}

// Format a session duration (ms) as a Go-Duration-like string that fmtDur
// re-renders like the backend's time.Duration.String() (fractional seconds
// preserved so fmtDur rounds the same way).
function fmtSessionDuration(ms) {
    if (ms < 1000) return Math.round(ms) + 'ms';
    const totalSec = ms / 1000;
    const h = Math.floor(totalSec / 3600);
    const m = Math.floor((totalSec % 3600) / 60);
    const s = totalSec % 60;
    const sStr = (Number.isInteger(s) ? String(s) : s.toFixed(3)) + 's';
    return (h ? h + 'h' : '') + (h || m ? m + 'm' : '') + sStr;
}

// Session-duration histogram buckets — same boundaries/labels the backend
// emits (analysis/connections.go newSessionDistribution).
const SESSION_BUCKETS = [
    { label: '< 1s', max: 1000 },
    { label: '1s - 1min', max: 60000 },
    { label: '1min - 30min', max: 1800000 },
    { label: '30min - 2h', max: 7200000 },
    { label: '2h - 5h', max: 18000000 },
    { label: '> 5h', max: Infinity },
];

function bucketSessionDurations(durationsMs) {
    const dist = {};
    for (const b of SESSION_BUCKETS) dist[b.label] = 0;
    for (const d of durationsMs) {
        for (const b of SESSION_BUCKETS) { if (d < b.max) { dist[b.label]++; break; } }
    }
    return dist;
}

/**
 * Build a session-stats object ({count, min/max/avg/median/cumulated
 * duration}, formatted strings) from a list of durations in ms — same shape
 * as the backend's SessionStatsJSON. Shared by the overall `session_stats`
 * and the per-user/database/host recompute so both use identical rounding.
 * @param {number[]} durationsMs
 * @returns {{count: number, min_duration: string, max_duration: string,
 *   avg_duration: string, median_duration: string, cumulated_duration: string}}
 */
function computeSessionStats(durationsMs) {
    const n = durationsMs.length;
    if (!n) {
        const zero = '0s';
        return {
            count: 0, min_duration: zero, max_duration: zero,
            avg_duration: zero, median_duration: zero, cumulated_duration: zero,
        };
    }
    const sorted = [...durationsMs].sort((a, b) => a - b);
    const sum = sorted.reduce((a, b) => a + b, 0);
    const median = n % 2
        ? sorted[(n - 1) / 2]
        : (sorted[n / 2 - 1] + sorted[n / 2]) / 2;
    return {
        count: n,
        min_duration: fmtSessionDuration(sorted[0]),
        max_duration: fmtSessionDuration(sorted[n - 1]),
        avg_duration: fmtSessionDuration(sum / n),
        median_duration: fmtSessionDuration(median),
        cumulated_duration: fmtSessionDuration(sum),
    };
}

/**
 * Re-aggregate connections data: connection count and hourly rate are
 * recomputed; session_events are kept when they OVERLAP the range
 * (start <= end-of-range and end >= start-of-range), not only when fully
 * contained. The per-user/database/host session tables are rebuilt too, via
 * the interned entity indices (`u`/`db`/`h`) each session_events item
 * carries — resolved against the payload's session_users/session_databases/
 * session_hosts reverse tables — so they re-scope like every other stat
 * instead of staying pinned to the full log.
 * @param {Object} original - Original connections object
 * @param {string[]} filteredConnections - Connection timestamps in range
 * @param {Date} beginDate - Filter start
 * @param {Date} endDate - Filter end
 * @returns {Object|null} Re-aggregated connections (null if no original)
 */
function reaggregateConnections(original, filteredConnections, beginDate, endDate) {
    if (!original) return null;

    const result = { ...original };
    result.connections = filteredConnections;
    result.connection_count = filteredConnections.length;

    // Recalculate rate
    const durationHours = (endDate - beginDate) / (1000 * 60 * 60);
    result.avg_connections_per_hour = durationHours > 0
        ? (filteredConnections.length / durationHours).toFixed(2)
        : '0';

    // Session events overlapping the window drive the concurrent-sessions
    // chart and its peak. These are TIME-based (who is connected when), so a
    // second-precision session_events array re-scopes them faithfully.
    if (original.session_events) {
        const overlap = original.session_events.filter(ev => {
            const start = parseTimestamp(ev.s);
            const end = parseTimestamp(ev.e);
            return start && end && start <= endDate && end >= beginDate;
        });
        result.session_events = overlap;

        // Peak concurrency within the window: sweep start/end events clipped to
        // [begin, end] and track the running maximum.
        const sweep = [];
        for (const ev of overlap) {
            const s = parseTimestamp(ev.s), e = parseTimestamp(ev.e);
            const start = s < beginDate ? beginDate : s;
            const end = e > endDate ? endDate : e;
            if (end < start) continue;
            sweep.push({ t: start.getTime(), d: 1 });
            sweep.push({ t: end.getTime(), d: -1 });
        }
        // At a tie, a disconnect frees its slot before a new connect counts.
        sweep.sort((a, b) => a.t - b.t || a.d - b.d);
        let cur = 0, peak = 0, peakT = null;
        for (const ev of sweep) { cur += ev.d; if (cur > peak) { peak = cur; peakT = ev.t; } }
        result.peak_concurrent_sessions = peak;
        if (peakT != null) {
            const d = new Date(peakT), p = n => String(n).padStart(2, '0');
            result.peak_concurrent_timestamp = `${d.getFullYear()}-${p(d.getMonth() + 1)}-` +
                `${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
        }

        // Duration stats from the precise per-session `d` (ms) — the s/e strings
        // are second-truncated, but `d` keeps PostgreSQL's sub-second precision.
        // Count sessions that DISCONNECTED in the window (e in range), skipping
        // orphans still open at the log's end (e at the original log end, no
        // real disconnect) so the numbers track the backend's methodology.
        const logEnd = originalData?.summary?.end_date
            ? parseTimestamp(originalData.summary.end_date) : null;
        const users = original.session_users || [];
        const databases = original.session_databases || [];
        const hosts = original.session_hosts || [];
        const durations = [];
        const byUser = new Map(), byDatabase = new Map(), byHost = new Map();
        const bucket = (map, name, ms) => {
            let arr = map.get(name);
            if (!arr) map.set(name, arr = []);
            arr.push(ms);
        };
        for (const ev of original.session_events) {
            if (typeof ev.d !== 'number') continue;
            const e = parseTimestamp(ev.e);
            if (!e || e < beginDate || e > endDate) continue;
            if (logEnd && e >= logEnd) continue;
            durations.push(ev.d);

            // Per-entity breakdown, keyed by name resolved from the interned
            // index. Index 0 ("unknown") is skipped in each dimension
            // independently, matching the backend's per-entity maps, which
            // only ever hold sessions with a known entity for that dimension.
            if (ev.u > 0 && users[ev.u]) bucket(byUser, users[ev.u], ev.d);
            if (ev.db > 0 && databases[ev.db]) bucket(byDatabase, databases[ev.db], ev.d);
            if (ev.h > 0 && hosts[ev.h]) bucket(byHost, hosts[ev.h], ev.d);
        }
        result.disconnection_count = durations.length;
        const stats = computeSessionStats(durations);
        result.session_stats = stats;
        result.avg_session_time = stats.avg_duration;
        result.session_distribution = bucketSessionDurations(durations);

        const toStatsMap = (byEntity) => {
            const out = {};
            for (const [name, arr] of byEntity) out[name] = computeSessionStats(arr);
            return out;
        };
        result.sessions_by_user = toStatsMap(byUser);
        result.sessions_by_database = toStatsMap(byDatabase);
        result.sessions_by_host = toStatsMap(byHost);
    }

    return result;
}

/**
 * Apply a time filter to the stored report data. Re-aggregates
 * sql_performance, temp_files, checkpoints and connections from their
 * event arrays and rewrites the summary time range; other sections
 * (locks, maintenance, events, ...) are passed through unchanged.
 * The result carries `_timeFiltered: true` and `_filterRange`.
 * @param {string} beginStr - Begin timestamp string ("YYYY-MM-DD HH:MM:SS")
 * @param {string} endStr - End timestamp string (same format)
 * @returns {Object|null} Filtered and re-aggregated copy of the payload;
 *   the original on an invalid range; null when nothing was stored
 */
export function applyReportTimeFilter(beginStr, endStr) {
    if (!originalData) {
        console.warn('[report-filter] No original data stored');
        return null;
    }

    const beginDate = parseTimestamp(beginStr);
    const endDate = parseTimestamp(endStr);

    if (!beginDate || !endDate) {
        console.warn('[report-filter] Invalid time range:', beginStr, endStr);
        return originalData;
    }

    console.log('[report-filter] Filtering:', beginStr, 'to', endStr);

    // Start with a copy of original data
    const filtered = JSON.parse(JSON.stringify(originalData));

    // Update summary time range
    if (filtered.summary) {
        filtered.summary.start_date = beginStr;
        filtered.summary.end_date = endStr;

        const durationMs = endDate - beginDate;
        const hours = Math.floor(durationMs / (1000 * 60 * 60));
        const mins = Math.floor((durationMs % (1000 * 60 * 60)) / (1000 * 60));
        const secs = Math.floor((durationMs % (1000 * 60)) / 1000);
        filtered.summary.duration = `${hours}h${mins}m${secs}s`;
    }

    // Filter SQL performance executions
    if (originalData.sql_performance?.executions) {
        const filteredExecs = filterEventsByTime(
            originalData.sql_performance.executions,
            beginDate,
            endDate
        );
        filtered.sql_performance = reaggregateSqlPerformance(
            originalData.sql_performance,
            filteredExecs
        );
    }

    // Filter temp files events
    if (originalData.temp_files?.events) {
        const filteredEvents = filterEventsByTime(
            originalData.temp_files.events,
            beginDate,
            endDate
        );
        filtered.temp_files = reaggregateTempFiles(
            originalData.temp_files,
            filteredEvents
        );
    }

    // Filter checkpoint events
    if (originalData.checkpoints?.events) {
        const filteredEvents = filterEventsByTime(
            originalData.checkpoints.events,
            beginDate,
            endDate
        );
        filtered.checkpoints = reaggregateCheckpoints(
            originalData.checkpoints,
            filteredEvents,
            beginDate,
            endDate
        );
    }

    // Filter connection events
    if (originalData.connections?.connections) {
        const filteredConnections = filterEventsByTime(
            originalData.connections.connections,
            beginDate,
            endDate
        );
        filtered.connections = reaggregateConnections(
            originalData.connections,
            filteredConnections,
            beginDate,
            endDate
        );
    }

    // Mark as filtered
    filtered._timeFiltered = true;
    filtered._filterRange = { begin: beginStr, end: endStr };

    return filtered;
}

/**
 * Reset to original unfiltered data
 * @returns {Object|null} Original data (null before setOriginalReportData)
 */
export function resetReportTimeFilter() {
    return originalData;
}
