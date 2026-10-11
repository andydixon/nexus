//! Environment variables for terminal sessions: TERM and a UTF-8 character locale.

use std::collections::BTreeMap;

const LANG: &str = "LANG";
const LC_ALL: &str = "LC_ALL";
const LC_CTYPE: &str = "LC_CTYPE";
const TERM: &str = "TERM";
const DEFAULT_TERM: &str = "xterm-256color";

/// `KEY=VALUE` entries for a local interactive PTY.
pub fn local_shell_env(env: Vec<String>) -> Vec<String> {
    ensure_utf8_character_locale(set_env(env, TERM, DEFAULT_TERM), std::env::consts::OS)
}

/// Variables to request for an SSH session. A locale set by the profile is kept as is; otherwise
/// a UTF-8 `LC_CTYPE` is added. Keys are trimmed and empty keys dropped. Iteration order is sorted.
pub fn ssh_env_vars(profile_env: &BTreeMap<String, String>) -> BTreeMap<String, String> {
    let mut out: BTreeMap<String, String> = profile_env.iter().map(|(k, v)| (k.trim().to_string(), v.clone())).filter(|(k, _)| !k.is_empty()).collect();
    if ![LC_ALL, LC_CTYPE, LANG].iter().any(|k| out.contains_key(*k)) {
        out.insert(LC_CTYPE.into(), default_utf8_locale(std::env::consts::OS).into());
    }
    out
}

/// A UTF-8 locale broadly available on `os` (Go's `darwin` or Rust's `macos` get `en_US.UTF-8`).
pub fn default_utf8_locale(os: &str) -> &'static str {
    if os == "darwin" || os == "macos" { "en_US.UTF-8" } else { "C.UTF-8" }
}

fn ensure_utf8_character_locale(env: Vec<String>, os: &str) -> Vec<String> {
    // The first of LC_ALL, LC_CTYPE, LANG that is set decides the character locale.
    let effective = [LC_ALL, LC_CTYPE, LANG].iter().find_map(|k| get_env(&env, k));
    if effective.is_some_and(is_utf8_locale) {
        return env;
    }
    let locale = default_utf8_locale(os);
    if get_env(&env, LC_ALL).is_some() {
        set_env(env, LC_ALL, locale)
    } else if get_env(&env, LC_CTYPE).is_some() || get_env(&env, LANG).is_some() {
        set_env(env, LC_CTYPE, locale)
    } else {
        set_env(env, LANG, locale)
    }
}

fn is_utf8_locale(v: &str) -> bool {
    v.replace('-', "").to_lowercase().contains("utf8")
}

fn get_env<'a>(env: &'a [String], key: &str) -> Option<&'a str> {
    env.iter().rev().find_map(|e| e.strip_prefix(key)?.strip_prefix('='))
}

fn set_env(env: Vec<String>, key: &str, value: &str) -> Vec<String> {
    let mut out: Vec<String> = env.into_iter().filter(|e| get_env(std::slice::from_ref(e), key).is_none()).collect();
    out.push(format!("{key}={value}"));
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn s(v: &[&str]) -> Vec<String> {
        v.iter().map(|x| x.to_string()).collect()
    }

    #[test]
    fn local_env_sets_term_and_utf8_when_missing() {
        let got = local_shell_env(s(&["SHELL=/bin/zsh"]));
        assert_eq!(get_env(&got, TERM), Some(DEFAULT_TERM));
        assert!(get_env(&got, LANG).is_some_and(is_utf8_locale));
    }

    #[test]
    fn local_env_uses_lc_ctype_when_lang_is_not_utf8() {
        let got = ensure_utf8_character_locale(s(&["LANG=C"]), "linux");
        assert_eq!(get_env(&got, LANG), Some("C"));
        assert_eq!(get_env(&got, LC_CTYPE), Some("C.UTF-8"));
    }

    #[test]
    fn local_env_overrides_non_utf8_lc_all() {
        let got = ensure_utf8_character_locale(s(&["LC_ALL=C", "LANG=C"]), "darwin");
        assert_eq!(get_env(&got, LC_ALL), Some("en_US.UTF-8"));
    }

    #[test]
    fn ssh_env_preserves_profile_locale() {
        let got = ssh_env_vars(&BTreeMap::from([("LANG".into(), "de_DE.UTF-8".into()), ("FOO".into(), "bar".into())]));
        assert_eq!((got["LANG"].as_str(), got["FOO"].as_str()), ("de_DE.UTF-8", "bar"));
        assert!(!got.contains_key(LC_CTYPE));
        let got = ssh_env_vars(&BTreeMap::from([("LANG".into(), "C".into())]));
        assert_eq!(got["LANG"], "C");
        assert!(!got.contains_key(LC_CTYPE));
    }

    #[test]
    fn ssh_env_adds_utf8_lc_ctype_by_default() {
        let got = ssh_env_vars(&BTreeMap::from([("FOO".into(), "bar".into())]));
        assert_eq!(got["FOO"], "bar");
        assert!(is_utf8_locale(&got[LC_CTYPE]));
    }
}
