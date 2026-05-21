(function () {
  /**
   * Nexus Terminal — SSH Key Manager.
   * Panel for listing, generating, copying, and deleting SSH key pairs.
   */

  const { createElement, dismissContextMenus } = window.NexusTerminal;

  class SSHKeyManager {
    constructor(app) {
      this.app = app;
      this._backdrop = null;
    }

    // ── Public API ───────────────────────────────────────────────────

    open() {
      if (this._backdrop) return;
      this._render();
      this._loadKeys();
    }

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

    // ── Rendering ────────────────────────────────────────────────────

    _render() {
      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-lg");

      modal.innerHTML = `
        <div class="modal-header">
          <h3 class="modal-title">SSH Key Manager</h3>
          <button class="btn btn-icon btn-xs modal-close">&times;</button>
        </div>
        <div class="modal-body">
          <div class="sshkey-toolbar">
            <button class="btn btn-sm btn-accent sshkey-generate-btn">Generate New Key</button>
          </div>
          <div class="sshkey-list"></div>
        </div>
      `;

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);
      this._backdrop = backdrop;

      requestAnimationFrame(() => {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
      });

      // Events
      modal.querySelector(".modal-close").addEventListener("click", () => this.close());
      backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) this.close();
      });
      modal.querySelector(".sshkey-generate-btn").addEventListener("click", () => this._showGenerateForm());
    }

    async _loadKeys() {
      const list = this._backdrop.querySelector(".sshkey-list");
      if (!list) return;

      list.innerHTML = '<div class="sshkey-loading">Loading keys…</div>';

      try {
        const keys = await window.go.api.Service.ListSSHKeyPairs();
        if (keys.length === 0) {
          list.innerHTML = '<div class="sshkey-empty">No SSH keys found in ~/.ssh/</div>';
          return;
        }
        list.innerHTML = "";
        for (const key of keys) {
          list.appendChild(this._renderKeyRow(key));
        }
      } catch (err) {
        list.innerHTML = `<div class="sshkey-error">Failed to load keys: ${err}</div>`;
      }
    }

    _renderKeyRow(key) {
      const row = createElement("div", "sshkey-row");

      const info = createElement("div", "sshkey-info");
      const name = createElement("div", "sshkey-name", { textContent: key.name });
      const meta = createElement("div", "sshkey-meta", {
        textContent: key.type.toUpperCase() + (key.bits ? ` ${key.bits}` : ""),
      });
      info.appendChild(name);
      info.appendChild(meta);

      const actions = createElement("div", "sshkey-actions");

      const copyBtn = createElement("button", "btn btn-xs", { textContent: "Copy Public Key" });
      copyBtn.addEventListener("mousedown", (e) => e.preventDefault());
      copyBtn.addEventListener("click", () => this._copyPublicKey(key));

      const deleteBtn = createElement("button", "btn btn-xs btn-danger", { textContent: "Delete" });
      deleteBtn.addEventListener("mousedown", (e) => e.preventDefault());
      deleteBtn.addEventListener("click", () => this._confirmDelete(key));

      actions.appendChild(copyBtn);
      actions.appendChild(deleteBtn);

      row.appendChild(info);
      row.appendChild(actions);
      return row;
    }

    async _copyPublicKey(key) {
      try {
        const pubKey = await window.go.api.Service.GetSSHPublicKey(key.name);
        if (window.runtime && window.runtime.ClipboardSetText) {
          window.runtime.ClipboardSetText(pubKey);
        } else {
          await navigator.clipboard.writeText(pubKey);
        }
        this.app.notifications.success("Public key copied to clipboard");
      } catch (err) {
        this.app.notifications.error("Failed to copy: " + err);
      }
    }

    _confirmDelete(key) {
      const confirmed = this._showConfirm(
        "Delete Key",
        `Delete key pair "${key.name}"? This cannot be undone.`,
        "Delete",
      );
      confirmed.then((ok) => {
        if (ok) this._deleteKey(key);
      });
    }

    async _deleteKey(key) {
      try {
        await window.go.api.Service.DeleteSSHKey(key.name);
        this.app.notifications.success(`Key "${key.name}" deleted`);
        this._loadKeys();
      } catch (err) {
        this.app.notifications.error("Delete failed: " + err);
      }
    }

    // ── Generate form ────────────────────────────────────────────────

    _showGenerateForm() {
      const list = this._backdrop.querySelector(".sshkey-list");
      if (!list) return;

      const form = createElement("div", "sshkey-generate-form");
      form.innerHTML = `
        <div class="form-group">
          <label class="form-label">Key Name</label>
          <input type="text" class="form-input sshkey-name-input" placeholder="id_ed25519_nexus" autocomplete="off" spellcheck="false" />
        </div>
        <div class="form-group">
          <label class="form-label">Type</label>
          <select class="form-input sshkey-type-select">
            <option value="ed25519" selected>Ed25519 (recommended)</option>
            <option value="rsa">RSA</option>
            <option value="ecdsa">ECDSA</option>
          </select>
        </div>
        <div class="form-group sshkey-bits-group" style="display:none">
          <label class="form-label">Bits</label>
          <select class="form-input sshkey-bits-select">
            <option value="4096" selected>4096</option>
            <option value="2048">2048</option>
          </select>
        </div>
        <div class="form-group">
          <label class="form-label">Passphrase (optional)</label>
          <input type="password" class="form-input sshkey-pass-input" placeholder="Leave blank for no passphrase" autocomplete="off" />
        </div>
        <div class="sshkey-form-actions">
          <button class="btn btn-sm sshkey-cancel-btn">Cancel</button>
          <button class="btn btn-sm btn-accent sshkey-create-btn">Generate</button>
        </div>
      `;

      // Insert form above the key list
      list.parentNode.insertBefore(form, list);
      list.style.display = "none";

      // Show bits field for RSA
      const typeSelect = form.querySelector(".sshkey-type-select");
      const bitsGroup = form.querySelector(".sshkey-bits-group");
      typeSelect.addEventListener("change", () => {
        bitsGroup.style.display = typeSelect.value === "rsa" ? "" : "none";
      });

      form.querySelector(".sshkey-cancel-btn").addEventListener("click", () => {
        form.remove();
        list.style.display = "";
      });

      form.querySelector(".sshkey-create-btn").addEventListener("click", async () => {
        const nameInput = form.querySelector(".sshkey-name-input");
        const name = nameInput.value.trim();
        const keyType = typeSelect.value;
        const bits = parseInt(form.querySelector(".sshkey-bits-select").value, 10);
        const passphrase = form.querySelector(".sshkey-pass-input").value;

        if (!name) {
          this.app.notifications.error("Key name is required");
          return;
        }

        try {
          await window.go.api.Service.GenerateSSHKey(name, keyType, bits, passphrase);
          this.app.notifications.success(`Key "${name}" generated successfully`);
          form.remove();
          list.style.display = "";
          this._loadKeys();
        } catch (err) {
          this.app.notifications.error("Generation failed: " + err);
        }
      });

      form.querySelector(".sshkey-name-input").focus();
    }

    // ── Confirmation helper ──────────────────────────────────────────

    _showConfirm(title, message, confirmLabel) {
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        modal.innerHTML = `
          <div class="modal-header">
            <h3 class="modal-title">${title}</h3>
            <button class="btn btn-icon btn-xs modal-close">&times;</button>
          </div>
          <div class="modal-body">
            <p style="font-size:var(--text-sm);color:var(--text-secondary)">${message}</p>
          </div>
          <div class="modal-footer">
            <button class="btn btn-sm confirm-cancel">Cancel</button>
            <button class="btn btn-sm btn-danger confirm-ok">${confirmLabel}</button>
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
        modal.querySelector(".modal-close").addEventListener("click", () => close(false));
        modal.querySelector(".confirm-cancel").addEventListener("click", () => close(false));
        modal.querySelector(".confirm-ok").addEventListener("click", () => close(true));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(false);
        });
      });
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.SSHKeyManager = SSHKeyManager;
})();
