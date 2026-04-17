// Report mode time filtering - client-side filtering and re-aggregation
// This module handles time range filtering for the HTML report export

import { fmtBytes, fmtDuration, fmtMs } from './utils.js';

// Store original unfiltered data
let originalData = null;

/**
 * Store the original data for filtering
 * @param {Object} data - The original analysis data
 */
export function setOriginalReportData(data) {
    // Deep clone to avoid mutations
    originalData = JSON.parse(JSON.stringify(data));
}

/**
 * Get the original unfiltered data
 * @returns {Object} The original data
 */
export function getOriginalReportData() {
    return originalData;
}

/**
 * Parse a timestamp string to Date object
 * @param {string} ts - Timestamp string (e.g., "2025-01-01 12:00:00")
 * @returns {Date} Date object
 */
function parseTimestamp(ts) {
    if (!ts) return null;
    // Handle both "2025-01-01 12:00:00" and ISO formats
    return new Date(ts.replace(' ', 'T'));
}

/**
 * Filter events by time range
 * @param {Array} events - Array of events with timestamp property
 * @param {Date} beginDate - Start of time range
 * @param {Date} endDate - End of time range
 * @param {string} tsField - Name of timestamp field (default: 'timestamp')
 * @returns {Array} Filtered events
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
 * Parse size string to bytes (e.g., "50 MB" -> 52428800)
 * @param {string} sizeStr - Size string with unit
 * @returns {number} Size in bytes
 */
function parseSizeToBytes(sizeStr) {
    if (!sizeStr || typeof sizeStr !== 'string') return 0;
    const match = sizeStr.match(/^([\d.]+)\s*(B|KB|MB|GB|TB)?$/i);
    if (!match) return 0;

    const value = parseFloat(match[1]);
    const unit = (match[2] || 'B').toUpperCase();
    const multipliers = { B: 1, KB: 1024, MB: 1024**2, GB: 1024**3, TB: 1024**4 };
    return Math.round(value * (multipliers[unit] || 1));
}

/**
 * Calculate statistics from an array of durations
 * @param {Array<number>} durations - Array of duration values in ms
 * @returns {Object} Statistics object
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
 * Re-aggregate SQL performance data from filtered executions
 * @param {Object} original - Original sql_performance object
 * @param {Array} filteredExecutions - Filtered executions
 * @returns {Object} Re-aggregated sql_performance
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
    result.total_query_duration = fmtDuration(stats.total / 1000);
    result.query_min_duration = fmtMs(stats.min);
    result.query_max_duration = fmtMs(stats.max);
    result.query_median_duration = fmtMs(stats.median);
    result.query_99th_percentile = fmtMs(stats.p99);

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
 * Re-aggregate temp files data from filtered events
 * @param {Object} original - Original temp_files object
 * @param {Array} filteredEvents - Filtered events
 * @returns {Object} Re-aggregated temp_files
 */
function reaggregateTempFiles(original, filteredEvents) {
    if (!original) return null;

    const result = { ...original };
    result.events = filteredEvents;

    // Recalculate totals
    let totalBytes = 0;
    for (const event of filteredEvents) {
        totalBytes += parseSizeToBytes(event.size);
    }

    result.total_messages = filteredEvents.length;
    result.total_size = fmtBytes(totalBytes);
    result.avg_size = filteredEvents.length > 0 ? fmtBytes(totalBytes / filteredEvents.length) : '0 B';

    return result;
}

/**
 * Re-aggregate checkpoints data from filtered events
 * @param {Object} original - Original checkpoints object
 * @param {Array} filteredEvents - Filtered timestamp strings
 * @param {Date} beginDate - Filter start
 * @param {Date} endDate - Filter end
 * @returns {Object} Re-aggregated checkpoints
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

    // Filter warning events by time range
    if (original.warning_events) {
        result.warning_events = filterEventsByTime(
            original.warning_events, beginDate, endDate
        );
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

/**
 * Re-aggregate connections data from filtered events
 * @param {Object} original - Original connections object
 * @param {Array} filteredConnections - Filtered connection timestamps
 * @param {Date} beginDate - Filter start
 * @param {Date} endDate - Filter end
 * @returns {Object} Re-aggregated connections
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

    // Filter session events if present (fields: s=start, e=end)
    if (original.session_events) {
        result.session_events = original.session_events.filter(ev => {
            const start = parseTimestamp(ev.s);
            const end = parseTimestamp(ev.e);
            if (!start || !end) return false;
            return start <= endDate && end >= beginDate;
        });
    }

    return result;
}

/**
 * Apply time filter to report data
 * @param {string} beginStr - Begin timestamp string
 * @param {string} endStr - End timestamp string
 * @returns {Object} Filtered and re-aggregated data
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
 * @returns {Object} Original data
 */
export function resetReportTimeFilter() {
    return originalData;
}
