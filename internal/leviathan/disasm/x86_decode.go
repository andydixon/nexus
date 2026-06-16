package disasm

import "fmt"

// x86_decode.go is a from-scratch x86 / x86-64 instruction decoder. Unlike
// the previous heuristic, it tracks legacy prefixes, the REX prefix, and
// fully decodes ModR/M + SIB + displacement + immediate operands so that
// instruction *lengths* are correct (which keeps a linear sweep in sync) and
// operands are rendered in Intel syntax.
//
// Coverage: the complete general-purpose integer ISA (ALU groups, MOV/LEA,
// shifts/rotates, the F6/F7/FE/FF groups, string ops, stack ops, all near
// branches), the two-byte 0F map (Jcc/SETcc/CMOVcc, MOVZX/MOVSX, IMUL, bit
// tests, CMPXCHG/XADD, BSWAP, SHLD/SHRD, system ops) and the common SSE/SSE2
// moves and arithmetic. VEX/EVEX (AVX), x87 detail and a few rare encodings
// fall back to a one-byte `db` resync, exactly like objdump's "(bad)".

type x86dec struct {
	buf  []byte
	pos  int
	base int64
	is64 bool

	opSize   int // 16/32/64 effective operand size
	addrSize int // 16/32/64 effective address size
	has66    bool
	seg      string
	rep      string // "rep" / "repne" / ""
	lock     bool

	rexW, rexR, rexX, rexB bool
	hasRex                 bool

	trunc   bool  // ran past end of buffer mid-instruction
	hasRip  bool  // a RIP-relative memory operand was decoded
	ripDisp int64 // its displacement (resolved once length is known)
}

func decodeX86(buf []byte, base int64, is64 bool, maxInstr int) []map[string]any {
	out := []map[string]any{}
	i := 0
	for i < len(buf) && len(out) < maxInstr {
		d := &x86dec{buf: buf, pos: i, base: base, is64: is64}
		mn, ops, conf := d.decode()
		var length int
		if d.trunc || mn == "" {
			length = 1
			mn = "db"
			ops = fmt.Sprintf("0x%02x", buf[i])
			conf = false
		} else {
			length = d.pos - i
			if length <= 0 {
				length = 1
			}
			if i+length > len(buf) {
				length = len(buf) - i
			}
			if d.hasRip {
				abs := base + int64(i+length) + d.ripDisp
				ops += fmt.Sprintf(" ; 0x%x", abs)
			}
		}
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i : i+length]),
			"mnemonic":  mn,
			"operands":  ops,
			"confident": conf,
		})
		i += length
	}
	return out
}

// ---- byte readers (set trunc on overrun, never panic) ----

func (d *x86dec) u8() uint8 {
	if d.pos >= len(d.buf) {
		d.trunc = true
		return 0
	}
	b := d.buf[d.pos]
	d.pos++
	return b
}

func (d *x86dec) u16() uint16 { return uint16(d.u8()) | uint16(d.u8())<<8 }
func (d *x86dec) u32() uint32 {
	return uint32(d.u8()) | uint32(d.u8())<<8 | uint32(d.u8())<<16 | uint32(d.u8())<<24
}
func (d *x86dec) u64() uint64 { return uint64(d.u32()) | uint64(d.u32())<<32 }

func (d *x86dec) peek() (uint8, bool) {
	if d.pos >= len(d.buf) {
		return 0, false
	}
	return d.buf[d.pos], true
}

// ---- prefix + opcode dispatch ----

func (d *x86dec) decode() (string, string, bool) {
	d.opSize = 32
	if d.is64 {
		d.addrSize = 64
	} else {
		d.addrSize = 32
	}
prefixes:
	for {
		b, ok := d.peek()
		if !ok {
			return "", "", false
		}
		switch b {
		case 0x66:
			d.has66 = true
			d.opSize = 16
			d.pos++
			continue
		case 0x67:
			if d.is64 {
				d.addrSize = 32
			} else {
				d.addrSize = 16
			}
			d.pos++
			continue
		case 0xF0:
			d.lock = true
			d.pos++
			continue
		case 0xF2:
			d.rep = "repne"
			d.pos++
			continue
		case 0xF3:
			d.rep = "rep"
			d.pos++
			continue
		case 0x2E:
			d.seg = "cs"
			d.pos++
			continue
		case 0x36:
			d.seg = "ss"
			d.pos++
			continue
		case 0x3E:
			d.seg = "ds"
			d.pos++
			continue
		case 0x26:
			d.seg = "es"
			d.pos++
			continue
		case 0x64:
			d.seg = "fs"
			d.pos++
			continue
		case 0x65:
			d.seg = "gs"
			d.pos++
			continue
		}
		if d.is64 && b >= 0x40 && b <= 0x4f {
			d.hasRex = true
			d.rexW = b&0x08 != 0
			d.rexR = b&0x04 != 0
			d.rexX = b&0x02 != 0
			d.rexB = b&0x01 != 0
			d.pos++
		}
		break prefixes
	}
	if d.rexW {
		d.opSize = 64
	}

	op := d.u8()
	if d.trunc {
		return "", "", false
	}
	if op == 0x0f {
		return d.decode0F()
	}
	return d.decode1(op)
}

// ---- register naming ----

var reg8 = [...]string{"al", "cl", "dl", "bl", "ah", "ch", "dh", "bh"}
var reg8rex = [...]string{"al", "cl", "dl", "bl", "spl", "bpl", "sil", "dil",
	"r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"}
var reg16 = [...]string{"ax", "cx", "dx", "bx", "sp", "bp", "si", "di",
	"r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"}
var reg32 = [...]string{"eax", "ecx", "edx", "ebx", "esp", "ebp", "esi", "edi",
	"r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"}
var reg64 = [...]string{"rax", "rcx", "rdx", "rbx", "rsp", "rbp", "rsi", "rdi",
	"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"}

func (d *x86dec) regName(size, n int) string {
	switch size {
	case 8:
		if d.hasRex {
			return reg8rex[n&15]
		}
		return reg8[n&7]
	case 16:
		return reg16[n&15]
	case 64:
		return reg64[n&15]
	case 128:
		return fmt.Sprintf("xmm%d", n&15)
	default:
		return reg32[n&15]
	}
}

func accReg(size int) string {
	switch size {
	case 8:
		return "al"
	case 16:
		return "ax"
	case 64:
		return "rax"
	default:
		return "eax"
	}
}

func ptrSize(size int) string {
	switch size {
	case 8:
		return "byte ptr"
	case 16:
		return "word ptr"
	case 64:
		return "qword ptr"
	case 128:
		return "xmmword ptr"
	default:
		return "dword ptr"
	}
}

// ---- ModR/M + SIB ----

// modrm reads the ModR/M byte (and any SIB/displacement) and returns the
// extended reg field plus the textual r/m operand sized to rmSize.
func (d *x86dec) modrm(rmSize int) (int, string) {
	return d.modrmEx(rmSize, true)
}

func (d *x86dec) modrmEx(rmSize int, ptr bool) (int, string) {
	m := d.u8()
	mod := int(m >> 6)
	reg := int((m >> 3) & 7)
	if d.rexR {
		reg += 8
	}
	rm := int(m & 7)
	if mod == 3 {
		r := rm
		if d.rexB {
			r += 8
		}
		return reg, d.regName(rmSize, r)
	}
	if d.addrSize == 16 {
		return reg, d.mem16(mod, rm, rmSize, ptr)
	}
	return reg, d.mem32(mod, rm, rmSize, ptr)
}

func (d *x86dec) mem32(mod, rm, opsize int, ptr bool) string {
	var base, index string
	scale := 1
	dispSize := 0
	ripRel := false

	switch {
	case rm == 4:
		sib := d.u8()
		scale = 1 << ((sib >> 6) & 3)
		idx := int((sib >> 3) & 7)
		baseRaw := int(sib & 7)
		if !(idx == 4 && !d.rexX) {
			ir := idx
			if d.rexX {
				ir += 8
			}
			index = d.regName(d.addrSize, ir)
		}
		if baseRaw == 5 && mod == 0 {
			dispSize = 32
		} else {
			br := baseRaw
			if d.rexB {
				br += 8
			}
			base = d.regName(d.addrSize, br)
		}
	case rm == 5 && mod == 0:
		dispSize = 32
		if d.is64 {
			ripRel = true
		}
	default:
		br := rm
		if d.rexB {
			br += 8
		}
		base = d.regName(d.addrSize, br)
	}

	switch mod {
	case 1:
		if dispSize == 0 {
			dispSize = 8
		}
	case 2:
		if dispSize == 0 {
			dispSize = 32
		}
	}

	var disp int64
	hasDisp := dispSize != 0
	switch dispSize {
	case 8:
		disp = int64(int8(d.u8()))
	case 32:
		disp = int64(int32(d.u32()))
	}
	return d.formatMem(opsize, base, index, scale, disp, hasDisp, ripRel, ptr)
}

var amode16 = [...]string{"bx+si", "bx+di", "bp+si", "bp+di", "si", "di", "bp", "bx"}

func (d *x86dec) mem16(mod, rm, opsize int, ptr bool) string {
	base := amode16[rm]
	dispSize := 0
	if rm == 6 && mod == 0 {
		base = ""
		dispSize = 16
	}
	switch mod {
	case 1:
		dispSize = 8
	case 2:
		dispSize = 16
	}
	var disp int64
	hasDisp := dispSize != 0
	switch dispSize {
	case 8:
		disp = int64(int8(d.u8()))
	case 16:
		disp = int64(int16(d.u16()))
	}
	return d.formatMem(opsize, base, "", 1, disp, hasDisp, false, ptr)
}

func (d *x86dec) formatMem(opsize int, base, index string, scale int, disp int64, hasDisp, ripRel, ptr bool) string {
	var inner string
	if ripRel {
		d.hasRip = true
		d.ripDisp = disp
		inner = "rip" + signHex(disp)
	} else {
		first := true
		if base != "" {
			inner += base
			first = false
		}
		if index != "" {
			if !first {
				inner += "+"
			}
			inner += index
			if scale != 1 {
				inner += fmt.Sprintf("*%d", scale)
			}
			first = false
		}
		if hasDisp {
			if first {
				inner += fmt.Sprintf("0x%x", uint64(disp)&addrMask(d.addrSize))
			} else {
				inner += signHex(disp)
			}
		} else if first {
			inner += "0x0"
		}
	}
	seg := ""
	if d.seg != "" {
		seg = d.seg + ":"
	}
	if ptr {
		return ptrSize(opsize) + " " + seg + "[" + inner + "]"
	}
	return seg + "[" + inner + "]"
}

func addrMask(size int) uint64 {
	switch size {
	case 16:
		return 0xffff
	case 32:
		return 0xffffffff
	default:
		return ^uint64(0)
	}
}

func signHex(v int64) string {
	if v < 0 {
		return fmt.Sprintf("-0x%x", -v)
	}
	return fmt.Sprintf("+0x%x", v)
}

// ---- immediates as text ----

func (d *x86dec) immZ() string {
	if d.opSize == 16 {
		return fmt.Sprintf("0x%x", d.u16())
	}
	return fmt.Sprintf("0x%x", d.u32())
}

func (d *x86dec) immB() string  { return fmt.Sprintf("0x%x", d.u8()) }
func (d *x86dec) immBs() string { return fmt.Sprintf("0x%x", uint64(int64(int8(d.u8())))) }

// ---- condition codes ----

var ccName = [...]string{"o", "no", "b", "ae", "e", "ne", "be", "a",
	"s", "ns", "p", "np", "l", "ge", "le", "g"}

func (d *x86dec) rel8() string {
	rel := int64(int8(d.u8()))
	return fmt.Sprintf("0x%x", d.base+int64(d.pos)+rel)
}

func (d *x86dec) rel32() string {
	rel := int64(int32(d.u32()))
	return fmt.Sprintf("0x%x", d.base+int64(d.pos)+rel)
}

// stackSize is the operand size for implicit-stack ops (push/pop/call/jmp),
// which default to 64-bit in long mode.
func (d *x86dec) stackSize() int {
	if d.is64 {
		if d.has66 {
			return 16
		}
		return 64
	}
	return d.opSize
}

var aluName = [...]string{"add", "or", "adc", "sbb", "and", "sub", "xor", "cmp"}
var shiftName = [...]string{"rol", "ror", "rcl", "rcr", "shl", "shr", "shl", "sar"}

// ---- one-byte opcode map ----

func (d *x86dec) decode1(op uint8) (string, string, bool) {
	// ALU group: 0x00-0x3F in the standard 8-wide pattern.
	if op <= 0x3f && (op&7) <= 5 {
		name := aluName[(op>>3)&7]
		switch op & 7 {
		case 0:
			reg, rm := d.modrm(8)
			return name, rm + ", " + d.regName(8, reg), true
		case 1:
			reg, rm := d.modrm(d.opSize)
			return name, rm + ", " + d.regName(d.opSize, reg), true
		case 2:
			reg, rm := d.modrm(8)
			return name, d.regName(8, reg) + ", " + rm, true
		case 3:
			reg, rm := d.modrm(d.opSize)
			return name, d.regName(d.opSize, reg) + ", " + rm, true
		case 4:
			return name, "al, " + d.immB(), true
		case 5:
			return name, accReg(d.opSize) + ", " + d.immZ(), true
		}
	}

	switch {
	case op == 0x0f:
		return d.decode0F()

	// INC/DEC reg (32-bit only; REX in 64-bit is handled as a prefix).
	case op >= 0x40 && op <= 0x4f && !d.is64:
		name := "inc"
		if op >= 0x48 {
			name = "dec"
		}
		return name, d.regName(d.opSize, int(op&7)), true

	case op >= 0x50 && op <= 0x57:
		r := int(op - 0x50)
		if d.rexB {
			r += 8
		}
		return "push", d.regName(d.stackSize(), r), true
	case op >= 0x58 && op <= 0x5f:
		r := int(op - 0x58)
		if d.rexB {
			r += 8
		}
		return "pop", d.regName(d.stackSize(), r), true

	case op == 0x63: // MOVSXD (64) / ARPL (32)
		if d.is64 {
			reg, rm := d.modrm(32)
			return "movsxd", d.regName(64, reg) + ", " + rm, true
		}
		reg, rm := d.modrm(16)
		return "arpl", rm + ", " + d.regName(16, reg), true

	case op == 0x68:
		return "push", d.immZ(), true
	case op == 0x6a:
		return "push", d.immBs(), true
	case op == 0x69:
		reg, rm := d.modrm(d.opSize)
		return "imul", d.regName(d.opSize, reg) + ", " + rm + ", " + d.immZ(), true
	case op == 0x6b:
		reg, rm := d.modrm(d.opSize)
		return "imul", d.regName(d.opSize, reg) + ", " + rm + ", " + d.immBs(), true

	case op >= 0x70 && op <= 0x7f:
		return "j" + ccName[op-0x70], d.rel8(), true

	case op == 0x80: // GRP1 Eb, ib
		reg, rm := d.modrm(8)
		return aluName[reg&7], rm + ", " + d.immB(), true
	case op == 0x81: // GRP1 Ev, iz
		reg, rm := d.modrm(d.opSize)
		return aluName[reg&7], rm + ", " + d.immZ(), true
	case op == 0x82: // alias of 0x80
		reg, rm := d.modrm(8)
		return aluName[reg&7], rm + ", " + d.immB(), true
	case op == 0x83: // GRP1 Ev, ib (sign-extended)
		reg, rm := d.modrm(d.opSize)
		return aluName[reg&7], rm + ", " + d.immBs(), true

	case op == 0x84:
		reg, rm := d.modrm(8)
		return "test", rm + ", " + d.regName(8, reg), true
	case op == 0x85:
		reg, rm := d.modrm(d.opSize)
		return "test", rm + ", " + d.regName(d.opSize, reg), true
	case op == 0x86:
		reg, rm := d.modrm(8)
		return "xchg", rm + ", " + d.regName(8, reg), true
	case op == 0x87:
		reg, rm := d.modrm(d.opSize)
		return "xchg", rm + ", " + d.regName(d.opSize, reg), true

	case op == 0x88:
		reg, rm := d.modrm(8)
		return "mov", rm + ", " + d.regName(8, reg), true
	case op == 0x89:
		reg, rm := d.modrm(d.opSize)
		return "mov", rm + ", " + d.regName(d.opSize, reg), true
	case op == 0x8a:
		reg, rm := d.modrm(8)
		return "mov", d.regName(8, reg) + ", " + rm, true
	case op == 0x8b:
		reg, rm := d.modrm(d.opSize)
		return "mov", d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0x8c: // mov Ew, Sreg
		reg, rm := d.modrm(16)
		return "mov", rm + ", " + segReg(reg), true
	case op == 0x8d: // lea
		reg, rm := d.modrmEx(d.opSize, false)
		return "lea", d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0x8e: // mov Sreg, Ew
		reg, rm := d.modrm(16)
		return "mov", segReg(reg) + ", " + rm, true
	case op == 0x8f: // GRP1A pop Ev
		_, rm := d.modrm(d.stackSize())
		return "pop", rm, true

	case op == 0x90:
		if d.rep == "rep" {
			return "pause", "", true
		}
		if !d.rexB {
			return "nop", "", true
		}
		fallthrough
	case op >= 0x91 && op <= 0x97:
		r := int(op - 0x90)
		if d.rexB {
			r += 8
		}
		return "xchg", accReg(d.opSize) + ", " + d.regName(d.opSize, r), true
	case op == 0x98:
		return map[int]string{16: "cbw", 32: "cwde", 64: "cdqe"}[d.opSize], "", true
	case op == 0x99:
		return map[int]string{16: "cwd", 32: "cdq", 64: "cqo"}[d.opSize], "", true
	case op == 0x9b:
		return "fwait", "", true
	case op == 0x9c:
		return "pushf", "", true
	case op == 0x9d:
		return "popf", "", true
	case op == 0x9e:
		return "sahf", "", true
	case op == 0x9f:
		return "lahf", "", true

	case op == 0xa0:
		return "mov", "al, " + d.moffs(), true
	case op == 0xa1:
		return "mov", accReg(d.opSize) + ", " + d.moffs(), true
	case op == 0xa2:
		return "mov", d.moffs() + ", al", true
	case op == 0xa3:
		return "mov", d.moffs() + ", " + accReg(d.opSize), true
	case op == 0xa4:
		return d.repPfx() + "movsb", "", true
	case op == 0xa5:
		return d.repPfx() + "movs" + sizeSuffix(d.opSize), "", true
	case op == 0xa6:
		return d.repPfx() + "cmpsb", "", true
	case op == 0xa7:
		return d.repPfx() + "cmps" + sizeSuffix(d.opSize), "", true
	case op == 0xa8:
		return "test", "al, " + d.immB(), true
	case op == 0xa9:
		return "test", accReg(d.opSize) + ", " + d.immZ(), true
	case op == 0xaa:
		return d.repPfx() + "stosb", "", true
	case op == 0xab:
		return d.repPfx() + "stos" + sizeSuffix(d.opSize), "", true
	case op == 0xac:
		return d.repPfx() + "lodsb", "", true
	case op == 0xad:
		return d.repPfx() + "lods" + sizeSuffix(d.opSize), "", true
	case op == 0xae:
		return d.repPfx() + "scasb", "", true
	case op == 0xaf:
		return d.repPfx() + "scas" + sizeSuffix(d.opSize), "", true

	case op >= 0xb0 && op <= 0xb7:
		r := int(op - 0xb0)
		if d.rexB {
			r += 8
		}
		return "mov", d.regName(8, r) + ", " + d.immB(), true
	case op >= 0xb8 && op <= 0xbf:
		r := int(op - 0xb8)
		if d.rexB {
			r += 8
		}
		if d.opSize == 64 {
			return "movabs", d.regName(64, r) + fmt.Sprintf(", 0x%x", d.u64()), true
		}
		return "mov", d.regName(d.opSize, r) + ", " + d.immZ(), true

	case op == 0xc0: // GRP2 Eb, ib
		reg, rm := d.modrm(8)
		return shiftName[reg&7], rm + ", " + d.immB(), true
	case op == 0xc1: // GRP2 Ev, ib
		reg, rm := d.modrm(d.opSize)
		return shiftName[reg&7], rm + ", " + d.immB(), true
	case op == 0xc2:
		return "ret", fmt.Sprintf("0x%x", d.u16()), true
	case op == 0xc3:
		return "ret", "", true
	case op == 0xc6: // GRP11 mov Eb, ib
		_, rm := d.modrm(8)
		return "mov", rm + ", " + d.immB(), true
	case op == 0xc7: // GRP11 mov Ev, iz
		_, rm := d.modrm(d.opSize)
		return "mov", rm + ", " + d.immZ(), true
	case op == 0xc8:
		iw := d.u16()
		ib := d.u8()
		return "enter", fmt.Sprintf("0x%x, 0x%x", iw, ib), true
	case op == 0xc9:
		return "leave", "", true
	case op == 0xca:
		return "retf", fmt.Sprintf("0x%x", d.u16()), true
	case op == 0xcb:
		return "retf", "", true
	case op == 0xcc:
		return "int3", "", true
	case op == 0xcd:
		return "int", d.immB(), true
	case op == 0xce:
		return "into", "", true
	case op == 0xcf:
		return "iret", "", true

	case op == 0xd0: // GRP2 Eb, 1
		reg, rm := d.modrm(8)
		return shiftName[reg&7], rm + ", 1", true
	case op == 0xd1: // GRP2 Ev, 1
		reg, rm := d.modrm(d.opSize)
		return shiftName[reg&7], rm + ", 1", true
	case op == 0xd2: // GRP2 Eb, cl
		reg, rm := d.modrm(8)
		return shiftName[reg&7], rm + ", cl", true
	case op == 0xd3: // GRP2 Ev, cl
		reg, rm := d.modrm(d.opSize)
		return shiftName[reg&7], rm + ", cl", true
	case op == 0xd7:
		return "xlat", "", true
	case op >= 0xd8 && op <= 0xdf: // x87 — consume ModR/M for length, name generic
		_, rm := d.modrmEx(32, true)
		return "fpu", rm, false

	case op == 0xe0:
		return "loopne", d.rel8(), true
	case op == 0xe1:
		return "loope", d.rel8(), true
	case op == 0xe2:
		return "loop", d.rel8(), true
	case op == 0xe3:
		if d.addrSize == 32 {
			return "jecxz", d.rel8(), true
		}
		return "jrcxz", d.rel8(), true
	case op == 0xe4:
		return "in", "al, " + d.immB(), true
	case op == 0xe5:
		return "in", accReg(d.opSize) + ", " + d.immB(), true
	case op == 0xe6:
		return "out", d.immB() + ", al", true
	case op == 0xe7:
		return "out", d.immB() + ", " + accReg(d.opSize), true
	case op == 0xe8:
		return "call", d.rel32(), true
	case op == 0xe9:
		return "jmp", d.rel32(), true
	case op == 0xeb:
		return "jmp", d.rel8(), true
	case op == 0xec:
		return "in", "al, dx", true
	case op == 0xed:
		return "in", accReg(d.opSize) + ", dx", true
	case op == 0xee:
		return "out", "dx, al", true
	case op == 0xef:
		return "out", "dx, " + accReg(d.opSize), true

	case op == 0xf1:
		return "int1", "", true
	case op == 0xf4:
		return "hlt", "", true
	case op == 0xf5:
		return "cmc", "", true
	case op == 0xf6:
		return d.grp3(8)
	case op == 0xf7:
		return d.grp3(d.opSize)
	case op == 0xf8:
		return "clc", "", true
	case op == 0xf9:
		return "stc", "", true
	case op == 0xfa:
		return "cli", "", true
	case op == 0xfb:
		return "sti", "", true
	case op == 0xfc:
		return "cld", "", true
	case op == 0xfd:
		return "std", "", true
	case op == 0xfe: // GRP4
		reg, rm := d.modrm(8)
		if reg&7 == 0 {
			return "inc", rm, true
		}
		return "dec", rm, true
	case op == 0xff: // GRP5
		return d.grp5()
	}
	return "", "", false
}

func (d *x86dec) repPfx() string {
	if d.rep == "rep" {
		return "rep "
	}
	if d.rep == "repne" {
		return "repne "
	}
	return ""
}

func sizeSuffix(size int) string {
	switch size {
	case 16:
		return "w"
	case 64:
		return "q"
	default:
		return "d"
	}
}

func segReg(n int) string {
	names := [...]string{"es", "cs", "ss", "ds", "fs", "gs", "?", "?"}
	return names[n&7]
}

func (d *x86dec) moffs() string {
	var off uint64
	switch d.addrSize {
	case 16:
		off = uint64(d.u16())
	case 32:
		off = uint64(d.u32())
	default:
		off = d.u64()
	}
	seg := ""
	if d.seg != "" {
		seg = d.seg + ":"
	}
	return fmt.Sprintf("%s[0x%x]", seg, off)
}

func (d *x86dec) grp3(size int) (string, string, bool) {
	reg, rm := d.modrm(size)
	switch reg & 7 {
	case 0, 1:
		if size == 8 {
			return "test", rm + ", " + d.immB(), true
		}
		return "test", rm + ", " + d.immZ(), true
	case 2:
		return "not", rm, true
	case 3:
		return "neg", rm, true
	case 4:
		return "mul", rm, true
	case 5:
		return "imul", rm, true
	case 6:
		return "div", rm, true
	default:
		return "idiv", rm, true
	}
}

func (d *x86dec) grp5() (string, string, bool) {
	// Peek reg field without committing size, then decode appropriately.
	m, ok := d.peek()
	if !ok {
		return "", "", false
	}
	reg := int((m >> 3) & 7)
	switch reg {
	case 0:
		_, rm := d.modrm(d.opSize)
		return "inc", rm, true
	case 1:
		_, rm := d.modrm(d.opSize)
		return "dec", rm, true
	case 2:
		_, rm := d.modrm(d.stackSize())
		return "call", rm, true
	case 3:
		_, rm := d.modrm(d.opSize)
		return "callf", rm, true
	case 4:
		_, rm := d.modrm(d.stackSize())
		return "jmp", rm, true
	case 5:
		_, rm := d.modrm(d.opSize)
		return "jmpf", rm, true
	case 6:
		_, rm := d.modrm(d.stackSize())
		return "push", rm, true
	default:
		_, rm := d.modrm(d.opSize)
		return "(bad)", rm, false
	}
}

// ---- two-byte 0F map ----

func (d *x86dec) decode0F() (string, string, bool) {
	op := d.u8()
	if d.trunc {
		return "", "", false
	}
	switch {
	case op == 0x05:
		return "syscall", "", true
	case op == 0x06:
		return "clts", "", true
	case op == 0x07:
		return "sysret", "", true
	case op == 0x08:
		return "invd", "", true
	case op == 0x09:
		return "wbinvd", "", true
	case op == 0x0b:
		return "ud2", "", true
	case op == 0x1f: // multi-byte NOP
		_, rm := d.modrm(d.opSize)
		return "nop", rm, true
	case op == 0x1e && d.rep == "rep": // ENDBR32/ENDBR64 (f3 0f 1e fa/fb)
		m := d.u8()
		if m == 0xfa {
			return "endbr64", "", true
		}
		if m == 0xfb {
			return "endbr32", "", true
		}
		return "nop", "", true
	case op >= 0x18 && op <= 0x1e: // hints / prefetch
		_, rm := d.modrmEx(8, true)
		return "nop", rm, true
	case op == 0x31:
		return "rdtsc", "", true
	case op == 0x34:
		return "sysenter", "", true
	case op == 0x35:
		return "sysexit", "", true
	case op == 0xa2:
		return "cpuid", "", true
	case op == 0x77:
		return "emms", "", true
	case op == 0xa0:
		return "push", "fs", true
	case op == 0xa1:
		return "pop", "fs", true
	case op == 0xa8:
		return "push", "gs", true
	case op == 0xa9:
		return "pop", "gs", true

	case op >= 0x40 && op <= 0x4f: // CMOVcc
		reg, rm := d.modrm(d.opSize)
		return "cmov" + ccName[op-0x40], d.regName(d.opSize, reg) + ", " + rm, true
	case op >= 0x80 && op <= 0x8f: // Jcc rel32
		return "j" + ccName[op-0x80], d.rel32(), true
	case op >= 0x90 && op <= 0x9f: // SETcc Eb
		_, rm := d.modrm(8)
		return "set" + ccName[op-0x90], rm, true

	case op == 0xa3:
		reg, rm := d.modrm(d.opSize)
		return "bt", rm + ", " + d.regName(d.opSize, reg), true
	case op == 0xab:
		reg, rm := d.modrm(d.opSize)
		return "bts", rm + ", " + d.regName(d.opSize, reg), true
	case op == 0xb3:
		reg, rm := d.modrm(d.opSize)
		return "btr", rm + ", " + d.regName(d.opSize, reg), true
	case op == 0xbb:
		reg, rm := d.modrm(d.opSize)
		return "btc", rm + ", " + d.regName(d.opSize, reg), true
	case op == 0xba: // GRP8 Ev, ib
		reg, rm := d.modrm(d.opSize)
		names := map[int]string{4: "bt", 5: "bts", 6: "btr", 7: "btc"}
		if n, okn := names[reg&7]; okn {
			return n, rm + ", " + d.immB(), true
		}
		return "(bad)", rm, false

	case op == 0xa4:
		reg, rm := d.modrm(d.opSize)
		return "shld", rm + ", " + d.regName(d.opSize, reg) + ", " + d.immB(), true
	case op == 0xa5:
		reg, rm := d.modrm(d.opSize)
		return "shld", rm + ", " + d.regName(d.opSize, reg) + ", cl", true
	case op == 0xac:
		reg, rm := d.modrm(d.opSize)
		return "shrd", rm + ", " + d.regName(d.opSize, reg) + ", " + d.immB(), true
	case op == 0xad:
		reg, rm := d.modrm(d.opSize)
		return "shrd", rm + ", " + d.regName(d.opSize, reg) + ", cl", true
	case op == 0xaf:
		reg, rm := d.modrm(d.opSize)
		return "imul", d.regName(d.opSize, reg) + ", " + rm, true

	case op == 0xb0:
		reg, rm := d.modrm(8)
		return "cmpxchg", rm + ", " + d.regName(8, reg), true
	case op == 0xb1:
		reg, rm := d.modrm(d.opSize)
		return "cmpxchg", rm + ", " + d.regName(d.opSize, reg), true
	case op == 0xb6:
		reg, rm := d.modrm(8)
		return "movzx", d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0xb7:
		reg, rm := d.modrm(16)
		return "movzx", d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0xbe:
		reg, rm := d.modrm(8)
		return "movsx", d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0xbf:
		reg, rm := d.modrm(16)
		return "movsx", d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0xbc:
		reg, rm := d.modrm(d.opSize)
		name := "bsf"
		if d.rep == "rep" {
			name = "tzcnt"
		}
		return name, d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0xbd:
		reg, rm := d.modrm(d.opSize)
		name := "bsr"
		if d.rep == "rep" {
			name = "lzcnt"
		}
		return name, d.regName(d.opSize, reg) + ", " + rm, true
	case op == 0xc0:
		reg, rm := d.modrm(8)
		return "xadd", rm + ", " + d.regName(8, reg), true
	case op == 0xc1:
		reg, rm := d.modrm(d.opSize)
		return "xadd", rm + ", " + d.regName(d.opSize, reg), true
	case op >= 0xc8 && op <= 0xcf:
		r := int(op - 0xc8)
		if d.rexB {
			r += 8
		}
		return "bswap", d.regName(d.opSize, r), true
	}
	// Common SSE / SSE2 (XMM) subset.
	if mn, ops, ok := d.sse0F(op); ok {
		return mn, ops, true
	}
	return "", "", false
}

// sse0F decodes the common SSE/SSE2 instructions. Mnemonics depend on the
// mandatory prefix (none / 0x66 / 0xF3 / 0xF2).
func (d *x86dec) sse0F(op uint8) (string, string, bool) {
	pfx := "none"
	switch {
	case d.has66:
		pfx = "66"
	case d.rep == "rep":
		pfx = "f3"
	case d.rep == "repne":
		pfx = "f2"
	}

	xmmRM := func(name string, store bool) (string, string, bool) {
		reg, rm := d.modrm(128)
		r := d.regName(128, reg)
		if store {
			return name, rm + ", " + r, true
		}
		return name, r + ", " + rm, true
	}

	switch op {
	case 0x10:
		return xmmRM(map[string]string{"none": "movups", "66": "movupd", "f3": "movss", "f2": "movsd"}[pfx], false)
	case 0x11:
		return xmmRM(map[string]string{"none": "movups", "66": "movupd", "f3": "movss", "f2": "movsd"}[pfx], true)
	case 0x28:
		return xmmRM(map[string]string{"none": "movaps", "66": "movapd"}[pfx2(pfx)], false)
	case 0x29:
		return xmmRM(map[string]string{"none": "movaps", "66": "movapd"}[pfx2(pfx)], true)
	case 0x2e:
		return xmmRM(map[string]string{"none": "ucomiss", "66": "ucomisd"}[pfx2(pfx)], false)
	case 0x2f:
		return xmmRM(map[string]string{"none": "comiss", "66": "comisd"}[pfx2(pfx)], false)
	case 0x51:
		return xmmRM(sseScalar("sqrt", pfx), false)
	case 0x54:
		return xmmRM(map[string]string{"none": "andps", "66": "andpd"}[pfx2(pfx)], false)
	case 0x57:
		return xmmRM(map[string]string{"none": "xorps", "66": "xorpd"}[pfx2(pfx)], false)
	case 0x58:
		return xmmRM(sseScalar("add", pfx), false)
	case 0x59:
		return xmmRM(sseScalar("mul", pfx), false)
	case 0x5c:
		return xmmRM(sseScalar("sub", pfx), false)
	case 0x5e:
		return xmmRM(sseScalar("div", pfx), false)
	case 0x5d:
		return xmmRM(sseScalar("min", pfx), false)
	case 0x5f:
		return xmmRM(sseScalar("max", pfx), false)
	case 0x6e: // movd/movq xmm, r/m
		sz := 32
		name := "movd"
		if d.opSize == 64 {
			sz, name = 64, "movq"
		}
		reg, rm := d.modrm(sz)
		return name, d.regName(128, reg) + ", " + rm, true
	case 0x7e:
		if pfx == "f3" { // movq xmm, xmm/m64
			return xmmRM("movq", false)
		}
		sz := 32
		name := "movd"
		if d.opSize == 64 {
			sz, name = 64, "movq"
		}
		reg, rm := d.modrm(sz)
		return name, rm + ", " + d.regName(128, reg), true
	case 0x6f:
		return xmmRM(map[string]string{"66": "movdqa", "f3": "movdqu"}[pfx], false)
	case 0x7f:
		return xmmRM(map[string]string{"66": "movdqa", "f3": "movdqu"}[pfx], true)
	case 0xd6:
		return xmmRM("movq", true)
	case 0xef:
		return xmmRM("pxor", false)
	case 0x76:
		return xmmRM("pcmpeqd", false)
	case 0x74:
		return xmmRM("pcmpeqb", false)
	case 0xfc:
		return xmmRM("paddb", false)
	case 0xfe:
		return xmmRM("paddd", false)
	case 0x12:
		return xmmRM(map[string]string{"none": "movlps", "66": "movlpd"}[pfx2(pfx)], false)
	case 0x13:
		return xmmRM(map[string]string{"none": "movlps", "66": "movlpd"}[pfx2(pfx)], true)
	case 0x16:
		return xmmRM(map[string]string{"none": "movhps", "66": "movhpd"}[pfx2(pfx)], false)
	case 0x17:
		return xmmRM(map[string]string{"none": "movhps", "66": "movhpd"}[pfx2(pfx)], true)
	case 0x14:
		return xmmRM(map[string]string{"none": "unpcklps", "66": "unpcklpd"}[pfx2(pfx)], false)
	case 0x2a:
		reg, rm := d.modrm(d.opSize)
		return sseScalar("cvtsi2", pfx), d.regName(128, reg) + ", " + rm, true
	case 0x2c, 0x2d:
		reg, rm := d.modrm(128)
		return "cvt", d.regName(d.opSize, reg) + ", " + rm, true
	}
	return "", "", false
}

// pfx2 collapses f3/f2 to "none" for instructions that only vary between the
// no-prefix and 0x66 forms.
func pfx2(p string) string {
	if p == "66" {
		return "66"
	}
	return "none"
}

func sseScalar(base, pfx string) string {
	switch pfx {
	case "f3":
		return base + "ss"
	case "f2":
		return base + "sd"
	case "66":
		return base + "pd"
	default:
		return base + "ps"
	}
}
