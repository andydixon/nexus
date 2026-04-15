(function() {
/**
 * Nexus Terminal — xterm.js terminal manager.
 * Creates, manages, and destroys xterm.js instances for SSH and Telnet sessions.
 * Handles data routing, resizing, and search.
 */

/* global Terminal, FitAddon, SearchAddon, WebLinksAddon */

class TerminalManager {
    /**
     * @param {App} app
     */
    constructor(app) {
        this.app = app;

        /**
         * Map of sessionId → { term, fitAddon, searchAddon, observer }.
         * @type {Map<string, Object>}
         */
        this.terminals = new Map();
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Creates and opens an xterm.js terminal inside the given container.
     * Wires up data input → backend and container resizing.
     *
     * @param {string} sessionId
     * @param {string} containerId - DOM id of the panel element.
     * @param {Object} profile - The connection profile (used for font/theme prefs).
     * @returns {Terminal} The xterm.js Terminal instance.
     */
    createTerminal(sessionId, containerId, profile) {
        const term = new Terminal({
            theme: this._buildTheme(),
            fontFamily: profile.terminalFontFamily || 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
            fontSize: profile.terminalFontSize || 14,
            scrollback: profile.terminalScrollback || 10000,
            cursorStyle: profile.terminalCursorStyle || 'block',
            cursorBlink: true,
            allowProposedApi: true,
            convertEol: false,
            windowsMode: false,
        });

        // Load addons
        const fitAddon = new FitAddon.FitAddon();
        const searchAddon = new SearchAddon.SearchAddon();
        let webLinksAddon = null;

        term.loadAddon(fitAddon);
        term.loadAddon(searchAddon);

        // WebLinksAddon may not always be available
        try {
            webLinksAddon = new WebLinksAddon.WebLinksAddon((_event, uri) => {
                window.runtime.BrowserOpenURL(uri);
            });
            term.loadAddon(webLinksAddon);
        } catch (_) {
            // Not critical — links just won't be clickable
        }

        // Open the terminal in the DOM container
        const container = document.getElementById(containerId);
        term.open(container);

        // Force a synchronous layout reflow so the container reports its
        // true dimensions, then fit the terminal to fill the space.
        // eslint-disable-next-line no-unused-expressions
        container.offsetHeight;
        const doFit = () => { try { fitAddon.fit(); } catch (_) { /* noop */ } };
        doFit();
        // Belt-and-braces: retry once after a paint in case the initial
        // reflow dimensions were still stale.
        requestAnimationFrame(doFit);

        // Forward user keystrokes to the backend
        term.onData((data) => {
            try {
                const b64 = btoa(data);
                window.go.api.Service.WriteSession(sessionId, b64);
            } catch (err) {
                console.error('WriteSession error:', err);
            }
        });

        // Notify backend when terminal dimensions change
        term.onResize(({ cols, rows }) => {
            try {
                window.go.api.Service.ResizeSession(sessionId, cols, rows);
            } catch (err) {
                console.error('ResizeSession error:', err);
            }
        });

        // Re-fit when the container itself resizes
        const observer = new ResizeObserver(() => {
            try {
                fitAddon.fit();
            } catch (_) {
                // Ignore transient errors during layout shifts
            }
        });
        observer.observe(container);

        this.terminals.set(sessionId, {
            term,
            fitAddon,
            searchAddon,
            webLinksAddon,
            observer,
        });

        return term;
    }

    /**
     * Writes incoming backend data (base64-encoded) to the appropriate terminal.
     * @param {string} sessionId
     * @param {string} base64Data
     */
    handleData(sessionId, base64Data) {
        const entry = this.terminals.get(sessionId);
        if (!entry) return;

        try {
            const raw = atob(base64Data);
            entry.term.write(raw);
        } catch (err) {
            console.error('Failed to decode session data:', err);
        }
    }

    /**
     * Tears down a terminal instance and frees resources.
     * @param {string} sessionId
     */
    destroyTerminal(sessionId) {
        const entry = this.terminals.get(sessionId);
        if (!entry) return;

        entry.observer.disconnect();
        entry.term.dispose();
        this.terminals.delete(sessionId);
    }

    /**
     * Shows the terminal for a given session, hiding all others.
     * @param {string} sessionId
     */
    showTerminal(sessionId) {
        for (const [id, entry] of this.terminals) {
            const panel = document.getElementById(`panel-${id}`);
            if (panel) {
                panel.style.display = id === sessionId ? 'flex' : 'none';
            }
        }

        // Re-fit the newly visible terminal
        const entry = this.terminals.get(sessionId);
        if (entry) {
            requestAnimationFrame(() => {
                try { entry.fitAddon.fit(); } catch (_) { /* noop */ }
            });
        }
    }

    /**
     * Re-fits the currently active terminal to its container.
     */
    fitActive() {
        const id = this.app.state.activeSessionId;
        if (id) this.fitSession(id);
    }

    /**
     * Re-fits a specific session's terminal to its container.
     * @param {string} sessionId
     */
    fitSession(sessionId) {
        const entry = this.terminals.get(sessionId);
        if (entry) {
            requestAnimationFrame(() => {
                try { entry.fitAddon.fit(); } catch (_) { /* noop */ }
            });
        }
    }

    /**
     * Searches within a terminal using the search addon.
     * @param {string} sessionId
     * @param {string} query
     * @param {Object} [options]
     * @param {boolean} [options.regex=false]
     * @param {boolean} [options.caseSensitive=false]
     * @param {boolean} [options.wholeWord=false]
     * @returns {boolean} Whether a match was found.
     */
    search(sessionId, query, options = {}) {
        const entry = this.terminals.get(sessionId);
        if (!entry) return false;

        if (!query) {
            entry.searchAddon.clearDecorations();
            return false;
        }

        return entry.searchAddon.findNext(query, {
            regex: options.regex || false,
            caseSensitive: options.caseSensitive || false,
            wholeWord: options.wholeWord || false,
            decorations: {
                matchBackground: '#ffa72633',
                activeMatchBackground: '#4ecdc466',
                matchBorder: '#ffa726',
                activeMatchBorder: '#4ecdc4',
            },
        });
    }

    /**
     * Finds the previous search result.
     * @param {string} sessionId
     * @param {string} query
     * @param {Object} [options]
     * @returns {boolean}
     */
    searchPrevious(sessionId, query, options = {}) {
        const entry = this.terminals.get(sessionId);
        if (!entry || !query) return false;
        return entry.searchAddon.findPrevious(query, {
            regex: options.regex || false,
            caseSensitive: options.caseSensitive || false,
            wholeWord: options.wholeWord || false,
        });
    }

    /**
     * Clears the terminal buffer and viewport.
     * @param {string} sessionId
     */
    clearTerminal(sessionId) {
        const entry = this.terminals.get(sessionId);
        if (entry) entry.term.clear();
    }

    /**
     * Fully resets the terminal (clears buffer, restores default state).
     * @param {string} sessionId
     */
    resetTerminal(sessionId) {
        const entry = this.terminals.get(sessionId);
        if (entry) entry.term.reset();
    }

    /**
     * Focuses the terminal so it can receive keyboard input.
     * @param {string} sessionId
     */
    focusTerminal(sessionId) {
        const entry = this.terminals.get(sessionId);
        if (entry) entry.term.focus();
    }

    // ── Private helpers ──────────────────────────────────────────────

    /**
     * Builds the xterm.js colour theme.
     * @returns {Object}
     * @private
     */
    _buildTheme() {
        return {
            background: '#1c1c1c',
            foreground: '#e4e4e4',
            cursor: '#4ecdc4',
            cursorAccent: '#1c1c1c',
            selectionBackground: 'rgba(78, 205, 196, 0.3)',
            selectionForeground: '#ffffff',
            black: '#1c1c1c',
            red: '#ef5350',
            green: '#66bb6a',
            yellow: '#ffa726',
            blue: '#42a5f5',
            magenta: '#ab47bc',
            cyan: '#4ecdc4',
            white: '#e4e4e4',
            brightBlack: '#555555',
            brightRed: '#ff7043',
            brightGreen: '#81c784',
            brightYellow: '#ffcc02',
            brightBlue: '#64b5f6',
            brightMagenta: '#ce93d8',
            brightCyan: '#80deea',
            brightWhite: '#ffffff',
        };
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.TerminalManager = TerminalManager;

})();
