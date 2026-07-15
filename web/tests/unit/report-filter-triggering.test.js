// Regression for R-2 (event modal, impossible triggering-query figures).
//
// Bug: reaggregateTopEvents re-scoped an event's occurrence count to the
// slider window but passed its whole-log `triggering_queries` list through
// UNCHANGED. The event modal then computed pct = triggerCount / scopedCount
// * 100 with no clamp, so a whole-log trigger count (16) over a scoped count
// (1) printed 1600 % and the Count column showed whole-log numbers against a
// scoped "Occurrences" card. At v0.11.0 top_events passed through whole-log
// untouched, so count and triggers always matched and pct stayed <= 100 %.
//
// Fix (two layers):
//   1. report-filter.js — reaggregateTopEvents drops triggering_queries to []
//      for any event the window narrows (kept.length !== ts.length), and keeps
//      them untouched when the window contains every occurrence (full range /
//      unfiltered modal unchanged).
//   2. modals.js — triggerRowStat / buildEventTriggeringTable clamp the pct to
//      <= 100 and cap the displayed per-query count at the event total, so even
//      a stale/legacy payload can never render an impossible number.
//
// DOM-free (node --test). report-filter is driven via setOriginalReportData +
// applyReportTimeFilter(beginStr, endStr); modals.js is imported for its pure
// (DOM-free) helpers — its module-load listeners are guarded behind
// `typeof document !== 'undefined'` so the import succeeds under node.

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import {
    setOriginalReportData,
    applyReportTimeFilter,
} from '../../js/report-filter.js';
import { triggerRowStat, buildEventTriggeringTable } from '../../js/sections/modals.js';

// buildEventTriggeringTable escapes cell text via utils.esc(), which uses
// textContent -> innerHTML (a DOM operation). Node's test runner isolates each
// file in its own process, so a minimal faithful document stub — escaping only
// & < > exactly like textContent->innerHTML — is safe and does not leak. Set
// AFTER modals.js is imported (its module-load listeners are already guarded
// behind `typeof document !== 'undefined'`, so they stay unregistered here).
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

// applyReportTimeFilter logs progress/warnings; keep node --test output clean.
function quiet(fn) {
    const log = console.log, warn = console.warn;
    console.log = () => {}; console.warn = () => {};
    try { return fn(); } finally { console.log = log; console.warn = warn; }
}

const FULL_BEGIN = '2025-01-01 00:00:00';
const FULL_END   = '2025-01-01 16:00:00';
const utc = (h) => Date.UTC(2025, 0, 1, h, 0, 0);

// One ERROR event: 16 occurrences (hourly 00:00..15:00); triggering_queries
// sum to the whole-log total 16 (10 + 6), exactly as the backend emits.
function makeDataset() {
    return {
        summary: { start_date: FULL_BEGIN, end_date: FULL_END, duration: '16h0m0s' },
        top_events: [
            {
                id: 'er-1', message: 'deadlock detected', severity: 'ERROR',
                count: 16,
                timestamps: Array.from({ length: 16 }, (_, i) => utc(i)),
                triggering_queries: [
                    { id: 'qA', normalized_query: 'UPDATE a', count: 10 },
                    { id: 'qB', normalized_query: 'UPDATE b', count: 6 },
                ],
            },
        ],
    };
}

// Extract every "NNN.N%" percentage the triggering-queries table renders.
function pctsIn(html) {
    return [...html.matchAll(/([\d.]+)%/g)].map(m => Number(m[1]));
}

describe('R-2 — reaggregateTopEvents drops stale triggering_queries under a scoping filter', () => {
    it('a narrowing window scopes count and empties triggering_queries', () => {
        setOriginalReportData(makeDataset());
        // Window keeps only the first occurrence (utc(0)); 15 are dropped.
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, '2025-01-01 00:30:00'));
        const ev = out.top_events[0];
        assert.equal(ev.count, 1);                       // scoped down from 16
        assert.equal(ev.timestamps.length, 1);
        assert.deepEqual(ev.triggering_queries, []);     // dropped (can't re-scope)
    });

    it('a partial window (some occurrences dropped) still empties triggering_queries', () => {
        setOriginalReportData(makeDataset());
        // 00:00..07:00 -> 8 of 16 occurrences kept: still a narrowing, so drop.
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, '2025-01-01 07:30:00'));
        const ev = out.top_events[0];
        assert.equal(ev.count, 8);
        assert.deepEqual(ev.triggering_queries, []);
    });

    it('the modal can no longer render an impossible pct/count for a scoped event', () => {
        setOriginalReportData(makeDataset());
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, '2025-01-01 00:30:00'));
        const ev = out.top_events[0];
        // triggering_queries is [] -> the table renders the empty state, never a
        // row whose count (was 10/6) exceeds the scoped total (1).
        const html = buildEventTriggeringTable(ev.triggering_queries, ev.count, ev.id);
        assert.match(html, /No triggering queries/);
        assert.equal(pctsIn(html).length, 0);
    });
});

describe('R-2 — the unfiltered / full-range modal is unchanged', () => {
    it('a full-window filter preserves triggering_queries untouched', () => {
        const original = makeDataset();
        setOriginalReportData(makeDataset());
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, FULL_END));
        const ev = out.top_events[0];
        assert.equal(ev.count, 16);                       // nothing dropped
        // Whole-log trigger counts are still exactly correct -> keep them.
        assert.deepEqual(ev.triggering_queries, original.top_events[0].triggering_queries);
    });

    it('the full-range table renders the real trigger counts, all pcts <= 100', () => {
        const original = makeDataset();
        setOriginalReportData(makeDataset());
        const out = quiet(() => applyReportTimeFilter(FULL_BEGIN, FULL_END));
        const ev = out.top_events[0];
        const html = buildEventTriggeringTable(ev.triggering_queries, ev.count, ev.id);
        assert.match(html, /UPDATE a/);
        assert.match(html, /62\.5%/);                     // 10/16
        assert.match(html, /37\.5%/);                     // 6/16
        assert.ok(pctsIn(html).every(p => p <= 100));
    });
});

describe('R-2 — modals.js clamp is a hard belt-and-suspenders', () => {
    it('triggerRowStat clamps pct to <= 100 and count to the event total', () => {
        // Stale/edge payload: a whole-log count (16) over a scoped total (1).
        const { count, pct } = triggerRowStat(16, 1);
        assert.equal(count, 1);                           // capped at event total
        assert.equal(pct, '100.0');                       // clamped, not 1600.0
    });

    it('triggerRowStat leaves a legitimate row untouched', () => {
        const { count, pct } = triggerRowStat(6, 16);
        assert.equal(count, 6);
        assert.equal(pct, '37.5');
    });

    it('triggerRowStat degrades safely when the event total is 0/unknown', () => {
        const { count, pct } = triggerRowStat(5, 0);
        assert.equal(count, 5);                           // nothing to reconcile against
        assert.equal(pct, '0.0');
    });

    it('buildEventTriggeringTable never emits a pct > 100 or a count > total for a stale payload', () => {
        // Feed the table stale whole-log triggers directly (bypassing the
        // filter) against a scoped event total of 1 — the layer-1 fix would
        // normally have emptied these, but the render must still be safe.
        const triggers = [
            { id: 'qA', normalized_query: 'UPDATE a', count: 10 },
            { id: 'qB', normalized_query: 'UPDATE b', count: 6 },
        ];
        const html = buildEventTriggeringTable(triggers, 1, 'er-1');
        assert.ok(pctsIn(html).every(p => p <= 100), 'no pct exceeds 100%');
        // Displayed counts are capped at the event total (1): the raw 10/16
        // never appear as a Count cell.
        assert.doesNotMatch(html, /<td class="num">10<\/td>/);
        assert.doesNotMatch(html, /<td class="num">16<\/td>/);
        assert.match(html, /<td class="num">1<\/td>/);
    });
});
