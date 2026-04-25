// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

mod audio;
mod capture;
mod dedup;
mod doctor;
mod events;
mod picker;
pub mod policy;
mod redact;
mod session_log;

use std::io::{self, Write};
use std::num::ParseIntError;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::str::FromStr;
use std::sync::mpsc::{self, RecvTimeoutError};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use clap::{Parser, Subcommand};
use time::OffsetDateTime;

use events::{EventSink, SlideEvent};
use session_log::{LogEvent, SessionLog};

use audio::{AudioCaptureHandle, NullTranscriber, Transcriber};
use capture::{default_backend, Capture, CropSpec, Region, WindowSpec};
use dedup::Dedup;
use policy::ParanoidSaver;
use redact::{FaceBlurRedactor, RedactPipeline};

enum Target {
    Region(Region),
    Window(WindowSpec),
    /// Window captured every tick, output cropped to fractional sub-region.
    WindowCropped(WindowSpec, CropSpec),
}

const HELP_AGENT: &str = include_str!("help_agent.txt");

/// Subcommands. When absent the default capture loop runs.
#[derive(Subcommand, Debug)]
enum Cmd {
    /// Print a markdown diagnostics report for bug reports.
    Doctor {
        /// Tail the last 200 lines of pageflip's session log into the report.
        #[arg(long, value_name = "PATH")]
        log: Option<PathBuf>,
        /// Tail the last 200 lines of meetcat's session log into the report
        /// (passed through to `meetcat doctor --log-file`).
        #[arg(long, value_name = "PATH")]
        meetcat_log: Option<PathBuf>,
        /// Skip invoking `meetcat doctor` even if it is on PATH.
        #[arg(long)]
        no_meetcat: bool,
    },
}

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
    #[command(subcommand)]
    command: Option<Cmd>,

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

    /// Mask PII (emails, phone numbers, credit cards, government IDs, IPs)
    /// detected via OCR in every saved frame.
    #[arg(long)]
    redact_pii: bool,

    /// Save unredacted frames to DIR before redaction is applied. The
    /// redacted versions go to --output as normal; the raw originals stay
    /// local in this directory.
    #[arg(long, value_name = "DIR")]
    paranoid: Option<PathBuf>,

    /// Capture system audio alongside frames and feed it through a local
    /// Transcriber. Raw audio never leaves the audio module — only derived
    /// transcript segments are surfaced.
    #[arg(long)]
    audio: bool,

    /// Transcriber to use when --audio is set. Options: `null`, `whisperx`.
    #[arg(long, value_name = "KIND", default_value = "null")]
    transcriber: String,

    /// When --transcriber whisperx is set, override the Whisper model name
    /// passed to the Python subprocess. Defaults to `large-v3`.
    #[arg(long, value_name = "MODEL")]
    whisperx_model: Option<String>,

    /// Enable speaker diarisation when --transcriber whisperx is set.
    /// Adds speaker_id to each transcript segment via pyannote.
    #[arg(long)]
    diarize: bool,

    /// Emit one structured JSON object per saved slide (NDJSON).
    /// SPEC can be `stdout`, `fd:<N>`, or a file path.
    /// When absent, the legacy stdout contract (one absolute path per line)
    /// is preserved for backwards compatibility.
    #[arg(long, value_name = "SPEC")]
    events_out: Option<String>,

    /// Write a structured NDJSON session log to this path for bug reports.
    /// Use `pageflip doctor --log <PATH>` to include it in a doctor report.
    #[arg(long, value_name = "PATH")]
    log_file: Option<PathBuf>,

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

    if let Some(Cmd::Doctor {
        log,
        meetcat_log,
        no_meetcat,
    }) = &cli.command
    {
        doctor::run(log.as_deref(), meetcat_log.as_deref(), *no_meetcat);
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
    let paranoid = cli
        .paranoid
        .as_ref()
        .map(|dir| ParanoidSaver::new(dir.clone()));

    let mut sink = cli
        .events_out
        .as_deref()
        .map(events::parse_events_out)
        .transpose()?;

    // Open session log if requested.
    let mut slog: Option<SessionLog> = cli
        .log_file
        .as_deref()
        .map(|p| SessionLog::open(p).map_err(|e| format!("--log-file {p:?}: {e}")))
        .transpose()?;

    if let Some(ref mut sl) = slog {
        sl.log(LogEvent::SessionStart {
            version: env!("CARGO_PKG_VERSION").to_string(),
            git_sha: env!("PAGEFLIP_GIT_SHA").to_string(),
            pid: std::process::id(),
            t_ms: unix_ms(),
        });
    }

    let audio_handle = if cli.audio {
        Some(start_audio_capture(
            &cli.transcriber,
            &output_dir,
            cli.whisperx_model.clone(),
            cli.diarize,
        )?)
    } else {
        None
    };

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

    let mut slides_saved: u32 = 0;
    let mut slides_deduped: u32 = 0;

    loop {
        let tick_start = std::time::Instant::now();

        match capture_once(
            backend.as_ref(),
            &target,
            &mut dedup,
            &redact,
            paranoid.as_ref(),
            &output_dir,
            sink.as_mut(),
            slog.as_mut(),
            cli.threshold,
        ) {
            Ok(SaveResult::Saved) => slides_saved += 1,
            Ok(SaveResult::Deduped) => slides_deduped += 1,
            Err(e) => return Err(e),
        }

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

    if let Some(ref mut sl) = slog {
        sl.log(LogEvent::SessionStop {
            t_ms: unix_ms(),
            slides_saved,
            slides_deduped,
        });
    }

    if let Some(handle) = audio_handle {
        match handle.stop() {
            Ok(trailing) => {
                if !trailing.is_empty() {
                    eprintln!(
                        "pageflip: audio finalised with {} trailing segments.",
                        trailing.len()
                    );
                }
            }
            Err(e) => eprintln!("pageflip: audio stop returned error: {e}"),
        }
    }

    eprintln!("pageflip: stopped.");
    Ok(())
}

enum SaveResult {
    Saved,
    Deduped,
}

fn start_audio_capture(
    kind: &str,
    output_dir: &Path,
    whisperx_model: Option<String>,
    diarize: bool,
) -> Result<AudioCaptureHandle, String> {
    let transcriber: Box<dyn Transcriber> = match kind {
        "null" => Box::new(NullTranscriber::default()),
        "whisperx" => if diarize {
            audio::whisperx_transcriber_with_diarize(output_dir.to_path_buf(), whisperx_model, true)
        } else {
            audio::whisperx_transcriber(output_dir.to_path_buf(), whisperx_model)
        }
        .map_err(|e| e.to_string())?,
        other => {
            return Err(format!(
                "--transcriber {other:?} not supported; options: `null`, `whisperx`"
            ));
        }
    };
    let handle = audio::start_capture(transcriber).map_err(|e| e.to_string())?;
    eprintln!("pageflip: audio capture running (transcriber={kind}); raw audio stays in-process");
    Ok(handle)
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

#[allow(clippy::too_many_arguments)]
fn capture_once(
    backend: &dyn Capture,
    target: &Target,
    dedup: &mut Dedup,
    redact: &Option<RedactPipeline>,
    paranoid: Option<&ParanoidSaver>,
    output_dir: &Path,
    sink: Option<&mut EventSink>,
    slog: Option<&mut SessionLog>,
    threshold: u32,
) -> Result<SaveResult, String> {
    let t_start_ms = unix_ms();

    let frame = match target {
        Target::Region(r) => backend.capture(*r),
        Target::Window(spec) => backend.capture_window(spec),
        Target::WindowCropped(spec, crop) => backend.capture_window_cropped(spec, crop),
    }
    .map_err(|e| e.to_string())?;

    let (phash_opt, dist) = dedup.classify_detail(&frame);
    let phash_hex = match phash_opt {
        Some(h) => h,
        None => {
            if let Some(sl) = slog {
                sl.log(LogEvent::SlideDeduped {
                    t_ms: t_start_ms,
                    dist: dist.unwrap_or(0),
                    threshold,
                });
            }
            return Ok(SaveResult::Deduped);
        }
    };
    let _ = dist;

    let slug = timestamp_slug();

    // Paranoid mode: save the unredacted frame before redaction is applied.
    if let Some(saver) = paranoid {
        saver
            .save_raw(&frame, &slug)
            .map_err(|e| format!("paranoid save: {e}"))?;
    }

    let frame = match redact {
        Some(pipeline) => pipeline.apply(frame).map_err(|e| e.to_string())?,
        None => frame,
    };
    let filename = format!("{slug}.png");
    let path = output_dir.join(&filename);
    frame.save_png(&path).map_err(|e| e.to_string())?;

    let t_end_ms = unix_ms();

    let absolute = path
        .canonicalize()
        .unwrap_or_else(|_| output_dir.join(path.file_name().unwrap()));

    // Session log: record the save with a path hash (never the raw path).
    if let Some(sl) = slog {
        let bytes = std::fs::metadata(&absolute).map(|m| m.len()).unwrap_or(0);
        let phash = session_log::path_hash(&absolute, &sl.salt.clone());
        sl.log(LogEvent::SlideSaved {
            t_ms: t_end_ms,
            slide_id: slug.clone(),
            bytes,
            path_hash: phash,
        });
    }

    if let Some(sink) = sink {
        // --events-out mode: emit structured NDJSON. Downstream can derive
        // the plain path from ev.path, so we do not also write the legacy line.
        let ev = SlideEvent {
            slide_id: slug,
            path: absolute,
            t_start_ms,
            t_end_ms,
            phash: Some(phash_hex.clone()),
            ocr: None,
            transcript_window: None,
            frontmost_app: None,
        };
        sink.write(&ev)
            .map_err(|e| format!("events-out write failed: {e}"))?;
    } else {
        // Legacy contract: one absolute path per line on stdout.
        // The downstream feeder (🎯T5) relies on this.
        let mut stdout = io::stdout().lock();
        writeln!(stdout, "{}", absolute.display())
            .and_then(|_| stdout.flush())
            .map_err(|e| format!("stdout write failed: {e}"))?;
    }
    Ok(SaveResult::Saved)
}

/// Returns milliseconds since the Unix epoch.
fn unix_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

fn build_redact_pipeline(cli: &Cli) -> Option<RedactPipeline> {
    if !cli.redact_faces && !cli.redact_pii {
        return None;
    }
    let mut pipeline = RedactPipeline::new();
    if cli.redact_faces {
        pipeline.push(Box::new(FaceBlurRedactor::default()));
    }
    if cli.redact_pii {
        pipeline.push(Box::new(redact::TextPiiRedactor));
    }
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
