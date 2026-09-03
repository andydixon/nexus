package sshclient

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"time"

	"github.com/andydixon/link/internal/models"
	"golang.org/x/crypto/ssh"
)

// HostKeyStore is the interface for known-host persistence.
// Implementations handle the underlying storage mechanism (e.g. SQLite, file).
type HostKeyStore interface {
	GetKnownHost(host string, port int) (*models.KnownHost, error)
	SaveKnownHost(kh *models.KnownHost) error
}

// HostKeyVerifier handles SSH host key verification against the known-hosts
// store. It supports four policies:
//   - strict: reject unknown or changed keys
//   - ask: prompt the user for unknown keys (via callback)
//   - accept-new: automatically accept new keys, reject changed keys
//   - insecure: accept all keys (for testing only — clearly warned)
type HostKeyVerifier struct {
	store    HostKeyStore
	policy   models.HostKeyPolicy
	onPrompt func(host string, keyType string, fingerprint string) (bool, error)
}

// NewHostKeyVerifier creates a verifier with the given store and policy.
func NewHostKeyVerifier(store HostKeyStore, policy models.HostKeyPolicy) *HostKeyVerifier {
	return &HostKeyVerifier{
		store:  store,
		policy: policy,
	}
}

// SetPromptHandler sets the callback used in "ask" mode to prompt the user
// for acceptance of an unknown host key.
func (v *HostKeyVerifier) SetPromptHandler(fn func(string, string, string) (bool, error)) {
	v.onPrompt = fn
}

// HostKeyCallback returns an ssh.HostKeyCallback suitable for use with
// ssh.ClientConfig. The behaviour depends on the configured policy.
func (v *HostKeyVerifier) HostKeyCallback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		host, port := extractHostPort(hostname, remote)
		keyType := key.Type()
		fingerprint := computeFingerprint(key)

		stored, err := v.store.GetKnownHost(host, port)
		if err != nil {
			return fmt.Errorf("failed to query known hosts: %w", err)
		}

		switch v.policy {
		case models.HostKeyInsecure:
			return v.saveIfNew(stored, host, port, keyType, fingerprint, key)

		case models.HostKeyAcceptNew:
			if stored == nil {
				return v.saveHost(host, port, keyType, fingerprint, key)
			}
			return v.verifyStored(stored, fingerprint, host)

		case models.HostKeyAsk:
			if stored == nil {
				return v.promptAndSave(host, port, keyType, fingerprint, key)
			}
			return v.verifyStored(stored, fingerprint, host)

		case models.HostKeyStrict:
			if stored == nil {
				return fmt.Errorf("host key for %s:%d is not known (policy: strict)", host, port)
			}
			return v.verifyStored(stored, fingerprint, host)

		default:
			return fmt.Errorf("unknown host key policy: %s", v.policy)
		}
	}
}

// verifyStored checks that the presented fingerprint matches the stored one.
func (v *HostKeyVerifier) verifyStored(stored *models.KnownHost, fingerprint, host string) error {
	if stored.Fingerprint != fingerprint {
		return fmt.Errorf(
			"host key for %s has changed! stored fingerprint: %s, received: %s — possible MITM attack",
			host, stored.Fingerprint, fingerprint,
		)
	}
	stored.LastSeen = time.Now()
	_ = v.store.SaveKnownHost(stored)
	return nil
}

// promptAndSave asks the user whether to accept an unknown host key.
func (v *HostKeyVerifier) promptAndSave(host string, port int, keyType, fingerprint string, key ssh.PublicKey) error {
	if v.onPrompt == nil {
		return fmt.Errorf("host key for %s:%d is unknown and no prompt handler is set (policy: ask)", host, port)
	}

	accepted, err := v.onPrompt(host, keyType, fingerprint)
	if err != nil {
		return fmt.Errorf("host key prompt failed: %w", err)
	}
	if !accepted {
		return fmt.Errorf("host key for %s:%d rejected by user", host, port)
	}

	return v.saveHost(host, port, keyType, fingerprint, key)
}

// saveIfNew stores the host key only when no prior record exists.
func (v *HostKeyVerifier) saveIfNew(stored *models.KnownHost, host string, port int, keyType, fingerprint string, key ssh.PublicKey) error {
	if stored == nil {
		return v.saveHost(host, port, keyType, fingerprint, key)
	}
	stored.LastSeen = time.Now()
	_ = v.store.SaveKnownHost(stored)
	return nil
}

// saveHost persists a new known-host record.
func (v *HostKeyVerifier) saveHost(host string, port int, keyType, fingerprint string, key ssh.PublicKey) error {
	now := time.Now()
	kh := &models.KnownHost{
		Host:        host,
		Port:        port,
		KeyType:     keyType,
		Fingerprint: fingerprint,
		PublicKey:   base64.StdEncoding.EncodeToString(key.Marshal()),
		FirstSeen:   now,
		LastSeen:    now,
		Trusted:     true,
	}
	return v.store.SaveKnownHost(kh)
}

// computeFingerprint returns the SHA-256 fingerprint of a public key in
// the standard "SHA256:<base64>" format.
func computeFingerprint(key ssh.PublicKey) string {
	hash := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:])
}

// extractHostPort splits the hostname and port from the SSH callback arguments.
func extractHostPort(hostname string, remote net.Addr) (string, int) {
	host, portStr, err := net.SplitHostPort(hostname)
	if err != nil {
		host, portStr, _ = net.SplitHostPort(remote.String())
	}
	port := 22
	if portStr != "" {
		fmt.Sscanf(portStr, "%d", &port)
	}
	return host, port
}
