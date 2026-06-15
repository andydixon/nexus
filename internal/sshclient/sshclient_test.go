package sshclient

import (
	"net"
	"testing"
	"time"

	"github.com/andydixon/nexus/internal/models"
	"golang.org/x/crypto/ssh"
)

func TestConnectRequiresHostKeyCallback(t *testing.T) {
	profile := models.NewProfile(models.ProtocolSSH)
	client := NewClient(&profile)

	err := client.Connect(nil, nil)
	if err == nil {
		t.Fatal("expected Connect to reject missing host key callback")
	}
}

func TestJumpHostClientConfigUsesFinalHostKeyCallback(t *testing.T) {
	called := false
	hostKeyCallback := func(hostname string, _ net.Addr, _ ssh.PublicKey) error {
		called = true
		if hostname != "jump.example:2222" {
			t.Fatalf("hostname = %q, want jump.example:2222", hostname)
		}
		return nil
	}

	finalConfig := &ssh.ClientConfig{
		HostKeyCallback: hostKeyCallback,
		Timeout:         12 * time.Second,
	}
	jump := models.JumpHost{
		Host:     "jump.example",
		Port:     2222,
		Username: "alice",
	}

	got := jumpHostClientConfig(jump, nil, finalConfig)
	if got.User != jump.Username {
		t.Fatalf("User = %q, want %q", got.User, jump.Username)
	}
	if got.Timeout != finalConfig.Timeout {
		t.Fatalf("Timeout = %s, want %s", got.Timeout, finalConfig.Timeout)
	}
	if got.HostKeyCallback == nil {
		t.Fatal("HostKeyCallback is nil")
	}

	if err := got.HostKeyCallback("jump.example:2222", nil, nil); err != nil {
		t.Fatalf("HostKeyCallback() error = %v", err)
	}
	if !called {
		t.Fatal("final host key callback was not used")
	}
}
