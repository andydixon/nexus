package api

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/andydixon/nexus/internal/models"
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
