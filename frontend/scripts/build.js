/**
 * build.js — Build script for the Nexus Terminal frontend.
 *
 * In production mode (no flags): copies assets and validates that all
 * required files are in place.
 *
 * In watch mode (--watch): copies assets, then watches for source changes
 * and prints a notification when files change. Wails dev mode handles the
 * actual live-reload; this script simply keeps the process alive so the
 * frontend:dev:watcher contract is satisfied.
 */
'use strict';

const fs   = require('fs');
const path = require('path');
const { execSync } = require('child_process');

const frontendDir = path.resolve(__dirname, '..');
const watchMode   = process.argv.includes('--watch');

// Files that must exist after a successful build
const requiredFiles = [
  'dist/index.html',
  'src/css/lib/xterm.css',
  'src/js/lib/xterm.min.js',
  'src/js/lib/xterm-addon-fit.min.js',
  'src/js/lib/xterm-addon-search.min.js',
  'src/js/lib/xterm-addon-web-links.min.js',
];

/**
 * Runs the asset copy script to ensure library files are present.
 */
function copyAssets() {
  console.log('[build] Copying library assets…');
  execSync('node scripts/copy-assets.js', {
    cwd:   frontendDir,
    stdio: 'inherit',
  });
}

/**
 * Validates that every required file exists.
 * Returns true if all files are present.
 */
function validate() {
  console.log('[build] Validating required files…');
  let valid = true;
  for (const relPath of requiredFiles) {
    const full = path.join(frontendDir, relPath);
    if (!fs.existsSync(full)) {
      console.error(`  ✗ Missing: ${relPath}`);
      valid = false;
    } else {
      console.log(`  ✓ ${relPath}`);
    }
  }
  return valid;
}

/**
 * Watches the src/ directory for changes (development mode).
 * Wails dev mode picks up the changes; we just keep the process alive.
 */
function watch() {
  const srcDir = path.join(frontendDir, 'src');
  console.log(`[build] Watching ${srcDir} for changes…`);

  // Use fs.watch recursively where supported (macOS, Windows).
  // On Linux, this falls back to non-recursive — acceptable for dev use.
  try {
    fs.watch(srcDir, { recursive: true }, (_event, filename) => {
      if (filename) {
        const stamp = new Date().toLocaleTimeString('en-GB');
        console.log(`[build] ${stamp} — changed: ${filename}`);
      }
    });
  } catch (err) {
    console.warn('[build] Recursive watch not supported; polling fallback.');
    // Keep the process alive even if watch fails
  }

  // Prevent the process from exiting
  setInterval(() => {}, 60_000);
}

// --- Main ---

try {
  copyAssets();
} catch (err) {
  console.error('[build] Asset copy failed:', err.message);
  process.exit(1);
}

if (!validate()) {
  console.error('[build] Validation failed — some required files are missing.');
  process.exit(1);
}

console.log('[build] Frontend build complete.');

if (watchMode) {
  watch();
}
