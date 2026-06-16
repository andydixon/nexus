package disasm

import (
	"encoding/binary"
	"fmt"
)

func diThumb16(buf []byte, base int64, maxInstr int) []map[string]any {
	out := []map[string]any{}
	for i := 0; i+2 <= len(buf) && len(out) < maxInstr; i += 2 {
		h := binary.LittleEndian.Uint16(buf[i : i+2])
		mn, ops, conf := decodeThumb16(h, base+int64(i))
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i : i+2]),
			"mnemonic":  mn,
			"operands":  ops,
			"confident": conf,
		})
	}
	for i := len(buf) - len(buf)%2; i < len(buf) && len(out) < maxInstr; i++ {
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

func decodeThumb16(h uint16, pc int64) (string, string, bool) {
	switch {
	case h == 0xbf00:
		return "nop", "", true
	case h&0xff87 == 0x4700:
		return "bx", thumbHighReg((h >> 3) & 0xf), true
	case h&0xff87 == 0x4780:
		return "blx", thumbHighReg((h >> 3) & 0xf), true
	case h&0xf800 == 0xe000:
		imm := signExtend64(uint64(h&0x07ff)<<1, 12)
		return "b", fmt.Sprintf("0x%x", pc+4+imm), true
	case h&0xf000 == 0xd000 && h&0x0f00 != 0x0f00:
		cond := uint32((h >> 8) & 0xf)
		imm := signExtend64(uint64(h&0x00ff)<<1, 9)
		return "b" + armCondSuffix(cond), fmt.Sprintf("0x%x", pc+4+imm), true
	case h&0xff00 == 0xdf00:
		return "svc", fmt.Sprintf("#0x%x", h&0x00ff), true
	case h&0xfe00 == 0xb400:
		mask := uint32(h & 0x00ff)
		if h&0x0100 != 0 {
			mask |= 1 << 14
		}
		return "push", armRegList(mask), true
	case h&0xfe00 == 0xbc00:
		mask := uint32(h & 0x00ff)
		if h&0x0100 != 0 {
			mask |= 1 << 15
		}
		return "pop", armRegList(mask), true
	case h&0xf800 == 0x2000:
		rd := uint32((h >> 8) & 0x7)
		return "movs", fmt.Sprintf("%s, #0x%x", armReg(rd), h&0x00ff), true
	case h&0xf800 == 0x2800:
		rn := uint32((h >> 8) & 0x7)
		return "cmp", fmt.Sprintf("%s, #0x%x", armReg(rn), h&0x00ff), true
	case h&0xf800 == 0x3000:
		rd := uint32((h >> 8) & 0x7)
		return "adds", fmt.Sprintf("%s, #0x%x", armReg(rd), h&0x00ff), true
	case h&0xf800 == 0x3800:
		rd := uint32((h >> 8) & 0x7)
		return "subs", fmt.Sprintf("%s, #0x%x", armReg(rd), h&0x00ff), true
	case h&0xfc00 == 0x4000:
		op := (h >> 6) & 0xf
		rs := uint32((h >> 3) & 0x7)
		rd := uint32(h & 0x7)
		names := [...]string{"ands", "eors", "lsls", "lsrs", "asrs", "adcs", "sbcs", "rors", "tst", "rsbs", "cmp", "cmn", "orrs", "muls", "bics", "mvns"}
		mn := names[op]
		if op == 8 || op == 10 || op == 11 {
			return mn, fmt.Sprintf("%s, %s", armReg(rd), armReg(rs)), true
		}
		return mn, fmt.Sprintf("%s, %s", armReg(rd), armReg(rs)), true
	case h&0xfe00 == 0x1800:
		rm := uint32((h >> 6) & 0x7)
		rn := uint32((h >> 3) & 0x7)
		rd := uint32(h & 0x7)
		mn := "adds"
		if h&0x0200 != 0 {
			mn = "subs"
		}
		return mn, fmt.Sprintf("%s, %s, %s", armReg(rd), armReg(rn), armReg(rm)), true
	case h&0xe000 == 0x6000:
		imm := uint32((h>>6)&0x1f) * 4
		rn := uint32((h >> 3) & 0x7)
		rd := uint32(h & 0x7)
		mn := "str"
		if h&0x0800 != 0 {
			mn = "ldr"
		}
		return mn, fmt.Sprintf("%s, [%s, #0x%x]", armReg(rd), armReg(rn), imm), true
	}
	return ".hword", fmt.Sprintf("0x%04x", h), false
}

func thumbHighReg(r uint16) string {
	if r == 13 {
		return "sp"
	}
	if r == 14 {
		return "lr"
	}
	if r == 15 {
		return "pc"
	}
	return fmt.Sprintf("r%d", r)
}
