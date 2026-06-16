package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	vaultFileName = "vault.json"
	vaultVersion  = 1

	// Argon2id key derivation parameters.
	argonTime    = 3
	argonMemory  = 128 * 1024 // 128 MiB — deliberately strong for a desktop app
	argonThreads = 4
	argonKeyLen  = 32

	saltLen = 32

	maxVaultFileBytes = 10 * 1024 * 1024
)

// SecretStore manages encrypted credential storage.
// It uses AES-256-GCM with Argon2id key derivation from a user-controlled
// master password. No hard-coded secrets are used.
//
// Threat model:
//   - Protects secrets at rest on the local filesystem
//   - Requires the master password to decrypt
//   - Does NOT protect against a compromised running process
//   - Does NOT protect against memory forensics on a compromised machine
//   - The master password is held in memory only for the duration of the session
type SecretStore struct {
	mu        sync.RWMutex
	storePath string
	key       []byte // derived from master password, held in memory
	vault     *secretVault
	unlocked  bool
}

type secretVault struct {
	Version int               `json:"version"`
	Salt    string            `json:"salt"`    // base64-encoded
	Secrets map[string]string `json:"secrets"` // key -> base64(nonce + ciphertext)
}

// NewSecretStore creates a new SecretStore that persists its vault file in the
// given directory path. The vault is not unlocked until Unlock is called.
func NewSecretStore(storePath string) *SecretStore {
	return &SecretStore{
		storePath: storePath,
	}
}

// IsInitialised returns true if a vault file already exists on disc.
func (s *SecretStore) IsInitialised() bool {
	_, err := os.Stat(s.vaultPath())
	return err == nil
}

// Initialise creates a new vault with the given master password. A random
// 32-byte salt is generated and the key is derived using Argon2id. Returns an
// error if a vault already exists.
func (s *SecretStore) Initialise(masterPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.IsInitialised() {
		return errors.New("secrets: vault already initialised")
	}

	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("secrets: failed to generate salt: %w", err)
	}

	s.vault = &secretVault{
		Version: vaultVersion,
		Salt:    base64.StdEncoding.EncodeToString(salt),
		Secrets: make(map[string]string),
	}
	s.key = deriveKey(masterPassword, salt)
	s.unlocked = true

	if err := os.MkdirAll(s.storePath, 0700); err != nil {
		return fmt.Errorf("secrets: failed to create store directory: %w", err)
	}

	if err := s.save(); err != nil {
		return fmt.Errorf("secrets: failed to save initial vault: %w", err)
	}

	return nil
}

// Unlock decrypts the vault using the provided master password. The derived
// key is verified by attempting to decrypt the first stored secret; if no
// secrets exist the password is accepted optimistically.
func (s *SecretStore) Unlock(masterPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	vault, err := s.load()
	if err != nil {
		return fmt.Errorf("secrets: failed to load vault: %w", err)
	}

	salt, err := base64.StdEncoding.DecodeString(vault.Salt)
	if err != nil {
		return fmt.Errorf("secrets: corrupt vault salt: %w", err)
	}

	s.vault = vault
	s.key = deriveKey(masterPassword, salt)
	s.unlocked = true

	// Attempt to decrypt any existing secret to verify the password.
	for _, encVal := range vault.Secrets {
		if _, err := s.decrypt(encVal); err != nil {
			s.lockInternal()
			return fmt.Errorf("secrets: incorrect master password or corrupt vault")
		}
		break
	}

	return nil
}

// Lock zeroes the derived key material and locks the vault. After calling Lock
// no secret operations are possible until Unlock is called again.
func (s *SecretStore) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lockInternal()
}

// lockInternal performs the actual lock without acquiring the mutex. The caller
// must already hold s.mu.
func (s *SecretStore) lockInternal() {
	for i := range s.key {
		s.key[i] = 0
	}
	s.key = nil
	s.vault = nil
	s.unlocked = false
}

// IsUnlocked returns whether the vault is currently unlocked and ready for use.
func (s *SecretStore) IsUnlocked() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unlocked
}

// SetSecret encrypts and stores a secret under the given key. The key format
// should follow "profile:{id}:password" or "profile:{id}:passphrase".
func (s *SecretStore) SetSecret(key string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.unlocked {
		return errors.New("secrets: vault is locked")
	}

	encrypted, err := s.encrypt(value)
	if err != nil {
		return fmt.Errorf("secrets: encryption failed: %w", err)
	}

	s.vault.Secrets[key] = encrypted
	return s.save()
}

// GetSecret retrieves and decrypts a secret by key. Returns an error if the
// key does not exist or decryption fails.
func (s *SecretStore) GetSecret(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.unlocked {
		return "", errors.New("secrets: vault is locked")
	}

	encVal, ok := s.vault.Secrets[key]
	if !ok {
		return "", fmt.Errorf("secrets: key %q not found", key)
	}

	return s.decrypt(encVal)
}

// DeleteSecret removes a secret by key. Returns an error if the vault is
// locked or the save fails.
func (s *SecretStore) DeleteSecret(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.unlocked {
		return errors.New("secrets: vault is locked")
	}

	delete(s.vault.Secrets, key)
	return s.save()
}

// DeleteProfileSecrets removes all secrets whose key begins with
// "profile:{profileID}:".
func (s *SecretStore) DeleteProfileSecrets(profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.unlocked {
		return errors.New("secrets: vault is locked")
	}

	prefix := "profile:" + profileID + ":"
	for k := range s.vault.Secrets {
		if strings.HasPrefix(k, prefix) {
			delete(s.vault.Secrets, k)
		}
	}

	return s.save()
}

// ListKeys returns all secret keys stored in the vault. Secret values are
// never returned by this method.
func (s *SecretStore) ListKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.unlocked || s.vault == nil {
		return nil
	}

	keys := make([]string, 0, len(s.vault.Secrets))
	for k := range s.vault.Secrets {
		keys = append(keys, k)
	}
	return keys
}

// --- internal helpers --------------------------------------------------------

// vaultPath returns the full filesystem path to the vault JSON file.
func (s *SecretStore) vaultPath() string {
	return filepath.Join(s.storePath, vaultFileName)
}

// deriveKey uses Argon2id to derive a 256-bit key from the master password and
// salt. The parameters are intentionally hard-coded to ensure consistency
// across vault open/close cycles.
func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
}

// encrypt encrypts plaintext using AES-256-GCM. A random nonce is generated
// for each call and prepended to the ciphertext. The result is returned as a
// base64-encoded string.
func (s *SecretStore) encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decrypt decodes a base64 string and decrypts it using AES-256-GCM. The
// nonce is expected to be prepended to the ciphertext.
func (s *SecretStore) decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode secret: %w", err)
	}

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %w", err)
	}

	return string(plaintext), nil
}

// save writes the vault to disc as a JSON file with restricted permissions.
func (s *SecretStore) save() error {
	data, err := json.MarshalIndent(s.vault, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal vault: %w", err)
	}

	return os.WriteFile(s.vaultPath(), data, 0600)
}

// load reads and unmarshals the vault from disc.
func (s *SecretStore) load() (*secretVault, error) {
	info, err := os.Stat(s.vaultPath())
	if err != nil {
		return nil, fmt.Errorf("failed to stat vault file: %w", err)
	}
	if info.IsDir() {
		return nil, errors.New("vault path is a directory")
	}
	if info.Size() > maxVaultFileBytes {
		return nil, fmt.Errorf("vault file is too large: %d bytes exceeds %d bytes", info.Size(), maxVaultFileBytes)
	}
	data, err := os.ReadFile(s.vaultPath())
	if err != nil {
		return nil, fmt.Errorf("failed to read vault file: %w", err)
	}

	var vault secretVault
	if err := json.Unmarshal(data, &vault); err != nil {
		return nil, fmt.Errorf("failed to parse vault file: %w", err)
	}

	return &vault, nil
}
