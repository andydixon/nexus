package binsnoop

import (
	"bytes"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// PNG
// ---------------------------------------------------------------------------

const PNGDescription = "PNG image"

func PNGMagic() [][]byte {
	// PNG magic followed by IHDR chunk size (0x0000000D) and IHDR chunk type
	return [][]byte{
		{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52},
	}
}

func PNGParser(data []byte, offset int) (*SignatureResult, error) {
	const pngHeaderLen = 8
	const crcSize = 4
	const iendChunkType = 0x49454E44

	chunkFields := []StructField{
		{"length", "u32"},
		{"type", "u32"},
	}
	chunkHeaderSize := StructSize(chunkFields)

	if offset+pngHeaderLen >= len(data) {
		return nil, fmt.Errorf("PNG: not enough data for header")
	}

	// Walk chunks starting after the 8-byte PNG header
	chunkData := data[offset+pngHeaderLen:]
	available := len(chunkData)
	chunkOffset := 0
	var previousOffset *int

	for IsOffsetSafe(available, chunkOffset, previousOffset) {
		if chunkOffset+chunkHeaderSize > available {
			break
		}
		hdr, err := Parse(chunkData[chunkOffset:], chunkFields, "big")
		if err != nil {
			break
		}
		isLast := hdr["type"] == iendChunkType
		totalChunkSize := chunkHeaderSize + hdr["length"] + crcSize
		prev := chunkOffset
		previousOffset = &prev
		chunkOffset += totalChunkSize
		if isLast {
			pngSize := pngHeaderLen + chunkOffset
			return &SignatureResult{
				Offset:      offset,
				Description: fmt.Sprintf("%s, total size: %d bytes", PNGDescription, pngSize),
				Confidence:  ConfidenceHigh,
				Size:        pngSize,
			}, nil
		}
	}

	return nil, fmt.Errorf("PNG: failed to walk chunks to IEND")
}

// ---------------------------------------------------------------------------
// JPEG
// ---------------------------------------------------------------------------

const JPEGDescription = "JPEG image"

func JPEGMagic() [][]byte {
	return [][]byte{
		{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00}, // JFIF
		{0xFF, 0xD8, 0xFF, 0xE1}, // EXIF
		{0xFF, 0xD8, 0xFF, 0xDB}, // DQT
	}
}

func JPEGParser(data []byte, offset int) (*SignatureResult, error) {
	const markerMagic byte = 0xFF
	const sosMarker byte = 0xDA
	const eofMarker byte = 0xD9

	noLengthMarkers := []byte{
		0x00, 0x01, 0xD0, 0xD1, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7, 0xD8, eofMarker,
	}
	sosSkipMarkers := []byte{
		0x00, 0xD0, 0xD1, 0xD2, 0xD3, 0xD4, 0xD5, 0xD6, 0xD7,
	}

	jpegData := data[offset:]
	nextMarkerOffset := 0

	for {
		if nextMarkerOffset >= len(jpegData) {
			break
		}
		magic := jpegData[nextMarkerOffset]
		if magic != markerMagic {
			break
		}
		nextMarkerOffset++

		if nextMarkerOffset >= len(jpegData) {
			break
		}
		markerID := jpegData[nextMarkerOffset]
		nextMarkerOffset++

		// If this marker has a length field
		if !byteInSlice(markerID, noLengthMarkers) {
			if nextMarkerOffset+2 > len(jpegData) {
				break
			}
			segLen := int(jpegData[nextMarkerOffset])<<8 | int(jpegData[nextMarkerOffset+1])
			nextMarkerOffset += segLen
		}

		// SOS marker: scan until next valid marker
		if markerID == sosMarker {
			for {
				if nextMarkerOffset+2 > len(jpegData) {
					break
				}
				if jpegData[nextMarkerOffset] == markerMagic && !byteInSlice(jpegData[nextMarkerOffset+1], sosSkipMarkers) {
					break
				}
				nextMarkerOffset++
			}
		}

		// EOF marker
		if markerID == eofMarker {
			jpegSize := nextMarkerOffset
			return &SignatureResult{
				Offset:      offset,
				Description: fmt.Sprintf("%s, total size: %d bytes", JPEGDescription, jpegSize),
				Confidence:  ConfidenceMedium,
				Size:        jpegSize,
			}, nil
		}
	}

	return nil, fmt.Errorf("JPEG: failed to find EOI marker")
}

// ---------------------------------------------------------------------------
// BMP
// ---------------------------------------------------------------------------

const BMPDescription = "BMP image (Bitmap)"

func BMPMagic() [][]byte {
	return [][]byte{
		{0x42, 0x4D}, // "BM"
	}
}

func BMPParser(data []byte, offset int) (*SignatureResult, error) {
	const bmpFileHeaderSize = 14

	bmpHeaderFields := []StructField{
		{"bfType", "u16"},
		{"bfSize", "u32"},
		{"bfReserved1", "u16"},
		{"bfReserved2", "u16"},
		{"bfOffBits", "u32"},
	}

	validDIBHeaderSizes := []int{12, 40, 108, 124}

	bmpData := data[offset:]
	if len(bmpData) < StructSize(bmpHeaderFields) {
		return nil, fmt.Errorf("BMP: not enough data for header")
	}

	hdr, err := Parse(bmpData, bmpHeaderFields, "little")
	if err != nil {
		return nil, fmt.Errorf("BMP: parse error: %w", err)
	}

	bmpDataSize := len(bmpData)

	// The BMP file size cannot be bigger than available data
	if bmpDataSize < hdr["bfSize"] {
		return nil, fmt.Errorf("BMP: reported size %d exceeds available data %d", hdr["bfSize"], bmpDataSize)
	}
	if hdr["bfSize"] == 0 {
		return nil, fmt.Errorf("BMP: file size is 0")
	}
	if hdr["bfOffBits"] == 0 {
		return nil, fmt.Errorf("BMP: bitmap bits offset is 0")
	}
	if hdr["bfOffBits"] > bmpDataSize {
		return nil, fmt.Errorf("BMP: bitmap bits offset exceeds available data")
	}

	// Validate DIB header size
	if offset+bmpFileHeaderSize+4 > len(data) {
		return nil, fmt.Errorf("BMP: not enough data for DIB header size")
	}
	dibHeaderSize := int(LE32(data, offset+bmpFileHeaderSize))
	validDIB := false
	for _, vs := range validDIBHeaderSizes {
		if dibHeaderSize == vs {
			validDIB = true
			break
		}
	}
	if !validDIB {
		return nil, fmt.Errorf("BMP: invalid DIB header size %d", dibHeaderSize)
	}

	// The offset that points to the image data cannot point into the second header
	if hdr["bfOffBits"] < bmpFileHeaderSize+dibHeaderSize {
		return nil, fmt.Errorf("BMP: bitmap bits offset points into header")
	}

	totalSize := hdr["bfSize"]
	return &SignatureResult{
		Offset:      offset,
		Name:        "bmp",
		Description: fmt.Sprintf("BMP image, total size: %d", totalSize),
		Confidence:  ConfidenceMedium,
		Size:        totalSize,
	}, nil
}

// ---------------------------------------------------------------------------
// GIF
// ---------------------------------------------------------------------------

const GIFDescription = "GIF image"

func GIFMagic() [][]byte {
	return [][]byte{
		{0x47, 0x49, 0x46, 0x38, 0x37, 0x61}, // GIF87a
		{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, // GIF89a
	}
}

func GIFParser(data []byte, offset int) (*SignatureResult, error) {
	gifHeaderFields := []StructField{
		{"magic_p1", "u24"},
		{"magic_p2", "u24"},
		{"image_width", "u16"},
		{"image_height", "u16"},
		{"flags", "u8"},
		{"bg_color_index", "u8"},
		{"aspect_ratio", "u8"},
	}

	gifData := data[offset:]
	if len(gifData) < StructSize(gifHeaderFields) {
		return nil, fmt.Errorf("GIF: not enough data for header")
	}

	hdr, err := Parse(gifData, gifHeaderFields, "little")
	if err != nil {
		return nil, fmt.Errorf("GIF: parse error: %w", err)
	}

	// Calculate header size including optional global color table
	headerSize := StructSize(gifHeaderFields) + gifColorTableSize(hdr["flags"])

	if offset+headerSize > len(data) {
		return nil, fmt.Errorf("GIF: not enough data for header + color table")
	}

	// Walk GIF blocks to find terminator
	blockData := data[offset+headerSize:]
	gifDataSize, err := getGIFDataSize(blockData)
	if err != nil {
		return nil, fmt.Errorf("GIF: %w", err)
	}

	totalSize := headerSize + gifDataSize
	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, %dx%d pixels, total size: %d bytes", GIFDescription, hdr["image_width"], hdr["image_height"], totalSize),
		Confidence:  ConfidenceHigh,
		Size:        totalSize,
	}, nil
}

func gifColorTableSize(flags int) int {
	const hasColorTable = 0x80
	const colorTableSizeMask = 0x07

	if flags&hasColorTable == 0 {
		return 0
	}
	encodedSize := (flags & colorTableSizeMask) + 1
	size := 3
	for i := 0; i < encodedSize; i++ {
		size *= 2
	}
	return size
}

func getGIFDataSize(gifData []byte) (int, error) {
	const extensionBlock byte = 0x21
	const terminatorBlock byte = 0x3B
	const imageDescriptorBlock byte = 0x2C

	nextOffset := 0
	var previousOffset *int
	available := len(gifData)

	for IsOffsetSafe(available, nextOffset, previousOffset) {
		if nextOffset >= available {
			break
		}
		blockType := gifData[nextOffset]

		var blockSize int
		var err error

		switch blockType {
		case imageDescriptorBlock:
			blockSize, err = parseGIFImageDescriptor(gifData[nextOffset:])
		case extensionBlock:
			blockSize, err = parseGIFExtension(gifData[nextOffset:])
		case terminatorBlock:
			return nextOffset + 1, nil
		default:
			return 0, fmt.Errorf("unknown GIF block type 0x%02X at offset %d", blockType, nextOffset)
		}

		if err != nil {
			return 0, err
		}
		prev := nextOffset
		previousOffset = &prev
		nextOffset += blockSize
	}

	return 0, fmt.Errorf("failed to find GIF terminator block")
}

func parseGIFImageDescriptor(gifData []byte) (int, error) {
	const lzwCodeSize = 1

	imgDescFields := []StructField{
		{"magic", "u8"},
		{"image_left", "u16"},
		{"image_top", "u16"},
		{"image_width", "u16"},
		{"image_height", "u16"},
		{"flags", "u8"},
	}

	if len(gifData) < StructSize(imgDescFields) {
		return 0, fmt.Errorf("GIF image descriptor: not enough data")
	}

	hdr, err := Parse(gifData, imgDescFields, "little")
	if err != nil {
		return 0, err
	}

	totalSize := StructSize(imgDescFields) + gifColorTableSize(hdr["flags"]) + lzwCodeSize

	if totalSize > len(gifData) {
		return 0, fmt.Errorf("GIF image descriptor: not enough data for sub-blocks")
	}

	subBlocksSize, err := parseGIFSubBlocks(gifData[totalSize:])
	if err != nil {
		return 0, err
	}
	totalSize += subBlocksSize
	return totalSize, nil
}

func parseGIFExtension(extensionData []byte) (int, error) {
	const plainText = 1
	const application = 0xFF
	const headerSize = 2

	extFields := []StructField{
		{"magic", "u8"},
		{"extension_type", "u8"},
		{"sub_block_offset", "u8"},
	}

	if len(extensionData) < StructSize(extFields) {
		return 0, fmt.Errorf("GIF extension: not enough data")
	}

	hdr, err := Parse(extensionData, extFields, "little")
	if err != nil {
		return 0, err
	}

	subBlocksOffset := headerSize
	extType := hdr["extension_type"]
	if extType == application || extType == plainText {
		subBlocksOffset += hdr["sub_block_offset"] + 1
	}

	if subBlocksOffset > len(extensionData) {
		return 0, fmt.Errorf("GIF extension: sub-blocks offset exceeds data")
	}

	subBlocksSize, err := parseGIFSubBlocks(extensionData[subBlocksOffset:])
	if err != nil {
		return 0, err
	}
	return subBlocksOffset + subBlocksSize, nil
}

func parseGIFSubBlocks(subBlockData []byte) (int, error) {
	const subBlockTerminator byte = 0

	available := len(subBlockData)
	nextOffset := 0
	var previousOffset *int

	for IsOffsetSafe(available, nextOffset, previousOffset) {
		if nextOffset >= available {
			break
		}
		subBlockSize := subBlockData[nextOffset]
		if subBlockSize == subBlockTerminator {
			return nextOffset + 1, nil
		}
		prev := nextOffset
		previousOffset = &prev
		nextOffset += int(subBlockSize) + 1
	}

	return 0, fmt.Errorf("GIF sub-blocks: terminator not found")
}

// ---------------------------------------------------------------------------
// SVG
// ---------------------------------------------------------------------------

const SVGDescription = "SVG image"

const svgOpenTag = "<svg "
const svgCloseTag = "</svg>"
const svgHeadMagic = `xmlns="http://www.w3.org/2000/svg"`

func SVGMagic() [][]byte {
	return [][]byte{
		{0x3C, 0x73, 0x76, 0x67, 0x20}, // "<svg "
	}
}

func SVGParser(data []byte, offset int) (*SignatureResult, error) {
	// Operate directly on the byte slice. A previous implementation did
	// string(data[offset:]) — copying the entire remaining file — on every
	// call; since the parser runs once per "<svg " match, a buffer dense with
	// "<svg " tokens made this O(n^2) in copies and could hang indefinitely.
	svgData := data[offset:]
	openTag := []byte(svgOpenTag)
	closeTag := []byte(svgCloseTag)
	headMagic := []byte(svgHeadMagic)

	// The matched "<svg " must be a real SVG root: its opening tag has to
	// terminate with '>' and declare the SVG xmlns (which the loop below
	// requires anyway via headTagCount). Verifying this up front, with a cap
	// on how far we look for the closing '>', keeps the parser O(1) per call
	// regardless of file size — so a buffer densely packed with "<svg " tokens
	// can't turn scanning into an O(n^2) hang.
	const maxRootTagLen = 64 * 1024
	rootLimit := len(svgData)
	if rootLimit > maxRootTagLen {
		rootLimit = maxRootTagLen
	}
	rootEnd := bytes.IndexByte(svgData[:rootLimit], '>')
	if rootEnd < 0 {
		return nil, fmt.Errorf("SVG: root tag not terminated")
	}
	if !bytes.Contains(svgData[:rootEnd+1], headMagic) {
		return nil, fmt.Errorf("SVG: root tag missing SVG namespace")
	}

	headTagCount := 0
	unclosedSVGTags := 0

	// Search for all <svg  and </svg> occurrences
	searchIdx := 0
	for searchIdx < len(svgData) {
		// Find next occurrence of either tag
		openIdx := bytes.Index(svgData[searchIdx:], openTag)
		closeIdx := bytes.Index(svgData[searchIdx:], closeTag)

		var tagStart int
		if openIdx == -1 && closeIdx == -1 {
			break
		} else if openIdx == -1 {
			tagStart = searchIdx + closeIdx
		} else if closeIdx == -1 {
			tagStart = searchIdx + openIdx
		} else if openIdx <= closeIdx {
			tagStart = searchIdx + openIdx
		} else {
			tagStart = searchIdx + closeIdx
		}

		tagBytes := svgData[tagStart:]

		// Find the end '>' of this tag
		endIdx := bytes.IndexByte(tagBytes, '>')
		if endIdx == -1 {
			break
		}
		fullTag := tagBytes[:endIdx+1]

		isOpen := bytes.HasPrefix(fullTag, openTag)
		isClose := bytes.HasPrefix(fullTag, closeTag)
		isHead := bytes.Contains(fullTag, headMagic)

		if isHead {
			headTagCount++
		}
		if isOpen {
			unclosedSVGTags++
		}
		if isClose {
			unclosedSVGTags--
		}

		// More than one head tag is invalid
		if headTagCount > 1 {
			break
		}

		// If one head tag and all tags closed, that's EOF
		if headTagCount == 1 && unclosedSVGTags == 0 {
			totalSize := tagStart + len(svgCloseTag)
			return &SignatureResult{
				Offset:      offset,
				Description: fmt.Sprintf("%s, total size: %d bytes", SVGDescription, totalSize),
				Confidence:  ConfidenceMedium,
				Size:        totalSize,
			}, nil
		}

		searchIdx = tagStart + 1
	}

	return nil, fmt.Errorf("SVG: failed to find matching closing tag")
}

// ---------------------------------------------------------------------------
// RIFF
// ---------------------------------------------------------------------------

const RIFFDescription = "RIFF image"

func RIFFMagic() [][]byte {
	return [][]byte{
		{0x52, 0x49, 0x46, 0x46}, // "RIFF"
	}
}

func RIFFParser(data []byte, offset int) (*SignatureResult, error) {
	const riffMagicValue = 0x46464952 // "RIFF" in LE
	const chunkTypeStart = 8
	const chunkTypeEnd = 12
	const fileSizeOffset = 8

	riffFields := []StructField{
		{"magic", "u32"},
		{"file_size", "u32"},
		{"chunk_type", "u32"},
	}

	riffData := data[offset:]
	if len(riffData) < StructSize(riffFields) {
		return nil, fmt.Errorf("RIFF: not enough data for header")
	}

	hdr, err := Parse(riffData, riffFields, "little")
	if err != nil {
		return nil, fmt.Errorf("RIFF: parse error: %w", err)
	}

	if hdr["magic"] != riffMagicValue {
		return nil, fmt.Errorf("RIFF: invalid magic")
	}

	if chunkTypeEnd > len(riffData) {
		return nil, fmt.Errorf("RIFF: not enough data for chunk type string")
	}

	// Get the RIFF type string (e.g., "WAVE", "AVI ")
	typeBytes := riffData[chunkTypeStart:chunkTypeEnd]
	typeString := strings.TrimSpace(string(typeBytes))

	// Validate that the type string is valid UTF-8 / printable
	for _, b := range typeBytes {
		if b < 0x20 || b > 0x7E {
			return nil, fmt.Errorf("RIFF: non-printable chunk type")
		}
	}

	totalSize := hdr["file_size"] + fileSizeOffset

	return &SignatureResult{
		Offset:      offset,
		Description: fmt.Sprintf("%s, encoding type: %s, total size: %d bytes", RIFFDescription, typeString, totalSize),
		Confidence:  ConfidenceMedium,
		Size:        totalSize,
	}, nil
}

// ---------------------------------------------------------------------------
// PDF
// ---------------------------------------------------------------------------

const PDFDescription = "PDF document"

func PDFMagic() [][]byte {
	return [][]byte{
		{0x25, 0x50, 0x44, 0x46, 0x2D, 0x31, 0x2E}, // "%PDF-1."
	}
}

func PDFParser(data []byte, offset int) (*SignatureResult, error) {
	const minPDFSize = 16
	const newlineOffset = 8
	const minorNumberOffset = 7

	const asciiZero byte = 0x30
	const asciiNine byte = 0x39
	const asciiNewline byte = 0x0A
	const asciiPercent byte = 0x25
	const asciiCarriageReturn byte = 0x0D

	if offset+minPDFSize > len(data) {
		return nil, fmt.Errorf("PDF: not enough data for header")
	}

	pdfHeader := data[offset : offset+minPDFSize]

	// Get the minor version number
	versionMinor := pdfHeader[minorNumberOffset]
	if versionMinor < asciiZero || versionMinor > asciiNine {
		return nil, fmt.Errorf("PDF: invalid minor version number")
	}

	// After the version line, expect newline(s) followed by '%'
	for _, b := range pdfHeader[newlineOffset:] {
		if b == asciiNewline || b == asciiCarriageReturn {
			continue
		} else if b == asciiPercent {
			return &SignatureResult{
				Offset:      offset,
				Description: fmt.Sprintf("%s, version 1.%d", PDFDescription, versionMinor-asciiZero),
				Confidence:  ConfidenceLow,
				Size:        0,
			}, nil
		} else {
			break
		}
	}

	return nil, fmt.Errorf("PDF: invalid header format")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func byteInSlice(b byte, s []byte) bool {
	return bytes.ContainsRune(s, rune(b))
}
