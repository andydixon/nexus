// Package document implements the chunked binary document model.
//
// The document holds three conceptual layers per slot:
//
//	source  — immutable original bytes, loaded once.
//	patch   — sparse overlay of modified bytes keyed by offset.
//	working — logical view = source with patch applied on read.
//
// Reads happen through windowed Range() calls so neither the editor
// nor analysis services need to materialise the whole file.
package document

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ChunkSize is the granularity at which source bytes are exposed to
// analysis services. It is independent of the view row width.
const ChunkSize = 64 * 1024

// Document represents one loaded artefact. A Session may hold many.
type Document struct {
	mu      sync.RWMutex
	name    string
	source  []byte // original immutable content
	path    string // optional immutable file-backed source
	size    int64
	patch   map[int64]byte
	history []EditOp
	redo    []EditOp
	cursor  int
}

// EditOp records a single byte-level mutation for undo/redo.
type EditOp struct {
	Offset int64
	Old    byte
	New    byte
	Kind   string // "set", "fill", "xor"
}

// New creates a Document from raw source bytes. The slice is not
// copied; callers must not mutate it after handoff.
func New(name string, src []byte) *Document {
	return &Document{
		name:   name,
		source: src,
		size:   int64(len(src)),
		patch:  make(map[int64]byte),
	}
}

// Open creates a Document backed by an immutable file on disk. Source
// bytes are read on demand through Range rather than loaded eagerly.
func Open(path string) (*Document, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("document path is a directory")
	}
	return &Document{
		name:  filepath.Base(path),
		path:  path,
		size:  st.Size(),
		patch: make(map[int64]byte),
	}, nil
}

// Name returns the document's display name.
func (d *Document) Name() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.name
}

// Size returns the logical document length in bytes.
func (d *Document) Size() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.size
}

// At returns the logical byte at offset (patch takes precedence).
func (d *Document) At(offset int64) (byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if offset < 0 || offset >= d.size {
		return 0, errors.New("offset out of range")
	}
	if b, ok := d.patch[offset]; ok {
		return b, nil
	}
	return d.sourceByteLocked(offset)
}

// Range copies [start, start+length) into dst. It applies the patch
// overlay. Callers get a fresh slice they are free to retain.
func (d *Document) Range(start, length int64) ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	n := d.size
	if start < 0 || start > n {
		return nil, errors.New("range start out of bounds")
	}
	if length < 0 {
		return nil, errors.New("negative range")
	}
	if length > n-start {
		length = n - start
	}
	out, err := d.sourceRangeLocked(start, length)
	if err != nil {
		return nil, err
	}
	if len(d.patch) > 0 {
		end := start + length
		for off, b := range d.patch {
			if off >= start && off < end {
				out[off-start] = b
			}
		}
	}
	return out, nil
}

// IsDirty reports whether any byte has been patched.
func (d *Document) IsDirty() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.patch) > 0
}

// Set patches a single byte. Returns the previous value.
func (d *Document) Set(offset int64, b byte) (byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if offset < 0 || offset >= d.size {
		return 0, errors.New("offset out of range")
	}
	orig, err := d.sourceByteLocked(offset)
	if err != nil {
		return 0, err
	}
	var old byte
	if v, ok := d.patch[offset]; ok {
		old = v
	} else {
		old = orig
	}
	if b == orig {
		delete(d.patch, offset)
	} else {
		d.patch[offset] = b
	}
	d.history = append(d.history, EditOp{Offset: offset, Old: old, New: b, Kind: "set"})
	d.redo = nil
	return old, nil
}

// Fill patches [start,start+length) with b.
func (d *Document) Fill(start, length int64, b byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validateRangeLocked("fill", start, length); err != nil {
		return err
	}
	orig, err := d.sourceRangeLocked(start, length)
	if err != nil {
		return err
	}
	for i := int64(0); i < length; i++ {
		off := start + i
		origB := orig[i]
		var old byte
		if v, ok := d.patch[off]; ok {
			old = v
		} else {
			old = origB
		}
		if b == origB {
			delete(d.patch, off)
		} else {
			d.patch[off] = b
		}
		d.history = append(d.history, EditOp{Offset: off, Old: old, New: b, Kind: "fill"})
	}
	d.redo = nil
	return nil
}

// XOR patches [start,start+length) with a repeating key.
func (d *Document) XOR(start, length int64, key []byte) error {
	if len(key) == 0 {
		return errors.New("empty key")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validateRangeLocked("xor", start, length); err != nil {
		return err
	}
	orig, err := d.sourceRangeLocked(start, length)
	if err != nil {
		return err
	}
	for i := int64(0); i < length; i++ {
		off := start + i
		origB := orig[i]
		var cur byte
		if v, ok := d.patch[off]; ok {
			cur = v
		} else {
			cur = origB
		}
		newB := cur ^ key[i%int64(len(key))]
		if newB == origB {
			delete(d.patch, off)
		} else {
			d.patch[off] = newB
		}
		d.history = append(d.history, EditOp{Offset: off, Old: cur, New: newB, Kind: "xor"})
	}
	d.redo = nil
	return nil
}

// Copy copies [src,src+length) onto [dst,dst+length) as a patch.
func (d *Document) Copy(src, length, dst int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.validateRangeLocked("copy source", src, length); err != nil {
		return err
	}
	if err := d.validateRangeLocked("copy destination", dst, length); err != nil {
		return err
	}
	buf, err := d.workingRangeLocked(src, length)
	if err != nil {
		return err
	}
	dstOrig, err := d.sourceRangeLocked(dst, length)
	if err != nil {
		return err
	}
	for i := int64(0); i < length; i++ {
		off := dst + i
		origB := dstOrig[i]
		var cur byte
		if v, ok := d.patch[off]; ok {
			cur = v
		} else {
			cur = origB
		}
		b := buf[i]
		if b == origB {
			delete(d.patch, off)
		} else {
			d.patch[off] = b
		}
		d.history = append(d.history, EditOp{Offset: off, Old: cur, New: b, Kind: "fill"})
	}
	d.redo = nil
	return nil
}

// Undo reverts the last edit. Returns false if there is no history.
func (d *Document) Undo() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.history) == 0 {
		return false
	}
	op := d.history[len(d.history)-1]
	d.history = d.history[:len(d.history)-1]
	orig, err := d.sourceByteLocked(op.Offset)
	if err != nil {
		return false
	}
	if op.Old == orig {
		delete(d.patch, op.Offset)
	} else {
		d.patch[op.Offset] = op.Old
	}
	d.redo = append(d.redo, op)
	return true
}

// Redo reapplies the last undone edit. Returns false if there is no redo.
func (d *Document) Redo() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.redo) == 0 {
		return false
	}
	op := d.redo[len(d.redo)-1]
	d.redo = d.redo[:len(d.redo)-1]
	orig, err := d.sourceByteLocked(op.Offset)
	if err != nil {
		return false
	}
	if op.New == orig {
		delete(d.patch, op.Offset)
	} else {
		d.patch[op.Offset] = op.New
	}
	d.history = append(d.history, op)
	return true
}

// Revert drops all patches and history.
func (d *Document) Revert() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.patch = make(map[int64]byte)
	d.history = nil
	d.redo = nil
}

// Patches returns a snapshot of the patch overlay.
func (d *Document) Patches() map[int64]byte {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[int64]byte, len(d.patch))
	for k, v := range d.patch {
		out[k] = v
	}
	return out
}

// HistoryLen returns the number of recorded edits.
func (d *Document) HistoryLen() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.history)
}

// RedoLen returns the number of redoable edits.
func (d *Document) RedoLen() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.redo)
}

// ForEachChunk yields chunks covering [start,start+length) with the
// working-copy bytes visible. It is suitable for analysis passes that
// want bounded memory use.
func (d *Document) ForEachChunk(start, length int64, fn func(off int64, chunk []byte) error) error {
	d.mu.RLock()
	n := d.size
	d.mu.RUnlock()
	if start < 0 {
		start = 0
	}
	end := start + length
	if length < 0 || end > n {
		end = n
	}
	off := start
	for off < end {
		chunkEnd := off + ChunkSize
		if chunkEnd > end {
			chunkEnd = end
		}
		buf, err := d.Range(off, chunkEnd-off)
		if err != nil {
			return err
		}
		if err := fn(off, buf); err != nil {
			return err
		}
		off = chunkEnd
	}
	return nil
}

func (d *Document) sourceByteLocked(offset int64) (byte, error) {
	buf, err := d.sourceRangeLocked(offset, 1)
	if err != nil {
		return 0, err
	}
	if len(buf) == 0 {
		return 0, errors.New("offset out of range")
	}
	return buf[0], nil
}

func (d *Document) workingRangeLocked(start, length int64) ([]byte, error) {
	buf, err := d.sourceRangeLocked(start, length)
	if err != nil {
		return nil, err
	}
	for off, b := range d.patch {
		if off >= start && off < start+length {
			buf[off-start] = b
		}
	}
	return buf, nil
}

func (d *Document) sourceRangeLocked(start, length int64) ([]byte, error) {
	if err := d.validateRangeLocked("range", start, length); err != nil {
		return nil, err
	}
	if length == 0 {
		return []byte{}, nil
	}
	if d.path == "" {
		out := make([]byte, length)
		copy(out, d.source[start:start+length])
		return out, nil
	}
	f, err := os.Open(d.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := make([]byte, length)
	_, err = f.ReadAt(out, start)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Document) validateRangeLocked(label string, start, length int64) error {
	if start < 0 || start > d.size || length < 0 || length > d.size-start {
		return fmt.Errorf("%s range invalid", label)
	}
	return nil
}
