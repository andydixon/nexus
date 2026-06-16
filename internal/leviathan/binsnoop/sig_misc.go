package binsnoop

import (
	"bytes"
	"fmt"
	"strings"
)

// ---- Copyright ----

const DescriptionCopyright = "Copyright text"

// copyrightMagic returns magic bytes for copyright strings.
func copyrightMagic() [][]byte {
	return [][]byte{
		[]byte("copyright"),
		[]byte("Copyright"),
		[]byte("COPYRIGHT"),
	}
}

// copyrightParser validates copyright text signatures.
func copyrightParser(data []byte, offset int) (*SignatureResult, error) {
	const magicSize = 9

	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionCopyright,
		Confidence:  ConfidenceHigh,
	}

	copyrightStr := GetCString(data[offset:])
	if len(copyrightStr) <= magicSize {
		return nil, fmt.Errorf("copyright string too short")
	}

	result.Size = len(copyrightStr)
	truncated := copyrightStr
	if len(truncated) > 100 {
		truncated = truncated[:100]
	}
	result.Description = fmt.Sprintf("%s: \"%s\"", result.Description, truncated)
	return result, nil
}

// ---- VxWorks ----

const (
	DescriptionVxWorksSymTab     = "VxWorks symbol table"
	DescriptionWINDKernelVersion = "VxWorks WIND kernel version"
)

// windKernelMagic returns magic bytes for VxWorks WIND kernel version.
func windKernelMagic() [][]byte {
	return [][]byte{
		[]byte("WIND version "),
	}
}

// vxworksSymtabMagic returns magic bytes for VxWorks symbol tables.
func vxworksSymtabMagic() [][]byte {
	return [][]byte{
		{0x00, 0x00, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x00, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x07, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0x09, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}
}

// windKernelParser validates VxWorks WIND kernel version signatures.
func windKernelParser(data []byte, offset int) (*SignatureResult, error) {
	const magicSize = 13

	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionWINDKernelVersion,
		Confidence:  ConfidenceLow,
	}

	versionOffset := offset + magicSize
	if versionOffset >= len(data) {
		return nil, fmt.Errorf("WIND kernel version offset out of bounds")
	}

	versionStr := GetCString(data[versionOffset:])
	if versionStr == "" {
		return nil, fmt.Errorf("WIND kernel version string empty")
	}

	result.Size = magicSize + len(versionStr)
	result.Description = fmt.Sprintf("%s %s", result.Description, versionStr)
	return result, nil
}

// vxworksSymtabParser validates VxWorks symbol table signatures.
func vxworksSymtabParser(data []byte, offset int) (*SignatureResult, error) {
	const symtabMagicOffset = 8

	result := &SignatureResult{
		Description: DescriptionVxWorksSymTab,
		Confidence:  ConfidenceHigh,
	}

	if offset < symtabMagicOffset {
		return nil, fmt.Errorf("VxWorks symbol table: offset too small")
	}

	symtabStart := offset - symtabMagicOffset

	// Determine endianness from the type field at offset 9 from symtab start
	endianness := "little"
	typeFieldOffset := symtabStart + 9
	if typeFieldOffset < len(data) && data[typeFieldOffset] == 0 {
		endianness = "big"
	}

	// Validate the first symbol table entry
	symtabFields := []StructField{
		{"name_ptr", "u32"},
		{"value_ptr", "u32"},
		{"type", "u32"},
		{"group", "u32"},
	}
	entrySize := StructSize(symtabFields)

	allowedTypes := map[int]string{
		0x500: "function",
		0x700: "initialized data",
		0x900: "uninitialized data",
	}

	if symtabStart+entrySize > len(data) {
		return nil, fmt.Errorf("VxWorks symbol table: insufficient data")
	}

	entry, err := Parse(data[symtabStart:], symtabFields, endianness)
	if err != nil {
		return nil, fmt.Errorf("VxWorks symbol table: parse failed")
	}

	if _, ok := allowedTypes[entry["type"]]; !ok {
		return nil, fmt.Errorf("VxWorks symbol table: invalid type %#x", entry["type"])
	}
	if entry["name_ptr"] == 0 || entry["value_ptr"] == 0 {
		return nil, fmt.Errorf("VxWorks symbol table: null pointers")
	}

	result.Offset = symtabStart
	result.Size = entrySize
	result.Description = fmt.Sprintf("%s, total size: %d bytes", result.Description, result.Size)
	return result, nil
}

// ---- eCos ----

const DescriptionECosExceptionHandler = "eCos kernel exception handler"

// ecosExceptionHandlerMagic returns magic bytes for eCos exception handlers (MIPS).
func ecosExceptionHandlerMagic() [][]byte {
	return [][]byte{
		{0x00, 0x68, 0x1A, 0x40, 0x00, 0x00, 0x00, 0x00, 0x7F, 0x00, 0x5A, 0x33},
		{0x00, 0x68, 0x1A, 0x40, 0x7F, 0x00, 0x5A, 0x33},
		{0x40, 0x1A, 0x68, 0x00, 0x00, 0x00, 0x00, 0x00, 0x33, 0x5A, 0x00, 0x7F},
		{0x40, 0x1A, 0x68, 0x00, 0x33, 0x5A, 0x00, 0x7F},
	}
}

// ecosExceptionHandlerParser validates eCos exception handler signatures.
func ecosExceptionHandlerParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionECosExceptionHandler,
		Confidence:  ConfidenceLow,
	}

	endianness := "big"
	if offset < len(data) && data[offset] == 0 {
		endianness = "little"
	}

	result.Description = fmt.Sprintf("%s, MIPS %s endian", result.Description, endianness)
	return result, nil
}

// ---- Linux ----

const (
	DescriptionLinuxARMzImage      = "Linux ARM boot executable zImage"
	DescriptionLinuxBootImage      = "Linux kernel boot image"
	DescriptionLinuxKernelVersion  = "Linux kernel version"
	DescriptionLinuxARM64BootImage = "Linux kernel ARM64 boot image"
)

// linuxBootImageMagic returns magic bytes for Linux boot images.
func linuxBootImageMagic() [][]byte {
	return [][]byte{
		{0xb8, 0xc0, 0x07, 0x8e, 0xd8, 0xb8, 0x00, 0x90, 0x8e, 0xc0, 0xb9, 0x00, 0x01, 0x29, 0xf6, 0x29},
	}
}

// linuxKernelVersionMagic returns magic bytes for Linux kernel version strings.
func linuxKernelVersionMagic() [][]byte {
	return [][]byte{
		[]byte("Linux version "),
	}
}

// linuxARM64BootImageMagic returns magic bytes for Linux ARM64 boot images.
func linuxARM64BootImageMagic() [][]byte {
	return [][]byte{
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x41, 0x52, 0x4D, 0x64}, // "\x00\x00\x00\x00\x00\x00\x00\x00ARMd"
	}
}

// linuxARMzImageMagic returns magic bytes for Linux ARM zImage.
func linuxARMzImageMagic() [][]byte {
	return [][]byte{
		{0x18, 0x28, 0x6F, 0x01},
		{0x01, 0x6F, 0x28, 0x18},
	}
}

// linuxARMzImageParser validates Linux ARM zImage signatures.
func linuxARMzImageParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOffset = 36
	const nopLE = 0xE1A00000
	const nopBE = 0x0000A0E1

	result := &SignatureResult{
		Confidence:  ConfidenceMedium,
		Description: DescriptionLinuxARMzImage,
	}

	if offset < magicOffset {
		return nil, fmt.Errorf("ARM zImage: offset too small")
	}

	result.Offset = offset - magicOffset
	zimageData := data[result.Offset:]

	// Parse 8 NOP instructions at the start
	nopFields := []StructField{
		{"nop1", "u32"}, {"nop2", "u32"}, {"nop3", "u32"}, {"nop4", "u32"},
		{"nop5", "u32"}, {"nop6", "u32"}, {"nop7", "u32"}, {"nop8", "u32"},
	}

	if len(zimageData) < StructSize(nopFields) {
		return nil, fmt.Errorf("ARM zImage: insufficient data")
	}

	nops, err := Parse(zimageData, nopFields, "little")
	if err != nil {
		return nil, fmt.Errorf("ARM zImage: parse failed")
	}

	nop1 := nops["nop1"]
	if nop1 != nops["nop2"] || nop1 != nops["nop3"] || nop1 != nops["nop4"] ||
		nop1 != nops["nop5"] || nop1 != nops["nop6"] || nop1 != nops["nop7"] || nop1 != nops["nop8"] {
		return nil, fmt.Errorf("ARM zImage: NOP instructions not uniform")
	}

	var endianness string
	if nop1 == nopLE {
		endianness = "little"
	} else if nop1 == nopBE {
		endianness = "big"
	} else {
		return nil, fmt.Errorf("ARM zImage: unrecognized NOP value")
	}

	result.Description = fmt.Sprintf("%s, %s endian", result.Description, endianness)
	return result, nil
}

// linuxARM64BootImageParser validates Linux ARM64 boot image signatures.
func linuxARM64BootImageParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOffset = 0x30
	const flagsEndianMask = 1
	const bigEndianFlag = 1

	result := &SignatureResult{
		Confidence:  ConfidenceMedium,
		Description: DescriptionLinuxARM64BootImage,
	}

	if offset < magicOffset {
		return nil, fmt.Errorf("ARM64 boot image: offset too small")
	}

	result.Offset = offset - magicOffset
	imgData := data[result.Offset:]

	bootImgFields := []StructField{
		{"code0", "u32"},
		{"code1", "u32"},
		{"image_load_offset", "u64"},
		{"image_size", "u64"},
		{"flags", "u64"},
		{"reserved1", "u64"},
		{"reserved2", "u64"},
		{"reserved3", "u64"},
		{"magic", "u32"},
		{"pe_offset", "u32"},
	}

	structSize := StructSize(bootImgFields)
	if len(imgData) < structSize {
		return nil, fmt.Errorf("ARM64 boot image: insufficient data")
	}

	imgHeader, err := Parse(imgData, bootImgFields, "little")
	if err != nil {
		return nil, fmt.Errorf("ARM64 boot image: parse failed")
	}

	if imgHeader["reserved1"] != 0 || imgHeader["reserved2"] != 0 || imgHeader["reserved3"] != 0 {
		return nil, fmt.Errorf("ARM64 boot image: reserved fields not zero")
	}

	peOffset := imgHeader["pe_offset"]
	peEnd := peOffset + 2
	if peEnd > len(imgData) {
		return nil, fmt.Errorf("ARM64 boot image: PE offset out of bounds")
	}
	if imgData[peOffset] != 'P' || imgData[peOffset+1] != 'E' {
		return nil, fmt.Errorf("ARM64 boot image: PE signature not found")
	}

	flags := imgHeader["flags"]
	// Reserved bits are the upper 60 bits (mask 0xFFFFFFFFFFFFFFF0)
	if (uint64(flags) & 0xFFFFFFFFFFFFFFF0) != 0 {
		return nil, fmt.Errorf("ARM64 boot image: reserved flag bits set")
	}

	endianness := "little"
	if (flags & flagsEndianMask) == bigEndianFlag {
		endianness = "big"
	}

	imageSize := imgHeader["image_size"]
	result.Size = structSize
	result.Description = fmt.Sprintf("%s, %s endian, effective image size: %d bytes",
		result.Description, endianness, imageSize)
	return result, nil
}

// linuxBootImageParser validates Linux boot image signatures.
func linuxBootImageParser(data []byte, offset int) (*SignatureResult, error) {
	const hdrsOffset = 514
	const hdrsExpected = "!HdrS"

	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionLinuxBootImage,
	}

	hdrsStart := offset + hdrsOffset
	hdrsEnd := hdrsStart + len(hdrsExpected)

	if hdrsEnd > len(data) {
		return nil, fmt.Errorf("Linux boot image: insufficient data for HdrS check")
	}

	actual := string(data[hdrsStart:hdrsEnd])
	if actual != hdrsExpected {
		return nil, fmt.Errorf("Linux boot image: HdrS signature not found")
	}

	return result, nil
}

// linuxKernelVersionParser validates Linux kernel version string signatures.
func linuxKernelVersionParser(data []byte, offset int) (*SignatureResult, error) {
	const periodByte byte = 0x2E
	const periodOffset1 = 15
	const periodOffset2 = 17
	const periodOffset3 = 18
	const minFileSize = 100 * 1024
	const minVersionStrLen = 75
	const gccVersionStr = "gcc "

	result := &SignatureResult{
		Offset:     offset,
		Confidence: ConfidenceLow,
	}

	fileSize := len(data)
	if fileSize <= minFileSize {
		return nil, fmt.Errorf("Linux kernel version: file too small")
	}

	kvString := GetCString(data[offset:])
	if len(kvString) <= minVersionStrLen {
		return nil, fmt.Errorf("Linux kernel version: string too short")
	}

	if !strings.Contains(kvString, gccVersionStr) {
		return nil, fmt.Errorf("Linux kernel version: no gcc version string")
	}

	if !strings.Contains(kvString, "@") {
		return nil, fmt.Errorf("Linux kernel version: no ampersand")
	}

	if !strings.HasSuffix(kvString, "\n") {
		return nil, fmt.Errorf("Linux kernel version: does not end with newline")
	}

	kvBytes := []byte(kvString)
	if periodOffset1 >= len(kvBytes) || periodOffset3 >= len(kvBytes) {
		return nil, fmt.Errorf("Linux kernel version: string too short for period checks")
	}

	if kvBytes[periodOffset1] != periodByte || (kvBytes[periodOffset2] != periodByte && kvBytes[periodOffset3] != periodByte) {
		return nil, fmt.Errorf("Linux kernel version: periods not at expected offsets")
	}

	// Search for Linux kernel symbol table
	symtabMagic := []byte("\x000\x001\x002\x003\x004\x005\x006\x007\x008\x009\x00")
	symtabPresent := bytes.Count(data, symtabMagic) == 1

	if symtabPresent {
		result.Offset = 0
		result.Size = len(data)
	} else {
		result.Size = len(kvString)
		result.ExtractionDeclined = true
	}

	result.Description = fmt.Sprintf("%s, has symbol table: %t",
		strings.TrimSpace(kvString), symtabPresent)
	return result, nil
}

// ---- EFI GPT ----

const DescriptionEFIGPT = "EFI Global Partition Table"

// efigptMagic returns magic bytes for EFI GPT headers.
func efigptMagic() [][]byte {
	return [][]byte{
		{0x55, 0xAA, 0x45, 0x46, 0x49, 0x20, 0x50, 0x41, 0x52, 0x54}, // "\x55\xAAEFI PART"
	}
}

func lbaToOffset(lba int) int {
	return lba * 512
}

// efigptParser validates EFI GPT header signatures.
func efigptParser(data []byte, offset int) (*SignatureResult, error) {
	const magicOffset = 0x01FE
	const blockSize = 512
	const expectedRevision = 0x00010000

	result := &SignatureResult{
		Description: DescriptionEFIGPT,
		Confidence:  ConfidenceHigh,
	}

	availableData := len(data) - offset
	if offset < magicOffset {
		return nil, fmt.Errorf("EFI GPT: offset too small")
	}

	result.Offset = offset - magicOffset
	efiData := data[result.Offset:]

	if len(efiData) <= blockSize {
		return nil, fmt.Errorf("EFI GPT: insufficient data for GPT structure")
	}

	gptData := efiData[blockSize:]

	gptFields := []StructField{
		{"magic", "u64"},
		{"revision", "u32"},
		{"header_size", "u32"},
		{"header_crc", "u32"},
		{"reserved", "u32"},
		{"my_lba", "u64"},
		{"alternate_lba", "u64"},
		{"first_usable_lba", "u64"},
		{"last_usable_lba", "u64"},
		{"disk_guid_p1", "u64"},
		{"disk_guid_p2", "u64"},
		{"partition_entry_lba", "u64"},
		{"partition_entry_count", "u32"},
		{"partition_entry_size", "u32"},
		{"partition_entries_crc", "u32"},
	}

	if len(gptData) < StructSize(gptFields) {
		return nil, fmt.Errorf("EFI GPT: insufficient GPT header data")
	}

	gptHeader, err := Parse(gptData, gptFields, "little")
	if err != nil {
		return nil, fmt.Errorf("EFI GPT: parse failed")
	}

	if gptHeader["reserved"] != 0 {
		return nil, fmt.Errorf("EFI GPT: reserved field not zero")
	}
	if gptHeader["revision"] != expectedRevision {
		return nil, fmt.Errorf("EFI GPT: unexpected revision")
	}

	partEntriesStart := lbaToOffset(gptHeader["partition_entry_lba"])
	partEntriesEnd := partEntriesStart + (gptHeader["partition_entry_count"] * gptHeader["partition_entry_size"])

	if partEntriesEnd > len(efiData) || partEntriesStart >= partEntriesEnd {
		return nil, fmt.Errorf("EFI GPT: partition entries out of bounds")
	}

	partEntriesData := efiData[partEntriesStart:partEntriesEnd]

	// Validate partition entries CRC
	if CRC32(partEntriesData) != uint32(gptHeader["partition_entries_crc"]) {
		return nil, fmt.Errorf("EFI GPT: partition entries CRC mismatch")
	}

	// Parse partition entries to find total size
	entryFields := []StructField{
		{"type_guid_p1", "u64"},
		{"type_guid_p2", "u64"},
		{"partition_guid_p1", "u64"},
		{"partition_guid_p2", "u64"},
		{"starting_lba", "u64"},
		{"ending_lba", "u64"},
		{"attributes", "u64"},
	}

	entrySize := gptHeader["partition_entry_size"]
	totalSize := 0

	for i := 0; i+entrySize <= len(partEntriesData); i += entrySize {
		entry, err := Parse(partEntriesData[i:], entryFields, "little")
		if err != nil {
			continue
		}
		if entry["type_guid_p1"] == 0 && entry["type_guid_p2"] == 0 {
			continue
		}
		startOff := lbaToOffset(entry["starting_lba"])
		endOff := lbaToOffset(entry["ending_lba"])
		if startOff < endOff && endOff > totalSize {
			totalSize = endOff
		}
	}

	if totalSize == 0 {
		return nil, fmt.Errorf("EFI GPT: no valid partitions found")
	}

	if totalSize > availableData {
		result.Size = availableData
	} else {
		result.Size = totalSize
	}
	result.Description = fmt.Sprintf("%s, total size: %d", result.Description, result.Size)
	return result, nil
}

// ---- MBR ----

const DescriptionMBR = "DOS Master Boot Record"
const MBRMagicOffset = 0x01FE

// mbrMagic returns magic bytes for MBR signatures.
func mbrMagic() [][]byte {
	return [][]byte{
		{0x55, 0xAA},
	}
}

// mbrParser validates MBR header signatures.
func mbrParser(data []byte, offset int) (*SignatureResult, error) {
	const blockSize = 512
	const minImageSize = blockSize * 2
	const partitionCount = 4
	const partitionTableOffset = 446

	result := &SignatureResult{
		Description: DescriptionMBR,
		Confidence:  ConfidenceMedium,
	}

	if offset != MBRMagicOffset {
		return nil, fmt.Errorf("MBR: magic not at expected offset")
	}

	result.Offset = offset - MBRMagicOffset

	partEntryFields := []StructField{
		{"status", "u8"},
		{"chs_start", "u24"},
		{"os_type", "u8"},
		{"chs_end", "u24"},
		{"lba_start", "u32"},
		{"lba_size", "u32"},
	}

	knownOSTypes := map[int]string{
		0x07: "NTFS_IFS_HPFS_exFAT",
		0x0B: "FAT32",
		0x0C: "FAT32",
		0x43: "Linux",
		0x4D: "QNX Primary Volume",
		0x4E: "QNX Secondary Volume",
		0x81: "Minix",
		0x83: "Linux",
		0x8E: "Linux LVM",
		0x96: "ISO-9660",
		0xB1: "QNXv6 File System",
		0xB2: "QNXv6 File System",
		0xB3: "QNXv6 File System",
		0xEE: "EFI GPT Protective",
		0xEF: "EFI System Partition",
	}

	allowedStatus := map[int]bool{0: true, 0x80: true}
	entrySize := StructSize(partEntryFields)

	partTableStart := partitionTableOffset
	partTableEnd := partTableStart + (entrySize * partitionCount)

	if partTableEnd > len(data) {
		return nil, fmt.Errorf("MBR: insufficient data for partition table")
	}

	partTable := data[partTableStart:partTableEnd]

	type mbrPartition struct {
		start int
		size  int
		name  string
	}

	var partitions []mbrPartition
	imageSize := 0

	for i := 0; i < partitionCount; i++ {
		entryStart := i * entrySize
		entry, err := Parse(partTable[entryStart:], partEntryFields, "little")
		if err != nil {
			return nil, fmt.Errorf("MBR: partition entry parse failed")
		}

		if entry["os_type"] == 0 && entry["lba_size"] == 0 {
			continue
		}

		if !allowedStatus[entry["status"]] {
			continue
		}

		partName := "Unknown"
		if name, ok := knownOSTypes[entry["os_type"]]; ok {
			partName = name
		}

		partStart := entry["lba_start"] * blockSize
		partSize := entry["lba_size"] * blockSize
		partEnd := partStart + partSize

		if partEnd > len(data) {
			continue
		}

		if partStart != result.Offset {
			partitions = append(partitions, mbrPartition{
				start: partStart,
				size:  partSize,
				name:  partName,
			})
		}

		if partEnd > imageSize {
			imageSize = partEnd
		}
	}

	if len(partitions) == 0 {
		return nil, fmt.Errorf("MBR: no valid partitions")
	}

	if imageSize <= minImageSize {
		return nil, fmt.Errorf("MBR: image size too small")
	}

	result.Size = imageSize
	for _, p := range partitions {
		if p.start == result.Offset {
			result.ExtractionDeclined = true
		}
		result.Description = fmt.Sprintf("%s, partition: %s", result.Description, p.name)
	}
	result.Description = fmt.Sprintf("%s, image size: %d bytes", result.Description, result.Size)
	return result, nil
}

// ---- UEFI ----

const (
	DescriptionUEFIVolume  = "UEFI PI firmware volume"
	DescriptionUEFICapsule = "UEFI capsule image"
)

// uefiVolumeMagic returns magic bytes for UEFI PI firmware volumes.
func uefiVolumeMagic() [][]byte {
	return [][]byte{
		[]byte("_FVH"),
	}
}

// uefiCapsuleMagic returns magic bytes for UEFI capsule images.
func uefiCapsuleMagic() [][]byte {
	return [][]byte{
		// EFI capsule GUID
		{0xBD, 0x86, 0x66, 0x3B, 0x76, 0x0D, 0x30, 0x40, 0xB7, 0x0E, 0xB5, 0x51, 0x9E, 0x2F, 0xC5, 0xA0},
		// EFI2 capsule GUID
		{0x8B, 0xA6, 0x3C, 0x4A, 0x23, 0x77, 0xFB, 0x48, 0x80, 0x3D, 0x57, 0x8C, 0xC1, 0xFE, 0xC4, 0x4D},
		// UEFI capsule GUID
		{0xB9, 0x82, 0x91, 0x53, 0xB5, 0xAB, 0x91, 0x43, 0xB6, 0x9A, 0xE3, 0xA9, 0x43, 0xF7, 0x2F, 0xCC},
	}
}

// uefiVolumeParser validates UEFI PI firmware volume signatures.
func uefiVolumeParser(data []byte, offset int) (*SignatureResult, error) {
	const uefiMagicOffset = 40

	result := &SignatureResult{
		Description: DescriptionUEFIVolume,
		Confidence:  ConfidenceMedium,
	}

	if offset < uefiMagicOffset {
		return nil, fmt.Errorf("UEFI volume: offset too small")
	}

	result.Offset = offset - uefiMagicOffset
	uefiData := data[result.Offset:]

	validRevisions := map[int]bool{1: true, 2: true}

	volFields := []StructField{
		{"volume_size", "u64"},
		{"magic", "u32"},
		{"attributes", "u32"},
		{"header_size", "u16"},
		{"header_crc", "u16"},
		{"extended_header_offset", "u16"},
		{"reserved", "u8"},
		{"revision", "u8"},
	}

	if len(uefiData) < StructSize(volFields) {
		return nil, fmt.Errorf("UEFI volume: insufficient data")
	}

	volHeader, err := Parse(uefiData, volFields, "little")
	if err != nil {
		return nil, fmt.Errorf("UEFI volume: parse failed")
	}

	if volHeader["header_size"] >= volHeader["volume_size"] {
		return nil, fmt.Errorf("UEFI volume: header size >= volume size")
	}
	if volHeader["reserved"] != 0 {
		return nil, fmt.Errorf("UEFI volume: reserved field not zero")
	}
	if !validRevisions[volHeader["revision"]] {
		return nil, fmt.Errorf("UEFI volume: invalid revision")
	}

	volumeSize := volHeader["volume_size"]
	if len(data) < result.Offset+volumeSize {
		return nil, fmt.Errorf("UEFI volume: volume size exceeds data")
	}

	result.Size = volumeSize
	result.Description = fmt.Sprintf("%s, header CRC: %#X, header size: %d bytes, total size: %d bytes",
		result.Description, uint32(volHeader["header_crc"]), volHeader["header_size"], volumeSize)
	return result, nil
}

// uefiCapsuleParser validates UEFI capsule image signatures.
func uefiCapsuleParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionUEFICapsule,
		Confidence:  ConfidenceMedium,
	}

	capsuleFields := []StructField{
		{"guid_p1", "u64"},
		{"guid_p2", "u64"},
		{"header_size", "u32"},
		{"flags", "u32"},
		{"total_size", "u32"},
	}

	if offset+StructSize(capsuleFields) > len(data) {
		return nil, fmt.Errorf("UEFI capsule: insufficient data")
	}

	capsuleHeader, err := Parse(data[offset:], capsuleFields, "little")
	if err != nil {
		return nil, fmt.Errorf("UEFI capsule: parse failed")
	}

	headerSize := capsuleHeader["header_size"]
	totalSize := capsuleHeader["total_size"]
	availableData := len(data) - offset

	if headerSize >= totalSize {
		return nil, fmt.Errorf("UEFI capsule: header size >= total size")
	}

	if totalSize < availableData {
		return nil, fmt.Errorf("UEFI capsule: total size smaller than available data")
	}

	result.Size = totalSize
	result.Description = fmt.Sprintf("%s, header size: %d bytes, total size: %d bytes",
		result.Description, headerSize, totalSize)
	return result, nil
}

// ---- PCH ROM ----

const DescriptionPCHRom = "Intel serial flash for PCH ROM"

// pchRomMagic returns magic bytes for PCH ROM signatures.
func pchRomMagic() [][]byte {
	return [][]byte{
		{0x5a, 0xa5, 0xf0, 0x0f},
	}
}

// pchRomParser validates PCH ROM signatures.
func pchRomParser(data []byte, offset int) (*SignatureResult, error) {
	const pchMagicOffset = 16
	const headerStructOffset = 16
	const headerStructSize = 8
	const expectedFCBA = 3
	const expectedFRBA = 4
	const flashRegionCount = 5
	const flashRegionEntrySize = 4

	result := &SignatureResult{
		Description: DescriptionPCHRom,
	}

	if offset < pchMagicOffset {
		return nil, fmt.Errorf("PCH ROM: offset too small")
	}

	result.Offset = offset - pchMagicOffset
	pchData := data[result.Offset:]

	structStart := headerStructOffset
	structEnd := structStart + headerStructSize
	if structEnd > len(pchData) {
		return nil, fmt.Errorf("PCH ROM: insufficient data for header")
	}

	pchHeaderFields := []StructField{
		{"flmagic", "u32"},
		{"flmap0_fcba", "u8"},
		{"flmap0_nc", "u8"},
		{"flmap0_frba_nr", "u16"},
	}

	pchHeader, err := Parse(pchData[structStart:structEnd], pchHeaderFields, "little")
	if err != nil {
		return nil, fmt.Errorf("PCH ROM: header parse failed")
	}

	if pchHeader["flmap0_fcba"] != expectedFCBA {
		return nil, fmt.Errorf("PCH ROM: unexpected FCBA value")
	}
	if pchHeader["flmap0_frba_nr"] != expectedFRBA {
		return nil, fmt.Errorf("PCH ROM: unexpected FRBA value")
	}

	nc := pchHeader["flmap0_nc"]
	if nc != 0 && nc != 1 {
		return nil, fmt.Errorf("PCH ROM: unexpected NC value")
	}

	// Parse flash region entries
	fcba := pchHeader["flmap0_fcba"]
	flashRegionBaseAddr := ((fcba >> 16) & 0xFF) << 4

	imageSize := 0
	for i := 0; i < flashRegionCount; i++ {
		regionEntryStart := flashRegionBaseAddr + (i * flashRegionEntrySize)
		regionEntryEnd := regionEntryStart + flashRegionEntrySize

		if regionEntryEnd > len(pchData) {
			return nil, fmt.Errorf("PCH ROM: region entry out of bounds")
		}

		regionFields := []StructField{{"region_value", "u32"}}
		regionEntry, err := Parse(pchData[regionEntryStart:regionEntryEnd], regionFields, "little")
		if err != nil {
			return nil, fmt.Errorf("PCH ROM: region entry parse failed")
		}

		regionValue := regionEntry["region_value"]
		regionBase := (regionValue & 0x1FFF) << 12
		regionLimit := (((regionValue & 0x1FFF0000) >> 4) | 0xFFFF) + 1
		regionSize := regionLimit - regionBase

		if regionSize > 0 && regionLimit > imageSize {
			imageSize = regionLimit
		}
	}

	if imageSize == 0 {
		return nil, fmt.Errorf("PCH ROM: no valid flash regions")
	}

	result.Size = headerStructOffset + imageSize
	return result, nil
}

// ---- PCAP ----

const DescriptionPcapNG = "Pcap-NG capture file"

// pcapngMagic returns magic bytes for Pcap-NG files.
func pcapngMagic() [][]byte {
	return [][]byte{
		{0x0A, 0x0D, 0x0D, 0x0A},
	}
}

// pcapngParser validates Pcap-NG capture file signatures.
func pcapngParser(data []byte, offset int) (*SignatureResult, error) {
	const sectionHeaderBlockType = 0x0A0D0D0A

	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionPcapNG,
		Confidence:  ConfidenceHigh,
	}

	sectionFields := []StructField{
		{"block_type", "u32"},
		{"block_size", "u32"},
		{"endian_magic", "u32"},
		{"major_version", "u16"},
		{"minor_version", "u16"},
		{"section_length", "u32"},
	}

	if offset+StructSize(sectionFields) > len(data) {
		return nil, fmt.Errorf("Pcap-NG: insufficient data")
	}

	sectionHeader, err := Parse(data[offset:], sectionFields, "little")
	if err != nil {
		return nil, fmt.Errorf("Pcap-NG: parse failed")
	}

	// Determine endianness
	endianMagic := sectionHeader["endian_magic"]
	var endianness string
	switch endianMagic {
	case 0x1A2B3C4D:
		endianness = "little"
	case 0x4D3C2B1A:
		endianness = "big"
	default:
		return nil, fmt.Errorf("Pcap-NG: invalid endian magic")
	}

	// Re-parse with correct endianness
	blockFields := []StructField{
		{"block_type", "u32"},
		{"block_size", "u32"},
	}
	footerFields := []StructField{{"block_size", "u32"}}
	footerSize := StructSize(footerFields)

	blockHeader, err := Parse(data[offset:], blockFields, endianness)
	if err != nil {
		return nil, fmt.Errorf("Pcap-NG: block header parse failed")
	}

	if blockHeader["block_type"] != sectionHeaderBlockType {
		return nil, fmt.Errorf("Pcap-NG: unexpected block type")
	}

	blockSize := blockHeader["block_size"]
	if blockSize < footerSize {
		return nil, fmt.Errorf("Pcap-NG: block size too small")
	}

	// Validate block size via footer
	footerStart := offset + blockSize - footerSize
	if footerStart+footerSize > len(data) {
		return nil, fmt.Errorf("Pcap-NG: block footer out of bounds")
	}

	blockFooter, err := Parse(data[footerStart:], footerFields, endianness)
	if err != nil {
		return nil, fmt.Errorf("Pcap-NG: block footer parse failed")
	}

	if blockFooter["block_size"] != blockSize {
		return nil, fmt.Errorf("Pcap-NG: block size mismatch between header and footer")
	}

	// Reserved bit check
	if (blockHeader["block_type"] & 0x80000000) != 0 {
		return nil, fmt.Errorf("Pcap-NG: reserved bit set in block type")
	}

	result.Size = blockSize
	result.Description = fmt.Sprintf("%s, total size: %d bytes", result.Description, result.Size)

	if offset == 0 && result.Size == len(data) {
		result.ExtractionDeclined = true
	}

	return result, nil
}

// ---- PJL ----

const DescriptionPJL = "HP Printer Job Language data"

// pjlMagic returns magic bytes for PJL data.
func pjlMagic() [][]byte {
	return [][]byte{
		{0x1B, 0x25, 0x2D, 0x31, 0x32, 0x33, 0x34, 0x35, 0x58, 0x40, 0x50, 0x4A, 0x4C}, // "\x1B%-12345X@PJL"
	}
}

// pjlParser validates PJL data signatures.
func pjlParser(data []byte, offset int) (*SignatureResult, error) {
	const pjlCommandsOffset = 9

	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionPJL,
		Confidence:  ConfidenceLow,
	}

	if offset+pjlCommandsOffset >= len(data) {
		return nil, fmt.Errorf("PJL: insufficient data")
	}

	pjlText := GetCString(data[offset+pjlCommandsOffset:])
	if len(pjlText) == 0 {
		return nil, fmt.Errorf("PJL: no command text found")
	}

	result.Size = len(pjlText)
	displayText := strings.ReplaceAll(pjlText, "\r", " ")
	displayText = strings.ReplaceAll(displayText, "\n", "")
	result.Description = fmt.Sprintf("%s: \"%s\"", result.Description, displayText)
	return result, nil
}

// ---- S-record ----

const (
	DescriptionSRecord        = "Motorola S-record"
	DescriptionSRecordGeneric = "Motorola S-record (generic)"
)

// srecordMagic returns magic bytes for Motorola S-records.
func srecordMagic() [][]byte {
	return [][]byte{
		[]byte("S00600004844521B"),
	}
}

// srecordGenericMagic returns short/generic magic bytes for S-records.
func srecordGenericMagic() [][]byte {
	return [][]byte{
		[]byte("S0"),
	}
}

// srecordParser validates Motorola S-record signatures.
func srecordParser(data []byte, offset int) (*SignatureResult, error) {
	const unixTermChar byte = 0x0A
	const windowsTermChar byte = 0x0D

	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionSRecord,
		Confidence:  ConfidenceHigh,
	}

	availableData := len(data)

	// Search for srec footer lines
	srecFooters := [][]byte{[]byte("\nS9"), []byte("\nS8"), []byte("\nS7")}
	footerLen := 3

	searchData := data[offset:]

	for _, footer := range srecFooters {
		idx := bytes.Index(searchData, footer)
		if idx < 0 {
			continue
		}

		osType := "Unix"
		srecEOF := offset + idx + footerLen

		for srecEOF < availableData {
			b := data[srecEOF]
			if b == windowsTermChar || b == unixTermChar {
				if b == windowsTermChar {
					srecEOF++
					osType = "Windows"
				}
				if srecEOF >= availableData {
					return nil, fmt.Errorf("S-record: unexpected end after footer")
				}
				if data[srecEOF] != unixTermChar {
					return nil, fmt.Errorf("S-record: invalid termination")
				}
				srecEOF++
				result.Size = srecEOF - offset
				result.Description = fmt.Sprintf("%s, origin OS: %s, total size: %d bytes",
					result.Description, osType, result.Size)
				return result, nil
			}
			srecEOF++
		}
	}

	return nil, fmt.Errorf("S-record: no valid footer found")
}

// ---- DMG ----

const DescriptionDMG = "Apple Disk iMaGe"

// dmgMagic returns magic bytes for DMG files (footer signature).
func dmgMagic() [][]byte {
	return [][]byte{
		{0x6B, 0x6F, 0x6C, 0x79, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x02, 0x00}, // "koly\x00\x00\x00\x04\x00\x00\x02\x00"
	}
}

// dmgParser validates DMG footer signatures.
func dmgParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Description: DescriptionDMG,
		Confidence:  ConfidenceHigh + 1,
	}

	dmgFooterFields := []StructField{
		{"magic", "u32"},
		{"version", "u32"},
		{"header_size", "u32"},
		{"flags", "u32"},
		{"running_data_fork_offset", "u64"},
		{"data_fork_offset", "u64"},
		{"data_fork_length", "u64"},
		{"rsrc_fork_offset", "u64"},
		{"rsrc_fork_length", "u64"},
		{"segment_number", "u32"},
		{"segment_count", "u32"},
		{"segment_id_p1", "u64"},
		{"segment_id_p2", "u64"},
		{"data_checksum_type", "u32"},
		{"data_checksum_size", "u32"},
		{"data_checksum_1", "u32"},
		{"data_checksum_2", "u32"},
		{"data_checksum_3", "u32"},
		{"data_checksum_4", "u32"},
		{"data_checksum_5", "u32"},
		{"data_checksum_6", "u32"},
		{"data_checksum_7", "u32"},
		{"data_checksum_8", "u32"},
		{"data_checksum_9", "u32"},
		{"data_checksum_10", "u32"},
		{"data_checksum_11", "u32"},
		{"data_checksum_12", "u32"},
		{"data_checksum_13", "u32"},
		{"data_checksum_14", "u32"},
		{"data_checksum_15", "u32"},
		{"data_checksum_16", "u32"},
		{"data_checksum_17", "u32"},
		{"data_checksum_18", "u32"},
		{"data_checksum_19", "u32"},
		{"data_checksum_20", "u32"},
		{"data_checksum_21", "u32"},
		{"data_checksum_22", "u32"},
		{"data_checksum_23", "u32"},
		{"data_checksum_24", "u32"},
		{"data_checksum_25", "u32"},
		{"data_checksum_26", "u32"},
		{"data_checksum_27", "u32"},
		{"data_checksum_28", "u32"},
		{"data_checksum_29", "u32"},
		{"data_checksum_30", "u32"},
		{"data_checksum_31", "u32"},
		{"data_checksum_32", "u32"},
		{"xml_offset", "u64"},
		{"xml_length", "u64"},
		{"reserved_1", "u64"},
		{"reserved_2", "u64"},
		{"reserved_3", "u64"},
		{"reserved_4", "u64"},
		{"reserved_5", "u64"},
		{"reserved_6", "u64"},
		{"reserved_7", "u64"},
		{"reserved_8", "u64"},
		{"reserved_9", "u64"},
		{"reserved_10", "u64"},
		{"reserved_11", "u64"},
		{"reserved_12", "u64"},
		{"reserved_13", "u64"},
		{"reserved_14", "u64"},
		{"reserved_15", "u64"},
		{"checksum_type", "u32"},
		{"checksum_size", "u32"},
		{"checksum_1", "u32"},
		{"checksum_2", "u32"},
		{"checksum_3", "u32"},
		{"checksum_4", "u32"},
		{"checksum_5", "u32"},
		{"checksum_6", "u32"},
		{"checksum_7", "u32"},
		{"checksum_8", "u32"},
		{"checksum_9", "u32"},
		{"checksum_10", "u32"},
		{"checksum_11", "u32"},
		{"checksum_12", "u32"},
		{"checksum_13", "u32"},
		{"checksum_14", "u32"},
		{"checksum_15", "u32"},
		{"checksum_16", "u32"},
		{"checksum_17", "u32"},
		{"checksum_18", "u32"},
		{"checksum_19", "u32"},
		{"checksum_20", "u32"},
		{"checksum_21", "u32"},
		{"checksum_22", "u32"},
		{"checksum_23", "u32"},
		{"checksum_24", "u32"},
		{"checksum_25", "u32"},
		{"checksum_26", "u32"},
		{"checksum_27", "u32"},
		{"checksum_28", "u32"},
		{"checksum_29", "u32"},
		{"checksum_30", "u32"},
		{"checksum_31", "u32"},
		{"checksum_32", "u32"},
		{"image_variant", "u32"},
		{"sector_count", "u64"},
		{"reserved_16", "u32"},
		{"reserved_17", "u32"},
		{"reserved_18", "u32"},
	}

	footerSize := StructSize(dmgFooterFields)

	if offset+footerSize > len(data) {
		return nil, fmt.Errorf("DMG: insufficient data for footer")
	}

	dmgFooter, err := Parse(data[offset:], dmgFooterFields, "big")
	if err != nil {
		return nil, fmt.Errorf("DMG: footer parse failed")
	}

	if dmgFooter["header_size"] != footerSize {
		return nil, fmt.Errorf("DMG: header size mismatch (got %d, expected %d)", dmgFooter["header_size"], footerSize)
	}

	dataLength := dmgFooter["data_fork_length"]
	xmlLength := dmgFooter["xml_length"]

	if dataLength+xmlLength > offset {
		return nil, fmt.Errorf("DMG: data + xml length exceeds offset")
	}

	// Search for XML property list with blkx key
	xmlSig := []byte("<?xml")
	blkxKey := "<key>blkx</key>"
	const minXMLLen = 1024

	xmlOffset := -1
	searchStart := 0
	for {
		idx := bytes.Index(data[searchStart:], xmlSig)
		if idx < 0 {
			break
		}
		xmlStart := searchStart + idx
		xmlEnd := xmlStart + minXMLLen
		if xmlEnd > len(data) {
			searchStart = xmlStart + len(xmlSig)
			continue
		}
		xmlStr := string(data[xmlStart:xmlEnd])
		if strings.Contains(xmlStr, blkxKey) {
			xmlOffset = xmlStart
			break
		}
		searchStart = xmlStart + len(xmlSig)
	}

	if xmlOffset < 0 {
		return nil, fmt.Errorf("DMG: XML property list not found")
	}

	if xmlOffset < dataLength {
		return nil, fmt.Errorf("DMG: XML data before image data")
	}

	result.Size = offset + footerSize
	result.Offset = xmlOffset - dataLength
	result.Description = fmt.Sprintf("%s, total size: %d bytes", result.Description, result.Size)
	return result, nil
}

// ---- QNX IFS ----

const DescriptionQNXIFS = "QNX IFS image"

// qnxIFSMagic returns magic bytes for QNX IFS images.
func qnxIFSMagic() [][]byte {
	return [][]byte{
		{0xEB, 0x7E, 0xFF, 0x00, 0x01, 0x00},
	}
}

// qnxIFSParser validates QNX IFS image signatures.
func qnxIFSParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionQNXIFS,
	}

	ifsFields := []StructField{
		{"magic", "u32"},
		{"version", "u16"},
		{"flags1", "u8"},
		{"flags2", "u8"},
		{"header_size", "u16"},
		{"machine", "u16"},
		{"startup_vaddr", "u32"},
		{"paddr_bias", "u32"},
		{"image_paddr", "u32"},
		{"ram_paddr", "u32"},
		{"ram_size", "u32"},
		{"startup_size", "u32"},
		{"stored_size", "u32"},
		{"imagefs_paddr", "u32"},
		{"imagefs_size", "u32"},
		{"preboot_size", "u16"},
		{"zero_0", "u16"},
		{"zero_1", "u32"},
		{"zero_2", "u32"},
		{"zero_3", "u32"},
	}

	if offset+StructSize(ifsFields) > len(data) {
		return nil, fmt.Errorf("QNX IFS: insufficient data")
	}

	ifsHeader, err := Parse(data[offset:], ifsFields, "little")
	if err != nil {
		return nil, fmt.Errorf("QNX IFS: parse failed")
	}

	if ifsHeader["flags2"] != 0 {
		return nil, fmt.Errorf("QNX IFS: flags2 not zero")
	}
	if ifsHeader["zero_0"] != 0 || ifsHeader["zero_1"] != 0 || ifsHeader["zero_2"] != 0 || ifsHeader["zero_3"] != 0 {
		return nil, fmt.Errorf("QNX IFS: zero fields not zero")
	}

	totalSize := ifsHeader["stored_size"]
	availableData := len(data) - offset
	if totalSize > availableData {
		return nil, fmt.Errorf("QNX IFS: total size exceeds available data")
	}

	result.Size = totalSize
	result.Description = fmt.Sprintf("%s, total size: %d bytes", result.Description, result.Size)
	return result, nil
}
