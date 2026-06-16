package analysis

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Inspect interprets bytes starting at `buf[0]` as many candidate
// types. It is resilient: types that don't have enough bytes simply
// return empty strings.
func Inspect(buf []byte, offset int64) map[string]any {
	out := map[string]any{
		"offset": offset,
		"length": len(buf),
	}

	// 8-bit
	if len(buf) >= 1 {
		out["u8"] = int(buf[0])
		out["i8"] = int(int8(buf[0]))
		out["char"] = printable(buf[0])
		out["bin8"] = fmt.Sprintf("%08b", buf[0])
	}

	// 16-bit
	if len(buf) >= 2 {
		le16 := binary.LittleEndian.Uint16(buf)
		be16 := binary.BigEndian.Uint16(buf)
		out["u16_le"] = int(le16)
		out["u16_be"] = int(be16)
		out["i16_le"] = int(int16(le16))
		out["i16_be"] = int(int16(be16))
	}

	// 32-bit
	if len(buf) >= 4 {
		le32 := binary.LittleEndian.Uint32(buf)
		be32 := binary.BigEndian.Uint32(buf)
		out["u32_le"] = uint(le32)
		out["u32_be"] = uint(be32)
		out["i32_le"] = int(int32(le32))
		out["i32_be"] = int(int32(be32))
		out["f32_le"] = float64(math.Float32frombits(le32))
		out["f32_be"] = float64(math.Float32frombits(be32))
	}

	// 64-bit
	if len(buf) >= 8 {
		le64 := binary.LittleEndian.Uint64(buf)
		be64 := binary.BigEndian.Uint64(buf)
		out["u64_le"] = float64(le64)
		out["u64_be"] = float64(be64)
		out["i64_le"] = float64(int64(le64))
		out["i64_be"] = float64(int64(be64))
		out["f64_le"] = math.Float64frombits(le64)
		out["f64_be"] = math.Float64frombits(be64)
	}

	// Timestamp guesses
	if len(buf) >= 4 {
		le := binary.LittleEndian.Uint32(buf)
		out["unix_le"] = formatUnix(int64(le))
	}
	if len(buf) >= 8 {
		le := binary.LittleEndian.Uint64(buf)
		out["filetime_le"] = formatFiletime(le)
		out["webkit_le"] = formatWebkit(le)
		out["oletime_le"] = formatOLEDate(math.Float64frombits(le))
	}

	// ASCII preview (up to 32 bytes)
	n := len(buf)
	if n > 32 {
		n = 32
	}
	ascii := make([]byte, n)
	for i := 0; i < n; i++ {
		if buf[i] >= 0x20 && buf[i] <= 0x7e {
			ascii[i] = buf[i]
		} else {
			ascii[i] = '.'
		}
	}
	out["ascii"] = string(ascii)

	// UTF-8 attempt
	if utf8.Valid(buf[:n]) {
		out["utf8"] = string(buf[:n])
	}

	// UTF-16 LE/BE preview
	if len(buf) >= 2 {
		lu := n / 2
		if lu > 0 {
			u := make([]uint16, lu)
			for i := 0; i < lu; i++ {
				u[i] = uint16(buf[i*2]) | uint16(buf[i*2+1])<<8
			}
			out["utf16le"] = string(utf16.Decode(u))
			for i := 0; i < lu; i++ {
				u[i] = uint16(buf[i*2])<<8 | uint16(buf[i*2+1])
			}
			out["utf16be"] = string(utf16.Decode(u))
		}
	}

	return out
}

func printable(b byte) string {
	if b >= 0x20 && b <= 0x7e {
		return string([]byte{b})
	}
	return "."
}

func formatUnix(sec int64) string {
	if sec < 0 || sec > 4102444800 { // past 2100
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// Windows FILETIME epoch 1601-01-01 in 100ns ticks.
const filetimeEpochDelta = 11644473600

func formatFiletime(v uint64) string {
	if v == 0 {
		return ""
	}
	sec := int64(v/10_000_000) - filetimeEpochDelta
	if sec < 0 || sec > 4102444800 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// WebKit/Chrome epoch 1601-01-01 in microseconds.
func formatWebkit(v uint64) string {
	if v == 0 {
		return ""
	}
	sec := int64(v/1_000_000) - filetimeEpochDelta
	if sec < 0 || sec > 4102444800 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// OLE Automation date: days since 1899-12-30, as float64.
func formatOLEDate(days float64) string {
	if days < 2 || days > 75000 {
		return ""
	}
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	sec := int64(days * 86400)
	return base.Add(time.Duration(sec) * time.Second).Format(time.RFC3339)
}
