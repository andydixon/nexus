/**
 * Nexus Terminal — main application entry point.
 * Initialises the application shell, loads profiles, and sets up event listeners.
 */

import { State } from './state.js';
import { Sidebar } from './sidebar.js';
import { TabManager } from './tabs.js';
import { TerminalManager } from './terminal.js';
import { ProfileEditor } from './profiles.js';
import { RawViewer } from './raw.js';
import { Notifications } from './notifications.js';
import { Settings } from './settings.js';
import { CommandPalette } from './commands.js';
import { SecretPrompt } from './secrets.js';
import { stateLabel } from './utils.js';

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
     */
    async init() {
        await this.loadProfiles();
        await this.loadPreferences();
        await this.checkSecretStore();

        this.setupEvents();
        this.setupKeyboard();
        this.setupUI();
        this.updateStatusBar();
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
     */
    setupEvents() {
        // Incoming session data (terminal output)
        window.runtime.EventsOn('session:data', (payload) => {
            this.terminals.handleData(payload.sessionID, payload.data);
        });

        // Session state transitions (connecting → connected → disconnected)
        window.runtime.EventsOn('session:state', (payload) => {
            this.tabs.updateSessionState(payload.sessionID, payload.state, payload.error);
            this.updateStatusBar();
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
                    sessionID: payload.sessionID,
                    value,
                });
            } catch (_) {
                window.runtime.EventsEmit('session:secret:response', {
                    sessionID: payload.sessionID,
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

        // Window resize → re-fit active terminal
        window.addEventListener('resize', () => this.terminals.fitActive());
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
        const input = prompt('Quick connect (host[:port]):');
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
        if (el) el.addEventListener('click', handler);
    }
}

// ── Bootstrap ────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => {
    const app = new App();
    app.init().catch((err) => console.error('App initialisation failed:', err));
});
