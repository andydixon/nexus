package parsers

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// ParseCerts scans the whole document for PEM-encoded certificates
// and for DER-encoded X.509 certificates appearing at arbitrary
// offsets (heuristic: SEQUENCE 0x30 0x82 <len>, then tbsCertificate
// SEQUENCE, then version INTEGER). Returns a list of decoded certs.
func ParseCerts(d *document.Document) (*Field, error) {
	size := d.Size()
	root := &Field{Name: "Certificates", Type: "container", Offset: 0, Length: size}

	// Read whole doc — bounded by caller's judgment. For huge firmware
	// blobs this might be expensive but certs are small; most real
	// cases are <few MB of text or firmware tables.
	if size > 32*1024*1024 {
		return nil, errors.New("certs scan skipped: file > 32 MiB")
	}
	all, err := d.Range(0, size)
	if err != nil {
		return nil, err
	}

	// PEM pass.
	rest := all
	pemCount := 0
	var pemOff int64 = 0
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		// Approximate offset: the header "-----BEGIN" will be somewhere in `rest`.
		idx := indexOfHeader(rest)
		if idx < 0 {
			break
		}
		start := pemOff + int64(idx)
		end := pemOff + int64(len(rest)-len(next))
		pemCount++
		child := &Field{
			Name:   fmt.Sprintf("PEM %s #%d", block.Type, pemCount),
			Type:   "pem",
			Offset: start,
			Length: end - start,
		}
		if block.Type == "CERTIFICATE" {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				populateCert(child, cert)
			} else {
				child.Note = "x509 parse failed: " + err.Error()
			}
		}
		root.Children = append(root.Children, child)
		pemOff += int64(len(rest) - len(next))
		rest = next
	}

	// DER pass — scan for SEQUENCE 0x30 0x82 xx yy (definite long-form, 2 length bytes).
	//
	// Each candidate runs a full x509.ParseCertificate over a blob up to 64 KiB.
	// Without guards a buffer dense with "30 82" byte pairs (random/firmware
	// data, or a hostile file) would attempt that parse at nearly every offset
	// — O(n) expensive parses, enough to freeze the UI. Two guards keep it
	// bounded: a cheap structural pre-filter (a DER certificate body begins
	// with the tbsCertificate SEQUENCE, 0x30) rejects almost all false
	// candidates without parsing, and a hard cap on parse attempts bounds even
	// adversarial input that defeats the pre-filter.
	const maxDERAttempts = 1 << 16
	derCount := 0
	derAttempts := 0
	for i := 0; i+4 < len(all); i++ {
		if all[i] != 0x30 || all[i+1] != 0x82 {
			continue
		}
		if all[i+4] != 0x30 {
			continue
		}
		totalLen := int(all[i+2])<<8 | int(all[i+3])
		if totalLen < 100 || i+4+totalLen > len(all) {
			continue
		}
		if derAttempts++; derAttempts > maxDERAttempts {
			break
		}
		blob := all[i : i+4+totalLen]
		cert, err := x509.ParseCertificate(blob)
		if err != nil {
			continue
		}
		derCount++
		child := &Field{
			Name:   fmt.Sprintf("DER cert #%d", derCount),
			Type:   "der",
			Offset: int64(i),
			Length: int64(4 + totalLen),
		}
		populateCert(child, cert)
		root.Children = append(root.Children, child)
		i += 4 + totalLen - 1
	}

	if len(root.Children) == 0 {
		return nil, errors.New("no certificates found")
	}
	root.Value = fmt.Sprintf("PEM=%d DER=%d", pemCount, derCount)
	return root, nil
}

func populateCert(f *Field, c *x509.Certificate) {
	f.Value = fmt.Sprintf("subject=%q issuer=%q serial=%x",
		c.Subject.String(), c.Issuer.String(), c.SerialNumber)
	add := func(name, value string) {
		f.Children = append(f.Children, &Field{Name: name, Type: "field", Offset: f.Offset, Length: 0, Value: value})
	}
	add("subject", c.Subject.String())
	add("issuer", c.Issuer.String())
	add("serial", fmt.Sprintf("%x", c.SerialNumber))
	add("valid_from", c.NotBefore.UTC().Format("2006-01-02 15:04:05 UTC"))
	add("valid_until", c.NotAfter.UTC().Format("2006-01-02 15:04:05 UTC"))
	add("pubkey_alg", c.PublicKeyAlgorithm.String())
	add("sig_alg", c.SignatureAlgorithm.String())
	if len(c.DNSNames) > 0 {
		add("dns_names", strings.Join(c.DNSNames, ", "))
	}
	if len(c.IPAddresses) > 0 {
		ips := make([]string, 0, len(c.IPAddresses))
		for _, ip := range c.IPAddresses {
			ips = append(ips, ip.String())
		}
		add("ip_addresses", strings.Join(ips, ", "))
	}
	if len(c.EmailAddresses) > 0 {
		add("emails", strings.Join(c.EmailAddresses, ", "))
	}
	if c.IsCA {
		add("ca", "true")
	}
	if c.KeyUsage != 0 {
		add("key_usage", fmt.Sprintf("0x%x", int(c.KeyUsage)))
	}
	if len(c.ExtKeyUsage) > 0 {
		parts := make([]string, 0, len(c.ExtKeyUsage))
		for _, u := range c.ExtKeyUsage {
			parts = append(parts, extKeyUsageName(u))
		}
		add("ext_key_usage", strings.Join(parts, ", "))
	}
	// SHA-256 fingerprint of DER
	add("sha256_fingerprint", sha256Hex(c.Raw))
}

func extKeyUsageName(u x509.ExtKeyUsage) string {
	switch u {
	case x509.ExtKeyUsageAny:
		return "any"
	case x509.ExtKeyUsageServerAuth:
		return "serverAuth"
	case x509.ExtKeyUsageClientAuth:
		return "clientAuth"
	case x509.ExtKeyUsageCodeSigning:
		return "codeSigning"
	case x509.ExtKeyUsageEmailProtection:
		return "emailProtection"
	case x509.ExtKeyUsageTimeStamping:
		return "timeStamping"
	case x509.ExtKeyUsageOCSPSigning:
		return "OCSPSigning"
	}
	return fmt.Sprintf("%d", int(u))
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h[:])
}

// indexOfHeader returns the byte offset of the next PEM BEGIN line.
func indexOfHeader(b []byte) int {
	const tag = "-----BEGIN"
	for i := 0; i+len(tag) <= len(b); i++ {
		if string(b[i:i+len(tag)]) == tag {
			return i
		}
	}
	return -1
}
