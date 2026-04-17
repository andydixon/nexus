// Package exportimport provides encrypted profile export and import.
// Profiles are serialised to JSON, then encrypted with AES-256-GCM using
// an Argon2id-derived key from a user-supplied password. No hard-coded
// secrets are used. The file format includes a version byte, salt, nonce,
// and ciphertext — everything needed for self-contained decryption.
package exportimport

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/andydixon/nexus/internal/models"
)

const (
	// File format version. Increment if the envelope format changes.
	formatVersion byte = 1

	// Argon2id parameters for export encryption (same strength as the vault).
	exportArgonTime    = 3
	exportArgonMemory  = 128 * 1024 // 128 MiB
	exportArgonThreads = 4
	exportArgonKeyLen  = 32 // AES-256

	saltLength  = 32
	nonceLength = 12 // AES-GCM standard nonce size
)

// ExportBundle is the cleartext payload that gets encrypted.
type ExportBundle struct {
	Version    int              `json:"version"`
	ExportedAt string           `json:"exportedAt"` // ISO 8601
	Profiles   []models.Profile `json:"profiles"`
	Count      int              `json:"count"`
}

// EncryptAndExport serialises the given profiles, encrypts the JSON with
// AES-256-GCM (Argon2id-derived key), and writes the result to the file.
//
// File layout: [1 byte version][32 bytes salt][12 bytes nonce][ciphertext]
func EncryptAndExport(profiles []models.Profile, password, filePath string) error {
	if password == "" {
		return errors.New("exportimport: password cannot be empty")
	}
	if len(profiles) == 0 {
		return errors.New("exportimport: no profiles to export")
	}

	bundle := ExportBundle{
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Profiles:   profiles,
		Count:      len(profiles),
	}

	plaintext, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("exportimport: failed to serialise profiles: %w", err)
	}

	// Generate random salt and nonce.
	salt := make([]byte, saltLength)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("exportimport: failed to generate salt: %w", err)
	}

	nonce := make([]byte, nonceLength)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("exportimport: failed to generate nonce: %w", err)
	}

	// Derive encryption key from password.
	key := argon2.IDKey(
		[]byte(password),
		salt,
		exportArgonTime,
		exportArgonMemory,
		exportArgonThreads,
		exportArgonKeyLen,
	)

	// Encrypt with AES-256-GCM.
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("exportimport: cipher init failed: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("exportimport: GCM init failed: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Write the file: version + salt + nonce + ciphertext.
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("exportimport: failed to create file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write([]byte{formatVersion}); err != nil {
		return fmt.Errorf("exportimport: write version failed: %w", err)
	}
	if _, err := f.Write(salt); err != nil {
		return fmt.Errorf("exportimport: write salt failed: %w", err)
	}
	if _, err := f.Write(nonce); err != nil {
		return fmt.Errorf("exportimport: write nonce failed: %w", err)
	}
	if _, err := f.Write(ciphertext); err != nil {
		return fmt.Errorf("exportimport: write ciphertext failed: %w", err)
	}

	return f.Sync()
}

// DecryptAndImport reads an encrypted export file, decrypts it using the
// given password, and returns the contained profiles.
func DecryptAndImport(filePath, password string) ([]models.Profile, error) {
	if password == "" {
		return nil, errors.New("exportimport: password cannot be empty")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("exportimport: failed to read file: %w", err)
	}

	// Minimum size: version(1) + salt(32) + nonce(12) + at least 1 byte ciphertext + GCM tag(16).
	minSize := 1 + saltLength + nonceLength + 1 + 16
	if len(data) < minSize {
		return nil, errors.New("exportimport: file is too small or corrupt")
	}

	// Parse envelope.
	version := data[0]
	if version != formatVersion {
		return nil, fmt.Errorf("exportimport: unsupported format version %d", version)
	}

	salt := data[1 : 1+saltLength]
	nonce := data[1+saltLength : 1+saltLength+nonceLength]
	ciphertext := data[1+saltLength+nonceLength:]

	// Derive decryption key.
	key := argon2.IDKey(
		[]byte(password),
		salt,
		exportArgonTime,
		exportArgonMemory,
		exportArgonThreads,
		exportArgonKeyLen,
	)

	// Decrypt with AES-256-GCM.
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("exportimport: cipher init failed: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("exportimport: GCM init failed: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("exportimport: decryption failed — wrong password or corrupt file")
	}

	// Parse the JSON bundle.
	var bundle ExportBundle
	if err := json.Unmarshal(plaintext, &bundle); err != nil {
		return nil, fmt.Errorf("exportimport: invalid bundle JSON: %w", err)
	}

	return bundle.Profiles, nil
}
