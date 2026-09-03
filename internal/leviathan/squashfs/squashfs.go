// Package squashfs implements a read-only SquashFS 4.0 extractor.
//
// Scope is deliberately pragmatic: gzip, xz and zstd-compressed images,
// basic and extended file and directory inodes, symlink inodes. Other
// compressors (lzma/lzo/lz4) return a structured error so the UI can show
// what's missing.
//
// The module never trusts its input: every table offset and length is
// bounded against the image size and a hard cap on uncompressed metadata
// block size. A malformed or truncated image returns an error rather
// than causing a wild read.
package squashfs

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"

	"github.com/andydixon/link/internal/leviathan/document"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

const (
	magicLE           uint32 = 0x73717368 // "hsqs"
	metadataBlockSize        = 8192
	dataBlockMax             = 1 << 20 // 1 MiB guard for a single data block
	maxEntries               = 1 << 20 // total files + dirs we're willing to walk
)

// Compression IDs from the spec.
const (
	compGZIP = 1
	compLZMA = 2
	compLZO  = 3
	compXZ   = 4
	compLZ4  = 5
	compZSTD = 6
)

// Inode type codes.
const (
	itBasicDir      = 1
	itBasicFile     = 2
	itBasicSymlink  = 3
	itBasicBlockDev = 4
	itBasicCharDev  = 5
	itBasicFifo     = 6
	itBasicSocket   = 7
	itExtDir        = 8
	itExtFile       = 9
	itExtSymlink    = 10
	itExtBlockDev   = 11
	itExtCharDev    = 12
	itExtFifo       = 13
	itExtSocket     = 14
)

// Superblock is the parsed 96-byte header.
type Superblock struct {
	Magic           uint32
	InodeCount      uint32
	ModTime         uint32
	BlockSize       uint32
	FragCount       uint32
	CompressionID   uint16
	BlockLog        uint16
	Flags           uint16
	IDCount         uint16
	VersionMajor    uint16
	VersionMinor    uint16
	RootInodeRef    uint64
	BytesUsed       uint64
	IDTableStart    uint64
	XattrTableStart uint64
	InodeTable      uint64
	DirTable        uint64
	FragTable       uint64
	ExportTable     uint64
}

// Entry describes a filesystem entry discovered by the walker.
type Entry struct {
	Path        string
	Type        string // "file", "dir", "symlink", "special"
	Size        int64
	Mode        uint16
	UID         uint16 // id_idx (not resolved)
	GID         uint16
	Mtime       uint32
	InodeNumber uint32
	InodeRef    uint64 // 64-bit metadata reference (for targeted extraction)
	LinkTarget  string // for symlinks
	// For file extraction; zero for non-files.
	blocksStart uint64
	fileSize    int64
	fragIndex   uint32
	fragOffset  uint32
	blockSizes  []uint32 // raw (top bit=uncompressed)
}

// FS is the opened image handle.
type FS struct {
	d  *document.Document
	sb Superblock
}

// Open validates the superblock and returns an FS for further queries.
func Open(d *document.Document) (*FS, error) {
	head, err := d.Range(0, 96)
	if err != nil {
		return nil, fmt.Errorf("read superblock: %w", err)
	}
	sb, err := parseSuperblock(head)
	if err != nil {
		return nil, err
	}
	if sb.VersionMajor != 4 {
		return nil, fmt.Errorf("unsupported squashfs version %d.%d (only 4.x)", sb.VersionMajor, sb.VersionMinor)
	}
	if sb.BytesUsed > uint64(d.Size()) {
		return nil, fmt.Errorf("superblock bytes_used=%d exceeds document size=%d", sb.BytesUsed, d.Size())
	}
	return &FS{d: d, sb: sb}, nil
}

// Superblock returns the parsed header (copy).
func (f *FS) Superblock() Superblock { return f.sb }

// CompressionName returns the human name of the image compressor.
func (f *FS) CompressionName() string {
	return compressionName(f.sb.CompressionID)
}

func compressionName(id uint16) string {
	switch id {
	case compGZIP:
		return "gzip"
	case compLZMA:
		return "lzma"
	case compLZO:
		return "lzo"
	case compXZ:
		return "xz"
	case compLZ4:
		return "lz4"
	case compZSTD:
		return "zstd"
	}
	return fmt.Sprintf("unknown(%d)", id)
}

func parseSuperblock(b []byte) (Superblock, error) {
	var s Superblock
	if len(b) < 96 {
		return s, errors.New("superblock: short read")
	}
	s.Magic = binary.LittleEndian.Uint32(b[0:4])
	if s.Magic != magicLE {
		return s, fmt.Errorf("bad magic 0x%08x (want 0x%08x)", s.Magic, magicLE)
	}
	s.InodeCount = binary.LittleEndian.Uint32(b[4:8])
	s.ModTime = binary.LittleEndian.Uint32(b[8:12])
	s.BlockSize = binary.LittleEndian.Uint32(b[12:16])
	s.FragCount = binary.LittleEndian.Uint32(b[16:20])
	s.CompressionID = binary.LittleEndian.Uint16(b[20:22])
	s.BlockLog = binary.LittleEndian.Uint16(b[22:24])
	s.Flags = binary.LittleEndian.Uint16(b[24:26])
	s.IDCount = binary.LittleEndian.Uint16(b[26:28])
	s.VersionMajor = binary.LittleEndian.Uint16(b[28:30])
	s.VersionMinor = binary.LittleEndian.Uint16(b[30:32])
	s.RootInodeRef = binary.LittleEndian.Uint64(b[32:40])
	s.BytesUsed = binary.LittleEndian.Uint64(b[40:48])
	s.IDTableStart = binary.LittleEndian.Uint64(b[48:56])
	s.XattrTableStart = binary.LittleEndian.Uint64(b[56:64])
	s.InodeTable = binary.LittleEndian.Uint64(b[64:72])
	s.DirTable = binary.LittleEndian.Uint64(b[72:80])
	s.FragTable = binary.LittleEndian.Uint64(b[80:88])
	s.ExportTable = binary.LittleEndian.Uint64(b[88:96])
	return s, nil
}

// ---------- metadata block reader ----------

func (f *FS) decompress(buf []byte) ([]byte, error) {
	return f.decompressWithLimit(buf, metadataBlockSize+16)
}

func (f *FS) decompressWithLimit(buf []byte, limit int64) ([]byte, error) {
	var r io.ReadCloser
	switch f.sb.CompressionID {
	case compGZIP:
		zr, err := zlib.NewReader(bytes.NewReader(buf))
		if err != nil {
			return nil, fmt.Errorf("zlib init: %w", err)
		}
		r = zr
	case compXZ:
		xr, err := xz.NewReader(bytes.NewReader(buf))
		if err != nil {
			return nil, fmt.Errorf("xz init: %w", err)
		}
		r = io.NopCloser(xr)
	case compZSTD:
		zr, err := zstd.NewReader(bytes.NewReader(buf))
		if err != nil {
			return nil, fmt.Errorf("zstd init: %w", err)
		}
		r = zr.IOReadCloser()
	default:
		return nil, fmt.Errorf("compressor %q not implemented in this build", compressionName(f.sb.CompressionID))
	}
	defer r.Close()
	return readLimited(r, limit)
}

// readMetadataBlock reads one metadata block starting at absolute offset
// `off` and returns (data, bytesConsumed, error). bytesConsumed includes
// the 2-byte header.
func (f *FS) readMetadataBlock(off int64) ([]byte, int64, error) {
	if off < 0 || off+2 > f.d.Size() {
		return nil, 0, fmt.Errorf("metadata block offset %d out of range", off)
	}
	hdrBuf, err := f.d.Range(off, 2)
	if err != nil {
		return nil, 0, err
	}
	hdr := binary.LittleEndian.Uint16(hdrBuf)
	compressed := hdr&0x8000 == 0
	size := int64(hdr & 0x7fff)
	if size == 0 || size > metadataBlockSize {
		return nil, 0, fmt.Errorf("metadata block size %d invalid", size)
	}
	body, err := f.d.Range(off+2, size)
	if err != nil {
		return nil, 0, err
	}
	if !compressed {
		return append([]byte(nil), body...), size + 2, nil
	}
	out, err := f.decompress(body)
	if err != nil {
		return nil, 0, err
	}
	return out, size + 2, nil
}

// metaCursor streams uncompressed data out of a chain of metadata blocks.
// table[i] is the absolute file offset of the i-th metadata block, plus
// a running byte cursor within the uncompressed stream.
type metaCursor struct {
	f      *FS
	base   int64  // absolute file offset of first metadata block
	blkAbs int64  // absolute file offset of *current* metadata block
	inBlk  int    // index within current uncompressed block
	buffer []byte // current decompressed block
}

func (f *FS) metaCursorAt(base int64, blockOffset uint32, inBlk uint16) (*metaCursor, error) {
	mc := &metaCursor{f: f, base: base, blkAbs: base + int64(blockOffset)}
	if err := mc.load(); err != nil {
		return nil, err
	}
	if int(inBlk) > len(mc.buffer) {
		return nil, fmt.Errorf("inode intra-block offset %d > block length %d", inBlk, len(mc.buffer))
	}
	mc.inBlk = int(inBlk)
	return mc, nil
}

func (mc *metaCursor) load() error {
	data, _, err := mc.f.readMetadataBlock(mc.blkAbs)
	if err != nil {
		return err
	}
	mc.buffer = data
	mc.inBlk = 0
	return nil
}

// advance one metadata block. The on-disk size is the consumed length of
// the current compressed block, which we must read from the header again.
func (mc *metaCursor) nextBlock() error {
	// Re-read just the 2-byte header of current block to find its length.
	hdrBuf, err := mc.f.d.Range(mc.blkAbs, 2)
	if err != nil {
		return err
	}
	hdr := binary.LittleEndian.Uint16(hdrBuf)
	size := int64(hdr & 0x7fff)
	mc.blkAbs += 2 + size
	return mc.load()
}

// readN returns n uncompressed bytes, crossing metadata-block boundaries
// as required.
func (mc *metaCursor) readN(n int) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	out := make([]byte, 0, n)
	for n > 0 {
		if mc.inBlk >= len(mc.buffer) {
			if err := mc.nextBlock(); err != nil {
				return nil, err
			}
		}
		take := len(mc.buffer) - mc.inBlk
		if take > n {
			take = n
		}
		out = append(out, mc.buffer[mc.inBlk:mc.inBlk+take]...)
		mc.inBlk += take
		n -= take
	}
	return out, nil
}

// ---------- inode ----------

type inodeHeader struct {
	Type  uint16
	Mode  uint16
	UID   uint16
	GID   uint16
	Mtime uint32
	Num   uint32
}

func readInodeHeader(b []byte) (inodeHeader, error) {
	var h inodeHeader
	if len(b) < 16 {
		return h, errors.New("inode header short")
	}
	h.Type = binary.LittleEndian.Uint16(b[0:2])
	h.Mode = binary.LittleEndian.Uint16(b[2:4])
	h.UID = binary.LittleEndian.Uint16(b[4:6])
	h.GID = binary.LittleEndian.Uint16(b[6:8])
	h.Mtime = binary.LittleEndian.Uint32(b[8:12])
	h.Num = binary.LittleEndian.Uint32(b[12:16])
	return h, nil
}

// readInodeAt reads the inode described by a 64-bit ref and returns the
// filled Entry plus the metadata location of the directory contents (if
// it's a directory).
type inodeContent struct {
	entry       Entry
	dirStart    uint32 // offset into DirTable (bytes from sb.DirTable)
	dirBlockOff uint16 // intra-metadata-block offset for first dir header
	dirSize     uint32 // directory listing size, real value (file_size - 3)
	parentInode uint32
}

func (f *FS) readInode(ref uint64) (*inodeContent, error) {
	blkOff := uint32(ref >> 16)
	inBlk := uint16(ref & 0xffff)
	mc, err := f.metaCursorAt(int64(f.sb.InodeTable), blkOff, inBlk)
	if err != nil {
		return nil, err
	}
	hdrBytes, err := mc.readN(16)
	if err != nil {
		return nil, err
	}
	h, err := readInodeHeader(hdrBytes)
	if err != nil {
		return nil, err
	}
	ic := &inodeContent{
		entry: Entry{
			Mode:        h.Mode,
			UID:         h.UID,
			GID:         h.GID,
			Mtime:       h.Mtime,
			InodeNumber: h.Num,
			InodeRef:    ref,
		},
	}
	switch h.Type {
	case itBasicDir:
		body, err := mc.readN(16)
		if err != nil {
			return nil, err
		}
		ic.entry.Type = "dir"
		ic.dirStart = binary.LittleEndian.Uint32(body[0:4])
		// nlink := binary.LittleEndian.Uint32(body[4:8])
		fileSize := binary.LittleEndian.Uint16(body[8:10])
		ic.dirBlockOff = binary.LittleEndian.Uint16(body[10:12])
		ic.parentInode = binary.LittleEndian.Uint32(body[12:16])
		// file_size in dir inodes is real dir bytes + 3. Zero-size (3) → empty dir.
		if fileSize >= 3 {
			ic.dirSize = uint32(fileSize) - 3
		}
	case itExtDir:
		body, err := mc.readN(24)
		if err != nil {
			return nil, err
		}
		ic.entry.Type = "dir"
		// nlink := binary.LittleEndian.Uint32(body[0:4])
		fileSize := binary.LittleEndian.Uint32(body[4:8])
		ic.dirStart = binary.LittleEndian.Uint32(body[8:12])
		ic.parentInode = binary.LittleEndian.Uint32(body[12:16])
		indexCount := binary.LittleEndian.Uint16(body[16:18])
		ic.dirBlockOff = binary.LittleEndian.Uint16(body[18:20])
		// xattr := binary.LittleEndian.Uint32(body[20:24])
		if fileSize >= 3 {
			ic.dirSize = fileSize - 3
		}
		// Skip index entries: indexCount × (4 + 4 + 4 + name[...variable]).
		for i := uint16(0); i < indexCount; i++ {
			hdr, err := mc.readN(12)
			if err != nil {
				return nil, err
			}
			nameSize := binary.LittleEndian.Uint32(hdr[8:12])
			if nameSize > 256 {
				return nil, fmt.Errorf("dir index name_size too large: %d", nameSize)
			}
			if _, err := mc.readN(int(nameSize) + 1); err != nil {
				return nil, err
			}
		}
	case itBasicFile:
		body, err := mc.readN(16)
		if err != nil {
			return nil, err
		}
		ic.entry.Type = "file"
		ic.entry.blocksStart = uint64(binary.LittleEndian.Uint32(body[0:4]))
		ic.entry.fragIndex = binary.LittleEndian.Uint32(body[4:8])
		ic.entry.fragOffset = binary.LittleEndian.Uint32(body[8:12])
		fileSize := binary.LittleEndian.Uint32(body[12:16])
		ic.entry.fileSize = int64(fileSize)
		ic.entry.Size = int64(fileSize)
		blocks, err := readBlockSizes(mc, fileSize, ic.entry.fragIndex, f.sb.BlockSize)
		if err != nil {
			return nil, err
		}
		ic.entry.blockSizes = blocks
	case itExtFile:
		body, err := mc.readN(40)
		if err != nil {
			return nil, err
		}
		ic.entry.Type = "file"
		ic.entry.blocksStart = binary.LittleEndian.Uint64(body[0:8])
		fileSize := binary.LittleEndian.Uint64(body[8:16])
		// sparse   := binary.LittleEndian.Uint64(body[16:24])
		// nlink    := binary.LittleEndian.Uint32(body[24:28])
		ic.entry.fragIndex = binary.LittleEndian.Uint32(body[28:32])
		ic.entry.fragOffset = binary.LittleEndian.Uint32(body[32:36])
		// xattr := binary.LittleEndian.Uint32(body[36:40])
		ic.entry.fileSize = int64(fileSize)
		ic.entry.Size = int64(fileSize)
		if fileSize > uint64(1)<<48 {
			return nil, fmt.Errorf("unreasonable file size %d", fileSize)
		}
		blocks, err := readBlockSizes(mc, uint32(fileSize), ic.entry.fragIndex, f.sb.BlockSize)
		if err != nil {
			return nil, err
		}
		ic.entry.blockSizes = blocks
	case itBasicSymlink, itExtSymlink:
		body, err := mc.readN(8)
		if err != nil {
			return nil, err
		}
		// nlink := binary.LittleEndian.Uint32(body[0:4])
		target := binary.LittleEndian.Uint32(body[4:8])
		if target > 4096 {
			return nil, fmt.Errorf("symlink target length %d too large", target)
		}
		link, err := mc.readN(int(target))
		if err != nil {
			return nil, err
		}
		ic.entry.Type = "symlink"
		ic.entry.LinkTarget = string(link)
		ic.entry.Size = int64(target)
		if h.Type == itExtSymlink {
			// trailing xattr u32
			if _, err := mc.readN(4); err != nil {
				return nil, err
			}
		}
	default:
		ic.entry.Type = "special"
	}
	return ic, nil
}

func readBlockSizes(mc *metaCursor, fileSize uint32, fragIdx uint32, blockSize uint32) ([]uint32, error) {
	// If there is a fragment (fragIdx != 0xFFFFFFFF), the tail of the file
	// (< blockSize) is stored there — do not count it as a full block.
	full := fileSize / blockSize
	if fragIdx == 0xFFFFFFFF && fileSize%blockSize != 0 {
		full++ // final partial block stored on disk as a block
	}
	if full > (1 << 20) {
		return nil, fmt.Errorf("unreasonable block count %d", full)
	}
	out := make([]uint32, full)
	for i := range out {
		b, err := mc.readN(4)
		if err != nil {
			return nil, err
		}
		out[i] = binary.LittleEndian.Uint32(b)
	}
	return out, nil
}

// ---------- directory walking ----------

// readDirEntries returns all (name, inode-ref) tuples in a directory.
func (f *FS) readDirEntries(parent *inodeContent) ([]dirEntry, error) {
	if parent.dirSize == 0 {
		return nil, nil
	}
	// dirStart is an offset into the dir table (relative to sb.DirTable).
	// dirBlockOff is the intra-block start.
	mc, err := f.metaCursorAt(int64(f.sb.DirTable), parent.dirStart, parent.dirBlockOff)
	if err != nil {
		return nil, err
	}
	remaining := int(parent.dirSize)
	var out []dirEntry
	for remaining > 0 {
		hdr, err := mc.readN(12)
		if err != nil {
			return nil, err
		}
		remaining -= 12
		count := binary.LittleEndian.Uint32(hdr[0:4]) + 1 // stored as count-1
		startBlock := binary.LittleEndian.Uint32(hdr[4:8])
		baseInode := binary.LittleEndian.Uint32(hdr[8:12])
		for i := uint32(0); i < count; i++ {
			entHdr, err := mc.readN(8)
			if err != nil {
				return nil, err
			}
			remaining -= 8
			entOff := binary.LittleEndian.Uint16(entHdr[0:2])
			iDelta := int16(binary.LittleEndian.Uint16(entHdr[2:4]))
			entType := binary.LittleEndian.Uint16(entHdr[4:6])
			nameSize := binary.LittleEndian.Uint16(entHdr[6:8])
			if nameSize+1 > 256 {
				return nil, fmt.Errorf("directory entry name_size %d too large", nameSize)
			}
			name, err := mc.readN(int(nameSize) + 1) // stored as name_size-1
			if err != nil {
				return nil, err
			}
			remaining -= int(nameSize) + 1
			ref := (uint64(startBlock) << 16) | uint64(entOff)
			out = append(out, dirEntry{
				name:     string(name),
				ref:      ref,
				inodeNum: uint32(int64(baseInode) + int64(iDelta)),
				entType:  entType,
			})
			if len(out) > maxEntries {
				return nil, fmt.Errorf("directory entry cap %d exceeded", maxEntries)
			}
		}
	}
	return out, nil
}

type dirEntry struct {
	name     string
	ref      uint64
	inodeNum uint32
	entType  uint16
}

// Walk walks the entire filesystem from the root, emitting every Entry.
// The returned list is sorted by path. Walk is bounded by maxEntries
// and by recursion depth.
func (f *FS) Walk() ([]Entry, error) {
	root, err := f.readInode(f.sb.RootInodeRef)
	if err != nil {
		return nil, fmt.Errorf("root inode: %w", err)
	}
	if root.entry.Type != "dir" {
		return nil, fmt.Errorf("root is not a directory (type %q)", root.entry.Type)
	}
	out := []Entry{}
	root.entry.Path = "/"
	out = append(out, root.entry)
	if err := f.walk(root, "/", &out, 0); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *FS) walk(node *inodeContent, prefix string, out *[]Entry, depth int) error {
	if depth > 64 {
		return errors.New("directory depth exceeds 64 — possible cycle")
	}
	entries, err := f.readDirEntries(node)
	if err != nil {
		return err
	}
	for _, de := range entries {
		child, err := f.readInode(de.ref)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", prefix, de.name, err)
		}
		full := path.Join(prefix, de.name)
		child.entry.Path = full
		*out = append(*out, child.entry)
		if len(*out) > maxEntries {
			return fmt.Errorf("entry cap %d exceeded", maxEntries)
		}
		if child.entry.Type == "dir" {
			if err := f.walk(child, full, out, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------- file extraction ----------

// fragmentEntry describes one entry in the fragment table.
type fragmentEntry struct {
	Start      uint64
	Size       uint32 // top bit = uncompressed
	Compressed bool
}

// fragmentTable reads the entire fragment lookup table and returns the
// flat list of entries by index. Result is cached on first call.
func (f *FS) fragmentTable() ([]fragmentEntry, error) {
	if f.sb.FragCount == 0 {
		return nil, nil
	}
	// The fragment_table_start points at a u64[] of metadata-block offsets,
	// one per 512 fragments (ceil).
	indirCount := (int(f.sb.FragCount) + 511) / 512
	indirBytes, err := f.d.Range(int64(f.sb.FragTable), int64(indirCount*8))
	if err != nil {
		return nil, fmt.Errorf("frag indirection: %w", err)
	}
	out := make([]fragmentEntry, 0, f.sb.FragCount)
	remaining := int(f.sb.FragCount)
	for i := 0; i < indirCount && remaining > 0; i++ {
		absOff := int64(binary.LittleEndian.Uint64(indirBytes[i*8 : i*8+8]))
		block, _, err := f.readMetadataBlock(absOff)
		if err != nil {
			return nil, err
		}
		// Each fragment entry is 16 bytes: u64 start, u32 size, u32 unused.
		take := remaining
		if take > 512 {
			take = 512
		}
		if len(block) < take*16 {
			return nil, fmt.Errorf("fragment block short: have %d need %d", len(block), take*16)
		}
		for j := 0; j < take; j++ {
			off := j * 16
			sz := binary.LittleEndian.Uint32(block[off+8 : off+12])
			out = append(out, fragmentEntry{
				Start:      binary.LittleEndian.Uint64(block[off : off+8]),
				Size:       sz & 0x00ffffff, // documented size field occupies low 24 bits
				Compressed: sz&0x01000000 == 0,
			})
		}
		remaining -= take
	}
	return out, nil
}

// ExtractFile returns the full decompressed contents of the file at the
// given InodeRef. Returns an error if the inode is not a file.
func (f *FS) ExtractFile(ref uint64) ([]byte, error) {
	ic, err := f.readInode(ref)
	if err != nil {
		return nil, err
	}
	if ic.entry.Type != "file" {
		return nil, fmt.Errorf("inode %d is not a file (%s)", ic.entry.InodeNumber, ic.entry.Type)
	}
	out := make([]byte, 0, ic.entry.fileSize)
	cursor := int64(ic.entry.blocksStart)
	for i, bs := range ic.entry.blockSizes {
		size := int64(bs & 0x00ffffff)
		compressed := bs&0x01000000 == 0
		if size == 0 {
			// sparse hole: emit blockSize zeroes (or trailing partial)
			remaining := ic.entry.fileSize - int64(len(out))
			n := int64(f.sb.BlockSize)
			if remaining < n {
				n = remaining
			}
			out = append(out, make([]byte, n)...)
			continue
		}
		if size > dataBlockMax {
			return nil, fmt.Errorf("data block %d size %d exceeds cap", i, size)
		}
		body, err := f.d.Range(cursor, size)
		if err != nil {
			return nil, fmt.Errorf("data block %d: %w", i, err)
		}
		cursor += size
		if !compressed {
			out = append(out, body...)
			continue
		}
		dec, err := f.decompressBlock(body)
		if err != nil {
			return nil, fmt.Errorf("data block %d: %w", i, err)
		}
		out = append(out, dec...)
	}
	if ic.entry.fragIndex != 0xFFFFFFFF {
		frags, err := f.fragmentTable()
		if err != nil {
			return nil, err
		}
		if int(ic.entry.fragIndex) >= len(frags) {
			return nil, fmt.Errorf("fragment index %d out of range (%d entries)", ic.entry.fragIndex, len(frags))
		}
		fe := frags[ic.entry.fragIndex]
		fragBody, err := f.d.Range(int64(fe.Start), int64(fe.Size))
		if err != nil {
			return nil, fmt.Errorf("fragment data: %w", err)
		}
		var payload []byte
		if fe.Compressed {
			payload, err = f.decompressBlock(fragBody)
			if err != nil {
				return nil, err
			}
		} else {
			payload = fragBody
		}
		tailLen := int(ic.entry.fileSize) - len(out)
		if int(ic.entry.fragOffset)+tailLen > len(payload) {
			return nil, fmt.Errorf("fragment slice out of range: off=%d len=%d payload=%d",
				ic.entry.fragOffset, tailLen, len(payload))
		}
		out = append(out, payload[ic.entry.fragOffset:int(ic.entry.fragOffset)+tailLen]...)
	}
	if int64(len(out)) != ic.entry.fileSize {
		return nil, fmt.Errorf("extraction length mismatch: got %d, want %d", len(out), ic.entry.fileSize)
	}
	return out, nil
}

// decompressBlock is like decompress but bounded by dataBlockMax; used for fragment payloads.
func (f *FS) decompressBlock(buf []byte) ([]byte, error) {
	return f.decompressWithLimit(buf, dataBlockMax)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("invalid decompression limit")
	}
	lr := &io.LimitedReader{R: r, N: limit + 1}
	out, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > limit {
		return nil, fmt.Errorf("decompressed block exceeds cap %d", limit)
	}
	return out, nil
}
