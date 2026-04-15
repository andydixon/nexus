package models

import (
	"time"

	"github.com/google/uuid"
)

// Default port numbers for each protocol.
const (
	DefaultSSHPort    = 22
	DefaultTelnetPort = 23
	DefaultRAWPort    = 23
	DefaultGitHubPort = 443
)

// Default timeout values (in seconds).
const (
	DefaultConnectTimeout = 30
	DefaultReadTimeout    = 0 // no timeout
)

// Default terminal rendering settings.
const (
	DefaultScrollback   = 10000
	DefaultFontSize     = 14
	DefaultFontFamily   = "JetBrains Mono, Menlo, Monaco, Consolas, monospace"
	DefaultCursorStyle  = "block"
	DefaultTerminalType = "xterm-256color"
)

// Default SSH keepalive settings.
const (
	DefaultKeepaliveInterval = 30
	DefaultKeepaliveCountMax = 3
)

// DefaultAppPreferences returns sensible default application preferences.
func DefaultAppPreferences() AppPreferences {
	return AppPreferences{
		Theme:               "dark",
		TerminalTheme:       "charcoal",
		DefaultProtocol:     string(ProtocolSSH),
		ConfirmOnClose:      true,
		ConfirmOnDisconnect: true,
		ShowStatusBar:       true,
		SidebarWidth:        280,
		DefaultFontFamily:   DefaultFontFamily,
		DefaultFontSize:     DefaultFontSize,
		DefaultScrollback:   DefaultScrollback,
		DefaultCursorStyle:  DefaultCursorStyle,
	}
}

// NewProfile creates a new Profile with sensible defaults for the given protocol.
func NewProfile(protocol Protocol) Profile {
	now := time.Now()
	p := Profile{
		ID:                  uuid.New().String(),
		Protocol:            protocol,
		Tags:                []string{},
		ConnectTimeout:      DefaultConnectTimeout,
		ReadTimeout:         DefaultReadTimeout,
		TerminalFontFamily:  DefaultFontFamily,
		TerminalFontSize:    DefaultFontSize,
		TerminalScrollback:  DefaultScrollback,
		TerminalCursorStyle: DefaultCursorStyle,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	switch protocol {
	case ProtocolSSH:
		p.Port = DefaultSSHPort
		p.SSH = &SSHConfig{
			AuthMethods:       []string{"publickey", "password", "keyboard-interactive"},
			SecretMode:        SecretModeSaved,
			KeySecretMode:     SecretModeSaved,
			HostKeyPolicy:     HostKeyAsk,
			TerminalType:      DefaultTerminalType,
			KeepaliveInterval: DefaultKeepaliveInterval,
			KeepaliveCountMax: DefaultKeepaliveCountMax,
			JumpHosts:         []JumpHost{},
			PortForwards:      []PortForward{},
			EnvVars:           map[string]string{},
		}
	case ProtocolTelnet:
		p.Port = DefaultTelnetPort
		p.Telnet = &TelnetConfig{
			LocalEcho:    false,
			LineEnding:   LineEndingCRLF,
			TerminalType: DefaultTerminalType,
			Encoding:     "utf-8",
		}
	case ProtocolRAW:
		p.Port = DefaultRAWPort
		p.RAW = &RAWConfig{
			LineEnding:     LineEndingCRLF,
			Encoding:       "utf-8",
			PayloadPresets: []PayloadPreset{},
		}
	case ProtocolGitHub:
		p.Port = DefaultGitHubPort
		p.Host = "github.com"
		p.GitHub = &GitHubConfig{
			BaseURL:             "https://api.github.com",
			SecretMode:          SecretModeSaved,
			RefreshIntervalSecs: 300,
		}
	}

	return p
}
