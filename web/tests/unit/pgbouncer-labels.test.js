// Freezes the naming contract between the HTML pgBouncer section and the
// text/Markdown pooler reports: the same figure must carry the same name
// in every format, and a truncated breakdown must say so.
//
// The canonical wording is the text report's (output/pgbouncer_text.go).
// Where a card label has to be shorter than the text label it is listed
// in DOCUMENTED_DIVERGENCES below, so a divergence is always a decision
// and never a drift.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { buildPgBouncerSection as buildSections } from '../../js/sections/pgbouncer.js';

// The payloads below each describe one pooler; the section takes the list.
const buildPgBouncerSection = (d) => buildSections({ ...d, pgbouncer: [d.pgbouncer] });

// esc() needs document.createElement. node --test runs each file in its own
// process, so the same minimal stub report-filter-triggering.test.js uses —
// escaping & < > exactly like textContent->innerHTML — is safe here.
if (typeof globalThis.document === 'undefined') {
    globalThis.document = {
        createElement() {
            let text = '';
            return {
                set textContent(v) { text = v == null ? '' : String(v); },
                get innerHTML() {
                    return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
                },
            };
        },
    };
}

const payload = {
    lines: 500,
    lines_read: 540,
    lines_unrecognized: 12,
    debug_lines: 9,
    start_date: '2026-09-20 13:59:04',
    end_date: '2026-09-20 14:12:50',
    total_session_seconds: 362,
    stats_sample_count: 3,
    avg_queries_per_sec: 48.3,
    max_queries_per_sec: 58,
    login_attempts: 69,
    server_connections: 10,
    sessions: 68,
    reserve_pool_activations: 6,
    total_errors: 45,
};

// The card labels, keyed by the text report's wording for the same
// figure: the stats-line averages, worded as the text report's REQUEST
// THROUGHPUT, QUERY TIMING and POOL WAIT rows word them, then the
// counters the events feed.
const CANONICAL_CARD_LABELS = [
    'Transactions',
    'Queries',
    'Avg transaction time',
    'Avg query time',
    'Avg pool wait time',
    'Network in',
    'Network out',
    'Login attempts',
    'Completed sessions',
    'Average session time',
    'Server connections opened',
    'Reserve pool activations',
    'Errors',
];

// The line accounting is a line of text under the pooler's identity, not
// cards, but it keeps the text report's wording.
const READ_LINE_LABELS = [
    'Log lines read',
    'Log entries',
    'Unrecognized lines',
    'Filtered-out lines',
    'Debug/noise entries',
    'Stats snapshots',
];

// Card labels the text report has no single counterpart for.
const DOCUMENTED_DIVERGENCES = [];

const BANNED = ['Server Conns', 'Reserve Pool<', 'Avg Queries', 'Peak Queries'];

function labelsOf(html) {
    return [...html.matchAll(/<div class="stat-label"[^>]*>([^<]*)<\/div>/g)].map(m => m[1].trim());
}

test('every stat card carries the text report\'s wording', () => {
    const html = buildPgBouncerSection({ pgbouncer: payload });
    const labels = labelsOf(html);
    for (const want of CANONICAL_CARD_LABELS) {
        assert.ok(labels.includes(want), `card label ${JSON.stringify(want)} missing, got ${JSON.stringify(labels)}`);
    }
    for (const label of labels) {
        assert.ok(
            CANONICAL_CARD_LABELS.includes(label) || DOCUMENTED_DIVERGENCES.includes(label),
            `card label ${JSON.stringify(label)} matches no text-report label and is not a documented divergence`,
        );
    }
});

test('the line accounting carries the text report\'s wording', () => {
    const html = buildPgBouncerSection({ pgbouncer: { ...payload, lines_read: 130, lines: 100, lines_unrecognized: 20, debug_lines: 7 } });
    for (const want of READ_LINE_LABELS) {
        assert.ok(html.includes(`${want} <b>`), `line accounting ${JSON.stringify(want)} missing`);
    }
});

test('the abbreviations the text report never uses are gone', () => {
    const html = buildPgBouncerSection({ pgbouncer: payload });
    for (const banned of BANNED) {
        assert.ok(!html.includes(banned), `HTML still renders ${JSON.stringify(banned)}`);
    }
});

test('a truncated breakdown says how many rows it dropped', () => {
    const connections_per_user = {};
    for (let i = 0; i < 150; i++) connections_per_user[`user${String(i).padStart(3, '0')}`] = 150 - i;
    const html = buildPgBouncerSection({ pgbouncer: { ...payload, connections_per_user } });
    assert.ok(html.includes('… and 140 more'), 'the 140 dropped rows are not declared');
});

test('the timing chart names the queue wait as a pool wait', () => {
    const html = buildPgBouncerSection({
        pgbouncer: {
            ...payload,
            stats_series: [
                { time: '2026-09-09 21:00:00', wait_us: 5 },
                { time: '2026-09-09 21:01:00', wait_us: 7 },
            ],
        },
    });
    assert.ok(html.includes('pool wait'), 'the timing legend still calls the queue wait a plain "wait"');
});

// A card label is read on its own: the row "Queries : 2.7 /s (max
// snapshot avg 48 /s)" carries its subject in the row label, a card does
// not. "Max snapshot avg" states a maximum of nothing in particular.
test('every card label names the figure it carries', () => {
    const html = buildPgBouncerSection({ pgbouncer: payload });
    for (const label of labelsOf(html)) {
        assert.ok(label !== 'Max snapshot avg',
            'the max card does not say what it is the maximum of');
    }
});
