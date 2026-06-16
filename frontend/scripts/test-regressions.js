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

console.log("frontend regression checks passed");
