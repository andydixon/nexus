(function () {
  /**
   * Nexus Terminal - IRC viewer.
   * Uses xterm.js as the chat stream, with room subtabs, a composer, and
   * room/user sidebars.
   */

  /* global Terminal, FitAddon, SearchAddon, WebLinksAddon */

  const { createElement } = window.NexusTerminal;
  const SERVER_TARGET = "server";
  const MAX_LINES_PER_TARGET = 2000;
  const MAX_LISTED_ROOMS = 200;

  class IRCViewer {
    constructor() {
      this._viewers = new Map();
      this.app = null;
    }

    setApp(app) {
      this.app = app;
    }

    createViewer(sessionID, container, profile) {
      if (this._viewers.has(sessionID) || !container) return;
      const viewer = new IRCViewerInstance(sessionID, container, profile, this.app);
      this._viewers.set(sessionID, viewer);
      viewer.render();
    }

    destroyViewer(sessionID) {
      const viewer = this._viewers.get(sessionID);
      if (!viewer) return;
      viewer.destroy();
      this._viewers.delete(sessionID);
    }

    handleEvent(payload) {
      const sessionID = payload?.sessionID;
      const event = payload?.event || payload;
      const viewer = this._viewers.get(sessionID);
      if (viewer) viewer.handleEvent(event);
    }

    fit(sessionID) {
      const viewer = this._viewers.get(sessionID);
      if (viewer) viewer.fit();
    }
  }

  class IRCViewerInstance {
    constructor(sessionID, container, profile, app) {
      this.sessionID = sessionID;
      this.container = container;
      this.profile = profile || {};
      this.app = app || null;

      this.term = null;
      this.fitAddon = null;
      this.searchAddon = null;
      this.observer = null;

      this.activeTarget = SERVER_TARGET;
      this.targets = new Map();
      this.users = new Map();
      this.joinedRooms = [];
      this.roomList = [];
      this.roomFilter = "";
      this.unreadTargets = new Set();

      this.tabsEl = null;
      this.termHost = null;
      this.inputEl = null;
      this.roomsEl = null;
      this.usersEl = null;
      this.roomListEl = null;
      this.roomFilterEl = null;

      this._ensureTarget(SERVER_TARGET, "Server");
    }

    render() {
      this.container.innerHTML = "";
      this.container.classList.add("irc-viewer-panel");

      const wrapper = createElement("div", "irc-viewer");
      const main = createElement("div", "irc-main");

      this.tabsEl = createElement("div", "irc-room-tabs");
      main.appendChild(this.tabsEl);

      this.termHost = createElement("div", "irc-terminal");
      main.appendChild(this.termHost);

      const composer = createElement("form", "irc-composer");
      this.inputEl = createElement("input", "irc-input", {
        type: "text",
        placeholder: "Message or /command",
        autocomplete: "off",
      });
      const sendBtn = createElement("button", "btn btn-sm btn-primary irc-send", {
        type: "submit",
        textContent: "Send",
      });
      composer.appendChild(this.inputEl);
      composer.appendChild(sendBtn);
      composer.addEventListener("submit", (event) => {
        event.preventDefault();
        this._sendComposer();
      });
      this.inputEl.addEventListener("keydown", (event) => {
        if (event.key === "Tab" && this._completeMention()) {
          event.preventDefault();
        }
      });
      main.appendChild(composer);

      const sidebar = this._buildSidebar();
      wrapper.appendChild(main);
      wrapper.appendChild(sidebar);
      this.container.appendChild(wrapper);

      this._openTerminal();
      this._renderTabs();
      this._renderRooms();
      this._renderUsers();
      this._renderRoomList();
      this._appendLine(SERVER_TARGET, {
        type: "system",
        text: "Connecting...",
        timestamp: Date.now(),
      });
    }

    handleEvent(event) {
      if (!event || !event.type) return;

      if (event.type === "rooms") {
        this.joinedRooms = Array.isArray(event.rooms) ? event.rooms : [];
        for (const room of this.joinedRooms) {
          this._ensureTarget(room.name, room.name);
        }
        this._renderTabs();
        this._renderRooms();
        return;
      }

      if (event.type === "users") {
        const target = event.target;
        if (target) {
          this.users.set(target, Array.isArray(event.users) ? event.users : []);
          if (target === this.activeTarget) this._renderUsers();
        }
        return;
      }

      if (event.type === "room-list") {
        this.roomList = Array.isArray(event.rooms) ? event.rooms : [];
        this._renderRoomList();
        return;
      }

      const target = this._eventTarget(event);
      this._ensureTarget(target, this._targetLabel(target));
      this._appendLine(target, event);

      if (target !== this.activeTarget) {
        this.unreadTargets.add(target);
        this._renderTabs();
      }

      if (["join", "part", "topic"].includes(event.type)) {
        this._renderRooms();
      }
    }

    fit() {
      if (!this.fitAddon) return;
      requestAnimationFrame(() => {
        try {
          this.fitAddon.fit();
        } catch (_) {
          /* noop */
        }
      });
    }

    destroy() {
      if (this.observer) {
        this.observer.disconnect();
        this.observer = null;
      }
      if (this.term) {
        this.term.dispose();
        this.term = null;
      }
      this.fitAddon = null;
      this.searchAddon = null;
      this.targets.clear();
      this.users.clear();
      this.unreadTargets.clear();
      this.joinedRooms = [];
      this.roomList = [];
      this.container.classList.remove("irc-viewer-panel");
      this.container.innerHTML = "";
    }

    _buildSidebar() {
      const sidebar = createElement("aside", "irc-sidebar");

      const joinSection = createElement("div", "irc-sidebar-section");
      joinSection.appendChild(
        createElement("div", "irc-sidebar-title", { textContent: "Rooms" }),
      );
      const joinRow = createElement("form", "irc-join-row");
      const joinInput = createElement("input", "irc-join-input", {
        type: "text",
        placeholder: "#room",
        autocomplete: "off",
      });
      const joinBtn = createElement("button", "btn btn-xs btn-secondary", {
        type: "submit",
        textContent: "Join",
      });
      joinRow.appendChild(joinInput);
      joinRow.appendChild(joinBtn);
      joinRow.addEventListener("submit", async (event) => {
        event.preventDefault();
        const room = joinInput.value.trim();
        if (!room) return;
        try {
          await window.NexusTerminal.wailsService.IRCJoin(this.sessionID, room);
          joinInput.value = "";
        } catch (err) {
          this._notifyError("Join failed: " + err);
        }
      });
      joinSection.appendChild(joinRow);
      this.roomsEl = createElement("div", "irc-sidebar-list");
      joinSection.appendChild(this.roomsEl);
      sidebar.appendChild(joinSection);

      const listSection = createElement("div", "irc-sidebar-section");
      const listTitleRow = createElement("div", "irc-sidebar-title-row");
      listTitleRow.appendChild(
        createElement("div", "irc-sidebar-title", { textContent: "Room List" }),
      );
      const listBtn = createElement("button", "btn btn-xs btn-outline", {
        type: "button",
        textContent: "List",
      });
      listBtn.addEventListener("click", async () => {
        this.roomList = [];
        this._renderRoomList("Loading rooms...");
        try {
          await window.NexusTerminal.wailsService.IRCListRooms(this.sessionID);
        } catch (err) {
          this._renderRoomList("Room list failed");
          this._notifyError("Room list failed: " + err);
        }
      });
      listTitleRow.appendChild(listBtn);
      listSection.appendChild(listTitleRow);
      this.roomFilterEl = createElement("input", "irc-filter-input", {
        type: "text",
        placeholder: "Filter rooms",
        autocomplete: "off",
      });
      this.roomFilterEl.addEventListener("input", () => {
        this.roomFilter = this.roomFilterEl.value.toLowerCase();
        this._renderRoomList();
      });
      listSection.appendChild(this.roomFilterEl);
      this.roomListEl = createElement("div", "irc-room-list");
      listSection.appendChild(this.roomListEl);
      sidebar.appendChild(listSection);

      const usersSection = createElement("div", "irc-sidebar-section irc-users-section");
      usersSection.appendChild(
        createElement("div", "irc-sidebar-title", { textContent: "Users" }),
      );
      this.usersEl = createElement("div", "irc-user-list");
      usersSection.appendChild(this.usersEl);
      sidebar.appendChild(usersSection);

      return sidebar;
    }

    _openTerminal() {
      const themeName =
        (this.app?.state?.preferences || {}).terminalTheme || "charcoal";
      this.term = new Terminal({
        theme: window.NexusTerminal.getTerminalTheme(themeName),
        fontFamily:
          this.profile.terminalFontFamily ||
          'JetBrains Mono, Menlo, Monaco, Consolas, "Apple Color Emoji", "Segoe UI Emoji", "Noto Color Emoji", monospace',
        fontSize: this.profile.terminalFontSize || 14,
        scrollback: this.profile.terminalScrollback || 10000,
        cursorBlink: false,
        convertEol: true,
        disableStdin: true,
      });
      this.fitAddon = new FitAddon.FitAddon();
      this.searchAddon = new SearchAddon.SearchAddon();
      this.term.loadAddon(this.fitAddon);
      this.term.loadAddon(this.searchAddon);
      try {
        this.term.loadAddon(new WebLinksAddon.WebLinksAddon((_event, uri) => {
          window.runtime.BrowserOpenURL(uri);
        }));
      } catch (_) {
        /* optional */
      }
      this.term.open(this.termHost);
      this.fit();
      this.observer = new ResizeObserver(() => this.fit());
      this.observer.observe(this.termHost);
    }

    _renderTabs() {
      if (!this.tabsEl) return;
      this.tabsEl.innerHTML = "";
      for (const [target, state] of this.targets) {
        const btn = createElement(
          "button",
          `irc-room-tab ${target === this.activeTarget ? "active" : ""} ${
            this.unreadTargets.has(target) ? "has-unread" : ""
          }`,
          {
            type: "button",
            textContent: state.label,
            title: state.label,
          },
        );
        btn.addEventListener("click", () => this._setActiveTarget(target));
        this.tabsEl.appendChild(btn);
      }
    }

    _renderRooms() {
      if (!this.roomsEl) return;
      this.roomsEl.innerHTML = "";
      if (this.joinedRooms.length === 0) {
        this.roomsEl.appendChild(
          createElement("div", "irc-empty", { textContent: "No joined rooms" }),
        );
        return;
      }
      for (const room of this.joinedRooms) {
        const row = createElement("button", "irc-room-row", {
          type: "button",
          title: room.topic || room.name,
        });
        row.classList.toggle("active", room.name === this.activeTarget);
        row.appendChild(createElement("span", "irc-room-name", { textContent: room.name }));
        row.appendChild(
          createElement("span", "irc-room-count", {
            textContent: String(room.users || 0),
          }),
        );
        row.addEventListener("click", () => this._setActiveTarget(room.name));
        this.roomsEl.appendChild(row);
      }
    }

    _renderUsers() {
      if (!this.usersEl) return;
      this.usersEl.innerHTML = "";
      const users = this.users.get(this.activeTarget) || [];
      if (!this._isRoom(this.activeTarget)) {
        this.usersEl.appendChild(
          createElement("div", "irc-empty", { textContent: "No room selected" }),
        );
        return;
      }
      if (users.length === 0) {
        this.usersEl.appendChild(
          createElement("div", "irc-empty", { textContent: "No users yet" }),
        );
        return;
      }
      for (const user of users) {
        const row = createElement("button", "irc-user-row", {
          type: "button",
          textContent: this._displayNick(user),
          title: user,
        });
        row.addEventListener("click", () => {
          this._insertMention(this._displayNick(user));
        });
        this.usersEl.appendChild(row);
      }
    }

    _renderRoomList(message = "") {
      if (!this.roomListEl) return;
      this.roomListEl.innerHTML = "";
      if (message) {
        this.roomListEl.appendChild(
          createElement("div", "irc-empty", { textContent: message }),
        );
        return;
      }
      let rooms = this.roomList;
      if (this.roomFilter) {
        rooms = rooms.filter((room) => {
          const haystack = `${room.name || ""} ${room.topic || ""}`.toLowerCase();
          return haystack.includes(this.roomFilter);
        });
      }
      rooms = rooms.slice(0, MAX_LISTED_ROOMS);
      if (rooms.length === 0) {
        this.roomListEl.appendChild(
          createElement("div", "irc-empty", { textContent: "No rooms loaded" }),
        );
        return;
      }
      for (const room of rooms) {
        const row = createElement("div", "irc-listed-room");
        const meta = createElement("button", "irc-listed-room-main", {
          type: "button",
          title: room.topic || room.name,
        });
        meta.appendChild(
          createElement("span", "irc-listed-room-name", {
            textContent: room.name || "",
          }),
        );
        meta.appendChild(
          createElement("span", "irc-listed-room-topic", {
            textContent: room.topic || "",
          }),
        );
        meta.addEventListener("click", () => this._joinRoom(room.name));
        const count = createElement("span", "irc-listed-room-count", {
          textContent: String(room.users || 0),
        });
        row.appendChild(meta);
        row.appendChild(count);
        this.roomListEl.appendChild(row);
      }
    }

    _setActiveTarget(target) {
      this._ensureTarget(target, this._targetLabel(target));
      this.activeTarget = target;
      this.unreadTargets.delete(target);
      this._renderTabs();
      this._renderRooms();
      this._renderUsers();
      this._replayActiveTarget();
      if (this.inputEl) this.inputEl.focus();
    }

    _appendLine(target, event) {
      const state = this._ensureTarget(target, this._targetLabel(target));
      const line = this._formatEvent(event);
      if (!line) return;
      state.lines.push(line);
      if (state.lines.length > MAX_LINES_PER_TARGET) {
        state.lines.splice(0, state.lines.length - MAX_LINES_PER_TARGET);
      }
      if (target === this.activeTarget && this.term) {
        this.term.writeln(line);
        this.term.scrollToBottom();
      }
    }

    _replayActiveTarget() {
      if (!this.term) return;
      this.term.clear();
      const state = this.targets.get(this.activeTarget);
      for (const line of state?.lines || []) {
        this.term.writeln(line);
      }
      this.term.scrollToBottom();
      this.fit();
    }

    async _sendComposer() {
      const text = this.inputEl.value;
      if (!text.trim()) return;
      this.inputEl.value = "";
      try {
        await window.NexusTerminal.wailsService.IRCHandleInput(
          this.sessionID,
          this.activeTarget,
          text,
        );
      } catch (err) {
        this._notifyError(String(err));
        this._appendLine(this.activeTarget, {
          type: "error",
          text: String(err),
          timestamp: Date.now(),
        });
      }
    }

    async _joinRoom(room) {
      if (!room) return;
      try {
        await window.NexusTerminal.wailsService.IRCJoin(this.sessionID, room);
      } catch (err) {
        this._notifyError("Join failed: " + err);
      }
    }

    _completeMention() {
      if (!this.inputEl || !this._isRoom(this.activeTarget)) return false;
      const start = this.inputEl.selectionStart || 0;
      const before = this.inputEl.value.slice(0, start);
      const match = before.match(/(^|\s)@([^\s@]*)$/);
      if (!match) return false;
      const prefix = match[2].toLowerCase();
      const users = this.users.get(this.activeTarget) || [];
      const nick = users
        .map((user) => this._displayNick(user))
        .find((user) => user.toLowerCase().startsWith(prefix));
      if (!nick) return false;
      const replacement = `${match[1]}@${nick} `;
      const nextValue =
        this.inputEl.value.slice(0, start - match[0].length) +
        replacement +
        this.inputEl.value.slice(start);
      const caret = start - match[0].length + replacement.length;
      this.inputEl.value = nextValue;
      this.inputEl.setSelectionRange(caret, caret);
      return true;
    }

    _insertMention(nick) {
      if (!this.inputEl) return;
      const current = this.inputEl.value;
      const spacer = current && !/\s$/.test(current) ? " " : "";
      this.inputEl.value = `${current}${spacer}@${nick} `;
      this.inputEl.focus();
    }

    _ensureTarget(target, label) {
      const safeTarget = target || SERVER_TARGET;
      if (!this.targets.has(safeTarget)) {
        this.targets.set(safeTarget, {
          label: label || this._targetLabel(safeTarget),
          lines: [],
        });
      }
      return this.targets.get(safeTarget);
    }

    _eventTarget(event) {
      if (!event) return SERVER_TARGET;
      if (event.target) return event.target;
      return SERVER_TARGET;
    }

    _targetLabel(target) {
      return target === SERVER_TARGET ? "Server" : target;
    }

    _formatEvent(event) {
      const time = this._time(event.timestamp);
      const from = this._sanitize(event.from || "");
      const text = this._sanitize(event.text || "");
      switch (event.type) {
        case "message":
          return `[${time}] <${from}> ${text}`;
        case "action":
          return `[${time}] * ${from} ${text}`;
        case "notice":
          return `[${time}] -${from || "notice"}- ${text}`;
        case "join":
          return `[${time}] --> ${from || "user"} joined`;
        case "part":
          return `[${time}] <-- ${from || "user"} left${text ? ` (${text})` : ""}`;
        case "quit":
          return `[${time}] <-- ${from || "user"} quit${text ? ` (${text})` : ""}`;
        case "nick":
          return `[${time}] ${from || "user"} is now known as ${text}`;
        case "topic":
          return `[${time}] topic: ${text}`;
        case "error":
          return `[${time}] error: ${text}`;
        case "system":
        default:
          return `[${time}] ${text}`;
      }
    }

    _time(value) {
      const date = value ? new Date(value) : new Date();
      const hours = String(date.getHours()).padStart(2, "0");
      const mins = String(date.getMinutes()).padStart(2, "0");
      const secs = String(date.getSeconds()).padStart(2, "0");
      return `${hours}:${mins}:${secs}`;
    }

    _sanitize(value) {
      return String(value || "")
        .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
        .replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/g, "")
        .replace(/\x03(\d{1,2}(,\d{1,2})?)?/g, "")
        .replace(/[\x02\x0f\x16\x1d\x1f]/g, "");
    }

    _displayNick(user) {
      return String(user || "").replace(/^[~&@%+]+/, "");
    }

    _isRoom(target) {
      return /^[#&+!]/.test(target || "");
    }

    _notifyError(message) {
      if (this.app?.notifications) {
        this.app.notifications.error(message);
      }
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.IRCViewer = new IRCViewer();
})();
