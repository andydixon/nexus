#!/usr/bin/env node
const assert = require("assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const frontendRoot = path.resolve(__dirname, "..");

function loadScript(relativePath, additions = {}) {
  const context = {
    console,
    Promise,
    Map,
    Set,
    setTimeout,
    clearTimeout,
    window: { NexusTerminal: {} },
    document: {
      querySelector: () => null,
      addEventListener: () => {},
    },
    ...additions,
  };
  vm.createContext(context);
  vm.runInContext(
    fs.readFileSync(path.join(frontendRoot, relativePath), "utf8"),
    context,
    { filename: relativePath },
  );
  return context;
}

async function testRepeatedSplitCleanup() {
  const context = loadScript("src/js/splitpane.js");
  const SplitPaneManager = context.window.NexusTerminal.SplitPaneManager;
  const closed = [];
  const app = {
    closeSessionImmediate: async (sessionId, options) => {
      closed.push({ sessionId, options });
    },
  };
  const manager = new SplitPaneManager(app);
  let cancelledDrags = 0;

  for (let i = 0; i < 500; i++) {
    const ownerId = `owner-${i}`;
    const childId = `child-${i}`;
    const ownerEl = { _splitNode: {} };
    const childEl = { _splitNode: {} };
    const ownerLeaf = {
      type: "leaf",
      paneId: `owner-pane-${i}`,
      el: ownerEl,
      sessionId: ownerId,
      parent: null,
    };
    const childLeaf = {
      type: "leaf",
      paneId: `child-pane-${i}`,
      el: childEl,
      sessionId: childId,
      parent: null,
    };
    const branchEl = { _splitNode: {} };
    const branch = {
      type: "branch",
      first: ownerLeaf,
      second: childLeaf,
      el: branchEl,
      parent: null,
      cancelDrag: () => {
        cancelledDrags++;
      },
    };
    ownerLeaf.parent = branch;
    childLeaf.parent = branch;
    manager.trees.set(ownerId, branch);
    manager.pendingConnections.set(`pending-${i}`, {
      ownerSessionId: ownerId,
      cancelled: false,
      completed: false,
    });

    await manager.cleanup(ownerId);

    assert.strictEqual(ownerEl._splitNode, null);
    assert.strictEqual(childEl._splitNode, null);
    assert.strictEqual(branchEl._splitNode, null);
  }

  assert.strictEqual(manager.trees.size, 0);
  assert.strictEqual(manager.pendingConnections.size, 0);
  assert.strictEqual(closed.length, 500);
  assert.strictEqual(cancelledDrags, 500);
  for (const call of closed) {
    assert.strictEqual(call.options.removeTab, false);
    assert.strictEqual(call.options.cleanupSplits, false);
  }
}

function testClosedPaneRejectsLateConnection() {
  const context = loadScript("src/js/splitpane.js");
  const SplitPaneManager = context.window.NexusTerminal.SplitPaneManager;
  const manager = new SplitPaneManager({ closeSessionImmediate: async () => {} });
  const pane = {
    dataset: { paneId: "pane-1" },
    isConnected: true,
  };
  const leaf = {
    type: "leaf",
    paneId: "pane-1",
    el: pane,
    sessionId: null,
    parent: null,
  };
  pane._splitNode = leaf;
  manager.trees.set("owner-1", leaf);

  const token = manager._beginPendingConnection(pane, "owner-1");
  manager._cancelPendingConnection("pane-1");

  assert.strictEqual(
    manager._completePendingConnection(pane, "owner-1", token),
    false,
  );
  assert.strictEqual(manager.pendingConnections.size, 0);
}

function testPerSessionStoresReleaseIDs() {
  const stateContext = loadScript("src/js/state.js");
  const State = stateContext.window.NexusTerminal.State;
  const state = new State();
  state.addSession({ id: "session-1" });
  state.broadcastTargets = new Set(["session-1"]);
  state.removeSession("session-1");
  assert.strictEqual(state.sessions.size, 0);
  assert.strictEqual(state.broadcastTargets.size, 0);

  const recordingContext = loadScript("src/js/recordings.js");
  const RecordingManager =
    recordingContext.window.NexusTerminal.RecordingManager;
  const recordings = new RecordingManager({});
  recordings.activeSessions.add("session-1");
  recordings.cleanup("session-1");
  assert.strictEqual(recordings.activeSessions.size, 0);
}

async function testCompleteAppTeardownIsImmediateAndIdempotent() {
  let finishBackendClose;
  let backendCloses = 0;
  const context = loadScript("src/js/app.js");
  const App = context.window.NexusTerminal.App;
  const app = Object.create(App.prototype);
  const resources = {
    sftp: new Set(["session-1"]),
    triggers: new Set(["session-1"]),
    recordings: new Set(["session-1"]),
    terminals: new Set(["session-1"]),
    raw: new Set(["session-1"]),
    github: new Set(["session-1"]),
    vnc: new Set(["session-1"]),
    irc: new Set(["session-1"]),
    leviathan: new Set(["session-1"]),
  };
  const drop = (name) => ({
    cleanup: (id) => resources[name].delete(id),
    close: (id) => resources[name].delete(id),
    destroyTerminal: (id) => resources[name].delete(id),
    destroyViewer: (id) => resources[name].delete(id),
    destroyPanel: (id) => resources[name].delete(id),
  });

  app._closingSessions = new Map();
  app.splitPanes = { cleanup: async () => {} };
  app.sftp = drop("sftp");
  app.triggers = drop("triggers");
  app.recordings = drop("recordings");
  app.terminals = drop("terminals");
  app.rawViewer = drop("raw");
  app.leviathan = drop("leviathan");
  app.tabs = {
    order: ["session-1"],
    removeTab: (id) => {
      app.tabs.order = app.tabs.order.filter((value) => value !== id);
    },
  };
  app.state = {
    sessions: new Map([["session-1", { id: "session-1" }]]),
    getSession(id) {
      return this.sessions.get(id);
    },
    removeSession(id) {
      this.sessions.delete(id);
    },
  };
  app.updateStatusBar = () => {};

  context.window.NexusTerminal.GitHubViewer = drop("github");
  context.window.NexusTerminal.VNCViewer = drop("vnc");
  context.window.NexusTerminal.IRCViewer = drop("irc");
  context.window.NexusTerminal.wailsService = {
    DisconnectSession: () => {
      backendCloses++;
      return new Promise((resolve) => {
        finishBackendClose = resolve;
      });
    },
  };

  const first = app.closeSessionImmediate("session-1");
  const second = app.closeSessionImmediate("session-1");
  assert.strictEqual(first, second);
  assert.strictEqual(backendCloses, 1);
  assert.strictEqual(app.tabs.order.length, 0);
  assert.strictEqual(app.state.sessions.size, 0);
  for (const retained of Object.values(resources)) {
    assert.strictEqual(retained.size, 0);
  }

  finishBackendClose();
  await first;
  assert.strictEqual(app._closingSessions.size, 0);
}

(async () => {
  await testRepeatedSplitCleanup();
  testClosedPaneRejectsLateConnection();
  testPerSessionStoresReleaseIDs();
  await testCompleteAppTeardownIsImmediateAndIdempotent();
  console.log("Session lifecycle regression tests passed.");
})().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
