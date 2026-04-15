(function() {
/**
 * Nexus Terminal — sidebar module.
 * Renders the profile list with search, sections (favourites, recent, all),
 * and context menus.
 */

const { createElement, escapeHtml, fuzzyMatch, protocolColour, dismissContextMenus, truncate } = window.NexusTerminal;

/** Maximum number of profiles shown in the "Recent" section. */
const RECENT_LIMIT = 5;

class Sidebar {
    /**
     * @param {App} app
     */
    constructor(app) {
        this.app = app;
        this.el = document.getElementById('sidebar-profiles');
        this.searchQuery = '';

        // Section collapse state
        this.collapsedSections = new Set();

        // Dismiss context menus on any outside click
        document.addEventListener('click', () => dismissContextMenus());
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
        this.el.innerHTML = '';

        const profiles = this._filteredProfiles();

        // Favourites
        const favourites = profiles.filter((p) => p.favourite);
        if (favourites.length > 0) {
            this._renderSection('★ Favourites', 'favourites', favourites);
        }

        // Recent
        const recent = profiles
            .filter((p) => p.lastConnected)
            .sort((a, b) => new Date(b.lastConnected) - new Date(a.lastConnected))
            .slice(0, RECENT_LIMIT);
        if (recent.length > 0) {
            this._renderSection('Recent', 'recent', recent);
        }

        // ── Group-based sections ─────────────────────────────────────
        // Collect distinct groups and sort them alphabetically.
        const groupMap = new Map();
        const ungrouped = [];

        for (const p of profiles) {
            const grp = (p.folder || '').trim();
            if (grp) {
                if (!groupMap.has(grp)) groupMap.set(grp, []);
                groupMap.get(grp).push(p);
            } else {
                ungrouped.push(p);
            }
        }

        // Render each group as its own collapsible section
        const sortedGroups = [...groupMap.keys()].sort((a, b) => a.localeCompare(b));
        for (const grp of sortedGroups) {
            this._renderSection(grp, `group:${grp}`, groupMap.get(grp));
        }

        // Ungrouped connections
        if (ungrouped.length > 0) {
            const label = sortedGroups.length > 0 ? 'Ungrouped' : 'All Connections';
            this._renderSection(label, 'ungrouped', ungrouped);
        }

        // Empty state
        if (profiles.length === 0) {
            const empty = createElement('div', 'sidebar-empty');
            empty.innerHTML = `
                <p class="sidebar-empty-text">No profiles found</p>
            `;
            this.el.appendChild(empty);
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
        this.el.querySelectorAll('.sidebar-item').forEach((item) => {
            item.classList.toggle('sidebar-item-active', item.dataset.profileId === profileId);
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
     * Renders a collapsible section of profiles.
     * @param {string} title
     * @param {string} key
     * @param {Array<Object>} profiles
     * @private
     */
    _renderSection(title, key, profiles) {
        const section = createElement('div', 'sidebar-section');
        const isCollapsed = this.collapsedSections.has(key);

        // Section header
        const header = createElement('div', 'sidebar-section-header');
        header.innerHTML = `
            <span class="section-chevron ${isCollapsed ? 'collapsed' : ''}">${isCollapsed ? '▸' : '▾'}</span>
            <span>${escapeHtml(title)}</span>
            <span class="section-count">${profiles.length}</span>
        `;
        // Prevent mousedown from starting a text selection gesture in WKWebView,
        // which would swallow the first click and require a double-click.
        header.addEventListener('mousedown', (e) => e.preventDefault());
        header.addEventListener('click', () => this.toggleSection(key));
        section.appendChild(header);

        // Items
        if (!isCollapsed) {
            const list = createElement('div', 'sidebar-list');
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
        const item = createElement('div', 'sidebar-item', {
            dataset: { profileId: profile.id },
        });

        // Highlight if this profile's session is active
        const activeSession = this.app.state.activeSessionId
            ? this.app.state.getSession(this.app.state.activeSessionId)
            : null;
        if (activeSession && activeSession.profileId === profile.id) {
            item.classList.add('sidebar-item-active');
        }

        // Protocol colour dot
        const dot = createElement('span', `protocol-indicator ${protocolColour(profile.protocol)}`);
        item.appendChild(dot);

        // Text content
        const textWrapper = createElement('div', 'sidebar-item-text');
        const nameEl = createElement('span', 'sidebar-item-name', {
            textContent: profile.displayName || profile.host || 'Untitled',
        });
        textWrapper.appendChild(nameEl);

        const subtitle = profile.host
            ? `${profile.host}:${profile.port}`
            : profile.protocol.toUpperCase();
        const subEl = createElement('span', 'sidebar-item-host', {
            textContent: truncate(subtitle, 30),
        });
        textWrapper.appendChild(subEl);
        item.appendChild(textWrapper);

        // Edit button (visible on hover)
        const editBtn = createElement('button', 'sidebar-item-edit', {
            textContent: '✎',
            title: 'Edit profile',
        });
        editBtn.addEventListener('click', (e) => {
            e.stopPropagation();
            this.app.profileEditor.openEdit(profile.id);
        });
        item.appendChild(editBtn);

        // Favourite star
        if (profile.favourite) {
            const star = createElement('span', 'sidebar-item-star is-favourite', { textContent: '★' });
            item.appendChild(star);
        }

        // Click → connect
        item.addEventListener('click', () => {
            this.app.connectProfile(profile.id);
        });

        // Double-click → edit
        item.addEventListener('dblclick', (e) => {
            e.stopPropagation();
            this.app.profileEditor.openEdit(profile.id);
        });

        // Right-click → context menu
        item.addEventListener('contextmenu', (e) => {
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

        const menu = createElement('div', 'context-menu');
        menu.style.left = `${x}px`;
        menu.style.top = `${y}px`;

        const items = [
            { label: 'Connect', action: () => this.app.connectProfile(profile.id) },
            { label: 'Edit', action: () => this.app.profileEditor.openEdit(profile.id) },
            { label: 'Duplicate', action: () => this._duplicateProfile(profile) },
            { divider: true },
            {
                label: profile.favourite ? 'Remove from Favourites' : 'Add to Favourites',
                action: () => this._toggleFavourite(profile),
            },
            { divider: true },
            {
                label: 'Delete',
                className: 'context-menu-danger',
                action: () => this._deleteProfile(profile),
            },
        ];

        for (const entry of items) {
            if (entry.divider) {
                menu.appendChild(createElement('div', 'context-menu-divider'));
                continue;
            }
            const btn = createElement('button', `context-menu-item ${entry.className || ''}`, {
                textContent: entry.label,
            });
            btn.addEventListener('click', () => {
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
            await window.go.api.Service.SetFavourite(profile.id, newVal);
            profile.favourite = newVal;
            this.app.state.updateProfile(profile);
            this.render();
        } catch (err) {
            this.app.notifications.error('Failed to update favourite: ' + err);
        }
    }

    /**
     * Duplicates a profile.
     * @param {Object} profile
     * @private
     */
    async _duplicateProfile(profile) {
        try {
            const dup = await window.go.api.Service.DuplicateProfile(profile.id);
            this.app.state.addProfile(dup);
            this.render();
            this.app.notifications.success(`Duplicated "${profile.displayName}"`);
        } catch (err) {
            this.app.notifications.error('Failed to duplicate profile: ' + err);
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
            await window.go.api.Service.DeleteProfile(profile.id);
            this.app.state.removeProfile(profile.id);
            this.render();
            this.app.notifications.success('Profile deleted');
        } catch (err) {
            this.app.notifications.error('Failed to delete profile: ' + err);
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
            const backdrop = createElement('div', 'modal-backdrop');
            const modal = createElement('div', 'modal modal-sm');
            const label = escapeHtml(profile.displayName || profile.host || 'Untitled');
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
                backdrop.classList.add('is-visible');
                modal.classList.add('is-visible');
            });

            const close = (val) => {
                backdrop.classList.remove('is-visible');
                modal.classList.remove('is-visible');
                setTimeout(() => backdrop.remove(), 200);
                resolve(val);
            };
            modal.querySelector('.modal-close').addEventListener('click', () => close(false));
            modal.querySelector('.confirm-cancel').addEventListener('click', () => close(false));
            modal.querySelector('.confirm-ok').addEventListener('click', () => close(true));
            backdrop.addEventListener('click', (e) => { if (e.target === backdrop) close(false); });
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
                    p.displayName || '',
                    p.host || '',
                    (p.tags || []).join(' '),
                    p.protocol || '',
                ].join(' ');
                const result = fuzzyMatch(fields, this.searchQuery);
                return { profile: p, ...result };
            })
            .filter((r) => r.match)
            .sort((a, b) => b.score - a.score)
            .map((r) => r.profile);
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.Sidebar = Sidebar;

})();
