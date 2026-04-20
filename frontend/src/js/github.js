(function() {
/**
 * Nexus Terminal — GitHub repository viewer.
 * Renders a scrollable list of repositories grouped by owner/organisation,
 * sorted by last commit date (newest first). Automatically refreshes via
 * Wails events emitted by the Go backend.
 */

const { createElement, escapeHtml, relativeTime, $ } = window.NexusTerminal;

class GitHubViewer {
    constructor() {
        /** @type {Map<string, GitHubViewerInstance>} sessionID → viewer */
        this._viewers = new Map();
    }

    /**
     * Creates a viewer panel for a GitHub session inside the given container.
     * @param {string} sessionID
     * @param {HTMLElement} container - The .session-panel element.
     * @param {string} profileName
     */
    createViewer(sessionID, container, profileName) {
        if (this._viewers.has(sessionID)) return;

        const viewer = new GitHubViewerInstance(sessionID, container, profileName);
        this._viewers.set(sessionID, viewer);
        viewer.render();
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
     * Handles incoming repo data from the backend event.
     * @param {string} sessionID
     * @param {string} login - The authenticated GitHub username.
     * @param {Array} repos
     */
    updateRepos(sessionID, login, repos) {
        const viewer = this._viewers.get(sessionID);
        if (viewer) {
            viewer.setData(login, repos);
        }
    }

    /**
     * Updates the status line for a session.
     * @param {string} sessionID
     * @param {string} message
     */
    setStatus(sessionID, message) {
        const viewer = this._viewers.get(sessionID);
        if (viewer) {
            viewer.setStatus(message);
        }
    }

    /**
     * Handles an error event for a session.
     * @param {string} sessionID
     * @param {string} message
     */
    showError(sessionID, message) {
        const viewer = this._viewers.get(sessionID);
        if (viewer) {
            viewer.showError(message);
        }
    }
}

class GitHubViewerInstance {
    /**
     * @param {string} sessionID
     * @param {HTMLElement} container
     * @param {string} profileName
     */
    constructor(sessionID, container, profileName) {
        this.sessionID = sessionID;
        this.container = container;
        this.profileName = profileName;

        /** @type {string} */
        this._login = '';

        /** @type {Array} */
        this._repos = [];

        /** @type {string} */
        this._filter = '';

        /** @type {HTMLElement|null} */
        this._el = null;

        /** @type {HTMLElement|null} */
        this._listEl = null;

        /** @type {HTMLElement|null} */
        this._statusEl = null;

        /** @type {number|null} */
        this._refreshTimer = null;
    }

    render() {
        this.container.innerHTML = '';

        const wrapper = createElement('div', 'github-viewer');

        // ── Header bar ──────────────────────────────────────────
        const header = createElement('div', 'github-viewer-header');

        const titleArea = createElement('div', 'github-viewer-title-area');
        const icon = createElement('span', 'github-viewer-icon');
        icon.innerHTML = this._githubSVG();
        titleArea.appendChild(icon);

        const titleText = createElement('span', 'github-viewer-title');
        titleText.textContent = this.profileName || 'GitHub';
        titleArea.appendChild(titleText);
        header.appendChild(titleArea);

        // Search / filter
        const searchWrap = createElement('div', 'github-viewer-search');
        const searchInput = createElement('input', 'github-viewer-search-input', {
            type: 'text',
            placeholder: 'Filter repositories…',
        });
        searchInput.addEventListener('input', (e) => {
            this._filter = e.target.value.toLowerCase();
            this._renderList();
        });
        searchWrap.appendChild(searchInput);
        header.appendChild(searchWrap);

        // Refresh button
        const refreshBtn = createElement('button', 'github-viewer-refresh', {
            title: 'Refresh now',
        });
        refreshBtn.innerHTML = '↻';
        refreshBtn.addEventListener('click', () => this._manualRefresh());
        header.appendChild(refreshBtn);

        wrapper.appendChild(header);

        // ── Status bar ──────────────────────────────────────────
        this._statusEl = createElement('div', 'github-viewer-status');
        this._statusEl.textContent = 'Connecting…';
        wrapper.appendChild(this._statusEl);

        // ── Repo list ───────────────────────────────────────────
        this._listEl = createElement('div', 'github-viewer-list');
        wrapper.appendChild(this._listEl);

        this.container.appendChild(wrapper);
        this._el = wrapper;
    }

    /**
     * Updates the viewer with fresh data.
     * @param {string} login
     * @param {Array} repos
     */
    setData(login, repos) {
        this._login = login;
        this._repos = repos || [];
        this._updateStatus();
        this._renderList();
    }

    /**
     * Updates the status line with a custom message.
     * @param {string} message
     */
    setStatus(message) {
        if (!this._statusEl) return;
        this._statusEl.classList.remove('github-viewer-status-error');
        this._statusEl.textContent = message || '';
    }

    /**
     * Displays an error message in the status bar.
     * @param {string} message
     */
    showError(message) {
        if (this._statusEl) {
            this._statusEl.textContent = `⚠ ${message}`;
            this._statusEl.classList.add('github-viewer-status-error');
        }
    }

    destroy() {
        if (this._refreshTimer) {
            clearInterval(this._refreshTimer);
            this._refreshTimer = null;
        }
        if (this._el) {
            this._el.remove();
            this._el = null;
        }
    }

    // ── Private ──────────────────────────────────────────────────

    _updateStatus() {
        if (!this._statusEl) return;
        this._statusEl.classList.remove('github-viewer-status-error');

        const count = this._repos.length;
        const now = new Date().toLocaleTimeString();
        this._statusEl.textContent = `${this._login} · ${count} repositor${count === 1 ? 'y' : 'ies'} · updated ${now}`;
    }

    _renderList() {
        if (!this._listEl) return;
        this._listEl.innerHTML = '';

        // Filter
        let filtered = this._repos;
        if (this._filter) {
            filtered = this._repos.filter((r) =>
                r.fullName.toLowerCase().includes(this._filter) ||
                (r.description || '').toLowerCase().includes(this._filter) ||
                (r.language || '').toLowerCase().includes(this._filter)
            );
        }

        if (filtered.length === 0) {
            const empty = createElement('div', 'github-viewer-empty');
            empty.textContent = this._filter ? 'No matching repositories.' : 'No repositories found.';
            this._listEl.appendChild(empty);
            return;
        }

        // Group by owner
        const groups = new Map();
        for (const repo of filtered) {
            const owner = repo.owner || 'unknown';
            if (!groups.has(owner)) groups.set(owner, []);
            groups.get(owner).push(repo);
        }

        // Sort groups alphabetically (user's own repos first)
        const sortedKeys = [...groups.keys()].sort((a, b) => {
            if (a === this._login) return -1;
            if (b === this._login) return 1;
            return a.localeCompare(b);
        });

        for (const owner of sortedKeys) {
            const repos = groups.get(owner);

            // Group header
            const groupEl = createElement('div', 'github-group');
            const groupHeader = createElement('div', 'github-group-header');
            groupHeader.innerHTML = `
                <span class="github-group-name">${escapeHtml(owner)}</span>
                <span class="github-group-count">${repos.length}</span>
            `;
            groupEl.appendChild(groupHeader);

            // Repo cards
            for (const repo of repos) {
                groupEl.appendChild(this._renderRepoCard(repo));
            }

            this._listEl.appendChild(groupEl);
        }
    }

    /**
     * Renders a single repository card.
     * @param {Object} repo
     * @returns {HTMLElement}
     */
    _renderRepoCard(repo) {
        const card = createElement('div', 'github-repo-card');
        card.addEventListener('click', () => {
            if (repo.htmlURL) window.open(repo.htmlURL, '_blank');
        });

        // Top row: name + badges
        const topRow = createElement('div', 'github-repo-top');
        const nameEl = createElement('span', 'github-repo-name');
        nameEl.textContent = repo.name;
        topRow.appendChild(nameEl);


        if (repo.private) {
            const badge = createElement('span', 'github-repo-badge github-repo-private');
            badge.textContent = 'Private';
            topRow.appendChild(badge);
        }
        if (repo.fork) {
            const badge = createElement('span', 'github-repo-badge github-repo-fork');
            badge.textContent = 'Fork';
            topRow.appendChild(badge);
        }
        if (repo.archived) {
            const badge = createElement('span', 'github-repo-badge github-repo-archived');
            badge.textContent = 'Archived';
            topRow.appendChild(badge);
        }

        const actions = createElement('div', 'github-repo-actions');

        const downloadBtn = createElement('button', 'github-repo-download', {
            textContent: 'ZIP',
            title: 'Download repository as zip',
        });
        downloadBtn.addEventListener('click', (e) => {
            e.stopPropagation();
            this._downloadRepo(repo);
        });
        actions.appendChild(downloadBtn);

        const visitLink = createElement('button', 'github-repo-visit', {
            textContent: 'Visit',
            title: 'Open repository in browser',
        });
        visitLink.addEventListener('click', (e) => {
            e.stopPropagation();
            if (repo.htmlURL) {
                if (window.runtime && window.runtime.BrowserOpenURL) {
                    window.runtime.BrowserOpenURL(repo.htmlURL);
                } else {
                    window.open(repo.htmlURL, '_blank');
                }
            }
        });
        actions.appendChild(visitLink);

        topRow.appendChild(actions);

        card.appendChild(topRow);

        if (repo.latestCommitter && repo.latestCommitAt) {
            const commitLine = createElement('div', 'github-repo-commit');
            const when = new Date(repo.latestCommitAt);
            const whenText = isNaN(when.getTime()) ? repo.latestCommitAt : when.toLocaleString();
            commitLine.textContent = `Last commit: ${whenText} by ${repo.latestCommitter}`;
            card.appendChild(commitLine);
        }

        // Description
        if (repo.description) {
            const desc = createElement('div', 'github-repo-desc');
            desc.textContent = repo.description;
            card.appendChild(desc);
        }

        // Meta row: language, stars, pushed
        const meta = createElement('div', 'github-repo-meta');

        if (repo.language) {
            const lang = createElement('span', 'github-repo-lang');
            lang.innerHTML = `<span class="github-lang-dot" style="background:${this._langColour(repo.language)}"></span> ${escapeHtml(repo.language)}`;
            meta.appendChild(lang);
        }

        if (repo.stars > 0) {
            const stars = createElement('span', 'github-repo-stars');
            stars.textContent = `★ ${repo.stars.toLocaleString()}`;
            meta.appendChild(stars);
        }

        if (repo.pushedAt) {
            const pushed = createElement('span', 'github-repo-pushed');
            pushed.textContent = relativeTime(repo.pushedAt);
            pushed.title = new Date(repo.pushedAt).toLocaleString();
            meta.appendChild(pushed);
        }

        card.appendChild(meta);

        return card;
    }

    async _downloadRepo(repo) {
        if (!this._statusEl) return;
        const ref = repo.defaultBranch || '';
        this._statusEl.classList.remove('github-viewer-status-error');
        this._statusEl.textContent = `Downloading ${repo.fullName}...`;

        try {
            await window.go.api.Service.DownloadGitHubRepoArchive(
                this.sessionID,
                repo.fullName,
                ref,
            );
            this._updateStatus();
        } catch (err) {
            this._statusEl.textContent = `⚠ Download failed: ${err}`;
            this._statusEl.classList.add('github-viewer-status-error');
        }
    }

    async _manualRefresh() {
        if (!this._statusEl) return;
        this._statusEl.textContent = 'Refreshing…';
        try {
            await window.go.api.Service.RefreshGitHubRepos(this.sessionID);
        } catch (err) {
            this.showError(err.message || 'Refresh failed');
        }
    }

    /**
     * Returns a colour for the given programming language.
     * Uses a subset of GitHub's linguist colours.
     * @param {string} lang
     * @returns {string} CSS colour value.
     */
    _langColour(lang) {
        const colours = {
            'JavaScript': '#f1e05a',
            'TypeScript': '#3178c6',
            'Python': '#3572A5',
            'Go': '#00ADD8',
            'Rust': '#dea584',
            'Java': '#b07219',
            'C': '#555555',
            'C++': '#f34b7d',
            'C#': '#178600',
            'Ruby': '#701516',
            'PHP': '#4F5D95',
            'Swift': '#F05138',
            'Kotlin': '#A97BFF',
            'Shell': '#89e051',
            'HTML': '#e34c26',
            'CSS': '#563d7c',
            'Dart': '#00B4AB',
            'Lua': '#000080',
            'Scala': '#c22d40',
            'Elixir': '#6e4a7e',
            'Haskell': '#5e5086',
            'Zig': '#ec915c',
            'Vue': '#41b883',
            'SCSS': '#c6538c',
        };
        return colours[lang] || '#8b949e';
    }

    /**
     * GitHub Invertocat SVG icon.
     * @returns {string}
     */
    _githubSVG() {
        return '<svg viewBox="0 0 16 16" width="20" height="20" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/></svg>';
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.GitHubViewer = new GitHubViewer();

})();
