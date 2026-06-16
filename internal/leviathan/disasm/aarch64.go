package disasm

import (
	"encoding/binary"
	"fmt"
)

// diAArch64 is a heuristic AArch64 (ARMv8 A64) decoder. It recognises a
// subset of instructions commonly seen in disassembly and falls back to
// ".word 0x…" for everything else. 32-bit fixed-width little-endian.
func diAArch64(buf []byte, base int64, maxInstr int) []map[string]any {
	out := []map[string]any{}
	i := 0
	for i+4 <= len(buf) && len(out) < maxInstr {
		w := binary.LittleEndian.Uint32(buf[i : i+4])
		mn, ops, conf := decodeA64(w, base+int64(i))
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i : i+4]),
			"mnemonic":  mn,
			"operands":  ops,
			"confident": conf,
		})
		i += 4
	}
	// Tail bytes (not multiple of 4).
	for i < len(buf) && len(out) < maxInstr {
		end := i + 1
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i:end]),
			"mnemonic":  "db",
			"operands":  hexCommaList(buf[i:end]),
			"confident": false,
		})
		i++
	}
	return out
}

func decodeA64(w uint32, pc int64) (string, string, bool) {
	// NOP
	if w == 0xd503201f {
		return "nop", "", true
	}
	// RET Xn (default x30)
	if w&0xfffffc1f == 0xd65f0000 {
		rn := (w >> 5) & 0x1f
		if rn == 30 {
			return "ret", "", true
		}
		return "ret", regX(rn), true
	}
	// BR Xn
	if w&0xfffffc1f == 0xd61f0000 {
		return "br", regX((w >> 5) & 0x1f), true
	}
	// BLR Xn
	if w&0xfffffc1f == 0xd63f0000 {
		return "blr", regX((w >> 5) & 0x1f), true
	}
	// B / BL (unconditional branch, imm26 * 4, signed)
	if w&0x7c000000 == 0x14000000 {
		imm := signExtend64(uint64(w&0x03ffffff)<<2, 28)
		target := pc + int64(imm)
		if w&0x80000000 != 0 {
			return "bl", fmt.Sprintf("0x%x", target), true
		}
		return "b", fmt.Sprintf("0x%x", target), true
	}
	// B.cond (conditional branch, imm19*4)
	if w&0xff000010 == 0x54000000 {
		cond := int(w & 0xf)
		imm := signExtend64(uint64((w>>5)&0x7ffff)<<2, 21)
		return "b." + condName(cond), fmt.Sprintf("0x%x", pc+int64(imm)), true
	}
	// CBZ / CBNZ
	if w&0x7e000000 == 0x34000000 {
		sf := (w >> 31) & 1
		op := (w >> 24) & 1
		rt := w & 0x1f
		imm := signExtend64(uint64((w>>5)&0x7ffff)<<2, 21)
		mn := "cbz"
		if op == 1 {
			mn = "cbnz"
		}
		r := regX(rt)
		if sf == 0 {
			r = regW(rt)
		}
		return mn, fmt.Sprintf("%s, 0x%x", r, pc+int64(imm)), true
	}
	// MOVZ / MOVN / MOVK (wide immediate)
	if w&0x1f800000 == 0x12800000 {
		sf := (w >> 31) & 1
		opc := (w >> 29) & 0x3
		hw := (w >> 21) & 0x3
		imm16 := (w >> 5) & 0xffff
		rd := w & 0x1f
		mn := "movn"
		switch opc {
		case 0b10:
			mn = "movz"
		case 0b11:
			mn = "movk"
		}
		shift := hw * 16
		r := regX(rd)
		if sf == 0 {
			r = regW(rd)
		}
		if shift == 0 {
			return mn, fmt.Sprintf("%s, #0x%x", r, imm16), true
		}
		return mn, fmt.Sprintf("%s, #0x%x, lsl #%d", r, imm16, shift), true
	}
	// ADR / ADRP
	if w&0x1f000000 == 0x10000000 {
		page := (w >> 31) & 1
		rd := w & 0x1f
		immlo := uint64((w >> 29) & 0x3)
		immhi := uint64((w >> 5) & 0x7ffff)
		imm := (immhi << 2) | immlo
		mn := "adr"
		shift := uint(21)
		signed := signExtend64(imm, shift)
		if page == 1 {
			mn = "adrp"
			signed = signExtend64(imm<<12, 33)
			// adrp target is page-aligned: pc & ~0xFFF
			return mn, fmt.Sprintf("%s, 0x%x", regX(rd), (pc & ^int64(0xfff))+int64(signed)), true
		}
		return mn, fmt.Sprintf("%s, 0x%x", regX(rd), pc+int64(signed)), true
	}
	// ADD / SUB (immediate)
	if w&0x1f000000 == 0x11000000 {
		sf := (w >> 31) & 1
		op := (w >> 30) & 1
		sh := (w >> 22) & 1
		imm12 := (w >> 10) & 0xfff
		rn := (w >> 5) & 0x1f
		rd := w & 0x1f
		mn := "add"
		if op == 1 {
			mn = "sub"
		}
		lhs := regX(rd)
		mid := regX(rn)
		if sf == 0 {
			lhs = regW(rd)
			mid = regW(rn)
		}
		if sh == 1 {
			return mn, fmt.Sprintf("%s, %s, #0x%x, lsl #12", lhs, mid, imm12), true
		}
		return mn, fmt.Sprintf("%s, %s, #0x%x", lhs, mid, imm12), true
	}
	// LDR / STR (unsigned offset, 32/64-bit int)
	if w&0x3fc00000 == 0x39400000 || w&0x3fc00000 == 0x39000000 {
		size := (w >> 30) & 0x3
		l := (w >> 22) & 1
		imm12 := (w >> 10) & 0xfff
		rn := (w >> 5) & 0x1f
		rt := w & 0x1f
		var mn string
		if l == 1 {
			mn = "ldr"
		} else {
			mn = "str"
		}
		scale := int(1 << size)
		imm := int(imm12) * scale
		var reg string
		if size == 3 {
			reg = regX(rt)
		} else {
			reg = regW(rt)
		}
		base := regXorSP(rn)
		if imm == 0 {
			return mn, fmt.Sprintf("%s, [%s]", reg, base), true
		}
		return mn, fmt.Sprintf("%s, [%s, #0x%x]", reg, base, imm), true
	}
	// LDR (literal / PC-rel)
	if w&0xbf000000 == 0x18000000 {
		sf := (w >> 30) & 1
		imm := signExtend64(uint64((w>>5)&0x7ffff)<<2, 21)
		rt := w & 0x1f
		reg := regW(rt)
		if sf == 1 {
			reg = regX(rt)
		}
		return "ldr", fmt.Sprintf("%s, 0x%x", reg, pc+int64(imm)), true
	}
	// ORR / AND / EOR (shifted reg) — simplified detection, no shifts reported.
	if w&0x7f200000 == 0x2a000000 { // ORR (32/64) w/ shift=0
		sf := (w >> 31) & 1
		rm := (w >> 16) & 0x1f
		rn := (w >> 5) & 0x1f
		rd := w & 0x1f
		// MOV (register) alias: ORR Xd, XZR, Xm  →  MOV Xd, Xm  (when Rn==31 and shift==0)
		if rn == 31 && ((w>>10)&0x3f) == 0 && ((w>>22)&0x3) == 0 {
			if sf == 1 {
				return "mov", fmt.Sprintf("%s, %s", regX(rd), regX(rm)), true
			}
			return "mov", fmt.Sprintf("%s, %s", regW(rd), regW(rm)), true
		}
		if sf == 1 {
			return "orr", fmt.Sprintf("%s, %s, %s", regX(rd), regX(rn), regX(rm)), true
		}
		return "orr", fmt.Sprintf("%s, %s, %s", regW(rd), regW(rn), regW(rm)), true
	}
	// SVC #imm16
	if w&0xffe0001f == 0xd4000001 {
		imm16 := (w >> 5) & 0xffff
		return "svc", fmt.Sprintf("#0x%x", imm16), true
	}
	// BRK #imm16
	if w&0xffe0001f == 0xd4200000 {
		imm16 := (w >> 5) & 0xffff
		return "brk", fmt.Sprintf("#0x%x", imm16), true
	}
	// HINT family → treat common ones
	if w&0xfffff01f == 0xd503201f {
		op := (w >> 5) & 0x7f
		switch op {
		case 0:
			return "nop", "", true
		case 1:
			return "yield", "", true
		case 2:
			return "wfe", "", true
		case 3:
			return "wfi", "", true
		case 4:
			return "sev", "", true
		case 5:
			return "sevl", "", true
		}
		return "hint", fmt.Sprintf("#0x%x", op), true
	}
	// Fallback
	return ".word", fmt.Sprintf("0x%08x", w), false
}

func regX(r uint32) string {
	r &= 0x1f
	if r == 31 {
		return "xzr"
	}
	return fmt.Sprintf("x%d", r)
}
func regW(r uint32) string {
	r &= 0x1f
	if r == 31 {
		return "wzr"
	}
	return fmt.Sprintf("w%d", r)
}

// regXorSP is used for the "Rn" slot in memory ops where 31 means SP, not XZR.
func regXorSP(r uint32) string {
	r &= 0x1f
	if r == 31 {
		return "sp"
	}
	return fmt.Sprintf("x%d", r)
}

func condName(c int) string {
	names := []string{
		"eq", "ne", "cs", "cc", "mi", "pl", "vs", "vc",
		"hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
	}
	if c >= 0 && c < 16 {
		return names[c]
	}
	return fmt.Sprintf("c%d", c)
}

func signExtend64(v uint64, bits uint) int64 {
	mask := uint64(1) << (bits - 1)
	if v&mask != 0 {
		v |= ^uint64(0) << bits
	}
	return int64(v)
}
