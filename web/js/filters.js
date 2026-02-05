// Filter bar UI and logic for quellog web app

import { esc } from './utils.js';
import {
    originalDimensions, currentFilters, appliedFilters, availableDimensions, openDropdown,
    timeFilterMode, timeFilterStartTs, timeFilterEndTs, timeFilterDurationMins,
    setOriginalDimensions, setCurrentFilters, setAvailableDimensions, setOpenDropdown,
    setTimeFilterMode, setTimeFilterStartTs, setTimeFilterEndTs, setTimeFilterDurationMins,
    clearCurrentFilters
} from './state.js';

// ===== Filter Bar Show/Hide =====

export function showFilterBar() {
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

export function initTimeFilter(startDate, endDate) {
    const slider = document.getElementById('filterTimeSlider');
    const pickers = document.getElementById('filterTimePickers');
    const begin = document.getElementById('filterBegin');
    const end = document.getElementById('filterEnd');

    if (!startDate || !endDate) return;

    // Parse timestamps
    const startTs = new Date(startDate.replace(' ', 'T')).getTime();
    const endTs = new Date(endDate.replace(' ', 'T')).getTime();
    const durationMs = endTs - startTs;
    const durationHours = durationMs / (1000 * 60 * 60);

    if (durationHours <= 24) {
        // Slider mode - offset from start
        setTimeFilterMode('slider');
        setTimeFilterStartTs(startTs);
        setTimeFilterEndTs(endTs);
        const durationMins = Math.ceil(durationMs / (1000 * 60));
        setTimeFilterDurationMins(durationMins);

        slider.style.display = 'block';
        pickers.style.display = 'none';

        // Set slider range
        const minSlider = document.getElementById('filterTimeMin');
        const maxSlider = document.getElementById('filterTimeMax');
        minSlider.min = 0;
        minSlider.max = durationMins;
        maxSlider.min = 0;
        maxSlider.max = durationMins;
        minSlider.value = 0;
        maxSlider.value = durationMins;
        minSlider.setAttribute('data-original', '0');
        maxSlider.setAttribute('data-original', String(durationMins));

        // Set day label
        const startDay = startDate.split(' ')[0];
        const endDay = endDate.split(' ')[0];
        if (startDay === endDay) {
            document.getElementById('filterTimeDay').textContent = formatDateHuman(startDay);
        } else {
            document.getElementById('filterTimeDay').textContent = formatDateHuman(startDay) + ' – ' + formatDateHuman(endDay);
        }

        // Update display
        updateTimeSlider();

        // Add event listeners
        minSlider.oninput = () => { enforceMinMax(); updateTimeSlider(); updateApplyButton(); updateTimeDropdownTrigger(); };
        maxSlider.oninput = () => { enforceMinMax(); updateTimeSlider(); updateApplyButton(); updateTimeDropdownTrigger(); };
    } else {
        // Pickers mode
        setTimeFilterMode('pickers');
        setTimeFilterStartTs(null);
        setTimeFilterEndTs(null);
        slider.style.display = 'none';
        pickers.style.display = 'grid';

        if (begin) {
            begin.value = startDate.slice(0, 16).replace(' ', 'T');
            begin.setAttribute('data-original', begin.value);
        }
        if (end) {
            end.value = endDate.slice(0, 16).replace(' ', 'T');
            end.setAttribute('data-original', end.value);
        }
    }
}

// ===== Time Utilities =====

export function timeToMinutes(timeStr) {
    const parts = timeStr.split(':');
    return parseInt(parts[0] || 0) * 60 + parseInt(parts[1] || 0);
}

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
    const minVal = parseInt(minSlider.value);
    const maxVal = parseInt(maxSlider.value);
    if (minVal > maxVal - 5) {
        minSlider.value = maxVal - 5;
    }
    if (maxVal < minVal + 5) {
        maxSlider.value = minVal + 5;
    }
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

    // Convert offset to actual time
    const startTime = offsetToTimeStr(minVal);
    const endTime = offsetToTimeStr(maxVal);
    label.textContent = startTime + ' – ' + endTime;
}

export function offsetToTimeStr(offsetMins) {
    if (!timeFilterStartTs) return minutesToTime(offsetMins);
    const ts = new Date(timeFilterStartTs + offsetMins * 60 * 1000);
    return ts.getHours().toString().padStart(2, '0') + ':' + ts.getMinutes().toString().padStart(2, '0');
}

export function offsetToDatetime(offsetMins) {
    if (!timeFilterStartTs) return null;
    const ts = new Date(timeFilterStartTs + offsetMins * 60 * 1000);
    const y = ts.getFullYear();
    const m = (ts.getMonth() + 1).toString().padStart(2, '0');
    const d = ts.getDate().toString().padStart(2, '0');
    const hh = ts.getHours().toString().padStart(2, '0');
    const mm = ts.getMinutes().toString().padStart(2, '0');
    // Go expects format "2006-01-02T15:04" (with T, no seconds)
    return `${y}-${m}-${d}T${hh}:${mm}`;
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
        return `<div class="filter-dropdown-item ${selected}" data-value="${esc(name)}" onclick="toggleFilterValue('${category}', '${esc(name)}', this)">
            <input type="checkbox" class="filter-item-checkbox" ${selected ? 'checked' : ''} tabindex="-1">
            <span class="filter-dropdown-item-label" title="${esc(name)}">${esc(name)}</span>
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

export function toggleFilterValue(category, value, element) {
    // Toggle selection
    const filters = { ...currentFilters };
    if (!filters[category]) filters[category] = [];

    const idx = filters[category].indexOf(value);
    if (idx >= 0) {
        filters[category].splice(idx, 1);
        if (filters[category].length === 0) delete filters[category];
        element?.classList.remove('selected');
        const cb = element?.querySelector('.filter-item-checkbox');
        if (cb) cb.checked = false;
    } else {
        filters[category].push(value);
        element?.classList.add('selected');
        const cb = element?.querySelector('.filter-item-checkbox');
        if (cb) cb.checked = true;
    }

    setCurrentFilters(filters);
    updateDropdownTrigger(category);
    updateToggleAllCheckbox(category);
    updateApplyButton();
}

export function toggleAllFilterValues(category, checked) {
    const list = document.getElementById(`dropdownList-${category}`);
    if (!list) return;

    const items = list.querySelectorAll('.filter-dropdown-item');
    const values = Array.from(items).map(item => item.dataset.value);
    const filters = { ...currentFilters };

    if (checked) {
        // Select all
        filters[category] = [...values];
        items.forEach(item => {
            item.classList.add('selected');
            const cb = item.querySelector('.filter-item-checkbox');
            if (cb) cb.checked = true;
        });
    } else {
        // Deselect all
        delete filters[category];
        items.forEach(item => {
            item.classList.remove('selected');
            const cb = item.querySelector('.filter-item-checkbox');
            if (cb) cb.checked = false;
        });
    }

    setCurrentFilters(filters);
    updateDropdownTrigger(category);
    updateApplyButton();
}

export function updateToggleAllCheckbox(category) {
    const dropdown = document.querySelector(`.filter-dropdown[data-category="${category}"]`);
    const checkbox = dropdown?.querySelector('.filter-dropdown-toggle-all');
    const list = document.getElementById(`dropdownList-${category}`);
    if (!checkbox || !list) return;

    const items = list.querySelectorAll('.filter-dropdown-item');
    const selectedCount = currentFilters[category]?.length || 0;

    checkbox.checked = selectedCount === items.length && items.length > 0;
    checkbox.indeterminate = selectedCount > 0 && selectedCount < items.length;
}

export function clearCategoryFilter(category) {
    // Clear this category's filters
    const filters = { ...currentFilters };
    delete filters[category];
    setCurrentFilters(filters);

    // Update UI - unselect items in this dropdown
    const list = document.getElementById(`dropdownList-${category}`);
    if (list) {
        list.querySelectorAll('.filter-dropdown-item.selected').forEach(item => {
            item.classList.remove('selected');
            const cb = item.querySelector('.filter-item-checkbox');
            if (cb) cb.checked = false;
        });
    }

    updateToggleAllCheckbox(category);
    updateDropdownTrigger(category);
    updateApplyButton();
}

// ===== Dropdown Trigger Updates =====

export function updateDropdownTrigger(category) {
    const dropdown = document.querySelector(`.filter-dropdown[data-category="${category}"]`);
    if (!dropdown) return;

    const count = currentFilters[category]?.length || 0;

    // Use component API if available (ql-dropdown)
    if ('count' in dropdown) {
        dropdown.count = count;
    } else {
        // Fallback for non-component dropdowns
        const trigger = dropdown.querySelector('.filter-dropdown-trigger');
        const countEl = dropdown.querySelector('.filter-dropdown-count');
        if (count > 0) {
            dropdown.classList.add('has-selection');
            trigger?.classList.add('has-selection');
            if (countEl) countEl.textContent = `(${count})`;
        } else {
            dropdown.classList.remove('has-selection');
            trigger?.classList.remove('has-selection');
            if (countEl) countEl.textContent = '';
        }
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
    if (timeFilterMode === 'slider') {
        const minSlider = document.getElementById('filterTimeMin');
        const maxSlider = document.getElementById('filterTimeMax');
        const minOrig = minSlider?.getAttribute('data-original') || '0';
        const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
        return minSlider?.value !== minOrig || maxSlider?.value !== maxOrig;
    } else {
        const begin = document.getElementById('filterBegin');
        const end = document.getElementById('filterEnd');
        const beginOrig = begin?.getAttribute('data-original') || '';
        const endOrig = end?.getAttribute('data-original') || '';
        return (begin?.value || '') !== beginOrig || (end?.value || '') !== endOrig;
    }
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

    if (timeFilterMode === 'slider') {
        const minSlider = document.getElementById('filterTimeMin');
        const maxSlider = document.getElementById('filterTimeMax');
        const minOrig = minSlider?.getAttribute('data-original') || '0';
        const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
        const minVal = minSlider?.value || '0';
        const maxVal = maxSlider?.value || String(timeFilterDurationMins);

        if (minVal !== minOrig) {
            currBegin = offsetToDatetime(parseInt(minVal));
        }
        if (maxVal !== maxOrig) {
            currEnd = offsetToDatetime(parseInt(maxVal));
        }
    } else {
        const begin = document.getElementById('filterBegin');
        const end = document.getElementById('filterEnd');
        const beginOrig = begin?.getAttribute('data-original') || '';
        const endOrig = end?.getAttribute('data-original') || '';
        const beginVal = begin?.value || '';
        const endVal = end?.value || '';

        currBegin = beginVal !== beginOrig ? beginVal : null;
        currEnd = endVal !== endOrig ? endVal : null;
    }

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

    // Add time range based on mode
    if (timeFilterMode === 'slider') {
        const minSlider = document.getElementById('filterTimeMin');
        const maxSlider = document.getElementById('filterTimeMax');
        const minOrig = minSlider?.getAttribute('data-original') || '0';
        const maxOrig = maxSlider?.getAttribute('data-original') || String(timeFilterDurationMins);
        const minVal = minSlider?.value || '0';
        const maxVal = maxSlider?.value || String(timeFilterDurationMins);

        if (minVal !== minOrig) {
            filters.begin = offsetToDatetime(parseInt(minVal));
        }
        if (maxVal !== maxOrig) {
            filters.end = offsetToDatetime(parseInt(maxVal));
        }
    } else {
        const begin = document.getElementById('filterBegin')?.value;
        const end = document.getElementById('filterEnd')?.value;
        const beginOrig = document.getElementById('filterBegin')?.getAttribute('data-original') || '';
        const endOrig = document.getElementById('filterEnd')?.getAttribute('data-original') || '';

        if (begin && begin !== beginOrig) filters.begin = begin.replace('T', ' ');
        if (end && end !== endOrig) filters.end = end.replace('T', ' ');
    }

    return filters;
}

// ===== Reset Time Inputs =====

export function resetTimeInputs() {
    if (timeFilterMode === 'slider') {
        const minSlider = document.getElementById('filterTimeMin');
        const maxSlider = document.getElementById('filterTimeMax');
        if (minSlider) minSlider.value = minSlider.getAttribute('data-original') || '0';
        if (maxSlider) maxSlider.value = maxSlider.getAttribute('data-original') || String(timeFilterDurationMins);
        updateTimeSlider();
    } else {
        const begin = document.getElementById('filterBegin');
        const end = document.getElementById('filterEnd');
        if (begin) begin.value = begin.getAttribute('data-original') || '';
        if (end) end.value = end.getAttribute('data-original') || '';
    }
}

// ===== Clear All UI State =====

export function clearFilterSelections() {
    clearCurrentFilters();
    document.querySelectorAll('.filter-dropdown-item.selected').forEach(item => {
        item.classList.remove('selected');
        const cb = item.querySelector('.filter-item-checkbox');
        if (cb) cb.checked = false;
    });
    ['database', 'user', 'application', 'host'].forEach(updateToggleAllCheckbox);
}

// ===== Event Listeners Setup =====

export function setupFilterEventListeners() {
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
}

// ===== Expose to Window for onclick handlers =====

export function exposeFilterGlobals() {
    window.toggleDropdown = toggleDropdown;
    window.searchDropdown = searchDropdown;
    window.toggleFilterValue = toggleFilterValue;
    window.toggleAllFilterValues = toggleAllFilterValues;
    window.clearCategoryFilter = clearCategoryFilter;
    window.applyTimeFilter = () => { closeAllDropdowns(); updateApplyButton(); };
}
