// Unit tests for the Patroni section: its cards, the notes that say what the
// report cannot see, and the grouping of PostgreSQL events across nodes.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { buildPatroniSection } from '../../js/sections/patroni.js';

// esc() escapes through the DOM: the same minimal stub as
// report-filter-triggering.test.js (textContent -> innerHTML on & < >).
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

const patroni = {
    start_date: '2026-09-24 10:00:00', end_date: '2026-09-24 10:10:00',
    members: [{ name: 'pg1', role: 'replica', leader_time: '1m0s', total_logs: 5 }],
    members_without_log: ['pg2'],
    leadership: [{ member: 'pg2', start: '2026-09-24 10:01:00', end: '2026-09-24 10:09:00', duration: '8m0s' }],
    changes: [{
        timestamp: '2026-09-24 10:01:00', kind: 'failover', from: 'pg1', to: 'pg2', leaderless: '30s',
        postgres: [
            { timestamp: '2026-09-24 10:01:02', kind: 'sighup', detail: 'reloading configuration files' },
            { timestamp: '2026-09-24 10:01:02', kind: 'sighup', detail: 'reloading configuration files' },
        ],
    }],
    switchovers: 0, failovers: 1, lock_retaken: 0, leaderless_time: '30s', error_count: 0,
};

test('PostgreSQL events logged by two nodes read once, with a count', () => {
    const html = buildPatroniSection({ patroni, summary: {} });
    assert.match(html, /<span class="t">10:01:02<\/span><span>reloading configuration files ×2<\/span>/);
    assert.match(html, /on any node/);
});

test('a report without PostgreSQL logs lists no PostgreSQL events and gives no advice', () => {
    const html = buildPatroniSection({ patroni });
    assert.doesNotMatch(html, /pt-pgl|on any node|No PostgreSQL log given/);
});

test('the section is four cards: figures and leader lock, changes beside events, timeline', () => {
    const html = buildPatroniSection({ patroni: { ...patroni, events: [{ timestamp: '2026-09-24 10:01:00', member: 'pg1', kind: 'promote' }] }, summary: {} });
    for (const id of ['patroni', 'patroni-changes', 'patroni-events', 'patroni-timeline']) assert.match(html, new RegExp(`id="${id}"`));
    assert.match(html, /<span class="pt-meta">replica · <b>1m<\/b> as leader · 5 entries<\/span>/);
});

test('errors and warnings sit in tabs, with their first and last occurrence', () => {
    const messages = [{ id: 'er-AAAA', level: 'ERROR', message: 'Error communicating with DCS', count: 3, first_date: '2026-09-24 10:02:00', last_date: '2026-09-24 10:03:00' },
        { id: 'wa-BBBB', level: 'WARNING', message: 'Loop time exceeded', count: 2 }];
    const html = buildPatroniSection({ patroni: { ...patroni, messages } });
    assert.match(html, /<ql-tab class="error" selected>ERROR<ql-badge>3<\/ql-badge>/);
    assert.match(html, /<ql-tab class="warning">WARNING<ql-badge>2<\/ql-badge>/);
    assert.match(html, /10:02:00 → 10:03:00/);
    assert.doesNotMatch(html, /er-AAAA/);
});

test('a leader whose log was not given is named, next to its figures', () => {
    const html = buildPatroniSection({ patroni: { ...patroni, leader: 'pg2', leader_since: '2026-09-24 10:01:00' } });
    assert.match(html, /No log from pg2/);
    assert.match(html, /Leader since 10:01:00, as seen by the others/);
});

test('a member name from the log cannot leave a title attribute', () => {
    const evil = 'pg1" onmouseover="alert(1)';
    const html = buildPatroniSection({ patroni: {
        ...patroni,
        members: [{ ...patroni.members[0], name: evil }],
        leadership: [{ ...patroni.leadership[0], member: evil }],
        changes: [{ ...patroni.changes[0], from: evil }],
        dcs_incidents: [{ start: '2026-09-24 10:02:00', end: '2026-09-24 10:03:00', duration: '1m0s', members: [evil] }],
    } });
    assert.doesNotMatch(html, /="[^"]*" onmouseover=/);
});
