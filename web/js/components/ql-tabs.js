/**
 * <ql-tabs> Web Component
 *
 * A tabs component with built-in accessibility (ARIA) and keyboard navigation.
 *
 * Usage:
 *   <ql-tabs>
 *     <ql-tab selected>Tab 1</ql-tab>
 *     <ql-tab>Tab 2</ql-tab>
 *     <ql-panel>Content 1</ql-panel>
 *     <ql-panel>Content 2</ql-panel>
 *   </ql-tabs>
 *
 * Or with badges:
 *   <ql-tab>Events <ql-badge>42</ql-badge></ql-tab>
 */

let tabIdCounter = 0;

class QlTabs extends HTMLElement {
    constructor() {
        super();
        this._selectedIndex = 0;
    }

    connectedCallback() {
        // Setup after DOM is ready
        requestAnimationFrame(() => this._setup());
    }

    _setup() {
        const tabs = this.querySelectorAll(':scope > ql-tab');
        const panels = this.querySelectorAll(':scope > ql-panel');

        if (tabs.length === 0) return;

        // Create tablist container
        const tablist = document.createElement('div');
        tablist.setAttribute('role', 'tablist');
        tablist.className = 'tabs';

        // Generate unique IDs and setup tabs
        const baseId = `ql-tabs-${++tabIdCounter}`;

        // Find which tab should be selected (explicit selected attr, or first one as fallback)
        let selectedIdx = Array.from(tabs).findIndex(t => t.hasAttribute('selected'));
        if (selectedIdx === -1) selectedIdx = 0;

        tabs.forEach((tab, i) => {
            const tabId = `${baseId}-tab-${i}`;
            const panelId = `${baseId}-panel-${i}`;
            const isSelected = i === selectedIdx;

            // Setup tab button
            tab.setAttribute('role', 'tab');
            tab.setAttribute('id', tabId);
            tab.setAttribute('aria-controls', panelId);
            tab.setAttribute('aria-selected', isSelected ? 'true' : 'false');
            tab.setAttribute('tabindex', isSelected ? '0' : '-1');

            if (isSelected) {
                tab.classList.add('active');
                this._selectedIndex = i;
            }

            // Move tab into tablist
            tablist.appendChild(tab);

            // Setup panel
            if (panels[i]) {
                panels[i].setAttribute('role', 'tabpanel');
                panels[i].setAttribute('id', panelId);
                panels[i].setAttribute('aria-labelledby', tabId);
                panels[i].style.display = isSelected ? 'block' : 'none';
                if (!isSelected) {
                    panels[i].setAttribute('hidden', '');
                }
            }
        });

        // Insert tablist at the beginning
        this.insertBefore(tablist, this.firstChild);

        // Event delegation for clicks
        tablist.addEventListener('click', (e) => {
            const tab = e.target.closest('ql-tab');
            if (tab) this._selectTab(tab);
        });

        // Keyboard navigation
        tablist.addEventListener('keydown', (e) => this._handleKeydown(e));
    }

    _selectTab(tab) {
        const tabs = Array.from(this.querySelectorAll('ql-tab'));
        const panels = Array.from(this.querySelectorAll('ql-panel'));
        const index = tabs.indexOf(tab);

        if (index === -1) return;

        // Update tabs
        tabs.forEach((t, i) => {
            const isSelected = i === index;
            t.setAttribute('aria-selected', isSelected ? 'true' : 'false');
            t.setAttribute('tabindex', isSelected ? '0' : '-1');
            t.classList.toggle('active', isSelected);
        });

        // Update panels
        panels.forEach((p, i) => {
            const isSelected = i === index;
            p.style.display = isSelected ? 'block' : 'none';
            if (isSelected) {
                p.removeAttribute('hidden');
            } else {
                p.setAttribute('hidden', '');
            }
        });

        this._selectedIndex = index;

        // Dispatch event
        this.dispatchEvent(new CustomEvent('tab-change', {
            detail: { index, tab },
            bubbles: true
        }));
    }

    _handleKeydown(e) {
        const tabs = Array.from(this.querySelectorAll('ql-tab'));
        let index = this._selectedIndex;

        switch (e.key) {
            case 'ArrowLeft':
            case 'ArrowUp':
                index = index > 0 ? index - 1 : tabs.length - 1;
                break;
            case 'ArrowRight':
            case 'ArrowDown':
                index = index < tabs.length - 1 ? index + 1 : 0;
                break;
            case 'Home':
                index = 0;
                break;
            case 'End':
                index = tabs.length - 1;
                break;
            default:
                return;
        }

        e.preventDefault();
        tabs[index].focus();
        this._selectTab(tabs[index]);
    }

    // Public API
    get selectedIndex() {
        return this._selectedIndex;
    }

    set selectedIndex(index) {
        const tabs = this.querySelectorAll('ql-tab');
        if (tabs[index]) {
            this._selectTab(tabs[index]);
        }
    }
}

class QlTab extends HTMLElement {
    constructor() {
        super();
    }

    connectedCallback() {
        // Make it focusable
        if (!this.hasAttribute('tabindex')) {
            this.setAttribute('tabindex', '-1');
        }
        this.classList.add('tab');
    }
}

class QlPanel extends HTMLElement {
    constructor() {
        super();
    }

    connectedCallback() {
        this.classList.add('tab-content');
    }
}

class QlBadge extends HTMLElement {
    constructor() {
        super();
    }

    connectedCallback() {
        this.classList.add('tab-badge');
    }
}

// Register components
customElements.define('ql-tabs', QlTabs);
customElements.define('ql-tab', QlTab);
customElements.define('ql-panel', QlPanel);
customElements.define('ql-badge', QlBadge);

export { QlTabs, QlTab, QlPanel, QlBadge };
