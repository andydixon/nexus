//! mIRC formatting (^B ^] ^^ ^_ ^V ^Q ^O ^C fg,bg ^D hex) to styled spans, the nick colour hash
//! and the IRC event line layout. Ported from the Wails frontend's `irc.js`.
//!
//! Colours are xterm-256 palette indices: IRC colour codes are mapped through `IRC_COLOURS`.

use crate::clients::irc::Event;

/// IRC colours 00-98 mapped to the xterm 256-colour palette (modern IRC formatting spec).
/// Colour 99, and anything out of range, means the terminal default.
pub const IRC_COLOURS: [u8; 99] = [
    15, 0, 4, 2, 9, 1, 5, 130, 11, 10, 6, 14, 12, 13, 8, 7, //
    52, 94, 100, 58, 22, 29, 23, 24, 17, 54, 53, 89, //
    88, 130, 142, 64, 28, 35, 30, 25, 18, 91, 90, 125, //
    124, 166, 184, 106, 34, 49, 37, 33, 19, 129, 127, 161, //
    196, 208, 226, 154, 46, 86, 51, 75, 21, 171, 201, 198, //
    203, 215, 227, 191, 83, 122, 87, 111, 63, 177, 207, 205, //
    217, 223, 229, 193, 157, 158, 159, 153, 147, 183, 219, 212, //
    16, 233, 235, 237, 239, 241, 244, 247, 250, 254, 231,
];

/// Palette indices used for nicks.
pub const NICK_COLOURS: [u8; 12] = [33, 39, 45, 69, 75, 81, 111, 141, 177, 207, 214, 220];

const GREY: u8 = 244;
const RED: u8 = 203;
const GREEN: u8 = 83;
const YELLOW: u8 = 221;
const CYAN: u8 = 81;
const MAGENTA: u8 = 177;

/// Text style. `fg`/`bg` are xterm-256 indices; `fg_rgb`/`bg_rgb` come from ^D hex colours.
/// At most one of `fg`/`fg_rgb` (and of `bg`/`bg_rgb`) is set. `None` is the default colour.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Style {
    pub fg: Option<u8>,
    pub bg: Option<u8>,
    pub fg_rgb: Option<[u8; 3]>,
    pub bg_rgb: Option<[u8; 3]>,
    pub bold: bool,
    pub dim: bool,
    pub italic: bool,
    pub underline: bool,
    pub reverse: bool,
    pub strike: bool,
    pub monospace: bool,
}

impl Style {
    fn fg(colour: u8) -> Style {
        Style { fg: Some(colour), ..Style::default() }
    }
    fn set_fg(&mut self, colour: u8) {
        self.fg = Some(colour);
        self.fg_rgb = None;
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Span {
    pub text: String,
    pub style: Style,
}

/// Spans under construction; `style` applies to the next text pushed.
#[derive(Default)]
struct Line {
    spans: Vec<Span>,
    style: Style,
}

impl Line {
    fn push(&mut self, text: &str) {
        if text.is_empty() {
            return;
        }
        match self.spans.last_mut() {
            Some(last) if last.style == self.style => last.text.push_str(text),
            _ => self.spans.push(Span { text: text.to_string(), style: self.style }),
        }
    }

    /// `text` in `style`, then back to the default style.
    fn styled(&mut self, style: Style, text: &str) {
        self.style = style;
        self.push(text);
        self.style = Style::default();
    }

    /// A bold, nick-coloured name layered on the current style, then reset.
    fn nick(&mut self, nick: &str) {
        let mut style = self.style;
        style.bold = true;
        style.set_fg(nick_colour(nick));
        self.styled(style, nick);
    }

    /// Applies mIRC formatting codes in `value` on top of the current style.
    fn mirc(&mut self, value: &str) {
        let input: Vec<char> = strip_terminal_escapes(value).chars().collect();
        // Toggle states start off regardless of the inherited style, as in irc.js.
        let (mut bold, mut italic, mut strike, mut underline, mut reverse, mut mono) = (false, false, false, false, false, false);
        let mut i = 0;
        while i < input.len() {
            let c = input[i];
            i += 1;
            match c {
                '\x02' => {
                    bold = !bold;
                    self.style.bold = bold;
                    if !bold {
                        self.style.dim = false; // SGR 22 clears both
                    }
                }
                '\x1d' => {
                    italic = !italic;
                    self.style.italic = italic;
                }
                '\x1e' => {
                    strike = !strike;
                    self.style.strike = strike;
                }
                '\x1f' => {
                    underline = !underline;
                    self.style.underline = underline;
                }
                '\x16' => {
                    reverse = !reverse;
                    self.style.reverse = reverse;
                }
                '\x11' => {
                    mono = !mono;
                    self.style.monospace = mono;
                }
                '\x0f' => {
                    self.style = Style::default();
                    (bold, italic, strike, underline, reverse, mono) = (false, false, false, false, false, false);
                }
                '\x03' | '\x04' => {
                    let hex = c == '\x04';
                    let read = |at: usize| if hex { read_hex(&input, at) } else { read_decimal(&input, at) };
                    let Some((fg, len)) = read(i) else {
                        self.reset_colours();
                        continue;
                    };
                    i += len;
                    self.apply_colour(fg, false);
                    if input.get(i) != Some(&',') {
                        continue;
                    }
                    match read(i + 1) {
                        Some((bg, len)) => {
                            i += len + 1;
                            self.apply_colour(bg, true);
                        }
                        None => {
                            self.push(",");
                            i += 1;
                        }
                    }
                }
                c if (c as u32) < 0x20 || c as u32 == 0x7f => {}
                c => self.push(c.encode_utf8(&mut [0; 4])),
            }
        }
    }

    fn reset_colours(&mut self) {
        (self.style.fg, self.style.bg, self.style.fg_rgb, self.style.bg_rgb) = (None, None, None, None);
    }

    fn apply_colour(&mut self, colour: Colour, background: bool) {
        let s = &mut self.style;
        let (index, rgb) = if background { (&mut s.bg, &mut s.bg_rgb) } else { (&mut s.fg, &mut s.fg_rgb) };
        match colour {
            Colour::Indexed(code) => {
                *index = IRC_COLOURS.get(code as usize).copied();
                *rgb = None;
            }
            Colour::Rgb(value) => {
                *rgb = Some(value);
                *index = None;
            }
        }
    }
}

enum Colour {
    Indexed(u8),
    Rgb([u8; 3]),
}

fn read_decimal(input: &[char], at: usize) -> Option<(Colour, usize)> {
    let digits: String = input.iter().skip(at).take(2).take_while(|c| c.is_ascii_digit()).collect();
    if digits.is_empty() {
        return None;
    }
    Some((Colour::Indexed(digits.parse().ok()?), digits.len()))
}

fn read_hex(input: &[char], at: usize) -> Option<(Colour, usize)> {
    let hex: String = input.get(at..at + 6)?.iter().collect();
    if !hex.chars().all(|c| c.is_ascii_hexdigit()) {
        return None;
    }
    let byte = |i: usize| u8::from_str_radix(&hex[i..i + 2], 16).ok();
    Some((Colour::Rgb([byte(0)?, byte(2)?, byte(4)?]), 6))
}

/// Removes untrusted terminal sequences (OSC, CSI, other ESC pairs) and C1 controls, applying the
/// same four passes as irc.js.
fn strip_terminal_escapes(value: &str) -> String {
    // OSC: ESC ] ... (BEL | ESC \), greedy like the JS regex.
    let mut s = String::new();
    let mut rest = value;
    while let Some(pos) = rest.find("\x1b]") {
        let body = &rest[pos + 2..];
        let run_end = body.find('\x07');
        let end = match run_end {
            Some(bel) => Some(bel + 1),
            None => body.rfind("\x1b\\").map(|st| st + 2),
        };
        match end {
            Some(end) => {
                s.push_str(&rest[..pos]);
                rest = &body[end..];
            }
            // No terminator anywhere after this OSC, so none after any later OSC either:
            // keep the rest as is. (Advancing two bytes and searching again was quadratic, and
            // one hostile 1 MiB line of "\x1b]" froze the UI for minutes.)
            None => break,
        }
    }
    s.push_str(rest);

    // CSI: ESC [ params intermediates final.
    let chars: Vec<char> = s.chars().collect();
    let mut csi = String::new();
    let mut i = 0;
    while i < chars.len() {
        if chars[i] == '\x1b' && chars.get(i + 1) == Some(&'[') {
            let mut j = i + 2;
            while chars.get(j).is_some_and(|c| ('0'..='?').contains(c)) {
                j += 1;
            }
            while chars.get(j).is_some_and(|c| (' '..='/').contains(c)) {
                j += 1;
            }
            if chars.get(j).is_some_and(|c| ('@'..='~').contains(c)) {
                i = j + 1;
                continue;
            }
        }
        csi.push(chars[i]);
        i += 1;
    }

    // ESC plus any character but a line terminator.
    let chars: Vec<char> = csi.chars().collect();
    let mut out = String::new();
    let mut i = 0;
    while i < chars.len() {
        if chars[i] == '\x1b' && chars.get(i + 1).is_some_and(|c| !matches!(c, '\n' | '\r' | '\u{2028}' | '\u{2029}')) {
            i += 2;
            continue;
        }
        out.push(chars[i]);
        i += 1;
    }
    out.retain(|c| !('\u{80}'..='\u{9f}').contains(&c));
    out
}

/// mIRC-formatted text as styled spans, starting from the default style.
pub fn format_text(value: &str) -> Vec<Span> {
    let mut line = Line::default();
    line.mirc(value);
    line.spans
}

/// The text with all formatting removed.
pub fn plain_text(value: &str) -> String {
    format_text(value).into_iter().map(|s| s.text).collect()
}

/// The palette index for a nick (Java-style string hash over code points).
pub fn nick_colour(nick: &str) -> u8 {
    let hash = nick.chars().fold(0u32, |h, c| h.wrapping_mul(31).wrapping_add(c as u32));
    NICK_COLOURS[hash as usize % NICK_COLOURS.len()]
}

/// One chat line for an event: `[time] body`, laid out per event type as the Wails viewer did.
pub fn format_event(event: &Event, time: &str) -> Vec<Span> {
    let from = plain_text(&event.from);
    let subject = plain_text(&event.command);
    let mut l = Line::default();
    l.styled(Style { dim: true, ..Style::fg(GREY) }, &format!("[{time}]"));
    l.push(" ");

    let nick_or = |l: &mut Line, fallback: &str| {
        if from.is_empty() { l.push(fallback) } else { l.nick(&from) }
    };
    let suffix = |l: &mut Line| {
        if !event.text.is_empty() {
            l.push(" (");
            l.mirc(&event.text);
            l.push(")");
        }
    };
    let magenta_italic = Style { italic: true, ..Style::fg(MAGENTA) };

    match event.kind.as_str() {
        "message" => {
            l.styled(Style::fg(GREY), "<");
            nick_or(&mut l, "");
            l.styled(Style::fg(GREY), ">");
            l.push(" ");
            l.mirc(&event.text);
        }
        "action" => {
            l.style = magenta_italic;
            l.push("* ");
            nick_or(&mut l, "");
            l.style = magenta_italic;
            l.push(" ");
            l.mirc(&event.text);
        }
        "notice" => {
            l.style = Style::fg(YELLOW);
            l.push("-");
            nick_or(&mut l, "notice");
            l.styled(Style::fg(YELLOW), "-");
            l.push(" ");
            l.mirc(&event.text);
        }
        "join" => {
            l.styled(Style::fg(GREEN), "-->");
            l.push(" ");
            nick_or(&mut l, "user");
            l.push(" joined");
        }
        "part" | "quit" => {
            l.styled(Style::fg(RED), "<--");
            l.push(" ");
            nick_or(&mut l, "user");
            l.push(if event.kind == "part" { " left" } else { " quit" });
            suffix(&mut l);
        }
        "kick" => {
            l.styled(Style::fg(RED), "<--");
            l.push(" ");
            l.styled(Style { bold: true, ..Style::default() }, if subject.is_empty() { "user" } else { &subject });
            l.push(" was kicked by ");
            nick_or(&mut l, "server");
            suffix(&mut l);
        }
        "nick" => {
            nick_or(&mut l, "user");
            l.push(" is now known as ");
            l.styled(Style { bold: true, ..Style::default() }, &plain_text(&event.text));
        }
        "topic" | "error" => {
            let (colour, label) = if event.kind == "topic" { (CYAN, "topic:") } else { (RED, "error:") };
            l.styled(Style { bold: true, ..Style::fg(colour) }, label);
            l.push(" ");
            l.mirc(&event.text);
        }
        _ => {
            l.style = Style::fg(CYAN);
            l.mirc(&event.text);
        }
    }
    l.spans
}

#[cfg(test)]
mod tests {
    use super::*;

    fn span(text: &str, style: Style) -> Span {
        Span { text: text.into(), style }
    }

    #[test]
    fn toggles() {
        let b = Style { bold: true, ..Style::default() };
        let i = Style { italic: true, ..Style::default() };
        let u = Style { underline: true, ..Style::default() };
        let d = Style::default();
        assert_eq!(
            format_text("plain \x02bold\x02 \x1ditalic\x1d \x1funderlined\x1f"),
            vec![span("plain ", d), span("bold", b), span(" ", d), span("italic", i), span(" ", d), span("underlined", u)]
        );
        let all = format_text("\x16\x1e\x11x\x0fy");
        assert_eq!(all[0].style, Style { reverse: true, strike: true, monospace: true, ..Style::default() });
        assert_eq!(all[1], span("y", d));
    }

    #[test]
    fn indexed_colours() {
        assert_eq!(format_text("\x0304red\x03 default"), vec![span("red", Style::fg(9)), span(" default", Style::default())]);
        assert_eq!(
            format_text("\x0352extended \x0303,01matrix"),
            vec![span("extended ", Style::fg(196)), span("matrix", Style { bg: Some(0), ..Style::fg(2) })]
        );
        assert_eq!(format_text("\x0399default"), vec![span("default", Style::default())]);
    }

    #[test]
    fn hex_colours() {
        assert_eq!(
            format_text("\x04ff8800orange\x04 default"),
            vec![span("orange", Style { fg_rgb: Some([255, 136, 0]), ..Style::default() }), span(" default", Style::default())]
        );
        let s = format_text("\x0400ff00,0000FFx");
        assert_eq!(s[0].style, Style { fg_rgb: Some([0, 255, 0]), bg_rgb: Some([0, 0, 255]), ..Style::default() });
    }

    #[test]
    fn colour_without_background_keeps_comma() {
        assert_eq!(format_text("\x0304,comma"), vec![span(",comma", Style::fg(9))]);
    }

    #[test]
    fn plain_text_strips_formatting() {
        assert_eq!(plain_text("\x02bold\x02 \x0304red\x03"), "bold red");
    }

    #[test]
    fn unterminated_osc_runs_are_linear() {
        let start = std::time::Instant::now();
        let out = plain_text(&"\x1b]".repeat(500_000));
        assert!(start.elapsed() < std::time::Duration::from_secs(2), "{:?}", start.elapsed());
        assert!(!out.contains('\x1b'));
    }

    #[test]
    fn untrusted_terminal_sequences_are_removed() {
        assert_eq!(plain_text("before\x1b[31munsafe\x1b]0;title\x07after"), "beforeunsafeafter");
        assert_eq!(plain_text("a\x1b]8;;x\x1b\\b\u{9b}c\x1bZd\x01"), "abcd");
    }

    #[test]
    fn nick_colour_matches_js_hash() {
        // Hashes computed with the irc.js function under node.
        assert_eq!(nick_colour(""), 33);
        assert_eq!(nick_colour("bob"), NICK_COLOURS[97717 % 12]);
        assert_eq!(nick_colour("alice"), NICK_COLOURS[92903040 % 12]);
    }

    #[test]
    fn event_lines() {
        let message =
            format_event(&Event { kind: "message".into(), from: "alice".into(), text: "hello \x02world\x02".into(), ..Default::default() }, "12:34:56");
        assert_eq!(message[0], span("[12:34:56]", Style { dim: true, ..Style::fg(GREY) }));
        assert!(message.iter().any(|s| s.text == "world" && s.style.bold));
        let nick = message.iter().find(|s| s.text == "alice").unwrap();
        assert_eq!(nick.style, Style { bold: true, ..Style::fg(nick_colour("alice")) });
        let text: String = message.iter().map(|s| s.text.as_str()).collect();
        assert_eq!(text, "[12:34:56] <alice> hello world");

        let action = format_event(&Event { kind: "action".into(), from: "bob".into(), text: "waves".into(), ..Default::default() }, "01:02:03");
        assert!(action.iter().any(|s| s.text.ends_with("waves") && s.style.italic && s.style.fg == Some(MAGENTA)));

        let kick = format_event(&Event { kind: "kick".into(), from: "op".into(), command: "eve".into(), text: "spam".into(), ..Default::default() }, "t");
        assert_eq!(kick.iter().map(|s| s.text.as_str()).collect::<String>(), "[t] <-- eve was kicked by op (spam)");
        let system = format_event(&Event { kind: "system".into(), text: "hi".into(), ..Default::default() }, "t");
        assert_eq!(system.last().unwrap(), &span("hi", Style::fg(CYAN)));
    }
}
