(function () {
  /**
   * Nexus Terminal — main application entry point.
   * Initialises the application shell, loads profiles, and sets up event listeners.
   */

  const {
    State,
    Sidebar,
    TabManager,
    TerminalManager,
    ProfileEditor,
    RawViewer,
    Notifications,
    Settings,
    CommandPalette,
    SecretPrompt,
    SplitPaneManager,
    RecordingManager,
    ExportImportUI,
    SFTPBrowser,
    SnippetLibrary,
    SSHKeyManager,
    TriggerEngine,
    LeviathanWorkbench,
    stateLabel,
  } = window.NexusTerminal;

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
      this.splitPanes = new SplitPaneManager(this);
      this.recordings = new RecordingManager(this);
      this.exportImport = new ExportImportUI(this);
      this.sftp = new SFTPBrowser(this);
      this.snippets = new SnippetLibrary(this);
      this.sshKeys = new SSHKeyManager(this);
      this.triggers = new TriggerEngine(this);
      this.leviathan = new LeviathanWorkbench(this);
      this.updateInfo = null;
      this.mode = "nexus";
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
      this.checkForUpdates(true);
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
            console.warn(
              "Wails runtime not detected after " +
                maxWait +
                "ms — proceeding anyway",
            );
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
        this.notifications.error("Failed to load profiles: " + err);
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
        console.warn("Failed to load preferences:", err);
      }
    }

    /**
     * Queries the secret store status (initialised / unlocked).
     */
    async checkSecretStore() {
      try {
        const initialised =
          await window.go.api.Service.IsSecretStoreInitialised();
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
      if (!window.runtime || typeof window.runtime.EventsOn !== "function") {
        console.warn(
          "window.runtime not available — Wails event listeners not bound",
        );
        return;
      }
      // Incoming session data (terminal output)
      window.runtime.EventsOn("session:data", (payload) => {
        this.terminals.handleData(payload.sessionID, payload.data);
        this.tabs.markActivity(payload.sessionID);
        // Check trigger rules against decoded text
        try {
          const raw = atob(payload.data);
          this.triggers.check(payload.sessionID, raw);
        } catch (_) {
          /* noop */
        }
      });

      // Session state transitions (connecting → connected → reconnecting → disconnected)
      window.runtime.EventsOn("session:state", (payload) => {
        this.tabs.updateSessionState(
          payload.sessionID,
          payload.state,
          payload.error,
        );
        this.updateStatusBar();

        const showDisconnectedOverlay =
          payload.state === "disconnected" || payload.state === "error";
        this.terminals.setDisconnectedOverlay(
          payload.sessionID,
          showDisconnectedOverlay,
          payload.error,
        );

        if (payload.state === "error" && payload.error) {
          this._handleSessionError(payload.sessionID, payload.error);
        }

        // Show reconnecting notification
        if (payload.state === "reconnecting") {
          const attempt = payload.attempt || 1;
          const maxRetries = payload.maxRetries || 5;
          const delaySec = payload.delaySec || 1;
          this.notifications.info(
            `Reconnecting (attempt ${attempt}/${maxRetries}) in ${delaySec}s…`,
          );
        }

        // Once connected, ensure the backend PTY has the correct dimensions.
        // The initial fit happens before the SSH handshake completes, so the
        // onResize callback fires when the backend isn't ready. By the time
        // the connection is established, xterm already has the right cols/rows
        // and fitAddon.fit() won't trigger a new resize event. We must
        // explicitly send the current terminal size to the backend.
        if (payload.state === "connected") {
          const entry = this.terminals.terminals.get(payload.sessionID);
          if (entry) {
            const { cols, rows } = entry.term;
            try {
              window.go.api.Service.ResizeSession(
                payload.sessionID,
                cols,
                rows,
              );
            } catch (_) {
              /* noop */
            }
          }
          // Also re-fit to catch any layout changes that occurred while connecting.
          // Multiple delays help byobu/tmux render correctly after its status
          // bar initialisation completes.
          const fit = () => this.terminals.fitSession(payload.sessionID);
          setTimeout(fit, 50);
          setTimeout(fit, 300);
          setTimeout(fit, 800);
        }
      });

      // Session closed by backend
      window.runtime.EventsOn("session:closed", (payload) => {
        this.sftp.close(payload.sessionID);
        this.triggers.cleanup(payload.sessionID);
        this.tabs.handleSessionClosed(payload.sessionID);
        this.updateStatusBar();
      });

      // RAW TCP data (separate channel for the raw viewer)
      window.runtime.EventsOn("session:raw:data", (payload) => {
        this.rawViewer.handleData(payload);
      });

      // GitHub repository data
      window.runtime.EventsOn("session:github:repos", (payload) => {
        window.NexusTerminal.GitHubViewer.updateRepos(
          payload.sessionID,
          payload.login,
          payload.repos,
        );
      });

      // GitHub refresh errors
      window.runtime.EventsOn("session:github:error", (payload) => {
        window.NexusTerminal.GitHubViewer.showError(
          payload.sessionID,
          payload.error,
        );
      });

      // GitHub status updates
      window.runtime.EventsOn("session:github:status", (payload) => {
        window.NexusTerminal.GitHubViewer.setStatus(
          payload.sessionID,
          payload.status,
        );
      });

      // VNC WebSocket proxy ready — connect the viewer
      window.runtime.EventsOn("session:vnc:ready", (payload) => {
        window.NexusTerminal.VNCViewer.connect(
          payload.sessionID,
          payload.wsURL,
        );
      });

      // App-wide commands from the native menu
      window.runtime.EventsOn("app:open-about", () => {
        this.settings.openTab("about");
      });

      // Port-forwarding events
      window.runtime.EventsOn("session:forward:started", (payload) => {
        const fwd = payload.forward || {};
        const label =
          fwd.label ||
          `${fwd.bindHost || ""}:${fwd.bindPort} → ${fwd.destHost}:${fwd.destPort}`;
        this.notifications.success(`Forward started: ${label}`);
      });

      window.runtime.EventsOn("session:forward:stopped", (payload) => {
        this.notifications.info(`Forward stopped: ${payload.forwardID}`);
      });

      window.runtime.EventsOn("session:forward:error", (payload) => {
        this.notifications.error(`Forward error: ${payload.error}`);
      });

      // Recording events
      window.runtime.EventsOn("session:recording:started", (payload) => {
        this.recordings.activeSessions.add(payload.sessionID);
        this.recordings._updateRecordingIndicator(payload.sessionID, true);
      });

      window.runtime.EventsOn("session:recording:stopped", (payload) => {
        this.recordings.activeSessions.delete(payload.sessionID);
        this.recordings._updateRecordingIndicator(payload.sessionID, false);
      });

      // Telnet negotiation debug messages
      window.runtime.EventsOn("session:debug", (payload) => {
        console.debug("[session:debug]", payload);
      });

      // Backend ready event (emitted from DomReady)
      window.runtime.EventsOn("backend:ready", () => {
        console.log("Backend is ready");
      });

      // Host key verification requests
      window.runtime.EventsOn("session:hostkey:verify", async (payload) => {
        try {
          const accepted = await this.secretPrompt.promptHostKeyVerification(
            payload.host,
            payload.keyType,
            payload.fingerprint,
          );
          window.runtime.EventsEmit("session:hostkey:response", {
            requestID: payload.requestID,
            sessionID: payload.sessionID,
            accepted,
          });
        } catch (_) {
          window.runtime.EventsEmit("session:hostkey:response", {
            requestID: payload.requestID,
            sessionID: payload.sessionID,
            accepted: false,
          });
        }
      });

      // SFTP transfer progress events
      window.runtime.EventsOn("session:sftp:progress", (payload) => {
        this.sftp.handleTransferProgress(payload);
      });

      // Password/passphrase prompt requests from the backend
      window.runtime.EventsOn("session:secret:prompt", async (payload) => {
        try {
          const value = await this.secretPrompt.prompt(
            payload.title || "Credentials Required",
            payload.message || "Enter your password or passphrase.",
          );
          window.runtime.EventsEmit("session:secret:response", {
            requestID: payload.requestID,
            value,
          });
        } catch (_) {
          window.runtime.EventsEmit("session:secret:response", {
            requestID: payload.requestID,
            value: "",
            cancelled: true,
          });
        }
      });
    }

    /**
     * Disconnects and removes a session without confirmation prompts.
     * @param {string} sessionId
     */
    async closeSessionImmediate(sessionId) {
      try {
        await window.go.api.Service.DisconnectSession(sessionId);
      } catch (_) {
        // Ignore failures; we still want to clean up locally.
      }

      this.terminals.destroyTerminal(sessionId);
      this.rawViewer.destroyViewer(sessionId);
      window.NexusTerminal.GitHubViewer.destroyViewer(sessionId);
      window.NexusTerminal.VNCViewer.destroyViewer(sessionId);
      this.tabs.removeTab(sessionId);
      this.state.removeSession(sessionId);
      this.updateStatusBar();
    }

    // ── Keyboard shortcuts ───────────────────────────────────────────

    /**
     * Registers global keyboard shortcuts.
     */
    setupKeyboard() {
      document.addEventListener("keydown", (e) => {
        const mod = e.ctrlKey || e.metaKey;

        // Ctrl/Cmd+K — command palette
        if (mod && e.key === "k") {
          e.preventDefault();
          this.commandPalette.toggle();
          return;
        }

        // Ctrl/Cmd+N — new connection
        if (mod && e.key === "n") {
          e.preventDefault();
          this.profileEditor.openNew();
          return;
        }

        // Ctrl/Cmd+W — close active tab
        if (mod && e.key === "w") {
          e.preventDefault();
          this.tabs.closeActive();
          return;
        }

        // Ctrl/Cmd+Tab — next tab
        if (mod && e.key === "Tab" && !e.shiftKey) {
          e.preventDefault();
          const next = this.tabs.getNextTab();
          if (next) this.tabs.setActive(next);
          return;
        }

        // Ctrl/Cmd+Shift+Tab — previous tab
        if (mod && e.key === "Tab" && e.shiftKey) {
          e.preventDefault();
          const prev = this.tabs.getPrevTab();
          if (prev) this.tabs.setActive(prev);
          return;
        }

        // Ctrl/Cmd+1-9 — switch to tab by index
        if (mod && e.key >= "1" && e.key <= "9") {
          e.preventDefault();
          const idx = parseInt(e.key, 10) - 1;
          const order = this.tabs.getTabOrder();
          if (idx < order.length) {
            this.tabs.setActive(order[idx]);
          }
          return;
        }

        // Ctrl/Cmd+, — settings
        if (mod && e.key === ",") {
          e.preventDefault();
          this.settings.open();
          return;
        }

        // Ctrl/Cmd+F — find in terminal
        if (mod && e.key === "f") {
          e.preventDefault();
          this._openTerminalSearch();
          return;
        }

        // F11 — toggle fullscreen (Windows/Linux convention)
        if (e.key === "F11") {
          e.preventDefault();
          if (
            window.runtime &&
            typeof window.runtime.WindowFullscreen === "function"
          ) {
            window.runtime.WindowFullscreen();
          }
          return;
        }

        // Escape — close any open overlays
        if (e.key === "Escape") {
          this.commandPalette.close();
          // Also exit fullscreen if active (Windows/Linux)
          if (
            window.runtime &&
            typeof window.runtime.WindowUnfullscreen === "function"
          ) {
            window.runtime.WindowUnfullscreen();
          }
        }

        // Ctrl/Cmd+Shift+D — split pane vertically (left/right)
        if (mod && e.shiftKey && e.key === "D") {
          e.preventDefault();
          this.splitPane("vertical");
          return;
        }

        // Ctrl/Cmd+Shift+E — split pane horizontally (top/bottom)
        if (mod && e.shiftKey && e.key === "E") {
          e.preventDefault();
          this.splitPane("horizontal");
          return;
        }

        // Ctrl/Cmd+Shift+R — toggle recording on active session
        if (mod && e.shiftKey && e.key === "R") {
          e.preventDefault();
          if (this.state.activeSessionId) {
            this.recordings.toggle(this.state.activeSessionId);
          }
          return;
        }

        // Ctrl/Cmd+Shift+B — toggle broadcast mode
        if (mod && e.shiftKey && e.key === "B") {
          e.preventDefault();
          this.toggleBroadcast();
          return;
        }

        // Ctrl/Cmd+Shift+S — open snippet library
        if (mod && e.shiftKey && e.key === "S") {
          e.preventDefault();
          this.snippets.open();
          return;
        }
      });
    }

    // ── UI event wiring ──────────────────────────────────────────────

    /**
     * Binds click handlers to static UI elements.
     */
    setupUI() {
      var btnEl = document.getElementById("btn-new-connection");
      this._bindClick("btn-new-connection", () => this.profileEditor.openNew());
      this._bindClick("btn-command-palette", () =>
        this.commandPalette.toggle(),
      );
      this._bindClick("btn-settings", () => this.settings.open());
      this._bindClick("btn-import", () => this.importProfiles());
      this._bindClick("btn-export", () => this.exportProfiles());
      this._bindClick("btn-recordings", () => this.recordings.openBrowser());
      this._bindClick("btn-new-tab", () => this.profileEditor.openNew());
      this._bindClick("btn-broadcast", () => this.toggleBroadcast());
      this._bindClick("mode-nexus", () => this.setMode("nexus"));
      this._bindClick("mode-leviathan", () => this.setMode("leviathan"));

      // Wire the "Local Machine" sidebar button
      this.sidebar.onConnectLocal = () => this.connectLocal();

      // Right-click on broadcast button opens target selector
      const broadcastBtn = document.getElementById("btn-broadcast");
      if (broadcastBtn) {
        broadcastBtn.addEventListener("contextmenu", (e) => {
          e.preventDefault();
          this.selectBroadcastTargets();
        });
      }
      this._bindClick("btn-empty-connect", () => this.profileEditor.openNew());
      this._bindClick("btn-empty-quick", () => this.quickConnect());

      const updateBtn = document.getElementById("status-update");
      if (updateBtn) {
        updateBtn.addEventListener("mousedown", (e) => e.preventDefault());
        updateBtn.addEventListener("click", () =>
          this.settings.openTab("about"),
        );
      }

      const searchInput = document.getElementById("profile-search");
      if (searchInput) {
        searchInput.addEventListener("input", (e) =>
          this.sidebar.search(e.target.value),
        );
      }

      // Window resize → re-fit active terminal (debounced)
      const { debounce } = window.NexusTerminal;
      window.addEventListener(
        "resize",
        debounce(() => {
          this.terminals.fitActive();
          this.splitPanes.refitAll();
        }, 100),
      );

      window.addEventListener("focus", () => {
        requestAnimationFrame(() => {
          this._focusVisibleTerminal();
        });
      });
    }

    // ── App mode switching ─────────────────────────────────────────

    /**
     * Switches the sidebar between Nexus profiles and Leviathan tools.
     * @param {"nexus"|"leviathan"} mode
     */
    setMode(mode) {
      this.mode = mode === "leviathan" ? "leviathan" : "nexus";
      const root = document.getElementById("app");
      if (root) {
        root.classList.toggle("mode-leviathan", this.mode === "leviathan");
      }
      document
        .getElementById("mode-nexus")
        ?.classList.toggle("is-active", this.mode === "nexus");
      document
        .getElementById("mode-leviathan")
        ?.classList.toggle("is-active", this.mode === "leviathan");

      if (this.mode === "leviathan") {
        this.leviathan.renderSidebar();
        this.leviathan.openTool("hex", { reuseExisting: true });
      } else {
        this.sidebar.render();
      }
    }

    // ── Split pane ─────────────────────────────────────────────────

    /**
     * Splits the active session panel in the given direction.
     * @param {'horizontal'|'vertical'} direction
     */
    splitPane(direction) {
      if (!this.state.activeSessionId) return;
      if (direction === "horizontal") {
        this.splitPanes.splitHorizontal();
      } else {
        this.splitPanes.splitVertical();
      }
    }

    // ── Broadcast mode ─────────────────────────────────────────────

    /**
     * Toggles broadcast mode — when active, keystrokes are sent to all
     * connected sessions simultaneously.
     */
    toggleBroadcast() {
      this.state.broadcastMode = !this.state.broadcastMode;
      if (!this.state.broadcastMode) {
        this.state.broadcastTargets = null;
      }
      const btn = document.getElementById("btn-broadcast");
      if (btn) {
        btn.classList.toggle("is-active", this.state.broadcastMode);
        btn.title = this.state.broadcastMode
          ? "Broadcast Mode ON — keystrokes sent to all sessions (Ctrl+Shift+B)"
          : "Toggle Broadcast Mode (Ctrl+Shift+B)";
      }
      this.notifications.info(
        this.state.broadcastMode
          ? "Broadcast mode enabled — input sent to all sessions"
          : "Broadcast mode disabled",
      );
    }

    /**
     * Opens a modal to select which sessions receive broadcast input.
     */
    selectBroadcastTargets() {
      const { createElement, escapeHtml } = window.NexusTerminal;
      const sessions = this.state
        .getAllSessions()
        .filter(
          (s) => s.state === "connected" && s.id !== this.state.activeSessionId,
        );

      if (sessions.length === 0) {
        this.notifications.error("No other connected sessions to broadcast to");
        return;
      }

      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-sm");
      const currentTargets = this.state.broadcastTargets || new Set();

      let listHtml = "";
      for (const s of sessions) {
        const profile = this.state.getProfile(s.profileId);
        const label = profile
          ? profile.displayName || profile.host
          : s.id.substring(0, 8);
        const checked =
          currentTargets.size === 0 || currentTargets.has(s.id)
            ? "checked"
            : "";
        listHtml += `
          <label style="display:flex;align-items:center;gap:var(--space-2);padding:var(--space-1) 0;font-size:var(--text-sm);cursor:pointer;">
            <input type="checkbox" class="broadcast-target-check" data-session-id="${s.id}" ${checked} />
            <span style="color:var(--text-primary);">${escapeHtml(label)}</span>
          </label>
        `;
      }

      modal.innerHTML = `
        <div class="modal-header">
          <h3 class="modal-title">Broadcast Targets</h3>
          <button class="btn btn-icon btn-xs modal-close">&times;</button>
        </div>
        <div class="modal-body">
          <p style="font-size:var(--text-xs);color:var(--text-tertiary);margin-bottom:var(--space-2);">
            Select which sessions should receive broadcast input:
          </p>
          ${listHtml}
        </div>
        <div class="modal-footer">
          <button class="btn btn-sm btn-ghost broadcast-cancel">Cancel</button>
          <button class="btn btn-sm btn-secondary broadcast-all">Select All</button>
          <button class="btn btn-sm btn-primary broadcast-apply">Apply & Enable</button>
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
      modal.querySelector(".broadcast-cancel").addEventListener("click", close);
      backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) close();
      });

      modal.querySelector(".broadcast-all").addEventListener("click", () => {
        modal.querySelectorAll(".broadcast-target-check").forEach((cb) => {
          cb.checked = true;
        });
      });

      modal.querySelector(".broadcast-apply").addEventListener("click", () => {
        const checks = modal.querySelectorAll(".broadcast-target-check");
        const selected = new Set();
        checks.forEach((cb) => {
          if (cb.checked) selected.add(cb.dataset.sessionId);
        });

        if (selected.size === 0) {
          this.notifications.error("Select at least one target session");
          return;
        }

        // If all are selected, use null (broadcast to all)
        this.state.broadcastTargets =
          selected.size === sessions.length ? null : selected;
        if (!this.state.broadcastMode) {
          this.toggleBroadcast();
        } else {
          const targetLabel =
            selected.size === sessions.length
              ? "all sessions"
              : selected.size + " sessions";
          this.notifications.info("Broadcasting to " + targetLabel);
        }
        close();
      });
    }

    // ── Connection lifecycle ─────────────────────────────────────────

    /**
     * Opens a new session for the given profile ID.
     * @param {string} profileId
     */
    async connectProfile(profileId, options = {}) {
      const profile = this.state.getProfile(profileId);
      if (!profile) {
        this.notifications.error("Profile not found");
        return;
      }

      const existingSessions = this.state.getAllSessions().filter((session) => {
        if (session.profileId !== profileId) return false;
        return (
          session.state === "connected" ||
          session.state === "connecting" ||
          session.state === "reconnecting"
        );
      });
      if (existingSessions.length > 0) {
        const preferred =
          existingSessions.find((s) => s.id === this.state.activeSessionId) ||
          existingSessions[0];
        const decision = await this._confirmReuseSession(profile, preferred);
        if (decision === "existing") {
          this.tabs.setActive(preferred.id);
          if (preferred.protocol !== "raw" && preferred.protocol !== "github") {
            this.terminals.focusTerminal(preferred.id);
          }
          return;
        }
        if (decision !== "new") return;
      }

      if (
        !options.skipDirectVNCWarning &&
        this._requiresDirectVNCWarning(profile)
      ) {
        const ok = await this.confirmDirectVNCProfile(profile, "connect");
        if (!ok) return;
      }

      try {
        const sessionInfo =
          await window.go.api.Service.ConnectSession(profileId);
        this.state.addSession(sessionInfo);
        this.tabs.addTab(sessionInfo);

        // Create the appropriate viewer
        const panelId = `panel-${sessionInfo.id}`;
        if (profile.protocol === "raw") {
          this.rawViewer.createViewer(sessionInfo.id, panelId, profile);
        } else if (profile.protocol === "github") {
          const container = document.getElementById(panelId);
          if (container) {
            window.NexusTerminal.GitHubViewer.createViewer(
              sessionInfo.id,
              container,
              profile.displayName || profile.host,
            );
          }
        } else if (profile.protocol === "vnc") {
          const container = document.getElementById(panelId);
          if (container) {
            window.NexusTerminal.VNCViewer.createViewer(
              sessionInfo.id,
              container,
              profile,
            );
          }
        } else {
          this.terminals.createTerminal(sessionInfo.id, panelId, profile);
        }

        this.notifications.info(
          `Connecting to ${profile.displayName || profile.host}…`,
        );
      } catch (err) {
        this.notifications.error("Connection failed: " + err);
      }
    }

    /**
     * Opens a local terminal session.
     */
    async connectLocal() {
      try {
        const sessionInfo = await window.go.api.Service.ConnectLocal();
        this.state.addSession(sessionInfo);
        this.tabs.addTab(sessionInfo);

        const panelId = `panel-${sessionInfo.id}`;
        const prefs = this.state.preferences || {};
        this.terminals.createTerminal(sessionInfo.id, panelId, {
          terminalFontFamily: prefs.defaultFontFamily,
          terminalFontSize: prefs.defaultFontSize,
          terminalScrollback: prefs.defaultScrollback,
          terminalCursorStyle: prefs.defaultCursorStyle,
        });

        this.notifications.info("Opening local terminal…");
      } catch (err) {
        this.notifications.error("Local terminal failed: " + err);
      }
    }

    /**
     * Opens a quick connect dialog for ad-hoc connections without saving a profile.
     */
    async quickConnect() {
      const createElement = window.NexusTerminal.createElement;
      const result = await new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">Quick Connect</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label class="form-label">Protocol</label>
                        <select class="form-input qc-protocol">
                            <option value="ssh">SSH</option>
                            <option value="telnet">Telnet</option>
                            <option value="raw">RAW TCP</option>
                        </select>
                    </div>
                    <div class="form-group">
                        <label class="form-label">Host</label>
                        <input class="form-input qc-host" type="text" placeholder="e.g. 192.168.1.1" autofocus />
                    </div>
                    <div class="form-group">
                        <label class="form-label">Port</label>
                        <input class="form-input qc-port" type="number" value="22" min="1" max="65535" />
                    </div>
                    <div class="form-group qc-username-group">
                        <label class="form-label">Username</label>
                        <input class="form-input qc-username" type="text" placeholder="root" />
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm qc-cancel">Cancel</button>
                    <button class="btn btn-sm btn-primary qc-connect">Connect</button>
                </div>
            `;
        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => {
          backdrop.classList.add("is-visible");
          modal.classList.add("is-visible");
        });

        const protocolSelect = modal.querySelector(".qc-protocol");
        const portInput = modal.querySelector(".qc-port");
        const usernameGroup = modal.querySelector(".qc-username-group");
        const hostInput = modal.querySelector(".qc-host");

        // Update port default and username visibility when protocol changes
        protocolSelect.addEventListener("change", () => {
          const proto = protocolSelect.value;
          const defaultPorts = { ssh: 22, telnet: 23, raw: 23 };
          portInput.value = defaultPorts[proto] || 22;
          usernameGroup.style.display = proto === "ssh" ? "" : "none";
        });

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
          .querySelector(".qc-cancel")
          .addEventListener("click", () => close(null));
        modal.querySelector(".qc-connect").addEventListener("click", () => {
          const host = hostInput.value.trim();
          if (!host) {
            hostInput.focus();
            return;
          }
          close({
            protocol: protocolSelect.value,
            host,
            port: parseInt(portInput.value, 10) || 22,
            username: modal.querySelector(".qc-username").value.trim(),
          });
        });
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(null);
        });
        hostInput.addEventListener("keydown", (e) => {
          if (e.key === "Enter") modal.querySelector(".qc-connect").click();
          if (e.key === "Escape") close(null);
        });

        setTimeout(() => hostInput.focus(), 50);
      });

      if (!result) return;

      try {
        const profile = {
          protocol: result.protocol,
          host: result.host,
          port: result.port,
          username: result.username || "",
          displayName: result.host,
          tags: [],
        };
        const sessionInfo = await window.go.api.Service.ConnectQuick(profile);
        this.state.addSession(sessionInfo);
        this.tabs.addTab(sessionInfo);

        const panelId = `panel-${sessionInfo.id}`;
        if (result.protocol === "raw") {
          this.rawViewer.createViewer(sessionInfo.id, panelId, {
            protocol: "raw",
            raw: {},
          });
        } else {
          this.terminals.createTerminal(sessionInfo.id, panelId, {
            protocol: result.protocol,
          });
        }

        this.notifications.info(`Connecting to ${result.host}…`);
      } catch (err) {
        this.notifications.error("Quick connect failed: " + err);
      }
    }

    /**
     * Prompts to reuse an existing session for a profile.
     * @param {Object} profile
     * @param {Object} session
     * @returns {Promise<'existing'|'new'|null>}
     * @private
     */
    _confirmReuseSession(profile, session) {
      const createElement = window.NexusTerminal.createElement;
      const label = window.NexusTerminal.escapeHtml(
        (profile.displayName || profile.host || "Untitled").trim(),
      );
      const stateLabel =
        session.state === "connected" ? "connected" : "connecting";

      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">Connection Already Open</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <p style="font-size:var(--text-sm);color:var(--text-secondary)">
                        <strong>${label}</strong> is already ${stateLabel}.
                        Do you want to use the existing session or open a new one?
                    </p>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm confirm-cancel">Cancel</button>
                    <button class="btn btn-sm confirm-new">New Connection</button>
                    <button class="btn btn-sm btn-primary confirm-ok">Use Existing</button>
                </div>
            `;
        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => {
          backdrop.classList.add("is-visible");
          modal.classList.add("is-visible");
        });

        const close = (val) => {
          backdrop.classList.remove("is-visible");
          modal.classList.remove("is-visible");
          setTimeout(() => backdrop.remove(), 200);
          resolve(val);
        };

        modal
          .querySelector(".modal-close")
          .addEventListener("click", () => close(null));
        modal
          .querySelector(".confirm-cancel")
          .addEventListener("click", () => close(null));
        modal
          .querySelector(".confirm-ok")
          .addEventListener("click", () => close("existing"));
        modal
          .querySelector(".confirm-new")
          .addEventListener("click", () => close("new"));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(null);
        });
      });
    }

    /**
     * Reports whether a profile connects directly to VNC without SSH tunnelling.
     * @param {Object} profile
     * @returns {boolean}
     * @private
     */
    _requiresDirectVNCWarning(profile) {
      return !!(
        profile &&
        profile.protocol === "vnc" &&
        profile.vnc &&
        !profile.vnc.sshTunnel
      );
    }

    /**
     * Warns before saving or connecting direct VNC profiles.
     * @param {Object} profile
     * @param {"save"|"connect"} action
     * @returns {Promise<boolean>}
     */
    confirmDirectVNCProfile(profile, action) {
      const { createElement, createModalShell } = window.NexusTerminal;
      const label = (
        profile.displayName ||
        profile.vnc?.vncHost ||
        profile.host ||
        "VNC"
      ).trim();
      const actionLabel = action === "connect" ? "Connect" : "Save";

      return new Promise((resolve) => {
        const shell = createModalShell("Direct VNC Connection", "modal-sm");
        const bodyText = createElement("p", "", {
          textContent:
            label +
            " is configured without an SSH tunnel. VNC traffic may be visible to the network unless the server is otherwise protected.",
        });
        bodyText.style.fontSize = "var(--text-sm)";
        bodyText.style.color = "var(--text-secondary)";
        shell.body.appendChild(bodyText);

        const cancelBtn = createElement("button", "btn btn-sm", {
          textContent: "Cancel",
        });
        const okBtn = createElement(
          "button",
          "btn btn-sm btn-warning direct-vnc-confirm",
          { textContent: actionLabel },
        );
        shell.footer.appendChild(cancelBtn);
        shell.footer.appendChild(okBtn);

        const close = (value) => {
          shell.remove();
          resolve(value);
        };

        shell.closeButton.addEventListener("click", () => close(false));
        cancelBtn.addEventListener("click", () => close(false));
        okBtn.addEventListener("click", () => close(true));
        shell.backdrop.addEventListener("click", (e) => {
          if (e.target === shell.backdrop) close(false);
        });
        shell.show();
      });
    }

    // ── Import / Export ──────────────────────────────────────────────

    /**
     * Triggers the encrypted profile import dialog.
     */
    async importProfiles() {
      this.exportImport.showImportModal();
    }

    /**
     * Triggers the encrypted profile export dialog.
     */
    async exportProfiles() {
      this.exportImport.showExportModal();
    }

    // ── Status bar ───────────────────────────────────────────────────

    /**
     * Updates the status bar with current session info.
     */
    updateStatusBar() {
      const connEl = document.getElementById("status-connection");
      const countEl = document.getElementById("status-session-count");
      const latencyEl = document.getElementById("status-latency");
      const vaultEl = document.getElementById("status-vault");
      const updateEl = document.getElementById("status-update");
      if (!connEl) return;

      const sessions = this.state.getAllSessions();
      const connected = sessions.filter((s) => s.state === "connected").length;
      const total = sessions.length;

      const activeSession = this.state.activeSessionId
        ? this.state.getSession(this.state.activeSessionId)
        : null;

      if (activeSession) {
        connEl.innerHTML = `<span class="status-dot is-${activeSession.state}"></span> ${stateLabel(activeSession.state)}`;
      } else {
        connEl.innerHTML =
          '<span class="status-dot is-disconnected"></span> No active session';
      }

      // Latency indicator — only show for connected SSH sessions.
      if (latencyEl) {
        if (
          activeSession &&
          activeSession.state === "connected" &&
          activeSession.protocol === "ssh"
        ) {
          this._startLatencyPing(activeSession.id);
        } else {
          this._stopLatencyPing();
          latencyEl.textContent = "";
        }
      }

      if (countEl) countEl.textContent = `${connected}/${total} sessions`;
      if (vaultEl) {
        vaultEl.textContent = this.state.secretStoreUnlocked
          ? "🔓 Unlocked"
          : "🔒 Locked";
      }

      if (updateEl) {
        if (this.updateInfo && this.updateInfo.available) {
          const version = this.updateInfo.latestVersion
            ? ` (${this.updateInfo.latestVersion})`
            : "";
          updateEl.textContent = `Update available${version}`;
          updateEl.classList.remove("is-hidden");
        } else {
          updateEl.textContent = "Update available";
          updateEl.classList.add("is-hidden");
        }
      }
    }

    _startLatencyPing(sessionID) {
      // Don't restart if already pinging the same session.
      if (this._latencySessionID === sessionID && this._latencyTimer) return;
      this._stopLatencyPing();
      this._latencySessionID = sessionID;

      const doPing = async () => {
        if (!window.go || !window.go.api || !window.go.api.Service) return;
        try {
          const ms = await window.go.api.Service.PingSession(sessionID);
          const el = document.getElementById("status-latency");
          if (el && this._latencySessionID === sessionID) {
            el.textContent = ms + " ms";
            el.className =
              ms < 100
                ? "latency-good"
                : ms < 250
                  ? "latency-warn"
                  : "latency-bad";
          }
        } catch (_) {
          // Session may have disconnected — clear indicator.
          const el = document.getElementById("status-latency");
          if (el) {
            el.textContent = "";
            el.className = "";
          }
          this._stopLatencyPing();
        }
      };

      doPing();
      this._latencyTimer = setInterval(doPing, 10000);
    }

    _stopLatencyPing() {
      if (this._latencyTimer) {
        clearInterval(this._latencyTimer);
        this._latencyTimer = null;
      }
      this._latencySessionID = null;
    }

    async checkForUpdates(silent) {
      if (!window.go || !window.go.api || !window.go.api.Service) {
        return;
      }

      try {
        const info = await window.go.api.Service.CheckForUpdates();
        this.setUpdateInfo(info);
        if (!silent && info && info.available) {
          this.notifications.info(
            `Update available: ${info.latestVersion || "new version"}`,
          );
        }
      } catch (err) {
        if (!silent) {
          this.notifications.error("Update check failed: " + err);
        }
      }
    }

    setUpdateInfo(info) {
      this.updateInfo = info || null;
      this.updateStatusBar();
    }

    // ── Internal helpers ─────────────────────────────────────────────

    /**
     * Applies loaded preferences to the UI.
     * @private
     */
    _applyPreferences() {
      const prefs = this.state.preferences;

      // Sidebar width (drives the grid column width).
      // Use the CSS variable so the workspace never sits underneath the sidebar.
      const root = document.documentElement;
      const sidebar = document.getElementById("sidebar");
      const parsedSidebarWidth = Number(prefs.sidebarWidth);
      if (Number.isFinite(parsedSidebarWidth) && parsedSidebarWidth > 0) {
        const clampedWidth = Math.max(
          180,
          Math.min(500, Math.round(parsedSidebarWidth)),
        );
        root.style.setProperty("--sidebar-width", `${clampedWidth}px`);
      } else {
        root.style.removeProperty("--sidebar-width");
      }
      if (sidebar) {
        sidebar.style.width = "";
      }

      // Status bar visibility
      const statusBar = document.getElementById("status-bar");
      if (statusBar) {
        statusBar.style.display =
          prefs.showStatusBar === false ? "none" : "flex";
      }

      // Refit any visible terminal after geometry changes.
      requestAnimationFrame(() => {
        this.terminals.fitActive();
        this.splitPanes.refitAll();
      });
    }

    /**
     * Determines whether an error is a common authentication or host key issue.
     * @param {string} message
     * @returns {Object|null}
     * @private
     */
    _classifySessionError(message) {
      const msg = (message || "").toLowerCase();

      const authPatterns = [
        "unable to authenticate",
        "authentication failed",
        "permission denied",
        "no supported methods",
      ];
      const hostKeyPatterns = ["host key", "fingerprint"];

      if (authPatterns.some((p) => msg.includes(p))) {
        return {
          type: "auth",
          title: "Authentication Failed",
          message:
            "The server rejected the provided credentials. Check username, password, keys, and auth methods.",
          actions: ["edit", "retry"],
        };
      }

      if (hostKeyPatterns.some((p) => msg.includes(p))) {
        return {
          type: "hostkey",
          title: "Host Key Verification Failed",
          message:
            "The server's host key could not be verified. Manage known hosts or update the profile's policy.",
          actions: ["knownhosts", "edit", "retry"],
        };
      }

      return null;
    }

    /**
     * Shows a guided action modal for common session errors.
     * @param {string} sessionId
     * @param {string} message
     * @private
     */
    _handleSessionError(sessionId, message) {
      const info = this._classifySessionError(message);
      if (!info) return;

      const session = this.state.getSession(sessionId);
      const label = session?.tabName || session?.host || "Session";
      const body = `${info.message}\n\n${message}`;
      this._showGuidedErrorModal(
        info.title,
        label,
        body,
        (action) => {
          if (action === "edit" && session?.profileId) {
            this.profileEditor.openEdit(session.profileId);
          }
          if (action === "retry") {
            this.tabs._reconnect(sessionId);
          }
          if (action === "knownhosts") {
            this.settings.openTab("knownhosts");
          }
        },
        info.actions,
      );
    }

    /**
     * Builds and displays a modal with guided action buttons.
     * @param {string} title
     * @param {string} label
     * @param {string} message
     * @param {Function} onAction
     * @param {Array<string>} actions
     * @private
     */
    _showGuidedErrorModal(title, label, message, onAction, actions) {
      const createElement = window.NexusTerminal.createElement;
      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-sm");
      const safeLabel = window.NexusTerminal.escapeHtml(label || "Session");

      modal.innerHTML = `
        <div class="modal-header">
          <h3 class="modal-title">${title}</h3>
          <button class="btn btn-icon btn-xs modal-close">&times;</button>
        </div>
        <div class="modal-body">
          <p style="font-size:var(--text-sm);color:var(--text-secondary)">
            <strong>${safeLabel}</strong>
          </p>
          <p style="font-size:var(--text-sm);color:var(--text-secondary);white-space:pre-line;">
            ${window.NexusTerminal.escapeHtml(message)}
          </p>
        </div>
        <div class="modal-footer"></div>
      `;

      const footer = modal.querySelector(".modal-footer");
      const addActionBtn = (label, action, primary) => {
        const btn = createElement(
          "button",
          `btn btn-sm${primary ? " btn-primary" : ""}`,
          {
            textContent: label,
          },
        );
        btn.addEventListener("click", () => close(action));
        footer.appendChild(btn);
      };

      addActionBtn("Close", null, false);
      if (actions.includes("knownhosts"))
        addActionBtn("Known Hosts", "knownhosts", false);
      if (actions.includes("edit")) addActionBtn("Edit Profile", "edit", false);
      if (actions.includes("retry")) addActionBtn("Retry", "retry", true);

      const close = (action) => {
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(() => backdrop.remove(), 200);
        if (action) onAction(action);
      };

      modal
        .querySelector(".modal-close")
        .addEventListener("click", () => close(null));
      backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) close(null);
      });

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);
      requestAnimationFrame(() => {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
      });
    }

    /**
     * Opens an inline search bar for the active terminal.
     * @private
     */
    _openTerminalSearch() {
      const sessionId = this.state.activeSessionId;
      if (!sessionId) return;

      const panel = document.getElementById(`panel-${sessionId}`);
      if (!panel) return;

      // If search bar already exists, just focus the input
      const existing = panel.querySelector(".terminal-search");
      if (existing) {
        const input = existing.querySelector("input");
        if (input) input.focus();
        return;
      }

      const bar = document.createElement("div");
      bar.className = "terminal-search";

      const input = document.createElement("input");
      input.type = "text";
      input.placeholder = "Find in terminal…";
      input.className = "terminal-search-input";

      const count = document.createElement("span");
      count.className = "terminal-search-count";
      count.textContent = "";

      const prevBtn = document.createElement("button");
      prevBtn.className = "btn btn-xs btn-ghost";
      prevBtn.textContent = "▲";
      prevBtn.title = "Previous match (Shift+Enter)";

      const nextBtn = document.createElement("button");
      nextBtn.className = "btn btn-xs btn-ghost";
      nextBtn.textContent = "▼";
      nextBtn.title = "Next match (Enter)";

      const caseSensBtn = document.createElement("button");
      caseSensBtn.className = "btn btn-xs btn-ghost terminal-search-toggle";
      caseSensBtn.textContent = "Aa";
      caseSensBtn.title = "Toggle case sensitivity";
      let caseSensitive = false;

      const regexBtn = document.createElement("button");
      regexBtn.className = "btn btn-xs btn-ghost terminal-search-toggle";
      regexBtn.textContent = ".*";
      regexBtn.title = "Toggle regular expression";
      let useRegex = false;

      const closeBtn = document.createElement("button");
      closeBtn.className = "btn btn-xs btn-ghost";
      closeBtn.textContent = "✕";
      closeBtn.title = "Close (Escape)";

      bar.appendChild(input);
      bar.appendChild(count);
      bar.appendChild(prevBtn);
      bar.appendChild(nextBtn);
      bar.appendChild(caseSensBtn);
      bar.appendChild(regexBtn);
      bar.appendChild(closeBtn);

      const searchOpts = () => ({ caseSensitive, regex: useRegex });

      let matchIndex = 0;
      let totalMatches = 0;

      const updateCount = (found) => {
        if (!input.value) {
          count.textContent = "";
          matchIndex = 0;
          totalMatches = 0;
          return;
        }
        if (found) {
          matchIndex++;
          // Count total matches by searching the full buffer
          const entry = this.terminals.terminals.get(sessionId);
          if (entry) {
            const buffer = entry.term.buffer.active;
            let total = 0;
            const query = input.value;
            for (let i = 0; i < buffer.length; i++) {
              const line = buffer.getLine(i);
              if (!line) continue;
              const text = line.translateToString(true);
              if (useRegex) {
                try {
                  const re = new RegExp(query, caseSensitive ? "g" : "gi");
                  const m = text.match(re);
                  if (m) total += m.length;
                } catch (_) {
                  /* invalid regex */
                }
              } else {
                const searchText = caseSensitive ? text : text.toLowerCase();
                const searchQuery = caseSensitive ? query : query.toLowerCase();
                let pos = 0;
                while ((pos = searchText.indexOf(searchQuery, pos)) !== -1) {
                  total++;
                  pos += searchQuery.length;
                }
              }
            }
            totalMatches = total;
            if (matchIndex > totalMatches) matchIndex = 1;
            count.textContent = matchIndex + " of " + totalMatches;
          } else {
            count.textContent = "Match found";
          }
        } else {
          matchIndex = 0;
          totalMatches = 0;
          count.textContent = "No matches";
        }
        count.style.color = found ? "" : "var(--error)";
      };

      const doSearch = () => {
        const found = this.terminals.search(
          sessionId,
          input.value,
          searchOpts(),
        );
        updateCount(found);
      };
      const doPrev = () => {
        matchIndex = Math.max(0, matchIndex - 2);
        const found = this.terminals.searchPrevious(
          sessionId,
          input.value,
          searchOpts(),
        );
        updateCount(found);
      };

      const closeSearch = () => {
        // Clear search decorations
        const entry = this.terminals.terminals.get(sessionId);
        if (entry && entry.searchAddon) {
          entry.searchAddon.clearDecorations();
        }
        bar.classList.remove("is-visible");
        setTimeout(() => bar.remove(), 200);
        this.terminals.focusTerminal(sessionId);
      };

      input.addEventListener("input", doSearch);
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          e.shiftKey ? doPrev() : doSearch();
        }
        if (e.key === "Escape") {
          e.preventDefault();
          closeSearch();
        }
      });

      nextBtn.addEventListener("click", doSearch);
      prevBtn.addEventListener("click", doPrev);
      closeBtn.addEventListener("click", closeSearch);

      caseSensBtn.addEventListener("click", () => {
        caseSensitive = !caseSensitive;
        caseSensBtn.classList.toggle("is-active", caseSensitive);
        doSearch();
      });

      regexBtn.addEventListener("click", () => {
        useRegex = !useRegex;
        regexBtn.classList.toggle("is-active", useRegex);
        doSearch();
      });

      panel.appendChild(bar);
      // Trigger the entrance animation
      requestAnimationFrame(() => {
        bar.classList.add("is-visible");
        input.focus();
      });
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
        el.addEventListener("click", (e) => {
          try {
            handler(e);
          } catch (err) {
            console.error("Click handler error for", id, err);
          }
        });
      }
    }

    /**
     * Focuses the currently visible xterm instance if one is active.
     * @private
     */
    _focusVisibleTerminal() {
      const sessionId = this.state.activeSessionId;
      if (!sessionId) return;
      this.terminals.focusTerminal(sessionId);
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
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">${title}</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label class="form-label">${label}</label>
                        <input class="form-input" type="text" placeholder="${placeholder || ""}" autofocus />
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm prompt-cancel">Cancel</button>
                    <button class="btn btn-sm btn-primary prompt-ok">Connect</button>
                </div>
            `;
        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => backdrop.classList.add("is-visible"));

        const input = modal.querySelector("input");
        const close = (value) => {
          backdrop.classList.remove("is-visible");
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
          .querySelector(".prompt-ok")
          .addEventListener("click", () => close(input.value));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(null);
        });
        input.addEventListener("keydown", (e) => {
          if (e.key === "Enter") close(input.value);
          if (e.key === "Escape") close(null);
        });

        setTimeout(() => input.focus(), 50);
      });
    }
  }

  // ── Bootstrap ────────────────────────────────────────────────────────

  document.addEventListener("DOMContentLoaded", () => {
    try {
      const app = new App();
      app.init().catch((err) => {
        console.error("App initialisation failed:", err);
      });
    } catch (err) {
      console.error("App constructor failed:", err);
    }
  });
})();
