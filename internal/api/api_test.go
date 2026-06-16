package api

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andydixon/nexus/internal/audit"
	"github.com/andydixon/nexus/internal/leviathan/document"
	"github.com/andydixon/nexus/internal/models"
	"github.com/andydixon/nexus/internal/storage"
)

func TestSSHKeyPairPathsAcceptsSafeName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	privPath, pubPath, err := sshKeyPairPaths("deploy_key-01")
	if err != nil {
		t.Fatalf("sshKeyPairPaths() error = %v", err)
	}

	wantPriv := filepath.Join(home, ".ssh", "deploy_key-01")
	if privPath != wantPriv {
		t.Fatalf("private path = %q, want %q", privPath, wantPriv)
	}
	if pubPath != wantPriv+".pub" {
		t.Fatalf("public path = %q, want %q", pubPath, wantPriv+".pub")
	}
}

func TestSSHKeyPairPathsRejectsTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, _, err := sshKeyPairPaths("../outside")
	if err == nil {
		t.Fatal("expected traversal key name to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid characters") {
		t.Fatalf("error = %q, want invalid characters", err.Error())
	}
}

func TestSSHKeyPairPathsRejectsNestedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, _, err := sshKeyPairPaths("nested/key")
	if err == nil {
		t.Fatal("expected nested key path to be rejected")
	}
}

func TestRedactProfileSecretsClearsInlineSSHSecrets(t *testing.T) {
	profile := models.NewProfile(models.ProtocolSSH)
	profile.SSH.ProxyPassword = "proxy-secret"
	profile.SSH.TOTPSecret = "totp-secret"

	got := redactProfileSecrets([]*models.Profile{&profile})
	if got[0].SSH.ProxyPassword != "" {
		t.Fatalf("ProxyPassword = %q, want empty", got[0].SSH.ProxyPassword)
	}
	if got[0].SSH.TOTPSecret != "" {
		t.Fatalf("TOTPSecret = %q, want empty", got[0].SSH.TOTPSecret)
	}
	if profile.SSH.ProxyPassword == "" || profile.SSH.TOTPSecret == "" {
		t.Fatal("redaction mutated the original profile")
	}
}

func TestCreateProfileRejectsUnsafeGitHubBaseURL(t *testing.T) {
	svc := newTestService(t)
	profile := models.NewProfile(models.ProtocolGitHub)
	profile.DisplayName = "Unsafe GitHub"
	profile.GitHub.BaseURL = "http://evil.example/api/v3"

	if _, err := svc.CreateProfile(profile); err == nil {
		t.Fatal("expected unsafe GitHub base URL to be rejected")
	}
}

func TestImportProfilesRejectsNullEntry(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.ImportProfiles(`[null]`, "skip")
	if err == nil {
		t.Fatal("expected null import entry to be rejected")
	}
	if !strings.Contains(err.Error(), "entry 0 is null") {
		t.Fatalf("error = %q, want null entry detail", err.Error())
	}
}

func TestImportProfilesRejectsOversizedJSON(t *testing.T) {
	svc := newTestService(t)
	oversized := strings.Repeat(" ", maxProfileImportJSONBytes+1)
	_, err := svc.ImportProfiles(oversized, "skip")
	if err == nil {
		t.Fatal("expected oversized import JSON to be rejected")
	}
}

func TestSFTPUploadDataRejectsOversizedPayloadBeforeSessionLookup(t *testing.T) {
	svc := newTestService(t)
	payload := base64.StdEncoding.EncodeToString(make([]byte, maxInlineSFTPUploadBytes+1))

	err := svc.SFTPUploadData("missing-session", "/", "large.bin", payload)
	if err == nil {
		t.Fatal("expected oversized upload payload to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %q, want too large", err.Error())
	}
}

func TestSFTPUploadChunkRejectsOversizedChunkBeforeUploadLookup(t *testing.T) {
	svc := newTestService(t)
	payload := base64.StdEncoding.EncodeToString(make([]byte, maxSFTPUploadChunkBytes+1))

	err := svc.SFTPUploadChunk("missing-upload", payload)
	if err == nil {
		t.Fatal("expected oversized upload chunk to be rejected")
	}
	if !strings.Contains(err.Error(), "chunk is too large") {
		t.Fatalf("error = %q, want chunk is too large", err.Error())
	}
}

func TestSafeSFTPUploadNamePreservesHTMLCharacters(t *testing.T) {
	got, err := safeSFTPUploadName(`../report <final>.txt`)
	if err != nil {
		t.Fatalf("safeSFTPUploadName() error = %v", err)
	}
	if got != "report <final>.txt" {
		t.Fatalf("safe name = %q, want HTML characters preserved in base name", got)
	}
}

func TestImportProfilesNormalizesSparseRAWProfile(t *testing.T) {
	svc := newTestService(t)
	data := `[
		{
			"displayName": "Raw Device",
			"protocol": "raw",
			"host": "device.local",
			"raw": {}
		}
	]`

	count, err := svc.ImportProfiles(data, "skip")
	if err != nil {
		t.Fatalf("ImportProfiles() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("imported = %d, want 1", count)
	}
	profiles, err := svc.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(profiles))
	}
	if profiles[0].Port != models.DefaultRAWPort {
		t.Fatalf("port = %d, want %d", profiles[0].Port, models.DefaultRAWPort)
	}
	if profiles[0].RAW.Encoding != "utf-8" {
		t.Fatalf("raw encoding = %q, want utf-8", profiles[0].RAW.Encoding)
	}
}

func TestLeviathanDocumentBytesRejectsOversizedDocument(t *testing.T) {
	doc := document.New("large.bin", []byte("abcd"))
	_, err := leviathanDocumentBytes(doc, 3, "test")
	if err == nil {
		t.Fatal("expected oversized document to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteLeviathanDocumentFileWritesPatchedBytes(t *testing.T) {
	doc := document.New("patched.bin", []byte("abcd"))
	if _, err := doc.Set(1, 'Z'); err != nil {
		t.Fatalf("Set() failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "out.bin")
	if err := writeLeviathanDocumentFile(path, doc); err != nil {
		t.Fatalf("writeLeviathanDocumentFile() failed: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}
	if string(got) != "aZcd" {
		t.Fatalf("written bytes = %q, want %q", string(got), "aZcd")
	}
}

func TestReadLeviathanSmallFileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.img")
	if err := os.WriteFile(path, []byte("abcd"), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	_, err := readLeviathanSmallFile(path, 3, "image")
	if err == nil {
		t.Fatal("expected oversized file to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadSSHPublicKeyFileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.pub")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxSSHPublicKeyBytes+1)), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	_, err := readSSHPublicKeyFile(path)
	if err == nil {
		t.Fatal("expected oversized public key to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateProfileNormalizesLegacyVNCSecretMode(t *testing.T) {
	svc := newTestService(t)
	profile := models.NewProfile(models.ProtocolVNC)
	profile.DisplayName = "VNC"
	profile.Host = "vnc.local"
	profile.VNC.SecretMode = "prompt"
	profile.VNC.SSHSecretMode = "prompt"

	got, err := svc.CreateProfile(profile)
	if err != nil {
		t.Fatalf("CreateProfile() error = %v", err)
	}
	if got.VNC.SecretMode != models.SecretModeAskEveryTime {
		t.Fatalf("secret mode = %q, want %q", got.VNC.SecretMode, models.SecretModeAskEveryTime)
	}
	if got.VNC.SSHSecretMode != models.SecretModeAskEveryTime {
		t.Fatalf("ssh secret mode = %q, want %q", got.VNC.SSHSecretMode, models.SecretModeAskEveryTime)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return NewService(store, nil, nil, nil, audit.NewLogger(store), nil)
}
