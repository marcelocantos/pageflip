// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Structured slide-event NDJSON output for the --events-out flag.

use std::fs::File;
use std::io::{self, BufWriter, Write};
use std::os::unix::io::{FromRawFd, RawFd};
use std::path::PathBuf;

use serde::Serialize;

/// A bounding box for an OCR word: [x, y, width, height] in normalised 0–1 coords.
#[derive(Debug, Clone, Serialize)]
pub struct OcrWord {
    pub text: String,
    pub bbox: [f32; 4],
}

/// A transcript segment (stub; populated by 🎯T9.2).
#[derive(Debug, Clone, Serialize)]
pub struct Segment {
    pub text: String,
    pub start_ms: u64,
    pub end_ms: u64,
}

/// One event emitted to --events-out per saved slide.
#[derive(Debug, Clone, Serialize)]
pub struct SlideEvent {
    pub slide_id: String,
    pub path: PathBuf,
    pub t_start_ms: u64,
    pub t_end_ms: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ocr: Option<Vec<OcrWord>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub transcript_window: Option<Vec<Segment>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub frontmost_app: Option<String>,
}

/// Destination for slide events.
pub enum EventSink {
    Stdout(BufWriter<io::Stdout>),
    File(BufWriter<File>),
    Fd(BufWriter<File>),
}

impl EventSink {
    pub fn write(&mut self, ev: &SlideEvent) -> io::Result<()> {
        let json = serde_json::to_string(ev).map_err(io::Error::other)?;
        match self {
            EventSink::Stdout(w) => {
                writeln!(w, "{json}")?;
                w.flush()
            }
            EventSink::File(w) => {
                writeln!(w, "{json}")?;
                w.flush()
            }
            EventSink::Fd(w) => {
                writeln!(w, "{json}")?;
                w.flush()
            }
        }
    }
}

/// Parse an `--events-out` spec:
/// - `"stdout"` → stdout
/// - `"fd:<N>"` → file descriptor N
/// - anything else → file path
///
/// # Errors
/// Returns a human-readable error if the spec is malformed or the target
/// cannot be opened.
pub fn parse_events_out(spec: &str) -> Result<EventSink, String> {
    if spec == "stdout" {
        return Ok(EventSink::Stdout(BufWriter::new(io::stdout())));
    }
    if let Some(rest) = spec.strip_prefix("fd:") {
        let fd: RawFd = rest
            .parse::<i32>()
            .map_err(|_| format!("--events-out fd:{rest:?}: not a valid file descriptor number"))?;
        if fd < 0 {
            return Err(format!(
                "--events-out fd:{fd}: file descriptor must be non-negative"
            ));
        }
        // SAFETY: The caller controls the fd spec. We trust it is open and
        // writable; if not, the first write will return an error rather than UB.
        let file = unsafe { File::from_raw_fd(fd) };
        return Ok(EventSink::Fd(BufWriter::new(file)));
    }
    let file = File::create(spec)
        .map_err(|e| format!("--events-out {spec:?}: could not create file: {e}"))?;
    Ok(EventSink::File(BufWriter::new(file)))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn base_event() -> SlideEvent {
        SlideEvent {
            slide_id: "20260415T130050Z".to_string(),
            path: PathBuf::from("/tmp/test/20260415T130050Z.png"),
            t_start_ms: 1_000,
            t_end_ms: 1_050,
            ocr: None,
            transcript_window: None,
            frontmost_app: None,
        }
    }

    #[test]
    fn required_fields_round_trip() {
        let ev = base_event();
        let json = serde_json::to_string(&ev).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed["slide_id"], "20260415T130050Z");
        assert_eq!(parsed["t_start_ms"], 1_000_u64);
        assert_eq!(parsed["t_end_ms"], 1_050_u64);
        assert_eq!(parsed["path"], "/tmp/test/20260415T130050Z.png");
    }

    #[test]
    fn optional_fields_omitted_when_none() {
        let ev = base_event();
        let json = serde_json::to_string(&ev).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();
        assert!(parsed.get("ocr").is_none());
        assert!(parsed.get("transcript_window").is_none());
        assert!(parsed.get("frontmost_app").is_none());
    }

    #[test]
    fn optional_fields_present_when_set() {
        let mut ev = base_event();
        ev.frontmost_app = Some("Keynote".to_string());
        ev.ocr = Some(vec![OcrWord {
            text: "Hello".to_string(),
            bbox: [0.1, 0.2, 0.3, 0.4],
        }]);
        let json = serde_json::to_string(&ev).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed["frontmost_app"], "Keynote");
        assert_eq!(parsed["ocr"][0]["text"], "Hello");
    }

    #[test]
    fn parse_events_out_stdout() {
        assert!(matches!(
            parse_events_out("stdout"),
            Ok(EventSink::Stdout(_))
        ));
    }

    #[test]
    fn parse_events_out_fd_valid() {
        // Create a temporary file and use its fd so we don't close a
        // well-known fd (e.g. stdout) when the EventSink is dropped.
        let dir = std::env::temp_dir();
        let path = dir.join("pageflip_test_fd_valid.ndjson");
        let f = File::create(&path).expect("temp file");
        let fd = {
            use std::os::unix::io::IntoRawFd;
            f.into_raw_fd()
        };
        // parse_events_out will wrap the fd; drop closes it.
        assert!(matches!(
            parse_events_out(&format!("fd:{fd}")),
            Ok(EventSink::Fd(_))
        ));
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn parse_events_out_fd_not_a_number() {
        assert!(parse_events_out("fd:nope").is_err());
    }

    #[test]
    fn parse_events_out_fd_negative() {
        assert!(parse_events_out("fd:-1").is_err());
    }

    #[test]
    fn parse_events_out_file_path() {
        let dir = std::env::temp_dir();
        let path = dir.join("pageflip_test_events_out.ndjson");
        let spec = path.to_string_lossy().to_string();
        assert!(matches!(parse_events_out(&spec), Ok(EventSink::File(_))));
        let _ = std::fs::remove_file(&path);
    }
}
