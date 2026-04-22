(function () {
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
      protocol: "ssh",
      ssh: {
        authMethods: ["publickey", "password", "keyboard-interactive"],
        secretMode: "saved",
        keySecretMode: "saved",
        hostKeyPolicy: "ask",
        terminalType: "xterm-256color",
        keepaliveInterval: 30,
        keepaliveCountMax: 3,
        compression: false,
        jumpHosts: [],
        portForwards: [],
        startupCommand: "",
        envVars: {},
        loginScripts: [],
      },
    },
    telnet: {
      port: 23,
      protocol: "telnet",
      telnet: {
        localEcho: false,
        lineEnding: "crlf",
        terminalType: "xterm-256color",
        encoding: "utf-8",
        negotiationDebug: false,
        lineMode: false,
        loginScripts: [],
      },
    },
    raw: {
      port: 23,
      protocol: "raw",
      raw: {
        autoReconnect: false,
        lineEnding: "crlf",
        encoding: "utf-8",
        showTimestamps: false,
        showDirection: false,
        payloadPresets: [],
      },
    },
    github: {
      port: 443,
      protocol: "github",
      host: "github.com",
      github: {
        baseURL: "https://api.github.com",
        secretMode: "saved",
        refreshIntervalSecs: 300,
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

      /** @type {string[]} */
      this._sshKeyOptions = [];

      /** @type {boolean} */
      this._sshKeyOptionsLoaded = false;

      /** @type {boolean} */
      this._sshKeyOptionsLoading = false;
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Opens the editor with a blank profile of the given protocol.
     * @param {string} [protocol='ssh']
     */
    openNew(protocol = "ssh") {
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
        this.app.notifications.error("Profile not found");
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

      const overlay = createElement("div", "drawer-backdrop");
      const drawer = createElement("div", "drawer");

      // ── Header ──────────────────────────────────────────────
      const header = createElement("div", "drawer-header");
      header.innerHTML = `
            <h2 class="drawer-title">${isNew ? "New Connection" : "Edit Connection"}</h2>
            <button class="drawer-close" aria-label="Close">&times;</button>
        `;
      drawer.appendChild(header);

      // ── Protocol selector tabs ──────────────────────────────
      const protocols = ["ssh", "telnet", "raw", "github"];
      const tabBar = createElement("div", "drawer-protocol-tabs");
      for (const proto of protocols) {
        const label = proto === "github" ? "GitHub" : proto.toUpperCase();
        const btn = createElement(
          "button",
          `drawer-proto-tab ${profile.protocol === proto ? "active" : ""}`,
          {
            textContent: label,
            dataset: { proto },
          },
        );
        btn.addEventListener("click", () => this.handleProtocolChange(proto));
        tabBar.appendChild(btn);
      }
      drawer.appendChild(tabBar);

      // ── Scrollable body ─────────────────────────────────────
      const body = createElement("div", "drawer-body");
      const sshKeyDatalist = this._buildSSHKeyDatalist();

      // General section (common to all protocols)
      body.appendChild(this._renderGeneralSection(profile));

      // Protocol-specific sections
      switch (profile.protocol) {
        case "ssh":
          this._renderSSHSections(profile).forEach((s) => body.appendChild(s));
          body.appendChild(sshKeyDatalist);
          break;
        case "telnet":
          this._renderTelnetSections(profile).forEach((s) =>
            body.appendChild(s),
          );
          break;
        case "raw":
          this._renderRAWSections(profile).forEach((s) => body.appendChild(s));
          break;
        case "github":
          this._renderGitHubSections(profile).forEach((s) =>
            body.appendChild(s),
          );
          break;
      }

      // Terminal section (SSH and Telnet only)
      if (profile.protocol !== "raw" && profile.protocol !== "github") {
        body.appendChild(this._renderTerminalSection(profile));
      }

      // Notes & Tags section (common)
      body.appendChild(this._renderNotesSection(profile));

      drawer.appendChild(body);

      // ── Footer ──────────────────────────────────────────────
      const footer = createElement("div", "drawer-footer");
      const cancelBtn = createElement("button", "btn btn-secondary", {
        textContent: "Cancel",
      });
      const testBtn = createElement("button", "btn btn-outline", {
        textContent: "Test",
      });
      const saveBtn = createElement("button", "btn btn-primary", {
        textContent: isNew ? "Create" : "Save",
      });
      const connectBtn = createElement("button", "btn btn-accent", {
        textContent: isNew ? "Create & Connect" : "Save & Connect",
      });
      const deleteBtn = !isNew
        ? createElement("button", "btn btn-danger", { textContent: "Delete" })
        : null;

      cancelBtn.addEventListener("click", () => this.close());
      testBtn.addEventListener("click", () => this._testConnection());
      saveBtn.addEventListener("click", () => this._handleSave(false));
      connectBtn.addEventListener("click", () => this._handleSave(true));
      if (deleteBtn) {
        deleteBtn.addEventListener("click", () => this._handleDelete());
      }

      if (deleteBtn) footer.appendChild(deleteBtn);
      footer.appendChild(cancelBtn);
      footer.appendChild(testBtn);
      footer.appendChild(saveBtn);
      footer.appendChild(connectBtn);
      drawer.appendChild(footer);

      // ── Events ──────────────────────────────────────────────
      header
        .querySelector(".drawer-close")
        .addEventListener("click", () => this.close());
      overlay.addEventListener("click", () => this.close());

      overlay.appendChild(drawer);
      document.body.appendChild(overlay);
      this.drawer = overlay;

      // Trigger visibility after append so CSS transitions animate
      requestAnimationFrame(() => {
        overlay.classList.add("is-visible");
        drawer.classList.add("is-visible");
      });

      // Prevent overlay click from reaching through the drawer
      drawer.addEventListener("click", (e) => e.stopPropagation());

      // Focus the first input
      const firstInput = drawer.querySelector("input, select, textarea");
      if (firstInput) firstInput.focus();

      this._loadSSHKeyOptions();
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
      const fields = [
        this._field(
          "text",
          "displayName",
          "Display Name",
          profile.displayName,
          { placeholder: "My Server" },
        ),
        this._field("text", "host", "Host", profile.host, {
          placeholder:
            profile.protocol === "github"
              ? "github.com"
              : "192.168.1.1 or example.com",
          required: true,
        }),
      ];

      // Port and username not relevant for GitHub connections
      if (profile.protocol !== "github") {
        fields.push(
          this._field("number", "port", "Port", profile.port, {
            min: 1,
            max: 65535,
            required: true,
          }),
        );
        fields.push(
          this._field("text", "username", "Username", profile.username, {
            placeholder: "root",
          }),
        );
      }

      fields.push(this._renderGroupField(profile));
      fields.push(
        this._field("text", "description", "Description", profile.description, {
          placeholder: "Optional description",
        }),
      );

      return this._renderSection("General", fields);
    }

    /**
     * Renders a combo-box for the Group field — a text input backed by a
     * datalist of existing group names. Typing a new name creates a group.
     * @param {Object} profile
     * @returns {HTMLElement}
     * @private
     */
    _renderGroupField(profile) {
      const group = createElement("div", "form-group");
      const label = createElement("label", "form-label", {
        textContent: "Group",
        for: "field-folder",
      });
      group.appendChild(label);

      // Collect distinct group names from all profiles
      const existingGroups = [
        ...new Set(
          this.app.state.profiles
            .map((p) => (p.folder || "").trim())
            .filter(Boolean),
        ),
      ].sort((a, b) => a.localeCompare(b));

      // Create the datalist so the browser offers autocomplete
      const listId = "folder-suggestions";
      const datalist = document.createElement("datalist");
      datalist.id = listId;
      for (const g of existingGroups) {
        const opt = document.createElement("option");
        opt.value = g;
        datalist.appendChild(opt);
      }
      group.appendChild(datalist);

      const input = createElement("input", "form-input", {
        type: "text",
        id: "field-folder",
        name: "folder",
        value: profile.folder || "",
        placeholder: "e.g. Production, Staging, Home Lab",
      });
      input.setAttribute("list", listId);
      input.setAttribute("autocomplete", "off");
      group.appendChild(input);

      const hint = createElement("span", "form-hint", {
        textContent:
          "Choose an existing group or type a new name to create one",
      });
      group.appendChild(hint);

      return group;
    }

    _buildSSHKeyDatalist() {
      const list = createElement("datalist", "", {
        id: "ssh-key-path-options",
      });
      this._applySSHKeyOptionsToList(list);
      return list;
    }

    _applySSHKeyOptionsToList(listEl) {
      if (!listEl) return;
      listEl.innerHTML = "";
      for (const keyPath of this._sshKeyOptions) {
        const opt = createElement("option", "", { value: keyPath });
        listEl.appendChild(opt);
      }
    }

    _loadSSHKeyOptions() {
      if (this._sshKeyOptionsLoading || this._sshKeyOptionsLoaded) return;
      if (!window.go || !window.go.api || !window.go.api.Service) return;

      this._sshKeyOptionsLoading = true;
      window.go.api.Service.ListSSHKeys()
        .then((keys) => {
          this._sshKeyOptions = Array.isArray(keys) ? keys : [];
          this._sshKeyOptionsLoaded = true;
          this._sshKeyOptionsLoading = false;
          if (!this.drawer) return;
          const listEl = this.drawer.querySelector("#ssh-key-path-options");
          this._applySSHKeyOptionsToList(listEl);
        })
        .catch(() => {
          this._sshKeyOptionsLoading = false;
        });
    }

    /** @private */
    _renderSSHSections(profile) {
      const ssh = profile.ssh || {};
      const sections = [];

      // Authentication
      sections.push(
        this._renderSection("Authentication", [
          this._field(
            "select",
            "ssh.secretMode",
            "Password Mode",
            ssh.secretMode,
            {
              options: [
                { value: "saved", label: "Saved in vault" },
                { value: "session-only", label: "Session only" },
                { value: "ask-every-time", label: "Ask every time" },
              ],
            },
          ),
          this._field(
            "tags",
            "ssh.authMethods",
            "Auth Methods",
            (ssh.authMethods || []).join(", "),
            {
              help: "Comma-separated: publickey, password, keyboard-interactive",
            },
          ),
        ]),
      );

      // Key & Passphrase
      sections.push(
        this._renderSection("Key & Passphrase", [
          this._renderSSHKeyPathField(ssh.privateKeyPath),
          this._field(
            "select",
            "ssh.keySecretMode",
            "Passphrase Mode",
            ssh.keySecretMode,
            {
              options: [
                { value: "saved", label: "Saved in vault" },
                { value: "session-only", label: "Session only" },
                { value: "ask-every-time", label: "Ask every time" },
              ],
            },
          ),
        ]),
      );

      // Host Verification
      sections.push(
        this._renderSection("Host Verification", [
          this._field(
            "select",
            "ssh.hostKeyPolicy",
            "Host Key Policy",
            ssh.hostKeyPolicy,
            {
              options: [
                { value: "strict", label: "Strict (reject unknown)" },
                { value: "ask", label: "Ask (prompt on unknown)" },
                { value: "accept-new", label: "Accept new keys" },
                { value: "insecure", label: "Insecure (accept all)" },
              ],
            },
          ),
        ]),
      );

      // Jump Hosts
      sections.push(
        this._renderSection(
          "Jump Hosts",
          [this._renderJumpHostsList(ssh.jumpHosts || [])],
          true,
        ),
      );

      // Port Forwarding
      sections.push(
        this._renderSection(
          "Port Forwarding",
          [this._renderPortForwardsList(ssh.portForwards || [])],
          true,
        ),
      );

      // Startup
      sections.push(
        this._renderSection("Startup", [
          this._field(
            "text",
            "ssh.startupCommand",
            "Startup Command",
            ssh.startupCommand,
            {
              placeholder: "Optional command to run after connecting",
            },
          ),
          this._field(
            "textarea",
            "ssh.envVarsText",
            "Environment Variables",
            this._envVarsToText(ssh.envVars),
            {
              placeholder: "KEY=VALUE (one per line)",
              rows: 3,
            },
          ),
        ]),
      );

      // Login Scripts (expect-style)
      sections.push(
        this._renderLoginScriptSection("ssh", ssh.loginScripts || []),
      );

      // SSH Agent
      sections.push(
        this._renderSection(
          "SSH Agent",
          [
            this._field(
              "toggle",
              "ssh.agentAuth",
              "Use SSH Agent for Authentication",
              ssh.agentAuth,
              {
                help: "Authenticate using keys from the local SSH agent (ssh-agent)",
              },
            ),
            this._field(
              "toggle",
              "ssh.agentForwarding",
              "Forward Agent to Remote",
              ssh.agentForwarding,
              {
                help: "Allow the remote host to use your local SSH agent keys (equivalent to ssh -A)",
              },
            ),
          ],
          true,
        ),
      );

      // TOTP / 2FA
      sections.push(this._renderTOTPSection(profile));

      // Proxy
      sections.push(
        this._renderSection(
          "Proxy",
          [
            this._field(
              "select",
              "ssh.proxyType",
              "Proxy Type",
              ssh.proxyType || "none",
              {
                options: [
                  { value: "none", label: "None (direct connection)" },
                  { value: "socks5", label: "SOCKS5" },
                  { value: "http", label: "HTTP CONNECT" },
                ],
              },
            ),
            this._field(
              "text",
              "ssh.proxyHost",
              "Proxy Host",
              ssh.proxyHost || "",
              {
                placeholder: "proxy.example.com",
              },
            ),
            this._field(
              "number",
              "ssh.proxyPort",
              "Proxy Port",
              ssh.proxyPort || 1080,
              { min: 1, max: 65535 },
            ),
            this._field(
              "text",
              "ssh.proxyUsername",
              "Proxy Username",
              ssh.proxyUsername || "",
              {
                placeholder: "Optional",
              },
            ),
            this._field(
              "text",
              "ssh.proxyPassword",
              "Proxy Password",
              ssh.proxyPassword || "",
              {
                placeholder: "Optional",
              },
            ),
          ],
          true,
        ),
      );

      // Advanced
      sections.push(
        this._renderSection(
          "Advanced",
          [
            this._field(
              "text",
              "ssh.terminalType",
              "Terminal Type",
              ssh.terminalType,
              { placeholder: "xterm-256color" },
            ),
            this._field(
              "number",
              "ssh.keepaliveInterval",
              "Keepalive Interval (s)",
              ssh.keepaliveInterval,
              { min: 0 },
            ),
            this._field(
              "number",
              "ssh.keepaliveCountMax",
              "Max Missed Keepalives",
              ssh.keepaliveCountMax,
              { min: 0 },
            ),
            this._field(
              "toggle",
              "ssh.compression",
              "Compression",
              ssh.compression,
            ),
            this._field(
              "toggle",
              "ssh.autoReconnect",
              "Auto-Reconnect",
              ssh.autoReconnect,
              {
                help: "Automatically reconnect with exponential backoff when the connection drops",
              },
            ),
            this._field(
              "number",
              "ssh.maxReconnectTries",
              "Max Reconnect Attempts",
              ssh.maxReconnectTries || 5,
              { min: 1, max: 20 },
            ),
          ],
          true,
        ),
      );

      return sections;
    }

    /** @private */
    _renderTelnetSections(profile) {
      const tel = profile.telnet || {};
      const sections = [];

      sections.push(
        this._renderSection("Login", [
          this._field(
            "toggle",
            "telnet.localEcho",
            "Local Echo",
            tel.localEcho,
          ),
          this._field("toggle", "telnet.lineMode", "Line Mode", tel.lineMode),
        ]),
      );

      sections.push(
        this._renderSection("Terminal & Encoding", [
          this._field(
            "text",
            "telnet.terminalType",
            "Terminal Type",
            tel.terminalType,
            { placeholder: "xterm-256color" },
          ),
          this._field("select", "telnet.encoding", "Encoding", tel.encoding, {
            options: [
              { value: "utf-8", label: "UTF-8" },
              { value: "ascii", label: "ASCII" },
              { value: "iso-8859-1", label: "ISO 8859-1" },
            ],
          }),
        ]),
      );

      sections.push(
        this._renderSection("Echo & Line Endings", [
          this._field(
            "select",
            "telnet.lineEnding",
            "Line Ending",
            tel.lineEnding,
            {
              options: [
                { value: "crlf", label: "CR+LF (\\r\\n)" },
                { value: "lf", label: "LF (\\n)" },
                { value: "cr", label: "CR (\\r)" },
                { value: "none", label: "None" },
              ],
            },
          ),
        ]),
      );

      sections.push(
        this._renderSection(
          "Negotiation",
          [
            this._field(
              "toggle",
              "telnet.negotiationDebug",
              "Debug Negotiation",
              tel.negotiationDebug,
            ),
          ],
          true,
        ),
      );

      // Login Scripts (expect-style)
      sections.push(
        this._renderLoginScriptSection("telnet", tel.loginScripts || []),
      );

      sections.push(
        this._renderSection(
          "Reconnection",
          [
            this._field(
              "toggle",
              "telnet.autoReconnect",
              "Auto-Reconnect",
              tel.autoReconnect,
              {
                help: "Automatically reconnect with exponential backoff when the connection drops",
              },
            ),
            this._field(
              "number",
              "telnet.maxReconnectTries",
              "Max Reconnect Attempts",
              tel.maxReconnectTries || 5,
              { min: 1, max: 20 },
            ),
          ],
          true,
        ),
      );

      return sections;
    }

    /** @private */
    _renderRAWSections(profile) {
      const raw = profile.raw || {};
      const sections = [];

      sections.push(
        this._renderSection("Encoding & Framing", [
          this._field("select", "raw.encoding", "Encoding", raw.encoding, {
            options: [
              { value: "utf-8", label: "UTF-8" },
              { value: "ascii", label: "ASCII" },
              { value: "hex", label: "Hex" },
            ],
          }),
          this._field(
            "select",
            "raw.lineEnding",
            "Line Ending",
            raw.lineEnding,
            {
              options: [
                { value: "crlf", label: "CR+LF (\\r\\n)" },
                { value: "lf", label: "LF (\\n)" },
                { value: "cr", label: "CR (\\r)" },
                { value: "none", label: "None" },
              ],
            },
          ),
        ]),
      );

      sections.push(
        this._renderSection("Display", [
          this._field(
            "toggle",
            "raw.showTimestamps",
            "Show Timestamps",
            raw.showTimestamps,
          ),
          this._field(
            "toggle",
            "raw.showDirection",
            "Show Direction",
            raw.showDirection,
          ),
        ]),
      );

      sections.push(
        this._renderSection(
          "Payload Presets",
          [this._renderPayloadPresetsList(raw.payloadPresets || [])],
          true,
        ),
      );

      sections.push(
        this._renderSection("Timeouts & Reconnect", [
          this._field(
            "number",
            "connectTimeout",
            "Connect Timeout (s)",
            profile.connectTimeout,
            { min: 0 },
          ),
          this._field(
            "number",
            "readTimeout",
            "Read Timeout (s)",
            profile.readTimeout,
            { min: 0 },
          ),
          this._field(
            "toggle",
            "raw.autoReconnect",
            "Auto-reconnect",
            raw.autoReconnect,
          ),
        ]),
      );

      return sections;
    }

    /** @private */
    _renderGitHubSections(profile) {
      const sections = [];
      const gh = profile.github || DEFAULTS.github.github;

      sections.push(
        this._renderSection("API", [
          this._field("text", "github.baseURL", "API Base URL", gh.baseURL, {
            placeholder: "https://api.github.com",
          }),
          this._field(
            "select",
            "github.secretMode",
            "Token Storage",
            gh.secretMode,
            {
              options: [
                { value: "saved", label: "Save to keychain" },
                { value: "session-only", label: "Remember for session" },
                { value: "ask-every-time", label: "Ask every time" },
              ],
            },
          ),
        ]),
      );

      sections.push(
        this._renderSection("Refresh", [
          this._field(
            "number",
            "github.refreshIntervalSecs",
            "Refresh Interval (seconds)",
            gh.refreshIntervalSecs,
            {
              min: 60,
              max: 3600,
            },
          ),
        ]),
      );

      return sections;
    }

    /** @private */
    _renderTerminalSection(profile) {
      return this._renderSection("Terminal", [
        this._field(
          "text",
          "terminalFontFamily",
          "Font Family",
          profile.terminalFontFamily,
          {
            placeholder: "JetBrains Mono, Menlo, Monaco, Consolas, monospace",
          },
        ),
        this._field(
          "number",
          "terminalFontSize",
          "Font Size",
          profile.terminalFontSize,
          { min: 8, max: 36 },
        ),
        this._field(
          "number",
          "terminalScrollback",
          "Scrollback Lines",
          profile.terminalScrollback,
          { min: 0, max: 100000 },
        ),
        this._field(
          "select",
          "terminalCursorStyle",
          "Cursor Style",
          profile.terminalCursorStyle,
          {
            options: [
              { value: "block", label: "Block" },
              { value: "underline", label: "Underline" },
              { value: "bar", label: "Bar" },
            ],
          },
        ),
      ]);
    }

    /** @private */
    _renderNotesSection(profile) {
      return this._renderSection("Notes & Tags", [
        this._field("tags", "tags", "Tags", (profile.tags || []).join(", "), {
          placeholder: "production, london, vpn",
          help: "Comma-separated tags for filtering",
        }),
        this._field("textarea", "notes", "Notes", profile.notes, {
          placeholder: "Free-form notes…",
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
    /**
     * Renders the TOTP/2FA section with secret management and test button.
     * @param {Object} profile
     * @returns {HTMLElement}
     * @private
     */
    _renderTOTPSection(profile) {
      const section = createElement("div", "form-section");
      const header = createElement("div", "form-section-header");
      let collapsed = true;

      header.innerHTML = `
            <span class="form-section-chevron">▸</span>
            <span class="form-section-title">TOTP / 2FA</span>
        `;

      const content = createElement("div", "form-section-content");
      content.style.display = "none";

      const helpText = createElement("div", "form-help", {
        textContent:
          "Configure a TOTP secret to auto-fill 2FA codes during SSH keyboard-interactive authentication. The secret is stored encrypted.",
      });
      content.appendChild(helpText);

      // TOTP secret input
      const group = createElement("div", "form-group");
      const label = createElement("label", "form-label", {
        textContent: "TOTP Secret",
      });
      group.appendChild(label);

      const row = createElement("div", "totp-input-row");

      const secretInput = createElement("input", "form-input", {
        type: "password",
        placeholder:
          "Base32-encoded secret (e.g. from authenticator app setup)",
        id: "field-ssh.totpSecret",
        dataset: { field: "ssh.totpSecret" },
        autocapitalize: "off",
        autocorrect: "off",
        spellcheck: false,
      });

      // Show/hide toggle
      const toggleBtn = createElement(
        "button",
        "btn btn-small btn-secondary totp-toggle",
        {
          textContent: "👁",
          title: "Show/hide secret",
          type: "button",
        },
      );
      toggleBtn.addEventListener("mousedown", (e) => e.preventDefault());
      toggleBtn.addEventListener("click", () => {
        secretInput.type =
          secretInput.type === "password" ? "text" : "password";
      });

      // Test button — generates a live code
      const testBtn = createElement(
        "button",
        "btn btn-small btn-accent totp-test",
        {
          textContent: "Test",
          title: "Generate a code with this secret",
          type: "button",
        },
      );
      testBtn.addEventListener("mousedown", (e) => e.preventDefault());
      testBtn.addEventListener("click", async () => {
        const profileId = profile.id;
        if (profileId) {
          try {
            const code = await window.go.api.Service.GetTOTPCode(profileId);
            testBtn.textContent = code;
            setTimeout(() => {
              testBtn.textContent = "Test";
            }, 5000);
          } catch (err) {
            // If no saved secret yet, try computing locally using the input value
            testBtn.textContent = "Save first";
            setTimeout(() => {
              testBtn.textContent = "Test";
            }, 2000);
          }
        }
      });

      // Save button
      const saveBtn = createElement(
        "button",
        "btn btn-small btn-primary totp-save",
        {
          textContent: "Save",
          title: "Encrypt and save the TOTP secret",
          type: "button",
        },
      );
      saveBtn.addEventListener("mousedown", (e) => e.preventDefault());
      saveBtn.addEventListener("click", async () => {
        const profileId = profile.id;
        const secret = secretInput.value.trim();
        if (!profileId) {
          NexusTerminal.Notifications.show(
            "Save the profile first, then set the TOTP secret.",
            "warning",
          );
          return;
        }
        if (!secret) {
          // Delete existing secret
          try {
            await window.go.api.Service.DeleteTOTPSecret(profileId);
            NexusTerminal.Notifications.show("TOTP secret removed.", "info");
          } catch (err) {
            NexusTerminal.Notifications.show(
              "Failed to remove TOTP secret.",
              "error",
            );
          }
          return;
        }
        try {
          await window.go.api.Service.SetTOTPSecret(profileId, secret);
          NexusTerminal.Notifications.show(
            "TOTP secret saved securely.",
            "success",
          );
          secretInput.value = "";
          secretInput.placeholder = "••••••••• (secret saved)";
        } catch (err) {
          NexusTerminal.Notifications.show(
            "Failed to save TOTP secret: " + err,
            "error",
          );
        }
      });

      // Delete button
      const deleteBtn = createElement(
        "button",
        "btn btn-small btn-danger totp-delete",
        {
          textContent: "Delete",
          title: "Remove the TOTP secret",
          type: "button",
        },
      );
      deleteBtn.addEventListener("mousedown", (e) => e.preventDefault());
      deleteBtn.addEventListener("click", async () => {
        const profileId = profile.id;
        if (!profileId) return;
        try {
          await window.go.api.Service.DeleteTOTPSecret(profileId);
          NexusTerminal.Notifications.show("TOTP secret removed.", "info");
          secretInput.placeholder =
            "Base32-encoded secret (e.g. from authenticator app setup)";
        } catch (err) {
          NexusTerminal.Notifications.show(
            "Failed to remove TOTP secret.",
            "error",
          );
        }
      });

      row.appendChild(secretInput);
      row.appendChild(toggleBtn);
      row.appendChild(testBtn);
      row.appendChild(saveBtn);
      row.appendChild(deleteBtn);
      group.appendChild(row);
      content.appendChild(group);

      // Check if secret is already saved and show placeholder
      if (profile.id) {
        (async () => {
          try {
            const has = await window.go.api.Service.HasTOTPSecret(profile.id);
            if (has) {
              secretInput.placeholder = "••••••••• (secret saved)";
            }
          } catch (_) {
            // Ignore — might not be unlocked.
          }
        })();
      }

      header.addEventListener("mousedown", (e) => e.preventDefault());
      header.addEventListener("click", () => {
        collapsed = !collapsed;
        content.style.display = collapsed ? "none" : "block";
        header.querySelector(".form-section-chevron").textContent = collapsed
          ? "▸"
          : "▾";
      });

      section.appendChild(header);
      section.appendChild(content);
      return section;
    }

    /**
     * Renders the login script section (expect-style send/wait steps).
     * @param {string} prefix - 'ssh' or 'telnet'
     * @param {Array} steps - Array of {type, value, timeout}
     * @returns {HTMLElement}
     * @private
     */
    _renderLoginScriptSection(prefix, steps) {
      const section = createElement("div", "form-section");
      const header = createElement("div", "form-section-header");
      let collapsed = true;

      header.innerHTML = `
            <span class="form-section-chevron">▸</span>
            <span class="form-section-title">Login Script</span>
        `;

      const content = createElement("div", "form-section-content");
      content.style.display = "none";

      const helpText = createElement("div", "form-help", {
        textContent:
          "Automate login sequences: wait for text from the server, then send a response. Steps execute in order after connection.",
      });
      content.appendChild(helpText);

      // Hidden input to hold JSON
      const hiddenInput = createElement("input", "", {
        type: "hidden",
        id: `field-${prefix}.loginScripts`,
        dataset: { field: `${prefix}.loginScripts` },
      });
      content.appendChild(hiddenInput);

      // List container
      const listContainer = createElement("div", "login-script-list");
      content.appendChild(listContainer);

      // Working copy
      let currentSteps = JSON.parse(JSON.stringify(steps || []));

      const renderSteps = () => {
        listContainer.innerHTML = "";
        hiddenInput.value = JSON.stringify(currentSteps);

        if (currentSteps.length === 0) {
          const empty = createElement("div", "login-script-empty", {
            textContent: "No login script steps configured.",
          });
          listContainer.appendChild(empty);
        }

        currentSteps.forEach((step, idx) => {
          const row = createElement("div", "login-script-row");

          const typeSelect = createElement(
            "select",
            "form-input login-script-type",
          );
          ["wait", "send"].forEach((t) => {
            const opt = createElement("option", "", {
              value: t,
              textContent: t === "wait" ? "Wait for" : "Send",
            });
            if (t === step.type) opt.selected = true;
            typeSelect.appendChild(opt);
          });
          typeSelect.addEventListener("change", () => {
            currentSteps[idx].type = typeSelect.value;
            hiddenInput.value = JSON.stringify(currentSteps);
            renderSteps();
          });

          const valueInput = createElement(
            "input",
            "form-input login-script-value",
            {
              type: "text",
              value: step.value || "",
              placeholder:
                step.type === "wait" ? "Text to match..." : "Text to send...",
              autocapitalize: "off",
              autocorrect: "off",
              spellcheck: false,
            },
          );
          valueInput.addEventListener("input", () => {
            currentSteps[idx].value = valueInput.value;
            hiddenInput.value = JSON.stringify(currentSteps);
          });

          row.appendChild(typeSelect);
          row.appendChild(valueInput);

          // Timeout field for wait steps
          if (step.type === "wait") {
            const timeoutInput = createElement(
              "input",
              "form-input login-script-timeout",
              {
                type: "number",
                value: step.timeout || 10,
                min: 1,
                max: 120,
                title: "Timeout (seconds)",
              },
            );
            timeoutInput.style.width = "60px";
            timeoutInput.addEventListener("input", () => {
              currentSteps[idx].timeout =
                parseInt(timeoutInput.value, 10) || 10;
              hiddenInput.value = JSON.stringify(currentSteps);
            });
            const timeoutLabel = createElement(
              "span",
              "login-script-timeout-label",
              { textContent: "s" },
            );
            row.appendChild(timeoutInput);
            row.appendChild(timeoutLabel);
          }

          // Move up/down/remove buttons
          const actions = createElement("div", "login-script-actions");

          if (idx > 0) {
            const upBtn = createElement("button", "login-script-btn", {
              textContent: "▲",
              title: "Move up",
              type: "button",
            });
            upBtn.addEventListener("mousedown", (e) => e.preventDefault());
            upBtn.addEventListener("click", () => {
              [currentSteps[idx - 1], currentSteps[idx]] = [
                currentSteps[idx],
                currentSteps[idx - 1],
              ];
              renderSteps();
            });
            actions.appendChild(upBtn);
          }

          if (idx < currentSteps.length - 1) {
            const downBtn = createElement("button", "login-script-btn", {
              textContent: "▼",
              title: "Move down",
              type: "button",
            });
            downBtn.addEventListener("mousedown", (e) => e.preventDefault());
            downBtn.addEventListener("click", () => {
              [currentSteps[idx], currentSteps[idx + 1]] = [
                currentSteps[idx + 1],
                currentSteps[idx],
              ];
              renderSteps();
            });
            actions.appendChild(downBtn);
          }

          const removeBtn = createElement(
            "button",
            "login-script-btn login-script-btn-remove",
            { textContent: "✕", title: "Remove", type: "button" },
          );
          removeBtn.addEventListener("mousedown", (e) => e.preventDefault());
          removeBtn.addEventListener("click", () => {
            currentSteps.splice(idx, 1);
            renderSteps();
          });
          actions.appendChild(removeBtn);

          row.appendChild(actions);
          listContainer.appendChild(row);
        });
      };

      renderSteps();

      // Add step buttons
      const addRow = createElement("div", "login-script-add-row");
      const addWaitBtn = createElement(
        "button",
        "btn btn-small btn-secondary",
        { textContent: "+ Wait", type: "button" },
      );
      addWaitBtn.addEventListener("mousedown", (e) => e.preventDefault());
      addWaitBtn.addEventListener("click", () => {
        currentSteps.push({ type: "wait", value: "", timeout: 10 });
        renderSteps();
      });
      const addSendBtn = createElement(
        "button",
        "btn btn-small btn-secondary",
        { textContent: "+ Send", type: "button" },
      );
      addSendBtn.addEventListener("mousedown", (e) => e.preventDefault());
      addSendBtn.addEventListener("click", () => {
        currentSteps.push({ type: "send", value: "", timeout: 0 });
        renderSteps();
      });
      addRow.appendChild(addWaitBtn);
      addRow.appendChild(addSendBtn);
      content.appendChild(addRow);

      header.addEventListener("mousedown", (e) => e.preventDefault());
      header.addEventListener("click", () => {
        collapsed = !collapsed;
        content.style.display = collapsed ? "none" : "block";
        header.querySelector(".form-section-chevron").textContent = collapsed
          ? "▸"
          : "▾";
      });

      section.appendChild(header);
      section.appendChild(content);
      return section;
    }

    _renderSection(title, fields, startCollapsed = false) {
      const section = createElement("div", "form-section");
      const header = createElement("div", "form-section-header");
      let collapsed = startCollapsed;

      header.innerHTML = `
            <span class="form-section-chevron">${collapsed ? "▸" : "▾"}</span>
            <span class="form-section-title">${escapeHtml(title)}</span>
        `;

      const content = createElement("div", "form-section-content");
      if (collapsed) content.style.display = "none";

      for (const field of fields) {
        if (field instanceof HTMLElement) {
          content.appendChild(field);
        }
      }

      // Prevent mousedown from starting a text selection gesture in WKWebView,
      // which would swallow the first click and require a double-click.
      header.addEventListener("mousedown", (e) => e.preventDefault());
      header.addEventListener("click", () => {
        collapsed = !collapsed;
        content.style.display = collapsed ? "none" : "block";
        header.querySelector(".form-section-chevron").textContent = collapsed
          ? "▸"
          : "▾";
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
      const group = createElement("div", "form-group");

      // Label
      const labelEl = createElement("label", "form-label", {
        textContent: label,
        for: `field-${name}`,
      });
      group.appendChild(labelEl);

      // Input element based on type
      let input;

      switch (type) {
        case "text":
        case "file":
          input = createElement("input", "form-input", {
            type: type === "file" ? "text" : "text",
            id: `field-${name}`,
            name,
            value: value || "",
            placeholder: opts.placeholder || "",
          });
          if (opts.required) input.required = true;
          break;

        case "number":
          input = createElement("input", "form-input", {
            type: "number",
            id: `field-${name}`,
            name,
            value: value != null ? String(value) : "",
          });
          if (opts.min != null) input.min = opts.min;
          if (opts.max != null) input.max = opts.max;
          if (opts.required) input.required = true;
          break;

        case "select": {
          input = createElement("select", "form-select", {
            id: `field-${name}`,
            name,
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
        }

        case "toggle": {
          const wrapper = createElement("div", "form-toggle-wrapper");
          input = createElement("input", "form-toggle", {
            type: "checkbox",
            id: `field-${name}`,
            name,
          });
          input.checked = !!value;
          const slider = createElement("label", "form-toggle-label", {
            for: `field-${name}`,
          });
          wrapper.appendChild(input);
          wrapper.appendChild(slider);
          group.appendChild(wrapper);
          // Help text
          if (opts.help) {
            group.appendChild(
              createElement("span", "form-hint", { textContent: opts.help }),
            );
          }
          return group;
        }

        case "textarea":
          input = createElement("textarea", "form-textarea", {
            id: `field-${name}`,
            name,
            placeholder: opts.placeholder || "",
          });
          input.rows = opts.rows || 3;
          input.textContent = value || "";
          break;

        case "tags":
          input = createElement("input", "form-input", {
            type: "text",
            id: `field-${name}`,
            name,
            value: value || "",
            placeholder: opts.placeholder || "",
          });
          break;

        default:
          input = createElement("input", "form-input", {
            type: "text",
            id: `field-${name}`,
            name,
            value: value || "",
          });
      }

      group.appendChild(input);

      if (opts.datalistId && input && input.tagName === "INPUT") {
        input.setAttribute("list", opts.datalistId);
      }

      // Help text
      if (opts.help) {
        group.appendChild(
          createElement("span", "form-hint", { textContent: opts.help }),
        );
      }

      return group;
    }

    _renderSSHKeyPathField(value) {
      const group = createElement("div", "form-group");
      const labelEl = createElement("label", "form-label", {
        textContent: "Private Key Path",
        for: "field-ssh.privateKeyPath",
      });
      group.appendChild(labelEl);

      const row = createElement("div", "key-path-row");
      const input = createElement("input", "form-input", {
        type: "text",
        id: "field-ssh.privateKeyPath",
        name: "ssh.privateKeyPath",
        value: value || "",
        placeholder: "/Users/username/.ssh/id_ed25519",
      });
      input.setAttribute("list", "ssh-key-path-options");
      row.appendChild(input);

      const browseBtn = createElement("button", "btn btn-sm btn-outline", {
        textContent: "Browse",
        type: "button",
      });
      browseBtn.addEventListener("mousedown", (e) => e.preventDefault());
      browseBtn.addEventListener("click", () => {
        if (!window.go || !window.go.api || !window.go.api.Service) return;
        window.go.api.Service.SelectFile("Select SSH Private Key", "*")
          .then((path) => {
            if (path) input.value = path;
          })
          .catch((err) => {
            this.app.notifications.error(
              "Failed to open file dialog: " + (err.message || err),
            );
          });
      });
      row.appendChild(browseBtn);
      group.appendChild(row);

      const error = createElement("span", "form-error");
      error.style.display = "none";
      group.appendChild(error);

      const success = createElement("span", "form-success");
      success.style.display = "none";
      group.appendChild(success);

      this._attachKeyPathValidation(input, error, success);

      group.appendChild(
        createElement("span", "form-hint", {
          textContent: "Select a key from your .ssh folder or type a full path",
        }),
      );

      return group;
    }

    /**
     * Renders the jump-hosts list editor.
     * @param {Array<Object>} jumpHosts
     * @returns {HTMLElement}
     * @private
     */
    _renderJumpHostsList(jumpHosts) {
      const wrapper = createElement("div", "jump-hosts-list");

      for (let i = 0; i < jumpHosts.length; i++) {
        const jh = jumpHosts[i];
        const row = createElement("div", "jump-host-row");
        row.innerHTML = `
                <div class="jump-host-fields">
                    <input class="form-input form-input-sm" name="jumpHost-${i}-host" value="${escapeHtml(jh.host || "")}" placeholder="Host" />
                    <input class="form-input form-input-sm" name="jumpHost-${i}-port" type="number" value="${jh.port || 22}" min="1" max="65535" style="width:80px" />
                    <input class="form-input form-input-sm" name="jumpHost-${i}-username" value="${escapeHtml(jh.username || "")}" placeholder="User" />
                </div>
                <div class="jump-host-fields">
                    <input class="form-input form-input-sm" name="jumpHost-${i}-privateKeyPath" value="${escapeHtml(jh.privateKeyPath || "")}" placeholder="Full key path (optional)" list="ssh-key-path-options" style="flex:1" />
                    <button class="btn btn-sm btn-outline jump-host-browse" type="button" title="Browse">Browse</button>
                    <button class="btn btn-icon btn-danger jump-host-remove" data-index="${i}" title="Remove">&times;</button>
                </div>
            `;
        const keyInput = row.querySelector(
          `input[name="jumpHost-${i}-privateKeyPath"]`,
        );
        const browseBtn = row.querySelector(".jump-host-browse");
        const error = createElement("span", "form-error");
        error.style.display = "none";
        const success = createElement("span", "form-success");
        success.style.display = "none";

        browseBtn.addEventListener("mousedown", (e) => e.preventDefault());
        browseBtn.addEventListener("click", () => {
          if (!window.go || !window.go.api || !window.go.api.Service) return;
          window.go.api.Service.SelectFile("Select SSH Private Key", "*")
            .then((path) => {
              if (path) keyInput.value = path;
              this._validateKeyPath(keyInput, error);
            })
            .catch((err) => {
              this.app.notifications.error(
                "Failed to open file dialog: " + (err.message || err),
              );
            });
        });

        this._attachKeyPathValidation(keyInput, error, success);
        row.querySelector(".jump-host-remove").addEventListener("click", () => {
          jumpHosts.splice(i, 1);
          this.render(this._profile, this._isNew);
        });
        wrapper.appendChild(row);
        wrapper.appendChild(error);
        wrapper.appendChild(success);
      }

      const addBtn = createElement("button", "btn btn-sm btn-outline", {
        textContent: "+ Add Jump Host",
      });
      addBtn.addEventListener("click", () => {
        jumpHosts.push({
          host: "",
          port: 22,
          username: "",
          authMethod: "publickey",
          secretMode: "ask-every-time",
          privateKeyPath: "",
        });
        this.render(this._profile, this._isNew);
      });
      wrapper.appendChild(addBtn);

      return wrapper;
    }

    _attachKeyPathValidation(input, errorEl, successEl) {
      if (!input || !errorEl) return;
      input.addEventListener("blur", () =>
        this._validateKeyPath(input, errorEl, successEl),
      );
      input.addEventListener("input", () => {
        errorEl.style.display = "none";
        errorEl.textContent = "";
        if (successEl) {
          successEl.style.display = "none";
          successEl.textContent = "";
        }
      });
    }

    _validateKeyPath(input, errorEl, successEl) {
      const value = (input.value || "").trim();
      if (!value) {
        errorEl.style.display = "none";
        errorEl.textContent = "";
        if (successEl) {
          successEl.style.display = "none";
          successEl.textContent = "";
        }
        return;
      }
      if (value.startsWith("~")) {
        errorEl.textContent = "Please use a full path (no ~).";
        errorEl.style.display = "block";
        if (successEl) {
          successEl.style.display = "none";
          successEl.textContent = "";
        }
        return;
      }
      if (!window.go || !window.go.api || !window.go.api.Service) {
        return;
      }
      window.go.api.Service.FileExists(value)
        .then((exists) => {
          if (!exists) {
            errorEl.textContent = "Key file not found.";
            errorEl.style.display = "block";
            if (successEl) {
              successEl.style.display = "none";
              successEl.textContent = "";
            }
            return;
          }
          return window.go.api.Service.FileIsPrivateKey(value).then((isKey) => {
            if (!isKey) {
              errorEl.textContent = "File is not a recognised private key.";
              errorEl.style.display = "block";
              if (successEl) {
                successEl.style.display = "none";
                successEl.textContent = "";
              }
              return;
            }
            errorEl.style.display = "none";
            errorEl.textContent = "";
            if (successEl) {
              successEl.textContent = "Key verified.";
              successEl.style.display = "block";
            }
          });
        })
        .catch(() => {
          errorEl.textContent = "Could not verify this path.";
          errorEl.style.display = "block";
          if (successEl) {
            successEl.style.display = "none";
            successEl.textContent = "";
          }
        });
    }

    /**
     * Renders the port-forwarding rules list editor.
     * @param {Array<Object>} portForwards
     * @returns {HTMLElement}
     * @private
     */
    _renderPortForwardsList(portForwards) {
      const wrapper = createElement("div", "port-forwards-list");

      for (let i = 0; i < portForwards.length; i++) {
        const pf = portForwards[i];
        const row = createElement("div", "port-forward-row");
        row.innerHTML = `
                <div class="port-forward-fields">
                    <select class="form-select form-select-sm" name="portFwd-${i}-direction" style="width:110px">
                        <option value="local" ${pf.direction === "local" ? "selected" : ""}>Local →</option>
                        <option value="remote" ${pf.direction === "remote" ? "selected" : ""}>← Remote</option>
                    </select>
                    <input class="form-input form-input-sm" name="portFwd-${i}-bindHost" value="${escapeHtml(pf.bindHost || "127.0.0.1")}" placeholder="Bind host" style="width:110px" />
                    <input class="form-input form-input-sm" name="portFwd-${i}-bindPort" type="number" value="${pf.bindPort || 0}" min="1" max="65535" placeholder="Bind port" style="width:80px" />
                    <span class="port-forward-arrow">⟶</span>
                    <input class="form-input form-input-sm" name="portFwd-${i}-destHost" value="${escapeHtml(pf.destHost || "")}" placeholder="Dest host" style="width:110px" />
                    <input class="form-input form-input-sm" name="portFwd-${i}-destPort" type="number" value="${pf.destPort || 0}" min="1" max="65535" placeholder="Dest port" style="width:80px" />
                </div>
                <div class="port-forward-fields">
                    <input class="form-input form-input-sm" name="portFwd-${i}-label" value="${escapeHtml(pf.label || "")}" placeholder="Label (optional)" style="flex:1" />
                    <label class="port-forward-enabled-label" title="Enable on connect">
                        <input type="checkbox" name="portFwd-${i}-enabled" ${pf.enabled !== false ? "checked" : ""} />
                        <span>Active</span>
                    </label>
                    <button class="btn btn-icon btn-danger port-forward-remove" data-index="${i}" title="Remove">&times;</button>
                </div>
            `;
        row
          .querySelector(".port-forward-remove")
          .addEventListener("click", () => {
            portForwards.splice(i, 1);
            this.render(this._profile, this._isNew);
          });
        wrapper.appendChild(row);
      }

      const addBtn = createElement("button", "btn btn-sm btn-outline", {
        textContent: "+ Add Port Forward",
      });
      addBtn.addEventListener("click", () => {
        portForwards.push({
          id: generateId(),
          direction: "local",
          bindHost: "127.0.0.1",
          bindPort: 0,
          destHost: "",
          destPort: 0,
          enabled: true,
          label: "",
        });
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
      const wrapper = createElement("div", "payload-presets-list");

      for (let i = 0; i < presets.length; i++) {
        const ps = presets[i];
        const row = createElement("div", "preset-row");
        row.innerHTML = `
                <input class="form-input form-input-sm" name="preset-${i}-name" value="${escapeHtml(ps.name || "")}" placeholder="Name" />
                <input class="form-input form-input-sm" name="preset-${i}-payload" value="${escapeHtml(ps.payload || "")}" placeholder="Payload" />
                <select class="form-select form-select-sm" name="preset-${i}-format">
                    <option value="text" ${ps.format === "text" ? "selected" : ""}>Text</option>
                    <option value="hex" ${ps.format === "hex" ? "selected" : ""}>Hex</option>
                    <option value="escaped" ${ps.format === "escaped" ? "selected" : ""}>Escaped</option>
                </select>
                <button class="btn btn-icon btn-danger preset-remove" data-index="${i}" title="Remove">&times;</button>
            `;
        row.querySelector(".preset-remove").addEventListener("click", () => {
          presets.splice(i, 1);
          this.render(this._profile, this._isNew);
        });
        wrapper.appendChild(row);
      }

      const addBtn = createElement("button", "btn btn-sm btn-outline", {
        textContent: "+ Add Preset",
      });
      addBtn.addEventListener("click", () => {
        presets.push({
          id: generateId(),
          name: "",
          payload: "",
          format: "text",
        });
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
        if (el.type === "checkbox") return el.checked;
        if (el.type === "number") return el.value !== "" ? Number(el.value) : 0;
        return el.value;
      };

      const p = this._profile;

      // General
      p.displayName = val("displayName") || "";
      p.host = val("host") || "";
      p.port = val("port") || p.port;
      p.username = val("username") || "";
      p.description = val("description") || "";
      p.folder = (val("folder") || "").trim();
      p.terminalFontFamily = val("terminalFontFamily") || "";
      p.terminalFontSize = val("terminalFontSize") || 14;
      p.terminalScrollback = val("terminalScrollback") || 10000;
      p.terminalCursorStyle = val("terminalCursorStyle") || "block";
      p.connectTimeout = val("connectTimeout") || 30;
      p.readTimeout = val("readTimeout") || 0;

      // Tags
      const tagsStr = val("tags");
      if (tagsStr !== undefined) {
        p.tags = tagsStr
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean);
      }

      // Notes
      const notes = val("notes");
      if (notes !== undefined) p.notes = notes;

      // SSH-specific
      if (p.protocol === "ssh" && p.ssh) {
        p.ssh.secretMode = val("ssh.secretMode") || p.ssh.secretMode;
        p.ssh.privateKeyPath = val("ssh.privateKeyPath") || "";
        p.ssh.keySecretMode = val("ssh.keySecretMode") || p.ssh.keySecretMode;
        p.ssh.hostKeyPolicy = val("ssh.hostKeyPolicy") || p.ssh.hostKeyPolicy;
        p.ssh.terminalType = val("ssh.terminalType") || "xterm-256color";
        p.ssh.keepaliveInterval = val("ssh.keepaliveInterval") ?? 30;
        p.ssh.keepaliveCountMax = val("ssh.keepaliveCountMax") ?? 3;
        p.ssh.compression = val("ssh.compression") || false;
        p.ssh.agentAuth = val("ssh.agentAuth") || false;
        p.ssh.agentForwarding = val("ssh.agentForwarding") || false;
        p.ssh.autoReconnect = val("ssh.autoReconnect") || false;
        p.ssh.maxReconnectTries = val("ssh.maxReconnectTries") ?? 5;
        p.ssh.startupCommand = val("ssh.startupCommand") || "";

        // Proxy settings
        p.ssh.proxyType = val("ssh.proxyType") || "none";
        p.ssh.proxyHost = val("ssh.proxyHost") || "";
        p.ssh.proxyPort = val("ssh.proxyPort") || 1080;
        p.ssh.proxyUsername = val("ssh.proxyUsername") || "";
        p.ssh.proxyPassword = val("ssh.proxyPassword") || "";

        const authStr = val("ssh.authMethods");
        if (authStr !== undefined) {
          p.ssh.authMethods = authStr
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean);
        }

        // Env vars from textarea
        const envText = val("ssh.envVarsText");
        if (envText !== undefined) {
          p.ssh.envVars = {};
          envText.split("\n").forEach((line) => {
            const eq = line.indexOf("=");
            if (eq > 0) {
              p.ssh.envVars[line.slice(0, eq).trim()] = line
                .slice(eq + 1)
                .trim();
            }
          });
        }

        // Jump hosts
        const jh = [];
        let ji = 0;
        while (this.drawer.querySelector(`[name="jumpHost-${ji}-host"]`)) {
          jh.push({
            host: val(`jumpHost-${ji}-host`) || "",
            port: val(`jumpHost-${ji}-port`) || 22,
            username: val(`jumpHost-${ji}-username`) || "",
            authMethod: "publickey",
            secretMode: "ask-every-time",
            privateKeyPath: val(`jumpHost-${ji}-privateKeyPath`) || "",
          });
          ji++;
        }
        p.ssh.jumpHosts = jh;

        // Port forwards
        const pfs = [];
        let fi = 0;
        while (this.drawer.querySelector(`[name="portFwd-${fi}-direction"]`)) {
          pfs.push({
            id:
              (p.ssh.portForwards && p.ssh.portForwards[fi]?.id) ||
              generateId(),
            direction: val(`portFwd-${fi}-direction`) || "local",
            bindHost: val(`portFwd-${fi}-bindHost`) || "127.0.0.1",
            bindPort: val(`portFwd-${fi}-bindPort`) || 0,
            destHost: val(`portFwd-${fi}-destHost`) || "",
            destPort: val(`portFwd-${fi}-destPort`) || 0,
            enabled: val(`portFwd-${fi}-enabled`) !== false,
            label: val(`portFwd-${fi}-label`) || "",
          });
          fi++;
        }
        p.ssh.portForwards = pfs;

        // Login scripts
        const loginScriptsRaw = val("ssh.loginScripts");
        if (loginScriptsRaw) {
          try {
            p.ssh.loginScripts = JSON.parse(loginScriptsRaw);
          } catch (_) {
            p.ssh.loginScripts = [];
          }
        } else {
          p.ssh.loginScripts = [];
        }
      }

      // Telnet-specific
      if (p.protocol === "telnet" && p.telnet) {
        p.telnet.localEcho = val("telnet.localEcho") || false;
        p.telnet.lineMode = val("telnet.lineMode") || false;
        p.telnet.terminalType = val("telnet.terminalType") || "xterm-256color";
        p.telnet.encoding = val("telnet.encoding") || "utf-8";
        p.telnet.lineEnding = val("telnet.lineEnding") || "crlf";
        p.telnet.negotiationDebug = val("telnet.negotiationDebug") || false;
        p.telnet.autoReconnect = val("telnet.autoReconnect") || false;
        p.telnet.maxReconnectTries = val("telnet.maxReconnectTries") ?? 5;

        // Login scripts
        const telLoginScriptsRaw = val("telnet.loginScripts");
        if (telLoginScriptsRaw) {
          try {
            p.telnet.loginScripts = JSON.parse(telLoginScriptsRaw);
          } catch (_) {
            p.telnet.loginScripts = [];
          }
        } else {
          p.telnet.loginScripts = [];
        }
      }

      // RAW-specific
      if (p.protocol === "raw" && p.raw) {
        p.raw.encoding = val("raw.encoding") || "utf-8";
        p.raw.lineEnding = val("raw.lineEnding") || "crlf";
        p.raw.showTimestamps = val("raw.showTimestamps") || false;
        p.raw.showDirection = val("raw.showDirection") || false;
        p.raw.autoReconnect = val("raw.autoReconnect") || false;

        // Presets
        const presets = [];
        let pi = 0;
        while (this.drawer.querySelector(`[name="preset-${pi}-name"]`)) {
          presets.push({
            id:
              (p.raw.payloadPresets && p.raw.payloadPresets[pi]?.id) ||
              generateId(),
            name: val(`preset-${pi}-name`) || "",
            payload: val(`preset-${pi}-payload`) || "",
            format: val(`preset-${pi}-format`) || "text",
          });
          pi++;
        }
        p.raw.payloadPresets = presets;
      }

      // GitHub-specific
      if (p.protocol === "github") {
        if (!p.github) p.github = {};
        p.github.baseURL = val("github.baseURL") || "https://api.github.com";
        p.github.secretMode = val("github.secretMode") || "saved";
        p.github.refreshIntervalSecs = val("github.refreshIntervalSecs") || 300;
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
        errors.push("Host is required.");
      }
      if (!profile.port || profile.port < 1 || profile.port > 65535) {
        errors.push("Port must be between 1 and 65535.");
      }
      return errors;
    }

    // ── Actions ──────────────────────────────────────────────────────

    /** @private */
    async _handleSave(andConnect = false) {
      this._collectFormValues();
      const errors = this._validate(this._profile);
      if (errors.length > 0) {
        this.app.notifications.warning(errors.join(" "));
        return;
      }

      try {
        let saved;
        if (this._isNew) {
          saved = await window.go.api.Service.CreateProfile(this._profile);
          this.app.state.addProfile(saved);
          this.app.notifications.success("Profile created");
        } else {
          saved = await window.go.api.Service.UpdateProfile(this._profile);
          this.app.state.updateProfile(saved);
          this.app.notifications.success("Profile updated");
        }
        this.app.sidebar.render();
        this.close();

        if (andConnect && saved && saved.id) {
          this.app.connectProfile(saved.id);
        }
      } catch (err) {
        this.app.notifications.error("Save failed: " + err);
      }
    }

    /** @private */
    async _handleDelete() {
      const ok = await this._confirmDelete();
      if (!ok) return;

      try {
        await window.go.api.Service.DeleteProfile(this._profile.id);
        this.app.state.removeProfile(this._profile.id);
        this.app.sidebar.render();
        this.app.notifications.success("Profile deleted");
        this.close();
      } catch (err) {
        this.app.notifications.error("Failed to delete profile: " + err);
      }
    }

    /** @private */
    async _testConnection() {
      this._collectFormValues();
      const errors = this._validate(this._profile);
      if (errors.length > 0) {
        this.app.notifications.warning(errors.join(" "));
        return;
      }

      const host = this._profile.host;
      const port = this._profile.port;
      this.app.notifications.info(`Testing connection to ${host}:${port}…`);

      try {
        const result = await window.go.api.Service.TestConnection(host, port);
        this.app.notifications.success(
          `Reachable — ${result.latencyMs}ms latency`,
        );
      } catch (err) {
        this.app.notifications.error("Test failed: " + err);
      }
    }

    /**
     * Shows a confirmation modal for profile deletion.
     * @returns {Promise<boolean>}
     * @private
     */
    _confirmDelete() {
      return new Promise((resolve) => {
        const backdrop = createElement("div", "modal-backdrop");
        const modal = createElement("div", "modal modal-sm");
        const label = escapeHtml(
          this._profile.displayName || this._profile.host || "Untitled",
        );
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
          backdrop.classList.add("is-visible");
          modal.classList.add("is-visible");
        });

        const close = (val) => {
          backdrop.classList.remove("is-visible");
          modal.classList.remove("is-visible");
          setTimeout(() => backdrop.remove(), 200);
          resolve(val);
        };
        modal
          .querySelector(".modal-close")
          .addEventListener("click", () => close(false));
        modal
          .querySelector(".confirm-cancel")
          .addEventListener("click", () => close(false));
        modal
          .querySelector(".confirm-ok")
          .addEventListener("click", () => close(true));
        backdrop.addEventListener("click", (e) => {
          if (e.target === backdrop) close(false);
        });
      });
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
        displayName: "",
        description: "",
        protocol,
        host: "",
        port: 0,
        username: "",
        tags: [],
        folder: "",
        environment: "",
        notes: "",
        favourite: false,
        lastConnected: null,
        connectTimeout: 30,
        readTimeout: 0,
        terminalFontFamily:
          "JetBrains Mono, Menlo, Monaco, Consolas, monospace",
        terminalFontSize: 14,
        terminalScrollback: 10000,
        terminalCursorStyle: "block",
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
      if (!envVars) return "";
      return Object.entries(envVars)
        .map(([k, v]) => `${k}=${v}`)
        .join("\n");
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.ProfileEditor = ProfileEditor;
})();
