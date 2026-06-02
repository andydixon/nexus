package terminalenv

import "testing"

func TestLocalShellEnvSetsTermAndUTF8WhenMissing(t *testing.T) {
	got := LocalShellEnv([]string{"SHELL=/bin/zsh"})

	if value := envValue(got, envTerm); value != defaultTerm {
		t.Fatalf("TERM = %q, want %q", value, defaultTerm)
	}
	if value := envValue(got, envLANG); value == "" || !isUTF8Locale(value) {
		t.Fatalf("LANG = %q, want UTF-8 locale", value)
	}
}

func TestLocalShellEnvUsesLCTypeWhenLangIsNonUTF8(t *testing.T) {
	got := ensureUTF8CharacterLocale([]string{"LANG=C"}, "linux")

	if value := envValue(got, envLANG); value != "C" {
		t.Fatalf("LANG = %q, want C", value)
	}
	if value := envValue(got, envLCType); value != "C.UTF-8" {
		t.Fatalf("LC_CTYPE = %q, want C.UTF-8", value)
	}
}

func TestLocalShellEnvOverridesNonUTF8LCAll(t *testing.T) {
	got := ensureUTF8CharacterLocale([]string{"LC_ALL=C", "LANG=C"}, "darwin")

	if value := envValue(got, envLCAll); value != "en_US.UTF-8" {
		t.Fatalf("LC_ALL = %q, want en_US.UTF-8", value)
	}
}

func TestSSHEnvVarsPreservesProfileUTF8Locale(t *testing.T) {
	got := SSHEnvVars(map[string]string{
		"LANG": "de_DE.UTF-8",
		"FOO":  "bar",
	})

	if got["LANG"] != "de_DE.UTF-8" {
		t.Fatalf("LANG = %q, want profile value", got["LANG"])
	}
	if got["FOO"] != "bar" {
		t.Fatalf("FOO = %q, want bar", got["FOO"])
	}
	if _, ok := got[envLCType]; ok {
		t.Fatalf("unexpected LC_CTYPE default when LANG is already UTF-8")
	}
}

func TestSSHEnvVarsPreservesProfileNonUTF8Locale(t *testing.T) {
	got := SSHEnvVars(map[string]string{
		envLANG: "C",
		"FOO":   "bar",
	})

	if got[envLANG] != "C" {
		t.Fatalf("LANG = %q, want C", got[envLANG])
	}
	if _, ok := got[envLCType]; ok {
		t.Fatalf("unexpected LC_CTYPE default when profile sets locale")
	}
}

func TestSSHEnvVarsAddsUTF8LCTypeByDefault(t *testing.T) {
	got := SSHEnvVars(map[string]string{"FOO": "bar"})

	if got["FOO"] != "bar" {
		t.Fatalf("FOO = %q, want bar", got["FOO"])
	}
	if value := got[envLCType]; value == "" || !isUTF8Locale(value) {
		t.Fatalf("LC_CTYPE = %q, want UTF-8 locale", value)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			return item[len(prefix):]
		}
	}
	return ""
}
