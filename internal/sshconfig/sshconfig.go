// Package sshconfig parses OpenSSH ~/.ssh/config files and converts
// Host blocks into Nexus Terminal connection profiles. Supports a useful
// subset of SSH config directives: HostName, Port, User, IdentityFile,
// ProxyJump, ForwardAgent, and Compression.
//
// Limitations (future work):
//   - Match blocks are ignored
//   - Wildcards in Host patterns are skipped
//   - Include directives are not followed
//   - Token expansion (%h, %p, etc.) is not performed
package sshconfig

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/copilot-ssh-client/app/internal/models"
	"github.com/google/uuid"
)

// HostBlock represents a parsed SSH config Host section.
type HostBlock struct {
	Aliases       []string
	HostName      string
	Port          int
	User          string
	IdentityFile  string
	ProxyJump     string
	ForwardAgent  bool
	Compression   bool
}

// ParseFile reads and parses an OpenSSH config file.
func ParseFile(path string) ([]HostBlock, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sshconfig: failed to open %s: %w", path, err)
	}
	defer f.Close()

	var blocks []HostBlock
	var current *HostBlock

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split into directive and value.
		key, value := splitDirective(line)
		key = strings.ToLower(key)

		switch key {
		case "host":
			// Commit previous block.
			if current != nil {
				blocks = append(blocks, *current)
			}
			aliases := strings.Fields(value)
			// Skip wildcard-only hosts (e.g. "Host *").
			hasWildcard := false
			for _, a := range aliases {
				if strings.Contains(a, "*") || strings.Contains(a, "?") {
					hasWildcard = true
				}
			}
			if hasWildcard && len(aliases) == 1 {
				current = nil
				continue
			}
			current = &HostBlock{
				Aliases: aliases,
				Port:    22,
			}

		case "match":
			// Commit previous block and skip Match blocks.
			if current != nil {
				blocks = append(blocks, *current)
			}
			current = nil

		case "hostname":
			if current != nil {
				current.HostName = value
			}
		case "port":
			if current != nil {
				if p, err := strconv.Atoi(value); err == nil {
					current.Port = p
				}
			}
		case "user":
			if current != nil {
				current.User = value
			}
		case "identityfile":
			if current != nil {
				current.IdentityFile = expandTilde(value)
			}
		case "proxyjump":
			if current != nil {
				current.ProxyJump = value
			}
		case "forwardagent":
			if current != nil {
				current.ForwardAgent = isTrueish(value)
			}
		case "compression":
			if current != nil {
				current.Compression = isTrueish(value)
			}
		}
	}

	// Commit final block.
	if current != nil {
		blocks = append(blocks, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sshconfig: read error: %w", err)
	}

	return blocks, nil
}

// DefaultConfigPath returns the standard OpenSSH config file path.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// ToProfiles converts parsed Host blocks into Nexus Terminal profiles.
// Each block produces one profile; the first alias becomes the display name.
func ToProfiles(blocks []HostBlock, folder string) []models.Profile {
	profiles := make([]models.Profile, 0, len(blocks))

	for _, b := range blocks {
		if len(b.Aliases) == 0 {
			continue
		}

		host := b.HostName
		if host == "" {
			host = b.Aliases[0]
		}

		displayName := b.Aliases[0]
		username := b.User

		sshConf := &models.SSHConfig{
			AuthMethods:   []string{"publickey", "password"},
			SecretMode:    models.SecretModeAskEveryTime,
			HostKeyPolicy: models.HostKeyAsk,
			TerminalType:  "xterm-256color",
			AgentAuth:     b.ForwardAgent,
			AgentForwarding: b.ForwardAgent,
			Compression:   b.Compression,
		}

		if b.IdentityFile != "" {
			sshConf.PrivateKeyPath = b.IdentityFile
		}

		// Parse ProxyJump into a jump host.
		if b.ProxyJump != "" && b.ProxyJump != "none" {
			jh := parseProxyJump(b.ProxyJump)
			if jh != nil {
				sshConf.JumpHosts = []models.JumpHost{*jh}
			}
		}

		profile := models.NewProfile(models.ProtocolSSH)
		profile.ID = uuid.New().String()
		profile.DisplayName = displayName
		profile.Host = host
		profile.Port = b.Port
		profile.Username = username
		profile.Folder = folder
		profile.SSH = sshConf
		profile.Tags = []string{"ssh-config-import"}

		profiles = append(profiles, profile)
	}

	return profiles
}

// parseProxyJump parses a simple ProxyJump value (user@host:port format).
// Only the first hop is parsed; chained jumps are future work.
func parseProxyJump(value string) *models.JumpHost {
	// Take only the first hop if comma-separated.
	if idx := strings.Index(value, ","); idx >= 0 {
		value = value[:idx]
	}
	value = strings.TrimSpace(value)
	if value == "" || value == "none" {
		return nil
	}

	jh := &models.JumpHost{
		Port:       22,
		AuthMethod: "publickey",
		SecretMode: models.SecretModeAskEveryTime,
	}

	// Extract user@ prefix.
	if at := strings.Index(value, "@"); at >= 0 {
		jh.Username = value[:at]
		value = value[at+1:]
	}

	// Extract :port suffix.
	if colon := strings.LastIndex(value, ":"); colon >= 0 {
		if p, err := strconv.Atoi(value[colon+1:]); err == nil {
			jh.Port = p
			value = value[:colon]
		}
	}

	jh.Host = value
	return jh
}

// splitDirective splits a line into key and value, handling both
// "Key Value" and "Key=Value" formats.
func splitDirective(line string) (string, string) {
	// Try = separator first.
	if idx := strings.Index(line, "="); idx >= 0 {
		return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
	}
	// Fall back to whitespace separator.
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 1 {
		parts = strings.SplitN(line, "\t", 2)
	}
	if len(parts) < 2 {
		return parts[0], ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// isTrueish returns true for "yes", "true", "on", and "1".
func isTrueish(val string) bool {
	switch strings.ToLower(val) {
	case "yes", "true", "on", "1":
		return true
	}
	return false
}
