// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

mod capture;
mod dedup;
mod picker;

use std::io::{self, Write};
use std::num::ParseIntError;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::str::FromStr;
use std::sync::mpsc::{self, RecvTimeoutError};
use std::time::Duration;

use clap::Parser;
use time::OffsetDateTime;

use capture::{default_backend, Capture, Region};
use dedup::Dedup;

const HELP_AGENT: &str = include_str!("help_agent.txt");

#[derive(Parser, Debug)]
#[command(
    name = "pageflip",
    version,
    about = "Capture slides from a screen region whenever they change",
    long_about = "pageflip watches a region of the screen and writes a PNG each time the \
                  contents change meaningfully (as measured by perceptual-hash Hamming \
                  distance). It is designed to feed a live stream of slides into a Claude \
                  Code session during a meeting."
)]
struct Cli {
    /// Region to capture as X,Y,W,H (screen coordinates, pixels).
    #[arg(long, value_name = "X,Y,W,H")]
    region: Option<RegionArg>,

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

#[derive(Clone, Copy, Debug)]
struct RegionArg {
    x: i32,
    y: i32,
    w: u32,
    h: u32,
}

impl From<RegionArg> for Region {
    fn from(r: RegionArg) -> Self {
        Region {
            x: r.x,
            y: r.y,
            w: r.w,
            h: r.h,
        }
    }
}

impl FromStr for RegionArg {
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
        Ok(RegionArg {
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

    match run(&cli) {
        Ok(()) => ExitCode::SUCCESS,
        Err(msg) => {
            eprintln!("pageflip: {msg}");
            ExitCode::FAILURE
        }
    }
}

fn run(cli: &Cli) -> Result<(), String> {
    let region: Region = match cli.region {
        Some(r) => r.into(),
        None => match picker::pick_region().map_err(|e| e.to_string())? {
            Some(r) => r,
            None => {
                eprintln!("pageflip: picker cancelled.");
                return Ok(());
            }
        },
    };

    if !cli.interval.is_finite() || cli.interval <= 0.0 {
        return Err(format!(
            "--interval must be a positive number of seconds, got {}",
            cli.interval
        ));
    }
    let interval = Duration::from_secs_f64(cli.interval);

    let output_dir = cli
        .output
        .clone()
        .unwrap_or_else(|| PathBuf::from(default_output_dir_name()));
    std::fs::create_dir_all(&output_dir)
        .map_err(|e| format!("could not create output directory {output_dir:?}: {e}"))?;

    let backend = default_backend().map_err(|e| e.to_string())?;
    let mut dedup = Dedup::new(cli.threshold);

    let stop = install_signal_handler()?;

    eprintln!(
        "pageflip: capturing region {:?} every {:.3}s (threshold {} bits); Ctrl-C to stop",
        region, cli.interval, cli.threshold
    );

    loop {
        let tick_start = std::time::Instant::now();

        capture_once(backend.as_ref(), region, &mut dedup, &output_dir)?;

        // Wait the remainder of the tick, waking early on Ctrl-C. If capture
        // took longer than `interval`, skip the sleep and capture immediately.
        let remaining = interval.saturating_sub(tick_start.elapsed());
        if remaining.is_zero() {
            if matches!(
                stop.try_recv(),
                Ok(()) | Err(mpsc::TryRecvError::Disconnected)
            ) {
                break;
            }
            continue;
        }
        match stop.recv_timeout(remaining) {
            Ok(()) | Err(RecvTimeoutError::Disconnected) => break,
            Err(RecvTimeoutError::Timeout) => continue,
        }
    }

    eprintln!("pageflip: stopped.");
    Ok(())
}

fn capture_once(
    backend: &dyn Capture,
    region: Region,
    dedup: &mut Dedup,
    output_dir: &Path,
) -> Result<(), String> {
    let frame = backend.capture(region).map_err(|e| e.to_string())?;
    if !dedup.should_save(&frame) {
        return Ok(());
    }
    let filename = format!("{}.png", timestamp_slug());
    let path = output_dir.join(filename);
    frame.save_png(&path).map_err(|e| e.to_string())?;

    let absolute = path
        .canonicalize()
        .unwrap_or_else(|_| output_dir.join(path.file_name().unwrap()));
    // The downstream feeder (🎯T5) relies on one line per saved PNG on stdout.
    // Flush explicitly so consumers don't buffer a slide.
    let mut stdout = io::stdout().lock();
    writeln!(stdout, "{}", absolute.display())
        .and_then(|_| stdout.flush())
        .map_err(|e| format!("stdout write failed: {e}"))?;
    Ok(())
}

/// Installs a Ctrl-C handler that sends one message on the returned receiver
/// when the user interrupts the process. Subsequent signals are ignored so a
/// second Ctrl-C does not abort mid-save.
fn install_signal_handler() -> Result<mpsc::Receiver<()>, String> {
    let (tx, rx) = mpsc::channel();
    ctrlc::set_handler(move || {
        let _ = tx.send(());
    })
    .map_err(|e| format!("failed to install signal handler: {e}"))?;
    Ok(rx)
}

fn default_output_dir_name() -> String {
    format!("pageflip-{}", OffsetDateTime::now_utc().unix_timestamp())
}

fn timestamp_slug() -> String {
    let now = OffsetDateTime::now_utc();
    format!(
        "{:04}{:02}{:02}T{:02}{:02}{:02}Z",
        now.year(),
        u8::from(now.month()),
        now.day(),
        now.hour(),
        now.minute(),
        now.second()
    )
}
