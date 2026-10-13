// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Finds the OSC sequences the VT parser ignores but Nexus uses: OSC 133 (shell integration
//! marks: prompt, command, output, finished with exit status) and OSC 7 (the shell's working
//! directory). Sequences may be split across reads, so the scanner keeps state between chunks.

#[derive(Debug, PartialEq)]
pub enum Osc {
    /// 133;A — a prompt starts.
    Prompt,
    /// 133;C — the command's output starts.
    Output,
    /// 133;D[;status] — the command finished.
    Finished(Option<i32>),
    /// 7;file://host/path
    Cwd { host: String, path: String },
    /// 1337;File=…;inline=1:<base64> (iTerm2 inline image): the decoded file bytes and the
    /// requested width/height arguments (cells, `Npx`, `N%` or `auto`).
    Image { data: Vec<u8>, width: String, height: String },
}

/// Longest OSC body collected; longer ones (not ours) are dropped.
const MAX_OSC: usize = 4096;
/// Inline images are bigger: up to 16 MiB of base64.
const MAX_IMAGE_OSC: usize = 16 << 20;
const IMAGE_PREFIX: &[u8] = b"1337;File=";

fn limit(body: &[u8]) -> usize {
    if body.len() >= IMAGE_PREFIX.len() && body.starts_with(IMAGE_PREFIX) { MAX_IMAGE_OSC } else { MAX_OSC }
}

#[derive(Default)]
enum State {
    #[default]
    Idle,
    Esc,
    Body(Vec<u8>),
    BodyEsc(Vec<u8>),
}

#[derive(Default)]
pub struct OscScanner {
    state: State,
}

impl OscScanner {
    /// The sequences that complete in `bytes`, each with the offset just past its terminator.
    pub fn scan(&mut self, bytes: &[u8]) -> Vec<(usize, Osc)> {
        let mut out = Vec::new();
        let mut i = 0;
        while i < bytes.len() {
            let state = std::mem::take(&mut self.state);
            self.state = match state {
                State::Idle => match bytes[i..].iter().position(|&b| b == 0x1b) {
                    Some(p) => {
                        i += p;
                        State::Esc
                    }
                    None => {
                        i = bytes.len();
                        State::Idle
                    }
                },
                State::Esc if bytes[i] == b']' => State::Body(Vec::new()),
                State::Esc => State::Idle,
                State::Body(mut b) => match bytes[i] {
                    0x07 => {
                        if let Some(osc) = parse(&b) {
                            out.push((i + 1, osc));
                        }
                        State::Idle
                    }
                    0x1b => State::BodyEsc(b),
                    c if b.len() < limit(&b) => {
                        b.push(c);
                        State::Body(b)
                    }
                    _ => State::Idle,
                },
                State::BodyEsc(b) if bytes[i] == b'\\' => {
                    if let Some(osc) = parse(&b) {
                        out.push((i + 1, osc));
                    }
                    State::Idle
                }
                // An ESC that doesn't end the OSC starts a new sequence.
                State::BodyEsc(_) => {
                    // Re-examine this byte as the one after an ESC.
                    self.state = State::Esc;
                    continue;
                }
            };
            i += 1;
        }
        out
    }
}

fn parse(body: &[u8]) -> Option<Osc> {
    let s = std::str::from_utf8(body).ok()?;
    if let Some(rest) = s.strip_prefix("133;") {
        let mut parts = rest.split(';');
        return match parts.next()? {
            "A" => Some(Osc::Prompt),
            "C" => Some(Osc::Output),
            "D" => Some(Osc::Finished(parts.next().and_then(|c| c.parse().ok()))),
            _ => None,
        };
    }
    if let Some(rest) = s.strip_prefix("1337;File=") {
        let (args, data) = rest.split_once(':')?;
        let arg = |k: &str| args.split(';').find_map(|a| a.strip_prefix(k)?.strip_prefix('=')).map(str::to_string);
        if arg("inline").as_deref() != Some("1") {
            return None;
        }
        use base64::Engine;
        let data = base64::engine::general_purpose::STANDARD.decode(data.trim()).ok()?;
        return Some(Osc::Image { data, width: arg("width").unwrap_or_default(), height: arg("height").unwrap_or_default() });
    }
    let url = s.strip_prefix("7;")?.strip_prefix("file://")?;
    let (host, path) = url.split_at(url.find('/')?);
    Some(Osc::Cwd { host: host.into(), path: percent_decode(path) })
}

fn percent_decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        match (b[i], b.get(i + 1..i + 3).and_then(|h| u8::from_str_radix(std::str::from_utf8(h).ok()?, 16).ok())) {
            (b'%', Some(v)) => {
                out.push(v);
                i += 3;
            }
            (c, _) => {
                out.push(c);
                i += 1;
            }
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn finds_marks_and_cwd_even_when_split() {
        let stream = b"hi\x1b]133;A\x07$ ls\x1b]133;C\x1b\\file\x1b]133;D;2\x07\x1b]7;file://box/home/me/My%20Dir\x07\x1b]0;title\x07\x1b[31mred";
        let whole = OscScanner::default().scan(stream);
        let want = [Osc::Prompt, Osc::Output, Osc::Finished(Some(2)), Osc::Cwd { host: "box".into(), path: "/home/me/My Dir".into() }];
        assert_eq!(whole.iter().map(|(_, o)| o).collect::<Vec<_>>(), want.iter().collect::<Vec<_>>());
        assert_eq!(whole[0].0, 10, "offset is just past the terminator");
        // Byte by byte gives the same sequences at the same absolute offsets.
        let mut s = OscScanner::default();
        let mut split = vec![];
        for (i, b) in stream.iter().enumerate() {
            split.extend(s.scan(std::slice::from_ref(b)).into_iter().map(|(end, o)| (i + end, o)));
        }
        assert_eq!(split, whole);
        // Unterminated and oversized bodies don't leak into later output.
        let mut s = OscScanner::default();
        let mut long = b"\x1b]133;A".to_vec();
        long.extend(std::iter::repeat_n(b'x', MAX_OSC + 10));
        long.extend(b"\x07\x1b]133;D\x07");
        assert_eq!(s.scan(&long).into_iter().map(|(_, o)| o).collect::<Vec<_>>(), vec![Osc::Finished(None)]);
    }

    #[test]
    fn inline_images_need_inline_1_and_valid_base64() {
        let img = |args: &str, data: &str| {
            let seq = format!("\x1b]1337;File={args}:{data}\x07");
            OscScanner::default().scan(seq.as_bytes()).into_iter().map(|(_, o)| o).collect::<Vec<_>>()
        };
        assert_eq!(img("name=eA==;width=10;inline=1", "aGk="), vec![Osc::Image { data: b"hi".to_vec(), width: "10".into(), height: String::new() }]);
        assert!(img("inline=0", "aGk=").is_empty(), "downloads (inline=0) are ignored");
        assert!(img("inline=1", "!!!").is_empty());
        // A large image body is collected, past the 4 KiB cap for other OSCs.
        use base64::Engine;
        let big = base64::engine::general_purpose::STANDARD.encode(vec![7u8; 100_000]);
        assert!(matches!(&img("inline=1", &big)[..], [Osc::Image { data, .. }] if data.len() == 100_000));
    }
}
