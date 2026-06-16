(function () {
  const { createElement, escapeHtml, formatBytes, generateId } =
    window.NexusTerminal;

  const tools = [
    {
      group: "Editor",
      id: "hex",
      name: "Hex Editor",
      ico: "HX",
      desc: "Editable hex view over the loaded working copy.",
    },
    {
      group: "Triage",
      id: "overview",
      name: "Overview",
      ico: "OV",
      desc: "First-pass summary across hashes, entropy, strings, IOCs and carve candidates.",
    },
    { group: "Triage", id: "hashes", name: "Hashes", ico: "#", desc: "CRC32, MD5, SHA-1, SHA-256 and SHA-512." },
    { group: "Triage", id: "entropy", name: "Entropy", ico: "EN", desc: "Windowed entropy for packed or encrypted regions." },
    { group: "Triage", id: "strings", name: "Strings", ico: "ST", desc: "ASCII and UTF-16 string extraction." },
    { group: "Triage", id: "iocs", name: "IOCs", ico: "IO", desc: "URLs, domains, paths, hashes and other indicators." },
    { group: "Find", id: "search", name: "Search", ico: "SR", desc: "Single text, hex or regular-expression search." },
    { group: "Find", id: "multi-search", name: "Multi-search", ico: "MS", desc: "Batch text, hex and regex searches." },
    { group: "Structure", id: "parse", name: "Structures", ico: "PX", desc: "Parsers for PE, ELF, Mach-O, ZIP, images, RIFF, ASN.1 and certs." },
    { group: "Structure", id: "disasm", name: "Disasm", ico: "DA", desc: "Heuristic disassembly for common CPU families." },
    { group: "Structure", id: "pdf", name: "PDF", ico: "PDF", desc: "PDF object, stream, xref and suspicious-surface inspection." },
    { group: "Structure", id: "squashfs", name: "SquashFS", ico: "SQ", desc: "SquashFS superblock and directory metadata." },
    { group: "Audio", id: "spectrogram-encode", name: "Spectrogram Encode", ico: "SE", desc: "Convert an image into WAV audio, or hide an image inside a carrier WAV/MP3." },
    { group: "Audio", id: "spectrogram-view", name: "Spectrogram View", ico: "SV", desc: "Render WAV or MP3 audio as a spectrogram image." },
    { group: "Compare", id: "diff", name: "Diff", ico: "DF", desc: "Changed byte regions against a compare file." },
    { group: "Recover", id: "carve", name: "Carve", ico: "CV", desc: "Embedded file carving candidates." },
    { group: "Recover", id: "keys", name: "Keys", ico: "KY", desc: "PEM, DER, OpenSSH and PKCS#12 artefact discovery." },
    { group: "Recover", id: "timestamps", name: "Timestamps", ico: "TS", desc: "UNIX, FILETIME, WebKit and OLE timestamp heuristics." },
    { group: "Recover", id: "stego", name: "Stego", ico: "SG", desc: "Appended payloads and embedded trailer formats." },
    { group: "Recover", id: "metadata", name: "Metadata", ico: "MD", desc: "PNG, JPEG and GIF textual metadata extraction." },
    { group: "Advanced", id: "binsnoop", name: "Binsnoop", ico: "BS", desc: "Deep signature scanning through the Binsnoop engine." },
    { group: "Advanced", id: "rules", name: "Rules", ico: "RL", desc: "YARA-like rule scans over the current document." },
    { group: "Edit", id: "patch-plan", name: "Patch Plan", ico: "PP", desc: "Validate patch scripts without modifying the working copy." },
    { group: "Edit", id: "patch-apply", name: "Patch Apply", ico: "PA", desc: "Apply a patch script to the working copy." },
    { group: "Edit", id: "transform", name: "Transforms", ico: "TR", desc: "Hex, Base64, URL, XOR, inflate and cipher pipelines." },
    { group: "Case", id: "case", name: "Case Export", ico: "CE", desc: "Build a case archive payload from the current working copy." },
  ];

  const defaults = {
    search: { mode: "text", query: "MZ", max: 1000 },
    "multi-search": { spec: "text MZ\nhex 7f454c46\nregex https?://", max: 1000 },
    parse: { kind: "auto" },
    disasm: { arch: "x86_64", start: 0, length: 4096, max: 1000 },
    entropy: { window: 4096, step: 4096 },
    strings: { min: 4, max: 5000 },
    iocs: { min: 5 },
    carve: { max: 256 },
    keys: { max: 200 },
    timestamps: { max: 1000 },
    rules: { source: 'rule mz { strings: $a = "MZ" condition: $a }', max: 1000 },
    "patch-plan": { source: "set 0x00 90 90" },
    "patch-apply": { source: "set 0x00 90 90" },
    transform: { pipeline: "hex.encode", start: 0, length: 1024 },
    "spectrogram-view": { fftSize: 2048, height: 1024, maxWidth: 1400, aspectRatio: 1, minHz: 0, maxHz: 0, brightness: 1, contrast: 1 },
    hex: { start: 0, bytes: 2048, width: 16 },
  };

  class LeviathanWorkbench {
    constructor(app) {
      this.app = app;
      this.state = null;
      this.lastResults = new Map();
      this.autoRunners = new Map();
      this.hexSearches = new Map();
    }

    renderSidebar() {
      const el = document.getElementById("sidebar-profiles");
      if (!el) return;
      el.innerHTML = "";

      const actions = createElement("div", "leviathan-actions");
      const load = createElement("button", "btn btn-primary btn-sm", {
        type: "button",
        textContent: "Load",
      });
      const save = createElement("button", "btn btn-secondary btn-sm", {
        type: "button",
        textContent: "Save",
      });
      load.addEventListener("click", () => this.loadFile());
      save.addEventListener("click", () => this.saveFile());
      actions.appendChild(load);
      actions.appendChild(save);
      el.appendChild(actions);

      let lastGroup = "";
      for (const tool of tools) {
        if (tool.group !== lastGroup) {
          el.appendChild(
            createElement("div", "sidebar-section-header leviathan-group", {
              textContent: tool.group,
            }),
          );
          lastGroup = tool.group;
        }
        const item = createElement("div", "sidebar-item leviathan-tool", {
          dataset: { toolId: tool.id },
          title: tool.desc,
        });
        item.appendChild(createElement("span", "leviathan-tool-ico", { textContent: tool.ico }));
        const label = createElement("span", "sidebar-item-label", { textContent: tool.name });
        item.appendChild(label);
        item.addEventListener("click", () => this.openTool(tool.id));
        el.appendChild(item);
      }
      this._highlightActiveTool();
    }

    async loadFile() {
      try {
        const payload = await window.NexusTerminal.wailsService.LeviathanOpenFile();
        this.applyState(payload);
        this.refreshOpenHexTabs();
      } catch (err) {
        if (String(err || "").includes("cancel")) return;
        this.app.notifications.error("Load failed: " + err);
      }
    }

    async saveFile() {
      try {
        const path = await window.NexusTerminal.wailsService.LeviathanSaveWorking();
        if (path) this.app.notifications.success("Saved working copy");
        const state = await window.NexusTerminal.wailsService.LeviathanState();
        this.applyState(state);
      } catch (err) {
        this.app.notifications.error("Save failed: " + err);
      }
    }

    openTool(toolId, options = {}) {
      const tool = tools.find((t) => t.id === toolId) || tools[0];
      const existingId = options.reuseExisting
        ? this._findExistingToolSession(tool.id)
        : null;
      const sessionId = existingId || `leviathan-${tool.id}-${generateId(8)}`;

      if (!existingId) {
        const sessionInfo = {
          id: sessionId,
          profileId: `leviathan:${tool.id}`,
          profileName: "Leviathan",
          protocol: "leviathan",
          host: "local",
          port: 0,
          state: "connected",
          tabName: tool.name,
          toolId: tool.id,
          leviathanParams: options.params || null,
        };
        this.app.state.addSession(sessionInfo);
        this.app.tabs.addTab(sessionInfo);
        const panel = document.getElementById(`panel-${sessionId}`);
        if (panel) this.renderToolPanel(panel, sessionId, tool);
      } else {
        this.app.tabs.setActive(existingId);
      }
      this._highlightActiveTool(tool.id);
    }

    renderToolPanel(panel, sessionId, tool) {
      panel.classList.add("leviathan-panel");
      panel.innerHTML = "";

      const header = createElement("div", "leviathan-panel-header");
      const titleWrap = createElement("div", "leviathan-title-wrap");
      titleWrap.appendChild(createElement("span", "leviathan-tool-mark", { textContent: tool.ico }));
      const heading = createElement("div", "leviathan-heading");
      heading.appendChild(createElement("h2", "", { textContent: tool.name }));
      heading.appendChild(createElement("span", "leviathan-eyebrow", { textContent: this._stateLabel() }));
      titleWrap.appendChild(heading);
      header.appendChild(titleWrap);

      const headerActions = createElement("div", "leviathan-header-actions");
      const undo = createElement("button", "btn btn-sm", { textContent: "Undo" });
      const redo = createElement("button", "btn btn-sm", { textContent: "Redo" });
      const revert = createElement("button", "btn btn-sm", { textContent: "Revert" });
      undo.addEventListener("click", () => this._stateAction("LeviathanUndo", sessionId));
      redo.addEventListener("click", () => this._stateAction("LeviathanRedo", sessionId));
      revert.addEventListener("click", () => this._stateAction("LeviathanRevert", sessionId));
      headerActions.appendChild(undo);
      headerActions.appendChild(redo);
      headerActions.appendChild(revert);
      header.appendChild(headerActions);
      panel.appendChild(header);

      const body = createElement("div", "leviathan-panel-body");
      panel.appendChild(body);

      if (tool.id === "hex") {
        this.renderHexEditor(body, sessionId);
      } else if (tool.id === "search") {
        this.renderSearchTool(body, sessionId);
      } else if (tool.id === "spectrogram-encode") {
        this.renderSpectrogramEncode(body, sessionId);
      } else if (tool.id === "spectrogram-view") {
        this.renderSpectrogramView(body, sessionId);
      } else {
        this.renderToolRunner(body, sessionId, tool);
      }
    }

    renderHexEditor(body, sessionId) {
      const { controls, output } = this._createWorkbench(body, "leviathan-hex-workbench", { topBar: true });
      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const initial = this._sessionParams(sessionId);
      const start = this._input("start", String(initial.start ?? 0));
      const bytes = this._input("bytes", String(initial.bytes ?? initial.length ?? 2048));
      const width = this._selectInput("row", [
        ["16", "16 bytes"],
        ["32", "32 bytes"],
      ], String(initial.width || 16));
      const search = createElement("button", "btn btn-sm", { type: "button", textContent: "Search" });
      const prev = createElement("button", "btn btn-sm", { type: "button", textContent: "Prev" });
      const next = createElement("button", "btn btn-sm", { type: "button", textContent: "Next" });
      toolbar.append(start.row, bytes.row, width.row, search, prev, next);

      const status = createElement("div", "leviathan-status", { textContent: this._stateLabel() });
      const grid = createElement("div", "leviathan-hex-grid");
      controls.append(toolbar, status);
      output.appendChild(grid);

      const load = async () => {
        try {
          status.textContent = "Loading hex...";
          const result = await window.NexusTerminal.wailsService.LeviathanRunTool("hex", {
            start: Number(start.input.value || 0),
            bytes: Number(bytes.input.value || 2048),
            width: Number(width.input.value || 16),
          });
          this.lastResults.set(sessionId, result);
          this._renderHexRows(grid, result.rows || [], status, Number(width.input.value || 16), this._currentHexSearchHit(sessionId));
          status.textContent = this._stateLabel();
        } catch (err) {
          status.textContent = String(err);
        }
      };
      [start.input, bytes.input, width.input].forEach((input) => {
        input.addEventListener("change", load);
      });
      search.addEventListener("click", () => this._showHexSearchModal(sessionId, start.input, width.input, load));
      prev.addEventListener("click", () => this._gotoHexSearchHit(sessionId, -1, start.input, width.input, load));
      next.addEventListener("click", () => this._gotoHexSearchHit(sessionId, 1, start.input, width.input, load));
      load();
    }

    renderSearchTool(body, sessionId) {
      const { controls, output } = this._createWorkbench(body, "leviathan-compact-workbench", { topBar: true });
      const params = createElement("div", "leviathan-params");
      const mode = this._selectInput("mode", [
        ["text", "String"],
        ["hex", "Hex"],
        ["regex", "Regex"],
      ], "text");
      const query = this._input("query", "MZ");
      const start = this._input("start", "0");
      const length = this._input("length", "-1");
      const max = this._input("max", "1000");
      params.append(mode.row, query.row, start.row, length.row, max.row);

      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const search = createElement("button", "btn btn-sm btn-primary", { type: "button", textContent: "Search" });
      const exportBtn = createElement("button", "btn btn-sm", { type: "button", textContent: "Export JSON" });
      toolbar.append(search, exportBtn);
      controls.append(params, toolbar);

      const summary = createElement("div", "leviathan-summary");
      const results = createElement("div", "leviathan-results");
      output.append(summary, results);
      results.appendChild(createElement("div", "leviathan-empty", { textContent: "Search results will appear here." }));

      const runSearch = async () => {
        try {
          results.innerHTML = "";
          results.appendChild(createElement("div", "leviathan-empty", { textContent: "Searching..." }));
          const payload = {
            mode: mode.input.value,
            query: query.input.value,
            start: Number(start.input.value || 0),
            length: Number(length.input.value || -1),
            max: Number(max.input.value || 1000),
          };
          const out = await window.NexusTerminal.wailsService.LeviathanRunTool("search", payload);
          this.lastResults.set(sessionId, out);
          this._renderResult(summary, results, out, "search");
        } catch (err) {
          results.innerHTML = "";
          this._renderToolError(results, "search", err);
        }
      };
      search.addEventListener("click", runSearch);
      query.input.addEventListener("keydown", (event) => {
        if (event.key === "Enter") runSearch();
      });
      exportBtn.addEventListener("click", async () => {
        const out = this.lastResults.get(sessionId);
        if (!out) return;
        try {
          await window.NexusTerminal.wailsService.LeviathanExportJSON("search.json", out);
          this.app.notifications.success("Exported JSON");
        } catch (err) {
          this.app.notifications.error("Export failed: " + err);
        }
      });
      this.autoRunners.set(sessionId, runSearch);
      requestAnimationFrame(runSearch);
    }

    renderSpectrogramEncode(body, sessionId) {
      const { controls, output } = this._createWorkbench(body);
      const params = createElement("div", "leviathan-params");
      const fields = [
        this._paramInput(params, "durationSec", "Duration (s)", 5, { type: "number", min: "0.25", max: "60", step: "0.25" }),
        this._paramInput(params, "sampleRate", "Sample rate", 44100, { type: "number", min: "8000", max: "96000", step: "1000" }),
        this._paramInput(params, "minHz", "Min frequency", 200, { type: "number", min: "20", step: "10" }),
        this._paramInput(params, "maxHz", "Max frequency", 20000, { type: "number", min: "120", step: "10" }),
        this._paramInput(params, "bins", "Frequency bins", 160, { type: "number", min: "16", max: "384", step: "1" }),
        this._paramInput(params, "columns", "Time columns", 768, { type: "number", min: "16", max: "2048", step: "1" }),
        this._paramInput(params, "gain", "Output gain", 0.85, { type: "number", min: "0.05", max: "1", step: "0.05" }),
        this._paramInput(params, "startSec", "Insert start (s)", 0, { type: "number", min: "0", step: "0.1" }),
        this._paramInput(params, "mixLevel", "Insert mix", 0.35, { type: "number", min: "0.01", max: "1", step: "0.01" }),
        this._paramInput(params, "preserveAspectRatio", "Preserve aspect", true, { type: "checkbox" }),
        this._paramInput(params, "invert", "Invert image", true, { type: "checkbox" }),
      ];

      const imagePicker = createElement("div", "leviathan-file-picker");
      const chooseImage = createElement("button", "btn btn-sm", { type: "button", textContent: "Select Image" });
      const imagePath = createElement("span", "leviathan-file-path", { textContent: "No insertion image selected" });
      imagePicker.append(chooseImage, imagePath);

      let selectedImagePath = "";
      chooseImage.addEventListener("click", async () => {
        try {
          const path = await window.NexusTerminal.wailsService.SelectFile("Select Image to Insert", "");
          if (!path) return;
          selectedImagePath = path;
          imagePath.textContent = path;
        } catch (err) {
          this.app.notifications.error("Image selection failed: " + err);
        }
      });

      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const encode = createElement("button", "btn btn-sm btn-primary", { type: "button", textContent: "Encode Loaded Image" });
      const insert = createElement("button", "btn btn-sm", { type: "button", textContent: "Insert Into Loaded Audio" });
      toolbar.append(encode, insert);
      controls.append(params, imagePicker, toolbar);

      const summary = createElement("div", "leviathan-summary");
      const results = createElement("div", "leviathan-results");
      output.append(summary, results);
      results.appendChild(createElement("div", "leviathan-empty", { textContent: "Load an image to encode, or load WAV/MP3 audio and select an image to insert." }));

      const run = async (toolId) => {
        try {
          results.innerHTML = "";
          results.appendChild(createElement("div", "leviathan-empty", { textContent: "Rendering audio..." }));
          const payload = {};
          for (const field of fields) payload[field.key] = this._fieldValue(field);
          if (toolId === "spectrogram-insert") {
            if (!selectedImagePath) throw new Error("Select an image to insert first.");
            payload.imagePath = selectedImagePath;
          }
          const out = await window.NexusTerminal.wailsService.LeviathanRunTool(toolId, payload);
          this.lastResults.set(sessionId, out);
          summary.innerHTML = "";
          results.innerHTML = "";
          this._renderSummaryPills(summary, out.summary || out);
          results.appendChild(createElement("pre", "leviathan-json", { textContent: JSON.stringify(out, null, 2) }));
          if (out?.path) this.app.notifications.success("Saved " + out.path);
        } catch (err) {
          results.innerHTML = "";
          this._renderToolError(results, toolId, err);
        }
      };
      encode.addEventListener("click", () => run("spectrogram-encode"));
      insert.addEventListener("click", () => run("spectrogram-insert"));
    }

    renderSpectrogramView(body, sessionId) {
      const { controls, output } = this._createWorkbench(body);
      const params = createElement("div", "leviathan-params");
      const fields = [
        this._paramInput(params, "fftSize", "FFT size", 2048, { type: "number", min: "256", max: "8192", step: "256" }),
        this._paramInput(params, "height", "Base height", 1024, { type: "number", min: "64", max: "1024", step: "16" }),
        this._paramInput(params, "maxWidth", "Max width", 1400, { type: "number", min: "64", max: "2400", step: "32" }),
        this._paramInput(params, "aspectRatio", "Aspect ratio", 1, { type: "number", min: "0.25", max: "4", step: "0.25" }),
        this._paramInput(params, "minHz", "Min frequency", 0, { type: "number", min: "0", step: "10" }),
        this._paramInput(params, "maxHz", "Max frequency", 0, { type: "number", min: "0", step: "10" }),
        this._paramInput(params, "brightness", "Brightness", 1, { type: "number", min: "0.1", max: "4", step: "0.1" }),
        this._paramInput(params, "contrast", "Contrast", 1, { type: "number", min: "0.1", max: "4", step: "0.1" }),
      ];

      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const run = createElement("button", "btn btn-sm btn-primary", { type: "button", textContent: "Render Spectrogram" });
      const exportBtn = createElement("button", "btn btn-sm", { type: "button", textContent: "Export JSON" });
      toolbar.append(run, exportBtn);
      controls.append(params, toolbar);

      const summary = createElement("div", "leviathan-summary");
      const results = createElement("div", "leviathan-results");
      output.append(summary, results);
      results.appendChild(createElement("div", "leviathan-empty", { textContent: "Load a WAV or MP3 file, then render its spectrogram." }));

      const render = async () => {
        try {
          results.innerHTML = "";
          results.appendChild(createElement("div", "leviathan-empty", { textContent: "Rendering spectrogram..." }));
          const payload = {};
          for (const field of fields) payload[field.key] = this._fieldValue(field);
          const out = await window.NexusTerminal.wailsService.LeviathanRunTool("spectrogram-view", payload);
          this.lastResults.set(sessionId, out);
          summary.innerHTML = "";
          results.innerHTML = "";
          this._renderSummaryPills(summary, out.summary || out);
          if (out?.pngBase64) {
            const frame = createElement("div", "leviathan-spectrogram-frame");
            const img = createElement("img", "leviathan-spectrogram-image", {
              alt: "Rendered audio spectrogram",
            });
            img.src = `data:${out.mime || "image/png"};base64,${out.pngBase64}`;
            frame.appendChild(img);
            results.appendChild(frame);
          } else {
            results.appendChild(createElement("pre", "leviathan-json", { textContent: JSON.stringify(out, null, 2) }));
          }
        } catch (err) {
          results.innerHTML = "";
          this._renderToolError(results, "spectrogram-view", err);
        }
      };
      run.addEventListener("click", render);
      exportBtn.addEventListener("click", async () => {
        const out = this.lastResults.get(sessionId);
        if (!out) return;
        try {
          await window.NexusTerminal.wailsService.LeviathanExportJSON("spectrogram-view.json", out);
          this.app.notifications.success("Exported JSON");
        } catch (err) {
          this.app.notifications.error("Export failed: " + err);
        }
      });
      this.autoRunners.set(sessionId, render);
      requestAnimationFrame(render);
    }

    renderToolRunner(body, sessionId, tool) {
      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const params = createElement("div", "leviathan-params");
      const fields = this._renderParams(tool.id, params);
      const autoRun = this._shouldAutoRunTool(tool.id, fields);
      const topBar = fields.length === 0 || this._shouldUseTopBar(fields);
      const { controls, output } = this._createWorkbench(
        body,
        topBar ? "leviathan-compact-workbench" : "",
        { topBar },
      );
      const run = createElement("button", "btn btn-sm btn-primary", { textContent: this._toolActionLabel(tool.id) });
      const exportBtn = createElement("button", "btn btn-sm", { textContent: "Export JSON" });
      if (!autoRun || fields.length) toolbar.append(run);
      toolbar.append(exportBtn);
      if (fields.length) controls.append(params);
      controls.append(toolbar);

      const summary = createElement("div", "leviathan-summary");
      const results = createElement("div", "leviathan-results");
      output.append(summary, results);

      let running = false;
      const runTool = async () => {
        if (running) return;
        running = true;
        try {
          if (tool.id === "diff" && !this.state?.compare) {
            const compare = await window.NexusTerminal.wailsService.LeviathanOpenCompare();
            this.applyState(compare);
          }
          results.innerHTML = "";
          results.appendChild(createElement("div", "leviathan-empty", { textContent: "Running..." }));
          const payload = {};
          for (const field of fields) {
            payload[field.key] = this._fieldValue(field);
          }
          const out = await window.NexusTerminal.wailsService.LeviathanRunTool(tool.id, payload);
          this.lastResults.set(sessionId, out);
          this._renderResult(summary, results, out, tool.id);
          if (out && out.state) this.applyState(out.state);
        } catch (err) {
          results.innerHTML = "";
          this._renderToolError(results, tool.id, err);
        } finally {
          running = false;
        }
      };
      if (!autoRun || fields.length) run.addEventListener("click", runTool);
      exportBtn.addEventListener("click", async () => {
        const out = this.lastResults.get(sessionId);
        if (!out) return;
        try {
          await window.NexusTerminal.wailsService.LeviathanExportJSON(`${tool.id}.json`, out);
          this.app.notifications.success("Exported JSON");
        } catch (err) {
          this.app.notifications.error("Export failed: " + err);
        }
      });
      if (autoRun) {
        this.autoRunners.set(sessionId, runTool);
        requestAnimationFrame(runTool);
      } else {
        this.autoRunners.delete(sessionId);
        results.appendChild(
          createElement("div", "leviathan-empty", {
            textContent: "Ready.",
          }),
        );
      }
    }

    _createWorkbench(body, extraClass = "", options = {}) {
      body.classList.add("leviathan-workbench");
      body.classList.toggle("leviathan-top-workbench", Boolean(options.topBar));
      if (extraClass) body.classList.add(extraClass);
      const controls = createElement("section", "leviathan-control-plane");
      const output = createElement("section", "leviathan-output-plane");
      body.append(controls, output);
      return { controls, output };
    }

    applyState(payload) {
      this.state = payload?.state || payload || null;
      this._updateOpenHeaders();
    }

    destroyPanel(sessionId) {
      this.lastResults.delete(sessionId);
      this.autoRunners.delete(sessionId);
    }

    handleTabFocus(sessionId) {
      const session = this.app.state.getSession(sessionId);
      if (session?.protocol !== "leviathan") return;
      this._highlightActiveTool(session.toolId);
      const runner = this.autoRunners.get(sessionId);
      if (runner) runner();
    }

    refreshOpenHexTabs() {
      for (const session of this.app.state.getAllSessions()) {
        if (session.protocol !== "leviathan" || session.toolId !== "hex") continue;
        const panel = document.getElementById(`panel-${session.id}`);
        if (panel) this.renderToolPanel(panel, session.id, tools[0]);
      }
    }

    _renderHexRows(grid, rows, status, rowWidth = 16, activeHit = null) {
      grid.innerHTML = "";
      grid.style.setProperty("--leviathan-hex-width", String(rowWidth));
      if (!rows.length) {
        grid.appendChild(createElement("div", "leviathan-empty", { textContent: "Load a file to inspect bytes." }));
        return;
      }
      for (const row of rows) {
        const line = createElement("div", "leviathan-hex-row");
        line.appendChild(createElement("span", "leviathan-offset", { textContent: this._hexOffset(row.offset) }));
        const bytes = createElement("span", "leviathan-byte-cells");
        const cells = row.bytes || String(row.hex || "").split(/\s+/).filter(Boolean);
        const ascii = createElement("span", "leviathan-ascii", { textContent: row.ascii || "" });
        cells.forEach((value, idx) => {
          const cellOffset = (row.offset || 0) + idx;
          const hexValue = this._normaliseHexByte(value) || "00";
          const cell = createElement("input", "leviathan-byte", {
            value: hexValue,
            maxlength: "2",
            inputmode: "text",
            autocomplete: "off",
            spellcheck: "false",
            "aria-label": `Byte ${this._hexOffset(cellOffset)}`,
            dataset: {
              offset: String(cellOffset),
              committedHex: hexValue,
            },
          });
          if (activeHit && cellOffset >= activeHit.offset && cellOffset < activeHit.offset + activeHit.length) {
            cell.classList.add("is-search-hit");
          }
          this._wireHexByteCell(cell, bytes, ascii, idx, status);
          bytes.appendChild(cell);
        });
        line.appendChild(bytes);
        line.appendChild(ascii);
        grid.appendChild(line);
      }
    }

    _wireHexByteCell(cell, rowCells, ascii, asciiIndex, status) {
      cell.addEventListener("focus", () => {
        cell.select();
      });
      cell.addEventListener("keydown", (event) => {
        if (event.key === "ArrowRight") {
          event.preventDefault();
          this._focusHexByte(cell, 1);
        } else if (event.key === "ArrowLeft") {
          event.preventDefault();
          this._focusHexByte(cell, -1);
        } else if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          this._finaliseHexByte(cell, rowCells, ascii, asciiIndex, status, { advance: 1 });
        } else if (event.key === "Backspace" && cell.value.length === 0) {
          event.preventDefault();
          this._focusHexByte(cell, -1);
        }
      });
      cell.addEventListener("input", () => {
        this._handleHexByteInput(cell, rowCells, ascii, asciiIndex, status);
      });
      cell.addEventListener("paste", (event) => {
        this._pasteHexBytes(event, cell, rowCells, ascii, asciiIndex, status);
      });
      cell.addEventListener("blur", () => {
        this._finaliseHexByte(cell, rowCells, ascii, asciiIndex, status, { advance: 0 });
      });
    }

    _handleHexByteInput(cell, rowCells, ascii, asciiIndex, status) {
      const clean = this._normaliseHexByte(cell.value);
      if (cell.value !== clean) cell.value = clean;
      cell.classList.toggle("is-editing", clean.length === 1);
      if (clean.length === 0) {
        this._updateHexAsciiCell(ascii, asciiIndex, cell.dataset.committedHex || "00");
        rowCells.classList.toggle("is-editing", Boolean(rowCells.querySelector(".leviathan-byte.is-editing")));
        return;
      }
      if (clean.length === 2) {
        this._updateHexAsciiCell(ascii, asciiIndex, clean);
        this._commitHexByte(cell, ascii, asciiIndex, status);
        this._focusHexByte(cell, 1);
      }
      rowCells.classList.toggle("is-editing", Boolean(rowCells.querySelector(".leviathan-byte.is-editing")));
    }

    _finaliseHexByte(cell, rowCells, ascii, asciiIndex, status, options = {}) {
      let clean = this._normaliseHexByte(cell.value);
      if (clean.length === 0) {
        clean = cell.dataset.committedHex || "00";
      } else if (clean.length === 1) {
        clean = clean.padStart(2, "0");
      }
      cell.value = clean;
      cell.classList.remove("is-editing");
      rowCells.classList.toggle("is-editing", Boolean(rowCells.querySelector(".leviathan-byte.is-editing")));
      this._updateHexAsciiCell(ascii, asciiIndex, clean);
      this._commitHexByte(cell, ascii, asciiIndex, status);
      if (options.advance) this._focusHexByte(cell, options.advance);
    }

    _pasteHexBytes(event, cell, rowCells, ascii, asciiIndex, status) {
      const text = event.clipboardData?.getData("text/plain") || "";
      const pasted = this._hexByteList(text);
      if (!pasted.length) return;
      event.preventDefault();
      const scope = cell.closest(".leviathan-hex-grid") || rowCells;
      const cells = Array.from(scope.querySelectorAll(".leviathan-byte"));
      const startIndex = cells.indexOf(cell);
      if (startIndex < 0) return;
      pasted.forEach((hex, idx) => {
        const target = cells[startIndex + idx];
        if (!target) return;
        const context = this._hexCellContext(target);
        if (!context) return;
        target.value = hex;
        target.classList.remove("is-editing");
        this._updateHexAsciiCell(context.ascii, context.index, hex);
        this._commitHexByte(target, context.ascii, context.index, status);
      });
      const next = cells[Math.min(startIndex + pasted.length, cells.length - 1)];
      if (next) {
        next.focus();
        next.select();
      }
    }

    _commitHexByte(cell, ascii, asciiIndex, status) {
      const hex = this._normaliseHexByte(cell.value);
      if (hex.length !== 2) return;
      if (hex === cell.dataset.committedHex || hex === cell.dataset.pendingHex) return;
      const previous = cell.dataset.committedHex || "00";
      const offset = Number(cell.dataset.offset || 0);
      cell.dataset.pendingHex = hex;
      cell.classList.add("is-saving");
      window.NexusTerminal.wailsService
        .LeviathanSetByte(offset, hex)
        .then((payload) => {
          if (cell.dataset.pendingHex === hex) {
            delete cell.dataset.pendingHex;
            cell.dataset.committedHex = hex;
            cell.classList.remove("is-saving");
            status.textContent = "Edited " + this._hexOffset(offset);
            this.applyState(payload);
          }
        })
        .catch((err) => {
          if (cell.dataset.pendingHex === hex) {
            delete cell.dataset.pendingHex;
            cell.classList.remove("is-saving");
            cell.value = previous;
            this._updateHexAsciiCell(ascii, asciiIndex, previous);
            status.textContent = String(err);
          }
        });
    }

    _focusHexByte(cell, direction) {
      const scope = cell.closest(".leviathan-hex-grid") || cell.closest(".leviathan-byte-cells");
      if (!scope) return;
      const cells = Array.from(scope.querySelectorAll(".leviathan-byte"));
      const index = cells.indexOf(cell);
      const next = cells[index + direction];
      if (!next) return;
      next.focus();
      next.select();
    }

    _hexCellContext(cell) {
      const row = cell.closest(".leviathan-hex-row");
      if (!row) return null;
      const ascii = row.querySelector(".leviathan-ascii");
      const rowCells = Array.from(row.querySelectorAll(".leviathan-byte"));
      const index = rowCells.indexOf(cell);
      if (!ascii || index < 0) return null;
      return { ascii, index };
    }

    _updateHexAsciiCell(ascii, index, hex) {
      const chars = Array.from(ascii.textContent || "");
      while (chars.length <= index) chars.push(" ");
      chars[index] = this._hexByteToAscii(hex);
      ascii.textContent = chars.join("");
    }

    _hexByteToAscii(hex) {
      const value = Number.parseInt(hex, 16);
      if (!Number.isFinite(value)) return ".";
      return value >= 32 && value < 127 ? String.fromCharCode(value) : ".";
    }

    _normaliseHexByte(value) {
      return String(value || "")
        .replace(/[^0-9a-f]/gi, "")
        .slice(0, 2)
        .toLowerCase();
    }

    _hexByteList(value) {
      const clean = String(value || "")
        .replace(/0x/gi, "")
        .replace(/[^0-9a-f]/gi, "")
        .toLowerCase();
      const bytes = [];
      for (let i = 0; i + 1 < clean.length; i += 2) {
        bytes.push(clean.slice(i, i + 2));
      }
      return bytes;
    }

    _renderParams(toolId, container) {
      container.innerHTML = "";
      const cfg = defaults[toolId] || {};
      return Object.entries(cfg).map(([key, value]) => {
        const row = createElement("label", "leviathan-param");
        row.appendChild(createElement("span", "", { textContent: key }));
        let input;
        if (key === "source" || key === "spec" || key === "pipeline") {
          input = createElement("textarea", "form-input");
        } else if (key === "mode") {
          input = createElement("select", "form-input");
          ["text", "hex", "regex"].forEach((v) => input.add(new Option(v, v)));
        } else if (key === "kind") {
          input = createElement("select", "form-input");
          ["auto", "png", "jpeg", "gif", "bmp", "zip", "elf", "pe", "macho", "riff", "asn1", "cert"].forEach((v) => input.add(new Option(v, v)));
        } else if (key === "arch") {
          input = createElement("select", "form-input");
          ["x86_64", "x86", "aarch64", "arm", "thumb", "mips", "mipsle"].forEach((v) => input.add(new Option(v, v)));
        } else {
          input = createElement("input", "form-input");
        }
        input.value = value;
        row.appendChild(input);
        container.appendChild(row);
        return { key, input };
      });
    }

    _shouldUseTopBar(fields) {
      return fields.length <= 3 && !fields.some((field) => field.input.tagName === "TEXTAREA");
    }

    _shouldAutoRunTool(toolId, fields) {
      const autoRunTools = new Set([
        "overview",
        "hashes",
        "entropy",
        "strings",
        "iocs",
        "search",
        "multi-search",
        "parse",
        "disasm",
        "pdf",
        "squashfs",
        "carve",
        "keys",
        "timestamps",
        "stego",
        "metadata",
        "binsnoop",
        "rules",
        "patch-plan",
        "transform",
        "spectrogram-view",
      ]);
      return autoRunTools.has(toolId);
    }

    _toolActionLabel(toolId) {
      if (toolId === "case") return "Export Case";
      if (toolId === "diff") return this.state?.compare ? "Compare" : "Load Compare";
      return "Run";
    }

    _renderResult(summary, results, out, toolId = "") {
      summary.innerHTML = "";
      results.innerHTML = "";
      this._renderSummaryPills(summary, this._flatSummary(out));
      if (toolId === "overview") return this._renderOverview(results, out);
      if (toolId === "hashes") return this._renderHashes(results, out);
      if (toolId === "entropy") return this._renderEntropy(summary, results, out);
      if (toolId === "search") return this._renderSearchResults(summary, results, out);
      if (toolId === "strings") return this._renderStrings(results, out.items || []);
      if (toolId === "binsnoop") return this._renderBinsnoop(summary, results, out);
      if (toolId === "carve") return this._renderCarve(summary, results, out);
      if (out?.tree) {
        this._renderTree(results, out.tree);
        return;
      }
      const arr = this._firstArray(out);
      if (arr) {
        this._renderTable(results, arr);
        return;
      }
      results.appendChild(createElement("pre", "leviathan-json", { textContent: JSON.stringify(out, null, 2) }));
    }

    _renderOverview(container, out) {
      const dashboard = createElement("div", "leviathan-dashboard");
      dashboard.appendChild(this._metricCard("File", out?.name || this.state?.name || "No file"));
      dashboard.appendChild(this._metricCard("Size", formatBytes(out?.size || 0)));
      dashboard.appendChild(this._metricCard("Entropy mean", this._formatNumber(out?.entropy?.mean, 2)));
      dashboard.appendChild(this._metricCard("Strings", out?.strings?.total ?? 0));
      dashboard.appendChild(this._metricCard("Carve hits", out?.carve?.total ?? 0));
      dashboard.appendChild(this._metricCard("Valid carve", out?.carve?.valid ?? 0));
      container.appendChild(dashboard);

      const sections = createElement("div", "leviathan-sections");
      sections.appendChild(this._kvSection("Hashes", out?.hashes || {}, ["crc32", "md5", "sha1", "sha256", "sha512"]));
      sections.appendChild(this._kvSection("IOC totals", out?.iocs || {}, Object.keys(out?.iocs || {}).sort()));
      sections.appendChild(this._kvSection("Scan limits", out?.limits || {}, Object.keys(out?.limits || {}), true));
      container.appendChild(sections);
    }

    _renderHashes(container, out) {
      const rows = ["crc32", "md5", "sha1", "sha256", "sha512"]
        .filter((key) => out?.[key])
        .map((key) => ({ algorithm: key.toUpperCase(), digest: out[key] }));
      this._renderTable(container, rows, ["algorithm", "digest"]);
    }

    _renderEntropy(summary, container, out) {
      summary.innerHTML = "";
      this._renderSummaryPills(summary, {
        samples: Array.isArray(out?.samples) ? out.samples.length : 0,
        mean: this._formatNumber(out?.mean, 3),
        min: this._formatNumber(out?.min, 3),
        max: this._formatNumber(out?.max, 3),
        high: out?.highCount ?? 0,
      });

      const frame = createElement("div", "leviathan-entropy-frame");
      const canvas = createElement("canvas", "leviathan-entropy-canvas", {
        width: "1200",
        height: "260",
      });
      frame.appendChild(canvas);
      container.appendChild(frame);

      const samples = (out?.samples || []).map((v) => Number(v) || 0);
      const offsets = (out?.offsets || []).map((v) => Number(v) || 0);
      const step = Number(out?.step || out?.windowSize || 4096);
      let dragStart = null;
      let dragEnd = null;

      const pointerIndex = (event) => {
        const rect = canvas.getBoundingClientRect();
        const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / Math.max(rect.width, 1)));
        return Math.min(samples.length - 1, Math.max(0, Math.round(ratio * (samples.length - 1))));
      };
      const draw = () => this._drawEntropyCanvas(canvas, samples, dragStart, dragEnd);

      canvas.addEventListener("mousedown", (event) => {
        if (!samples.length) return;
        dragStart = pointerIndex(event);
        dragEnd = dragStart;
        draw();
      });
      canvas.addEventListener("mousemove", (event) => {
        if (dragStart == null) return;
        dragEnd = pointerIndex(event);
        draw();
      });
      canvas.addEventListener("mouseup", () => {
        if (dragStart == null || dragEnd == null || !offsets.length) {
          dragStart = null;
          dragEnd = null;
          draw();
          return;
        }
        const lo = Math.min(dragStart, dragEnd);
        const hi = Math.max(dragStart, dragEnd);
        const start = offsets[lo] || 0;
        const end = (offsets[hi] || start) + step;
        dragStart = null;
        dragEnd = null;
        draw();
        if (end > start) this.openHexRange(start, end - start);
      });
      canvas.addEventListener("mouseleave", () => {
        if (dragStart == null) return;
        dragStart = null;
        dragEnd = null;
        draw();
      });
      requestAnimationFrame(draw);
    }

    _renderSearchResults(summary, container, out) {
      const hits = out?.hits || [];
      summary.innerHTML = "";
      this._renderSummaryPills(summary, { hits: hits.length });
      this._renderTable(container, hits, ["offset", "length", "sample"]);
    }

    _renderStrings(container, rows) {
      this._renderTable(container, rows, ["offset", "length", "encoding", "value"]);
    }

    _renderCarve(summary, container, out) {
      const rows = out?.items || [];
      summary.innerHTML = "";
      this._renderSummaryPills(summary, {
        hits: rows.length,
        exportable: rows.filter((row) => this._rowRange(row).length > 0).length,
      });
      this._renderTable(container, rows, ["offset", "length", "kind", "extension", "confidence", "status"], {
        exportRows: true,
        exportName: (row) => this._foundFileName(row, "carve"),
      });
    }

    _renderBinsnoop(summary, container, out) {
      const results = out?.results || {};
      const rows = results.file_map || results.fileMap || [];
      summary.innerHTML = "";
      this._renderSummaryPills(summary, {
        hits: rows.length,
        scanned: formatBytes(results.scanned_bytes || results.document_bytes || this.state?.size || 0),
        mode: results.whole_file ? "whole file" : "range",
        window: results.windowed ? formatBytes(results.window_bytes || 0) : "full",
      });
      this._renderTable(container, rows, ["offset", "size", "name", "description", "confidence"], {
        exportRows: true,
        exportName: (row) => this._foundFileName(row, "binsnoop"),
      });
    }

    _drawEntropyCanvas(canvas, samples, dragStart, dragEnd) {
      const ctx = canvas.getContext("2d");
      const width = canvas.width;
      const height = canvas.height;
      ctx.clearRect(0, 0, width, height);
      ctx.fillStyle = "#101010";
      ctx.fillRect(0, 0, width, height);
      ctx.strokeStyle = "rgba(255,255,255,0.08)";
      ctx.lineWidth = 1;
      for (let i = 1; i < 8; i++) {
        const y = height - (i / 8) * height;
        ctx.beginPath();
        ctx.moveTo(0, y);
        ctx.lineTo(width, y);
        ctx.stroke();
      }
      if (dragStart != null && dragEnd != null) {
        const lo = Math.min(dragStart, dragEnd);
        const hi = Math.max(dragStart, dragEnd);
        const x1 = (lo / Math.max(samples.length - 1, 1)) * width;
        const x2 = (hi / Math.max(samples.length - 1, 1)) * width;
        ctx.fillStyle = "rgba(78, 205, 196, 0.18)";
        ctx.fillRect(x1, 0, Math.max(2, x2 - x1), height);
      }
      if (!samples.length) return;
      ctx.strokeStyle = "#4ecdc4";
      ctx.lineWidth = 2;
      ctx.beginPath();
      samples.forEach((sample, index) => {
        const x = (index / Math.max(samples.length - 1, 1)) * width;
        const y = height - (Math.max(0, Math.min(8, sample)) / 8) * height;
        if (index === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      });
      ctx.stroke();
    }

    _renderTable(container, rows, preferredColumns = null, options = {}) {
      const sourceRows = rows || [];
      const maxRows = options.maxRows || (options.exportRows ? sourceRows.length : 1000);
      rows = sourceRows.slice(0, maxRows);
      if (!rows.length) {
        container.appendChild(createElement("div", "leviathan-empty", { textContent: "No rows." }));
        return;
      }
      const preferred = preferredColumns || ["offset", "length", "size", "kind", "name", "category", "value", "sample", "summary", "description", "iso", "type"];
      const all = Array.from(new Set(rows.flatMap((row) => Object.keys(row || {}))));
      const cols = [...preferred.filter((c) => all.includes(c)), ...all.filter((c) => !preferred.includes(c))].slice(0, 8);
      if (options.exportRows) cols.push("export");
      const table = createElement("table", "leviathan-table");
      const thead = createElement("thead");
      const headRow = createElement("tr");
      cols.forEach((c) => headRow.appendChild(createElement("th", "", { textContent: c })));
      thead.appendChild(headRow);
      const tbody = createElement("tbody");
      rows.forEach((row) => {
        const tr = createElement("tr");
        cols.forEach((c) => {
          const td = createElement("td");
          if (c === "export") {
            const range = this._rowRange(row);
            if (range.length > 0) {
              const btn = createElement("button", "btn btn-xs leviathan-export-found", {
                type: "button",
                textContent: "Export",
              });
              btn.addEventListener("click", () => this._exportFoundRange(row, options.exportName));
              td.appendChild(btn);
            } else {
              td.appendChild(createElement("span", "leviathan-muted", { textContent: "No length" }));
            }
          } else if (c === "offset" && row[c] != null) {
            const offset = Number(row[c]);
            const length = Number(row.length || row.size || 256);
            const btn = createElement("button", "leviathan-offset-link", {
              type: "button",
              textContent: this._hexOffset(offset),
            });
            btn.addEventListener("click", () => this.openHexRange(offset, Math.max(256, length || 256)));
            td.appendChild(btn);
          } else {
            td.textContent = this._formatCell(row[c]);
          }
          tr.appendChild(td);
        });
        tbody.appendChild(tr);
      });
      table.append(thead, tbody);
      container.appendChild(table);
      if (sourceRows.length > rows.length) {
        container.appendChild(createElement("div", "leviathan-table-note", {
          textContent: `Showing ${rows.length} of ${sourceRows.length} rows.`,
        }));
      }
    }

    _rowRange(row) {
      const offset = Number(row?.offset);
      if (!Number.isFinite(offset) || offset < 0) return { offset: 0, length: 0 };
      let length = Number(row.length ?? row.size ?? 0);
      if ((!Number.isFinite(length) || length <= 0) && Number.isFinite(Number(row.end))) {
        length = Number(row.end) - offset;
      }
      if ((!Number.isFinite(length) || length <= 0) && this.state?.size > offset) {
        length = Number(this.state.size) - offset;
      }
      return {
        offset,
        length: Number.isFinite(length) ? Math.max(0, Math.floor(length)) : 0,
      };
    }

    _foundFileName(row, prefix) {
      const range = this._rowRange(row);
      const label = String(row.kind || row.name || row.id || prefix || "found")
        .toLowerCase()
        .replace(/[^a-z0-9._-]+/g, "-")
        .replace(/^-+|-+$/g, "")
        .slice(0, 48) || "found";
      const ext = String(row.extension || row.ext || "bin")
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "")
        .slice(0, 12) || "bin";
      return `${prefix || "found"}-${label}-${this._hexOffset(range.offset)}.${ext}`;
    }

    async _exportFoundRange(row, nameBuilder) {
      const range = this._rowRange(row);
      if (range.length <= 0) {
        this.app.notifications.error("Export failed: finding has no readable range");
        return;
      }
      const name = typeof nameBuilder === "function" ? nameBuilder(row) : this._foundFileName(row, "found");
      try {
        const path = await window.NexusTerminal.wailsService.LeviathanExportRange(name, range.offset, range.length);
        if (path) this.app.notifications.success("Exported " + path);
      } catch (err) {
        this.app.notifications.error("Export failed: " + err);
      }
    }

    _renderToolError(container, toolId, err) {
      const message = String(err || "The tool could not complete.");
      const isSquashMagic = toolId === "squashfs" && /magic|superblock|squash/i.test(message);
      const state = createElement("div", isSquashMagic ? "leviathan-error-state leviathan-error-state-warning" : "leviathan-error-state");
      const icon = createElement("div", "leviathan-error-icon", { "aria-hidden": "true" });
      icon.appendChild(createElement("span", "", { textContent: "!" }));
      const title = isSquashMagic ? "Not a SquashFS image" : "Tool could not run";
      const body = isSquashMagic
        ? "The loaded file does not start with a valid SquashFS superblock. Try another file, or inspect this one in the Hex Editor."
        : message;
      state.appendChild(icon);
      state.appendChild(createElement("strong", "", { textContent: title }));
      state.appendChild(createElement("p", "", { textContent: body }));
      container.appendChild(state);
    }

    _renderTree(container, root) {
      const wrap = createElement("div", "leviathan-tree");
      const walk = (node, depth = 0) => {
        const row = createElement("div", "leviathan-tree-node");
        row.style.paddingLeft = `${depth * 16}px`;
        row.appendChild(createElement("strong", "", { textContent: node.name || "" }));
        row.appendChild(createElement("span", "", { textContent: ` ${node.type || ""} ${node.offset != null ? this._hexOffset(node.offset) : ""} +${node.length || 0}` }));
        if (node.value) row.appendChild(createElement("div", "", { textContent: node.value }));
        wrap.appendChild(row);
        (node.children || []).forEach((child) => walk(child, depth + 1));
      };
      walk(root);
      container.appendChild(wrap);
    }

    _stateAction(method, sessionId) {
      return window.NexusTerminal.wailsService[method]()
        .then((state) => {
          this.applyState(state);
          const session = this.app.state.getSession(sessionId);
          if (session?.toolId === "hex") {
            const panel = document.getElementById(`panel-${sessionId}`);
            if (panel) this.renderToolPanel(panel, sessionId, tools[0]);
          }
        })
        .catch((err) => this.app.notifications.error(String(err)));
    }

    _input(label, value) {
      const row = createElement("label", "leviathan-param-inline");
      row.appendChild(createElement("span", "", { textContent: label }));
      const input = createElement("input", "form-input", { value });
      row.appendChild(input);
      return { row, input };
    }

    _selectInput(label, options, value) {
      const row = createElement("label", "leviathan-param-inline");
      row.appendChild(createElement("span", "", { textContent: label }));
      const input = createElement("select", "form-input");
      options.forEach(([optionValue, text]) => input.add(new Option(text, optionValue)));
      input.value = value;
      row.appendChild(input);
      return { row, input };
    }

    _paramInput(container, key, label, value, attrs = {}) {
      const row = createElement("label", "leviathan-param");
      row.appendChild(createElement("span", "", { textContent: label }));
      const input = createElement("input", "form-input");
      for (const [attr, attrValue] of Object.entries(attrs)) {
        input.setAttribute(attr, attrValue);
      }
      if (attrs.type === "checkbox") {
        input.checked = Boolean(value);
        input.classList.add("leviathan-checkbox");
      } else {
        input.value = value;
      }
      row.appendChild(input);
      container.appendChild(row);
      return { key, input };
    }

    _fieldValue(field) {
      if (field.input.type === "checkbox") {
        return field.input.checked;
      }
      const raw = field.input.value;
      const numeric = Number(raw);
      const stringKeys = ["query", "source", "spec", "pipeline", "kind", "arch", "mode"];
      return raw !== "" && Number.isFinite(numeric) && !stringKeys.includes(field.key)
        ? numeric
        : raw;
    }

    openHexRange(start, length = 2048) {
      const safeStart = Math.max(0, Math.floor(Number(start) || 0));
      const safeLength = Math.max(256, Math.min(8192, Math.floor(Number(length) || 2048)));
      this.openTool("hex", {
        params: { start: safeStart, bytes: safeLength, width: 16 },
      });
    }

    _sessionParams(sessionId) {
      return this.app.state.getSession(sessionId)?.leviathanParams || {};
    }

    _currentHexSearchHit(sessionId) {
      const state = this.hexSearches.get(sessionId);
      if (!state || state.index < 0 || !state.hits?.length) return null;
      const hit = state.hits[state.index];
      return {
        offset: Number(hit.offset || 0),
        length: Number(hit.length || 1),
      };
    }

    _gotoHexSearchHit(sessionId, direction, startInput, widthInput, load) {
      const state = this.hexSearches.get(sessionId);
      if (!state || !state.hits?.length) {
        this._showHexSearchModal(sessionId, startInput, widthInput, load);
        return;
      }
      state.index = (state.index + direction + state.hits.length) % state.hits.length;
      const hit = state.hits[state.index];
      const rowWidth = Number(widthInput.value || 16);
      const offset = Number(hit.offset || 0);
      startInput.value = String(Math.max(0, offset - (offset % rowWidth)));
      load();
    }

    _showHexSearchModal(sessionId, startInput, widthInput, load) {
      const backdrop = createElement("div", "modal-backdrop");
      const modal = createElement("div", "modal modal-sm leviathan-search-modal");
      const header = createElement("div", "modal-header");
      header.appendChild(createElement("h3", "modal-title", { textContent: "Hex Search" }));
      const closeBtn = createElement("button", "btn btn-icon btn-xs modal-close", {
        type: "button",
        textContent: "x",
      });
      header.appendChild(closeBtn);

      const body = createElement("div", "modal-body");
      const fields = createElement("div", "leviathan-modal-fields");
      const mode = this._selectInput("mode", [
        ["text", "String"],
        ["hex", "Hex"],
        ["regex", "Regex"],
      ], "text");
      const query = this._input("query", "");
      fields.append(mode.row, query.row);
      const status = createElement("div", "leviathan-status", { textContent: "" });
      body.append(fields, status);

      const footer = createElement("div", "modal-footer");
      const cancel = createElement("button", "btn btn-sm", { type: "button", textContent: "Cancel" });
      const run = createElement("button", "btn btn-sm btn-primary", { type: "button", textContent: "Search" });
      footer.append(cancel, run);
      modal.append(header, body, footer);
      backdrop.appendChild(modal);
      document.body.appendChild(backdrop);

      const close = () => {
        backdrop.classList.remove("is-visible");
        modal.classList.remove("is-visible");
        setTimeout(() => backdrop.remove(), 200);
      };
      const search = async () => {
        try {
          status.textContent = "Searching...";
          const out = await window.NexusTerminal.wailsService.LeviathanRunTool("search", {
            mode: mode.input.value,
            query: query.input.value,
            start: 0,
            length: -1,
            max: 5000,
          });
          const hits = (out.hits || []).map((hit) => ({
            offset: Number(hit.offset || 0),
            length: Number(hit.length || 1),
            sample: hit.sample || "",
          }));
          this.hexSearches.set(sessionId, {
            mode: mode.input.value,
            query: query.input.value,
            hits,
            index: -1,
          });
          close();
          if (hits.length) this._gotoHexSearchHit(sessionId, 1, startInput, widthInput, load);
        } catch (err) {
          status.textContent = String(err);
        }
      };

      closeBtn.addEventListener("click", close);
      cancel.addEventListener("click", close);
      run.addEventListener("click", search);
      query.input.addEventListener("keydown", (event) => {
        if (event.key === "Enter") search();
      });
      requestAnimationFrame(() => {
        backdrop.classList.add("is-visible");
        modal.classList.add("is-visible");
        query.input.focus();
      });
    }

    _metricCard(label, value) {
      const item = createElement("div", "leviathan-metric");
      item.appendChild(createElement("span", "", { textContent: label }));
      item.appendChild(createElement("strong", "", { textContent: this._formatCell(value) }));
      return item;
    }

    _kvSection(title, values, keys, bytes = false) {
      const section = createElement("section", "leviathan-section");
      section.appendChild(createElement("h3", "", { textContent: title }));
      const list = createElement("div", "leviathan-kv-list");
      keys.forEach((key) => {
        if (values?.[key] == null) return;
        const row = createElement("div", "leviathan-kv-row");
        row.appendChild(createElement("span", "", { textContent: key }));
        row.appendChild(createElement("strong", "", {
          textContent: bytes ? formatBytes(Number(values[key]) || 0) : this._formatCell(values[key]),
        }));
        list.appendChild(row);
      });
      section.appendChild(list);
      return section;
    }

    _formatNumber(value, digits = 2) {
      const n = Number(value);
      return Number.isFinite(n) ? n.toFixed(digits) : "0";
    }

    _renderSummaryPills(container, values) {
      Object.entries(values || {})
        .slice(0, 10)
        .forEach(([key, value]) => {
          const item = createElement("div", "leviathan-pill");
          item.appendChild(createElement("span", "", { textContent: key }));
          item.appendChild(createElement("strong", "", { textContent: this._formatCell(value) }));
          container.appendChild(item);
        });
    }

    _findExistingToolSession(toolId) {
      const session = this.app.state
        .getAllSessions()
        .find((s) => s.protocol === "leviathan" && s.toolId === toolId);
      return session?.id || null;
    }

    _updateOpenHeaders() {
      document.querySelectorAll(".leviathan-eyebrow").forEach((el) => {
        el.textContent = this._stateLabel();
      });
    }

    _highlightActiveTool(toolId) {
      document.querySelectorAll(".leviathan-tool").forEach((el) => {
        el.classList.toggle("sidebar-item-active", el.dataset.toolId === toolId);
      });
    }

    _stateLabel() {
      if (!this.state?.name) return "No file loaded";
      const dirty = this.state.dirty ? "modified" : "clean";
      return `${this.state.name} · ${formatBytes(this.state.size || 0)} · ${dirty}`;
    }

    _flatSummary(out) {
      if (!out || typeof out !== "object") return {};
      const summary = {};
      for (const [key, value] of Object.entries(out)) {
        if (Array.isArray(value)) summary[key] = value.length;
        else if (value && typeof value === "object") {
          if (key === "hashes") summary.hash = value.sha256 || value.md5 || "ready";
          else summary[key] = Object.keys(value).length + " keys";
        } else {
          summary[key] = value;
        }
      }
      return summary;
    }

    _firstArray(out) {
      if (!out || typeof out !== "object") return null;
      for (const key of ["items", "hits", "all", "regions", "blocks", "entries", "metadata"]) {
        if (Array.isArray(out[key])) return out[key];
      }
      if (out.results?.Results && Array.isArray(out.results.Results)) return out.results.Results;
      return null;
    }

    _formatCell(value) {
      return typeof value === "object" ? JSON.stringify(value) : String(value ?? "");
    }

    _hexOffset(value) {
      return "0x" + Number(value || 0).toString(16).padStart(8, "0");
    }
  }

  window.NexusTerminal = window.NexusTerminal || {};
  window.NexusTerminal.LeviathanWorkbench = LeviathanWorkbench;
  window.NexusTerminal.LeviathanTools = tools;
})();
