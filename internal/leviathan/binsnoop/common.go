package binsnoop

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"time"
)

const MaxFileReadBytes int64 = 64 << 20

// CRC32 calculates the CRC32 checksum using the IEEE polynomial.
func CRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// EpochToString converts a Unix timestamp to a formatted time string.
func EpochToString(epoch uint32) string {
	t := time.Unix(int64(epoch), 0).UTC()
	return t.Format("2006-01-02 15:04:05")
}

// GetCString extracts a NULL-terminated C string from raw bytes.
func GetCString(data []byte) string {
	for i, b := range data {
		if b == 0 {
			s := string(data[:i])
			// Validate UTF-8 printability
			for _, c := range s {
				if c == 0 {
					return ""
				}
			}
			return s
		}
	}
	return string(data)
}

// IsASCIINumber returns true if the byte is an ASCII digit (0-9).
func IsASCIINumber(b byte) bool {
	return b >= '0' && b <= '9'
}

// IsPrintableASCII returns true if the byte is a printable ASCII character.
func IsPrintableASCII(b byte) bool {
	return b >= 0x0A && b <= 0x7E
}

// IsOffsetSafe validates that the next offset is within bounds and progressing forward.
func IsOffsetSafe(availableData, nextOffset int, lastOffset *int) bool {
	if lastOffset != nil {
		if *lastOffset >= nextOffset {
			return false
		}
	}
	return nextOffset < availableData
}

// ReadFile reads a bounded file into memory for scanner APIs that require a
// byte slice. Larger callers should use a streaming document path.
func ReadFile(path string) ([]byte, error) {
	return ReadFileLimited(path, MaxFileReadBytes)
}

func ReadFileLimited(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("binsnoop: path is a directory")
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, fmt.Errorf("binsnoop: file is too large: %d bytes exceeds %d bytes", info.Size(), maxBytes)
	}
	return os.ReadFile(path)
}

// LE16 reads a little-endian uint16 from data at the given offset.
func LE16(data []byte, offset int) uint16 {
	if offset+2 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint16(data[offset:])
}

// BE16 reads a big-endian uint16 from data at the given offset.
func BE16(data []byte, offset int) uint16 {
	if offset+2 > len(data) {
		return 0
	}
	return binary.BigEndian.Uint16(data[offset:])
}

// LE32 reads a little-endian uint32 from data at the given offset.
func LE32(data []byte, offset int) uint32 {
	if offset+4 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint32(data[offset:])
}

// BE32 reads a big-endian uint32 from data at the given offset.
func BE32(data []byte, offset int) uint32 {
	if offset+4 > len(data) {
		return 0
	}
	return binary.BigEndian.Uint32(data[offset:])
}

// LE64 reads a little-endian uint64 from data at the given offset.
func LE64(data []byte, offset int) uint64 {
	if offset+8 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint64(data[offset:])
}

// BE64 reads a big-endian uint64 from data at the given offset.
func BE64(data []byte, offset int) uint64 {
	if offset+8 > len(data) {
		return 0
	}
	return binary.BigEndian.Uint64(data[offset:])
}
