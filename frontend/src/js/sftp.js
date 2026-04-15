(function() {
/**
 * Nexus Terminal — SFTP File Browser
 *
 * Provides a right-side panel for browsing, uploading, downloading,
 * renaming and deleting remote files alongside the terminal.
 */

const {
    createElement, escapeHtml, formatBytes, relativeTime, dismissContextMenus,
} = window.NexusTerminal;

/* ── Helpers ──────────────────────────────────────────────────────── */

function pathJoin(base, name) {
    if (base === '/') return '/' + name;
    return base.replace(/\/+$/, '') + '/' + name;
}

function pathSegments(p) {
    const parts = p.split('/').filter(Boolean);
    var segs = [{ label: '/', path: '/' }];
    var acc = '';
    for (var i = 0; i < parts.length; i++) {
        acc += '/' + parts[i];
        segs.push({ label: parts[i], path: acc });
    }
    return segs;
}

function parentPath(p) {
    if (p === '/') return '/';
    var trimmed = p.replace(/\/+$/, '');
    var idx = trimmed.lastIndexOf('/');
    return idx <= 0 ? '/' : trimmed.substring(0, idx);
}

function fileIcon(entry) {
    if (entry.isDir) return '📁';
    var ext = (entry.name.split('.').pop() || '').toLowerCase();
    switch (ext) {
        case 'jpg': case 'jpeg': case 'png': case 'gif': case 'svg': case 'webp': return '🖼';
        case 'mp3': case 'wav': case 'ogg': case 'flac': return '🎵';
        case 'mp4': case 'avi': case 'mkv': case 'mov': return '🎬';
        case 'zip': case 'tar': case 'gz': case 'bz2': case 'xz': case '7z': return '📦';
        case 'sh': case 'bash': case 'zsh': return '⚙';
        case 'py': case 'js': case 'ts': case 'go': case 'rs': case 'c': case 'h': case 'cpp': case 'java': return '◇';
        case 'md': case 'txt': case 'log': case 'csv': return '📝';
        case 'json': case 'yaml': case 'yml': case 'toml': case 'xml': return '⚙';
        case 'key': case 'pem': case 'crt': case 'pub': return '🔑';
        default: return '📄';
    }
}

function modeString(mode) {
    if (!mode && mode !== 0) return '';
    var s = '';
    var perms = ['---', '--x', '-w-', '-wx', 'r--', 'r-x', 'rw-', 'rwx'];
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

        var sessionPanel = document.getElementById('panel-' + sessionId);
        if (!sessionPanel) {
            this.app.notifications.error('Cannot open file browser: session panel not found.');
            return;
        }

        var state = {
            el: null,
            handle: null,
            cwd: '/',
            files: [],
            selected: null,
            loading: false,
        };

        // Build the panel DOM
        var panel = createElement('div', 'sftp-panel');
        var handle = createElement('div', 'sftp-resize-handle');
        panel.appendChild(handle);
        state.el = panel;
        state.handle = handle;

        // Toolbar
        var toolbar = this._buildToolbar(sessionId, state);
        panel.appendChild(toolbar);

        // File list container
        var fileList = createElement('div', 'sftp-file-list');
        panel.appendChild(fileList);

        // Context action bar (hidden initially)
        var ctxBar = createElement('div', 'sftp-context-bar');
        ctxBar.style.display = 'none';
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

    /* ── Toolbar ──────────────────────────────────────────────────── */

    _buildToolbar(sessionId, state) {
        var toolbar = createElement('div', 'sftp-toolbar');

        var breadcrumb = createElement('div', 'sftp-breadcrumb');
        breadcrumb.dataset.role = 'breadcrumb';
        toolbar.appendChild(breadcrumb);

        var actions = createElement('div', 'sftp-toolbar-actions');

        var mkdirBtn = this._toolbarBtn('＋', 'New folder', 'is-accent');
        mkdirBtn.addEventListener('click', () => this._promptMkdir(sessionId));
        actions.appendChild(mkdirBtn);

        var uploadBtn = this._toolbarBtn('↑', 'Upload file', 'is-accent');
        uploadBtn.addEventListener('click', () => this._upload(sessionId));
        actions.appendChild(uploadBtn);

        var refreshBtn = this._toolbarBtn('⟳', 'Refresh');
        refreshBtn.addEventListener('click', () => this._refresh(sessionId));
        actions.appendChild(refreshBtn);

        var closeBtn = this._toolbarBtn('✕', 'Close', 'is-close');
        closeBtn.addEventListener('click', () => this.close(sessionId));
        actions.appendChild(closeBtn);

        toolbar.appendChild(actions);
        return toolbar;
    }

    _toolbarBtn(label, title, extraClass) {
        var cls = 'sftp-toolbar-btn' + (extraClass ? ' ' + extraClass : '');
        return createElement('button', cls, { textContent: label, title: title });
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
                path = await window.go.api.Service.SFTPPwd(sessionId);
            }
            var files = await window.go.api.Service.SFTPList(sessionId, path);
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
        return files.slice().sort(function(a, b) {
            // Directories first
            if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
            return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
        });
    }

    /* ── Rendering ────────────────────────────────────────────────── */

    _getFileList(sessionId) {
        var state = this.panels.get(sessionId);
        if (!state) return null;
        return state.el.querySelector('.sftp-file-list');
    }

    _getCtxBar(sessionId) {
        var state = this.panels.get(sessionId);
        if (!state) return null;
        return state.el.querySelector('.sftp-context-bar');
    }

    _renderBreadcrumb(sessionId) {
        var state = this.panels.get(sessionId);
        if (!state) return;
        var container = state.el.querySelector('[data-role="breadcrumb"]');
        container.innerHTML = '';

        var segs = pathSegments(state.cwd);
        var self = this;
        for (var i = 0; i < segs.length; i++) {
            if (i > 0) {
                container.appendChild(createElement('span', 'sftp-breadcrumb-sep', { textContent: '›' }));
            }
            var btn = createElement('button', 'sftp-breadcrumb-segment', {
                textContent: segs[i].label,
                title: segs[i].path,
            });
            (function(p) {
                btn.addEventListener('click', function() { self._navigate(sessionId, p); });
            })(segs[i].path);
            container.appendChild(btn);
        }
    }

    _renderFiles(sessionId) {
        var state = this.panels.get(sessionId);
        if (!state) return;
        var list = this._getFileList(sessionId);
        list.innerHTML = '';

        // Hide context bar
        var ctxBar = this._getCtxBar(sessionId);
        if (ctxBar) ctxBar.style.display = 'none';

        // Parent directory row if not at root
        if (state.cwd !== '/') {
            var parentRow = this._buildFileRow({
                name: '..',
                isDir: true,
                path: parentPath(state.cwd),
                size: 0,
                modifiedAt: 0,
            }, sessionId, true);
            parentRow.classList.add('is-parent');
            list.appendChild(parentRow);
        }

        if (state.files.length === 0) {
            var empty = createElement('div', 'sftp-state');
            empty.innerHTML = '<div class="sftp-state-icon">📂</div><div class="sftp-state-msg">Empty directory</div>';
            list.appendChild(empty);
            return;
        }

        var self = this;
        for (var i = 0; i < state.files.length; i++) {
            list.appendChild(this._buildFileRow(state.files[i], sessionId, false));
        }
    }

    _buildFileRow(entry, sessionId, isParent) {
        var row = createElement('div', 'sftp-file-row');
        var self = this;

        // Icon
        var icon = createElement('span', 'sftp-file-icon' + (entry.isDir ? ' is-dir' : ''));
        icon.textContent = isParent ? '↩' : fileIcon(entry);
        row.appendChild(icon);

        // Name
        var name = createElement('span', 'sftp-file-name', {
            textContent: entry.name,
            title: entry.name,
        });
        row.appendChild(name);

        // Meta (size or date)
        var meta = createElement('span', 'sftp-file-meta');
        if (!isParent && !entry.isDir) {
            meta.textContent = formatBytes(entry.size || 0);
            meta.title = entry.modifiedAt ? new Date(entry.modifiedAt).toLocaleString() : '';
        } else if (!isParent && entry.isDir) {
            meta.textContent = '';
        }
        row.appendChild(meta);

        // Double-click: navigate into directory
        if (entry.isDir) {
            row.addEventListener('dblclick', function() {
                var target = isParent ? parentPath(self.panels.get(sessionId).cwd) : entry.path;
                self._navigate(sessionId, target);
            });
        }

        // Single click: select (files only, or directories too for context actions)
        if (!isParent) {
            row.addEventListener('click', function(e) {
                e.stopPropagation();
                self._selectFile(sessionId, entry, row);
            });
        }

        return row;
    }

    _renderLoading(sessionId) {
        var list = this._getFileList(sessionId);
        if (!list) return;
        list.innerHTML = '';
        var s = createElement('div', 'sftp-state');
        s.innerHTML = '<div class="sftp-spinner"></div><div class="sftp-state-msg">Loading…</div>';
        list.appendChild(s);
        var ctxBar = this._getCtxBar(sessionId);
        if (ctxBar) ctxBar.style.display = 'none';
    }

    _renderError(sessionId, msg) {
        var list = this._getFileList(sessionId);
        if (!list) return;
        list.innerHTML = '';
        var s = createElement('div', 'sftp-state');
        s.innerHTML =
            '<div class="sftp-state-icon">⚠</div>' +
            '<div class="sftp-state-msg">' + escapeHtml(msg) + '</div>' +
            '<button class="btn btn-sm btn-secondary sftp-state-retry">Retry</button>';
        var self = this;
        var retryBtn = s.querySelector('.sftp-state-retry');
        if (retryBtn) {
            retryBtn.addEventListener('click', function() { self._refresh(sessionId); });
        }
        list.appendChild(s);
    }

    /* ── Selection & context actions ──────────────────────────────── */

    _selectFile(sessionId, entry, rowEl) {
        var state = this.panels.get(sessionId);
        if (!state) return;

        // Deselect previous
        var prev = state.el.querySelector('.sftp-file-row.is-selected');
        if (prev) prev.classList.remove('is-selected');

        // If clicking the same entry, deselect
        if (state.selected === entry.path) {
            state.selected = null;
            var ctxBar = this._getCtxBar(sessionId);
            if (ctxBar) ctxBar.style.display = 'none';
            return;
        }

        state.selected = entry.path;
        rowEl.classList.add('is-selected');
        this._showContextBar(sessionId, entry);
    }

    _showContextBar(sessionId, entry) {
        var ctxBar = this._getCtxBar(sessionId);
        if (!ctxBar) return;

        ctxBar.innerHTML = '';
        ctxBar.style.display = 'flex';

        var label = createElement('span', 'sftp-ctx-label', {
            textContent: entry.name,
            title: entry.path,
        });
        ctxBar.appendChild(label);

        var self = this;

        if (!entry.isDir) {
            var dlBtn = createElement('button', 'sftp-ctx-btn is-accent', { textContent: '↓ Download' });
            dlBtn.addEventListener('click', function() { self._download(sessionId, entry.path); });
            ctxBar.appendChild(dlBtn);
        }

        var renameBtn = createElement('button', 'sftp-ctx-btn', { textContent: '✎ Rename' });
        renameBtn.addEventListener('click', function() { self._promptRename(sessionId, entry); });
        ctxBar.appendChild(renameBtn);

        var delBtn = createElement('button', 'sftp-ctx-btn is-danger', { textContent: '✕ Delete' });
        delBtn.addEventListener('click', function() { self._confirmDelete(sessionId, entry); });
        ctxBar.appendChild(delBtn);
    }

    /* ── File operations ──────────────────────────────────────────── */

    async _download(sessionId, filePath) {
        try {
            await window.go.api.Service.SFTPDownload(sessionId, filePath);
            this.app.notifications.success('Download started');
        } catch (err) {
            this.app.notifications.error('Download failed: ' + (err.message || err));
        }
    }

    async _upload(sessionId) {
        var state = this.panels.get(sessionId);
        if (!state) return;
        try {
            await window.go.api.Service.SFTPUpload(sessionId, state.cwd);
            this.app.notifications.success('Upload complete');
            await this._refresh(sessionId);
        } catch (err) {
            // User may have cancelled the dialog — only show error if meaningful
            var msg = err.message || String(err);
            if (msg && msg.toLowerCase().indexOf('cancel') === -1) {
                this.app.notifications.error('Upload failed: ' + msg);
            }
        }
    }

    async _mkdir(sessionId, name) {
        var state = this.panels.get(sessionId);
        if (!state) return;
        var fullPath = pathJoin(state.cwd, name);
        try {
            await window.go.api.Service.SFTPMkdir(sessionId, fullPath);
            this.app.notifications.success('Directory created');
            await this._refresh(sessionId);
        } catch (err) {
            this.app.notifications.error('mkdir failed: ' + (err.message || err));
        }
    }

    async _rename(sessionId, oldPath, newName) {
        var state = this.panels.get(sessionId);
        if (!state) return;
        var newPath = pathJoin(parentPath(oldPath), newName);
        try {
            await window.go.api.Service.SFTPRename(sessionId, oldPath, newPath);
            this.app.notifications.success('Renamed');
            await this._refresh(sessionId);
        } catch (err) {
            this.app.notifications.error('Rename failed: ' + (err.message || err));
        }
    }

    async _delete(sessionId, entry) {
        try {
            await window.go.api.Service.SFTPRemove(sessionId, entry.path);
            this.app.notifications.success((entry.isDir ? 'Directory' : 'File') + ' deleted');
            await this._refresh(sessionId);
        } catch (err) {
            this.app.notifications.error('Delete failed: ' + (err.message || err));
        }
    }

    /* ── Modal dialogs (no prompt/confirm/alert) ──────────────────── */

    _promptMkdir(sessionId) {
        this._showInputModal({
            title: 'New Folder',
            placeholder: 'Folder name',
            confirmLabel: 'Create',
            onConfirm: function(value) {
                if (value.trim()) this._mkdir(sessionId, value.trim());
            }.bind(this),
        });
    }

    _promptRename(sessionId, entry) {
        this._showInputModal({
            title: 'Rename',
            placeholder: 'New name',
            initialValue: entry.name,
            confirmLabel: 'Rename',
            onConfirm: function(value) {
                var v = value.trim();
                if (v && v !== entry.name) this._rename(sessionId, entry.path, v);
            }.bind(this),
        });
    }

    _confirmDelete(sessionId, entry) {
        this._showConfirmModal({
            title: 'Delete ' + (entry.isDir ? 'Folder' : 'File'),
            message: 'Permanently delete <strong>' + escapeHtml(entry.name) + '</strong>?',
            confirmLabel: 'Delete',
            isDanger: true,
            onConfirm: function() {
                this._delete(sessionId, entry);
            }.bind(this),
        });
    }

    /**
     * Displays a modal with a text input field.
     */
    _showInputModal(opts) {
        var backdrop = createElement('div', 'modal-backdrop');
        var modal = createElement('div', 'modal modal-sm');
        modal.innerHTML =
            '<div class="modal-header">' +
                '<span class="modal-title">' + escapeHtml(opts.title) + '</span>' +
                '<button class="modal-close btn btn-icon btn-xs btn-ghost">✕</button>' +
            '</div>' +
            '<div class="modal-body">' +
                '<input class="sftp-inline-input" type="text" placeholder="' + escapeHtml(opts.placeholder || '') + '"' +
                    (opts.initialValue ? ' value="' + escapeHtml(opts.initialValue) + '"' : '') + ' />' +
            '</div>' +
            '<div class="modal-footer">' +
                '<button class="btn btn-sm btn-ghost sftp-modal-cancel">Cancel</button>' +
                '<button class="btn btn-sm btn-primary sftp-modal-confirm">' + escapeHtml(opts.confirmLabel || 'OK') + '</button>' +
            '</div>';

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);

        var input = modal.querySelector('.sftp-inline-input');
        requestAnimationFrame(function() {
            backdrop.classList.add('is-visible');
            modal.classList.add('is-visible');
            input.focus();
            input.select();
        });

        var closed = false;
        function close(confirmed) {
            if (closed) return;
            closed = true;
            backdrop.classList.remove('is-visible');
            modal.classList.remove('is-visible');
            setTimeout(function() { backdrop.remove(); }, 200);
            if (confirmed && opts.onConfirm) {
                opts.onConfirm(input.value);
            }
        }

        modal.querySelector('.modal-close').addEventListener('click', function() { close(false); });
        modal.querySelector('.sftp-modal-cancel').addEventListener('click', function() { close(false); });
        modal.querySelector('.sftp-modal-confirm').addEventListener('click', function() { close(true); });
        backdrop.addEventListener('click', function(e) { if (e.target === backdrop) close(false); });
        input.addEventListener('keydown', function(e) {
            if (e.key === 'Enter') close(true);
            if (e.key === 'Escape') close(false);
        });
    }

    /**
     * Displays a confirmation modal (no native confirm).
     */
    _showConfirmModal(opts) {
        var backdrop = createElement('div', 'modal-backdrop');
        var modal = createElement('div', 'modal modal-sm confirm-dialog');
        modal.innerHTML =
            '<div class="modal-header">' +
                '<span class="modal-title">' + escapeHtml(opts.title) + '</span>' +
                '<button class="modal-close btn btn-icon btn-xs btn-ghost">✕</button>' +
            '</div>' +
            '<div class="modal-body">' +
                '<p>' + (opts.message || '') + '</p>' +
            '</div>' +
            '<div class="modal-footer">' +
                '<button class="btn btn-sm btn-ghost sftp-modal-cancel">Cancel</button>' +
                '<button class="btn btn-sm ' + (opts.isDanger ? 'btn-danger-filled' : 'btn-primary') + ' sftp-modal-confirm">' +
                    escapeHtml(opts.confirmLabel || 'Confirm') +
                '</button>' +
            '</div>';

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);

        requestAnimationFrame(function() {
            backdrop.classList.add('is-visible');
            modal.classList.add('is-visible');
        });

        var closed = false;
        function close(confirmed) {
            if (closed) return;
            closed = true;
            backdrop.classList.remove('is-visible');
            modal.classList.remove('is-visible');
            setTimeout(function() { backdrop.remove(); }, 200);
            if (confirmed && opts.onConfirm) {
                opts.onConfirm();
            }
        }

        modal.querySelector('.modal-close').addEventListener('click', function() { close(false); });
        modal.querySelector('.sftp-modal-cancel').addEventListener('click', function() { close(false); });
        modal.querySelector('.sftp-modal-confirm').addEventListener('click', function() { close(true); });
        backdrop.addEventListener('click', function(e) { if (e.target === backdrop) close(false); });
    }

    /* ── Resize handle ────────────────────────────────────────────── */

    _setupResize(sessionId, state) {
        var handle = state.handle;
        var panel = state.el;
        var self = this;
        var startX, startWidth;

        function onMouseMove(e) {
            var delta = startX - e.clientX;
            var newWidth = Math.max(180, Math.min(startWidth + delta, window.innerWidth * 0.5));
            panel.style.width = newWidth + 'px';
        }

        function onMouseUp() {
            handle.classList.remove('is-dragging');
            document.body.style.userSelect = '';
            document.body.style.cursor = '';
            document.removeEventListener('mousemove', onMouseMove);
            document.removeEventListener('mouseup', onMouseUp);
            self._refitTerminal(sessionId);
        }

        handle.addEventListener('mousedown', function(e) {
            e.preventDefault();
            startX = e.clientX;
            startWidth = panel.offsetWidth;
            handle.classList.add('is-dragging');
            document.body.style.userSelect = 'none';
            document.body.style.cursor = 'col-resize';
            document.addEventListener('mousemove', onMouseMove);
            document.addEventListener('mouseup', onMouseUp);
        });
    }

    /* ── Terminal refit ────────────────────────────────────────────── */

    _refitTerminal(sessionId) {
        if (this.app.terminals && typeof this.app.terminals.fitSession === 'function') {
            // Small delay lets the layout settle before measuring
            setTimeout(function() {
                this.app.terminals.fitSession(sessionId);
            }.bind(this), 50);
        }
    }
}

/* ── Export ────────────────────────────────────────────────────────── */
window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.SFTPBrowser = SFTPBrowser;

})();
