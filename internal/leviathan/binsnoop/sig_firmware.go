package binsnoop

import (
	"encoding/hex"
	"fmt"
)

// ============================================================================
// uImage
// ============================================================================

const uimageDescription = "uImage firmware image"

func uimageMagic() [][]byte {
	return [][]byte{
		{0x27, 0x05, 0x19, 0x56},
		[]byte("OKLI"),
	}
}

func uimageParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+64 > len(data) {
		return nil, fmt.Errorf("insufficient data for uImage header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_crc", "u32"}, {"timestamp", "u32"},
		{"data_size", "u32"}, {"load_address", "u32"}, {"entry_point", "u32"},
		{"data_crc", "u32"}, {"os_type", "u8"}, {"cpu_type", "u8"},
		{"image_type", "u8"}, {"compression_type", "u8"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := 64
	dataSize := parsed["data_size"]
	if dataSize == 0 {
		return nil, fmt.Errorf("uImage data size is 0")
	}
	totalSize := headerSize + dataSize
	if offset+totalSize > len(data) {
		return nil, fmt.Errorf("uImage extends beyond EOF")
	}

	// Validate header CRC: zero out the header_crc field, calc CRC32 over 64 bytes
	headerCopy := make([]byte, headerSize)
	copy(headerCopy, hdr[:headerSize])
	headerCopy[4] = 0
	headerCopy[5] = 0
	headerCopy[6] = 0
	headerCopy[7] = 0
	calcCRC := CRC32(headerCopy)
	headerCRCValid := calcCRC == uint32(parsed["header_crc"])

	osMap := map[int]string{0: "OpenBSD", 1: "NetBSD", 2: "FreeBSD", 3: "4.4BSD", 4: "Linux", 5: "SVR4", 6: "Esix", 7: "Solaris", 8: "Irix", 9: "SCO", 10: "Dell", 11: "NCR", 12: "LynxOS", 13: "VxWorks", 14: "pSOS", 15: "QNX", 16: "U-Boot", 17: "RTEMS", 18: "ARTOS", 19: "Unity OS", 20: "INTEGRITY", 21: "OSE", 22: "Plan 9", 23: "OpenRTOS", 24: "ARM Trusted Firmware", 25: "Trusted Execution Environment", 26: "RISC-V OpenSBI", 27: "EFI Firmware"}
	cpuMap := map[int]string{0: "Invalid", 1: "Alpha", 2: "ARM", 3: "Intel x86", 4: "IA64", 5: "MIPS", 6: "MIPS 64-bit", 7: "PowerPC", 8: "IBM S390", 9: "SuperH", 10: "Sparc", 11: "Sparc 64-bit", 12: "M68K", 13: "Nios-32", 14: "MicroBlaze", 15: "Nios-II", 16: "Blackfin", 17: "AVR32", 18: "ST200", 19: "Sandbox", 20: "NDS32", 21: "OpenRISC 1000", 22: "ARM64", 23: "ARC", 24: "x86_64", 25: "Xtensa", 26: "RISC-V"}
	compMap := map[int]string{0: "none", 1: "gzip", 2: "bzip2", 3: "lzma", 4: "lzo", 5: "lz4", 6: "zstd"}
	imgMap := map[int]string{0: "Invalid", 1: "Standalone", 2: "Kernel", 3: "RAMDisk", 4: "Multi-File", 5: "Firmware", 6: "Script", 7: "Filesystem", 8: "Flat DT", 9: "Kwb", 10: "IMX", 11: "AIS", 12: "Kernel (no load)", 13: "PBL", 14: "MXS", 15: "GP Header", 16: "ATMEL", 17: "IMX (Silicon Labs)", 18: "TEE", 19: "FW IVT", 20: "PMC", 21: "STM32", 22: "Renesas SPKG", 23: "ST SunXI", 24: "FDT Legacy"}

	osType := lookupMap(osMap, parsed["os_type"], "Unknown")
	cpuType := lookupMap(cpuMap, parsed["cpu_type"], "Unknown")
	compType := lookupMap(compMap, parsed["compression_type"], "Unknown")
	imageType := lookupMap(imgMap, parsed["image_type"], "Unknown")
	name := GetCString(hdr[32:64])

	confidence := ConfidenceHigh
	desc := fmt.Sprintf("%s, header size: %d bytes, data size: %d bytes, compression: %s, CPU: %s, OS: %s, image type: %s, load address: 0x%X, entry point: 0x%X, creation time: %s, image name: \"%s\"",
		uimageDescription, headerSize, dataSize, compType, cpuType, osType, imageType,
		parsed["load_address"], parsed["entry_point"],
		EpochToString(uint32(parsed["timestamp"])), name)

	if !headerCRCValid {
		if offset == 0 {
			confidence = ConfidenceMedium
		} else {
			confidence = ConfidenceLow
		}
		desc += ", invalid checksum"
	}

	return &SignatureResult{
		Offset:             offset,
		Size:               totalSize,
		Description:        desc,
		Confidence:         confidence,
		ExtractionDeclined: !headerCRCValid || dataSize == 0,
	}, nil
}

// ============================================================================
// U-Boot version string
// ============================================================================

const ubootDescription = "U-Boot version string"

func ubootMagic() [][]byte {
	return [][]byte{[]byte("U-Boot ")}
}

func ubootParser(data []byte, offset int) (*SignatureResult, error) {
	numOff := offset + 7
	if numOff >= len(data) {
		return nil, fmt.Errorf("insufficient data")
	}
	if !IsASCIINumber(data[numOff]) {
		return nil, fmt.Errorf("expected version number")
	}
	ver := GetCString(data[numOff:])
	if len(ver) == 0 {
		return nil, fmt.Errorf("empty version string")
	}
	if len(ver) > 100 {
		ver = ver[:100]
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        len(ver),
		Description: fmt.Sprintf("%s: %s", ubootDescription, ver),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// TRX
// ============================================================================

const trxDescription = "TRX firmware image"

func trxMagic() [][]byte {
	return [][]byte{[]byte("HDR0")}
}

func trxParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+28 > len(data) {
		return nil, fmt.Errorf("insufficient data for TRX header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"total_size", "u32"}, {"header_crc", "u32"},
		{"version", "u16"}, {"flags", "u16"},
		{"partition1", "u32"}, {"partition2", "u32"}, {"partition3", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	totalSize := parsed["total_size"]
	if totalSize <= 28 || offset+totalSize > len(data) {
		return nil, fmt.Errorf("invalid TRX size")
	}
	// CRC32 validation (over data after the CRC field)
	if offset+12 < len(data) {
		calcCRC := CRC32(hdr[12:totalSize])
		if int(calcCRC) != parsed["header_crc"] {
			return nil, fmt.Errorf("TRX CRC mismatch")
		}
	}
	partCount := 0
	for _, p := range []string{"partition1", "partition2", "partition3"} {
		if parsed[p] > 0 {
			partCount++
		}
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, version %d, partition count: %d, header size: 28 bytes, total size: %d bytes", trxDescription, parsed["version"], partCount, totalSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// TP-Link firmware header
// ============================================================================

const tplinkDescription = "TP-Link firmware header"

func tplinkMagic() [][]byte {
	return [][]byte{
		{0x01, 0x00, 0x00, 0x00, 0x54, 0x50, 0x2D, 0x4C, 0x49, 0x4E, 0x4B, 0x20, 0x54, 0x65, 0x63, 0x68, 0x6E, 0x6F, 0x6C, 0x6F, 0x67, 0x69, 0x65, 0x73, 0x00, 0x00, 0x00, 0x00, 0x76, 0x65, 0x72, 0x2E, 0x20, 0x31, 0x2E, 0x30},
	}
}

func tplinkParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+0x200 > len(data) {
		return nil, fmt.Errorf("insufficient data for TP-Link header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"version", "u32"}, {"fw_version", "u32"}, {"hw_id", "u32"}, {"hw_rev", "u32"},
		{"unk1", "u32"}, {"kernel_data_offset", "u32"}, {"kernel_load_address", "u32"},
		{"kernel_entry_point", "u32"}, {"kernel_data_length", "u32"},
	}
	parsed, err := Parse(hdr[0x40:], fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := 0x200
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, kernel load address: 0x%X, kernel entry point: 0x%X, header size: %d bytes", tplinkDescription, parsed["kernel_load_address"], parsed["kernel_entry_point"], headerSize),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// TP-Link RTOS firmware
// ============================================================================

const tplinkRTOSDescription = "TP-Link RTOS firmware"

func tplinkRTOSMagic() [][]byte {
	return [][]byte{{0x00, 0x14, 0x2F, 0xC0}}
}

func tplinkRTOSParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+0x200 > len(data) {
		return nil, fmt.Errorf("insufficient data for TP-Link RTOS header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"total_size", "u32"},
		{"model_number", "u32"}, {"hw_rev_major", "u32"}, {"hw_rev_minor", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        parsed["header_size"],
		Description: fmt.Sprintf("%s, model number: %X, hardware version: %X.%X, header size: %d bytes, total size: %d bytes", tplinkRTOSDescription, parsed["model_number"], parsed["hw_rev_major"], parsed["hw_rev_minor"], parsed["header_size"], parsed["total_size"]),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// DLOB firmware header
// ============================================================================

const dlobDescription = "DLOB firmware header"

func dlobMagic() [][]byte {
	return [][]byte{{0x5e, 0xa3, 0xa4, 0x17}}
}

func dlobParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+16 > len(data) {
		return nil, fmt.Errorf("insufficient data for DLOB header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"data_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	availableData := len(data) - offset
	headerSize := parsed["header_size"]
	dataSize := parsed["data_size"]
	if availableData < headerSize+dataSize {
		return nil, fmt.Errorf("DLOB size exceeds available data")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, data size: %d", dlobDescription, headerSize, dataSize),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// Arcadyan obfuscated LZMA
// ============================================================================

const arcadyanDescription = "Arcadyan obfuscated LZMA"

func arcadyanMagic() [][]byte {
	return [][]byte{{0x00, 0xD5, 0x08, 0x00}}
}

func arcadyanParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOffset = 0x68
	if offset < magicOffset {
		return nil, fmt.Errorf("arcadyan magic offset too small")
	}
	startOffset := offset - magicOffset
	return &SignatureResult{
		Offset:      startOffset,
		Description: arcadyanDescription,
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// CHK firmware header (Netgear)
// ============================================================================

const chkDescription = "CHK firmware header"

func chkMagic() [][]byte {
	return [][]byte{{0x2A, 0x23, 0x24, 0x5E}}
}

func chkParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+40 > len(data) {
		return nil, fmt.Errorf("insufficient data for CHK header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"reserved", "u32"},
		{"kernel_checksum", "u32"}, {"rootfs_checksum", "u32"},
		{"kernel_size", "u32"}, {"rootfs_size", "u32"},
		{"image_checksum", "u32"}, {"header_checksum", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	kernelSize := parsed["kernel_size"]
	rootfsSize := parsed["rootfs_size"]
	totalSize := headerSize + kernelSize + rootfsSize
	availableData := len(data) - offset
	if availableData < totalSize || totalSize <= headerSize {
		return nil, fmt.Errorf("invalid CHK sizes")
	}
	boardID := GetCString(hdr[40:])
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, board ID: %s, header size: %d bytes, data size: %d bytes", chkDescription, boardID, headerSize, kernelSize+rootfsSize),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// SEAMA firmware header
// ============================================================================

const seamaDescription = "SEAMA firmware header"

func seamaMagic() [][]byte {
	return [][]byte{
		{0x5E, 0xA3, 0xA4, 0x17, 0x00, 0x00},
		{0x17, 0xA4, 0xA3, 0x5E, 0x00, 0x00},
	}
}

func seamaParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+12 > len(data) {
		return nil, fmt.Errorf("insufficient data for SEAMA header")
	}
	hdr := data[offset:]
	endianness := "big"
	if hdr[0] == 0x17 {
		endianness = "little"
	}
	fields := []StructField{
		{"magic", "u32"}, {"meta_size", "u16"}, {"data_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, endianness)
	if err != nil {
		return nil, err
	}
	headerSize := 12 + parsed["meta_size"]
	dataSize := parsed["data_size"]
	totalSize := headerSize + dataSize
	if offset+totalSize > len(data) {
		return nil, fmt.Errorf("SEAMA size exceeds data")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, data size: %d bytes", seamaDescription, headerSize, dataSize),
		Confidence:  ConfidenceLow,
	}, nil
}

// ============================================================================
// CFE bootloader
// ============================================================================

const cfeDescription = "CFE bootloader"

func cfeMagic() [][]byte {
	return [][]byte{[]byte("CFE1CFE1")}
}

func cfeParser(_ []byte, offset int) (*SignatureResult, error) {
	const cfeMagicOffset = 28
	if offset < cfeMagicOffset {
		return nil, fmt.Errorf("CFE magic offset too small")
	}
	confidence := ConfidenceLow
	startOffset := offset - cfeMagicOffset
	if startOffset == 0 {
		confidence = ConfidenceMedium
	}
	return &SignatureResult{
		Offset:      startOffset,
		Description: cfeDescription,
		Confidence:  confidence,
	}, nil
}

// ============================================================================
// JBOOT ARM firmware
// ============================================================================

const jbootARMDescription = "JBOOT firmware header"

func jbootARMMagic() [][]byte {
	return [][]byte{
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x42, 0x48},
	}
}

func jbootARMParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOff = 48
	if offset < magicOff {
		return nil, fmt.Errorf("JBOOT ARM magic offset too small")
	}
	headerStart := offset - magicOff
	if headerStart+80 > len(data) {
		return nil, fmt.Errorf("insufficient data for JBOOT ARM header")
	}
	hdr := data[headerStart:]
	fields := []StructField{
		{"rom_id_size", "u32"}, {"rom_id", "u32"}, {"product_id", "u32"},
		{"func_id", "u32"}, {"erase_offset", "u32"}, {"erase_size", "u32"},
		{"data_offset", "u32"}, {"data_size", "u32"},
	}
	parsed, err := Parse(hdr[20:], fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := 80
	return &SignatureResult{
		Offset:      headerStart,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, ROM ID: %d, erase offset: 0x%X, erase size: 0x%X, data flash offset: 0x%X, data size: 0x%X", jbootARMDescription, headerSize, parsed["rom_id"], parsed["erase_offset"], parsed["erase_size"], parsed["data_offset"], parsed["data_size"]),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// JBOOT STAG header
// ============================================================================

const jbootSTAGDescription = "JBOOT STAG header"

func jbootSTAGMagic() [][]byte {
	return [][]byte{
		{0x04, 0x04, 0x24, 0x2B},
		{0xFF, 0x04, 0x24, 0x2B},
	}
}

func jbootSTAGParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+16 > len(data) {
		return nil, fmt.Errorf("insufficient data for JBOOT STAG header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"tag", "u32"}, {"header_size", "u16"}, {"flags", "u16"}, {"image_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	imageSize := parsed["image_size"]
	if offset+headerSize+imageSize >= len(data) {
		return nil, fmt.Errorf("JBOOT STAG size exceeds data")
	}
	imageType := "factory image"
	if parsed["flags"]&0x01 != 0 {
		imageType = "system upgrade image"
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, %s, header size: %d bytes, kernel data size: %d bytes", jbootSTAGDescription, imageType, headerSize, imageSize),
		Confidence:  ConfidenceLow,
	}, nil
}

// ============================================================================
// JBOOT SCH2 header
// ============================================================================

const jbootSCH2Description = "JBOOT SCH2 header"

func jbootSCH2Magic() [][]byte {
	return [][]byte{
		{0x24, 0x21, 0x00, 0x02},
		{0x24, 0x21, 0x01, 0x02},
		{0x24, 0x21, 0x02, 0x02},
		{0x24, 0x21, 0x03, 0x02},
	}
}

func jbootSCH2Parser(data []byte, offset int) (*SignatureResult, error) {
	if offset+32 > len(data) {
		return nil, fmt.Errorf("insufficient data for JBOOT SCH2 header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"kernel_size", "u32"},
		{"kernel_checksum", "u32"}, {"kernel_entry_point", "u32"},
		{"compression", "u8"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	compTypes := map[int]string{0: "none", 1: "gzip", 2: "bzip2", 3: "lzma"}
	comp := lookupMap(compTypes, parsed["compression"], "unknown")
	headerSize := parsed["header_size"]
	kernelSize := parsed["kernel_size"]
	totalSize := headerSize + kernelSize
	if offset+totalSize > len(data) {
		return nil, fmt.Errorf("JBOOT SCH2 extends beyond EOF")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, kernel size: %d bytes, kernel compression: %s, kernel entry point: 0x%X", jbootSCH2Description, headerSize, kernelSize, comp, parsed["kernel_entry_point"]),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// PackImg firmware header
// ============================================================================

const packimgDescription = "PackImg firmware header"

func packimgMagic() [][]byte {
	return [][]byte{[]byte("--PaCkImGs--")}
}

func packimgParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+24 > len(data) {
		return nil, fmt.Errorf("insufficient data for PackImg header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic1", "u32"}, {"magic2", "u32"}, {"magic3", "u32"},
		{"data_size", "u32"}, {"header_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	dataSize := parsed["data_size"]
	if len(data)-offset < headerSize+dataSize {
		return nil, fmt.Errorf("PackImg size exceeds data")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, data size: %d bytes", packimgDescription, headerSize, dataSize),
		Confidence:  ConfidenceLow,
	}, nil
}

// ============================================================================
// BIN firmware header
// ============================================================================

const binhdrDescription = "BIN firmware header"

func binhdrMagic() [][]byte {
	return [][]byte{[]byte("U2ND")}
}

func binhdrParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOff = 14
	if offset < magicOff {
		return nil, fmt.Errorf("BIN header magic offset too small")
	}
	startOffset := offset - magicOff
	if startOffset+24 > len(data) {
		return nil, fmt.Errorf("insufficient data for BIN header")
	}
	hdr := data[startOffset:]
	boardID := GetCString(hdr[:14])
	fields := []StructField{
		{"magic", "u32"}, {"hw_rev", "u16"}, {"fw_major", "u8"}, {"fw_minor", "u8"},
	}
	parsed, err := Parse(hdr[14:], fields, "big")
	if err != nil {
		return nil, err
	}
	return &SignatureResult{
		Offset:      startOffset,
		Description: fmt.Sprintf("%s, board ID: %s, hardware revision: %d, firmware version: %d.%d", binhdrDescription, boardID, parsed["hw_rev"], parsed["fw_major"], parsed["fw_minor"]),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// Autel obfuscated firmware
// ============================================================================

const autelDescription = "Autel obfuscated firmware"

func autelMagic() [][]byte {
	return [][]byte{[]byte("ECC0101\x00")}
}

func autelParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+16 > len(data) {
		return nil, fmt.Errorf("insufficient data for Autel header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u64"}, {"data_size", "u32"}, {"header_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	dataSize := parsed["data_size"]
	totalSize := headerSize + dataSize
	if offset+totalSize > len(data) {
		return nil, fmt.Errorf("Autel size exceeds data")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, data size: %d, total size: %d", autelDescription, headerSize, dataSize, totalSize),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// D-Link TLV firmware
// ============================================================================

const dlinkTLVDescription = "D-Link TLV firmware"

func dlinkTLVMagic() [][]byte {
	return [][]byte{{0x64, 0x80, 0x19, 0x40}}
}

func dlinkTLVParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+32 > len(data) {
		return nil, fmt.Errorf("insufficient data for D-Link TLV header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"data_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	dataSize := parsed["data_size"]
	totalSize := headerSize + dataSize
	if offset+totalSize > len(data) {
		return nil, fmt.Errorf("D-Link TLV size exceeds data")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, data size: %d bytes", dlinkTLVDescription, headerSize, dataSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// DLKE encrypted firmware
// ============================================================================

const dlkeDescription = "DLK encrypted firmware"

func dlkeMagic() [][]byte {
	return [][]byte{
		[]byte("DLK6E8202001"),
		[]byte("DLK6E6110002"),
	}
}

func dlkeParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+80 > len(data) {
		return nil, fmt.Errorf("insufficient data for DLKE header")
	}
	// Parse first JBOOT-like header
	hdr1 := data[offset:]
	fields := []StructField{
		{"rom_id_size", "u32"}, {"rom_id", "u32"}, {"product_id", "u32"},
		{"func_id", "u32"}, {"erase_offset", "u32"}, {"erase_size", "u32"},
		{"data_offset", "u32"}, {"data_size", "u32"},
	}
	parsed1, err := Parse(hdr1[20:], fields, "big")
	if err != nil {
		return nil, err
	}
	hdr1Size := 80
	sig1DataSize := parsed1["data_size"]

	nextOffset := offset + hdr1Size + sig1DataSize
	if nextOffset+80 > len(data) {
		return nil, fmt.Errorf("DLKE second header beyond EOF")
	}
	hdr2 := data[nextOffset:]
	parsed2, err := Parse(hdr2[20:], fields, "big")
	if err != nil {
		return nil, err
	}
	hdr2Size := 80
	cryptDataSize := parsed2["data_size"]

	totalSize := hdr1Size + sig1DataSize + hdr2Size + cryptDataSize
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, signature size: %d bytes, encrypted data size: %d bytes", dlkeDescription, sig1DataSize, cryptDataSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// SHRS encrypted firmware
// ============================================================================

const shrsDescription = "SHRS encrypted firmware"

func shrsMagic() [][]byte {
	return [][]byte{[]byte("SHRS")}
}

func shrsParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+32 > len(data) {
		return nil, fmt.Errorf("insufficient data for SHRS header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"data_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	dataSize := parsed["data_size"]
	totalSize := headerSize + dataSize
	if offset+totalSize > len(data) {
		return nil, fmt.Errorf("SHRS size exceeds data")
	}
	// Extract IV (16 bytes after the fixed header)
	ivStr := ""
	if headerSize >= 28 && offset+28 <= len(data) {
		iv := hdr[12:28]
		ivStr = hex.EncodeToString(iv)
	}
	confidence := ConfidenceLow
	if offset == 0 {
		confidence = ConfidenceMedium
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, header size: %d bytes, encrypted data size: %d bytes, IV: %s", shrsDescription, headerSize, dataSize, ivStr),
		Confidence:  confidence,
	}, nil
}

// ============================================================================
// DKBS firmware header
// ============================================================================

const dkbsDescription = "DKBS firmware header"

func dkbsMagic() [][]byte {
	return [][]byte{[]byte("_dkbs_")}
}

func dkbsParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOff = 7
	if offset < magicOff {
		return nil, fmt.Errorf("DKBS magic offset too small")
	}
	startOffset := offset - magicOff
	if startOffset+64 > len(data) {
		return nil, fmt.Errorf("insufficient data for DKBS header")
	}
	hdr := data[startOffset:]
	fields := []StructField{
		{"version", "u32"}, {"magic", "u32"}, {"padding", "u32"},
		{"header_size", "u32"}, {"data_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	dataSize := parsed["data_size"]
	availableData := len(data) - startOffset
	if availableData < headerSize+dataSize {
		return nil, fmt.Errorf("DKBS size exceeds data")
	}
	confidence := ConfidenceMedium
	if startOffset == 0 {
		confidence = ConfidenceHigh
	}
	boardID := GetCString(hdr[24:])
	return &SignatureResult{
		Offset:      startOffset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, board ID: %s, header size: %d bytes, data size: %d", dkbsDescription, boardID, headerSize, dataSize),
		Confidence:  confidence,
	}, nil
}

// ============================================================================
// DMS firmware image
// ============================================================================

const dmsDescription = "DMS firmware image"

func dmsMagic() [][]byte {
	return [][]byte{[]byte("0><1")}
}

func dmsParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOff = 4
	const minSize = 0x100
	if offset < magicOff {
		return nil, fmt.Errorf("DMS magic offset too small")
	}
	startOffset := offset - magicOff
	if startOffset+minSize > len(data) {
		return nil, fmt.Errorf("insufficient data for DMS header")
	}

	// DMS images have byte-swapped u16 pairs
	dmsData := make([]byte, minSize)
	copy(dmsData, data[startOffset:startOffset+minSize])
	for i := 0; i+1 < len(dmsData); i += 2 {
		dmsData[i], dmsData[i+1] = dmsData[i+1], dmsData[i]
	}

	fields := []StructField{
		{"magic", "u32"}, {"unk1", "u32"}, {"image_size", "u32"},
	}
	parsed, err := Parse(dmsData, fields, "big")
	if err != nil {
		return nil, err
	}
	imageSize := parsed["image_size"]
	if imageSize <= 0 || startOffset+imageSize > len(data) {
		return nil, fmt.Errorf("invalid DMS image size")
	}
	return &SignatureResult{
		Offset:      startOffset,
		Size:        imageSize,
		Description: fmt.Sprintf("%s, total size: %d bytes", dmsDescription, imageSize),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// Known encrypted firmware (encfw)
// ============================================================================

const encfwDescription = "Known encrypted firmware"

func encfwKnownFirmware() map[string]string {
	return map[string]string{
		"\xdf\x8c\x39\x0d": "D-Link DIR-822 rev C",
		"\x35\x66\x6f\x68": "D-Link DAP-1665",
		"\xf5\x2a\xa0\xb4": "D-Link DIR-842 rev C",
		"\xe3\x13\x00\x5b": "D-Link DIR-850 rev A",
		"\x0a\x14\xe4\x24": "D-Link DIR-850 rev B",
	}
}

func encfwMagic() [][]byte {
	var magics [][]byte
	for k := range encfwKnownFirmware() {
		magics = append(magics, []byte(k))
	}
	return magics
}

func encfwParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+4 > len(data) {
		return nil, fmt.Errorf("insufficient data for encfw")
	}
	key := string(data[offset : offset+4])
	known := encfwKnownFirmware()
	model, ok := known[key]
	if !ok {
		return nil, fmt.Errorf("unknown encfw magic")
	}
	confidence := ConfidenceMedium
	if offset != 0 {
		confidence = ConfidenceLow
	}
	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, %s", encfwDescription, model),
		Confidence:  confidence,
	}, nil
}

// ============================================================================
// D-Link encrpted_img
// ============================================================================

const encrptedImgDescription = "D-Link Encrpted Image"

func encrptedImgMagic() [][]byte {
	return [][]byte{[]byte("encrpted_img")}
}

func encrptedImgParser(_ []byte, offset int) (*SignatureResult, error) {
	confidence := ConfidenceMedium
	if offset != 0 {
		confidence = ConfidenceLow
	}
	return &SignatureResult{
		Offset:      offset,
		Description: encrptedImgDescription,
		Confidence:  confidence,
	}, nil
}

// ============================================================================
// Android boot image
// ============================================================================

const androidBootimgDescription = "Android boot image"

func androidBootimgMagic() [][]byte {
	return [][]byte{[]byte("ANDROID!")}
}

func androidBootimgParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+40 > len(data) {
		return nil, fmt.Errorf("insufficient data for Android boot image header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u64"}, {"kernel_size", "u32"}, {"kernel_load_addr", "u32"},
		{"ramdisk_size", "u32"}, {"ramdisk_load_addr", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	confidence := ConfidenceLow
	if offset == 0 {
		confidence = ConfidenceMedium
	}
	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, kernel size: %d bytes, kernel load address: 0x%X, ramdisk size: %d bytes, ramdisk load address: 0x%X", androidBootimgDescription, parsed["kernel_size"], parsed["kernel_load_addr"], parsed["ramdisk_size"], parsed["ramdisk_load_addr"]),
		Confidence:  confidence,
	}, nil
}

// ============================================================================
// Android sparse image
// ============================================================================

const androidSparseDescription = "Android sparse image"

func androidSparseMagic() [][]byte {
	return [][]byte{{0x3A, 0xFF, 0x26, 0xED}}
}

func androidSparseParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+28 > len(data) {
		return nil, fmt.Errorf("insufficient data for Android sparse header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"major_version", "u16"}, {"minor_version", "u16"},
		{"header_size", "u16"}, {"chunk_header_size", "u16"},
		{"block_size", "u32"}, {"total_blocks", "u32"},
		{"total_chunks", "u32"}, {"checksum", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, version %d.%d, header size: %d, block size: %d, chunk count: %d", androidSparseDescription, parsed["major_version"], parsed["minor_version"], headerSize, parsed["block_size"], parsed["total_chunks"]),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// Windows CE binary image
// ============================================================================

const winceDescription = "Windows CE binary image"

func winceMagic() [][]byte {
	return [][]byte{[]byte("B000FF\n")}
}

func winceParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+16 > len(data) {
		return nil, fmt.Errorf("insufficient data for WinCE header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic1", "u32"}, {"magic2", "u32"},
		{"base_address", "u32"}, {"image_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        parsed["image_size"],
		Description: fmt.Sprintf("%s, base address: 0x%X, image size: %d bytes", winceDescription, parsed["base_address"], parsed["image_size"]),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// D-Link MH01 firmware
// ============================================================================

const mh01Description = "D-Link MH01 firmware image"

func mh01Magic() [][]byte {
	return [][]byte{[]byte("MH01")}
}

func mh01Parser(data []byte, offset int) (*SignatureResult, error) {
	if offset+32 > len(data) {
		return nil, fmt.Errorf("insufficient data for MH01 header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"total_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	totalSize := parsed["total_size"]
	if offset+totalSize > len(data) {
		return nil, fmt.Errorf("MH01 size exceeds data")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, total size: %d bytes", mh01Description, totalSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// CSman DAT
// ============================================================================

const csmanDescription = "CSman DAT file"

func csmanMagic() [][]byte {
	return [][]byte{[]byte("SC"), []byte("CS")}
}

func csmanParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+8 > len(data) {
		return nil, fmt.Errorf("insufficient data for CSman header")
	}
	hdr := data[offset:]
	endianness := "big"
	if hdr[0] == 'C' && hdr[1] == 'S' {
		endianness = "little"
	}
	fields := []StructField{
		{"magic", "u16"}, {"count", "u16"}, {"size", "u32"},
	}
	parsed, err := Parse(hdr, fields, endianness)
	if err != nil {
		return nil, err
	}
	totalSize := parsed["size"]
	if totalSize <= 0 || offset+totalSize > len(data) {
		return nil, fmt.Errorf("invalid CSman size")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, total size: %d bytes", csmanDescription, totalSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// DXBC (DirectX shader bytecode)
// ============================================================================

const dxbcDescription = "DirectX shader bytecode"

func dxbcMagic() [][]byte {
	return [][]byte{[]byte("DXBC")}
}

func dxbcParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+32 > len(data) {
		return nil, fmt.Errorf("insufficient data for DXBC header")
	}
	hdr := data[offset:]
	// DXBC header: 4-byte magic, 16-byte hash, 4-byte version, 4-byte total_size, 4-byte chunk_count
	fields := []StructField{
		{"magic", "u32"}, {"hash1", "u32"}, {"hash2", "u32"},
		{"hash3", "u32"}, {"hash4", "u32"},
		{"version", "u32"}, {"total_size", "u32"}, {"chunk_count", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	totalSize := parsed["total_size"]
	if totalSize <= 32 || offset+totalSize > len(data) {
		return nil, fmt.Errorf("invalid DXBC size")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, total size: %d bytes", dxbcDescription, totalSize),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// DTB (Device Tree Blob)
// ============================================================================

const dtbDescription = "Device tree blob (DTB)"

func dtbMagic() [][]byte {
	return [][]byte{{0xD0, 0x0D, 0xFE, 0xED}}
}

func dtbParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+40 > len(data) {
		return nil, fmt.Errorf("insufficient data for DTB header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"total_size", "u32"}, {"struct_offset", "u32"},
		{"strings_offset", "u32"}, {"mem_reserve_offset", "u32"},
		{"version", "u32"}, {"last_comp_version", "u32"},
		{"cpu_id", "u32"}, {"strings_size", "u32"}, {"struct_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	totalSize := parsed["total_size"]
	structEnd := offset + parsed["struct_offset"] + parsed["struct_size"]
	stringsEnd := offset + parsed["strings_offset"] + parsed["strings_size"]
	if structEnd > len(data) || stringsEnd > len(data) {
		return nil, fmt.Errorf("DTB offsets exceed data")
	}
	if totalSize <= 0 || offset+totalSize > len(data) {
		return nil, fmt.Errorf("invalid DTB total size")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        totalSize,
		Description: fmt.Sprintf("%s, version: %d, CPU ID: %d, total size: %d bytes", dtbDescription, parsed["version"], parsed["cpu_id"], totalSize),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// Matter OTA firmware
// ============================================================================

const matterOTADescription = "Matter OTA firmware"

func matterOTAMagic() [][]byte {
	return [][]byte{{0x1e, 0xf1, 0xee, 0x1b}}
}

func matterOTAParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+32 > len(data) {
		return nil, fmt.Errorf("insufficient data for Matter OTA header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"total_size", "u32"}, {"header_size", "u32"},
		{"header_tlv_size", "u32"}, {"vendor_id", "u16"}, {"product_id", "u16"},
		{"version", "u32"}, {"min_version", "u32"},
		{"max_version", "u32"}, {"payload_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	headerSize := parsed["header_size"]
	return &SignatureResult{
		Offset:      offset,
		Size:        headerSize,
		Description: fmt.Sprintf("%s, total size: %d bytes, header size: %d bytes, vendor id: 0x%x, product id: 0x%x, version: %d, payload size: %d bytes", matterOTADescription, parsed["total_size"], headerSize, parsed["vendor_id"], parsed["product_id"], parsed["version"], parsed["payload_size"]),
		Confidence:  ConfidenceHigh,
	}, nil
}

// ============================================================================
// RTK firmware header
// ============================================================================

const rtkDescription = "RTK firmware header"

func rtkMagic() [][]byte {
	return [][]byte{[]byte("RTK0")}
}

func rtkParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+16 > len(data) {
		return nil, fmt.Errorf("insufficient data for RTK header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"header_size", "u32"}, {"image_size", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	availableData := len(data) - offset
	if parsed["image_size"] != availableData {
		return nil, fmt.Errorf("RTK image size mismatch")
	}
	return &SignatureResult{
		Offset:      offset,
		Size:        parsed["header_size"],
		Description: fmt.Sprintf("%s, header size: %d bytes, image size: %d", rtkDescription, parsed["header_size"], parsed["image_size"]),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// DPAPI blob data
// ============================================================================

const dpapiDescription = "DPAPI blob data"

func dpapiMagic() [][]byte {
	return [][]byte{
		{0x01, 0x00, 0x00, 0x00, 0xD0, 0x8c, 0x9d, 0xdf, 0x01, 0x15, 0xd1, 0x11, 0x8c, 0x7a, 0x00, 0xc0, 0x4f, 0xc2, 0x97, 0xeb},
	}
}

func dpapiParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+44 > len(data) {
		return nil, fmt.Errorf("insufficient data for DPAPI blob header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"version", "u32"}, {"provider_guid1", "u32"}, {"provider_guid2", "u32"},
		{"provider_guid3", "u32"}, {"provider_guid4", "u32"},
		{"mk_version", "u32"}, {"mk_guid1", "u32"}, {"mk_guid2", "u32"},
		{"mk_guid3", "u32"}, {"mk_guid4", "u32"}, {"flags", "u32"},
	}
	parsed, err := Parse(hdr, fields, "little")
	if err != nil {
		return nil, err
	}
	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, version: %d, flags: %d", dpapiDescription, parsed["version"], parsed["flags"]),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// QEMU QCOW Image
// ============================================================================

const qcowDescription = "QEMU QCOW Image"

func qcowMagic() [][]byte {
	return [][]byte{[]byte("QFI\xFB")}
}

func qcowParser(data []byte, offset int) (*SignatureResult, error) {
	if offset+24 > len(data) {
		return nil, fmt.Errorf("insufficient data for QCOW header")
	}
	hdr := data[offset:]
	fields := []StructField{
		{"magic", "u32"}, {"version", "u32"}, {"backing_file_offset", "u64"},
		{"backing_file_size", "u32"}, {"cluster_bits", "u32"},
		{"disk_size", "u64"}, {"encryption_method", "u32"},
	}
	parsed, err := Parse(hdr, fields, "big")
	if err != nil {
		return nil, err
	}
	encMap := map[int]string{0: "none", 1: "AES", 2: "LUKS"}
	enc := lookupMap(encMap, parsed["encryption_method"], "unknown")
	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, version: %d, storage media size: 0x%x bytes, cluster block size: 0x%x bytes, encryption method: %s", qcowDescription, parsed["version"], parsed["disk_size"], 1<<parsed["cluster_bits"], enc),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ============================================================================
// Helpers
// ============================================================================

func lookupMap(m map[int]string, key int, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return fallback
}
