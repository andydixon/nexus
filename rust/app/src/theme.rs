//! Design system: colour tokens, radii and typography (Inter for UI, JetBrains Mono for bytes).
//! Widgets read `Tokens::get(ctx)` instead of hard-coding colours, so every theme applies
//! everywhere. Copied from Leviathan Desktop so both apps share one look.

use egui::{Color32, FontData, FontDefinitions, FontFamily, FontId, Stroke, TextStyle, Visuals};
use std::sync::Arc;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ThemeKind {
    /// Charcoal panels, blue accent: a pro-tool dark.
    #[default]
    Pro,
    /// Near-black surfaces, violet accent.
    Midnight,
    Light,
}

impl ThemeKind {
    pub const ALL: [ThemeKind; 3] = [ThemeKind::Pro, ThemeKind::Midnight, ThemeKind::Light];
    pub fn label(self) -> &'static str {
        match self {
            ThemeKind::Pro => "Pro (dark)",
            ThemeKind::Midnight => "Midnight",
            ThemeKind::Light => "Light",
        }
    }
    pub fn id(self) -> &'static str {
        match self {
            ThemeKind::Pro => "pro",
            ThemeKind::Midnight => "midnight",
            ThemeKind::Light => "light",
        }
    }
    pub fn from_id(s: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|k| k.id() == s)
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Tokens {
    pub kind: ThemeKind,
    pub chrome: Color32,
    pub dock: Color32,
    pub card: Color32,
    pub card_border: Color32,
    pub tab_strip: Color32,
    pub field: Color32,
    pub field_border: Color32,
    pub hover: Color32,
    pub row_selected: Color32,
    pub text: Color32,
    pub text_dim: Color32,
    pub text_faint: Color32,
    pub accent: Color32,
    pub accent_soft: Color32,
    pub accent_text: Color32,
    pub danger: Color32,
    pub warning: Color32,
    pub success: Color32,
    pub separator: Color32,
    /// Hex view background and byte-class colours.
    pub canvas: Color32,
    pub byte_null: Color32,
    pub byte_print: Color32,
    pub byte_space: Color32,
    pub byte_ctrl: Color32,
    pub byte_high: Color32,
    pub byte_ff: Color32,
    pub hit: Color32,
    pub bookmark: Color32,
    pub patched: Color32,
    pub radius: f32,
}

impl Tokens {
    pub fn for_kind(kind: ThemeKind) -> Self {
        let rgb = Color32::from_rgb;
        match kind {
            ThemeKind::Pro => Tokens {
                kind,
                chrome: rgb(38, 38, 40),
                dock: rgb(30, 30, 32),
                card: rgb(44, 44, 47),
                card_border: rgb(28, 28, 30),
                tab_strip: rgb(34, 34, 36),
                field: rgb(30, 30, 32),
                field_border: rgb(72, 72, 76),
                hover: rgb(60, 60, 64),
                row_selected: rgb(40, 66, 102),
                text: rgb(226, 226, 230),
                text_dim: rgb(170, 170, 178),
                text_faint: rgb(118, 118, 126),
                accent: rgb(55, 142, 240),
                accent_soft: Color32::from_rgba_unmultiplied(55, 142, 240, 56),
                accent_text: Color32::WHITE,
                danger: rgb(236, 91, 98),
                warning: rgb(232, 176, 70),
                success: rgb(80, 196, 128),
                separator: rgb(24, 24, 26),
                canvas: rgb(26, 26, 28),
                byte_null: rgb(84, 84, 92),
                byte_print: rgb(214, 222, 235),
                byte_space: rgb(120, 190, 160),
                byte_ctrl: rgb(232, 176, 70),
                byte_high: rgb(196, 140, 236),
                byte_ff: rgb(236, 110, 110),
                hit: Color32::from_rgba_unmultiplied(232, 176, 70, 90),
                bookmark: rgb(80, 196, 128),
                patched: rgb(236, 91, 98),
                radius: 4.0,
            },
            ThemeKind::Midnight => Tokens {
                kind,
                chrome: rgb(18, 18, 21),
                dock: rgb(14, 14, 16),
                card: rgb(24, 24, 28),
                card_border: rgb(40, 40, 46),
                tab_strip: rgb(18, 18, 21),
                field: rgb(32, 32, 37),
                field_border: rgb(52, 52, 60),
                hover: rgb(42, 42, 50),
                row_selected: rgb(52, 44, 96),
                text: rgb(236, 236, 242),
                text_dim: rgb(150, 150, 162),
                text_faint: rgb(96, 96, 108),
                accent: rgb(139, 124, 246),
                accent_soft: Color32::from_rgba_unmultiplied(139, 124, 246, 50),
                accent_text: rgb(240, 238, 255),
                separator: rgb(34, 34, 40),
                canvas: rgb(12, 12, 14),
                byte_null: rgb(70, 70, 82),
                byte_print: rgb(220, 224, 240),
                byte_space: rgb(110, 200, 180),
                byte_ctrl: rgb(240, 190, 90),
                byte_high: rgb(170, 150, 255),
                byte_ff: rgb(255, 120, 140),
                hit: Color32::from_rgba_unmultiplied(240, 190, 90, 80),
                bookmark: rgb(110, 220, 150),
                patched: rgb(255, 110, 120),
                ..Tokens::for_kind(ThemeKind::Pro)
            },
            ThemeKind::Light => Tokens {
                kind,
                chrome: rgb(236, 236, 240),
                dock: rgb(226, 226, 231),
                card: rgb(250, 250, 252),
                card_border: rgb(214, 214, 220),
                tab_strip: rgb(232, 232, 236),
                field: rgb(255, 255, 255),
                field_border: rgb(200, 200, 208),
                hover: rgb(222, 222, 230),
                row_selected: rgb(204, 222, 250),
                text: rgb(28, 28, 32),
                text_dim: rgb(84, 84, 96),
                text_faint: rgb(140, 140, 150),
                accent: rgb(30, 110, 220),
                accent_soft: Color32::from_rgba_unmultiplied(30, 110, 220, 40),
                accent_text: Color32::WHITE,
                danger: rgb(200, 50, 56),
                warning: rgb(176, 120, 10),
                success: rgb(30, 140, 80),
                separator: rgb(214, 214, 220),
                canvas: rgb(252, 252, 253),
                byte_null: rgb(180, 180, 190),
                byte_print: rgb(30, 34, 44),
                byte_space: rgb(20, 130, 100),
                byte_ctrl: rgb(176, 110, 0),
                byte_high: rgb(120, 70, 200),
                byte_ff: rgb(200, 50, 56),
                hit: Color32::from_rgba_unmultiplied(240, 180, 40, 110),
                bookmark: rgb(30, 150, 80),
                patched: rgb(210, 40, 50),
                radius: 4.0,
            },
        }
    }

    pub fn get(ctx: &egui::Context) -> Tokens {
        ctx.data(|d| d.get_temp::<Tokens>(egui::Id::new("nexus-theme"))).unwrap_or_else(|| Tokens::for_kind(ThemeKind::Pro))
    }

    pub fn dark(&self) -> bool {
        self.kind != ThemeKind::Light
    }
}

pub fn install_fonts(ctx: &egui::Context) {
    let mut fonts = FontDefinitions::default();
    let mut add = |name: &str, bytes: &'static [u8]| {
        fonts.font_data.insert(name.into(), Arc::new(FontData::from_static(bytes)));
    };
    add("Inter", include_bytes!("../assets/fonts/Inter-Regular.ttf"));
    add("Inter-SemiBold", include_bytes!("../assets/fonts/Inter-SemiBold.ttf"));
    add("JetBrainsMono", include_bytes!("../assets/fonts/JetBrainsMono-Regular.ttf"));
    egui_phosphor::add_to_fonts(&mut fonts, egui_phosphor::Variant::Regular);
    // The bundled Inter has its private-use-area cmap stripped, so phosphor's icons win there.
    fonts.families.entry(FontFamily::Proportional).or_default().insert(0, "Inter".into());
    fonts.families.entry(FontFamily::Monospace).or_default().insert(0, "JetBrainsMono".into());
    // CJK and other scripts the bundled fonts lack: borrow a system font if one is installed.
    if let Some(bytes) = system_fallback_font() {
        fonts.font_data.insert("system-fallback".into(), Arc::new(FontData::from_owned(bytes)));
        for fam in [FontFamily::Proportional, FontFamily::Monospace] {
            fonts.families.entry(fam).or_default().push("system-fallback".into());
        }
    }
    let mut semibold = vec!["Inter-SemiBold".to_owned()];
    semibold.extend(fonts.families[&FontFamily::Proportional].iter().cloned());
    fonts.families.insert(FontFamily::Name("semibold".into()), semibold);
    ctx.set_fonts(fonts);
}

/// The first wide-coverage (CJK) system font found on this machine.
fn system_fallback_font() -> Option<Vec<u8>> {
    let candidates: &[&str] = if cfg!(target_os = "macos") {
        &[
            "/System/Library/Fonts/PingFang.ttc",
            "/System/Library/Fonts/Hiragino Sans GB.ttc",
            "/Library/Fonts/Arial Unicode.ttf",
            "/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
        ]
    } else if cfg!(windows) {
        &["C:\\Windows\\Fonts\\msyh.ttc", "C:\\Windows\\Fonts\\YuGothM.ttc", "C:\\Windows\\Fonts\\msgothic.ttc", "C:\\Windows\\Fonts\\malgun.ttf"]
    } else {
        &[
            "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
            "/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
            "/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc",
            "/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
            "/usr/share/fonts/wenquanyi/wqy-microhei/wqy-microhei.ttc",
            "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
        ]
    };
    candidates.iter().find_map(|p| std::fs::read(p).ok())
}

pub fn semibold(size: f32) -> FontId {
    FontId::new(size, FontFamily::Name("semibold".into()))
}

pub fn mono(size: f32) -> FontId {
    FontId::monospace(size)
}

pub fn apply(ctx: &egui::Context, kind: ThemeKind) {
    let t = Tokens::for_kind(kind);
    ctx.data_mut(|d| d.insert_temp(egui::Id::new("nexus-theme"), t));
    let mut v = if t.dark() { Visuals::dark() } else { Visuals::light() };
    v.panel_fill = t.card;
    v.window_fill = t.card;
    v.window_stroke = Stroke::new(1.0, t.card_border);
    v.extreme_bg_color = t.field;
    v.faint_bg_color = t.tab_strip;
    v.code_bg_color = t.field;
    v.hyperlink_color = t.accent;
    v.selection.bg_fill = t.accent_soft;
    v.selection.stroke = Stroke::new(1.0, t.accent);
    v.override_text_color = Some(t.text);
    v.warn_fg_color = t.warning;
    v.error_fg_color = t.danger;
    let r = egui::CornerRadius::same(t.radius as u8);
    v.window_corner_radius = egui::CornerRadius::same(8);
    v.menu_corner_radius = egui::CornerRadius::same(6);
    for (w, fill) in [
        (&mut v.widgets.noninteractive, t.card),
        (&mut v.widgets.inactive, t.field),
        (&mut v.widgets.hovered, t.hover),
        (&mut v.widgets.active, t.hover),
        (&mut v.widgets.open, t.hover),
    ] {
        w.bg_fill = fill;
        w.weak_bg_fill = fill;
        w.corner_radius = r;
        w.fg_stroke.color = t.text;
    }
    v.widgets.noninteractive.bg_stroke = Stroke::new(1.0, t.separator);
    v.widgets.noninteractive.fg_stroke.color = t.text_dim;
    v.widgets.inactive.bg_stroke = Stroke::new(1.0, t.field_border);
    v.widgets.hovered.bg_stroke = Stroke::new(1.0, t.text_faint);
    v.widgets.active.bg_stroke = Stroke::new(1.0, t.accent);
    v.popup_shadow = egui::epaint::Shadow { offset: [0, 6], blur: 18, spread: 0, color: Color32::from_black_alpha(if t.dark() { 120 } else { 40 }) };
    v.window_shadow = v.popup_shadow;
    ctx.set_visuals(v);
    ctx.all_styles_mut(|s| {
        s.text_styles = [
            (TextStyle::Small, FontId::proportional(11.0)),
            (TextStyle::Body, FontId::proportional(13.0)),
            (TextStyle::Button, FontId::proportional(13.0)),
            (TextStyle::Heading, semibold(16.0)),
            (TextStyle::Monospace, FontId::monospace(12.5)),
        ]
        .into();
        s.spacing.item_spacing = egui::vec2(8.0, 6.0);
        s.spacing.button_padding = egui::vec2(10.0, 4.0);
        s.spacing.interact_size.y = 24.0;
        s.spacing.scroll = egui::style::ScrollStyle::thin();
    });
}
