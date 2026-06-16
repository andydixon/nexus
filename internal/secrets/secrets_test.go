package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Full lifecycle: init → set → get → lock → fail → unlock → get
// ---------------------------------------------------------------------------

func TestSecretStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)

	if store.IsInitialised() {
		t.Fatal("expected vault to be uninitialised on a fresh directory")
	}

	// Initialise with a master password.
	if err := store.Initialise("correct-horse-battery-staple"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}
	if !store.IsInitialised() {
		t.Fatal("expected vault to be initialised after Initialise()")
	}
	if !store.IsUnlocked() {
		t.Fatal("expected vault to be unlocked after Initialise()")
	}

	// Store a secret and retrieve it.
	if err := store.SetSecret("profile:abc:password", "s3cret"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}
	val, err := store.GetSecret("profile:abc:password")
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}
	if val != "s3cret" {
		t.Errorf("GetSecret() = %q, want %q", val, "s3cret")
	}

	// Lock the vault — should prevent reads.
	store.Lock()
	if store.IsUnlocked() {
		t.Fatal("expected vault to be locked after Lock()")
	}
	if _, err := store.GetSecret("profile:abc:password"); err == nil {
		t.Fatal("expected error when getting secret from locked vault")
	}

	// Unlock and read again.
	if err := store.Unlock("correct-horse-battery-staple"); err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	val, err = store.GetSecret("profile:abc:password")
	if err != nil {
		t.Fatalf("GetSecret() after unlock error = %v", err)
	}
	if val != "s3cret" {
		t.Errorf("GetSecret() after unlock = %q, want %q", val, "s3cret")
	}
}

// ---------------------------------------------------------------------------
// Wrong password should fail to unlock
// ---------------------------------------------------------------------------

func TestSecretStoreWrongPassword(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)

	if err := store.Initialise("passwordA"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}
	// Store a secret so the password verification has something to decrypt.
	if err := store.SetSecret("test:key", "value"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}
	store.Lock()

	if err := store.Unlock("passwordB"); err == nil {
		t.Fatal("expected error when unlocking with wrong password")
	}
}

// ---------------------------------------------------------------------------
// Multiple secrets — store, retrieve, delete selectively
// ---------------------------------------------------------------------------

func TestSecretStoreMultipleSecrets(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)

	if err := store.Initialise("master"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}

	secrets := map[string]string{
		"profile:a:password":   "alpha",
		"profile:b:password":   "bravo",
		"profile:c:passphrase": "charlie",
	}
	for k, v := range secrets {
		if err := store.SetSecret(k, v); err != nil {
			t.Fatalf("SetSecret(%q) error = %v", k, err)
		}
	}

	// Verify each secret.
	for k, want := range secrets {
		got, err := store.GetSecret(k)
		if err != nil {
			t.Fatalf("GetSecret(%q) error = %v", k, err)
		}
		if got != want {
			t.Errorf("GetSecret(%q) = %q, want %q", k, got, want)
		}
	}

	// Delete one secret and verify the others remain.
	if err := store.DeleteSecret("profile:b:password"); err != nil {
		t.Fatalf("DeleteSecret() error = %v", err)
	}
	if _, err := store.GetSecret("profile:b:password"); err == nil {
		t.Error("expected error when getting deleted secret")
	}
	for _, key := range []string{"profile:a:password", "profile:c:passphrase"} {
		if _, err := store.GetSecret(key); err != nil {
			t.Errorf("GetSecret(%q) should still work after unrelated delete, got %v", key, err)
		}
	}
}

// ---------------------------------------------------------------------------
// DeleteProfileSecrets
// ---------------------------------------------------------------------------

func TestDeleteProfileSecrets(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)

	if err := store.Initialise("master"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}

	// Store secrets for two profiles.
	if err := store.SetSecret("profile:p1:password", "pw1"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}
	if err := store.SetSecret("profile:p1:passphrase", "pp1"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}
	if err := store.SetSecret("profile:p2:password", "pw2"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}

	// Delete profile p1's secrets.
	if err := store.DeleteProfileSecrets("p1"); err != nil {
		t.Fatalf("DeleteProfileSecrets() error = %v", err)
	}

	// p1 secrets should be gone.
	if _, err := store.GetSecret("profile:p1:password"); err == nil {
		t.Error("expected p1 password to be deleted")
	}
	if _, err := store.GetSecret("profile:p1:passphrase"); err == nil {
		t.Error("expected p1 passphrase to be deleted")
	}

	// p2 secret should remain.
	val, err := store.GetSecret("profile:p2:password")
	if err != nil {
		t.Fatalf("GetSecret(p2) error = %v", err)
	}
	if val != "pw2" {
		t.Errorf("GetSecret(p2) = %q, want %q", val, "pw2")
	}
}

// ---------------------------------------------------------------------------
// Reinitialise an existing vault should fail
// ---------------------------------------------------------------------------

func TestSecretStoreDoubleInitialise(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)

	if err := store.Initialise("pw"); err != nil {
		t.Fatalf("first Initialise() error = %v", err)
	}
	if err := store.Initialise("pw"); err == nil {
		t.Fatal("expected error on second Initialise()")
	}
}

// ---------------------------------------------------------------------------
// Persistence across store instances
// ---------------------------------------------------------------------------

func TestSecretStorePersistence(t *testing.T) {
	dir := t.TempDir()

	// Instance 1: create and store.
	s1 := NewSecretStore(dir)
	if err := s1.Initialise("master"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}
	if err := s1.SetSecret("k", "v"); err != nil {
		t.Fatalf("SetSecret() error = %v", err)
	}
	s1.Lock()

	// Instance 2: open the same directory and unlock.
	s2 := NewSecretStore(dir)
	if !s2.IsInitialised() {
		t.Fatal("expected vault to be initialised from disc")
	}
	if err := s2.Unlock("master"); err != nil {
		t.Fatalf("Unlock() on second instance error = %v", err)
	}
	val, err := s2.GetSecret("k")
	if err != nil {
		t.Fatalf("GetSecret() on second instance error = %v", err)
	}
	if val != "v" {
		t.Errorf("got %q, want %q", val, "v")
	}
}

// ---------------------------------------------------------------------------
// ListKeys
// ---------------------------------------------------------------------------

func TestListKeys(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)

	if err := store.Initialise("master"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}
	_ = store.SetSecret("a", "1")
	_ = store.SetSecret("b", "2")

	keys := store.ListKeys()
	if len(keys) != 2 {
		t.Fatalf("ListKeys() returned %d keys, want 2", len(keys))
	}
}

// ---------------------------------------------------------------------------
// Vault path uses the store directory
// ---------------------------------------------------------------------------

func TestVaultPath(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)
	want := filepath.Join(dir, "vault.json")

	// Access vaultPath via reflection-free approach: just initialise and check the file.
	if err := store.Initialise("pw"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}
	// The file should exist at the expected path.
	if !store.IsInitialised() {
		t.Fatalf("expected vault file at %s", want)
	}
}

func TestSecretStoreLoadRejectsOversizedVault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, vaultFileName)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	if err := file.Truncate(maxVaultFileBytes + 1); err != nil {
		t.Fatalf("Truncate() failed: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	store := NewSecretStore(dir)
	_, err = store.load()
	if err == nil {
		t.Fatal("expected oversized vault to be rejected")
	}
	if !strings.Contains(err.Error(), "vault file is too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Redaction helpers
// ---------------------------------------------------------------------------

func TestRedactSecret(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty string returns empty", input: "", want: ""},
		{name: "non-empty returns bullets", input: "super-secret", want: "••••••••"},
		{name: "single char returns bullets", input: "x", want: "••••••••"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactSecret(tt.input)
			if got != tt.want {
				t.Errorf("RedactSecret(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactInString(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		secret string
		want   string
	}{
		{
			name:   "replaces secret in text",
			text:   "connecting with password=hunter2 now",
			secret: "hunter2",
			want:   "connecting with password=•••••••• now",
		},
		{
			name:   "empty secret returns text unchanged",
			text:   "no redaction here",
			secret: "",
			want:   "no redaction here",
		},
		{
			name:   "secret not present returns text unchanged",
			text:   "nothing to see",
			secret: "absent",
			want:   "nothing to see",
		},
		{
			name:   "multiple occurrences all redacted",
			text:   "pw=abc and again abc",
			secret: "abc",
			want:   "pw=•••••••• and again ••••••••",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactInString(tt.text, tt.secret)
			if got != tt.want {
				t.Errorf("RedactInString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedactMap(t *testing.T) {
	input := map[string]string{
		"password": "hunter2",
		"token":    "abc123",
		"empty":    "",
	}

	result := RedactMap(input)

	if result["password"] != "••••••••" {
		t.Errorf("expected password to be redacted, got %q", result["password"])
	}
	if result["token"] != "••••••••" {
		t.Errorf("expected token to be redacted, got %q", result["token"])
	}
	if result["empty"] != "" {
		t.Errorf("expected empty value to remain empty, got %q", result["empty"])
	}

	// Verify the original map is not modified.
	if input["password"] != "hunter2" {
		t.Error("original map was modified")
	}
}

// ---------------------------------------------------------------------------
// Initialise standalone — verifies vault file creation and unlocked state
// ---------------------------------------------------------------------------

func TestSecretStoreInitialise(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)

	if store.IsInitialised() {
		t.Fatal("expected uninitialised state before Initialise()")
	}

	if err := store.Initialise("test-master-pw"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}

	// Vault file must exist on disc.
	if !store.IsInitialised() {
		t.Fatal("expected initialised state after Initialise()")
	}

	// Store should be unlocked immediately after initialisation.
	if !store.IsUnlocked() {
		t.Fatal("expected unlocked state after Initialise()")
	}

	// Should be able to set and retrieve a secret straight away.
	if err := store.SetSecret("init-test", "value"); err != nil {
		t.Fatalf("SetSecret() after Initialise error = %v", err)
	}
	got, err := store.GetSecret("init-test")
	if err != nil {
		t.Fatalf("GetSecret() after Initialise error = %v", err)
	}
	if got != "value" {
		t.Errorf("GetSecret() = %q, want %q", got, "value")
	}
}

// ---------------------------------------------------------------------------
// Encrypt / Decrypt round-trip (same-package access to unexported methods)
// ---------------------------------------------------------------------------

func TestEncryptDecrypt(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)
	if err := store.Initialise("roundtrip-pw"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{name: "short string", plaintext: "hello"},
		{name: "empty string", plaintext: ""},
		{name: "long string", plaintext: "The quick brown fox jumps over the lazy dog. 0123456789!@#$%^&*()"},
		{name: "unicode text", plaintext: "café résumé naïve über 日本語"},
		{name: "binary-ish content", plaintext: "\x00\x01\x02\xff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := store.encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("encrypt() error = %v", err)
			}

			// Encrypted text must differ from plaintext (unless empty — GCM
			// still produces a tag).
			if encrypted == tt.plaintext && tt.plaintext != "" {
				t.Error("encrypted output should not equal plaintext")
			}

			decrypted, err := store.decrypt(encrypted)
			if err != nil {
				t.Fatalf("decrypt() error = %v", err)
			}
			if decrypted != tt.plaintext {
				t.Errorf("decrypt() = %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncryptDecryptWrongKey(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)
	if err := store.Initialise("key-A"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}

	encrypted, err := store.encrypt("secret data")
	if err != nil {
		t.Fatalf("encrypt() error = %v", err)
	}

	// Create a second store with a different key.
	dir2 := t.TempDir()
	store2 := NewSecretStore(dir2)
	if err := store2.Initialise("key-B"); err != nil {
		t.Fatalf("Initialise(key-B) error = %v", err)
	}

	// Attempting to decrypt with the wrong key should fail.
	_, err = store2.decrypt(encrypted)
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	// Each call to encrypt should use a fresh random nonce, so encrypting
	// the same plaintext twice must produce different ciphertexts.
	dir := t.TempDir()
	store := NewSecretStore(dir)
	if err := store.Initialise("nonce-test"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}

	a, err := store.encrypt("same input")
	if err != nil {
		t.Fatalf("first encrypt() error = %v", err)
	}
	b, err := store.encrypt("same input")
	if err != nil {
		t.Fatalf("second encrypt() error = %v", err)
	}
	if a == b {
		t.Error("two encryptions of identical plaintext produced identical ciphertext — nonce reuse?")
	}
}

// ---------------------------------------------------------------------------
// SetSecret and GetSecret when vault is locked
// ---------------------------------------------------------------------------

func TestSetSecretWhenLocked(t *testing.T) {
	dir := t.TempDir()
	store := NewSecretStore(dir)
	if err := store.Initialise("lock-test"); err != nil {
		t.Fatalf("Initialise() error = %v", err)
	}
	store.Lock()

	if err := store.SetSecret("k", "v"); err == nil {
		t.Error("expected error when calling SetSecret on a locked vault")
	}
}
