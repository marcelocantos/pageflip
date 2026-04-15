// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

mod capture;
mod dedup;
mod picker;
mod redact;

use std::io::{self, Write};
use std::num::ParseIntError;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::str::FromStr;
use std::sync::mpsc::{self, RecvTimeoutError};
use std::time::Duration;

use clap::Parser;
use time::OffsetDateTime;

use capture::{default_backend, Capture, CropSpec, Region, WindowSpec};
use dedup::Dedup;
use redact::{FaceBlurRedactor, RedactPipeline, Redactor};

enum Target {
    Region(Region),
    Window(WindowSpec),
    /// Window captured every tick, output cropped to fractional sub-region.
    WindowCropped(WindowSpec, CropSpec),
}

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
#[command(group(
    clap::ArgGroup::new("target")
        .args(["region", "window", "window_title", "window_id", "list_windows"])
        .multiple(false),
))]
struct Cli {
    /// Region to capture as X,Y,W,H (logical-point screen coordinates).
    #[arg(long, value_name = "X,Y,W,H")]
    region: Option<RegionArg>,

    /// Capture a window picked interactively from the list of visible windows.
    #[arg(long)]
    window: bool,

    /// Capture the first window whose title contains this substring.
    #[arg(long, value_name = "SUBSTRING")]
    window_title: Option<String>,

    /// Capture the window with this numeric ID (see --list-windows).
    #[arg(long, value_name = "ID")]
    window_id: Option<u32>,

    /// Print the list of visible windows (id, app, title) and exit.
    #[arg(long)]
    list_windows: bool,

    /// After resolving a window target, open a crop picker on a snapshot of
    /// that window. The user drags a rectangle; the crop is stored as
    /// fractional (0.0–1.0) window-relative coordinates and re-applied every
    /// tick so the crop tracks the window if it resizes. Requires one of
    /// --window, --window-title, or --window-id.
    #[arg(long)]
    crop: bool,

    /// Capture interval in seconds.
    #[arg(long, default_value_t = 2.0, value_name = "SECS")]
    interval: f64,

    /// pHash Hamming-distance threshold; frames closer than this to the last saved frame are skipped.
    #[arg(long, default_value_t = 10, value_name = "N")]
    threshold: u32,

    /// Output directory for captured PNGs.
    #[arg(long, value_name = "DIR")]
    output: Option<PathBuf>,

    /// Blur detected faces in every saved frame using Apple Vision (macOS).
    #[arg(long)]
    redact_faces: bool,

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
    let backend = default_backend().map_err(|e| e.to_string())?;

    if cli.list_windows {
        return print_window_list(backend.as_ref());
    }

    let target = resolve_target(cli, backend.as_ref())?;
    let Some(target) = target else {
        eprintln!("pageflip: picker cancelled.");
        return Ok(());
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

    let mut dedup = Dedup::new(cli.threshold);
    let redact = build_redact_pipeline(cli);

    let stop = install_signal_handler()?;

    match &target {
        Target::Region(r) => eprintln!(
            "pageflip: capturing region {:?} every {:.3}s (threshold {} bits); Ctrl-C to stop",
            r, cli.interval, cli.threshold
        ),
        Target::Window(spec) => eprintln!(
            "pageflip: capturing window {:?} every {:.3}s (threshold {} bits); Ctrl-C to stop",
            spec, cli.interval, cli.threshold
        ),
        Target::WindowCropped(spec, crop) => eprintln!(
            "pageflip: capturing window {:?} crop ({:.3},{:.3},{:.3},{:.3}) \
             every {:.3}s (threshold {} bits); Ctrl-C to stop",
            spec, crop.x, crop.y, crop.w, crop.h, cli.interval, cli.threshold
        ),
    };

    loop {
        let tick_start = std::time::Instant::now();

        capture_once(backend.as_ref(), &target, &mut dedup, &redact, &output_dir)?;

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

fn resolve_target(cli: &Cli, backend: &dyn Capture) -> Result<Option<Target>, String> {
    if cli.crop && cli.region.is_some() {
        return Err("--crop requires a window target (--window, --window-title, or --window-id); use --region for a fixed absolute crop instead".to_string());
    }
    if cli.crop && !cli.window && cli.window_title.is_none() && cli.window_id.is_none() {
        return Err("--crop requires one of --window, --window-title, or --window-id".to_string());
    }

    if let Some(r) = cli.region {
        return Ok(Some(Target::Region(r.into())));
    }

    // Resolve window spec from flags.
    let window_spec = if let Some(id) = cli.window_id {
        Some(WindowSpec::Id(id))
    } else if let Some(title) = &cli.window_title {
        Some(WindowSpec::TitleContains(title.clone()))
    } else if cli.window {
        pick_window_interactively(backend)?
    } else {
        None
    };

    if let Some(spec) = window_spec {
        if cli.crop {
            // Take a one-shot snapshot to show in the crop picker.
            eprintln!("pageflip: snapshotting window for crop picker…");
            let snapshot = backend.capture_window(&spec).map_err(|e| e.to_string())?;
            match picker::pick_crop(&snapshot).map_err(|e| e.to_string())? {
                Some(crop) => return Ok(Some(Target::WindowCropped(spec, crop))),
                None => return Ok(None),
            }
        }
        return Ok(Some(Target::Window(spec)));
    }

    // No target flags: fall back to the interactive region picker.
    match picker::pick_region().map_err(|e| e.to_string())? {
        Some(r) => Ok(Some(Target::Region(r))),
        None => Ok(None),
    }
}

fn pick_window_interactively(backend: &dyn Capture) -> Result<Option<WindowSpec>, String> {
    let windows = backend.list_windows().map_err(|e| e.to_string())?;
    if windows.is_empty() {
        return Err("no visible windows available for capture".to_string());
    }
    eprintln!("Visible windows:");
    for (i, w) in windows.iter().enumerate() {
        eprintln!("  [{i}] id={} {} — {}", w.id, w.app_name, w.title);
    }
    eprint!("Pick by index (blank to cancel): ");
    io::stderr().flush().ok();
    let mut line = String::new();
    io::stdin()
        .read_line(&mut line)
        .map_err(|e| format!("stdin: {e}"))?;
    let trimmed = line.trim();
    if trimmed.is_empty() {
        return Ok(None);
    }
    let idx: usize = trimmed
        .parse()
        .map_err(|e| format!("invalid index {trimmed:?}: {e}"))?;
    let picked = windows
        .get(idx)
        .ok_or_else(|| format!("index {idx} out of range (0..{})", windows.len()))?;
    Ok(Some(WindowSpec::Id(picked.id)))
}

fn print_window_list(backend: &dyn Capture) -> Result<(), String> {
    let windows = backend.list_windows().map_err(|e| e.to_string())?;
    for w in &windows {
        println!("{}\t{}\t{}", w.id, w.app_name, w.title);
    }
    Ok(())
}

fn capture_once(
    backend: &dyn Capture,
    target: &Target,
    dedup: &mut Dedup,
    redact: &Option<RedactPipeline>,
    output_dir: &Path,
) -> Result<(), String> {
    let frame = match target {
        Target::Region(r) => backend.capture(*r),
        Target::Window(spec) => backend.capture_window(spec),
        Target::WindowCropped(spec, crop) => backend.capture_window_cropped(spec, crop),
    }
    .map_err(|e| e.to_string())?;
    if !dedup.should_save(&frame) {
        return Ok(());
    }
    let frame = match redact {
        Some(pipeline) => pipeline.apply(frame).map_err(|e| e.to_string())?,
        None => frame,
    };
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

fn build_redact_pipeline(cli: &Cli) -> Option<RedactPipeline> {
    if !cli.redact_faces {
        return None;
    }
    let mut pipeline = RedactPipeline::new();
    let face: Box<dyn Redactor> = Box::new(FaceBlurRedactor::default());
    pipeline.push(face);
    Some(pipeline)
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
