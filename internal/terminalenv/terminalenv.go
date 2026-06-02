// Package terminalenv prepares environment variables for terminal sessions.
package terminalenv

import (
	"runtime"
	"sort"
	"strings"
)

const (
	envLANG     = "LANG"
	envLCAll    = "LC_ALL"
	envLCType   = "LC_CTYPE"
	envTerm     = "TERM"
	defaultTerm = "xterm-256color"
)

// LocalShellEnv returns an environment suitable for a local interactive PTY.
func LocalShellEnv(env []string) []string {
	env = setEnv(env, envTerm, defaultTerm)
	return ensureUTF8CharacterLocale(env, runtime.GOOS)
}

// SSHEnvVars returns environment variables to request for an SSH session.
// Profile locale values are preserved; otherwise a UTF-8 character locale is
// requested by default.
func SSHEnvVars(profileEnv map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range profileEnv {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		result[key] = value
	}

	if hasAnyLocaleMap(result) {
		return result
	}
	result[envLCType] = DefaultUTF8Locale(runtime.GOOS)
	return result
}

// SortedEnvKeys returns stable keys for deterministic environment requests.
func SortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// DefaultUTF8Locale returns a locale name broadly available on the target OS.
func DefaultUTF8Locale(goos string) string {
	if goos == "darwin" {
		return "en_US.UTF-8"
	}
	return "C.UTF-8"
}

func ensureUTF8CharacterLocale(env []string, goos string) []string {
	if hasEffectiveUTF8Locale(env) {
		return env
	}

	locale := DefaultUTF8Locale(goos)
	if hasEnv(env, envLCAll) {
		return setEnv(env, envLCAll, locale)
	}
	if hasEnv(env, envLCType) {
		return setEnv(env, envLCType, locale)
	}
	if hasEnv(env, envLANG) {
		return setEnv(env, envLCType, locale)
	}
	return setEnv(env, envLANG, locale)
}

func hasEffectiveUTF8Locale(env []string) bool {
	values := envToMap(env)
	if value, ok := values[envLCAll]; ok {
		return isUTF8Locale(value)
	}
	if value, ok := values[envLCType]; ok {
		return isUTF8Locale(value)
	}
	if value, ok := values[envLANG]; ok {
		return isUTF8Locale(value)
	}
	return false
}

func hasAnyLocaleMap(env map[string]string) bool {
	_, hasLCAll := env[envLCAll]
	_, hasLCType := env[envLCType]
	_, hasLANG := env[envLANG]
	return hasLCAll || hasLCType || hasLANG
}

func isUTF8Locale(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(value, "-", ""))
	return strings.Contains(normalized, "utf8")
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		result = append(result, item)
	}
	return append(result, prefix+value)
}

func hasEnv(env []string, key string) bool {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
}

func envToMap(env []string) map[string]string {
	result := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}
