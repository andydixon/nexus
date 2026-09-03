#!/usr/bin/env node
const fs = require("fs");
const path = require("path");

const root = path.resolve(__dirname, "..");
const repo = path.resolve(root, "..");

function read(rel) {
  return fs.readFileSync(path.join(repo, rel), "utf8");
}

function assertContains(file, needle, message) {
  const text = read(file);
  if (!text.includes(needle)) {
    throw new Error(`${message}\nMissing ${JSON.stringify(needle)} in ${file}`);
  }
}

function assertNotContains(file, needle, message) {
  const text = read(file);
  if (text.includes(needle)) {
    throw new Error(`${message}\nUnexpected ${JSON.stringify(needle)} in ${file}`);
  }
}

function assertBefore(file, first, second, message) {
  const text = read(file);
  const firstIndex = text.indexOf(first);
  const secondIndex = text.indexOf(second);
  if (firstIndex === -1 || secondIndex === -1 || firstIndex >= secondIndex) {
    throw new Error(
      `${message}\nExpected ${JSON.stringify(first)} before ${JSON.stringify(second)} in ${file}`,
    );
  }
}

function assertTreeNotContains(relDir, needle, message) {
  const dir = path.join(repo, relDir);
  const offenders = [];
  const walk = (current) => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) {
        walk(full);
        continue;
      }
      if (!entry.isFile() || !entry.name.endsWith(".js")) continue;
      const rel = path.relative(repo, full);
      if (read(rel).includes(needle)) offenders.push(rel);
    }
  };
  walk(dir);
  if (offenders.length) {
    throw new Error(`${message}\nUnexpected ${JSON.stringify(needle)} in ${offenders.join(", ")}`);
  }
}

assertContains(
  "frontend/src/js/terminal.js",
  "escapeHtml(title)",
  "file-drop prompt titles must escape filenames containing HTML characters",
);
assertContains(
  "frontend/src/js/terminal.js",
  "SFTPUploadStart",
  "drag-drop upload must use chunked SFTP streaming",
);
assertContains(
  "frontend/src/js/terminal.js",
  "SFTPUploadChunk",
  "drag-drop upload must send bounded chunks",
);
assertContains(
  "frontend/src/js/terminal.js",
  "scheduleFit",
  "terminal resize handling must be throttled through scheduled fits",
);
assertContains(
  "frontend/src/js/terminal.js",
  "dimensionsChanged",
  "terminal resize handling must skip unchanged container dimensions",
);
assertContains(
  "frontend/src/js/terminal.js",
  "cancelScheduledFit",
  "terminal teardown must cancel pending fit work",
);
assertContains(
  "frontend/src/js/terminal.js",
  "this._sendTerminalSize(sessionId, term);",
  "every completed terminal fit must publish its geometry to the backend PTY",
);
assertContains(
  "frontend/src/js/terminal.js",
  "[Exited - press enter to restart]",
  "exited local shells must show the inline restart prompt",
);
assertContains(
  "frontend/src/js/terminal.js",
  "RestartLocalSession",
  "pressing enter after a local shell exit must restart the existing session",
);
assertContains(
  "frontend/src/js/app.js",
  "!localExited &&",
  "local shell exits must not show the disconnected overlay",
);
assertNotContains(
  "frontend/src/js/terminal.js",
  "new ResizeObserver(() => {\n        try {\n          fitAddon.fit();",
  "ResizeObserver must not call fitAddon.fit() directly",
);
assertNotContains(
  "frontend/src/js/terminal.js",
  "Use SFTP Upload for files over 25 MB",
  "drag-drop upload must not reject files only because they exceed the old inline limit",
);
assertContains(
  "frontend/src/js/app.js",
  "confirmDirectVNCProfile",
  "direct VNC connections must show an explicit warning",
);
assertContains(
  "frontend/src/js/profiles.js",
  "skipDirectVNCWarning",
  "save-and-connect should not show duplicate direct VNC prompts",
);
assertContains(
  "frontend/src/js/recordings.js",
  "import-status",
  "profile import failures must be visible inside the import modal",
);
assertContains(
  "frontend/src/js/recordings.js",
  "_formatImportError",
  "profile import failures must be formatted without using HTML injection",
);
assertContains(
  "internal/api/api_test.go",
  "TestCreateProfileRejectsUnsafeGitHubBaseURL",
  "GitHub HTTP/HTTPS URL validation must stay covered",
);
assertContains(
  "frontend/index.html",
  "mode-leviathan",
  "Leviathan mode toggle must be present beside Alchementary Link",
);
assertContains(
  "frontend/index.html",
  'id="mode-leviathan"\n                type="button"\n                hidden',
  "Leviathan mode toggle must be hidden before preferences load",
);
assertContains(
  "frontend/src/js/app.js",
  "prefs.leviathanEnabled === true",
  "Leviathan mode must only be exposed by an explicit persisted preference",
);
assertContains(
  "frontend/src/js/settings.js",
  "KONAMI_SEQUENCE",
  "the About window must recognise the Konami sequence",
);
assertContains(
  "frontend/src/js/settings.js",
  "_toggleLeviathanFromKonami",
  "the About-window Konami sequence must toggle Leviathan",
);
assertContains(
  "frontend/src/js/settings.js",
  "SavePreferences(prefs)",
  "the Konami toggle must persist the Leviathan preference immediately",
);
assertContains(
  "frontend/src/css/sidebar.css",
  "user-select: none",
  "sidebar title and mode switcher text must not be selectable",
);
assertContains(
  "frontend/src/css/sidebar.css",
  "#mode-leviathan",
  "Leviathan mode label must have its own right-alignment rule",
);
assertContains(
  "frontend/src/css/sidebar.css",
  "margin-left: auto;",
  "Leviathan mode label must sit on the right side of the sidebar header",
);
assertContains(
  "frontend/src/css/tokens.css",
  "--tab-bar-height: 44px;",
  "tab bar row must match the sidebar header height",
);
assertContains(
  "frontend/src/css/sidebar.css",
  "height: var(--tab-bar-height);",
  "sidebar header must use the shared tab bar height",
);
assertContains(
  "frontend/src/css/layout.css",
  "height: var(--tab-bar-height);",
  "tab bar must use the shared sidebar header height",
);
assertContains(
  "frontend/src/js/tabs.js",
  "this._setupScrolling();",
  "tab bar must enable horizontal scroll handling for overflowed tabs",
);
assertContains(
  "frontend/src/js/tabs.js",
  "this._setupTooltip();",
  "tab bar must show rich hover hints for truncated tabs",
);
assertContains(
  "frontend/src/js/tabs.js",
  "removeTab(sessionId) {\n      this._hideTabTooltip();",
  "removing a tab must clear any visible tab tooltip",
);
assertContains(
  "frontend/src/js/tabs.js",
  "async closeTab(sessionId) {\n      this._hideTabTooltip();",
  "closing a tab must clear any visible or pending tab tooltip",
);
assertContains(
  "frontend/src/js/tabs.js",
  "await this.app.closeSessionImmediate(sessionId);",
  "all normal tab closes must use the complete session teardown path",
);
assertContains(
  "frontend/src/js/app.js",
  "this.splitPanes.cleanup(sessionId)",
  "closing an owner tab must release all split-pane state and child sessions",
);
assertContains(
  "frontend/src/js/app.js",
  "window.AlchementaryLink.VNCViewer?.destroyViewer(sessionId)",
  "session teardown must disconnect and remove VNC viewers",
);
assertContains(
  "frontend/src/js/app.js",
  "window.AlchementaryLink.IRCViewer?.destroyViewer(sessionId)",
  "session teardown must dispose IRC terminals and resize observers",
);
assertContains(
  "frontend/src/js/irc.js",
  "wailsService.IRCRooms(this.sessionID)",
  "IRC viewers created after connection must hydrate the already joined rooms",
);
assertContains(
  "internal/sessions/manager.go",
  "GetIRCLastRooms(profile.ID)",
  "saved IRC profiles must restore the channels open in their last session",
);
assertContains(
  "frontend/src/js/app.js",
  "this.recordings.cleanup(sessionId)",
  "session teardown must clear frontend recording state",
);
assertContains(
  "frontend/src/js/splitpane.js",
  "this.app.closeSessionImmediate(leaf.sessionId",
  "closing a split child must release its backend session as well as its terminal",
);
assertContains(
  "frontend/src/js/splitpane.js",
  "await window.AlchementaryLink.wailsService.DisconnectSession(sessionInfo.id)",
  "late split-pane connection results must be disconnected after their pane closes",
);
assertContains(
  "internal/sessions/manager.go",
  "delete(m.sessions, sessionID)",
  "explicit backend disconnect must remove the session from the manager map",
);
assertContains(
  "frontend/src/js/tabs.js",
  "!tab?.isConnected",
  "tab tooltip timers must not show hints for removed tab elements",
);
assertContains(
  "frontend/src/js/tabs.js",
  "_clearTabTooltipTimer",
  "tab tooltip cleanup must cancel pending hover timers",
);
assertContains(
  "frontend/src/js/tabs.js",
  "session.protocol !== \"leviathan\"",
  "Leviathan tabs must close without connected-session confirmation",
);
assertContains(
  "frontend/src/js/tabs.js",
  "session.protocol !== \"github\"",
  "GitHub tabs must close without connected-session confirmation",
);
assertContains(
  "frontend/src/js/tabs.js",
  "Connected since:",
  "connection tab hover hints must include the connected-since timestamp",
);
assertContains(
  "frontend/src/js/tabs.js",
  "tab.appendChild(closeBtn);\n\n      // Protocol badge",
  "tab close button must render on the left side of each tab",
);
assertContains(
  "frontend/src/css/layout.css",
  "scrollbar-gutter: stable;",
  "overflowing tab lists must expose a stable horizontal scrollbar",
);
assertContains(
  "frontend/src/css/tabs.css",
  ".tab-tooltip",
  "tab hover hints must have dedicated tooltip styling",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "Hex Editor",
  "Leviathan mode must put Hex Editor in the tool list",
);
assertContains(
  "frontend/src/js/settings.js",
  "Leviathan Binary Tools",
  "in-app help must document the integrated Leviathan workbench",
);
assertContains(
  "frontend/src/js/settings.js",
  "VNC Connections",
  "in-app help must document VNC connection profiles",
);
assertContains(
  "docs/user-manual.md",
  "## 11. Leviathan binary workbench",
  "user manual must include a full Leviathan section",
);
assertContains(
  "docs/user-manual.md",
  "### VNC",
  "user manual must include VNC connection guidance",
);
assertContains(
  "docs/overview.md",
  "### Leviathan binary workbench",
  "product overview must include Leviathan as a first-class feature group",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "LeviathanSetByte",
  "Leviathan hex view must support byte editing",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "_handleHexByteInput",
  "Leviathan hex byte edits must update while typing",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "_updateHexAsciiCell",
  "Leviathan hex byte edits must update the ASCII preview immediately",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "_focusHexByte(cell, 1)",
  "Leviathan hex byte edits must advance to the next byte after entry",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "spectrogram-encode",
  "Leviathan must expose an image-to-audio spectrogram encoder",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "spectrogram-view",
  "Leviathan must expose an audio spectrogram visualiser",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "preserveAspectRatio",
  "Leviathan spectrogram encoding must expose aspect-ratio preservation",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "aspectRatio",
  "Leviathan spectrogram visualiser must expose display aspect-ratio control",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "spectrogram-insert",
  "Leviathan spectrogram encoder must support inserting images into carrier audio",
);
assertContains(
  "frontend/src/js/app.js",
  "setMode(\"leviathan\")",
  "App mode switch must route to Leviathan",
);
assertBefore(
  "frontend/src/js/splitpane.js",
  "localLabel.textContent = 'Local Machine';",
  "profiles.forEach(function(profile)",
  "split-pane connection choices must put Local Machine before saved profiles",
);
assertContains(
  "frontend/src/js/splitpane.js",
  "wailsService.ConnectLocal()",
  "the Local Machine split-pane choice must open a real local shell",
);
assertContains(
  "internal/api/leviathan.go",
  "LeviathanRunTool",
  "Link API must expose the imported Leviathan engine",
);
assertNotContains(
  "internal/api/leviathan.go",
  "Pattern: \"*\"",
  "Leviathan open dialogs must not use wildcard filters that grey out files on macOS",
);
assertContains(
  "internal/leviathan/spectrogram/spectrogram.go",
  "github.com/hajimehoshi/go-mp3",
  "Leviathan spectrogram visualiser must support MP3 decoding",
);
assertNotContains(
  "wails.json",
  "\"obfuscated\": true",
  "Wails obfuscation must stay disabled because this app embeds frontend/src and uses normal Wails globals",
);
assertNotContains(
  "wails.json",
  "\"garbleargs\"",
  "garble arguments must not be configured when Wails obfuscation is disabled",
);
assertContains(
  "frontend/index.html",
  "wails-service.js",
  "frontend must load the Wails service bridge before app code",
);
assertNotContains(
  "frontend/index.html",
  'type="module" src="/src/js/wails-service.js"',
  "Wails bridge must load as a synchronous classic script before app code",
);
assertContains(
  "frontend/src/js/wails-service.js",
  'prop === "then"',
  "Wails service proxy must not masquerade as a Promise thenable",
);
assertContains(
  "frontend/src/js/wails-service.js",
  "window.go?.api?.Service",
  "Wails service bridge must use the normal named Wails API",
);
assertContains(
  "frontend/src/js/sshkeys.js",
  "textContent: \"Failed to load keys: \" + String(err)",
  "SSH key manager errors must be rendered as text rather than injected HTML",
);
assertContains(
  "frontend/src/js/sshkeys.js",
  "textContent: message",
  "SSH key delete confirmation messages must be rendered as text",
);
assertNotContains(
  "frontend/src/js/sshkeys.js",
  "Failed to load keys: ${err}",
  "SSH key manager must not interpolate backend errors into innerHTML",
);
assertContains(
  "frontend/package.json",
  '"@xterm/xterm":',
  "frontend dependencies must use the maintained scoped xterm package",
);
assertContains(
  "frontend/package.json",
  '"@xterm/addon-fit":',
  "frontend dependencies must use the maintained scoped xterm addons",
);
assertContains(
  "frontend/scripts/copy-assets.js",
  "@xterm/xterm/lib/xterm.js",
  "xterm browser assets must be copied from the maintained scoped package",
);
assertContains(
  "frontend/scripts/copy-assets.js",
  "@xterm/addon-fit/lib/addon-fit.js",
  "xterm addon browser assets must be copied from the maintained scoped packages",
);
assertNotContains(
  "frontend/package.json",
  '"xterm":',
  "deprecated unscoped xterm dependency must not be restored",
);
assertNotContains(
  "frontend/package.json",
  '"xterm-addon-',
  "deprecated unscoped xterm addon dependencies must not be restored",
);
assertNotContains(
  "frontend/package-lock.json",
  '"node_modules/xterm"',
  "package lock must not contain the deprecated unscoped xterm package",
);
assertNotContains(
  "frontend/package-lock.json",
  '"node_modules/xterm-addon-',
  "package lock must not contain deprecated unscoped xterm addon packages",
);
assertContains(
  "frontend/scripts/build.js",
  "sourceMappingURL",
  "frontend production build must remove source-map references",
);
assertContains(
  "frontend/scripts/build.js",
  "bundle-novnc.js",
  "frontend production build must regenerate the noVNC bundle",
);
assertContains(
  "frontend/scripts/novnc-entry.js",
  "initLogging(\"error\")",
  "bundled noVNC logging must default to errors only",
);
assertNotContains(
  "frontend/src/js/app.js",
  "console.debug(\"[session:debug]\"",
  "release UI must not print telnet negotiation debug events",
);
assertNotContains(
  "frontend/src/js/app.js",
  "console.log(\"Backend is ready\")",
  "release UI must not print backend-ready lifecycle events",
);
assertContains(
  "build.sh",
  "LINK_COMPRESS",
  "local builds must expose an explicit compression switch",
);
assertContains(
  "scripts/build-common.sh",
  "link_use_upx_for_target",
  "build scripts must share one UPX target-selection helper",
);
assertContains(
  "build.sh",
  "Skipping UPX compression on macOS",
  "local compressed builds must avoid unsupported macOS UPX by default",
);
assertContains(
  "build.sh",
  "-trimpath",
  "local production builds should trim file paths from binaries",
);
assertContains(
  "scripts/build-common.sh",
  "-buildid=",
  "production builds must strip the Go build ID",
);
assertContains(
  "scripts/build-common.sh",
  "CGO_ENABLED=0",
  "production builds must keep the pure-Go SQLite toolchain",
);
assertContains(
  "Makefile",
  "build-compressed",
  "Makefile must expose an explicit compressed build target",
);
assertContains(
  "Makefile",
  "LINK_FORCE_MACOS_UPX",
  "Makefile compressed builds must make macOS UPX opt-in",
);
assertTreeNotContains(
  "frontend/src/js",
  "window.go.api.Service",
  "app code must not call named Wails backend methods directly",
);

console.log("frontend regression checks passed");
