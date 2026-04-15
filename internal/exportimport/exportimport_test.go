package exportimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/copilot-ssh-client/app/internal/models"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-export.nexus")

	profiles := []models.Profile{
		{
			ID:          "test-1",
			DisplayName: "Test Server",
			Protocol:    models.ProtocolSSH,
			Host:        "example.com",
			Port:        22,
			Username:    "admin",
		},
		{
			ID:          "test-2",
			DisplayName: "Dev Box",
			Protocol:    models.ProtocolSSH,
			Host:        "dev.example.com",
			Port:        2222,
			Username:    "developer",
		},
	}

	password := "strong-test-password-2024!"

	// Encrypt and export.
	if err := EncryptAndExport(profiles, password, path); err != nil {
		t.Fatalf("EncryptAndExport failed: %v", err)
	}

	// Verify file exists and has content.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("exported file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("exported file is empty")
	}

	// Decrypt and import.
	imported, err := DecryptAndImport(path, password)
	if err != nil {
		t.Fatalf("DecryptAndImport failed: %v", err)
	}

	if len(imported) != len(profiles) {
		t.Fatalf("expected %d profiles, got %d", len(profiles), len(imported))
	}

	for i, p := range imported {
		if p.ID != profiles[i].ID {
			t.Errorf("profile %d: expected ID %q, got %q", i, profiles[i].ID, p.ID)
		}
		if p.DisplayName != profiles[i].DisplayName {
			t.Errorf("profile %d: expected DisplayName %q, got %q", i, profiles[i].DisplayName, p.DisplayName)
		}
		if p.Host != profiles[i].Host {
			t.Errorf("profile %d: expected Host %q, got %q", i, profiles[i].Host, p.Host)
		}
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-export.nexus")

	profiles := []models.Profile{
		{
			ID:          "test-1",
			DisplayName: "Test",
			Protocol:    models.ProtocolSSH,
			Host:        "example.com",
			Port:        22,
		},
	}

	if err := EncryptAndExport(profiles, "correct-password", path); err != nil {
		t.Fatalf("EncryptAndExport failed: %v", err)
	}

	_, err := DecryptAndImport(path, "wrong-password")
	if err == nil {
		t.Fatal("expected error with wrong password, got nil")
	}
}

func TestEncryptEmptyProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.nexus")

	err := EncryptAndExport(nil, "password", path)
	if err == nil {
		t.Fatal("expected error for empty profiles, got nil")
	}
}

func TestEncryptEmptyPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.nexus")

	profiles := []models.Profile{{ID: "1"}}
	err := EncryptAndExport(profiles, "", path)
	if err == nil {
		t.Fatal("expected error for empty password, got nil")
	}
}

func TestDecryptCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.nexus")

	// Write garbage data.
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := DecryptAndImport(path, "password")
	if err == nil {
		t.Fatal("expected error for corrupt file, got nil")
	}
}
