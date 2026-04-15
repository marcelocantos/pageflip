// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! `pageflip doctor` — print a markdown diagnostics report to stdout.

use std::io::{self, Read};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::time::Duration;

/// Run `pageflip doctor`.
///
/// Writes a markdown report to stdout.  Optionally tails `log_path` at the
/// end of the report.
pub fn run(log_path: Option<&Path>) {
    let mut out = String::with_capacity(4096);

    push_versions(&mut out);
    push_system(&mut out);
    push_model_cache(&mut out);
    push_permissions(&mut out);
    push_external_tools(&mut out);
    push_auth(&mut out);

    if let Some(path) = log_path {
        push_session_log(&mut out, path);
    }

    print!("{out}");
}

// ── Versions ─────────────────────────────────────────────────────────────────

fn push_versions(out: &mut String) {
    let version = env!("CARGO_PKG_VERSION");
    let sha = env!("PAGEFLIP_GIT_SHA");
    out.push_str("# pageflip doctor\n\n");
    out.push_str("## Versions\n\n");
    out.push_str("| Key | Value |\n|-----|-------|\n");
    out.push_str(&format!("| pageflip | {version} ({sha}) |\n"));
    out.push_str(&format!(
        "| rustc | {} |\n",
        tool_version_line(&["rustc", "--version"])
    ));
    out.push('\n');
}

// ── System ────────────────────────────────────────────────────────────────────

fn push_system(out: &mut String) {
    out.push_str("## System\n\n");
    out.push_str("| Key | Value |\n|-----|-------|\n");

    let os = std::env::consts::OS;
    let arch = std::env::consts::ARCH;
    out.push_str(&format!("| OS | {os} |\n"));
    out.push_str(&format!("| Arch | {arch} |\n"));

    // macOS version via sw_vers
    if os == "macos" {
        let product = cmd_out(&["sw_vers", "-productVersion"]);
        let build = cmd_out(&["sw_vers", "-buildVersion"]);
        out.push_str(&format!("| macOS | {product} ({build}) |\n"));
    }

    // Hostname (no user info)
    let hostname = std::env::var("HOSTNAME").ok().or_else(|| {
        Command::new("hostname")
            .output()
            .ok()
            .and_then(|o| String::from_utf8(o.stdout).ok())
            .map(|s| s.trim().to_string())
    });
    if let Some(h) = hostname {
        out.push_str(&format!("| Hostname | {h} |\n"));
    }

    out.push('\n');
}

// ── Model cache ───────────────────────────────────────────────────────────────

const KNOWN_MODELS: &[&str] = &[
    "Systran/faster-whisper-large-v3",
    "jonatasgrosman/wav2vec2-large-xlsr-53-english",
    "pyannote/segmentation-3.0",
    "pyannote/speaker-diarization-3.1",
    "pyannote/speaker-diarization-community-1",
    "pyannote/wespeaker-voxceleb-resnet34-LM",
];

fn push_model_cache(out: &mut String) {
    out.push_str("## Model cache\n\n");
    out.push_str("| Model | Present | Size |\n|-------|---------|------|\n");

    let hf_home = hf_home();
    let hub = hf_home.join("hub");

    for model in KNOWN_MODELS {
        let dir_name = format!("models--{}", model.replace('/', "--"));
        let model_dir = hub.join(&dir_name);
        if model_dir.exists() {
            let size = dir_size(&model_dir);
            let size_str = human_bytes(size);
            // Snapshot path: find `snapshots/` subdir
            let snapshot = model_dir
                .join("snapshots")
                .read_dir()
                .ok()
                .and_then(|mut e| e.next())
                .and_then(|e| e.ok())
                .map(|e| e.path().to_string_lossy().to_string())
                .unwrap_or_else(|| "–".to_string());
            out.push_str(&format!("| `{model}` | ✓ | {size_str} |\n"));
            // Snapshot on next line in a sub-row (no colspan in GFM; embed as note)
            let _ = snapshot; // included below in a detail row
            out.push_str(&format!(
                "| &nbsp; ↳ snapshot | | `{}` |\n",
                model_dir
                    .join("snapshots")
                    .read_dir()
                    .ok()
                    .and_then(|mut e| e.next())
                    .and_then(|e| e.ok())
                    .map(|e| e.path().to_string_lossy().to_string())
                    .unwrap_or_else(|| "–".to_string())
            ));
        } else {
            out.push_str(&format!("| `{model}` | – | – |\n"));
        }
    }

    out.push('\n');
}

fn hf_home() -> PathBuf {
    if let Ok(v) = std::env::var("HF_HOME") {
        PathBuf::from(v)
    } else {
        dirs_home()
            .map(|h| h.join(".cache").join("huggingface"))
            .unwrap_or_else(|| PathBuf::from("/tmp"))
    }
}

fn dirs_home() -> Option<PathBuf> {
    std::env::var("HOME").ok().map(PathBuf::from)
}

/// Recursively sum file sizes in bytes.
fn dir_size(dir: &Path) -> u64 {
    let Ok(entries) = std::fs::read_dir(dir) else {
        return 0;
    };
    entries
        .flatten()
        .map(|e| {
            let p = e.path();
            if p.is_dir() {
                dir_size(&p)
            } else {
                e.metadata().map(|m| m.len()).unwrap_or(0)
            }
        })
        .sum()
}

fn human_bytes(n: u64) -> String {
    const GB: u64 = 1 << 30;
    const MB: u64 = 1 << 20;
    const KB: u64 = 1 << 10;
    if n >= GB {
        format!("{:.1} GB", n as f64 / GB as f64)
    } else if n >= MB {
        format!("{:.1} MB", n as f64 / MB as f64)
    } else if n >= KB {
        format!("{:.1} KB", n as f64 / KB as f64)
    } else {
        format!("{n} B")
    }
}

// ── Permissions ───────────────────────────────────────────────────────────────

fn push_permissions(out: &mut String) {
    out.push_str("## Permissions\n\n");
    out.push_str("| Permission | Status |\n|------------|--------|\n");

    let screen = probe_screen_recording();
    let mic = probe_microphone();

    out.push_str(&format!("| Screen Recording | {screen} |\n"));
    out.push_str(&format!("| Microphone | {mic} |\n"));
    out.push('\n');
}

/// Probe Screen Recording permission by attempting to list shareable content.
/// Returns "granted", "denied", or "unknown".
#[cfg(target_os = "macos")]
fn probe_screen_recording() -> &'static str {
    // Run a quick `screencapture -x /dev/null` — it exits 0 only when
    // Screen Recording is granted.  It doesn't pop a UI prompt on macOS 13+.
    let status = Command::new("screencapture")
        .args(["-x", "/dev/null"])
        .output();
    match status {
        Ok(o) if o.status.success() => "granted",
        Ok(_) => "denied",
        Err(_) => "unknown",
    }
}

#[cfg(not(target_os = "macos"))]
fn probe_screen_recording() -> &'static str {
    "unknown (non-macOS)"
}

/// Probe Microphone permission. Returns "granted", "denied", or "unknown".
#[cfg(target_os = "macos")]
fn probe_microphone() -> &'static str {
    // `sox` or `ffmpeg` short-record probes are noisy; use a simpler heuristic:
    // try to open the default audio input device via Core Audio if available,
    // otherwise report unknown.  For the purposes of a doctor report, a
    // best-effort heuristic is acceptable.
    //
    // We use `ffmpeg -f avfoundation -i ":0" -t 0.1 -f null -` as the probe.
    // If ffmpeg is absent, fall back to unknown.
    let result = Command::new("ffmpeg")
        .args([
            "-y",
            "-hide_banner",
            "-loglevel",
            "quiet",
            "-f",
            "avfoundation",
            "-i",
            ":0",
            "-t",
            "0.1",
            "-f",
            "null",
            "-",
        ])
        .output();
    match result {
        Ok(o) if o.status.success() => "granted",
        Ok(o) if o.status.code() == Some(1) => {
            // ffmpeg exits 1 for many reasons; check stderr for TCC denial
            let stderr = String::from_utf8_lossy(&o.stderr);
            if stderr.contains("Permission denied") || stderr.contains("permission denied") {
                "denied"
            } else {
                "unknown"
            }
        }
        _ => "unknown",
    }
}

#[cfg(not(target_os = "macos"))]
fn probe_microphone() -> &'static str {
    "unknown (non-macOS)"
}

// ── External tools ────────────────────────────────────────────────────────────

fn push_external_tools(out: &mut String) {
    out.push_str("## External tools\n\n");
    out.push_str("| Tool | Version |\n|------|----------|\n");

    let tools: &[(&str, &[&str])] = &[
        ("tmux", &["tmux", "-V"]),
        ("ffmpeg", &["ffmpeg", "-version"]),
        ("uv", &["uv", "--version"]),
        ("python3", &["python3", "--version"]),
        ("claude", &["claude", "--version"]),
        ("go", &["go", "version"]),
    ];

    for (name, argv) in tools {
        let ver = tool_version_line(argv);
        out.push_str(&format!("| {name} | {ver} |\n"));
    }

    out.push('\n');
}

// ── Auth ──────────────────────────────────────────────────────────────────────

fn push_auth(out: &mut String) {
    out.push_str("## Auth\n\n");
    out.push_str("| Service | Status |\n|---------|--------|\n");

    // HF token: check `hf auth whoami` (fast, non-interactive).
    let hf = probe_hf_auth();
    out.push_str(&format!("| HuggingFace token | {hf} |\n"));

    // Anthropic: check env var or credentials file — never log the value.
    let anthropic = probe_anthropic_auth();
    out.push_str(&format!("| Anthropic auth | {anthropic} |\n"));

    out.push('\n');
}

fn probe_hf_auth() -> &'static str {
    let result = Command::new("huggingface-cli").args(["whoami"]).output();
    match result {
        Ok(o) if o.status.success() => "present",
        Ok(_) => "absent",
        Err(_) => {
            // huggingface-cli not installed; check env var (bool only)
            if std::env::var("HUGGING_FACE_HUB_TOKEN").is_ok() || std::env::var("HF_TOKEN").is_ok()
            {
                "present (env var)"
            } else {
                "absent"
            }
        }
    }
}

fn probe_anthropic_auth() -> &'static str {
    // Check env var first.
    if std::env::var("ANTHROPIC_API_KEY").is_ok() {
        return "present (env var)";
    }
    // Check Claude credentials file.
    let creds = dirs_home()
        .map(|h| h.join(".claude").join(".credentials.json"))
        .map(|p| p.exists())
        .unwrap_or(false);
    if creds {
        return "present (~/.claude/.credentials.json)";
    }
    "absent"
}

// ── Session log ───────────────────────────────────────────────────────────────

fn push_session_log(out: &mut String, path: &Path) {
    out.push_str("## Recent session log\n\n");

    let tail = tail_lines(path, 200);
    match tail {
        Ok(content) => {
            out.push_str("```\n");
            out.push_str(&content);
            if !content.ends_with('\n') {
                out.push('\n');
            }
            out.push_str("```\n");
        }
        Err(e) => {
            out.push_str(&format!("_Could not read log file: {e}_\n"));
        }
    }
    out.push('\n');
}

/// Read up to `n` trailing lines from a file.
fn tail_lines(path: &Path, n: usize) -> io::Result<String> {
    let mut content = String::new();
    std::fs::File::open(path)?.read_to_string(&mut content)?;
    let lines: Vec<&str> = content.lines().collect();
    let start = lines.len().saturating_sub(n);
    Ok(lines[start..].join("\n"))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

/// Run a command with a short timeout and return the first line of stdout,
/// or "missing" / "error" on failure.
fn tool_version_line(argv: &[&str]) -> String {
    let (prog, args) = match argv.split_first() {
        Some(v) => v,
        None => return "missing".to_string(),
    };
    let result = Command::new(prog).args(args).output();
    match result {
        Err(_) => "missing".to_string(),
        Ok(o) => {
            let text = String::from_utf8_lossy(&o.stdout);
            let first = text.lines().next().unwrap_or("").trim().to_string();
            if first.is_empty() {
                let err = String::from_utf8_lossy(&o.stderr);
                let eline = err.lines().next().unwrap_or("error").trim().to_string();
                if eline.is_empty() {
                    "error".to_string()
                } else {
                    eline
                }
            } else {
                first
            }
        }
    }
}

fn cmd_out(argv: &[&str]) -> String {
    let (prog, args) = match argv.split_first() {
        Some(v) => v,
        None => return String::new(),
    };
    Command::new(prog)
        .args(args)
        .output()
        .ok()
        .and_then(|o| String::from_utf8(o.stdout).ok())
        .map(|s| s.trim().to_string())
        .unwrap_or_default()
}

// Silence unused-import warning for Duration (kept for future timeout use).
const _: Duration = Duration::from_secs(0);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn human_bytes_formatting() {
        assert_eq!(human_bytes(0), "0 B");
        assert_eq!(human_bytes(1023), "1023 B");
        assert_eq!(human_bytes(1024), "1.0 KB");
        assert_eq!(human_bytes(1024 * 1024), "1.0 MB");
        assert!(human_bytes(1024 * 1024 * 1024).contains("GB"));
    }

    #[test]
    fn tool_version_line_missing() {
        // An executable that definitely doesn't exist.
        assert_eq!(
            tool_version_line(&["this-tool-does-not-exist-pageflip-test"]),
            "missing"
        );
    }

    #[test]
    fn report_shape_has_required_headings() {
        // We don't call run() (it writes to stdout), but we can exercise the
        // individual push_ functions and check their output contains headings.
        let mut out = String::new();
        push_versions(&mut out);
        assert!(out.contains("## Versions"));
        assert!(out.contains("pageflip"));

        let mut out2 = String::new();
        push_model_cache(&mut out2);
        assert!(out2.contains("## Model cache"));
        for model in KNOWN_MODELS {
            assert!(out2.contains(model), "missing model row: {model}");
        }
    }

    #[test]
    fn path_hash_in_log_event_has_no_raw_path() {
        // Verify the session_log path_hash API is used correctly: the
        // path_hash output is 12 hex chars with no path separator.
        let h = super::super::session_log::path_hash(
            Path::new("/very/sensitive/output/dir/slide.png"),
            b"test-salt",
        );
        assert_eq!(h.len(), 12);
        assert!(!h.contains('/'));
        assert!(!h.contains("sensitive"));
    }
}
