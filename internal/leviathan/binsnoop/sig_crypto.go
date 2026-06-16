package binsnoop

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// ---- AES descriptions ----

const (
	DescriptionAESSBox = "AES S-Box"
	DescriptionAESFT   = "AES Forward Table"
	DescriptionAESRT   = "AES Reverse Table"
	DescriptionAESRCON = "AES RCON"
	DescriptionAESAcc  = "AES Acceleration Table"
)

// aesSBoxMagic returns magic bytes for AES S-Box (forward and reverse).
func aesSBoxMagic() [][]byte {
	return [][]byte{
		{0x63, 0x7C, 0x77, 0x7B, 0xF2, 0x6B, 0x6F, 0xC5}, // Forward S-Box
		{0x52, 0x09, 0x6A, 0xD5, 0x30, 0x36, 0xA5, 0x38}, // Reverse S-Box
	}
}

// aesSBoxParser validates AES S-Box signatures.
func aesSBoxParser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Description: DescriptionAESSBox,
		Confidence:  ConfidenceLow,
	}, nil
}

// aesForwardTableMagic returns magic bytes for AES Forward Table.
func aesForwardTableMagic() [][]byte {
	return [][]byte{
		{0xC6, 0x63, 0x63, 0xA5, 0xF8, 0x7C, 0x7C, 0x84, 0xEE, 0x77, 0x77, 0x99, 0xF6, 0x7B, 0x7B, 0x8D},
	}
}

// aesForwardTableParser validates AES Forward Table signatures.
func aesForwardTableParser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Description: DescriptionAESFT,
		Confidence:  ConfidenceLow,
	}, nil
}

// aesReverseTableMagic returns magic bytes for AES Reverse Table.
func aesReverseTableMagic() [][]byte {
	return [][]byte{
		{0x51, 0xF4, 0xA7, 0x50, 0x7E, 0x41, 0x65, 0x53, 0x1A, 0x17, 0xA4, 0xC3, 0x3A, 0x27, 0x5E, 0x96},
	}
}

// aesReverseTableParser validates AES Reverse Table signatures.
func aesReverseTableParser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Description: DescriptionAESRT,
		Confidence:  ConfidenceLow,
	}, nil
}

// aesRconMagic returns magic bytes for AES RCON.
func aesRconMagic() [][]byte {
	return [][]byte{
		{0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36},
		{0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00,
			0x10, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x40, 0x00, 0x00, 0x00, 0x80, 0x00, 0x00, 0x00,
			0x1B, 0x00, 0x00, 0x00, 0x36, 0x00, 0x00, 0x00},
	}
}

// aesRconParser validates AES RCON signatures.
func aesRconParser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Description: DescriptionAESRCON,
		Confidence:  ConfidenceLow,
	}, nil
}

// aesAccelerationTableMagic returns magic bytes for AES Acceleration Table.
func aesAccelerationTableMagic() [][]byte {
	return [][]byte{
		// combined sbox x2
		{0xA5, 0x84, 0x99, 0x8D, 0x0D, 0xBD, 0xB1, 0x54, 0x50, 0x03, 0xA9, 0x7D, 0x19, 0x62, 0xE6, 0x9A},
		// combined sbox x3
		{0xC6, 0xF8, 0xEE, 0xF6, 0xFF, 0xD6, 0xDE, 0x91, 0x60, 0x02, 0xCE, 0x56, 0xE7, 0xB5, 0x4D, 0xEC},
		// Galois mult 2
		{0x00, 0x02, 0x04, 0x06, 0x08, 0x0a, 0x0c, 0x0e, 0x10, 0x12, 0x14, 0x16, 0x18, 0x1a, 0x1c, 0x1e,
			0x20, 0x22, 0x24, 0x26, 0x28, 0x2a, 0x2c, 0x2e},
		// Galois mult 3
		{0x00, 0x03, 0x06, 0x05, 0x0c, 0x0f, 0x0a, 0x09, 0x18, 0x1b, 0x1e, 0x1d, 0x14, 0x17, 0x12, 0x11,
			0x30, 0x33, 0x36, 0x35, 0x3c, 0x3f, 0x3a, 0x39},
		// Galois mult 9
		{0x00, 0x09, 0x12, 0x1b, 0x24, 0x2d, 0x36, 0x3f, 0x48, 0x41, 0x5a, 0x53, 0x6c, 0x65, 0x7e, 0x77,
			0x90, 0x99, 0x82, 0x8b, 0xb4, 0xbd, 0xa6, 0xaf},
		// Galois mult 11
		{0x00, 0x0b, 0x16, 0x1d, 0x2c, 0x27, 0x3a, 0x31, 0x58, 0x53, 0x4e, 0x45, 0x74, 0x7f, 0x62, 0x69,
			0xb0, 0xbb, 0xa6, 0xad, 0x9c, 0x97, 0x8a, 0x81},
		// Galois mult 13
		{0x00, 0x0d, 0x1a, 0x17, 0x34, 0x39, 0x2e, 0x23, 0x68, 0x65, 0x72, 0x7f, 0x5c, 0x51, 0x46, 0x4b,
			0xd0, 0xdd, 0xca, 0xc7, 0xe4, 0xe9, 0xfe, 0xf3},
		// Galois mult 14
		{0x00, 0x0e, 0x1c, 0x12, 0x38, 0x36, 0x24, 0x2a, 0x70, 0x7e, 0x6c, 0x62, 0x48, 0x46, 0x54, 0x5a,
			0xe0, 0xee, 0xfc, 0xf2, 0xd8, 0xd6, 0xc4, 0xca},
	}
}

// aesAccelerationTableParser validates AES Acceleration Table signatures.
func aesAccelerationTableParser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Description: DescriptionAESAcc,
		Confidence:  ConfidenceLow,
	}, nil
}

// ---- RSA ----

const DescriptionRSA = "RSA encrypted session key"

type rsaKeyDefinition struct {
	magic            []byte
	keySize          int
	usageOffset      int
	keyIDOffset      int
	validChecks      map[int][][]byte
	terminatorOffset int
}

func rsaKeyDefinitions() []rsaKeyDefinition {
	return []rsaKeyDefinition{
		// 1024b RSA key
		{
			magic:            []byte{0x84, 0x8C, 0x03},
			keySize:          1024,
			keyIDOffset:      3,
			terminatorOffset: 142,
			usageOffset:      11,
			validChecks: map[int][][]byte{
				12: {
					{0x04, 0x00}, {0x03, 0xff}, {0x03, 0xfe}, {0x03, 0xfd},
					{0x03, 0xfc}, {0x03, 0xfb}, {0x03, 0xfa}, {0x03, 0xf9},
				},
			},
		},
		// 2048b RSA key
		{
			magic:            []byte{0x85, 0x01, 0x0c, 0x03},
			keySize:          2048,
			keyIDOffset:      4,
			terminatorOffset: 271,
			usageOffset:      12,
			validChecks: map[int][][]byte{
				13: {
					{0x08, 0x00}, {0x07, 0xff}, {0x07, 0xfe}, {0x07, 0xfd},
					{0x07, 0xfc}, {0x07, 0xfb}, {0x07, 0xfa}, {0x07, 0xf9},
				},
			},
		},
		// 3072b RSA key
		{
			magic:            []byte{0x85, 0x01, 0x8c, 0x03},
			keySize:          3072,
			keyIDOffset:      4,
			terminatorOffset: 399,
			usageOffset:      12,
			validChecks: map[int][][]byte{
				13: {
					{0x0c, 0x00}, {0x0b, 0xff}, {0x0b, 0xfe}, {0x0b, 0xfd},
					{0x0b, 0xfc}, {0x0b, 0xfb}, {0x0b, 0xfa}, {0x0b, 0xf9},
				},
			},
		},
		// 4096b RSA key
		{
			magic:            []byte{0x85, 0x02, 0x0c, 0x03},
			keySize:          4096,
			keyIDOffset:      4,
			terminatorOffset: 527,
			usageOffset:      12,
			validChecks: map[int][][]byte{
				13: {
					{0x10, 0x00}, {0x0f, 0xff}, {0x0f, 0xfe}, {0x0f, 0xfd},
					{0x0f, 0xfc}, {0x0f, 0xfb}, {0x0f, 0xfa}, {0x0f, 0xf9},
				},
			},
		},
		// 8192b RSA key
		{
			magic:            []byte{0x85, 0x04, 0x0c, 0x03},
			keySize:          8192,
			keyIDOffset:      4,
			terminatorOffset: 1039,
			usageOffset:      12,
			validChecks: map[int][][]byte{
				13: {
					{0x20, 0x00}, {0x1f, 0xff}, {0x1f, 0xfe}, {0x1f, 0xfd},
					{0x1f, 0xfc}, {0x1f, 0xfb}, {0x1f, 0xfa}, {0x1f, 0xf9},
				},
			},
		},
	}
}

// rsaMagic returns magic bytes for all known RSA key sizes.
func rsaMagic() [][]byte {
	var magics [][]byte
	for _, def := range rsaKeyDefinitions() {
		m := make([]byte, len(def.magic))
		copy(m, def.magic)
		magics = append(magics, m)
	}
	return magics
}

type rsaKeyInfo struct {
	bits       int
	canSign    bool
	canEncrypt bool
	keyID      int
	dataSize   int
}

func rsaKeyParse(rawData []byte, def *rsaKeyDefinition) (*rsaKeyInfo, error) {
	const terminatorByte byte = 0xD2
	const encryptOnly byte = 2
	const signAndEncrypt byte = 1
	const validBytesSize = 2

	keyIDFields := []StructField{{"id", "u64"}}

	keyDataLen := def.terminatorOffset + 1
	if len(rawData) < keyDataLen {
		return nil, fmt.Errorf("insufficient RSA key data")
	}
	keyData := rawData[:keyDataLen]

	if keyData[def.terminatorOffset] != terminatorByte {
		return nil, fmt.Errorf("invalid RSA terminator byte")
	}

	if def.keyIDOffset+8 > len(keyData) {
		return nil, fmt.Errorf("key ID offset out of bounds")
	}
	keyIDParsed, err := Parse(keyData[def.keyIDOffset:], keyIDFields, "big")
	if err != nil {
		return nil, fmt.Errorf("failed to parse RSA key ID")
	}

	info := &rsaKeyInfo{keyID: keyIDParsed["id"]}
	info.canEncrypt = keyData[def.usageOffset] == encryptOnly
	info.canSign = keyData[def.usageOffset] == signAndEncrypt
	if info.canSign {
		info.canEncrypt = true
	}

	if !info.canSign && !info.canEncrypt {
		return nil, fmt.Errorf("RSA key cannot sign or encrypt")
	}

	for checkOffset, expectedList := range def.validChecks {
		if checkOffset+validBytesSize > len(keyData) {
			continue
		}
		actual := keyData[checkOffset : checkOffset+validBytesSize]
		for _, expected := range expectedList {
			if len(expected) == validBytesSize && actual[0] == expected[0] && actual[1] == expected[1] {
				info.bits = def.keySize
				info.dataSize = keyDataLen
				return info, nil
			}
		}
	}

	return nil, fmt.Errorf("RSA key validation bytes mismatch")
}

// rsaParser validates RSA encrypted session key signatures.
func rsaParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionRSA,
		Confidence:  ConfidenceMedium,
	}

	for _, def := range rsaKeyDefinitions() {
		magicEnd := offset + len(def.magic)
		if magicEnd > len(data) {
			continue
		}
		match := true
		for i, b := range def.magic {
			if data[offset+i] != b {
				match = false
				break
			}
		}
		if !match {
			continue
		}

		info, err := rsaKeyParse(data[offset:], &def)
		if err != nil {
			break
		}
		result.Size = info.dataSize
		result.Description = fmt.Sprintf("%s, %d bits, can sign: %t, can encrypt: %t, key ID: %#018X",
			result.Description, info.bits, info.canSign, info.canEncrypt, info.keyID)
		return result, nil
	}

	return nil, fmt.Errorf("invalid RSA signature")
}

// ---- OpenSSL ----

const DescriptionOpenSSL = "OpenSSL encryption"

// opensslMagic returns magic bytes for OpenSSL encrypted files.
func opensslMagic() [][]byte {
	return [][]byte{
		[]byte("Salted__"),
	}
}

func isSaltInvalid(salt int) bool {
	const saltLen = 8
	badByteCount := 0
	for i := 0; i < saltLen; i++ {
		saltByte := byte((salt >> (8 * i)) & 0xFF)
		if saltByte == 0 || IsPrintableASCII(saltByte) {
			badByteCount++
		}
	}
	return badByteCount == saltLen
}

// opensslParser validates OpenSSL encryption signatures.
func opensslParser(data []byte, offset int) (*SignatureResult, error) {
	sslFields := []StructField{{"magic", "u64"}, {"salt", "u64"}}

	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionOpenSSL,
		Confidence:  ConfidenceLow,
	}

	if offset+StructSize(sslFields) > len(data) {
		return nil, fmt.Errorf("insufficient OpenSSL data")
	}

	sslHeader, err := Parse(data[offset:], sslFields, "big")
	if err != nil {
		return nil, fmt.Errorf("failed to parse OpenSSL header")
	}

	salt := sslHeader["salt"]
	if salt == 0 {
		return nil, fmt.Errorf("OpenSSL salt is zero")
	}

	if isSaltInvalid(salt) {
		return nil, fmt.Errorf("OpenSSL salt is invalid")
	}

	if offset == 0 {
		result.Confidence = ConfidenceMedium
	}

	result.Description = fmt.Sprintf("%s, salt: %#X", result.Description, salt)
	return result, nil
}

// ---- GPG ----

const DescriptionGPGSigned = "GPG signed file"

// gpgSignedMagic returns magic bytes for GPG signed files.
func gpgSignedMagic() [][]byte {
	return [][]byte{
		{0xA3, 0x01},
	}
}

// gpgSignedParser validates GPG signed file signatures.
// The Rust version does a zlib decompression dry run; here we do a simplified validation.
func gpgSignedParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Offset:      offset,
		Confidence:  ConfidenceHigh,
		Description: DescriptionGPGSigned,
	}

	// GPG signed files are zlib compressed with magic bytes replaced.
	// Validate there is enough data and the zlib-like stream follows.
	if offset+4 > len(data) {
		return nil, fmt.Errorf("insufficient GPG data")
	}

	// The third byte should be a valid zlib CMF byte (deflate method = 8)
	cmf := data[offset+2]
	if cmf&0x0F != 8 {
		return nil, fmt.Errorf("GPG: invalid deflate method in underlying zlib stream")
	}

	result.Description = fmt.Sprintf("%s, compressed data at offset %d", result.Description, offset)
	return result, nil
}

// ---- PEM ----

const (
	DescriptionPEMCertificate = "PEM certificate"
	DescriptionPEMPublicKey   = "PEM public key"
	DescriptionPEMPrivateKey  = "PEM private key"
)

// pemCertificateMagic returns magic bytes for PEM certificates.
func pemCertificateMagic() [][]byte {
	return [][]byte{
		[]byte("-----BEGIN CERTIFICATE-----"),
	}
}

// pemPublicKeyMagic returns magic bytes for PEM public keys.
func pemPublicKeyMagic() [][]byte {
	return [][]byte{
		[]byte("-----BEGIN PUBLIC KEY-----"),
		[]byte("-----BEGIN RSA PUBLIC KEY-----"),
		[]byte("-----BEGIN DSA PUBLIC KEY-----"),
		[]byte("-----BEGIN ECDSA PUBLIC KEY-----"),
	}
}

// pemPrivateKeyMagic returns magic bytes for PEM private keys.
func pemPrivateKeyMagic() [][]byte {
	return [][]byte{
		[]byte("-----BEGIN PRIVATE KEY-----"),
		[]byte("-----BEGIN EC PRIVATE KEY-----"),
		[]byte("-----BEGIN RSA PRIVATE KEY-----"),
		[]byte("-----BEGIN DSA PRIVATE KEY-----"),
		[]byte("-----BEGIN OPENSSH PRIVATE KEY-----"),
		[]byte("-----BEGIN ANY PRIVATE KEY-----"),
		[]byte("-----BEGIN ENCRYPTED PRIVATE KEY-----"),
		[]byte("-----BEGIN TSS2 PRIVATE KEY-----"),
	}
}

func decodePEMData(pemData []byte) (int, error) {
	const delim = "--"

	pemStr := string(pemData)
	delimCount := 0
	var base64Str strings.Builder

	for _, line := range strings.Split(pemStr, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, delim) {
			delimCount++
			if delimCount == 2 {
				break
			}
			continue
		}
		base64Str.WriteString(line)
	}

	if base64Str.Len() == 0 {
		return 0, fmt.Errorf("no PEM base64 data found")
	}

	decoded, err := base64.StdEncoding.DecodeString(base64Str.String())
	if err != nil {
		return 0, fmt.Errorf("PEM base64 decode failed: %v", err)
	}

	return len(decoded), nil
}

// pemFindEnd locates the end delimiter and returns the total PEM size.
func pemFindEnd(data []byte, offset int) (int, bool) {
	const endPrefix = "-----END "
	pemData := string(data[offset:])
	idx := strings.Index(pemData, endPrefix)
	if idx < 0 {
		return 0, false
	}
	// Find end of the delimiter line
	rest := pemData[idx:]
	nlIdx := strings.Index(rest, "\n")
	if nlIdx < 0 {
		return idx + len(rest), true
	}
	return idx + nlIdx + 1, true
}

// pemParser validates PEM certificate and key signatures.
func pemParser(data []byte, offset int) (*SignatureResult, error) {
	const minPEMLen = 26

	result := &SignatureResult{
		Offset:     offset,
		Confidence: ConfidenceHigh,
	}

	if offset+minPEMLen > len(data) {
		return nil, fmt.Errorf("insufficient PEM data")
	}

	pemMagic := data[offset : offset+minPEMLen]
	pemMagicStr := string(pemMagic)

	// Determine type based on first 26 bytes
	matched := false
	for _, m := range pemPublicKeyMagic() {
		if len(m) >= minPEMLen && strings.HasPrefix(string(m), pemMagicStr[:minPEMLen]) {
			result.Description = DescriptionPEMPublicKey
			matched = true
			break
		}
	}
	if !matched {
		for _, m := range pemPrivateKeyMagic() {
			if len(m) >= minPEMLen && strings.HasPrefix(string(m), pemMagicStr[:minPEMLen]) {
				result.Description = DescriptionPEMPrivateKey
				matched = true
				break
			}
		}
	}
	if !matched {
		for _, m := range pemCertificateMagic() {
			if len(m) >= minPEMLen && strings.HasPrefix(string(m), pemMagicStr[:minPEMLen]) {
				result.Description = DescriptionPEMCertificate
				matched = true
				break
			}
		}
	}
	if !matched {
		return nil, fmt.Errorf("PEM magic not recognised")
	}

	// Find the end of PEM data
	pemSize, found := pemFindEnd(data, offset)
	if !found || pemSize == 0 {
		return nil, fmt.Errorf("PEM end delimiter not found")
	}

	// Validate base64 content
	if offset+pemSize > len(data) {
		pemSize = len(data) - offset
	}
	if _, err := decodePEMData(data[offset : offset+pemSize]); err != nil {
		return nil, fmt.Errorf("PEM validation failed: %v", err)
	}

	if offset == 0 && pemSize == len(data) {
		result.ExtractionDeclined = true
	}

	result.Size = pemSize
	return result, nil
}

// ---- PKCS DER ----

const DescriptionDERHash = "PKCS DER hash"

func derHashLookups() map[string][]byte {
	return map[string][]byte{
		"MD5":    {0x30, 0x20, 0x30, 0x0c, 0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x02, 0x05, 0x05, 0x00, 0x04, 0x10},
		"SHA1":   {0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2b, 0x0e, 0x03, 0x02, 0x1a, 0x05, 0x00, 0x04, 0x14},
		"SHA256": {0x30, 0x31, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05, 0x00, 0x04, 0x20},
		"SHA384": {0x30, 0x41, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02, 0x05, 0x00, 0x04, 0x30},
		"SHA512": {0x30, 0x51, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03, 0x05, 0x00, 0x04},
	}
}

// derHashMagic returns magic bytes for all DER hash types.
func derHashMagic() [][]byte {
	var magics [][]byte
	for _, m := range derHashLookups() {
		mc := make([]byte, len(m))
		copy(mc, m)
		magics = append(magics, mc)
	}
	return magics
}

// derHashParser validates PKCS DER hash signatures.
func derHashParser(data []byte, offset int) (*SignatureResult, error) {
	result := &SignatureResult{
		Offset:      offset,
		Description: DescriptionDERHash,
		Confidence:  ConfidenceMedium,
	}

	for name, magic := range derHashLookups() {
		hashEnd := offset + len(magic)
		if hashEnd > len(data) {
			continue
		}
		match := true
		for i, b := range magic {
			if data[offset+i] != b {
				match = false
				break
			}
		}
		if match {
			result.Size = len(magic)
			result.Description = fmt.Sprintf("%s, %s", result.Description, name)
			return result, nil
		}
	}

	return nil, fmt.Errorf("DER hash validation failed")
}

// ---- LUKS ----

const DescriptionLUKS = "LUKS header"

// luksMagic returns magic bytes for LUKS headers.
func luksMagic() [][]byte {
	return [][]byte{
		{0x4C, 0x55, 0x4B, 0x53, 0xBA, 0xBE}, // "LUKS\xBA\xBE"
	}
}

// luksParser validates LUKS header signatures.
func luksParser(data []byte, offset int) (*SignatureResult, error) {
	const cipherAlgoStart = 8
	const cipherAlgoEnd = 40
	const cipherModeStart = 40
	const cipherModeEnd = 72
	const hashFnStart = 72
	const hashFnEnd = 104
	const luks2MinHeaderSize = 4032

	luksBaseFields := []StructField{
		{"magic_1", "u32"},
		{"magic_2", "u16"},
		{"version", "u16"},
		{"header_size", "u64"},
	}

	result := &SignatureResult{
		Offset:      offset,
		Name:        "luks",
		Description: DescriptionLUKS,
		Confidence:  ConfidenceMedium,
	}

	if offset+StructSize(luksBaseFields) > len(data) {
		return nil, fmt.Errorf("insufficient LUKS data")
	}

	luksBase, err := Parse(data[offset:], luksBaseFields, "big")
	if err != nil {
		return nil, fmt.Errorf("failed to parse LUKS header")
	}

	version := luksBase["version"]

	// Hash function string
	if offset+hashFnEnd > len(data) {
		return nil, fmt.Errorf("LUKS hash function data out of bounds")
	}
	hashFn := GetCString(data[offset+hashFnStart : offset+hashFnEnd])
	if hashFn == "" {
		return nil, fmt.Errorf("LUKS hash function string empty")
	}

	if version == 1 {
		if offset+cipherAlgoEnd > len(data) {
			return nil, fmt.Errorf("LUKS cipher data out of bounds")
		}
		cipherAlgo := GetCString(data[offset+cipherAlgoStart : offset+cipherAlgoEnd])
		cipherMode := GetCString(data[offset+cipherModeStart : offset+cipherModeEnd])
		if cipherAlgo == "" || cipherMode == "" {
			return nil, fmt.Errorf("LUKS v1 cipher strings empty")
		}
		result.Description = fmt.Sprintf("%s, version: %d, cipher algorithm: %s, cipher mode: %s, hash fn: %s",
			result.Description, version, cipherAlgo, cipherMode, hashFn)
		return result, nil
	} else if version == 2 {
		headerSize := luksBase["header_size"]
		availableData := len(data) - offset
		if headerSize <= luks2MinHeaderSize || headerSize >= availableData {
			return nil, fmt.Errorf("LUKS v2 header size invalid")
		}
		result.Description = fmt.Sprintf("%s, version: %d, header size: %d bytes, hash fn: %s",
			result.Description, version, headerSize, hashFn)
		return result, nil
	}

	return nil, fmt.Errorf("unsupported LUKS version: %d", version)
}

// ---- Hash constants ----

const hashMagicLen = 16

const (
	DescriptionCRC32  = "CRC32 polynomial table"
	DescriptionSHA256 = "SHA256 hash constants"
	DescriptionMD5    = "MD5 hash constants"
)

// crc32Magic returns magic bytes for CRC32 polynomial tables.
// Order matters: index 0 is big endian, index 1 is little endian.
func crc32ConstMagic() [][]byte {
	return [][]byte{
		// Big endian
		{0x00, 0x00, 0x00, 0x00, 0x77, 0x07, 0x30, 0x96, 0xEE, 0x0E, 0x61, 0x2C, 0x99, 0x09, 0x51, 0xBA},
		// Little endian
		{0x00, 0x00, 0x00, 0x00, 0x96, 0x30, 0x07, 0x77, 0x2C, 0x61, 0x0E, 0xEE, 0xBA, 0x51, 0x09, 0x99},
	}
}

// sha256Magic returns magic bytes for SHA256 hash constants.
func sha256ConstMagic() [][]byte {
	return [][]byte{
		// Big endian
		{0x42, 0x8a, 0x2f, 0x98, 0x71, 0x37, 0x44, 0x91, 0xb5, 0xc0, 0xfb, 0xcf, 0xe9, 0xb5, 0xdb, 0xa5},
		// Little endian
		{0x98, 0x2f, 0x8a, 0x42, 0x91, 0x44, 0x37, 0x71, 0xcf, 0xfb, 0xc0, 0xb5, 0xa5, 0xdb, 0xb5, 0xe9},
	}
}

// md5Magic returns magic bytes for MD5 hash constants.
func md5ConstMagic() [][]byte {
	return [][]byte{
		// Big endian
		{0xd7, 0x6a, 0xa4, 0x78, 0xe8, 0xc7, 0xb7, 0x56, 0x24, 0x20, 0x70, 0xdb, 0xc1, 0xbd, 0xce, 0xee},
		// Little endian
		{0x78, 0xa4, 0x6a, 0xd7, 0x56, 0xb7, 0xc7, 0xe8, 0xdb, 0x70, 0x20, 0x24, 0xee, 0xce, 0xbd, 0xc1},
	}
}

func hashEndianness(data []byte, offset int, magics [][]byte) string {
	if offset+hashMagicLen > len(data) {
		return "little"
	}
	thisMagic := data[offset : offset+hashMagicLen]
	if len(magics) > 0 && len(magics[0]) == hashMagicLen {
		match := true
		for i := 0; i < hashMagicLen; i++ {
			if thisMagic[i] != magics[0][i] {
				match = false
				break
			}
		}
		if match {
			return "big"
		}
	}
	return "little"
}

// crc32Parser validates CRC32 polynomial table signatures.
func crc32Parser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Size:        hashMagicLen,
		Description: fmt.Sprintf("%s, %s endian", DescriptionCRC32, hashEndianness(data, offset, crc32ConstMagic())),
	}, nil
}

// sha256Parser validates SHA256 hash constant signatures.
func sha256Parser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Size:        hashMagicLen,
		Description: fmt.Sprintf("%s, %s endian", DescriptionSHA256, hashEndianness(data, offset, sha256ConstMagic())),
	}, nil
}

// md5Parser validates MD5 hash constant signatures.
func md5Parser(data []byte, offset int) (*SignatureResult, error) {
	return &SignatureResult{
		Offset:      offset,
		Size:        hashMagicLen,
		Description: fmt.Sprintf("%s, %s endian", DescriptionMD5, hashEndianness(data, offset, md5ConstMagic())),
	}, nil
}
