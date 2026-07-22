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
	DefaultVNCPort    = 5900
	DefaultIRCPort    = 6667
	DefaultIRCTLSPort = 6697
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
		UseNativePaste:      false,
		LocalShell:          "", // empty means use system default
		LeviathanEnabled:    false,
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
	case ProtocolVNC:
		p.Port = DefaultVNCPort
		p.VNC = &VNCConfig{
			VNCPort:       DefaultVNCPort,
			SecretMode:    SecretModeSaved,
			ScaleToFit:    true,
			SharedSession: true,
			Quality:       6,
			SSHPort:       DefaultSSHPort,
			SSHAuthMethod: "publickey",
			SSHSecretMode: SecretModeSaved,
		}
	case ProtocolIRC:
		p.Port = DefaultIRCPort
		p.IRC = &IRCConfig{
			Nick:               "nexus",
			RealName:           "Nexus Terminal",
			ServerPasswordMode: SecretModeAskEveryTime,
			NickServMode:       SecretModeSaved,
			AutoJoinRooms:      []string{},
			SSHPort:            DefaultSSHPort,
			SSHAuthMethod:      "publickey",
			SSHSecretMode:      SecretModeSaved,
			AutoReconnect:      true,
		}
	}

	return p
}

// NormalizeProfileDefaults fills missing default values on profiles loaded
// from older exports, third-party imports, or sparse frontend payloads.
func NormalizeProfileDefaults(p *Profile) {
	if p == nil {
		return
	}
	if p.Protocol == "" {
		p.Protocol = ProtocolSSH
	}

	defaults := NewProfile(p.Protocol)
	if p.ID == "" {
		p.ID = defaults.ID
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	if p.ConnectTimeout == 0 {
		p.ConnectTimeout = DefaultConnectTimeout
	}
	if p.TerminalFontFamily == "" {
		p.TerminalFontFamily = DefaultFontFamily
	}
	if p.TerminalFontSize == 0 {
		p.TerminalFontSize = DefaultFontSize
	}
	if p.TerminalScrollback == 0 {
		p.TerminalScrollback = DefaultScrollback
	}
	if p.TerminalCursorStyle == "" {
		p.TerminalCursorStyle = DefaultCursorStyle
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = defaults.CreatedAt
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = defaults.UpdatedAt
	}
	if p.Port == 0 {
		p.Port = defaults.Port
	}
	if p.DisplayName == "" {
		p.DisplayName = defaultProfileName(p)
	}

	switch p.Protocol {
	case ProtocolSSH:
		normalizeSSHConfig(p, defaults.SSH)
	case ProtocolTelnet:
		normalizeTelnetConfig(p, defaults.Telnet)
	case ProtocolRAW:
		normalizeRAWConfig(p, defaults.RAW)
	case ProtocolGitHub:
		if p.Host == "" {
			p.Host = defaults.Host
		}
		normalizeGitHubConfig(p, defaults.GitHub)
	case ProtocolVNC:
		normalizeVNCConfig(p, defaults.VNC)
	case ProtocolIRC:
		normalizeIRCConfig(p, defaults.IRC)
	}
}

func defaultProfileName(p *Profile) string {
	if p.Host != "" {
		return p.Host
	}
	if p.VNC != nil && p.VNC.VNCHost != "" {
		return p.VNC.VNCHost
	}
	switch p.Protocol {
	case ProtocolGitHub:
		return "GitHub"
	case ProtocolVNC:
		return "VNC"
	case ProtocolIRC:
		return "IRC"
	case ProtocolRAW:
		return "RAW TCP"
	case ProtocolTelnet:
		return "Telnet"
	default:
		return "SSH"
	}
}

func normalizeSecretMode(mode SecretMode, fallback SecretMode) SecretMode {
	switch mode {
	case SecretModeSaved, SecretModeSessionOnly, SecretModeAskEveryTime:
		return mode
	case "prompt":
		return SecretModeAskEveryTime
	case "":
		return fallback
	default:
		return mode
	}
}

func normalizeSSHConfig(p *Profile, defaults *SSHConfig) {
	if p.SSH == nil {
		cfg := *defaults
		p.SSH = &cfg
		return
	}
	cfg := p.SSH
	if len(cfg.AuthMethods) == 0 {
		cfg.AuthMethods = append([]string{}, defaults.AuthMethods...)
	}
	cfg.SecretMode = normalizeSecretMode(cfg.SecretMode, defaults.SecretMode)
	cfg.KeySecretMode = normalizeSecretMode(cfg.KeySecretMode, defaults.KeySecretMode)
	if cfg.HostKeyPolicy == "" {
		cfg.HostKeyPolicy = defaults.HostKeyPolicy
	}
	if cfg.TerminalType == "" {
		cfg.TerminalType = defaults.TerminalType
	}
	if cfg.KeepaliveInterval == 0 {
		cfg.KeepaliveInterval = defaults.KeepaliveInterval
	}
	if cfg.KeepaliveCountMax == 0 {
		cfg.KeepaliveCountMax = defaults.KeepaliveCountMax
	}
	if cfg.JumpHosts == nil {
		cfg.JumpHosts = []JumpHost{}
	}
	if cfg.PortForwards == nil {
		cfg.PortForwards = []PortForward{}
	}
	if cfg.EnvVars == nil {
		cfg.EnvVars = map[string]string{}
	}
}

func normalizeTelnetConfig(p *Profile, defaults *TelnetConfig) {
	if p.Telnet == nil {
		cfg := *defaults
		p.Telnet = &cfg
		return
	}
	cfg := p.Telnet
	if cfg.LineEnding == "" {
		cfg.LineEnding = defaults.LineEnding
	}
	if cfg.TerminalType == "" {
		cfg.TerminalType = defaults.TerminalType
	}
	if cfg.Encoding == "" {
		cfg.Encoding = defaults.Encoding
	}
}

func normalizeRAWConfig(p *Profile, defaults *RAWConfig) {
	if p.RAW == nil {
		cfg := *defaults
		p.RAW = &cfg
		return
	}
	cfg := p.RAW
	if cfg.LineEnding == "" {
		cfg.LineEnding = defaults.LineEnding
	}
	if cfg.Encoding == "" {
		cfg.Encoding = defaults.Encoding
	}
	if cfg.PayloadPresets == nil {
		cfg.PayloadPresets = []PayloadPreset{}
	}
	for i := range cfg.PayloadPresets {
		if cfg.PayloadPresets[i].ID == "" {
			cfg.PayloadPresets[i].ID = uuid.New().String()
		}
		if cfg.PayloadPresets[i].Format == "" {
			cfg.PayloadPresets[i].Format = "text"
		}
	}
}

func normalizeGitHubConfig(p *Profile, defaults *GitHubConfig) {
	if p.GitHub == nil {
		cfg := *defaults
		p.GitHub = &cfg
		return
	}
	cfg := p.GitHub
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaults.BaseURL
	}
	cfg.SecretMode = normalizeSecretMode(cfg.SecretMode, defaults.SecretMode)
	if cfg.RefreshIntervalSecs == 0 {
		cfg.RefreshIntervalSecs = defaults.RefreshIntervalSecs
	}
}

func normalizeVNCConfig(p *Profile, defaults *VNCConfig) {
	if p.VNC == nil {
		cfg := *defaults
		p.VNC = &cfg
		return
	}
	cfg := p.VNC
	if cfg.VNCPort == 0 {
		cfg.VNCPort = defaults.VNCPort
	}
	cfg.SecretMode = normalizeSecretMode(cfg.SecretMode, defaults.SecretMode)
	if cfg.Quality < 0 {
		cfg.Quality = defaults.Quality
	}
	if cfg.SSHPort == 0 {
		cfg.SSHPort = defaults.SSHPort
	}
	if cfg.SSHAuthMethod == "" {
		cfg.SSHAuthMethod = defaults.SSHAuthMethod
	}
	cfg.SSHSecretMode = normalizeSecretMode(cfg.SSHSecretMode, defaults.SSHSecretMode)
}

func normalizeIRCConfig(p *Profile, defaults *IRCConfig) {
	if p.IRC == nil {
		cfg := *defaults
		cfg.AutoJoinRooms = append([]string{}, defaults.AutoJoinRooms...)
		p.IRC = &cfg
		return
	}
	cfg := p.IRC
	if cfg.Nick == "" {
		cfg.Nick = defaults.Nick
	}
	if cfg.RealName == "" {
		cfg.RealName = defaults.RealName
	}
	cfg.ServerPasswordMode = normalizeSecretMode(cfg.ServerPasswordMode, defaults.ServerPasswordMode)
	cfg.NickServMode = normalizeSecretMode(cfg.NickServMode, defaults.NickServMode)
	if cfg.AutoJoinRooms == nil {
		cfg.AutoJoinRooms = []string{}
	}
	if cfg.SSHPort == 0 {
		cfg.SSHPort = defaults.SSHPort
	}
	if cfg.SSHAuthMethod == "" {
		cfg.SSHAuthMethod = defaults.SSHAuthMethod
	}
	cfg.SSHSecretMode = normalizeSecretMode(cfg.SSHSecretMode, defaults.SSHSecretMode)
}
