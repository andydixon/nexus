'use strict';

const vscode = require('vscode');
const cp = require('child_process');
const fs = require('fs');
const http = require('http');
const path = require('path');

class Bridge {
  constructor(context) {
    this.context = context;
    this.proc = null;
    this.addr = null;
    this.token = null;
    this.events = new vscode.EventEmitter();
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
    this.openEvents();
  }

  stop() {
    if (this.proc && !this.proc.killed) this.proc.kill();
    this.proc = null;
  }

  async rpc(method, args = []) {
    await this.start();
    return this.request('/rpc', { method, args });
  }

  async runtimeEvent(name, payload) {
    await this.start();
    return this.request('/runtime/event', { name, payload });
  }

  request(route, body) {
    const payload = Buffer.from(JSON.stringify(body));
    return new Promise((resolve, reject) => {
      const req = http.request({
        hostname: this.addr.split(':')[0],
        port: Number(this.addr.split(':')[1]),
        path: route,
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

  openEvents() {
    const req = http.request({
      hostname: this.addr.split(':')[0],
      port: Number(this.addr.split(':')[1]),
      path: '/events',
      method: 'GET',
      headers: { Authorization: `Bearer ${this.token}` }
    }, res => {
      res.setEncoding('utf8');
      let buffer = '';
      res.on('data', chunk => {
        buffer += chunk;
        let idx;
        while ((idx = buffer.indexOf('\n\n')) >= 0) {
          const frame = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          for (const line of frame.split(/\r?\n/)) {
            if (!line.startsWith('data: ')) continue;
            try {
              this.events.fire(JSON.parse(line.slice(6)));
            } catch (_) {
              // Ignore malformed bridge events.
            }
          }
        }
      });
    });
    req.on('error', () => {});
    req.end();
  }
}

class ConnectionsProvider {
  constructor(context, bridge) {
    this.context = context;
    this.bridge = bridge;
    this.view = null;
    this.profiles = [];
  }

  resolveWebviewView(view) {
    this.view = view;
    view.webview.options = { enableScripts: true };
    view.webview.html = this.html(view.webview);
    view.webview.onDidReceiveMessage(msg => this.onMessage(msg));
    this.refresh();
  }

  async refresh() {
    this.profiles = await this.bridge.rpc('ListProfiles');
    this.postState();
  }

  postState() {
    if (this.view) this.view.webview.postMessage({ type: 'profiles', profiles: this.profiles });
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'refresh') await this.refresh();
      if (msg.type === 'local') {
        const session = await this.bridge.rpc('ConnectLocal');
        openSessionPanel(this.context, this.bridge, session, { protocol: 'local' });
      }
      if (msg.type === 'new') {
        const profile = await this.bridge.rpc('NewProfile', [msg.protocol || 'ssh']);
        new ProfileEditorPanel(this.context, this.bridge, this, profile, true);
      }
      if (msg.type === 'connect') {
        const profile = await this.bridge.rpc('GetProfile', [msg.id]);
        const session = await this.bridge.rpc('ConnectSession', [profile.id]);
        openSessionPanel(this.context, this.bridge, session, profile);
      }
      if (msg.type === 'edit') {
        const profile = await this.bridge.rpc('GetProfile', [msg.id]);
        new ProfileEditorPanel(this.context, this.bridge, this, profile, false);
      }
      if (msg.type === 'duplicate') {
        const copy = await this.bridge.rpc('DuplicateProfile', [msg.id]);
        await this.refresh();
        new ProfileEditorPanel(this.context, this.bridge, this, copy, false);
      }
      if (msg.type === 'delete') {
        const profile = this.profiles.find(p => p.id === msg.id) || {};
        const label = profile.displayName || profile.host || profile.id || 'profile';
        const ok = await vscode.window.showWarningMessage(`Delete Nexus profile "${label}"?`, { modal: true }, 'Delete');
        if (ok !== 'Delete') return;
        await this.bridge.rpc('DeleteProfile', [msg.id]);
        await this.refresh();
      }
      if (msg.type === 'sftp') {
        const profile = await this.bridge.rpc('GetProfile', [msg.id]);
        if (String(profile.protocol).toLowerCase() !== 'ssh') {
          vscode.window.showErrorMessage('SFTP is available for SSH profiles.');
          return;
        }
        const session = await this.bridge.rpc('ConnectSession', [profile.id]);
        new SFTPPanel(this.context, this.bridge, session);
      }
      if (msg.type === 'settings') new SettingsPanel(this.context, this.bridge);
    } catch (err) {
      this.postError(err);
    }
  }

  postError(err) {
    if (this.view) this.view.webview.postMessage({ type: 'error', error: err.message || String(err) });
    else vscode.window.showErrorMessage(err.message || String(err));
  }

  html(webview) {
    const nonce = String(Date.now());
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    :root { color-scheme: light dark; }
    body {
      margin: 0;
      color: var(--vscode-foreground);
      background: var(--vscode-sideBar-background);
      font: var(--vscode-font-size) var(--vscode-font-family);
    }
    .shell { display: grid; gap: 10px; padding: 10px; }
    .toolbar { display: grid; grid-template-columns: 1fr auto auto; gap: 6px; align-items: center; }
    .search {
      width: 100%;
      min-width: 0;
      border: 1px solid var(--vscode-input-border, transparent);
      background: var(--vscode-input-background);
      color: var(--vscode-input-foreground);
      padding: 6px 8px;
      box-sizing: border-box;
      outline: none;
    }
    .search:focus { border-color: var(--vscode-focusBorder); }
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
    .primary {
      background: var(--vscode-button-background);
      color: var(--vscode-button-foreground);
    }
    .segments { display: flex; flex-wrap: wrap; gap: 5px; }
    .seg {
      border: 1px solid var(--vscode-panel-border);
      background: transparent;
      color: var(--vscode-descriptionForeground);
      padding: 3px 7px;
      min-height: 23px;
    }
    .seg.active {
      color: var(--vscode-button-foreground);
      background: var(--vscode-button-background);
      border-color: transparent;
    }
    .stats {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      gap: 6px;
    }
    .stat {
      border: 1px solid var(--vscode-panel-border);
      padding: 7px;
      background: var(--vscode-editorWidget-background);
    }
    .stat strong { display: block; font-size: 15px; line-height: 18px; }
    .stat span { color: var(--vscode-descriptionForeground); font-size: 11px; }
    .group-title {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin: 10px 0 5px;
      color: var(--vscode-descriptionForeground);
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: .04em;
    }
    .card {
      border: 1px solid var(--vscode-panel-border);
      background: var(--vscode-editor-background);
      margin-bottom: 7px;
      overflow: hidden;
    }
    .card:hover { border-color: var(--vscode-focusBorder); }
    .card-main {
      display: grid;
      grid-template-columns: auto 1fr;
      gap: 8px;
      padding: 8px;
      cursor: pointer;
    }
    .proto {
      width: 34px;
      height: 28px;
      display: grid;
      place-items: center;
      border: 1px solid var(--vscode-panel-border);
      font-size: 10px;
      font-weight: 700;
      color: var(--vscode-badge-foreground);
      background: var(--vscode-badge-background);
    }
    .title {
      font-weight: 600;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      line-height: 18px;
    }
    .meta {
      color: var(--vscode-descriptionForeground);
      font-size: 11px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      line-height: 16px;
    }
    .tags { display: flex; flex-wrap: wrap; gap: 4px; padding-top: 4px; }
    .tag {
      border: 1px solid var(--vscode-panel-border);
      color: var(--vscode-descriptionForeground);
      padding: 1px 5px;
      font-size: 10px;
    }
    .actions {
      display: grid;
      grid-template-columns: repeat(4, 1fr);
      border-top: 1px solid var(--vscode-panel-border);
    }
    .actions button {
      border: 0;
      border-right: 1px solid var(--vscode-panel-border);
      background: transparent;
      color: var(--vscode-descriptionForeground);
      padding: 5px 4px;
      font-size: 11px;
    }
    .actions button:last-child { border-right: 0; }
    .actions button:hover { color: var(--vscode-foreground); background: var(--vscode-list-hoverBackground); }
    .empty {
      border: 1px dashed var(--vscode-panel-border);
      padding: 18px 10px;
      text-align: center;
      color: var(--vscode-descriptionForeground);
    }
    .status { color: var(--vscode-descriptionForeground); font-size: 11px; min-height: 15px; }
  </style>
</head>
<body>
  <div class="shell">
    <div class="toolbar">
      <input id="search" class="search" placeholder="Search host, user, tag">
      <button id="new" class="primary" title="New profile">+</button>
      <button id="settings" title="Settings">⚙</button>
    </div>
    <div class="toolbar">
      <button id="local" class="primary">Local</button>
      <button id="refresh">Refresh</button>
      <span></span>
    </div>
    <div id="segments" class="segments"></div>
    <div id="stats" class="stats"></div>
    <div id="list"></div>
    <div id="status" class="status"></div>
  </div>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    let profiles = [];
    let filter = 'all';
    const protocols = ['all', 'ssh', 'telnet', 'raw', 'github', 'vnc', 'irc'];
    const search = document.getElementById('search');
    const status = document.getElementById('status');
    function esc(value) {
      return String(value == null ? '' : value).replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
    }
    function detail(profile) {
      const user = profile.username ? profile.username + '@' : '';
      const host = profile.host || (profile.protocol === 'github' ? 'GitHub API' : 'local');
      const port = profile.port ? ':' + profile.port : '';
      return user + host + port;
    }
    function groupName(profile) {
      if (profile.favourite) return 'Favourites';
      return profile.folder || 'Ungrouped';
    }
    function protoLabel(protocol) {
      const p = String(protocol || '').toUpperCase();
      return p === 'GITHUB' ? 'GH' : p;
    }
    function matches(profile) {
      const q = search.value.trim().toLowerCase();
      if (filter !== 'all' && profile.protocol !== filter) return false;
      if (!q) return true;
      const haystack = [profile.displayName, profile.host, profile.username, profile.folder, profile.description, profile.protocol, ...(profile.tags || [])].join(' ').toLowerCase();
      return haystack.includes(q);
    }
    function renderSegments() {
      const root = document.getElementById('segments');
      root.textContent = '';
      for (const proto of protocols) {
        const count = proto === 'all' ? profiles.length : profiles.filter(p => p.protocol === proto).length;
        const btn = document.createElement('button');
        btn.className = 'seg' + (filter === proto ? ' active' : '');
        btn.textContent = proto === 'all' ? 'All ' + count : proto.toUpperCase() + ' ' + count;
        btn.onclick = () => { filter = proto; render(); };
        root.appendChild(btn);
      }
    }
    function renderStats(visible) {
      const ssh = profiles.filter(p => p.protocol === 'ssh').length;
      const fav = profiles.filter(p => p.favourite).length;
      document.getElementById('stats').innerHTML =
        '<div class="stat"><strong>' + profiles.length + '</strong><span>profiles</span></div>' +
        '<div class="stat"><strong>' + ssh + '</strong><span>SSH</span></div>' +
        '<div class="stat"><strong>' + visible.length + '</strong><span>shown</span></div>';
    }
    function card(profile) {
      const tags = (profile.tags || []).slice(0, 4).map(tag => '<span class="tag">' + esc(tag) + '</span>').join('');
      const sftp = profile.protocol === 'ssh' ? '<button data-action="sftp">SFTP</button>' : '<button data-action="duplicate">Copy</button>';
      return '<article class="card" data-id="' + esc(profile.id) + '">' +
        '<div class="card-main" data-action="connect"><div class="proto">' + esc(protoLabel(profile.protocol)) + '</div><div><div class="title">' + esc(profile.displayName || profile.host || profile.id) + '</div><div class="meta">' + esc(detail(profile)) + '</div>' + (tags ? '<div class="tags">' + tags + '</div>' : '') + '</div></div>' +
        '<div class="actions"><button data-action="connect">Open</button><button data-action="edit">Edit</button>' + sftp + '<button data-action="delete">Delete</button></div>' +
      '</article>';
    }
    function render() {
      renderSegments();
      const visible = profiles.filter(matches);
      renderStats(visible);
      const root = document.getElementById('list');
      root.textContent = '';
      if (!visible.length) {
        root.innerHTML = '<div class="empty">No matching connections<br><br><button class="primary" data-empty-new>New profile</button></div>';
        root.querySelector('[data-empty-new]').onclick = () => vscode.postMessage({ type: 'new', protocol: 'ssh' });
        status.textContent = '';
        return;
      }
      const groups = new Map();
      for (const profile of visible) {
        const group = groupName(profile);
        if (!groups.has(group)) groups.set(group, []);
        groups.get(group).push(profile);
      }
      const ordered = [...groups.entries()].sort((a, b) => a[0].localeCompare(b[0]));
      root.innerHTML = ordered.map(([name, items]) =>
        '<div class="group-title"><span>' + esc(name) + '</span><span>' + items.length + '</span></div>' +
        items.sort((a, b) => String(a.displayName || a.host).localeCompare(String(b.displayName || b.host))).map(card).join('')
      ).join('');
      status.textContent = visible.length + ' connection' + (visible.length === 1 ? '' : 's');
    }
    document.getElementById('new').onclick = () => vscode.postMessage({ type: 'new', protocol: filter === 'all' ? 'ssh' : filter });
    document.getElementById('settings').onclick = () => vscode.postMessage({ type: 'settings' });
    document.getElementById('local').onclick = () => vscode.postMessage({ type: 'local' });
    document.getElementById('refresh').onclick = () => vscode.postMessage({ type: 'refresh' });
    search.addEventListener('input', render);
    document.getElementById('list').addEventListener('click', event => {
      const actionEl = event.target.closest('[data-action]');
      const cardEl = event.target.closest('.card');
      if (!actionEl || !cardEl) return;
      vscode.postMessage({ type: actionEl.dataset.action, id: cardEl.dataset.id });
    });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'profiles') { profiles = msg.profiles || []; render(); }
      if (msg.type === 'error') status.textContent = msg.error || '';
    });
    vscode.postMessage({ type: 'refresh' });
  </script>
</body>
</html>`;
  }
}

class TerminalPanel {
  constructor(context, bridge, session, profile) {
    this.context = context;
    this.bridge = bridge;
    this.session = session;
    this.profile = profile || {};
    this.disposables = [];
    this.panel = vscode.window.createWebviewPanel(
      'nexusTerminal',
      session.tabName || session.profileName || 'Nexus',
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true, localResourceRoots: [vscode.Uri.file(path.join(context.extensionPath, 'media'))] }
    );
    this.panel.webview.html = this.html();
    this.panel.webview.onDidReceiveMessage(msg => this.onMessage(msg), null, this.disposables);
    this.bridge.events.event(evt => this.onBridgeEvent(evt), null, this.disposables);
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  dispose() {
    this.bridge.rpc('DisconnectSession', [this.session.id]).catch(() => {});
    while (this.disposables.length) this.disposables.pop().dispose();
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'input') {
        await this.bridge.rpc('WriteSession', [this.session.id, msg.data]);
      } else if (msg.type === 'resize') {
        await this.bridge.rpc('ResizeSession', [this.session.id, msg.cols, msg.rows]);
      } else if (msg.type === 'sftp') {
        new SFTPPanel(this.context, this.bridge, this.session);
      }
    } catch (err) {
      this.panel.webview.postMessage({ type: 'status', text: err.message });
    }
  }

  onBridgeEvent(evt) {
    const payload = evt.payload || {};
    if (evt.name === 'session:data' && payload.sessionID === this.session.id) {
      this.panel.webview.postMessage({ type: 'data', data: payload.data });
    }
    if (evt.name === 'session:state' && payload.sessionID === this.session.id) {
      this.panel.webview.postMessage({ type: 'status', text: payload.error || payload.state });
    }
    if (evt.name === 'session:closed' && payload.sessionID === this.session.id) {
      this.panel.webview.postMessage({ type: 'status', text: 'disconnected' });
    }
  }

  html() {
    const webview = this.panel.webview;
    const mediaRoot = vscode.Uri.file(path.join(this.context.extensionPath, 'media'));
    const xtermJs = webview.asWebviewUri(vscode.Uri.joinPath(mediaRoot, 'xterm.min.js'));
    const fitJs = webview.asWebviewUri(vscode.Uri.joinPath(mediaRoot, 'xterm-addon-fit.min.js'));
    const xtermCss = webview.asWebviewUri(vscode.Uri.joinPath(mediaRoot, 'xterm.css'));
    const nonce = String(Date.now());
    const sftpStyle = String(this.profile.protocol || this.session.protocol || '').toLowerCase() === 'ssh' ? '' : 'display:none';
    const terminalFontFamily = JSON.stringify(this.profile.terminalFontFamily || 'Menlo, Monaco, Consolas, "Courier New", monospace');
    const terminalFontSize = Number(this.profile.terminalFontSize || 13);
    const terminalScrollback = Number(this.profile.terminalScrollback || 10000);
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; script-src ${webview.cspSource} 'nonce-${nonce}';">
  <link rel="stylesheet" href="${xtermCss}">
  <style>
    html, body {
      width: 100%;
      height: 100%;
      margin: 0;
      overflow: hidden;
      background: #0f1720;
      font-family: Menlo, Monaco, Consolas, "Courier New", monospace;
    }
    #terminal {
      position: fixed;
      inset: 0;
      width: 100vw;
      height: 100vh;
      background: #0f1720;
      font-family: Menlo, Monaco, Consolas, "Courier New", monospace;
    }
    .xterm, .xterm-viewport, .xterm-screen {
      width: 100% !important;
      height: 100% !important;
      font-family: Menlo, Monaco, Consolas, "Courier New", monospace !important;
    }
    #status {
      position: fixed;
      right: 10px;
      top: 8px;
      z-index: 2;
      color: #a7f3d0;
      font: 12px Menlo, Monaco, Consolas, "Courier New", monospace;
      pointer-events: none;
      opacity: .9;
    }
    #sftp {
      position: fixed;
      left: 10px;
      top: 8px;
      z-index: 2;
      border: 1px solid rgba(148, 163, 184, .35);
      background: rgba(15, 23, 32, .82);
      color: #dbeafe;
      padding: 4px 9px;
      font: 12px Menlo, Monaco, Consolas, "Courier New", monospace;
      cursor: pointer;
    }
    #sftp:hover { background: rgba(30, 41, 59, .95); }
  </style>
</head>
<body>
  <div id="status">connecting</div>
  <button id="sftp" style="${sftpStyle}">SFTP</button>
  <div id="terminal"></div>
  <script nonce="${nonce}" src="${xtermJs}"></script>
  <script nonce="${nonce}" src="${fitJs}"></script>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    const term = new Terminal({
      cursorBlink: true,
      scrollback: ${terminalScrollback},
      fontFamily: ${terminalFontFamily},
      fontSize: ${terminalFontSize},
      theme: { background: '#0f1720' }
    });
    const fit = new FitAddon.FitAddon();
    term.loadAddon(fit);
    term.open(document.getElementById('terminal'));
    fit.fit();
    term.onData(data => vscode.postMessage({ type: 'input', data: btoa(unescape(encodeURIComponent(data))) }));
    document.getElementById('sftp').onclick = () => vscode.postMessage({ type: 'sftp' });
    function resize() {
      fit.fit();
      vscode.postMessage({ type: 'resize', cols: term.cols, rows: term.rows });
    }
    window.addEventListener('resize', resize);
    setTimeout(resize, 50);
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'data') term.write(Uint8Array.from(atob(msg.data), c => c.charCodeAt(0)));
      if (msg.type === 'status') document.getElementById('status').textContent = msg.text || '';
    });
  </script>
</body>
</html>`;
  }
}

class RawPanel {
  constructor(context, bridge, session, profile) {
    this.context = context;
    this.bridge = bridge;
    this.session = session;
    this.profile = profile || {};
    this.disposables = [];
    this.panel = vscode.window.createWebviewPanel(
      'nexusRaw',
      session.tabName || session.profileName || 'RAW',
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true }
    );
    this.panel.webview.html = this.html();
    this.panel.webview.onDidReceiveMessage(msg => this.onMessage(msg), null, this.disposables);
    this.bridge.events.event(evt => this.onBridgeEvent(evt), null, this.disposables);
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  dispose() {
    this.bridge.rpc('DisconnectSession', [this.session.id]).catch(() => {});
    while (this.disposables.length) this.disposables.pop().dispose();
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'send') {
        await this.bridge.rpc('WriteSession', [this.session.id, msg.data]);
        this.panel.webview.postMessage({ type: 'raw', direction: 'send', data: msg.data, timestamp: new Date().toISOString() });
      } else if (msg.type === 'saveCapture') {
        const picked = await vscode.window.showSaveDialog({ defaultUri: vscode.Uri.file(`${this.session.tabName || 'raw'}-capture.txt`) });
        if (!picked) return;
        await vscode.workspace.fs.writeFile(picked, Buffer.from(msg.content || '', 'utf8'));
      }
    } catch (err) {
      this.panel.webview.postMessage({ type: 'status', text: err.message });
    }
  }

  onBridgeEvent(evt) {
    const payload = evt.payload || {};
    if (evt.name === 'session:raw:data' && payload.sessionID === this.session.id) {
      this.panel.webview.postMessage({ type: 'raw', direction: payload.direction || 'recv', data: payload.data, timestamp: payload.timestamp || new Date().toISOString() });
    }
    if (evt.name === 'session:state' && payload.sessionID === this.session.id) {
      this.panel.webview.postMessage({ type: 'status', text: payload.error || payload.state });
    }
  }

  html() {
    const nonce = String(Date.now());
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-editor-background); font: var(--vscode-font-size) var(--vscode-font-family); }
    header { display: flex; gap: 8px; align-items: center; padding: 8px; border-bottom: 1px solid var(--vscode-panel-border); }
    select, input { color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); padding: 4px 6px; }
    input { flex: 1; }
    button { padding: 4px 10px; }
    #status { margin-left: auto; color: var(--vscode-descriptionForeground); }
    #log { height: calc(100vh - 44px); overflow: auto; padding: 8px; box-sizing: border-box; font-family: var(--vscode-editor-font-family), monospace; white-space: pre-wrap; }
    .row { border-bottom: 1px solid var(--vscode-panel-border); padding: 4px 0; }
    .send { color: var(--vscode-terminal-ansiGreen); }
    .recv { color: var(--vscode-terminal-ansiCyan); }
    .hex { color: var(--vscode-descriptionForeground); }
  </style>
</head>
<body>
  <header>
    <select id="format"><option value="text">Text</option><option value="hex">Hex</option><option value="escaped">Escaped</option></select>
    <input id="input" autocomplete="off" spellcheck="false">
    <button id="send">Send</button>
    <button id="clear">Clear</button>
    <button id="save">Save</button>
    <span id="status">connecting</span>
  </header>
  <main id="log"></main>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    const log = document.getElementById('log');
    const capture = [];
    function bytesFromBase64(data) {
      const raw = atob(data || '');
      return Uint8Array.from(raw, c => c.charCodeAt(0));
    }
    function textFromBytes(bytes) {
      return new TextDecoder('utf-8', { fatal: false }).decode(bytes);
    }
    function base64FromBytes(bytes) {
      let raw = '';
      for (const b of bytes) raw += String.fromCharCode(b);
      return btoa(raw);
    }
    function parseEscaped(value) {
      const out = [];
      for (let i = 0; i < value.length; i++) {
        if (value[i] !== '\\\\') { out.push(value.charCodeAt(i)); continue; }
        const n = value[++i];
        if (n === 'n') out.push(10);
        else if (n === 'r') out.push(13);
        else if (n === 't') out.push(9);
        else if (n === 'x') { out.push(parseInt(value.slice(i + 1, i + 3), 16) || 0); i += 2; }
        else out.push((n || '').charCodeAt(0));
      }
      return new Uint8Array(out);
    }
    function send() {
      const value = document.getElementById('input').value;
      const format = document.getElementById('format').value;
      let bytes;
      if (format === 'hex') bytes = Uint8Array.from((value.match(/[0-9a-fA-F]{2}/g) || []).map(h => parseInt(h, 16)));
      else if (format === 'escaped') bytes = parseEscaped(value);
      else bytes = new TextEncoder().encode(value);
      vscode.postMessage({ type: 'send', data: base64FromBytes(bytes) });
      document.getElementById('input').value = '';
    }
    function append(direction, data, timestamp) {
      const bytes = bytesFromBase64(data);
      const text = textFromBytes(bytes);
      const hex = Array.from(bytes).map(b => b.toString(16).padStart(2, '0')).join(' ');
      capture.push({ direction, timestamp, text });
      const row = document.createElement('div');
      row.className = 'row ' + direction;
      row.textContent = '[' + new Date(timestamp).toLocaleTimeString() + '] [' + direction.toUpperCase() + '] ' + text;
      const hexEl = document.createElement('div');
      hexEl.className = 'hex';
      hexEl.textContent = hex;
      row.appendChild(hexEl);
      log.appendChild(row);
      log.scrollTop = log.scrollHeight;
    }
    document.getElementById('send').onclick = send;
    document.getElementById('input').addEventListener('keydown', event => { if (event.key === 'Enter') send(); });
    document.getElementById('clear').onclick = () => { capture.length = 0; log.textContent = ''; };
    document.getElementById('save').onclick = () => vscode.postMessage({
      type: 'saveCapture',
      content: capture.map(entry => '[' + entry.timestamp + '] [' + entry.direction.toUpperCase() + '] ' + entry.text).join('\\n')
    });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'raw') append(msg.direction, msg.data, msg.timestamp);
      if (msg.type === 'status') document.getElementById('status').textContent = msg.text || '';
    });
  </script>
</body>
</html>`;
  }
}

class GitHubPanel {
  constructor(context, bridge, session) {
    this.context = context;
    this.bridge = bridge;
    this.session = session;
    this.disposables = [];
    this.panel = vscode.window.createWebviewPanel(
      'nexusGitHub',
      session.tabName || session.profileName || 'GitHub',
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true }
    );
    this.panel.webview.html = this.html();
    this.panel.webview.onDidReceiveMessage(msg => this.onMessage(msg), null, this.disposables);
    this.bridge.events.event(evt => this.onBridgeEvent(evt), null, this.disposables);
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
    setTimeout(() => this.refresh(), 1000);
  }

  dispose() {
    this.bridge.rpc('DisconnectSession', [this.session.id]).catch(() => {});
    while (this.disposables.length) this.disposables.pop().dispose();
  }

  async refresh() {
    try {
      const repos = await this.bridge.rpc('RefreshGitHubRepos', [this.session.id]);
      this.panel.webview.postMessage({ type: 'repos', repos });
    } catch (err) {
      this.panel.webview.postMessage({ type: 'error', error: err.message });
    }
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'refresh') await this.refresh();
      if (msg.type === 'open' && msg.url) await vscode.env.openExternal(vscode.Uri.parse(msg.url));
      if (msg.type === 'download') {
        const defaultName = `${(msg.fullName || 'repository').split('/').pop()}${msg.ref ? '-' + msg.ref.replace(/[\\\\/]/g, '-') : ''}.zip`;
        const picked = await vscode.window.showSaveDialog({ defaultUri: vscode.Uri.file(defaultName) });
        if (!picked) return;
        const pathWritten = await this.bridge.rpc('DownloadGitHubRepoArchiveToPath', [this.session.id, msg.fullName, msg.ref || '', picked.fsPath]);
        vscode.window.showInformationMessage(`Downloaded ${pathWritten}`);
      }
    } catch (err) {
      this.panel.webview.postMessage({ type: 'error', error: err.message });
    }
  }

  onBridgeEvent(evt) {
    const payload = evt.payload || {};
    if (payload.sessionID !== this.session.id) return;
    if (evt.name === 'session:github:repos') this.panel.webview.postMessage({ type: 'repos', login: payload.login, repos: payload.repos || [] });
    if (evt.name === 'session:github:status') this.panel.webview.postMessage({ type: 'status', status: payload.status });
    if (evt.name === 'session:github:error') this.panel.webview.postMessage({ type: 'error', error: payload.error });
    if (evt.name === 'session:state') this.panel.webview.postMessage({ type: 'status', status: payload.error || payload.state });
  }

  html() {
    const nonce = String(Date.now());
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-editor-background); font: var(--vscode-font-size) var(--vscode-font-family); }
    header { display: flex; gap: 8px; padding: 8px; border-bottom: 1px solid var(--vscode-panel-border); align-items: center; }
    input { flex: 1; color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); padding: 5px 7px; }
    button { padding: 4px 10px; }
    #status { color: var(--vscode-descriptionForeground); }
    #repos { padding: 8px; display: grid; gap: 8px; }
    .repo { border: 1px solid var(--vscode-panel-border); padding: 8px; }
    .repo h3 { margin: 0 0 4px; font-size: 14px; }
    .meta { color: var(--vscode-descriptionForeground); font-size: 12px; }
  </style>
</head>
<body>
  <header><input id="filter" placeholder="Filter repositories"><button id="refresh">Refresh</button><span id="status">connecting</span></header>
  <main id="repos"></main>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    let repos = [];
    function esc(value) { return String(value || '').replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch])); }
    function render() {
      const q = document.getElementById('filter').value.toLowerCase();
      const root = document.getElementById('repos');
      root.textContent = '';
      for (const repo of repos.filter(r => (r.fullName + ' ' + (r.description || '') + ' ' + (r.language || '')).toLowerCase().includes(q))) {
        const el = document.createElement('section');
        el.className = 'repo';
        el.innerHTML = '<h3>' + esc(repo.fullName) + '</h3><div>' + esc(repo.description || '') + '</div><div class="meta">' + esc(repo.language || '') + ' ★ ' + (repo.stars || 0) + ' ' + (repo.private ? 'private' : 'public') + ' updated ' + esc(repo.updatedAt || repo.pushedAt || '') + '</div><p><button data-open>Open</button> <button data-download>Download ZIP</button></p>';
        el.querySelector('[data-open]').onclick = () => vscode.postMessage({ type: 'open', url: repo.htmlURL });
        el.querySelector('[data-download]').onclick = () => vscode.postMessage({ type: 'download', fullName: repo.fullName, ref: repo.defaultBranch || '' });
        root.appendChild(el);
      }
    }
    document.getElementById('filter').oninput = render;
    document.getElementById('refresh').onclick = () => vscode.postMessage({ type: 'refresh' });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'repos') { repos = msg.repos || []; document.getElementById('status').textContent = repos.length + ' repositories'; render(); }
      if (msg.type === 'status') document.getElementById('status').textContent = msg.status || '';
      if (msg.type === 'error') document.getElementById('status').textContent = msg.error || '';
    });
  </script>
</body>
</html>`;
  }
}

class IRCPanel {
  constructor(context, bridge, session, profile) {
    this.context = context;
    this.bridge = bridge;
    this.session = session;
    this.profile = profile || {};
    this.disposables = [];
    this.panel = vscode.window.createWebviewPanel(
      'nexusIrc',
      session.tabName || session.profileName || 'IRC',
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true }
    );
    this.panel.webview.html = this.html();
    this.panel.webview.onDidReceiveMessage(msg => this.onMessage(msg), null, this.disposables);
    this.bridge.events.event(evt => this.onBridgeEvent(evt), null, this.disposables);
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  dispose() {
    this.bridge.rpc('DisconnectSession', [this.session.id]).catch(() => {});
    while (this.disposables.length) this.disposables.pop().dispose();
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'send') await this.bridge.rpc('IRCHandleInput', [this.session.id, msg.target || 'server', msg.text || '']);
      if (msg.type === 'join') await this.bridge.rpc('IRCJoin', [this.session.id, msg.room || '']);
      if (msg.type === 'part') await this.bridge.rpc('IRCPart', [this.session.id, msg.room || '']);
      if (msg.type === 'list') await this.bridge.rpc('IRCListRooms', [this.session.id]);
    } catch (err) {
      this.panel.webview.postMessage({ type: 'error', error: err.message });
    }
  }

  onBridgeEvent(evt) {
    const payload = evt.payload || {};
    if (payload.sessionID !== this.session.id) return;
    if (evt.name === 'session:irc:event') this.panel.webview.postMessage({ type: 'irc', event: payload.event || payload });
    if (evt.name === 'session:state') this.panel.webview.postMessage({ type: 'status', status: payload.error || payload.state });
  }

  html() {
    const nonce = String(Date.now());
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-editor-background); font: var(--vscode-font-size) var(--vscode-font-family); }
    header, footer { display: flex; gap: 8px; padding: 8px; border-bottom: 1px solid var(--vscode-panel-border); }
    footer { border-top: 1px solid var(--vscode-panel-border); border-bottom: 0; }
    input { color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); padding: 5px 7px; }
    #target { width: 140px; }
    #message { flex: 1; }
    #log { height: calc(100vh - 92px); overflow: auto; padding: 8px; font-family: var(--vscode-editor-font-family), monospace; white-space: pre-wrap; }
    .status { color: var(--vscode-descriptionForeground); }
  </style>
</head>
<body>
  <header><input id="room" placeholder="#room"><button id="join">Join</button><button id="part">Part</button><button id="list">List</button><span id="status" class="status">connecting</span></header>
  <main id="log"></main>
  <footer><input id="target" value="server"><input id="message" autocomplete="off" spellcheck="false"><button id="send">Send</button></footer>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    const log = document.getElementById('log');
    function append(text, cls) {
      const row = document.createElement('div');
      row.className = cls || '';
      row.textContent = text;
      log.appendChild(row);
      log.scrollTop = log.scrollHeight;
    }
    function eventText(ev) {
      if (!ev) return '';
      if (ev.text) return '[' + (ev.target || ev.nick || 'server') + '] ' + ev.text;
      if (ev.message) return '[' + (ev.target || 'server') + '] ' + ev.message;
      return JSON.stringify(ev);
    }
    function send() {
      const text = document.getElementById('message').value;
      vscode.postMessage({ type: 'send', target: document.getElementById('target').value || 'server', text });
      document.getElementById('message').value = '';
    }
    document.getElementById('send').onclick = send;
    document.getElementById('message').addEventListener('keydown', e => { if (e.key === 'Enter') send(); });
    document.getElementById('join').onclick = () => vscode.postMessage({ type: 'join', room: document.getElementById('room').value });
    document.getElementById('part').onclick = () => vscode.postMessage({ type: 'part', room: document.getElementById('room').value });
    document.getElementById('list').onclick = () => vscode.postMessage({ type: 'list' });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'irc') append(eventText(msg.event), msg.event && msg.event.type === 'error' ? 'status' : '');
      if (msg.type === 'status') document.getElementById('status').textContent = msg.status || '';
      if (msg.type === 'error') append(msg.error, 'status');
    });
  </script>
</body>
</html>`;
  }
}

class VNCPanel {
  constructor(context, bridge, session, profile) {
    this.context = context;
    this.bridge = bridge;
    this.session = session;
    this.profile = profile || {};
    this.disposables = [];
    this.panel = vscode.window.createWebviewPanel(
      'nexusVnc',
      session.tabName || session.profileName || 'VNC',
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true, localResourceRoots: [vscode.Uri.file(path.join(context.extensionPath, 'media'))] }
    );
    this.panel.webview.html = this.html();
    this.bridge.events.event(evt => this.onBridgeEvent(evt), null, this.disposables);
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  dispose() {
    this.bridge.rpc('DisconnectSession', [this.session.id]).catch(() => {});
    while (this.disposables.length) this.disposables.pop().dispose();
  }

  onBridgeEvent(evt) {
    const payload = evt.payload || {};
    if (payload.sessionID !== this.session.id) return;
    if (evt.name === 'session:vnc:ready') this.panel.webview.postMessage({ type: 'connect', wsURL: payload.wsURL });
    if (evt.name === 'session:state') this.panel.webview.postMessage({ type: 'status', status: payload.error || payload.state });
  }

  html() {
    const webview = this.panel.webview;
    const mediaRoot = vscode.Uri.file(path.join(this.context.extensionPath, 'media'));
    const noVncJs = webview.asWebviewUri(vscode.Uri.joinPath(mediaRoot, 'novnc.min.js'));
    const nonce = String(Date.now());
    const vncConfig = JSON.stringify((this.profile && this.profile.vnc) || {});
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; connect-src ws://127.0.0.1:* ws://localhost:*; style-src 'unsafe-inline'; script-src ${webview.cspSource} 'nonce-${nonce}';">
  <style>
    html, body { margin: 0; width: 100%; height: 100%; color: var(--vscode-foreground); background: #111; font: var(--vscode-font-size) var(--vscode-font-family); }
    #toolbar { display: flex; gap: 8px; align-items: center; height: 36px; padding: 0 8px; box-sizing: border-box; background: var(--vscode-sideBar-background); }
    #screen { height: calc(100% - 36px); overflow: auto; display: flex; align-items: center; justify-content: center; }
    button { padding: 4px 10px; }
    #status { margin-right: auto; color: var(--vscode-descriptionForeground); }
  </style>
</head>
<body>
  <div id="toolbar"><span id="status">connecting</span><button id="cad">Ctrl+Alt+Del</button><button id="fit">Fit</button></div>
  <div id="screen"></div>
  <script nonce="${nonce}" src="${noVncJs}"></script>
  <script nonce="${nonce}">
    const config = ${vncConfig};
    let rfb = null;
    document.getElementById('cad').onclick = () => rfb && rfb.sendCtrlAltDel();
    document.getElementById('fit').onclick = () => { if (rfb) rfb.scaleViewport = !rfb.scaleViewport; };
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'status') document.getElementById('status').textContent = msg.status || '';
      if (msg.type === 'connect') {
        if (!window.noVNC || !window.noVNC.RFB) {
          document.getElementById('status').textContent = 'noVNC asset missing';
          return;
        }
        rfb = new window.noVNC.RFB(document.getElementById('screen'), msg.wsURL, { shared: config.sharedSession !== false, credentials: {} });
        rfb.viewOnly = !!config.viewOnly;
        rfb.scaleViewport = config.scaleToFit !== false;
        rfb.resizeSession = false;
        rfb.addEventListener('connect', () => document.getElementById('status').textContent = 'connected');
        rfb.addEventListener('disconnect', ev => document.getElementById('status').textContent = ev.detail && ev.detail.clean ? 'disconnected' : 'connection lost');
      }
    });
  </script>
</body>
</html>`;
  }
}

function openSessionPanel(context, bridge, session, profile) {
  const protocol = String(session.protocol || profile?.protocol || '').toLowerCase();
  if (protocol === 'raw') return new RawPanel(context, bridge, session, profile);
  if (protocol === 'github') return new GitHubPanel(context, bridge, session, profile);
  if (protocol === 'irc') return new IRCPanel(context, bridge, session, profile);
  if (protocol === 'vnc') return new VNCPanel(context, bridge, session, profile);
  return new TerminalPanel(context, bridge, session, profile);
}

class ProfileEditorPanel {
  constructor(context, bridge, provider, profile, isNew) {
    this.context = context;
    this.bridge = bridge;
    this.provider = provider;
    this.profile = profile;
    this.isNew = isNew;
    this.panel = vscode.window.createWebviewPanel(
      'nexusProfileEditor',
      isNew ? 'New Nexus Profile' : `Edit ${profile.displayName || profile.host || 'Profile'}`,
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true }
    );
    this.panel.webview.html = this.html();
    this.panel.webview.onDidReceiveMessage(msg => this.onMessage(msg));
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'protocol') {
        const next = await this.bridge.rpc('NewProfile', [msg.protocol]);
        const merged = {
          ...next,
          id: this.profile.id,
          displayName: msg.common.displayName || this.profile.displayName || '',
          description: msg.common.description || this.profile.description || '',
          host: msg.common.host || next.host || '',
          port: Number(msg.common.port || next.port || 0),
          username: msg.common.username || '',
          folder: msg.common.folder || '',
          tags: parseTags(msg.common.tags),
          favourite: !!msg.common.favourite
        };
        this.profile = merged;
        this.panel.webview.postMessage({ type: 'profile', profile: merged });
      }
      if (msg.type === 'save') {
        const profile = buildProfileFromEditor(msg.profile, this.profile);
        const saved = this.isNew
          ? await this.bridge.rpc('CreateProfile', [profile])
          : await this.bridge.rpc('UpdateProfile', [profile]);
        await saveProfileSecrets(this.bridge, saved.id, msg.secrets || {});
        await this.provider.refresh();
        vscode.window.showInformationMessage(`Saved ${saved.displayName || saved.host || saved.id}`);
        this.panel.dispose();
      }
      if (msg.type === 'test') {
        const host = msg.host || '';
        const port = Number(msg.port || 0);
        if (!host || !port) throw new Error('Host and port are required for a connection test.');
        const result = await this.bridge.rpc('TestConnection', [host, port]);
        this.panel.webview.postMessage({ type: 'testResult', result });
      }
    } catch (err) {
      this.panel.webview.postMessage({ type: 'error', error: err.message || String(err) });
    }
  }

  html() {
    const nonce = String(Date.now());
    const profileJson = JSON.stringify(this.profile);
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-editor-background); font: var(--vscode-font-size) var(--vscode-font-family); }
    header { display: flex; align-items: center; gap: 8px; padding: 10px 12px; border-bottom: 1px solid var(--vscode-panel-border); }
    h1 { font-size: 16px; margin: 0; flex: 1; }
    main { max-width: 1040px; padding: 14px; display: grid; gap: 14px; }
    section { border: 1px solid var(--vscode-panel-border); padding: 12px; }
    h2 { font-size: 13px; margin: 0 0 10px; }
    .grid { display: grid; grid-template-columns: repeat(2, minmax(220px, 1fr)); gap: 10px; }
    label { display: grid; gap: 4px; }
    input, select, textarea { color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); padding: 5px 7px; box-sizing: border-box; }
    textarea { width: 100%; min-height: 240px; font-family: var(--vscode-editor-font-family), monospace; }
    button { padding: 5px 12px; }
    .hint, #status { color: var(--vscode-descriptionForeground); }
    .danger { color: var(--vscode-errorForeground); }
  </style>
</head>
<body>
  <header>
    <h1>${this.isNew ? 'New Connection Profile' : 'Edit Connection Profile'}</h1>
    <button id="test">Test Host</button>
    <button id="save">Save</button>
  </header>
  <main>
    <section>
      <h2>General</h2>
      <div class="grid">
        <label>Name <input id="displayName"></label>
        <label>Protocol <select id="protocol"><option>ssh</option><option>telnet</option><option>raw</option><option>github</option><option>vnc</option><option>irc</option></select></label>
        <label>Host <input id="host"></label>
        <label>Port <input id="port" type="number" min="0" max="65535"></label>
        <label>Username <input id="username"></label>
        <label>Folder <input id="folder"></label>
        <label>Tags <input id="tags" placeholder="comma separated"></label>
        <label>Description <input id="description"></label>
        <label><span>Favourite</span><input id="favourite" type="checkbox"></label>
      </div>
    </section>
    <section>
      <h2>Terminal Defaults</h2>
      <div class="grid">
        <label>Font family <input id="terminalFontFamily"></label>
        <label>Font size <input id="terminalFontSize" type="number" min="6" max="48"></label>
        <label>Scrollback <input id="terminalScrollback" type="number" min="0"></label>
        <label>Cursor <select id="terminalCursorStyle"><option>block</option><option>underline</option><option>bar</option></select></label>
      </div>
    </section>
    <section>
      <h2>Protocol Settings</h2>
      <p class="hint">Edit the active protocol block as JSON. This preserves full Nexus support for SSH jumps, port forwards, VNC tunnels, IRC auto-join lists and RAW presets without hiding advanced fields.</p>
      <textarea id="protocolConfig" spellcheck="false"></textarea>
    </section>
    <section>
      <h2>Secrets</h2>
      <p class="hint">Leave blank to keep existing secrets unchanged. Values are sent to the backend secret store, not saved inside the profile JSON.</p>
      <div class="grid">
        <label>Password / token <input id="secretPrimary" type="password"></label>
        <label>Key passphrase / secondary <input id="secretSecondary" type="password"></label>
        <label>TOTP secret <input id="secretTotp" type="password"></label>
      </div>
    </section>
    <section>
      <h2>Status</h2>
      <div id="status" class="hint">Ready</div>
    </section>
  </main>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    let profile = ${profileJson};
    const ids = ['displayName','protocol','host','port','username','folder','tags','description','favourite','terminalFontFamily','terminalFontSize','terminalScrollback','terminalCursorStyle'];
    function setStatus(text, error) {
      const el = document.getElementById('status');
      el.textContent = text || '';
      el.className = error ? 'danger' : 'hint';
    }
    function activeConfigKey() {
      const p = document.getElementById('protocol').value;
      return p === 'ssh' ? 'ssh' : p;
    }
    function fill(p) {
      profile = p;
      document.getElementById('displayName').value = p.displayName || '';
      document.getElementById('protocol').value = p.protocol || 'ssh';
      document.getElementById('host').value = p.host || '';
      document.getElementById('port').value = p.port || 0;
      document.getElementById('username').value = p.username || '';
      document.getElementById('folder').value = p.folder || '';
      document.getElementById('tags').value = Array.isArray(p.tags) ? p.tags.join(', ') : '';
      document.getElementById('description').value = p.description || '';
      document.getElementById('favourite').checked = !!p.favourite;
      document.getElementById('terminalFontFamily').value = p.terminalFontFamily || '';
      document.getElementById('terminalFontSize').value = p.terminalFontSize || 14;
      document.getElementById('terminalScrollback').value = p.terminalScrollback || 10000;
      document.getElementById('terminalCursorStyle').value = p.terminalCursorStyle || 'block';
      const key = activeConfigKey();
      document.getElementById('protocolConfig').value = JSON.stringify(p[key] || {}, null, 2);
    }
    function common() {
      return Object.fromEntries(ids.map(id => [id, document.getElementById(id).type === 'checkbox' ? document.getElementById(id).checked : document.getElementById(id).value]));
    }
    function collect() {
      const c = common();
      const p = { ...profile };
      p.displayName = c.displayName;
      p.protocol = c.protocol;
      p.host = c.host;
      p.port = Number(c.port || 0);
      p.username = c.username;
      p.folder = c.folder;
      p.tags = String(c.tags || '').split(',').map(s => s.trim()).filter(Boolean);
      p.description = c.description;
      p.favourite = !!c.favourite;
      p.terminalFontFamily = c.terminalFontFamily;
      p.terminalFontSize = Number(c.terminalFontSize || 14);
      p.terminalScrollback = Number(c.terminalScrollback || 10000);
      p.terminalCursorStyle = c.terminalCursorStyle || 'block';
      p[activeConfigKey()] = JSON.parse(document.getElementById('protocolConfig').value || '{}');
      return p;
    }
    document.getElementById('protocol').addEventListener('change', () => {
      vscode.postMessage({ type: 'protocol', protocol: document.getElementById('protocol').value, common: common() });
    });
    document.getElementById('save').onclick = () => {
      try {
        vscode.postMessage({
          type: 'save',
          profile: collect(),
          secrets: {
            primary: document.getElementById('secretPrimary').value,
            secondary: document.getElementById('secretSecondary').value,
            totp: document.getElementById('secretTotp').value
          }
        });
      } catch (err) {
        setStatus(err.message, true);
      }
    };
    document.getElementById('test').onclick = () => vscode.postMessage({ type: 'test', host: document.getElementById('host').value, port: Number(document.getElementById('port').value || 0) });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'profile') fill(msg.profile);
      if (msg.type === 'error') setStatus(msg.error, true);
      if (msg.type === 'testResult') setStatus(JSON.stringify(msg.result));
    });
    fill(profile);
  </script>
</body>
</html>`;
  }
}

function parseTags(value) {
  if (Array.isArray(value)) return value;
  return String(value || '').split(',').map(tag => tag.trim()).filter(Boolean);
}

function buildProfileFromEditor(profile, original) {
  const merged = { ...original, ...profile };
  merged.tags = parseTags(merged.tags);
  if (!String(merged.displayName || '').trim()) {
    merged.displayName = merged.host || `${merged.protocol || 'nexus'} connection`;
  }
  merged.port = Number(merged.port || 0);
  merged.connectTimeout = Number(merged.connectTimeout || 10);
  merged.readTimeout = Number(merged.readTimeout || 0);
  merged.terminalFontSize = Number(merged.terminalFontSize || 14);
  merged.terminalScrollback = Number(merged.terminalScrollback || 10000);
  return merged;
}

async function saveProfileSecrets(bridge, profileID, secrets) {
  const profile = await bridge.rpc('GetProfile', [profileID]);
  const protocol = String(profile.protocol || '').toLowerCase();
  const writes = [];
  if (secrets.primary) {
    if (protocol === 'github') writes.push(['github-token', secrets.primary]);
    else if (protocol === 'vnc') writes.push(['vnc-password', secrets.primary]);
    else if (protocol === 'irc') writes.push(['irc-server-password', secrets.primary]);
    else writes.push(['password', secrets.primary]);
  }
  if (secrets.secondary) {
    if (protocol === 'irc') writes.push(['irc-nickserv-password', secrets.secondary]);
    else if (protocol === 'vnc') writes.push(['vnc-ssh-passphrase', secrets.secondary]);
    else writes.push(['passphrase', secrets.secondary]);
  }
  if (secrets.totp) writes.push(['totp', secrets.totp]);
  for (const [kind, value] of writes) {
    await bridge.rpc('SetProfileSecret', [profileID, kind, value]);
  }
}

class SettingsPanel {
  constructor(context, bridge) {
    this.context = context;
    this.bridge = bridge;
    this.panel = vscode.window.createWebviewPanel(
      'nexusSettings',
      'Nexus Settings',
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true }
    );
    this.panel.webview.html = this.html();
    this.panel.webview.onDidReceiveMessage(msg => this.onMessage(msg));
    this.load();
  }

  async load() {
    const [prefs, knownHosts, appInfo, defaultShell] = await Promise.all([
      this.bridge.rpc('GetPreferences'),
      this.bridge.rpc('ListKnownHosts').catch(() => []),
      this.bridge.rpc('GetAppInfo').catch(() => ({})),
      this.bridge.rpc('GetDefaultShell').catch(() => '')
    ]);
    this.panel.webview.postMessage({ type: 'state', prefs, knownHosts, appInfo, defaultShell });
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'save') {
        await this.bridge.rpc('SavePreferences', [normalisePrefs(msg.prefs)]);
        vscode.window.showInformationMessage('Nexus settings saved');
        await this.load();
      }
      if (msg.type === 'deleteKnownHost') {
        await this.bridge.rpc('DeleteKnownHost', [msg.id]);
        await this.load();
      }
    } catch (err) {
      this.panel.webview.postMessage({ type: 'error', error: err.message || String(err) });
    }
  }

  html() {
    const nonce = String(Date.now());
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-editor-background); font: var(--vscode-font-size) var(--vscode-font-family); }
    header { display: flex; gap: 8px; align-items: center; padding: 10px 12px; border-bottom: 1px solid var(--vscode-panel-border); }
    h1 { font-size: 16px; margin: 0; flex: 1; }
    main { max-width: 980px; padding: 14px; display: grid; gap: 14px; }
    section { border: 1px solid var(--vscode-panel-border); padding: 12px; }
    h2 { margin: 0 0 10px; font-size: 13px; }
    .grid { display: grid; grid-template-columns: repeat(2, minmax(220px, 1fr)); gap: 10px; }
    label { display: grid; gap: 4px; }
    input, select { color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); padding: 5px 7px; }
    table { width: 100%; border-collapse: collapse; }
    td, th { border: 1px solid var(--vscode-panel-border); padding: 5px 7px; text-align: left; }
    .hint, #status { color: var(--vscode-descriptionForeground); }
    .danger { color: var(--vscode-errorForeground); }
  </style>
</head>
<body>
  <header><h1>Nexus Settings</h1><button id="save">Save</button></header>
  <main>
    <section>
      <h2>General</h2>
      <div class="grid">
        <label>Theme <select id="theme"><option>dark</option><option>light</option></select></label>
        <label>Default protocol <select id="defaultProtocol"><option>ssh</option><option>telnet</option><option>raw</option><option>github</option><option>vnc</option><option>irc</option></select></label>
        <label><span>Confirm on close</span><input id="confirmOnClose" type="checkbox"></label>
        <label><span>Confirm on disconnect</span><input id="confirmOnDisconnect" type="checkbox"></label>
        <label><span>Show status bar</span><input id="showStatusBar" type="checkbox"></label>
        <label><span>Use native paste</span><input id="useNativePaste" type="checkbox"></label>
      </div>
    </section>
    <section>
      <h2>Terminal</h2>
      <div class="grid">
        <label>Terminal theme <select id="terminalTheme"><option>charcoal</option><option>solarized-dark</option><option>solarized-light</option><option>monokai</option><option>dracula</option></select></label>
        <label>Default font family <input id="defaultFontFamily"></label>
        <label>Default font size <input id="defaultFontSize" type="number" min="6" max="48"></label>
        <label>Default scrollback <input id="defaultScrollback" type="number" min="0"></label>
        <label>Default cursor <select id="defaultCursorStyle"><option>block</option><option>underline</option><option>bar</option></select></label>
        <label>Local shell <input id="localShell"></label>
      </div>
      <p class="hint" id="defaultShell"></p>
    </section>
    <section>
      <h2>Known Hosts</h2>
      <table><thead><tr><th>Host</th><th>Key</th><th>Fingerprint</th><th></th></tr></thead><tbody id="knownHosts"></tbody></table>
    </section>
    <section>
      <h2>About</h2>
      <pre id="about" class="hint"></pre>
      <div id="status" class="hint">Loading</div>
    </section>
  </main>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    let prefs = {};
    const fields = ['theme','terminalTheme','defaultProtocol','confirmOnClose','confirmOnDisconnect','showStatusBar','defaultFontFamily','defaultFontSize','defaultScrollback','defaultCursorStyle','useNativePaste','localShell'];
    function setStatus(text, error) {
      const el = document.getElementById('status');
      el.textContent = text || '';
      el.className = error ? 'danger' : 'hint';
    }
    function fill(state) {
      prefs = state.prefs || {};
      for (const id of fields) {
        const el = document.getElementById(id);
        if (!el) continue;
        if (el.type === 'checkbox') el.checked = !!prefs[id];
        else el.value = prefs[id] == null ? '' : prefs[id];
      }
      document.getElementById('defaultShell').textContent = state.defaultShell ? 'System default shell: ' + state.defaultShell : '';
      document.getElementById('about').textContent = JSON.stringify(state.appInfo || {}, null, 2);
      const tbody = document.getElementById('knownHosts');
      tbody.textContent = '';
      for (const host of state.knownHosts || []) {
        const tr = document.createElement('tr');
        tr.innerHTML = '<td>' + esc(host.host) + ':' + esc(host.port) + '</td><td>' + esc(host.keyType) + '</td><td>' + esc(host.fingerprint) + '</td><td><button data-id="' + esc(host.id) + '">Delete</button></td>';
        tr.querySelector('button').onclick = () => vscode.postMessage({ type: 'deleteKnownHost', id: host.id });
        tbody.appendChild(tr);
      }
      setStatus('Ready');
    }
    function collect() {
      const out = { ...prefs };
      for (const id of fields) {
        const el = document.getElementById(id);
        out[id] = el.type === 'checkbox' ? el.checked : el.value;
      }
      out.defaultFontSize = Number(out.defaultFontSize || 14);
      out.defaultScrollback = Number(out.defaultScrollback || 10000);
      return out;
    }
    function esc(value) { return String(value == null ? '' : value).replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch])); }
    document.getElementById('save').onclick = () => vscode.postMessage({ type: 'save', prefs: collect() });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'state') fill(msg);
      if (msg.type === 'error') setStatus(msg.error, true);
    });
  </script>
</body>
</html>`;
  }
}

function normalisePrefs(prefs) {
  return {
    ...prefs,
    sidebarWidth: Number(prefs.sidebarWidth || 280),
    defaultFontSize: Number(prefs.defaultFontSize || 14),
    defaultScrollback: Number(prefs.defaultScrollback || 10000)
  };
}

class SFTPPanel {
  constructor(context, bridge, session) {
    this.context = context;
    this.bridge = bridge;
    this.session = session;
    this.cwd = '/';
    this.panel = vscode.window.createWebviewPanel(
      'nexusSftp',
      `SFTP: ${session.tabName || session.profileName || session.host || 'Session'}`,
      vscode.ViewColumn.Active,
      { enableScripts: true, retainContextWhenHidden: true }
    );
    this.panel.webview.html = this.html();
    this.panel.webview.onDidReceiveMessage(msg => this.onMessage(msg));
    this.init();
  }

  async init() {
    try {
      this.cwd = await this.bridge.rpc('SFTPPwd', [this.session.id]);
    } catch (_) {
      this.cwd = '/';
    }
    await this.list(this.cwd || '/');
  }

  async list(remotePath) {
    const entries = await this.bridge.rpc('SFTPList', [this.session.id, remotePath || '/']);
    this.cwd = remotePath || '/';
    this.panel.webview.postMessage({ type: 'entries', cwd: this.cwd, entries });
  }

  async onMessage(msg) {
    try {
      if (msg.type === 'list') await this.list(msg.path || this.cwd);
      if (msg.type === 'mkdir') {
        const name = await vscode.window.showInputBox({ title: 'New remote folder', prompt: 'Folder name or absolute path', ignoreFocusOut: true });
        if (!name) return;
        await this.bridge.rpc('SFTPMkdir', [this.session.id, remoteJoin(this.cwd, name)]);
        await this.list(this.cwd);
      }
      if (msg.type === 'delete') {
        const ok = await vscode.window.showWarningMessage(`Delete ${msg.path}?`, { modal: true }, 'Delete');
        if (ok !== 'Delete') return;
        await this.bridge.rpc('SFTPRemove', [this.session.id, msg.path]);
        await this.list(this.cwd);
      }
      if (msg.type === 'rename') {
        const next = await vscode.window.showInputBox({ title: 'Rename remote path', value: msg.path, ignoreFocusOut: true });
        if (!next || next === msg.path) return;
        await this.bridge.rpc('SFTPRename', [this.session.id, msg.path, next]);
        await this.list(this.cwd);
      }
      if (msg.type === 'download') {
        const defaultName = remoteBase(msg.path);
        const picked = await vscode.window.showSaveDialog({ defaultUri: vscode.Uri.file(defaultName) });
        if (!picked) return;
        await this.bridge.rpc('SFTPDownloadToPath', [this.session.id, msg.path, picked.fsPath]);
        vscode.window.showInformationMessage(`Downloaded ${msg.path}`);
      }
      if (msg.type === 'upload') {
        const picked = await vscode.window.showOpenDialog({ canSelectFiles: true, canSelectFolders: false, canSelectMany: true });
        if (!picked || !picked.length) return;
        for (const file of picked) {
          await this.bridge.rpc('SFTPUploadPath', [this.session.id, this.cwd, file.fsPath]);
        }
        await this.list(this.cwd);
      }
    } catch (err) {
      this.panel.webview.postMessage({ type: 'error', error: err.message || String(err) });
    }
  }

  html() {
    const nonce = String(Date.now());
    return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';">
  <style>
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-editor-background); font: var(--vscode-font-size) var(--vscode-font-family); }
    header { display: flex; gap: 8px; align-items: center; padding: 8px; border-bottom: 1px solid var(--vscode-panel-border); }
    #path { flex: 1; color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); padding: 5px 7px; }
    button { padding: 4px 10px; }
    table { width: 100%; border-collapse: collapse; }
    th, td { border-bottom: 1px solid var(--vscode-panel-border); padding: 6px 8px; text-align: left; vertical-align: middle; }
    th { position: sticky; top: 0; background: var(--vscode-editor-background); z-index: 1; }
    tr:hover { background: var(--vscode-list-hoverBackground); }
    .name { cursor: pointer; }
    .muted, #status { color: var(--vscode-descriptionForeground); }
    .danger { color: var(--vscode-errorForeground); }
    main { height: calc(100vh - 43px); overflow: auto; }
  </style>
</head>
<body>
  <header>
    <button id="up">Up</button>
    <input id="path" value="/">
    <button id="go">Go</button>
    <button id="mkdir">New Folder</button>
    <button id="upload">Upload</button>
    <button id="refresh">Refresh</button>
  </header>
  <main>
    <table>
      <thead><tr><th>Name</th><th>Size</th><th>Mode</th><th>Modified</th><th>Actions</th></tr></thead>
      <tbody id="rows"></tbody>
    </table>
    <p id="status" class="muted"></p>
  </main>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    let cwd = '/';
    let entries = [];
    function esc(value) { return String(value == null ? '' : value).replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch])); }
    function parent(path) {
      if (!path || path === '/') return '/';
      const trimmed = path.replace(/\\/+$/, '');
      const idx = trimmed.lastIndexOf('/');
      return idx <= 0 ? '/' : trimmed.slice(0, idx);
    }
    function size(n) {
      n = Number(n || 0);
      if (n < 1024) return n + ' B';
      if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
      if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
      return (n / 1073741824).toFixed(1) + ' GB';
    }
    function setStatus(text, error) {
      const el = document.getElementById('status');
      el.textContent = text || '';
      el.className = error ? 'danger' : 'muted';
    }
    function render() {
      document.getElementById('path').value = cwd;
      const rows = document.getElementById('rows');
      rows.textContent = '';
      const sorted = [...entries].sort((a, b) => Number(b.isDir) - Number(a.isDir) || String(a.name).localeCompare(String(b.name)));
      for (const entry of sorted) {
        const tr = document.createElement('tr');
        tr.innerHTML = '<td class="name">' + (entry.isDir ? '▸ ' : '') + esc(entry.name) + '</td><td>' + (entry.isDir ? '' : size(entry.size)) + '</td><td class="muted">' + esc(entry.mode || '') + '</td><td class="muted">' + esc(entry.modifiedAt || '') + '</td><td><button data-download>Download</button> <button data-rename>Rename</button> <button data-delete>Delete</button></td>';
        tr.querySelector('.name').onclick = () => entry.isDir ? vscode.postMessage({ type: 'list', path: entry.path }) : vscode.postMessage({ type: 'download', path: entry.path });
        tr.querySelector('[data-download]').disabled = !!entry.isDir;
        tr.querySelector('[data-download]').onclick = () => vscode.postMessage({ type: 'download', path: entry.path });
        tr.querySelector('[data-rename]').onclick = () => vscode.postMessage({ type: 'rename', path: entry.path });
        tr.querySelector('[data-delete]').onclick = () => vscode.postMessage({ type: 'delete', path: entry.path });
        rows.appendChild(tr);
      }
      setStatus(sorted.length + ' entries');
    }
    document.getElementById('go').onclick = () => vscode.postMessage({ type: 'list', path: document.getElementById('path').value || '/' });
    document.getElementById('path').addEventListener('keydown', e => { if (e.key === 'Enter') vscode.postMessage({ type: 'list', path: e.target.value || '/' }); });
    document.getElementById('up').onclick = () => vscode.postMessage({ type: 'list', path: parent(cwd) });
    document.getElementById('mkdir').onclick = () => vscode.postMessage({ type: 'mkdir' });
    document.getElementById('upload').onclick = () => vscode.postMessage({ type: 'upload' });
    document.getElementById('refresh').onclick = () => vscode.postMessage({ type: 'list', path: cwd });
    window.addEventListener('message', event => {
      const msg = event.data || {};
      if (msg.type === 'entries') { cwd = msg.cwd || '/'; entries = msg.entries || []; render(); }
      if (msg.type === 'error') setStatus(msg.error, true);
    });
  </script>
</body>
</html>`;
  }
}

function remoteJoin(base, name) {
  if (!name) return base || '/';
  if (name.startsWith('/')) return name;
  return `${(base || '/').replace(/\/+$/, '')}/${name}`.replace(/^\/\//, '/');
}

function remoteBase(remotePath) {
  const parts = String(remotePath || '').split('/').filter(Boolean);
  return parts.length ? parts[parts.length - 1] : 'download';
}

async function promptRuntimeEvents(bridge) {
  bridge.events.event(async evt => {
    const payload = evt.payload || {};
    if (evt.name === 'session:secret:prompt') {
      const value = await vscode.window.showInputBox({
        title: payload.title || 'Credentials Required',
        prompt: payload.message || 'Enter the requested secret.',
        password: true,
        ignoreFocusOut: true
      });
      await bridge.runtimeEvent('session:secret:response', {
        requestID: payload.requestID,
        value: value || '',
        cancelled: value === undefined
      });
    }
    if (evt.name === 'session:hostkey:verify') {
      const accepted = await vscode.window.showWarningMessage(
        `Trust host key for ${payload.host} (${payload.keyType}, ${payload.fingerprint})?`,
        { modal: true },
        'Trust'
      );
      await bridge.runtimeEvent('session:hostkey:response', {
        requestID: payload.requestID,
        sessionID: payload.sessionID,
        accepted: accepted === 'Trust'
      });
    }
  });
}

async function activate(context) {
  const bridge = new Bridge(context);
  await bridge.start();
  promptRuntimeEvents(bridge);

  const provider = new ConnectionsProvider(context, bridge);
  context.subscriptions.push(vscode.window.registerWebviewViewProvider('nexus.connections', provider));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.refreshConnections', () => provider.refresh()));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.newProfile', async () => {
    const protocol = await vscode.window.showQuickPick(['ssh', 'telnet', 'raw', 'github', 'vnc', 'irc'], {
      title: 'New Nexus Profile',
      placeHolder: 'Choose a connection protocol'
    });
    if (!protocol) return;
    const profile = await bridge.rpc('NewProfile', [protocol]);
    new ProfileEditorPanel(context, bridge, provider, profile, true);
  }));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.editProfile', async item => {
    const profile = await resolveProfileArg(bridge, item);
    if (profile) new ProfileEditorPanel(context, bridge, provider, profile, false);
  }));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.duplicateProfile', async item => {
    const profile = await resolveProfileArg(bridge, item);
    if (!profile) return;
    const copy = await bridge.rpc('DuplicateProfile', [profile.id]);
    await provider.refresh();
    new ProfileEditorPanel(context, bridge, provider, copy, false);
  }));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.deleteProfile', async item => {
    const profile = await resolveProfileArg(bridge, item);
    if (!profile) return;
    const label = profile.displayName || profile.host || profile.id;
    const ok = await vscode.window.showWarningMessage(`Delete Nexus profile "${label}"?`, { modal: true }, 'Delete');
    if (ok !== 'Delete') return;
    await bridge.rpc('DeleteProfile', [profile.id]);
    await provider.refresh();
  }));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.openSettings', () => {
    new SettingsPanel(context, bridge);
  }));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.connectLocal', async () => {
    const session = await bridge.rpc('ConnectLocal');
    openSessionPanel(context, bridge, session, { protocol: 'local' });
  }));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.openConnection', async profileOrItem => {
    const profile = profileOrItem.profile || profileOrItem;
    const session = await bridge.rpc('ConnectSession', [profile.id]);
    openSessionPanel(context, bridge, session, profile);
  }));
  context.subscriptions.push(vscode.commands.registerCommand('nexus.openSftp', async profileOrItem => {
    const profile = await resolveProfileArg(bridge, profileOrItem);
    if (!profile) return;
    if (String(profile.protocol).toLowerCase() !== 'ssh') {
      vscode.window.showErrorMessage('SFTP is available for SSH profiles.');
      return;
    }
    const session = await bridge.rpc('ConnectSession', [profile.id]);
    new SFTPPanel(context, bridge, session);
  }));
  provider.refresh();
}

async function resolveProfileArg(bridge, item) {
  const candidate = item && (item.profile || item);
  if (candidate && candidate.id) return bridge.rpc('GetProfile', [candidate.id]);
  const profiles = await bridge.rpc('ListProfiles');
  const picked = await vscode.window.showQuickPick(
    profiles.map(profile => ({
      label: profile.displayName || profile.host || profile.id,
      description: profile.protocol,
      detail: `${profile.username ? profile.username + '@' : ''}${profile.host || ''}`,
      profile
    })),
    { title: 'Select Nexus Profile' }
  );
  return picked ? picked.profile : null;
}

function deactivate() {}

module.exports = { activate, deactivate };
