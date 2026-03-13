/**
 * <ql-modal> Web Component
 *
 * A modal dialog with built-in accessibility (ARIA) and keyboard handling.
 *
 * Usage:
 *   <ql-modal id="myModal">
 *     <span slot="title">Modal Title</span>
 *     <div slot="body">Modal content here</div>
 *   </ql-modal>
 *
 * With actions:
 *   <ql-modal id="myModal">
 *     <span slot="title">Modal Title</span>
 *     <div slot="actions">
 *       <button onclick="...">Action</button>
 *     </div>
 *     <div slot="body">Modal content here</div>
 *   </ql-modal>
 *
 * API:
 *   modal.open()   - Open the modal
 *   modal.close()  - Close the modal
 *   modal.isOpen   - Check if modal is open
 *
 * Attributes:
 *   open      - Modal is open (reflects state)
 *   closable  - Can be closed by Escape/click-outside (default: true)
 *   size      - Modal size: "default", "large", "fullscreen"
 *
 * Events:
 *   modal-open   - Fired when modal opens
 *   modal-close  - Fired when modal closes
 */

class QlModal extends HTMLElement {
    constructor() {
        super();
        this._isOpen = false;
        this._previousActiveElement = null;
        this._handleKeydown = this._handleKeydown.bind(this);
        this._handleOverlayClick = this._handleOverlayClick.bind(this);
    }

    static get observedAttributes() {
        return ['open'];
    }

    connectedCallback() {
        this._setup();
    }

    disconnectedCallback() {
        this._removeListeners();
    }

    attributeChangedCallback(name, oldValue, newValue) {
        if (name === 'open') {
            if (newValue !== null) {
                this._show();
            } else {
                this._hide();
            }
        }
    }

    _setup() {
        // Create modal structure
        const titleSlot = this.querySelector('[slot="title"]');
        const actionsSlot = this.querySelector('[slot="actions"]');
        const bodySlot = this.querySelector('[slot="body"]');

        // Generate unique ID for title
        const titleId = `ql-modal-title-${Date.now()}`;

        // Build header
        const header = document.createElement('div');
        header.className = 'modal-header';

        const titleWrapper = document.createElement('h3');
        titleWrapper.id = titleId;
        if (titleSlot) {
            titleWrapper.appendChild(titleSlot);
        }
        header.appendChild(titleWrapper);

        // Actions in header (optional)
        if (actionsSlot) {
            const actionsWrapper = document.createElement('div');
            actionsWrapper.className = 'modal-actions';
            actionsWrapper.appendChild(actionsSlot);
            header.appendChild(actionsWrapper);
        }

        // Close button
        const closeBtn = document.createElement('button');
        closeBtn.className = 'modal-close';
        closeBtn.innerHTML = '&times;';
        closeBtn.setAttribute('aria-label', 'Close');
        closeBtn.addEventListener('click', () => this.close());
        header.appendChild(closeBtn);

        // Build body
        const body = document.createElement('div');
        body.className = 'modal-body';
        if (bodySlot) {
            body.appendChild(bodySlot);
        }

        // Build modal container
        const modal = document.createElement('div');
        modal.className = 'modal';
        modal.setAttribute('role', 'dialog');
        modal.setAttribute('aria-modal', 'true');
        modal.setAttribute('aria-labelledby', titleId);
        modal.appendChild(header);
        modal.appendChild(body);

        // Apply size
        const size = this.getAttribute('size');
        if (size === 'large') {
            modal.classList.add('modal-large');
        } else if (size === 'fullscreen') {
            modal.classList.add('modal-fullscreen');
        }

        // Clear and setup overlay
        this.innerHTML = '';
        this.className = 'modal-overlay';
        this.appendChild(modal);

        // Store references
        this._modal = modal;
        this._closeBtn = closeBtn;
        this._body = body;

        // Check initial state
        if (this.hasAttribute('open')) {
            this._show();
        }
    }

    _show() {
        if (this._isOpen) return;
        this._isOpen = true;

        // Store current focus
        this._previousActiveElement = document.activeElement;

        // Show modal
        this.classList.add('active');

        // Add listeners
        document.addEventListener('keydown', this._handleKeydown);
        this.addEventListener('click', this._handleOverlayClick);

        // Focus first focusable element or close button
        requestAnimationFrame(() => {
            const focusable = this._modal.querySelector(
                'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
            );
            if (focusable) {
                focusable.focus();
            }
        });

        // Prevent body scroll
        document.body.style.overflow = 'hidden';

        // Dispatch event
        this.dispatchEvent(new CustomEvent('modal-open', { bubbles: true }));
    }

    _hide() {
        if (!this._isOpen) return;
        this._isOpen = false;

        // Hide modal
        this.classList.remove('active');

        // Remove listeners
        this._removeListeners();

        // Restore body scroll
        document.body.style.overflow = '';

        // Restore focus
        if (this._previousActiveElement) {
            this._previousActiveElement.focus();
        }

        // Dispatch event
        this.dispatchEvent(new CustomEvent('modal-close', { bubbles: true }));
    }

    _removeListeners() {
        document.removeEventListener('keydown', this._handleKeydown);
        this.removeEventListener('click', this._handleOverlayClick);
    }

    _handleKeydown(e) {
        if (e.key === 'Escape' && this._isClosable()) {
            e.preventDefault();
            this.close();
            return;
        }

        // Focus trap
        if (e.key === 'Tab') {
            const focusables = this._modal.querySelectorAll(
                'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
            );
            if (focusables.length === 0) return;

            const first = focusables[0];
            const last = focusables[focusables.length - 1];

            if (e.shiftKey && document.activeElement === first) {
                e.preventDefault();
                last.focus();
            } else if (!e.shiftKey && document.activeElement === last) {
                e.preventDefault();
                first.focus();
            }
        }
    }

    _handleOverlayClick(e) {
        // Close only if clicking directly on overlay (not modal content)
        if (e.target === this && this._isClosable()) {
            this.close();
        }
    }

    _isClosable() {
        return this.getAttribute('closable') !== 'false';
    }

    // Public API
    open() {
        this.setAttribute('open', '');
    }

    close() {
        this.removeAttribute('open');
    }

    get isOpen() {
        return this._isOpen;
    }

    // Allow setting body content directly
    set bodyContent(html) {
        if (this._body) {
            this._body.innerHTML = html;
        }
    }

    get bodyElement() {
        return this._body;
    }
}

// Register component
customElements.define('ql-modal', QlModal);

export { QlModal };
