package analysis

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/andydixon/link/internal/leviathan/document"
)

// KeyArtefact is a single key-ish object we've found in the raw file.
// Length is the span we recognised; Note carries any decoding detail.
type KeyArtefact struct {
	Kind    string `json:"kind"`
	Format  string `json:"format"` // "pem" | "der" | "ssh" | "p12"
	Offset  int64  `json:"offset"`
	Length  int64  `json:"length"`
	Summary string `json:"summary"`
	Note    string `json:"note,omitempty"`
}

// ScanKeys walks the whole document looking for cryptographic artefacts:
//   - PEM blocks with common key-related labels
//   - OpenSSH keys (authorized_keys lines and the "openssh-key-v1" private-key envelope)
//   - PKCS#12 magic (1.2.840.113549.1.12.1 appears in headers)
//   - Loose DER SEQUENCEs that parse as RSA/EC/PKCS#8 keys
//
// We cap the scan at 64 MiB. Users with larger blobs can select a range
// and call this against that subset via a future "keys.scanRange" bridge.
func ScanKeys(d *document.Document) ([]KeyArtefact, error) {
	size := d.Size()
	if size > 64*1024*1024 {
		return nil, fmt.Errorf("keys.scan: file > 64 MiB; use selection")
	}
	buf, err := d.Range(0, size)
	if err != nil {
		return nil, err
	}
	out := []KeyArtefact{}
	out = append(out, scanPEMKeys(buf)...)
	out = append(out, scanOpenSSHPubKeys(buf)...)
	out = append(out, scanOpenSSHPrivKeys(buf)...)
	out = append(out, scanPKCS12(buf)...)
	// Loose DER pass — for raw PKCS#8 / PKCS#1 blobs that aren't wrapped in PEM.
	out = append(out, scanLooseDERKeys(buf)...)
	return out, nil
}

// Known PEM labels that indicate key-ish content.
var pemKeyLabels = map[string]string{
	"RSA PRIVATE KEY":         "rsa-private",
	"RSA PUBLIC KEY":          "rsa-public",
	"EC PRIVATE KEY":          "ec-private",
	"EC PUBLIC KEY":           "ec-public",
	"DSA PRIVATE KEY":         "dsa-private",
	"PRIVATE KEY":             "pkcs8-private",
	"ENCRYPTED PRIVATE KEY":   "pkcs8-encrypted",
	"PUBLIC KEY":              "pkix-public",
	"OPENSSH PRIVATE KEY":     "openssh-private",
	"SSH2 PUBLIC KEY":         "ssh2-public",
	"PGP PRIVATE KEY BLOCK":   "pgp-private",
	"PGP PUBLIC KEY BLOCK":    "pgp-public",
	"CERTIFICATE REQUEST":     "csr",
	"NEW CERTIFICATE REQUEST": "csr",
	"DH PARAMETERS":           "dh-params",
	"PARAMETERS":              "params",
}

func scanPEMKeys(buf []byte) []KeyArtefact {
	out := []KeyArtefact{}
	rest := buf
	origLen := int64(len(buf))
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		beginIdx := bytes.Index(rest, []byte("-----BEGIN"))
		if beginIdx < 0 {
			break
		}
		start := origLen - int64(len(rest)) + int64(beginIdx)
		end := origLen - int64(len(next))
		kind, match := pemKeyLabels[block.Type]
		if !match {
			rest = next
			continue
		}
		art := KeyArtefact{
			Kind: kind, Format: "pem",
			Offset: start, Length: end - start,
		}
		art.Summary = describePEMBlock(block)
		out = append(out, art)
		rest = next
	}
	return out
}

func describePEMBlock(b *pem.Block) string {
	name := b.Type
	switch b.Type {
	case "RSA PRIVATE KEY":
		if k, err := x509.ParsePKCS1PrivateKey(b.Bytes); err == nil {
			return fmt.Sprintf("%s — RSA %d-bit", name, k.N.BitLen())
		}
	case "RSA PUBLIC KEY":
		if k, err := x509.ParsePKCS1PublicKey(b.Bytes); err == nil {
			return fmt.Sprintf("%s — RSA %d-bit", name, k.N.BitLen())
		}
	case "EC PRIVATE KEY":
		if k, err := x509.ParseECPrivateKey(b.Bytes); err == nil {
			return fmt.Sprintf("%s — curve %s", name, k.Curve.Params().Name)
		}
	case "PRIVATE KEY":
		if k, err := x509.ParsePKCS8PrivateKey(b.Bytes); err == nil {
			return fmt.Sprintf("%s — %s", name, describePrivateKey(k))
		}
	case "PUBLIC KEY":
		if k, err := x509.ParsePKIXPublicKey(b.Bytes); err == nil {
			return fmt.Sprintf("%s — %s", name, describePublicKey(k))
		}
	case "OPENSSH PRIVATE KEY":
		return fmt.Sprintf("%s (%d B payload)", name, len(b.Bytes))
	case "ENCRYPTED PRIVATE KEY":
		return fmt.Sprintf("%s — PKCS#8 encrypted (%d B)", name, len(b.Bytes))
	}
	return fmt.Sprintf("%s (%d B)", name, len(b.Bytes))
}

func describePrivateKey(k any) string {
	switch key := k.(type) {
	case *rsa.PrivateKey:
		return fmt.Sprintf("RSA %d-bit", key.N.BitLen())
	case *ecdsa.PrivateKey:
		return fmt.Sprintf("ECDSA %s", key.Curve.Params().Name)
	case ed25519.PrivateKey:
		return "Ed25519"
	}
	return fmt.Sprintf("%T", k)
}

func describePublicKey(k any) string {
	switch key := k.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d-bit", key.N.BitLen())
	case *ecdsa.PublicKey:
		return fmt.Sprintf("ECDSA %s", key.Curve.Params().Name)
	case ed25519.PublicKey:
		return "Ed25519"
	}
	return fmt.Sprintf("%T", k)
}

// OpenSSH authorized_keys / id_*.pub format is one line per key:
// "<algo> <base64> [comment]\n". We search for well-known algos and
// delimit by the next newline. False positives on text logs are possible,
// so we validate by base64-decoding.
var sshAlgos = []string{
	"ssh-rsa", "ssh-dss", "ssh-ed25519",
	"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
	"sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com",
}

func scanOpenSSHPubKeys(buf []byte) []KeyArtefact {
	out := []KeyArtefact{}
	for _, algo := range sshAlgos {
		needle := []byte(algo + " ")
		off := 0
		for {
			idx := bytes.Index(buf[off:], needle)
			if idx < 0 {
				break
			}
			absStart := off + idx
			// Must be at start of line or after whitespace.
			if absStart > 0 {
				prev := buf[absStart-1]
				if prev != '\n' && prev != '\r' && prev != ' ' && prev != '\t' {
					off = absStart + 1
					continue
				}
			}
			// End of line.
			endOfLine := absStart + len(needle)
			for endOfLine < len(buf) && buf[endOfLine] != '\n' && buf[endOfLine] != '\r' {
				endOfLine++
			}
			line := buf[absStart:endOfLine]
			// Expect at least one space-separated payload that decodes.
			parts := bytes.SplitN(line, []byte(" "), 3)
			if len(parts) < 2 || len(parts[1]) < 16 || !isBase64Like(parts[1]) {
				off = absStart + 1
				continue
			}
			art := KeyArtefact{
				Kind: "openssh-public", Format: "ssh",
				Offset: int64(absStart), Length: int64(len(line)),
				Summary: fmt.Sprintf("%s (%d-byte payload b64)", algo, len(parts[1])),
			}
			if len(parts) == 3 && len(parts[2]) > 0 {
				art.Note = "comment: " + string(parts[2])
			}
			out = append(out, art)
			off = endOfLine
		}
	}
	return out
}

func scanOpenSSHPrivKeys(buf []byte) []KeyArtefact {
	// Already caught by PEM scan (label "OPENSSH PRIVATE KEY"). We also
	// look for the magic string inside the PEM body — "openssh-key-v1\0".
	out := []KeyArtefact{}
	needle := []byte("openssh-key-v1\x00")
	off := 0
	for {
		idx := bytes.Index(buf[off:], needle)
		if idx < 0 {
			break
		}
		absStart := off + idx
		out = append(out, KeyArtefact{
			Kind: "openssh-private-magic", Format: "ssh",
			Offset: int64(absStart), Length: int64(len(needle)),
			Summary: "OpenSSH v1 private-key magic",
			Note:    "magic only; no envelope length inferred",
		})
		off = absStart + len(needle)
	}
	return out
}

// PKCS#12 (.p12 / .pfx) files begin with an ASN.1 SEQUENCE; the second
// element is an OID for the bag type. Its encoded form is reasonably
// distinctive: "\x06\x0b\x2a\x86\x48\x86\xf7\x0d\x01\x07\x01" (data).
// The simplest reliable magic for our purposes is the OID for
// pkcs-12 (1.2.840.113549.1.12.1): "\x06\x0a\x2a\x86\x48\x86\xf7\x0d\x01\x0c\x01".
func scanPKCS12(buf []byte) []KeyArtefact {
	out := []KeyArtefact{}
	needle := []byte{0x06, 0x0a, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x0c, 0x01}
	off := 0
	for {
		idx := bytes.Index(buf[off:], needle)
		if idx < 0 {
			break
		}
		absStart := off + idx
		out = append(out, KeyArtefact{
			Kind: "pkcs12-oid", Format: "p12",
			Offset: int64(absStart), Length: int64(len(needle)),
			Summary: "pkcs-12 OID (1.2.840.113549.1.12.1)",
			Note:    "bag-type marker; may indicate a .p12 or .pfx container",
		})
		off = absStart + len(needle)
	}
	return out
}

// scanLooseDERKeys walks DER SEQUENCE headers and tries to parse each
// candidate as a known key format. False negatives are fine; we only
// emit an artefact on a positive parse.
func scanLooseDERKeys(buf []byte) []KeyArtefact {
	out := []KeyArtefact{}
	// Use the same SEQUENCE 0x30 0x82 xx yy (long-form, 2 length bytes)
	// pattern as the cert scanner. Minimum useful key length ~200 bytes.
	//
	// Each candidate runs up to six x509 parse attempts over a blob up to
	// 64 KiB, so an unfiltered scan of a buffer dense with "30 82" pairs is
	// O(n) expensive parses and could freeze the UI. A cheap structural
	// pre-filter (a DER key body begins with a SEQUENCE 0x30 or an INTEGER
	// 0x02) rejects almost all false candidates without parsing, and a hard
	// cap bounds adversarial input that defeats the pre-filter.
	const maxDERAttempts = 1 << 16
	derAttempts := 0
	for i := 0; i+4 < len(buf); i++ {
		if buf[i] != 0x30 || buf[i+1] != 0x82 {
			continue
		}
		if c := buf[i+4]; c != 0x30 && c != 0x02 {
			continue
		}
		total := int(buf[i+2])<<8 | int(buf[i+3])
		if total < 80 || i+4+total > len(buf) {
			continue
		}
		if derAttempts++; derAttempts > maxDERAttempts {
			break
		}
		blob := buf[i : i+4+total]
		// Skip anything that looks like a certificate (already caught by certs panel).
		if _, err := x509.ParseCertificate(blob); err == nil {
			i += 4 + total - 1
			continue
		}
		if _, err := x509.ParsePKCS1PrivateKey(blob); err == nil {
			out = append(out, KeyArtefact{
				Kind: "rsa-private", Format: "der",
				Offset: int64(i), Length: int64(4 + total),
				Summary: "RSA private key (PKCS#1 DER)",
			})
			i += 4 + total - 1
			continue
		}
		if _, err := x509.ParsePKCS1PublicKey(blob); err == nil {
			out = append(out, KeyArtefact{
				Kind: "rsa-public", Format: "der",
				Offset: int64(i), Length: int64(4 + total),
				Summary: "RSA public key (PKCS#1 DER)",
			})
			i += 4 + total - 1
			continue
		}
		if k, err := x509.ParseECPrivateKey(blob); err == nil {
			out = append(out, KeyArtefact{
				Kind: "ec-private", Format: "der",
				Offset: int64(i), Length: int64(4 + total),
				Summary: "EC private key (SEC1 DER) curve=" + k.Curve.Params().Name,
			})
			i += 4 + total - 1
			continue
		}
		if k, err := x509.ParsePKCS8PrivateKey(blob); err == nil {
			out = append(out, KeyArtefact{
				Kind: "pkcs8-private", Format: "der",
				Offset: int64(i), Length: int64(4 + total),
				Summary: "PKCS#8 private key — " + describePrivateKey(k),
			})
			i += 4 + total - 1
			continue
		}
		if k, err := x509.ParsePKIXPublicKey(blob); err == nil {
			out = append(out, KeyArtefact{
				Kind: "pkix-public", Format: "der",
				Offset: int64(i), Length: int64(4 + total),
				Summary: "PKIX public key — " + describePublicKey(k),
			})
			i += 4 + total - 1
			continue
		}
	}
	return out
}

func isBase64Like(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '+' || c == '/' || c == '=' {
			continue
		}
		return false
	}
	return true
}

// Stringer helper used by the bridge; avoids leaking internal types to JS.
func (a KeyArtefact) String() string {
	return strings.TrimSpace(fmt.Sprintf("%s %s @0x%x %s", a.Kind, a.Format, a.Offset, a.Summary))
}
