package binsnoop

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// ---------------------------------------------------------------------------
// SquashFS
// ---------------------------------------------------------------------------

const SquashFSDescription = "SquashFS file system"

func squashfs_magic() [][]byte {
	return [][]byte{
		[]byte("sqsh"),
		[]byte("hsqs"),
		[]byte("sqlz"),
		[]byte("qshs"),
		[]byte("tqsh"),
		[]byte("hsqt"),
		[]byte("shsq"),
	}
}

func squashfs_parser(data []byte, offset int) (*SignatureResult, error) {
	const squashfsV4 = 4
	const maxSquashfsVersion uint16 = 4
	const minSquashfsHeaderSize = 120
	const versionStart = 28
	const versionEnd = 30

	compressionTypes := map[int]string{
		0: "unknown", 1: "gzip", 2: "lzma", 3: "lzo", 4: "xz", 5: "lz4", 6: "zstd",
	}

	available := len(data) - offset
	sqshData := data[offset:]

	if len(sqshData) <= minSquashfsHeaderSize {
		return nil, fmt.Errorf("not enough data for squashfs header")
	}

	// Determine endianness from version field at fixed offset
	endianness := "little"
	squashfsVersion := int(binary.LittleEndian.Uint16(sqshData[versionStart:versionEnd]))
	if squashfsVersion == 0 || squashfsVersion > int(maxSquashfsVersion) {
		endianness = "big"
		squashfsVersion = int(binary.BigEndian.Uint16(sqshData[versionStart:versionEnd]))
	}
	if squashfsVersion <= 0 || squashfsVersion > int(maxSquashfsVersion) {
		return nil, fmt.Errorf("invalid squashfs version")
	}

	// Pick the right structure
	var fields []StructField
	if squashfsVersion == squashfsV4 {
		fields = []StructField{
			{"magic", "u32"}, {"inode_count", "u32"}, {"modification_time", "u32"},
			{"block_size", "u32"}, {"fragment_count", "u32"}, {"compression_id", "u16"},
			{"block_log", "u16"}, {"flags", "u16"}, {"id_count", "u16"},
			{"major_version", "u16"}, {"minor_version", "u16"},
			{"root_inode_ref", "u64"}, {"image_size", "u64"}, {"uid_start", "u64"},
		}
	} else {
		fields = []StructField{
			{"magic", "u32"}, {"inode_count", "u32"},
			{"bytes_used_2", "u32"}, {"uid_start_2", "u32"}, {"guid_start_2", "u32"},
			{"inode_table_start_2", "u32"}, {"directory_table_start_2", "u32"},
			{"major_version", "u16"}, {"minor_version", "u16"},
			{"block_size_1", "u16"}, {"block_log", "u16"},
			{"flags", "u8"}, {"uid_count", "u8"}, {"guid_count", "u8"},
			{"modification_time", "u32"},
			{"root_inode_ref", "u64"}, {"block_size", "u32"},
			{"fragment_entry_count", "u32"}, {"fragment_table_start_2", "u32"},
			{"image_size", "u64"}, {"uid_start", "u64"}, {"guid_start", "u64"},
			{"inode_table_start", "u64"}, {"directory_table_start", "u64"},
			{"fragment_table_start", "u64"}, {"lookup_table_start", "u64"},
		}
	}

	hdr, err := Parse(sqshData, fields, endianness)
	if err != nil {
		return nil, fmt.Errorf("squashfs header parse failed")
	}

	// For v1/v2 images, adjust fields
	if squashfsVersion < 3 {
		hdr["uid_start"] = hdr["uid_start_2"]
		hdr["image_size"] = hdr["bytes_used_2"]
	}

	headerSize := StructSize(fields)
	imageSize := hdr["image_size"]

	if imageSize <= minSquashfsHeaderSize {
		return nil, fmt.Errorf("squashfs image size too small")
	}
	if imageSize > available {
		return nil, fmt.Errorf("squashfs image size exceeds available data")
	}

	// Validate block_size vs block_log
	blockSize := hdr["block_size"]
	blockLog := hdr["block_log"]
	if blockSize <= 0 || blockLog != int(math.Log2(float64(blockSize))) {
		return nil, fmt.Errorf("squashfs block_size/block_log mismatch")
	}

	// Validate UID table
	uidTableStart := offset + hdr["uid_start"]
	if uidTableStart <= headerSize {
		return nil, fmt.Errorf("squashfs uid table offset invalid")
	}
	if uidTableStart >= len(data) {
		return nil, fmt.Errorf("squashfs uid table out of bounds")
	}

	// Parse one UID entry
	uidData := data[uidTableStart:]
	var uidFields []StructField
	if squashfsVersion == squashfsV4 {
		uidFields = []StructField{{"uid_block_ptr", "u64"}}
	} else {
		uidFields = []StructField{{"uid_block_ptr", "u32"}}
	}
	uidEntry, err := Parse(uidData, uidFields, endianness)
	if err != nil {
		return nil, fmt.Errorf("squashfs uid entry parse failed")
	}
	uidPtr := uidEntry["uid_block_ptr"]
	if uidPtr != 0 && (uidPtr <= headerSize || uidPtr > imageSize) {
		return nil, fmt.Errorf("squashfs uid table pointer invalid")
	}

	compressionID := hdr["compression_id"]
	if squashfsVersion < 4 {
		// v3 headers don't have compression_id; use 0 (unknown)
		compressionID = 0
	}
	compStr, ok := compressionTypes[compressionID]
	if !ok {
		return nil, fmt.Errorf("squashfs unknown compression type")
	}

	createDate := EpochToString(uint32(hdr["modification_time"]))

	return &SignatureResult{
		Offset:     offset,
		Size:       imageSize,
		Confidence: ConfidenceHigh,
		Description: fmt.Sprintf(
			"%s, %s endian, version: %d.%d, compression: %s, inode count: %d, block size: %d, image size: %d bytes, created: %s",
			SquashFSDescription, endianness,
			hdr["major_version"], hdr["minor_version"],
			compStr, hdr["inode_count"], blockSize, imageSize, createDate,
		),
	}, nil
}

// ---------------------------------------------------------------------------
// CramFS
// ---------------------------------------------------------------------------

const CramFSDescription = "CramFS filesystem"

func cramfs_magic() [][]byte {
	return [][]byte{
		[]byte("Compressed ROMFS"),
	}
}

func cramfs_parser(data []byte, offset int) (*SignatureResult, error) {
	const signatureOffset = 16
	const crcStartOffset = 32
	const crcEndOffset = 36
	const bigEndianMagic = 0x453DCD28
	const littleEndianMagic = 0x28CD3D45

	realOffset := offset - signatureOffset
	if realOffset < 0 || realOffset >= len(data) {
		return nil, fmt.Errorf("cramfs offset out of bounds")
	}

	cramfsData := data[realOffset:]

	fields := []StructField{
		{"magic", "u32"}, {"size", "u32"}, {"flags", "u32"}, {"future", "u32"},
		{"signature_p1", "u64"}, {"signature_p2", "u64"},
		{"checksum", "u32"}, {"edition", "u32"}, {"block_count", "u32"}, {"file_count", "u32"},
	}

	endianness := "little"
	hdr, err := Parse(cramfsData, fields, endianness)
	if err != nil {
		return nil, fmt.Errorf("cramfs parse failed")
	}

	magic := hdr["magic"]
	if magic != bigEndianMagic && magic != littleEndianMagic {
		return nil, fmt.Errorf("cramfs invalid magic")
	}
	if magic == bigEndianMagic {
		endianness = "big"
		hdr, err = Parse(cramfsData, fields, endianness)
		if err != nil {
			return nil, fmt.Errorf("cramfs big endian parse failed")
		}
	}

	structSize := StructSize(fields)
	if hdr["size"] <= structSize {
		return nil, fmt.Errorf("cramfs reported size too small")
	}

	imageSize := hdr["size"]
	checksum := uint32(hdr["checksum"])
	fileCount := hdr["file_count"]

	if realOffset+imageSize > len(data) {
		return nil, fmt.Errorf("cramfs image exceeds available data")
	}

	// Copy image data and null out checksum field for CRC validation
	cramfsImage := make([]byte, imageSize)
	copy(cramfsImage, cramfsData[:imageSize])
	for i := crcStartOffset; i < crcEndOffset && i < imageSize; i++ {
		cramfsImage[i] = 0
	}

	errorMsg := ""
	confidence := ConfidenceHigh
	if CRC32(cramfsImage) != checksum {
		errorMsg = " (checksum error)"
		confidence = ConfidenceMedium
	}

	return &SignatureResult{
		Offset:     realOffset,
		Size:       imageSize,
		Confidence: confidence,
		Description: fmt.Sprintf("%s, %s endian, %d files, total size: %d bytes%s",
			CramFSDescription, endianness, fileCount, imageSize, errorMsg),
	}, nil
}

// ---------------------------------------------------------------------------
// JFFS2
// ---------------------------------------------------------------------------

const JFFS2Description = "JFFS2 filesystem"

func jffs2_magic() [][]byte {
	return [][]byte{
		{0x19, 0x85, 0xE0, 0x01},
		{0x19, 0x85, 0xE0, 0x02},
		{0x19, 0x85, 0x20, 0x03},
		{0x85, 0x19, 0x01, 0xE0},
		{0x85, 0x19, 0x02, 0xE0},
		{0x85, 0x19, 0x03, 0x20},
	}
}

func jffs2_parser(data []byte, offset int) (*SignatureResult, error) {
	const maxPageSize = 0x20000
	const minValidNodeCount = 2
	const jffs2CorrectMagic = 0x1985
	const headerCRCSize = 8

	jffs2NodeFields := []StructField{
		{"magic", "u16"}, {"type", "u16"}, {"size", "u32"}, {"crc", "u32"},
	}
	jffs2NodeStructSize := StructSize(jffs2NodeFields) // 12

	// Parse first node
	firstNode, endianness, err := parseJFFS2NodeHeader(data[offset:], jffs2NodeFields, headerCRCSize, jffs2CorrectMagic)
	if err != nil {
		return nil, err
	}

	jffs2EOF := offset + jffs2Roundup(firstNode["size"])
	if jffs2EOF >= len(data) {
		return nil, fmt.Errorf("jffs2 first node exceeds data")
	}

	var nodeMagic []byte
	if endianness == "big" {
		nodeMagic = []byte{0x19, 0x85}
	} else {
		nodeMagic = []byte{0x85, 0x19}
	}

	nodeCount := 1

	// Scan for subsequent nodes
	searchData := data[jffs2EOF:]
	for i := 0; i < len(searchData); {
		idx := bytes.Index(searchData[i:], nodeMagic)
		if idx < 0 {
			break
		}
		absPos := jffs2EOF + i + idx
		if absPos < jffs2EOF {
			i += idx + 1
			continue
		}
		if absPos-jffs2EOF > maxPageSize {
			break
		}
		headerEnd := absPos + jffs2NodeStructSize
		if headerEnd > len(data) {
			break
		}
		nodeHdr, _, err := parseJFFS2NodeHeader(data[absPos:headerEnd], jffs2NodeFields, headerCRCSize, jffs2CorrectMagic)
		if err == nil {
			nodeCount++
			jffs2EOF = absPos + jffs2Roundup(nodeHdr["size"])
		}
		i += idx + 1
	}

	if nodeCount <= minValidNodeCount {
		return nil, fmt.Errorf("jffs2 too few valid nodes: %d", nodeCount)
	}

	totalSize := jffs2EOF - offset
	return &SignatureResult{
		Offset:     offset,
		Size:       totalSize,
		Confidence: ConfidenceHigh,
		Description: fmt.Sprintf("%s, %s endian, nodes: %d, total size: %d bytes",
			JFFS2Description, endianness, nodeCount, totalSize),
	}, nil
}

func parseJFFS2NodeHeader(nodeData []byte, fields []StructField, headerCRCSize, correctMagic int) (map[string]int, string, error) {
	endianness := "little"
	hdr, err := Parse(nodeData, fields, endianness)
	if err != nil {
		return nil, "", fmt.Errorf("jffs2 node parse failed")
	}
	if hdr["magic"] != correctMagic {
		endianness = "big"
		hdr, err = Parse(nodeData, fields, endianness)
		if err != nil {
			return nil, "", fmt.Errorf("jffs2 node big endian parse failed")
		}
	}
	if hdr["magic"] != correctMagic {
		return nil, "", fmt.Errorf("jffs2 node invalid magic")
	}

	// Validate header CRC
	if headerCRCSize > len(nodeData) {
		return nil, "", fmt.Errorf("jffs2 not enough data for crc")
	}
	calcCRC := int(jffs2NodeCRC(nodeData[:headerCRCSize]))
	if calcCRC != hdr["crc"] {
		return nil, "", fmt.Errorf("jffs2 node header crc mismatch")
	}

	return hdr, endianness, nil
}

func jffs2NodeCRC(data []byte) uint32 {
	return CRC32(data) // CRC32 uses IEEE polynomial; JFFS2: crc32(0xFFFFFFFF, data) ^ 0xFFFFFFFF which is standard CRC32
}

func jffs2Roundup(num int) int {
	base := 4
	return ((num + base - 1) / base) * base
}

// ---------------------------------------------------------------------------
// YAFFS
// ---------------------------------------------------------------------------

const YAFFSDescription = "YAFFSv2 filesystem"

func yaffs_magic() [][]byte {
	return [][]byte{
		{0x03, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0xFF, 0xFF},
		{0x00, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01, 0xFF, 0xFF},
		{0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0xFF, 0xFF},
		{0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0xFF, 0xFF},
	}
}

func yaffs_parser(data []byte, offset int) (*SignatureResult, error) {
	const maxObjSize = 16896
	const minNumberOfObjs = 2
	const bigEndianFirstByte byte = 0
	const fileType = 1

	available := len(data)
	requiredMinOffset := offset + (maxObjSize * minNumberOfObjs)
	if !IsOffsetSafe(available, requiredMinOffset, nil) {
		return nil, fmt.Errorf("yaffs not enough data")
	}

	endianness := "little"
	if data[offset] == bigEndianFirstByte {
		endianness = "big"
	}

	yaffsData := data[offset:]

	// Determine page size
	pageSize, err := yaffsGetPageSize(yaffsData)
	if err != nil {
		return nil, err
	}

	// Determine spare size
	spareSize, err := yaffsGetSpareSize(yaffsData, pageSize, endianness)
	if err != nil {
		return nil, err
	}

	// Get total image size
	imageSize, err := yaffsGetImageSize(yaffsData, pageSize, spareSize, endianness)
	if err != nil {
		return nil, err
	}

	return &SignatureResult{
		Offset:     offset,
		Size:       imageSize,
		Confidence: ConfidenceMedium,
		Description: fmt.Sprintf("%s, %s endian, page size: %d, spare size: %d, image size: %d bytes",
			YAFFSDescription, endianness, pageSize, spareSize, imageSize),
	}, nil
}

func yaffsGetPageSize(fileData []byte) (int, error) {
	spareMagics := [][]byte{
		{0x00, 0x00, 0x10, 0x00},
		{0x00, 0x10, 0x00, 0x00},
		{0xFF, 0xFF, 0x00, 0x00, 0x10, 0x00},
		{0xFF, 0xFF, 0x00, 0x10, 0x00, 0x00},
	}
	pageSizes := []int{512, 1024, 2048, 4096, 8192, 16384}

	for _, ps := range pageSizes {
		for _, sm := range spareMagics {
			start := ps
			end := start + len(sm)
			if end <= len(fileData) {
				if bytes.Equal(fileData[start:end], sm) {
					return ps, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("yaffs page size not detected")
}

func yaffsGetSpareSize(fileData []byte, pageSize int, endianness string) (int, error) {
	const minNumberOfObjs = 2
	spareSizes := []int{16, 32, 64, 128, 256, 512}

	yaffsObjFields := []StructField{
		{"type", "u32"}, {"parent_id", "u32"}, {"name_checksum", "u16"},
	}

	for _, ss := range spareSizes {
		nextObjOffset := (pageSize + ss) * minNumberOfObjs
		if nextObjOffset < len(fileData) {
			hdr, err := Parse(fileData[nextObjOffset:], yaffsObjFields, endianness)
			if err == nil {
				allowedTypes := []int{0, 1, 2, 3, 4, 5}
				if containsInt(allowedTypes, hdr["type"]) && hdr["parent_id"] > 0 && hdr["name_checksum"] == 0xFFFF {
					return ss, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("yaffs spare size not detected")
}

func yaffsGetImageSize(fileData []byte, pageSize, spareSize int, endianness string) (int, error) {
	const fileType = 1
	const minNumberOfObjs = 2
	const infoStructStart = 268

	yaffsObjFields := []StructField{
		{"type", "u32"}, {"parent_id", "u32"}, {"name_checksum", "u16"},
	}
	yaffsFileInfo := []StructField{
		{"mode", "u32"}, {"uid", "u32"}, {"gid", "u32"},
		{"atime", "u32"}, {"mtime", "u32"}, {"ctime", "u32"}, {"file_size", "u32"},
	}

	imageSize := 0
	nextObjOffset := 0
	var previousObjOffset *int
	availableData := len(fileData)
	blockSize := pageSize + spareSize

	for IsOffsetSafe(availableData, nextObjOffset, previousObjOffset) {
		objData := fileData[nextObjOffset:]
		hdr, err := Parse(objData, yaffsObjFields, endianness)
		if err != nil {
			break
		}
		allowedTypes := []int{0, 1, 2, 3, 4, 5}
		if !containsInt(allowedTypes, hdr["type"]) || hdr["parent_id"] <= 0 || hdr["name_checksum"] != 0xFFFF {
			break
		}

		dataBlocks := 1
		if hdr["type"] == fileType {
			if infoStructStart < len(objData) {
				fileInfo, err := Parse(objData[infoStructStart:], yaffsFileInfo, endianness)
				if err != nil {
					return 0, fmt.Errorf("yaffs file header parse failed")
				}
				fileBlockCount := int(math.Ceil(float64(fileInfo["file_size"]) / float64(pageSize)))
				dataBlocks += fileBlockCount
			} else {
				return 0, fmt.Errorf("yaffs not enough data for file header")
			}
		}

		prev := nextObjOffset
		previousObjOffset = &prev
		imageSize += dataBlocks * blockSize
		nextObjOffset = imageSize
	}

	if imageSize > blockSize*minNumberOfObjs && imageSize <= availableData {
		return imageSize, nil
	}
	return 0, fmt.Errorf("yaffs image size invalid")
}

// ---------------------------------------------------------------------------
// RomFS
// ---------------------------------------------------------------------------

const RomFSDescription = "RomFS filesystem"

func romfs_magic() [][]byte {
	return [][]byte{
		[]byte("-rom1fs-"),
	}
}

func romfs_parser(data []byte, offset int) (*SignatureResult, error) {
	const maxHeaderCRCDataLen = 512

	headerFields := []StructField{
		{"magic", "u64"}, {"image_size", "u32"}, {"checksum", "u32"},
	}

	romfsData := data[offset:]
	headerSize := StructSize(headerFields)

	hdr, err := Parse(romfsData, headerFields, "big")
	if err != nil {
		return nil, fmt.Errorf("romfs header parse failed")
	}

	if hdr["image_size"] <= headerSize {
		return nil, fmt.Errorf("romfs image size too small")
	}

	// Get volume name
	volumeName := ""
	if headerSize < len(romfsData) {
		volumeName = GetCString(romfsData[headerSize:])
	}

	// Validate CRC
	crcDataLen := maxHeaderCRCDataLen
	if hdr["image_size"] < crcDataLen {
		crcDataLen = hdr["image_size"]
	}
	if crcDataLen > len(romfsData) {
		return nil, fmt.Errorf("romfs not enough data for crc")
	}
	if !romfsCRCValid(romfsData[:crcDataLen]) {
		return nil, fmt.Errorf("romfs crc invalid")
	}

	return &SignatureResult{
		Offset:     offset,
		Size:       hdr["image_size"],
		Confidence: ConfidenceHigh,
		Description: fmt.Sprintf(`%s, volume name: "%s", total size: %d bytes`,
			RomFSDescription, volumeName, hdr["image_size"]),
	}, nil
}

func romfsCRCValid(crcData []byte) bool {
	wordSize := 4
	if len(crcData)%wordSize != 0 {
		return false
	}
	var sum uint32
	for i := 0; i < len(crcData); i += wordSize {
		sum += binary.BigEndian.Uint32(crcData[i : i+wordSize])
	}
	return sum == 0
}

// ---------------------------------------------------------------------------
// EXT
// ---------------------------------------------------------------------------

const EXTDescription = "EXT filesystem"

func ext_magic() [][]byte {
	return [][]byte{
		{0x53, 0xEF, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00},
		{0x53, 0xEF, 0x01, 0x00, 0x02, 0x00, 0x00, 0x00},
		{0x53, 0xEF, 0x01, 0x00, 0x03, 0x00, 0x00, 0x00},
		{0x53, 0xEF, 0x02, 0x00, 0x01, 0x00, 0x00, 0x00},
		{0x53, 0xEF, 0x02, 0x00, 0x02, 0x00, 0x00, 0x00},
		{0x53, 0xEF, 0x02, 0x00, 0x03, 0x00, 0x00, 0x00},
	}
}

const EXTMagicOffset = 1080

func ext_parser(data []byte, offset int) (*SignatureResult, error) {
	const magicOffset = 1080
	const superblockOffset = 1024
	const superblockSize = 1024
	const maxBlockLog = 2

	realOffset := offset - magicOffset
	if realOffset < 0 {
		return nil, fmt.Errorf("ext offset out of bounds")
	}

	extData := data[realOffset:]
	if len(extData) < superblockOffset+superblockSize {
		return nil, fmt.Errorf("ext not enough data for superblock")
	}

	extSBFields := []StructField{
		{"inodes_count", "u32"}, {"blocks_count", "u32"}, {"reserved_blocks_count", "u32"},
		{"free_blocks_count", "u32"}, {"free_inodes_count", "u32"}, {"first_data_block", "u32"},
		{"log_block_size", "u32"}, {"log_frag_size", "u32"}, {"blocks_per_group", "u32"},
		{"frags_per_group", "u32"}, {"inodes_per_group", "u32"}, {"modification_time", "u32"},
		{"write_time", "u32"}, {"mount_count", "u16"}, {"max_mount_count", "u16"},
		{"magic", "u16"}, {"state", "u16"}, {"errors", "u16"}, {"s_minor_rev_level", "u16"},
		{"last_check", "u32"}, {"check_interval", "u32"}, {"creator_os", "u32"},
		{"s_rev_level", "u32"}, {"resuid", "u16"}, {"resgid", "u16"},
	}

	hdr, err := Parse(extData[superblockOffset:], extSBFields, "little")
	if err != nil {
		return nil, fmt.Errorf("ext superblock parse failed")
	}

	supportedOS := map[int]string{0: "Linux", 1: "GNU HURD", 2: "MASIX", 3: "FreeBSD", 4: "Lites"}
	osName, osOk := supportedOS[hdr["creator_os"]]
	if !osOk {
		return nil, fmt.Errorf("ext unknown OS")
	}

	allowedRevLevels := []int{0, 1}
	if !containsInt(allowedRevLevels, hdr["s_rev_level"]) {
		return nil, fmt.Errorf("ext invalid rev level")
	}

	allowedFirstDataBlocks := []int{0, 1}
	if !containsInt(allowedFirstDataBlocks, hdr["first_data_block"]) {
		return nil, fmt.Errorf("ext invalid first_data_block")
	}

	if hdr["log_block_size"] > maxBlockLog {
		return nil, fmt.Errorf("ext invalid log_block_size")
	}

	blockSize := 1024 << hdr["log_block_size"]
	imageSize := blockSize * hdr["blocks_count"]

	return &SignatureResult{
		Offset:     realOffset,
		Size:       imageSize,
		Confidence: ConfidenceMedium,
		Description: fmt.Sprintf(
			"%s for %s, inodes: %d, block size: %d, block count: %d, free blocks: %d, reserved blocks: %d, total size: %d bytes",
			EXTDescription, osName, hdr["inodes_count"], blockSize,
			hdr["free_blocks_count"], hdr["reserved_blocks_count"], hdr["blocks_count"], imageSize),
	}, nil
}

// ---------------------------------------------------------------------------
// FAT
// ---------------------------------------------------------------------------

const FATDescription = "FAT file system"
const FATMagicOffset = 0x01FE

func fat_magic() [][]byte {
	return [][]byte{
		{0x55, 0xAA},
	}
}

func fat_parser(data []byte, offset int) (*SignatureResult, error) {
	const expectedFATCount = 2

	validOpcode1 := []int{0xEB, 0xE9}
	validSectorSizes := []int{512, 1024, 2048, 4096}
	validSectorsPerCluster := []int{1, 2, 4, 8, 16, 32, 64, 128}
	validMediaTypes := []int{0xF0, 0xF8, 0xF9, 0xFA, 0xFB, 0xFC, 0xFD, 0xFE}

	if offset < FATMagicOffset {
		return nil, fmt.Errorf("fat offset too small")
	}
	realOffset := offset - FATMagicOffset

	fatData := data[realOffset:]

	fields := []StructField{
		{"opcode1", "u8"}, {"opcode2", "u8"}, {"opcode3", "u8"},
		{"oem_name", "u64"}, {"bytes_per_sector", "u16"}, {"sectors_per_cluster", "u8"},
		{"reserved_sectors", "u16"}, {"fat_count", "u8"}, {"root_entries_count_16", "u16"},
		{"total_sectors_16", "u16"}, {"media_type", "u8"}, {"fat_size_16", "u16"},
		{"sectors_per_track", "u16"}, {"number_of_heads", "u16"},
		{"hidden_sectors", "u32"}, {"total_sectors_32", "u32"},
	}

	hdr, err := Parse(fatData, fields, "little")
	if err != nil {
		return nil, fmt.Errorf("fat header parse failed")
	}

	if !containsInt(validOpcode1, hdr["opcode1"]) {
		return nil, fmt.Errorf("fat invalid opcode1")
	}
	if !containsInt(validSectorSizes, hdr["bytes_per_sector"]) {
		return nil, fmt.Errorf("fat invalid sector size")
	}
	if !containsInt(validSectorsPerCluster, hdr["sectors_per_cluster"]) {
		return nil, fmt.Errorf("fat invalid sectors per cluster")
	}
	if hdr["reserved_sectors"] <= 0 {
		return nil, fmt.Errorf("fat invalid reserved sectors")
	}
	if hdr["fat_count"] != expectedFATCount {
		return nil, fmt.Errorf("fat invalid fat count")
	}
	if !containsInt(validMediaTypes, hdr["media_type"]) {
		return nil, fmt.Errorf("fat invalid media type")
	}

	isFAT32 := hdr["fat_size_16"] == 0

	totalSize := 0
	if hdr["total_sectors_16"] != 0 {
		totalSize = hdr["total_sectors_16"] * hdr["bytes_per_sector"]
	} else {
		totalSize = hdr["total_sectors_32"] * hdr["bytes_per_sector"]
	}
	if totalSize <= 0 {
		return nil, fmt.Errorf("fat total size is zero")
	}

	fatTypeDesc := "FAT12/16"
	if isFAT32 {
		fatTypeDesc = "FAT32"
	}

	return &SignatureResult{
		Offset:     realOffset,
		Size:       totalSize,
		Confidence: ConfidenceMedium,
		Description: fmt.Sprintf("%s, type: %s, total size: %d bytes",
			FATDescription, fatTypeDesc, totalSize),
	}, nil
}

// ---------------------------------------------------------------------------
// NTFS
// ---------------------------------------------------------------------------

const NTFSDescription = "NTFS partition"

func ntfs_magic() [][]byte {
	return [][]byte{
		{0xEB, 0x52, 0x90, 0x4E, 0x54, 0x46, 0x53, 0x20, 0x20, 0x20, 0x20},
	}
}

func ntfs_parser(data []byte, offset int) (*SignatureResult, error) {
	ntfsFields := []StructField{
		{"opcodes", "u24"}, {"magic", "u64"},
		{"bytes_per_sector", "u16"}, {"sectors_per_cluster", "u8"},
		{"unused1", "u16"}, {"unused2", "u24"}, {"unused3", "u16"},
		{"media_type", "u8"}, {"unused4", "u16"},
		{"sectors_per_track", "u16"}, {"head_count", "u16"},
		{"hidden_sector_count", "u32"}, {"unused5", "u32"},
		{"unknown", "u32"}, {"sector_count", "u64"},
	}

	ntfsData := data[offset:]
	hdr, err := Parse(ntfsData, ntfsFields, "little")
	if err != nil {
		return nil, fmt.Errorf("ntfs header parse failed")
	}

	if hdr["unused1"] != 0 || hdr["unused2"] != 0 || hdr["unused3"] != 0 ||
		hdr["unused4"] != 0 || hdr["unused5"] != 0 {
		return nil, fmt.Errorf("ntfs unused fields are not zero")
	}

	sectorSize := hdr["bytes_per_sector"]
	sectorCount := hdr["sector_count"]
	// The reported sector count does not include the NTFS boot sector itself
	totalSize := sectorSize * (sectorCount + 1)

	if totalSize <= sectorSize {
		return nil, fmt.Errorf("ntfs total size too small")
	}

	return &SignatureResult{
		Offset:     offset,
		Size:       totalSize,
		Confidence: ConfidenceMedium,
		Description: fmt.Sprintf("%s, number of sectors: %d, bytes per sector: %d, total size: %d bytes",
			NTFSDescription, sectorCount, sectorSize, totalSize),
	}, nil
}

// ---------------------------------------------------------------------------
// APFS
// ---------------------------------------------------------------------------

const APFSDescription = "APple File System"
const APFSMagicOffset = 0x20

func apfs_magic() [][]byte {
	return [][]byte{
		[]byte("NXSB"),
	}
}

func apfs_parser(data []byte, offset int) (*SignatureResult, error) {
	const mbrBlockSize = 512
	const maxFSCount = 100
	const fsCountBlockSize = 512

	if offset < APFSMagicOffset {
		return nil, fmt.Errorf("apfs offset too small")
	}
	realOffset := offset - APFSMagicOffset
	available := len(data) - realOffset

	apfsData := data[realOffset:]
	apfsStructStart := APFSMagicOffset

	fields := []StructField{
		{"magic", "u32"}, {"block_size", "u32"}, {"block_count", "u64"},
		{"nx_features", "u64"}, {"nx_ro_compat_features", "u64"}, {"nx_incompat_features", "u64"},
		{"nx_uuid_p1", "u64"}, {"nx_uuid_p2", "u64"},
		{"nx_next_oid", "u64"}, {"nx_next_xid", "u64"},
		{"nx_xp_desc_blocks", "u32"}, {"nx_xp_data_blocks", "u32"},
		{"nx_xp_desc_base", "u64"}, {"nx_xp_data_base", "u64"},
		{"nx_xp_desc_next", "u32"}, {"nx_xp_data_next", "u32"},
		{"nx_xp_desc_index", "u32"}, {"nx_xp_desc_len", "u32"},
		{"nx_xp_data_index", "u32"}, {"nx_xp_data_len", "u32"},
		{"nx_spaceman_oid", "u64"}, {"nx_omap_oid", "u64"}, {"nx_reaper_oid", "u64"},
		{"nx_xp_test_type", "u32"}, {"nx_xp_max_file_systems", "u32"},
	}

	structEnd := apfsStructStart + StructSize(fields)
	if structEnd > len(apfsData) {
		return nil, fmt.Errorf("apfs not enough data for header")
	}

	hdr, err := Parse(apfsData[apfsStructStart:structEnd], fields, "little")
	if err != nil {
		return nil, fmt.Errorf("apfs header parse failed")
	}

	if hdr["block_size"] == 0 || hdr["block_count"] == 0 {
		return nil, fmt.Errorf("apfs zero block size or count")
	}

	allowedFeatureFlags := []int{0, 1, 2, 3}
	allowedIncompatFlags := []int{0, 1, 2, 3, 0x100, 0x101, 0x102, 0x103}
	allowedROCompatFlags := []int{0}

	if !containsInt(allowedFeatureFlags, hdr["nx_features"]) ||
		!containsInt(allowedROCompatFlags, hdr["nx_ro_compat_features"]) ||
		!containsInt(allowedIncompatFlags, hdr["nx_incompat_features"]) {
		return nil, fmt.Errorf("apfs invalid feature flags")
	}

	if hdr["nx_xp_test_type"] != 0 {
		return nil, fmt.Errorf("apfs test_type must be null")
	}

	fsCount := int(math.Ceil(float64(hdr["nx_xp_max_file_systems"]) / float64(fsCountBlockSize)))
	if fsCount <= 0 || fsCount > maxFSCount {
		return nil, fmt.Errorf("apfs invalid filesystem count")
	}

	totalSize := hdr["block_count"] * hdr["block_size"]
	truncatedMsg := ""

	if totalSize > available {
		truncatedSize := totalSize - available
		if truncatedSize == mbrBlockSize {
			totalSize -= truncatedSize
			truncatedMsg = fmt.Sprintf(" (truncated by %d bytes)", truncatedSize)
		}
	}

	return &SignatureResult{
		Offset:     realOffset,
		Size:       totalSize,
		Confidence: ConfidenceMedium,
		Description: fmt.Sprintf("%s, block size: %d bytes, block count: %d, total size: %d bytes%s",
			APFSDescription, hdr["block_size"], hdr["block_count"], totalSize, truncatedMsg),
	}, nil
}

// ---------------------------------------------------------------------------
// BTRFS
// ---------------------------------------------------------------------------

const BTRFSDescription = "BTRFS file system"
const BTRFSMagicOffset = 0x10040

func btrfs_magic() [][]byte {
	return [][]byte{
		[]byte("_BHRfS_M"),
	}
}

func btrfs_parser(data []byte, offset int) (*SignatureResult, error) {
	const superblockOffset = 0x10000
	const superblockEnd = superblockOffset + 0x1000
	const crcStart = 0x20

	if offset < BTRFSMagicOffset {
		return nil, fmt.Errorf("btrfs offset too small")
	}
	realOffset := offset - BTRFSMagicOffset

	btrfsData := data[realOffset:]

	if len(btrfsData) < superblockEnd {
		return nil, fmt.Errorf("btrfs not enough data")
	}

	fields := []StructField{
		{"header_checksum", "u32"}, {"unused1", "u32"}, {"unused2", "u64"},
		{"unused3", "u64"}, {"unused4", "u64"},
		{"uuid_p1", "u64"}, {"uuid_p2", "u64"},
		{"block_phys_addr", "u64"}, {"flags", "u64"},
		{"magic", "u64"}, {"generation", "u64"},
		{"root_tree_address", "u64"}, {"chunk_tree_address", "u64"},
		{"log_tree_address", "u64"}, {"log_root_transid", "u64"},
		{"total_bytes", "u64"}, {"bytes_used", "u64"},
		{"root_dir_objid", "u64"}, {"num_devices", "u64"},
		{"sector_size", "u32"}, {"node_size", "u32"},
		{"leaf_size", "u32"}, {"stripe_size", "u32"},
	}

	headerData := btrfsData[superblockOffset:superblockEnd]
	hdr, err := Parse(headerData, fields, "little")
	if err != nil {
		return nil, fmt.Errorf("btrfs header parse failed")
	}

	// Validate CRC32C
	calcCRC := crc32c(headerData[crcStart:])
	if hdr["header_checksum"] != int(calcCRC) {
		return nil, fmt.Errorf("btrfs crc mismatch")
	}

	return &SignatureResult{
		Offset:     realOffset,
		Size:       hdr["total_bytes"],
		Confidence: ConfidenceMedium,
		Description: fmt.Sprintf(
			"%s, node size: %d, sector size: %d, leaf size: %d, stripe size: %d, bytes used: %d, total size: %d bytes",
			BTRFSDescription, hdr["node_size"], hdr["sector_size"],
			hdr["leaf_size"], hdr["stripe_size"], hdr["bytes_used"], hdr["total_bytes"]),
	}, nil
}

// crc32c computes a CRC-32C (Castagnoli) checksum.
func crc32c(data []byte) uint32 {
	// CRC-32C polynomial: 0x1EDC6F41
	const poly = 0x82F63B78 // reversed representation
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ poly
			} else {
				crc >>= 1
			}
		}
	}
	return crc ^ 0xFFFFFFFF
}

// ---------------------------------------------------------------------------
// LogFS
// ---------------------------------------------------------------------------

const LogFSDescription = "LogFS file system"
const LogFSMagicOffset = 0x18

func logfs_magic() [][]byte {
	return [][]byte{
		{0x7A, 0x3A, 0x8E, 0x5C, 0xB9, 0xD5, 0xBF, 0x67},
	}
}

func logfs_parser(data []byte, offset int) (*SignatureResult, error) {
	if offset < LogFSMagicOffset {
		return nil, fmt.Errorf("logfs offset too small")
	}
	realOffset := offset - LogFSMagicOffset

	logfsData := data[realOffset:]

	fields := []StructField{
		{"magic", "u64"}, {"crc32", "u32"},
		{"ifile_levels", "u8"}, {"iblock_levels", "u8"},
		{"data_levels", "u8"}, {"segment_shift", "u8"},
		{"block_shift", "u8"}, {"write_shift", "u8"},
		{"pad0", "u32"}, {"pad1", "u16"},
		{"filesystem_size", "u64"}, {"segment_size", "u32"},
		{"bad_seg_reserved", "u32"},
		{"feature_incompat", "u64"}, {"feature_ro_compat", "u64"},
		{"feature_compat", "u64"}, {"feature_flags", "u64"},
		{"root_reserve", "u64"}, {"speed_reserve", "u64"},
	}

	if LogFSMagicOffset >= len(logfsData) {
		return nil, fmt.Errorf("logfs not enough data")
	}

	hdr, err := Parse(logfsData[LogFSMagicOffset:], fields, "big")
	if err != nil {
		return nil, fmt.Errorf("logfs superblock parse failed")
	}

	if hdr["pad0"] != 0 || hdr["pad1"] != 0 {
		return nil, fmt.Errorf("logfs padding fields not zero")
	}

	totalSize := hdr["filesystem_size"]

	return &SignatureResult{
		Offset:     realOffset,
		Size:       totalSize,
		Confidence: ConfidenceMedium,
		Description: fmt.Sprintf("%s, total size: %d bytes",
			LogFSDescription, totalSize),
	}, nil
}

// ---------------------------------------------------------------------------
// UBI
// ---------------------------------------------------------------------------

const UBIImageDescription = "UBI image"

func ubi_magic() [][]byte {
	return [][]byte{
		{0x55, 0x42, 0x49, 0x23, 0x01}, // "UBI#" + version 1
	}
}

func ubi_parser(data []byte, offset int) (*SignatureResult, error) {
	ubiECFields := []StructField{
		{"magic", "u32"}, {"version", "u8"}, {"padding1", "u24"},
		{"ec", "u64"}, {"volume_id_header_offset", "u32"}, {"data_offset", "u32"},
		{"image_sequence_number", "u32"},
		{"padding2", "u64"}, {"padding3", "u64"}, {"padding4", "u64"}, {"padding5", "u64"},
		{"header_crc", "u32"},
	}

	ecHeaderSize := StructSize(ubiECFields)
	crcDataSize := ecHeaderSize - 4 // minus the header_crc field

	ubiData := data[offset:]
	ecHdr, err := Parse(ubiData, ubiECFields, "big")
	if err != nil {
		return nil, fmt.Errorf("ubi ec header parse failed")
	}

	if ecHdr["data_offset"] < ecHeaderSize || ecHdr["volume_id_header_offset"] < ecHeaderSize {
		return nil, fmt.Errorf("ubi invalid offsets")
	}

	// Validate header CRC
	if crcDataSize > len(ubiData) {
		return nil, fmt.Errorf("ubi not enough data for crc")
	}
	if ubiCRC(ubiData[:crcDataSize]) != ecHdr["header_crc"] {
		return nil, fmt.Errorf("ubi ec header crc mismatch")
	}

	dataOffset := offset + ecHdr["data_offset"]
	volumeOffset := offset + ecHdr["volume_id_header_offset"]
	if dataOffset >= len(data) || volumeOffset >= len(data) {
		return nil, fmt.Errorf("ubi offsets out of bounds")
	}

	// Get image size by scanning volume headers
	imageSize, err := getUBIImageSize(data[offset:])
	if err != nil {
		return nil, err
	}

	return &SignatureResult{
		Offset:     offset,
		Size:       imageSize,
		Confidence: ConfidenceHigh,
		Description: fmt.Sprintf("%s, version: %d, image size: %d bytes",
			UBIImageDescription, ecHdr["version"], imageSize),
	}, nil
}

func getUBIImageSize(ubiData []byte) (int, error) {
	volMagic := []byte{0x55, 0x42, 0x49, 0x21, 0x01} // "UBI!" + version 1

	ubiVolFields := []StructField{
		{"magic", "u32"}, {"version", "u8"}, {"volume_type", "u8"},
		{"copy_flag", "u8"}, {"compat_type", "u8"},
		{"volume_id", "u32"}, {"logical_erase_block_number", "u32"},
		{"padding1", "u32"}, {"data_size", "u32"},
		{"used_erase_block_count", "u32"}, {"data_padding_size", "u32"},
		{"data_crc", "u32"}, {"padding2", "u32"},
		{"sequence_number", "u64"},
		{"padding3", "u64"}, {"padding4", "u32"}, {"header_crc", "u32"},
	}

	volHeaderSize := StructSize(ubiVolFields)
	volCRCDataSize := volHeaderSize - 4

	blockCount := 0
	previousVolumeOffset := 0
	possibleLEBSizes := make(map[int]int)

	// Scan for volume headers
	for i := 0; i <= len(ubiData)-len(volMagic); {
		idx := bytes.Index(ubiData[i:], volMagic)
		if idx < 0 {
			break
		}
		thisVolumeOffset := i + idx

		if thisVolumeOffset+volHeaderSize > len(ubiData) {
			break
		}

		volHdr, err := Parse(ubiData[thisVolumeOffset:], ubiVolFields, "big")
		if err == nil &&
			volHdr["padding1"] == 0 && volHdr["padding2"] == 0 &&
			volHdr["padding3"] == 0 && volHdr["padding4"] == 0 {
			// Validate CRC
			if thisVolumeOffset+volCRCDataSize <= len(ubiData) {
				if ubiCRC(ubiData[thisVolumeOffset:thisVolumeOffset+volCRCDataSize]) == volHdr["header_crc"] {
					blockCount++
					if previousVolumeOffset != 0 {
						lebSize := thisVolumeOffset - previousVolumeOffset
						possibleLEBSizes[lebSize]++
					}
					previousVolumeOffset = thisVolumeOffset
				}
			}
		}
		i = thisVolumeOffset + 1
	}

	// Pick the most common LEB size
	lebSize := 0
	bestCount := 0
	for size, count := range possibleLEBSizes {
		if count > bestCount {
			lebSize = size
			bestCount = count
		}
	}

	if lebSize != 0 && blockCount != 0 {
		return blockCount * lebSize, nil
	}
	return 0, fmt.Errorf("ubi could not determine image size")
}

// ---------------------------------------------------------------------------
// UBIFS
// ---------------------------------------------------------------------------

const UBIFSDescription = "UBIFS image"

func ubifs_magic() [][]byte {
	return [][]byte{
		{0x31, 0x18, 0x10, 0x06},
	}
}

func ubifs_parser(data []byte, offset int) (*SignatureResult, error) {
	const maxGroupType = 2
	const crcStartOffset = 8
	const superblockNodeType = 6
	const superblockStructExtraSize = 3968

	ubiSBFields := []StructField{
		{"magic", "u32"}, {"header_crc", "u32"},
		{"sequence_number", "u64"}, {"node_len", "u32"},
		{"node_type", "u8"}, {"group_type", "u8"},
		{"padding1", "u32"}, {"key_hash", "u8"}, {"key_format", "u8"},
		{"flags", "u32"}, {"min_io_size", "u32"},
		{"leb_size", "u32"}, {"leb_count", "u32"}, {"max_leb_count", "u32"},
		{"max_bud_bytes", "u64"}, {"log_lebs", "u32"}, {"lpt_lebs", "u32"},
		{"orph_lebs", "u32"}, {"jhead_count", "u32"}, {"fanout", "u32"},
		{"lsave_count", "u32"}, {"fmt_version", "u32"},
		{"default_compression", "u16"}, {"padding2", "u16"},
		{"rp_uid", "u32"}, {"rp_gid", "u32"}, {"rp_size", "u64"},
		{"time_gran", "u32"}, {"uuid_p1", "u64"}, {"uuid_p2", "u64"},
		{"ro_compat_version", "u32"},
	}

	sbStructSize := StructSize(ubiSBFields) + superblockStructExtraSize

	ubisData := data[offset:]
	hdr, err := Parse(ubisData, ubiSBFields, "little")
	if err != nil {
		return nil, fmt.Errorf("ubifs superblock parse failed")
	}

	if hdr["padding1"] != 0 || hdr["padding2"] != 0 {
		return nil, fmt.Errorf("ubifs padding fields not zero")
	}
	if hdr["node_type"] != superblockNodeType {
		return nil, fmt.Errorf("ubifs not a superblock node")
	}
	if hdr["group_type"] > maxGroupType {
		return nil, fmt.Errorf("ubifs invalid group type")
	}

	// Validate header CRC
	if crcStartOffset+sbStructSize-crcStartOffset > len(ubisData) {
		return nil, fmt.Errorf("ubifs not enough data for crc")
	}
	crcData := ubisData[crcStartOffset:sbStructSize]
	if ubiCRC(crcData) != hdr["header_crc"] {
		return nil, fmt.Errorf("ubifs header crc mismatch")
	}

	imageSize := hdr["leb_count"] * hdr["leb_size"]

	return &SignatureResult{
		Offset:     offset,
		Size:       imageSize,
		Confidence: ConfidenceHigh,
		Description: fmt.Sprintf("%s, total size: %d bytes",
			UBIFSDescription, imageSize),
	}, nil
}

// ubiCRC computes a UBI checksum: NOT(CRC32(data)) & 0xFFFFFFFF
func ubiCRC(data []byte) int {
	const ubiCRCInit = uint32(0xFFFFFFFF)
	return int((^CRC32(data)) & ubiCRCInit)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsInt(slice []int, val int) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
