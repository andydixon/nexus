package analysis

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"hash/crc32"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// HashSet contains CRC32/MD5/SHA-1/SHA-256/SHA-512 for a range.
type HashSet struct {
	Length int64  `json:"length"`
	CRC32  string `json:"crc32"`
	MD5    string `json:"md5"`
	SHA1   string `json:"sha1"`
	SHA256 string `json:"sha256"`
	SHA512 string `json:"sha512"`
}

// Hashes computes all supported hashes over the document range.
// The computation runs chunk-by-chunk so large files do not force a
// full-buffer duplication.
func Hashes(d *document.Document, start, length int64) (map[string]any, error) {
	if length < 0 {
		length = d.Size() - start
	}

	crc := crc32.NewIEEE()
	mh := md5.New()
	s1 := sha1.New()
	s256 := sha256.New()
	s512 := sha512.New()

	writers := []hash.Hash{mh, s1, s256, s512}

	total := int64(0)
	err := d.ForEachChunk(start, length, func(_ int64, chunk []byte) error {
		crc.Write(chunk)
		for _, w := range writers {
			w.Write(chunk)
		}
		total += int64(len(chunk))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"length": total,
		"crc32":  toHex(crc.Sum(nil)),
		"md5":    toHex(mh.Sum(nil)),
		"sha1":   toHex(s1.Sum(nil)),
		"sha256": toHex(s256.Sum(nil)),
		"sha512": toHex(s512.Sum(nil)),
	}, nil
}

func toHex(b []byte) string { return hex.EncodeToString(b) }
