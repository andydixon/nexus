(function () {
  /**
   * Nexus Terminal — xterm.js terminal manager.
   * Creates, manages, and destroys xterm.js instances for SSH and Telnet sessions.
   * Handles data routing, resizing, and search.
   */

  /* global Terminal, FitAddon, SearchAddon, WebLinksAddon, Unicode11Addon */

  const UPLOAD_CHUNK_BYTES = 768 * 1024;

  /**
   * Encode a string as UTF-8 bytes then base64-encode them.
   * Unlike bare btoa(), this handles characters above U+007F correctly
   * (e.g. degree symbol °, emoji) and won't throw InvalidCharacterError.
   */
  function utf8ToBase64(str) {
    const bytes = new TextEncoder().encode(str);
    let binary = "";
    for (let i = 0; i < bytes.length; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return btoa(binary);
  }

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
        scrollOnUserInput: true,
        altClickMovesCursor: true,
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
        inputEl.addEventListener("keydown", (e) => {
          const mod = e.ctrlKey || e.metaKey;
          if (!mod || e.key !== "v") return;
          // Block the native paste when we handle Cmd/Ctrl+V ourselves.
          if (!suppressPasteEvent) return;
          e.preventDefault();
          e.stopPropagation();
        });
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

      // Alternate-screen scroll passthrough for screen/tmux/byobu.
      // When the terminal is in the alternate buffer (used by multiplexers),
      // convert mouse-wheel events into arrow-key sequences so the remote
      // application handles scrollback instead of xterm.js (which has no
      // scrollback content in alternate mode).
      const viewportEl = container.querySelector(".xterm-viewport");
      if (viewportEl) {
        viewportEl.addEventListener(
          "wheel",
          (e) => {
            if (term.buffer.active !== term.buffer.alternate) return;
            // Only intercept when mouse tracking is NOT active (if mouse mode
            // is on, xterm.js already forwards events correctly).
            if (
              term.modes &&
              term.modes.mouseTrackingMode &&
              term.modes.mouseTrackingMode !== "none"
            )
              return;

            e.preventDefault();
            e.stopPropagation();
            const lines = Math.max(1, Math.round(Math.abs(e.deltaY) / 40));
            const seq = e.deltaY < 0 ? "\x1b[A" : "\x1b[B";
            const data = seq.repeat(lines);
            const b64 = utf8ToBase64(data);
            try {
              window.NexusTerminal.wailsService.WriteSession(sessionId, b64);
            } catch (_) {
              /* non-critical */
            }
          },
          { passive: false },
        );
      }

      // Drag-and-drop file handling
      container.addEventListener("dragover", (e) => {
        if (
          e.dataTransfer &&
          Array.from(e.dataTransfer.types || []).includes("Files")
        ) {
          e.preventDefault();
          e.dataTransfer.dropEffect = "copy";
        }
      });
      container.addEventListener("drop", (e) => {
        const files = e.dataTransfer ? e.dataTransfer.files : null;
        if (!files || files.length === 0) return;
        e.preventDefault();
        e.stopPropagation();
        this._handleFileDrop(sessionId, term, files);
      });

      let fitRaf = 0;
      let forceNextFit = false;
      let lastFitWidth = 0;
      let lastFitHeight = 0;

      const cancelScheduledFit = () => {
        if (!fitRaf) return;
        cancelAnimationFrame(fitRaf);
        fitRaf = 0;
      };

      const scheduleFit = (force = false) => {
        forceNextFit = forceNextFit || force;
        if (fitRaf) return;

        fitRaf = requestAnimationFrame(() => {
          fitRaf = 0;

          if (!container.isConnected) {
            forceNextFit = false;
            return;
          }

          const rect = container.getBoundingClientRect();
          const width = Math.round(rect.width);
          const height = Math.round(rect.height);
          if (width <= 0 || height <= 0) {
            return;
          }

          const dimensionsChanged =
            width !== lastFitWidth || height !== lastFitHeight;
          if (!forceNextFit && !dimensionsChanged) {
            return;
          }

          lastFitWidth = width;
          lastFitHeight = height;
          forceNextFit = false;

          try {
            fitAddon.fit();
            // Forced fits do not emit onResize when xterm's geometry is
            // unchanged. Always publish the fitted size so a new local PTY
            // cannot remain at its 80x24 fallback dimensions.
            this._sendTerminalSize(sessionId, term);
          } catch (_) {
            /* noop */
          }
        });
      };

      // Force a synchronous layout reflow so the container reports its
      // true dimensions, then fit the terminal to fill the space.
      // eslint-disable-next-line no-unused-expressions
      container.offsetHeight;
      scheduleFit(true);
      // Belt-and-braces: retry once after a paint in case the initial
      // reflow dimensions were still stale.
      requestAnimationFrame(() => scheduleFit(true));

      const refreshTerminal = () => {
        try {
          term.refresh(0, term.rows - 1);
        } catch (_) {
          /* noop */
        }
      };

      // Forward user keystrokes to the backend
      term.onData((data) => {
        const entry = this.terminals.get(sessionId);
        if (entry?.localExited) {
          if (data === "\r" || data === "\n") {
            this._restartLocalTerminal(sessionId, entry);
          }
          return;
        }

        try {
          const b64 = utf8ToBase64(data);
          window.NexusTerminal.wailsService.WriteSession(sessionId, b64);

          // Broadcast to other connected sessions if enabled
          if (this.app.state.broadcastMode) {
            const targets = this.app.state.broadcastTargets;
            for (const [otherId] of this.terminals) {
              if (otherId === sessionId) continue;
              // If targets are set, only broadcast to selected sessions
              if (targets && !targets.has(otherId)) continue;
              const otherSession = this.app.state.getSession(otherId);
              if (otherSession && otherSession.state === "connected") {
                try {
                  window.NexusTerminal.wailsService.WriteSession(otherId, b64);
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
          const prefs = this.app.state.preferences || {};
          if (prefs.useNativePaste === true) {
            suppressPasteEvent = false;
            return true;
          }
          suppressPasteEvent = true;
          e.preventDefault();
          e.stopPropagation();
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
        this._sendTerminalSize(sessionId, { cols, rows });
      });

      // Re-fit when the container itself resizes. ResizeObserver can fire
      // repeatedly while xterm mutates its DOM, so schedule at most one fit
      // per frame and skip work when the container dimensions are unchanged.
      const observer = new ResizeObserver(() => {
        scheduleFit();
      });
      observer.observe(container);

      this.terminals.set(sessionId, {
        term,
        fitAddon,
        searchAddon,
        webLinksAddon,
        observer,
        scheduleFit,
        cancelScheduledFit,
        refreshTerminal,
        localExited: false,
        localRestarting: false,
        decoder:
          typeof TextDecoder !== "undefined" ? new TextDecoder("utf-8") : null,
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
      entry.cancelScheduledFit();
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
        entry.scheduleFit(true);
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
        entry.scheduleFit(true);
      }
    }

    /**
     * Shows or hides the disconnected overlay for a session panel.
     * @param {string} sessionId
     * @param {boolean} isDisconnected
     * @param {string} [error]
     */
    setDisconnectedOverlay(sessionId, isDisconnected, error) {
      const panel = document.getElementById(`panel-${sessionId}`);
      if (!panel) return;

      const overlay = this._ensureDisconnectOverlay(panel, sessionId);
      if (!overlay) return;

      const message = overlay.querySelector(".terminal-disconnect-message");
      if (message) {
        message.textContent = error ? error : "Connection lost.";
      }

      panel.classList.toggle("is-disconnected", isDisconnected);
      overlay.classList.toggle("is-visible", isDisconnected);
    }

    /**
     * Marks a local shell as exited without obscuring its terminal buffer.
     * Enter is intercepted by the terminal input handler and restarts it.
     * @param {string} sessionId
     * @param {boolean} isExited
     */
    setLocalExited(sessionId, isExited) {
      const entry = this.terminals.get(sessionId);
      if (!entry) return;

      if (isExited && !entry.localExited) {
        entry.term.write("\r\n[Exited - press enter to restart]\r\n");
      }
      entry.localExited = isExited;
      entry.localRestarting = false;
      if (isExited) entry.term.focus();
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

    _sendTerminalSize(sessionId, dimensions) {
      const cols = Number(dimensions?.cols || 0);
      const rows = Number(dimensions?.rows || 0);
      if (cols <= 0 || rows <= 0) return;

      Promise.resolve(
        window.NexusTerminal.wailsService.ResizeSession(sessionId, cols, rows),
      ).catch((err) => console.error("ResizeSession error:", err));
    }

    async _restartLocalTerminal(sessionId, entry) {
      if (entry.localRestarting) return;
      entry.localRestarting = true;

      try {
        await window.NexusTerminal.wailsService.RestartLocalSession(
          sessionId,
          entry.term.cols,
          entry.term.rows,
        );
      } catch (err) {
        entry.localRestarting = false;
        entry.term.write(
          `\r\n[Restart failed: ${String(err)} - press enter to retry]\r\n`,
        );
      }
    }

    /**
     * Ensures the disconnect overlay exists for a session panel.
     * @param {HTMLElement} panel
     * @param {string} sessionId
     * @returns {HTMLElement|null}
     * @private
     */
    _ensureDisconnectOverlay(panel, sessionId) {
      let overlay = panel.querySelector(".terminal-disconnect-overlay");
      if (overlay) return overlay;

      const { createElement } = window.NexusTerminal;
      if (!createElement) return null;

      overlay = createElement("div", "terminal-disconnect-overlay");
      const content = createElement("div", "terminal-disconnect-content");
      const title = createElement("div", "terminal-disconnect-title", {
        textContent: "DISCONNECTED",
      });
      const message = createElement("div", "terminal-disconnect-message", {
        textContent: "Connection lost.",
      });
      const button = createElement("button", "btn btn-sm btn-primary", {
        textContent: "Reconnect",
      });
      button.addEventListener("click", () => {
        this.app.tabs.reconnectSession(sessionId);
      });

      content.appendChild(title);
      content.appendChild(message);
      content.appendChild(button);
      overlay.appendChild(content);
      panel.appendChild(overlay);

      return overlay;
    }

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

      const isMac = navigator.platform.indexOf("Mac") !== -1;
      const modKey = isMac ? "⌘" : "Ctrl+";
      const hasSelection = term.hasSelection();
      const menu = createElement("div", "context-menu");
      menu.style.left = `${x}px`;
      menu.style.top = `${y}px`;

      const items = [
        {
          label: "Copy",
          shortcut: modKey + "C",
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
          shortcut: modKey + "V",
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
          shortcut: modKey + "A",
          action: () => term.selectAll(),
        },
        {
          label: "Find…",
          shortcut: modKey + "F",
          action: () => this.app._openTerminalSearch(),
        },
        {
          label: "Send Snippet…",
          action: () => this.app.snippets.open(),
        },
        {
          label: "Port Forwards…",
          action: () => this._showPortForwardPanel(sessionId),
        },
        { divider: true },
        {
          label: "Split Right",
          shortcut: modKey + "⇧D",
          action: () => this.app.splitPane("vertical"),
        },
        {
          label: "Split Down",
          shortcut: modKey + "⇧E",
          action: () => this.app.splitPane("horizontal"),
        },
        { divider: true },
        {
          label: "Export Output",
          action: () => this._exportTerminalOutput(sessionId, term),
        },
        {
          label: "Clear Terminal",
          shortcut: modKey + "K",
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
        const btn = createElement("button", "context-menu-item");
        const labelSpan = createElement("span", "context-menu-label", {
          textContent: entry.label,
        });
        btn.appendChild(labelSpan);
        if (entry.shortcut) {
          const shortcutSpan = createElement("span", "context-menu-shortcut", {
            textContent: entry.shortcut,
          });
          btn.appendChild(shortcutSpan);
        }
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

    _handleFileDrop(sessionId, term, fileList) {
      const session = this.app.state.getSession(sessionId);
      if (!session) return;

      const files = Array.from(fileList || []);
      if (files.length === 0) return;

      const canUpload = session.protocol === "ssh";
      this._promptFileDropAction(files, canUpload).then((choice) => {
        if (!choice) return;
        if (choice === "paste") {
          this._pasteFileContents(files, term);
          return;
        }
        if (choice === "upload") {
          if (!canUpload) {
            this.app.notifications.error(
              "Upload is only available for SSH sessions.",
            );
            return;
          }
          this._uploadDroppedFiles(sessionId, files);
        }
      });
    }

    _promptFileDropAction(files, canUpload) {
      const { createElement, escapeHtml, formatBytes } = window.NexusTerminal;
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        const count = files.length;
        const first = files[0];
        const sizeLabel =
          first && typeof first.size === "number"
            ? formatBytes(first.size)
            : "";
        const title =
          count === 1 ? (first ? first.name : "File") : count + " files";

        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">File Drop</h3>
                    <button class="btn btn-icon btn-xs modal-close">x</button>
                </div>
                <div class="modal-body">
                    <p style="font-size:var(--text-sm);color:var(--text-secondary)">
                        <strong>${escapeHtml(title)}</strong>${count === 1 && sizeLabel ? " (" + sizeLabel + ")" : ""}
                    </p>
                    <p style="font-size:var(--text-sm);color:var(--text-tertiary)">
                      Choose what to do with the dropped ${count === 1 ? "file" : "files"}.
                    </p>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm prompt-cancel">Cancel</button>
                    <button class="btn btn-sm btn-secondary action-paste">Paste contents</button>
                    <button class="btn btn-sm btn-primary action-upload">Upload to current directory</button>
                </div>
            `;

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => {
          backdrop.classList.add("is-visible");
          modal.classList.add("is-visible");
        });

        const uploadBtn = modal.querySelector(".action-upload");
        if (!canUpload) {
          uploadBtn.disabled = true;
          uploadBtn.title = "Upload is only available for SSH sessions.";
        }

        const close = (value) => {
          backdrop.classList.remove("is-visible");
          modal.classList.remove("is-visible");
          setTimeout(() => backdrop.remove(), 200);
          resolve(value);
        };

        modal
          .querySelector(".modal-close")
          .addEventListener("click", () => close(null));
        modal
          .querySelector(".prompt-cancel")
          .addEventListener("click", () => close(null));
        modal
          .querySelector(".action-paste")
          .addEventListener("click", () => close("paste"));
        uploadBtn.addEventListener("click", () => close("upload"));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(null);
        });
      });
    }

    _pasteFileContents(files, term) {
      const readFile = (file) =>
        new Promise((resolve, reject) => {
          const reader = new FileReader();
          reader.onload = () => resolve(reader.result || "");
          reader.onerror = () => reject(new Error("read failed"));
          reader.readAsText(file);
        });

      const run = async () => {
        try {
          let combined = "";
          for (let i = 0; i < files.length; i += 1) {
            const text = await readFile(files[i]);
            if (text) {
              if (combined) combined += "\n";
              combined += text;
            }
          }
          if (combined) term.paste(combined);
        } catch (_) {
          this.app.notifications.error("Failed to read file contents.");
        }
      };

      run();
    }

    _uploadDroppedFiles(sessionId, files) {
      const run = async () => {
        const remoteDir = await this._promptUploadDirectory(sessionId);
        if (!remoteDir) return;

        if (files.length > 1) {
          this.app.notifications.info(
            "Uploading " + files.length + " files...",
          );
        }

        for (let i = 0; i < files.length; i += 1) {
          const file = files[i];
          try {
            await this._uploadDroppedFileStream(sessionId, remoteDir, file);
          } catch (err) {
            const msg = err && err.message ? err.message : String(err);
            this.app.notifications.error(
              "Upload failed for " + file.name + ": " + msg,
            );
            return;
          }
        }

        if (files.length === 1) {
          this.app.notifications.success("Uploaded " + files[0].name);
        } else {
          this.app.notifications.success("Uploaded " + files.length + " files");
        }
      };

      run().catch((err) => {
        const msg = err && err.message ? err.message : String(err);
        this.app.notifications.error("Upload failed: " + msg);
      });
    }

    async _uploadDroppedFileStream(sessionId, remoteDir, file) {
      const totalBytes = typeof file.size === "number" ? file.size : 0;
      let uploadId = "";
      try {
        uploadId = await window.NexusTerminal.wailsService.SFTPUploadStart(
          sessionId,
          remoteDir,
          file.name,
          totalBytes,
        );

        for (let offset = 0; offset < totalBytes; offset += UPLOAD_CHUNK_BYTES) {
          const end = Math.min(offset + UPLOAD_CHUNK_BYTES, totalBytes);
          const buffer = await this._readFileSlice(file, offset, end);
          const base64Data = this._arrayBufferToBase64(buffer);
          await window.NexusTerminal.wailsService.SFTPUploadChunk(uploadId, base64Data);
        }

        await window.NexusTerminal.wailsService.SFTPUploadFinish(uploadId);
      } catch (err) {
        if (uploadId) {
          try {
            await window.NexusTerminal.wailsService.SFTPUploadCancel(uploadId);
          } catch (_) {
            /* noop */
          }
        }
        throw err;
      }
    }

    _readFileSlice(file, start, end) {
      return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = () => reject(new Error("read failed"));
        reader.readAsArrayBuffer(file.slice(start, end));
      });
    }

    _readFileBuffer(file) {
      return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = () => reject(new Error("read failed"));
        reader.readAsArrayBuffer(file);
      });
    }

    async _promptUploadDirectory(sessionId) {
      return this._openUploadDirectoryPicker(sessionId);
    }

    _openUploadDirectoryPicker(sessionId) {
      const { createElement, escapeHtml } = window.NexusTerminal;
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-lg");
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">Select Upload Directory</h3>
                    <button class="btn btn-icon btn-xs modal-close">x</button>
                </div>
                <div class="modal-body sftp-picker-body">
                  <div class="sftp-toolbar">
                    <div class="sftp-breadcrumb" data-role="breadcrumb"></div>
                    <input class="sftp-picker-filter" type="text" placeholder="Filter folders" />
                    <div class="sftp-toolbar-actions">
                      <button class="sftp-toolbar-btn sftp-picker-up" title="Up">Up</button>
                      <button class="sftp-toolbar-btn sftp-picker-refresh" title="Refresh">Ref</button>
                    </div>
                  </div>
                    <div class="sftp-file-list sftp-picker-list"></div>
                </div>
                <div class="modal-footer">
                  <div class="modal-footer-left">
                    <span class="form-help sftp-picker-path"></span>
                    <span class="sftp-picker-status"></span>
                    <span class="sftp-picker-legend">
                      <span class="sftp-picker-badge is-readonly">READ ONLY</span>
                      <span class="sftp-picker-badge is-noread">NO READ</span>
                    </span>
                  </div>
                  <button class="btn btn-sm prompt-cancel">Cancel</button>
                  <button class="btn btn-sm btn-primary prompt-ok">Use this folder</button>
                </div>
            `;

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => {
          backdrop.classList.add("is-visible");
          modal.classList.add("is-visible");
        });

        const listEl = modal.querySelector(".sftp-picker-list");
        const breadcrumbEl = modal.querySelector('[data-role="breadcrumb"]');
        const pathLabel = modal.querySelector(".sftp-picker-path");
        const upBtn = modal.querySelector(".sftp-picker-up");
        const refreshBtn = modal.querySelector(".sftp-picker-refresh");
        const useBtn = modal.querySelector(".prompt-ok");
        const filterInput = modal.querySelector(".sftp-picker-filter");
        const statusLabel = modal.querySelector(".sftp-picker-status");

        const state = {
          cwd: "",
          selected: "",
          selectedWritable: true,
          cwdWritable: true,
          entries: [],
          visibleEntries: [],
          loading: false,
          filter: "",
          cursorIndex: -1,
        };

        const close = (value) => {
          backdrop.classList.remove("is-visible");
          modal.classList.remove("is-visible");
          setTimeout(() => backdrop.remove(), 200);
          resolve(value);
        };

        modal
          .querySelector(".modal-close")
          .addEventListener("click", () => close(null));
        modal
          .querySelector(".prompt-cancel")
          .addEventListener("click", () => close(null));
        useBtn.addEventListener("click", () =>
          close(state.selected || state.cwd),
        );
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(null);
        });

        const renderBreadcrumb = () => {
          breadcrumbEl.innerHTML = "";
          const segments = this._pathSegments(state.cwd);
          for (let i = 0; i < segments.length; i += 1) {
            if (i > 0) {
              breadcrumbEl.appendChild(
                createElement("span", "sftp-breadcrumb-sep", {
                  textContent: ">",
                }),
              );
            }
            const seg = segments[i];
            const btn = createElement("button", "sftp-breadcrumb-segment", {
              textContent: seg.label,
              title: seg.path,
            });
            btn.addEventListener("click", () => loadDir(seg.path));
            breadcrumbEl.appendChild(btn);
          }
        };

        const updateStatus = () => {
          if (state.selected) {
            statusLabel.textContent = state.selectedWritable
              ? ""
              : "No write permission";
          } else {
            statusLabel.textContent = state.cwdWritable
              ? ""
              : "Current folder is read only";
          }
        };

        const setSelection = (entry, index) => {
          listEl.querySelectorAll(".sftp-file-row").forEach((el) => {
            el.classList.remove("is-selected");
          });
          const row = listEl.querySelector(
            `.sftp-file-row[data-index="${index}"]`,
          );
          if (row) row.classList.add("is-selected");
          state.selected = entry.path;
          state.selectedWritable = entry.canWrite !== false;
          state.cursorIndex = index;
          pathLabel.textContent = entry.path;
          useBtn.disabled = !state.selectedWritable;
          useBtn.title = state.selectedWritable ? "" : "No write permission";
          updateStatus();
        };

        const renderList = () => {
          listEl.innerHTML = "";
          const basePath = state.selected || state.cwd || "";
          const suffix = state.selected
            ? ""
            : state.cwdWritable
              ? ""
              : " (read only)";
          pathLabel.textContent = basePath + suffix;

          if (state.cwd && state.cwd !== "/") {
            const parentRow = this._buildPickerRow({
              name: "..",
              isDir: true,
              path: this._parentPath(state.cwd),
            });
            parentRow.classList.add("is-parent");
            parentRow.addEventListener("dblclick", () =>
              loadDir(parentRow.dataset.path),
            );
            listEl.appendChild(parentRow);
          }

          const filtered = state.filter
            ? state.entries.filter((entry) =>
                entry.name.toLowerCase().includes(state.filter),
              )
            : state.entries;
          state.visibleEntries = filtered;

          if (!filtered.length) {
            const empty = createElement("div", "sftp-state");
            empty.innerHTML =
              '<div class="sftp-state-icon">D</div><div class="sftp-state-msg">No folders found</div>';
            listEl.appendChild(empty);
            state.cursorIndex = -1;
            updateStatus();
            return;
          }

          filtered.forEach((entry, index) => {
            const row = this._buildPickerRow(entry, index);
            if (entry.canRead === false) {
              row.classList.add("is-unreadable");
            }
            if (entry.canWrite === false) {
              row.classList.add("is-readonly");
            }
            row.addEventListener("click", () => {
              if (entry.canRead === false) return;
              setSelection(entry, index);
            });
            row.addEventListener("dblclick", () => {
              if (entry.canRead === false) return;
              loadDir(entry.path);
            });
            listEl.appendChild(row);
          });
          updateStatus();
        };

        const renderLoading = () => {
          listEl.innerHTML = "";
          const s = createElement("div", "sftp-state");
          s.innerHTML =
            '<div class="sftp-spinner"></div><div class="sftp-state-msg">Loading...</div>';
          listEl.appendChild(s);
        };

        const renderError = (msg) => {
          listEl.innerHTML = "";
          const s = createElement("div", "sftp-state");
          s.innerHTML =
            '<div class="sftp-state-icon">!</div>' +
            '<div class="sftp-state-msg">' +
            escapeHtml(msg) +
            "</div>" +
            '<button class="btn btn-sm btn-secondary sftp-state-retry">Retry</button>';
          const retryBtn = s.querySelector(".sftp-state-retry");
          if (retryBtn)
            retryBtn.addEventListener("click", () => loadDir(state.cwd));
          listEl.appendChild(s);
        };

        const loadDir = async (path) => {
          if (state.loading) return;
          state.loading = true;
          renderLoading();
          try {
            const targetPath =
              path || (await window.NexusTerminal.wailsService.SFTPPwd(sessionId));
            const files = await window.NexusTerminal.wailsService.SFTPList(
              sessionId,
              targetPath,
            );
            const stat = await window.NexusTerminal.wailsService.SFTPStat(
              sessionId,
              targetPath,
            );
            state.cwd = targetPath;
            state.selected = "";
            state.selectedWritable = true;
            state.cwdWritable = stat && this._modeCanWrite(stat.mode);
            state.entries = (files || [])
              .filter((e) => e.isDir)
              .sort((a, b) =>
                a.name.localeCompare(b.name, undefined, {
                  sensitivity: "base",
                }),
              );
            state.entries = state.entries.map((entry) => {
              const canRead = this._modeCanRead(entry.mode);
              const canWrite = this._modeCanWrite(entry.mode);
              return Object.assign({}, entry, { canRead, canWrite });
            });
            useBtn.disabled = !state.cwdWritable;
            useBtn.title = state.cwdWritable ? "" : "No write permission";
            renderBreadcrumb();
            renderList();
          } catch (err) {
            renderError(err.message || String(err));
          } finally {
            state.loading = false;
          }
        };

        upBtn.addEventListener("click", () => {
          if (state.cwd) loadDir(this._parentPath(state.cwd));
        });
        refreshBtn.addEventListener("click", () => loadDir(state.cwd));

        filterInput.addEventListener("input", () => {
          state.filter = filterInput.value.trim().toLowerCase();
          renderList();
        });

        const moveSelection = (delta) => {
          if (!state.visibleEntries.length) return;
          let idx = state.cursorIndex;
          const max = state.visibleEntries.length - 1;
          let attempts = 0;
          do {
            idx = idx + delta;
            if (idx < 0) idx = max;
            if (idx > max) idx = 0;
            const entry = state.visibleEntries[idx];
            if (entry && entry.canRead !== false) {
              setSelection(entry, idx);
              return;
            }
            attempts += 1;
          } while (attempts <= max);
        };

        modal.addEventListener("keydown", (e) => {
          const isInput = e.target === filterInput;
          if (isInput && e.key !== "ArrowDown" && e.key !== "ArrowUp") return;

          if (e.key === "ArrowDown") {
            e.preventDefault();
            moveSelection(1);
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            moveSelection(-1);
          } else if (e.key === "Enter") {
            if (state.selected && state.selectedWritable) {
              e.preventDefault();
              close(state.selected);
            } else if (!state.selected && state.cwd && state.cwdWritable) {
              e.preventDefault();
              close(state.cwd);
            }
          } else if (e.key === "Escape") {
            e.preventDefault();
            close(null);
          }
        });

        const initial =
          this.app.sftp && typeof this.app.sftp.getCwd === "function"
            ? this.app.sftp.getCwd(sessionId)
            : "";
        loadDir(initial || "");
      });
    }

    _buildPickerRow(entry, index) {
      const { createElement } = window.NexusTerminal;
      const dataset = { path: entry.path || "" };
      if (typeof index === "number") {
        dataset.index = String(index);
      }
      const row = createElement("div", "sftp-file-row", { dataset });
      const icon = createElement("span", "sftp-file-icon is-dir", {
        textContent: "DIR",
      });
      const name = createElement("span", "sftp-file-name", {
        textContent: entry.name || entry.path || "",
        title: entry.name || entry.path || "",
      });
      let metaLabel = "";
      if (entry.canRead === false) {
        metaLabel = "NO READ";
      } else if (entry.canWrite === false) {
        metaLabel = "READ ONLY";
      }
      const meta = createElement("span", "sftp-file-meta", {
        textContent: metaLabel,
      });
      row.appendChild(icon);
      row.appendChild(name);
      row.appendChild(meta);
      return row;
    }

    _modeCanRead(mode) {
      if (!mode || typeof mode !== "string") return true;
      return mode.indexOf("r") !== -1;
    }

    _modeCanWrite(mode) {
      if (!mode || typeof mode !== "string") return true;
      return mode.indexOf("w") !== -1;
    }

    _pathSegments(p) {
      const parts = (p || "").split("/").filter(Boolean);
      const segs = [{ label: "/", path: "/" }];
      let acc = "";
      for (let i = 0; i < parts.length; i += 1) {
        acc += "/" + parts[i];
        segs.push({ label: parts[i], path: acc });
      }
      return segs;
    }

    _parentPath(p) {
      if (!p || p === "/") return "/";
      const trimmed = p.replace(/\/+$/, "");
      const idx = trimmed.lastIndexOf("/");
      return idx <= 0 ? "/" : trimmed.substring(0, idx);
    }

    _arrayBufferToBase64(buffer) {
      if (!buffer) return "";
      const bytes = new Uint8Array(buffer);
      let binary = "";
      const chunkSize = 0x8000;
      for (let i = 0; i < bytes.length; i += chunkSize) {
        const chunk = bytes.subarray(i, i + chunkSize);
        binary += String.fromCharCode.apply(null, chunk);
      }
      return btoa(binary);
    }

    /**
     * Exports the terminal scrollback buffer as plain text.
     * @param {string} sessionId
     * @param {Terminal} term
     * @private
     */
    _exportTerminalOutput(sessionId, term) {
      const { createElement } = window.NexusTerminal;
      const buffer = term.buffer.active;
      const lines = [];
      for (let i = 0; i < buffer.length; i++) {
        const line = buffer.getLine(i);
        if (line) lines.push(line.translateToString(true));
      }
      const text = lines.join("\n").trimEnd();

      // Use Wails save dialog if available
      if (window.runtime && window.runtime.SaveFileDialog) {
        // Copy to clipboard as a fallback
      }

      // Copy to clipboard
      if (window.runtime && window.runtime.ClipboardSetText) {
        window.runtime.ClipboardSetText(text);
      } else {
        navigator.clipboard.writeText(text).catch(function () {});
      }

      // Also offer download via blob
      const blob = new Blob([text], { type: "text/plain" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "terminal-output-" + sessionId.substring(0, 8) + ".txt";
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);

      this.app.notifications.success(
        "Terminal output exported & copied to clipboard",
      );
    }

    /**
     * Shows the port forwarding management panel for an SSH session.
     * @param {string} sessionId
     * @private
     */
    _showPortForwardPanel(sessionId) {
      const { createElement, escapeHtml } = window.NexusTerminal;
      const session = this.app.state.getSession(sessionId);
      if (!session || session.protocol !== "ssh") {
        this.app.notifications.error(
          "Port forwarding is only available for SSH sessions",
        );
        return;
      }

      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-lg");

      modal.innerHTML = `
        <div class="modal-header">
          <h3 class="modal-title">Port Forwards</h3>
          <button class="btn btn-icon btn-xs modal-close">&times;</button>
        </div>
        <div class="modal-body" style="min-height:200px;">
          <div class="portfwd-list" style="margin-bottom:var(--space-3);"></div>
          <div class="portfwd-add" style="border-top:1px solid var(--border-subtle);padding-top:var(--space-3);">
            <div style="font-weight:var(--weight-medium);font-size:var(--text-sm);margin-bottom:var(--space-2);">Add New Forward</div>
            <div style="display:grid;grid-template-columns:auto 1fr 60px 20px 1fr 60px;gap:var(--space-2);align-items:center;">
              <select class="form-input pf-direction" style="height:28px;font-size:var(--text-xs);">
                <option value="local">Local →</option>
                <option value="remote">Remote ←</option>
              </select>
              <input class="form-input pf-bind-host" type="text" placeholder="127.0.0.1" value="127.0.0.1" style="height:28px;font-size:var(--text-xs);" />
              <input class="form-input pf-bind-port" type="number" placeholder="Port" style="height:28px;font-size:var(--text-xs);" />
              <span style="text-align:center;color:var(--text-tertiary);">→</span>
              <input class="form-input pf-dest-host" type="text" placeholder="localhost" value="localhost" style="height:28px;font-size:var(--text-xs);" />
              <input class="form-input pf-dest-port" type="number" placeholder="Port" style="height:28px;font-size:var(--text-xs);" />
            </div>
            <div style="display:flex;gap:var(--space-2);margin-top:var(--space-2);">
              <input class="form-input pf-label" type="text" placeholder="Label (optional)" style="flex:1;height:28px;font-size:var(--text-xs);" />
              <button class="btn btn-sm btn-primary pf-add-btn">Start Forward</button>
            </div>
          </div>
        </div>
      `;

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);
      requestAnimationFrame(() => {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
      });

      const close = () => {
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(() => backdrop.remove(), 200);
      };

      modal.querySelector(".modal-close").addEventListener("click", close);
      backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) close();
      });

      const listEl = modal.querySelector(".portfwd-list");
      const self = this;

      const renderForwards = async () => {
        try {
          const forwards =
            (await window.NexusTerminal.wailsService.ListPortForwards(sessionId)) || [];
          listEl.innerHTML = "";
          if (forwards.length === 0) {
            listEl.innerHTML =
              '<div style="color:var(--text-tertiary);font-size:var(--text-sm);padding:var(--space-2);">No active port forwards</div>';
            return;
          }
          for (const fwd of forwards) {
            const row = createElement("div", "", {});
            row.style.cssText =
              "display:flex;align-items:center;gap:var(--space-2);padding:var(--space-1) 0;border-bottom:1px solid var(--border-subtle);";
            const dirLabel = fwd.direction === "local" ? "L" : "R";
            const dirColor =
              fwd.direction === "local" ? "var(--accent)" : "var(--warning)";
            row.innerHTML = `
              <span style="font-size:var(--text-xs);font-weight:var(--weight-semibold);color:${dirColor};width:16px;">${dirLabel}</span>
              <span style="font-family:var(--font-mono);font-size:var(--text-xs);color:var(--text-primary);flex:1;">
                ${escapeHtml(fwd.bindHost || "127.0.0.1")}:${fwd.bindPort} → ${escapeHtml(fwd.destHost || "localhost")}:${fwd.destPort}
              </span>
              ${fwd.label ? `<span style="font-size:var(--text-xs);color:var(--text-tertiary);">${escapeHtml(fwd.label)}</span>` : ""}
            `;
            const stopBtn = createElement("button", "btn btn-xs btn-danger", {
              textContent: "Stop",
            });
            stopBtn.addEventListener("click", async () => {
              try {
                await window.NexusTerminal.wailsService.StopPortForward(sessionId, fwd.id);
                self.app.notifications.info("Forward stopped");
                renderForwards();
              } catch (err) {
                self.app.notifications.error(
                  "Failed to stop: " + (err.message || err),
                );
              }
            });
            row.appendChild(stopBtn);
            listEl.appendChild(row);
          }
        } catch (err) {
          listEl.innerHTML =
            '<div style="color:var(--error);font-size:var(--text-sm);">Failed to load forwards</div>';
        }
      };

      renderForwards();

      // Add forward button
      modal.querySelector(".pf-add-btn").addEventListener("click", async () => {
        const direction = modal.querySelector(".pf-direction").value;
        const bindHost =
          modal.querySelector(".pf-bind-host").value.trim() || "127.0.0.1";
        const bindPort = parseInt(
          modal.querySelector(".pf-bind-port").value,
          10,
        );
        const destHost =
          modal.querySelector(".pf-dest-host").value.trim() || "localhost";
        const destPort = parseInt(
          modal.querySelector(".pf-dest-port").value,
          10,
        );
        const label = modal.querySelector(".pf-label").value.trim();

        if (!bindPort || !destPort) {
          self.app.notifications.error(
            "Bind port and destination port are required",
          );
          return;
        }

        try {
          await window.NexusTerminal.wailsService.StartPortForward(sessionId, {
            id: "",
            direction: direction,
            bindHost: bindHost,
            bindPort: bindPort,
            destHost: destHost,
            destPort: destPort,
            enabled: true,
            label: label,
          });
          self.app.notifications.success("Forward started");
          modal.querySelector(".pf-bind-port").value = "";
          modal.querySelector(".pf-dest-port").value = "";
          modal.querySelector(".pf-label").value = "";
          renderForwards();
        } catch (err) {
          self.app.notifications.error(
            "Failed to start forward: " + (err.message || err),
          );
        }
      });
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
