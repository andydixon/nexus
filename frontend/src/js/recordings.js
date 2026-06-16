(function() {
/**
 * Nexus Terminal — Session Recording & Playback.
 * Provides controls for starting/stopping recordings on active sessions,
 * listing saved recordings, and a terminal-based playback viewer with
 * speed controls and a progress scrubber.
 */

const { createElement, formatDuration } = window.NexusTerminal;

// ---------------------------------------------------------------------------
// Recording Manager
// ---------------------------------------------------------------------------

class RecordingManager {
    constructor(app) {
        this.app = app;
        /** @type {Set<string>} Session IDs currently being recorded. */
        this.activeSessions = new Set();
    }

    /**
     * Toggles recording for the given session.
     * @param {string} sessionId
     */
    async toggle(sessionId) {
        if (this.activeSessions.has(sessionId)) {
            await this.stop(sessionId);
        } else {
            await this.start(sessionId);
        }
    }

    /**
     * Starts recording a session.
     * @param {string} sessionId
     */
    async start(sessionId) {
        try {
            const entry = this.app.terminals.terminals.get(sessionId);
            const cols = entry ? entry.term.cols : 80;
            const rows = entry ? entry.term.rows : 24;

            await window.NexusTerminal.wailsService.StartRecording(sessionId, cols, rows);
            this.activeSessions.add(sessionId);
            this._updateRecordingIndicator(sessionId, true);
            this.app.notifications.success('Recording started');
        } catch (err) {
            this.app.notifications.error('Failed to start recording: ' + err);
        }
    }

    /**
     * Stops recording a session.
     * @param {string} sessionId
     */
    async stop(sessionId) {
        try {
            await window.NexusTerminal.wailsService.StopRecording(sessionId);
            this.activeSessions.delete(sessionId);
            this._updateRecordingIndicator(sessionId, false);
            this.app.notifications.success('Recording saved');
        } catch (err) {
            this.app.notifications.error('Failed to stop recording: ' + err);
        }
    }

    /**
     * Opens the recordings browser modal.
     */
    async openBrowser() {
        try {
            const [recordings, storageInfo] = await Promise.all([
                window.NexusTerminal.wailsService.ListRecordings(),
                window.NexusTerminal.wailsService.GetRecordingStorageInfo(),
            ]);
            this._showBrowserModal(recordings || [], storageInfo || {});
        } catch (err) {
            this.app.notifications.error('Failed to list recordings: ' + err);
        }
    }

    /**
     * Plays back a recording in a modal terminal viewer.
     * @param {string} id — Recording ID.
     */
    async playback(id) {
        try {
            const data = await window.NexusTerminal.wailsService.GetRecording(id);
            this._showPlaybackModal(data);
        } catch (err) {
            this.app.notifications.error('Failed to load recording: ' + err);
        }
    }

    /**
     * Deletes a recording by ID.
     * @param {string} id
     */
    async deleteRecording(id) {
        try {
            await window.NexusTerminal.wailsService.DeleteRecording(id);
            this.app.notifications.success('Recording deleted');
        } catch (err) {
            this.app.notifications.error('Failed to delete recording: ' + err);
        }
    }

    // ── Recording indicator on tabs ──────────────────────────────────

    /**
     * Updates the recording indicator on a session tab.
     * @param {string} sessionId
     * @param {boolean} recording
     * @private
     */
    _updateRecordingIndicator(sessionId, recording) {
        const tab = document.querySelector(`.session-tab[data-session-id="${sessionId}"]`);
        if (!tab) return;

        let dot = tab.querySelector('.recording-dot');
        if (recording) {
            if (!dot) {
                dot = createElement('span', 'recording-dot');
                dot.title = 'Recording';
                const label = tab.querySelector('.tab-label');
                if (label) label.parentNode.insertBefore(dot, label);
            }
        } else {
            if (dot) dot.remove();
        }
    }

    // ── Recordings browser modal ─────────────────────────────────────

    /**
     * Renders a modal listing all saved recordings.
     * @param {Array} recordings
     * @param {Object} storageInfo
     * @private
     */
    _showBrowserModal(recordings, storageInfo = {}) {
        const backdrop = createElement('div', 'modal-backdrop');
        const modal = createElement('div', 'modal modal-lg');

        let listHTML = '';
        if (recordings.length === 0) {
            listHTML = '<p class="text-muted" style="padding:1rem;text-align:center;">No recordings yet. Use the record button on a terminal tab to capture a session.</p>';
        } else {
            listHTML = '<div class="recordings-list">';
            for (const rec of recordings) {
                const date = new Date(rec.createdAt).toLocaleString();
                const dur = _formatSeconds(rec.duration);
                const size = _formatBytes(rec.size);
                listHTML += `
                    <div class="recording-item" data-id="${rec.id}">
                        <div class="recording-info">
                            <span class="recording-title">${_esc(rec.title || rec.id)}</span>
                            <span class="recording-meta">${date} · ${dur} · ${size} · ${rec.width}×${rec.height}</span>
                        </div>
                        <div class="recording-actions">
                            <button class="btn btn-xs btn-primary rec-play" data-id="${rec.id}" title="Play">▶</button>
                            <button class="btn btn-xs btn-danger rec-delete" data-id="${rec.id}" title="Delete">✕</button>
                        </div>
                    </div>
                `;
            }
            listHTML += '</div>';
        }

        const totalSize = _formatBytes(storageInfo.totalBytes || 0);
        const count = storageInfo.count ?? recordings.length;

        modal.innerHTML = `
            <div class="modal-header">
                <h3 class="modal-title">Session Recordings</h3>
                <button class="btn btn-icon btn-xs modal-close">&times;</button>
            </div>
            <div class="modal-body" style="max-height:60vh;overflow-y:auto;">
                <div class="recording-storage">
                    <div class="recording-storage-summary">
                        <span>${count} recording${count === 1 ? '' : 's'}</span>
                        <span>${totalSize}</span>
                    </div>
                    <div class="recording-cleanup-controls">
                        <select class="form-input recording-cleanup-age">
                            <option value="30">Older than 30 days</option>
                            <option value="7">Older than 7 days</option>
                            <option value="90">Older than 90 days</option>
                            <option value="0">Ignore age</option>
                        </select>
                        <select class="form-input recording-cleanup-size">
                            <option value="1073741824">Keep under 1 GB</option>
                            <option value="5368709120">Keep under 5 GB</option>
                            <option value="0">Ignore size</option>
                        </select>
                        <button class="btn btn-sm recording-cleanup">Clean up</button>
                    </div>
                    <div class="form-help recording-cleanup-status"></div>
                </div>
                ${listHTML}
            </div>
            <div class="modal-footer">
                <button class="btn btn-sm modal-close-btn">Close</button>
            </div>
        `;

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => backdrop.classList.add('is-visible'));

        const close = () => {
            backdrop.classList.remove('is-visible');
            setTimeout(() => backdrop.remove(), 200);
        };

        modal.querySelector('.modal-close').addEventListener('click', close);
        modal.querySelector('.modal-close-btn').addEventListener('click', close);

        modal.querySelector('.recording-cleanup').addEventListener('click', async () => {
            const ageDays = parseInt(modal.querySelector('.recording-cleanup-age').value, 10) || 0;
            const maxBytes = parseInt(modal.querySelector('.recording-cleanup-size').value, 10) || 0;
            const status = modal.querySelector('.recording-cleanup-status');
            const btn = modal.querySelector('.recording-cleanup');
            status.textContent = '';
            btn.disabled = true;
            try {
                const result = await window.NexusTerminal.wailsService.CleanupRecordings(ageDays, maxBytes);
                status.textContent = `Deleted ${result.deleted || 0}; freed ${_formatBytes(result.freedBytes || 0)}.`;
                const fresh = await window.NexusTerminal.wailsService.ListRecordings();
                close();
                this._showBrowserModal(fresh || [], await window.NexusTerminal.wailsService.GetRecordingStorageInfo());
            } catch (err) {
                status.textContent = 'Cleanup failed: ' + err;
            } finally {
                btn.disabled = false;
            }
        });

        // Play buttons
        modal.querySelectorAll('.rec-play').forEach(btn => {
            btn.addEventListener('click', () => {
                close();
                this.playback(btn.dataset.id);
            });
        });

        // Delete buttons
        modal.querySelectorAll('.rec-delete').forEach(btn => {
            btn.addEventListener('click', async () => {
                await this.deleteRecording(btn.dataset.id);
                const item = btn.closest('.recording-item');
                if (item) item.remove();
            });
        });
    }

    // ── Playback modal with embedded terminal ────────────────────────

    /**
     * Shows a playback modal with an xterm.js terminal, progress bar, and speed controls.
     * @param {Object} data — { header, events }
     * @private
     */
    _showPlaybackModal(data) {
        const header = data.header;
        const events = data.events || [];
        if (events.length === 0) {
            this.app.notifications.info('Recording is empty — nothing to play back.');
            return;
        }

        const totalDuration = events[events.length - 1][0] || 1;

        const backdrop = createElement('div', 'modal-backdrop');
        const modal = createElement('div', 'modal modal-playback');

        modal.innerHTML = `
            <div class="modal-header">
                <h3 class="modal-title">${_esc(header.title || 'Recording Playback')}</h3>
                <button class="btn btn-icon btn-xs modal-close">&times;</button>
            </div>
            <div class="modal-body playback-body">
                <div id="playback-terminal" class="playback-terminal"></div>
                <div class="playback-controls">
                    <button class="btn btn-xs btn-primary playback-play-pause" title="Play/Pause">▶</button>
                    <button class="btn btn-xs btn-outline playback-restart" title="Restart">⟲</button>
                    <input type="range" class="playback-progress" min="0" max="1000" value="0" />
                    <span class="playback-time">0:00 / ${_formatSeconds(totalDuration)}</span>
                    <select class="playback-speed form-input" style="width:auto;padding:2px 6px;font-size:11px;">
                        <option value="0.5">0.5×</option>
                        <option value="1" selected>1×</option>
                        <option value="2">2×</option>
                        <option value="5">5×</option>
                        <option value="10">10×</option>
                    </select>
                </div>
            </div>
        `;

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => backdrop.classList.add('is-visible'));

        // Create xterm instance for playback.
        const termContainer = modal.querySelector('#playback-terminal');
        const { getTerminalTheme } = window.NexusTerminal;
        const themeName = (this.app.state.preferences && this.app.state.preferences.terminalTheme) || 'charcoal';
        const theme = getTerminalTheme ? getTerminalTheme(themeName) : {};

        const term = new window.Terminal({
            cols: header.width || 80,
            rows: header.height || 24,
            theme: theme,
            fontFamily: '"JetBrains Mono", "Fira Code", "Cascadia Code", "Menlo", monospace',
            fontSize: 13,
            cursorBlink: false,
            disableStdin: true,
            scrollback: 10000,
        });
        term.open(termContainer);

        const fitAddon = new window.FitAddon.FitAddon();
        term.loadAddon(fitAddon);
        setTimeout(() => fitAddon.fit(), 50);

        // Playback state.
        let playing = false;
        let speed = 1;
        let eventIndex = 0;
        let playbackTimer = null;
        let startRealTime = 0;
        let pausedAt = 0; // elapsed virtual time when paused

        const playPauseBtn = modal.querySelector('.playback-play-pause');
        const restartBtn = modal.querySelector('.playback-restart');
        const progressInput = modal.querySelector('.playback-progress');
        const timeLabel = modal.querySelector('.playback-time');
        const speedSelect = modal.querySelector('.playback-speed');

        const updateTimeDisplay = (elapsed) => {
            timeLabel.textContent = `${_formatSeconds(elapsed)} / ${_formatSeconds(totalDuration)}`;
            progressInput.value = Math.round((elapsed / totalDuration) * 1000);
        };

        const scheduleNext = () => {
            if (!playing || eventIndex >= events.length) {
                if (eventIndex >= events.length) {
                    playing = false;
                    playPauseBtn.textContent = '▶';
                }
                return;
            }

            const evt = events[eventIndex];
            const evtTime = evt[0]; // seconds from start
            const realElapsed = (Date.now() - startRealTime) / 1000;
            const virtualElapsed = pausedAt + realElapsed * speed;
            const delay = Math.max(0, (evtTime - virtualElapsed) / speed * 1000);

            playbackTimer = setTimeout(() => {
                if (!playing) return;

                // Write output events to the terminal.
                if (evt[1] === 'o') {
                    term.write(evt[2]);
                }

                eventIndex++;
                const currentVirtual = pausedAt + ((Date.now() - startRealTime) / 1000) * speed;
                updateTimeDisplay(Math.min(currentVirtual, totalDuration));
                scheduleNext();
            }, delay);
        };

        const play = () => {
            if (eventIndex >= events.length) {
                // Restart from beginning.
                eventIndex = 0;
                pausedAt = 0;
                term.reset();
            }
            playing = true;
            playPauseBtn.textContent = '⏸';
            startRealTime = Date.now();
            scheduleNext();
        };

        const pause = () => {
            playing = false;
            playPauseBtn.textContent = '▶';
            if (playbackTimer) {
                clearTimeout(playbackTimer);
                playbackTimer = null;
            }
            const realElapsed = (Date.now() - startRealTime) / 1000;
            pausedAt = pausedAt + realElapsed * speed;
        };

        const seekTo = (fraction) => {
            const wasPlaying = playing;
            if (playing) pause();

            const targetTime = fraction * totalDuration;
            term.reset();
            eventIndex = 0;

            // Replay all events up to targetTime.
            for (let i = 0; i < events.length; i++) {
                if (events[i][0] > targetTime) break;
                if (events[i][1] === 'o') {
                    term.write(events[i][2]);
                }
                eventIndex = i + 1;
            }

            pausedAt = targetTime;
            updateTimeDisplay(targetTime);

            if (wasPlaying) play();
        };

        playPauseBtn.addEventListener('click', () => {
            if (playing) pause(); else play();
        });

        restartBtn.addEventListener('click', () => {
            if (playing) pause();
            term.reset();
            eventIndex = 0;
            pausedAt = 0;
            updateTimeDisplay(0);
        });

        progressInput.addEventListener('input', () => {
            seekTo(parseInt(progressInput.value, 10) / 1000);
        });

        speedSelect.addEventListener('change', () => {
            const newSpeed = parseFloat(speedSelect.value);
            if (playing) {
                const realElapsed = (Date.now() - startRealTime) / 1000;
                pausedAt = pausedAt + realElapsed * speed;
                speed = newSpeed;
                startRealTime = Date.now();
                if (playbackTimer) clearTimeout(playbackTimer);
                scheduleNext();
            } else {
                speed = newSpeed;
            }
        });

        // Auto-play on open.
        setTimeout(play, 200);

        // Close handler.
        const close = () => {
            if (playing) pause();
            term.dispose();
            backdrop.classList.remove('is-visible');
            setTimeout(() => backdrop.remove(), 200);
        };

        modal.querySelector('.modal-close').addEventListener('click', close);
    }
}

// ---------------------------------------------------------------------------
// Encrypted Export/Import UI
// ---------------------------------------------------------------------------

class ExportImportUI {
    constructor(app) {
        this.app = app;
    }

    /**
     * Shows the encrypted export modal.
     */
    async showExportModal() {
        const profiles = this.app.state.profiles || [];
        if (profiles.length === 0) {
            this.app.notifications.info('No profiles to export.');
            return;
        }

        const backdrop = createElement('div', 'modal-backdrop');
        const modal = createElement('div', 'modal modal-md');

        let profileListHTML = profiles.map(p => `
            <label class="export-profile-item">
                <input type="checkbox" value="${p.id}" checked />
                <span>${_esc(p.displayName || p.host)} <small class="text-muted">(${p.protocol})</small></span>
            </label>
        `).join('');

        modal.innerHTML = `
            <div class="modal-header">
                <h3 class="modal-title">Export Profiles (Encrypted)</h3>
                <button class="btn btn-icon btn-xs modal-close">&times;</button>
            </div>
            <div class="modal-body">
                <div class="form-group">
                    <label class="form-label">Select profiles to export</label>
                    <div class="export-profile-list" style="max-height:200px;overflow-y:auto;margin-bottom:12px;">
                        ${profileListHTML}
                    </div>
                </div>
                <div class="form-group">
                    <label class="form-label">Export password</label>
                    <input type="password" class="form-input export-password" placeholder="Enter a strong password" />
                    <small class="text-muted">This password will be required to import the file.</small>
                </div>
                <div class="form-group">
                    <label class="form-label">Confirm password</label>
                    <input type="password" class="form-input export-password-confirm" placeholder="Confirm password" />
                </div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-sm export-cancel">Cancel</button>
                <button class="btn btn-sm btn-primary export-confirm">Export</button>
            </div>
        `;

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => backdrop.classList.add('is-visible'));

        const close = () => {
            backdrop.classList.remove('is-visible');
            setTimeout(() => backdrop.remove(), 200);
        };

        modal.querySelector('.modal-close').addEventListener('click', close);
        modal.querySelector('.export-cancel').addEventListener('click', close);

        modal.querySelector('.export-confirm').addEventListener('click', async () => {
            const password = modal.querySelector('.export-password').value;
            const confirm = modal.querySelector('.export-password-confirm').value;

            if (!password) {
                this.app.notifications.error('Please enter a password.');
                return;
            }
            if (password !== confirm) {
                this.app.notifications.error('Passwords do not match.');
                return;
            }

            const checkboxes = modal.querySelectorAll('.export-profile-list input[type="checkbox"]:checked');
            const selectedIds = Array.from(checkboxes).map(cb => cb.value);

            if (selectedIds.length === 0) {
                this.app.notifications.error('Please select at least one profile.');
                return;
            }

            try {
                const path = await window.NexusTerminal.wailsService.ExportProfilesEncrypted(selectedIds, password);
                this.app.notifications.success(`Exported ${selectedIds.length} profile(s) to ${path}`);
                close();
            } catch (err) {
                if (String(err).includes('cancelled')) {
                    return;
                }
                this.app.notifications.error('Export failed: ' + err);
            }
        });
    }

    /**
     * Shows the encrypted import modal.
     */
    async showImportModal() {
        const backdrop = createElement('div', 'modal-backdrop');
        const modal = createElement('div', 'modal modal-md');

        modal.innerHTML = `
            <div class="modal-header">
                <h3 class="modal-title">Import Profiles (Encrypted)</h3>
                <button class="btn btn-icon btn-xs modal-close">&times;</button>
            </div>
            <div class="modal-body">
                <div class="form-group">
                    <label class="form-label">Decryption password</label>
                    <input type="password" class="form-input import-password" placeholder="Enter the export password" />
                </div>
                <div class="form-group">
                    <label class="form-label">Conflict strategy</label>
                    <select class="form-input import-strategy">
                        <option value="skip">Skip existing</option>
                        <option value="overwrite">Overwrite existing</option>
                        <option value="duplicate">Create duplicates</option>
                    </select>
                </div>
                <div class="form-error import-status" aria-live="polite"></div>
            </div>
            <div class="modal-footer">
                <button class="btn btn-sm import-cancel">Cancel</button>
                <button class="btn btn-sm btn-primary import-confirm">Import</button>
            </div>
        `;

        backdrop.appendChild(modal);
        document.body.appendChild(backdrop);
        requestAnimationFrame(() => backdrop.classList.add('is-visible'));

        const close = () => {
            backdrop.classList.remove('is-visible');
            setTimeout(() => backdrop.remove(), 200);
        };

        modal.querySelector('.modal-close').addEventListener('click', close);
        modal.querySelector('.import-cancel').addEventListener('click', close);

        modal.querySelector('.import-confirm').addEventListener('click', async () => {
            const password = modal.querySelector('.import-password').value;
            const strategy = modal.querySelector('.import-strategy').value;
            const status = modal.querySelector('.import-status');
            const confirmBtn = modal.querySelector('.import-confirm');
            status.textContent = '';

            if (!password) {
                status.textContent = 'Please enter the decryption password.';
                return;
            }

            try {
                confirmBtn.disabled = true;
                confirmBtn.textContent = 'Importing...';
                const count = await window.NexusTerminal.wailsService.ImportProfilesEncrypted(password, strategy);
                this.app.notifications.success(`Imported ${count} profile(s)`);
                await this.app.loadProfiles();
                close();
            } catch (err) {
                if (String(err).includes('cancelled')) {
                    return;
                }
                status.textContent = this._formatImportError(err);
                this.app.notifications.error('Import failed');
            } finally {
                confirmBtn.disabled = false;
                confirmBtn.textContent = 'Import';
            }
        });
    }

    _formatImportError(err) {
        const msg = String(err || 'Unknown import error');
        return msg.replace(/\s+/g, ' ').trim();
    }
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

function _esc(s) {
    const el = document.createElement('span');
    el.textContent = s || '';
    return el.innerHTML;
}

function _formatSeconds(secs) {
    if (!secs || secs <= 0) return '0:00';
    const m = Math.floor(secs / 60);
    const s = Math.floor(secs % 60);
    return `${m}:${s.toString().padStart(2, '0')}`;
}

function _formatBytes(bytes) {
    if (!bytes || bytes <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB'];
    let i = 0;
    let val = bytes;
    while (val >= 1024 && i < units.length - 1) {
        val /= 1024;
        i++;
    }
    return `${val.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

// ---------------------------------------------------------------------------
// Export to global namespace
// ---------------------------------------------------------------------------

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.RecordingManager = RecordingManager;
window.NexusTerminal.ExportImportUI = ExportImportUI;

})();
