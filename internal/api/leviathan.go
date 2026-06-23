package api

import (
	"archive/zip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/andydixon/nexus/internal/leviathan/analysis"
	"github.com/andydixon/nexus/internal/leviathan/binsnoop"
	"github.com/andydixon/nexus/internal/leviathan/disasm"
	"github.com/andydixon/nexus/internal/leviathan/document"
	"github.com/andydixon/nexus/internal/leviathan/parsers"
	"github.com/andydixon/nexus/internal/leviathan/patch"
	"github.com/andydixon/nexus/internal/leviathan/pdf"
	"github.com/andydixon/nexus/internal/leviathan/spectrogram"
	"github.com/andydixon/nexus/internal/leviathan/squashfs"
	"github.com/andydixon/nexus/internal/leviathan/stego"
	"github.com/andydixon/nexus/internal/leviathan/transforms"
)

type leviathanState struct {
	mu  sync.Mutex
	doc *document.Document
	cmp *document.Document
}

type leviathanDocState struct {
	Name       string             `json:"name"`
	Size       int64              `json:"size"`
	Dirty      bool               `json:"dirty"`
	UndoLen    int                `json:"undoLen"`
	RedoLen    int                `json:"redoLen"`
	Compare    *leviathanDocState `json:"compare,omitempty"`
	PreviewHex []map[string]any   `json:"previewHex"`
}

func (s *Service) leviathanEngine() *leviathanState {
	if s.leviathan == nil {
		s.leviathan = &leviathanState{}
	}
	return s.leviathan
}

// LeviathanOpenFile prompts for a binary and loads it into the Leviathan workbench.
func (s *Service) LeviathanOpenFile() (map[string]any, error) {
	if s.ctx == nil {
		return nil, fmt.Errorf("application context not set")
	}
	path, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Open Binary",
	})
	if err != nil || path == "" {
		return nil, err
	}
	return s.LeviathanLoadFile(path, false)
}

// LeviathanOpenCompare prompts for a binary to compare against the working file.
func (s *Service) LeviathanOpenCompare() (map[string]any, error) {
	if s.ctx == nil {
		return nil, fmt.Errorf("application context not set")
	}
	path, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Open Compare Binary",
	})
	if err != nil || path == "" {
		return nil, err
	}
	return s.LeviathanLoadFile(path, true)
}

// LeviathanLoadFile loads a path supplied by drag/drop or the native file picker.
func (s *Service) LeviathanLoadFile(path string, compare bool) (map[string]any, error) {
	d, err := document.Open(path)
	if err != nil {
		return nil, err
	}
	engine := s.leviathanEngine()
	engine.mu.Lock()
	if compare {
		engine.cmp = d
	} else {
		engine.doc = d
	}
	state := engine.stateLocked()
	engine.mu.Unlock()
	return map[string]any{"state": state}, nil
}

// LeviathanState returns the current binary workbench state.
func (s *Service) LeviathanState() (leviathanDocState, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.stateLocked(), nil
}

// LeviathanRunTool runs one Leviathan analysis/editing tool.
func (s *Service) LeviathanRunTool(tool string, params map[string]any) (map[string]any, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	if engine.doc == nil {
		engine.mu.Unlock()
		return nil, errors.New("open a file first")
	}
	if tool == "spectrogram-encode" || tool == "spectrogram-insert" || tool == "spectrogram-view" || tool == "case" {
		doc := engine.doc
		engine.mu.Unlock()
		switch tool {
		case "spectrogram-encode":
			return s.leviathanSpectrogramEncode(doc, params)
		case "spectrogram-insert":
			return s.leviathanSpectrogramInsert(doc, params)
		case "case":
			return s.leviathanExportCase(doc)
		default:
			return leviathanSpectrogramView(doc, params)
		}
	}
	defer engine.mu.Unlock()
	return engine.runToolLocked(tool, params)
}

// LeviathanSetByte patches one byte in the working document.
func (s *Service) LeviathanSetByte(offset int64, value string) (map[string]any, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.doc == nil {
		return nil, errors.New("open a file first")
	}
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if len(value) == 1 {
		value = "0" + value
	}
	if len(value) != 2 {
		return nil, errors.New("byte value must be two hex digits")
	}
	b, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if _, err := engine.doc.Set(offset, b[0]); err != nil {
		return nil, err
	}
	return map[string]any{"state": engine.stateLocked()}, nil
}

// LeviathanUndo reverts the latest byte edit.
func (s *Service) LeviathanUndo() (leviathanDocState, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.doc == nil {
		return leviathanDocState{}, errors.New("open a file first")
	}
	engine.doc.Undo()
	return engine.stateLocked(), nil
}

// LeviathanRedo reapplies the latest undone byte edit.
func (s *Service) LeviathanRedo() (leviathanDocState, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.doc == nil {
		return leviathanDocState{}, errors.New("open a file first")
	}
	engine.doc.Redo()
	return engine.stateLocked(), nil
}

// LeviathanRevert clears all working edits.
func (s *Service) LeviathanRevert() (leviathanDocState, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.doc == nil {
		return leviathanDocState{}, errors.New("open a file first")
	}
	engine.doc.Revert()
	return engine.stateLocked(), nil
}

// LeviathanSaveWorking prompts for a destination and writes the patched bytes.
func (s *Service) LeviathanSaveWorking() (string, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	if engine.doc == nil {
		engine.mu.Unlock()
		return "", errors.New("open a file first")
	}
	doc := engine.doc
	engine.mu.Unlock()
	if s.ctx == nil {
		return "", fmt.Errorf("application context not set")
	}
	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Save Working Copy",
		DefaultFilename: doc.Name() + ".patched",
	})
	if err != nil || path == "" {
		return "", err
	}
	return path, writeLeviathanDocumentFile(path, doc)
}

// LeviathanSaveWorkingToPath writes the patched bytes to an explicit path.
// This is used by non-Wails hosts, where file picking is owned by the host UI.
func (s *Service) LeviathanSaveWorkingToPath(path string) (string, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	if engine.doc == nil {
		engine.mu.Unlock()
		return "", errors.New("open a file first")
	}
	doc := engine.doc
	engine.mu.Unlock()
	if strings.TrimSpace(path) == "" {
		return "", errors.New("save path is required")
	}
	return path, writeLeviathanDocumentFile(path, doc)
}

// LeviathanExportJSON writes a tool result to disk.
func (s *Service) LeviathanExportJSON(name string, value map[string]any) (string, error) {
	if s.ctx == nil {
		return "", fmt.Errorf("application context not set")
	}
	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Export JSON",
		DefaultFilename: safeExportName(name, "leviathan-result.json"),
	})
	if err != nil || path == "" {
		return "", err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0600)
}

// LeviathanExportRange prompts for a destination and streams a document range to disk.
func (s *Service) LeviathanExportRange(name string, start, length int64) (string, error) {
	engine := s.leviathanEngine()
	engine.mu.Lock()
	if engine.doc == nil {
		engine.mu.Unlock()
		return "", errors.New("open a file first")
	}
	doc := engine.doc
	engine.mu.Unlock()

	if s.ctx == nil {
		return "", fmt.Errorf("application context not set")
	}
	if start < 0 {
		return "", errors.New("export start must be non-negative")
	}
	if length <= 0 {
		return "", errors.New("this finding does not have a known exportable length")
	}
	size := doc.Size()
	if start >= size {
		return "", errors.New("export start is outside the document")
	}
	if length > size-start {
		length = size - start
	}
	defaultName := fmt.Sprintf("leviathan-%08x.bin", uint64(start))
	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Export Found File",
		DefaultFilename: safeExportName(name, defaultName),
	})
	if err != nil || path == "" {
		return "", err
	}
	return path, writeLeviathanDocumentRangeFile(path, doc, start, length)
}

func (l *leviathanState) stateLocked() leviathanDocState {
	state := leviathanDocState{}
	if l.doc != nil {
		state = leviathanDocStateFor(l.doc, true)
	}
	if l.cmp != nil {
		cmp := leviathanDocStateFor(l.cmp, false)
		state.Compare = &cmp
	}
	return state
}

func leviathanDocStateFor(d *document.Document, preview bool) leviathanDocState {
	s := leviathanDocState{
		Name:    d.Name(),
		Size:    d.Size(),
		Dirty:   d.IsDirty(),
		UndoLen: d.HistoryLen(),
		RedoLen: d.RedoLen(),
	}
	if preview {
		s.PreviewHex = leviathanHexRows(d, 0, 512, 8)
	}
	return s
}

func (l *leviathanState) runToolLocked(tool string, params map[string]any) (map[string]any, error) {
	start := int64Param(params, "start", 0)
	length := int64Param(params, "length", -1)
	switch tool {
	case "overview":
		return analysis.Overview(l.doc)
	case "hashes":
		return analysis.Hashes(l.doc, start, length)
	case "entropy":
		return analysis.Entropy(l.doc, start, length, int64Param(params, "window", 4096), int64Param(params, "step", 4096))
	case "strings":
		items, err := analysis.ExtractStrings(l.doc, start, length, intParam(params, "min", 4), []string{"ascii", "utf16le", "utf16be"}, intParam(params, "max", 5000))
		return map[string]any{"items": items}, err
	case "iocs":
		return analysis.ExtractIOCs(l.doc, start, length, intParam(params, "min", 5))
	case "carve":
		items, err := analysis.Carve(l.doc, start, length, intParam(params, "max", 256))
		return map[string]any{"items": items}, err
	case "keys":
		items, err := analysis.ScanKeys(l.doc)
		return map[string]any{"items": items}, err
	case "timestamps":
		return analysis.ScanTimestamps(l.doc, start, length, intParam(params, "max", 1000))
	case "search":
		return l.runSearch(params, start, length)
	case "multi-search":
		return analysis.MultiSearch(l.doc, start, length, stringParam(params, "spec", ""), intParam(params, "max", 1000))
	case "parse":
		name, tree, err := parseLeviathanDocument(l.doc, stringParam(params, "kind", "auto"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"parser": name, "tree": tree.ToMap()}, nil
	case "disasm":
		return l.runDisasm(params, start, length)
	case "diff":
		if l.cmp == nil {
			return nil, errors.New("open a compare file first")
		}
		return analysis.Diff(l.doc, l.cmp, start, length)
	case "stego":
		out, err := stego.AnalyseAppended(l.doc)
		if err != nil {
			return nil, err
		}
		meta, _ := stego.ScanMetadata(l.doc)
		out["metadata"] = meta
		return out, nil
	case "metadata":
		items, err := stego.ScanMetadata(l.doc)
		return map[string]any{"items": items}, err
	case "pdf":
		return pdf.Parse(l.doc)
	case "squashfs":
		fs, err := squashfs.Open(l.doc)
		if err != nil {
			return nil, err
		}
		entries, err := fs.Walk()
		return map[string]any{"superblock": fs.Superblock(), "compression": fs.CompressionName(), "entries": entries}, err
	case "binsnoop":
		results, err := leviathanBinsnoopDocument(l.doc, intParam(params, "max", 10000))
		return map[string]any{"results": results, "signatures": binsnoop.ListSignatures()}, err
	case "rules":
		rules, err := analysis.ParseRules(stringParam(params, "source", ""))
		if err != nil {
			return nil, err
		}
		items, err := analysis.RunRules(l.doc, start, length, rules, intParam(params, "max", 1000))
		return map[string]any{"items": items}, err
	case "patch-plan":
		steps, err := patch.Parse(stringParam(params, "source", ""))
		if err != nil {
			return nil, err
		}
		plan, err := patch.Plan(l.doc, steps)
		return map[string]any{"plan": plan}, err
	case "patch-apply":
		steps, err := patch.Parse(stringParam(params, "source", ""))
		if err != nil {
			return nil, err
		}
		if _, err := patch.Plan(l.doc, steps); err != nil {
			return nil, err
		}
		return map[string]any{"state": l.stateLocked()}, patch.Apply(l.doc, steps)
	case "transform":
		return l.runTransform(params, start, length)
	case "hex":
		return map[string]any{"rows": leviathanHexRows(l.doc, start, int64Param(params, "bytes", 1024), intParam(params, "width", 16))}, nil
	case "case":
		return nil, errors.New("case export must run through the service")
	default:
		return nil, errors.New("unknown tool: " + tool)
	}
}

func (l *leviathanState) runSearch(params map[string]any, start, length int64) (map[string]any, error) {
	mode := stringParam(params, "mode", "text")
	query := stringParam(params, "query", "")
	max := intParam(params, "max", 1000)
	switch mode {
	case "hex":
		b, err := hex.DecodeString(strings.ReplaceAll(query, " ", ""))
		if err != nil {
			return nil, err
		}
		hits, err := analysis.SearchBytes(l.doc, start, length, b, max)
		return map[string]any{"hits": analysis.HitsToMaps(hits)}, err
	case "regex":
		hits, err := analysis.SearchRegex(l.doc, start, length, query, max)
		return map[string]any{"hits": analysis.HitsToMaps(hits)}, err
	default:
		hits, err := analysis.SearchBytes(l.doc, start, length, []byte(query), max)
		return map[string]any{"hits": analysis.HitsToMaps(hits)}, err
	}
}

func (l *leviathanState) runDisasm(params map[string]any, start, length int64) (map[string]any, error) {
	if length < 0 || length > 65536 {
		length = 4096
	}
	buf, err := l.rangeBytes(start, length)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": disasm.Disassemble(buf, start, stringParam(params, "arch", "x86_64"), intParam(params, "max", 1000))}, nil
}

func (l *leviathanState) runTransform(params map[string]any, start, length int64) (map[string]any, error) {
	if length < 0 || length > 8<<20 {
		length = min64(l.doc.Size()-start, 1<<20)
	}
	buf, err := l.rangeBytes(start, length)
	if err != nil {
		return nil, err
	}
	steps := parseLeviathanTransformPipeline(stringParam(params, "pipeline", "hex.encode"))
	results, err := transforms.ApplyPipeline(buf, steps)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(results))
	for i, r := range results {
		out = append(out, map[string]any{"step": i, "length": len(r), "preview": previewLeviathanBytes(r)})
	}
	return map[string]any{"steps": steps, "results": out}, nil
}

func leviathanSpectrogramView(doc *document.Document, params map[string]any) (map[string]any, error) {
	buf, err := leviathanDocumentBytes(doc, spectrogram.MaxAudioBytes, "audio")
	if err != nil {
		return nil, err
	}
	pngBytes, summary, err := spectrogram.AudioSpectrogramPNG(buf, doc.Name(), leviathanSpectrogramViewOptions(params))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"mime":      "image/png",
		"pngBase64": base64.StdEncoding.EncodeToString(pngBytes),
		"summary":   summary,
	}, nil
}

func (s *Service) leviathanExportCase(doc *document.Document) (map[string]any, error) {
	if s.ctx == nil {
		return nil, fmt.Errorf("application context not set")
	}
	filename := strings.TrimSuffix(doc.Name(), filepath.Ext(doc.Name())) + ".case.zip"
	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Export Leviathan Case",
		DefaultFilename: filename,
	})
	if err != nil || path == "" {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	add := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	hashes, _ := analysis.Hashes(doc, 0, -1)
	overview, _ := analysis.Overview(doc)
	manifest, _ := json.MarshalIndent(map[string]any{"name": doc.Name(), "size": doc.Size(), "exportedAt": time.Now().Format(time.RFC3339), "hashes": hashes}, "", "  ")
	overviewJSON, _ := json.MarshalIndent(overview, "", "  ")
	patchesJSON, _ := json.MarshalIndent(doc.Patches(), "", "  ")
	if err := add("manifest.json", manifest); err != nil {
		return nil, err
	}
	if err := addDocument("working.bin", doc, zw); err != nil {
		return nil, err
	}
	if err := add("overview.json", overviewJSON); err != nil {
		return nil, err
	}
	if err := add("patches.json", patchesJSON); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "bytes": info.Size()}, nil
}

func (s *Service) leviathanSpectrogramEncode(doc *document.Document, params map[string]any) (map[string]any, error) {
	if s.ctx == nil {
		return nil, fmt.Errorf("application context not set")
	}
	imageBytes, err := leviathanDocumentBytes(doc, spectrogram.MaxImageBytes, "image")
	if err != nil {
		return nil, err
	}
	wavBytes, summary, err := spectrogram.ImageToWAV(imageBytes, leviathanSpectrogramEncodeOptions(params))
	if err != nil {
		return nil, err
	}
	defaultName := strings.TrimSuffix(doc.Name(), filepath.Ext(doc.Name())) + ".spectrogram.wav"
	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Save Spectrogram WAV",
		DefaultFilename: safeExportName(defaultName, "spectrogram.wav"),
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "WAV Audio (*.wav)", Pattern: "*.wav"},
		},
	})
	if err != nil || path == "" {
		return nil, err
	}
	if filepath.Ext(strings.ToLower(path)) != ".wav" {
		path += ".wav"
	}
	if err := os.WriteFile(path, wavBytes, 0600); err != nil {
		return nil, err
	}
	summary["path"] = path
	return map[string]any{"path": path, "summary": summary}, nil
}

func (s *Service) leviathanSpectrogramInsert(doc *document.Document, params map[string]any) (map[string]any, error) {
	if s.ctx == nil {
		return nil, fmt.Errorf("application context not set")
	}
	imagePath := strings.TrimSpace(stringParam(params, "imagePath", ""))
	if imagePath == "" {
		return nil, errors.New("select an image to insert")
	}
	imageBytes, err := readLeviathanSmallFile(imagePath, spectrogram.MaxImageBytes, "image")
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	audioBytes, err := leviathanDocumentBytes(doc, spectrogram.MaxAudioBytes, "audio")
	if err != nil {
		return nil, err
	}
	wavBytes, summary, err := spectrogram.InsertImageIntoAudio(imageBytes, audioBytes, doc.Name(), leviathanSpectrogramInsertOptions(params))
	if err != nil {
		return nil, err
	}
	defaultName := strings.TrimSuffix(doc.Name(), filepath.Ext(doc.Name())) + ".with-spectrogram.wav"
	path, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		Title:           "Save Audio With Spectrogram Image",
		DefaultFilename: safeExportName(defaultName, "with-spectrogram.wav"),
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "WAV Audio (*.wav)", Pattern: "*.wav"},
		},
	})
	if err != nil || path == "" {
		return nil, err
	}
	if filepath.Ext(strings.ToLower(path)) != ".wav" {
		path += ".wav"
	}
	if err := os.WriteFile(path, wavBytes, 0600); err != nil {
		return nil, err
	}
	summary["path"] = path
	summary["imagePath"] = imagePath
	return map[string]any{"path": path, "summary": summary}, nil
}

func writeLeviathanDocumentFile(path string, doc *document.Document) error {
	return writeLeviathanDocumentRangeFile(path, doc, 0, doc.Size())
}

func writeLeviathanDocumentRangeFile(path string, doc *document.Document, start, length int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := doc.ForEachChunk(start, length, func(_ int64, chunk []byte) error {
		_, err := f.Write(chunk)
		return err
	}); err != nil {
		return err
	}
	return f.Sync()
}

func addDocument(name string, doc *document.Document, zw *zip.Writer) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	return doc.ForEachChunk(0, doc.Size(), func(_ int64, chunk []byte) error {
		_, err := w.Write(chunk)
		return err
	})
}

func leviathanDocumentBytes(doc *document.Document, maxBytes int64, label string) ([]byte, error) {
	size := doc.Size()
	if maxBytes > 0 && size > maxBytes {
		return nil, fmt.Errorf("%s file is too large for this operation: %s exceeds %s", label, humanBytes(size), humanBytes(maxBytes))
	}
	return doc.Range(0, size)
}

func readLeviathanSmallFile(path string, maxBytes int64, label string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s path is a directory", label)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, fmt.Errorf("%s file is too large for this operation: %s exceeds %s", label, humanBytes(info.Size()), humanBytes(maxBytes))
	}
	return os.ReadFile(path)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PiB", value/unit)
}

func (l *leviathanState) rangeBytes(start, length int64) ([]byte, error) {
	if start < 0 {
		start = 0
	}
	if length < 0 || start+length > l.doc.Size() {
		length = l.doc.Size() - start
	}
	if length < 0 {
		length = 0
	}
	return l.doc.Range(start, length)
}

func parseLeviathanDocument(d *document.Document, kind string) (string, *parsers.Field, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || kind == "auto" {
		return parsers.DetectAndParse(d)
	}
	var tree *parsers.Field
	var err error
	switch kind {
	case "png":
		tree, err = parsers.ParsePNG(d)
	case "jpeg", "jpg":
		tree, err = parsers.ParseJPEG(d)
		kind = "jpeg"
	case "gif":
		tree, err = parsers.ParseGIF(d)
	case "bmp":
		tree, err = parsers.ParseBMP(d)
	case "zip":
		tree, err = parsers.ParseZIP(d)
	case "elf":
		tree, err = parsers.ParseELF(d)
	case "pe":
		tree, err = parsers.ParsePE(d)
	case "macho":
		tree, err = parsers.ParseMachO(d)
	case "riff":
		tree, err = parsers.ParseRIFF(d)
	case "asn1":
		tree, err = parsers.ParseASN1(d, 0, d.Size())
	case "cert":
		tree, err = parsers.ParseCerts(d)
	default:
		return "", nil, errors.New("unknown parser: " + kind)
	}
	return kind, tree, err
}

func leviathanHexRows(d *document.Document, start, length int64, width int) []map[string]any {
	if width <= 0 {
		width = 16
	}
	if length < 0 || length > 8192 {
		length = 8192
	}
	rows := []map[string]any{}
	for off := start; off < start+length && off < d.Size(); off += int64(width) {
		n := int64(width)
		if off+n > d.Size() {
			n = d.Size() - off
		}
		buf, err := d.Range(off, n)
		if err != nil {
			break
		}
		rows = append(rows, map[string]any{"offset": off, "hex": spacedLeviathanHex(buf), "bytes": byteHexCells(buf), "ascii": asciiLeviathanPreview(buf)})
	}
	return rows
}

func parseLeviathanTransformPipeline(src string) []transforms.Step {
	parts := strings.Split(src, "|")
	steps := []transforms.Step{}
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		op := fields[0]
		arg := ""
		if len(fields) > 1 {
			arg = strings.Join(fields[1:], " ")
		}
		switch op {
		case "hex":
			op = "hex.encode"
		case "unhex":
			op = "hex.decode"
		case "base64", "b64":
			op = "base64.encode"
		case "unbase64", "unb64":
			op = "base64.decode"
		case "url":
			op = "url.encode"
		case "unurl":
			op = "url.decode"
		}
		steps = append(steps, transforms.Step{Op: op, Arg: arg})
	}
	return steps
}

func spacedLeviathanHex(buf []byte) string {
	return strings.Join(byteHexCells(buf), " ")
}

func byteHexCells(buf []byte) []string {
	parts := make([]string, len(buf))
	for i, b := range buf {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return parts
}

func asciiLeviathanPreview(buf []byte) string {
	var b strings.Builder
	for _, c := range buf {
		if c >= 32 && c < 127 {
			b.WriteByte(c)
		} else {
			b.WriteByte('.')
		}
	}
	return b.String()
}

func previewLeviathanBytes(buf []byte) string {
	if len(buf) > 2048 {
		buf = buf[:2048]
	}
	if isMostlyLeviathanText(buf) {
		return string(buf)
	}
	return spacedLeviathanHex(buf)
}

func isMostlyLeviathanText(buf []byte) bool {
	if len(buf) == 0 {
		return true
	}
	printable := 0
	for _, b := range buf {
		if b == '\n' || b == '\r' || b == '\t' || (b >= 32 && b < 127) {
			printable++
		}
	}
	return printable*100/len(buf) > 85
}

func intParam(m map[string]any, key string, def int) int {
	return int(int64Param(m, key, int64(def)))
}

func int64Param(m map[string]any, key string, def int64) int64 {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		if n == "" {
			return def
		}
		if parsed, err := strconv.ParseInt(n, 0, 64); err == nil {
			return parsed
		}
	}
	return def
}

func stringParam(m map[string]any, key, def string) string {
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return def
}

func floatParam(m map[string]any, key string, def float64) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		if n == "" {
			return def
		}
		if parsed, err := strconv.ParseFloat(n, 64); err == nil {
			return parsed
		}
	}
	return def
}

func boolParam(m map[string]any, key string, def bool) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		if b == "" {
			return def
		}
		if parsed, err := strconv.ParseBool(b); err == nil {
			return parsed
		}
	}
	return def
}

func leviathanBinsnoopDocument(d *document.Document, maxResults int) (map[string]any, error) {
	if maxResults <= 0 {
		maxResults = 10000
	}
	const windowSize int64 = 32 << 20
	const overlapSize int64 = 1 << 20

	scanner := binsnoop.New()
	size := d.Size()
	seen := make(map[string]bool)
	fileMap := make([]binsnoop.SignatureResult, 0, 128)
	truncated := false

	maxInt := int64(^uint(0) >> 1)
	for off := int64(0); off < size; {
		readLen := windowSize + overlapSize
		if off+readLen > size {
			readLen = size - off
		}
		buf, err := d.Range(off, readLen)
		if err != nil {
			return nil, err
		}
		for _, hit := range scanner.Scan(buf) {
			abs := off + int64(hit.Offset)
			if abs < 0 || abs > maxInt || abs >= size {
				continue
			}
			if hit.Size < 0 || int64(hit.Size) > size-abs {
				continue
			}
			hit.Offset = int(abs)
			key := fmt.Sprintf("%d:%s", hit.Offset, hit.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			fileMap = append(fileMap, hit)
			if len(fileMap) >= maxResults {
				truncated = true
				break
			}
		}
		if truncated || off+windowSize >= size {
			break
		}
		off += windowSize
	}

	sort.Slice(fileMap, func(i, j int) bool {
		if fileMap[i].Offset == fileMap[j].Offset {
			return fileMap[i].Confidence > fileMap[j].Confidence
		}
		return fileMap[i].Offset < fileMap[j].Offset
	})

	return map[string]any{
		"file_path":      d.Name(),
		"file_map":       fileMap,
		"scanned_bytes":  size,
		"window_bytes":   windowSize,
		"overlap_bytes":  overlapSize,
		"whole_file":     true,
		"windowed":       true,
		"truncated":      truncated,
		"max_results":    maxResults,
		"extractions":    map[string]any{},
		"result_count":   len(fileMap),
		"document_bytes": size,
	}, nil
}

func leviathanSpectrogramEncodeOptions(params map[string]any) spectrogram.EncodeOptions {
	return spectrogram.EncodeOptions{
		DurationSec: floatParam(params, "durationSec", 5),
		SampleRate:  intParam(params, "sampleRate", 44100),
		MinHz:       floatParam(params, "minHz", 200),
		MaxHz:       floatParam(params, "maxHz", 20000),
		Bins:        intParam(params, "bins", 160),
		Columns:     intParam(params, "columns", 768),
		Invert:      boolParam(params, "invert", true),
		PreserveAR:  boolParam(params, "preserveAspectRatio", true),
		Gain:        floatParam(params, "gain", 0.85),
	}
}

func leviathanSpectrogramInsertOptions(params map[string]any) spectrogram.InsertOptions {
	return spectrogram.InsertOptions{
		EncodeOptions: leviathanSpectrogramEncodeOptions(params),
		StartSec:      floatParam(params, "startSec", 0),
		MixLevel:      floatParam(params, "mixLevel", 0.35),
	}
}

func leviathanSpectrogramViewOptions(params map[string]any) spectrogram.ViewOptions {
	return spectrogram.ViewOptions{
		FFTSize:    intParam(params, "fftSize", 2048),
		Height:     intParam(params, "height", 1024),
		MaxWidth:   intParam(params, "maxWidth", 1400),
		Aspect:     floatParam(params, "aspectRatio", 1),
		MinHz:      floatParam(params, "minHz", 0),
		MaxHz:      floatParam(params, "maxHz", 0),
		Brightness: floatParam(params, "brightness", 1),
		Contrast:   floatParam(params, "contrast", 1),
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func safeExportName(name, fallback string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || strings.ContainsRune(name, 0) {
		return fallback
	}
	return name
}
