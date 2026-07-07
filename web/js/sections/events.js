// Events section: severity tabs (ERROR/FATAL/PANIC/WARNING) with grouped
// event tables, plus the muted noise-severity indicator row.

import { fmt, esc, wholeLogBadge } from '../utils.js';

export function buildEventsSection(data) {
	// Filter logic
	const onlyErrors = data.meta?.sections && data.meta.sections.includes('errors') && !data.meta.sections.includes('events') && !data.meta.sections.includes('all');

	// Prepare data
	const topEvents = data.top_events || [];
	const bySeverity = {};
	topEvents.forEach(e => {
		if (!bySeverity[e.severity]) bySeverity[e.severity] = [];
		bySeverity[e.severity].push(e);
	});

	// Stats for bars
	const eventsArr = data.events || []; // {type, count}
	const summaryMap = {};
	eventsArr.forEach(e => { summaryMap[e.type] = e.count; });

	// Define Groups
	const criticalSeverities = ['PANIC', 'FATAL', 'ERROR', 'WARNING'];
	const noiseSeverities = ['NOTICE', 'LOG', 'INFO', 'DEBUG'];

	// Determine default active tab
	let defaultTab = 'ERROR';
	if (summaryMap['ERROR'] > 0) defaultTab = 'ERROR';
	else if (summaryMap['FATAL'] > 0) defaultTab = 'FATAL';
	else if (summaryMap['PANIC'] > 0) defaultTab = 'PANIC';
	else if (summaryMap['WARNING'] > 0) defaultTab = 'WARNING';

	// Generate Tabs HTML
	let tabsHtml = criticalSeverities.map(sev => {
		const count = summaryMap[sev] || 0;
		const isSelected = sev === defaultTab ? 'selected' : '';
		const cls = sev.toLowerCase();
		return `<ql-tab class="${cls}" ${isSelected}>${sev}<ql-badge>${fmt(count)}</ql-badge></ql-tab>`;
	}).join('');

	// Generate Panels HTML
	let panelsHtml = criticalSeverities.map(sev => {
		const count = summaryMap[sev] || 0;
		const sevEvents = bySeverity[sev] || [];

		let innerContent = '';

		if (count === 0 || sevEvents.length === 0) {
			innerContent = `
			<div style="padding: 2rem; text-align: center; color: var(--text-muted); font-style: italic;">
				No ${sev} events recorded
			</div>`;
		} else {
			// Group by Class
			const byClass = {};
			sevEvents.forEach(e => {
				const cls = e.sql_state_class || 'Unclassified';
				if (!byClass[cls]) byClass[cls] = [];
				byClass[cls].push(e);
			});
			const classes = Object.keys(byClass).sort();
			if (classes.includes('Unclassified')) {
				classes.splice(classes.indexOf('Unclassified'), 1);
				classes.push('Unclassified');
			}

			let rows = '';
			const sevColor = sev === 'ERROR' ? 'var(--danger)' : sev === 'FATAL' || sev === 'PANIC' ? 'var(--purple)' : sev === 'WARNING' ? 'var(--warning)' : 'var(--text-muted)';

			classes.forEach(cls => {
				const classEvents = byClass[cls];
				classEvents.sort((a, b) => b.count - a.count);

				let code = "";
				let desc = cls;
				if (cls === 'Unclassified') { code = ''; desc = ''; }
				else if (cls.match(/^\w{2} - /)) { code = cls.substring(0, 2); desc = cls.substring(5); }
				else if (cls.length === 2) { code = cls; desc = ''; }

				classEvents.forEach(e => {
					// Resolve original index in data.top_events so the modal
					// can grab the full Example + timestamps array.
					const origIdx = topEvents.indexOf(e);
					rows += `
					<tr class="event-row" onclick="showEventDetail(${origIdx})" style="cursor:pointer;" title="Click for details">
						<td style="width: 50px; vertical-align: top; padding: 0.25rem 0.5rem;">
							${code ? `<span class="event-class-badge" style="border-color:${sevColor}; color:${sevColor};">${code}</span>` : ''}
						</td>
						<td style="vertical-align: top; padding: 0.25rem 0.5rem;">
							${desc ? `<div style="font-size: 0.6rem; font-weight: 600; color: var(--text-muted); margin-bottom: 2px;">${esc(desc)}</div>` : ''}
							<div class="event-msg-text">${esc(e.message)}</div>
						</td>
						<td class="num" style="width: 60px; vertical-align: top; padding: 0.25rem 0.5rem; font-weight: 600;">${fmt(e.count)}</td>
					</tr>`;
				});
			});

			innerContent = `
			<div class="table-container" style="max-height: 200px;">
				<table class="data-table" style="width: 100%;">
					${rows}
				</table>
			</div>`;
		}

		return `<ql-panel>${innerContent}</ql-panel>`;
	}).join('');

	// Generate Indicators HTML (Right)
	let indicatorsHtml = '';
	if (!onlyErrors) {
		indicatorsHtml = '<div class="events-indicators"><div class="tabs indicators-group">';
		noiseSeverities.forEach(sev => {
			const count = summaryMap[sev] || 0;
			const cls = sev.toLowerCase();
			indicatorsHtml += `<div class="tab indicator ${cls}">${sev}<span class="tab-badge">${fmt(count)}</span></div>`;
		});
		indicatorsHtml += '</div></div>';
	}

	// Under a report time filter the message tables/sparklines re-scope (from
	// top_events[].timestamps) but the severity distribution + noise counters
	// are whole-log population counts with no per-item timestamps — annotate.
	const wholeLogNote = data._wholeLog?.events
		? wholeLogBadge('Severity totals and the noise counters cover the whole log; the message tables and charts are time-scoped.')
		: '';

	return `
	<div class="section" id="events">
		<div class="section-header">Events${wholeLogNote}</div>
		<div class="section-body events-section">
			${indicatorsHtml}
			<ql-tabs>
				${tabsHtml}
				${panelsHtml}
			</ql-tabs>
		</div>
	</div>
	`;
}
