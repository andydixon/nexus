package binsnoop

import (
	"errors"
	"fmt"
)

// ErrParse is returned when structure parsing fails.
var ErrParse = errors.New("structure parse error")

// StructField defines a single field in a binary structure.
type StructField struct {
	Name string
	Type string // "u8", "u16", "u24", "u32", "u64"
}

// typeSize returns the byte size for a type string.
func typeSize(ctype string) (int, error) {
	switch ctype {
	case "u8":
		return 1, nil
	case "u16":
		return 2, nil
	case "u24":
		return 3, nil
	case "u32":
		return 4, nil
	case "u64":
		return 8, nil
	default:
		return 0, fmt.Errorf("unknown type: %s", ctype)
	}
}

// StructSize returns the total byte size of a structure definition.
func StructSize(fields []StructField) int {
	total := 0
	for _, f := range fields {
		sz, err := typeSize(f.Type)
		if err != nil {
			continue
		}
		total += sz
	}
	return total
}

// Parse parses a binary structure from data according to the field definitions.
// Endianness must be "big" or "little". All values are returned as int.
func Parse(data []byte, fields []StructField, endianness string) (map[string]int, error) {
	structSize := StructSize(fields)
	if len(data) < structSize {
		return nil, ErrParse
	}

	result := make(map[string]int, len(fields))
	offset := 0

	for _, f := range fields {
		sz, err := typeSize(f.Type)
		if err != nil {
			return nil, ErrParse
		}

		var value int

		switch sz {
		case 1:
			value = int(data[offset])
		case 2:
			if endianness == "big" {
				value = int(data[offset])<<8 | int(data[offset+1])
			} else {
				value = int(data[offset+1])<<8 | int(data[offset])
			}
		case 3:
			if endianness == "big" {
				value = int(data[offset])<<16 | int(data[offset+1])<<8 | int(data[offset+2])
			} else {
				value = int(data[offset+2])<<16 | int(data[offset+1])<<8 | int(data[offset])
			}
		case 4:
			if endianness == "big" {
				value = int(data[offset])<<24 | int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
			} else {
				value = int(data[offset+3])<<24 | int(data[offset+2])<<16 | int(data[offset+1])<<8 | int(data[offset])
			}
		case 8:
			if endianness == "big" {
				value = int(uint64(data[offset])<<56 | uint64(data[offset+1])<<48 |
					uint64(data[offset+2])<<40 | uint64(data[offset+3])<<32 |
					uint64(data[offset+4])<<24 | uint64(data[offset+5])<<16 |
					uint64(data[offset+6])<<8 | uint64(data[offset+7]))
			} else {
				value = int(uint64(data[offset+7])<<56 | uint64(data[offset+6])<<48 |
					uint64(data[offset+5])<<40 | uint64(data[offset+4])<<32 |
					uint64(data[offset+3])<<24 | uint64(data[offset+2])<<16 |
					uint64(data[offset+1])<<8 | uint64(data[offset]))
			}
		}

		offset += sz
		result[f.Name] = value
	}

	return result, nil
}
