// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
// The renderer is not registered in the incomplete core HTTP application yet.
#[path = "../src/profile_badge.rs"]
mod profile_badge;

use profile_badge::{Animation, ModelUsage, Options, RenderError, Theme, Usage, render};

fn usage() -> Usage {
    Usage {
        display_name: "Design preview".into(),
        period_label: "Last 30 days".into(),
        tokens: Some(1_234_567),
        requests: Some(2_048),
        models: vec![ModelUsage {
            name: "Example model".into(),
            tokens: 1_234_567,
        }],
        footer: "Illustrative values · not connected to live usage".into(),
    }
}

#[test]
fn copied_geometry_matches_the_shared_brand_master() {
    let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../.github/assets/logo-geometry.json");
    let master: serde_json::Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    assert_eq!(
        master["symbol"]["viewBox"],
        serde_json::json!(profile_badge::LOGO_VIEW_BOX)
    );
    let paths = master["symbol"]["paths"].as_array().unwrap();
    assert_eq!(paths.len(), profile_badge::LOGO_PATHS.len());
    for (path, (tone, d)) in paths.iter().zip(profile_badge::LOGO_PATHS) {
        assert_eq!(path["tone"], tone);
        assert_eq!(path["d"], d);
    }
    assert_eq!(
        master["lettering"]["paths"],
        serde_json::json!(profile_badge::LOGO_LETTERING)
    );
}

#[test]
fn image_is_self_contained_with_accessible_usage_and_brand() {
    let svg = render(Options::default(), &usage()).unwrap();
    assert!(svg.contains("LMM API · Design preview"));
    assert!(svg.contains("Language Model Mesh"));
    assert!(svg.contains("Tokens: 1,234,567 · API requests: 2,048"));
    assert!(svg.contains("Illustrative values · not connected to live usage"));
    assert!(svg.contains("preserveAspectRatio=\"xMidYMid meet\""));
    for forbidden in [
        "<script",
        "<foreignObject",
        "<image",
        "href=",
        "@import",
        "<animate",
    ] {
        assert!(!svg.contains(forbidden), "unexpected {forbidden}");
    }
}

#[test]
fn wave_and_pulse_are_inline_svg_animations_with_static_motion_alternatives() {
    let wave = render(Options::default(), &usage()).unwrap();
    let pulse = render(
        Options {
            animation: Animation::Pulse,
            ..Options::default()
        },
        &usage(),
    )
    .unwrap();
    assert!(wave.contains("@keyframes ribbon-flow"));
    assert!(wave.contains("clip-path=\"url(#ribbon-clip)\""));
    assert!(pulse.contains("@keyframes ribbon-breathe"));
    assert!(!pulse.contains("@keyframes ribbon-flow"));
    for svg in [wave, pulse] {
        assert!(svg.contains("@media(prefers-reduced-motion:reduce)"));
        assert!(svg.contains("animation:none!important"));
        assert!(svg.contains("1,234,567"));
    }
}

#[test]
fn no_animation_does_not_emit_styles_or_animated_elements() {
    let svg = render(
        Options {
            animation: Animation::None,
            ..Options::default()
        },
        &usage(),
    )
    .unwrap();
    assert!(!svg.contains("<style>"));
    assert!(!svg.contains("class=\"ribbon-sheen\""));
    assert!(svg.contains("1,234,567"));
}

#[test]
fn all_untrusted_strings_are_escaped_as_text() {
    let mut data = usage();
    data.display_name = "<script onload=\"x\">&'".into();
    data.period_label = "<image href='javascript:x'/>".into();
    data.footer = "</text><script>alert(1)</script>".into();
    data.models[0].name = "</text><foreignObject>".into();
    let svg = render(Options::default(), &data).unwrap();
    assert!(svg.contains("&lt;script onload=&quot;x&quot;&gt;&amp;&apos;"));
    assert!(svg.contains("&lt;foreignObject&gt;"));
    assert!(!svg.contains("<script"));
    assert!(!svg.contains("<foreignObject"));
    assert!(!svg.contains("<image"));
}

#[test]
fn invalid_xml_controls_and_unbounded_text_are_rejected() {
    for invalid in [
        "line\nfeed".into(),
        "NUL\0".into(),
        "bad\u{ffff}".into(),
        "x".repeat(49),
    ] {
        let mut data = usage();
        data.models[0].name = invalid;
        assert_eq!(
            render(Options::default(), &data),
            Err(RenderError::InvalidText("invalid model name"))
        );
    }
}

#[test]
fn canvas_and_row_limits_are_enforced() {
    for (width, height) in [(479, 720), (1601, 720), (1200, 239), (1200, 1201)] {
        assert_eq!(
            render(
                Options {
                    width,
                    height,
                    ..Options::default()
                },
                &usage()
            ),
            Err(RenderError::Canvas)
        );
    }
    for (width, height) in [(480, 240), (1600, 1200)] {
        assert!(
            render(
                Options {
                    width,
                    height,
                    ..Options::default()
                },
                &usage()
            )
            .is_ok()
        );
    }
    let mut data = usage();
    data.models = (0..7)
        .map(|_| ModelUsage {
            name: "model".into(),
            tokens: 0,
        })
        .collect();
    assert_eq!(
        render(Options::default(), &data),
        Err(RenderError::TooManyModels)
    );
    data.models.pop();
    assert!(render(Options::default(), &data).is_ok());
}

#[test]
fn unknown_totals_are_distinct_from_known_zero_and_large_integers_stay_exact() {
    let mut data = usage();
    data.tokens = None;
    data.requests = Some(0);
    let svg = render(Options::default(), &data).unwrap();
    assert!(svg.contains("Tokens: — · API requests: 0"));
    data.tokens = Some(u64::MAX);
    data.models[0].tokens = u64::MAX;
    let svg = render(Options::default(), &data).unwrap();
    assert!(svg.contains("18,446,744,073,709,551,615"));
    assert!(!svg.contains("NaN"));
    assert!(!svg.contains("hinf"));
    assert!(!svg.contains("h-inf"));
}

#[test]
fn monochrome_themes_keep_transparency_and_empty_usage_truthful() {
    let mut data = usage();
    data.models.clear();
    for (theme, fill) in [
        (Theme::Light, "#ffffff"),
        (Theme::Dark, "#1d2023"),
        (Theme::Transparent, "none"),
    ] {
        let svg = render(
            Options {
                theme,
                ..Options::default()
            },
            &data,
        )
        .unwrap();
        assert!(svg.contains(&format!("rx=\"16\" fill=\"{fill}\"")));
        assert!(!svg.contains("Example model"));
    }
}

#[test]
fn long_cjk_labels_are_shortened_without_losing_accessible_full_names() {
    let mut data = usage();
    data.display_name = "模型网络".repeat(12);
    data.models[0].name = "语言模型网络".repeat(8);
    let svg = render(Options::default(), &data).unwrap();
    assert!(svg.contains('…'));
    assert!(svg.contains(&format!("<title>{}</title>", data.models[0].name)));
}
