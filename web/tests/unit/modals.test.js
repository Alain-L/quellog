// Unit tests for the pure (DOM-free) helpers behind the query-detail and
// event-detail modals — the fixes that shipped without test exercise (the
// visual harness never opens a modal). See docs/web-data-contract.md.
import { test } from 'node:test';
import assert from 'node:assert/strict';

// The helpers are homed in utils.js (DOM-free) precisely so they are testable —
// importing modals.js in node fails (it pulls charts.js, which registers a
// document keydown listener at module load).
import { qdDurationBuckets } from '../../js/utils.js';

test('qdDurationBuckets reads duration_ms and buckets like the backend', () => {
    const execs = [
        { duration_ms: 0.5 },    // < 1 ms
        { duration_ms: 5 },      // < 10 ms
        { duration_ms: 5 },      // < 10 ms
        { duration_ms: 100 },    // < 1 s  (100 is not < 100)
        { duration_ms: 15000 },  // >= 10 s
        { duration: '5 ms' },    // old (non-existent) field -> ignored
        { duration_ms: null },   // ignored
    ];
    const dist = qdDurationBuckets(execs);
    const count = (label) => dist.find(b => b.label === label).count;
    assert.equal(count('< 1 ms'), 1);
    assert.equal(count('< 10 ms'), 2);
    assert.equal(count('< 100 ms'), 0);
    assert.equal(count('< 1 s'), 1);
    assert.equal(count('< 10 s'), 0);
    assert.equal(count('>= 10 s'), 1);
    // Only the 5 numeric duration_ms values count; the `duration` string and the
    // null are ignored (the bug: reading `duration` made every value 0, so the
    // whole block filtered out and never rendered).
    assert.equal(dist.reduce((s, b) => s + b.count, 0), 5);
});
