/**
 * <ql-tooltip> Web Component
 *
 * An accessible tooltip with keyboard support.
 *
 * Usage:
 *   <ql-tooltip text="Helpful information">ⓘ</ql-tooltip>
 *   <ql-tooltip text="Helpful info" position="bottom">?</ql-tooltip>
 *
 * Attributes:
 *   text     - Tooltip content (required)
 *   position - Tooltip position: "top" (default), "bottom", "left", "right"
 *
 * The component wraps its content (default: "i") in a focusable button
 * and shows the tooltip on hover or focus.
 */

let tooltipIdCounter = 0;

class QlTooltip extends HTMLElement {
    constructor() {
        super();
        this._tooltipId = `ql-tooltip-${++tooltipIdCounter}`;
    }

    connectedCallback() {
        this._setup();
    }

    static get observedAttributes() {
        return ['text', 'position'];
    }

    attributeChangedCallback(name, oldValue, newValue) {
        if (name === 'text' && this._tooltipEl) {
            this._tooltipEl.textContent = newValue;
        }
        if (name === 'position' && this._tooltipEl) {
            this._updatePosition();
        }
    }

    _setup() {
        const text = this.getAttribute('text') || '';
        const position = this.getAttribute('position') || 'top';
        const trigger = this.textContent.trim() || 'i';

        // Clear content
        this.textContent = '';

        // Create trigger button
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'tooltip-trigger';
        button.setAttribute('aria-describedby', this._tooltipId);
        button.textContent = trigger;

        // Create tooltip
        const tooltip = document.createElement('span');
        tooltip.id = this._tooltipId;
        tooltip.className = 'tooltip-content';
        tooltip.setAttribute('role', 'tooltip');
        tooltip.textContent = text;

        // Store reference
        this._tooltipEl = tooltip;
        this._buttonEl = button;

        // Add to DOM
        this.appendChild(button);
        this.appendChild(tooltip);

        // Set position class
        this._updatePosition();

        // Add base class
        this.classList.add('ql-tooltip');
    }

    _updatePosition() {
        const position = this.getAttribute('position') || 'top';
        this._tooltipEl.className = `tooltip-content tooltip-${position}`;
    }
}

// Register component
customElements.define('ql-tooltip', QlTooltip);

export { QlTooltip };
