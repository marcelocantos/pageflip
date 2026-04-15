// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Structured session log for bug reports.
//!
//! All types are designed to be sensitivity-free by construction: no window
//! titles, no OCR text, no transcript text, no display names. Only numeric,
//! enum, or hash values that carry zero personal information.

use std::fmt;
use std::fs::File;
use std::io::{self, BufWriter, Write};
use std::path::Path;

use serde::Serialize;
use sha2::{Digest, Sha256};

// ── Sensitive-free geometry types ───────────────────────────────────────────

/// Screen-region info: only coords and dimensions (no display name or title).
#[derive(Debug, Clone, Serialize)]
pub struct RegionInfo {
    pub x: i32,
    pub y: i32,
    pub w: u32,
    pub h: u32,
}

/// Window info: only the numeric OS window-id (never a title or app name).
#[derive(Debug, Clone, Serialize)]
pub struct WindowInfo {
    pub window_id: u32,
}

// ── Error codes ──────────────────────────────────────────────────────────────

// Variants are part of the stable log-event schema — emitted only when
// specific failure modes land in the audio path. Dead-code lint isn't the
// right signal here; the variants exist to reserve a shape for downstream
// consumers, not because they're needed at the current call sites.
#[allow(dead_code)]
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum AudioErrorCode {
    PermissionDenied,
    DeviceUnavailable,
    BufferOverrun,
    Other,
}

impl fmt::Display for AudioErrorCode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let s = match self {
            Self::PermissionDenied => "permission_denied",
            Self::DeviceUnavailable => "device_unavailable",
            Self::BufferOverrun => "buffer_overrun",
            Self::Other => "other",
        };
        f.write_str(s)
    }
}

// ── Event enum ───────────────────────────────────────────────────────────────

/// A structured log event. Every variant is sensitive-free by construction.
///
/// No window titles, no OCR text, no transcript text, no specialist output,
/// no frontmost-app display names — only numerics, enums, and hashes.
///
/// Unused variants are reserved for future wiring (🎯T9.2 audio-path events
/// beyond the capture loop, 🎯T10.2 OCR-path events). Leaving them on dead_code
/// keeps the schema stable while the codebase grows into them.
#[allow(dead_code)]
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "event", rename_all = "snake_case")]
pub enum LogEvent {
    SessionStart {
        version: String,
        git_sha: String,
        pid: u32,
        t_ms: u64,
    },
    /// One iteration of the capture loop.
    CaptureTick {
        t_ms: u64,
        #[serde(skip_serializing_if = "Option::is_none")]
        region: Option<RegionInfo>,
        #[serde(skip_serializing_if = "Option::is_none")]
        window: Option<WindowInfo>,
    },
    /// A frame passed dedup and was written to disk.
    SlideSaved {
        t_ms: u64,
        slide_id: String,
        bytes: u64,
        /// SHA-256 of the path, truncated to 12 hex chars, salted per-session.
        /// Stable within a session for correlation; opaque across sessions.
        path_hash: String,
    },
    /// A frame was suppressed by the dedup gate.
    SlideDeduped {
        t_ms: u64,
        dist: u32,
        threshold: u32,
    },
    /// A batch of audio samples was dispatched to the transcriber.
    AudioBatch {
        t_ms: u64,
        samples: u32,
        sample_rate: u32,
        channels: u8,
    },
    /// Audio capture or transcription encountered an error.
    AudioError { t_ms: u64, code: AudioErrorCode },
    /// A redactor was invoked on a frame.
    RedactInvoked {
        t_ms: u64,
        redactor: String,
        faces_detected: u32,
        duration_ms: u64,
    },
    /// Transcription of a batch started.
    TranscribeStart { t_ms: u64, model: String },
    /// Transcription of a batch completed.
    TranscribeEnd {
        t_ms: u64,
        duration_ms: u64,
        segments: u32,
        #[serde(skip_serializing_if = "Option::is_none")]
        error: Option<String>,
    },
    /// The capture session ended.
    SessionStop {
        t_ms: u64,
        slides_saved: u32,
        slides_deduped: u32,
    },
}

// ── Per-session path hasher ──────────────────────────────────────────────────

/// Produces a salted, truncated SHA-256 of a file path.
///
/// `salt` is a random per-session prefix — logs from different sessions
/// cannot be correlated via path hashes even when two sessions write to the
/// same directory.
pub fn path_hash(path: &Path, salt: &[u8]) -> String {
    let mut h = Sha256::new();
    h.update(salt);
    h.update(path.as_os_str().as_encoded_bytes());
    let digest = h.finalize();
    // Truncate to 12 hex chars (48 bits).
    hex_lower(&digest[..6])
}

fn hex_lower(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

// ── SessionLog writer ────────────────────────────────────────────────────────

/// NDJSON session log.  One serialised [`LogEvent`] per line.
pub struct SessionLog {
    writer: BufWriter<File>,
    /// Per-session random salt for path hashing.
    pub salt: Vec<u8>,
}

impl SessionLog {
    /// Open (create or truncate) the log at `path`.
    ///
    /// Generates a fresh random salt for path hashing.
    pub fn open(path: &Path) -> io::Result<Self> {
        let file = File::create(path)?;
        let writer = BufWriter::new(file);
        // Use a timestamp-derived salt — good enough to prevent cross-session
        // path-hash correlation without needing a rand crate.
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default();
        let salt = format!("{}.{}", now.as_secs(), now.subsec_nanos()).into_bytes();
        Ok(Self { writer, salt })
    }

    /// Serialize `event` as NDJSON and flush.
    pub fn log(&mut self, event: LogEvent) {
        // Best-effort: log failures are not fatal.
        if let Ok(json) = serde_json::to_string(&event) {
            let _ = writeln!(self.writer, "{json}");
            let _ = self.writer.flush();
        }
    }
}

// ── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::Value;

    fn t_ms() -> u64 {
        1_000
    }

    #[test]
    fn session_start_serialisation() {
        let ev = LogEvent::SessionStart {
            version: "0.1.0".into(),
            git_sha: "abc1234".into(),
            pid: 42,
            t_ms: t_ms(),
        };
        let v: Value = serde_json::from_str(&serde_json::to_string(&ev).unwrap()).unwrap();
        assert_eq!(v["event"], "session_start");
        assert_eq!(v["version"], "0.1.0");
        assert_eq!(v["git_sha"], "abc1234");
        assert_eq!(v["pid"], 42);
        assert_eq!(v["t_ms"], 1_000_u64);
    }

    #[test]
    fn slide_saved_serialisation() {
        let ev = LogEvent::SlideSaved {
            t_ms: t_ms(),
            slide_id: "20260415T130050Z".into(),
            bytes: 98_304,
            path_hash: "deadbeefcafe".into(),
        };
        let v: Value = serde_json::from_str(&serde_json::to_string(&ev).unwrap()).unwrap();
        assert_eq!(v["event"], "slide_saved");
        assert_eq!(v["bytes"], 98_304_u64);
        assert_eq!(v["path_hash"], "deadbeefcafe");
        // Must NOT have any title / path / sensitive field.
        assert!(v.get("path").is_none());
        assert!(v.get("title").is_none());
    }

    #[test]
    fn session_stop_serialisation() {
        let ev = LogEvent::SessionStop {
            t_ms: t_ms(),
            slides_saved: 5,
            slides_deduped: 12,
        };
        let v: Value = serde_json::from_str(&serde_json::to_string(&ev).unwrap()).unwrap();
        assert_eq!(v["event"], "session_stop");
        assert_eq!(v["slides_saved"], 5);
        assert_eq!(v["slides_deduped"], 12);
    }

    #[test]
    fn capture_tick_optional_fields_omitted_when_none() {
        let ev = LogEvent::CaptureTick {
            t_ms: t_ms(),
            region: None,
            window: None,
        };
        let v: Value = serde_json::from_str(&serde_json::to_string(&ev).unwrap()).unwrap();
        assert!(v.get("region").is_none());
        assert!(v.get("window").is_none());
    }

    #[test]
    fn path_hash_deterministic_within_salt() {
        let path = Path::new("/tmp/pageflip/20260415T130050Z.png");
        let salt = b"session-salt-42";
        let h1 = path_hash(path, salt);
        let h2 = path_hash(path, salt);
        assert_eq!(h1, h2);
        assert_eq!(h1.len(), 12);
    }

    #[test]
    fn path_hash_diverges_across_salts() {
        let path = Path::new("/tmp/pageflip/20260415T130050Z.png");
        let h1 = path_hash(path, b"salt-session-A");
        let h2 = path_hash(path, b"salt-session-B");
        assert_ne!(h1, h2);
    }

    #[test]
    fn no_sensitive_fields_in_window_info() {
        // Compile-time check: WindowInfo has no `title` or `app_name` field.
        let wi = WindowInfo { window_id: 99 };
        let v: Value = serde_json::from_str(&serde_json::to_string(&wi).unwrap()).unwrap();
        assert_eq!(v["window_id"], 99);
        assert!(v.get("title").is_none());
        assert!(v.get("app_name").is_none());
    }
}
