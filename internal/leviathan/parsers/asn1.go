package parsers

import (
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/andydixon/link/internal/leviathan/document"
)

// ParseASN1 produces a field tree for the DER TLV structure starting
// at `at`, spanning at most `length` bytes. Nested SEQUENCE / SET
// structures are decoded recursively; primitive content is rendered
// as best-effort.
func ParseASN1(d *document.Document, at, length int64) (*Field, error) {
	size := d.Size()
	if length < 0 || at+length > size {
		length = size - at
	}
	if length <= 0 {
		return nil, errors.New("asn1: empty range")
	}
	buf, err := d.Range(at, length)
	if err != nil {
		return nil, err
	}
	// If the buffer looks like PEM, decode first.
	if pemBlock, _ := pem.Decode(buf); pemBlock != nil {
		root := &Field{Name: "PEM " + pemBlock.Type, Type: "container", Offset: at, Length: length}
		inner, err := decodeASN1(pemBlock.Bytes, at)
		if err == nil {
			root.Children = append(root.Children, inner)
		} else {
			root.Note = "pem decoded but asn1 failed: " + err.Error()
		}
		return root, nil
	}
	return decodeASN1(buf, at)
}

// decodeASN1 walks buf recursively, treating it as one or more DER TLVs.
func decodeASN1(buf []byte, base int64) (*Field, error) {
	root := &Field{Name: "ASN.1", Type: "container", Offset: base, Length: int64(len(buf))}
	off := 0
	for off < len(buf) {
		raw, consumed, err := readTLV(buf[off:])
		if err != nil {
			if off == 0 {
				return nil, err
			}
			break
		}
		node := asn1Node(raw, base+int64(off))
		root.Children = append(root.Children, node)
		off += consumed
	}
	if len(root.Children) == 0 {
		return nil, errors.New("asn1: no TLV decoded")
	}
	if len(root.Children) == 1 {
		return root.Children[0], nil
	}
	return root, nil
}

// tlv is one DER TLV we've split out of the byte stream.
type tlv struct {
	Class       int // 0=universal, 1=application, 2=context, 3=private
	Constructed bool
	Tag         int
	Content     []byte // the V of the TLV
	HeaderLen   int    // length of T+L bytes
}

func readTLV(buf []byte) (tlv, int, error) {
	if len(buf) < 2 {
		return tlv{}, 0, errors.New("short TLV")
	}
	b0 := buf[0]
	t := tlv{
		Class:       int(b0 >> 6),
		Constructed: b0&0x20 != 0,
	}
	tagVal := int(b0 & 0x1f)
	i := 1
	if tagVal == 0x1f {
		// multi-byte tag
		tagVal = 0
		for i < len(buf) {
			c := buf[i]
			i++
			tagVal = (tagVal << 7) | int(c&0x7f)
			if c&0x80 == 0 {
				break
			}
		}
	}
	t.Tag = tagVal
	if i >= len(buf) {
		return tlv{}, 0, errors.New("truncated length")
	}
	ln := int(buf[i])
	i++
	if ln >= 0x80 {
		n := ln & 0x7f
		if n == 0 || n > 8 || i+n > len(buf) {
			return tlv{}, 0, errors.New("bad length")
		}
		ln = 0
		for k := 0; k < n; k++ {
			ln = (ln << 8) | int(buf[i+k])
		}
		i += n
	}
	if i+ln > len(buf) {
		return tlv{}, 0, errors.New("length exceeds buffer")
	}
	t.HeaderLen = i
	t.Content = buf[i : i+ln]
	return t, i + ln, nil
}

func asn1Node(t tlv, off int64) *Field {
	name, kind := asn1TagName(t)
	total := int64(t.HeaderLen + len(t.Content))
	node := &Field{Name: name, Type: kind, Offset: off, Length: total}
	if t.Class == 2 {
		node.Name = fmt.Sprintf("[%d]", t.Tag)
	}
	if t.Constructed {
		// Recurse into children.
		inner := t.Content
		childOff := off + int64(t.HeaderLen)
		i := 0
		for i < len(inner) {
			child, consumed, err := readTLV(inner[i:])
			if err != nil {
				break
			}
			node.Children = append(node.Children, asn1Node(child, childOff+int64(i)))
			i += consumed
		}
	} else {
		node.Value = renderPrimitive(t)
	}
	return node
}

func asn1TagName(t tlv) (string, string) {
	if t.Class != 0 {
		prefix := []string{"", "APP ", "CTX ", "PRIV "}[t.Class]
		return fmt.Sprintf("%s%d", prefix, t.Tag), "tag"
	}
	names := map[int]string{
		1:  "BOOLEAN",
		2:  "INTEGER",
		3:  "BIT STRING",
		4:  "OCTET STRING",
		5:  "NULL",
		6:  "OBJECT IDENTIFIER",
		10: "ENUMERATED",
		12: "UTF8String",
		16: "SEQUENCE",
		17: "SET",
		19: "PrintableString",
		20: "T61String",
		22: "IA5String",
		23: "UTCTime",
		24: "GeneralizedTime",
	}
	if n, ok := names[t.Tag]; ok {
		return n, strings.ToLower(n)
	}
	return fmt.Sprintf("tag %d", t.Tag), "tag"
}

func renderPrimitive(t tlv) string {
	switch t.Tag {
	case 1:
		if len(t.Content) == 1 {
			if t.Content[0] == 0 {
				return "false"
			}
			return "true"
		}
	case 2:
		return fmt.Sprintf("%s (%d bytes)", bigIntString(t.Content), len(t.Content))
	case 3:
		if len(t.Content) == 0 {
			return "empty"
		}
		return fmt.Sprintf("unused=%d bits=%x", t.Content[0], t.Content[1:])
	case 4:
		if isPrintable(t.Content) {
			return fmt.Sprintf("%q (%d bytes)", string(t.Content), len(t.Content))
		}
		return fmt.Sprintf("%x (%d bytes)", t.Content, len(t.Content))
	case 5:
		return "null"
	case 6:
		oid, err := parseOID(t.Content)
		if err != nil {
			return err.Error()
		}
		return oid + oidNote(oid)
	case 12, 19, 20, 22:
		return fmt.Sprintf("%q", string(t.Content))
	case 23, 24:
		return fmt.Sprintf("%q (time)", string(t.Content))
	}
	if isPrintable(t.Content) {
		return fmt.Sprintf("%q", string(t.Content))
	}
	if len(t.Content) > 32 {
		return fmt.Sprintf("%x… (%d bytes)", t.Content[:32], len(t.Content))
	}
	return fmt.Sprintf("%x", t.Content)
}

func bigIntString(b []byte) string {
	if len(b) == 0 {
		return "0"
	}
	if len(b) <= 8 {
		var v int64
		negative := b[0]&0x80 != 0
		for _, c := range b {
			v = (v << 8) | int64(c)
		}
		if negative {
			// sign-extend
			v -= 1 << uint(8*len(b))
		}
		return fmt.Sprintf("%d", v)
	}
	// large — hex is more useful than a decimal approximation.
	return fmt.Sprintf("0x%x", b)
}

func parseOID(b []byte) (string, error) {
	// Use Go's asn1 parser for correctness.
	var oid asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(append([]byte{0x06, byte(len(b))}, b...), &oid)
	if err != nil || len(rest) != 0 {
		// Fallback manual decode.
		return manualOID(b), nil
	}
	return oid.String(), nil
}

func manualOID(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	parts := []string{}
	first := int(b[0])
	parts = append(parts, fmt.Sprintf("%d.%d", first/40, first%40))
	v := 0
	for _, c := range b[1:] {
		v = (v << 7) | int(c&0x7f)
		if c&0x80 == 0 {
			parts = append(parts, fmt.Sprintf("%d", v))
			v = 0
		}
	}
	return strings.Join(parts, ".")
}

// Known-OID table for human-readable hints in cert dumps.
var oidNames = map[string]string{
	"1.2.840.113549.1.1.1":  " rsaEncryption",
	"1.2.840.113549.1.1.11": " sha256WithRSAEncryption",
	"1.2.840.113549.1.1.5":  " sha1WithRSAEncryption",
	"1.2.840.113549.1.1.13": " sha512WithRSAEncryption",
	"1.2.840.10045.2.1":     " ecPublicKey",
	"1.2.840.10045.4.3.2":   " ecdsaWithSHA256",
	"1.3.101.112":           " Ed25519",
	"2.5.4.3":               " commonName",
	"2.5.4.6":               " countryName",
	"2.5.4.7":               " localityName",
	"2.5.4.8":               " stateOrProvinceName",
	"2.5.4.10":              " organizationName",
	"2.5.4.11":              " organizationalUnitName",
	"2.5.4.5":               " serialNumber",
	"1.2.840.113549.1.9.1":  " emailAddress",
	"2.5.29.14":             " subjectKeyIdentifier",
	"2.5.29.15":             " keyUsage",
	"2.5.29.17":             " subjectAltName",
	"2.5.29.19":             " basicConstraints",
	"2.5.29.31":             " cRLDistributionPoints",
	"2.5.29.32":             " certificatePolicies",
	"2.5.29.35":             " authorityKeyIdentifier",
	"2.5.29.37":             " extKeyUsage",
	"1.3.6.1.5.5.7.3.1":     " serverAuth",
	"1.3.6.1.5.5.7.3.2":     " clientAuth",
	"1.3.6.1.5.5.7.1.1":     " authorityInfoAccess",
}

func oidNote(oid string) string {
	if n, ok := oidNames[oid]; ok {
		return n
	}
	return ""
}

func isPrintable(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c == 0x09 || c == 0x0a || c == 0x0d {
			continue
		}
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
