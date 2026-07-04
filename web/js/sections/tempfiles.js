// Temp Files section: count/size stats, combined count+size activity
// chart, and the top-queries-by-temp-usage table.

import { fmt, esc, truncQuery, buildNoDataMessage } from '../utils.js';
import { chartData, buildChartContainer } from '../charts.js';

export function buildTempFilesSection(data) {
    const tf = data.temp_files;
    // JSON has: total_messages, total_size, avg_size, events (array with timestamps), queries (array)
    if (!tf || tf.total_messages === 0) {
        return `
            <div class="section" id="temp_files">
                <div class="section-header muted">Temp Files</div>
                <div class="section-body">
                    ${buildNoDataMessage('<code>log_temp_files = 0</code>')}
                </div>
            </div>
        `;
    }
    const hasQueries = tf.queries?.length > 0;
    const hasEvents = tf.events?.length > 0;
    // Store full events for combined chart (count + size)
    if (hasEvents) {
        chartData.set('chart-tempfiles', { type: 'combined-tempfiles', events: tf.events });
    }
    return `
        <div class="section" id="temp_files">
            <div class="section-header">Temp Files</div>
            <div class="section-body">
                <div class="stat-grid">
                    <div class="stat-card"><div class="stat-value">${fmt(tf.total_messages)}</div><div class="stat-label">Count</div></div>
                    <div class="stat-card"><div class="stat-value">${tf.total_size}</div><div class="stat-label">Total</div></div>
                    <div class="stat-card"><div class="stat-value">${tf.avg_size}</div><div class="stat-label">Avg</div></div>
                    <div class="stat-card"><div class="stat-value">${tf.max_size || '-'}</div><div class="stat-label">Max</div></div>
                </div>
                ${hasEvents ? `
                    ${buildChartContainer('chart-tempfiles', 'Temp File Activity', { showFilterBtn: true, tooltip: 'Temp file count and cumulative size over time. Created when queries exceed work_mem.' })}
                    <div class="chart-legend" style="display:flex;gap:16px;justify-content:center;margin-top:4px;font-size:12px;">
                        <span><span style="display:inline-block;width:12px;height:12px;background:var(--chart-bar);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Count</span>
                        <span><span style="display:inline-block;width:12px;height:12px;background:var(--accent);border-radius:2px;vertical-align:middle;margin-right:4px;"></span>Size</span>
                        <span><span style="display:inline-block;width:16px;height:0;border-top:2px dashed var(--text-muted);vertical-align:middle;margin-right:4px;"></span>Median</span>
                    </div>
                ` : ''}
                ${hasQueries ? `
                    <div class="subsection">
                        <div class="subsection-title">Top Queries</div>
                        <div class="table-container" style="max-height: 180px;">
                            <table>
                                <thead><tr>
                                    <th>Query</th>
                                    <th class="num">Count</th>
                                    <th class="num">Total Size</th>
                                </tr></thead>
                                <tbody>
                                    ${tf.queries.slice(0, 10).map(q => `
                                        <tr>
                                            <td class="query-cell" onclick="showQueryModal('${esc(q.id || '')}')">${esc(truncQuery(q.normalized_query || ''))}</td>
                                            <td class="num">${fmt(q.count)}</td>
                                            <td class="num">${q.total_size}</td>
                                        </tr>
                                    `).join('')}
                                </tbody>
                            </table>
                        </div>
                    </div>
                ` : ''}
            </div>
        </div>
    `;
}
