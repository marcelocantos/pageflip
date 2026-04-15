// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::num::ParseIntError;
use std::path::PathBuf;
use std::process::ExitCode;
use std::str::FromStr;

use clap::Parser;

const HELP_AGENT: &str = include_str!("help_agent.txt");

#[derive(Parser, Debug)]
#[command(
    name = "pageflip",
    version,
    about = "Capture slides from a screen region whenever they change",
    long_about = "pageflip watches a region of the screen and writes a PNG each time the \
                  contents change meaningfully (as measured by perceptual-hash Hamming \
                  distance). It is designed to feed a live stream of slides into a Claude \
                  Code session during a meeting.",
    disable_help_flag = false
)]
struct Cli {
    /// Region to capture as X,Y,W,H (screen coordinates, pixels).
    #[arg(long, value_name = "X,Y,W,H")]
    region: Option<Region>,

    /// Capture interval in seconds.
    #[arg(long, default_value_t = 2.0, value_name = "SECS")]
    interval: f64,

    /// pHash Hamming-distance threshold; frames closer than this to the last saved frame are skipped.
    #[arg(long, default_value_t = 10, value_name = "N")]
    threshold: u32,

    /// Output directory for captured PNGs.
    #[arg(long, value_name = "DIR")]
    output: Option<PathBuf>,

    /// Print agent-oriented help (machine-readable invocation notes) and exit.
    #[arg(long, exclusive = true)]
    help_agent: bool,
}

#[derive(Clone, Debug)]
#[allow(dead_code)] // fields are consumed by 🎯T1.2 (capture implementation)
struct Region {
    x: i32,
    y: i32,
    w: u32,
    h: u32,
}

impl FromStr for Region {
    type Err = String;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let parts: Vec<&str> = s.split(',').collect();
        if parts.len() != 4 {
            return Err(format!(
                "expected X,Y,W,H (4 comma-separated integers), got {s:?}"
            ));
        }
        let parse = |i: usize| -> Result<i64, ParseIntError> { parts[i].trim().parse::<i64>() };
        let x = parse(0).map_err(|e| format!("X: {e}"))?;
        let y = parse(1).map_err(|e| format!("Y: {e}"))?;
        let w = parse(2).map_err(|e| format!("W: {e}"))?;
        let h = parse(3).map_err(|e| format!("H: {e}"))?;
        if w <= 0 || h <= 0 {
            return Err(format!("W and H must be positive, got W={w} H={h}"));
        }
        Ok(Region {
            x: x as i32,
            y: y as i32,
            w: w as u32,
            h: h as u32,
        })
    }
}

fn main() -> ExitCode {
    let cli = Cli::parse();

    if cli.help_agent {
        print!("{HELP_AGENT}");
        return ExitCode::SUCCESS;
    }

    eprintln!(
        "pageflip {}: capture is not yet implemented (🎯T1.2 is next).",
        env!("CARGO_PKG_VERSION")
    );
    eprintln!("Parsed arguments:");
    eprintln!("  region:    {:?}", cli.region);
    eprintln!("  interval:  {} s", cli.interval);
    eprintln!("  threshold: {}", cli.threshold);
    eprintln!("  output:    {:?}", cli.output);
    ExitCode::from(2)
}
