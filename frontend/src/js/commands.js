/**
 * Nexus Terminal — command palette (Ctrl+K).
 * Provides a searchable overlay for quick access to application commands,
 * profile connections, and utility actions.
 */

const { createElement, escapeHtml, fuzzyMatch, $ } = window.NexusTerminal;

class CommandPalette {
    /**
     * @param {import('./app.js').App} app
     */
    constructor(app) {
        this.app = app;

        /** @type {HTMLElement|null} */
        this._overlay = null;

        /** @type {string} Current filter query. */
        this._query = '';

        /** @type {number} Index of the highlighted command. */
        this._selectedIndex = 0;
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Toggles the command palette open/closed.
     */
    toggle() {
        if (this._overlay) {
            this.close();
        } else {
            this.open();
        }
    }

    /**
     * Opens the command palette.
     */
    open() {
        if (this._overlay) return;
        this._query = '';
        this._selectedIndex = 0;
        this._render();
    }

    /**
     * Closes the command palette.
     */
    close() {
        if (this._overlay) {
            this._overlay.remove();
            this._overlay = null;
        }
    }

    // ── Command definitions ──────────────────────────────────────────

    /**
     * Returns the full list of available commands.
     * @returns {Array<{id: string, label: string, shortcut?: string, section?: string, action: Function}>}
     */
    getCommands() {
        const cmds = [];

        // Profile connections
        const profiles = this.app.state.profiles || [];
        for (const p of profiles) {
            cmds.push({
                id: `connect-${p.id}`,
                label: `Connect to ${p.displayName || p.host}`,
                section: 'Connections',
                detail: `${p.protocol.toUpperCase()} — ${p.host}:${p.port}`,
                action: () => this.app.connectProfile(p.id),
            });
        }

        // New connections
        cmds.push({
            id: 'new-ssh',
            label: 'New SSH Connection',
            section: 'Actions',
            shortcut: '⌘N',
            action: () => this.app.profileEditor.openNew('ssh'),
        });
        cmds.push({
            id: 'new-telnet',
            label: 'New Telnet Connection',
            section: 'Actions',
            action: () => this.app.profileEditor.openNew('telnet'),
        });
        cmds.push({
            id: 'new-raw',
            label: 'New RAW Connection',
            section: 'Actions',
            action: () => this.app.profileEditor.openNew('raw'),
        });

        // Quick connect
        cmds.push({
            id: 'quick-connect',
            label: 'Quick Connect…',
            section: 'Actions',
            action: () => this.app.quickConnect(),
        });

        // Tab actions
        cmds.push({
            id: 'close-tab',
            label: 'Close Tab',
            section: 'Tabs',
            shortcut: '⌘W',
            action: () => this.app.tabs.closeActive(),
        });
        cmds.push({
            id: 'next-tab',
            label: 'Next Tab',
            section: 'Tabs',
            shortcut: '⌘Tab',
            action: () => {
                const next = this.app.tabs.getNextTab();
                if (next) this.app.tabs.setActive(next);
            },
        });
        cmds.push({
            id: 'prev-tab',
            label: 'Previous Tab',
            section: 'Tabs',
            shortcut: '⌘⇧Tab',
            action: () => {
                const prev = this.app.tabs.getPrevTab();
                if (prev) this.app.tabs.setActive(prev);
            },
        });

        // Settings & utility
        cmds.push({
            id: 'settings',
            label: 'Settings',
            section: 'Application',
            action: () => this.app.settings.open(),
        });
        cmds.push({
            id: 'import-profiles',
            label: 'Import Profiles',
            section: 'Application',
            action: () => this.app.importProfiles(),
        });
        cmds.push({
            id: 'export-profiles',
            label: 'Export Profiles',
            section: 'Application',
            action: () => this.app.exportProfiles(),
        });

        // Secret store
        if (this.app.state.secretStoreUnlocked) {
            cmds.push({
                id: 'lock-secrets',
                label: 'Lock Secret Store',
                section: 'Security',
                action: async () => {
                    try {
                        await window.go.api.Service.LockSecretStore();
                        this.app.state.setSecretStoreState(false);
                        this.app.notifications.info('Secret store locked');
                    } catch (err) {
                        this.app.notifications.error('Failed to lock: ' + err);
                    }
                },
            });
        } else {
            cmds.push({
                id: 'unlock-secrets',
                label: 'Unlock Secret Store',
                section: 'Security',
                action: async () => {
                    try {
                        const pw = await this.app.secretPrompt.promptMasterPassword(false);
                        await window.go.api.Service.UnlockSecretStore(pw);
                        this.app.state.setSecretStoreState(true);
                        this.app.notifications.success('Secret store unlocked');
                    } catch (err) {
                        if (err.message !== 'User cancelled') {
                            this.app.notifications.error('Unlock failed: ' + err);
                        }
                    }
                },
            });
        }

        // Terminal actions (only when a session is active)
        if (this.app.state.activeSessionId) {
            cmds.push({
                id: 'clear-terminal',
                label: 'Clear Terminal',
                section: 'Terminal',
                action: () => this.app.terminals.clearTerminal(this.app.state.activeSessionId),
            });
            cmds.push({
                id: 'reset-terminal',
                label: 'Reset Terminal',
                section: 'Terminal',
                action: () => this.app.terminals.resetTerminal(this.app.state.activeSessionId),
            });
        }

        return cmds;
    }

    /**
     * Returns commands filtered and sorted by the current query.
     * @param {string} query
     * @returns {Array<Object>}
     */
    filter(query) {
        if (!query) return this.getCommands();
        return this.getCommands()
            .map((cmd) => {
                const result = fuzzyMatch(cmd.label + ' ' + (cmd.detail || ''), query);
                return { ...cmd, ...result };
            })
            .filter((c) => c.match)
            .sort((a, b) => b.score - a.score);
    }

    /**
     * Executes a command and closes the palette.
     * @param {Object} command
     */
    execute(command) {
        this.close();
        if (command && typeof command.action === 'function') {
            command.action();
        }
    }

    // ── Rendering ────────────────────────────────────────────────────

    /** @private */
    _render() {
        const overlay = createElement('div', 'palette-overlay');
        const palette = createElement('div', 'palette');

        // Search input
        const input = createElement('input', 'palette-input', {
            type: 'text',
            placeholder: 'Type a command…',
            autocomplete: 'off',
            spellcheck: 'false',
        });

        palette.appendChild(input);

        // Results list
        const list = createElement('div', 'palette-list');
        palette.appendChild(list);

        overlay.appendChild(palette);
        document.body.appendChild(overlay);
        this._overlay = overlay;

        // Initial render
        this._renderList(list, this.filter(''));

        // Focus the input
        input.focus();

        // Event handlers
        input.addEventListener('input', () => {
            this._query = input.value;
            this._selectedIndex = 0;
            this._renderList(list, this.filter(this._query));
        });

        input.addEventListener('keydown', (e) => {
            const items = list.querySelectorAll('.palette-item');
            switch (e.key) {
                case 'ArrowDown':
                    e.preventDefault();
                    this._selectedIndex = Math.min(this._selectedIndex + 1, items.length - 1);
                    this._highlightItem(items);
                    break;
                case 'ArrowUp':
                    e.preventDefault();
                    this._selectedIndex = Math.max(this._selectedIndex - 1, 0);
                    this._highlightItem(items);
                    break;
                case 'Enter': {
                    e.preventDefault();
                    const selected = items[this._selectedIndex];
                    if (selected && selected._command) {
                        this.execute(selected._command);
                    }
                    break;
                }
                case 'Escape':
                    e.preventDefault();
                    this.close();
                    break;
            }
        });

        // Click outside to close
        overlay.addEventListener('click', (e) => {
            if (e.target === overlay) this.close();
        });
    }

    /**
     * Renders the filtered command list.
     * @param {HTMLElement} list
     * @param {Array<Object>} commands
     * @private
     */
    _renderList(list, commands) {
        list.innerHTML = '';
        let currentSection = null;

        if (commands.length === 0) {
            const empty = createElement('div', 'palette-empty', { textContent: 'No commands found' });
            list.appendChild(empty);
            return;
        }

        for (let i = 0; i < commands.length; i++) {
            const cmd = commands[i];

            // Section header
            if (cmd.section && cmd.section !== currentSection) {
                currentSection = cmd.section;
                const sectionEl = createElement('div', 'palette-section', { textContent: cmd.section });
                list.appendChild(sectionEl);
            }

            const item = createElement('div', `palette-item ${i === this._selectedIndex ? 'palette-item-active' : ''}`);
            item._command = cmd;

            // Label
            const label = createElement('span', 'palette-item-label', { textContent: cmd.label });
            item.appendChild(label);

            // Detail
            if (cmd.detail) {
                const detail = createElement('span', 'palette-item-detail', { textContent: cmd.detail });
                item.appendChild(detail);
            }

            // Shortcut badge
            if (cmd.shortcut) {
                const badge = createElement('span', 'palette-item-shortcut', { textContent: cmd.shortcut });
                item.appendChild(badge);
            }

            item.addEventListener('click', () => this.execute(cmd));
            item.addEventListener('mouseenter', () => {
                this._selectedIndex = i;
                this._highlightItem(list.querySelectorAll('.palette-item'));
            });

            list.appendChild(item);
        }
    }

    /**
     * Highlights the currently selected item and scrolls it into view.
     * @param {NodeListOf<Element>} items
     * @private
     */
    _highlightItem(items) {
        items.forEach((el, i) => {
            el.classList.toggle('palette-item-active', i === this._selectedIndex);
        });
        const active = items[this._selectedIndex];
        if (active) {
            active.scrollIntoView({ block: 'nearest' });
        }
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.CommandPalette = CommandPalette;
