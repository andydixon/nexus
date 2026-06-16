package disasm

import (
	"encoding/binary"
	"fmt"
)

func diMIPS32(buf []byte, base int64, little bool, maxInstr int) []map[string]any {
	out := []map[string]any{}
	for i := 0; i+4 <= len(buf) && len(out) < maxInstr; i += 4 {
		var w uint32
		if little {
			w = binary.LittleEndian.Uint32(buf[i : i+4])
		} else {
			w = binary.BigEndian.Uint32(buf[i : i+4])
		}
		mn, ops, conf := decodeMIPS32(w, base+int64(i))
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i : i+4]),
			"mnemonic":  mn,
			"operands":  ops,
			"confident": conf,
		})
	}
	for i := len(buf) - len(buf)%4; i < len(buf) && len(out) < maxInstr; i++ {
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i : i+1]),
			"mnemonic":  "db",
			"operands":  hexCommaList(buf[i : i+1]),
			"confident": false,
		})
	}
	return out
}

func decodeMIPS32(w uint32, pc int64) (string, string, bool) {
	if w == 0 {
		return "nop", "", true
	}
	op := (w >> 26) & 0x3f
	rs := (w >> 21) & 0x1f
	rt := (w >> 16) & 0x1f
	rd := (w >> 11) & 0x1f
	shamt := (w >> 6) & 0x1f
	funct := w & 0x3f
	imm := uint16(w)
	simm := int16(imm)
	target26 := w & 0x03ffffff

	switch op {
	case 0x00:
		switch funct {
		case 0x00:
			return "sll", fmt.Sprintf("%s, %s, %d", mipsReg(rd), mipsReg(rt), shamt), true
		case 0x02:
			return "srl", fmt.Sprintf("%s, %s, %d", mipsReg(rd), mipsReg(rt), shamt), true
		case 0x03:
			return "sra", fmt.Sprintf("%s, %s, %d", mipsReg(rd), mipsReg(rt), shamt), true
		case 0x08:
			return "jr", mipsReg(rs), true
		case 0x09:
			return "jalr", fmt.Sprintf("%s, %s", mipsReg(rd), mipsReg(rs)), true
		case 0x0c:
			return "syscall", fmt.Sprintf("0x%x", (w>>6)&0xfffff), true
		case 0x20:
			return "add", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x21:
			return "addu", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x22:
			return "sub", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x23:
			return "subu", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x24:
			return "and", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x25:
			return "or", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x26:
			return "xor", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x27:
			return "nor", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		case 0x2a:
			return "slt", fmt.Sprintf("%s, %s, %s", mipsReg(rd), mipsReg(rs), mipsReg(rt)), true
		}
	case 0x02:
		return "j", fmt.Sprintf("0x%x", mipsJumpTarget(pc, target26)), true
	case 0x03:
		return "jal", fmt.Sprintf("0x%x", mipsJumpTarget(pc, target26)), true
	case 0x04:
		return "beq", fmt.Sprintf("%s, %s, 0x%x", mipsReg(rs), mipsReg(rt), mipsBranchTarget(pc, simm)), true
	case 0x05:
		return "bne", fmt.Sprintf("%s, %s, 0x%x", mipsReg(rs), mipsReg(rt), mipsBranchTarget(pc, simm)), true
	case 0x08:
		return "addi", fmt.Sprintf("%s, %s, %d", mipsReg(rt), mipsReg(rs), simm), true
	case 0x09:
		return "addiu", fmt.Sprintf("%s, %s, %d", mipsReg(rt), mipsReg(rs), simm), true
	case 0x0a:
		return "slti", fmt.Sprintf("%s, %s, %d", mipsReg(rt), mipsReg(rs), simm), true
	case 0x0c:
		return "andi", fmt.Sprintf("%s, %s, 0x%x", mipsReg(rt), mipsReg(rs), imm), true
	case 0x0d:
		return "ori", fmt.Sprintf("%s, %s, 0x%x", mipsReg(rt), mipsReg(rs), imm), true
	case 0x0e:
		return "xori", fmt.Sprintf("%s, %s, 0x%x", mipsReg(rt), mipsReg(rs), imm), true
	case 0x0f:
		return "lui", fmt.Sprintf("%s, 0x%x", mipsReg(rt), imm), true
	case 0x20:
		return "lb", mipsMem(rt, rs, simm), true
	case 0x21:
		return "lh", mipsMem(rt, rs, simm), true
	case 0x23:
		return "lw", mipsMem(rt, rs, simm), true
	case 0x28:
		return "sb", mipsMem(rt, rs, simm), true
	case 0x29:
		return "sh", mipsMem(rt, rs, simm), true
	case 0x2b:
		return "sw", mipsMem(rt, rs, simm), true
	}
	return ".word", fmt.Sprintf("0x%08x", w), false
}

func mipsJumpTarget(pc int64, target uint32) int64 {
	upper := uint64(pc+4) & 0xfffffffff0000000
	return int64(upper | uint64(target<<2))
}

func mipsBranchTarget(pc int64, imm int16) int64 {
	return pc + 4 + int64(imm)*4
}

func mipsMem(rt, rs uint32, off int16) string {
	return fmt.Sprintf("%s, %d(%s)", mipsReg(rt), off, mipsReg(rs))
}

func mipsReg(r uint32) string {
	return [...]string{
		"$zero", "$at", "$v0", "$v1", "$a0", "$a1", "$a2", "$a3",
		"$t0", "$t1", "$t2", "$t3", "$t4", "$t5", "$t6", "$t7",
		"$s0", "$s1", "$s2", "$s3", "$s4", "$s5", "$s6", "$s7",
		"$t8", "$t9", "$k0", "$k1", "$gp", "$sp", "$fp", "$ra",
	}[r&0x1f]
}
