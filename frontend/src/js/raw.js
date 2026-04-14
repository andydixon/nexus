/**
 * Nexus Terminal — RAW TCP data viewer.
 * Provides text, hex, and split display modes for raw socket data,
 * along with a send bar, pause/resume, and capture saving.
 */

const { createElement, escapeHtml, formatTimestamp, formatBytes } = window.NexusTerminal;

/**
 * Maximum number of data entries retained in the scrollback buffer.
 * @type {number}
 */
const MAX_BUFFER_ENTRIES = 5000;

class RawViewer {
    /**
     * @param {import('./app.js').App} app
     */
    constructor(app) {
        this.app = app;

        /**
         * Map of sessionId → viewer state.
         * @type {Map<string, Object>}
         */
        this.viewers = new Map();
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Creates a RAW viewer inside the given panel container.
     * @param {string} sessionId
     * @param {string} containerId - DOM id of the panel element.
     * @param {Object} profile
     */
    createViewer(sessionId, containerId, profile) {
        const container = document.getElementById(containerId);
        if (!container) return;

        const state = {
            mode: 'text', // text | hex | split
            paused: false,
            buffer: [],
            totalBytes: 0,
            sendFormat: 'text', // text | hex | escaped
        };

        // ── Toolbar ──────────────────────────────────────────────
        const toolbar = createElement('div', 'raw-toolbar');

        // Mode toggle
        const modeGroup = createElement('div', 'raw-mode-group');
        for (const mode of ['text', 'hex', 'split']) {
            const btn = createElement('button', `raw-mode-btn ${mode === state.mode ? 'active' : ''}`, {
                textContent: mode.charAt(0).toUpperCase() + mode.slice(1),
                dataset: { mode },
            });
            btn.addEventListener('click', () => this._setMode(sessionId, mode));
            modeGroup.appendChild(btn);
        }
        toolbar.appendChild(modeGroup);

        // Controls
        const controls = createElement('div', 'raw-controls');

        const pauseBtn = createElement('button', 'btn btn-sm btn-outline raw-pause', { textContent: '⏸ Pause' });
        pauseBtn.addEventListener('click', () => this.togglePause(sessionId));
        controls.appendChild(pauseBtn);

        const clearBtn = createElement('button', 'btn btn-sm btn-outline', { textContent: '✕ Clear' });
        clearBtn.addEventListener('click', () => this.clearBuffer(sessionId));
        controls.appendChild(clearBtn);

        const saveBtn = createElement('button', 'btn btn-sm btn-outline', { textContent: '💾 Save' });
        saveBtn.addEventListener('click', () => this.saveCapture(sessionId));
        controls.appendChild(saveBtn);

        // Byte counter
        const counter = createElement('span', 'raw-counter', { textContent: '0 B' });
        controls.appendChild(counter);

        toolbar.appendChild(controls);
        container.appendChild(toolbar);

        // ── Data display ─────────────────────────────────────────
        const display = createElement('div', 'raw-display');
        container.appendChild(display);

        // ── Send bar ─────────────────────────────────────────────
        const sendBar = this._renderSendBar(sessionId, profile);
        container.appendChild(sendBar);

        this.viewers.set(sessionId, {
            ...state,
            container,
            display,
            toolbar,
            pauseBtn,
            counter,
            profile,
        });
    }

    /**
     * Processes incoming data from the backend.
     * @param {Object} payload - { sessionID, data, direction, timestamp }
     */
    handleData(payload) {
        const viewer = this.viewers.get(payload.sessionID);
        if (!viewer) return;

        // Decode base64 payload
        let rawStr;
        try {
            rawStr = atob(payload.data);
        } catch (_) {
            rawStr = payload.data || '';
        }

        const bytes = new Uint8Array(rawStr.length);
        for (let i = 0; i < rawStr.length; i++) {
            bytes[i] = rawStr.charCodeAt(i);
        }

        const entry = {
            direction: payload.direction || 'recv', // 'send' | 'recv'
            timestamp: payload.timestamp ? new Date(payload.timestamp) : new Date(),
            data: rawStr,
            bytes,
        };

        viewer.buffer.push(entry);
        viewer.totalBytes += bytes.length;

        // Trim buffer if it exceeds the maximum
        if (viewer.buffer.length > MAX_BUFFER_ENTRIES) {
            viewer.buffer.splice(0, viewer.buffer.length - MAX_BUFFER_ENTRIES);
        }

        // Update counter
        viewer.counter.textContent = formatBytes(viewer.totalBytes);

        // Render unless paused
        if (!viewer.paused) {
            this._renderEntry(viewer, entry);
            this._scrollToBottom(viewer);
        }
    }

    /**
     * Toggles pause/resume on the data display.
     * @param {string} sessionId
     */
    togglePause(sessionId) {
        const viewer = this.viewers.get(sessionId);
        if (!viewer) return;

        viewer.paused = !viewer.paused;
        viewer.pauseBtn.textContent = viewer.paused ? '▶ Resume' : '⏸ Pause';
        viewer.pauseBtn.classList.toggle('active', viewer.paused);

        // On resume, flush any buffered entries
        if (!viewer.paused) {
            this._renderAllEntries(viewer);
        }
    }

    /**
     * Clears the data buffer and display.
     * @param {string} sessionId
     */
    clearBuffer(sessionId) {
        const viewer = this.viewers.get(sessionId);
        if (!viewer) return;

        viewer.buffer = [];
        viewer.totalBytes = 0;
        viewer.display.innerHTML = '';
        viewer.counter.textContent = '0 B';
    }

    /**
     * Prompts the user to save the capture buffer.
     * @param {string} sessionId
     */
    async saveCapture(sessionId) {
        const viewer = this.viewers.get(sessionId);
        if (!viewer || viewer.buffer.length === 0) {
            this.app.notifications.warning('Nothing to save');
            return;
        }

        try {
            const lines = viewer.buffer.map((e) => {
                const ts = formatTimestamp(e.timestamp);
                const dir = e.direction === 'send' ? 'TX' : 'RX';
                return `[${ts}] [${dir}] ${e.data}`;
            });
            const content = lines.join('\n');
            await window.go.api.Service.SaveSessionCapture(sessionId, content);
            this.app.notifications.success('Capture saved');
        } catch (err) {
            this.app.notifications.error('Save failed: ' + err);
        }
    }

    /**
     * Sends data from the send bar to the backend.
     * @param {string} sessionId
     */
    async send(sessionId) {
        const viewer = this.viewers.get(sessionId);
        if (!viewer) return;

        const input = viewer.container.querySelector('.raw-send-input');
        const formatSelect = viewer.container.querySelector('.raw-send-format');
        if (!input) return;

        const rawValue = input.value;
        if (!rawValue) return;

        const format = formatSelect?.value || 'text';
        let dataToSend;

        switch (format) {
            case 'hex':
                dataToSend = this._hexToString(rawValue);
                break;
            case 'escaped':
                dataToSend = this._unescapeString(rawValue);
                break;
            default:
                dataToSend = rawValue;
        }

        try {
            const b64 = btoa(dataToSend);
            await window.go.api.Service.WriteSession(sessionId, b64);
            input.value = '';

            // Also show in viewer as sent data
            this.handleData({
                sessionID: sessionId,
                data: b64,
                direction: 'send',
                timestamp: new Date().toISOString(),
            });
        } catch (err) {
            this.app.notifications.error('Send failed: ' + err);
        }
    }

    /**
     * Tears down a viewer and frees resources.
     * @param {string} sessionId
     */
    destroyViewer(sessionId) {
        this.viewers.delete(sessionId);
    }

    // ── Rendering ────────────────────────────────────────────────────

    /**
     * Renders the send bar at the bottom of the viewer.
     * @param {string} sessionId
     * @param {Object} profile
     * @returns {HTMLElement}
     * @private
     */
    _renderSendBar(sessionId, profile) {
        const bar = createElement('div', 'raw-send-bar');

        // Format selector
        const formatSelect = createElement('select', 'form-select form-select-sm raw-send-format');
        for (const [value, label] of [['text', 'Text'], ['hex', 'Hex'], ['escaped', 'Escaped']]) {
            const opt = createElement('option', '', { value, textContent: label });
            formatSelect.appendChild(opt);
        }
        bar.appendChild(formatSelect);

        // Input
        const input = createElement('input', 'form-input raw-send-input', {
            type: 'text',
            placeholder: 'Type data to send…',
        });
        input.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') this.send(sessionId);
        });
        bar.appendChild(input);

        // Quick-insert buttons for common control bytes
        const quickBtns = createElement('div', 'raw-quick-btns');
        const controlBytes = [
            { label: 'NUL', value: '\\x00' },
            { label: 'CR', value: '\\r' },
            { label: 'LF', value: '\\n' },
            { label: 'ETX', value: '\\x03' },
            { label: 'EOT', value: '\\x04' },
        ];
        for (const cb of controlBytes) {
            const btn = createElement('button', 'btn btn-xs btn-outline', { textContent: cb.label, title: cb.value });
            btn.addEventListener('click', () => {
                input.value += cb.value;
                input.focus();
            });
            quickBtns.appendChild(btn);
        }
        bar.appendChild(quickBtns);

        // Payload preset dropdown
        const presets = profile?.raw?.payloadPresets || [];
        if (presets.length > 0) {
            const presetSelect = createElement('select', 'form-select form-select-sm raw-preset-select');
            const defaultOpt = createElement('option', '', { value: '', textContent: 'Presets…' });
            presetSelect.appendChild(defaultOpt);
            for (const ps of presets) {
                const opt = createElement('option', '', { value: ps.payload, textContent: ps.name });
                opt.dataset.format = ps.format;
                presetSelect.appendChild(opt);
            }
            presetSelect.addEventListener('change', () => {
                if (presetSelect.value) {
                    input.value = presetSelect.value;
                    const selOpt = presetSelect.selectedOptions[0];
                    if (selOpt?.dataset.format) {
                        formatSelect.value = selOpt.dataset.format;
                    }
                    presetSelect.value = '';
                    input.focus();
                }
            });
            bar.appendChild(presetSelect);
        }

        // Send button
        const sendBtn = createElement('button', 'btn btn-sm btn-primary', { textContent: 'Send' });
        sendBtn.addEventListener('click', () => this.send(sessionId));
        bar.appendChild(sendBtn);

        return bar;
    }

    /**
     * Renders a single data entry into the display area.
     * @param {Object} viewer
     * @param {Object} entry
     * @private
     */
    _renderEntry(viewer, entry) {
        let el;
        switch (viewer.mode) {
            case 'hex':
                el = this._renderHexView(entry);
                break;
            case 'split':
                el = this._renderSplitView(entry, viewer.profile);
                break;
            default:
                el = this._renderTextView(entry, viewer.profile);
        }
        viewer.display.appendChild(el);
    }

    /**
     * Re-renders all buffered entries (e.g. after mode change or resume).
     * @param {Object} viewer
     * @private
     */
    _renderAllEntries(viewer) {
        viewer.display.innerHTML = '';
        for (const entry of viewer.buffer) {
            this._renderEntry(viewer, entry);
        }
        this._scrollToBottom(viewer);
    }

    /**
     * Renders a text-mode entry.
     * @param {Object} entry
     * @param {Object} profile
     * @returns {HTMLElement}
     * @private
     */
    _renderTextView(entry, profile) {
        const row = createElement('div', `raw-entry raw-entry-${entry.direction}`);
        const raw = profile?.raw || {};

        let prefix = '';
        if (raw.showTimestamps) {
            prefix += `[${formatTimestamp(entry.timestamp)}] `;
        }
        if (raw.showDirection) {
            prefix += entry.direction === 'send' ? '[TX] ' : '[RX] ';
        }

        const pre = createElement('pre', 'raw-text');
        pre.textContent = prefix + entry.data;
        row.appendChild(pre);
        return row;
    }

    /**
     * Renders a hex-mode entry.
     * @param {Object} entry
     * @returns {HTMLElement}
     * @private
     */
    _renderHexView(entry) {
        const row = createElement('div', `raw-entry raw-entry-${entry.direction}`);
        const pre = createElement('pre', 'raw-hex');
        pre.textContent = this.formatHexDump(entry.bytes);
        row.appendChild(pre);
        return row;
    }

    /**
     * Renders a split-mode entry (text + hex side by side).
     * @param {Object} entry
     * @param {Object} profile
     * @returns {HTMLElement}
     * @private
     */
    _renderSplitView(entry, profile) {
        const row = createElement('div', `raw-entry raw-entry-split raw-entry-${entry.direction}`);
        const raw = profile?.raw || {};

        // Direction/timestamp header
        const header = createElement('div', 'raw-split-header');
        let headerText = '';
        if (raw.showTimestamps) headerText += formatTimestamp(entry.timestamp) + ' ';
        headerText += entry.direction === 'send' ? '▲ TX' : '▼ RX';
        header.textContent = headerText;
        row.appendChild(header);

        // Split panes
        const panes = createElement('div', 'raw-split-panes');

        const textPane = createElement('pre', 'raw-text raw-split-text');
        textPane.textContent = entry.data;
        panes.appendChild(textPane);

        const hexPane = createElement('pre', 'raw-hex raw-split-hex');
        hexPane.textContent = this.formatHexDump(entry.bytes);
        panes.appendChild(hexPane);

        row.appendChild(panes);
        return row;
    }

    /**
     * Formats a Uint8Array into a traditional hex dump:
     * offset | hex bytes | ASCII representation.
     * @param {Uint8Array} bytes
     * @returns {string}
     */
    formatHexDump(bytes) {
        const lines = [];
        const bytesPerLine = 16;

        for (let offset = 0; offset < bytes.length; offset += bytesPerLine) {
            const chunk = bytes.slice(offset, offset + bytesPerLine);

            // Offset column
            const offsetStr = offset.toString(16).padStart(8, '0');

            // Hex column
            const hexParts = [];
            for (let i = 0; i < bytesPerLine; i++) {
                if (i < chunk.length) {
                    hexParts.push(chunk[i].toString(16).padStart(2, '0'));
                } else {
                    hexParts.push('  ');
                }
                // Extra space after 8th byte for readability
                if (i === 7) hexParts.push('');
            }
            const hexStr = hexParts.join(' ');

            // ASCII column
            let ascii = '';
            for (let i = 0; i < chunk.length; i++) {
                const b = chunk[i];
                ascii += (b >= 0x20 && b <= 0x7e) ? String.fromCharCode(b) : '.';
            }

            lines.push(`${offsetStr}  ${hexStr}  |${ascii}|`);
        }

        return lines.join('\n');
    }

    // ── Internal helpers ─────────────────────────────────────────────

    /**
     * Changes the display mode (text, hex, split) and re-renders.
     * @param {string} sessionId
     * @param {string} mode
     * @private
     */
    _setMode(sessionId, mode) {
        const viewer = this.viewers.get(sessionId);
        if (!viewer) return;

        viewer.mode = mode;

        // Update active button
        viewer.toolbar.querySelectorAll('.raw-mode-btn').forEach((btn) => {
            btn.classList.toggle('active', btn.dataset.mode === mode);
        });

        this._renderAllEntries(viewer);
    }

    /**
     * Scrolls the data display to the bottom.
     * @param {Object} viewer
     * @private
     */
    _scrollToBottom(viewer) {
        requestAnimationFrame(() => {
            viewer.display.scrollTop = viewer.display.scrollHeight;
        });
    }

    /**
     * Converts a hex string (e.g. "48656c6c6f") into a character string.
     * @param {string} hex
     * @returns {string}
     * @private
     */
    _hexToString(hex) {
        const cleaned = hex.replace(/\s+/g, '');
        let str = '';
        for (let i = 0; i < cleaned.length; i += 2) {
            str += String.fromCharCode(parseInt(cleaned.substring(i, i + 2), 16));
        }
        return str;
    }

    /**
     * Converts C-style escape sequences (\\n, \\r, \\x00, etc.) into their literal bytes.
     * @param {string} str
     * @returns {string}
     * @private
     */
    _unescapeString(str) {
        return str.replace(/\\(n|r|t|0|x[0-9a-fA-F]{2}|\\)/g, (match, seq) => {
            switch (seq) {
                case 'n': return '\n';
                case 'r': return '\r';
                case 't': return '\t';
                case '0': return '\0';
                case '\\': return '\\';
                default:
                    // \\xHH
                    if (seq.startsWith('x')) {
                        return String.fromCharCode(parseInt(seq.slice(1), 16));
                    }
                    return match;
            }
        });
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.RawViewer = RawViewer;
