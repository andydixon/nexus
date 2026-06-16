package binsnoop

import (
	"encoding/binary"
	"fmt"
)

// ---------------------------------------------------------------------------
// LZMA
// ---------------------------------------------------------------------------

const LZMADescription = "LZMA compressed data"

func LZMAMagic() [][]byte {
	// Common LZMA properties
	properties := []byte{0x5D, 0x6E, 0x6D, 0x6C}

	dictionarySizes := []uint32{
		0x10000000,
		0x20000000,
		0x01000000,
		0x02000000,
		0x04000000,
		0x00800000,
		0x00400000,
		0x00200000,
		0x00100000,
		0x00080000,
		0x00020000,
		0x00010000,
	}

	// Build magic signatures from property+dictionary_size combinations (little-endian)
	var sigs [][]byte
	for _, prop := range properties {
		for _, dict := range dictionarySizes {
			magic := make([]byte, 5)
			magic[0] = prop
			binary.LittleEndian.PutUint32(magic[1:], dict)
			sigs = append(sigs, magic)
		}
	}
	return sigs
}

func LZMAParser(data []byte, offset int) (*SignatureResult, error) {
	// Streamed data has a reported size of -1 (0xFFFFFFFFFFFFFFFF)
	const lzmaStreamSize = -1

	const minSupportedDecompressedSize = 256
	const maxSupportedDecompressedSize = 0xFFFFFFFF

	lzmaStructure := []StructField{
		{"properties", "u8"},
		{"dictionary_size", "u32"},
		{"decompressed_size", "u64"},
		{"null_byte", "u8"},
	}

	lzmaData := data[offset:]
	if len(lzmaData) < StructSize(lzmaStructure) {
		return nil, fmt.Errorf("LZMA: not enough data for header")
	}

	hdr, err := Parse(lzmaData, lzmaStructure, "little")
	if err != nil {
		return nil, fmt.Errorf("LZMA: header parse error: %w", err)
	}

	// Make sure the expected NULL byte is NULL
	if hdr["null_byte"] != 0 {
		return nil, fmt.Errorf("LZMA: null byte is not zero")
	}

	// Sanity check the reported decompressed size.
	// In Go int (signed 64-bit), 0xFFFFFFFFFFFFFFFF becomes -1.
	decompressedSize := hdr["decompressed_size"]
	validSize := decompressedSize == lzmaStreamSize ||
		(decompressedSize >= minSupportedDecompressedSize &&
			decompressedSize <= maxSupportedDecompressedSize)
	if !validSize {
		return nil, fmt.Errorf("LZMA: invalid decompressed size: %d", decompressedSize)
	}

	return &SignatureResult{
		Offset: offset,
		Description: fmt.Sprintf(
			"%s, properties: 0x%02X, dictionary size: %d bytes, uncompressed size: %d bytes",
			LZMADescription,
			hdr["properties"],
			hdr["dictionary_size"],
			decompressedSize,
		),
		Confidence: ConfidenceHigh,
	}, nil
}

// ---------------------------------------------------------------------------
// bzip2
// ---------------------------------------------------------------------------

const Bzip2Description = "bzip2 compressed data"

func Bzip2Magic() [][]byte {
	// Magic bytes include version, block size, and compressed magic signature
	return [][]byte{
		[]byte("BZh91AY&SY"),
		[]byte("BZh81AY&SY"),
		[]byte("BZh71AY&SY"),
		[]byte("BZh61AY&SY"),
		[]byte("BZh51AY&SY"),
		[]byte("BZh41AY&SY"),
		[]byte("BZh31AY&SY"),
		[]byte("BZh21AY&SY"),
		[]byte("BZh11AY&SY"),
	}
}

func Bzip2Parser(data []byte, offset int) (*SignatureResult, error) {
	// Validate we have enough data for at least the magic + some compressed data
	if offset+10 >= len(data) {
		return nil, fmt.Errorf("bzip2: not enough data")
	}

	return &SignatureResult{
		Offset:      offset,
		Description: Bzip2Description,
		Confidence:  ConfidenceHigh,
	}, nil
}

// ---------------------------------------------------------------------------
// XZ
// ---------------------------------------------------------------------------

const XZDescription = "XZ compressed data"

func XZMagic() [][]byte {
	return [][]byte{
		{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00},
	}
}

func XZParser(data []byte, offset int) (*SignatureResult, error) {
	const xzCRCStart = 6
	const xzCRCEnd = 8
	const xzHeaderSize = 12

	xzStructure := []StructField{
		{"magic_p1", "u32"},
		{"magic_p2", "u16"},
		{"flags", "u16"},
		{"header_crc", "u32"},
	}

	xzData := data[offset:]
	if len(xzData) < StructSize(xzStructure) {
		return nil, fmt.Errorf("XZ: not enough data for header")
	}

	hdr, err := Parse(xzData, xzStructure, "little")
	if err != nil {
		return nil, fmt.Errorf("XZ: header parse error: %w", err)
	}

	// Validate the header CRC over the flags bytes (bytes 6..8)
	if xzCRCEnd > len(xzData) {
		return nil, fmt.Errorf("XZ: not enough data for CRC")
	}

	crcData := xzData[xzCRCStart:xzCRCEnd]
	calculatedCRC := CRC32(crcData)
	if calculatedCRC != uint32(hdr["header_crc"]) {
		return nil, fmt.Errorf("XZ: header CRC mismatch: expected 0x%08X, got 0x%08X",
			hdr["header_crc"], calculatedCRC)
	}

	return &SignatureResult{
		Offset:      offset,
		Description: XZDescription,
		Confidence:  ConfidenceHigh,
		Size:        xzHeaderSize,
	}, nil
}

// ---------------------------------------------------------------------------
// ZSTD
// ---------------------------------------------------------------------------

const ZSTDDescription = "ZSTD compressed data"

func ZSTDMagic() [][]byte {
	return [][]byte{
		{0x28, 0xB5, 0x2F, 0xFD},
	}
}

func ZSTDParser(data []byte, offset int) (*SignatureResult, error) {
	const eofChecksumSize = 4
	const minBlockCount = 2

	availableData := len(data)

	// Parse the ZSTD frame header
	zstdHeader, err := parseZSTDHeader(data[offset:])
	if err != nil {
		return nil, fmt.Errorf("ZSTD: %w", err)
	}

	// Calculate first block header offset accounting for optional fields
	nextBlockStart := offset + zstdHeader.fixedHeaderSize

	// If single segment flag is not set, window descriptor byte is present
	if !zstdHeader.singleSegmentFlag {
		nextBlockStart++
	}

	// Dictionary ID field size
	if zstdHeader.dictionaryIDFlag == 1 {
		nextBlockStart++
	} else if zstdHeader.dictionaryIDFlag == 2 {
		nextBlockStart += 2
	} else if zstdHeader.dictionaryIDFlag == 3 {
		nextBlockStart += 4
	}

	// Frame content header field size
	if zstdHeader.frameContentFlag == 0 && zstdHeader.singleSegmentFlag {
		nextBlockStart++
	} else if zstdHeader.frameContentFlag == 1 {
		nextBlockStart += 2
	} else if zstdHeader.frameContentFlag == 2 {
		nextBlockStart += 4
	} else if zstdHeader.frameContentFlag == 3 {
		nextBlockStart += 8
	}

	var previousBlockStart *int
	blockCount := 0
	totalSize := 0

	// Walk through blocks to determine end of ZSTD data
	for IsOffsetSafe(availableData, nextBlockStart, previousBlockStart) {
		blockHeader, err := parseZSTDBlockHeader(data[nextBlockStart:])
		if err != nil {
			break
		}

		blockCount++
		prev := nextBlockStart
		previousBlockStart = &prev
		nextBlockStart += blockHeader.headerSize + blockHeader.blockSize

		if blockHeader.lastBlock {
			totalSize = nextBlockStart - offset
			if zstdHeader.contentChecksumPresent {
				totalSize += eofChecksumSize
			}
			if blockCount >= minBlockCount {
				return &SignatureResult{
					Offset:      offset,
					Description: fmt.Sprintf("%s, total size: %d bytes", ZSTDDescription, totalSize),
					Confidence:  ConfidenceHigh,
					Size:        totalSize,
				}, nil
			}
			break
		}
	}

	return nil, fmt.Errorf("ZSTD: failed to validate block structure")
}

type zstdHeaderInfo struct {
	fixedHeaderSize        int
	dictionaryIDFlag       int
	contentChecksumPresent bool
	singleSegmentFlag      bool
	frameContentFlag       int
}

func parseZSTDHeader(zstdData []byte) (*zstdHeaderInfo, error) {
	const frameUnusedBitsMask = 0b00011000
	const dictionaryIDMask = 0b11
	const contentChecksumMask = 0b100
	const singleSegmentMask = 0b100000
	const frameContentMask = 0b11000000
	const frameContentShift = 6

	structure := []StructField{
		{"magic", "u32"},
		{"frame_header_descriptor", "u8"},
	}

	hdr, err := Parse(zstdData, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("header parse error: %w", err)
	}

	// Unused bits should be zero
	if (hdr["frame_header_descriptor"] & frameUnusedBitsMask) != 0 {
		return nil, fmt.Errorf("unused bits are set in frame header descriptor")
	}

	return &zstdHeaderInfo{
		fixedHeaderSize:        StructSize(structure),
		dictionaryIDFlag:       hdr["frame_header_descriptor"] & dictionaryIDMask,
		contentChecksumPresent: (hdr["frame_header_descriptor"] & contentChecksumMask) != 0,
		singleSegmentFlag:      (hdr["frame_header_descriptor"] & singleSegmentMask) != 0,
		frameContentFlag:       (hdr["frame_header_descriptor"] & frameContentMask) >> frameContentShift,
	}, nil
}

type zstdBlockHeaderInfo struct {
	headerSize int
	blockType  int
	blockSize  int
	lastBlock  bool
}

func parseZSTDBlockHeader(blockData []byte) (*zstdBlockHeaderInfo, error) {
	const blockTypeMask = 0b110
	const blockTypeShift = 1
	const rleBlockType = 1
	const reservedBlockType = 3
	const lastBlockMask = 0b1
	const blockSizeMask = 0b1111_1111_1111_1111_1111_1000
	const blockSizeShift = 3

	structure := []StructField{{"info_bits", "u24"}}

	hdr, err := Parse(blockData, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("block header parse error: %w", err)
	}

	info := &zstdBlockHeaderInfo{
		headerSize: StructSize(structure),
		lastBlock:  (hdr["info_bits"] & lastBlockMask) != 0,
		blockType:  (hdr["info_bits"] & blockTypeMask) >> blockTypeShift,
		blockSize:  (hdr["info_bits"] & blockSizeMask) >> blockSizeShift,
	}

	// RLE block: single byte of raw data
	if info.blockType == rleBlockType {
		info.blockSize = 1
	}

	// Reserved block type is invalid
	if info.blockType == reservedBlockType {
		return nil, fmt.Errorf("reserved block type encountered")
	}

	return info, nil
}

// ---------------------------------------------------------------------------
// LZ4
// ---------------------------------------------------------------------------

const LZ4Description = "LZ4 compressed data"

func LZ4Magic() [][]byte {
	return [][]byte{
		{0x04, 0x22, 0x4D, 0x18},
	}
}

func LZ4Parser(data []byte, offset int) (*SignatureResult, error) {
	const contentChecksumLen = 4

	lz4Header, err := parseLZ4FileHeader(data[offset:])
	if err != nil {
		return nil, fmt.Errorf("LZ4: %w", err)
	}

	if offset+lz4Header.headerSize >= len(data) {
		return nil, fmt.Errorf("LZ4: not enough data after header")
	}

	lz4Data := data[offset+lz4Header.headerSize:]
	dataSize, err := getLZ4DataSize(lz4Data, lz4Header.blockChecksumPresent)
	if err != nil {
		return nil, fmt.Errorf("LZ4: %w", err)
	}

	totalSize := lz4Header.headerSize + dataSize
	if lz4Header.contentChecksumPresent {
		totalSize += contentChecksumLen
	}

	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, total size: %d bytes", LZ4Description, totalSize),
		Confidence:  ConfidenceMedium,
		Size:        totalSize,
	}, nil
}

type lz4FileHeaderInfo struct {
	headerSize             int
	blockChecksumPresent   bool
	contentChecksumPresent bool
}

func parseLZ4FileHeader(lz4Data []byte) (*lz4FileHeaderInfo, error) {
	const magicSize = 4
	const lz4StructSize = 6

	const bdReservedMask = 0b10001111
	const flagsReservedMask = 0b00000010

	const flagDictionaryPresent = 0b00000001
	const flagContentSizePresent = 0b00001000
	const flagBlockChecksumPresent = 0b00010000
	const flagContentChecksumPresent = 0b00000100

	const dictionaryLen = 4
	const contentSizeLen = 8

	structure := []StructField{
		{"magic", "u32"},
		{"flags", "u8"},
		{"bd", "u8"},
	}

	hdr, err := Parse(lz4Data, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("file header parse error: %w", err)
	}

	// Reserved bits must not be set
	if (hdr["flags"] & flagsReservedMask) != 0 {
		return nil, fmt.Errorf("flags reserved bits are set")
	}
	if (hdr["bd"] & bdReservedMask) != 0 {
		return nil, fmt.Errorf("bd reserved bits are set")
	}

	// Calculate CRC data boundaries (CRC covers descriptor excluding magic)
	crcDataStart := magicSize
	crcDataEnd := crcDataStart + (lz4StructSize - magicSize)

	if (hdr["flags"] & flagContentSizePresent) != 0 {
		crcDataEnd += contentSizeLen
	}
	if (hdr["flags"] & flagDictionaryPresent) != 0 {
		crcDataEnd += dictionaryLen
	}

	if crcDataEnd >= len(lz4Data) {
		return nil, fmt.Errorf("not enough data for header CRC")
	}

	crcData := lz4Data[crcDataStart:crcDataEnd]
	actualCRC := lz4Data[crcDataEnd]

	// LZ4 header CRC is the second byte of xxh32 hash over the descriptor
	calculatedCRC := byte((xxh32(crcData, 0) >> 8) & 0xFF)
	if actualCRC != calculatedCRC {
		return nil, fmt.Errorf("header CRC mismatch: expected 0x%02X, got 0x%02X",
			actualCRC, calculatedCRC)
	}

	return &lz4FileHeaderInfo{
		headerSize:             crcDataEnd + 1,
		blockChecksumPresent:   (hdr["flags"] & flagBlockChecksumPresent) != 0,
		contentChecksumPresent: (hdr["flags"] & flagContentChecksumPresent) != 0,
	}, nil
}

func getLZ4DataSize(lz4Data []byte, checksumPresent bool) (int, error) {
	dataSize := 0
	var lastOffset *int
	available := len(lz4Data)

	for IsOffsetSafe(available, dataSize, lastOffset) {
		if dataSize >= len(lz4Data) {
			break
		}

		blockHeader, err := parseLZ4BlockHeader(lz4Data[dataSize:], checksumPresent)
		if err != nil {
			break
		}

		prev := dataSize
		lastOffset = &prev
		dataSize += blockHeader.headerSize + blockHeader.dataSize + blockHeader.checksumSize

		if blockHeader.lastBlock {
			return dataSize, nil
		}
	}

	return 0, fmt.Errorf("failed to find LZ4 end marker")
}

type lz4BlockHeaderInfo struct {
	dataSize     int
	headerSize   int
	checksumSize int
	lastBlock    bool
}

func parseLZ4BlockHeader(blockData []byte, checksumPresent bool) (*lz4BlockHeaderInfo, error) {
	const sizeMask uint32 = 0x7FFFFFFF
	const endMarker = 0
	const checksumSize = 4
	const blockStructSize = 4

	structure := []StructField{{"block_size", "u32"}}

	hdr, err := Parse(blockData, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("block header parse error: %w", err)
	}

	info := &lz4BlockHeaderInfo{
		headerSize: blockStructSize,
		lastBlock:  hdr["block_size"] == endMarker,
	}

	if checksumPresent {
		info.checksumSize = checksumSize
	}

	// High bit is not part of the actual block size
	info.dataSize = int(uint32(hdr["block_size"]) & sizeMask)

	return info, nil
}

// xxh32 implements the XXHash32 algorithm used by LZ4 for header checksums.
func xxh32(input []byte, seed uint32) uint32 {
	const (
		prime1 uint32 = 0x9E3779B1
		prime2 uint32 = 0x85EBCA77
		prime3 uint32 = 0xC2B2AE3D
		prime4 uint32 = 0x27D4EB2F
		prime5 uint32 = 0x165667B1
	)

	n := len(input)
	p := 0
	var h32 uint32

	if n >= 16 {
		v1 := seed + prime1 + prime2
		v2 := seed + prime2
		v3 := seed
		v4 := seed - prime1
		for p+16 <= n {
			v1 = xxh32Round(v1, binary.LittleEndian.Uint32(input[p:]), prime1, prime2)
			v2 = xxh32Round(v2, binary.LittleEndian.Uint32(input[p+4:]), prime1, prime2)
			v3 = xxh32Round(v3, binary.LittleEndian.Uint32(input[p+8:]), prime1, prime2)
			v4 = xxh32Round(v4, binary.LittleEndian.Uint32(input[p+12:]), prime1, prime2)
			p += 16
		}
		h32 = ((v1 << 1) | (v1 >> 31)) +
			((v2 << 7) | (v2 >> 25)) +
			((v3 << 12) | (v3 >> 20)) +
			((v4 << 18) | (v4 >> 14))
	} else {
		h32 = seed + prime5
	}

	h32 += uint32(n)

	for p+4 <= n {
		h32 += binary.LittleEndian.Uint32(input[p:]) * prime3
		h32 = ((h32 << 17) | (h32 >> 15)) * prime4
		p += 4
	}

	for p < n {
		h32 += uint32(input[p]) * prime5
		h32 = ((h32 << 11) | (h32 >> 21)) * prime1
		p++
	}

	h32 ^= h32 >> 15
	h32 *= prime2
	h32 ^= h32 >> 13
	h32 *= prime3
	h32 ^= h32 >> 16

	return h32
}

func xxh32Round(acc, input, prime1, prime2 uint32) uint32 {
	acc += input * prime2
	acc = (acc << 13) | (acc >> 19)
	acc *= prime1
	return acc
}

// ---------------------------------------------------------------------------
// LZOP
// ---------------------------------------------------------------------------

const LZOPDescription = "LZO compressed data"

func LZOPMagic() [][]byte {
	return [][]byte{
		{0x89, 0x4C, 0x5A, 0x4F, 0x00, 0x0D, 0x0A, 0x1A, 0x0A},
	}
}

func LZOPParser(data []byte, offset int) (*SignatureResult, error) {
	lzopHeader, err := parseLZOPFileHeader(data[offset:])
	if err != nil {
		return nil, fmt.Errorf("LZOP: %w", err)
	}

	if offset+lzopHeader.headerSize >= len(data) {
		return nil, fmt.Errorf("LZOP: not enough data after header")
	}

	lzopData := data[offset+lzopHeader.headerSize:]
	dataSize, err := getLZODataSize(lzopData, lzopHeader.blockChecksumPresent)
	if err != nil {
		return nil, fmt.Errorf("LZOP: %w", err)
	}

	totalSize := lzopHeader.headerSize + dataSize
	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, total size: %d bytes", LZOPDescription, totalSize),
		Confidence:  ConfidenceHigh,
		Size:        totalSize,
	}, nil
}

type lzopFileHeaderInfo struct {
	headerSize           int
	blockChecksumPresent bool
}

func parseLZOPFileHeader(lzopData []byte) (*lzopFileHeaderInfo, error) {
	const lzoMaxVersion = 0x1040
	const lzoHeaderSizeP1 = 21
	const lzoHeaderSizeP2 = 13
	const filterSize = 4
	const lzoChecksumSize = 4

	const flagFilter = 0x0000_0800
	const flagCRC32C = 0x0000_0200
	const flagAdler32C = 0x0000_0002

	structureP1 := []StructField{
		{"magic_p1", "u8"},
		{"magic_p2", "u64"},
		{"version", "u16"},
		{"lib_version", "u16"},
		{"version_needed", "u16"},
		{"method", "u8"},
		{"level", "u8"},
		{"flags", "u32"},
	}

	structureP2 := []StructField{
		{"mode", "u32"},
		{"mtime", "u32"},
		{"gmt_diff", "u32"},
		{"file_name_length", "u8"},
	}

	allowedMethods := []int{1, 2, 3}

	hdrP1, err := Parse(lzopData, structureP1, "big")
	if err != nil {
		return nil, fmt.Errorf("header part 1 parse error: %w", err)
	}

	// Sanity check methods
	methodValid := false
	for _, m := range allowedMethods {
		if hdrP1["method"] == m {
			methodValid = true
			break
		}
	}
	if !methodValid {
		return nil, fmt.Errorf("invalid method: %d", hdrP1["method"])
	}

	// Sanity check version numbers
	if hdrP1["version"] > lzoMaxVersion {
		return nil, fmt.Errorf("version too high: 0x%04X", hdrP1["version"])
	}
	if hdrP1["version"] < hdrP1["version_needed"] {
		return nil, fmt.Errorf("version %d < version_needed %d", hdrP1["version"], hdrP1["version_needed"])
	}

	headerP2Start := lzoHeaderSizeP1
	if (hdrP1["flags"] & flagFilter) != 0 {
		headerP2Start += filterSize
	}

	headerP2End := headerP2Start + lzoHeaderSizeP2
	if headerP2End > len(lzopData) {
		return nil, fmt.Errorf("not enough data for header part 2")
	}

	hdrP2, err := Parse(lzopData[headerP2Start:headerP2End], structureP2, "big")
	if err != nil {
		return nil, fmt.Errorf("header part 2 parse error: %w", err)
	}

	headerSize := headerP2End + hdrP2["file_name_length"] + lzoChecksumSize

	// Faithfully ported: in the Rust source this is & (AND) which always yields false
	blockChecksumPresent := (hdrP1["flags"] & flagAdler32C & flagCRC32C) != 0

	if headerSize > len(lzopData) {
		return nil, fmt.Errorf("calculated header size exceeds available data")
	}

	return &lzopFileHeaderInfo{
		headerSize:           headerSize,
		blockChecksumPresent: blockChecksumPresent,
	}, nil
}

type lzopBlockHeaderInfo struct {
	headerSize       int
	compressedSize   int
	uncompressedSize int
	checksumSize     int
}

func parseLZOPBlockHeader(lzoData []byte, compressedChecksumPresent bool) (*lzopBlockHeaderInfo, error) {
	const blockHeaderSize = 12
	const maxUncompressedBlockSize = 64 * 1024 * 1024
	const lzoChecksumSize = 4

	structure := []StructField{
		{"uncompressed_size", "u32"},
		{"compressed_size", "u32"},
		{"uncompressed_checksum", "u32"},
	}

	hdr, err := Parse(lzoData, structure, "big")
	if err != nil {
		return nil, fmt.Errorf("block header parse error: %w", err)
	}

	if hdr["compressed_size"] == 0 ||
		hdr["uncompressed_size"] == 0 ||
		hdr["uncompressed_checksum"] == 0 ||
		hdr["uncompressed_size"] > maxUncompressedBlockSize {
		return nil, fmt.Errorf("invalid block header values")
	}

	info := &lzopBlockHeaderInfo{
		headerSize:       blockHeaderSize,
		compressedSize:   hdr["compressed_size"],
		uncompressedSize: hdr["uncompressed_size"],
	}

	if compressedChecksumPresent {
		info.checksumSize = lzoChecksumSize
	}

	return info, nil
}

func parseLZOPEOFMarker(eofData []byte) (int, error) {
	const eofMarkerValue = 0
	const eofMarkerSize = 4

	structure := []StructField{{"marker", "u32"}}

	hdr, err := Parse(eofData, structure, "big")
	if err != nil {
		return 0, fmt.Errorf("EOF marker parse error: %w", err)
	}

	if hdr["marker"] != eofMarkerValue {
		return 0, fmt.Errorf("invalid EOF marker: %d", hdr["marker"])
	}

	return eofMarkerSize, nil
}

func getLZODataSize(lzoData []byte, compressedChecksumPresent bool) (int, error) {
	const minBlockCount = 2

	available := len(lzoData)
	var lastOffset *int
	dataSize := 0
	blockCount := 0

	for IsOffsetSafe(available, dataSize, lastOffset) {
		blockHeader, err := parseLZOPBlockHeader(lzoData[dataSize:], compressedChecksumPresent)
		if err != nil {
			break
		}

		blockCount++
		prev := dataSize
		lastOffset = &prev
		dataSize += blockHeader.headerSize + blockHeader.compressedSize + blockHeader.checksumSize
	}

	if blockCount >= minBlockCount {
		if dataSize < len(lzoData) {
			eofSize, err := parseLZOPEOFMarker(lzoData[dataSize:])
			if err != nil {
				return 0, fmt.Errorf("EOF marker error: %w", err)
			}
			dataSize += eofSize
			return dataSize, nil
		}
	}

	return 0, fmt.Errorf("failed to validate LZO block structure")
}

// ---------------------------------------------------------------------------
// LZFSE
// ---------------------------------------------------------------------------

const LZFSEDescription = "LZFSE compressed data"

func LZFSEMagic() [][]byte {
	return [][]byte{
		[]byte("bvx-"),
		[]byte("bvx1"),
		[]byte("bvx2"),
		[]byte("bvxn"),
	}
}

func LZFSEParser(data []byte, offset int) (*SignatureResult, error) {
	available := len(data)
	nextBlockOffset := offset
	var previousBlockOffset *int

	for IsOffsetSafe(available, nextBlockOffset, previousBlockOffset) {
		prev := nextBlockOffset
		previousBlockOffset = &prev

		block, err := parseLZFSEBlockHeader(data[nextBlockOffset:])
		if err != nil {
			break
		}

		nextBlockOffset += block.headerSize + block.dataSize

		if block.eof {
			totalSize := nextBlockOffset - offset
			return &SignatureResult{
				Offset:      offset,
				Description: fmt.Sprintf("%s, total size: %d bytes", LZFSEDescription, totalSize),
				Confidence:  ConfidenceHigh,
				Size:        totalSize,
			}, nil
		}
	}

	return nil, fmt.Errorf("LZFSE: failed to find end-of-stream block")
}

type lzfseBlockInfo struct {
	eof        bool
	dataSize   int
	headerSize int
}

func parseLZFSEBlockHeader(lzfseData []byte) (*lzfseBlockInfo, error) {
	// LZFSE block types (little-endian u32 representations)
	const endOfStream = 0x24787662    // "bvx$"
	const uncompressed = 0x2D787662   // "bvx-"
	const compressedV1 = 0x31787662   // "bvx1"
	const compressedV2 = 0x32787662   // "bvx2"
	const compressedLZVN = 0x6E787662 // "bvxn"

	blockTypeStructure := []StructField{{"block_type", "u32"}}

	hdr, err := Parse(lzfseData, blockTypeStructure, "little")
	if err != nil {
		return nil, fmt.Errorf("block header parse error: %w", err)
	}

	blockType := hdr["block_type"]

	switch blockType {
	case endOfStream:
		return &lzfseBlockInfo{eof: true, dataSize: 0, headerSize: 4}, nil

	case uncompressed:
		return parseLZFSEUncompressedBlock(lzfseData)

	case compressedV1:
		return parseLZFSECompressedV1Block(lzfseData)

	case compressedV2:
		return parseLZFSECompressedV2Block(lzfseData)

	case compressedLZVN:
		return parseLZFSECompressedLZVNBlock(lzfseData)
	}

	return nil, fmt.Errorf("unknown LZFSE block type: 0x%08X", blockType)
}

func parseLZFSEUncompressedBlock(lzfseData []byte) (*lzfseBlockInfo, error) {
	const headerSize = 8

	structure := []StructField{
		{"magic", "u32"},
		{"n_raw_bytes", "u32"},
	}

	hdr, err := Parse(lzfseData, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("uncompressed block parse error: %w", err)
	}

	return &lzfseBlockInfo{
		eof:        false,
		dataSize:   hdr["n_raw_bytes"],
		headerSize: headerSize,
	}, nil
}

func parseLZFSECompressedV1Block(lzfseData []byte) (*lzfseBlockInfo, error) {
	const headerSize = 770

	structure := []StructField{
		{"magic", "u32"},
		{"n_raw_bytes", "u32"},
		{"n_payload_bytes", "u32"},
		{"n_literals", "u32"},
		{"n_matches", "u32"},
		{"n_literal_payload_bytes", "u32"},
		{"n_lmd_payload_bytes", "u32"},
		{"literal_bits", "u32"},
		{"literal_state", "u64"},
		{"lmd_bits", "u32"},
		{"l_state", "u16"},
		{"m_state", "u16"},
		{"d_state", "u16"},
	}

	hdr, err := Parse(lzfseData, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("compressed v1 block parse error: %w", err)
	}

	return &lzfseBlockInfo{
		eof:        false,
		dataSize:   hdr["n_literal_payload_bytes"] + hdr["n_lmd_payload_bytes"],
		headerSize: headerSize,
	}, nil
}

func parseLZFSECompressedV2Block(lzfseData []byte) (*lzfseBlockInfo, error) {
	const nPayloadShift = 20
	const lmdPayloadShift = 40
	const payloadMask = 0b11111_11111_11111_11111

	structure := []StructField{
		{"magic", "u32"},
		{"uncompressed_size", "u32"},
		{"packed_field_1", "u64"},
		{"packed_field_2", "u64"},
		{"header_size", "u32"},
		{"state_fields", "u32"},
	}

	hdr, err := Parse(lzfseData, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("compressed v2 block parse error: %w", err)
	}

	nLMDPayloadBytes := (hdr["packed_field_2"] >> lmdPayloadShift) & payloadMask
	nLiteralPayloadBytes := (hdr["packed_field_1"] >> nPayloadShift) & payloadMask

	return &lzfseBlockInfo{
		eof:        false,
		dataSize:   nLMDPayloadBytes + nLiteralPayloadBytes,
		headerSize: hdr["header_size"],
	}, nil
}

func parseLZFSECompressedLZVNBlock(lzfseData []byte) (*lzfseBlockInfo, error) {
	const headerSize = 12

	structure := []StructField{
		{"magic", "u32"},
		{"n_raw_bytes", "u32"},
		{"n_payload_bytes", "u32"},
	}

	hdr, err := Parse(lzfseData, structure, "little")
	if err != nil {
		return nil, fmt.Errorf("compressed LZVN block parse error: %w", err)
	}

	return &lzfseBlockInfo{
		eof:        false,
		dataSize:   hdr["n_payload_bytes"],
		headerSize: headerSize,
	}, nil
}

// ---------------------------------------------------------------------------
// Zlib
// ---------------------------------------------------------------------------

const ZlibDescription = "Zlib compressed file"

func ZlibMagic() [][]byte {
	return [][]byte{
		{0x78, 0x9C},
		{0x78, 0xDA},
		{0x78, 0x5E},
	}
}

func ZlibParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+2 > len(data) {
		return nil, fmt.Errorf("zlib: not enough data")
	}

	// Zlib header: CMF (byte 0) and FLG (byte 1)
	// The two-byte value (CMF*256 + FLG) must be a multiple of 31 (FCHECK)
	cmf := int(data[offset])
	flg := int(data[offset+1])
	if (cmf*256+flg)%31 != 0 {
		return nil, fmt.Errorf("zlib: FCHECK validation failed")
	}

	// CM (compression method) should be 8 (deflate)
	cm := cmf & 0x0F
	if cm != 8 {
		return nil, fmt.Errorf("zlib: invalid compression method: %d", cm)
	}

	// CINFO should be <= 7
	cinfo := (cmf >> 4) & 0x0F
	if cinfo > 7 {
		return nil, fmt.Errorf("zlib: invalid CINFO: %d", cinfo)
	}

	return &SignatureResult{
		Offset:      offset,
		Description: ZlibDescription,
		Confidence:  ConfidenceHigh,
	}, nil
}

// ---------------------------------------------------------------------------
// compress'd
// ---------------------------------------------------------------------------

const CompressdDescription = "compress'd data"

func CompressdMagic() [][]byte {
	return [][]byte{
		{0x1F, 0x9D, 0x90},
	}
}

func CompressdParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Offset:      offset,
		Description: CompressdDescription,
		Confidence:  ConfidenceLow,
	}

	// Higher confidence when found at the beginning of a file
	if offset == 0 {
		result.Confidence = ConfidenceMedium
	}

	return result, nil
}
