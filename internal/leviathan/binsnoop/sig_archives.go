package binsnoop

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// gzip
// ---------------------------------------------------------------------------

const gzipDescription = "gzip compressed data"

func gzipMagic() [][]byte {
	return [][]byte{
		{0x1f, 0x8b, 0x08},
	}
}

func gzipParser(data []byte, offset int) (*SignatureResult, error) {
	const (
		flagCRC      = 0b0000_0010
		flagExtra    = 0b0000_0100
		flagName     = 0b0000_1000
		flagComment  = 0b0001_0000
		flagReserved = 0b1110_0000

		deflateCompression = 8
		crcSize            = 2
		nullByteSize       = 1
	)

	knownOSIDs := map[int]string{
		0:   "FAT filesystem (MS-DOS, OS/2, NT/Win32",
		1:   "Amiga",
		2:   "VMS (or OpenVMS)",
		3:   "Unix",
		4:   "VM/CMS",
		5:   "Atari TOS",
		6:   "HPFS filesystem (OS/2, NT)",
		7:   "Macintosh",
		8:   "Z-System",
		9:   "CP/M",
		10:  "TOPS-20",
		11:  "NTFS filesystem (NT)",
		12:  "QDOS",
		13:  "Acorn RISCOS",
		255: "unknown",
	}

	gzipHeaderFields := []StructField{
		{"magic", "u16"},
		{"compression_method", "u8"},
		{"flags", "u8"},
		{"timestamp", "u32"},
		{"extra_flags", "u8"},
		{"osid", "u8"},
	}

	gzipExtraHeaderFields := []StructField{
		{"id", "u16"},
		{"extra_data_len", "u16"},
	}

	if offset >= len(data) {
		return nil, fmt.Errorf("gzip: offset out of bounds")
	}

	headerData := data[offset:]
	hdr, err := Parse(headerData, gzipHeaderFields, "little")
	if err != nil {
		return nil, fmt.Errorf("gzip: failed to parse header")
	}

	if hdr["flags"]&flagReserved != 0 {
		return nil, fmt.Errorf("gzip: reserved flags set")
	}
	if hdr["compression_method"] != deflateCompression {
		return nil, fmt.Errorf("gzip: invalid compression method %d", hdr["compression_method"])
	}
	osName, ok := knownOSIDs[hdr["osid"]]
	if !ok {
		return nil, fmt.Errorf("gzip: unknown OS ID %d", hdr["osid"])
	}

	headerSize := StructSize(gzipHeaderFields)

	// Handle optional extra data
	if hdr["flags"]&flagExtra != 0 {
		extraHeaderSize := StructSize(gzipExtraHeaderFields)
		extraStart := headerSize
		extraEnd := extraStart + extraHeaderSize
		if extraEnd > len(headerData) {
			return nil, fmt.Errorf("gzip: not enough data for extra header")
		}
		extraHdr, err := Parse(headerData[extraStart:], gzipExtraHeaderFields, "little")
		if err != nil {
			return nil, fmt.Errorf("gzip: failed to parse extra header")
		}
		headerSize += extraHeaderSize + extraHdr["extra_data_len"]
	}

	// Handle optional original file name (NULL-terminated)
	originalName := ""
	if hdr["flags"]&flagName != 0 {
		if headerSize >= len(headerData) {
			return nil, fmt.Errorf("gzip: not enough data for original name")
		}
		originalName = GetCString(headerData[headerSize:])
		headerSize += len(originalName) + nullByteSize
	}

	// Handle optional comment (NULL-terminated)
	if hdr["flags"]&flagComment != 0 {
		if headerSize >= len(headerData) {
			return nil, fmt.Errorf("gzip: not enough data for comment")
		}
		comment := GetCString(headerData[headerSize:])
		headerSize += len(comment) + nullByteSize
	}

	// Handle optional header CRC
	if hdr["flags"]&flagCRC != 0 {
		headerSize += crcSize
	}

	if len(headerData) < headerSize {
		return nil, fmt.Errorf("gzip: data shorter than header")
	}

	originalFileNameText := ""
	if originalName != "" {
		originalFileNameText = fmt.Sprintf(" original file name: \"%s\",", originalName)
	}

	description := fmt.Sprintf("%s,%s operating system: %s, timestamp: %s",
		gzipDescription,
		originalFileNameText,
		osName,
		EpochToString(uint32(hdr["timestamp"])),
	)

	return &SignatureResult{
		Offset:      offset,
		Size:        0, // size unknown without decompression
		Description: description,
		Confidence:  ConfidenceMedium,
	}, nil
}

// ---------------------------------------------------------------------------
// zip
// ---------------------------------------------------------------------------

const zipDescription = "ZIP archive"

func zipMagic() [][]byte {
	return [][]byte{
		{0x50, 0x4b, 0x03, 0x04}, // PK\x03\x04
	}
}

func zipParser(data []byte, offset int) (*SignatureResult, error) {
	return parseZipLike(data, offset, zipDescription)
}

// parseZipLike is shared between zip and dahua_zip parsers.
func parseZipLike(data []byte, offset int, descPrefix string) (*SignatureResult, error) {
	const (
		unusedFlagsMask      = 0b11010111_10000000
		compressionEncrypted = 99
	)

	zipLocalFileFields := []StructField{
		{"magic", "u32"},
		{"version", "u16"},
		{"flags", "u16"},
		{"compression", "u16"},
		{"modification_time", "u16"},
		{"modification_date", "u16"},
		{"crc", "u32"},
		{"compressed_size", "u32"},
		{"uncompressed_size", "u32"},
		{"file_name_len", "u16"},
		{"extra_field_len", "u16"},
	}

	allowedCompressionMethods := map[int]bool{
		0: true, 1: true, 2: true, 3: true, 4: true, 5: true, 6: true,
		8: true, 9: true, 10: true, 12: true, 14: true, 18: true,
		19: true, 20: true, 93: true, 94: true, 95: true, 96: true,
		97: true, 98: true, compressionEncrypted: true,
	}

	if offset >= len(data) {
		return nil, fmt.Errorf("zip: offset out of bounds")
	}

	zipData := data[offset:]
	hdr, err := Parse(zipData, zipLocalFileFields, "little")
	if err != nil {
		return nil, fmt.Errorf("zip: failed to parse local file header")
	}

	if hdr["flags"]&unusedFlagsMask != 0 {
		return nil, fmt.Errorf("zip: unused flags set")
	}
	if !allowedCompressionMethods[hdr["compression"]] {
		return nil, fmt.Errorf("zip: unsupported compression method %d", hdr["compression"])
	}

	versionMajor := hdr["version"] / 10
	versionMinor := hdr["version"] % 10

	localHeaderSize := StructSize(zipLocalFileFields) + hdr["file_name_len"] + hdr["extra_field_len"]
	dataSize := hdr["compressed_size"]
	if dataSize == 0 {
		dataSize = hdr["uncompressed_size"]
	}
	firstEntryTotalSize := localHeaderSize + dataSize

	// Try to find the end-of-central-directory header
	eocdMagic := []byte("PK\x05\x06\x00\x00\x00\x00")
	eocdFields := []StructField{
		{"magic", "u32"},
		{"disk_number", "u16"},
		{"central_directory_disk_number", "u16"},
		{"central_directory_disk_entries", "u16"},
		{"central_directory_total_entries", "u16"},
		{"central_directory_size", "u32"},
		{"central_directory_offset", "u32"},
		{"comment_length", "u16"},
	}

	totalSize := 0
	fileCount := 0
	missingEOCD := false

	// Search for EOCD in data after offset
	searchData := data[offset:]
	eocdIdx := -1
	for i := 0; i+len(eocdMagic) <= len(searchData); i++ {
		if bytes.Equal(searchData[i:i+len(eocdMagic)], eocdMagic) {
			eocdData := searchData[i:]
			eocdHdr, err := Parse(eocdData, eocdFields, "little")
			if err != nil {
				continue
			}
			if eocdHdr["central_directory_disk_entries"] == eocdHdr["central_directory_total_entries"] &&
				eocdHdr["central_directory_total_entries"] > 0 {
				eocdSize := StructSize(eocdFields) + eocdHdr["comment_length"]
				eocdIdx = i
				totalSize = eocdIdx + eocdSize
				fileCount = eocdHdr["central_directory_total_entries"]
				break
			}
		}
	}

	if eocdIdx < 0 {
		// No EOCD found — walk local file entries
		missingEOCD = true
		availableData := len(data) - offset
		var previousOffset *int
		nextOffset := offset + firstEntryTotalSize

		for IsOffsetSafe(availableData, nextOffset, previousOffset) {
			entryHdr, err := Parse(data[nextOffset:], zipLocalFileFields, "little")
			if err != nil {
				totalSize = nextOffset - offset
				break
			}
			if entryHdr["flags"]&unusedFlagsMask != 0 || !allowedCompressionMethods[entryHdr["compression"]] {
				totalSize = nextOffset - offset
				break
			}
			entryHeaderSize := StructSize(zipLocalFileFields) + entryHdr["file_name_len"] + entryHdr["extra_field_len"]
			entryDataSize := entryHdr["compressed_size"]
			if entryDataSize == 0 {
				entryDataSize = entryHdr["uncompressed_size"]
			}
			prev := nextOffset
			previousOffset = &prev
			nextOffset += entryHeaderSize + entryDataSize
		}
		if totalSize == 0 {
			totalSize = nextOffset - offset
		}
	}

	if totalSize <= 0 {
		return nil, fmt.Errorf("zip: could not determine archive size")
	}

	var description string
	if missingEOCD {
		description = fmt.Sprintf("%s, version: %d.%d, missing end-of-central-directory header, total size: %d bytes",
			descPrefix, versionMajor, versionMinor, totalSize)
	} else {
		description = fmt.Sprintf("%s, version: %d.%d, file count: %d, total size: %d bytes",
			descPrefix, versionMajor, versionMinor, fileCount, totalSize)
	}

	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: description,
		Confidence:  ConfidenceHigh,
	}, nil
}

// ---------------------------------------------------------------------------
// tarball
// ---------------------------------------------------------------------------

const tarballDescription = "POSIX tar archive"

const (
	tarballBlockSize       = 512
	tarballMagicOffset     = 257
	tarballMagicSize       = 5
	tarballSizeOffset      = 124
	tarballSizeLen         = 11
	tarballMinExpectedHdrs = 10
	tarballChecksumStart   = 148
	tarballChecksumEnd     = 156
)

var tarballUniversalMagic = []byte("ustar")

func tarballMagic() [][]byte {
	return [][]byte{
		{0x75, 0x73, 0x74, 0x61, 0x72, 0x00},             // ustar\x00
		{0x75, 0x73, 0x74, 0x61, 0x72, 0x20, 0x20, 0x00}, // ustar\x20\x20\x00
	}
}

func tarballParser(data []byte, offset int) (*SignatureResult, error) {
	// The magic bytes are at offset 257 within a tar header block
	tarballStartOffset := offset - tarballMagicOffset

	if tarballStartOffset < 0 || tarballStartOffset >= len(data) {
		return nil, fmt.Errorf("tarball: invalid start offset")
	}

	var tarballTotalSize int
	var validHeaderCount int
	nextHeaderStart := tarballStartOffset
	var previousHeaderStart *int
	availableData := len(data)

	for IsOffsetSafe(availableData, nextHeaderStart, previousHeaderStart) {
		nextHeaderEnd := nextHeaderStart + tarballBlockSize
		if nextHeaderEnd > len(data) {
			break
		}
		headerBlock := data[nextHeaderStart:nextHeaderEnd]

		if !tarballHeaderChecksumValid(headerBlock) {
			break
		}

		validHeaderCount++

		entrySize, err := tarballEntrySize(headerBlock)
		if err != nil {
			break
		}

		tarballTotalSize += entrySize
		prev := nextHeaderStart
		previousHeaderStart = &prev
		nextHeaderStart += entrySize
	}

	if tarballTotalSize < tarballBlockSize {
		return nil, fmt.Errorf("tarball: archive too small")
	}

	confidence := ConfidenceMedium
	if validHeaderCount >= tarballMinExpectedHdrs {
		confidence = ConfidenceHigh
	}

	return &SignatureResult{
		Offset:      tarballStartOffset,
		Size:        tarballTotalSize,
		Description: fmt.Sprintf("%s, file count: %d", tarballDescription, validHeaderCount),
		Confidence:  confidence,
	}, nil
}

func tarballHeaderChecksumValid(headerBlock []byte) bool {
	checksumValueBytes := headerBlock[tarballChecksumStart:tarballChecksumEnd]
	reportedChecksum := tarballOctal(checksumValueBytes)
	var sum int
	for i, b := range headerBlock {
		if i >= tarballChecksumStart && i < tarballChecksumEnd {
			sum += 0x20
		} else {
			sum += int(b)
		}
	}
	return sum == reportedChecksum
}

func tarballEntrySize(entryData []byte) (int, error) {
	entryMagic := entryData[tarballMagicOffset : tarballMagicOffset+tarballMagicSize]
	if !bytes.Equal(entryMagic, tarballUniversalMagic) {
		return 0, fmt.Errorf("tarball: invalid entry magic")
	}

	entrySizeBytes := entryData[tarballSizeOffset : tarballSizeOffset+tarballSizeLen]
	reportedEntrySize := tarballOctal(entrySizeBytes)

	blockCount := 1 + int(math.Ceil(float64(reportedEntrySize)/float64(tarballBlockSize)))
	return blockCount * tarballBlockSize, nil
}

func tarballOctal(octalStr []byte) int {
	var num int
	for _, c := range octalStr {
		if c < 0x30 || c > 0x39 {
			break
		}
		num = num*8 + int(c) - 0x30
	}
	return num
}

// ---------------------------------------------------------------------------
// 7-zip (sevenzip)
// ---------------------------------------------------------------------------

const sevenzipDescription = "7-zip archive data"

func sevenzipMagic() [][]byte {
	return [][]byte{
		{0x37, 0x7a, 0xbc, 0xaf, 0x27, 0x1c}, // 7z\xbc\xaf\x27\x1c
	}
}

func sevenzipParser(data []byte, offset int) (*SignatureResult, error) {
	const (
		sevenzipCRCStart   = 12
		sevenzipHeaderSize = 32
	)

	sevenzipFields := []StructField{
		{"magic_p1", "u16"},
		{"magic_p2", "u32"},
		{"major_version", "u8"},
		{"minor_version", "u8"},
		{"header_crc", "u32"},
		{"next_header_offset", "u64"},
		{"next_header_size", "u64"},
		{"next_header_crc", "u32"},
	}

	if offset >= len(data) {
		return nil, fmt.Errorf("7zip: offset out of bounds")
	}

	szData := data[offset:]
	hdr, err := Parse(szData, sevenzipFields, "little")
	if err != nil {
		return nil, fmt.Errorf("7zip: failed to parse header")
	}

	// Validate the header CRC (over bytes 12..32)
	if sevenzipHeaderSize > len(szData) {
		return nil, fmt.Errorf("7zip: not enough data for header CRC")
	}
	crcData := szData[sevenzipCRCStart:sevenzipHeaderSize]
	calculatedHeaderCRC := CRC32(crcData)
	if calculatedHeaderCRC != uint32(hdr["header_crc"]) {
		return nil, fmt.Errorf("7zip: header CRC mismatch")
	}

	// Validate the next_header CRC
	nextCRCStart := offset + sevenzipHeaderSize + hdr["next_header_offset"]
	nextCRCEnd := nextCRCStart + hdr["next_header_size"]
	if nextCRCEnd > len(data) {
		return nil, fmt.Errorf("7zip: not enough data for next header CRC")
	}
	nextCRCData := data[nextCRCStart:nextCRCEnd]
	calculatedNextCRC := CRC32(nextCRCData)
	if int(calculatedNextCRC) != hdr["next_header_crc"] {
		return nil, fmt.Errorf("7zip: next header CRC mismatch")
	}

	totalSize := sevenzipHeaderSize + hdr["next_header_offset"] + hdr["next_header_size"]

	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, version %d.%d, total size: %d bytes", sevenzipDescription, hdr["major_version"], hdr["minor_version"], totalSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ---------------------------------------------------------------------------
// RAR
// ---------------------------------------------------------------------------

const rarDescription = "RAR archive"

func rarMagic() [][]byte {
	return [][]byte{
		{0x52, 0x61, 0x72, 0x21, 0x1a, 0x07}, // Rar!\x1A\x07
	}
}

func rarParser(data []byte, offset int) (*SignatureResult, error) {
	archiveHeaderFields := []StructField{
		{"magic_p1", "u32"},
		{"magic_p2", "u16"},
		{"version", "u8"},
	}

	// version field 0 => RARv4, 1 => RARv5
	versionMap := map[int]int{0: 4, 1: 5}

	if offset >= len(data) {
		return nil, fmt.Errorf("rar: offset out of bounds")
	}

	hdr, err := Parse(data[offset:], archiveHeaderFields, "little")
	if err != nil {
		return nil, fmt.Errorf("rar: failed to parse header")
	}

	version, ok := versionMap[hdr["version"]]
	if !ok {
		return nil, fmt.Errorf("rar: unknown version %d", hdr["version"])
	}

	// EOF markers for RARv4 and RARv5
	eofMarkers := map[int][]byte{
		4: {0xc4, 0x3d, 0x7b, 0x00, 0x40, 0x07, 0x00},
		5: {0x1d, 0x77, 0x56, 0x51, 0x03, 0x05, 0x04, 0x00},
	}

	totalSize := 0
	extraDesc := ""

	if marker, ok := eofMarkers[version]; ok {
		searchData := data[offset:]
		idx := bytes.Index(searchData, marker)
		if idx >= 0 {
			totalSize = idx + len(marker)
		} else {
			extraDesc = " (failed to locate RAR EOF)"
		}
	}

	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, version: %d, total size: %d bytes%s", rarDescription, version, totalSize, extraDesc),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ---------------------------------------------------------------------------
// CAB (Microsoft Cabinet)
// ---------------------------------------------------------------------------

const cabDescription = "Microsoft Cabinet archive"

func cabMagic() [][]byte {
	return [][]byte{
		{0x4d, 0x53, 0x43, 0x46, 0x00, 0x00, 0x00, 0x00}, // MSCF\x00\x00\x00\x00
	}
}

func cabParser(data []byte, offset int) (*SignatureResult, error) {
	const (
		majorVersion         = 1
		minorVersion         = 3
		cabStructSize        = 40
		cabExtraStructSize   = 20
		flagExtraDataPresent = 4
	)

	cabHeaderFields := []StructField{
		{"magic", "u32"},
		{"reserved1", "u32"},
		{"size", "u32"},
		{"reserved2", "u32"},
		{"first_file_offset", "u32"},
		{"reserved3", "u32"},
		{"minor_version", "u8"},
		{"major_version", "u8"},
		{"folder_count", "u16"},
		{"file_count", "u16"},
		{"flags", "u16"},
		{"id", "u16"},
		{"set_number", "u16"},
		{"extra_header_size", "u16"},
		{"cbCFFolder", "u8"},
		{"cbCFData", "u8"},
	}

	cabExtraHeaderFields := []StructField{
		{"unknown1", "u32"},
		{"data_offset", "u32"},
		{"data_size", "u32"},
		{"unknown2", "u32"},
		{"unknown3", "u32"},
	}

	if offset >= len(data) {
		return nil, fmt.Errorf("cab: offset out of bounds")
	}

	hdr, err := Parse(data[offset:], cabHeaderFields, "little")
	if err != nil {
		return nil, fmt.Errorf("cab: failed to parse header")
	}

	// All reserved fields must be 0
	if hdr["reserved1"] != 0 || hdr["reserved2"] != 0 || hdr["reserved3"] != 0 {
		return nil, fmt.Errorf("cab: reserved fields not zero")
	}

	// Version must be 1.3
	if hdr["major_version"] != majorVersion || hdr["minor_version"] != minorVersion {
		return nil, fmt.Errorf("cab: invalid version %d.%d", hdr["major_version"], hdr["minor_version"])
	}

	totalSize := hdr["size"]
	headerSize := cabStructSize

	// Handle extra data flag
	if hdr["flags"]&flagExtraDataPresent != 0 && hdr["extra_header_size"] == cabExtraStructSize {
		extraStart := cabStructSize
		extraEnd := extraStart + cabExtraStructSize
		if offset+extraEnd > len(data) {
			return nil, fmt.Errorf("cab: not enough data for extra header")
		}
		extraHdr, err := Parse(data[offset+extraStart:], cabExtraHeaderFields, "little")
		if err != nil {
			return nil, fmt.Errorf("cab: failed to parse extra header")
		}
		if extraHdr["data_offset"] != hdr["size"] {
			return nil, fmt.Errorf("cab: extra data offset mismatch")
		}
		totalSize += extraHdr["data_size"]
		_ = headerSize
	} else if hdr["flags"]&flagExtraDataPresent != 0 {
		return nil, fmt.Errorf("cab: extra data flag set but wrong extra header size")
	}

	availableData := len(data) - offset
	if totalSize > availableData {
		return nil, fmt.Errorf("cab: reported size exceeds available data")
	}

	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, file count: %d, folder count: %d, header size: %d, total size: %d bytes", cabDescription, hdr["file_count"], hdr["folder_count"], cabStructSize, totalSize),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ---------------------------------------------------------------------------
// Debian package (deb)
// ---------------------------------------------------------------------------

const debDescription = "Debian package file"

func debMagic() [][]byte {
	return [][]byte{
		// "!<arch>\ndebian-binary   "
		{0x21, 0x3c, 0x61, 0x72, 0x63, 0x68, 0x3e, 0x0a,
			0x64, 0x65, 0x62, 0x69, 0x61, 0x6e, 0x2d, 0x62,
			0x69, 0x6e, 0x61, 0x72, 0x79, 0x20, 0x20, 0x20},
	}
}

func debParser(data []byte, offset int) (*SignatureResult, error) {
	const (
		endMarkerSize        = 2
		dataFileSizeLen      = 10
		dataFileSizeOffset   = 48
		controlFileSizeEnd   = 130
		controlFileSizeStart = 120
	)

	if offset >= len(data) {
		return nil, fmt.Errorf("deb: offset out of bounds")
	}

	debData := data[offset:]

	if len(debData) < controlFileSizeEnd {
		return nil, fmt.Errorf("deb: not enough data for control file size")
	}

	// Read control file size as ASCII decimal
	controlFileSizeBytes := debData[controlFileSizeStart:controlFileSizeEnd]
	controlFileSizeStr := strings.TrimSpace(string(controlFileSizeBytes))
	controlFileSize, err := strconv.Atoi(controlFileSizeStr)
	if err != nil {
		return nil, fmt.Errorf("deb: invalid control file size: %s", controlFileSizeStr)
	}

	// Calculate data file size location
	dataFileSizeStart := controlFileSizeEnd + endMarkerSize + controlFileSize + dataFileSizeOffset
	dataFileSizeEnd := dataFileSizeStart + dataFileSizeLen

	if dataFileSizeEnd > len(debData) {
		return nil, fmt.Errorf("deb: not enough data for data file size")
	}

	dataFileSizeBytes := debData[dataFileSizeStart:dataFileSizeEnd]
	dataFileSizeStr := strings.TrimSpace(string(dataFileSizeBytes))
	dataFileSize, err := strconv.Atoi(dataFileSizeStr)
	if err != nil {
		return nil, fmt.Errorf("deb: invalid data file size: %s", dataFileSizeStr)
	}

	totalSize := dataFileSizeEnd + endMarkerSize + dataFileSize

	if totalSize > len(debData) {
		return nil, fmt.Errorf("deb: reported size exceeds available data")
	}

	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, total size: %d bytes", debDescription, totalSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ---------------------------------------------------------------------------
// CPIO
// ---------------------------------------------------------------------------

const cpioDescription = "CPIO ASCII archive"

const cpioHeaderSize = 110

func cpioMagic() [][]byte {
	return [][]byte{
		{0x30, 0x37, 0x30, 0x37, 0x30, 0x31}, // 070701
		{0x30, 0x37, 0x30, 0x37, 0x30, 0x32}, // 070702
	}
}

func cpioParser(data []byte, offset int) (*SignatureResult, error) {
	const eofMarker = "TRAILER!!!"

	if offset >= len(data) {
		return nil, fmt.Errorf("cpio: offset out of bounds")
	}

	validMagics := cpioMagic()

	var headerCount int
	var totalSize int
	nextHeaderOffset := offset
	var previousHeaderOffset *int
	availableData := len(data)

	for IsOffsetSafe(availableData, nextHeaderOffset, previousHeaderOffset) {
		if nextHeaderOffset >= len(data) {
			break
		}
		cpioEntryData := data[nextHeaderOffset:]

		entryHeader, err := parseCPIOEntryHeader(cpioEntryData)
		if err != nil {
			break
		}

		// Validate magic
		magicValid := false
		for _, m := range validMagics {
			if bytes.Equal(entryHeader.magic, m) {
				magicValid = true
				break
			}
		}
		if !magicValid {
			break
		}

		headerCount++
		totalSize += entryHeader.headerSize + entryHeader.dataSize

		if entryHeader.fileName == eofMarker {
			if headerCount > 1 {
				return &SignatureResult{
					Offset:      offset,
					Size:        totalSize,
					Description: fmt.Sprintf("%s, file count: %d", cpioDescription, headerCount-1),
					Confidence:  ConfidenceHigh,
				}, nil
			}
			break
		}

		prev := nextHeaderOffset
		previousHeaderOffset = &prev
		nextHeaderOffset = offset + totalSize
	}

	return nil, fmt.Errorf("cpio: no valid archive found")
}

type cpioEntry struct {
	magic      []byte
	fileName   string
	headerSize int
	dataSize   int
}

func parseCPIOEntryHeader(cpioData []byte) (*cpioEntry, error) {
	const (
		nullByteSize      = 1
		cpioMagicStart    = 0
		cpioMagicEnd      = 6
		fileSizeStart     = 54
		fileSizeEnd       = 62
		fileNameSizeStart = 94
		fileNameSizeEnd   = 102
	)

	if len(cpioData) <= cpioHeaderSize {
		return nil, fmt.Errorf("cpio: not enough data")
	}

	headerMagic := make([]byte, cpioMagicEnd-cpioMagicStart)
	copy(headerMagic, cpioData[cpioMagicStart:cpioMagicEnd])

	// Get file data size (ASCII hex)
	fileSizeStr := string(cpioData[fileSizeStart:fileSizeEnd])
	fileDataSize, err := strconv.ParseInt(fileSizeStr, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("cpio: invalid file data size")
	}

	// Get file name size (ASCII hex)
	fileNameSizeStr := string(cpioData[fileNameSizeStart:fileNameSizeEnd])
	fileNameSize, err := strconv.ParseInt(fileNameSizeStr, 16, 64)
	if err != nil {
		return nil, fmt.Errorf("cpio: invalid file name size")
	}

	fileNameStart := cpioHeaderSize
	fileNameEnd := fileNameStart + int(fileNameSize) - nullByteSize

	if fileNameEnd > len(cpioData) || fileNameEnd < fileNameStart {
		return nil, fmt.Errorf("cpio: file name out of bounds")
	}

	fileName := string(cpioData[fileNameStart:fileNameEnd])

	headerTotalSize := cpioHeaderSize + int(fileNameSize)

	return &cpioEntry{
		magic:      headerMagic,
		fileName:   fileName,
		headerSize: headerTotalSize + cpioBytePadding(headerTotalSize),
		dataSize:   int(fileDataSize) + cpioBytePadding(int(fileDataSize)),
	}, nil
}

func cpioBytePadding(n int) int {
	mod := n % 4
	if mod == 0 {
		return 0
	}
	return 4 - mod
}

// ---------------------------------------------------------------------------
// ARJ
// ---------------------------------------------------------------------------

const arjDescription = "ARJ archive data"

func arjMagic() [][]byte {
	return [][]byte{
		{0x60, 0xea},
	}
}

func arjParser(data []byte, offset int) (*SignatureResult, error) {
	arjHeaderFields := []StructField{
		{"magic", "u16"},
		{"basic_header_size", "u16"},
		{"extra_header_size", "u8"},
		{"archiver_version", "u8"},
		{"min_version", "u8"},
		{"host_os", "u8"},
		{"internal_flags", "u8"},
		{"compression_method", "u8"},
		{"file_type", "u8"},
		{"reserved1", "u8"},
		{"datetime_file", "u32"},
		{"compressed_filesize", "u32"},
		{"original_filesize", "u32"},
	}

	if offset >= len(data) {
		return nil, fmt.Errorf("arj: offset out of bounds")
	}

	hdr, err := Parse(data[offset:], arjHeaderFields, "little")
	if err != nil {
		return nil, fmt.Errorf("arj: failed to parse header")
	}

	// Version validation
	if hdr["archiver_version"] < 1 || hdr["archiver_version"] > 16 ||
		hdr["min_version"] < 1 || hdr["min_version"] > 16 ||
		hdr["archiver_version"] < hdr["min_version"] {
		return nil, fmt.Errorf("arj: invalid version info")
	}

	// Flags
	flags := "no password"
	if hdr["internal_flags"]&0x01 != 0 {
		flags = "password"
	}
	if hdr["internal_flags"]&0x04 != 0 {
		flags += "|multi-volume"
	}
	if hdr["internal_flags"]&0x10 != 0 {
		flags += "|slash-switched"
	}
	if hdr["internal_flags"]&0x20 != 0 {
		flags += "|backup"
	}

	// Host OS
	hostOSMap := map[int]string{
		0: "MS-DOS", 1: "PRIMOS", 2: "UNIX", 3: "AMIGA", 4: "MAX-OS",
		5: "OS/2", 6: "APPLE GS", 7: "ATARI ST", 8: "NeXT", 9: "VAX VMS",
	}
	hostOS, ok := hostOSMap[hdr["host_os"]]
	if !ok {
		return nil, fmt.Errorf("arj: unknown host OS %d", hdr["host_os"])
	}

	// Compression method
	compressionMethodMap := map[int]string{
		0: "stored", 1: "compressed most", 2: "compressed",
		3: "compressed faster", 4: "compressed fastest",
	}
	compressionMethod, ok := compressionMethodMap[hdr["compression_method"]]
	if !ok {
		return nil, fmt.Errorf("arj: unknown compression method %d", hdr["compression_method"])
	}

	// File type
	fileTypeMap := map[int]string{
		0: "binary", 1: "7-bit text", 2: "comment header",
		3: "directory", 4: "volume label",
	}
	fileType, ok := fileTypeMap[hdr["file_type"]]
	if !ok {
		return nil, fmt.Errorf("arj: unknown file type %d", hdr["file_type"])
	}

	// Compressed/uncompressed sizes — check for negative as signed i32
	compressedFileSize := int32(hdr["compressed_filesize"])
	if compressedFileSize < 0 {
		return nil, fmt.Errorf("arj: negative compressed file size")
	}
	uncompressedFileSize := int32(hdr["original_filesize"])
	if uncompressedFileSize < 0 {
		return nil, fmt.Errorf("arj: negative uncompressed file size")
	}

	// Original file name
	headerSize := hdr["extra_header_size"]
	originalName := ""
	if headerSize+4 < len(data[offset:]) {
		originalName = GetCString(data[offset+headerSize+4:])
	}

	originalFileDate := EpochToString(uint32(hdr["datetime_file"]))

	availableData := len(data) - offset
	if headerSize > availableData {
		return nil, fmt.Errorf("arj: header size exceeds available data")
	}

	extractionDeclined := fileType != "comment header"

	return &SignatureResult{
		Offset: offset,
		Size:   headerSize,
		Description: fmt.Sprintf(
			"%s, header size: %d, version %d, minimum version to extract: %d, flags: %s, compression method: %s, file type: %s, original name: %s, original file date: %s, compressed file size: %d, uncompressed file size: %d, os: %s",
			arjDescription,
			headerSize,
			hdr["archiver_version"],
			hdr["min_version"],
			flags,
			compressionMethod,
			fileType,
			originalName,
			originalFileDate,
			compressedFileSize,
			uncompressedFileSize,
			hostOS,
		),
		Confidence:         ConfidenceMedium,
		ExtractionDeclined: extractionDeclined,
	}, nil
}

// ---------------------------------------------------------------------------
// ISO 9660
// ---------------------------------------------------------------------------

const iso9660Description = "ISO9660 primary volume"

func iso9660Magic() [][]byte {
	return [][]byte{
		{0x01, 0x43, 0x44, 0x30, 0x30, 0x31, 0x01, 0x00}, // \x01CD001\x01\x00
	}
}

func iso9660Parser(data []byte, offset int) (*SignatureResult, error) {
	const (
		isoMagicOffset = 32768
		isoStructStart = 32840
	)

	isoFields := []StructField{
		{"unused1", "u64"},
		{"volume_size_lsb", "u32"},
		{"volume_size_msb", "u32"},
		{"unused2", "u64"},
		{"unused3", "u64"},
		{"unused4", "u64"},
		{"unused5", "u64"},
		{"set_size_lsb", "u16"},
		{"set_size_msb", "u16"},
		{"sequence_number_lsb", "u16"},
		{"sequence_number_msb", "u16"},
		{"block_size_lsb", "u16"},
		{"block_size_msb", "u16"},
		{"path_table_size_lsb", "u32"},
		{"path_table_size_msb", "u32"},
	}

	if offset < isoMagicOffset {
		return nil, fmt.Errorf("iso9660: not enough data before magic offset")
	}

	isoStart := offset - isoMagicOffset

	if isoStart+isoStructStart >= len(data) {
		return nil, fmt.Errorf("iso9660: not enough data for header")
	}

	isoHeaderData := data[isoStart+isoStructStart:]
	hdr, err := Parse(isoHeaderData, isoFields, "little")
	if err != nil {
		return nil, fmt.Errorf("iso9660: failed to parse header")
	}

	// Unused fields must be 0
	if hdr["unused1"] != 0 || hdr["unused2"] != 0 || hdr["unused3"] != 0 ||
		hdr["unused4"] != 0 || hdr["unused5"] != 0 {
		return nil, fmt.Errorf("iso9660: unused fields not zero")
	}

	// Validate byte-swapped field pairs
	if hdr["set_size_lsb"] != int(swapU16(uint16(hdr["set_size_msb"]))) {
		return nil, fmt.Errorf("iso9660: set_size mismatch")
	}
	if hdr["block_size_lsb"] != int(swapU16(uint16(hdr["block_size_msb"]))) {
		return nil, fmt.Errorf("iso9660: block_size mismatch")
	}
	if hdr["volume_size_lsb"] != int(swapU32(uint32(hdr["volume_size_msb"]))) {
		return nil, fmt.Errorf("iso9660: volume_size mismatch")
	}
	if hdr["sequence_number_lsb"] != int(swapU16(uint16(hdr["sequence_number_msb"]))) {
		return nil, fmt.Errorf("iso9660: sequence_number mismatch")
	}
	if hdr["path_table_size_lsb"] != int(swapU32(uint32(hdr["path_table_size_msb"]))) {
		return nil, fmt.Errorf("iso9660: path_table_size mismatch")
	}

	imageSize := hdr["volume_size_lsb"] * hdr["block_size_lsb"]

	return &SignatureResult{
		Offset:      isoStart,
		Size:        imageSize,
		Description: fmt.Sprintf("%s, total size: %d bytes", iso9660Description, imageSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// swapU16 swaps bytes of a uint16 (big-endian to native / vice versa)
func swapU16(v uint16) uint16 {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return binary.LittleEndian.Uint16(b)
}

// swapU32 swaps bytes of a uint32
func swapU32(v uint32) uint32 {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return binary.LittleEndian.Uint32(b)
}

// ---------------------------------------------------------------------------
// Dahua ZIP
// ---------------------------------------------------------------------------

const dahuaZipDescription = "Dahua ZIP archive"

func dahuaZipMagic() [][]byte {
	return [][]byte{
		{0x44, 0x48, 0x03, 0x04}, // DH\x03\x04
	}
}

func dahuaZipParser(data []byte, offset int) (*SignatureResult, error) {
	// Parse as a normal ZIP file, then replace the description prefix
	result, err := parseZipLike(data, offset, zipDescription)
	if err != nil {
		return nil, fmt.Errorf("dahua_zip: %v", err)
	}
	result.Description = strings.Replace(result.Description, zipDescription, dahuaZipDescription, 1)
	return result, nil
}
