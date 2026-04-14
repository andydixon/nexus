/**
 * Nexus Terminal — toast notification system.
 * Provides non-blocking, auto-dismissing notifications that stack
 * in the top-right corner of the viewport.
 */

const { createElement, escapeHtml } = window.NexusTerminal;

/** Map of notification type to SVG icon path data. */
const ICONS = {
    success: 'M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z',
    error: 'M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z',
    warning: 'M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z',
    info: 'M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
};

class Notifications {
    constructor() {
        /** @type {HTMLElement} */
        this.container = document.getElementById('notifications');
        if (!this.container) {
            // Create the container if it does not yet exist
            this.container = createElement('div', 'notifications-container', { id: 'notifications' });
            document.body.appendChild(this.container);
        }
    }

    /**
     * Displays a toast notification.
     * @param {string} message - Notification text.
     * @param {'info'|'success'|'warning'|'error'} [type='info'] - Severity.
     * @param {number} [duration=4000] - Auto-dismiss delay in ms. Pass 0 to keep until dismissed.
     */
    show(message, type = 'info', duration = 4000) {
        const toast = createElement('div', `toast toast-${type}`);

        // Icon
        const iconSvg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        iconSvg.setAttribute('class', 'toast-icon');
        iconSvg.setAttribute('viewBox', '0 0 24 24');
        iconSvg.setAttribute('fill', 'none');
        iconSvg.setAttribute('stroke', 'currentColor');
        iconSvg.setAttribute('stroke-width', '2');
        const iconPath = document.createElementNS('http://www.w3.org/2000/svg', 'path');
        iconPath.setAttribute('stroke-linecap', 'round');
        iconPath.setAttribute('stroke-linejoin', 'round');
        iconPath.setAttribute('d', ICONS[type] || ICONS.info);
        iconSvg.appendChild(iconPath);
        toast.appendChild(iconSvg);

        // Message
        const msg = createElement('span', 'toast-message', { textContent: message });
        toast.appendChild(msg);

        // Dismiss button
        const close = createElement('button', 'toast-close', { textContent: '×', 'aria-label': 'Dismiss' });
        close.addEventListener('click', () => this._dismiss(toast));
        toast.appendChild(close);

        // Insert at the top of the stack
        this.container.prepend(toast);

        // Trigger enter animation on next frame
        requestAnimationFrame(() => {
            toast.classList.add('toast-enter');
        });

        // Auto-dismiss
        if (duration > 0) {
            toast._timer = setTimeout(() => this._dismiss(toast), duration);
        }
    }

    /**
     * Shorthand: success notification.
     * @param {string} message
     */
    success(message) {
        this.show(message, 'success');
    }

    /**
     * Shorthand: error notification (longer display time).
     * @param {string} message
     */
    error(message) {
        this.show(message, 'error', 6000);
    }

    /**
     * Shorthand: warning notification.
     * @param {string} message
     */
    warning(message) {
        this.show(message, 'warning');
    }

    /**
     * Shorthand: informational notification.
     * @param {string} message
     */
    info(message) {
        this.show(message, 'info');
    }

    /**
     * Animates out and removes a toast element.
     * @param {HTMLElement} toast
     * @private
     */
    _dismiss(toast) {
        if (toast._dismissed) return;
        toast._dismissed = true;
        clearTimeout(toast._timer);

        toast.classList.add('toast-exit');
        toast.addEventListener('animationend', () => toast.remove(), { once: true });

        // Safety net — remove after 500 ms even if animation event does not fire
        setTimeout(() => {
            if (toast.parentNode) toast.remove();
        }, 500);
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.Notifications = Notifications;
