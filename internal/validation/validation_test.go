package validation

import (
	"testing"

	"github.com/andydixon/link/internal/models"
)

// ---------------------------------------------------------------------------
// ValidateHost
// ---------------------------------------------------------------------------

func TestValidateHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{name: "valid hostname", host: "example.com", wantErr: false},
		{name: "valid IP address", host: "192.168.1.1", wantErr: false},
		{name: "valid IPv6 address", host: "::1", wantErr: false},
		{name: "empty string", host: "", wantErr: true},
		{name: "only spaces", host: "   ", wantErr: true},
		{name: "contains spaces", host: "host name", wantErr: true},
		{name: "contains tab", host: "host\tname", wantErr: true},
		{name: "URI scheme present", host: "ssh://example.com", wantErr: true},
		{name: "leading hyphen", host: "-bad-host", wantErr: true},
		{name: "subdomain hostname", host: "server.internal.corp", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHost(tt.host)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateHost(%q) error = %v, wantErr %v", tt.host, err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidatePort
// ---------------------------------------------------------------------------

func TestValidatePort(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{name: "port 1 is valid", port: 1, wantErr: false},
		{name: "port 22 is valid", port: 22, wantErr: false},
		{name: "port 443 is valid", port: 443, wantErr: false},
		{name: "port 65535 is valid", port: 65535, wantErr: false},
		{name: "port 0 is invalid", port: 0, wantErr: true},
		{name: "negative port is invalid", port: -1, wantErr: true},
		{name: "port 65536 is invalid", port: 65536, wantErr: true},
		{name: "very large port is invalid", port: 99999, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePort(tt.port)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePort(%d) error = %v, wantErr %v", tt.port, err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateUsername
// ---------------------------------------------------------------------------

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		wantErr  bool
	}{
		{name: "valid username", username: "admin", wantErr: false},
		{name: "empty string is invalid", username: "", wantErr: true},
		{name: "only whitespace is invalid", username: "   ", wantErr: true},
		{name: "username with spaces is allowed", username: "my user", wantErr: false},
		{name: "very long username is valid", username: "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789", wantErr: false},
		{name: "contains control character", username: "bad\x01user", wantErr: true},
		{name: "contains DEL character", username: "bad\x7fuser", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.username)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUsername(%q) error = %v, wantErr %v", tt.username, err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateProfile
// ---------------------------------------------------------------------------

func TestValidateProfile(t *testing.T) {
	// Helper to build a valid SSH profile.
	validSSH := func() *models.Profile {
		p := models.NewProfile(models.ProtocolSSH)
		p.DisplayName = "Test SSH"
		p.Host = "example.com"
		p.Username = "root"
		return &p
	}

	// Helper to build a valid Telnet profile.
	validTelnet := func() *models.Profile {
		p := models.NewProfile(models.ProtocolTelnet)
		p.DisplayName = "Test Telnet"
		p.Host = "switch.local"
		return &p
	}

	// Helper to build a valid RAW profile.
	validRAW := func() *models.Profile {
		p := models.NewProfile(models.ProtocolRAW)
		p.DisplayName = "Test RAW"
		p.Host = "device.local"
		return &p
	}

	validVNC := func() *models.Profile {
		p := models.NewProfile(models.ProtocolVNC)
		p.DisplayName = "Test VNC"
		p.Host = ""
		p.VNC.VNCHost = "desktop.local"
		return &p
	}

	tests := []struct {
		name    string
		profile *models.Profile
		wantErr bool
	}{
		{name: "full valid SSH profile", profile: validSSH(), wantErr: false},
		{name: "full valid Telnet profile", profile: validTelnet(), wantErr: false},
		{name: "full valid RAW profile", profile: validRAW(), wantErr: false},
		{name: "valid VNC profile with VNC-specific host", profile: validVNC(), wantErr: false},
		{name: "nil profile", profile: nil, wantErr: true},
		{
			name: "missing host",
			profile: func() *models.Profile {
				p := validSSH()
				p.Host = ""
				return p
			}(),
			wantErr: true,
		},
		{
			name: "missing port (zero)",
			profile: func() *models.Profile {
				p := validSSH()
				p.Port = 0
				return p
			}(),
			wantErr: true,
		},
		{
			name: "invalid protocol",
			profile: func() *models.Profile {
				p := validSSH()
				p.Protocol = "ftp"
				return p
			}(),
			wantErr: true,
		},
		{
			name: "SSH missing username",
			profile: func() *models.Profile {
				p := validSSH()
				p.Username = ""
				return p
			}(),
			wantErr: true,
		},
		{
			name: "SSH nil config",
			profile: func() *models.Profile {
				p := validSSH()
				p.SSH = nil
				return p
			}(),
			wantErr: true,
		},
		{
			name: "negative connect timeout",
			profile: func() *models.Profile {
				p := validSSH()
				p.ConnectTimeout = -1
				return p
			}(),
			wantErr: true,
		},
		{
			name: "font size out of range",
			profile: func() *models.Profile {
				p := validSSH()
				p.TerminalFontSize = 0
				return p
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfile(tt.profile)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateProfile() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateGitHubConfig
// ---------------------------------------------------------------------------

func TestValidateGitHubConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *models.GitHubConfig
		wantErr bool
	}{
		{
			name: "valid github.com API URL",
			cfg: &models.GitHubConfig{
				BaseURL:             "https://api.github.com",
				SecretMode:          models.SecretModeSaved,
				RefreshIntervalSecs: 300,
			},
			wantErr: false,
		},
		{
			name: "valid loopback HTTP URL",
			cfg: &models.GitHubConfig{
				BaseURL:             "http://localhost:3000/api/v3",
				SecretMode:          models.SecretModeAskEveryTime,
				RefreshIntervalSecs: 60,
			},
			wantErr: false,
		},
		{
			name: "rejects non-HTTPS remote URL",
			cfg: &models.GitHubConfig{
				BaseURL:             "http://evil.example/api/v3",
				SecretMode:          models.SecretModeSaved,
				RefreshIntervalSecs: 300,
			},
			wantErr: true,
		},
		{
			name: "rejects URL credentials",
			cfg: &models.GitHubConfig{
				BaseURL:             "https://token@example.com/api/v3",
				SecretMode:          models.SecretModeSaved,
				RefreshIntervalSecs: 300,
			},
			wantErr: true,
		},
		{
			name: "rejects aggressive refresh interval",
			cfg: &models.GitHubConfig{
				BaseURL:             "https://api.github.com",
				SecretMode:          models.SecretModeSaved,
				RefreshIntervalSecs: 1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGitHubConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateGitHubConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateVNCConfig
// ---------------------------------------------------------------------------

func TestValidateVNCConfig(t *testing.T) {
	tests := []struct {
		name        string
		profileHost string
		cfg         *models.VNCConfig
		wantErr     bool
	}{
		{
			name:        "uses VNC-specific host",
			profileHost: "",
			cfg: &models.VNCConfig{
				VNCHost: "desktop.local",
				VNCPort: 5900,
				Quality: 6,
			},
			wantErr: false,
		},
		{
			name:        "uses profile host fallback",
			profileHost: "desktop.local",
			cfg: &models.VNCConfig{
				VNCPort: 5900,
				Quality: 6,
			},
			wantErr: false,
		},
		{
			name:        "rejects missing host",
			profileHost: "",
			cfg: &models.VNCConfig{
				VNCPort: 5900,
				Quality: 6,
			},
			wantErr: true,
		},
		{
			name:        "rejects invalid SSH tunnel auth method",
			profileHost: "desktop.local",
			cfg: &models.VNCConfig{
				VNCPort:       5900,
				Quality:       6,
				SSHTunnel:     true,
				SSHHost:       "jump.local",
				SSHPort:       22,
				SSHUsername:   "admin",
				SSHAuthMethod: "token",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVNCConfig(tt.profileHost, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateVNCConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateSSHConfig
// ---------------------------------------------------------------------------

func TestValidateSSHConfig(t *testing.T) {
	validCfg := func() *models.SSHConfig {
		return &models.SSHConfig{
			AuthMethods:       []string{"password"},
			SecretMode:        models.SecretModeSaved,
			KeySecretMode:     models.SecretModeAskEveryTime,
			HostKeyPolicy:     models.HostKeyAsk,
			KeepaliveInterval: 30,
			KeepaliveCountMax: 3,
			JumpHosts:         []models.JumpHost{},
		}
	}

	tests := []struct {
		name    string
		cfg     *models.SSHConfig
		wantErr bool
	}{
		{name: "valid config", cfg: validCfg(), wantErr: false},
		{
			name: "empty auth methods",
			cfg: func() *models.SSHConfig {
				c := validCfg()
				c.AuthMethods = nil
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid auth method",
			cfg: func() *models.SSHConfig {
				c := validCfg()
				c.AuthMethods = []string{"oauth"}
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid secret mode",
			cfg: func() *models.SSHConfig {
				c := validCfg()
				c.SecretMode = "wrong"
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid host key policy",
			cfg: func() *models.SSHConfig {
				c := validCfg()
				c.HostKeyPolicy = "yolo"
				return c
			}(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSSHConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSSHConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateTelnetConfig
// ---------------------------------------------------------------------------

func TestValidateTelnetConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *models.TelnetConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &models.TelnetConfig{
				LineEnding: models.LineEndingCRLF,
				Encoding:   "utf-8",
			},
			wantErr: false,
		},
		{
			name: "invalid line ending",
			cfg: &models.TelnetConfig{
				LineEnding: "bad",
				Encoding:   "utf-8",
			},
			wantErr: true,
		},
		{
			name: "empty encoding",
			cfg: &models.TelnetConfig{
				LineEnding: models.LineEndingLF,
				Encoding:   "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTelnetConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTelnetConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateRAWConfig
// ---------------------------------------------------------------------------

func TestValidateRAWConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *models.RAWConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &models.RAWConfig{
				LineEnding:     models.LineEndingCRLF,
				Encoding:       "utf-8",
				PayloadPresets: []models.PayloadPreset{},
			},
			wantErr: false,
		},
		{
			name: "invalid line ending",
			cfg: &models.RAWConfig{
				LineEnding: "bad",
				Encoding:   "utf-8",
			},
			wantErr: true,
		},
		{
			name: "empty encoding",
			cfg: &models.RAWConfig{
				LineEnding: models.LineEndingLF,
				Encoding:   "",
			},
			wantErr: true,
		},
		{
			name: "preset with invalid format",
			cfg: &models.RAWConfig{
				LineEnding: models.LineEndingCRLF,
				Encoding:   "utf-8",
				PayloadPresets: []models.PayloadPreset{
					{Name: "ping", Format: "binary"},
				},
			},
			wantErr: true,
		},
		{
			name: "preset with empty name",
			cfg: &models.RAWConfig{
				LineEnding: models.LineEndingCRLF,
				Encoding:   "utf-8",
				PayloadPresets: []models.PayloadPreset{
					{Name: "", Format: "text"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRAWConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRAWConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
