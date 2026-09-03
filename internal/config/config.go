// Package config resolves platform-specific application paths and configuration.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

const appName = "AlchementaryLink"

// AppConfig holds the resolved application configuration paths.
type AppConfig struct {
	DataDir    string // base data directory
	DBPath     string // SQLite database path
	SecretPath string // encrypted vault directory
	LogPath    string // log file path
}

// Load resolves the application configuration.
// Data directory follows platform conventions:
//   - macOS: ~/Library/Application Support/AlchementaryLink
//   - Linux: ~/.config/AlchementaryLink
//   - Windows: %APPDATA%/AlchementaryLink
func Load() (*AppConfig, error) {
	dataDir, err := resolveDataDir()
	if err != nil {
		return nil, err
	}

	cfg := &AppConfig{
		DataDir:    dataDir,
		DBPath:     filepath.Join(dataDir, "link.db"),
		SecretPath: filepath.Join(dataDir, "vault"),
		LogPath:    filepath.Join(dataDir, "link.log"),
	}
	return cfg, nil
}

// EnsureDirectories creates required directories if they don't exist.
func (c *AppConfig) EnsureDirectories() error {
	dirs := []string{
		c.DataDir,
		c.SecretPath,
		filepath.Dir(c.LogPath),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0700); err != nil {
			return err
		}
	}
	return nil
}

// resolveDataDir determines the base data directory for the current platform.
func resolveDataDir() (string, error) {
	var base string

	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "Library", "Application Support")
	case "windows":
		base = os.Getenv("APPDATA")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, "AppData", "Roaming")
		}
	default: // Linux and other UNIX-likes
		base = os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".config")
		}
	}

	return filepath.Join(base, appName), nil
}
