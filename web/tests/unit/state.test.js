// Unit tests for web/js/state.js — setter/getter round-trips on the live
// ES-module bindings, the modal chart counter, and clearAllCharts with fake
// chart objects.
//
// State is module-global; tests in this file run sequentially and set every
// binding they assert on, so no cross-test reset is needed beyond what each
// test does itself.

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import * as state from '../../js/state.js';

describe('setter/getter round-trips', () => {
    it('round-trips WASM state', () => {
        const mod = { fake: 'module' };
        state.setWasmModule(mod);
        assert.equal(state.wasmModule, mod);
        state.setWasmModule(null);
        assert.equal(state.wasmModule, null);

        state.setWasmReady(true);
        assert.equal(state.wasmReady, true);
        state.setWasmReady(false);
        assert.equal(state.wasmReady, false);
    });

    it('round-trips analysis/file state', () => {
        const data = { summary: {} };
        state.setAnalysisData(data);
        assert.equal(state.analysisData, data);

        state.setCurrentFileContent('raw log bytes');
        assert.equal(state.currentFileContent, 'raw log bytes');

        state.setCurrentFileName('postgresql.log');
        assert.equal(state.currentFileName, 'postgresql.log');

        state.setCurrentFileSize(123456);
        assert.equal(state.currentFileSize, 123456);

        const dims = { databases: ['db1'], users: ['u1'] };
        state.setOriginalDimensions(dims);
        assert.equal(state.originalDimensions, dims);
    });

    it('round-trips filter state', () => {
        const filters = { database: ['db1', 'db2'] };
        state.setCurrentFilters(filters);
        assert.equal(state.currentFilters, filters);

        const applied = { database: ['db1'], _begin: '2025-01-01 00:00:00' };
        state.setAppliedFilters(applied);
        assert.equal(state.appliedFilters, applied);

        const dims = { users: ['u1'] };
        state.setAvailableDimensions(dims);
        assert.equal(state.availableDimensions, dims);

        state.setOpenDropdown('database');
        assert.equal(state.openDropdown, 'database');
        state.setOpenDropdown(null);
        assert.equal(state.openDropdown, null);
    });

    it('clearCurrentFilters replaces the map with a fresh empty object', () => {
        const filters = { user: ['alice'] };
        state.setCurrentFilters(filters);
        state.clearCurrentFilters();
        assert.deepEqual(state.currentFilters, {});
        assert.notEqual(state.currentFilters, filters);
    });

    it('round-trips the time filter axis and selection', () => {
        state.setTimeFilterStartTs(1736895600000);
        state.setTimeFilterEndTs(1736982000000);
        state.setTimeFilterDurationMins(1440);
        state.setTimeFilterSelMin(60);
        state.setTimeFilterSelMax(1380);
        state.setTimeFilterDefMin(30);
        state.setTimeFilterDefMax(1410);

        assert.equal(state.timeFilterStartTs, 1736895600000);
        assert.equal(state.timeFilterEndTs, 1736982000000);
        assert.equal(state.timeFilterDurationMins, 1440);
        assert.equal(state.timeFilterSelMin, 60);
        assert.equal(state.timeFilterSelMax, 1380);
        assert.equal(state.timeFilterDefMin, 30);
        assert.equal(state.timeFilterDefMax, 1410);
    });
});

describe('modal chart counter', () => {
    it('increments and returns the new value', () => {
        const first = state.incrementModalChartCounter();
        assert.equal(state.modalChartCounter, first);
        const second = state.incrementModalChartCounter();
        assert.equal(second, first + 1);
        assert.equal(state.modalChartCounter, second);
    });
});

describe('clearAllCharts', () => {
    function makeFakeChart(log, name) {
        return { destroy() { log.push(name); } };
    }

    it('destroys every chart, clears the containers, resets the counter', () => {
        const destroyed = [];

        state.charts.set('chart-a', makeFakeChart(destroyed, 'a'));
        state.charts.set('chart-b', makeFakeChart(destroyed, 'b'));
        state.modalCharts.push(makeFakeChart(destroyed, 'm1'));
        state.modalCharts.push(makeFakeChart(destroyed, 'm2'));
        state.modalChartsData.set('m1', { some: 'payload' });
        state.chartIntervalMap.set('chart-a', 300);
        state.incrementModalChartCounter();
        assert.ok(state.modalChartCounter > 0);

        state.clearAllCharts();

        assert.deepEqual(destroyed.sort(), ['a', 'b', 'm1', 'm2']);
        assert.equal(state.charts.size, 0);
        assert.equal(state.modalCharts.length, 0);
        assert.equal(state.modalChartsData.size, 0);
        assert.equal(state.chartIntervalMap.size, 0);
        assert.equal(state.modalChartCounter, 0);
        assert.equal(state.incrementModalChartCounter(), 1); // counter restarted
        state.clearAllCharts(); // leave a clean slate
    });

    it('disconnects a chart ResizeObserver (_ro) before destroy', () => {
        const calls = [];
        state.charts.set('with-ro', {
            _ro: { disconnect() { calls.push('disconnect'); } },
            destroy() { calls.push('destroy'); }
        });
        state.charts.set('without-ro', { destroy() { calls.push('plain-destroy'); } });

        state.clearAllCharts();

        // Observer disconnected before its chart is destroyed; charts without
        // an observer are tolerated.
        assert.deepEqual(calls.filter(c => c !== 'plain-destroy'), ['disconnect', 'destroy']);
        assert.ok(calls.includes('plain-destroy'));
        assert.equal(state.charts.size, 0);
    });

    it('swallows a throwing destroy() and still clears everything', () => {
        const destroyed = [];
        state.charts.set('bad', { destroy() { throw new Error('boom'); } });
        state.charts.set('good', makeFakeChart(destroyed, 'good'));
        state.modalCharts.push({ destroy() { throw new Error('boom modal'); } });

        assert.doesNotThrow(() => state.clearAllCharts());
        assert.deepEqual(destroyed, ['good']);
        assert.equal(state.charts.size, 0);
        assert.equal(state.modalCharts.length, 0);
    });

    it('is a no-op on an already-empty state', () => {
        assert.doesNotThrow(() => state.clearAllCharts());
        assert.equal(state.charts.size, 0);
        assert.equal(state.modalChartCounter, 0);
    });
});
