/**
 * <ql-dropdown> Web Component - Phase 4
 *
 * A dropdown with built-in accessibility (ARIA) and keyboard handling.
 *
 * Usage:
 *   <ql-dropdown label="Database" category="database">
 *     <div class="filter-dropdown-menu">...</div>
 *   </ql-dropdown>
 *
 * Attributes:
 *   label    - Button label text
 *   category - Category identifier (for integration with filters.js)
 *   open     - Dropdown is open (reflects state)
 *
 * Properties:
 *   selectedValues - Array of selected values (get/set)
 *   count          - Number of selected items (get/set)
 *
 * Events:
 *   dropdown-open   - Fired when dropdown opens
 *   dropdown-close  - Fired when dropdown closes
 *   change          - Fired when selection changes (detail: { category, values, added?, removed? })
 *
 * Keyboard:
 *   ArrowDown/Up - Navigate items
 *   Enter/Space  - Toggle selection
 *   Home/End     - Jump to first/last item
 *   Escape       - Close dropdown
 */

class QlDropdown extends HTMLElement {
    constructor() {
        super();
        this._isOpen = false;
        this._focusedIndex = -1;
        this._handleKeydown = this._handleKeydown.bind(this);
        this._handleClickOutside = this._handleClickOutside.bind(this);
        this._handleItemClick = this._handleItemClick.bind(this);
        this._handleSearchInput = this._handleSearchInput.bind(this);
    }

    connectedCallback() {
        this._setup();
    }

    disconnectedCallback() {
        this._removeGlobalListeners();
    }

    static get observedAttributes() {
        return ['open', 'label'];
    }

    attributeChangedCallback(name, oldValue, newValue) {
        if (name === 'open') {
            if (newValue !== null) {
                this._show();
            } else {
                this._hide();
            }
        }
        if (name === 'label' && this._labelEl) {
            this._labelEl.textContent = newValue;
        }
    }

    _setup() {
        const label = this.getAttribute('label') || '';
        const category = this.getAttribute('category') || '';

        // Get existing menu content
        const menuContent = this.querySelector('.filter-dropdown-menu');

        // Clear and rebuild
        this.innerHTML = '';

        // Create trigger button
        const trigger = document.createElement('button');
        trigger.className = 'filter-dropdown-trigger';
        trigger.setAttribute('type', 'button');
        trigger.setAttribute('aria-haspopup', 'listbox');
        trigger.setAttribute('aria-expanded', 'false');

        const labelSpan = document.createElement('span');
        labelSpan.className = 'filter-dropdown-label';
        labelSpan.textContent = label;
        this._labelEl = labelSpan;

        const countSpan = document.createElement('span');
        countSpan.className = 'filter-dropdown-count';
        this._countEl = countSpan;

        const arrow = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        arrow.setAttribute('class', 'filter-dropdown-arrow');
        arrow.setAttribute('viewBox', '0 0 24 24');
        arrow.setAttribute('fill', 'none');
        arrow.setAttribute('stroke', 'currentColor');
        arrow.setAttribute('stroke-width', '2');
        arrow.innerHTML = '<polyline points="6 9 12 15 18 9"></polyline>';

        trigger.appendChild(labelSpan);
        trigger.appendChild(countSpan);
        trigger.appendChild(arrow);

        // Add click handler
        trigger.addEventListener('click', (e) => {
            e.stopPropagation();
            this.toggle();
        });

        this._trigger = trigger;
        this.appendChild(trigger);

        // Re-add menu content
        if (menuContent) {
            this.appendChild(menuContent);
            this._menu = menuContent;
            // Listen for item clicks (event delegation)
            menuContent.addEventListener('click', this._handleItemClick);
            // Listen for search input
            const searchInput = menuContent.querySelector('.filter-dropdown-search');
            if (searchInput) {
                searchInput.addEventListener('input', this._handleSearchInput);
                this._searchInput = searchInput;
            }
        }

        // Add base class
        this.classList.add('filter-dropdown');
        if (category) {
            this.dataset.category = category;
        }
    }

    _show() {
        if (this._isOpen) return;
        this._isOpen = true;
        this._focusedIndex = -1;

        this.classList.add('open');
        this._trigger.setAttribute('aria-expanded', 'true');

        // Add global listeners
        document.addEventListener('keydown', this._handleKeydown);
        document.addEventListener('click', this._handleClickOutside);

        // Focus search input if present
        requestAnimationFrame(() => {
            const search = this.querySelector('.filter-dropdown-search');
            if (search) {
                search.focus();
                search.select();
            }
        });

        this.dispatchEvent(new CustomEvent('dropdown-open', { bubbles: true }));
    }

    _hide() {
        if (!this._isOpen) return;
        this._isOpen = false;

        this.classList.remove('open');
        this._trigger.setAttribute('aria-expanded', 'false');

        this._removeGlobalListeners();

        // Clear focus
        this._clearFocus();
        this._focusedIndex = -1;

        // Clear search and reset filter
        if (this._searchInput) {
            this._searchInput.value = '';
            this._filterItems('');
        }

        this.dispatchEvent(new CustomEvent('dropdown-close', { bubbles: true }));
    }

    _removeGlobalListeners() {
        document.removeEventListener('keydown', this._handleKeydown);
        document.removeEventListener('click', this._handleClickOutside);
    }

    _handleKeydown(e) {
        const visibleItems = this._getVisibleItems();
        const maxIndex = visibleItems.length - 1;

        switch (e.key) {
            case 'Escape':
                e.preventDefault();
                this.close();
                this._trigger.focus();
                break;

            case 'ArrowDown':
                e.preventDefault();
                if (this._focusedIndex < maxIndex) {
                    this._setFocusedIndex(this._focusedIndex + 1, visibleItems);
                } else {
                    this._setFocusedIndex(0, visibleItems); // Wrap to first
                }
                break;

            case 'ArrowUp':
                e.preventDefault();
                if (this._focusedIndex > 0) {
                    this._setFocusedIndex(this._focusedIndex - 1, visibleItems);
                } else {
                    this._setFocusedIndex(maxIndex, visibleItems); // Wrap to last
                }
                break;

            case 'Home':
                e.preventDefault();
                this._setFocusedIndex(0, visibleItems);
                break;

            case 'End':
                e.preventDefault();
                this._setFocusedIndex(maxIndex, visibleItems);
                break;

            case 'Enter':
            case ' ':
                // Only handle if we have a focused item and not in search input
                if (this._focusedIndex >= 0 && e.target !== this._searchInput) {
                    e.preventDefault();
                    this._toggleFocusedItem(visibleItems);
                }
                break;
        }
    }

    _handleClickOutside(e) {
        if (!this.contains(e.target)) {
            this.close();
        }
    }

    // Public API
    toggle() {
        if (this._isOpen) {
            this.close();
        } else {
            // Close other dropdowns first
            document.querySelectorAll('ql-dropdown[open]').forEach(d => {
                if (d !== this) d.close();
            });
            this.open();
        }
    }

    open() {
        this.setAttribute('open', '');
    }

    close() {
        this.removeAttribute('open');
    }

    get isOpen() {
        return this._isOpen;
    }

    // Update count badge
    set count(value) {
        if (this._countEl) {
            this._countEl.textContent = value > 0 ? `(${value})` : '';
            this.classList.toggle('has-selection', value > 0);
            this._trigger?.classList.toggle('has-selection', value > 0);
        }
    }

    get count() {
        const text = this._countEl?.textContent || '';
        const match = text.match(/\((\d+)\)/);
        return match ? parseInt(match[1], 10) : 0;
    }

    // Selected values API
    get selectedValues() {
        const items = this.querySelectorAll('.filter-dropdown-item.selected');
        return Array.from(items).map(item => item.dataset.value);
    }

    set selectedValues(values) {
        const valueSet = new Set(values || []);
        const items = this.querySelectorAll('.filter-dropdown-item');

        items.forEach(item => {
            const value = item.dataset.value;
            const isSelected = valueSet.has(value);
            item.classList.toggle('selected', isSelected);
            const cb = item.querySelector('.filter-item-checkbox');
            if (cb) cb.checked = isSelected;
        });

        this.count = valueSet.size;
        this._updateToggleAll();
    }

    // Handle item click (event delegation)
    _handleItemClick(e) {
        const item = e.target.closest('.filter-dropdown-item');
        if (!item) return;

        // Prevent default checkbox behavior - we handle it ourselves
        if (e.target.type === 'checkbox') {
            e.preventDefault();
        }

        const value = item.dataset.value;
        const wasSelected = item.classList.contains('selected');

        // Toggle selection
        item.classList.toggle('selected');
        const cb = item.querySelector('.filter-item-checkbox');
        if (cb) cb.checked = !wasSelected;

        // Update count and toggle-all
        const newValues = this.selectedValues;
        this.count = newValues.length;
        this._updateToggleAll();

        // Emit change event
        this.dispatchEvent(new CustomEvent('change', {
            bubbles: true,
            detail: {
                category: this.dataset.category,
                values: newValues,
                [wasSelected ? 'removed' : 'added']: value
            }
        }));
    }

    // Update "toggle all" checkbox state
    _updateToggleAll() {
        const checkbox = this.querySelector('.filter-dropdown-toggle-all');
        if (!checkbox) return;

        const items = this.querySelectorAll('.filter-dropdown-item');
        const selectedCount = this.querySelectorAll('.filter-dropdown-item.selected').length;

        checkbox.checked = selectedCount === items.length && items.length > 0;
        checkbox.indeterminate = selectedCount > 0 && selectedCount < items.length;
    }

    // Handle search input
    _handleSearchInput(e) {
        this._filterItems(e.target.value);
    }

    // Filter visible items by search query
    _filterItems(query) {
        const q = query.toLowerCase();
        const items = this.querySelectorAll('.filter-dropdown-item');

        items.forEach(item => {
            const value = (item.dataset.value || '').toLowerCase();
            item.style.display = value.includes(q) ? '' : 'none';
        });

        // Reset focus when filter changes
        this._clearFocus();
        this._focusedIndex = -1;
    }

    // Get visible (not hidden by search) items
    _getVisibleItems() {
        const items = this.querySelectorAll('.filter-dropdown-item');
        return Array.from(items).filter(item => item.style.display !== 'none');
    }

    // Set focused item by index
    _setFocusedIndex(index, visibleItems = null) {
        const items = visibleItems || this._getVisibleItems();
        if (index < 0 || index >= items.length) return;

        this._clearFocus();
        this._focusedIndex = index;

        const item = items[index];
        item.classList.add('focused');
        item.scrollIntoView({ block: 'nearest' });
    }

    // Clear focus from all items
    _clearFocus() {
        this.querySelectorAll('.filter-dropdown-item.focused').forEach(item => {
            item.classList.remove('focused');
        });
    }

    // Toggle selection of the currently focused item
    _toggleFocusedItem(visibleItems = null) {
        const items = visibleItems || this._getVisibleItems();
        if (this._focusedIndex < 0 || this._focusedIndex >= items.length) return;

        const item = items[this._focusedIndex];
        // Simulate click to reuse existing logic
        this._handleItemClick({ target: item, preventDefault: () => {} });
    }

    // Select/deselect all items
    selectAll(selected = true) {
        const items = this.querySelectorAll('.filter-dropdown-item');
        const values = [];

        items.forEach(item => {
            item.classList.toggle('selected', selected);
            const cb = item.querySelector('.filter-item-checkbox');
            if (cb) cb.checked = selected;
            if (selected) values.push(item.dataset.value);
        });

        this.count = selected ? items.length : 0;
        this._updateToggleAll();

        this.dispatchEvent(new CustomEvent('change', {
            bubbles: true,
            detail: {
                category: this.dataset.category,
                values: selected ? values : []
            }
        }));
    }

    // Clear all selections
    clearSelection() {
        this.selectAll(false);
    }
}

// Register component
customElements.define('ql-dropdown', QlDropdown);

export { QlDropdown };
