(function () {
  /**
   * Nexus Terminal — snippet / command library.
   * Provides a panel for managing and sending reusable commands to active sessions.
   */

  const { createElement, escapeHtml, dismissContextMenus } =
    window.NexusTerminal;

  class SnippetLibrary {
    /**
     * @param {App} app
     */
    constructor(app) {
      this.app = app;
      /** @type {Array<Object>} */
      this.snippets = [];
      this.searchQuery = "";
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Opens the snippet library modal.
     */
    async open() {
      await this._loadSnippets();
      this._showModal();
    }

    /**
     * Sends a snippet command to the active session.
     * Supports {{variable}} placeholders that prompt the user for values.
     * Built-in variables: {{hostname}}, {{date}}, {{time}}, {{timestamp}}.
     * @param {string} command
     */
    sendToActiveSession(command) {
      const sessionId = this.app.state.activeSessionId;
      if (!sessionId) {
        this.app.notifications.error("No active session");
        return;
      }
      const session = this.app.state.getSession(sessionId);
      if (!session || session.state !== "connected") {
        this.app.notifications.error("Session is not connected");
        return;
      }

      // Check for template variables
      const varPattern = /\{\{([^}]+)\}\}/g;
      const matches = [];
      let match;
      while ((match = varPattern.exec(command)) !== null) {
        matches.push(match[1].trim());
      }

      // Resolve built-in variables first
      const builtins = {
        hostname: session.host || "unknown",
        date: new Date().toISOString().split("T")[0],
        time: new Date().toTimeString().split(" ")[0],
        timestamp: Math.floor(Date.now() / 1000).toString(),
      };

      let resolved = command;
      const userVars = [];
      for (const varName of matches) {
        const lower = varName.toLowerCase();
        if (builtins[lower] !== undefined) {
          resolved = resolved.replace("{{" + varName + "}}", builtins[lower]);
        } else if (!userVars.includes(varName)) {
          userVars.push(varName);
        }
      }

      if (userVars.length === 0) {
        this._sendCommand(sessionId, resolved);
        return;
      }

      // Prompt for user-defined variables
      this._promptVariables(userVars, (values) => {
        let final = resolved;
        for (let i = 0; i < userVars.length; i++) {
          final = final.split("{{" + userVars[i] + "}}").join(values[i] || "");
        }
        this._sendCommand(sessionId, final);
      });
    }

    /** @private */
    _sendCommand(sessionId, command) {
      try {
        const b64 = btoa(command + "\n");
        window.go.api.Service.WriteSession(sessionId, b64);
        this.app.notifications.info("Snippet sent");
      } catch (err) {
        this.app.notifications.error("Failed to send snippet: " + err);
      }
    }

    /** @private */
    _promptVariables(varNames, onConfirm) {
      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-sm");

      let inputsHtml = "";
      for (const v of varNames) {
        inputsHtml += `
                <div class="form-group" style="margin-bottom:var(--space-2);">
                    <label class="form-label">${escapeHtml(v)}</label>
                    <input type="text" class="form-input snippet-var-input" data-var="${escapeHtml(v)}" placeholder="Enter value for ${escapeHtml(v)}" />
                </div>
            `;
      }

      modal.innerHTML = `
            <div class="modal-header">
                <h3 class="modal-title">Snippet Variables</h3>
                <button class="btn btn-icon btn-xs modal-close">&times;</button>
            </div>
            <div class="modal-body">${inputsHtml}</div>
            <div class="modal-footer">
                <button class="btn btn-sm btn-ghost snippet-var-cancel">Cancel</button>
                <button class="btn btn-sm btn-primary snippet-var-send">Send</button>
            </div>
        `;

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);
      requestAnimationFrame(() => {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
        const firstInput = modal.querySelector(".snippet-var-input");
        if (firstInput) firstInput.focus();
      });

      const close = () => {
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(() => backdrop.remove(), 200);
      };

      modal.querySelector(".modal-close").addEventListener("click", close);
      modal
        .querySelector(".snippet-var-cancel")
        .addEventListener("click", close);
      backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) close();
      });

      modal.querySelector(".snippet-var-send").addEventListener("click", () => {
        const inputs = modal.querySelectorAll(".snippet-var-input");
        const values = Array.from(inputs).map((inp) => inp.value);
        close();
        onConfirm(values);
      });

      // Enter to send
      modal.addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          modal.querySelector(".snippet-var-send").click();
        }
        if (e.key === "Escape") close();
      });
    }

    // ── Data ─────────────────────────────────────────────────────────

    /** @private */
    async _loadSnippets() {
      try {
        this.snippets = (await window.go.api.Service.ListSnippets()) || [];
      } catch (err) {
        this.snippets = [];
        console.error("Failed to load snippets:", err);
      }
    }

    // ── Modal ────────────────────────────────────────────────────────

    /** @private */
    _showModal() {
      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-lg");

      modal.innerHTML = `
            <div class="modal-header">
                <h3 class="modal-title">Command Library</h3>
                <button class="btn btn-icon btn-xs modal-close">&times;</button>
            </div>
            <div class="modal-body snippet-body" style="display:flex;gap:var(--space-3);height:400px;">
                <div class="snippet-list-panel" style="flex:1;display:flex;flex-direction:column;gap:var(--space-2);overflow:hidden;">
                    <div style="display:flex;gap:var(--space-2);">
                        <input type="text" class="form-input snippet-search" placeholder="Search snippets…" style="flex:1;" />
                        <button class="btn btn-sm btn-primary snippet-add-btn">+ New</button>
                    </div>
                    <div class="snippet-list" style="flex:1;overflow-y:auto;"></div>
                </div>
                <div class="snippet-detail-panel" style="flex:1;display:flex;flex-direction:column;gap:var(--space-2);overflow:hidden;">
                    <div class="snippet-detail-empty" style="display:flex;align-items:center;justify-content:center;flex:1;color:var(--text-tertiary);font-size:var(--text-sm);">
                        Select a snippet or create a new one
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

      // Search
      const searchInput = modal.querySelector(".snippet-search");
      searchInput.addEventListener("input", () => {
        this.searchQuery = searchInput.value.trim().toLowerCase();
        this._renderList(modal, close);
      });

      // New snippet button
      modal.querySelector(".snippet-add-btn").addEventListener("click", () => {
        this._showEditor(modal, null, close);
      });

      this._renderList(modal, close);
    }

    /** @private */
    _renderList(modal, closeFn) {
      const listEl = modal.querySelector(".snippet-list");
      if (!listEl) return;
      listEl.innerHTML = "";

      const filtered = this.snippets.filter((sn) => {
        if (!this.searchQuery) return true;
        return (
          sn.name.toLowerCase().includes(this.searchQuery) ||
          sn.command.toLowerCase().includes(this.searchQuery) ||
          (sn.description || "").toLowerCase().includes(this.searchQuery)
        );
      });

      if (filtered.length === 0) {
        listEl.innerHTML =
          '<div style="color:var(--text-tertiary);font-size:var(--text-sm);padding:var(--space-3);text-align:center;">No snippets found</div>';
        return;
      }

      for (const sn of filtered) {
        const item = createElement("div", "snippet-item", {
          dataset: { id: sn.id },
        });
        item.style.cssText =
          "padding:var(--space-2);border-radius:var(--radius-md);cursor:pointer;border:1px solid var(--border-subtle);";
        item.innerHTML = `
                <div style="font-weight:var(--weight-medium);font-size:var(--text-sm);color:var(--text-primary);">${escapeHtml(sn.name)}</div>
                <div style="font-family:var(--font-mono);font-size:var(--text-xs);color:var(--text-secondary);margin-top:2px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">${escapeHtml(sn.command)}</div>
            `;
        item.addEventListener("click", () =>
          this._showDetail(modal, sn, closeFn),
        );
        item.addEventListener("mousedown", (e) => e.preventDefault());
        item.addEventListener("mouseenter", () => {
          item.style.background = "var(--bg-hover)";
        });
        item.addEventListener("mouseleave", () => {
          item.style.background = "";
        });
        listEl.appendChild(item);
      }
    }

    /** @private */
    _showDetail(modal, snippet, closeFn) {
      const detailPanel = modal.querySelector(".snippet-detail-panel");
      if (!detailPanel) return;
      detailPanel.innerHTML = "";

      const nameEl = createElement("div", "", {
        innerHTML: `<strong style="font-size:var(--text-base);">${escapeHtml(snippet.name)}</strong>`,
      });
      detailPanel.appendChild(nameEl);

      if (snippet.description) {
        const descEl = createElement("div", "", {
          textContent: snippet.description,
        });
        descEl.style.cssText =
          "font-size:var(--text-sm);color:var(--text-secondary);";
        detailPanel.appendChild(descEl);
      }

      const cmdEl = createElement("pre", "", {
        textContent: snippet.command,
      });
      cmdEl.style.cssText =
        "flex:1;font-family:var(--font-mono);font-size:var(--text-sm);background:var(--bg-base);padding:var(--space-3);border-radius:var(--radius-md);overflow:auto;color:var(--text-primary);margin:0;white-space:pre-wrap;word-break:break-all;";
      detailPanel.appendChild(cmdEl);

      // Action buttons
      const actions = createElement("div", "", {});
      actions.style.cssText = "display:flex;gap:var(--space-2);";

      const sendBtn = createElement("button", "btn btn-sm btn-primary", {
        textContent: "Send to Session",
      });
      sendBtn.addEventListener("click", () => {
        this.sendToActiveSession(snippet.command);
      });

      const editBtn = createElement("button", "btn btn-sm btn-secondary", {
        textContent: "Edit",
      });
      editBtn.addEventListener("click", () => {
        this._showEditor(modal, snippet, closeFn);
      });

      const deleteBtn = createElement("button", "btn btn-sm btn-danger", {
        textContent: "Delete",
      });
      deleteBtn.addEventListener("click", async () => {
        try {
          await window.go.api.Service.DeleteSnippet(snippet.id);
          this.snippets = this.snippets.filter((s) => s.id !== snippet.id);
          this._renderList(modal, closeFn);
          detailPanel.innerHTML =
            '<div class="snippet-detail-empty" style="display:flex;align-items:center;justify-content:center;flex:1;color:var(--text-tertiary);font-size:var(--text-sm);">Snippet deleted</div>';
        } catch (err) {
          this.app.notifications.error("Failed to delete: " + err);
        }
      });

      actions.appendChild(sendBtn);
      actions.appendChild(editBtn);
      actions.appendChild(deleteBtn);
      detailPanel.appendChild(actions);
    }

    /** @private */
    _showEditor(modal, snippet, closeFn) {
      const detailPanel = modal.querySelector(".snippet-detail-panel");
      if (!detailPanel) return;
      detailPanel.innerHTML = "";

      const isNew = !snippet;
      const title = isNew ? "New Snippet" : "Edit Snippet";

      detailPanel.innerHTML = `
            <div style="font-weight:var(--weight-medium);font-size:var(--text-sm);">${title}</div>
            <div class="form-group" style="margin-bottom:var(--space-2);">
                <label class="form-label">Name</label>
                <input type="text" class="form-input snippet-ed-name" value="${isNew ? "" : escapeHtml(snippet.name)}" />
            </div>
            <div class="form-group" style="margin-bottom:var(--space-2);">
                <label class="form-label">Description</label>
                <input type="text" class="form-input snippet-ed-desc" value="${isNew ? "" : escapeHtml(snippet.description || "")}" />
            </div>
            <div class="form-group" style="flex:1;display:flex;flex-direction:column;">
                <label class="form-label">Command</label>
                <textarea class="form-input snippet-ed-cmd" style="flex:1;font-family:var(--font-mono);font-size:var(--text-sm);resize:none;">${isNew ? "" : escapeHtml(snippet.command)}</textarea>
            </div>
            <div class="form-group" style="margin-bottom:var(--space-2);">
                <label class="form-label">Folder</label>
                <input type="text" class="form-input snippet-ed-folder" value="${isNew ? "" : escapeHtml(snippet.folder || "")}" placeholder="e.g. Networking, Debugging" />
            </div>
            <div style="display:flex;gap:var(--space-2);">
                <button class="btn btn-sm btn-primary snippet-save-btn">Save</button>
                <button class="btn btn-sm snippet-cancel-btn">Cancel</button>
            </div>
        `;

      detailPanel
        .querySelector(".snippet-save-btn")
        .addEventListener("click", async () => {
          const name = detailPanel
            .querySelector(".snippet-ed-name")
            .value.trim();
          const command = detailPanel.querySelector(".snippet-ed-cmd").value;
          const description = detailPanel
            .querySelector(".snippet-ed-desc")
            .value.trim();
          const folder = detailPanel
            .querySelector(".snippet-ed-folder")
            .value.trim();

          if (!name) {
            this.app.notifications.error("Name is required");
            return;
          }
          if (!command) {
            this.app.notifications.error("Command is required");
            return;
          }

          try {
            const data = {
              id: isNew ? "" : snippet.id,
              name,
              command,
              description,
              folder,
              tags: isNew ? [] : snippet.tags || [],
            };

            let saved;
            if (isNew) {
              saved = await window.go.api.Service.CreateSnippet(data);
              this.snippets.push(saved);
            } else {
              saved = await window.go.api.Service.UpdateSnippet(data);
              const idx = this.snippets.findIndex((s) => s.id === saved.id);
              if (idx !== -1) this.snippets[idx] = saved;
            }

            this._renderList(modal, closeFn);
            this._showDetail(modal, saved, closeFn);
            this.app.notifications.info(
              isNew ? "Snippet created" : "Snippet updated",
            );
          } catch (err) {
            this.app.notifications.error("Failed to save snippet: " + err);
          }
        });

      detailPanel
        .querySelector(".snippet-cancel-btn")
        .addEventListener("click", () => {
          if (snippet) {
            this._showDetail(modal, snippet, closeFn);
          } else {
            detailPanel.innerHTML =
              '<div class="snippet-detail-empty" style="display:flex;align-items:center;justify-content:center;flex:1;color:var(--text-tertiary);font-size:var(--text-sm);">Select a snippet or create a new one</div>';
          }
        });
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.SnippetLibrary = SnippetLibrary;
})();
