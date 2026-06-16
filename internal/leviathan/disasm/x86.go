// Package disasm provides a honest, limited heuristic disassembler.
// It does NOT aim to be a full x86/ARM decoder. It recognises a
// subset of common opcodes so the UI can show plausible disassembly
// for inspection, and is explicit in the output when instructions
// are unknown.
package disasm

import "fmt"

// Instr is one decoded instruction.
type Instr struct {
	Offset    int64
	Bytes     []byte
	Mnemonic  string
	Operands  string
	Confident bool
}

// Disassemble returns a slice of Instr for buf starting at `base`.
// arch is one of "x86", "x86_64", "arm", "thumb", "aarch64", "mips".
// For unsupported arches we emit a byte stream with "db" entries.
func Disassemble(buf []byte, base int64, arch string, maxInstr int) []map[string]any {
	if maxInstr <= 0 {
		maxInstr = 4096
	}
	switch arch {
	case "x86", "x86_64":
		return diX86(buf, base, arch == "x86_64", maxInstr)
	case "aarch64", "arm64":
		return diAArch64(buf, base, maxInstr)
	case "arm", "arm32":
		return diARM32(buf, base, maxInstr)
	case "thumb", "thumb16", "t16":
		return diThumb16(buf, base, maxInstr)
	case "mips", "mipsbe", "mips32":
		return diMIPS32(buf, base, false, maxInstr)
	case "mipsle", "mipsel":
		return diMIPS32(buf, base, true, maxInstr)
	}
	return diRaw(buf, base, arch, maxInstr)
}

// diRaw emits `db` lines 16 bytes at a time for unsupported arches.
func diRaw(buf []byte, base int64, arch string, maxInstr int) []map[string]any {
	out := []map[string]any{}
	step := 16
	for i := 0; i < len(buf) && len(out) < maxInstr; i += step {
		end := i + step
		if end > len(buf) {
			end = len(buf)
		}
		out = append(out, map[string]any{
			"offset":    base + int64(i),
			"bytes":     hexStr(buf[i:end]),
			"mnemonic":  "db",
			"operands":  hexCommaList(buf[i:end]),
			"confident": false,
			"note":      arch + " decode not implemented; raw bytes only",
		})
	}
	return out
}

// diX86 decodes x86 / x86-64 instructions. The full decoder lives in
// x86_decode.go; this dispatcher only forwards to it.
func diX86(buf []byte, base int64, is64 bool, maxInstr int) []map[string]any {
	return decodeX86(buf, base, is64, maxInstr)
}

func hexStr(b []byte) string {
	const h = "0123456789abcdef"
	out := make([]byte, 0, len(b)*3)
	for i, c := range b {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, h[c>>4], h[c&0x0f])
	}
	return string(out)
}

func hexCommaList(b []byte) string {
	out := ""
	for i, c := range b {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("0x%02x", c)
	}
	return out
}
