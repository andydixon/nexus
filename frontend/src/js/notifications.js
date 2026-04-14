(function() {
/**
 * Nexus Terminal — toast notification system.
 * Provides non-blocking, auto-dismissing notifications that stack
 * in the top-right corner of the viewport.
 */

const { createElement, escapeHtml } = window.NexusTerminal;

/** Small SVG icons for each notification type (16×16 viewBox). */
const ICONS = {
    success: '✓',
    error: '✕',
    warning: '!',
    info: 'i',
};

class Notifications {
    constructor() {
        /** @type {HTMLElement} */
        this.container = document.getElementById('notifications');
        if (!this.container) {
            this.container = createElement('div', 'notifications-container', { id: 'notifications' });
            document.body.appendChild(this.container);
        }
    }

    /**
     * Displays a notification.
     * @param {string} message - Notification text.
     * @param {'info'|'success'|'warning'|'error'} [type='info'] - Severity.
     * @param {number} [duration=4000] - Auto-dismiss delay in ms.
     */
    show(message, type = 'info', duration = 4000) {
        const el = createElement('div', `notification notification-${type}`);

        // Icon
        const icon = createElement('span', 'notification-icon', {
            textContent: ICONS[type] || ICONS.info,
        });
        el.appendChild(icon);

        // Body
        const body = createElement('div', 'notification-body');
        const msg = createElement('span', 'notification-message', { textContent: message });
        body.appendChild(msg);
        el.appendChild(body);

        // Dismiss button
        const close = createElement('button', 'notification-close', {
            textContent: '×',
            'aria-label': 'Dismiss',
        });
        close.addEventListener('click', () => this._dismiss(el));
        el.appendChild(close);

        // Insert at the top of the stack
        this.container.prepend(el);

        // Auto-dismiss
        if (duration > 0) {
            el._timer = setTimeout(() => this._dismiss(el), duration);
        }
    }

    /** @param {string} message */
    success(message) { this.show(message, 'success'); }

    /** @param {string} message */
    error(message) { this.show(message, 'error', 6000); }

    /** @param {string} message */
    warning(message) { this.show(message, 'warning'); }

    /** @param {string} message */
    info(message) { this.show(message, 'info'); }

    /**
     * Animates out and removes a notification element.
     * @param {HTMLElement} el
     * @private
     */
    _dismiss(el) {
        if (el._dismissed) return;
        el._dismissed = true;
        clearTimeout(el._timer);

        el.classList.add('is-leaving');
        el.addEventListener('animationend', () => el.remove(), { once: true });

        // Safety net — remove after 500 ms even if animation event does not fire
        setTimeout(() => {
            if (el.parentNode) el.remove();
        }, 500);
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.Notifications = Notifications;

})();
