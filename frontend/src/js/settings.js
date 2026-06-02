(function () {
  /**
   * Nexus Terminal — settings modal.
   * Renders a tabbed settings dialog for application-wide preferences,
   * terminal defaults, security, known-hosts management, and about info.
   */

  const { createElement, escapeHtml, formatTimestamp, $ } =
    window.NexusTerminal;

  /** Settings tab definitions. */
  const TABS = [
    { key: "general", label: "General" },
    { key: "terminal", label: "Terminal" },
    { key: "security", label: "Security" },
    { key: "knownhosts", label: "Known Hosts" },
    { key: "help", label: "Help" },
    { key: "about", label: "About" },
  ];

  class Settings {
    /**
     * @param {App} app
     */
    constructor(app) {
      this.app = app;

      /** @type {HTMLElement|null} */
      this._overlay = null;

      /** @type {string} Currently active tab key. */
      this._activeTab = "general";

      /** @type {Object} Working copy of preferences. */
      this._prefs = {};

      /** @type {Array<Object>} Cached known hosts list. */
      this._knownHosts = [];
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Opens the settings modal.
     */
    async open(tabKey) {
      this._prefs = JSON.parse(JSON.stringify(this.app.state.preferences));
      this._activeTab = tabKey || this._activeTab || "general";

      try {
        this._knownHosts = (await window.go.api.Service.ListKnownHosts()) || [];
      } catch (_) {
        this._knownHosts = [];
      }

      try {
        this._defaultShell = await window.go.api.Service.GetDefaultShell();
      } catch (_) {
        this._defaultShell = "";
      }

      this._render();
    }

    /**
     * Opens the settings modal on a specific tab.
     * @param {string} tabKey
     */
    async openTab(tabKey) {
      this._activeTab = tabKey || "general";
      return this.open(this._activeTab);
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

        // Apply terminal colour theme change immediately.
        if (this.app.terminals && this._prefs.terminalTheme) {
          this.app.terminals.applyTheme(this._prefs.terminalTheme);
        }
        if (this.app && typeof this.app._applyPreferences === "function") {
          this.app._applyPreferences();
        }

        this.app.notifications.success("Settings saved");
        this.close();
      } catch (err) {
        this.app.notifications.error("Failed to save settings: " + err);
      }
    }

    // ── Rendering ────────────────────────────────────────────────────

    /** @private */
    _render() {
      this.close();

      const overlay = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-settings");

      // Header
      const header = createElement("div", "modal-header");
      header.innerHTML = `
            <h2 class="modal-title">Settings</h2>
            <button class="modal-close" aria-label="Close">&times;</button>
        `;
      header
        .querySelector(".modal-close")
        .addEventListener("click", () => this.close());
      modal.appendChild(header);

      // Tab bar
      const tabBar = createElement("div", "settings-tab-bar");
      for (const tab of TABS) {
        const btn = createElement(
          "button",
          `settings-tab ${tab.key === this._activeTab ? "active" : ""}`,
          {
            textContent: tab.label,
            dataset: { tab: tab.key },
          },
        );
        btn.addEventListener("click", () => {
          this._activeTab = tab.key;
          this._renderBody(modal);
          tabBar.querySelectorAll(".settings-tab").forEach((t) => {
            t.classList.toggle("active", t.dataset.tab === tab.key);
          });
        });
        tabBar.appendChild(btn);
      }
      modal.appendChild(tabBar);

      // Body (tab content)
      const body = createElement("div", "modal-body settings-body");
      modal.appendChild(body);
      this._renderTabContent(body);

      // Footer
      const footer = createElement("div", "modal-footer");
      const cancelBtn = createElement("button", "btn btn-secondary", {
        textContent: "Cancel",
      });
      const saveBtn = createElement("button", "btn btn-primary", {
        textContent: "Save",
      });
      cancelBtn.addEventListener("click", () => this.close());
      saveBtn.addEventListener("click", () => this.save());
      footer.appendChild(cancelBtn);
      footer.appendChild(saveBtn);
      modal.appendChild(footer);

      overlay.appendChild(modal);
      overlay.addEventListener("click", (e) => {
        if (e.target === overlay) this.close();
      });
      modal.addEventListener("click", (e) => e.stopPropagation());
      document.body.appendChild(overlay);
      this._overlay = overlay;

      // Trigger visibility after append so CSS transitions animate
      requestAnimationFrame(() => {
        overlay.classList.add("is-visible");
        modal.classList.add("is-visible");
      });
    }

    /**
     * Re-renders only the body when the active tab changes.
     * @param {HTMLElement} modal
     * @private
     */
    _renderBody(modal) {
      const body = modal.querySelector(".settings-body");
      if (body) this._renderTabContent(body);
    }

    /**
     * Renders the content for the currently active tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderTabContent(body) {
      body.innerHTML = "";
      body.classList.remove("help-layout");
      switch (this._activeTab) {
        case "general":
          this._renderGeneralTab(body);
          break;
        case "terminal":
          this._renderTerminalTab(body);
          break;
        case "security":
          this._renderSecurityTab(body);
          break;
        case "knownhosts":
          this._renderKnownHostsTab(body);
          break;
        case "help":
          this._renderHelpTab(body);
          break;
        case "about":
          this._renderAboutTab(body);
          break;
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
      body.appendChild(
        this._settingField("select", "theme", "Theme", p.theme, {
          options: [
            { value: "dark", label: "Dark" },
            { value: "light", label: "Light" },
          ],
        }),
      );
      body.appendChild(
        this._settingField(
          "select",
          "defaultProtocol",
          "Default Protocol",
          p.defaultProtocol,
          {
            options: [
              { value: "ssh", label: "SSH" },
              { value: "telnet", label: "Telnet" },
              { value: "raw", label: "RAW" },
            ],
          },
        ),
      );
      body.appendChild(
        this._settingField(
          "toggle",
          "confirmOnClose",
          "Confirm on close",
          p.confirmOnClose,
        ),
      );
      body.appendChild(
        this._settingField(
          "toggle",
          "confirmOnDisconnect",
          "Confirm on disconnect",
          p.confirmOnDisconnect,
        ),
      );
      body.appendChild(
        this._settingField(
          "toggle",
          "showStatusBar",
          "Show status bar",
          p.showStatusBar,
        ),
      );
      body.appendChild(
        this._settingField(
          "number",
          "sidebarWidth",
          "Sidebar width (px)",
          p.sidebarWidth,
          { min: 180, max: 500 },
        ),
      );

      // Local shell path with browse button
      const shellGroup = createElement("div", "form-group settings-field");
      const shellLabel = createElement("label", "form-label", {
        textContent: "Local shell",
        for: "setting-localShell",
      });
      shellGroup.appendChild(shellLabel);

      const shellRow = createElement("div", "settings-shell-row");
      const shellInput = createElement("input", "form-input", {
        type: "text",
        id: "setting-localShell",
        name: "setting-localShell",
        value: p.localShell || "",
        placeholder: this._defaultShell || "/bin/zsh",
      });
      shellRow.appendChild(shellInput);

      const browseBtn = createElement("button", "btn btn-sm btn-secondary", {
        textContent: "Browse…",
        type: "button",
      });
      browseBtn.addEventListener("click", async () => {
        try {
          const path = await window.go.api.Service.BrowseForShell();
          if (path) {
            shellInput.value = path;
          }
        } catch (_) {}
      });
      shellRow.appendChild(browseBtn);
      shellGroup.appendChild(shellRow);

      const shellHint = createElement("span", "form-hint", {
        textContent: "Leave empty to use system default",
      });
      shellGroup.appendChild(shellHint);
      body.appendChild(shellGroup);
    }

    /**
     * Terminal defaults tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderTerminalTab(body) {
      const p = this._prefs;

      // Terminal colour theme selector
      const themes = window.NexusTerminal.listTerminalThemes();
      body.appendChild(
        this._settingField(
          "select",
          "terminalTheme",
          "Colour scheme",
          p.terminalTheme || "charcoal",
          {
            options: themes,
          },
        ),
      );

      body.appendChild(
        this._settingField(
          "text",
          "defaultFontFamily",
          "Font family",
          p.defaultFontFamily,
          {
            placeholder: "JetBrains Mono, Menlo, Monaco, Consolas, monospace",
          },
        ),
      );
      body.appendChild(
        this._settingField(
          "number",
          "defaultFontSize",
          "Font size",
          p.defaultFontSize,
          { min: 8, max: 36 },
        ),
      );
      body.appendChild(
        this._settingField(
          "number",
          "defaultScrollback",
          "Scrollback lines",
          p.defaultScrollback,
          { min: 0, max: 100000 },
        ),
      );
      body.appendChild(
        this._settingField(
          "select",
          "defaultCursorStyle",
          "Cursor style",
          p.defaultCursorStyle,
          {
            options: [
              { value: "block", label: "Block" },
              { value: "underline", label: "Underline" },
              { value: "bar", label: "Bar" },
            ],
          },
        ),
      );
      body.appendChild(
        this._settingField(
          "toggle",
          "useNativePaste",
          "Use native paste",
          p.useNativePaste === true,
        ),
      );
    }

    /**
     * Security settings tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderSecurityTab(body) {
      const storeState = this.app.state.secretStoreUnlocked
        ? "Unlocked"
        : "Locked";
      const storeInit = this.app.state.secretStoreInitialised;

      const statusRow = createElement("div", "settings-row");
      statusRow.innerHTML = `
            <span class="settings-label">Secret store</span>
            <span class="settings-value ${this.app.state.secretStoreUnlocked ? "text-success" : "text-muted"}">${storeState}</span>
        `;
      body.appendChild(statusRow);

      if (!storeInit) {
        const initBtn = createElement("button", "btn btn-primary btn-sm", {
          textContent: "Initialise Secret Store",
        });
        initBtn.addEventListener("click", async () => {
          try {
            const pw = await this.app.secretPrompt.promptMasterPassword(true);
            await window.go.api.Service.InitialiseSecretStore(pw);
            this.app.state.setSecretStoreInitialised(true);
            this.app.state.setSecretStoreState(true);
            this.app.notifications.success("Secret store created");
            this._renderTabContent(body);
          } catch (err) {
            if (err.message !== "User cancelled") {
              this.app.notifications.error("Failed to initialise: " + err);
            }
          }
        });
        body.appendChild(initBtn);
      } else if (!this.app.state.secretStoreUnlocked) {
        const unlockBtn = createElement("button", "btn btn-primary btn-sm", {
          textContent: "Unlock",
        });
        unlockBtn.addEventListener("click", async () => {
          try {
            const pw = await this.app.secretPrompt.promptMasterPassword(false);
            await window.go.api.Service.UnlockSecretStore(pw);
            this.app.state.setSecretStoreState(true);
            this.app.notifications.success("Secret store unlocked");
            this._renderTabContent(body);
          } catch (err) {
            if (err.message !== "User cancelled") {
              this.app.notifications.error("Unlock failed: " + err);
            }
          }
        });
        body.appendChild(unlockBtn);
      } else {
        const lockBtn = createElement("button", "btn btn-outline btn-sm", {
          textContent: "Lock",
        });
        lockBtn.addEventListener("click", async () => {
          try {
            await window.go.api.Service.LockSecretStore();
            this.app.state.setSecretStoreState(false);
            this.app.notifications.info("Secret store locked");
            this._renderTabContent(body);
          } catch (err) {
            this.app.notifications.error("Lock failed: " + err);
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
        body.appendChild(
          createElement("p", "text-muted", {
            textContent: "No known hosts recorded.",
          }),
        );
        return;
      }

      const table = createElement("table", "settings-table");
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
      const tbody = createElement("tbody");

      for (const kh of this._knownHosts) {
        const tr = createElement("tr");
        tr.innerHTML = `
                <td>${escapeHtml(kh.host)}</td>
                <td>${kh.port}</td>
                <td><code>${escapeHtml(kh.keyType)}</code></td>
                <td class="fingerprint-cell" title="${escapeHtml(kh.fingerprint)}"><code>${escapeHtml(kh.fingerprint.slice(0, 20))}…</code></td>
                <td>${formatTimestamp(kh.firstSeen)}</td>
                <td></td>
            `;
        const deleteBtn = createElement("button", "btn btn-xs btn-danger", {
          textContent: "Remove",
        });
        deleteBtn.addEventListener("click", async () => {
          try {
            await window.go.api.Service.DeleteKnownHost(kh.id);
            this._knownHosts = this._knownHosts.filter((h) => h.id !== kh.id);
            this._renderTabContent(body);
            this.app.notifications.success("Known host removed");
          } catch (err) {
            this.app.notifications.error("Failed to remove: " + err);
          }
        });
        tr.querySelector("td:last-child").appendChild(deleteBtn);
        tbody.appendChild(tr);
      }

      table.appendChild(tbody);
      body.appendChild(table);
    }

    /**
     * In-app help and documentation tab.
     * @param {HTMLElement} body
     * @private
     */
    _renderHelpTab(body) {
      const HELP_SECTIONS = [
        {
          id: "getting-started",
          title: "Getting Started",
          content: `
                <p>Welcome to <strong>Nexus Terminal</strong> — a desktop application for connecting to remote servers via SSH, Telnet, and RAW TCP, as well as browsing your GitHub repositories.</p>
                <h4>Creating Your First Connection</h4>
                <ol>
                    <li>Click the <strong>+</strong> button at the top of the sidebar, or press <kbd>⌘N</kbd> (Mac) / <kbd>Ctrl+N</kbd> (Windows/Linux).</li>
                    <li>Choose a protocol (SSH, Telnet, RAW, or GitHub).</li>
                    <li>Fill in the hostname, port, and any credentials required.</li>
                    <li>Optionally assign the connection to a <strong>group</strong> to keep your sidebar organised.</li>
                    <li>Click <strong>Save</strong>, then double-click the entry in the sidebar to connect.</li>
                </ol>
                <h4>Quick Connect</h4>
                <p>Need to connect quickly without saving a profile? Press <kbd>⌘⇧Q</kbd> / <kbd>Ctrl+Shift+Q</kbd> to open the quick connect dialog. Choose SSH, Telnet, or RAW, enter the host and port, and you're connected immediately.</p>
                <h4>Managing Connections</h4>
                <p>Right-click any connection in the sidebar to <strong>edit</strong>, <strong>duplicate</strong>, or <strong>delete</strong> it. You can drag connections between groups, and use the search box to filter by name, host, or tag.</p>
            `,
        },
        {
          id: "ssh",
          title: "SSH Connections",
          content: `
                <p>SSH (Secure Shell) is the most common way to connect to remote servers. Nexus Terminal supports:</p>
                <ul>
                    <li><strong>Password authentication</strong> — enter your password when prompted, or save it in the encrypted vault.</li>
                    <li><strong>Public key authentication</strong> — point to your private key file (e.g. <code>~/.ssh/id_rsa</code>). If the key has a passphrase, you will be prompted.</li>
                    <li><strong>Keyboard-interactive</strong> — for servers that use challenge-response (e.g. two-factor prompts).</li>
                    <li><strong>SSH agent</strong> — tick "Use SSH Agent" to authenticate via your system's SSH agent (e.g. <code>ssh-agent</code> or 1Password).</li>
                </ul>
                <h4>TOTP / 2FA Auto-Fill</h4>
                <p>If your server requires a time-based one-time password (TOTP) during login, you can store the TOTP secret in the profile editor under the <strong>TOTP / 2FA</strong> section. When connecting, Nexus Terminal will detect 2FA prompts (such as "Verification code" or "OTP") and automatically generate and submit a 6-digit code. Use the <strong>Test</strong> button to verify your secret generates valid codes before connecting.</p>
                <h4>Jump Hosts (Bastions)</h4>
                <p>If you need to reach a server through an intermediate host, add one or more jump hosts in the connection editor. Nexus Terminal will tunnel through each hop automatically.</p>
                <h4>Port Forwarding</h4>
                <p>You can set up <strong>local</strong>, <strong>remote</strong>, and <strong>dynamic</strong> (SOCKS proxy) port forwards in the connection editor. These start automatically when the session connects.</p>
                <h4>Proxy Support</h4>
                <p>If you need to connect through a proxy, open the <strong>Proxy</strong> section in the profile editor. Nexus Terminal supports <strong>SOCKS5</strong> and <strong>HTTP CONNECT</strong> proxies, with optional username/password authentication. The proxy is used to reach the SSH server (or the first jump host if jump hosts are configured).</p>
                <h4>SFTP File Browser</h4>
                <p>Once connected to an SSH server, click the <strong>📁</strong> icon on the tab to open the SFTP file browser. You can browse directories, upload, download, rename, and delete files from a side panel.</p>
                <h4>SSH Agent Forwarding</h4>
                <p>Tick "Agent Forwarding" in the SSH settings to forward your local SSH agent to the remote server. This lets you use your local keys when connecting onward from that server.</p>
                <h4>Auto-Login Scripts</h4>
                <p>Define expect-style login scripts to automate interactions after connecting. In the <strong>Login Script</strong> section of the profile editor, add a sequence of <strong>Wait for</strong> (match text from the server) and <strong>Send</strong> (send text + newline) steps. For example:</p>
                <ol>
                    <li>Wait for: <code>login:</code></li>
                    <li>Send: <code>admin</code></li>
                    <li>Wait for: <code>Password:</code></li>
                    <li>Send: <code>mysecretpassword</code></li>
                </ol>
                <p>Each wait step has a configurable timeout (default 10 seconds). Steps execute in order after the connection is established.</p>
                <h4>Auto-Reconnect</h4>
                <p>Enable <strong>Auto-Reconnect</strong> in the Advanced section to automatically reconnect when the connection drops unexpectedly. Reconnection uses exponential backoff (1s, 2s, 4s, 8s... up to 30s). You can configure the maximum number of attempts (default 5).</p>
            `,
        },
        {
          id: "telnet",
          title: "Telnet Connections",
          content: `
                <p>Telnet is an older, unencrypted protocol still used for network equipment, legacy systems, and some fun internet services.</p>
                <ul>
                    <li><strong>Option negotiation</strong> — Nexus Terminal handles standard Telnet options (echo, terminal type, window size) automatically.</li>
                    <li><strong>Character encoding</strong> — choose UTF-8, ASCII, or Latin-1 for the connection.</li>
                    <li><strong>Local echo</strong> — enable this if the remote host does not echo back what you type.</li>
                    <li><strong>Line mode</strong> — send data line-by-line (press Enter to send) rather than character-by-character.</li>
                    <li><strong>Auto-login scripts</strong> — same expect-style send/wait sequences as SSH (see the Login Script section).</li>
                    <li><strong>Auto-reconnect</strong> — automatically reconnect with exponential backoff when the connection drops.</li>
                </ul>
                <p><em>Note:</em> Telnet sends everything in plain text. Avoid using it for anything sensitive over an untrusted network.</p>
            `,
        },
        {
          id: "raw",
          title: "RAW TCP Connections",
          content: `
                <p>RAW TCP lets you connect to any TCP port and exchange data directly — useful for debugging network services, testing APIs, or working with custom protocols.</p>
                <ul>
                    <li><strong>Text view</strong> — shows incoming data as readable text.</li>
                    <li><strong>Hex view</strong> — shows raw bytes in a hex dump format.</li>
                    <li><strong>Split view</strong> — text and hex side by side.</li>
                    <li><strong>Payload presets</strong> — save frequently-used commands or data packets for quick sending.</li>
                    <li><strong>Line endings</strong> — choose CR, LF, or CRLF to match the remote service.</li>
                    <li><strong>Auto-reconnect</strong> — automatically reconnect if the connection drops.</li>
                </ul>
            `,
        },
        {
          id: "github",
          title: "GitHub Connections",
          content: `
                <p>The GitHub connection type lets you browse your repositories without leaving Nexus Terminal.</p>
                <h4>Setting Up</h4>
                <ol>
                    <li>Create a new connection and choose <strong>GitHub</strong> as the protocol.</li>
                    <li>Enter your <strong>Personal Access Token</strong> (create one at <em>GitHub → Settings → Developer settings → Personal access tokens</em>). The token needs the <code>repo</code> scope.</li>
                    <li>Save and connect.</li>
                </ol>
                <h4>What You'll See</h4>
                <p>Your repositories are listed grouped by organisation or owner, sorted by last commit date (newest first). The list refreshes automatically every few minutes.</p>
            `,
        },
        {
          id: "sessions",
          title: "Sessions & Tabs",
          content: `
                <h4>Tab Management</h4>
                <ul>
                    <li><strong>Rename</strong> — right-click a tab and choose "Rename".</li>
                    <li><strong>Reorder</strong> — drag tabs left or right to rearrange.</li>
                    <li><strong>Pin</strong> — right-click and pin a tab so it stays at the left of the bar.</li>
                    <li><strong>Close</strong> — click the <strong>×</strong> on a tab, or press <kbd>⌘W</kbd> / <kbd>Ctrl+W</kbd>.</li>
                </ul>
                <h4>Split Panes</h4>
                <p>You can split the terminal into multiple panes:</p>
                <ul>
                    <li><kbd>⌘⇧D</kbd> / <kbd>Ctrl+Shift+D</kbd> — split vertically (side by side).</li>
                    <li><kbd>⌘⇧E</kbd> / <kbd>Ctrl+Shift+E</kbd> — split horizontally (top and bottom).</li>
                </ul>
                <p>Drag the divider between panes to resize them.</p>
                <h4>Broadcast Input</h4>
                <p>Press <kbd>⌘⇧B</kbd> / <kbd>Ctrl+Shift+B</kbd> to toggle <strong>broadcast mode</strong>. When active, everything you type is sent to <strong>all connected sessions</strong> simultaneously — perfect for running the same command across multiple servers. The broadcast button in the tab bar pulses red to remind you it's active.</p>
                <h4>Session Recording</h4>
                <p>Press <kbd>⌘⇧R</kbd> / <kbd>Ctrl+Shift+R</kbd> to start recording a session. A red dot will appear on the tab. Press the same shortcut again to stop. Browse and replay recordings from the <strong>⏺</strong> button in the sidebar.</p>
                <h4>Right-Click Context Menu</h4>
                <p>Right-click in the terminal to access a context menu with:</p>
                <ul>
                    <li><strong>Copy</strong> — copy selected text to the clipboard</li>
                    <li><strong>Paste</strong> — paste from the clipboard into the terminal</li>
                    <li><strong>Select All</strong> — select all terminal content</li>
                    <li><strong>Find</strong> — open the terminal search overlay</li>
                    <li><strong>Send Snippet</strong> — quickly send a saved command from the snippet library</li>
                    <li><strong>Clear</strong> / <strong>Reset</strong> — clear the terminal buffer</li>
                </ul>
            `,
        },
        {
          id: "power",
          title: "Power Features",
          content: `
                <h4>Snippet Library</h4>
                <p>Press <kbd>⌘⇧S</kbd> / <kbd>Ctrl+Shift+S</kbd> to open the <strong>snippet library</strong>. Save frequently-used commands with a name, description, and optional folder and tags. Click <strong>Send to Session</strong> to inject a command into the active terminal. You can also access snippets from the right-click context menu.</p>
                <h4>Terminal Search</h4>
                <p>Press <kbd>⌘F</kbd> / <kbd>Ctrl+F</kbd> to open the search overlay on the active terminal. Features include:</p>
                <ul>
                    <li><strong>Aa</strong> button — toggle case-sensitive matching</li>
                    <li><strong>.*</strong> button — toggle regular expression mode</li>
                    <li>Match count indicator showing current/total matches</li>
                    <li>Navigate matches with the up/down arrows or <kbd>Enter</kbd> / <kbd>Shift+Enter</kbd></li>
                </ul>
                <h4>Quick Connect</h4>
                <p>Press <kbd>⌘⇧Q</kbd> / <kbd>Ctrl+Shift+Q</kbd> to open a lightweight connect dialog. Choose SSH, Telnet, or RAW; enter the host and port; and connect immediately without creating a saved profile.</p>
                <h4>Auto-Login Scripts</h4>
                <p>For SSH and Telnet connections, you can define expect-style login scripts — a sequence of "wait for text" and "send text" steps that execute automatically after connecting. Configure them in the <strong>Login Script</strong> section of the profile editor.</p>
                <h4>Auto-Reconnect</h4>
                <p>Enable auto-reconnect in a profile's settings to automatically reconnect when a connection drops. Uses exponential backoff with a configurable maximum number of attempts.</p>
            `,
        },
        {
          id: "security",
          title: "Security & Credentials",
          content: `
                <h4>How Your Passwords Are Stored</h4>
                <p>Nexus Terminal uses a <strong>two-tier</strong> approach to keep your credentials safe:</p>
                <ol>
                    <li><strong>OS keychain</strong> (preferred) — your operating system's built-in credential store (macOS Keychain, Windows Credential Manager, or Linux Secret Service). If available, this is used automatically.</li>
                    <li><strong>Encrypted vault</strong> (fallback) — if the OS keychain is not available, Nexus Terminal creates a local vault encrypted with AES-256-GCM. You set a master password to unlock it.</li>
                </ol>
                <h4>Encryption Details</h4>
                <p>The vault uses <strong>Argon2id</strong> to derive the encryption key from your master password (128 MiB memory, making brute-force attacks very expensive). Each secret is individually encrypted and never stored in plain text.</p>
                <h4>TOTP / 2FA Secrets</h4>
                <p>TOTP secrets for SSH two-factor authentication are stored alongside your other credentials — encrypted in the OS keychain or the file vault. They are never saved in the profile database in plain text.</p>
                <h4>Known Hosts</h4>
                <p>When you connect to an SSH server for the first time, Nexus Terminal records the server's fingerprint. If the fingerprint changes on a subsequent connection, you'll be warned — this protects against man-in-the-middle attacks.</p>
                <p>You can manage known hosts and change the verification policy in <strong>Settings → Known Hosts</strong>.</p>
                <h4>Encrypted Export</h4>
                <p>When exporting profiles, you can choose encrypted export (.nexus file) which protects the file with a password. This uses the same strong AES-256-GCM encryption as the vault.</p>
            `,
        },
        {
          id: "shortcuts",
          title: "Keyboard Shortcuts",
          content: `
                <p>On macOS, use <kbd>⌘</kbd> (Cmd). On Windows and Linux, use <kbd>Ctrl</kbd>.</p>
                <table class="help-shortcuts-table">
                    <tbody>
                        <tr><td><kbd>⌘K</kbd></td><td>Open the command palette</td></tr>
                        <tr><td><kbd>⌘N</kbd></td><td>New connection</td></tr>
                        <tr><td><kbd>⌘W</kbd></td><td>Close the active tab</td></tr>
                        <tr><td><kbd>⌘Tab</kbd></td><td>Next tab</td></tr>
                        <tr><td><kbd>⌘⇧Tab</kbd></td><td>Previous tab</td></tr>
                        <tr><td><kbd>⌘1</kbd>–<kbd>⌘9</kbd></td><td>Switch to tab by number</td></tr>
                        <tr><td><kbd>⌘,</kbd></td><td>Open Settings</td></tr>
                        <tr><td><kbd>⌘F</kbd></td><td>Search in terminal</td></tr>
                        <tr><td><kbd>⌘⇧B</kbd></td><td>Toggle broadcast input mode</td></tr>
                        <tr><td><kbd>⌘⇧D</kbd></td><td>Split pane vertically</td></tr>
                        <tr><td><kbd>⌘⇧E</kbd></td><td>Split pane horizontally</td></tr>
                        <tr><td><kbd>⌘⇧Q</kbd></td><td>Quick connect (no saved profile)</td></tr>
                        <tr><td><kbd>⌘⇧R</kbd></td><td>Toggle session recording</td></tr>
                        <tr><td><kbd>⌘⇧S</kbd></td><td>Open snippet library</td></tr>
                        <tr><td><kbd>F11</kbd></td><td>Toggle fullscreen (Windows/Linux)</td></tr>
                    </tbody>
                </table>
            `,
        },
        {
          id: "import-export",
          title: "Import & Export",
          content: `
                <h4>Exporting Profiles</h4>
                <p>Click the <strong>↗</strong> export button in the sidebar footer. You can choose which profiles to include, and set a password to encrypt the file. Exported files use the <code>.nexus</code> extension.</p>
                <h4>Importing Profiles</h4>
                <p>Click the <strong>↙</strong> import button. Select a <code>.nexus</code> file and enter the password used when exporting. Imported profiles will appear in your sidebar immediately.</p>
                <h4>Importing from OpenSSH</h4>
                <p>If you have an existing <code>~/.ssh/config</code> file, go to <strong>Settings → About</strong> and click <strong>Import ~/.ssh/config</strong>. Nexus Terminal will create profiles from your SSH Host blocks, including hostnames, usernames, ports, identity files, and jump hosts.</p>
            `,
        },
        {
          id: "troubleshooting",
          title: "Troubleshooting",
          content: `
                <h4>I can't connect to my server</h4>
                <ul>
                    <li>Double-check the <strong>hostname</strong> and <strong>port</strong> — a common mistake is using port 22 for Telnet (which is usually 23) or vice versa.</li>
                    <li>Make sure the server is reachable from your machine. Try <code>ping hostname</code> from another terminal.</li>
                    <li>If using SSH keys, check that the key file path is correct and that the file permissions are not too open (should be <code>600</code> or <code>400</code>).</li>
                    <li>If you're behind a firewall or VPN, ensure the relevant port is allowed through.</li>
                </ul>
                <h4>SSH connection fails through a proxy</h4>
                <ul>
                    <li>Check the proxy host, port, and type (SOCKS5 or HTTP CONNECT) are correct in the profile's <strong>Proxy</strong> section.</li>
                    <li>If the proxy requires authentication, make sure the username and password are filled in.</li>
                    <li>SOCKS5 defaults to port 1080; HTTP CONNECT defaults to port 8080.</li>
                    <li>Some proxies may block SSH traffic — check with your network administrator.</li>
                </ul>
                <h4>TOTP/2FA code is rejected</h4>
                <ul>
                    <li>Ensure the TOTP secret is correct (base32-encoded, as shown during authenticator app setup).</li>
                    <li>Check that your system clock is accurate — TOTP codes are only valid for 30-second windows.</li>
                    <li>Use the <strong>Test</strong> button in the profile's TOTP section to verify the secret generates valid codes.</li>
                </ul>
                <h4>Login script times out</h4>
                <ul>
                    <li>Each "wait" step has a configurable timeout (default 10 seconds). Increase it in the profile editor if the server takes longer.</li>
                    <li>Check that the wait pattern matches exactly — it's a plain text substring match, not a regular expression.</li>
                    <li>Make sure the server actually sends the expected text. Some servers may use different prompts depending on locale or configuration.</li>
                </ul>
                <h4>Auto-reconnect keeps failing</h4>
                <ul>
                    <li>Auto-reconnect only works after a successful initial connection.</li>
                    <li>If the server is genuinely unreachable, attempts will exhaust after the configured maximum (default 5).</li>
                    <li>Check the reconnection settings in the profile's Advanced section.</li>
                </ul>
                <h4>The terminal looks garbled or the wrong size</h4>
                <ul>
                    <li>Try resizing the window — the terminal should re-fit automatically.</li>
                    <li>If you're using split panes, drag the divider to give the terminal more space.</li>
                    <li>Some older servers may not support modern terminal features. Try a different terminal theme in <strong>Settings → Terminal</strong>.</li>
                </ul>
                <h4>"Host key verification failed"</h4>
                <p>This means the server's fingerprint has changed since your last connection. This can happen if the server was reinstalled, but it could also indicate a security threat. If you're certain it's safe, go to <strong>Settings → Known Hosts</strong>, find the entry, and remove it. You'll be prompted to accept the new key on your next connection.</p>
                <h4>I forgot my vault master password</h4>
                <p>Unfortunately, the vault encryption is designed so that without the master password, the stored credentials cannot be recovered. You would need to delete the vault file and set up a new one. Your connection profiles (without saved passwords) will remain intact.</p>
                <h4>SFTP panel doesn't open</h4>
                <p>SFTP is only available for <strong>SSH</strong> connections (not Telnet, RAW, or GitHub). Make sure you are connected to an SSH session first, then click the <strong>📁</strong> icon on the tab.</p>
                <h4>The application looks blurry on my display</h4>
                <p>Make sure your system display scaling is set correctly. On macOS, Nexus Terminal is fully Retina-aware. On Windows, check your DPI settings in Display preferences.</p>
                <h4>Export/import isn't working</h4>
                <p>Ensure you're using the same password to import that was used to export. The encrypted <code>.nexus</code> files cannot be opened with the wrong password.</p>
            `,
        },
        {
          id: "fun",
          title: "Fun Things to Try",
          content: `
                <p>Nexus Terminal isn't just for work! Here are some entertaining things you can connect to:</p>
                <h4>🌟 Star Wars in ASCII Art</h4>
                <p>Create a <strong>Telnet</strong> connection to <code>towel.blinkenlights.nl</code> on port <strong>23</strong>. Sit back and enjoy the original Star Wars film retold entirely in ASCII art.</p>
                <h4>🗺️ MapSCII — World Map in Your Terminal</h4>
                <p>Connect via <strong>SSH</strong> to <code>mapscii.me</code> on port <strong>22</strong>. Use the arrow keys to pan and <kbd>a</kbd>/<kbd>z</kbd> to zoom in and out of a fully interactive world map rendered in text.</p>
                <h4>🎮 Telnet BBS Systems</h4>
                <p>Connect via <strong>Telnet</strong> to <code>particlesbbs.dyndns.org</code> on port <strong>23</strong> for a retro bulletin board system experience, complete with door games, message boards, and ASCII art.</p>
                <h4>🐄 Fortune Cookies</h4>
                <p>Connect via <strong>Telnet</strong> to <code>djxmmx.net</code> on port <strong>17</strong> (the Quote of the Day protocol). You'll receive a random quote each time you connect — great for testing RAW TCP connections too.</p>
                <h4>🔒 Test Your SSH Setup</h4>
                <p>Connect via <strong>SSH</strong> to <code>ssh.github.com</code> on port <strong>443</strong> (or <code>github.com</code> on port <strong>22</strong>) with your GitHub username. GitHub will greet you by name if your key is set up correctly — a quick way to verify your SSH configuration.</p>
                <h4>📡 RAW TCP Experiments</h4>
                <p>Create a <strong>RAW TCP</strong> connection to any HTTP server on port <strong>80</strong>, then type <code>GET / HTTP/1.0</code> followed by two blank lines. You'll see the raw HTTP response — a great way to understand how the web works under the hood.</p>
                <h4>📢 Broadcast Mode Fun</h4>
                <p>Open two or more sessions (e.g. to different servers) and press <kbd>⌘⇧B</kbd> / <kbd>Ctrl+Shift+B</kbd> to turn on broadcast mode. Everything you type goes to all sessions at once — try running <code>hostname</code> and watch them all respond with different names!</p>
            `,
        },
      ];

      body.innerHTML = "";

      // Navigation sidebar
      body.classList.add("help-layout");
      var nav = createElement("div", "help-nav");
      var content = createElement("div", "help-content");

      for (var i = 0; i < HELP_SECTIONS.length; i++) {
        var section = HELP_SECTIONS[i];
        var navItem = createElement("button", "help-nav-item", {
          textContent: section.title,
          dataset: { section: section.id },
        });
        navItem.addEventListener("mousedown", function (e) {
          e.preventDefault();
        });
        (function (sec) {
          navItem.addEventListener("click", function () {
            nav.querySelectorAll(".help-nav-item").forEach(function (n) {
              n.classList.remove("active");
            });
            this.classList.add("active");
            content.innerHTML =
              '<h3 class="help-section-title">' +
              sec.title +
              "</h3>" +
              sec.content;
          });
        })(section);
        nav.appendChild(navItem);
      }

      body.appendChild(nav);
      body.appendChild(content);

      // Show first section by default
      if (nav.firstChild) {
        nav.firstChild.classList.add("active");
        content.innerHTML =
          '<h3 class="help-section-title">' +
          HELP_SECTIONS[0].title +
          "</h3>" +
          HELP_SECTIONS[0].content;
      }
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
                <p class="about-version" data-role="app-version">Version …</p>
                <p class="about-desc">Developed by Andy Dixon - www.dixon.cx/nexus</p>
                <div class="about-links">
                    <p>AES-256-GCM encrypted credential vault</p>
                    <p>Platform-native keychain integration</p>
                    <p>SFTP file browser and transfer</p>
                    <p>Session recording and playback</p>
                </div>
                <div style="margin-top:16px;display:flex;gap:8px;flex-wrap:wrap;">
                    <button class="btn btn-sm btn-primary" id="settings-check-update">Check for Updates</button>
                    <button class="btn btn-sm btn-outline" id="settings-import-sshconfig">Import ~/.ssh/config</button>
                </div>
                <div id="settings-update-result" style="margin-top:8px;font-size:12px;"></div>
            </div>
        `;

      const versionEl = body.querySelector("[data-role='app-version']");
      if (versionEl) {
        if (window.go && window.go.api && window.go.api.Service) {
          window.go.api.Service.GetAppInfo()
            .then((info) => {
              const version = (info && info.version) || "dev";
              versionEl.textContent = `Version ${version}`;
            })
            .catch(() => {
              versionEl.textContent = "Version unavailable";
            });
        } else {
          versionEl.textContent = "Version unavailable";
        }
      }

      const updateBtn = body.querySelector("#settings-check-update");
      if (updateBtn) {
        updateBtn.addEventListener("click", async () => {
          updateBtn.disabled = true;
          updateBtn.textContent = "Checking…";
          try {
            const info = await window.go.api.Service.CheckForUpdates();
            const resultEl = body.querySelector("#settings-update-result");
            if (this.app && typeof this.app.setUpdateInfo === "function") {
              this.app.setUpdateInfo(info);
            }
            if (info.available) {
              resultEl.textContent = "";
              const updateText = createElement("span");
              updateText.style.color = "var(--accent)";
              updateText.appendChild(document.createTextNode("Update available: "));
              const versionText = createElement("strong");
              versionText.textContent = String(info.latestVersion || "");
              updateText.appendChild(versionText);
              resultEl.appendChild(updateText);
              if (info.releaseURL) {
                resultEl.appendChild(document.createTextNode(" - "));
                const releaseLink = createElement("a", "", {
                  href: "#",
                  textContent: "View release",
                });
                releaseLink.style.color = "var(--accent)";
                releaseLink.addEventListener("click", (e) => {
                  e.preventDefault();
                  if (window.runtime && window.runtime.BrowserOpenURL) {
                    window.runtime.BrowserOpenURL(String(info.releaseURL));
                  }
                });
                resultEl.appendChild(releaseLink);
              }
            } else {
              resultEl.textContent = "You are running the latest version.";
            }
          } catch (err) {
            const resultEl = body.querySelector("#settings-update-result");
            resultEl.textContent = "Update check failed: " + err;
          } finally {
            updateBtn.disabled = false;
            updateBtn.textContent = "Check for Updates";
          }
        });
      }

      const importBtn = body.querySelector("#settings-import-sshconfig");
      if (importBtn) {
        importBtn.addEventListener("click", async () => {
          importBtn.disabled = true;
          importBtn.textContent = "Importing…";
          try {
            const count = await window.go.api.Service.ImportSSHConfig(
              "",
              "SSH Config",
            );
            this.app.notifications.success(
              `Imported ${count} host(s) from ~/.ssh/config`,
            );
            await this.app.loadProfiles();
          } catch (err) {
            this.app.notifications.error("SSH config import failed: " + err);
          } finally {
            importBtn.disabled = false;
            importBtn.textContent = "Import ~/.ssh/config";
          }
        });
      }
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
      const group = createElement("div", "form-group settings-field");
      const labelEl = createElement("label", "form-label", {
        textContent: label,
        for: `setting-${name}`,
      });
      group.appendChild(labelEl);

      let input;
      switch (type) {
        case "text":
          input = createElement("input", "form-input", {
            type: "text",
            id: `setting-${name}`,
            name: `setting-${name}`,
            value: value || "",
            placeholder: opts.placeholder || "",
          });
          break;

        case "number":
          input = createElement("input", "form-input", {
            type: "number",
            id: `setting-${name}`,
            name: `setting-${name}`,
            value: value != null ? String(value) : "",
          });
          if (opts.min != null) input.min = opts.min;
          if (opts.max != null) input.max = opts.max;
          break;

        case "select":
          input = createElement("select", "form-select", {
            id: `setting-${name}`,
            name: `setting-${name}`,
          });
          for (const opt of opts.options || []) {
            const option = createElement("option", "", {
              value: opt.value,
              textContent: opt.label,
            });
            if (opt.value === value) option.selected = true;
            input.appendChild(option);
          }
          break;

        case "toggle": {
          const wrapper = createElement("div", "form-toggle-wrapper");
          input = createElement("input", "form-toggle", {
            type: "checkbox",
            id: `setting-${name}`,
            name: `setting-${name}`,
          });
          input.checked = !!value;
          const slider = createElement("label", "form-toggle-label", {
            for: `setting-${name}`,
          });
          wrapper.appendChild(input);
          wrapper.appendChild(slider);
          group.appendChild(wrapper);
          return group;
        }

        default:
          input = createElement("input", "form-input", {
            type: "text",
            id: `setting-${name}`,
            name: `setting-${name}`,
            value: value || "",
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
        if (el.type === "checkbox") return el.checked;
        if (el.type === "number") return el.value !== "" ? Number(el.value) : 0;
        return el.value;
      };

      const fields = [
        "theme",
        "terminalTheme",
        "defaultProtocol",
        "confirmOnClose",
        "confirmOnDisconnect",
        "showStatusBar",
        "sidebarWidth",
        "defaultFontFamily",
        "defaultFontSize",
        "defaultScrollback",
        "defaultCursorStyle",
        "useNativePaste",
        "localShell",
      ];

      for (const field of fields) {
        const v = val(field);
        if (v !== undefined) {
          this._prefs[field] = v;
        }
      }
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.Settings = Settings;
})();
