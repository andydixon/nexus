package sshconfig

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testConfig = `# Test SSH config

Host *
    ServerAliveInterval 60

Host webserver
    HostName web.example.com
    Port 2222
    User deploy
    IdentityFile ~/.ssh/deploy_key
    ForwardAgent yes
    Compression yes

Host dbserver
    HostName db.internal.example.com
    User postgres
    ProxyJump bastion@jump.example.com:2200

Host simple
    HostName 10.0.0.5

Host wildcard-*
    User admin
`

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(testConfig), 0600); err != nil {
		t.Fatal(err)
	}

	blocks, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	// Should have 3 blocks: webserver, dbserver, simple.
	// "Host *" and "Host wildcard-*" should be skipped.
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}

	// Verify webserver block.
	wb := blocks[0]
	if wb.Aliases[0] != "webserver" {
		t.Errorf("expected alias 'webserver', got %q", wb.Aliases[0])
	}
	if wb.HostName != "web.example.com" {
		t.Errorf("expected HostName 'web.example.com', got %q", wb.HostName)
	}
	if wb.Port != 2222 {
		t.Errorf("expected port 2222, got %d", wb.Port)
	}
	if wb.User != "deploy" {
		t.Errorf("expected user 'deploy', got %q", wb.User)
	}
	if !wb.ForwardAgent {
		t.Error("expected ForwardAgent to be true")
	}
	if !wb.Compression {
		t.Error("expected Compression to be true")
	}

	// Verify dbserver block with ProxyJump.
	db := blocks[1]
	if db.Aliases[0] != "dbserver" {
		t.Errorf("expected alias 'dbserver', got %q", db.Aliases[0])
	}
	if db.ProxyJump != "bastion@jump.example.com:2200" {
		t.Errorf("unexpected ProxyJump: %q", db.ProxyJump)
	}

	// Verify simple block.
	sb := blocks[2]
	if sb.HostName != "10.0.0.5" {
		t.Errorf("expected HostName '10.0.0.5', got %q", sb.HostName)
	}
	if sb.Port != 22 {
		t.Errorf("expected default port 22, got %d", sb.Port)
	}
}

func TestParseFileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxConfigFileBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = ParseFile(path)
	if err == nil {
		t.Fatal("expected oversized config file to be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %q, want too large", err.Error())
	}
}

func TestParseFileRejectsTooManyHostBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	var b strings.Builder
	for i := 0; i < maxHostBlocks+1; i++ {
		b.WriteString("Host host")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n    HostName example.com\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected excessive Host blocks to be rejected")
	}
	if !strings.Contains(err.Error(), "too many Host blocks") {
		t.Fatalf("error = %q, want too many Host blocks", err.Error())
	}
}

func TestToProfiles(t *testing.T) {
	blocks := []HostBlock{
		{
			Aliases:      []string{"myserver"},
			HostName:     "srv.example.com",
			Port:         22,
			User:         "admin",
			ForwardAgent: true,
		},
		{
			Aliases:   []string{"jumpbox"},
			HostName:  "jump.example.com",
			Port:      2200,
			User:      "jumper",
			ProxyJump: "gateway@gw.example.com",
		},
	}

	profiles := ToProfiles(blocks, "Imported")
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}

	p := profiles[0]
	if p.DisplayName != "myserver" {
		t.Errorf("expected DisplayName 'myserver', got %q", p.DisplayName)
	}
	if p.Host != "srv.example.com" {
		t.Errorf("expected host 'srv.example.com', got %q", p.Host)
	}
	if p.SSH == nil {
		t.Fatal("expected SSH config to be set")
	}
	if !p.SSH.AgentAuth {
		t.Error("expected AgentAuth to be true for ForwardAgent host")
	}

	jp := profiles[1]
	if jp.SSH == nil || len(jp.SSH.JumpHosts) == 0 {
		t.Fatal("expected jump host from ProxyJump")
	}
	jh := jp.SSH.JumpHosts[0]
	if jh.Host != "gw.example.com" {
		t.Errorf("expected jump host 'gw.example.com', got %q", jh.Host)
	}
	if jh.Username != "gateway" {
		t.Errorf("expected jump user 'gateway', got %q", jh.Username)
	}
}

func TestParseProxyJump(t *testing.T) {
	tests := []struct {
		input    string
		wantHost string
		wantUser string
		wantPort int
	}{
		{"bastion.example.com", "bastion.example.com", "", 22},
		{"user@bastion.example.com", "bastion.example.com", "user", 22},
		{"user@bastion.example.com:2200", "bastion.example.com", "user", 2200},
		{"host:443", "host", "", 443},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			jh := parseProxyJump(tt.input)
			if jh == nil {
				t.Fatal("expected non-nil result")
			}
			if jh.Host != tt.wantHost {
				t.Errorf("host: got %q, want %q", jh.Host, tt.wantHost)
			}
			if jh.Username != tt.wantUser {
				t.Errorf("user: got %q, want %q", jh.Username, tt.wantUser)
			}
			if jh.Port != tt.wantPort {
				t.Errorf("port: got %d, want %d", jh.Port, tt.wantPort)
			}
		})
	}
}
