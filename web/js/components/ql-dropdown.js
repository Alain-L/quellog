/**
 * <ql-dropdown> Web Component - Phase 2
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
 */

class QlDropdown extends HTMLElement {
    constructor() {
        super();
        this._isOpen = false;
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
        if (e.key === 'Escape') {
            e.preventDefault();
            this.close();
            this._trigger.focus();
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
