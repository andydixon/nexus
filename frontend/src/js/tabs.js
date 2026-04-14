/**
 * Nexus Terminal — tab management.
 * Handles the tab bar: adding, removing, reordering, renaming,
 * pinning tabs, and context menus.
 */

const { createElement, escapeHtml, protocolColour, stateLabel, dismissContextMenus } = window.NexusTerminal;

class TabManager {
    /**
     * @param {import('./app.js').App} app
     */
    constructor(app) {
        this.app = app;

        /** @type {HTMLElement} The inner tab list where tab elements are inserted. */
        this.tabList = document.getElementById('tab-list');

        /** @type {HTMLElement} The outer tab bar wrapper (used for drag-and-drop). */
        this.tabBar = document.getElementById('tab-bar');

        /** @type {HTMLElement} The container for terminal/viewer panels. */
        this.panelContainer = document.getElementById('panel-container');

        /** @type {string[]} Ordered list of session IDs. */
        this.order = [];

        this._setupDragAndDrop();
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Adds a new tab for a session and makes it active.
     * @param {Object} sessionInfo
     */
    addTab(sessionInfo) {
        this.order.push(sessionInfo.id);
        this._renderTab(sessionInfo);

        // Create panel container for this session's terminal/viewer
        const panel = createElement('div', 'session-panel', {
            id: `panel-${sessionInfo.id}`,
            dataset: { sessionId: sessionInfo.id },
        });
        panel.style.display = 'none';
        this.panelContainer.appendChild(panel);

        this.setActive(sessionInfo.id);
        this._updateEmptyState();
    }

    /**
     * Removes a tab and its panel.
     * @param {string} sessionId
     */
    removeTab(sessionId) {
        const idx = this.order.indexOf(sessionId);
        if (idx !== -1) this.order.splice(idx, 1);

        // Remove tab element
        const tabEl = this.tabList.querySelector(`[data-session-id="${sessionId}"]`);
        if (tabEl) tabEl.remove();

        // Remove panel
        const panel = document.getElementById(`panel-${sessionId}`);
        if (panel) panel.remove();

        // If this was the active tab, activate the nearest neighbour
        if (this.app.state.activeSessionId === sessionId) {
            const next = this.order.length > 0
                ? this.order[Math.min(idx, this.order.length - 1)]
                : null;
            if (next) {
                this.setActive(next);
            } else {
                this.app.state.setActiveSession(null);
            }
        }

        this._updateEmptyState();
    }

    /**
     * Switches the visible tab/panel to the given session.
     * @param {string} sessionId
     */
    setActive(sessionId) {
        this.app.state.setActiveSession(sessionId);

        // Highlight active tab
        this.tabList.querySelectorAll('.tab').forEach((t) => {
            t.classList.toggle('tab-active', t.dataset.sessionId === sessionId);
        });

        // Show corresponding panel, hide others
        this.panelContainer.querySelectorAll('.session-panel').forEach((p) => {
            p.style.display = p.dataset.sessionId === sessionId ? 'flex' : 'none';
        });

        // Notify sidebar
        const session = this.app.state.getSession(sessionId);
        if (session) {
            this.app.sidebar.highlightActive(session.profileId);
        }

        // Fit the terminal for the newly active session
        this.app.terminals.fitActive();
    }

    /**
     * Closes the currently active tab.
     */
    closeActive() {
        if (this.app.state.activeSessionId) {
            this.closeTab(this.app.state.activeSessionId);
        }
    }

    /**
     * Disconnects and closes a tab by session ID.
     * @param {string} sessionId
     */
    async closeTab(sessionId) {
        const session = this.app.state.getSession(sessionId);

        // Ask for confirmation if connected and the preference is set
        if (session && session.state === 'connected' && this.app.state.preferences.confirmOnDisconnect) {
            const ok = confirm('Disconnect and close this session?');
            if (!ok) return;
        }

        try {
            await window.go.api.Service.DisconnectSession(sessionId);
        } catch (_) {
            // Session may already be closed — carry on
        }

        this.app.terminals.destroyTerminal(sessionId);
        this.app.rawViewer.destroyViewer(sessionId);
        this.app.state.removeSession(sessionId);
        this.removeTab(sessionId);
    }

    /**
     * Renames a tab.
     * @param {string} sessionId
     * @param {string} name
     */
    renameTab(sessionId, name) {
        this.app.state.updateSession(sessionId, { tabName: name });
        const tabEl = this.tabList.querySelector(`[data-session-id="${sessionId}"]`);
        if (tabEl) {
            tabEl.querySelector('.tab-name').textContent = name;
        }
    }

    /**
     * Pins a tab (moves it to the front).
     * @param {string} sessionId
     */
    pinTab(sessionId) {
        this.app.state.updateSession(sessionId, { pinned: true });
        this._reorderDOM();
        this._updateTabStyles(sessionId);
    }

    /**
     * Unpins a tab.
     * @param {string} sessionId
     */
    unpinTab(sessionId) {
        this.app.state.updateSession(sessionId, { pinned: false });
        this._reorderDOM();
        this._updateTabStyles(sessionId);
    }

    /**
     * Moves a tab one position to the left.
     * @param {string} sessionId
     */
    moveLeft(sessionId) {
        const idx = this.order.indexOf(sessionId);
        if (idx > 0) {
            [this.order[idx - 1], this.order[idx]] = [this.order[idx], this.order[idx - 1]];
            this._reorderDOM();
        }
    }

    /**
     * Moves a tab one position to the right.
     * @param {string} sessionId
     */
    moveRight(sessionId) {
        const idx = this.order.indexOf(sessionId);
        if (idx >= 0 && idx < this.order.length - 1) {
            [this.order[idx], this.order[idx + 1]] = [this.order[idx + 1], this.order[idx]];
            this._reorderDOM();
        }
    }

    /**
     * Opens a new session with the same profile as the given tab.
     * @param {string} sessionId
     */
    async duplicateTab(sessionId) {
        const session = this.app.state.getSession(sessionId);
        if (session && session.profileId) {
            await this.app.connectProfile(session.profileId);
        }
    }

    /**
     * Updates the visual state of a tab when session state changes.
     * @param {string} sessionId
     * @param {string} state - connecting | connected | disconnected | error
     * @param {string} [error]
     */
    updateSessionState(sessionId, state, error) {
        this.app.state.updateSession(sessionId, { state, error: error || '' });

        const tabEl = this.tabList.querySelector(`[data-session-id="${sessionId}"]`);
        if (!tabEl) return;

        const dot = tabEl.querySelector('.tab-status');
        if (dot) {
            dot.className = `tab-status status-${state}`;
            dot.title = stateLabel(state);
        }

        if (state === 'error' && error) {
            this.app.notifications.error(`${this._tabLabel(sessionId)}: ${error}`);
        }
    }

    /**
     * Handles a session closed event from the backend.
     * @param {string} sessionId
     */
    handleSessionClosed(sessionId) {
        this.updateSessionState(sessionId, 'disconnected');
    }

    /**
     * Shows a right-click context menu for a tab.
     * @param {string} sessionId
     * @param {number} x
     * @param {number} y
     */
    showContextMenu(sessionId, x, y) {
        dismissContextMenus();
        const session = this.app.state.getSession(sessionId);
        if (!session) return;

        const menu = createElement('div', 'context-menu');
        menu.style.left = `${x}px`;
        menu.style.top = `${y}px`;

        const isPinned = session.pinned;

        const items = [
            { label: 'Rename…', action: () => this._promptRename(sessionId) },
            { label: 'Duplicate', action: () => this.duplicateTab(sessionId) },
            { divider: true },
            { label: isPinned ? 'Unpin' : 'Pin', action: () => isPinned ? this.unpinTab(sessionId) : this.pinTab(sessionId) },
            { label: 'Move Left', action: () => this.moveLeft(sessionId) },
            { label: 'Move Right', action: () => this.moveRight(sessionId) },
            { divider: true },
            {
                label: 'Reconnect',
                action: () => this._reconnect(sessionId),
                disabled: session.state === 'connected' || session.state === 'connecting',
            },
            { divider: true },
            { label: 'Close', className: 'context-menu-danger', action: () => this.closeTab(sessionId) },
        ];

        for (const entry of items) {
            if (entry.divider) {
                menu.appendChild(createElement('div', 'context-menu-divider'));
                continue;
            }
            const btn = createElement('button', `context-menu-item ${entry.className || ''}`, {
                textContent: entry.label,
            });
            if (entry.disabled) {
                btn.disabled = true;
                btn.classList.add('context-menu-disabled');
            }
            btn.addEventListener('click', () => {
                dismissContextMenus();
                entry.action();
            });
            menu.appendChild(btn);
        }

        document.body.appendChild(menu);

        // Keep within viewport
        const rect = menu.getBoundingClientRect();
        if (rect.right > window.innerWidth) menu.style.left = `${window.innerWidth - rect.width - 8}px`;
        if (rect.bottom > window.innerHeight) menu.style.top = `${window.innerHeight - rect.height - 8}px`;
    }

    /**
     * Returns the current tab ordering.
     * @returns {string[]}
     */
    getTabOrder() {
        return [...this.order];
    }

    /**
     * Returns the session ID of the next tab (wrapping round).
     * @returns {string|null}
     */
    getNextTab() {
        if (this.order.length === 0) return null;
        const idx = this.order.indexOf(this.app.state.activeSessionId);
        return this.order[(idx + 1) % this.order.length];
    }

    /**
     * Returns the session ID of the previous tab (wrapping round).
     * @returns {string|null}
     */
    getPrevTab() {
        if (this.order.length === 0) return null;
        const idx = this.order.indexOf(this.app.state.activeSessionId);
        return this.order[(idx - 1 + this.order.length) % this.order.length];
    }

    // ── Rendering ────────────────────────────────────────────────────

    /**
     * Creates and appends a tab element.
     * @param {Object} sessionInfo
     * @private
     */
    _renderTab(sessionInfo) {
        const tab = createElement('div', 'tab', {
            dataset: { sessionId: sessionInfo.id },
            draggable: 'true',
        });

        // Protocol badge
        const badge = createElement('span', `tab-badge ${protocolColour(sessionInfo.protocol)}`, {
            textContent: sessionInfo.protocol.toUpperCase(),
        });
        tab.appendChild(badge);

        // Name
        const name = createElement('span', 'tab-name', {
            textContent: sessionInfo.tabName || sessionInfo.protocol.toUpperCase(),
        });
        tab.appendChild(name);

        // Status dot
        const status = createElement('span', `tab-status status-${sessionInfo.state || 'connecting'}`, {
            title: stateLabel(sessionInfo.state),
        });
        tab.appendChild(status);

        // Pin indicator
        if (sessionInfo.pinned) {
            tab.classList.add('tab-pinned');
        }

        // Close button
        const closeBtn = createElement('button', 'tab-close', {
            textContent: '×',
            'aria-label': 'Close tab',
        });
        closeBtn.addEventListener('click', (e) => {
            e.stopPropagation();
            this.closeTab(sessionInfo.id);
        });
        tab.appendChild(closeBtn);

        // Click → activate
        tab.addEventListener('click', () => this.setActive(sessionInfo.id));

        // Right-click → context menu
        tab.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            this.showContextMenu(sessionInfo.id, e.clientX, e.clientY);
        });

        this.tabList.appendChild(tab);
    }

    // ── Internal helpers ─────────────────────────────────────────────

    /**
     * Reorders the tab-bar DOM elements to match `this.order`,
     * with pinned tabs always first.
     * @private
     */
    _reorderDOM() {
        // Sort: pinned first, then by order index
        const sorted = [...this.order].sort((a, b) => {
            const sa = this.app.state.getSession(a);
            const sb = this.app.state.getSession(b);
            const pinnedA = sa?.pinned ? 0 : 1;
            const pinnedB = sb?.pinned ? 0 : 1;
            if (pinnedA !== pinnedB) return pinnedA - pinnedB;
            return this.order.indexOf(a) - this.order.indexOf(b);
        });

        for (const id of sorted) {
            const el = this.tabList.querySelector(`[data-session-id="${id}"]`);
            if (el) this.tabList.appendChild(el);
        }
    }

    /**
     * Updates the visual pin state on a tab element.
     * @param {string} sessionId
     * @private
     */
    _updateTabStyles(sessionId) {
        const session = this.app.state.getSession(sessionId);
        const tabEl = this.tabList.querySelector(`[data-session-id="${sessionId}"]`);
        if (tabEl && session) {
            tabEl.classList.toggle('tab-pinned', !!session.pinned);
        }
    }

    /**
     * Shows/hides the empty-state panel based on whether tabs exist.
     * @private
     */
    _updateEmptyState() {
        const empty = document.getElementById('empty-state');
        if (empty) {
            empty.style.display = this.order.length === 0 ? 'flex' : 'none';
        }
    }

    /**
     * Prompts the user to rename a tab.
     * @param {string} sessionId
     * @private
     */
    _promptRename(sessionId) {
        const session = this.app.state.getSession(sessionId);
        const current = session?.tabName || '';
        const name = prompt('Tab name:', current);
        if (name !== null && name.trim()) {
            this.renameTab(sessionId, name.trim());
        }
    }

    /**
     * Attempts to reconnect a disconnected session.
     * @param {string} sessionId
     * @private
     */
    async _reconnect(sessionId) {
        const session = this.app.state.getSession(sessionId);
        if (!session || !session.profileId) return;

        // Close the current tab, then open a fresh connection
        this.app.terminals.destroyTerminal(sessionId);
        this.app.rawViewer.destroyViewer(sessionId);
        this.app.state.removeSession(sessionId);
        this.removeTab(sessionId);

        await this.app.connectProfile(session.profileId);
    }

    /**
     * Returns the display label for a session's tab.
     * @param {string} sessionId
     * @returns {string}
     * @private
     */
    _tabLabel(sessionId) {
        const session = this.app.state.getSession(sessionId);
        return session?.tabName || 'Session';
    }

    // ── Drag and drop ────────────────────────────────────────────────

    /**
     * Sets up drag-and-drop reordering on the tab bar.
     * @private
     */
    _setupDragAndDrop() {
        if (!this.tabList) return;

        let draggedId = null;

        this.tabList.addEventListener('dragstart', (e) => {
            const tab = e.target.closest('.tab');
            if (!tab) return;
            draggedId = tab.dataset.sessionId;
            tab.classList.add('tab-dragging');
            e.dataTransfer.effectAllowed = 'move';
        });

        this.tabList.addEventListener('dragover', (e) => {
            e.preventDefault();
            e.dataTransfer.dropEffect = 'move';

            const target = e.target.closest('.tab');
            if (!target || target.dataset.sessionId === draggedId) return;

            const rect = target.getBoundingClientRect();
            const midX = rect.left + rect.width / 2;
            target.classList.toggle('tab-drop-before', e.clientX < midX);
            target.classList.toggle('tab-drop-after', e.clientX >= midX);
        });

        this.tabList.addEventListener('dragleave', (e) => {
            const target = e.target.closest('.tab');
            if (target) {
                target.classList.remove('tab-drop-before', 'tab-drop-after');
            }
        });

        this.tabList.addEventListener('drop', (e) => {
            e.preventDefault();
            const target = e.target.closest('.tab');
            if (!target || !draggedId) return;

            target.classList.remove('tab-drop-before', 'tab-drop-after');

            const targetId = target.dataset.sessionId;
            if (targetId === draggedId) return;

            // Reorder
            const fromIdx = this.order.indexOf(draggedId);
            const toIdx = this.order.indexOf(targetId);
            if (fromIdx === -1 || toIdx === -1) return;

            this.order.splice(fromIdx, 1);
            this.order.splice(toIdx, 0, draggedId);
            this._reorderDOM();
        });

        this.tabList.addEventListener('dragend', () => {
            this.tabList.querySelectorAll('.tab').forEach((t) => {
                t.classList.remove('tab-dragging', 'tab-drop-before', 'tab-drop-after');
            });
            draggedId = null;
        });
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.TabManager = TabManager;
