// Package validation provides thorough validation for connection profiles
// and their protocol-specific configurations.
package validation

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/andydixon/link/internal/models"
	"github.com/lrstanley/girc"
)

// ValidationError describes a single field-level validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error satisfies the error interface.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationErrors collects multiple validation failures into one error.
type ValidationErrors struct {
	Errors []ValidationError `json:"errors"`
}

// Error satisfies the error interface, returning a summary of all failures.
func (e *ValidationErrors) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, err := range e.Errors {
		msgs[i] = err.Error()
	}
	return fmt.Sprintf("validation failed: %s", strings.Join(msgs, "; "))
}

// HasErrors reports whether any validation errors have been collected.
func (e *ValidationErrors) HasErrors() bool {
	return len(e.Errors) > 0
}

// Add appends a validation error to the collection.
func (e *ValidationErrors) Add(field, message string) {
	e.Errors = append(e.Errors, ValidationError{Field: field, Message: message})
}

// ValidateProfile validates all fields of a connection profile, including
// protocol-specific configuration blocks.
func ValidateProfile(p *models.Profile) error {
	errs := &ValidationErrors{}

	if p == nil {
		return &ValidationError{Field: "profile", Message: "profile must not be nil"}
	}

	if strings.TrimSpace(p.ID) == "" {
		errs.Add("id", "profile ID must not be empty")
	}
	if strings.TrimSpace(p.DisplayName) == "" {
		errs.Add("displayName", "display name must not be empty")
	}

	switch p.Protocol {
	case models.ProtocolSSH:
		validateProfileHostPort(errs, p.Host, p.Port)
		if strings.TrimSpace(p.Username) == "" {
			errs.Add("username", "username is required for SSH connections")
		} else if err := ValidateUsername(p.Username); err != nil {
			errs.Add("username", err.(*ValidationError).Message)
		}
		if p.SSH == nil {
			errs.Add("ssh", "SSH configuration is required for SSH protocol")
		} else if err := ValidateSSHConfig(p.SSH); err != nil {
			if ve, ok := err.(*ValidationErrors); ok {
				errs.Errors = append(errs.Errors, ve.Errors...)
			}
		}
	case models.ProtocolTelnet:
		validateProfileHostPort(errs, p.Host, p.Port)
		if p.Telnet == nil {
			errs.Add("telnet", "Telnet configuration is required for Telnet protocol")
		} else if err := ValidateTelnetConfig(p.Telnet); err != nil {
			if ve, ok := err.(*ValidationErrors); ok {
				errs.Errors = append(errs.Errors, ve.Errors...)
			}
		}
	case models.ProtocolRAW:
		validateProfileHostPort(errs, p.Host, p.Port)
		if p.RAW == nil {
			errs.Add("raw", "RAW configuration is required for RAW protocol")
		} else if err := ValidateRAWConfig(p.RAW); err != nil {
			if ve, ok := err.(*ValidationErrors); ok {
				errs.Errors = append(errs.Errors, ve.Errors...)
			}
		}
	case models.ProtocolGitHub:
		if p.GitHub == nil {
			errs.Add("github", "GitHub configuration is required for GitHub protocol")
		} else if err := ValidateGitHubConfig(p.GitHub); err != nil {
			if ve, ok := err.(*ValidationErrors); ok {
				errs.Errors = append(errs.Errors, ve.Errors...)
			}
		}
	case models.ProtocolVNC:
		if p.VNC == nil {
			errs.Add("vnc", "VNC configuration is required for VNC protocol")
		} else if err := ValidateVNCConfig(p.Host, p.VNC); err != nil {
			if ve, ok := err.(*ValidationErrors); ok {
				errs.Errors = append(errs.Errors, ve.Errors...)
			}
		}
	case models.ProtocolIRC:
		validateProfileHostPort(errs, p.Host, p.Port)
		if p.IRC == nil {
			errs.Add("irc", "IRC configuration is required for IRC protocol")
		} else if err := ValidateIRCConfig(p.IRC); err != nil {
			if ve, ok := err.(*ValidationErrors); ok {
				errs.Errors = append(errs.Errors, ve.Errors...)
			}
		}
	default:
		errs.Add("protocol", fmt.Sprintf("unsupported protocol %q", p.Protocol))
	}

	if p.ConnectTimeout < 0 {
		errs.Add("connectTimeout", "connect timeout must not be negative")
	}
	if p.ReadTimeout < 0 {
		errs.Add("readTimeout", "read timeout must not be negative")
	}
	if p.TerminalFontSize < 1 || p.TerminalFontSize > 72 {
		errs.Add("terminalFontSize", "font size must be between 1 and 72")
	}
	if p.TerminalScrollback < 0 {
		errs.Add("terminalScrollback", "scrollback must not be negative")
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

func validateProfileHostPort(errs *ValidationErrors, host string, port int) {
	if err := ValidateHost(host); err != nil {
		errs.Add("host", err.(*ValidationError).Message)
	}
	if err := ValidatePort(port); err != nil {
		errs.Add("port", err.(*ValidationError).Message)
	}
}

// ValidateHost ensures the host string is non-empty and syntactically valid.
func ValidateHost(host string) error {
	h := strings.TrimSpace(host)
	if h == "" {
		return &ValidationError{Field: "host", Message: "host must not be empty"}
	}
	// Reject hosts that contain whitespace.
	if strings.ContainsAny(h, " \t\n\r") {
		return &ValidationError{Field: "host", Message: "host must not contain whitespace"}
	}
	// Reject hosts that contain a scheme (e.g. "ssh://").
	if strings.Contains(h, "://") {
		return &ValidationError{Field: "host", Message: "host must not include a URI scheme"}
	}
	// If it looks like an IP address, parse it properly.
	if ip := net.ParseIP(h); ip == nil {
		// Not a plain IP — verify it is a plausible hostname.
		if strings.HasPrefix(h, "-") {
			return &ValidationError{Field: "host", Message: "host must not begin with a hyphen"}
		}
	}
	return nil
}

// ValidatePort checks that the port number is within the valid TCP range.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return &ValidationError{Field: "port", Message: "port must be between 1 and 65535"}
	}
	return nil
}

// ValidateUsername checks that the username is non-empty and free of control characters.
func ValidateUsername(username string) error {
	u := strings.TrimSpace(username)
	if u == "" {
		return &ValidationError{Field: "username", Message: "username must not be empty"}
	}
	for _, r := range u {
		if r < 0x20 || r == 0x7f {
			return &ValidationError{Field: "username", Message: "username must not contain control characters"}
		}
	}
	return nil
}

// ValidateSSHConfig validates the SSH-specific configuration block.
func ValidateSSHConfig(cfg *models.SSHConfig) error {
	errs := &ValidationErrors{}

	if len(cfg.AuthMethods) == 0 {
		errs.Add("ssh.authMethods", "at least one authentication method is required")
	}
	validAuth := map[string]bool{
		"password": true, "publickey": true, "keyboard-interactive": true,
	}
	for _, m := range cfg.AuthMethods {
		if !validAuth[m] {
			errs.Add("ssh.authMethods", fmt.Sprintf("unsupported authentication method %q", m))
		}
	}

	validSecret := map[models.SecretMode]bool{
		models.SecretModeSaved: true, models.SecretModeSessionOnly: true, models.SecretModeAskEveryTime: true,
	}
	if !validSecret[cfg.SecretMode] {
		errs.Add("ssh.secretMode", fmt.Sprintf("unsupported secret mode %q", cfg.SecretMode))
	}
	if !validSecret[cfg.KeySecretMode] {
		errs.Add("ssh.keySecretMode", fmt.Sprintf("unsupported key secret mode %q", cfg.KeySecretMode))
	}

	validPolicy := map[models.HostKeyPolicy]bool{
		models.HostKeyStrict: true, models.HostKeyAsk: true,
		models.HostKeyAcceptNew: true, models.HostKeyInsecure: true,
	}
	if !validPolicy[cfg.HostKeyPolicy] {
		errs.Add("ssh.hostKeyPolicy", fmt.Sprintf("unsupported host key policy %q", cfg.HostKeyPolicy))
	}

	if cfg.KeepaliveInterval < 0 {
		errs.Add("ssh.keepaliveInterval", "keepalive interval must not be negative")
	}
	if cfg.KeepaliveCountMax < 0 {
		errs.Add("ssh.keepaliveCountMax", "keepalive count max must not be negative")
	}

	// Validate each jump host entry.
	for i, jh := range cfg.JumpHosts {
		prefix := fmt.Sprintf("ssh.jumpHosts[%d]", i)
		if strings.TrimSpace(jh.Host) == "" {
			errs.Add(prefix+".host", "jump host address must not be empty")
		}
		if jh.Port < 1 || jh.Port > 65535 {
			errs.Add(prefix+".port", "jump host port must be between 1 and 65535")
		}
		if strings.TrimSpace(jh.Username) == "" {
			errs.Add(prefix+".username", "jump host username must not be empty")
		}
	}

	// Validate each port-forwarding rule.
	for i, pf := range cfg.PortForwards {
		prefix := fmt.Sprintf("ssh.portForwards[%d]", i)
		if pf.Direction != models.PortForwardLocal && pf.Direction != models.PortForwardRemote {
			errs.Add(prefix+".direction", fmt.Sprintf("unsupported forwarding direction %q", pf.Direction))
		}
		if pf.BindPort < 1 || pf.BindPort > 65535 {
			errs.Add(prefix+".bindPort", "bind port must be between 1 and 65535")
		}
		if pf.DestPort < 1 || pf.DestPort > 65535 {
			errs.Add(prefix+".destPort", "destination port must be between 1 and 65535")
		}
		if strings.TrimSpace(pf.DestHost) == "" {
			errs.Add(prefix+".destHost", "destination host must not be empty")
		}
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// ValidateTelnetConfig validates the Telnet-specific configuration block.
func ValidateTelnetConfig(cfg *models.TelnetConfig) error {
	errs := &ValidationErrors{}

	validEnding := map[models.LineEnding]bool{
		models.LineEndingLF: true, models.LineEndingCR: true,
		models.LineEndingCRLF: true, models.LineEndingNone: true,
	}
	if !validEnding[cfg.LineEnding] {
		errs.Add("telnet.lineEnding", fmt.Sprintf("unsupported line ending %q", cfg.LineEnding))
	}

	if strings.TrimSpace(cfg.Encoding) == "" {
		errs.Add("telnet.encoding", "encoding must not be empty")
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// ValidateRAWConfig validates the RAW TCP-specific configuration block.
func ValidateRAWConfig(cfg *models.RAWConfig) error {
	errs := &ValidationErrors{}

	validEnding := map[models.LineEnding]bool{
		models.LineEndingLF: true, models.LineEndingCR: true,
		models.LineEndingCRLF: true, models.LineEndingNone: true,
	}
	if !validEnding[cfg.LineEnding] {
		errs.Add("raw.lineEnding", fmt.Sprintf("unsupported line ending %q", cfg.LineEnding))
	}

	if strings.TrimSpace(cfg.Encoding) == "" {
		errs.Add("raw.encoding", "encoding must not be empty")
	}

	validFormat := map[string]bool{"text": true, "hex": true, "escaped": true}
	for i, preset := range cfg.PayloadPresets {
		prefix := fmt.Sprintf("raw.payloadPresets[%d]", i)
		if strings.TrimSpace(preset.Name) == "" {
			errs.Add(prefix+".name", "payload preset name must not be empty")
		}
		if !validFormat[preset.Format] {
			errs.Add(prefix+".format", fmt.Sprintf("unsupported payload format %q", preset.Format))
		}
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// ValidateGitHubConfig validates GitHub API settings before tokens are sent.
func ValidateGitHubConfig(cfg *models.GitHubConfig) error {
	errs := &ValidationErrors{}

	if cfg == nil {
		return &ValidationError{Field: "github", Message: "GitHub configuration must not be nil"}
	}

	u, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		errs.Add("github.baseURL", "API base URL must be an absolute HTTPS URL")
	} else {
		if u.User != nil {
			errs.Add("github.baseURL", "API base URL must not include credentials")
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
			errs.Add("github.baseURL", "API base URL must use HTTPS unless it points to loopback")
		}
	}

	validSecret := map[models.SecretMode]bool{
		models.SecretModeSaved: true, models.SecretModeSessionOnly: true, models.SecretModeAskEveryTime: true,
	}
	if !validSecret[cfg.SecretMode] {
		errs.Add("github.secretMode", fmt.Sprintf("unsupported secret mode %q", cfg.SecretMode))
	}
	if cfg.RefreshIntervalSecs != 0 && cfg.RefreshIntervalSecs < 60 {
		errs.Add("github.refreshIntervalSecs", "refresh interval must be at least 60 seconds")
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// ValidateVNCConfig validates VNC connection settings.
func ValidateVNCConfig(profileHost string, cfg *models.VNCConfig) error {
	errs := &ValidationErrors{}

	if cfg == nil {
		return &ValidationError{Field: "vnc", Message: "VNC configuration must not be nil"}
	}

	vncHost := strings.TrimSpace(cfg.VNCHost)
	if vncHost == "" {
		vncHost = strings.TrimSpace(profileHost)
	}
	if err := ValidateHost(vncHost); err != nil {
		errs.Add("vnc.vncHost", err.(*ValidationError).Message)
	}
	if err := ValidatePort(cfg.VNCPort); err != nil {
		errs.Add("vnc.vncPort", err.(*ValidationError).Message)
	}
	if cfg.Quality < 0 || cfg.Quality > 9 {
		errs.Add("vnc.quality", "quality must be between 0 and 9")
	}

	if cfg.SSHTunnel {
		if err := ValidateHost(cfg.SSHHost); err != nil {
			errs.Add("vnc.sshHost", err.(*ValidationError).Message)
		}
		if err := ValidatePort(cfg.SSHPort); err != nil {
			errs.Add("vnc.sshPort", err.(*ValidationError).Message)
		}
		if strings.TrimSpace(cfg.SSHUsername) == "" {
			errs.Add("vnc.sshUsername", "SSH username is required when tunnelling")
		} else if err := ValidateUsername(cfg.SSHUsername); err != nil {
			errs.Add("vnc.sshUsername", err.(*ValidationError).Message)
		}
		if cfg.SSHAuthMethod != "password" && cfg.SSHAuthMethod != "publickey" {
			errs.Add("vnc.sshAuthMethod", fmt.Sprintf("unsupported SSH auth method %q", cfg.SSHAuthMethod))
		}
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// ValidateIRCConfig validates IRC connection settings.
func ValidateIRCConfig(cfg *models.IRCConfig) error {
	errs := &ValidationErrors{}

	if cfg == nil {
		return &ValidationError{Field: "irc", Message: "IRC configuration must not be nil"}
	}

	nick := strings.TrimSpace(cfg.Nick)
	if nick == "" {
		errs.Add("irc.nick", "nickname is required")
	} else if !girc.IsValidNick(nick) {
		errs.Add("irc.nick", "nickname is not valid for IRC")
	}

	if strings.ContainsAny(cfg.RealName, "\r\n\x00") {
		errs.Add("irc.realName", "real name must not contain control characters")
	}

	if !isValidSecretMode(cfg.ServerPasswordMode) {
		errs.Add("irc.serverPasswordMode", fmt.Sprintf("unsupported secret mode %q", cfg.ServerPasswordMode))
	}
	if !isValidSecretMode(cfg.NickServMode) {
		errs.Add("irc.nickServMode", fmt.Sprintf("unsupported secret mode %q", cfg.NickServMode))
	}
	if cfg.NickServIdentify && !isValidNickServAccount(cfg.NickServAccount) {
		errs.Add("irc.nickServAccount", "NickServ account must not contain control characters")
	}

	seenRooms := map[string]bool{}
	for i, room := range cfg.AutoJoinRooms {
		field := fmt.Sprintf("irc.autoJoinRooms[%d]", i)
		name := strings.TrimSpace(room)
		if name == "" {
			errs.Add(field, "room name must not be empty")
			continue
		}
		if !girc.IsValidChannel(name) {
			errs.Add(field, "room name must start with a valid IRC channel prefix")
		}
		key := strings.ToLower(name)
		if seenRooms[key] {
			errs.Add(field, "room is listed more than once")
		}
		seenRooms[key] = true
	}

	if cfg.SSHTunnel {
		if err := ValidateHost(cfg.SSHHost); err != nil {
			errs.Add("irc.sshHost", err.(*ValidationError).Message)
		}
		if err := ValidatePort(cfg.SSHPort); err != nil {
			errs.Add("irc.sshPort", err.(*ValidationError).Message)
		}
		if strings.TrimSpace(cfg.SSHUsername) == "" {
			errs.Add("irc.sshUsername", "SSH username is required when tunnelling")
		} else if err := ValidateUsername(cfg.SSHUsername); err != nil {
			errs.Add("irc.sshUsername", err.(*ValidationError).Message)
		}
		if cfg.SSHAuthMethod != "password" && cfg.SSHAuthMethod != "publickey" {
			errs.Add("irc.sshAuthMethod", fmt.Sprintf("unsupported SSH auth method %q", cfg.SSHAuthMethod))
		}
		if !isValidSecretMode(cfg.SSHSecretMode) {
			errs.Add("irc.sshSecretMode", fmt.Sprintf("unsupported secret mode %q", cfg.SSHSecretMode))
		}
	}

	if cfg.MaxReconnectTries < 0 {
		errs.Add("irc.maxReconnectTries", "max reconnect attempts must not be negative")
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

func isValidSecretMode(mode models.SecretMode) bool {
	switch mode {
	case models.SecretModeSaved, models.SecretModeSessionOnly, models.SecretModeAskEveryTime:
		return true
	default:
		return false
	}
}

func isValidNickServAccount(account string) bool {
	for _, r := range account {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
