(function () {
  /**
   * Nexus Terminal — Trigger Rules Engine.
   * Monitors terminal output and fires actions (highlight, notify, sound)
   * when patterns match. Rules are stored in localStorage.
   */

  const { createElement } = window.NexusTerminal;

  const STORAGE_KEY = "nexus:trigger-rules";

  class TriggerEngine {
    constructor(app) {
      this.app = app;
      this._rules = this._load();
      this._backdrop = null;
      // Buffer per session to accumulate partial lines
      this._buffers = new Map();
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Checks incoming terminal data against all enabled rules.
     * Called from the session:data event handler.
     * @param {string} sessionId
     * @param {string} text - decoded terminal text
     */
    check(sessionId, text) {
      if (this._rules.length === 0) return;

      // Accumulate text and check line by line
      const buf = (this._buffers.get(sessionId) || "") + text;
      const lines = buf.split("\n");
      // Keep the last incomplete line in the buffer
      this._buffers.set(sessionId, lines.pop());

      for (const line of lines) {
        this._checkLine(sessionId, line);
      }
    }

    /**
     * Opens the trigger rules management panel.
     */
    open() {
      if (this._backdrop) return;
      this._render();
    }

    /**
     * Closes the trigger rules panel.
     */
    close() {
      if (this._backdrop) {
        this._backdrop.classList.remove("is-visible");
        const modal = this._backdrop.querySelector(".modal");
        if (modal) modal.classList.remove("is-visible");
        setTimeout(() => {
          if (this._backdrop) {
            this._backdrop.remove();
            this._backdrop = null;
          }
        }, 200);
      }
    }

    /**
     * Cleans up session buffer on disconnect.
     * @param {string} sessionId
     */
    cleanup(sessionId) {
      this._buffers.delete(sessionId);
    }

    // ── Rule matching ────────────────────────────────────────────────

    _checkLine(sessionId, line) {
      // Strip ANSI escape sequences for matching
      const clean = line.replace(/\x1b\[[0-9;]*[a-zA-Z]/g, "");

      for (const rule of this._rules) {
        if (!rule.enabled) continue;

        let matched = false;
        if (rule.isRegex) {
          try {
            const re = new RegExp(rule.pattern, rule.caseSensitive ? "" : "i");
            matched = re.test(clean);
          } catch (_) {
            // Invalid regex — skip
          }
        } else {
          const haystack = rule.caseSensitive ? clean : clean.toLowerCase();
          const needle = rule.caseSensitive
            ? rule.pattern
            : rule.pattern.toLowerCase();
          matched = haystack.includes(needle);
        }

        if (!matched) continue;

        // Fire actions
        if (rule.action === "notify" || rule.action === "both") {
          const session = this.app.state.getSession(sessionId);
          const label = session
            ? session.tabName || session.profileName
            : sessionId;
          this.app.notifications.info(
            `[${label}] Trigger: ${rule.name || rule.pattern}`,
          );
        }

        if (rule.action === "highlight" || rule.action === "both") {
          // Flash the tab briefly
          const tabEl = document.querySelector(
            `.tab[data-session-id="${sessionId}"]`,
          );
          if (tabEl) {
            tabEl.classList.add("tab-trigger-flash");
            setTimeout(() => tabEl.classList.remove("tab-trigger-flash"), 2000);
          }
        }

        if (rule.action === "sound") {
          this._playBeep();
        }
      }
    }

    _playBeep() {
      try {
        const ctx = new (window.AudioContext || window.webkitAudioContext)();
        const osc = ctx.createOscillator();
        const gain = ctx.createGain();
        osc.connect(gain);
        gain.connect(ctx.destination);
        osc.frequency.value = 880;
        gain.gain.value = 0.1;
        osc.start();
        osc.stop(ctx.currentTime + 0.15);
      } catch (_) {
        // Audio not available — silent
      }
    }

    // ── Storage ──────────────────────────────────────────────────────

    _load() {
      try {
        const raw = localStorage.getItem(STORAGE_KEY);
        return raw ? JSON.parse(raw) : [];
      } catch (_) {
        return [];
      }
    }

    _save() {
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(this._rules));
      } catch (_) {
        // localStorage full or unavailable
      }
    }

    // ── UI ───────────────────────────────────────────────────────────

    _render() {
      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-lg");

      modal.innerHTML = `
        <div class="modal-header">
          <h3 class="modal-title">Trigger Rules</h3>
          <button class="btn btn-icon btn-xs modal-close">&times;</button>
        </div>
        <div class="modal-body">
          <p style="font-size:var(--text-xs);color:var(--text-tertiary);margin-bottom:var(--space-3)">
            Rules match against terminal output and fire actions when a pattern is detected.
          </p>
          <div class="trigger-toolbar">
            <button class="btn btn-sm btn-accent trigger-add-btn">Add Rule</button>
          </div>
          <div class="trigger-list"></div>
        </div>
      `;

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);
      this._backdrop = backdrop;

      requestAnimationFrame(() => {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
      });

      modal
        .querySelector(".modal-close")
        .addEventListener("click", () => this.close());
      backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) this.close();
      });
      modal
        .querySelector(".trigger-add-btn")
        .addEventListener("click", () => this._addRule());

      this._renderList();
    }

    _renderList() {
      const list = this._backdrop.querySelector(".trigger-list");
      if (!list) return;
      list.innerHTML = "";

      if (this._rules.length === 0) {
        list.innerHTML =
          '<div class="trigger-empty">No trigger rules defined</div>';
        return;
      }

      for (let i = 0; i < this._rules.length; i++) {
        list.appendChild(this._renderRuleRow(this._rules[i], i));
      }
    }

    _renderRuleRow(rule, index) {
      const row = createElement("div", "trigger-row");

      const toggle = createElement("input", "trigger-toggle");
      toggle.type = "checkbox";
      toggle.checked = rule.enabled;
      toggle.addEventListener("change", () => {
        this._rules[index].enabled = toggle.checked;
        this._save();
      });

      const info = createElement("div", "trigger-info");
      const name = createElement("div", "trigger-name", {
        textContent: rule.name || rule.pattern,
      });
      const meta = createElement("div", "trigger-meta", {
        textContent: `${rule.isRegex ? "Regex" : "Text"} · ${rule.action} · ${rule.caseSensitive ? "Case-sensitive" : "Case-insensitive"}`,
      });
      info.appendChild(name);
      info.appendChild(meta);

      const pattern = createElement("code", "trigger-pattern", {
        textContent: rule.pattern,
      });

      const actions = createElement("div", "trigger-actions");
      const editBtn = createElement("button", "btn btn-xs", {
        textContent: "Edit",
      });
      editBtn.addEventListener("mousedown", (e) => e.preventDefault());
      editBtn.addEventListener("click", () => this._editRule(index));

      const delBtn = createElement("button", "btn btn-xs btn-danger", {
        textContent: "✕",
      });
      delBtn.addEventListener("mousedown", (e) => e.preventDefault());
      delBtn.addEventListener("click", () => {
        this._rules.splice(index, 1);
        this._save();
        this._renderList();
      });

      actions.appendChild(editBtn);
      actions.appendChild(delBtn);

      row.appendChild(toggle);
      row.appendChild(info);
      row.appendChild(pattern);
      row.appendChild(actions);
      return row;
    }

    _addRule() {
      this._showRuleForm(null, (rule) => {
        this._rules.push(rule);
        this._save();
        this._renderList();
      });
    }

    _editRule(index) {
      this._showRuleForm(this._rules[index], (rule) => {
        this._rules[index] = rule;
        this._save();
        this._renderList();
      });
    }

    _showRuleForm(existing, onSave) {
      const list = this._backdrop.querySelector(".trigger-list");
      const toolbar = this._backdrop.querySelector(".trigger-toolbar");
      if (!list) return;

      // Hide list and toolbar
      list.style.display = "none";
      if (toolbar) toolbar.style.display = "none";

      const form = createElement("div", "trigger-form");
      form.innerHTML = `
        <div class="form-group">
          <label class="form-label">Name (optional)</label>
          <input type="text" class="form-input trigger-name-input" placeholder="e.g. Error alert" value="${existing ? existing.name || "" : ""}" />
        </div>
        <div class="form-group">
          <label class="form-label">Pattern</label>
          <input type="text" class="form-input trigger-pattern-input" placeholder="e.g. ERROR|FATAL|panic" value="${existing ? existing.pattern : ""}" />
        </div>
        <div class="form-row">
          <div class="form-group" style="flex:1">
            <label class="form-label">Match Type</label>
            <select class="form-input trigger-type-select">
              <option value="text"${existing && !existing.isRegex ? " selected" : ""}>Plain text</option>
              <option value="regex"${existing && existing.isRegex ? " selected" : ""}>Regex</option>
            </select>
          </div>
          <div class="form-group" style="flex:1">
            <label class="form-label">Action</label>
            <select class="form-input trigger-action-select">
              <option value="notify"${existing && existing.action === "notify" ? " selected" : ""}>Notify</option>
              <option value="highlight"${existing && existing.action === "highlight" ? " selected" : ""}>Highlight tab</option>
              <option value="both"${existing && existing.action === "both" ? " selected" : ""}>Notify + Highlight</option>
              <option value="sound"${existing && existing.action === "sound" ? " selected" : ""}>Sound</option>
            </select>
          </div>
        </div>
        <div class="form-group">
          <label class="form-label">
            <input type="checkbox" class="trigger-case-check"${existing && existing.caseSensitive ? " checked" : ""} />
            Case-sensitive
          </label>
        </div>
        <div class="trigger-form-actions">
          <button class="btn btn-sm trigger-cancel-btn">Cancel</button>
          <button class="btn btn-sm btn-accent trigger-save-btn">${existing ? "Save" : "Add"}</button>
        </div>
      `;

      list.parentNode.insertBefore(form, list);

      form
        .querySelector(".trigger-cancel-btn")
        .addEventListener("click", () => {
          form.remove();
          list.style.display = "";
          if (toolbar) toolbar.style.display = "";
        });

      form.querySelector(".trigger-save-btn").addEventListener("click", () => {
        const pattern = form
          .querySelector(".trigger-pattern-input")
          .value.trim();
        if (!pattern) {
          this.app.notifications.error("Pattern is required");
          return;
        }
        const rule = {
          name: form.querySelector(".trigger-name-input").value.trim(),
          pattern: pattern,
          isRegex: form.querySelector(".trigger-type-select").value === "regex",
          action: form.querySelector(".trigger-action-select").value,
          caseSensitive: form.querySelector(".trigger-case-check").checked,
          enabled: existing ? existing.enabled : true,
        };
        form.remove();
        list.style.display = "";
        if (toolbar) toolbar.style.display = "";
        onSave(rule);
      });

      form.querySelector(".trigger-pattern-input").focus();
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.TriggerEngine = TriggerEngine;
})();
