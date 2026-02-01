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
export let timeFilterMode = 'slider'; // 'slider' or 'pickers'
export let timeFilterStartTs = null;  // Start timestamp for slider offset
export let timeFilterEndTs = null;    // End timestamp
export let timeFilterDurationMins = 0;

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

// Filter state setters
export function setCurrentFilters(filters) { currentFilters = filters; }
export function setAppliedFilters(filters) { appliedFilters = filters; }
export function setAvailableDimensions(dims) { availableDimensions = dims; }
export function setOpenDropdown(dropdown) { openDropdown = dropdown; }
export function setTimeFilterMode(mode) { timeFilterMode = mode; }
export function setTimeFilterStartTs(ts) { timeFilterStartTs = ts; }
export function setTimeFilterEndTs(ts) { timeFilterEndTs = ts; }
export function setTimeFilterDurationMins(mins) { timeFilterDurationMins = mins; }
export function clearCurrentFilters() { currentFilters = {}; }
