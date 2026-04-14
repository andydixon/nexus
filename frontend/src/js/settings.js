/**
 * Nexus Terminal — settings modal.
 * Renders a tabbed settings dialog for application-wide preferences,
 * terminal defaults, security, known-hosts management, and about info.
 */

import { createElement, escapeHtml, formatTimestamp, $ } from './utils.js';

/** Settings tab definitions. */
const TABS = [
    { key: 'general', label: 'General' },
    { key: 'terminal', label: 'Terminal' },
    { key: 'security', label: 'Security' },
    { key: 'knownhosts', label: 'Known Hosts' },
    { key: 'about', label: 'About' },
];

export class Settings {
    /**
     * @param {import('./app.js').App} app
     */
    constructor(app) {
        this.app = app;

        /** @type {HTMLElement|null} */
        this._overlay = null;

        /** @type {string} Currently active tab key. */
        this._activeTab = 'general';

        /** @type {Object} Working copy of preferences. */
        this._prefs = {};

        /** @type {Array<Object>} Cached known hosts list. */
        this._knownHosts = [];
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Opens the settings modal.
     */
    async open() {
        this._prefs = JSON.parse(JSON.stringify(this.app.state.preferences));
        this._activeTab = 'general';

        try {
            this._knownHosts = await window.go.api.Service.ListKnownHosts() || [];
        } catch (_) {
            this._knownHosts = [];
        }

        this._render();
    }

    /**
     * Closes the settings modal.
     */
    close() {
        if (this._overlay) {
            this._overlay.remove();
            this._overlay = null;
        }
    }

    /**
     * Saves the modified preferences to the backend.
     */
    async save() {
        this._collectValues();

        try {
            await window.go.api.Service.SavePreferences(this._prefs);
            this.app.state.setPreferences(this._prefs);
            this.app.notifications.success('Settings saved');
            this.close();
        } catch (err) {
            this.app.notifications.error('Failed to save settings: ' + err);
        }
    }

    // ── Rendering ────────────────────────────────────────────────────

    /** @private */
    _render() {
        this.close();

        const overlay = createElement('div', 'modal-overlay');
        const modal = createElement('div', 'modal modal-settings');

        // Header
        const header = createElement('div', 'modal-header');
        header.innerHTML = `
            <h2 class="modal-title">Settings</h2>
            <button class="modal-close" aria-label="Close">&times;</button>
        `;
        header.querySelector('.modal-close').addEventListener('click', () => this.close());
        modal.appendChild(header);

        // Tab bar
        const tabBar = createElement('div', 'settings-tab-bar');
        for (const tab of TABS) {
            const btn = createElement('button', `settings-tab ${tab.key === this._activeTab ? 'active' : ''}`, {
                textContent: tab.label,
                dataset: { tab: tab.key },
            });
            btn.addEventListener('click', () => {
                this._activeTab = tab.key;
                this._renderBody(modal);
                tabBar.querySelectorAll('.settings-tab').forEach((t) => {
                    t.classList.toggle('active', t.dataset.tab === tab.key);
                });
            });
            tabBar.appendChild(btn);
        }
        modal.appendChild(tabBar);

        // Body (tab content)
        const body = createElement('div', 'modal-body settings-body');
        modal.appendChild(body);
        this._renderTabContent(body);

        // Footer
        const footer = createElement('div', 'modal-footer');
        const cancelBtn = createElement('button', 'btn btn-secondary', { textContent: 'Cancel' });
        const saveBtn = createElement('button', 'btn btn-primary', { textContent: 'Save' });
        cancelBtn.addEventListener('click', () => this.close());
        saveBtn.addEventListener('click', () => this.save());
        footer.appendChild(cancelBtn);
        footer.appendChild(saveBtn);
        modal.appendChild(footer);

        overlay.appendChild(modal);
        overlay.addEventListener('click', (e) => { if (e.target === overlay) this.close(); });
        modal.addEventListener('click', (e) => e.stopPropagation());
        document.body.appendChild(overlay);
        this._overlay = overlay;
    }

    /**
     * Re-renders only the body when the active tab changes.
     * @param {HTMLElement} modal
     * @private
     */
    _renderBody(modal) {
        const body = modal.querySelector('.settings-body');
        if (body) this._renderTabContent(body);
    }

    /**
     * Renders the content for the currently active tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderTabContent(body) {
        body.innerHTML = '';
        switch (this._activeTab) {
            case 'general': this._renderGeneralTab(body); break;
            case 'terminal': this._renderTerminalTab(body); break;
            case 'security': this._renderSecurityTab(body); break;
            case 'knownhosts': this._renderKnownHostsTab(body); break;
            case 'about': this._renderAboutTab(body); break;
        }
    }

    // ── Tab content renderers ────────────────────────────────────────

    /**
     * General preferences tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderGeneralTab(body) {
        const p = this._prefs;
        body.appendChild(this._settingField('select', 'theme', 'Theme', p.theme, {
            options: [
                { value: 'dark', label: 'Dark' },
                { value: 'light', label: 'Light' },
            ],
        }));
        body.appendChild(this._settingField('select', 'defaultProtocol', 'Default Protocol', p.defaultProtocol, {
            options: [
                { value: 'ssh', label: 'SSH' },
                { value: 'telnet', label: 'Telnet' },
                { value: 'raw', label: 'RAW' },
            ],
        }));
        body.appendChild(this._settingField('toggle', 'confirmOnClose', 'Confirm on close', p.confirmOnClose));
        body.appendChild(this._settingField('toggle', 'confirmOnDisconnect', 'Confirm on disconnect', p.confirmOnDisconnect));
        body.appendChild(this._settingField('toggle', 'showStatusBar', 'Show status bar', p.showStatusBar));
        body.appendChild(this._settingField('number', 'sidebarWidth', 'Sidebar width (px)', p.sidebarWidth, { min: 180, max: 500 }));
    }

    /**
     * Terminal defaults tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderTerminalTab(body) {
        const p = this._prefs;
        body.appendChild(this._settingField('text', 'defaultFontFamily', 'Font family', p.defaultFontFamily, {
            placeholder: 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
        }));
        body.appendChild(this._settingField('number', 'defaultFontSize', 'Font size', p.defaultFontSize, { min: 8, max: 36 }));
        body.appendChild(this._settingField('number', 'defaultScrollback', 'Scrollback lines', p.defaultScrollback, { min: 0, max: 100000 }));
        body.appendChild(this._settingField('select', 'defaultCursorStyle', 'Cursor style', p.defaultCursorStyle, {
            options: [
                { value: 'block', label: 'Block' },
                { value: 'underline', label: 'Underline' },
                { value: 'bar', label: 'Bar' },
            ],
        }));
    }

    /**
     * Security settings tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderSecurityTab(body) {
        const storeState = this.app.state.secretStoreUnlocked ? 'Unlocked' : 'Locked';
        const storeInit = this.app.state.secretStoreInitialised;

        const statusRow = createElement('div', 'settings-row');
        statusRow.innerHTML = `
            <span class="settings-label">Secret store</span>
            <span class="settings-value ${this.app.state.secretStoreUnlocked ? 'text-success' : 'text-muted'}">${storeState}</span>
        `;
        body.appendChild(statusRow);

        if (!storeInit) {
            const initBtn = createElement('button', 'btn btn-primary btn-sm', { textContent: 'Initialise Secret Store' });
            initBtn.addEventListener('click', async () => {
                try {
                    const pw = await this.app.secretPrompt.promptMasterPassword(true);
                    await window.go.api.Service.InitialiseSecretStore(pw);
                    this.app.state.setSecretStoreInitialised(true);
                    this.app.state.setSecretStoreState(true);
                    this.app.notifications.success('Secret store created');
                    this._renderTabContent(body);
                } catch (err) {
                    if (err.message !== 'User cancelled') {
                        this.app.notifications.error('Failed to initialise: ' + err);
                    }
                }
            });
            body.appendChild(initBtn);
        } else if (!this.app.state.secretStoreUnlocked) {
            const unlockBtn = createElement('button', 'btn btn-primary btn-sm', { textContent: 'Unlock' });
            unlockBtn.addEventListener('click', async () => {
                try {
                    const pw = await this.app.secretPrompt.promptMasterPassword(false);
                    await window.go.api.Service.UnlockSecretStore(pw);
                    this.app.state.setSecretStoreState(true);
                    this.app.notifications.success('Secret store unlocked');
                    this._renderTabContent(body);
                } catch (err) {
                    if (err.message !== 'User cancelled') {
                        this.app.notifications.error('Unlock failed: ' + err);
                    }
                }
            });
            body.appendChild(unlockBtn);
        } else {
            const lockBtn = createElement('button', 'btn btn-outline btn-sm', { textContent: 'Lock' });
            lockBtn.addEventListener('click', async () => {
                try {
                    await window.go.api.Service.LockSecretStore();
                    this.app.state.setSecretStoreState(false);
                    this.app.notifications.info('Secret store locked');
                    this._renderTabContent(body);
                } catch (err) {
                    this.app.notifications.error('Lock failed: ' + err);
                }
            });
            body.appendChild(lockBtn);
        }
    }

    /**
     * Known-hosts management tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderKnownHostsTab(body) {
        if (this._knownHosts.length === 0) {
            body.appendChild(createElement('p', 'text-muted', { textContent: 'No known hosts recorded.' }));
            return;
        }

        const table = createElement('table', 'settings-table');
        table.innerHTML = `
            <thead>
                <tr>
                    <th>Host</th>
                    <th>Port</th>
                    <th>Key Type</th>
                    <th>Fingerprint</th>
                    <th>First Seen</th>
                    <th></th>
                </tr>
            </thead>
        `;
        const tbody = createElement('tbody');

        for (const kh of this._knownHosts) {
            const tr = createElement('tr');
            tr.innerHTML = `
                <td>${escapeHtml(kh.host)}</td>
                <td>${kh.port}</td>
                <td><code>${escapeHtml(kh.keyType)}</code></td>
                <td class="fingerprint-cell" title="${escapeHtml(kh.fingerprint)}"><code>${escapeHtml(kh.fingerprint.slice(0, 20))}…</code></td>
                <td>${formatTimestamp(kh.firstSeen)}</td>
                <td></td>
            `;
            const deleteBtn = createElement('button', 'btn btn-xs btn-danger', { textContent: 'Remove' });
            deleteBtn.addEventListener('click', async () => {
                try {
                    await window.go.api.Service.DeleteKnownHost(kh.id);
                    this._knownHosts = this._knownHosts.filter((h) => h.id !== kh.id);
                    this._renderTabContent(body);
                    this.app.notifications.success('Known host removed');
                } catch (err) {
                    this.app.notifications.error('Failed to remove: ' + err);
                }
            });
            tr.querySelector('td:last-child').appendChild(deleteBtn);
            tbody.appendChild(tr);
        }

        table.appendChild(tbody);
        body.appendChild(table);
    }

    /**
     * About tab showing application info.
     * @param {HTMLElement} body
     * @private
     */
    _renderAboutTab(body) {
        body.innerHTML = `
            <div class="about-section">
                <h3 class="about-title">Nexus Terminal</h3>
                <p class="about-version">Version 1.0.0</p>
                <p class="about-desc">A premium desktop SSH, Telnet, and RAW TCP client built with Wails.</p>
                <div class="about-links">
                    <p>AES-256-GCM encrypted credential vault</p>
                    <p>Platform-native keychain integration</p>
                    <p>xterm.js terminal emulation</p>
                </div>
            </div>
        `;
    }

    // ── Form helpers ─────────────────────────────────────────────────

    /**
     * Renders a single settings form field.
     * @param {string} type
     * @param {string} name
     * @param {string} label
     * @param {*} value
     * @param {Object} [opts]
     * @returns {HTMLElement}
     * @private
     */
    _settingField(type, name, label, value, opts = {}) {
        const group = createElement('div', 'form-group settings-field');
        const labelEl = createElement('label', 'form-label', { textContent: label, for: `setting-${name}` });
        group.appendChild(labelEl);

        let input;
        switch (type) {
            case 'text':
                input = createElement('input', 'form-input', {
                    type: 'text',
                    id: `setting-${name}`,
                    name: `setting-${name}`,
                    value: value || '',
                    placeholder: opts.placeholder || '',
                });
                break;

            case 'number':
                input = createElement('input', 'form-input', {
                    type: 'number',
                    id: `setting-${name}`,
                    name: `setting-${name}`,
                    value: value != null ? String(value) : '',
                });
                if (opts.min != null) input.min = opts.min;
                if (opts.max != null) input.max = opts.max;
                break;

            case 'select':
                input = createElement('select', 'form-select', {
                    id: `setting-${name}`,
                    name: `setting-${name}`,
                });
                for (const opt of (opts.options || [])) {
                    const option = createElement('option', '', { value: opt.value, textContent: opt.label });
                    if (opt.value === value) option.selected = true;
                    input.appendChild(option);
                }
                break;

            case 'toggle': {
                const wrapper = createElement('div', 'form-toggle-wrapper');
                input = createElement('input', 'form-toggle', {
                    type: 'checkbox',
                    id: `setting-${name}`,
                    name: `setting-${name}`,
                });
                input.checked = !!value;
                const slider = createElement('label', 'form-toggle-label', { for: `setting-${name}` });
                wrapper.appendChild(input);
                wrapper.appendChild(slider);
                group.appendChild(wrapper);
                return group;
            }

            default:
                input = createElement('input', 'form-input', {
                    type: 'text',
                    id: `setting-${name}`,
                    name: `setting-${name}`,
                    value: value || '',
                });
        }

        group.appendChild(input);
        return group;
    }

    /**
     * Reads current form field values back into the working preferences copy.
     * @private
     */
    _collectValues() {
        if (!this._overlay) return;

        const val = (name) => {
            const el = this._overlay.querySelector(`[name="setting-${name}"]`);
            if (!el) return undefined;
            if (el.type === 'checkbox') return el.checked;
            if (el.type === 'number') return el.value !== '' ? Number(el.value) : 0;
            return el.value;
        };

        const fields = [
            'theme', 'defaultProtocol', 'confirmOnClose', 'confirmOnDisconnect',
            'showStatusBar', 'sidebarWidth', 'defaultFontFamily', 'defaultFontSize',
            'defaultScrollback', 'defaultCursorStyle',
        ];

        for (const field of fields) {
            const v = val(field);
            if (v !== undefined) {
                this._prefs[field] = v;
            }
        }
    }
}
