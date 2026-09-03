(function () {
  /**
   * Alchementary Link — sidebar module.
   * Renders the profile list with search, sections (favourites, recent, all),
   * and context menus.
   */

  const {
    createElement,
    escapeHtml,
    fuzzyMatch,
    protocolColour,
    dismissContextMenus,
    truncate,
  } = window.AlchementaryLink;

  /** Maximum number of profiles shown in the "Recent" section. */
  const RECENT_LIMIT = 5;

  class Sidebar {
    /**
     * @param {App} app
     */
    constructor(app) {
      this.app = app;
      this.el = document.getElementById("sidebar-profiles");
      this.searchQuery = "";

      /** @type {Set<string>} */
      this.selectedProfiles = new Set();

      // Section collapse state
      this.collapsedSections = new Set();

      // Dismiss context menus on any outside click
      document.addEventListener("click", () => dismissContextMenus());

      if (this.el) {
        this.el.addEventListener("click", (e) => {
          if (
            !e.target.closest(".sidebar-item") &&
            this.selectedProfiles.size > 0
          ) {
            this._clearSelection();
          }
        });
      }
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Full re-render of the sidebar profile list.
     * Profiles are organised into:
     *   1. Favourites (if any)
     *   2. Recent (last 5 connected)
     *   3. One section per group (sorted alphabetically)
     *   4. Ungrouped (profiles with no group assigned)
     */
    render() {
      if (!this.el) return;
      if (this.app.mode === "leviathan") {
        this.app.leviathan.renderSidebar();
        return;
      }
      this.el.innerHTML = "";

      const profiles = this._filteredProfiles();
      this._syncSelection(profiles);

      if (this.selectedProfiles.size > 0) {
        this.el.appendChild(this._renderBulkActions());
      }

      // Local Machine quick-connect button
      this.el.appendChild(this._renderLocalMachineItem());

      // Favourites
      const favourites = profiles.filter((p) => p.favourite);
      if (favourites.length > 0) {
        this._renderSection("★ Favourites", "favourites", favourites);
      }

      // Recent
      const recent = profiles
        .filter((p) => p.lastConnected)
        .sort((a, b) => new Date(b.lastConnected) - new Date(a.lastConnected))
        .slice(0, RECENT_LIMIT);
      if (recent.length > 0) {
        this._renderSection("Recent", "recent", recent);
      }

      // ── Group-based sections ─────────────────────────────────────
      // Collect distinct groups and sort them alphabetically.
      const groupMap = new Map();
      const ungrouped = [];

      for (const p of profiles) {
        const grp = (p.folder || "").trim();
        if (grp) {
          if (!groupMap.has(grp)) groupMap.set(grp, []);
          groupMap.get(grp).push(p);
        } else {
          ungrouped.push(p);
        }
      }

      // Render each group as its own collapsible section
      const sortedGroups = [...groupMap.keys()].sort((a, b) =>
        a.localeCompare(b),
      );
      for (const grp of sortedGroups) {
        this._renderSection(grp, `group:${grp}`, groupMap.get(grp));
      }

      // Ungrouped connections
      if (ungrouped.length > 0) {
        const label = sortedGroups.length > 0 ? "Ungrouped" : "All Connections";
        this._renderSection(label, "ungrouped", ungrouped);
      }

      // Empty state
      if (profiles.length === 0) {
        this.el.appendChild(this._renderEmptyState());
      }
    }

    /**
     * Filters the displayed profiles by query.
     * @param {string} query
     */
    search(query) {
      this.searchQuery = query.trim();
      this.render();
    }

    /**
     * Highlights the sidebar item for the currently active profile.
     * @param {string|null} profileId
     */
    highlightActive(profileId) {
      this.el.querySelectorAll(".sidebar-item").forEach((item) => {
        item.classList.toggle(
          "sidebar-item-active",
          item.dataset.profileId === profileId,
        );
      });
    }

    /**
     * Toggles the collapsed state of a named section.
     * @param {string} sectionName
     */
    toggleSection(sectionName) {
      if (this.collapsedSections.has(sectionName)) {
        this.collapsedSections.delete(sectionName);
      } else {
        this.collapsedSections.add(sectionName);
      }
      this.render();
    }

    // ── Rendering helpers ────────────────────────────────────────────

    /**
     * Renders the sidebar empty state for first-run and no-search-results cases.
     * @returns {HTMLElement}
     * @private
     */
    _renderEmptyState() {
      const isSearching = this.searchQuery.length > 0;
      const empty = createElement("div", "sidebar-empty");

      const icon = createElement("div", "sidebar-empty-icon", {
        textContent: isSearching ? "⌕" : "+",
        "aria-hidden": "true",
      });
      empty.appendChild(icon);

      empty.appendChild(
        createElement("div", "sidebar-empty-title", {
          textContent: isSearching ? "No matching profiles" : "No saved profiles",
        }),
      );

      empty.appendChild(
        createElement("p", "sidebar-empty-text", {
          textContent: isSearching
            ? "Try a different name, host, group, or protocol."
            : "Create a profile for the servers and tools you use often.",
        }),
      );

      if (!isSearching) {
        const actions = createElement("div", "sidebar-empty-actions");
        const addButton = createElement("button", "btn btn-primary btn-sm", {
          type: "button",
          textContent: "New Profile",
        });
        addButton.addEventListener("click", (e) => {
          e.preventDefault();
          e.stopPropagation();
          this.app.profileEditor.openNew();
        });
        actions.appendChild(addButton);
        empty.appendChild(actions);
      }

      return empty;
    }

    /**
     * Renders a collapsible section of profiles.
     * @param {string} title
     * @param {string} key
     * @param {Array<Object>} profiles
     * @private
     */
    _renderSection(title, key, profiles) {
      const section = createElement("div", "sidebar-section");
      const isCollapsed = this.collapsedSections.has(key);
      const isGroupSection = key.startsWith("group:");

      // Section header
      const header = createElement(
        "div",
        `sidebar-section-header ${isGroupSection ? "sidebar-section-header-group" : ""}`.trim(),
      );
      header.innerHTML = `
            <span class="section-chevron ${isCollapsed ? "collapsed" : ""}">${isCollapsed ? "▸" : "▾"}</span>
            <span class="section-title">${escapeHtml(title)}</span>
            <span class="section-count">${profiles.length}</span>
        `;
      // Prevent mousedown from starting a text selection gesture in WKWebView,
      // which would swallow the first click and require a double-click.
      header.addEventListener("mousedown", (e) => e.preventDefault());
      header.addEventListener("click", () => this.toggleSection(key));
      section.appendChild(header);

      // Items
      if (!isCollapsed) {
        const list = createElement("div", "sidebar-list");
        for (const profile of profiles) {
          list.appendChild(this.renderProfileItem(profile));
        }
        section.appendChild(list);
      }

      this.el.appendChild(section);
    }

    /**
     * Creates a single sidebar list item for a profile.
     * @param {Object} profile
     * @returns {HTMLElement}
     */
    renderProfileItem(profile) {
      const item = createElement("div", "sidebar-item", {
        dataset: { profileId: profile.id },
      });

      if (this.selectedProfiles.has(profile.id)) {
        item.classList.add("sidebar-item-selected");
      }

      // Highlight if this profile's session is active
      const activeSession = this.app.state.activeSessionId
        ? this.app.state.getSession(this.app.state.activeSessionId)
        : null;
      if (activeSession && activeSession.profileId === profile.id) {
        item.classList.add("sidebar-item-active");
      }

      // Protocol icon
      const protocolLabel = ""; // Seemed pretty naff to have this as well as the little icon badboy this._protocolLabel(profile.protocol);
      const indicator = createElement(
        "span",
        `protocol-indicator ${protocolColour(profile.protocol)}`,
        {
          title: protocolLabel,
          "aria-label": protocolLabel,
        },
      );
      item.appendChild(indicator);

      // Text content
      const textWrapper = createElement("div", "sidebar-item-text");
      const displayName = (profile.displayName || "").trim();
      const primaryLabel = displayName || profile.host || "Untitled";
      const nameEl = createElement("span", "sidebar-item-name", {
        textContent: primaryLabel,
      });
      textWrapper.appendChild(nameEl);

      const subtitle = protocolLabel;
      const subEl = createElement("span", "sidebar-item-host", {
        textContent: truncate(subtitle, 30),
      });
      textWrapper.appendChild(subEl);
      item.appendChild(textWrapper);

      // Edit button (visible on hover)
      const editBtn = createElement("button", "sidebar-item-edit", {
        textContent: "✎",
        title: "Edit profile",
        type: "button",
        "aria-label": "Edit profile",
      });
      let editTriggered = false;
      const triggerEdit = (e) => {
        e.preventDefault();
        e.stopPropagation();
        if (editTriggered) return;
        editTriggered = true;
        requestAnimationFrame(() => {
          editTriggered = false;
        });
        this.app.profileEditor.openEdit(profile.id);
      };
      editBtn.addEventListener("mousedown", triggerEdit);
      editBtn.addEventListener("click", triggerEdit);
      item.appendChild(editBtn);

      // Favourite star
      if (profile.favourite) {
        const star = createElement("span", "sidebar-item-star is-favourite", {
          textContent: "★",
        });
        item.appendChild(star);
      }

      // Prevent mousedown text selection in WKWebView, but allow real controls.
      item.addEventListener("mousedown", (e) => {
        if (e.target.closest("button, input, select, textarea")) return;
        e.preventDefault();
      });

      // Click → connect / multi-select
      item.addEventListener("click", (e) => {
        const multi = e.metaKey || e.ctrlKey;
        if (multi) {
          e.preventDefault();
          this._toggleSelection(profile.id);
          return;
        }

        if (this.selectedProfiles.size > 0) {
          this._clearSelection();
        }
        this.app.connectProfile(profile.id);
      });

      // Double-click → edit
      item.addEventListener("dblclick", (e) => {
        e.stopPropagation();
        this.app.profileEditor.openEdit(profile.id);
      });

      // Right-click → context menu
      item.addEventListener("contextmenu", (e) => {
        e.preventDefault();
        e.stopPropagation();
        this.renderContextMenu(profile, e.clientX, e.clientY);
      });

      return item;
    }

    /**
     * Shows a right-click context menu for a profile.
     * @param {Object} profile
     * @param {number} x - Viewport X.
     * @param {number} y - Viewport Y.
     */
    renderContextMenu(profile, x, y) {
      dismissContextMenus();

      const menu = createElement("div", "context-menu");
      menu.style.left = `${x}px`;
      menu.style.top = `${y}px`;

      const items = [
        { label: "Connect", action: () => this.app.connectProfile(profile.id) },
        {
          label: "Edit",
          action: () => this.app.profileEditor.openEdit(profile.id),
        },
        { label: "Duplicate", action: () => this._duplicateProfile(profile) },
        { divider: true },
        {
          label: profile.favourite
            ? "Remove from Favourites"
            : "Add to Favourites",
          action: () => this._toggleFavourite(profile),
        },
        { divider: true },
        {
          label: "Delete",
          className: "context-menu-danger",
          action: () => this._deleteProfile(profile),
        },
      ];

      for (const entry of items) {
        if (entry.divider) {
          menu.appendChild(createElement("div", "context-menu-divider"));
          continue;
        }
        const btn = createElement(
          "button",
          `context-menu-item ${entry.className || ""}`,
          {
            textContent: entry.label,
          },
        );
        btn.addEventListener("click", () => {
          dismissContextMenus();
          entry.action();
        });
        menu.appendChild(btn);
      }

      // Keep menu within viewport
      document.body.appendChild(menu);
      const rect = menu.getBoundingClientRect();
      if (rect.right > window.innerWidth) {
        menu.style.left = `${window.innerWidth - rect.width - 8}px`;
      }
      if (rect.bottom > window.innerHeight) {
        menu.style.top = `${window.innerHeight - rect.height - 8}px`;
      }
    }

    // ── Actions ──────────────────────────────────────────────────────

    /**
     * Toggles the favourite status of a profile.
     * @param {Object} profile
     * @private
     */
    async _toggleFavourite(profile) {
      try {
        const newVal = !profile.favourite;
        await window.AlchementaryLink.wailsService.SetFavourite(profile.id, newVal);
        profile.favourite = newVal;
        this.app.state.updateProfile(profile);
        this.render();
      } catch (err) {
        this.app.notifications.error("Failed to update favourite: " + err);
      }
    }

    /**
     * Duplicates a profile.
     * @param {Object} profile
     * @private
     */
    async _duplicateProfile(profile) {
      try {
        const dup = await window.AlchementaryLink.wailsService.DuplicateProfile(profile.id);
        this.app.state.addProfile(dup);
        this.render();
        this.app.notifications.success(`Duplicated "${profile.displayName}"`);
      } catch (err) {
        this.app.notifications.error("Failed to duplicate profile: " + err);
      }
    }

    /**
     * Deletes a profile after confirmation.
     * @param {Object} profile
     * @private
     */
    async _deleteProfile(profile) {
      // Use a custom confirmation modal — window.confirm() is silently
      // blocked in WKWebView and always returns false.
      const ok = await this._confirmDelete(profile);
      if (!ok) return;

      try {
        await window.AlchementaryLink.wailsService.DeleteProfile(profile.id);
        this.app.state.removeProfile(profile.id);
        this.render();
        this.app.notifications.success("Profile deleted");
      } catch (err) {
        this.app.notifications.error("Failed to delete profile: " + err);
      }
    }

    /**
     * Shows a confirmation modal for profile deletion.
     * @param {Object} profile
     * @returns {Promise<boolean>}
     * @private
     */
    _confirmDelete(profile) {
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        const label = escapeHtml(
          profile.displayName || profile.host || "Untitled",
        );
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">Delete Profile</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <p style="font-size:var(--text-sm);color:var(--text-secondary)">
                        Delete <strong>${label}</strong>? This cannot be undone.
                    </p>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm confirm-cancel">Cancel</button>
                    <button class="btn btn-sm btn-danger confirm-ok">Delete</button>
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
          .addEventListener("click", () => close(false));
        modal
          .querySelector(".confirm-cancel")
          .addEventListener("click", () => close(false));
        modal
          .querySelector(".confirm-ok")
          .addEventListener("click", () => close(true));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(false);
        });
      });
    }

    // ── Multi-select + bulk actions ─────────────────────────────────

    /**
     * Removes selections that are no longer visible in the filtered list.
     * @param {Array<Object>} profiles
     * @private
     */
    _syncSelection(profiles) {
      const valid = new Set(profiles.map((p) => p.id));
      for (const id of [...this.selectedProfiles]) {
        if (!valid.has(id)) this.selectedProfiles.delete(id);
      }
    }

    /** @private */
    _toggleSelection(profileId) {
      if (this.selectedProfiles.has(profileId)) {
        this.selectedProfiles.delete(profileId);
      } else {
        this.selectedProfiles.add(profileId);
      }
      this.render();
    }

    /** @private */
    _clearSelection() {
      if (this.selectedProfiles.size === 0) return;
      this.selectedProfiles.clear();
      this.render();
    }

    /** @private */
    _getSelectedProfiles() {
      return this.app.state.profiles.filter((p) =>
        this.selectedProfiles.has(p.id),
      );
    }

    /** @private */
    _renderLocalMachineItem() {
      const item = createElement("div", "sidebar-item sidebar-item-local");
      item.addEventListener("mousedown", (e) => e.preventDefault());

      const indicator = createElement(
        "span",
        "protocol-indicator proto-local",
        {
          title: "Local",
        },
      );
      item.appendChild(indicator);

      const textWrapper = createElement("div", "sidebar-item-text");
      const nameEl = createElement("span", "sidebar-item-name", {
        textContent: "Local Machine",
      });
      textWrapper.appendChild(nameEl);
      item.appendChild(textWrapper);

      item.addEventListener("click", () => {
        if (this.onConnectLocal) {
          this.onConnectLocal();
        }
      });

      return item;
    }

    /** @private */
    _renderBulkActions() {
      const bar = createElement("div", "sidebar-bulk-actions");
      const count = createElement("span", "sidebar-bulk-count", {
        textContent: `${this.selectedProfiles.size} selected`,
      });
      bar.appendChild(count);

      const actions = createElement("div", "sidebar-bulk-buttons");
      const makeBtn = (label, handler, className) => {
        const btn = createElement(
          "button",
          `btn btn-xs ${className || ""}`.trim(),
          {
            textContent: label,
          },
        );
        btn.addEventListener("click", handler);
        actions.appendChild(btn);
      };

      makeBtn("Connect", () => this._bulkConnect());
      makeBtn("Disconnect", () => this._bulkDisconnect(), "btn-outline");
      makeBtn("Tags", () => this._bulkEditTags(), "btn-outline");
      makeBtn("Move Group", () => this._bulkMoveGroup(), "btn-outline");
      makeBtn("Clear", () => this._clearSelection(), "btn-ghost");

      bar.appendChild(actions);
      return bar;
    }

    /** @private */
    _bulkConnect() {
      const profiles = this._getSelectedProfiles();
      for (const p of profiles) {
        this.app.connectProfile(p.id);
      }
      this._clearSelection();
    }

    /** @private */
    async _bulkDisconnect() {
      const sessions = this.app.state
        .getAllSessions()
        .filter((s) => this.selectedProfiles.has(s.profileId));
      if (sessions.length === 0) {
        this.app.notifications.info("No active sessions to disconnect");
        return;
      }

      const ok = await this._confirmBulkDisconnect(sessions.length);
      if (!ok) return;

      for (const s of sessions) {
        await this.app.closeSessionImmediate(s.id);
      }
      this._clearSelection();
    }

    /** @private */
    async _bulkEditTags() {
      const result = await this._promptTagEdit();
      if (!result) return;

      const profiles = this._getSelectedProfiles();
      for (const p of profiles) {
        const updated = JSON.parse(JSON.stringify(p));
        const existing = updated.tags || [];
        if (result.mode === "replace") {
          updated.tags = result.tags;
        } else {
          const merged = new Set([...existing, ...result.tags]);
          updated.tags = [...merged];
        }

        try {
          const saved = await window.AlchementaryLink.wailsService.UpdateProfile(updated);
          this.app.state.updateProfile(saved);
        } catch (err) {
          this.app.notifications.error("Failed to update tags: " + err);
        }
      }

      this.render();
      this._clearSelection();
    }

    /** @private */
    async _bulkMoveGroup() {
      const folder = await this._promptGroupMove();
      if (folder === null) return;

      const profiles = this._getSelectedProfiles();
      for (const p of profiles) {
        const updated = JSON.parse(JSON.stringify(p));
        updated.folder = folder;
        try {
          const saved = await window.AlchementaryLink.wailsService.UpdateProfile(updated);
          this.app.state.updateProfile(saved);
        } catch (err) {
          this.app.notifications.error("Failed to move group: " + err);
        }
      }

      this.render();
      this._clearSelection();
    }

    /** @private */
    _confirmBulkDisconnect(count) {
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">Disconnect Sessions</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <p style="font-size:var(--text-sm);color:var(--text-secondary)">
                        Disconnect ${count} active ${count === 1 ? "session" : "sessions"}?
                    </p>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm confirm-cancel">Cancel</button>
                    <button class="btn btn-sm btn-danger confirm-ok">Disconnect</button>
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
          .addEventListener("click", () => close(false));
        modal
          .querySelector(".confirm-cancel")
          .addEventListener("click", () => close(false));
        modal
          .querySelector(".confirm-ok")
          .addEventListener("click", () => close(true));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(false);
        });
      });
    }

    /** @private */
    _promptTagEdit() {
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">Edit Tags</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label class="form-label">Tags (comma separated)</label>
                        <input class="form-input tag-input" type="text" placeholder="prod, linux, db" />
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm confirm-cancel">Cancel</button>
                    <button class="btn btn-sm btn-outline confirm-add">Add Tags</button>
                    <button class="btn btn-sm btn-primary confirm-replace">Replace Tags</button>
                </div>
            `;
        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => {
          backdrop.classList.add("is-visible");
          modal.classList.add("is-visible");
        });

        const input = modal.querySelector(".tag-input");
        const parseTags = () => {
          const raw = (input.value || "").split(",");
          return raw.map((t) => t.trim()).filter((t) => t.length > 0);
        };

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
        modal.querySelector(".confirm-add").addEventListener("click", () => {
          close({ mode: "add", tags: parseTags() });
        });
        modal
          .querySelector(".confirm-replace")
          .addEventListener("click", () => {
            close({ mode: "replace", tags: parseTags() });
          });
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(null);
        });
        setTimeout(() => input.focus(), 50);
      });
    }

    /** @private */
    _promptGroupMove() {
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        modal.innerHTML = `
                <div class="modal-header">
                    <h3 class="modal-title">Move to Group</h3>
                    <button class="btn btn-icon btn-xs modal-close">&times;</button>
                </div>
                <div class="modal-body">
                    <div class="form-group">
                        <label class="form-label">Group name</label>
                        <input class="form-input group-input" type="text" placeholder="e.g. Production" />
                        <p style="font-size:var(--text-xs);color:var(--text-secondary);margin-top:6px;">
                            Leave blank to remove the group.
                        </p>
                    </div>
                </div>
                <div class="modal-footer">
                    <button class="btn btn-sm confirm-cancel">Cancel</button>
                    <button class="btn btn-sm btn-primary confirm-ok">Move</button>
                </div>
            `;
        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => {
          backdrop.classList.add("is-visible");
          modal.classList.add("is-visible");
        });

        const input = modal.querySelector(".group-input");
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
          .addEventListener("click", () => close(input.value.trim()));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(null);
        });
        setTimeout(() => input.focus(), 50);
      });
    }

    // ── Filtering ────────────────────────────────────────────────────

    /**
     * Returns profiles filtered by the current search query.
     * @returns {Array<Object>}
     * @private
     */
    _filteredProfiles() {
      if (!this.searchQuery) return this.app.state.profiles;

      return this.app.state.profiles
        .map((p) => {
          // Match against display name, host, tags, and protocol
          const fields = [
            p.displayName || "",
            p.host || "",
            (p.tags || []).join(" "),
            p.protocol || "",
          ].join(" ");
          const result = fuzzyMatch(fields, this.searchQuery);
          return { profile: p, ...result };
        })
        .filter((r) => r.match)
        .sort((a, b) => b.score - a.score)
        .map((r) => r.profile);
    }

    /**
     * Returns a display label for a protocol.
     * @param {string} protocol
     * @returns {string}
     * @private
     */
    _protocolLabel(protocol) {
      switch (protocol) {
        case "github":
          return "GitHub";
        case "ssh":
          return "SSH";
        case "telnet":
          return "TELNET";
        case "raw":
          return "RAW";
        case "irc":
          return "IRC";
        default:
          return (protocol || "").toUpperCase() || "UNKNOWN";
      }
    }
  }

  window.AlchementaryLink = window.AlchementaryLink || {};
  window.AlchementaryLink.Sidebar = Sidebar;
})();
