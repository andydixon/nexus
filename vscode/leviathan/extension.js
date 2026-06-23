'use strict';

const vscode = require('vscode');
const cp = require('child_process');
const fs = require('fs');
const http = require('http');
const path = require('path');

const TOOLS = [
  ['Editor', 'hex', 'Hex Editor', 'Browse and patch bytes in the working copy.'],
  ['Triage', 'overview', 'Overview', 'Fast first-pass summary across hashes, strings, entropy and IOCs.'],
  ['Triage', 'hashes', 'Hashes', 'CRC32, MD5, SHA-1, SHA-256 and SHA-512.'],
  ['Triage', 'entropy', 'Entropy', 'Windowed entropy for packed or encrypted regions.'],
  ['Triage', 'strings', 'Strings', 'ASCII and UTF-16 string extraction.'],
  ['Triage', 'iocs', 'IOCs', 'URLs, domains, hashes, paths and indicators.'],
  ['Find', 'search', 'Search', 'Text, hex or regular-expression search.'],
  ['Find', 'multi-search', 'Multi-search', 'Batch text, hex and regex searches.'],
  ['Structure', 'parse', 'Structures', 'PE, ELF, Mach-O, ZIP, images, RIFF, ASN.1 and certificates.'],
  ['Structure', 'disasm', 'Disasm', 'Heuristic disassembly for common CPU families.'],
  ['Structure', 'pdf', 'PDF', 'Objects, streams, xrefs and suspicious PDF surface.'],
  ['Structure', 'squashfs', 'SquashFS', 'Superblock and directory metadata.'],
  ['Audio', 'spectrogram-encode', 'Spectrogram Encode', 'Convert images into WAV spectrogram payloads.'],
  ['Audio', 'spectrogram-view', 'Spectrogram View', 'Render audio files as spectrogram images.'],
  ['Compare', 'diff', 'Diff', 'Changed byte regions against a compare file.'],
  ['Recover', 'carve', 'Carve', 'Embedded file carving candidates.'],
  ['Recover', 'keys', 'Keys', 'PEM, DER, OpenSSH and PKCS#12 artefact discovery.'],
  ['Recover', 'timestamps', 'Timestamps', 'UNIX, FILETIME, WebKit and OLE timestamp heuristics.'],
  ['Recover', 'stego', 'Stego', 'Appended payloads and embedded trailer formats.'],
  ['Recover', 'metadata', 'Metadata', 'PNG, JPEG and GIF textual metadata extraction.'],
  ['Advanced', 'binsnoop', 'Binsnoop', 'Deep signature scanning through Binsnoop.'],
  ['Advanced', 'rules', 'Rules', 'YARA-like rule scans over the current document.'],
  ['Edit', 'patch-plan', 'Patch Plan', 'Validate patch scripts without modifying bytes.'],
  ['Edit', 'patch-apply', 'Patch Apply', 'Apply a patch script to the working copy.'],
  ['Edit', 'transform', 'Transforms', 'Hex, Base64, URL, XOR, inflate and cipher pipelines.'],
  ['Case', 'case', 'Case Export', 'Build a case archive payload from the working copy.']
].map(([group, id, name, desc]) => ({ group, id, name, desc }));

const DEFAULTS = {
  search: { mode: 'text', query: 'MZ', max: 1000 },
  'multi-search': { spec: 'text MZ\nhex 7f454c46\nregex https?://', max: 1000 },
  parse: { kind: 'auto' },
  disasm: { arch: 'x86_64', start: 0, length: 4096, max: 1000 },
  entropy: { window: 4096, step: 4096 },
  strings: { min: 4, max: 5000 },
  iocs: { min: 5 },
  carve: { max: 256 },
  keys: { max: 200 },
  timestamps: { max: 1000 },
  rules: { source: 'rule mz { strings: $a = "MZ" condition: $a }', max: 1000 },
  'patch-plan': { source: 'set 0x00 90 90' },
  'patch-apply': { source: 'set 0x00 90 90' },
  transform: { pipeline: 'hex.encode', start: 0, length: 1024 },
  'spectrogram-view': { fftSize: 2048, height: 1024, maxWidth: 1400, aspectRatio: 1, minHz: 0, maxHz: 0, brightness: 1, contrast: 1 },
  hex: { start: 0, bytes: 2048, width: 16 }
};

class Bridge {
  constructor(context) {
    this.context = context;
    this.proc = null;
    this.addr = null;
    this.token = null;
  }

  async start() {
    if (this.addr) return;
    const exe = process.platform === 'win32' ? 'nexus-vscode-bridge.exe' : 'nexus-vscode-bridge';
    const bridgePath = path.join(this.context.extensionPath, 'bin', exe);
    if (!fs.existsSync(bridgePath)) {
      throw new Error(`Bridge executable not found: ${bridgePath}. Run vscode/build.sh first.`);
    }
    this.proc = cp.spawn(bridgePath, ['-listen', '127.0.0.1:0'], {
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true
    });
    this.context.subscriptions.push({ dispose: () => this.stop() });
    const firstLine = await new Promise((resolve, reject) => {
      let buf = '';
      const timer = setTimeout(() => reject(new Error('Timed out waiting for bridge startup')), 10000);
      this.proc.stdout.on('data', chunk => {
        buf += chunk.toString('utf8');
        const idx = buf.indexOf('\n');
        if (idx >= 0) {
          clearTimeout(timer);
          resolve(buf.slice(0, idx));
        }
      });
      this.proc.once('error', reject);
      this.proc.once('exit', code => reject(new Error(`Bridge exited during startup (${code})`)));
    });
    const meta = JSON.parse(firstLine);
    this.addr = meta.addr;
    this.token = meta.token;
  }

  stop() {
    if (this.proc && !this.proc.killed) this.proc.kill();
    this.proc = null;
  }

  async rpc(method, args = []) {
    await this.start();
    const payload = Buffer.from(JSON.stringify({ method, args }));
    return new Promise((resolve, reject) => {
      const req = http.request({
        hostname: this.addr.split(':')[0],
        port: Number(this.addr.split(':')[1]),
        path: '/rpc',
        method: 'POST',
        headers: {
          Authorization: `Bearer ${this.token}`,
          'Content-Type': 'application/json',
          'Content-Length': payload.length
        }
      }, res => {
        let data = '';
        res.on('data', chunk => data += chunk);
        res.on('end', () => {
          if (res.statusCode < 200 || res.statusCode >= 300) {
            reject(new Error(`Bridge HTTP ${res.statusCode}: ${data}`));
            return;
          }
          const decoded = JSON.parse(data || '{}');
          if (decoded.error) reject(new Error(decoded.error));
          else resolve(decoded.result);
        });
      });
      req.on('error', reject);
      req.write(payload);
      req.end();
    });
  }
}

class ToolsView {
  constructor(context, bridge) {
    this.context = context;
    this.bridge = bridge;
    this.view = null;
    this.state = null;
  }

  resolveWebviewView(view) {
    this.view = view;
    view.webview.options = { enableScripts: true };
    view.webview.html = this.html(view.webview);
    view.webview.onDidReceiveMessage(async msg => {
      try {
        if (msg.type === 'load') await loadFile(this.bridge, false, this);
        if (msg.type === 'compare') await loadFile(this.bridge, true, this);
        if (msg.type === 'save') await saveFile(this.bridge, this);
        if (msg.type === 'refresh') await this.refreshState();
        if (msg.type === 'tool') await openTool(this.context, this.bridge, this, msg.tool);
      } catch (err) {
        this.postError(err);
      }
    });
    this.refreshState();
  }

  async refreshState() {
    try {
      this.state = await this.bridge.rpc('LeviathanState');
    } catch (_) {
      this.state = null;
    }
    this.postState();
  }

  postState() {
    if (this.view) this.view.webview.postMessage({ type: 'state', state: this.state });
  }

  postError(err) {
    if (this.view) this.view.webview.postMessage({ type: 'error', error: err.message || String(err) });
    else vscode.window.showErrorMessage(err.message || String(err));
  }

  html(webview) {
    const nonce = String(Date.now());
    const toolsJson = JSON.stringify(TOOLS);
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body {
      margin: 0;
      color: var(--vscode-foreground);
      background: var(--vscode-sideBar-background);
      font: var(--vscode-font-size) var(--vscode-font-family);
    }
    .shell { display: grid; gap: 10px; padding: 10px; }
    .actions { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
    button {
      border: 1px solid var(--vscode-button-border, transparent);
      background: var(--vscode-button-secondaryBackground);
      color: var(--vscode-button-secondaryForeground);
      padding: 5px 8px;
      min-height: 26px;
      font: inherit;
      cursor: pointer;
    }
    button:hover { filter: brightness(1.08); }
    button.primary { background: var(--vscode-button-background); color: var(--vscode-button-foreground); }
    .state {
      border: 1px solid var(--vscode-panel-border);
      background: var(--vscode-editorWidget-background);
      padding: 8px;
      overflow-wrap: anywhere;
    }
    .state strong { display: block; line-height: 18px; }
    .state span { color: var(--vscode-descriptionForeground); font-size: 11px; }
    .search {
      width: 100%;
      box-sizing: border-box;
      border: 1px solid var(--vscode-input-border, transparent);
      background: var(--vscode-input-background);
      color: var(--vscode-input-foreground);
      padding: 6px 8px;
      outline: none;
    }
    .search:focus { border-color: var(--vscode-focusBorder); }
    .segments { display: flex; flex-wrap: wrap; gap: 5px; }
    .seg {
      min-height: 23px;
      padding: 3px 7px;
      background: transparent;
      color: var(--vscode-descriptionForeground);
      border: 1px solid var(--vscode-panel-border);
      font-size: 11px;
    }
    .seg.active {
      background: var(--vscode-button-background);
      color: var(--vscode-button-foreground);
      border-color: transparent;
    }
    .group {
      display: flex;
      justify-content: space-between;
      margin: 10px 0 5px;
      color: var(--vscode-descriptionForeground);
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: .04em;
    }
    .tool {
      width: 100%;
      display: grid;
      grid-template-columns: auto 1fr;
      gap: 8px;
      text-align: left;
      border: 1px solid var(--vscode-panel-border);
      background: var(--vscode-editor-background);
      color: var(--vscode-foreground);
      padding: 8px;
      margin-bottom: 7px;
    }
    .tool:hover { border-color: var(--vscode-focusBorder); background: var(--vscode-list-hoverBackground); }
    .mark {
      width: 30px;
      height: 28px;
      display: grid;
      place-items: center;
      background: var(--vscode-badge-background);
      color: var(--vscode-badge-foreground);
      border: 1px solid var(--vscode-panel-border);
      font-size: 10px;
      font-weight: 700;
    }
    .name { font-weight: 600; line-height: 17px; }
    .desc { color: var(--vscode-descriptionForeground); font-size: 11px; line-height: 15px; }
    .empty {
      border: 1px dashed var(--vscode-panel-border);
      color: var(--vscode-descriptionForeground);
      text-align: center;
      padding: 16px 10px;
    }
    .status { color: var(--vscode-descriptionForeground); font-size: 11px; min-height: 15px; }
  </style>
</head>
<body>
  <div class="shell">
    <div class="actions">
      <button class="primary" id="load">Load File</button>
      <button id="compare">Compare</button>
      <button id="save">Save Copy</button>
      <button id="refresh">Refresh</button>
    </div>
    <div class="state" id="state"><strong>No file loaded</strong><span>Load a binary or right-click a file in Explorer.</span></div>
    <input id="search" class="search" placeholder="Search tools">
    <div id="segments" class="segments"></div>
    <div id="tools"></div>
    <div id="status" class="status"></div>
  </div>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    const tools = ${toolsJson};
    let state = null;
    let filter = 'all';
    const groups = ['all', ...Array.from(new Set(tools.map(t => t.group)))];
    const search = document.getElementById('search');
    document.getElementById('load').onclick = () => vscode.postMessage({ type: 'load' });
    document.getElementById('compare').onclick = () => vscode.postMessage({ type: 'compare' });
    document.getElementById('save').onclick = () => vscode.postMessage({ type: 'save' });
    document.getElementById('refresh').onclick = () => vscode.postMessage({ type: 'refresh' });
    function esc(value) {
      return String(value == null ? '' : value).replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
    }
    function mark(tool) {
      return tool.name.split(/\\s+/).map(part => part[0]).join('').slice(0, 3).toUpperCase();
    }
    function renderState() {
      const el = document.getElementById('state');
      if (!state || !state.name) {
        el.innerHTML = '<strong>No file loaded</strong><span>Load a binary or right-click a file in Explorer.</span>';
        return;
      }
      el.innerHTML = '<strong>' + esc(state.name) + '</strong><span>' + Number(state.size || 0).toLocaleString() + ' bytes' + (state.dirty ? ' • modified' : '') + (state.compare ? ' • compare loaded' : '') + '</span>';
    }
    function renderSegments() {
      const root = document.getElementById('segments');
      root.textContent = '';
      for (const group of groups) {
        const count = group === 'all' ? tools.length : tools.filter(t => t.group === group).length;
        const btn = document.createElement('button');
        btn.className = 'seg' + (filter === group ? ' active' : '');
        btn.textContent = group === 'all' ? 'All ' + count : group + ' ' + count;
        btn.onclick = () => { filter = group; renderTools(); renderSegments(); };
        root.appendChild(btn);
      }
    }
    function visibleTools() {
      const q = search.value.trim().toLowerCase();
      return tools.filter(tool => {
        if (filter !== 'all' && tool.group !== filter) return false;
        if (!q) return true;
        return (tool.name + ' ' + tool.group + ' ' + tool.desc).toLowerCase().includes(q);
      });
    }
    function renderTools() {
      const root = document.getElementById('tools');
      const visible = visibleTools();
      if (!visible.length) {
        root.innerHTML = '<div class="empty">No matching tools</div>';
        return;
      }
      const byGroup = new Map();
      for (const tool of visible) {
        if (!byGroup.has(tool.group)) byGroup.set(tool.group, []);
        byGroup.get(tool.group).push(tool);
      }
      root.innerHTML = [...byGroup.entries()].map(([group, items]) =>
        '<div class="group"><span>' + esc(group) + '</span><span>' + items.length + '</span></div>' +
        items.map(tool => '<button class="tool" data-tool="' + esc(tool.id) + '"><span class="mark">' + esc(mark(tool)) + '</span><span><span class="name">' + esc(tool.name) + '</span><br><span class="desc">' + esc(tool.desc) + '</span></span></button>').join('')
      ).join('');
    }
    document.getElementById('tools').addEventListener('click', event => {
      const btn = event.target.closest('[data-tool]');
      if (btn) vscode.postMessage({ type: 'tool', tool: btn.dataset.tool });
    });
    search.addEventListener('input', renderTools);
    renderSegments();
    renderTools();
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'state') {
        state = msg.state;
        renderState();
      }
      if (msg.type === 'error') document.getElementById('status').textContent = msg.error || '';
    });
    vscode.postMessage({ type: 'refresh' });
  </script>
</body>
</html>`;
  }
}

class ToolPanel {
  constructor(context, bridge, tool, options = {}) {
    this.context = context;
    this.bridge = bridge;
    this.tool = tool;
    this.fileState = options.fileState || null;
    const meta = TOOLS.find(t => t.id === tool) || { name: tool };
    const title = this.fileState?.name ? `${meta.name}: ${this.fileState.name}` : meta.name;
    this.panel = vscode.window.createWebviewPanel('leviathanTool', title, vscode.ViewColumn.Active, { enableScripts: true, retainContextWhenHidden: true });
    this.panel.webview.html = this.tool === 'hex' ? this.hexHtml(meta) : this.html(meta);
    this.panel.webview.onDidReceiveMessage(msg => {
      if (msg.type === 'run') this.run(msg.params);
      if (msg.type === 'hexLoad') this.loadHex(msg);
      if (msg.type === 'hexSet') this.setHexByte(msg);
      if (msg.type === 'hexSearch') this.searchHex(msg);
      if (msg.type === 'undo') this.stateAction('LeviathanUndo', msg);
      if (msg.type === 'redo') this.stateAction('LeviathanRedo', msg);
      if (msg.type === 'revert') this.stateAction('LeviathanRevert', msg);
    });
    this.panel.webview.postMessage({ type: 'fileState', state: this.fileState });
    this.refreshState();
    if (options.autoRun) {
      if (this.tool === 'hex') {
        setTimeout(() => this.loadHex(DEFAULTS.hex), 150);
      } else {
        setTimeout(() => this.run(JSON.stringify(DEFAULTS[this.tool] || {})), 150);
      }
    }
  }

  async refreshState() {
    try {
      const state = await this.bridge.rpc('LeviathanState');
      this.fileState = state;
      this.panel.webview.postMessage({ type: 'fileState', state });
    } catch (err) {
      this.error(err);
    }
  }

  async run(paramsText) {
    try {
      const params = JSON.parse(paramsText || '{}');
      const result = await this.bridge.rpc('LeviathanRunTool', [this.tool, params]);
      this.panel.webview.postMessage({ type: 'result', result });
    } catch (err) {
      this.error(err);
    }
  }

  error(err) {
    this.panel.webview.postMessage({ type: 'error', error: err.message || String(err) });
  }

  async loadHex(params = {}) {
    try {
      const start = Number(params.start || 0);
      const bytes = Number(params.bytes || 2048);
      const width = Number(params.width || 16);
      const result = await this.bridge.rpc('LeviathanRunTool', ['hex', { start, bytes, width }]);
      this.panel.webview.postMessage({ type: 'hexRows', rows: result.rows || [], start, bytes, width });
      await this.refreshState();
    } catch (err) {
      this.error(err);
    }
  }

  async setHexByte(msg) {
    try {
      const offset = Number(msg.offset);
      const value = String(msg.value || '').trim();
      const payload = await this.bridge.rpc('LeviathanSetByte', [offset, value]);
      if (payload && payload.state) {
        this.fileState = payload.state;
        this.panel.webview.postMessage({ type: 'fileState', state: payload.state });
      }
      await this.loadHex({ start: msg.start, bytes: msg.bytes, width: msg.width });
    } catch (err) {
      this.panel.webview.postMessage({ type: 'hexEditError', offset: msg.offset, error: err.message || String(err) });
    }
  }

  async searchHex(msg) {
    try {
      const mode = msg.mode === 'hex' ? 'hex' : 'text';
      const query = String(msg.query || '');
      const result = await this.bridge.rpc('LeviathanRunTool', ['search', { mode, query, max: 2000 }]);
      this.panel.webview.postMessage({ type: 'hexSearchResults', hits: result.hits || [], mode, query });
    } catch (err) {
      this.error(err);
    }
  }

  async stateAction(method, msg = {}) {
    try {
      const state = await this.bridge.rpc(method);
      this.panel.webview.postMessage({ type: 'state', state });
      this.panel.webview.postMessage({ type: 'fileState', state });
      if (this.tool === 'hex') {
        await this.loadHex(msg.start === undefined ? DEFAULTS.hex : msg);
      }
    } catch (err) {
      this.error(err);
    }
  }

  hexHtml(meta) {
    const nonce = String(Date.now());
    const initialFileState = JSON.stringify(this.fileState || null);
    const name = escapeHtml(meta.name);
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body {
      margin: 0;
      color: var(--vscode-foreground);
      background: var(--vscode-editor-background);
      font: var(--vscode-font-size) var(--vscode-font-family);
      overflow: hidden;
    }
    header {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 8px;
      padding: 9px 10px;
      border-bottom: 1px solid var(--vscode-panel-border);
    }
    h1 { margin: 0; font-size: 15px; line-height: 19px; }
    .file-state { color: var(--vscode-descriptionForeground); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .toolbar {
      display: grid;
      grid-template-columns: auto auto auto auto 1fr auto auto;
      gap: 7px;
      align-items: center;
      padding: 8px 10px;
      border-bottom: 1px solid var(--vscode-panel-border);
      background: var(--vscode-sideBar-background);
    }
    .searchbar {
      display: grid;
      grid-template-columns: auto 1fr auto auto auto;
      gap: 7px;
      align-items: center;
      padding: 8px 10px;
      border-bottom: 1px solid var(--vscode-panel-border);
      background: var(--vscode-sideBar-background);
    }
    input, select {
      min-width: 0;
      color: var(--vscode-input-foreground);
      background: var(--vscode-input-background);
      border: 1px solid var(--vscode-input-border, transparent);
      padding: 5px 7px;
      font: 12px Menlo, Monaco, Consolas, "Courier New", monospace;
      box-sizing: border-box;
    }
    input:focus, select:focus { outline: 1px solid var(--vscode-focusBorder); }
    button {
      border: 1px solid var(--vscode-button-border, transparent);
      background: var(--vscode-button-secondaryBackground);
      color: var(--vscode-button-secondaryForeground);
      padding: 5px 9px;
      min-height: 26px;
      font: inherit;
      cursor: pointer;
    }
    button.primary { background: var(--vscode-button-background); color: var(--vscode-button-foreground); }
    .seg { display: inline-flex; border: 1px solid var(--vscode-panel-border); }
    .seg button { border: 0; border-right: 1px solid var(--vscode-panel-border); background: transparent; }
    .seg button:last-child { border-right: 0; }
    .seg button.active { background: var(--vscode-button-background); color: var(--vscode-button-foreground); }
    main { height: calc(100vh - 132px); overflow: auto; }
    table { width: 100%; border-collapse: collapse; font: 12px Menlo, Monaco, Consolas, "Courier New", monospace; }
    th, td { border-bottom: 1px solid var(--vscode-panel-border); padding: 3px 6px; vertical-align: top; }
    th { position: sticky; top: 0; background: var(--vscode-editor-background); z-index: 1; text-align: left; color: var(--vscode-descriptionForeground); }
    .offset { width: 88px; color: var(--vscode-descriptionForeground); user-select: text; }
    .hex-cell {
      width: 2.35ch;
      margin-right: .2ch;
      padding: 1px 2px;
      border: 1px solid transparent;
      background: transparent;
      color: var(--vscode-foreground);
      text-align: center;
      text-transform: uppercase;
      font: 12px Menlo, Monaco, Consolas, "Courier New", monospace;
    }
    .hex-cell:focus { border-color: var(--vscode-focusBorder); background: var(--vscode-input-background); outline: none; }
    .hex-cell.dirty { color: var(--vscode-terminal-ansiYellow); }
    .hex-cell.hit, .ascii-char.hit { background: var(--vscode-editor-findMatchHighlightBackground); }
    .ascii {
      white-space: pre;
      color: var(--vscode-descriptionForeground);
      letter-spacing: .08em;
      min-width: 34ch;
    }
    .ascii-char { display: inline-block; min-width: 1ch; }
    .status { color: var(--vscode-descriptionForeground); font-size: 12px; }
  </style>
</head>
<body>
  <header>
    <div><h1>${name}</h1><div id="fileState" class="file-state">Checking loaded file...</div></div>
    <div><button id="undo">Undo</button><button id="redo">Redo</button><button id="revert">Revert</button></div>
  </header>
  <section class="toolbar">
    <label>Offset <input id="start" value="0" size="10"></label>
    <label>Bytes <input id="bytes" value="2048" size="8"></label>
    <span class="seg"><button id="width16" class="active">16</button><button id="width32">32</button></span>
    <button id="prev">Prev</button>
    <button id="next">Next</button>
    <button id="load" class="primary">Load</button>
    <span id="status" class="status"></span>
  </section>
  <section class="searchbar">
    <select id="searchMode"><option value="hex">Hex</option><option value="ascii">ASCII</option></select>
    <input id="searchQuery" placeholder="Search hex bytes or ASCII text">
    <button id="search">Search</button>
    <button id="searchPrev">Prev Hit</button>
    <button id="searchNext">Next Hit</button>
  </section>
  <main>
    <table>
      <thead><tr><th class="offset">Offset</th><th>Hex</th><th>ASCII</th></tr></thead>
      <tbody id="rows"></tbody>
    </table>
  </main>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    const initialFileState = ${initialFileState};
    let width = 16;
    let rows = [];
    let hits = [];
    let hitIndex = -1;
    const changed = new Map();
    function esc(value) {
      return String(value == null ? '' : value).replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
    }
    function parseNum(value, fallback) {
      const text = String(value || '').trim();
      if (!text) return fallback;
      const n = Number(text.startsWith('0x') || text.startsWith('0X') ? parseInt(text, 16) : parseInt(text, 10));
      return Number.isFinite(n) ? n : fallback;
    }
    function printable(hex) {
      const n = parseInt(hex, 16);
      return n >= 32 && n < 127 ? String.fromCharCode(n) : '.';
    }
    function currentParams() {
      return {
        start: parseNum(document.getElementById('start').value, 0),
        bytes: parseNum(document.getElementById('bytes').value, 2048),
        width
      };
    }
    function renderFileState(s) {
      document.getElementById('fileState').textContent = s && s.name ? s.name + ' • ' + Number(s.size || 0).toLocaleString() + ' bytes' + (s.dirty ? ' • modified' : '') : 'No file loaded';
    }
    function hitOffsets() {
      const set = new Set();
      for (const hit of hits) {
        const start = Number(hit.offset || hit.start || 0);
        const length = Math.max(1, Number(hit.length || 1));
        for (let i = 0; i < length; i++) set.add(start + i);
      }
      return set;
    }
    function renderRows() {
      const hitSet = hitOffsets();
      const body = document.getElementById('rows');
      body.textContent = '';
      for (const row of rows) {
        const tr = document.createElement('tr');
        const off = Number(row.offset || 0);
        const bytes = Array.isArray(row.bytes) ? row.bytes : String(row.hex || '').split(/\\s+/).filter(Boolean);
        const hexTd = document.createElement('td');
        const asciiTd = document.createElement('td');
        asciiTd.className = 'ascii';
        bytes.forEach((byte, idx) => {
          const absolute = off + idx;
          const input = document.createElement('input');
          input.className = 'hex-cell' + (changed.has(absolute) ? ' dirty' : '') + (hitSet.has(absolute) ? ' hit' : '');
          input.maxLength = 2;
          input.value = String(changed.get(absolute) || byte).toUpperCase();
          input.dataset.offset = String(absolute);
          input.addEventListener('input', () => {
            input.value = input.value.replace(/[^0-9a-fA-F]/g, '').slice(0, 2).toUpperCase();
            updateAsciiFromInputs(tr);
          });
          input.addEventListener('change', () => commitByte(input));
          input.addEventListener('keydown', event => {
            if (event.key === 'Enter') { event.preventDefault(); input.blur(); }
          });
          hexTd.appendChild(input);
        });
        tr.innerHTML = '<td class="offset">0x' + off.toString(16).padStart(8, '0') + '</td>';
        tr.appendChild(hexTd);
        tr.appendChild(asciiTd);
        body.appendChild(tr);
        updateAsciiFromInputs(tr);
      }
    }
    function updateAsciiFromInputs(tr) {
      const asciiTd = tr.querySelector('.ascii');
      asciiTd.textContent = '';
      for (const input of tr.querySelectorAll('.hex-cell')) {
        const span = document.createElement('span');
        span.className = 'ascii-char' + (input.classList.contains('hit') ? ' hit' : '');
        span.textContent = /^[0-9A-Fa-f]{2}$/.test(input.value) ? printable(input.value) : '.';
        asciiTd.appendChild(span);
      }
    }
    function commitByte(input) {
      if (!/^[0-9A-Fa-f]{2}$/.test(input.value)) {
        document.getElementById('status').textContent = 'Byte must be two hex digits';
        return;
      }
      const offset = Number(input.dataset.offset);
      changed.set(offset, input.value.toUpperCase());
      input.classList.add('dirty');
      vscode.postMessage({ type: 'hexSet', offset, value: input.value, ...currentParams() });
    }
    function load() {
      document.getElementById('status').textContent = 'Loading...';
      vscode.postMessage({ type: 'hexLoad', ...currentParams() });
    }
    function gotoHit(delta) {
      if (!hits.length) return;
      hitIndex = (hitIndex + delta + hits.length) % hits.length;
      const offset = Number(hits[hitIndex].offset || hits[hitIndex].start || 0);
      const aligned = Math.max(0, Math.floor(offset / width) * width);
      document.getElementById('start').value = String(aligned);
      load();
    }
    document.getElementById('width16').onclick = () => { width = 16; document.getElementById('width16').classList.add('active'); document.getElementById('width32').classList.remove('active'); load(); };
    document.getElementById('width32').onclick = () => { width = 32; document.getElementById('width32').classList.add('active'); document.getElementById('width16').classList.remove('active'); load(); };
    document.getElementById('load').onclick = load;
    document.getElementById('prev').onclick = () => { const p = currentParams(); document.getElementById('start').value = String(Math.max(0, p.start - p.bytes)); load(); };
    document.getElementById('next').onclick = () => { const p = currentParams(); document.getElementById('start').value = String(p.start + p.bytes); load(); };
    document.getElementById('search').onclick = () => {
      const mode = document.getElementById('searchMode').value === 'hex' ? 'hex' : 'ascii';
      vscode.postMessage({ type: 'hexSearch', mode, query: document.getElementById('searchQuery').value });
    };
    document.getElementById('searchPrev').onclick = () => gotoHit(-1);
    document.getElementById('searchNext').onclick = () => gotoHit(1);
    document.getElementById('undo').onclick = () => vscode.postMessage({ type: 'undo', ...currentParams() });
    document.getElementById('redo').onclick = () => vscode.postMessage({ type: 'redo', ...currentParams() });
    document.getElementById('revert').onclick = () => vscode.postMessage({ type: 'revert', ...currentParams() });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'fileState') renderFileState(msg.state);
      if (msg.type === 'hexRows') {
        rows = msg.rows || [];
        width = Number(msg.width || width);
        renderRows();
        document.getElementById('status').textContent = rows.length ? rows.length + ' rows' : 'No bytes in range';
      }
      if (msg.type === 'hexSearchResults') {
        hits = msg.hits || [];
        hitIndex = hits.length ? 0 : -1;
        document.getElementById('status').textContent = hits.length + ' hit' + (hits.length === 1 ? '' : 's');
        if (hits.length) {
          const offset = Number(hits[0].offset || hits[0].start || 0);
          document.getElementById('start').value = String(Math.max(0, Math.floor(offset / width) * width));
          load();
        } else {
          renderRows();
        }
      }
      if (msg.type === 'hexEditError') document.getElementById('status').textContent = msg.error || 'Edit failed';
      if (msg.type === 'state') renderFileState(msg.state);
      if (msg.type === 'error') document.getElementById('status').textContent = msg.error || '';
    });
    renderFileState(initialFileState);
  </script>
</body>
</html>`;
  }

  html(meta) {
    const nonce = String(Date.now());
    const defaults = JSON.stringify(DEFAULTS[this.tool] || {});
    const initialFileState = JSON.stringify(this.fileState || null);
    const name = escapeHtml(meta.name);
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-editor-background); font: var(--vscode-font-size) var(--vscode-font-family); }
    header { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 10px 12px; border-bottom: 1px solid var(--vscode-panel-border); }
    h1 { margin: 0; font-size: 15px; }
    .title-wrap { min-width: 0; }
    .file-state { color: var(--vscode-descriptionForeground); font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    main { display: grid; grid-template-columns: minmax(260px, 360px) 1fr; height: calc(100vh - 58px); }
    textarea { width: calc(100% - 24px); height: calc(100% - 66px); margin: 12px; resize: none; color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); font-family: var(--vscode-editor-font-family); }
    .left { border-right: 1px solid var(--vscode-panel-border); }
    .actions { display: flex; gap: 6px; padding: 0 12px 12px; }
    button { padding: 5px 10px; }
    #out { margin: 0; padding: 12px; overflow: auto; height: calc(100% - 24px); box-sizing: border-box; }
    table { border-collapse: collapse; width: 100%; margin: 0 0 12px; }
    th, td { border: 1px solid var(--vscode-panel-border); padding: 4px 6px; text-align: left; vertical-align: top; font-family: var(--vscode-editor-font-family); font-size: 12px; }
    th { background: var(--vscode-editorWidget-background); position: sticky; top: 0; }
    pre { white-space: pre-wrap; word-break: break-word; }
    img { max-width: 100%; height: auto; border: 1px solid var(--vscode-panel-border); }
    h2 { font-size: 14px; margin: 12px 0 6px; }
    .hex-table td, .hex-table th { font-family: Menlo, Monaco, Consolas, "Courier New", monospace; }
    .hex-cell { display: inline-block; min-width: 2.2ch; margin-right: .35ch; }
    .ascii { color: var(--vscode-descriptionForeground); }
  </style>
</head>
<body>
  <header>
    <div class="title-wrap"><h1>${name}</h1><div id="fileState" class="file-state">Checking loaded file...</div></div>
    <div><button id="undo">Undo</button><button id="redo">Redo</button><button id="revert">Revert</button></div>
  </header>
  <main>
    <section class="left">
      <textarea id="params" spellcheck="false">${escapeHtml(JSON.stringify(JSON.parse(defaults), null, 2))}</textarea>
      <div class="actions"><button id="run">Run</button></div>
    </section>
    <section id="out">Run the tool to see results.</section>
  </main>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    const initialFileState = ${initialFileState};
    document.getElementById('run').onclick = () => vscode.postMessage({ type: 'run', params: document.getElementById('params').value });
    document.getElementById('undo').onclick = () => vscode.postMessage({ type: 'undo' });
    document.getElementById('redo').onclick = () => vscode.postMessage({ type: 'redo' });
    document.getElementById('revert').onclick = () => vscode.postMessage({ type: 'revert' });
    function esc(value) {
      return String(value == null ? '' : value).replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
    }
    function table(rows) {
      if (!Array.isArray(rows) || rows.length === 0 || typeof rows[0] !== 'object') return '<pre>' + esc(JSON.stringify(rows, null, 2)) + '</pre>';
      const keys = Array.from(rows.reduce((set, row) => {
        Object.keys(row || {}).slice(0, 12).forEach(key => set.add(key));
        return set;
      }, new Set()));
      return '<table><thead><tr>' + keys.map(k => '<th>' + esc(k) + '</th>').join('') + '</tr></thead><tbody>' +
        rows.slice(0, 1000).map(row => '<tr>' + keys.map(k => '<td>' + esc(typeof row[k] === 'object' ? JSON.stringify(row[k]) : row[k]) + '</td>').join('') + '</tr>').join('') +
        '</tbody></table>' + (rows.length > 1000 ? '<p>Showing first 1000 rows.</p>' : '');
    }
    function renderHexRows(rows) {
      return '<table class="hex-table"><thead><tr><th>Offset</th><th>Hex</th><th>ASCII</th></tr></thead><tbody>' +
        rows.map(row => {
          const cells = Array.isArray(row.bytes) ? row.bytes.map(cell => '<span class="hex-cell">' + esc(cell && cell.hex ? cell.hex : cell) + '</span>').join('') : esc(row.hex || '');
          return '<tr><td>' + esc(row.offset) + '</td><td>' + cells + '</td><td class="ascii">' + esc(row.ascii || '') + '</td></tr>';
        }).join('') + '</tbody></table>';
    }
    function render(value) {
      if (value && value.mime === 'image/png' && value.pngBase64) {
        const summary = value.summary ? '<h2>Summary</h2>' + render(value.summary) : '';
        return '<img src="data:image/png;base64,' + value.pngBase64 + '">' + summary;
      }
      if (Array.isArray(value)) return table(value);
      if (!value || typeof value !== 'object') return '<pre>' + esc(value) + '</pre>';
      if (Array.isArray(value.rows) && value.rows.length && Object.prototype.hasOwnProperty.call(value.rows[0], 'ascii')) {
        return renderHexRows(value.rows);
      }
      const preferred = ['rows', 'items', 'hits', 'results', 'entries', 'plan'];
      let html = '';
      for (const key of preferred) {
        if (Array.isArray(value[key])) html += '<h2>' + esc(key) + '</h2>' + table(value[key]);
      }
      for (const [key, val] of Object.entries(value)) {
        if (preferred.includes(key) && Array.isArray(val)) continue;
        html += '<h2>' + esc(key) + '</h2>' + (Array.isArray(val) ? table(val) : '<pre>' + esc(JSON.stringify(val, null, 2)) + '</pre>');
      }
      return html || '<pre>' + esc(JSON.stringify(value, null, 2)) + '</pre>';
    }
    function renderFileState(s) {
      document.getElementById('fileState').textContent = s && s.name ? s.name + ' • ' + Number(s.size || 0).toLocaleString() + ' bytes' + (s.dirty ? ' • modified' : '') : 'No file loaded';
    }
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'result') document.getElementById('out').innerHTML = render(msg.result);
      if (msg.type === 'state') document.getElementById('out').innerHTML = render(msg.state);
      if (msg.type === 'fileState') renderFileState(msg.state);
      if (msg.type === 'error') document.getElementById('out').textContent = msg.error;
    });
    renderFileState(initialFileState);
  </script>
</body>
</html>`;
  }
}

async function loadFile(bridge, compare, view) {
  const picked = await vscode.window.showOpenDialog({ canSelectFiles: true, canSelectFolders: false, canSelectMany: false });
  if (!picked || !picked.length) return null;
  await bridge.rpc('LeviathanLoadFile', [picked[0].fsPath, compare]);
  if (view) await view.refreshState();
  return bridge.rpc('LeviathanState');
}

async function saveFile(bridge, view) {
  const state = await bridge.rpc('LeviathanState');
  const defaultName = state && state.name ? `${state.name}.patched` : 'working-copy.bin';
  const picked = await vscode.window.showSaveDialog({ defaultUri: vscode.Uri.file(defaultName) });
  if (!picked) return;
  await bridge.rpc('LeviathanSaveWorkingToPath', [picked.fsPath]);
  await view.refreshState();
}

async function ensureLoadedFile(bridge, toolsView) {
  try {
    const state = await bridge.rpc('LeviathanState');
    if (state && state.name) return state;
  } catch (_) {
    // Fall through to picker.
  }
  const loaded = await loadFile(bridge, false, toolsView);
  if (!loaded || !loaded.name) {
    throw new Error('Load a file before opening a Leviathan tool.');
  }
  return loaded;
}

async function openTool(context, bridge, toolsView, tool) {
  const state = await ensureLoadedFile(bridge, toolsView);
  const autoRun = !['case', 'patch-apply', 'spectrogram-encode'].includes(tool);
  new ToolPanel(context, bridge, tool, { autoRun, fileState: state });
}

async function openSelectedTool(context, bridge, toolsView, tool, resource, selectedResources) {
  const target = Array.isArray(selectedResources) && selectedResources.length > 0
    ? selectedResources[0]
    : resource;
  if (!target || target.scheme !== 'file') {
    vscode.window.showErrorMessage('Select a local file before opening it in Leviathan.');
    return;
  }
  await bridge.rpc('LeviathanLoadFile', [target.fsPath, false]);
  await toolsView.refreshState();
  const state = await bridge.rpc('LeviathanState');
  const autoRun = !['case', 'patch-apply', 'spectrogram-encode'].includes(tool);
  new ToolPanel(context, bridge, tool, { autoRun, fileState: state });
}

function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
}

async function activate(context) {
  const bridge = new Bridge(context);
  await bridge.start();
  const toolsView = new ToolsView(context, bridge);
  context.subscriptions.push(vscode.window.registerWebviewViewProvider('leviathan.tools', toolsView));
  context.subscriptions.push(vscode.commands.registerCommand('leviathan.loadFile', () => loadFile(bridge, false, toolsView)));
  context.subscriptions.push(vscode.commands.registerCommand('leviathan.loadCompare', () => loadFile(bridge, true, toolsView)));
  context.subscriptions.push(vscode.commands.registerCommand('leviathan.saveFile', () => saveFile(bridge, toolsView)));
  context.subscriptions.push(vscode.commands.registerCommand('leviathan.openTool', tool => openTool(context, bridge, toolsView, tool)));
  for (const tool of TOOLS) {
    context.subscriptions.push(vscode.commands.registerCommand(
      `leviathan.openSelected.${tool.id}`,
      (resource, selectedResources) => openSelectedTool(context, bridge, toolsView, tool.id, resource, selectedResources)
    ));
  }
}

function deactivate() {}

module.exports = { activate, deactivate };
