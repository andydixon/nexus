(function() {
/**
 * Nexus Terminal — secret/passphrase prompt dialogs.
 * Provides modal prompts for collecting passwords, master-vault passphrases,
 * and SSH host-key verification confirmations.
 */

const { createElement, escapeHtml } = window.NexusTerminal;

class SecretPrompt {
    constructor(app) {
        /** @type {App} */
        this.app = app;

        /** @type {HTMLElement|null} Currently open overlay. */
        this._overlay = null;
    }

    // ── Generic password prompt ──────────────────────────────────────

    /**
     * Displays a modal with a single password field and returns the entered value.
     * Rejects if the user cancels.
     * @param {string} title - Dialog heading.
     * @param {string} message - Instructional text shown above the input.
     * @returns {Promise<string>}
     */
    prompt(title, message) {
        return new Promise((resolve, reject) => {
            this._close(); // dismiss any existing prompt

            const overlay = createElement('div', 'modal-backdrop');
            const modal = createElement('div', 'modal modal-secret');

            // Header
            const header = createElement('div', 'modal-header');
            header.innerHTML = `
                <h2 class="modal-title">${escapeHtml(title)}</h2>
                <button class="modal-close" aria-label="Close">&times;</button>
            `;
            modal.appendChild(header);

            // Body
            const body = createElement('div', 'modal-body');
            body.innerHTML = `
                <p class="secret-message">${escapeHtml(message)}</p>
                <div class="form-group">
                    <input type="password" class="form-input secret-input"
                           autocomplete="off" spellcheck="false" placeholder="Enter value…" />
                </div>
            `;
            modal.appendChild(body);

            // Footer
            const footer = createElement('div', 'modal-footer');
            const cancelBtn = createElement('button', 'btn btn-secondary', { textContent: 'Cancel' });
            const submitBtn = createElement('button', 'btn btn-primary', { textContent: 'Submit' });
            footer.appendChild(cancelBtn);
            footer.appendChild(submitBtn);
            modal.appendChild(footer);

            overlay.appendChild(modal);
            document.body.appendChild(overlay);
            this._overlay = overlay;

            requestAnimationFrame(() => {
                overlay.classList.add('is-visible');
                modal.classList.add('is-visible');
            });

            const input = modal.querySelector('.secret-input');
            input.focus();

            const handleSubmit = () => {
                const value = input.value;
                this._close();
                resolve(value);
            };

            const handleCancel = () => {
                this._close();
                reject(new Error('User cancelled'));
            };

            submitBtn.addEventListener('click', handleSubmit);
            cancelBtn.addEventListener('click', handleCancel);
            header.querySelector('.modal-close').addEventListener('click', handleCancel);
            overlay.addEventListener('click', (e) => {
                if (e.target === overlay) handleCancel();
            });

            input.addEventListener('keydown', (e) => {
                if (e.key === 'Enter') handleSubmit();
                if (e.key === 'Escape') handleCancel();
            });
        });
    }

    // ── Master password (vault initialisation / unlock) ──────────────

    /**
     * Prompts for the master vault password.
     * When `isNew` is true, a confirmation field is shown.
     * @param {boolean} isNew - Whether this is the first time (create vault).
     * @returns {Promise<string>} The master password.
     */
    promptMasterPassword(isNew) {
        return new Promise((resolve, reject) => {
            this._close();

            const overlay = createElement('div', 'modal-backdrop');
            const modal = createElement('div', 'modal modal-secret');

            const title = isNew ? 'Create Master Password' : 'Unlock Secret Store';
            const description = isNew
                ? 'Choose a strong master password. This protects all saved credentials.'
                : 'Enter your master password to unlock the secret store.';

            const header = createElement('div', 'modal-header');
            header.innerHTML = `
                <h2 class="modal-title">${escapeHtml(title)}</h2>
                <button class="modal-close" aria-label="Close">&times;</button>
            `;
            modal.appendChild(header);

            const body = createElement('div', 'modal-body');
            let formHtml = `
                <p class="secret-message">${escapeHtml(description)}</p>
                <div class="form-group">
                    <label class="form-label">Master password</label>
                    <input type="password" class="form-input master-password"
                           autocomplete="off" spellcheck="false" placeholder="Password…" />
                </div>
            `;
            if (isNew) {
                formHtml += `
                    <div class="form-group">
                        <label class="form-label">Confirm password</label>
                        <input type="password" class="form-input master-confirm"
                               autocomplete="off" spellcheck="false" placeholder="Confirm…" />
                    </div>
                    <p class="form-hint secret-error" style="display:none;"></p>
                `;
            }
            body.innerHTML = formHtml;
            modal.appendChild(body);

            const footer = createElement('div', 'modal-footer');
            const cancelBtn = createElement('button', 'btn btn-secondary', { textContent: 'Cancel' });
            const submitBtn = createElement('button', 'btn btn-primary', { textContent: isNew ? 'Create' : 'Unlock' });
            footer.appendChild(cancelBtn);
            footer.appendChild(submitBtn);
            modal.appendChild(footer);

            overlay.appendChild(modal);
            document.body.appendChild(overlay);
            this._overlay = overlay;

            requestAnimationFrame(() => {
                overlay.classList.add('is-visible');
                modal.classList.add('is-visible');
            });

            const passInput = modal.querySelector('.master-password');
            const confirmInput = modal.querySelector('.master-confirm');
            const errorEl = modal.querySelector('.secret-error');
            passInput.focus();

            const handleSubmit = () => {
                const pw = passInput.value;
                if (!pw) {
                    passInput.classList.add('input-error');
                    return;
                }
                if (isNew) {
                    const confirm = confirmInput.value;
                    if (pw !== confirm) {
                        errorEl.textContent = 'Passwords do not match.';
                        errorEl.style.display = 'block';
                        confirmInput.classList.add('input-error');
                        return;
                    }
                }
                this._close();
                resolve(pw);
            };

            const handleCancel = () => {
                this._close();
                reject(new Error('User cancelled'));
            };

            submitBtn.addEventListener('click', handleSubmit);
            cancelBtn.addEventListener('click', handleCancel);
            header.querySelector('.modal-close').addEventListener('click', handleCancel);
            overlay.addEventListener('click', (e) => {
                if (e.target === overlay) handleCancel();
            });

            const handleKeydown = (e) => {
                if (e.key === 'Enter') handleSubmit();
                if (e.key === 'Escape') handleCancel();
            };
            passInput.addEventListener('keydown', handleKeydown);
            if (confirmInput) {
                confirmInput.addEventListener('keydown', handleKeydown);
            }
        });
    }

    // ── SSH host-key verification ────────────────────────────────────

    /**
     * Asks the user whether to accept an SSH host key.
     * @param {string} host
     * @param {string} keyType - e.g. "ssh-ed25519"
     * @param {string} fingerprint - SHA256 fingerprint
     * @returns {Promise<boolean>} true = accept, false = reject.
     */
    promptHostKeyVerification(host, keyType, fingerprint) {
        return new Promise((resolve) => {
            this._close();

            const overlay = createElement('div', 'modal-backdrop');
            const modal = createElement('div', 'modal modal-secret modal-hostkey');

            const header = createElement('div', 'modal-header');
            header.innerHTML = `
                <h2 class="modal-title">Unknown Host Key</h2>
                <button class="modal-close" aria-label="Close">&times;</button>
            `;
            modal.appendChild(header);

            const body = createElement('div', 'modal-body');
            body.innerHTML = `
                <p class="secret-message">
                    The authenticity of host <strong>${escapeHtml(host)}</strong> cannot be established.
                </p>
                <div class="hostkey-details">
                    <div class="hostkey-row">
                        <span class="hostkey-label">Key type</span>
                        <code class="hostkey-value">${escapeHtml(keyType)}</code>
                    </div>
                    <div class="hostkey-row">
                        <span class="hostkey-label">Fingerprint</span>
                        <code class="hostkey-value">${escapeHtml(fingerprint)}</code>
                    </div>
                </div>
                <p class="secret-message">
                    Are you sure you want to continue connecting?
                </p>
            `;
            modal.appendChild(body);

            const footer = createElement('div', 'modal-footer');
            const rejectBtn = createElement('button', 'btn btn-secondary', { textContent: 'Reject' });
            const acceptBtn = createElement('button', 'btn btn-primary', { textContent: 'Accept & Save' });
            footer.appendChild(rejectBtn);
            footer.appendChild(acceptBtn);
            modal.appendChild(footer);

            overlay.appendChild(modal);
            document.body.appendChild(overlay);
            this._overlay = overlay;

            acceptBtn.focus();

            acceptBtn.addEventListener('click', () => {
                this._close();
                resolve(true);
            });

            rejectBtn.addEventListener('click', () => {
                this._close();
                resolve(false);
            });

            header.querySelector('.modal-close').addEventListener('click', () => {
                this._close();
                resolve(false);
            });

            overlay.addEventListener('click', (e) => {
                if (e.target === overlay) {
                    this._close();
                    resolve(false);
                }
            });

            overlay.addEventListener('keydown', (e) => {
                if (e.key === 'Escape') {
                    this._close();
                    resolve(false);
                }
            });
        });
    }

    // ── Internal helpers ─────────────────────────────────────────────

    /**
     * Closes and removes the current overlay, if any.
     * @private
     */
    _close() {
        if (this._overlay) {
            this._overlay.remove();
            this._overlay = null;
        }
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.SecretPrompt = SecretPrompt;

})();
