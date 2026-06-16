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

console.log("frontend regression checks passed");
