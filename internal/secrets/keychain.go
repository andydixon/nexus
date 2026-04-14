package secrets

import (
	"fmt"

	"github.com/zalando/go-keyring"
)

// KeychainStore attempts to use the OS keychain for secret storage.
// On macOS this uses the Keychain, on Linux it uses the Secret Service API,
// and on Windows it uses the Credential Manager.
//
// This is the preferred storage method when available. Falls back to
// the encrypted file-based SecretStore when the keychain is not accessible.
type KeychainStore struct {
	serviceName string
}

// NewKeychainStore creates a KeychainStore that stores credentials under the
// given service name in the operating system's native keychain.
func NewKeychainStore(serviceName string) *KeychainStore {
	return &KeychainStore{serviceName: serviceName}
}

// Available checks whether the OS keychain is accessible by performing a
// harmless write-then-delete probe. Returns true if the keychain responded
// without error.
func (k *KeychainStore) Available() bool {
	// Attempt a round-trip to verify the keychain is usable.
	probeKey := "__nexus_keychain_probe__"
	probeVal := "probe"

	if err := keyring.Set(k.serviceName, probeKey, probeVal); err != nil {
		return false
	}
	_ = keyring.Delete(k.serviceName, probeKey)
	return true
}

// SetSecret stores a secret in the OS keychain under the configured service
// name with the given key.
func (k *KeychainStore) SetSecret(key, value string) error {
	if err := keyring.Set(k.serviceName, key, value); err != nil {
		return fmt.Errorf("keychain: failed to store secret %q: %w", key, err)
	}
	return nil
}

// GetSecret retrieves a secret from the OS keychain. Returns an error if the
// key does not exist or the keychain is inaccessible.
func (k *KeychainStore) GetSecret(key string) (string, error) {
	val, err := keyring.Get(k.serviceName, key)
	if err != nil {
		return "", fmt.Errorf("keychain: failed to retrieve secret %q: %w", key, err)
	}
	return val, nil
}

// DeleteSecret removes a secret from the OS keychain. Returns an error if the
// key does not exist or the keychain is inaccessible.
func (k *KeychainStore) DeleteSecret(key string) error {
	if err := keyring.Delete(k.serviceName, key); err != nil {
		return fmt.Errorf("keychain: failed to delete secret %q: %w", key, err)
	}
	return nil
}
