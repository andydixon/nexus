// Package secrets provides encrypted credential storage and redaction utilities.
package secrets

import "strings"

// RedactSecret replaces a secret string with a redacted placeholder.
// Used for logging and UI display to prevent accidental secret exposure.
func RedactSecret(s string) string {
	if len(s) == 0 {
		return ""
	}
	return "••••••••"
}

// RedactInString replaces all occurrences of a secret within a larger string
// with a redacted placeholder. This is useful for sanitising log output that
// may inadvertently contain sensitive material.
func RedactInString(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "••••••••")
}

// RedactMap returns a shallow copy of the map with every value replaced by
// a redacted placeholder. The original map is not modified.
func RedactMap(m map[string]string) map[string]string {
	redacted := make(map[string]string, len(m))
	for k, v := range m {
		if v == "" {
			redacted[k] = ""
		} else {
			redacted[k] = "••••••••"
		}
	}
	return redacted
}
