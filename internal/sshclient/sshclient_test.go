package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/andydixon/link/internal/models"
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

func TestPingReturnsAfterSSHTransportCloses(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}

	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverReady := make(chan *ssh.ServerConn, 1)
	serverErrors := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr
			return
		}
		serverConn, _, _, handshakeErr := ssh.NewServerConn(conn, serverConfig)
		if handshakeErr != nil {
			_ = conn.Close()
			serverErrors <- handshakeErr
			return
		}
		serverReady <- serverConn
	}()

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialTimeout() error = %v", err)
	}
	clientConn, channels, requests, err := ssh.NewClientConn(conn, listener.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Test server uses an ephemeral key.
	})
	if err != nil {
		_ = conn.Close()
		t.Fatalf("NewClientConn() error = %v", err)
	}
	rawClient := ssh.NewClient(clientConn, channels, requests)

	var serverConn *ssh.ServerConn
	select {
	case serverConn = <-serverReady:
	case serverErr := <-serverErrors:
		t.Fatalf("SSH server handshake error = %v", serverErr)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSH server handshake")
	}

	client := NewClient(&models.Profile{})
	client.mu.Lock()
	client.sshClient = rawClient
	client.connected = true
	client.mu.Unlock()
	t.Cleanup(func() { _ = client.Close() })

	if err := serverConn.Close(); err != nil {
		t.Fatalf("server Close() error = %v", err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- rawClient.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSH transport to close")
	}

	pingDone := make(chan error, 1)
	go func() {
		_, pingErr := client.Ping()
		pingDone <- pingErr
	}()
	select {
	case pingErr := <-pingDone:
		if pingErr == nil {
			t.Fatal("Ping() succeeded after the SSH transport closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ping() did not return after the SSH transport closed")
	}
}
