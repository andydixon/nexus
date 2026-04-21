(function () {
  /**
   * Nexus Terminal — xterm.js terminal manager.
   * Creates, manages, and destroys xterm.js instances for SSH and Telnet sessions.
   * Handles data routing, resizing, and search.
   */

  /* global Terminal, FitAddon, SearchAddon, WebLinksAddon, Unicode11Addon */

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
      const themeName =
        (this.app.state.preferences || {}).terminalTheme || "charcoal";
      const term = new Terminal({
        theme: window.NexusTerminal.getTerminalTheme(themeName),
        fontFamily:
          profile.terminalFontFamily ||
          'JetBrains Mono, Menlo, Monaco, Consolas, "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", monospace',
        fontSize: profile.terminalFontSize || 14,
        scrollback: profile.terminalScrollback || 10000,
        cursorStyle: profile.terminalCursorStyle || "block",
        cursorBlink: true,
        allowProposedApi: true,
        convertEol: false,
        windowsMode: false,
      });

      // Load addons
      const fitAddon = new FitAddon.FitAddon();
      const searchAddon = new SearchAddon.SearchAddon();
      let webLinksAddon = null;
      let unicode11Addon = null;

      term.loadAddon(fitAddon);
      term.loadAddon(searchAddon);

      try {
        unicode11Addon = new Unicode11Addon.Unicode11Addon();
        term.loadAddon(unicode11Addon);
        term.unicode.activeVersion = "11";
      } catch (_) {
        // Unicode 11 addon is optional; fall back to default unicode handling.
      }

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

      let suppressPasteEvent = false;
      const attachPasteGuard = () => {
        const inputEl = term.textarea || container.querySelector("textarea");
        if (!inputEl) return;
        inputEl.addEventListener("paste", (e) => {
          if (!suppressPasteEvent) return;
          e.preventDefault();
          e.stopPropagation();
          suppressPasteEvent = false;
        });
      };
      attachPasteGuard();

      // Right-click context menu on the terminal viewport
      const termEl = container.querySelector(".xterm");
      if (termEl) {
        termEl.addEventListener("contextmenu", (e) => {
          e.preventDefault();
          this._showTerminalContextMenu(sessionId, term, e.clientX, e.clientY);
        });
      }

      // Force a synchronous layout reflow so the container reports its
      // true dimensions, then fit the terminal to fill the space.
      // eslint-disable-next-line no-unused-expressions
      container.offsetHeight;
      const doFit = () => {
        try {
          fitAddon.fit();
        } catch (_) {
          /* noop */
        }
      };
      doFit();
      // Belt-and-braces: retry once after a paint in case the initial
      // reflow dimensions were still stale.
      requestAnimationFrame(doFit);

      // Forward user keystrokes to the backend
      term.onData((data) => {
        try {
          const b64 = btoa(data);
          window.go.api.Service.WriteSession(sessionId, b64);

          // Broadcast to all other connected sessions if enabled
          if (this.app.state.broadcastMode) {
            for (const [otherId] of this.terminals) {
              if (otherId === sessionId) continue;
              const otherSession = this.app.state.getSession(otherId);
              if (otherSession && otherSession.state === "connected") {
                try {
                  window.go.api.Service.WriteSession(otherId, b64);
                } catch (_) {
                  /* non-critical */
                }
              }
            }
          }
        } catch (err) {
          console.error("WriteSession error:", err);
        }
      });

      // Clipboard: copy selection on Cmd/Ctrl+C, paste on Cmd/Ctrl+V
      term.attachCustomKeyEventHandler((e) => {
        const mod = e.ctrlKey || e.metaKey;

        // Cmd/Ctrl+C — copy selected text (let xterm handle it normally if no selection)
        if (mod && e.key === "c" && e.type === "keydown") {
          const selection = term.getSelection();
          if (selection) {
            if (window.runtime && window.runtime.ClipboardSetText) {
              window.runtime.ClipboardSetText(selection);
            } else {
              navigator.clipboard.writeText(selection).catch(function () {});
            }
            return false; // Prevent xterm from sending Ctrl+C
          }
          // No selection — let Ctrl+C pass through as SIGINT
          return true;
        }

        // Cmd/Ctrl+V — paste from clipboard
        if (mod && e.key === "v" && e.type === "keydown") {
          suppressPasteEvent = true;
          setTimeout(() => {
            suppressPasteEvent = false;
          }, 250);
          if (window.runtime && window.runtime.ClipboardGetText) {
            window.runtime
              .ClipboardGetText()
              .then(function (text) {
                if (text) term.paste(text);
              })
              .catch(function () {});
          } else {
            navigator.clipboard
              .readText()
              .then(function (text) {
                if (text) term.paste(text);
              })
              .catch(function () {});
          }
          return false;
        }

        return true;
      });

      // Notify backend when terminal dimensions change
      term.onResize(({ cols, rows }) => {
        try {
          window.go.api.Service.ResizeSession(sessionId, cols, rows);
        } catch (err) {
          console.error("ResizeSession error:", err);
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
        decoder:
          typeof TextDecoder !== "undefined"
            ? new TextDecoder("utf-8")
            : null,
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
        if (entry.decoder) {
          const bytes = new Uint8Array(raw.length);
          for (let i = 0; i < raw.length; i++) {
            bytes[i] = raw.charCodeAt(i);
          }
          const text = entry.decoder.decode(bytes, { stream: true });
          entry.term.write(text);
        } else {
          entry.term.write(raw);
        }
      } catch (err) {
        console.error("Failed to decode session data:", err);
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
          panel.style.display = id === sessionId ? "flex" : "none";
        }
      }

      // Re-fit the newly visible terminal
      const entry = this.terminals.get(sessionId);
      if (entry) {
        requestAnimationFrame(() => {
          try {
            entry.fitAddon.fit();
          } catch (_) {
            /* noop */
          }
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
          try {
            entry.fitAddon.fit();
          } catch (_) {
            /* noop */
          }
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
          matchBackground: "#ffa72633",
          activeMatchBackground: "#4ecdc466",
          matchBorder: "#ffa726",
          activeMatchBorder: "#4ecdc4",
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

    /**
     * Applies a named colour theme to all active terminal instances.
     * @param {string} themeName
     */
    applyTheme(themeName) {
      const themeObj = window.NexusTerminal.getTerminalTheme(themeName);
      for (const [, entry] of this.terminals) {
        entry.term.options.theme = themeObj;
      }
    }

    // ── Private helpers ──────────────────────────────────────────────

    /**
     * Shows a right-click context menu for a terminal.
     * @param {string} sessionId
     * @param {Terminal} term
     * @param {number} x
     * @param {number} y
     * @private
     */
    _showTerminalContextMenu(sessionId, term, x, y) {
      const { createElement, dismissContextMenus } = window.NexusTerminal;
      dismissContextMenus();

      const hasSelection = term.hasSelection();
      const menu = createElement("div", "context-menu");
      menu.style.left = `${x}px`;
      menu.style.top = `${y}px`;

      const items = [
        {
          label: "Copy",
          disabled: !hasSelection,
          action: () => {
            const text = term.getSelection();
            if (text) {
              if (window.runtime && window.runtime.ClipboardSetText) {
                window.runtime.ClipboardSetText(text);
              } else {
                navigator.clipboard.writeText(text).catch(function () {});
              }
              this.app.notifications.info("Copied to clipboard");
            }
          },
        },
        {
          label: "Paste",
          action: () => {
            if (window.runtime && window.runtime.ClipboardGetText) {
              window.runtime
                .ClipboardGetText()
                .then((text) => {
                  if (text) term.paste(text);
                })
                .catch(function () {});
            } else {
              navigator.clipboard
                .readText()
                .then((text) => {
                  if (text) term.paste(text);
                })
                .catch(function () {});
            }
          },
        },
        { divider: true },
        {
          label: "Select All",
          action: () => term.selectAll(),
        },
        {
          label: "Find…",
          action: () => this.app._openTerminalSearch(),
        },
        {
          label: "Send Snippet…",
          action: () => this.app.snippets.open(),
        },
        { divider: true },
        {
          label: "Clear Terminal",
          action: () => term.clear(),
        },
        {
          label: "Reset Terminal",
          action: () => term.reset(),
        },
      ];

      for (const entry of items) {
        if (entry.divider) {
          menu.appendChild(createElement("div", "context-menu-divider"));
          continue;
        }
        const btn = createElement("button", "context-menu-item", {
          textContent: entry.label,
        });
        if (entry.disabled) {
          btn.disabled = true;
          btn.classList.add("context-menu-disabled");
        }
        btn.addEventListener("click", () => {
          dismissContextMenus();
          entry.action();
        });
        // Prevent mousedown from causing text deselection in WKWebView
        btn.addEventListener("mousedown", (e) => e.preventDefault());
        menu.appendChild(btn);
      }

      document.body.appendChild(menu);

      // Keep within viewport
      const rect = menu.getBoundingClientRect();
      if (rect.right > window.innerWidth)
        menu.style.left = `${window.innerWidth - rect.width - 8}px`;
      if (rect.bottom > window.innerHeight)
        menu.style.top = `${window.innerHeight - rect.height - 8}px`;
    }

    /**
     * Builds the xterm.js colour theme.
     * @returns {Object}
     * @private
     */
    _buildTheme() {
      const themeName =
        (this.app.state.preferences || {}).terminalTheme || "charcoal";
      return window.NexusTerminal.getTerminalTheme(themeName);
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.TerminalManager = TerminalManager;
})();
