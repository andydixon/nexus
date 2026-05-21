(function () {
  /**
   * Nexus Terminal — VNC viewer.
   * Renders a noVNC canvas inside a session panel, connecting to the local
   * WebSocket proxy started by the Go backend.
   */

  const { createElement, $ } = window.NexusTerminal;

  class VNCViewer {
    constructor() {
      /** @type {Map<string, VNCViewerInstance>} sessionID → viewer */
      this._viewers = new Map();
    }

    /**
     * Creates a VNC viewer panel for a session inside the given container.
     * @param {string} sessionID
     * @param {HTMLElement} container - The .session-panel element.
     * @param {object} profile - The profile object with VNC config.
     */
    createViewer(sessionID, container, profile) {
      if (this._viewers.has(sessionID)) return;
      if (!container) return;

      const viewer = new VNCViewerInstance(sessionID, container, profile);
      this._viewers.set(sessionID, viewer);
    }

    /**
     * Connects the viewer to the WebSocket URL from the backend.
     * @param {string} sessionID
     * @param {string} wsURL
     */
    connect(sessionID, wsURL) {
      const viewer = this._viewers.get(sessionID);
      if (viewer) {
        viewer.connect(wsURL);
      }
    }

    /**
     * Destroys a viewer for the given session.
     * @param {string} sessionID
     */
    destroyViewer(sessionID) {
      const viewer = this._viewers.get(sessionID);
      if (viewer) {
        viewer.destroy();
        this._viewers.delete(sessionID);
      }
    }

    /**
     * Resizes the VNC display for the given session.
     * @param {string} sessionID
     */
    resize(sessionID) {
      const viewer = this._viewers.get(sessionID);
      if (viewer) {
        viewer.resize();
      }
    }
  }

  class VNCViewerInstance {
    /**
     * @param {string} sessionID
     * @param {HTMLElement} container
     * @param {object} profile
     */
    constructor(sessionID, container, profile) {
      this.sessionID = sessionID;
      this.container = container;
      this.profile = profile;
      this.rfb = null;

      this._buildUI();
    }

    _buildUI() {
      this.container.innerHTML = "";
      this.container.classList.add("vnc-viewer-panel");

      // Toolbar
      this.toolbar = createElement("div", "vnc-toolbar");

      const ctrlAltDel = createElement("button", "btn btn-sm btn-vnc-action", {
        textContent: "Ctrl+Alt+Del",
        title: "Send Ctrl+Alt+Del",
      });
      ctrlAltDel.addEventListener("click", () => this._sendCtrlAltDel());

      const clipboardBtn = createElement(
        "button",
        "btn btn-sm btn-vnc-action",
        {
          textContent: "Clipboard",
          title: "Sync clipboard",
        },
      );
      clipboardBtn.addEventListener("click", () => this._syncClipboard());

      const fullscreenBtn = createElement(
        "button",
        "btn btn-sm btn-vnc-action",
        {
          textContent: "Fullscreen",
          title: "Toggle fullscreen",
        },
      );
      fullscreenBtn.addEventListener("click", () => this._toggleFullscreen());

      this.statusEl = createElement("span", "vnc-status", {
        textContent: "Connecting…",
      });

      this.toolbar.appendChild(this.statusEl);
      this.toolbar.appendChild(ctrlAltDel);
      this.toolbar.appendChild(clipboardBtn);
      this.toolbar.appendChild(fullscreenBtn);

      // VNC display container
      this.displayEl = createElement("div", "vnc-display");

      this.container.appendChild(this.toolbar);
      this.container.appendChild(this.displayEl);
    }

    /**
     * Connects to the VNC WebSocket proxy.
     * @param {string} wsURL - WebSocket URL (ws://127.0.0.1:<port>)
     */
    connect(wsURL) {
      if (!window.noVNC || !window.noVNC.RFB) {
        this.statusEl.textContent = "Error: noVNC not loaded";
        this.statusEl.classList.add("vnc-status-error");
        return;
      }

      try {
        const vncConfig = this.profile.vnc || {};

        this.rfb = new window.noVNC.RFB(this.displayEl, wsURL, {
          shared: vncConfig.sharedSession !== false,
          credentials: {},
        });

        this.rfb.viewOnly = !!vncConfig.viewOnly;
        this.rfb.scaleViewport = vncConfig.scaleToFit !== false;
        this.rfb.resizeSession = false;
        this.rfb.clipViewport = false;
        this.rfb.qualityLevel = vncConfig.quality || 6;
        this.rfb.compressionLevel = 2;

        // Event handlers
        this.rfb.addEventListener("connect", () => {
          this.statusEl.textContent = "Connected";
          this.statusEl.classList.remove("vnc-status-error");
          this.statusEl.classList.add("vnc-status-connected");
        });

        this.rfb.addEventListener("disconnect", (ev) => {
          const clean = ev.detail.clean;
          this.statusEl.textContent = clean
            ? "Disconnected"
            : "Connection lost";
          this.statusEl.classList.remove("vnc-status-connected");
          if (!clean) {
            this.statusEl.classList.add("vnc-status-error");
          }
        });

        this.rfb.addEventListener("credentialsrequired", () => {
          this.statusEl.textContent = "Credentials required";
          // TODO: prompt for VNC password via modal
          this._promptCredentials();
        });

        this.rfb.addEventListener("desktopname", (ev) => {
          this.statusEl.textContent = "Connected — " + ev.detail.name;
        });
      } catch (err) {
        this.statusEl.textContent = "Error: " + err.message;
        this.statusEl.classList.add("vnc-status-error");
      }
    }

    _sendCtrlAltDel() {
      if (this.rfb) {
        this.rfb.sendCtrlAltDel();
      }
    }

    async _syncClipboard() {
      if (!this.rfb) return;
      try {
        const text = await navigator.clipboard.readText();
        if (text) {
          this.rfb.clipboardPasteFrom(text);
        }
      } catch (_) {
        // Clipboard API may not be available in WKWebView
      }
    }

    _toggleFullscreen() {
      if (!document.fullscreenElement) {
        this.container.requestFullscreen().catch(() => {});
      } else {
        document.exitFullscreen().catch(() => {});
      }
    }

    _promptCredentials() {
      // Build a simple modal for VNC password
      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-sm");
      modal.innerHTML = `
        <div class="modal-header">
          <h3 class="modal-title">VNC Password</h3>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">Password</label>
            <input type="password" class="form-input" id="vnc-password-input" autocomplete="off">
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" id="vnc-password-cancel">Cancel</button>
          <button class="btn btn-primary" id="vnc-password-submit">Connect</button>
        </div>
      `;

      document.body.appendChild(backdrop);
      document.body.appendChild(modal);
      requestAnimationFrame(() => {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
      });

      const input = modal.querySelector("#vnc-password-input");
      const submit = modal.querySelector("#vnc-password-submit");
      const cancel = modal.querySelector("#vnc-password-cancel");

      const close = () => {
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(() => {
          backdrop.remove();
          modal.remove();
        }, 200);
      };

      submit.addEventListener("click", () => {
        const pw = input.value;
        close();
        if (this.rfb && pw) {
          this.rfb.sendCredentials({ password: pw });
        }
      });

      cancel.addEventListener("click", () => {
        close();
      });

      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") submit.click();
        if (e.key === "Escape") cancel.click();
      });

      setTimeout(() => input.focus(), 50);
    }

    resize() {
      // noVNC handles resizing internally via ResizeObserver on its canvas,
      // but we can trigger a re-scale if needed.
      if (this.rfb && this.rfb.scaleViewport) {
        this.rfb.scaleViewport = true;
      }
    }

    destroy() {
      if (this.rfb) {
        this.rfb.disconnect();
        this.rfb = null;
      }
      this.container.innerHTML = "";
      this.container.classList.remove("vnc-viewer-panel");
    }
  }

  // Export to global namespace.
  window.NexusTerminal.VNCViewer = new VNCViewer();
})();
