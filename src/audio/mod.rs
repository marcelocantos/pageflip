// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Audio capture and transcription.
//!
//! # Architectural boundary — mandatory
//!
//! The project owner's legal/policy constraint: captured audio may be USED
//! to derive transcripts in real time, but MUST NEVER be written to disk or
//! transmitted over the network. This module is the only place in the
//! codebase where raw audio samples exist. To make the constraint a
//! compile-time guarantee:
//!
//! - [`AudioSamples`] keeps its fields private. Nothing outside this module
//!   can read, serialize, or persist them.
//! - The only thing that leaves the module is a [`Segment`] — derived
//!   transcript text, never audio.
//! - [`AudioCaptureHandle`] owns the [`Transcriber`] and drives it on a
//!   worker thread; the outer program only receives [`Segment`]s from the
//!   handle's channel.
//!
//! If you find yourself wanting to expose raw PCM outside this module,
//! stop — that would break the invariant the project depends on.

// Many public / pub(super) items here are part of the Transcriber-facing
// API surface for T9.2 (WhisperX) and will be consumed by the meetcat-side
// slide-event stream (T18). They're intentionally defined now so the
// boundary is in place before real consumers arrive.
#![allow(dead_code)]

use std::fmt;
use std::path::PathBuf;
use std::sync::mpsc;
use std::thread::JoinHandle;

#[cfg(target_os = "macos")]
mod capture;
pub mod whisperx;

/// A transcript segment produced by a Transcriber.
#[derive(Debug, Clone)]
pub struct Segment {
    pub start_ms: u64,
    pub end_ms: u64,
    pub text: String,
    pub speaker_id: Option<String>,
}

/// An opaque batch of PCM audio samples passed from the capture backend
/// to a [`Transcriber`]. Fields are deliberately private — see the module
/// doc-comment for why.
pub struct AudioSamples {
    samples: Vec<f32>,
    sample_rate: u32,
    channels: u16,
    capture_start_ms: u64,
}

impl AudioSamples {
    pub(super) fn new(
        samples: Vec<f32>,
        sample_rate: u32,
        channels: u16,
        capture_start_ms: u64,
    ) -> Self {
        Self {
            samples,
            sample_rate,
            channels,
            capture_start_ms,
        }
    }

    pub(super) fn samples(&self) -> &[f32] {
        &self.samples
    }
    pub(super) fn sample_rate(&self) -> u32 {
        self.sample_rate
    }
    pub(super) fn channels(&self) -> u16 {
        self.channels
    }
    pub(super) fn capture_start_ms(&self) -> u64 {
        self.capture_start_ms
    }
}

#[derive(Debug)]
pub enum TranscribeError {
    BackendUnavailable(String),
    ProcessingFailed(String),
}

impl fmt::Display for TranscribeError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::BackendUnavailable(m) => write!(f, "transcriber unavailable: {m}"),
            Self::ProcessingFailed(m) => write!(f, "transcription failed: {m}"),
        }
    }
}

impl std::error::Error for TranscribeError {}

/// Consumer of raw audio samples that produces transcript segments.
///
/// Implementations live inside this module (e.g. [`NullTranscriber`], and
/// in T9.2 a WhisperX-backed subprocess transcriber). `consume` is called
/// on a dedicated worker thread, not the main thread.
pub trait Transcriber: Send + 'static {
    /// Process a batch of samples and return any segments produced.
    fn consume(&mut self, samples: AudioSamples) -> Result<Vec<Segment>, TranscribeError>;

    /// Called once at capture-stop to flush any buffered state.
    fn finalise(&mut self) -> Result<Vec<Segment>, TranscribeError> {
        Ok(Vec::new())
    }
}

/// Transcriber that accepts samples and produces no segments.
///
/// Useful for smoke-testing `--audio` end-to-end without standing up a real
/// Whisper pipeline: the plumbing exercises every step from ScreenCaptureKit
/// through the boundary, and the caller sees a clean zero-segment session.
#[derive(Default)]
pub struct NullTranscriber {
    batches_seen: u64,
    samples_seen: u64,
}

impl Transcriber for NullTranscriber {
    fn consume(&mut self, samples: AudioSamples) -> Result<Vec<Segment>, TranscribeError> {
        self.batches_seen += 1;
        self.samples_seen += samples.samples().len() as u64;
        Ok(Vec::new())
    }

    fn finalise(&mut self) -> Result<Vec<Segment>, TranscribeError> {
        eprintln!(
            "pageflip: NullTranscriber saw {} batches / {} samples.",
            self.batches_seen, self.samples_seen
        );
        Ok(Vec::new())
    }
}

#[derive(Debug)]
pub enum AudioError {
    PermissionDenied(String),
    BackendUnavailable(String),
    BackendFailed(String),
    Transcribe(TranscribeError),
}

impl fmt::Display for AudioError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::PermissionDenied(m) => write!(
                f,
                "screen-recording permission required for audio capture: {m}"
            ),
            Self::BackendUnavailable(m) => write!(f, "audio backend unavailable: {m}"),
            Self::BackendFailed(m) => write!(f, "audio backend failed: {m}"),
            Self::Transcribe(e) => write!(f, "transcriber error: {e}"),
        }
    }
}

impl std::error::Error for AudioError {}

/// Live audio-capture session handle.
///
/// Holds the worker thread, a receiver for segments as they're produced, and
/// a stop channel. The outer program never sees raw samples — only the
/// [`Segment`] stream that flows out of this handle.
pub struct AudioCaptureHandle {
    worker: Option<JoinHandle<Result<(), AudioError>>>,
    segments_rx: mpsc::Receiver<Segment>,
    stop_tx: mpsc::Sender<()>,
}

impl AudioCaptureHandle {
    /// Signal the worker to stop, drain any remaining segments, and return
    /// them. Consumes the handle.
    pub fn stop(mut self) -> Result<Vec<Segment>, AudioError> {
        let _ = self.stop_tx.send(());
        let mut collected = Vec::new();
        // Block until the worker exits: recv() returns Err once segments_tx
        // is dropped at worker return.
        while let Ok(seg) = self.segments_rx.recv() {
            collected.push(seg);
        }
        if let Some(join) = self.worker.take() {
            join.join()
                .map_err(|_| AudioError::BackendFailed("audio worker panicked".to_string()))??;
        }
        Ok(collected)
    }
}

/// Start a capture session backed by the platform's native audio source,
/// feeding samples to `transcriber` on a worker thread.
#[cfg(target_os = "macos")]
pub fn start_capture(transcriber: Box<dyn Transcriber>) -> Result<AudioCaptureHandle, AudioError> {
    capture::start(transcriber)
}

#[cfg(not(target_os = "macos"))]
pub fn start_capture(_transcriber: Box<dyn Transcriber>) -> Result<AudioCaptureHandle, AudioError> {
    Err(AudioError::BackendUnavailable(format!(
        "no audio capture backend for this platform (target_os = {})",
        std::env::consts::OS
    )))
}

/// Build a [`whisperx::WhisperxTranscriber`] boxed as a [`Transcriber`].
///
/// `output_dir` is where `transcript.jsonl` will be written after `finalise`.
/// `model` overrides the default model name (`large-v3`).
pub fn whisperx_transcriber(
    output_dir: PathBuf,
    model: Option<String>,
) -> Result<Box<dyn Transcriber>, TranscribeError> {
    Ok(Box::new(whisperx::WhisperxTranscriber::new(
        output_dir, model,
    )))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn null_transcriber_absorbs_samples() {
        let mut t = NullTranscriber::default();
        let samples = AudioSamples::new(vec![0.0; 1024], 48_000, 2, 0);
        let out = t.consume(samples).unwrap();
        assert!(out.is_empty());
        assert_eq!(t.batches_seen, 1);
        assert_eq!(t.samples_seen, 1024);
    }

    #[test]
    fn null_transcriber_finalise_is_noop() {
        let mut t = NullTranscriber::default();
        let out = t.finalise().unwrap();
        assert!(out.is_empty());
    }

    #[test]
    fn segment_carries_optional_speaker() {
        let s = Segment {
            start_ms: 100,
            end_ms: 250,
            text: "hello".to_string(),
            speaker_id: None,
        };
        assert!(s.speaker_id.is_none());
        let s2 = Segment {
            speaker_id: Some("speaker_0".to_string()),
            ..s.clone()
        };
        assert_eq!(s2.speaker_id.as_deref(), Some("speaker_0"));
    }
}
