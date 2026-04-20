/**
 * copy-assets.js — Copies xterm library assets from node_modules into
 * the frontend source tree so they can be served directly by Wails.
 *
 * Run automatically via the postinstall hook in package.json.
 */
'use strict';

const fs   = require('fs');
const path = require('path');

const frontendDir = path.resolve(__dirname, '..');

// Mapping: source (relative to node_modules) → destination (relative to frontend/)
const assets = [
  {
    src:  'xterm/css/xterm.css',
    dest: 'src/css/lib/xterm.css',
  },
  {
    src:  'xterm/lib/xterm.js',
    dest: 'src/js/lib/xterm.min.js',
  },
  {
    src:  'xterm-addon-fit/lib/xterm-addon-fit.js',
    dest: 'src/js/lib/xterm-addon-fit.min.js',
  },
  {
    src:  'xterm-addon-search/lib/xterm-addon-search.js',
    dest: 'src/js/lib/xterm-addon-search.min.js',
  },
  {
    src:  'xterm-addon-unicode11/lib/xterm-addon-unicode11.js',
    dest: 'src/js/lib/xterm-addon-unicode11.min.js',
  },
  {
    src:  'xterm-addon-web-links/lib/xterm-addon-web-links.js',
    dest: 'src/js/lib/xterm-addon-web-links.min.js',
  },
];

/**
 * Ensures the parent directory of the given file path exists.
 */
function ensureDir(filePath) {
  const dir = path.dirname(filePath);
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }
}

/**
 * Copies a single asset, logging the result.
 */
function copyAsset(entry) {
  const srcPath  = path.join(frontendDir, 'node_modules', entry.src);
  const destPath = path.join(frontendDir, entry.dest);

  if (!fs.existsSync(srcPath)) {
    console.error(`  ✗ Source not found: ${entry.src}`);
    return false;
  }

  ensureDir(destPath);
  fs.copyFileSync(srcPath, destPath);
  console.log(`  ✓ ${entry.src} → ${entry.dest}`);
  return true;
}

// --- Main ---

console.log('[copy-assets] Copying xterm library files…');

let failures = 0;
for (const entry of assets) {
  if (!copyAsset(entry)) {
    failures++;
  }
}

if (failures > 0) {
  console.error(`\n[copy-assets] ${failures} file(s) could not be copied.`);
  console.error('[copy-assets] Have you run "npm install" first?');
  process.exit(1);
}

console.log('[copy-assets] All assets copied successfully.');
