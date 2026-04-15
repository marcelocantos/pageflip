// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::collections::HashSet;
use std::io::{self, BufRead};
use std::path::PathBuf;
use std::process::Command;
use std::sync::mpsc;
use std::time::Duration;

use clap::Parser;
use notify::{Event, EventKind, RecursiveMode, Watcher};
use serde::Deserialize;

const HELP_AGENT: &str = include_str!("help_agent_feed.txt");

#[derive(Parser, Debug)]
#[command(
    name = "pageflip-feed",
    version,
    about = "Watch a directory and feed each new PNG into a Claude Code session",
    long_about = "pageflip-feed watches an output directory for new PNG files and invokes \
                  `claude -p <prompt> --resume <session-id>` for each one. It is the \
                  downstream consumer of the stream produced by `pageflip`."
)]
struct Cli {
    /// Directory to watch for new PNG files (existing directory-watch mode).
    /// Mutually exclusive with --pipe.
    #[arg(long, value_name = "DIR", conflicts_with = "pipe")]
    watch: Option<PathBuf>,

    /// Read slide events from stdin instead of watching a directory.
    /// Accepts either path-per-line (legacy) or NDJSON (--events-out) format;
    /// the format is auto-detected from the first non-empty line.
    /// Mutually exclusive with --watch.
    #[arg(long, conflicts_with = "watch")]
    pipe: bool,

    /// Claude Code session ID to resume. Required; the feeder exits before
    /// starting the watch loop if this is absent.
    #[arg(long, value_name = "ID")]
    session_id: Option<String>,

    /// Prompt template. Use `{path}` as a placeholder for the PNG path.
    #[arg(
        long,
        default_value = "Analyse this slide: @{path}",
        value_name = "TEMPLATE"
    )]
    prompt: String,

    /// Path to the `claude` binary. Default: `claude` on PATH.
    #[arg(long, default_value = "claude", value_name = "PATH")]
    claude: String,

    /// Print agent-oriented help (machine-readable invocation notes) and exit.
    #[arg(long, exclusive = true)]
    help_agent: bool,
}

fn main() -> std::process::ExitCode {
    let cli = Cli::parse();

    if cli.help_agent {
        print!("{HELP_AGENT}");
        return std::process::ExitCode::SUCCESS;
    }

    match run(&cli) {
        Ok(()) => std::process::ExitCode::SUCCESS,
        Err(msg) => {
            eprintln!("pageflip-feed: {msg}");
            std::process::ExitCode::FAILURE
        }
    }
}

/// Minimal subset of SlideEvent needed to extract the path.
#[derive(Deserialize)]
struct SlideEventRef {
    path: PathBuf,
}

fn run(cli: &Cli) -> Result<(), String> {
    let session_id = cli.session_id.as_deref().ok_or(
        "--session-id is required; find your session ID with `claude --list-sessions` \
         and pass it via --session-id <ID>",
    )?;

    if cli.pipe {
        return run_pipe(session_id, &cli.prompt, &cli.claude);
    }

    let watch_dir = cli
        .watch
        .as_ref()
        .ok_or("either --watch <DIR> or --pipe is required")?;

    if !watch_dir.is_dir() {
        return Err(format!(
            "--watch {:?} is not a directory or does not exist",
            watch_dir
        ));
    }

    let (tx, rx) = mpsc::channel::<notify::Result<Event>>();

    let mut watcher = notify::recommended_watcher(move |res| {
        let _ = tx.send(res);
    })
    .map_err(|e| format!("failed to create filesystem watcher: {e}"))?;

    watcher
        .watch(watch_dir, RecursiveMode::NonRecursive)
        .map_err(|e| format!("failed to watch {:?}: {e}", watch_dir))?;

    eprintln!(
        "pageflip-feed: watching {:?} for new PNGs (session {}); Ctrl-C to stop",
        watch_dir, session_id
    );

    // Tracks canonical paths already fed to prevent duplicate events.
    let mut seen: HashSet<PathBuf> = HashSet::new();

    loop {
        match rx.recv_timeout(Duration::from_millis(500)) {
            Ok(Ok(event)) => {
                if is_create_or_modify(&event.kind) {
                    for path in event.paths {
                        if is_new_png(&path, &mut seen) {
                            feed_slide(&path, session_id, &cli.prompt, &cli.claude);
                        }
                    }
                }
            }
            Ok(Err(e)) => {
                // Watcher errors are non-fatal: log and keep going.
                eprintln!("pageflip-feed: watcher error: {e}");
            }
            Err(mpsc::RecvTimeoutError::Timeout) => {
                // Normal idle tick; loop back to check for Ctrl-C via the OS.
            }
            Err(mpsc::RecvTimeoutError::Disconnected) => {
                break;
            }
        }
    }

    Ok(())
}

/// Read lines from stdin. Auto-detect format from the first non-empty line:
/// - starts with `{` → NDJSON (SlideEvent); extract `path`
/// - otherwise → legacy path-per-line
fn run_pipe(session_id: &str, prompt: &str, claude: &str) -> Result<(), String> {
    eprintln!("pageflip-feed: reading from stdin (session {session_id}); Ctrl-D to stop");

    let stdin = io::stdin();
    let mut lines = stdin.lock().lines();

    // Peek at the first non-empty line to detect format.
    let first = loop {
        match lines.next() {
            None => return Ok(()),
            Some(Err(e)) => return Err(format!("stdin read error: {e}")),
            Some(Ok(line)) if line.trim().is_empty() => continue,
            Some(Ok(line)) => break line,
        }
    };

    let is_ndjson = first.trim_start().starts_with('{');

    // Process the first line, then continue with remaining lines.
    let all = std::iter::once(Ok(first)).chain(lines);
    for line in all {
        let line = line.map_err(|e| format!("stdin read error: {e}"))?;
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        let path = if is_ndjson {
            match serde_json::from_str::<SlideEventRef>(trimmed) {
                Ok(ev) => ev.path,
                Err(e) => {
                    eprintln!("pageflip-feed: skipping malformed NDJSON line: {e}");
                    continue;
                }
            }
        } else {
            PathBuf::from(trimmed)
        };
        feed_slide(&path, session_id, prompt, claude);
    }

    Ok(())
}

/// Returns true for event kinds that indicate a file appeared or was written.
fn is_create_or_modify(kind: &EventKind) -> bool {
    matches!(
        kind,
        EventKind::Create(_) | EventKind::Modify(notify::event::ModifyKind::Name(_))
    )
}

/// Returns true when `path` ends in `.png` and has not been seen before.
/// Canonicalises the path to survive symlinks and relative paths.
fn is_new_png(path: &std::path::Path, seen: &mut HashSet<PathBuf>) -> bool {
    if path.extension().and_then(|e| e.to_str()) != Some("png") {
        return false;
    }
    let canonical = path.canonicalize().unwrap_or_else(|_| path.to_path_buf());
    seen.insert(canonical)
}

/// Expands the prompt template and spawns `claude` in the background.
/// Any failure to spawn is logged; it never propagates to the caller so the
/// feeder continues processing subsequent slides.
fn feed_slide(path: &std::path::Path, session_id: &str, prompt_template: &str, claude: &str) {
    let abs = path.canonicalize().unwrap_or_else(|_| path.to_path_buf());
    let prompt = render_prompt(prompt_template, &abs.to_string_lossy());

    eprintln!(
        "pageflip-feed: feeding {} -> session {}",
        abs.display(),
        session_id
    );

    match Command::new(claude)
        .args(["-p", &prompt, "--resume", session_id])
        .spawn()
    {
        Ok(_child) => {
            // Child runs independently; we do not wait for it.
        }
        Err(e) => {
            eprintln!(
                "pageflip-feed: failed to spawn {} for {}: {e}",
                claude,
                abs.display()
            );
        }
    }
}

/// Replaces all occurrences of `{path}` in `template` with `path`.
pub fn render_prompt(template: &str, path: &str) -> String {
    template.replace("{path}", path)
}

#[cfg(test)]
mod tests {
    use super::render_prompt;

    #[test]
    fn render_prompt_substitutes_path() {
        let result = render_prompt("Analyse this slide: @{path}", "/slides/foo.png");
        assert_eq!(result, "Analyse this slide: @/slides/foo.png");
    }

    #[test]
    fn render_prompt_substitutes_multiple_occurrences() {
        let result = render_prompt("See {path} and also {path}", "/a/b.png");
        assert_eq!(result, "See /a/b.png and also /a/b.png");
    }

    #[test]
    fn render_prompt_no_placeholder_is_unchanged() {
        let result = render_prompt("Analyse this slide", "/slides/foo.png");
        assert_eq!(result, "Analyse this slide");
    }

    #[test]
    fn render_prompt_empty_template() {
        let result = render_prompt("", "/slides/foo.png");
        assert_eq!(result, "");
    }

    #[test]
    fn render_prompt_path_with_spaces() {
        let result = render_prompt("@{path}", "/my slides/deck 01.png");
        assert_eq!(result, "@/my slides/deck 01.png");
    }
}
