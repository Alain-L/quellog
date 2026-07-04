// Filter bar UI and logic for quellog web app

import { esc, escAttr } from './utils.js';
import {
    originalDimensions, currentFilters, appliedFilters,
    timeFilterStartTs, timeFilterDurationMins,
    timeFilterSelMin, timeFilterSelMax, timeFilterDefMin, timeFilterDefMax,
    setOriginalDimensions, setCurrentFilters, setAvailableDimensions, setOpenDropdown,
    setTimeFilterStartTs, setTimeFilterEndTs, setTimeFilterDurationMins,
    setTimeFilterSelMin, setTimeFilterSelMax, setTimeFilterDefMin, setTimeFilterDefMax,
    clearCurrentFilters
} from './state.js';

// ===== Filter Bar Show/Hide =====

export function showFilterBar() {
    // A CLI-generated static report can only filter by time, now hosted in the
    // Summary card; everything in the filter bar (dimension dropdowns, split,
    // Apply/Clear) drives WASM re-parsing against the raw file, which a static
    // report doesn't carry — so the whole bar is hidden there.
    if (window.REPORT_MODE) {
        document.getElementById('filterBar')?.classList.remove('active');
        return;
    }
    document.getElementById('filterBar')?.classList.add('active');
}

export function hideFilterBar() {
    document.getElementById('filterBar')?.classList.remove('active');
}

// ===== Filter Bar Initialization =====

export function initFilterBar(data, isInitial = false) {
    const extractNames = (arr) => (arr || []).map(item => item.name || item);

    if (isInitial || !originalDimensions) {
        setOriginalDimensions({
            databases: extractNames(data.databases),
            users: extractNames(data.users),
            applications: extractNames(data.apps),
            hosts: extractNames(data.hosts),
            timeRange: data.summary?.time_range || null
        });
    }

    setAvailableDimensions(originalDimensions);

    // Populate dropdowns
    populateDropdown('database', originalDimensions.databases);
    populateDropdown('user', originalDimensions.users);
    populateDropdown('application', originalDimensions.applications);
    populateDropdown('host', originalDimensions.hosts);

    // Set time range
    if (isInitial) {
        initTimeFilter(data.summary?.start_date, data.summary?.end_date);
    }

    updateAllDropdownTriggers();
    showFilterBar();
}

// ===== Time Filter =====

const DAY_MS = 86400000;
// Above this many days the per-day labels collide on the track, so the time
// filter switches to From/To date pickers. Shared by initTimeFilter and the
// Summary renderer (app.js) so both agree on the cutoff.
export const MAX_CANVAS_DAYS = 8;
// A leading/trailing calendar day with less coverage than this is folded away,
// so a log ending at e.g. 00:00:01 doesn't add a near-empty extra day.
const NEGLIGIBLE_DAY_MS = 5 * 60 * 1000;

// computeDayAxis turns the dataset bounds into the full-calendar-days axis used by
// the Summary time slider: each touched day is a full 24h of equal width, offsets
// are measured from midnight of the first day, and the default selection marks the
// real data extent. A leading/trailing day whose coverage is negligible is dropped
// (guard-rail). Shared by initTimeFilter and the Summary renderer so both agree.
export function computeDayAxis(startDate, endDate) {
    const startTs = new Date(startDate.replace(' ', 'T')).getTime();
    const endTs = new Date(endDate.replace(' ', 'T')).getTime();
    const sd = new Date(startTs);
    let axisStart = new Date(sd.getFullYear(), sd.getMonth(), sd.getDate()).getTime();
    const ed = new Date(endTs);
    let axisEnd = new Date(ed.getFullYear(), ed.getMonth(), ed.getDate() + 1).getTime();

    // Fold away a negligible trailing day, then a negligible leading day.
    if (axisEnd - axisStart > DAY_MS && endTs - (axisEnd - DAY_MS) < NEGLIGIBLE_DAY_MS) {
        axisEnd -= DAY_MS;
    }
    if (axisEnd - axisStart > DAY_MS && (axisStart + DAY_MS) - startTs < NEGLIGIBLE_DAY_MS) {
        axisStart += DAY_MS;
    }

    const nDays = Math.round((axisEnd - axisStart) / DAY_MS);
    const durMins = nDays * 1440;
    let defMin = Math.round((startTs - axisStart) / 60000);
    let defMax = Math.round((endTs - axisStart) / 60000);
    if (defMin < 0) defMin = 0;
    if (defMax > durMins) defMax = durMins;
    return { startTs, endTs, axisStart, axisEnd, nDays, durMins, defMin, defMax };
}

// initTimeFilter computes the slider axis from the dataset bounds. DOM-free and
// run once per dataset (initial render): the time control lives in the Summary
// card, which is rebuilt on every render, so wireTimeFilter() does the DOM
// binding after each render.
export function initTimeFilter(startDate, endDate) {
    if (!startDate || !endDate) return;

    const a = computeDayAxis(startDate, endDate);

    // Always the slider — beyond MAX_CANVAS_DAYS we just drop the per-day labels
    // and show the start/end dates at the ends instead (handled in the renderer).
    setTimeFilterStartTs(a.axisStart);
    setTimeFilterEndTs(a.axisEnd);
    setTimeFilterDurationMins(a.durMins);
    setTimeFilterDefMin(a.defMin);
    setTimeFilterDefMax(a.defMax);
    setTimeFilterSelMin(a.defMin);
    setTimeFilterSelMax(a.defMax);
}

// wireTimeFilter binds the in-Summary time control after each render. The axis
// (min/max) is the original span from state; the handles restore the persisted
// selection so applying a time filter — which rebuilds the Summary — does not
// reset the user's range.
export function wireTimeFilter() {
    const slider = document.getElementById('filterTimeSlider');
    if (!slider) return;
    slider.style.display = 'block';

    const dur = timeFilterDurationMins || 1;
    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    minSlider.min = maxSlider.min = 0;
    minSlider.max = maxSlider.max = dur;
    minSlider.value = timeFilterSelMin;
    maxSlider.value = timeFilterSelMax;
    // data-original = the no-filter baseline (data extent), so change detection
    // treats "handles at the data extent" as unfiltered.
    minSlider.setAttribute('data-original', String(timeFilterDefMin));
    maxSlider.setAttribute('data-original', String(timeFilterDefMax));

    updateTimeSlider();

    // oninput = live visual only; onchange (fires on release) = auto-apply.
    minSlider.oninput = () => { enforceMinMax(); updateTimeSlider(); };
    maxSlider.oninput = () => { enforceMinMax(); updateTimeSlider(); };
    minSlider.onchange = autoApplyTime;
    maxSlider.onchange = autoApplyTime;
}

// autoApplyTime persists the current selection and applies the time filter on
// release, debounced. Time filtering is client-side (cheap); dimension filters
// still go through the explicit Apply button.
let timeApplyTimer = null;
function autoApplyTime() {
    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    setTimeFilterSelMin(parseInt(minSlider.value));
    setTimeFilterSelMax(parseInt(maxSlider.value));
    updateApplyButton();
    clearTimeout(timeApplyTimer);
    timeApplyTimer = setTimeout(() => {
        if (typeof window.applyFilters === 'function') window.applyFilters();
    }, 200);
}

// Month abbreviations for compact date labels.
const MON_ABBR = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

// ===== Time Utilities =====

export function minutesToTime(mins) {
    const h = Math.floor(mins / 60);
    const m = mins % 60;
    return `${h.toString().padStart(2, '0')}:${m.toString().padStart(2, '0')}`;
}

export function formatDateHuman(dateStr) {
    if (!dateStr) return '';
    const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
    const parts = dateStr.split('-');
    if (parts.length !== 3) return dateStr;
    const day = parseInt(parts[2]);
    const month = months[parseInt(parts[1]) - 1] || parts[1];
    const year = parts[0];
    return `${day} ${month} ${year}`;
}

export function enforceMinMax() {
    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    if (!minSlider || !maxSlider) return;
    const lo = timeFilterDefMin;
    const hi = timeFilterDefMax;
    let minVal = parseInt(minSlider.value);
    let maxVal = parseInt(maxSlider.value);
    // Confine the selection to the data extent — handles can't move into the
    // empty (no-data) parts of the calendar canvas.
    if (minVal < lo) minVal = lo;
    if (maxVal > hi) maxVal = hi;
    // Keep a 5-minute minimum window without crossing the bounds.
    if (minVal > maxVal - 5) minVal = Math.max(lo, maxVal - 5);
    if (maxVal < minVal + 5) maxVal = Math.min(hi, minVal + 5);
    minSlider.value = minVal;
    maxSlider.value = maxVal;
}

export function updateTimeSlider() {
    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    const range = document.getElementById('filterTimeRange');
    const label = document.getElementById('filterTimeLabel');

    const minVal = parseInt(minSlider.value);
    const maxVal = parseInt(maxSlider.value);
    const maxRange = parseInt(maxSlider.max) || 1440;
    const minPercent = (minVal / maxRange) * 100;
    const maxPercent = (maxVal / maxRange) * 100;

    range.style.left = minPercent + '%';
    range.style.width = (maxPercent - minPercent) + '%';

    // Convert offset to actual time. Beyond the per-day-label span, the bounds
    // show dates (not 00:00/24:00), so the selection label carries the date too.
    const wide = (timeFilterDurationMins / 1440) > MAX_CANVAS_DAYS;
    const fmt = wide ? offsetToDateTimeStr : offsetToTimeStr;
    if (label) label.textContent = fmt(minVal) + ' – ' + fmt(maxVal);
}

export function offsetToTimeStr(offsetMins) {
    if (!timeFilterStartTs) return minutesToTime(offsetMins);
    const ts = new Date(timeFilterStartTs + offsetMins * 60 * 1000);
    return ts.getHours().toString().padStart(2, '0') + ':' + ts.getMinutes().toString().padStart(2, '0');
}

// "3 Jan 08:00" — date + time, for selections on a multi-day (no per-day labels) axis.
function offsetToDateTimeStr(offsetMins) {
    if (!timeFilterStartTs) return minutesToTime(offsetMins);
    const d = new Date(timeFilterStartTs + offsetMins * 60 * 1000);
    const hh = d.getHours().toString().padStart(2, '0');
    const mm = d.getMinutes().toString().padStart(2, '0');
    return `${d.getDate()} ${MON_ABBR[d.getMonth()]} ${hh}:${mm}`;
}

export function offsetToDatetime(offsetMins) {
    if (!timeFilterStartTs) return null;
    const ts = new Date(timeFilterStartTs + offsetMins * 60 * 1000);
    const y = ts.getFullYear();
    const m = (ts.getMonth() + 1).toString().padStart(2, '0');
    const d = ts.getDate().toString().padStart(2, '0');
    const hh = ts.getHours().toString().padStart(2, '0');
    const mm = ts.getMinutes().toString().padStart(2, '0');
    const ss = ts.getSeconds().toString().padStart(2, '0');
    // Format matching Go output: "2006-01-02 15:04:05" (space separator, with seconds)
    return `${y}-${m}-${d} ${hh}:${mm}:${ss}`;
}

// ===== Dropdown Functions =====

export function populateDropdown(category, values) {
    const list = document.getElementById(`dropdownList-${category}`);
    if (!list) return;

    if (!values || values.length === 0) {
        list.innerHTML = '<div class="filter-dropdown-empty">No data</div>';
        return;
    }

    list.innerHTML = values.map(v => {
        const name = typeof v === 'object' ? v.name : v;
        const selected = currentFilters[category]?.includes(name) ? 'selected' : '';
        return `<div class="filter-dropdown-item ${selected}" data-value="${escAttr(name)}">
            <input type="checkbox" class="filter-item-checkbox" ${selected ? 'checked' : ''} tabindex="-1">
            <span class="filter-dropdown-item-label" title="${escAttr(name)}">${esc(name)}</span>
        </div>`;
    }).join('');
    updateToggleAllCheckbox(category);
}

export function toggleDropdown(category) {
    const dropdown = document.querySelector(`.filter-dropdown[data-category="${category}"]`);
    if (!dropdown) return;

    // Use component API if available (ql-dropdown)
    if (typeof dropdown.toggle === 'function') {
        dropdown.toggle();
        setOpenDropdown(dropdown.isOpen ? category : null);
    } else {
        // Fallback for non-component dropdowns (e.g., time)
        const wasOpen = dropdown.classList.contains('open');
        closeAllDropdowns();
        if (!wasOpen) {
            dropdown.classList.add('open');
            setOpenDropdown(category);
            const search = dropdown.querySelector('.filter-dropdown-search');
            if (search) setTimeout(() => search.focus(), 10);
        }
    }
}

export function closeAllDropdowns() {
    // Close ql-dropdown components
    document.querySelectorAll('ql-dropdown[open]').forEach(d => d.close());
    // Close legacy dropdowns
    document.querySelectorAll('.filter-dropdown.open').forEach(d => {
        if (typeof d.close !== 'function') {
            d.classList.remove('open');
        }
    });
    // Clear search inputs
    document.querySelectorAll('.filter-dropdown-search').forEach(s => {
        s.value = '';
        const category = s.closest('.filter-dropdown')?.dataset.category;
        if (category) searchDropdown(category, '');
    });
    setOpenDropdown(null);
}

export function searchDropdown(category, query) {
    const list = document.getElementById(`dropdownList-${category}`);
    if (!list) return;

    const items = list.querySelectorAll('.filter-dropdown-item');
    const q = query.toLowerCase();

    items.forEach(item => {
        const value = item.dataset.value.toLowerCase();
        item.style.display = value.includes(q) ? '' : 'none';
    });
}

// ===== Filter Value Management =====

// Every dimension dropdown is a <ql-dropdown> component (see index.html);
// the functions below drive the component API. Selection state flows back
// through the component's change event (handleDropdownChange).

export function toggleAllFilterValues(category, checked) {
    const dropdown = document.querySelector(`ql-dropdown[data-category="${category}"]`);
    if (dropdown && typeof dropdown.selectAll === 'function') {
        dropdown.selectAll(checked);
    }
}

export function updateToggleAllCheckbox(category) {
    const dropdown = document.querySelector(`ql-dropdown[data-category="${category}"]`);
    if (dropdown && typeof dropdown._updateToggleAll === 'function') {
        dropdown._updateToggleAll();
    }
}

export function clearCategoryFilter(category) {
    const dropdown = document.querySelector(`ql-dropdown[data-category="${category}"]`);
    if (dropdown && typeof dropdown.clearSelection === 'function') {
        dropdown.clearSelection();
    }
}

// ===== Dropdown Trigger Updates =====

export function updateDropdownTrigger(category) {
    const dropdown = document.querySelector(`.filter-dropdown[data-category="${category}"]`);
    if (!dropdown) return;

    // Component API only: the count setter updates the trigger badge and the
    // has-selection styling. Non-component elements (e.g. the split control)
    // manage their own count display.
    if ('count' in dropdown) {
        dropdown.count = currentFilters[category]?.length || 0;
    }
}

export function updateAllDropdownTriggers() {
    ['database', 'user', 'application', 'host'].forEach(updateDropdownTrigger);
    updateTimeDropdownTrigger();
    updateApplyButton();
}

export function updateTimeDropdownTrigger() {
    const dropdown = document.querySelector('.filter-dropdown[data-category="time"]');
    if (!dropdown) return;

    const trigger = dropdown.querySelector('.filter-dropdown-trigger');
    const countEl = dropdown.querySelector('.filter-dropdown-count');
    const hasTimeFilter = hasTimeFilterChanged();

    if (hasTimeFilter) {
        trigger.classList.add('has-selection');
        countEl.textContent = '(1)';
    } else {
        trigger.classList.remove('has-selection');
        countEl.textContent = '';
    }
}

// ===== Filter State Checking =====

export function hasTimeFilterChanged() {
    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    const minOrig = minSlider?.getAttribute('data-original') || '0';
    const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
    return minSlider?.value !== minOrig || maxSlider?.value !== maxOrig;
}

export function filtersHaveChanged() {
    // Check if currentFilters differ from appliedFilters
    const currentKeys = Object.keys(currentFilters);
    const appliedKeys = Object.keys(appliedFilters).filter(k => !k.startsWith('_'));

    // Check category filters
    if (currentKeys.length !== appliedKeys.length) return true;
    for (const key of currentKeys) {
        if (!appliedFilters[key]) return true;
        const curr = [...currentFilters[key]].sort();
        const appl = [...appliedFilters[key]].sort();
        if (curr.length !== appl.length) return true;
        for (let i = 0; i < curr.length; i++) {
            if (curr[i] !== appl[i]) return true;
        }
    }

    // Check time filters
    let currBegin = null, currEnd = null;

    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    const minOrig = minSlider?.getAttribute('data-original') || '0';
    const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
    const minVal = minSlider?.value || '0';
    const maxVal = maxSlider?.value || String(timeFilterDurationMins);
    if (minVal !== minOrig) currBegin = offsetToDatetime(parseInt(minVal));
    if (maxVal !== maxOrig) currEnd = offsetToDatetime(parseInt(maxVal));

    if (currBegin !== (appliedFilters._begin || null)) return true;
    if (currEnd !== (appliedFilters._end || null)) return true;

    return false;
}

export function updateApplyButton() {
    const applyBtn = document.getElementById('filterApply');
    const clearBtn = document.getElementById('filterClear');
    if (!applyBtn) return;

    const hasChanges = filtersHaveChanged();
    applyBtn.classList.toggle('active', hasChanges);
    applyBtn.disabled = !hasChanges;

    // Also update clear button
    const hasAnyFilters = Object.keys(currentFilters).length > 0 || hasTimeFilterChanged();
    clearBtn?.classList.toggle('active', hasAnyFilters);
    if (clearBtn) clearBtn.disabled = !hasAnyFilters;
}

// ===== Build Filter Object for WASM =====

export function buildFiltersObject() {
    const filters = { ...currentFilters };

    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    const minOrig = minSlider?.getAttribute('data-original') || '0';
    const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
    const minVal = minSlider?.value || '0';
    const maxVal = maxSlider?.value || String(timeFilterDurationMins);
    if (minVal !== minOrig) filters.begin = offsetToDatetime(parseInt(minVal));
    if (maxVal !== maxOrig) filters.end = offsetToDatetime(parseInt(maxVal));

    return filters;
}

// ===== Reset Time Inputs =====

export function resetTimeInputs() {
    setTimeFilterSelMin(timeFilterDefMin);
    setTimeFilterSelMax(timeFilterDefMax);
    const minSlider = document.getElementById('filterTimeMin');
    const maxSlider = document.getElementById('filterTimeMax');
    if (minSlider) minSlider.value = String(timeFilterDefMin);
    if (maxSlider) maxSlider.value = String(timeFilterDefMax);
    updateTimeSlider();
}

// ===== Clear All UI State =====

export function clearFilterSelections() {
    clearCurrentFilters();

    // Clear ql-dropdown components
    document.querySelectorAll('ql-dropdown').forEach(dropdown => {
        if (typeof dropdown.clearSelection === 'function') {
            // Use silent clear to avoid triggering change events
            dropdown.selectedValues = [];
        }
    });

    // Clear legacy dropdowns
    document.querySelectorAll('.filter-dropdown:not(ql-dropdown) .filter-dropdown-item.selected').forEach(item => {
        item.classList.remove('selected');
        const cb = item.querySelector('.filter-item-checkbox');
        if (cb) cb.checked = false;
    });

    ['database', 'user', 'application', 'host'].forEach(updateToggleAllCheckbox);
}

// ===== Event Listeners Setup =====

let listenersRegistered = false;

export function setupFilterEventListeners() {
    if (listenersRegistered) return;
    listenersRegistered = true;

    // Close dropdowns when clicking outside
    document.addEventListener('click', function(e) {
        if (!e.target.closest('.filter-dropdown')) {
            closeAllDropdowns();
        }
    });

    // Close dropdowns with Escape key
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') {
            closeAllDropdowns();
        }
    });

    // Listen to ql-dropdown change events
    document.querySelectorAll('ql-dropdown').forEach(dropdown => {
        dropdown.addEventListener('change', handleDropdownChange);
    });
}

// Handle selection change from ql-dropdown component
function handleDropdownChange(e) {
    // A ql-dropdown selection dispatches a CustomEvent carrying {category, values}.
    // The dropdown's inner native checkbox/input also fires a bubbling 'change'
    // with no detail; ignore it rather than throwing on the destructure below.
    if (!e.detail) return;
    const { category, values } = e.detail;
    if (!category) return;

    // Update state
    const filters = { ...currentFilters };
    if (values.length > 0) {
        filters[category] = values;
    } else {
        delete filters[category];
    }
    setCurrentFilters(filters);

    updateApplyButton();
}

// ===== Expose to Window for onclick handlers =====

export function exposeFilterGlobals() {
    window.toggleDropdown = toggleDropdown;
    window.searchDropdown = searchDropdown;
    window.toggleAllFilterValues = toggleAllFilterValues;
    window.clearCategoryFilter = clearCategoryFilter;
}
