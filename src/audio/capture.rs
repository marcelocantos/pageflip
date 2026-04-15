// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! macOS ScreenCaptureKit-based audio capture.
//!
//! This module extracts PCM f32 samples from SCK's audio stream and hands
//! them to a [`Transcriber`] via the module-local channel. No raw audio
//! ever leaves the `audio` module.

use std::sync::mpsc::{self, RecvTimeoutError};
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use screencapturekit::prelude::*;

use super::{AudioCaptureHandle, AudioError, AudioSamples, Segment, TranscribeError, Transcriber};

struct AudioOutputHandler {
    samples_tx: mpsc::Sender<AudioSamples>,
    capture_start_ms: u64,
}

impl SCStreamOutputTrait for AudioOutputHandler {
    fn did_output_sample_buffer(&self, sample: CMSampleBuffer, of_type: SCStreamOutputType) {
        if !matches!(of_type, SCStreamOutputType::Audio) {
            return;
        }
        match extract_pcm_f32(&sample) {
            Ok((samples, sample_rate, channels)) if !samples.is_empty() => {
                let batch =
                    AudioSamples::new(samples, sample_rate, channels, self.capture_start_ms);
                let _ = self.samples_tx.send(batch);
            }
            Ok(_) => {}
            Err(e) => eprintln!("pageflip: audio extract failed: {e}"),
        }
    }
}

/// Pull PCM samples out of an audio CMSampleBuffer as interleaved f32.
fn extract_pcm_f32(sample: &CMSampleBuffer) -> Result<(Vec<f32>, u32, u16), String> {
    let buffer_list = sample
        .audio_buffer_list()
        .ok_or_else(|| "no audio buffer list in sample".to_string())?;
    let format = sample
        .format_description()
        .ok_or_else(|| "missing format description".to_string())?;
    if !format.audio_is_float() {
        return Err("audio format is not float PCM — expected f32 samples".to_string());
    }
    let sample_rate = format
        .audio_sample_rate()
        .ok_or_else(|| "missing audio sample rate".to_string())? as u32;
    let channels = format
        .audio_channel_count()
        .ok_or_else(|| "missing audio channel count".to_string())? as u16;

    let mut interleaved = Vec::new();
    for buf in buffer_list.iter() {
        let bytes: &[u8] = buf.data();
        if bytes.len() % 4 != 0 {
            return Err(format!(
                "audio buffer not f32-aligned ({} bytes)",
                bytes.len()
            ));
        }
        // SAFETY: AudioBuffer::data() is a &[u8] slice of packed PCM samples;
        // `audio_is_float` above guarantees 32-bit floats. Reinterpreting as
        // *const f32 over the same lifetime is sound for read-only access.
        let floats =
            unsafe { std::slice::from_raw_parts(bytes.as_ptr() as *const f32, bytes.len() / 4) };
        interleaved.extend_from_slice(floats);
    }
    Ok((interleaved, sample_rate, channels))
}

pub fn start(transcriber: Box<dyn Transcriber>) -> Result<AudioCaptureHandle, AudioError> {
    let content = SCShareableContent::get()
        .map_err(|e| classify_sck_error(&format!("{e:?}"), "SCShareableContent::get"))?;
    let display = content
        .displays()
        .into_iter()
        .next()
        .ok_or_else(|| AudioError::BackendUnavailable("no displays available".to_string()))?;

    let filter = SCContentFilter::create()
        .with_display(&display)
        .with_excluding_windows(&[])
        .build();

    // Minimum-viable video configuration — SCK requires dimensions even for
    // audio-only workflows. 2x2 BGRA is the cheapest possible frame.
    let config = SCStreamConfiguration::new()
        .with_width(2)
        .with_height(2)
        .with_pixel_format(PixelFormat::BGRA)
        .with_captures_audio(true)
        .with_sample_rate(48_000)
        .with_channel_count(2);

    let capture_start_ms = now_ms();
    let (samples_tx, samples_rx) = mpsc::channel::<AudioSamples>();
    let (stop_tx, stop_rx) = mpsc::channel::<()>();
    let (segments_tx, segments_rx) = mpsc::channel::<Segment>();

    let handler = AudioOutputHandler {
        samples_tx,
        capture_start_ms,
    };

    let mut stream = SCStream::new(&filter, &config);
    stream.add_output_handler(handler, SCStreamOutputType::Audio);
    stream
        .start_capture()
        .map_err(|e| classify_sck_error(&format!("{e:?}"), "start_capture"))?;

    let worker =
        thread::spawn(move || run_worker(transcriber, samples_rx, stop_rx, segments_tx, stream));

    Ok(AudioCaptureHandle {
        worker: Some(worker),
        segments_rx,
        stop_tx,
    })
}

fn run_worker(
    mut transcriber: Box<dyn Transcriber>,
    samples_rx: mpsc::Receiver<AudioSamples>,
    stop_rx: mpsc::Receiver<()>,
    segments_tx: mpsc::Sender<Segment>,
    stream: SCStream,
) -> Result<(), AudioError> {
    loop {
        if stop_rx.try_recv().is_ok() {
            break;
        }
        match samples_rx.recv_timeout(Duration::from_millis(100)) {
            Ok(batch) => {
                let produced = transcriber.consume(batch).map_err(AudioError::Transcribe)?;
                for seg in produced {
                    let _ = segments_tx.send(seg);
                }
            }
            Err(RecvTimeoutError::Timeout) => continue,
            Err(RecvTimeoutError::Disconnected) => break,
        }
    }
    // Stop the stream before finalise so no more samples arrive.
    if let Err(e) = stream.stop_capture() {
        eprintln!("pageflip: stop_capture returned error: {e:?}");
    }
    match transcriber.finalise() {
        Ok(trailing) => {
            for seg in trailing {
                let _ = segments_tx.send(seg);
            }
        }
        Err(e) => eprintln!("pageflip: transcriber.finalise error: {e}"),
    }
    Ok(())
}

fn classify_sck_error(msg: &str, op: &str) -> AudioError {
    let lower = msg.to_lowercase();
    if lower.contains("permission") || lower.contains("tcc") || lower.contains("not authorized") {
        AudioError::PermissionDenied(format!("{op}: {msg}"))
    } else {
        AudioError::BackendFailed(format!("{op}: {msg}"))
    }
}

fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}

// Unused import suppression: SCK's error types can surface through our
// `Transcribe` variant but the variants themselves are what matter.
#[allow(dead_code)]
fn _assert_transcribe_error_is_used(_e: TranscribeError) {}
