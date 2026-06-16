// Package transforms exposes chainable transform primitives. Each
// function takes an input []byte and options, and returns an output
// or an error. They are pure and do not mutate input.
package transforms

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
)

const MaxInflateOutput = 64 << 20

// HexEncode returns ASCII hex of src.
func HexEncode(src []byte) ([]byte, error) {
	return []byte(hex.EncodeToString(src)), nil
}

// HexDecode parses ASCII hex into bytes. Whitespace is ignored.
func HexDecode(src []byte) ([]byte, error) {
	stripped := stripHex(src)
	out := make([]byte, len(stripped)/2)
	_, err := hex.Decode(out, stripped)
	return out, err
}

func stripHex(s []byte) []byte {
	out := make([]byte, 0, len(s))
	for _, b := range s {
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' || b == ',' || b == ':' {
			continue
		}
		if b == '0' && len(out) == 0 {
			continue
		}
		out = append(out, b)
	}
	return out
}

// Base64Encode returns the standard Base64 of src.
func Base64Encode(src []byte) ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString(src)), nil
}

// Base64Decode accepts standard or URL-safe Base64, with or without padding.
func Base64Decode(src []byte) ([]byte, error) {
	s := string(bytes.TrimSpace(src))
	attempts := []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range attempts {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not valid base64")
}

// URLEncode applies percent-encoding.
func URLEncode(src []byte) ([]byte, error) {
	return []byte(url.QueryEscape(string(src))), nil
}

// URLDecode reverses percent-encoding.
func URLDecode(src []byte) ([]byte, error) {
	v, err := url.QueryUnescape(string(src))
	if err != nil {
		return nil, err
	}
	return []byte(v), nil
}

// XOR returns src^key, cycling key.
func XOR(src, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("empty key")
	}
	out := make([]byte, len(src))
	for i := range src {
		out[i] = src[i] ^ key[i%len(key)]
	}
	return out, nil
}

// InflateGzip, InflateZlib, InflateRaw attempt to decompress.
func InflateGzip(src []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readLimitedInflate(r)
}

func InflateZlib(src []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readLimitedInflate(r)
}

func InflateRaw(src []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(src))
	defer r.Close()
	return readLimitedInflate(r)
}

func readLimitedInflate(r io.Reader) ([]byte, error) {
	lr := io.LimitReader(r, MaxInflateOutput+1)
	out, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxInflateOutput {
		return nil, errors.New("decompressed output exceeds 64 MiB limit")
	}
	return out, nil
}

// TryInflate attempts gzip, zlib, and raw deflate in order.
func TryInflate(src []byte) (out []byte, kind string, err error) {
	if b, e := InflateGzip(src); e == nil {
		return b, "gzip", nil
	}
	if b, e := InflateZlib(src); e == nil {
		return b, "zlib", nil
	}
	if b, e := InflateRaw(src); e == nil {
		return b, "raw", nil
	}
	return nil, "", errors.New("no decompression succeeded")
}

// Step is one entry in a transform pipeline.
type Step struct {
	Op string // hex.encode, hex.decode, base64.encode, base64.decode,
	// xor, inflate, url.encode, url.decode, caesar, rot13, atbash,
	// vigenere.enc, vigenere.dec
	Arg string // ASCII parameter (e.g. key for XOR/Vigenere, shift for Caesar)
}

// ApplyPipeline runs steps left-to-right. Intermediate results are
// returned for inspection.
func ApplyPipeline(input []byte, steps []Step) ([][]byte, error) {
	results := make([][]byte, 0, len(steps)+1)
	cur := append([]byte(nil), input...)
	results = append(results, cur)
	for _, s := range steps {
		out, err := Apply(cur, s)
		if err != nil {
			return results, err
		}
		cur = out
		results = append(results, cur)
	}
	return results, nil
}

// Apply one step.
func Apply(in []byte, s Step) ([]byte, error) {
	switch s.Op {
	case "hex.encode":
		return HexEncode(in)
	case "hex.decode":
		return HexDecode(in)
	case "base64.encode":
		return Base64Encode(in)
	case "base64.decode":
		return Base64Decode(in)
	case "url.encode":
		return URLEncode(in)
	case "url.decode":
		return URLDecode(in)
	case "xor":
		key, err := parseKey(s.Arg)
		if err != nil {
			return nil, err
		}
		return XOR(in, key)
	case "inflate":
		switch s.Arg {
		case "gzip":
			return InflateGzip(in)
		case "zlib":
			return InflateZlib(in)
		case "raw":
			return InflateRaw(in)
		default:
			out, _, err := TryInflate(in)
			return out, err
		}
	case "caesar":
		n := 13
		if s.Arg != "" {
			var v int
			for _, c := range s.Arg {
				if c >= '0' && c <= '9' {
					v = v*10 + int(c-'0')
				}
			}
			n = v
		}
		return Caesar(in, n), nil
	case "rot13":
		return Caesar(in, 13), nil
	case "atbash":
		return Atbash(in), nil
	case "vigenere.enc":
		return Vigenere(in, []byte(s.Arg), true), nil
	case "vigenere.dec":
		return Vigenere(in, []byte(s.Arg), false), nil
	}
	return nil, errors.New("unknown op: " + s.Op)
}

// parseKey accepts either literal string (ASCII) or hex-prefixed "hex:..".
func parseKey(arg string) ([]byte, error) {
	if len(arg) > 4 && arg[:4] == "hex:" {
		return hex.DecodeString(arg[4:])
	}
	if arg == "" {
		return nil, errors.New("empty key")
	}
	return []byte(arg), nil
}
