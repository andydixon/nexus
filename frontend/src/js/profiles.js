(function() {
/**
 * Nexus Terminal — connection profile editor.
 * Renders a slide-out drawer for creating and editing connection profiles.
 * Dynamically switches form sections based on the selected protocol.
 */

const { createElement, escapeHtml, generateId, $ } = window.NexusTerminal;

/**
 * Default values mirroring the Go backend defaults.
 */
const DEFAULTS = {
    ssh: {
        port: 22,
        protocol: 'ssh',
        ssh: {
            authMethods: ['publickey', 'password', 'keyboard-interactive'],
            secretMode: 'ask-every-time',
            keySecretMode: 'ask-every-time',
            hostKeyPolicy: 'ask',
            terminalType: 'xterm-256color',
            keepaliveInterval: 30,
            keepaliveCountMax: 3,
            compression: false,
            jumpHosts: [],
            startupCommand: '',
            envVars: {},
        },
    },
    telnet: {
        port: 23,
        protocol: 'telnet',
        telnet: {
            localEcho: false,
            lineEnding: 'crlf',
            terminalType: 'xterm-256color',
            encoding: 'utf-8',
            negotiationDebug: false,
            lineMode: false,
        },
    },
    raw: {
        port: 23,
        protocol: 'raw',
        raw: {
            autoReconnect: false,
            lineEnding: 'crlf',
            encoding: 'utf-8',
            showTimestamps: false,
            showDirection: false,
            payloadPresets: [],
        },
    },
};

class ProfileEditor {
    /**
     * @param {App} app
     */
    constructor(app) {
        this.app = app;

        /** @type {HTMLElement|null} */
        this.drawer = null;

        /** @type {Object|null} The profile currently being edited. */
        this._profile = null;

        /** @type {boolean} */
        this._isNew = false;
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Opens the editor with a blank profile of the given protocol.
     * @param {string} [protocol='ssh']
     */
    openNew(protocol = 'ssh') {
        const profile = this._defaultProfile(protocol);
        this._isNew = true;
        this._profile = profile;
        this.render(profile, true);
    }

    /**
     * Opens the editor pre-populated with an existing profile.
     * @param {string} profileId
     */
    openEdit(profileId) {
        const profile = this.app.state.getProfile(profileId);
        if (!profile) {
            this.app.notifications.error('Profile not found');
            return;
        }
        this._isNew = false;
        this._profile = JSON.parse(JSON.stringify(profile)); // deep clone
        this.render(this._profile, false);
    }

    /**
     * Renders (or re-renders) the editor drawer.
     * @param {Object} profile
     * @param {boolean} isNew
     */
    render(profile, isNew) {
        this.close();

        const overlay = createElement('div', 'drawer-backdrop');
        const drawer = createElement('div', 'drawer');

        // ── Header ──────────────────────────────────────────────
        const header = createElement('div', 'drawer-header');
        header.innerHTML = `
            <h2 class="drawer-title">${isNew ? 'New Connection' : 'Edit Connection'}</h2>
            <button class="drawer-close" aria-label="Close">&times;</button>
        `;
        drawer.appendChild(header);

        // ── Protocol selector tabs ──────────────────────────────
        const protocols = ['ssh', 'telnet', 'raw'];
        const tabBar = createElement('div', 'drawer-protocol-tabs');
        for (const proto of protocols) {
            const btn = createElement('button', `drawer-proto-tab ${profile.protocol === proto ? 'active' : ''}`, {
                textContent: proto.toUpperCase(),
                dataset: { proto },
            });
            btn.addEventListener('click', () => this.handleProtocolChange(proto));
            tabBar.appendChild(btn);
        }
        drawer.appendChild(tabBar);

        // ── Scrollable body ─────────────────────────────────────
        const body = createElement('div', 'drawer-body');

        // General section (common to all protocols)
        body.appendChild(this._renderGeneralSection(profile));

        // Protocol-specific sections
        switch (profile.protocol) {
            case 'ssh':
                this._renderSSHSections(profile).forEach((s) => body.appendChild(s));
                break;
            case 'telnet':
                this._renderTelnetSections(profile).forEach((s) => body.appendChild(s));
                break;
            case 'raw':
                this._renderRAWSections(profile).forEach((s) => body.appendChild(s));
                break;
        }

        // Terminal section (SSH and Telnet only)
        if (profile.protocol !== 'raw') {
            body.appendChild(this._renderTerminalSection(profile));
        }

        // Notes & Tags section (common)
        body.appendChild(this._renderNotesSection(profile));

        drawer.appendChild(body);

        // ── Footer ──────────────────────────────────────────────
        const footer = createElement('div', 'drawer-footer');
        const cancelBtn = createElement('button', 'btn btn-secondary', { textContent: 'Cancel' });
        const testBtn = createElement('button', 'btn btn-outline', { textContent: 'Test' });
        const saveBtn = createElement('button', 'btn btn-primary', { textContent: isNew ? 'Create' : 'Save' });
        const connectBtn = createElement('button', 'btn btn-accent', { textContent: isNew ? 'Create & Connect' : 'Save & Connect' });

        cancelBtn.addEventListener('click', () => this.close());
        testBtn.addEventListener('click', () => this._testConnection());
        saveBtn.addEventListener('click', () => this._handleSave(false));
        connectBtn.addEventListener('click', () => this._handleSave(true));

        footer.appendChild(cancelBtn);
        footer.appendChild(testBtn);
        footer.appendChild(saveBtn);
        footer.appendChild(connectBtn);
        drawer.appendChild(footer);

        // ── Events ──────────────────────────────────────────────
        header.querySelector('.drawer-close').addEventListener('click', () => this.close());
        overlay.addEventListener('click', () => this.close());

        overlay.appendChild(drawer);
        document.body.appendChild(overlay);
        this.drawer = overlay;

        // Trigger visibility after append so CSS transitions animate
        requestAnimationFrame(() => {
            overlay.classList.add('is-visible');
            drawer.classList.add('is-visible');
        });

        // Prevent overlay click from reaching through the drawer
        drawer.addEventListener('click', (e) => e.stopPropagation());

        // Focus the first input
        const firstInput = drawer.querySelector('input, select, textarea');
        if (firstInput) firstInput.focus();
    }

    /**
     * Closes the editor drawer.
     */
    close() {
        if (this.drawer) {
            this.drawer.remove();
            this.drawer = null;
        }
    }

    /**
     * Handles protocol change from the tab bar.
     * @param {string} protocol
     */
    handleProtocolChange(protocol) {
        if (this._profile.protocol === protocol) return;

        // Preserve common fields, reset protocol-specific ones
        this._collectFormValues();
        const base = this._defaultProfile(protocol);
        base.id = this._profile.id;
        base.displayName = this._profile.displayName;
        base.description = this._profile.description;
        base.host = this._profile.host;
        base.username = this._profile.username;
        base.tags = this._profile.tags;
        base.notes = this._profile.notes;
        base.favourite = this._profile.favourite;

        this._profile = base;
        this.render(this._profile, this._isNew);
    }

    // ── Section renderers ────────────────────────────────────────────

    /** @private */
    _renderGeneralSection(profile) {
        return this._renderSection('General', [
            this._field('text', 'displayName', 'Display Name', profile.displayName, { placeholder: 'My Server' }),
            this._field('text', 'host', 'Host', profile.host, { placeholder: '192.168.1.1 or example.com', required: true }),
            this._field('number', 'port', 'Port', profile.port, { min: 1, max: 65535, required: true }),
            this._field('text', 'username', 'Username', profile.username, { placeholder: 'root' }),
            this._field('text', 'description', 'Description', profile.description, { placeholder: 'Optional description' }),
        ]);
    }

    /** @private */
    _renderSSHSections(profile) {
        const ssh = profile.ssh || {};
        const sections = [];

        // Authentication
        sections.push(this._renderSection('Authentication', [
            this._field('select', 'ssh.secretMode', 'Password Mode', ssh.secretMode, {
                options: [
                    { value: 'saved', label: 'Saved in vault' },
                    { value: 'session-only', label: 'Session only' },
                    { value: 'ask-every-time', label: 'Ask every time' },
                ],
            }),
            this._field('tags', 'ssh.authMethods', 'Auth Methods', (ssh.authMethods || []).join(', '), {
                help: 'Comma-separated: publickey, password, keyboard-interactive',
            }),
        ]));

        // Key & Passphrase
        sections.push(this._renderSection('Key & Passphrase', [
            this._field('text', 'ssh.privateKeyPath', 'Private Key Path', ssh.privateKeyPath, {
                placeholder: '~/.ssh/id_ed25519',
            }),
            this._field('select', 'ssh.keySecretMode', 'Passphrase Mode', ssh.keySecretMode, {
                options: [
                    { value: 'saved', label: 'Saved in vault' },
                    { value: 'session-only', label: 'Session only' },
                    { value: 'ask-every-time', label: 'Ask every time' },
                ],
            }),
        ]));

        // Host Verification
        sections.push(this._renderSection('Host Verification', [
            this._field('select', 'ssh.hostKeyPolicy', 'Host Key Policy', ssh.hostKeyPolicy, {
                options: [
                    { value: 'strict', label: 'Strict (reject unknown)' },
                    { value: 'ask', label: 'Ask (prompt on unknown)' },
                    { value: 'accept-new', label: 'Accept new keys' },
                    { value: 'insecure', label: 'Insecure (accept all)' },
                ],
            }),
        ]));

        // Jump Hosts
        sections.push(this._renderSection('Jump Hosts', [
            this._renderJumpHostsList(ssh.jumpHosts || []),
        ], true));

        // Startup
        sections.push(this._renderSection('Startup', [
            this._field('text', 'ssh.startupCommand', 'Startup Command', ssh.startupCommand, {
                placeholder: 'Optional command to run after connecting',
            }),
            this._field('textarea', 'ssh.envVarsText', 'Environment Variables', this._envVarsToText(ssh.envVars), {
                placeholder: 'KEY=VALUE (one per line)',
                rows: 3,
            }),
        ]));

        // Advanced
        sections.push(this._renderSection('Advanced', [
            this._field('text', 'ssh.terminalType', 'Terminal Type', ssh.terminalType, { placeholder: 'xterm-256color' }),
            this._field('number', 'ssh.keepaliveInterval', 'Keepalive Interval (s)', ssh.keepaliveInterval, { min: 0 }),
            this._field('number', 'ssh.keepaliveCountMax', 'Max Missed Keepalives', ssh.keepaliveCountMax, { min: 0 }),
            this._field('toggle', 'ssh.compression', 'Compression', ssh.compression),
        ], true));

        return sections;
    }

    /** @private */
    _renderTelnetSections(profile) {
        const tel = profile.telnet || {};
        const sections = [];

        sections.push(this._renderSection('Login', [
            this._field('toggle', 'telnet.localEcho', 'Local Echo', tel.localEcho),
            this._field('toggle', 'telnet.lineMode', 'Line Mode', tel.lineMode),
        ]));

        sections.push(this._renderSection('Terminal & Encoding', [
            this._field('text', 'telnet.terminalType', 'Terminal Type', tel.terminalType, { placeholder: 'xterm-256color' }),
            this._field('select', 'telnet.encoding', 'Encoding', tel.encoding, {
                options: [
                    { value: 'utf-8', label: 'UTF-8' },
                    { value: 'ascii', label: 'ASCII' },
                    { value: 'iso-8859-1', label: 'ISO 8859-1' },
                ],
            }),
        ]));

        sections.push(this._renderSection('Echo & Line Endings', [
            this._field('select', 'telnet.lineEnding', 'Line Ending', tel.lineEnding, {
                options: [
                    { value: 'crlf', label: 'CR+LF (\\r\\n)' },
                    { value: 'lf', label: 'LF (\\n)' },
                    { value: 'cr', label: 'CR (\\r)' },
                    { value: 'none', label: 'None' },
                ],
            }),
        ]));

        sections.push(this._renderSection('Negotiation', [
            this._field('toggle', 'telnet.negotiationDebug', 'Debug Negotiation', tel.negotiationDebug),
        ], true));

        return sections;
    }

    /** @private */
    _renderRAWSections(profile) {
        const raw = profile.raw || {};
        const sections = [];

        sections.push(this._renderSection('Encoding & Framing', [
            this._field('select', 'raw.encoding', 'Encoding', raw.encoding, {
                options: [
                    { value: 'utf-8', label: 'UTF-8' },
                    { value: 'ascii', label: 'ASCII' },
                    { value: 'hex', label: 'Hex' },
                ],
            }),
            this._field('select', 'raw.lineEnding', 'Line Ending', raw.lineEnding, {
                options: [
                    { value: 'crlf', label: 'CR+LF (\\r\\n)' },
                    { value: 'lf', label: 'LF (\\n)' },
                    { value: 'cr', label: 'CR (\\r)' },
                    { value: 'none', label: 'None' },
                ],
            }),
        ]));

        sections.push(this._renderSection('Display', [
            this._field('toggle', 'raw.showTimestamps', 'Show Timestamps', raw.showTimestamps),
            this._field('toggle', 'raw.showDirection', 'Show Direction', raw.showDirection),
        ]));

        sections.push(this._renderSection('Payload Presets', [
            this._renderPayloadPresetsList(raw.payloadPresets || []),
        ], true));

        sections.push(this._renderSection('Timeouts & Reconnect', [
            this._field('number', 'connectTimeout', 'Connect Timeout (s)', profile.connectTimeout, { min: 0 }),
            this._field('number', 'readTimeout', 'Read Timeout (s)', profile.readTimeout, { min: 0 }),
            this._field('toggle', 'raw.autoReconnect', 'Auto-reconnect', raw.autoReconnect),
        ]));

        return sections;
    }

    /** @private */
    _renderTerminalSection(profile) {
        return this._renderSection('Terminal', [
            this._field('text', 'terminalFontFamily', 'Font Family', profile.terminalFontFamily, {
                placeholder: 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
            }),
            this._field('number', 'terminalFontSize', 'Font Size', profile.terminalFontSize, { min: 8, max: 36 }),
            this._field('number', 'terminalScrollback', 'Scrollback Lines', profile.terminalScrollback, { min: 0, max: 100000 }),
            this._field('select', 'terminalCursorStyle', 'Cursor Style', profile.terminalCursorStyle, {
                options: [
                    { value: 'block', label: 'Block' },
                    { value: 'underline', label: 'Underline' },
                    { value: 'bar', label: 'Bar' },
                ],
            }),
        ]);
    }

    /** @private */
    _renderNotesSection(profile) {
        return this._renderSection('Notes & Tags', [
            this._field('tags', 'tags', 'Tags', (profile.tags || []).join(', '), {
                placeholder: 'production, london, vpn',
                help: 'Comma-separated tags for filtering',
            }),
            this._field('textarea', 'notes', 'Notes', profile.notes, {
                placeholder: 'Free-form notes…',
                rows: 4,
            }),
        ]);
    }

    // ── Form helpers ─────────────────────────────────────────────────

    /**
     * Renders a collapsible form section.
     * @param {string} title
     * @param {HTMLElement[]} fields
     * @param {boolean} [startCollapsed=false]
     * @returns {HTMLElement}
     * @private
     */
    _renderSection(title, fields, startCollapsed = false) {
        const section = createElement('div', 'form-section');
        const header = createElement('div', 'form-section-header');
        let collapsed = startCollapsed;

        header.innerHTML = `
            <span class="form-section-chevron">${collapsed ? '▸' : '▾'}</span>
            <span class="form-section-title">${escapeHtml(title)}</span>
        `;

        const content = createElement('div', 'form-section-content');
        if (collapsed) content.style.display = 'none';

        for (const field of fields) {
            if (field instanceof HTMLElement) {
                content.appendChild(field);
            }
        }

        header.addEventListener('click', () => {
            collapsed = !collapsed;
            content.style.display = collapsed ? 'none' : 'block';
            header.querySelector('.form-section-chevron').textContent = collapsed ? '▸' : '▾';
        });

        section.appendChild(header);
        section.appendChild(content);
        return section;
    }

    /**
     * Renders a single form field.
     * @param {string} type - text | number | select | toggle | textarea | tags | file
     * @param {string} name - Dot-notated field name, e.g. "ssh.secretMode".
     * @param {string} label
     * @param {*} value
     * @param {Object} [opts]
     * @returns {HTMLElement}
     * @private
     */
    _field(type, name, label, value, opts = {}) {
        const group = createElement('div', 'form-group');

        // Label
        const labelEl = createElement('label', 'form-label', {
            textContent: label,
            for: `field-${name}`,
        });
        group.appendChild(labelEl);

        // Input element based on type
        let input;

        switch (type) {
            case 'text':
            case 'file':
                input = createElement('input', 'form-input', {
                    type: type === 'file' ? 'text' : 'text',
                    id: `field-${name}`,
                    name,
                    value: value || '',
                    placeholder: opts.placeholder || '',
                });
                if (opts.required) input.required = true;
                break;

            case 'number':
                input = createElement('input', 'form-input', {
                    type: 'number',
                    id: `field-${name}`,
                    name,
                    value: value != null ? String(value) : '',
                });
                if (opts.min != null) input.min = opts.min;
                if (opts.max != null) input.max = opts.max;
                if (opts.required) input.required = true;
                break;

            case 'select': {
                input = createElement('select', 'form-select', {
                    id: `field-${name}`,
                    name,
                });
                for (const opt of (opts.options || [])) {
                    const option = createElement('option', '', {
                        value: opt.value,
                        textContent: opt.label,
                    });
                    if (opt.value === value) option.selected = true;
                    input.appendChild(option);
                }
                break;
            }

            case 'toggle': {
                const wrapper = createElement('div', 'form-toggle-wrapper');
                input = createElement('input', 'form-toggle', {
                    type: 'checkbox',
                    id: `field-${name}`,
                    name,
                });
                input.checked = !!value;
                const slider = createElement('label', 'form-toggle-label', { for: `field-${name}` });
                wrapper.appendChild(input);
                wrapper.appendChild(slider);
                group.appendChild(wrapper);
                // Help text
                if (opts.help) {
                    group.appendChild(createElement('span', 'form-hint', { textContent: opts.help }));
                }
                return group;
            }

            case 'textarea':
                input = createElement('textarea', 'form-textarea', {
                    id: `field-${name}`,
                    name,
                    placeholder: opts.placeholder || '',
                });
                input.rows = opts.rows || 3;
                input.textContent = value || '';
                break;

            case 'tags':
                input = createElement('input', 'form-input', {
                    type: 'text',
                    id: `field-${name}`,
                    name,
                    value: value || '',
                    placeholder: opts.placeholder || '',
                });
                break;

            default:
                input = createElement('input', 'form-input', {
                    type: 'text',
                    id: `field-${name}`,
                    name,
                    value: value || '',
                });
        }

        group.appendChild(input);

        // Help text
        if (opts.help) {
            group.appendChild(createElement('span', 'form-hint', { textContent: opts.help }));
        }

        return group;
    }

    /**
     * Renders the jump-hosts list editor.
     * @param {Array<Object>} jumpHosts
     * @returns {HTMLElement}
     * @private
     */
    _renderJumpHostsList(jumpHosts) {
        const wrapper = createElement('div', 'jump-hosts-list');

        for (let i = 0; i < jumpHosts.length; i++) {
            const jh = jumpHosts[i];
            const row = createElement('div', 'jump-host-row');
            row.innerHTML = `
                <input class="form-input form-input-sm" name="jumpHost-${i}-host" value="${escapeHtml(jh.host || '')}" placeholder="Host" />
                <input class="form-input form-input-sm" name="jumpHost-${i}-port" type="number" value="${jh.port || 22}" min="1" max="65535" style="width:80px" />
                <input class="form-input form-input-sm" name="jumpHost-${i}-username" value="${escapeHtml(jh.username || '')}" placeholder="User" />
                <button class="btn btn-icon btn-danger jump-host-remove" data-index="${i}" title="Remove">&times;</button>
            `;
            row.querySelector('.jump-host-remove').addEventListener('click', () => {
                jumpHosts.splice(i, 1);
                this.render(this._profile, this._isNew);
            });
            wrapper.appendChild(row);
        }

        const addBtn = createElement('button', 'btn btn-sm btn-outline', { textContent: '+ Add Jump Host' });
        addBtn.addEventListener('click', () => {
            jumpHosts.push({ host: '', port: 22, username: '', authMethod: 'publickey', secretMode: 'ask-every-time', privateKeyPath: '' });
            this.render(this._profile, this._isNew);
        });
        wrapper.appendChild(addBtn);

        return wrapper;
    }

    /**
     * Renders the payload-presets list editor.
     * @param {Array<Object>} presets
     * @returns {HTMLElement}
     * @private
     */
    _renderPayloadPresetsList(presets) {
        const wrapper = createElement('div', 'payload-presets-list');

        for (let i = 0; i < presets.length; i++) {
            const ps = presets[i];
            const row = createElement('div', 'preset-row');
            row.innerHTML = `
                <input class="form-input form-input-sm" name="preset-${i}-name" value="${escapeHtml(ps.name || '')}" placeholder="Name" />
                <input class="form-input form-input-sm" name="preset-${i}-payload" value="${escapeHtml(ps.payload || '')}" placeholder="Payload" />
                <select class="form-select form-select-sm" name="preset-${i}-format">
                    <option value="text" ${ps.format === 'text' ? 'selected' : ''}>Text</option>
                    <option value="hex" ${ps.format === 'hex' ? 'selected' : ''}>Hex</option>
                    <option value="escaped" ${ps.format === 'escaped' ? 'selected' : ''}>Escaped</option>
                </select>
                <button class="btn btn-icon btn-danger preset-remove" data-index="${i}" title="Remove">&times;</button>
            `;
            row.querySelector('.preset-remove').addEventListener('click', () => {
                presets.splice(i, 1);
                this.render(this._profile, this._isNew);
            });
            wrapper.appendChild(row);
        }

        const addBtn = createElement('button', 'btn btn-sm btn-outline', { textContent: '+ Add Preset' });
        addBtn.addEventListener('click', () => {
            presets.push({ id: generateId(), name: '', payload: '', format: 'text' });
            this.render(this._profile, this._isNew);
        });
        wrapper.appendChild(addBtn);

        return wrapper;
    }

    // ── Form data collection ─────────────────────────────────────────

    /**
     * Reads current form values into this._profile.
     * @private
     */
    _collectFormValues() {
        if (!this.drawer) return;

        const val = (name) => {
            const el = this.drawer.querySelector(`[name="${name}"]`);
            if (!el) return undefined;
            if (el.type === 'checkbox') return el.checked;
            if (el.type === 'number') return el.value !== '' ? Number(el.value) : 0;
            return el.value;
        };

        const p = this._profile;

        // General
        p.displayName = val('displayName') || '';
        p.host = val('host') || '';
        p.port = val('port') || p.port;
        p.username = val('username') || '';
        p.description = val('description') || '';
        p.terminalFontFamily = val('terminalFontFamily') || '';
        p.terminalFontSize = val('terminalFontSize') || 14;
        p.terminalScrollback = val('terminalScrollback') || 10000;
        p.terminalCursorStyle = val('terminalCursorStyle') || 'block';
        p.connectTimeout = val('connectTimeout') || 30;
        p.readTimeout = val('readTimeout') || 0;

        // Tags
        const tagsStr = val('tags');
        if (tagsStr !== undefined) {
            p.tags = tagsStr.split(',').map((t) => t.trim()).filter(Boolean);
        }

        // Notes
        const notes = val('notes');
        if (notes !== undefined) p.notes = notes;

        // SSH-specific
        if (p.protocol === 'ssh' && p.ssh) {
            p.ssh.secretMode = val('ssh.secretMode') || p.ssh.secretMode;
            p.ssh.privateKeyPath = val('ssh.privateKeyPath') || '';
            p.ssh.keySecretMode = val('ssh.keySecretMode') || p.ssh.keySecretMode;
            p.ssh.hostKeyPolicy = val('ssh.hostKeyPolicy') || p.ssh.hostKeyPolicy;
            p.ssh.terminalType = val('ssh.terminalType') || 'xterm-256color';
            p.ssh.keepaliveInterval = val('ssh.keepaliveInterval') ?? 30;
            p.ssh.keepaliveCountMax = val('ssh.keepaliveCountMax') ?? 3;
            p.ssh.compression = val('ssh.compression') || false;
            p.ssh.startupCommand = val('ssh.startupCommand') || '';

            const authStr = val('ssh.authMethods');
            if (authStr !== undefined) {
                p.ssh.authMethods = authStr.split(',').map((s) => s.trim()).filter(Boolean);
            }

            // Env vars from textarea
            const envText = val('ssh.envVarsText');
            if (envText !== undefined) {
                p.ssh.envVars = {};
                envText.split('\n').forEach((line) => {
                    const eq = line.indexOf('=');
                    if (eq > 0) {
                        p.ssh.envVars[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
                    }
                });
            }

            // Jump hosts
            const jh = [];
            let ji = 0;
            while (this.drawer.querySelector(`[name="jumpHost-${ji}-host"]`)) {
                jh.push({
                    host: val(`jumpHost-${ji}-host`) || '',
                    port: val(`jumpHost-${ji}-port`) || 22,
                    username: val(`jumpHost-${ji}-username`) || '',
                    authMethod: 'publickey',
                    secretMode: 'ask-every-time',
                    privateKeyPath: '',
                });
                ji++;
            }
            p.ssh.jumpHosts = jh;
        }

        // Telnet-specific
        if (p.protocol === 'telnet' && p.telnet) {
            p.telnet.localEcho = val('telnet.localEcho') || false;
            p.telnet.lineMode = val('telnet.lineMode') || false;
            p.telnet.terminalType = val('telnet.terminalType') || 'xterm-256color';
            p.telnet.encoding = val('telnet.encoding') || 'utf-8';
            p.telnet.lineEnding = val('telnet.lineEnding') || 'crlf';
            p.telnet.negotiationDebug = val('telnet.negotiationDebug') || false;
        }

        // RAW-specific
        if (p.protocol === 'raw' && p.raw) {
            p.raw.encoding = val('raw.encoding') || 'utf-8';
            p.raw.lineEnding = val('raw.lineEnding') || 'crlf';
            p.raw.showTimestamps = val('raw.showTimestamps') || false;
            p.raw.showDirection = val('raw.showDirection') || false;
            p.raw.autoReconnect = val('raw.autoReconnect') || false;

            // Presets
            const presets = [];
            let pi = 0;
            while (this.drawer.querySelector(`[name="preset-${pi}-name"]`)) {
                presets.push({
                    id: (p.raw.payloadPresets && p.raw.payloadPresets[pi]?.id) || generateId(),
                    name: val(`preset-${pi}-name`) || '',
                    payload: val(`preset-${pi}-payload`) || '',
                    format: val(`preset-${pi}-format`) || 'text',
                });
                pi++;
            }
            p.raw.payloadPresets = presets;
        }
    }

    // ── Validation ───────────────────────────────────────────────────

    /**
     * Validates the collected profile. Returns an array of error strings (empty = valid).
     * @param {Object} profile
     * @returns {string[]}
     * @private
     */
    _validate(profile) {
        const errors = [];
        if (!profile.host || !profile.host.trim()) {
            errors.push('Host is required.');
        }
        if (!profile.port || profile.port < 1 || profile.port > 65535) {
            errors.push('Port must be between 1 and 65535.');
        }
        return errors;
    }

    // ── Actions ──────────────────────────────────────────────────────

    /** @private */
    async _handleSave(andConnect = false) {
        this._collectFormValues();
        const errors = this._validate(this._profile);
        if (errors.length > 0) {
            this.app.notifications.warning(errors.join(' '));
            return;
        }

        try {
            let saved;
            if (this._isNew) {
                saved = await window.go.api.Service.CreateProfile(this._profile);
                this.app.state.addProfile(saved);
                this.app.notifications.success('Profile created');
            } else {
                saved = await window.go.api.Service.UpdateProfile(this._profile);
                this.app.state.updateProfile(saved);
                this.app.notifications.success('Profile updated');
            }
            this.app.sidebar.render();
            this.close();

            if (andConnect && saved && saved.id) {
                this.app.connectProfile(saved.id);
            }
        } catch (err) {
            this.app.notifications.error('Save failed: ' + err);
        }
    }

    /** @private */
    async _testConnection() {
        this._collectFormValues();
        const errors = this._validate(this._profile);
        if (errors.length > 0) {
            this.app.notifications.warning(errors.join(' '));
            return;
        }

        const host = this._profile.host;
        const port = this._profile.port;
        this.app.notifications.info(`Testing connection to ${host}:${port}…`);

        try {
            const result = await window.go.api.Service.TestConnection(host, port);
            this.app.notifications.success(`Reachable — ${result.latencyMs}ms latency`);
        } catch (err) {
            this.app.notifications.error('Test failed: ' + err);
        }
    }

    // ── Utilities ────────────────────────────────────────────────────

    /**
     * Returns a default profile object for the given protocol.
     * @param {string} protocol
     * @returns {Object}
     * @private
     */
    _defaultProfile(protocol) {
        const base = {
            id: generateId(),
            displayName: '',
            description: '',
            protocol,
            host: '',
            port: 0,
            username: '',
            tags: [],
            folder: '',
            environment: '',
            notes: '',
            favourite: false,
            lastConnected: null,
            connectTimeout: 30,
            readTimeout: 0,
            terminalFontFamily: 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
            terminalFontSize: 14,
            terminalScrollback: 10000,
            terminalCursorStyle: 'block',
            ssh: null,
            telnet: null,
            raw: null,
        };

        const defaults = DEFAULTS[protocol] || {};
        return { ...base, ...defaults };
    }

    /**
     * Converts an envVars map into "KEY=VALUE\n" text.
     * @param {Object} envVars
     * @returns {string}
     * @private
     */
    _envVarsToText(envVars) {
        if (!envVars) return '';
        return Object.entries(envVars).map(([k, v]) => `${k}=${v}`).join('\n');
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.ProfileEditor = ProfileEditor;

})();
