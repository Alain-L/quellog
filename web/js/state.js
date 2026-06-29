// Global state management for quellog web app

// WASM module state
export let wasmModule = null;  // Compiled WebAssembly.Module (reusable)
export let wasmReady = false;

// Analysis state
export let analysisData = null;
export let currentFileContent = null;  // Keep file content for re-filtering
export let currentFileName = null;
export let currentFileSize = 0;
export let originalDimensions = null;  // Keep original dimensions for filter chips

// Filter state
export let currentFilters = {};       // Currently selected filter values
export let appliedFilters = {};       // Last applied filters (for comparison)
export let availableDimensions = null; // Available filter dimensions
export let openDropdown = null;       // Currently open dropdown category

// Time filter state
export let timeFilterStartTs = null;  // Start timestamp for slider offset (original span)
export let timeFilterEndTs = null;    // End timestamp (original span)
export let timeFilterDurationMins = 0; // Slider axis length, in minutes (original span)
// Current handle selection, in minutes from the original start. Persisted across
// re-renders because the slider now lives inside the rebuilt Summary card.
export let timeFilterSelMin = 0;
export let timeFilterSelMax = 0;
// Baseline selection = "no filter" extent (data span within the axis). For a
// single day it's 0..duration; for a multi-day full-calendar-days axis it's the
// real data offsets, so the default handles mark the actual data coverage.
export let timeFilterDefMin = 0;
export let timeFilterDefMax = 0;

// Chart management
export const charts = new Map();  // Store chart instances by ID
export const modalCharts = [];    // Store modal chart instances
export const modalChartsData = new Map();  // Pending modal charts data
export let modalChartCounter = 0;
export const chartIntervalMap = new Map();  // Per-chart interval in seconds (0 = auto)
export const defaultInterval = 0;  // Auto

// Setters for mutable state (ES modules export bindings, not values)
export function setWasmModule(mod) { wasmModule = mod; }
export function setWasmReady(ready) { wasmReady = ready; }
export function setAnalysisData(data) { analysisData = data; }
export function setCurrentFileContent(content) { currentFileContent = content; }
export function setCurrentFileName(name) { currentFileName = name; }
export function setCurrentFileSize(size) { currentFileSize = size; }
export function setOriginalDimensions(dims) { originalDimensions = dims; }
export function incrementModalChartCounter() { return ++modalChartCounter; }

// Chart cleanup (call before loading a new file)
export function clearAllCharts() {
    charts.forEach(chart => { try { chart.destroy(); } catch (_) {} });
    charts.clear();
    modalCharts.forEach(chart => { try { chart.destroy(); } catch (_) {} });
    modalCharts.length = 0;
    modalChartsData.clear();
    chartIntervalMap.clear();
    modalChartCounter = 0;
}

// Filter state setters
export function setCurrentFilters(filters) { currentFilters = filters; }
export function setAppliedFilters(filters) { appliedFilters = filters; }
export function setAvailableDimensions(dims) { availableDimensions = dims; }
export function setOpenDropdown(dropdown) { openDropdown = dropdown; }
export function setTimeFilterStartTs(ts) { timeFilterStartTs = ts; }
export function setTimeFilterEndTs(ts) { timeFilterEndTs = ts; }
export function setTimeFilterDurationMins(mins) { timeFilterDurationMins = mins; }
export function setTimeFilterSelMin(v) { timeFilterSelMin = v; }
export function setTimeFilterSelMax(v) { timeFilterSelMax = v; }
export function setTimeFilterDefMin(v) { timeFilterDefMin = v; }
export function setTimeFilterDefMax(v) { timeFilterDefMax = v; }
export function clearCurrentFilters() { currentFilters = {}; }
