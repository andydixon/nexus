(function() {
/**
 * Nexus Terminal — main application entry point.
 * Initialises the application shell, loads profiles, and sets up event listeners.
 */

const { State, Sidebar, TabManager, TerminalManager, ProfileEditor, RawViewer, Notifications, Settings, CommandPalette, SecretPrompt, stateLabel } = window.NexusTerminal;

class App {
    constructor() {
        this.state = new State();
        this.notifications = new Notifications();
        this.sidebar = new Sidebar(this);
        this.tabs = new TabManager(this);
        this.terminals = new TerminalManager(this);
        this.profileEditor = new ProfileEditor(this);
        this.rawViewer = new RawViewer(this);
        this.settings = new Settings(this);
        this.commandPalette = new CommandPalette(this);
        this.secretPrompt = new SecretPrompt(this);
    }

    /**
     * Bootstraps the application after the DOM is ready.
     * UI event binding happens synchronously so buttons always work.
     * Backend data loading waits for the Wails runtime to be available.
     */
    async init() {
        // Phase 1 — bind all UI event handlers immediately (DOM-only, no backend).
        this.setupUI();
        this.setupKeyboard();
        this.updateStatusBar();

        // Phase 2 — wait for the Wails runtime, then load backend data and bind events.
        await this._waitForWailsRuntime();
        this.setupEvents();

        await this.loadProfiles();
        await this.loadPreferences();
        await this.checkSecretStore();
        this.updateStatusBar();
    }

    /**
     * Polls for `window.go` and `window.runtime` to become available.
     * Wails injects these into the webview; on fast machines they are
     * present before DOMContentLoaded, but on slower starts there may
     * be a brief delay.
     * @returns {Promise<void>}
     * @private
     */
    _waitForWailsRuntime() {
        return new Promise((resolve) => {
            const maxWait = 5000;
            const start = Date.now();
            const check = () => {
                if (window.go && window.runtime) {
                    resolve();
                } else if (Date.now() - start < maxWait) {
                    setTimeout(check, 50);
                } else {
                    console.warn('Wails runtime not detected after ' + maxWait + 'ms — proceeding anyway');
                    resolve();
                }
            };
            check();
        });
    }

    // ── Data loading ─────────────────────────────────────────────────

    /**
     * Fetches all connection profiles from the backend and renders the sidebar.
     */
    async loadProfiles() {
        try {
            const profiles = await window.go.api.Service.ListProfiles();
            this.state.setProfiles(profiles || []);
            this.sidebar.render();
        } catch (err) {
            this.notifications.error('Failed to load profiles: ' + err);
        }
    }

    /**
     * Loads user preferences from the backend.
     */
    async loadPreferences() {
        try {
            const prefs = await window.go.api.Service.GetPreferences();
            this.state.setPreferences(prefs || {});
            this._applyPreferences();
        } catch (err) {
            // Non-fatal — fall back to defaults
            console.warn('Failed to load preferences:', err);
        }
    }

    /**
     * Queries the secret store status (initialised / unlocked).
     */
    async checkSecretStore() {
        try {
            const initialised = await window.go.api.Service.IsSecretStoreInitialised();
            this.state.setSecretStoreInitialised(!!initialised);
            if (initialised) {
                const unlocked = await window.go.api.Service.IsSecretStoreUnlocked();
                this.state.setSecretStoreState(!!unlocked);
            } else {
                this.state.setSecretStoreState(false);
            }
        } catch (_) {
            // Secret store may not be available yet
        }
    }

    // ── Wails event listeners ────────────────────────────────────────

    /**
     * Registers listeners for backend events emitted via the Wails runtime.
     * Guarded: silently skips if the runtime is unavailable.
     */
    setupEvents() {
        if (!window.runtime || typeof window.runtime.EventsOn !== 'function') {
            console.warn('window.runtime not available — Wails event listeners not bound');
            return;
        }
        // Incoming session data (terminal output)
        window.runtime.EventsOn('session:data', (payload) => {
            this.terminals.handleData(payload.sessionID, payload.data);
        });

        // Session state transitions (connecting → connected → disconnected)
        window.runtime.EventsOn('session:state', (payload) => {
            this.tabs.updateSessionState(payload.sessionID, payload.state, payload.error);
            this.updateStatusBar();
            // Re-fit the terminal once connected so it picks up the final container size.
            // Use multiple delays because the panel layout may still be settling.
            if (payload.state === 'connected') {
                const fit = () => this.terminals.fitSession(payload.sessionID);
                setTimeout(fit, 30);
                setTimeout(fit, 150);
                setTimeout(fit, 500);
            }
        });

        // Session closed by backend
        window.runtime.EventsOn('session:closed', (payload) => {
            this.tabs.handleSessionClosed(payload.sessionID);
            this.updateStatusBar();
        });

        // RAW TCP data (separate channel for the raw viewer)
        window.runtime.EventsOn('session:raw:data', (payload) => {
            this.rawViewer.handleData(payload);
        });

        // Telnet negotiation debug messages
        window.runtime.EventsOn('session:debug', (payload) => {
            console.debug('[session:debug]', payload);
        });

        // Backend ready event (emitted from DomReady)
        window.runtime.EventsOn('backend:ready', () => {
            console.log('Backend is ready');
        });

        // Host key verification requests
        window.runtime.EventsOn('session:hostkey:verify', async (payload) => {
            try {
                const accepted = await this.secretPrompt.promptHostKeyVerification(
                    payload.host,
                    payload.keyType,
                    payload.fingerprint,
                );
                window.runtime.EventsEmit('session:hostkey:response', {
                    sessionID: payload.sessionID,
                    accepted,
                });
            } catch (_) {
                window.runtime.EventsEmit('session:hostkey:response', {
                    sessionID: payload.sessionID,
                    accepted: false,
                });
            }
        });

        // Password/passphrase prompt requests from the backend
        window.runtime.EventsOn('session:secret:prompt', async (payload) => {
            try {
                const value = await this.secretPrompt.prompt(
                    payload.title || 'Credentials Required',
                    payload.message || 'Enter your password or passphrase.',
                );
                window.runtime.EventsEmit('session:secret:response', {
                    requestID: payload.requestID,
                    value,
                });
            } catch (_) {
                window.runtime.EventsEmit('session:secret:response', {
                    requestID: payload.requestID,
                    value: '',
                    cancelled: true,
                });
            }
        });
    }

    // ── Keyboard shortcuts ───────────────────────────────────────────

    /**
     * Registers global keyboard shortcuts.
     */
    setupKeyboard() {
        document.addEventListener('keydown', (e) => {
            const mod = e.ctrlKey || e.metaKey;

            // Ctrl/Cmd+K — command palette
            if (mod && e.key === 'k') {
                e.preventDefault();
                this.commandPalette.toggle();
                return;
            }

            // Ctrl/Cmd+N — new connection
            if (mod && e.key === 'n') {
                e.preventDefault();
                this.profileEditor.openNew();
                return;
            }

            // Ctrl/Cmd+W — close active tab
            if (mod && e.key === 'w') {
                e.preventDefault();
                this.tabs.closeActive();
                return;
            }

            // Ctrl/Cmd+Tab — next tab
            if (mod && e.key === 'Tab' && !e.shiftKey) {
                e.preventDefault();
                const next = this.tabs.getNextTab();
                if (next) this.tabs.setActive(next);
                return;
            }

            // Ctrl/Cmd+Shift+Tab — previous tab
            if (mod && e.key === 'Tab' && e.shiftKey) {
                e.preventDefault();
                const prev = this.tabs.getPrevTab();
                if (prev) this.tabs.setActive(prev);
                return;
            }

            // Ctrl/Cmd+1-9 — switch to tab by index
            if (mod && e.key >= '1' && e.key <= '9') {
                e.preventDefault();
                const idx = parseInt(e.key, 10) - 1;
                const order = this.tabs.getTabOrder();
                if (idx < order.length) {
                    this.tabs.setActive(order[idx]);
                }
                return;
            }

            // Ctrl/Cmd+, — settings
            if (mod && e.key === ',') {
                e.preventDefault();
                this.settings.open();
                return;
            }

            // Ctrl/Cmd+F — find in terminal
            if (mod && e.key === 'f') {
                e.preventDefault();
                this._openTerminalSearch();
                return;
            }

            // Escape — close any open overlays
            if (e.key === 'Escape') {
                this.commandPalette.close();
            }
        });
    }

    // ── UI event wiring ──────────────────────────────────────────────

    /**
     * Binds click handlers to static UI elements.
     */
    setupUI() {
        var btnEl = document.getElementById('btn-new-connection');
        this._bindClick('btn-new-connection', () => this.profileEditor.openNew());
        this._bindClick('btn-settings', () => this.settings.open());
        this._bindClick('btn-import', () => this.importProfiles());
        this._bindClick('btn-new-tab', () => this.profileEditor.openNew());
        this._bindClick('btn-empty-connect', () => this.profileEditor.openNew());
        this._bindClick('btn-empty-quick', () => this.quickConnect());

        const searchInput = document.getElementById('profile-search');
        if (searchInput) {
            searchInput.addEventListener('input', (e) => this.sidebar.search(e.target.value));
        }

        // Window resize → re-fit active terminal (debounced)
        const { debounce } = window.NexusTerminal;
        window.addEventListener('resize', debounce(() => this.terminals.fitActive(), 100));
    }

    // ── Connection lifecycle ─────────────────────────────────────────

    /**
     * Opens a new session for the given profile ID.
     * @param {string} profileId
     */
    async connectProfile(profileId) {
        const profile = this.state.getProfile(profileId);
        if (!profile) {
            this.notifications.error('Profile not found');
            return;
        }

        try {
            const sessionInfo = await window.go.api.Service.ConnectSession(profileId);
            this.state.addSession(sessionInfo);
            this.tabs.addTab(sessionInfo);

            // Create the appropriate viewer
            const panelId = `panel-${sessionInfo.id}`;
            if (profile.protocol === 'raw') {
                this.rawViewer.createViewer(sessionInfo.id, panelId, profile);
            } else {
                this.terminals.createTerminal(sessionInfo.id, panelId, profile);
            }

            this.notifications.info(`Connecting to ${profile.displayName || profile.host}…`);
        } catch (err) {
            this.notifications.error('Connection failed: ' + err);
        }
    }

    /**
     * Prompts the user for a host/port and opens a quick connection.
     */
    async quickConnect() {
        const input = await this._inputPrompt('Quick Connect', 'Enter host[:port]', 'e.g. 192.168.1.1:22');
        if (!input || !input.trim()) return;

        const parts = input.trim().split(':');
        const host = parts[0];
        const port = parts[1] ? parseInt(parts[1], 10) : 22;
        const protocol = this.state.preferences.defaultProtocol || 'ssh';

        try {
            const profile = { protocol, host, port, displayName: host, tags: [] };
            const sessionInfo = await window.go.api.Service.ConnectQuick(profile);
            this.state.addSession(sessionInfo);
            this.tabs.addTab(sessionInfo);

            const panelId = `panel-${sessionInfo.id}`;
            if (protocol === 'raw') {
                this.rawViewer.createViewer(sessionInfo.id, panelId, { protocol, raw: {} });
            } else {
                this.terminals.createTerminal(sessionInfo.id, panelId, { protocol });
            }
        } catch (err) {
            this.notifications.error('Quick connect failed: ' + err);
        }
    }

    // ── Import / Export ──────────────────────────────────────────────

    /**
     * Triggers a profile import dialog.
     */
    async importProfiles() {
        try {
            const path = await window.go.api.Service.SelectFile('Import Profiles', '*.json');
            if (!path) return;
            this.notifications.info('Import from file is not yet supported. Selected: ' + path);
        } catch (err) {
            if (String(err) !== 'cancelled') {
                this.notifications.error('Import failed: ' + err);
            }
        }
    }

    /**
     * Triggers a profile export flow.
     */
    async exportProfiles() {
        try {
            const json = await window.go.api.Service.ExportProfiles(false);
            const path = await window.go.api.Service.SaveFile('Export Profiles', '*.json');
            if (!path) return;
            this.notifications.success('Profiles exported');
        } catch (err) {
            if (String(err) !== 'cancelled') {
                this.notifications.error('Export failed: ' + err);
            }
        }
    }

    // ── Status bar ───────────────────────────────────────────────────

    /**
     * Updates the status bar with current session info.
     */
    updateStatusBar() {
        const connEl = document.getElementById('status-connection');
        const countEl = document.getElementById('status-session-count');
        const vaultEl = document.getElementById('status-vault');
        if (!connEl) return;

        const sessions = this.state.getAllSessions();
        const connected = sessions.filter((s) => s.state === 'connected').length;
        const total = sessions.length;

        const activeSession = this.state.activeSessionId
            ? this.state.getSession(this.state.activeSessionId)
            : null;

        if (activeSession) {
            connEl.innerHTML = `<span class="status-dot is-${activeSession.state}"></span> ${stateLabel(activeSession.state)}`;
        } else {
            connEl.innerHTML = '<span class="status-dot is-disconnected"></span> No active session';
        }

        if (countEl) countEl.textContent = `${connected}/${total} sessions`;
        if (vaultEl) {
            vaultEl.textContent = this.state.secretStoreUnlocked ? '🔓 Unlocked' : '🔒 Locked';
        }
    }

    // ── Internal helpers ─────────────────────────────────────────────

    /**
     * Applies loaded preferences to the UI.
     * @private
     */
    _applyPreferences() {
        const prefs = this.state.preferences;

        // Sidebar width
        const sidebar = document.getElementById('sidebar');
        if (sidebar && prefs.sidebarWidth) {
            sidebar.style.width = `${prefs.sidebarWidth}px`;
        }

        // Status bar visibility
        const statusBar = document.getElementById('status-bar');
        if (statusBar) {
            statusBar.style.display = prefs.showStatusBar === false ? 'none' : 'flex';
        }
    }

    /**
     * Opens an inline search bar for the active terminal.
     * @private
     */
    _openTerminalSearch() {
        const sessionId = this.state.activeSessionId;
        if (!sessionId) return;

        // Check if search bar already exists
        const panel = document.getElementById(`panel-${sessionId}`);
        if (!panel || panel.querySelector('.terminal-search-bar')) return;

        const bar = document.createElement('div');
        bar.className = 'terminal-search-bar';
        bar.innerHTML = `
            <input type="text" class="form-input terminal-search-input" placeholder="Find…" />
            <button class="btn btn-xs btn-outline terminal-search-prev">▲</button>
            <button class="btn btn-xs btn-outline terminal-search-next">▼</button>
            <button class="btn btn-xs btn-outline terminal-search-close">✕</button>
        `;

        const input = bar.querySelector('.terminal-search-input');
        const closeBtn = bar.querySelector('.terminal-search-close');
        const prevBtn = bar.querySelector('.terminal-search-prev');
        const nextBtn = bar.querySelector('.terminal-search-next');

        const doSearch = () => this.terminals.search(sessionId, input.value);
        const doPrev = () => this.terminals.searchPrevious(sessionId, input.value);

        input.addEventListener('input', doSearch);
        input.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') {
                e.shiftKey ? doPrev() : doSearch();
            }
            if (e.key === 'Escape') {
                bar.remove();
                this.terminals.focusTerminal(sessionId);
            }
        });

        nextBtn.addEventListener('click', doSearch);
        prevBtn.addEventListener('click', doPrev);
        closeBtn.addEventListener('click', () => {
            bar.remove();
            this.terminals.focusTerminal(sessionId);
        });

        panel.prepend(bar);
        input.focus();
    }

    /**
     * Safely binds a click handler to an element by ID (no-op if the element does not exist).
     * @param {string} id - Element ID.
     * @param {Function} handler
     * @private
     */
    _bindClick(id, handler) {
        const el = document.getElementById(id);
        if (el) {
            el.addEventListener('click', (e) => {
                try {
                    handler(e);
                } catch (err) {
                    console.error('Click handler error for', id, err);
                }
            });
        }
    }

    /**
     * Shows a simple text-input prompt modal (replaces window.prompt which
     * is unavailable in WKWebView).
     * @param {string} title - Modal heading.
     * @param {string} label - Label above the input.
     * @param {string} [placeholder] - Placeholder text.
     * @returns {Promise<string|null>} Resolves with the value or null if cancelled.
     * @private
     */
    _inputPrompt(title, label, placeholder) {
        const createElement = window.NexusTerminal.createElement;
        return new Promise((resolve) => {
            const backdrop = createElement('div', 'modal-backdrop');
            const modal = createElement('div', 'modal modal-sm');
            modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">${title}</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label class="form-label">${label}</label>
                        <input class="form-input" type="text" placeholder="${placeholder || ''}" autofocus />
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm prompt-cancel">Cancel</button>
                    <button class="btn btn-sm btn-primary prompt-ok">Connect</button>
                </div>
            `;
            backdrop.appendChild(modal);
            document.body.appendChild(backdrop);
            requestAnimationFrame(() => backdrop.classList.add('is-visible'));

            const input = modal.querySelector('input');
            const close = (value) => {
                backdrop.classList.remove('is-visible');
                setTimeout(() => backdrop.remove(), 200);
                resolve(value);
            };

            modal.querySelector('.modal-close').addEventListener('click', () => close(null));
            modal.querySelector('.prompt-cancel').addEventListener('click', () => close(null));
            modal.querySelector('.prompt-ok').addEventListener('click', () => close(input.value));
            backdrop.addEventListener('click', (e) => { if (e.target === backdrop) close(null); });
            input.addEventListener('keydown', (e) => {
                if (e.key === 'Enter') close(input.value);
                if (e.key === 'Escape') close(null);
            });

            setTimeout(() => input.focus(), 50);
        });
    }
}

// ── Bootstrap ────────────────────────────────────────────────────────


document.addEventListener('DOMContentLoaded', () => {
    try {
        const app = new App();
        app.init().catch((err) => {
            console.error('App initialisation failed:', err);
        });
    } catch (err) {
        console.error('App constructor failed:', err);
    }
});

})();
