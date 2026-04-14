/**
 * build.js — Build script for the Nexus Terminal frontend.
 *
 * In production mode (no flags): copies vendor assets, assembles the
 * dist/ directory with all files needed by the Wails embed, and validates
 * that every required file is present.
 *
 * In watch mode (--watch): performs the same build, then watches for
 * source changes and prints a notification when files change.  Wails dev
 * mode handles the actual live-reload; this script simply keeps the
 * process alive so the frontend:dev:watcher contract is satisfied.
 */
'use strict';

const fs   = require('fs');
const path = require('path');
const { execSync } = require('child_process');

const frontendDir = path.resolve(__dirname, '..');
const distDir     = path.join(frontendDir, 'dist');
const watchMode   = process.argv.includes('--watch');

// Files that must exist after a successful build (relative to frontend/)
const requiredFiles = [
  'dist/index.html',
  'dist/src/css/lib/xterm.css',
  'dist/src/js/lib/xterm.min.js',
  'dist/src/js/lib/xterm-addon-fit.min.js',
  'dist/src/js/lib/xterm-addon-search.min.js',
  'dist/src/js/lib/xterm-addon-web-links.min.js',
];

/**
 * Recursively copies a directory tree, creating parents as needed.
 */
function copyDirSync(src, dest) {
  fs.mkdirSync(dest, { recursive: true });
  for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
    const srcPath  = path.join(src, entry.name);
    const destPath = path.join(dest, entry.name);
    if (entry.isDirectory()) {
      copyDirSync(srcPath, destPath);
    } else {
      fs.copyFileSync(srcPath, destPath);
    }
  }
}

/**
 * Removes a directory tree if it exists.
 */
function cleanDir(dir) {
  if (fs.existsSync(dir)) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

/**
 * Runs the asset copy script to pull xterm files into src/js/lib/ and
 * src/css/lib/.
 */
function copyAssets() {
  console.log('[build] Copying vendor library assets…');
  execSync('node scripts/copy-assets.js', {
    cwd:   frontendDir,
    stdio: 'inherit',
  });
}

/**
 * Assembles the dist/ directory with everything the Wails embed needs:
 *   dist/index.html
 *   dist/src/css/  (application + lib stylesheets)
 *   dist/src/js/   (application + lib scripts)
 *   dist/src/components/  (if present)
 *   dist/src/assets/      (if present)
 */
function buildDist() {
  console.log('[build] Assembling dist/ directory…');

  // Start fresh so stale files are removed
  cleanDir(distDir);
  fs.mkdirSync(distDir, { recursive: true });

  // Copy the root index.html
  fs.copyFileSync(
    path.join(frontendDir, 'index.html'),
    path.join(distDir, 'index.html'),
  );

  // Copy the entire src/ tree (CSS, JS, components, assets, lib files)
  const srcDir = path.join(frontendDir, 'src');
  if (fs.existsSync(srcDir)) {
    copyDirSync(srcDir, path.join(distDir, 'src'));
  }

  console.log('[build] dist/ assembled.');
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
  // On Linux this falls back to non-recursive — acceptable for dev use.
  try {
    fs.watch(srcDir, { recursive: true }, (_event, filename) => {
      if (filename) {
        const stamp = new Date().toLocaleTimeString('en-GB');
        console.log(`[build] ${stamp} — changed: ${filename}`);
      }
    });
  } catch (err) {
    console.warn('[build] Recursive watch not supported; polling fallback.');
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

buildDist();

if (!validate()) {
  console.error('[build] Validation failed — some required files are missing.');
  process.exit(1);
}

console.log('[build] Frontend build complete.');

if (watchMode) {
  watch();
}
