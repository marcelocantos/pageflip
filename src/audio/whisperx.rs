// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! WhisperX-backed transcriber — batch / post-hoc mode.
//!
//! `consume()` accumulates samples in memory; `finalise()` pipes them to
//! `scripts/transcribe.py` (via uv) and returns the parsed segments.
//!
//! # Architectural invariant
//! Raw audio bytes are buffered in memory only — they are never written to
//! disk. The only path from buffer to substrate is the stdin pipe opened in
//! `finalise()`. See the module-level doc-comment in `mod.rs` for the full
//! policy.

use std::io::Write as _;
use std::path::PathBuf;
use std::process::{Command, Stdio};

use serde::Deserialize;

use super::{AudioSamples, Segment, TranscribeError, Transcriber};

/// Segment as emitted by `transcribe.py` NDJSON.
#[derive(Deserialize)]
struct RawSegment {
    start_ms: u64,
    end_ms: u64,
    text: String,
}

/// A post-hoc transcriber that buffers all audio in memory and runs
/// faster-whisper via a Python subprocess on `finalise()`.
pub struct WhisperxTranscriber {
    /// SAFETY: samples buffered in memory only — never written to disk.
    buffer: Vec<f32>,
    sample_rate: Option<u32>,
    channels: Option<u16>,
    model: String,
    output_dir: PathBuf,
}

impl WhisperxTranscriber {
    pub fn new(output_dir: PathBuf, model: Option<String>) -> Self {
        Self {
            buffer: Vec::new(),
            sample_rate: None,
            channels: None,
            model: model.unwrap_or_else(|| "large-v3".to_string()),
            output_dir,
        }
    }

    /// Locate `scripts/transcribe.py` relative to the current executable or
    /// the current working directory (useful in tests and development).
    fn script_path() -> PathBuf {
        // First try next to the executable (release layout).
        if let Ok(exe) = std::env::current_exe() {
            let candidate = exe
                .parent()
                .unwrap_or(std::path::Path::new("."))
                .join("scripts/transcribe.py");
            if candidate.exists() {
                return candidate;
            }
        }
        // Fall back to cwd (dev / test layout where cwd is the repo root).
        PathBuf::from("scripts/transcribe.py")
    }

    fn spawn_subprocess(
        &self,
        sample_rate: u32,
        channels: u16,
    ) -> Result<std::process::Child, TranscribeError> {
        let script = Self::script_path();
        Command::new("uv")
            .args([
                "run",
                "--with",
                "faster-whisper",
                "--with",
                "scipy",
                "python",
                script.to_str().unwrap_or("scripts/transcribe.py"),
                "--sample-rate",
                &sample_rate.to_string(),
                "--channels",
                &channels.to_string(),
                "--model",
                &self.model,
            ])
            .env("HF_HUB_OFFLINE", "1")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|e| TranscribeError::ProcessingFailed(format!("failed to spawn uv: {e}")))
    }

    fn parse_segments(stdout: &[u8]) -> Vec<Segment> {
        stdout
            .split(|&b| b == b'\n')
            .filter(|line| !line.is_empty())
            .filter_map(|line| {
                serde_json::from_slice::<RawSegment>(line)
                    .ok()
                    .map(|r| Segment {
                        start_ms: r.start_ms,
                        end_ms: r.end_ms,
                        text: r.text,
                        speaker_id: None,
                    })
            })
            .collect()
    }

    fn write_transcript(&self, segments: &[Segment]) -> Result<(), TranscribeError> {
        std::fs::create_dir_all(&self.output_dir).map_err(|e| {
            TranscribeError::ProcessingFailed(format!(
                "could not create output dir {}: {e}",
                self.output_dir.display()
            ))
        })?;
        let path = self.output_dir.join("transcript.jsonl");
        let mut f = std::fs::File::create(&path).map_err(|e| {
            TranscribeError::ProcessingFailed(format!("could not create {}: {e}", path.display()))
        })?;
        for seg in segments {
            let line = serde_json::json!({
                "start_ms": seg.start_ms,
                "end_ms": seg.end_ms,
                "text": seg.text,
            });
            writeln!(f, "{}", line).map_err(|e| {
                TranscribeError::ProcessingFailed(format!(
                    "write to {} failed: {e}",
                    path.display()
                ))
            })?;
        }
        Ok(())
    }
}

impl Transcriber for WhisperxTranscriber {
    fn consume(&mut self, samples: AudioSamples) -> Result<Vec<Segment>, TranscribeError> {
        let rate = samples.sample_rate();
        let channels = samples.channels();

        match (self.sample_rate, self.channels) {
            (None, None) => {
                self.sample_rate = Some(rate);
                self.channels = Some(channels);
            }
            (Some(r), Some(c)) if r == rate && c == channels => {}
            (Some(r), Some(c)) => {
                return Err(TranscribeError::ProcessingFailed(format!(
                    "audio format changed mid-session: \
                     expected {r} Hz / {c} ch, got {rate} Hz / {channels} ch"
                )));
            }
            _ => unreachable!("sample_rate and channels are always set together"),
        }

        // SAFETY: samples buffered in memory only — never written to disk.
        self.buffer.extend_from_slice(samples.samples());
        Ok(vec![])
    }

    fn finalise(&mut self) -> Result<Vec<Segment>, TranscribeError> {
        if self.buffer.is_empty() {
            return Ok(vec![]);
        }

        let sample_rate = self.sample_rate.unwrap_or(48_000);
        let channels = self.channels.unwrap_or(1);

        let mut child = self.spawn_subprocess(sample_rate, channels)?;

        // Write buffered f32-LE samples to the subprocess's stdin, then close.
        {
            let stdin = child.stdin.take().expect("stdin was piped");
            let mut stdin = stdin;
            // SAFETY: samples buffered in memory only — writing to a pipe,
            // not a disk file.
            let bytes: &[u8] = bytemuck::cast_slice(&self.buffer);
            stdin.write_all(bytes).map_err(|e| {
                TranscribeError::ProcessingFailed(format!("writing samples to subprocess: {e}"))
            })?;
            // stdin is dropped here, signalling EOF to the subprocess.
        }

        let output = child.wait_with_output().map_err(|e| {
            TranscribeError::ProcessingFailed(format!("waiting for subprocess: {e}"))
        })?;

        if !output.status.success() {
            let tail = String::from_utf8_lossy(&output.stderr)
                .lines()
                .rev()
                .take(10)
                .collect::<Vec<_>>()
                .into_iter()
                .rev()
                .collect::<Vec<_>>()
                .join("\n");
            return Err(TranscribeError::ProcessingFailed(format!(
                "transcribe.py exited {:?}:\n{tail}",
                output.status.code()
            )));
        }

        let segments = Self::parse_segments(&output.stdout);
        self.write_transcript(&segments)?;
        Ok(segments)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn make_transcriber() -> WhisperxTranscriber {
        WhisperxTranscriber::new(PathBuf::from("/tmp/pf_test_output"), None)
    }

    fn audio(samples: Vec<f32>, rate: u32, channels: u16) -> AudioSamples {
        AudioSamples::new(samples, rate, channels, 0)
    }

    // -----------------------------------------------------------------------
    // Buffer accumulation
    // -----------------------------------------------------------------------

    #[test]
    fn consume_buffers_samples() {
        let mut t = make_transcriber();
        let segs = t.consume(audio(vec![0.1, 0.2, 0.3], 48_000, 1)).unwrap();
        assert!(segs.is_empty(), "consume must return no live segments");
        assert_eq!(t.buffer.len(), 3);
        assert_eq!(t.sample_rate, Some(48_000));
        assert_eq!(t.channels, Some(1));
    }

    #[test]
    fn consume_accumulates_across_calls() {
        let mut t = make_transcriber();
        t.consume(audio(vec![1.0; 100], 16_000, 2)).unwrap();
        t.consume(audio(vec![2.0; 50], 16_000, 2)).unwrap();
        assert_eq!(t.buffer.len(), 150);
    }

    // -----------------------------------------------------------------------
    // Format mismatch detection
    // -----------------------------------------------------------------------

    #[test]
    fn consume_rejects_sample_rate_change() {
        let mut t = make_transcriber();
        t.consume(audio(vec![0.0; 10], 48_000, 1)).unwrap();
        let err = t.consume(audio(vec![0.0; 10], 44_100, 1)).unwrap_err();
        match err {
            TranscribeError::ProcessingFailed(msg) => {
                assert!(msg.contains("format changed"), "unexpected message: {msg}");
            }
            other => panic!("expected ProcessingFailed, got {other:?}"),
        }
    }

    #[test]
    fn consume_rejects_channel_change() {
        let mut t = make_transcriber();
        t.consume(audio(vec![0.0; 10], 48_000, 2)).unwrap();
        let err = t.consume(audio(vec![0.0; 10], 48_000, 1)).unwrap_err();
        match err {
            TranscribeError::ProcessingFailed(msg) => {
                assert!(msg.contains("format changed"), "unexpected message: {msg}");
            }
            other => panic!("expected ProcessingFailed, got {other:?}"),
        }
    }

    // -----------------------------------------------------------------------
    // Segment parsing
    // -----------------------------------------------------------------------

    #[test]
    fn parse_segments_handles_valid_ndjson() {
        let ndjson = b"{\"start_ms\":0,\"end_ms\":1200,\"text\":\"Hello world\"}\n\
                       {\"start_ms\":1200,\"end_ms\":2500,\"text\":\"Second line\"}\n";
        let segs = WhisperxTranscriber::parse_segments(ndjson);
        assert_eq!(segs.len(), 2);
        assert_eq!(segs[0].start_ms, 0);
        assert_eq!(segs[0].end_ms, 1200);
        assert_eq!(segs[0].text, "Hello world");
        assert!(segs[0].speaker_id.is_none());
        assert_eq!(segs[1].text, "Second line");
    }

    #[test]
    fn parse_segments_skips_malformed_lines() {
        let ndjson = b"{\"start_ms\":0,\"end_ms\":500,\"text\":\"ok\"}\nnot-json\n";
        let segs = WhisperxTranscriber::parse_segments(ndjson);
        assert_eq!(segs.len(), 1);
        assert_eq!(segs[0].text, "ok");
    }

    #[test]
    fn parse_segments_empty_input() {
        let segs = WhisperxTranscriber::parse_segments(b"");
        assert!(segs.is_empty());
    }

    // -----------------------------------------------------------------------
    // finalise on empty buffer
    // -----------------------------------------------------------------------

    #[test]
    fn finalise_on_empty_buffer_returns_empty() {
        let mut t = make_transcriber();
        let segs = t.finalise().unwrap();
        assert!(segs.is_empty());
    }
}
