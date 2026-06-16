package disasm

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// diARM32 is a heuristic ARM state decoder for 32-bit little-endian ARM
// instructions. Thumb/T32 is intentionally not decoded here.
func diARM32(buf []byte, base int64, maxInstr int) []map[string]any {
	out := []map[string]any{}
	i := 0
	for i+4 <= len(buf) && len(out) < maxInstr {
		w := binary.LittleEndian.Uint32(buf[i : i+4])
		mn, ops, conf := decodeARM32(w, base+int64(i))
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i : i+4]),
			"mnemonic":  mn,
			"operands":  ops,
			"confident": conf,
		})
		i += 4
	}
	for i < len(buf) && len(out) < maxInstr {
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i : i+1]),
			"mnemonic":  "db",
			"operands":  hexCommaList(buf[i : i+1]),
			"confident": false,
		})
		i++
	}
	return out
}

func decodeARM32(w uint32, pc int64) (string, string, bool) {
	cond := (w >> 28) & 0xf
	suf := armCondSuffix(cond)
	if cond == 0xf {
		suf = ".nv"
	}
	// BX Rn
	if w&0x0ffffff0 == 0x012fff10 {
		return "bx" + suf, armReg(w & 0xf), true
	}
	// SVC #imm24
	if w&0x0f000000 == 0x0f000000 {
		return "svc" + suf, fmt.Sprintf("#0x%x", w&0x00ffffff), true
	}
	// B / BL, offset is sign-extended imm24 << 2 from PC+8.
	if w&0x0e000000 == 0x0a000000 {
		imm := signExtend64(uint64(w&0x00ffffff)<<2, 26)
		target := pc + 8 + int64(imm)
		if w&0x01000000 != 0 {
			return "bl" + suf, fmt.Sprintf("0x%x", target), true
		}
		return "b" + suf, fmt.Sprintf("0x%x", target), true
	}
	// PUSH/POP common aliases.
	if w&0xffff0000 == 0xe92d0000 {
		return "push", armRegList(w & 0xffff), true
	}
	if w&0xffff0000 == 0xe8bd0000 {
		return "pop", armRegList(w & 0xffff), true
	}
	// LDR/STR immediate offset.
	if w&0x0c000000 == 0x04000000 {
		p := (w >> 24) & 1
		u := (w >> 23) & 1
		b := (w >> 22) & 1
		wb := (w >> 21) & 1
		l := (w >> 20) & 1
		rn := (w >> 16) & 0xf
		rd := (w >> 12) & 0xf
		imm := w & 0xfff
		mn := "str"
		if l == 1 {
			mn = "ldr"
		}
		if b == 1 {
			mn += "b"
		}
		mn += suf
		sign := ""
		if u == 0 {
			sign = "-"
		}
		addr := ""
		switch {
		case p == 1 && imm == 0 && wb == 0:
			addr = fmt.Sprintf("[%s]", armReg(rn))
		case p == 1:
			addr = fmt.Sprintf("[%s, #%s0x%x]", armReg(rn), sign, imm)
			if wb == 1 {
				addr += "!"
			}
		default:
			addr = fmt.Sprintf("[%s], #%s0x%x", armReg(rn), sign, imm)
		}
		return mn, fmt.Sprintf("%s, %s", armReg(rd), addr), true
	}
	// Data processing immediate/register subset.
	if w&0x0c000000 == 0x00000000 || w&0x0e000000 == 0x02000000 {
		immediate := w&0x02000000 != 0
		opcode := (w >> 21) & 0xf
		s := (w >> 20) & 1
		rn := (w >> 16) & 0xf
		rd := (w >> 12) & 0xf
		mn := armDataProcName(opcode)
		if mn == "" {
			return ".word", fmt.Sprintf("0x%08x", w), false
		}
		mn += suf
		if s == 1 && opcode != 8 && opcode != 9 && opcode != 10 && opcode != 11 {
			mn += "s"
		}
		op2 := ""
		if immediate {
			imm8 := w & 0xff
			rot := ((w >> 8) & 0xf) * 2
			imm := (imm8 >> rot) | (imm8 << ((32 - rot) & 31))
			op2 = fmt.Sprintf("#0x%x", imm)
		} else {
			rm := w & 0xf
			shift := (w >> 4) & 0xff
			if shift == 0 {
				op2 = armReg(rm)
			} else {
				op2 = fmt.Sprintf("%s, %s", armReg(rm), armShift(shift))
			}
		}
		switch opcode {
		case 8, 9, 10, 11: // tst/teq/cmp/cmn
			return mn, fmt.Sprintf("%s, %s", armReg(rn), op2), true
		case 13, 15: // mov/mvn
			return mn, fmt.Sprintf("%s, %s", armReg(rd), op2), true
		default:
			return mn, fmt.Sprintf("%s, %s, %s", armReg(rd), armReg(rn), op2), true
		}
	}
	return ".word", fmt.Sprintf("0x%08x", w), false
}

func armCondSuffix(cond uint32) string {
	return [...]string{".eq", ".ne", ".cs", ".cc", ".mi", ".pl", ".vs", ".vc", ".hi", ".ls", ".ge", ".lt", ".gt", ".le", "", ""}[cond&0xf]
}

func armDataProcName(op uint32) string {
	return [...]string{"and", "eor", "sub", "rsb", "add", "adc", "sbc", "rsc", "tst", "teq", "cmp", "cmn", "orr", "mov", "bic", "mvn"}[op&0xf]
}

func armReg(r uint32) string {
	switch r & 0xf {
	case 13:
		return "sp"
	case 14:
		return "lr"
	case 15:
		return "pc"
	default:
		return fmt.Sprintf("r%d", r&0xf)
	}
}

func armRegList(mask uint32) string {
	regs := []string{}
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		if start == end {
			regs = append(regs, armReg(uint32(start)))
		} else {
			regs = append(regs, fmt.Sprintf("%s-%s", armReg(uint32(start)), armReg(uint32(end))))
		}
		start = -1
	}
	for i := 0; i < 16; i++ {
		if mask&(1<<uint(i)) != 0 {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i - 1)
	}
	flush(15)
	return "{" + strings.Join(regs, ", ") + "}"
}

func armShift(v uint32) string {
	typ := (v >> 1) & 0x3
	imm := (v >> 3) & 0x1f
	names := [...]string{"lsl", "lsr", "asr", "ror"}
	if imm == 0 {
		if typ == 0 {
			return "lsl #0"
		}
		if typ == 3 {
			return "rrx"
		}
		imm = 32
	}
	return fmt.Sprintf("%s #%d", names[typ], imm)
}
