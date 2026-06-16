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
  "Leviathan mode toggle must be present beside Nexus Terminal",
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
  "frontend/src/js/leviathan.js",
  "Hex Editor",
  "Leviathan mode must put Hex Editor in the tool list",
);
assertContains(
  "frontend/src/js/leviathan.js",
  "LeviathanSetByte",
  "Leviathan hex view must support byte editing",
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
assertContains(
  "internal/api/leviathan.go",
  "LeviathanRunTool",
  "Nexus API must expose the imported Leviathan engine",
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
  "NEXUS_COMPRESS",
  "local builds must expose an explicit compression switch",
);
assertContains(
  "scripts/build-common.sh",
  "nexus_use_upx_for_target",
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
  "-O2 -g0",
  "production builds must remove cgo debug output by default",
);
assertContains(
  "Makefile",
  "build-compressed",
  "Makefile must expose an explicit compressed build target",
);
assertContains(
  "Makefile",
  "NEXUS_FORCE_MACOS_UPX",
  "Makefile compressed builds must make macOS UPX opt-in",
);
assertTreeNotContains(
  "frontend/src/js",
  "window.go.api.Service",
  "app code must not call named Wails backend methods directly",
);

console.log("frontend regression checks passed");
