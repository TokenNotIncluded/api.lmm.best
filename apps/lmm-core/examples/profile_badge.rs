// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
//! `cargo run --locked --example profile_badge -- light wave > preview.svg`
//! Static embedding fallback: `cargo run --locked --example profile_badge -- light none`.
//! Synthetic design preview only: no sharing endpoint, database or live statistics.
#[path = "../src/profile_badge.rs"]
mod profile_badge;

use profile_badge::{Animation, ModelUsage, Options, Theme, Usage, render};

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = std::env::args().skip(1).collect();
    let theme = match args.first().map(String::as_str).unwrap_or("light") {
        "light" => Theme::Light,
        "dark" => Theme::Dark,
        "transparent" => Theme::Transparent,
        _ => return Err("theme must be light, dark or transparent".into()),
    };
    let animation = match args.get(1).map(String::as_str).unwrap_or("wave") {
        "wave" => Animation::Wave,
        "pulse" => Animation::Pulse,
        "none" => Animation::None,
        _ => return Err("animation must be wave, pulse or none".into()),
    };
    let data = Usage {
        display_name: "Design preview".into(),
        period_label: "Last 30 days · illustrative".into(),
        tokens: Some(12_840_620),
        requests: Some(8_426),
        models: vec![
            ModelUsage {
                name: "Example reasoning model".into(),
                tokens: 6_340_200,
            },
            ModelUsage {
                name: "Example coding model".into(),
                tokens: 4_280_420,
            },
            ModelUsage {
                name: "Example lightweight model".into(),
                tokens: 2_220_000,
            },
        ],
        footer: "Illustrative values · not connected to live usage".into(),
    };
    println!(
        "{}",
        render(
            Options {
                theme,
                animation,
                ..Options::default()
            },
            &data
        )?
    );
    Ok(())
}
