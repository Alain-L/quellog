/**
 * <ql-dropdown> Web Component - Phase 1
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
 * Events:
 *   dropdown-open  - Fired when dropdown opens
 *   dropdown-close - Fired when dropdown closes
 */

class QlDropdown extends HTMLElement {
    constructor() {
        super();
        this._isOpen = false;
        this._handleKeydown = this._handleKeydown.bind(this);
        this._handleClickOutside = this._handleClickOutside.bind(this);
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

        // Clear search
        const search = this.querySelector('.filter-dropdown-search');
        if (search) {
            search.value = '';
            // Trigger search reset
            const category = this.dataset.category;
            if (category && typeof window.searchDropdown === 'function') {
                window.searchDropdown(category, '');
            }
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
}

// Register component
customElements.define('ql-dropdown', QlDropdown);

export { QlDropdown };
