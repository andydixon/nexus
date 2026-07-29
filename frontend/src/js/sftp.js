(function () {
  /**
   * Nexus Terminal — SFTP File Browser
   *
   * Provides a right-side panel for browsing, uploading, downloading,
   * renaming and deleting remote files alongside the terminal.
   */

  const {
    createElement,
    escapeHtml,
    formatBytes,
    relativeTime,
    dismissContextMenus,
  } = window.NexusTerminal;

  /* ── Helpers ──────────────────────────────────────────────────────── */

  function pathJoin(base, name) {
    if (base === "/") return "/" + name;
    return base.replace(/\/+$/, "") + "/" + name;
  }

  function pathSegments(p) {
    const parts = p.split("/").filter(Boolean);
    var segs = [{ label: "/", path: "/" }];
    var acc = "";
    for (var i = 0; i < parts.length; i++) {
      acc += "/" + parts[i];
      segs.push({ label: parts[i], path: acc });
    }
    return segs;
  }

  function parentPath(p) {
    if (p === "/") return "/";
    var trimmed = p.replace(/\/+$/, "");
    var idx = trimmed.lastIndexOf("/");
    return idx <= 0 ? "/" : trimmed.substring(0, idx);
  }

  function fileIcon(entry) {
    if (entry.isDir) return "📁";
    var ext = (entry.name.split(".").pop() || "").toLowerCase();
    switch (ext) {
      case "jpg":
      case "jpeg":
      case "png":
      case "gif":
      case "svg":
      case "webp":
        return "🖼";
      case "mp3":
      case "wav":
      case "ogg":
      case "flac":
        return "🎵";
      case "mp4":
      case "avi":
      case "mkv":
      case "mov":
        return "🎬";
      case "zip":
      case "tar":
      case "gz":
      case "bz2":
      case "xz":
      case "7z":
        return "📦";
      case "sh":
      case "bash":
      case "zsh":
        return "⚙";
      case "py":
      case "js":
      case "ts":
      case "go":
      case "rs":
      case "c":
      case "h":
      case "cpp":
      case "java":
        return "◇";
      case "md":
      case "txt":
      case "log":
      case "csv":
        return "📝";
      case "json":
      case "yaml":
      case "yml":
      case "toml":
      case "xml":
        return "⚙";
      case "key":
      case "pem":
      case "crt":
      case "pub":
        return "🔑";
      default:
        return "📄";
    }
  }

  function modeString(mode) {
    if (!mode && mode !== 0) return "";
    var s = "";
    var perms = ["---", "--x", "-w-", "-wx", "r--", "r-x", "rw-", "rwx"];
    s += perms[(mode >> 6) & 7];
    s += perms[(mode >> 3) & 7];
    s += perms[mode & 7];
    return s;
  }

  /* ── SFTPBrowser class ────────────────────────────────────────────── */

  class SFTPBrowser {
    /**
     * @param {App} app — the application instance
     */
    constructor(app) {
      this.app = app;
      /** @type {Map<string, {el: HTMLElement, handle: HTMLElement, cwd: string, files: Array, selected: string|null, loading: boolean}>} */
      this.panels = new Map();
    }

    /* ── Public API ───────────────────────────────────────────────── */

    /**
     * Opens (or focuses) the SFTP panel for a session.
     * @param {string} sessionId
     */
    async open(sessionId) {
      if (this.panels.has(sessionId)) return;

      var sessionPanel = document.getElementById("panel-" + sessionId);
      if (!sessionPanel) {
        this.app.notifications.error(
          "Cannot open file browser: session panel not found.",
        );
        return;
      }

      var state = {
        el: null,
        handle: null,
        cwd: "/",
        files: [],
        selected: null,
        loading: false,
      };

      // Build the panel DOM
      var panel = createElement("div", "sftp-panel");
      var handle = createElement("div", "sftp-resize-handle");
      panel.appendChild(handle);
      state.el = panel;
      state.handle = handle;

      // Toolbar
      var toolbar = this._buildToolbar(sessionId, state);
      panel.appendChild(toolbar);

      // File list container
      var fileList = createElement("div", "sftp-file-list");
      panel.appendChild(fileList);

      // Context action bar (hidden initially)
      var ctxBar = createElement("div", "sftp-context-bar");
      ctxBar.style.display = "none";
      panel.appendChild(ctxBar);

      this.panels.set(sessionId, state);

      // Append the overlay panel into the session panel
      sessionPanel.appendChild(panel);

      this._setupResize(sessionId, state);

      // Fetch initial directory
      await this._navigate(sessionId, null);

      // Refit terminal after layout change
      this._refitTerminal(sessionId);
    }

    /**
     * Closes the SFTP panel for a session.
     * @param {string} sessionId
     */
    close(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;

      if (state.cleanupResize) state.cleanupResize();
      state.el.remove();

      this.panels.delete(sessionId);
      this._refitTerminal(sessionId);
    }

    /**
     * Toggles the SFTP panel for a session.
     * @param {string} sessionId
     */
    toggle(sessionId) {
      if (this.panels.has(sessionId)) {
        this.close(sessionId);
      } else {
        this.open(sessionId);
      }
    }

    /**
     * Returns the current directory for an open SFTP panel, or an empty string.
     * @param {string} sessionId
     * @returns {string}
     */
    getCwd(sessionId) {
      var state = this.panels.get(sessionId);
      return state ? state.cwd : "";
    }

    /* ── Toolbar ──────────────────────────────────────────────────── */

    _buildToolbar(sessionId, state) {
      var toolbar = createElement("div", "sftp-toolbar");

      var breadcrumb = createElement("div", "sftp-breadcrumb");
      breadcrumb.dataset.role = "breadcrumb";
      toolbar.appendChild(breadcrumb);

      var actions = createElement("div", "sftp-toolbar-actions");

      var goToBtn = this._toolbarBtn("⤳", "Go to path", "");
      goToBtn.addEventListener("click", () => this._promptGoToPath(sessionId));
      actions.appendChild(goToBtn);

      var bookmarkBtn = this._toolbarBtn("★", "Bookmarks", "");
      bookmarkBtn.addEventListener("click", () =>
        this._showBookmarks(sessionId),
      );
      actions.appendChild(bookmarkBtn);

      var mkdirBtn = this._toolbarBtn("＋", "New folder", "is-accent");
      mkdirBtn.addEventListener("click", () => this._promptMkdir(sessionId));
      actions.appendChild(mkdirBtn);

      var uploadBtn = this._toolbarBtn("↑", "Upload file", "is-accent");
      uploadBtn.addEventListener("click", () => this._upload(sessionId));
      actions.appendChild(uploadBtn);

      var refreshBtn = this._toolbarBtn("⟳", "Refresh");
      refreshBtn.addEventListener("click", () => this._refresh(sessionId));
      actions.appendChild(refreshBtn);

      var closeBtn = this._toolbarBtn("✕", "Close", "is-close");
      closeBtn.addEventListener("click", () => this.close(sessionId));
      actions.appendChild(closeBtn);

      toolbar.appendChild(actions);
      return toolbar;
    }

    _toolbarBtn(label, title, extraClass) {
      var cls = "sftp-toolbar-btn" + (extraClass ? " " + extraClass : "");
      return createElement("button", cls, { textContent: label, title: title });
    }

    /* ── Navigation ───────────────────────────────────────────────── */

    /**
     * Navigates to a directory path. If path is null, fetches pwd first.
     */
    async _navigate(sessionId, path) {
      var state = this.panels.get(sessionId);
      if (!state || state.loading) return;

      state.loading = true;
      state.selected = null;
      this._renderLoading(sessionId);

      try {
        if (path === null) {
          path = await window.NexusTerminal.wailsService.SFTPPwd(sessionId);
        }
        var files = await window.NexusTerminal.wailsService.SFTPList(sessionId, path);
        state.cwd = path;
        state.files = this._sortFiles(files || []);
        state.loading = false;
        this._renderBreadcrumb(sessionId);
        this._renderFiles(sessionId);
      } catch (err) {
        state.loading = false;
        this._renderError(sessionId, err.message || String(err));
      }
    }

    async _refresh(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      await this._navigate(sessionId, state.cwd);
    }

    _sortFiles(files) {
      return files.slice().sort(function (a, b) {
        // Directories first
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
      });
    }

    /* ── Rendering ────────────────────────────────────────────────── */

    _getFileList(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return null;
      return state.el.querySelector(".sftp-file-list");
    }

    _getCtxBar(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return null;
      return state.el.querySelector(".sftp-context-bar");
    }

    _renderBreadcrumb(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      var container = state.el.querySelector('[data-role="breadcrumb"]');
      container.innerHTML = "";

      var segs = pathSegments(state.cwd);
      var self = this;
      for (var i = 0; i < segs.length; i++) {
        if (i > 0) {
          container.appendChild(
            createElement("span", "sftp-breadcrumb-sep", { textContent: "›" }),
          );
        }
        var btn = createElement("button", "sftp-breadcrumb-segment", {
          textContent: segs[i].label,
          title: segs[i].path,
        });
        (function (p) {
          btn.addEventListener("click", function () {
            self._navigate(sessionId, p);
          });
        })(segs[i].path);
        container.appendChild(btn);
      }
    }

    _renderFiles(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      var list = this._getFileList(sessionId);
      list.innerHTML = "";

      // Hide context bar
      var ctxBar = this._getCtxBar(sessionId);
      if (ctxBar) ctxBar.style.display = "none";

      // Parent directory row if not at root
      if (state.cwd !== "/") {
        var parentRow = this._buildFileRow(
          {
            name: "..",
            isDir: true,
            path: parentPath(state.cwd),
            size: 0,
            modifiedAt: 0,
          },
          sessionId,
          true,
        );
        parentRow.classList.add("is-parent");
        list.appendChild(parentRow);
      }

      if (state.files.length === 0) {
        var empty = createElement("div", "sftp-state");
        empty.innerHTML =
          '<div class="sftp-state-icon">📂</div><div class="sftp-state-msg">Empty directory</div>';
        list.appendChild(empty);
        return;
      }

      var self = this;
      for (var i = 0; i < state.files.length; i++) {
        list.appendChild(this._buildFileRow(state.files[i], sessionId, false));
      }
    }

    _buildFileRow(entry, sessionId, isParent) {
      var row = createElement("div", "sftp-file-row");
      var self = this;

      // Icon
      var icon = createElement(
        "span",
        "sftp-file-icon" + (entry.isDir ? " is-dir" : ""),
      );
      icon.textContent = isParent ? "↩" : fileIcon(entry);
      row.appendChild(icon);

      // Name
      var name = createElement("span", "sftp-file-name", {
        textContent: entry.name,
        title: entry.name,
      });
      row.appendChild(name);

      // Meta (size or date)
      var meta = createElement("span", "sftp-file-meta");
      if (!isParent && !entry.isDir) {
        meta.textContent = formatBytes(entry.size || 0);
        meta.title = entry.modifiedAt
          ? new Date(entry.modifiedAt).toLocaleString()
          : "";
      } else if (!isParent && entry.isDir) {
        meta.textContent = "";
      }
      row.appendChild(meta);

      // Double-click: navigate into directory
      if (entry.isDir) {
        row.addEventListener("dblclick", function () {
          var target = isParent
            ? parentPath(self.panels.get(sessionId).cwd)
            : entry.path;
          self._navigate(sessionId, target);
        });
      }

      // Single click: select (files only, or directories too for context actions)
      if (!isParent) {
        row.addEventListener("click", function (e) {
          e.stopPropagation();
          self._selectFile(sessionId, entry, row, e);
        });
      }

      return row;
    }

    _renderLoading(sessionId) {
      var list = this._getFileList(sessionId);
      if (!list) return;
      list.innerHTML = "";
      var s = createElement("div", "sftp-state");
      s.innerHTML =
        '<div class="sftp-spinner"></div><div class="sftp-state-msg">Loading…</div>';
      list.appendChild(s);
      var ctxBar = this._getCtxBar(sessionId);
      if (ctxBar) ctxBar.style.display = "none";
    }

    _renderError(sessionId, msg) {
      var list = this._getFileList(sessionId);
      if (!list) return;
      list.innerHTML = "";
      var s = createElement("div", "sftp-state");
      s.innerHTML =
        '<div class="sftp-state-icon">⚠</div>' +
        '<div class="sftp-state-msg">' +
        escapeHtml(msg) +
        "</div>" +
        '<button class="btn btn-sm btn-secondary sftp-state-retry">Retry</button>';
      var self = this;
      var retryBtn = s.querySelector(".sftp-state-retry");
      if (retryBtn) {
        retryBtn.addEventListener("click", function () {
          self._refresh(sessionId);
        });
      }
      list.appendChild(s);
    }

    /* ── Selection & context actions ──────────────────────────────── */

    _selectFile(sessionId, entry, rowEl, event) {
      var state = this.panels.get(sessionId);
      if (!state) return;

      // Initialise multi-select set if needed
      if (!state.selectedFiles) state.selectedFiles = new Map();

      var multiKey = event && (event.metaKey || event.ctrlKey);

      if (multiKey) {
        // Toggle this entry in the multi-selection
        if (state.selectedFiles.has(entry.path)) {
          state.selectedFiles.delete(entry.path);
          rowEl.classList.remove("is-selected");
        } else {
          state.selectedFiles.set(entry.path, entry);
          rowEl.classList.add("is-selected");
        }
      } else {
        // Single click without modifier — clear all, select this one
        state.el
          .querySelectorAll(".sftp-file-row.is-selected")
          .forEach(function (r) {
            r.classList.remove("is-selected");
          });

        // If clicking the same entry (and it was the only selection), deselect
        if (
          state.selectedFiles.size === 1 &&
          state.selectedFiles.has(entry.path)
        ) {
          state.selectedFiles.clear();
          state.selected = null;
          var ctxBar = this._getCtxBar(sessionId);
          if (ctxBar) ctxBar.style.display = "none";
          return;
        }

        state.selectedFiles.clear();
        state.selectedFiles.set(entry.path, entry);
        rowEl.classList.add("is-selected");
      }

      state.selected = state.selectedFiles.size > 0 ? entry.path : null;
      this._showContextBar(sessionId, null);
    }

    _showContextBar(sessionId, _unused) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      var ctxBar = this._getCtxBar(sessionId);
      if (!ctxBar) return;

      if (!state.selectedFiles || state.selectedFiles.size === 0) {
        ctxBar.style.display = "none";
        return;
      }

      ctxBar.innerHTML = "";
      ctxBar.style.display = "flex";

      var self = this;
      var entries = Array.from(state.selectedFiles.values());
      var count = entries.length;
      var isSingle = count === 1;
      var entry = entries[0];

      var label = createElement("span", "sftp-ctx-label", {
        textContent: isSingle ? entry.name : count + " items selected",
        title: isSingle
          ? entry.path
          : entries
              .map(function (e) {
                return e.name;
              })
              .join(", "),
      });
      ctxBar.appendChild(label);

      // Download button — only for files (single or batch)
      var downloadableFiles = entries.filter(function (e) {
        return !e.isDir;
      });
      if (downloadableFiles.length > 0) {
        var dlLabel =
          downloadableFiles.length === 1
            ? "↓ Download"
            : "↓ Download (" + downloadableFiles.length + ")";
        var dlBtn = createElement("button", "sftp-ctx-btn is-accent", {
          textContent: dlLabel,
        });
        dlBtn.addEventListener("click", function () {
          self._downloadBatch(sessionId, downloadableFiles);
        });
        ctxBar.appendChild(dlBtn);
      }

      if (isSingle) {
        var renameBtn = createElement("button", "sftp-ctx-btn", {
          textContent: "✎ Rename",
        });
        renameBtn.addEventListener("click", function () {
          self._promptRename(sessionId, entry);
        });
        ctxBar.appendChild(renameBtn);
      }

      var delLabel = isSingle ? "✕ Delete" : "✕ Delete (" + count + ")";
      var delBtn = createElement("button", "sftp-ctx-btn is-danger", {
        textContent: delLabel,
      });
      delBtn.addEventListener("click", function () {
        if (isSingle) {
          self._confirmDelete(sessionId, entry);
        } else {
          self._confirmDeleteBatch(sessionId, entries);
        }
      });
      ctxBar.appendChild(delBtn);
    }

    /* ── File operations ──────────────────────────────────────────── */

    async _download(sessionId, filePath) {
      try {
        await window.NexusTerminal.wailsService.SFTPDownload(sessionId, filePath);
        this.app.notifications.success("Download started");
      } catch (err) {
        this.app.notifications.error(
          "Download failed: " + (err.message || err),
        );
      }
    }

    async _upload(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      try {
        await window.NexusTerminal.wailsService.SFTPUpload(sessionId, state.cwd);
        this.app.notifications.success("Upload complete");
        await this._refresh(sessionId);
      } catch (err) {
        // User may have cancelled the dialog — only show error if meaningful
        var msg = err.message || String(err);
        if (msg && msg.toLowerCase().indexOf("cancel") === -1) {
          this.app.notifications.error("Upload failed: " + msg);
        }
      }
    }

    async _mkdir(sessionId, name) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      var fullPath = pathJoin(state.cwd, name);
      try {
        await window.NexusTerminal.wailsService.SFTPMkdir(sessionId, fullPath);
        this.app.notifications.success("Directory created");
        await this._refresh(sessionId);
      } catch (err) {
        this.app.notifications.error("mkdir failed: " + (err.message || err));
      }
    }

    async _rename(sessionId, oldPath, newName) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      var newPath = pathJoin(parentPath(oldPath), newName);
      try {
        await window.NexusTerminal.wailsService.SFTPRename(sessionId, oldPath, newPath);
        this.app.notifications.success("Renamed");
        await this._refresh(sessionId);
      } catch (err) {
        this.app.notifications.error("Rename failed: " + (err.message || err));
      }
    }

    async _delete(sessionId, entry) {
      try {
        await window.NexusTerminal.wailsService.SFTPRemove(sessionId, entry.path);
        this.app.notifications.success(
          (entry.isDir ? "Directory" : "File") + " deleted",
        );
        await this._refresh(sessionId);
      } catch (err) {
        this.app.notifications.error("Delete failed: " + (err.message || err));
      }
    }

    async _downloadBatch(sessionId, files) {
      this.app.notifications.info("Downloading " + files.length + " files…");
      var failed = 0;
      for (var i = 0; i < files.length; i++) {
        try {
          await window.NexusTerminal.wailsService.SFTPDownload(sessionId, files[i].path);
        } catch (err) {
          failed++;
          this.app.notifications.error(
            "Download failed: " + files[i].name + " — " + (err.message || err),
          );
        }
      }
      if (failed === 0) {
        this.app.notifications.success(
          "All " + files.length + " files downloaded",
        );
      }
    }

    _confirmDeleteBatch(sessionId, entries) {
      this._showConfirmModal({
        title: "Delete " + entries.length + " Items",
        message:
          "Permanently delete <strong>" +
          entries.length +
          " selected items</strong>?",
        confirmLabel: "Delete All",
        isDanger: true,
        onConfirm: function () {
          this._deleteBatch(sessionId, entries);
        }.bind(this),
      });
    }

    async _deleteBatch(sessionId, entries) {
      var failed = 0;
      for (var i = 0; i < entries.length; i++) {
        try {
          await window.NexusTerminal.wailsService.SFTPRemove(sessionId, entries[i].path);
        } catch (err) {
          failed++;
          this.app.notifications.error(
            "Delete failed: " + entries[i].name + " — " + (err.message || err),
          );
        }
      }
      if (failed === 0) {
        this.app.notifications.success("Deleted " + entries.length + " items");
      }
      await this._refresh(sessionId);
    }

    /* ── Modal dialogs (no prompt/confirm/alert) ──────────────────── */

    _promptMkdir(sessionId) {
      this._showInputModal({
        title: "New Folder",
        placeholder: "Folder name",
        confirmLabel: "Create",
        onConfirm: function (value) {
          if (value.trim()) this._mkdir(sessionId, value.trim());
        }.bind(this),
      });
    }

    _promptRename(sessionId, entry) {
      this._showInputModal({
        title: "Rename",
        placeholder: "New name",
        initialValue: entry.name,
        confirmLabel: "Rename",
        onConfirm: function (value) {
          var v = value.trim();
          if (v && v !== entry.name) this._rename(sessionId, entry.path, v);
        }.bind(this),
      });
    }

    _confirmDelete(sessionId, entry) {
      this._showConfirmModal({
        title: "Delete " + (entry.isDir ? "Folder" : "File"),
        message:
          "Permanently delete <strong>" + escapeHtml(entry.name) + "</strong>?",
        confirmLabel: "Delete",
        isDanger: true,
        onConfirm: function () {
          this._delete(sessionId, entry);
        }.bind(this),
      });
    }

    /**
     * Displays a modal with a text input field.
     */
    _showInputModal(opts) {
      var backdrop = createElement("div", "modal-backdrop");
      var modal = createElement("div", "modal modal-sm");
      modal.innerHTML =
        '<div class="modal-header">' +
        '<span class="modal-title">' +
        escapeHtml(opts.title) +
        "</span>" +
        '<button class="modal-close btn btn-icon btn-xs btn-ghost">✕</button>' +
        "</div>" +
        '<div class="modal-body">' +
        '<input class="sftp-inline-input" type="text" placeholder="' +
        escapeHtml(opts.placeholder || "") +
        '"' +
        (opts.initialValue
          ? ' value="' + escapeHtml(opts.initialValue) + '"'
          : "") +
        " />" +
        "</div>" +
        '<div class="modal-footer">' +
        '<button class="btn btn-sm btn-ghost sftp-modal-cancel">Cancel</button>' +
        '<button class="btn btn-sm btn-primary sftp-modal-confirm">' +
        escapeHtml(opts.confirmLabel || "OK") +
        "</button>" +
        "</div>";

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);

      var input = modal.querySelector(".sftp-inline-input");
      requestAnimationFrame(function () {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
        input.focus();
        input.select();
      });

      var closed = false;
      function close(confirmed) {
        if (closed) return;
        closed = true;
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(function () {
          backdrop.remove();
        }, 200);
        if (confirmed && opts.onConfirm) {
          opts.onConfirm(input.value);
        }
      }

      modal
        .querySelector(".modal-close")
        .addEventListener("click", function () {
          close(false);
        });
      modal
        .querySelector(".sftp-modal-cancel")
        .addEventListener("click", function () {
          close(false);
        });
      modal
        .querySelector(".sftp-modal-confirm")
        .addEventListener("click", function () {
          close(true);
        });
      backdrop.addEventListener("click", function (e) {
        if (e.target === backdrop) close(false);
      });
      input.addEventListener("keydown", function (e) {
        if (e.key === "Enter") close(true);
        if (e.key === "Escape") close(false);
      });
    }

    /**
     * Displays a confirmation modal (no native confirm).
     */
    _showConfirmModal(opts) {
      var backdrop = createElement("div", "modal-backdrop");
      var modal = createElement("div", "modal modal-sm confirm-dialog");
      modal.innerHTML =
        '<div class="modal-header">' +
        '<span class="modal-title">' +
        escapeHtml(opts.title) +
        "</span>" +
        '<button class="modal-close btn btn-icon btn-xs btn-ghost">✕</button>' +
        "</div>" +
        '<div class="modal-body">' +
        "<p>" +
        (opts.message || "") +
        "</p>" +
        "</div>" +
        '<div class="modal-footer">' +
        '<button class="btn btn-sm btn-ghost sftp-modal-cancel">Cancel</button>' +
        '<button class="btn btn-sm ' +
        (opts.isDanger ? "btn-danger-filled" : "btn-primary") +
        ' sftp-modal-confirm">' +
        escapeHtml(opts.confirmLabel || "Confirm") +
        "</button>" +
        "</div>";

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);

      requestAnimationFrame(function () {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
      });

      var closed = false;
      function close(confirmed) {
        if (closed) return;
        closed = true;
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(function () {
          backdrop.remove();
        }, 200);
        if (confirmed && opts.onConfirm) {
          opts.onConfirm();
        }
      }

      modal
        .querySelector(".modal-close")
        .addEventListener("click", function () {
          close(false);
        });
      modal
        .querySelector(".sftp-modal-cancel")
        .addEventListener("click", function () {
          close(false);
        });
      modal
        .querySelector(".sftp-modal-confirm")
        .addEventListener("click", function () {
          close(true);
        });
      backdrop.addEventListener("click", function (e) {
        if (e.target === backdrop) close(false);
      });
    }

    /* ── Go to path & Bookmarks ───────────────────────────────────── */

    _promptGoToPath(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;
      this._showInputModal({
        title: "Go to Path",
        placeholder: "/path/to/directory",
        initialValue: state.cwd,
        confirmLabel: "Navigate",
        onConfirm: function (value) {
          var path = value.trim();
          if (path) this._navigate(sessionId, path);
        }.bind(this),
      });
    }

    _showBookmarks(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;

      var self = this;
      var bookmarks = this._getBookmarks(sessionId);

      var backdrop = createElement("div", "modal-backdrop");
      var modal = createElement("div", "modal modal-sm");

      var listHtml = "";
      if (bookmarks.length === 0) {
        listHtml =
          '<div style="color:var(--text-tertiary);font-size:var(--text-sm);text-align:center;padding:var(--space-3);">No bookmarks yet. Navigate to a directory and click "Bookmark Current".</div>';
      } else {
        for (var i = 0; i < bookmarks.length; i++) {
          listHtml +=
            '<div class="sftp-bookmark-row" data-index="' +
            i +
            '" data-path="' +
            escapeHtml(bookmarks[i]) +
            '">';
          listHtml +=
            '<span style="flex:1;font-family:var(--font-mono);font-size:var(--text-xs);color:var(--text-primary);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' +
            escapeHtml(bookmarks[i]) +
            "</span>";
          listHtml +=
            '<button class="btn btn-xs btn-ghost sftp-bookmark-del" data-index="' +
            i +
            '" title="Remove">✕</button>';
          listHtml += "</div>";
        }
      }

      modal.innerHTML =
        '<div class="modal-header">' +
        '<span class="modal-title">Directory Bookmarks</span>' +
        '<button class="modal-close btn btn-icon btn-xs btn-ghost">✕</button>' +
        "</div>" +
        '<div class="modal-body">' +
        '<div class="sftp-bookmark-list">' +
        listHtml +
        "</div>" +
        "</div>" +
        '<div class="modal-footer">' +
        '<button class="btn btn-sm btn-ghost sftp-modal-cancel">Close</button>' +
        '<button class="btn btn-sm btn-primary sftp-bookmark-add">★ Bookmark Current</button>' +
        "</div>";

      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);

      requestAnimationFrame(function () {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
      });

      var closed = false;
      function close() {
        if (closed) return;
        closed = true;
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(function () {
          backdrop.remove();
        }, 200);
      }

      modal.querySelector(".modal-close").addEventListener("click", close);
      modal
        .querySelector(".sftp-modal-cancel")
        .addEventListener("click", close);
      backdrop.addEventListener("click", function (e) {
        if (e.target === backdrop) close();
      });

      // Navigate to bookmark on click
      modal.querySelectorAll(".sftp-bookmark-row").forEach(function (row) {
        row.addEventListener("click", function (e) {
          if (e.target.classList.contains("sftp-bookmark-del")) return;
          var path = row.dataset.path;
          close();
          self._navigate(sessionId, path);
        });
        row.addEventListener("mousedown", function (e) {
          e.preventDefault();
        });
      });

      // Delete bookmark
      modal.querySelectorAll(".sftp-bookmark-del").forEach(function (btn) {
        btn.addEventListener("click", function (e) {
          e.stopPropagation();
          var idx = parseInt(btn.dataset.index, 10);
          self._removeBookmark(sessionId, idx);
          close();
          self._showBookmarks(sessionId);
        });
      });

      // Add current directory as bookmark
      modal
        .querySelector(".sftp-bookmark-add")
        .addEventListener("click", function () {
          self._addBookmark(sessionId, state.cwd);
          close();
          self._showBookmarks(sessionId);
        });
    }

    _getBookmarks(sessionId) {
      try {
        var key = "sftp-bookmarks-" + sessionId;
        var stored = localStorage.getItem(key);
        return stored ? JSON.parse(stored) : [];
      } catch (_) {
        return [];
      }
    }

    _addBookmark(sessionId, path) {
      var bookmarks = this._getBookmarks(sessionId);
      if (bookmarks.indexOf(path) === -1) {
        bookmarks.push(path);
        localStorage.setItem(
          "sftp-bookmarks-" + sessionId,
          JSON.stringify(bookmarks),
        );
        this.app.notifications.success("Bookmarked: " + path);
      } else {
        this.app.notifications.info("Already bookmarked");
      }
    }

    _removeBookmark(sessionId, index) {
      var bookmarks = this._getBookmarks(sessionId);
      bookmarks.splice(index, 1);
      localStorage.setItem(
        "sftp-bookmarks-" + sessionId,
        JSON.stringify(bookmarks),
      );
    }

    /* ── Transfer progress queue ─────────────────────────────────── */

    /**
     * Handles a transfer progress event from the backend.
     * @param {Object} payload - { sessionID, direction, transferred, total, path, done }
     */
    handleTransferProgress(payload) {
      var state = this.panels.get(payload.sessionID);
      if (!state) {
        // Panel not open — still track so we can show a notification on completion
        if (payload.done) {
          var dir =
            payload.direction === "upload" ? "↑ Uploaded" : "↓ Downloaded";
          var name = payload.path.split("/").pop() || payload.path;
          this.app.notifications.success(dir + " " + name);
        }
        return;
      }

      // Initialise transfers map if needed
      if (!state.transfers) state.transfers = new Map();

      var key = payload.direction + ":" + payload.path;

      if (payload.done) {
        state.transfers.delete(key);
        this._renderTransferQueue(payload.sessionID);
        // Refresh file list after upload completes
        if (payload.direction === "upload") {
          this._refresh(payload.sessionID);
        }
        return;
      }

      state.transfers.set(key, {
        direction: payload.direction,
        path: payload.path,
        name: payload.path.split("/").pop() || payload.path,
        transferred: payload.transferred,
        total: payload.total,
      });

      this._renderTransferQueue(payload.sessionID);
    }

    /**
     * Renders the transfer queue panel showing active transfers.
     */
    _renderTransferQueue(sessionId) {
      var state = this.panels.get(sessionId);
      if (!state) return;

      var queue = state.el.querySelector(".sftp-transfer-queue");

      if (!state.transfers || state.transfers.size === 0) {
        if (queue) queue.remove();
        return;
      }

      if (!queue) {
        queue = createElement("div", "sftp-transfer-queue");
        // Insert between toolbar and file-list
        var fileList = state.el.querySelector(".sftp-file-list");
        state.el.insertBefore(queue, fileList);
      }

      queue.innerHTML = "";

      var header = createElement("div", "sftp-transfer-header");
      header.textContent = "Transfers (" + state.transfers.size + ")";
      queue.appendChild(header);

      var self = this;
      state.transfers.forEach(function (transfer) {
        var row = createElement("div", "sftp-transfer-row");

        var icon = createElement("span", "sftp-transfer-icon");
        icon.textContent = transfer.direction === "upload" ? "↑" : "↓";
        row.appendChild(icon);

        var info = createElement("div", "sftp-transfer-info");

        var nameEl = createElement("span", "sftp-transfer-name", {
          textContent: transfer.name,
          title: transfer.path,
        });
        info.appendChild(nameEl);

        var progress = createElement("div", "sftp-transfer-progress");
        var bar = createElement("div", "sftp-transfer-bar");
        var pct =
          transfer.total > 0
            ? Math.min(
                100,
                Math.round((transfer.transferred / transfer.total) * 100),
              )
            : 0;
        bar.style.width = pct + "%";
        progress.appendChild(bar);
        info.appendChild(progress);

        var meta = createElement("span", "sftp-transfer-meta");
        var transferredStr = formatBytes(transfer.transferred);
        var totalStr = transfer.total > 0 ? formatBytes(transfer.total) : "?";
        meta.textContent =
          transferredStr + " / " + totalStr + " (" + pct + "%)";
        info.appendChild(meta);

        row.appendChild(info);
        queue.appendChild(row);
      });
    }

    /* ── Resize handle ────────────────────────────────────────────── */

    _setupResize(sessionId, state) {
      var handle = state.handle;
      var panel = state.el;
      var self = this;
      var startX, startWidth;

      function onMouseMove(e) {
        var delta = startX - e.clientX;
        var newWidth = Math.max(
          180,
          Math.min(startWidth + delta, window.innerWidth * 0.5),
        );
        panel.style.width = newWidth + "px";
      }

      function onMouseUp() {
        cleanupResize();
        self._refitTerminal(sessionId);
      }

      function cleanupResize() {
        handle.classList.remove("is-dragging");
        document.body.style.userSelect = "";
        document.body.style.cursor = "";
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
      }

      state.cleanupResize = cleanupResize;

      handle.addEventListener("mousedown", function (e) {
        e.preventDefault();
        startX = e.clientX;
        startWidth = panel.offsetWidth;
        handle.classList.add("is-dragging");
        document.body.style.userSelect = "none";
        document.body.style.cursor = "col-resize";
        document.addEventListener("mousemove", onMouseMove);
        document.addEventListener("mouseup", onMouseUp);
      });
    }

    /* ── Terminal refit ────────────────────────────────────────────── */

    _refitTerminal(sessionId) {
      if (
        this.app.terminals &&
        typeof this.app.terminals.fitSession === "function"
      ) {
        // Small delay lets the layout settle before measuring
        setTimeout(
          function () {
            this.app.terminals.fitSession(sessionId);
          }.bind(this),
          50,
        );
      }
    }
  }

  /* ── Export ────────────────────────────────────────────────────────── */
  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.SFTPBrowser = SFTPBrowser;
})();
