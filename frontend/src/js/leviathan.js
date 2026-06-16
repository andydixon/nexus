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
    binsnoop: { maxBytes: 67108864 },
    "spectrogram-view": { fftSize: 2048, height: 1024, maxWidth: 1400, aspectRatio: 1, minHz: 0, maxHz: 0, brightness: 1, contrast: 1 },
    hex: { start: 0, bytes: 2048, width: 16 },
  };

  class LeviathanWorkbench {
    constructor(app) {
      this.app = app;
      this.state = null;
      this.lastResults = new Map();
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
        const payload = await window.go.api.Service.LeviathanOpenFile();
        this.applyState(payload);
        this.refreshOpenHexTabs();
      } catch (err) {
        if (String(err || "").includes("cancel")) return;
        this.app.notifications.error("Load failed: " + err);
      }
    }

    async saveFile() {
      try {
        const path = await window.go.api.Service.LeviathanSaveWorking();
        if (path) this.app.notifications.success("Saved working copy");
        const state = await window.go.api.Service.LeviathanState();
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
      titleWrap.appendChild(createElement("span", "leviathan-eyebrow", { textContent: this._stateLabel() }));
      titleWrap.appendChild(createElement("h2", "", { textContent: tool.name }));
      titleWrap.appendChild(createElement("p", "", { textContent: tool.desc }));
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
      } else if (tool.id === "spectrogram-encode") {
        this.renderSpectrogramEncode(body, sessionId);
      } else if (tool.id === "spectrogram-view") {
        this.renderSpectrogramView(body, sessionId);
      } else {
        this.renderToolRunner(body, sessionId, tool);
      }
    }

    renderHexEditor(body, sessionId) {
      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const start = this._input("start", "0");
      const bytes = this._input("bytes", "2048");
      const refresh = createElement("button", "btn btn-sm btn-primary", { textContent: "Refresh" });
      toolbar.append(start.row, bytes.row, refresh);
      body.appendChild(toolbar);

      const status = createElement("div", "leviathan-status", { textContent: this._stateLabel() });
      const grid = createElement("div", "leviathan-hex-grid");
      body.append(status, grid);

      const load = async () => {
        try {
          status.textContent = "Loading hex...";
          const result = await window.go.api.Service.LeviathanRunTool("hex", {
            start: Number(start.input.value || 0),
            bytes: Number(bytes.input.value || 2048),
            width: 16,
          });
          this.lastResults.set(sessionId, result);
          this._renderHexRows(grid, result.rows || [], status);
          status.textContent = this._stateLabel();
        } catch (err) {
          status.textContent = String(err);
        }
      };
      refresh.addEventListener("click", load);
      load();
    }

    renderSpectrogramEncode(body, sessionId) {
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
      body.appendChild(params);

      const imagePicker = createElement("div", "leviathan-file-picker");
      const chooseImage = createElement("button", "btn btn-sm", { type: "button", textContent: "Select Image" });
      const imagePath = createElement("span", "leviathan-file-path", { textContent: "No insertion image selected" });
      imagePicker.append(chooseImage, imagePath);
      body.appendChild(imagePicker);

      let selectedImagePath = "";
      chooseImage.addEventListener("click", async () => {
        try {
          const path = await window.go.api.Service.SelectFile("Select Image to Insert", "");
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
      body.appendChild(toolbar);

      const summary = createElement("div", "leviathan-summary");
      const results = createElement("div", "leviathan-results");
      body.append(summary, results);
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
          const out = await window.go.api.Service.LeviathanRunTool(toolId, payload);
          this.lastResults.set(sessionId, out);
          summary.innerHTML = "";
          results.innerHTML = "";
          this._renderSummaryPills(summary, out.summary || out);
          results.appendChild(createElement("pre", "leviathan-json", { textContent: JSON.stringify(out, null, 2) }));
          if (out?.path) this.app.notifications.success("Saved " + out.path);
        } catch (err) {
          results.innerHTML = "";
          results.appendChild(createElement("pre", "leviathan-json", { textContent: String(err) }));
        }
      };
      encode.addEventListener("click", () => run("spectrogram-encode"));
      insert.addEventListener("click", () => run("spectrogram-insert"));
    }

    renderSpectrogramView(body, sessionId) {
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
      body.appendChild(params);

      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const run = createElement("button", "btn btn-sm btn-primary", { type: "button", textContent: "Render Spectrogram" });
      const exportBtn = createElement("button", "btn btn-sm", { type: "button", textContent: "Export JSON" });
      toolbar.append(run, exportBtn);
      body.appendChild(toolbar);

      const summary = createElement("div", "leviathan-summary");
      const results = createElement("div", "leviathan-results");
      body.append(summary, results);
      results.appendChild(createElement("div", "leviathan-empty", { textContent: "Load a WAV or MP3 file, then render its spectrogram." }));

      run.addEventListener("click", async () => {
        try {
          results.innerHTML = "";
          results.appendChild(createElement("div", "leviathan-empty", { textContent: "Rendering spectrogram..." }));
          const payload = {};
          for (const field of fields) payload[field.key] = this._fieldValue(field);
          const out = await window.go.api.Service.LeviathanRunTool("spectrogram-view", payload);
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
          results.appendChild(createElement("pre", "leviathan-json", { textContent: String(err) }));
        }
      });
      exportBtn.addEventListener("click", async () => {
        const out = this.lastResults.get(sessionId);
        if (!out) return;
        try {
          await window.go.api.Service.LeviathanExportJSON("spectrogram-view.json", out);
          this.app.notifications.success("Exported JSON");
        } catch (err) {
          this.app.notifications.error("Export failed: " + err);
        }
      });
    }

    renderToolRunner(body, sessionId, tool) {
      const toolbar = createElement("div", "leviathan-tool-toolbar");
      const params = createElement("div", "leviathan-params");
      const fields = this._renderParams(tool.id, params);
      const run = createElement("button", "btn btn-sm btn-primary", { textContent: "Run" });
      const exportBtn = createElement("button", "btn btn-sm", { textContent: "Export JSON" });
      toolbar.append(run, exportBtn);
      body.append(params, toolbar);

      const summary = createElement("div", "leviathan-summary");
      const results = createElement("div", "leviathan-results");
      body.append(summary, results);

      run.addEventListener("click", async () => {
        try {
          if (tool.id === "diff" && !this.state?.compare) {
            const compare = await window.go.api.Service.LeviathanOpenCompare();
            this.applyState(compare);
          }
          results.innerHTML = "";
          results.appendChild(createElement("div", "leviathan-empty", { textContent: "Running..." }));
          const payload = {};
          for (const field of fields) {
            payload[field.key] = this._fieldValue(field);
          }
          const out = await window.go.api.Service.LeviathanRunTool(tool.id, payload);
          this.lastResults.set(sessionId, out);
          this._renderResult(summary, results, out);
          if (out && out.state) this.applyState(out.state);
        } catch (err) {
          results.innerHTML = "";
          results.appendChild(createElement("pre", "leviathan-json", { textContent: String(err) }));
        }
      });
      exportBtn.addEventListener("click", async () => {
        const out = this.lastResults.get(sessionId);
        if (!out) return;
        try {
          await window.go.api.Service.LeviathanExportJSON(`${tool.id}.json`, out);
          this.app.notifications.success("Exported JSON");
        } catch (err) {
          this.app.notifications.error("Export failed: " + err);
        }
      });
      results.appendChild(
        createElement("div", "leviathan-empty", {
          textContent: "Run the tool to populate this tab.",
        }),
      );
    }

    applyState(payload) {
      this.state = payload?.state || payload || null;
      this._updateOpenHeaders();
    }

    destroyPanel(sessionId) {
      this.lastResults.delete(sessionId);
    }

    refreshOpenHexTabs() {
      for (const session of this.app.state.getAllSessions()) {
        if (session.protocol !== "leviathan" || session.toolId !== "hex") continue;
        const panel = document.getElementById(`panel-${session.id}`);
        if (panel) this.renderToolPanel(panel, session.id, tools[0]);
      }
    }

    _renderHexRows(grid, rows, status) {
      grid.innerHTML = "";
      if (!rows.length) {
        grid.appendChild(createElement("div", "leviathan-empty", { textContent: "Load a file to inspect bytes." }));
        return;
      }
      for (const row of rows) {
        const line = createElement("div", "leviathan-hex-row");
        line.appendChild(createElement("span", "leviathan-offset", { textContent: this._hexOffset(row.offset) }));
        const bytes = createElement("span", "leviathan-byte-cells");
        const cells = row.bytes || String(row.hex || "").split(/\s+/).filter(Boolean);
        cells.forEach((value, idx) => {
          const cell = createElement("input", "leviathan-byte", {
            value,
            maxlength: "2",
            "aria-label": `Byte ${this._hexOffset((row.offset || 0) + idx)}`,
          });
          cell.addEventListener("change", async () => {
            try {
              await window.go.api.Service.LeviathanSetByte((row.offset || 0) + idx, cell.value);
              cell.value = cell.value.trim().padStart(2, "0").slice(-2).toLowerCase();
              status.textContent = "Edited " + this._hexOffset((row.offset || 0) + idx);
            } catch (err) {
              status.textContent = String(err);
              cell.value = value;
            }
          });
          bytes.appendChild(cell);
        });
        line.appendChild(bytes);
        line.appendChild(createElement("span", "leviathan-ascii", { textContent: row.ascii || "" }));
        grid.appendChild(line);
      }
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

    _renderResult(summary, results, out) {
      summary.innerHTML = "";
      results.innerHTML = "";
      this._renderSummaryPills(summary, this._flatSummary(out));
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

    _renderTable(container, rows) {
      rows = rows.slice(0, 1000);
      if (!rows.length) {
        container.appendChild(createElement("div", "leviathan-empty", { textContent: "No rows." }));
        return;
      }
      const preferred = ["offset", "length", "kind", "name", "category", "value", "sample", "summary", "iso", "type"];
      const all = Array.from(new Set(rows.flatMap((row) => Object.keys(row || {}))));
      const cols = [...preferred.filter((c) => all.includes(c)), ...all.filter((c) => !preferred.includes(c))].slice(0, 8);
      const table = createElement("table", "leviathan-table");
      const thead = createElement("thead");
      const headRow = createElement("tr");
      cols.forEach((c) => headRow.appendChild(createElement("th", "", { textContent: c })));
      thead.appendChild(headRow);
      const tbody = createElement("tbody");
      rows.forEach((row) => {
        const tr = createElement("tr");
        cols.forEach((c) => tr.appendChild(createElement("td", "", { textContent: this._formatCell(row[c]) })));
        tbody.appendChild(tr);
      });
      table.append(thead, tbody);
      container.appendChild(table);
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
      return window.go.api.Service[method]()
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
