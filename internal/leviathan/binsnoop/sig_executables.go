package binsnoop

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// ELF
// ---------------------------------------------------------------------------

const ELFDescription = "ELF binary"

func ELFMagic() [][]byte {
	return [][]byte{
		{0x7F, 0x45, 0x4C, 0x46}, // "\x7FELF"
	}
}

func ELFParser(data []byte, offset int) (*SignatureResult, error) {
	const elfIdentSize = 16
	const elfInfoStructSize = 8
	const expectedVersion = 1

	elfIdentFields := []StructField{
		{"magic", "u32"},
		{"class", "u8"},
		{"endianness", "u8"},
		{"version", "u8"},
		{"osabi", "u8"},
		{"abiversion", "u8"},
		{"padding_1", "u32"},
		{"padding_2", "u24"},
	}

	elfInfoFields := []StructField{
		{"type", "u16"},
		{"machine", "u16"},
		{"version", "u32"},
	}

	elfClasses := map[int]int{
		1: 32,
		2: 64,
	}

	elfEndianness := map[int]string{
		1: "little",
		2: "big",
	}

	elfOSABI := map[int]string{
		0:   "System-V (Unix)",
		1:   "HP-UX",
		2:   "NetBSD",
		3:   "Linux",
		4:   "GNU Hurd",
		5:   "86Open",
		6:   "Solaris",
		7:   "AIX",
		8:   "IRIX",
		9:   "FreeBSD",
		10:  "Tru64",
		11:  "Novell Modesto",
		12:  "OpenBSD",
		13:  "OpenVMS",
		14:  "NonStop Kernel",
		15:  "AROS",
		16:  "FenixOS",
		17:  "Nuxi CloudABI",
		18:  "OpenVOS",
		97:  "ARM ABI",
		102: "Cell LV2",
		202: "Cafe OS",
		255: "embedded",
	}

	elfTypes := map[int]string{
		0: "no file type",
		1: "relocatable",
		2: "executable",
		3: "shared object",
		4: "core file",
	}

	elfMachines := map[int]string{
		0:   "no machine",
		1:   "AT&T WE 32100",
		2:   "SPARC",
		3:   "x86",
		4:   "Motorola 68k",
		5:   "Motorola 88k",
		6:   "Intel MCU",
		7:   "Intel 80860",
		8:   "MIPS",
		9:   "IBM System/370",
		10:  "MIPS RS3000",
		11:  "RS6000",
		15:  "HP PA-RISC",
		16:  "nCUBE",
		17:  "Fujitsu VPP500",
		18:  "SPARC32PLUS",
		19:  "Intel 80960",
		20:  "PowerPC",
		21:  "PowerPC 64-bit",
		22:  "S390",
		23:  "IBM SPU/SPC",
		24:  "cisco SVIP",
		25:  "cisco 7200",
		36:  "NEC V800",
		37:  "Fujitsu FR20",
		38:  "TRW RH-32",
		39:  "Motorola RCE",
		40:  "ARM",
		41:  "Digital Alpha",
		42:  "SuperH",
		43:  "SPARCv9",
		44:  "Siemens TriCore embedded processor",
		45:  "Argonaut RISC Core",
		46:  "Hitachi H8/300",
		47:  "Hitachi H8/300H",
		48:  "Hitachi H8S",
		49:  "Hitachi H8/500",
		50:  "IA-64",
		51:  "Stanford MIPS-X",
		52:  "Motorola ColdFire",
		53:  "Motorola M68HC12",
		54:  "Fujitsu MMA Multimedia Accelerator",
		55:  "Siemens PCP",
		56:  "Sony nCPU embedded RISC processor",
		57:  "Denso NDR1 microprocessor",
		58:  "Motorola StarCore",
		59:  "Toyota ME16",
		60:  "STMicroelectronics ST100",
		61:  "Advanced Logic TinyJ embedded processor",
		62:  "AMD X86-64",
		63:  "Sony DSP processor",
		64:  "PDP-10",
		65:  "PDP-11",
		66:  "Siemens FX66",
		67:  "STMicroelectronics ST9+",
		68:  "STMicroelectronics ST7",
		69:  "Motorola MC68HC16",
		70:  "Motorola MC68HC11",
		71:  "Motorola MC68HC08",
		72:  "Motorola MC68HC05",
		73:  "Silicon Graphics SVx",
		74:  "STMicroelectonrics ST19",
		75:  "Digital VAX",
		76:  "Axis Communications 32-bit CPU",
		77:  "Infineon Technologies 32-bit CPU",
		78:  "Element 14 64-bit DSP",
		79:  "LSI Logic 16-bit DSP",
		80:  "MMIX",
		81:  "Harvard machine-independent",
		82:  "SiTera Prism",
		83:  "Atmel AVR 8-bit",
		84:  "Fujitsu FR30",
		85:  "Mitsubishi D10V",
		86:  "Mitsubishi D30V",
		87:  "NEC v850",
		88:  "Renesas M32R",
		89:  "Matsushita MN10300",
		90:  "Matsushita MN10200",
		91:  "picoJava",
		92:  "OpenRISC",
		93:  "Synopsys ARCompact ARC700 cores",
		94:  "Tensilica Xtensa",
		95:  "Alphamosaic VideoCore",
		96:  "Thompson Multimedia",
		97:  "NatSemi 32k",
		98:  "Tenor Network TPC",
		99:  "Trebia SNP 1000",
		100: "STMicroelectronics ST200",
		101: "Ubicom IP2022",
		102: "MAX Processor",
		103: "NatSemi CompactRISC",
		104: "Fujitsu F2MC16",
		105: "TI msp430",
		106: "Analog Devices Blackfin",
		107: "S1C33 Family of Seiko Epson",
		108: "Sharp embedded",
		109: "Arca RISC",
		110: "PKU-Unity Ltd.",
		111: "eXcess: 16/32/64-bit",
		112: "Icera Deep Execution Processor",
		113: "Altera Nios II",
		114: "NatSemi CRX",
		115: "Motorola XGATE",
		116: "Infineon C16x/XC16x",
		117: "Renesas M16C series",
		118: "Microchip dsPIC30F",
		119: "Freescale RISC core",
		120: "Renesas M32C series",
		131: "Altium TSK3000 core",
		132: "Freescale RS08",
		134: "Cyan Technology eCOG2",
		135: "Sunplus S+core7 RISC",
		136: "New Japan Radio (NJR) 24-bit DSP",
		137: "Broadcom VideoCore III processor",
		138: "LatticeMico32",
		139: "Seiko Epson C17 family",
		140: "TMS320C6000",
		141: "TMS320C2000",
		142: "TMS320C55x",
		144: "TI Programmable Realtime Unit",
		160: "STMicroelectronics 64bit VLIW DSP",
		161: "Cypress M8C",
		162: "Renesas R32C series",
		163: "NXP TriMedia family",
		164: "Qualcomm DSP6",
		165: "Intel 8051 and variants",
		166: "STMicroelectronics STxP7x family",
		167: "Andes embedded RISC",
		168: "Cyan eCOG1X family",
		169: "Dallas MAXQ30",
		170: "New Japan Radio (NJR) 16-bit DSP",
		171: "M2000 Reconfigurable RISC",
		172: "Cray NV2 vector architecture",
		173: "Renesas RX family",
		174: "META",
		175: "MCST Elbrus e2k",
		176: "Cyan Technology eCOG16 family",
		177: "NatSemi CompactRISC",
		178: "Freescale Extended Time Processing Unit",
		179: "Infineon SLE9X",
		180: "Intel L1OM",
		181: "Intel K1OM",
		183: "ARM 64-bit",
		185: "Atmel 32-bit family",
		186: "STMicroeletronics STM8 8-bit",
		187: "Tilera TILE64",
		188: "Tilera TILEPro",
		189: "Xilinx MicroBlaze 32-bit RISC",
		190: "NVIDIA CUDA architecture",
		191: "Tilera TILE-Gx",
		195: "Synopsys ARCv2/HS3x/HS4x cores",
		197: "Renesas RL78 family",
		199: "Renesas 78K0R",
		200: "Freescale 56800EX",
		201: "Beyond BA1",
		202: "Beyond BA2",
		203: "XMOS xCORE",
		204: "Microchip 8-bit PIC(r)",
		210: "KM211 KM32",
		211: "KM211 KMX32",
		212: "KM211 KMX16",
		213: "KM211 KMX8",
		214: "KM211 KVARC",
		215: "Paneve CDP",
		216: "Cognitive Smart Memory",
		217: "iCelero CoolEngine",
		218: "Nanoradio Optimized RISC",
		219: "CSR Kalimba architecture family",
		220: "Zilog Z80",
		221: "Controls and Data Services VISIUMcore processor",
		222: "FTDI Chip FT32 high performance 32-bit RISC architecture",
		223: "Moxie processor family",
		224: "AMD GPU architecture",
		243: "RISC-V",
		244: "Lanai 32-bit processor",
		245: "CEVA Processor Architecture Family",
		246: "CEVA X2 Processor Family",
		247: "Berkeley Packet Filter",
		248: "Graphcore Intelligent Processing Unit",
		249: "Imagination Technologies",
		250: "Netronome Flow Processor",
		251: "NEC Vector Engine",
		252: "C-SKY processor family",
		253: "Synopsys ARCv3 64-bit ISA/HS6x cores",
		254: "MOS Technology MCS 6502 processor",
		255: "Synopsys ARCv3 32-bit",
		256: "Kalray VLIW core of the MPPA family",
		257: "WDC 65C816",
		258: "LoongArch",
		259: "ChipON KungFu32",
	}

	elfData := data[offset:]
	if len(elfData) < elfIdentSize+elfInfoStructSize {
		return nil, fmt.Errorf("ELF: not enough data")
	}

	// Parse the e_ident (endianness doesn't matter for single-byte fields)
	eIdent, err := Parse(elfData, elfIdentFields, "little")
	if err != nil {
		return nil, fmt.Errorf("ELF: e_ident parse error: %w", err)
	}

	// Sanity check padding fields
	if eIdent["padding_1"] != 0 || eIdent["padding_2"] != 0 {
		return nil, fmt.Errorf("ELF: non-zero padding in e_ident")
	}
	if eIdent["version"] != expectedVersion {
		return nil, fmt.Errorf("ELF: unexpected e_ident version %d", eIdent["version"])
	}

	classVal, classOK := elfClasses[eIdent["class"]]
	if !classOK {
		return nil, fmt.Errorf("ELF: unknown class %d", eIdent["class"])
	}

	endianStr, endianOK := elfEndianness[eIdent["endianness"]]
	if !endianOK {
		return nil, fmt.Errorf("ELF: unknown endianness %d", eIdent["endianness"])
	}

	osabiStr, osabiOK := elfOSABI[eIdent["osabi"]]
	if !osabiOK {
		return nil, fmt.Errorf("ELF: unknown OS/ABI %d", eIdent["osabi"])
	}

	// Parse the rest of the ELF header with the correct endianness
	elfInfoData := elfData[elfIdentSize : elfIdentSize+elfInfoStructSize]
	elfInfo, err := Parse(elfInfoData, elfInfoFields, endianStr)
	if err != nil {
		return nil, fmt.Errorf("ELF: info parse error: %w", err)
	}

	if elfInfo["version"] != expectedVersion {
		return nil, fmt.Errorf("ELF: unexpected info version %d", elfInfo["version"])
	}

	typeStr, typeOK := elfTypes[elfInfo["type"]]
	if !typeOK {
		return nil, fmt.Errorf("ELF: unknown type %d", elfInfo["type"])
	}

	machineStr := "Unknown"
	if m, ok := elfMachines[elfInfo["machine"]]; ok {
		machineStr = m
	}

	return &SignatureResult{
		Offset:      offset,
		Name:        "elf",
		Description: fmt.Sprintf("%s, %d-bit %s, %s for %s, %s endian", ELFDescription, classVal, typeStr, machineStr, osabiStr, endianStr),
		Confidence:  ConfidenceMedium,
	}, nil
}

// ---------------------------------------------------------------------------
// PE
// ---------------------------------------------------------------------------

const PEDescription = "Windows PE binary"

func PEMagic() [][]byte {
	// First 16 bytes of common DOS headers (e_magic through e_ss)
	return [][]byte{
		{0x4D, 0x5A, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00},
		{0x4D, 0x5A, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	}
}

func PEParser(data []byte, offset int) (*SignatureResult, error) {
	const peMagicValue = 0x00004550 // "PE\x00\x00"

	dosFields := []StructField{
		{"e_magic", "u16"},
		{"e_cblp", "u16"},
		{"e_cp", "u16"},
		{"e_crlc", "u16"},
		{"e_cparhdr", "u16"},
		{"e_minalloc", "u16"},
		{"e_maxalloc", "u16"},
		{"e_ss", "u16"},
		{"e_sp", "u16"},
		{"e_csum", "u16"},
		{"e_ip", "u16"},
		{"e_cs", "u16"},
		{"e_lfarlc", "u16"},
		{"e_ovno", "u16"},
		{"e_res_1", "u16"},
		{"e_res_2", "u16"},
		{"e_res_3", "u16"},
		{"e_res_4", "u16"},
		{"e_oemid", "u16"},
		{"e_oeminfo", "u16"},
		{"e_res_5", "u16"},
		{"e_res_6", "u16"},
		{"e_res_7", "u16"},
		{"e_res_8", "u16"},
		{"e_res_9", "u16"},
		{"e_res_10", "u16"},
		{"e_res_11", "u16"},
		{"e_res_12", "u16"},
		{"e_res_13", "u16"},
		{"e_res_14", "u16"},
		{"e_lfanew", "u32"},
	}

	peFields := []StructField{
		{"magic", "u32"},
		{"machine", "u16"},
		{"number_of_sections", "u16"},
		{"timestamp", "u32"},
		{"symbol_table_ptr", "u32"},
		{"number_of_symbols", "u32"},
		{"optional_header_size", "u16"},
		{"characteristics", "u16"},
	}

	knownMachineTypes := map[int]string{
		0x0:    "Unknown",
		0x184:  "Alpha32",
		0x284:  "Alpha64",
		0x1D3:  "Matsushita AM33",
		0x8664: "Intel x86-64",
		0x1C0:  "ARM",
		0xAA64: "ARM-64",
		0x1C4:  "ARM Thumb2",
		0xEBC:  "EFI",
		0x14C:  "Intel x86",
		0x200:  "Intel Itanium",
		0x6232: "LoongArch 32-bit",
		0x6264: "LoongArch 64-bit",
		0x9041: "Mitsubishi M32R",
		0x266:  "MIPS16",
		0x366:  "MIPS with FPU",
		0x466:  "MIPS16 with FPU",
		0x1F0:  "PowerPC",
		0x1F1:  "PowerPC with FPU",
		0x5032: "RISC-V 32-bit",
		0x5064: "RISC-V 64-bit",
		0x5128: "RISC-V 128-bit",
		0x1A2:  "Hitachi SH3",
		0x1A3:  "Hitachi SH3 DSP",
		0x1A6:  "Hitachi SH4",
		0x1A8:  "Hitachi SH5",
		0x1C2:  "Thumb",
		0x169:  "MIPS WCEv2",
	}

	peData := data[offset:]
	dosStructSize := StructSize(dosFields)
	peStructSize := StructSize(peFields)

	if len(peData) < dosStructSize {
		return nil, fmt.Errorf("PE: not enough data for DOS header")
	}

	dosHeader, err := Parse(peData, dosFields, "little")
	if err != nil {
		return nil, fmt.Errorf("PE: DOS header parse error: %w", err)
	}

	// Sanity check the reserved header fields; they should all be 0
	for _, field := range []string{
		"e_res_1", "e_res_2", "e_res_3", "e_res_4",
		"e_res_5", "e_res_6", "e_res_7", "e_res_8",
		"e_res_9", "e_res_10", "e_res_11", "e_res_12",
		"e_res_13", "e_res_14",
	} {
		if dosHeader[field] != 0 {
			return nil, fmt.Errorf("PE: reserved field %s is non-zero", field)
		}
	}

	peHeaderStart := dosHeader["e_lfanew"]
	peHeaderEnd := peHeaderStart + peStructSize

	if peHeaderEnd > len(peData) {
		return nil, fmt.Errorf("PE: e_lfanew points beyond available data")
	}

	peHeader, err := Parse(peData[peHeaderStart:peHeaderEnd], peFields, "little")
	if err != nil {
		return nil, fmt.Errorf("PE: PE header parse error: %w", err)
	}

	if peHeader["magic"] != peMagicValue {
		return nil, fmt.Errorf("PE: invalid PE magic 0x%08X", peHeader["magic"])
	}

	machineStr, machineOK := knownMachineTypes[peHeader["machine"]]
	if !machineOK {
		return nil, fmt.Errorf("PE: unknown machine type 0x%X", peHeader["machine"])
	}

	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, machine type: %s", PEDescription, machineStr),
		Confidence:  ConfidenceMedium,
	}, nil
}
