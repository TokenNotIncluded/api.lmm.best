// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
//! Self-contained SVG presentation, deliberately separate from sharing authorization.
//!
//! Callers must authorize and project public usage before calling this renderer.
//! This module performs no I/O and is not a public HTTP endpoint. It never derives
//! missing usage, fetches linked profiles, or combines different reporting periods.

use std::fmt::{self, Write};

// Generated from .github/assets/logo-geometry.json. The integration test checks
// this copy against the master; the independently built core needs no root asset.
pub const LOGO_VIEW_BOX: [u16; 4] = [0, 0, 448, 270];
pub const LOGO_PATHS: [(&str, &str); 4] = [
    (
        "back",
        "M8 67C8 53 12 45 24 38L76 7C79 5 80 7 80 12V125C80 135 85 138 92 134L25 174C15 180 8 174 8 164Z",
    ),
    (
        "front",
        "M8 163C8 173 16 178 26 172L119 118C132 110 144 110 156 118L169 127C178 133 183 142 183 153V174C183 187 178 194 167 201L119 229C108 236 96 236 85 229L24 193C13 186 8 178 8 163Z",
    ),
    (
        "fold",
        "M145 98L193 70C207 62 218 62 231 71L294 113C307 122 314 133 314 148V194C314 210 308 220 295 228L244 257C241 259 239 257 239 253V184C239 171 234 161 223 153Z",
    ),
    (
        "fold",
        "M282 98L330 70C344 62 355 62 368 71L418 105C430 114 437 125 437 140V194C437 210 431 220 418 228L367 257C364 259 362 257 362 253V184C362 171 357 161 346 153Z",
    ),
];
pub const LOGO_LETTERING: [&str; 6] = [
    "M0 0H18V82H76V100H0Z",
    "M90 100V0H110L148 44L186 0H206V100H186V30L148 74L110 30V100Z",
    "M224 100V0H244L282 44L320 0H340V100H320V30L282 74L244 30V100Z",
    "M390 100L432 0H455L497 100H475L467 80H420L412 100Z M428 61H459L443 22Z",
    "M510 100V0H564C590 0 605 13 605 36C605 59 590 72 564 72H530V100Z M530 18V54H562C576 54 585 49 585 36C585 24 576 18 562 18Z",
    "M623 0H643V100H623Z",
];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Theme {
    Light,
    Dark,
    Transparent,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Animation {
    Wave,
    Pulse,
    None,
}

#[derive(Clone, Copy, Debug)]
pub struct Options {
    pub width: u16,
    pub height: u16,
    pub theme: Theme,
    pub animation: Animation,
}

impl Default for Options {
    fn default() -> Self {
        Self {
            width: 1200,
            height: 720,
            theme: Theme::Light,
            animation: Animation::Wave,
        }
    }
}

#[derive(Debug)]
pub struct ModelUsage {
    pub name: String,
    pub tokens: u64,
}

#[derive(Debug)]
pub struct Usage {
    pub display_name: String,
    pub period_label: String,
    /// Unknown values remain unknown, rather than being rendered as zero.
    pub tokens: Option<u64>,
    pub requests: Option<u64>,
    /// These rows must belong to the same authorized reporting period.
    pub models: Vec<ModelUsage>,
    pub footer: String,
}

#[derive(Debug, PartialEq, Eq)]
pub enum RenderError {
    Canvas,
    TooManyModels,
    InvalidText(&'static str),
}

impl fmt::Display for RenderError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::Canvas => "badge canvas must be 480–1600 by 240–1200",
            Self::TooManyModels => "badge supports at most six model rows",
            Self::InvalidText(field) => field,
        })
    }
}

impl std::error::Error for RenderError {}

struct Palette {
    background: &'static str,
    foreground: &'static str,
    muted: &'static str,
    border: &'static str,
    fold: &'static str,
}

fn text(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

fn validate_text(value: &str, limit: usize, field: &'static str) -> Result<(), RenderError> {
    if value.chars().count() > limit
        || value
            .chars()
            .any(|ch| ch.is_control() || matches!(ch, '\u{fffe}' | '\u{ffff}'))
    {
        return Err(RenderError::InvalidText(field));
    }
    Ok(())
}

fn short_text(value: &str, max_units: usize) -> String {
    let mut units = 0;
    let mut output = String::new();
    for ch in value.chars() {
        units += if ch.is_ascii() { 1 } else { 2 };
        if units > max_units {
            output.push('…');
            break;
        }
        output.push(ch);
    }
    text(&output)
}

fn number(value: Option<u64>) -> String {
    let Some(value) = value else {
        return "—".into();
    };
    let digits = value.to_string();
    let mut output = String::with_capacity(digits.len() + digits.len() / 3);
    for (index, digit) in digits.chars().enumerate() {
        if index > 0 && (digits.len() - index).is_multiple_of(3) {
            output.push(',');
        }
        output.push(digit);
    }
    output
}

/// Render an image with inline geometry and CSS. Animation also works in `<img>`.
/// The complete static design is visible when animation is unsupported or disabled.
/// Callers that know the user's reduced-motion preference should select
/// `Animation::None`; the SVG media query is an additional host-dependent fallback.
pub fn render(options: Options, usage: &Usage) -> Result<String, RenderError> {
    if !(480..=1600).contains(&options.width) || !(240..=1200).contains(&options.height) {
        return Err(RenderError::Canvas);
    }
    if usage.models.len() > 6 {
        return Err(RenderError::TooManyModels);
    }
    for (value, limit, field) in [
        (&usage.display_name, 48, "invalid display name"),
        (&usage.period_label, 64, "invalid reporting period"),
        (&usage.footer, 100, "invalid footer"),
    ] {
        validate_text(value, limit, field)?;
    }
    for model in &usage.models {
        validate_text(&model.name, 48, "invalid model name")?;
    }
    let palette = match options.theme {
        Theme::Light | Theme::Transparent => Palette {
            background: if options.theme == Theme::Transparent {
                "none"
            } else {
                "#ffffff"
            },
            foreground: "#1d2023",
            muted: "#666d75",
            border: "#dfe2e6",
            fold: "#808892",
        },
        Theme::Dark => Palette {
            background: "#1d2023",
            foreground: "#ffffff",
            muted: "#aeb4bc",
            border: "#3b4046",
            fold: "#aeb4bc",
        },
    };
    let content_height = 382 + usage.models.len().max(1) * 48;
    let mut svg = String::with_capacity(9000);
    write!(svg, r#"<svg xmlns="http://www.w3.org/2000/svg" width="{}" height="{}" viewBox="0 0 1200 {}" preserveAspectRatio="xMidYMid meet" role="img" aria-labelledby="badge-title badge-description"><title id="badge-title">LMM API · {}</title><desc id="badge-description">{} · Tokens: {} · API requests: {}</desc>"#,
        options.width, options.height, content_height, text(&usage.display_name), text(&usage.period_label), number(usage.tokens), number(usage.requests)).unwrap();
    write!(svg, r##"<defs><linearGradient id="ribbon-fold" x1="0" y1="0" x2="1" y2="1"><stop stop-color="{}"/><stop offset="1" stop-color="{}"/></linearGradient><linearGradient id="ribbon-light"><stop stop-color="#fff" stop-opacity="0"/><stop offset=".5" stop-color="#fff" stop-opacity=".75"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient><clipPath id="ribbon-clip">"##,
        palette.foreground, palette.fold).unwrap();
    for (_, path) in LOGO_PATHS {
        write!(svg, r#"<path d="{path}"/>"#).unwrap();
    }
    svg.push_str("</clipPath></defs>");
    match options.animation {
        Animation::Wave => svg.push_str(r#"<style>@keyframes ribbon-flow{0%,10%{transform:translateX(-480px);opacity:0}20%{opacity:.7}70%{transform:translateX(480px);opacity:.7}80%,100%{transform:translateX(480px);opacity:0}}.ribbon-sheen{animation:ribbon-flow 4.8s linear infinite}@media(prefers-reduced-motion:reduce){.ribbon-sheen{display:none;animation:none!important}}</style>"#),
        Animation::Pulse => svg.push_str(r#"<style>@keyframes ribbon-breathe{0%,100%{opacity:1}50%{opacity:.55}}.ribbon-shape{animation:ribbon-breathe 3.2s ease-in-out infinite}@media(prefers-reduced-motion:reduce){.ribbon-shape{animation:none!important;opacity:1}}</style>"#),
        Animation::None => {}
    }
    let logo_scale = 103.0 / f64::from(LOGO_VIEW_BOX[2]);
    write!(svg, r#"<rect x="1" y="1" width="1198" height="{}" rx="16" fill="{}" stroke="{}"/><g font-family="Arial, 'Noto Sans CJK SC', sans-serif" fill="{}"><g transform="translate(48 35) scale({logo_scale:.6})"><g class="ribbon-shape">"#,
        content_height - 2, palette.background, palette.border, palette.foreground).unwrap();
    for (tone, path) in LOGO_PATHS {
        let (fill, opacity) = match tone {
            "back" => (palette.foreground, "1"),
            "front" => (palette.foreground, ".65"),
            "fold" => ("url(#ribbon-fold)", "1"),
            _ => unreachable!("trusted checked-in logo tone"),
        };
        write!(
            svg,
            r#"<path d="{path}" fill="{fill}" opacity="{opacity}"/>"#
        )
        .unwrap();
    }
    svg.push_str("</g>");
    if options.animation == Animation::Wave {
        svg.push_str(r#"<g clip-path="url(#ribbon-clip)"><rect class="ribbon-sheen" x="150" y="0" width="100" height="270" fill="url(#ribbon-light)"/></g>"#);
    }
    svg.push_str("</g>");
    svg.push_str(
        r#"<g transform="translate(168 40) scale(.325)" fill-rule="evenodd" aria-label="LMM API">"#,
    );
    for path in LOGO_LETTERING {
        write!(svg, r#"<path d="{path}"/>"#).unwrap();
    }
    svg.push_str("</g>");
    write!(svg, r#"<text x="169" y="95" font-size="10.8" letter-spacing="1.4" fill="{}">Language Model Mesh</text><text x="1152" y="57" text-anchor="end" font-size="19" font-weight="600">{}</text><text x="1152" y="84" text-anchor="end" font-size="14" fill="{}">{}</text><path d="M48 121H1152" stroke="{}"/>"#,
        palette.muted, short_text(&usage.display_name, 40), palette.muted, short_text(&usage.period_label, 58), palette.border).unwrap();
    for (x, label, value) in [
        (48, "TOKENS", usage.tokens),
        (640, "API REQUESTS", usage.requests),
    ] {
        let value = number(value);
        let font_size = if value.len() > 20 { 33 } else { 48 };
        write!(svg, r#"<text x="{x}" y="174" font-size="13" letter-spacing="1.8" fill="{}">{label}</text><text x="{x}" y="237" font-size="{font_size}" font-weight="600" letter-spacing="-1" font-variant-numeric="tabular-nums">{value}</text>"#, palette.muted).unwrap();
    }
    write!(
        svg,
        r#"<path d="M592 154V245M48 277H1152" stroke="{}"/>"#,
        palette.border
    )
    .unwrap();
    let maximum = usage.models.iter().map(|row| row.tokens).max().unwrap_or(0);
    if usage.models.is_empty() {
        write!(
            svg,
            r#"<text x="48" y="326" font-size="16" fill="{}">—</text>"#,
            palette.muted
        )
        .unwrap();
    }
    for (index, model) in usage.models.iter().enumerate() {
        let y = 313 + index * 48;
        let length = if maximum == 0 {
            0.0
        } else {
            model.tokens as f64 / maximum as f64 * 320.0
        };
        write!(svg, r#"<g><title>{}</title><text x="48" y="{y}" font-size="17">{}</text><path d="M640 {}H960" stroke="{}" stroke-width="4" stroke-linecap="round"/><path d="M640 {}h{length:.2}" stroke="{}" stroke-width="4" stroke-linecap="round"/><text x="1152" y="{y}" text-anchor="end" font-size="16" font-variant-numeric="tabular-nums">{}</text></g>"#,
            text(&model.name), short_text(&model.name, 52), y - 5, palette.border, y - 5, palette.foreground, number(Some(model.tokens))).unwrap();
    }
    let footer_y = content_height - 35;
    write!(svg, r#"<path d="M48 {}H1152" stroke="{}"/><text x="48" y="{footer_y}" font-size="13" fill="{}">{}</text><text x="1152" y="{footer_y}" text-anchor="end" font-size="13" fill="{}">api.lmm.best</text></g></svg>"#,
        footer_y - 31, palette.border, palette.muted, short_text(&usage.footer, 110), palette.muted).unwrap();
    Ok(svg)
}
