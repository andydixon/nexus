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
  const ANSI_RESET = "\x1b[0m";
  const ANSI_DIM = "\x1b[2m";
  const ANSI_BOLD = "\x1b[1m";
  const ANSI_ITALIC = "\x1b[3m";
  const ANSI_UNDERLINE = "\x1b[4m";
  const ANSI_REVERSE = "\x1b[7m";
  const ANSI_STRIKE = "\x1b[9m";
  const ANSI_GREY = "\x1b[38;5;244m";
  const ANSI_RED = "\x1b[38;5;203m";
  const ANSI_GREEN = "\x1b[38;5;83m";
  const ANSI_YELLOW = "\x1b[38;5;221m";
  const ANSI_CYAN = "\x1b[38;5;81m";
  const ANSI_MAGENTA = "\x1b[38;5;177m";
  const NICK_COLOURS = [33, 39, 45, 69, 75, 81, 111, 141, 177, 207, 214, 220];

  // IRC colours 00-98 mapped to the xterm 256-colour palette described by
  // the modern IRC formatting specification. Colour 99 means terminal default.
  const IRC_ANSI_COLOURS = [
    15, 0, 4, 2, 9, 1, 5, 130, 11, 10, 6, 14, 12, 13, 8, 7,
    52, 94, 100, 58, 22, 29, 23, 24, 17, 54, 53, 89,
    88, 130, 142, 64, 28, 35, 30, 25, 18, 91, 90, 125,
    124, 166, 184, 106, 34, 49, 37, 33, 19, 129, 127, 161,
    196, 208, 226, 154, 46, 86, 51, 75, 21, 171, 201, 198,
    203, 215, 227, 191, 83, 122, 87, 111, 63, 177, 207, 205,
    217, 223, 229, 193, 157, 158, 159, 153, 147, 183, 219, 212,
    16, 233, 235, 237, 239, 241, 244, 247, 250, 254, 231,
  ];

  function stripTerminalEscapes(value) {
    return String(value || "")
      .replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, "")
      .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
      .replace(/\x1b./g, "")
      .replace(/[\x80-\x9f]/g, "");
  }

  function readDecimalColour(input, offset) {
    const match = input.slice(offset).match(/^\d{1,2}/);
    if (!match) return null;
    return { value: Number(match[0]), length: match[0].length };
  }

  function readHexColour(input, offset) {
    const value = input.slice(offset, offset + 6);
    if (!/^[0-9a-f]{6}$/i.test(value)) return null;
    return { value, length: 6 };
  }

  function indexedColour(code, background = false) {
    if (code === 99 || code < 0 || code >= IRC_ANSI_COLOURS.length) {
      return `\x1b[${background ? 49 : 39}m`;
    }
    return `\x1b[${background ? 48 : 38};5;${IRC_ANSI_COLOURS[code]}m`;
  }

  function hexColour(value, background = false) {
    const red = parseInt(value.slice(0, 2), 16);
    const green = parseInt(value.slice(2, 4), 16);
    const blue = parseInt(value.slice(4, 6), 16);
    return `\x1b[${background ? 48 : 38};2;${red};${green};${blue}m`;
  }

  function formatIRCText(value) {
    const input = stripTerminalEscapes(value);
    const toggles = {
      "\x02": [ANSI_BOLD, "\x1b[22m", false],
      "\x1d": [ANSI_ITALIC, "\x1b[23m", false],
      "\x1e": [ANSI_STRIKE, "\x1b[29m", false],
      "\x1f": [ANSI_UNDERLINE, "\x1b[24m", false],
      "\x16": [ANSI_REVERSE, "\x1b[27m", false],
    };
    let output = "";

    for (let index = 0; index < input.length; index += 1) {
      const char = input[index];
      if (toggles[char]) {
        const toggle = toggles[char];
        toggle[2] = !toggle[2];
        output += toggle[toggle[2] ? 0 : 1];
        continue;
      }
      if (char === "\x0f") {
        output += ANSI_RESET;
        for (const toggle of Object.values(toggles)) toggle[2] = false;
        continue;
      }
      if (char === "\x03" || char === "\x04") {
        const readColour = char === "\x03" ? readDecimalColour : readHexColour;
        const foreground = readColour(input, index + 1);
        if (!foreground) {
          output += "\x1b[39;49m";
          continue;
        }
        index += foreground.length;
        output +=
          char === "\x03"
            ? indexedColour(foreground.value)
            : hexColour(foreground.value);

        if (input[index + 1] !== ",") continue;
        const background = readColour(input, index + 2);
        if (!background) {
          output += ",";
          index += 1;
          continue;
        }
        index += background.length + 1;
        output +=
          char === "\x03"
            ? indexedColour(background.value, true)
            : hexColour(background.value, true);
        continue;
      }
      // xterm is already monospace, so the IRC monospace toggle needs no SGR.
      if (char === "\x11") continue;
      const code = char.charCodeAt(0);
      if (code < 0x20 || code === 0x7f) continue;
      output += char;
    }
    return output;
  }

  function plainIRCText(value) {
    return formatIRCText(value).replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "");
  }

  function nickColour(nick) {
    let hash = 0;
    for (const char of String(nick || "")) {
      hash = (hash * 31 + char.codePointAt(0)) >>> 0;
    }
    return `\x1b[38;5;${NICK_COLOURS[hash % NICK_COLOURS.length]}m`;
  }

  function formatIRCEvent(event, time) {
    const from = plainIRCText(event?.from || "");
    const text = formatIRCText(event?.text || "");
    const subject = plainIRCText(event?.command || "");
    const timestamp = `${ANSI_DIM}${ANSI_GREY}[${time}]${ANSI_RESET}`;
    const styledNick = from
      ? `${ANSI_BOLD}${nickColour(from)}${from}${ANSI_RESET}`
      : "";
    let body;

    switch (event?.type) {
      case "message":
        body = `${ANSI_GREY}<${ANSI_RESET}${styledNick}${ANSI_GREY}>${ANSI_RESET} ${text}`;
        break;
      case "action":
        body = `${ANSI_MAGENTA}${ANSI_ITALIC}* ${styledNick}${ANSI_MAGENTA}${ANSI_ITALIC} ${text}`;
        break;
      case "notice":
        body = `${ANSI_YELLOW}-${styledNick || "notice"}${ANSI_YELLOW}-${ANSI_RESET} ${text}`;
        break;
      case "join":
        body = `${ANSI_GREEN}-->${ANSI_RESET} ${styledNick || "user"} joined`;
        break;
      case "part":
        body = `${ANSI_RED}<--${ANSI_RESET} ${styledNick || "user"} left${text ? ` (${text})` : ""}`;
        break;
      case "kick":
        body = `${ANSI_RED}<--${ANSI_RESET} ${ANSI_BOLD}${subject || "user"}${ANSI_RESET} was kicked by ${styledNick || "server"}${text ? ` (${text})` : ""}`;
        break;
      case "quit":
        body = `${ANSI_RED}<--${ANSI_RESET} ${styledNick || "user"} quit${text ? ` (${text})` : ""}`;
        break;
      case "nick":
        body = `${styledNick || "user"} is now known as ${ANSI_BOLD}${plainIRCText(event?.text || "")}${ANSI_RESET}`;
        break;
      case "topic":
        body = `${ANSI_CYAN}${ANSI_BOLD}topic:${ANSI_RESET} ${text}`;
        break;
      case "error":
        body = `${ANSI_RED}${ANSI_BOLD}error:${ANSI_RESET} ${text}`;
        break;
      case "system":
      default:
        body = `${ANSI_CYAN}${text}`;
        break;
    }
    return `${timestamp} ${body}${ANSI_RESET}`;
  }

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
      this.destroyed = false;

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
      void this._refreshJoinedRooms();
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

      if (["join", "part", "kick", "topic"].includes(event.type)) {
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
      this.destroyed = true;
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
          title: plainIRCText(room.topic || room.name),
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
          title: plainIRCText(room.topic || room.name),
        });
        meta.appendChild(
          createElement("span", "irc-listed-room-name", {
            textContent: room.name || "",
          }),
        );
        meta.appendChild(
          createElement("span", "irc-listed-room-topic", {
            textContent: plainIRCText(room.topic || ""),
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

    async _refreshJoinedRooms() {
      try {
        const rooms = await window.NexusTerminal.wailsService.IRCRooms(this.sessionID);
        if (this.destroyed) return;
        this.handleEvent({ type: "rooms", rooms: Array.isArray(rooms) ? rooms : [] });
      } catch (_) {
        // The session may have closed while the viewer was being created.
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
      return formatIRCEvent(event, this._time(event.timestamp));
    }

    _time(value) {
      const date = value ? new Date(value) : new Date();
      const hours = String(date.getHours()).padStart(2, "0");
      const mins = String(date.getMinutes()).padStart(2, "0");
      const secs = String(date.getSeconds()).padStart(2, "0");
      return `${hours}:${mins}:${secs}`;
    }

    _sanitize(value) {
      return plainIRCText(value);
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
  window.NexusTerminal.IRCFormatting = Object.freeze({
    formatEvent: formatIRCEvent,
    formatText: formatIRCText,
    plainText: plainIRCText,
  });
  window.NexusTerminal.IRCViewer = new IRCViewer();
})();
