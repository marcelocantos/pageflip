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
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};

use serde::Deserialize;

use super::{AudioSamples, Segment, TranscribeError, Transcriber};

/// Segment as emitted by `transcribe.py` NDJSON.
///
/// `speaker_id` is optional for backward compatibility — transcribe.py without
/// `--diarize` does not emit it.
#[derive(Deserialize)]
struct RawSegment {
    start_ms: u64,
    end_ms: u64,
    text: String,
    #[serde(default)]
    speaker_id: Option<String>,
}

/// A post-hoc transcriber that buffers all audio in memory and runs
/// WhisperX (and optionally pyannote diarisation) via a Python subprocess on
/// `finalise()`.
pub struct WhisperxTranscriber {
    /// SAFETY: samples buffered in memory only — never written to disk.
    buffer: Vec<f32>,
    sample_rate: Option<u32>,
    channels: Option<u16>,
    model: String,
    output_dir: PathBuf,
    /// When true, passes `--diarize` to the subprocess, which activates
    /// pyannote/speaker-diarization-3.1 and adds `speaker_id` to each segment.
    diarize: bool,
}

impl WhisperxTranscriber {
    pub fn new(output_dir: PathBuf, model: Option<String>, diarize: bool) -> Self {
        Self {
            buffer: Vec::new(),
            sample_rate: None,
            channels: None,
            model: model.unwrap_or_else(|| "large-v3".to_string()),
            output_dir,
            diarize,
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

        // Base uv --with dependencies (always needed).
        let mut args: Vec<String> = vec![
            "run".into(),
            "--with".into(),
            "whisperx".into(),
            "--with".into(),
            "faster-whisper".into(),
            "--with".into(),
            "scipy".into(),
        ];

        // Additional deps required only when diarising.
        if self.diarize {
            args.extend([
                "--with".into(),
                "pyannote.audio".into(),
                "--with".into(),
                "torch".into(),
                "--with".into(),
                "torchaudio".into(),
            ]);
        }

        args.extend([
            "python".into(),
            script
                .to_str()
                .unwrap_or("scripts/transcribe.py")
                .to_string(),
            "--sample-rate".into(),
            sample_rate.to_string(),
            "--channels".into(),
            channels.to_string(),
            "--model".into(),
            self.model.clone(),
        ]);

        if self.diarize {
            args.push("--diarize".into());
        }

        Command::new("uv")
            .args(&args)
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
                        speaker_id: r.speaker_id,
                        slide_id: None,
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
            let mut obj = serde_json::json!({
                "start_ms": seg.start_ms,
                "end_ms": seg.end_ms,
                "text": seg.text,
            });
            if let Some(ref sid) = seg.speaker_id {
                obj["speaker_id"] = serde_json::json!(sid);
            }
            if let Some(ref slid) = seg.slide_id {
                obj["slide_id"] = serde_json::json!(slid);
            }
            let line = obj;
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

// ---------------------------------------------------------------------------
// Slide alignment
// ---------------------------------------------------------------------------

/// Parse an ISO 8601 basic UTC timestamp from a PNG filename stem into
/// milliseconds since the Unix epoch.
///
/// Expected format: `YYYYMMDDTHHMMSSZopt_suffix` where the `Z` suffix
/// marks UTC.  For example `20260415T130050Z` → epoch ms.
///
/// Returns `None` if the stem cannot be parsed.
fn parse_slide_timestamp_ms(stem: &str) -> Option<u64> {
    // Accept filenames like "20260415T130050Z" or
    // "20260415T130050Z_extra_info" — take the leading 16 chars.
    if stem.len() < 16 {
        return None;
    }
    let ts = &stem[..16]; // "20260415T130050Z"
                          // Must match YYYYMMDDTHHMMSSz
    if !ts.ends_with('Z') {
        return None;
    }
    let digits = &ts[..15]; // "20260415T130050"
                            // Validate structure: 8 digits 'T' 6 digits
    let (date_part, time_part) = digits.split_once('T')?;
    if date_part.len() != 8 || time_part.len() != 6 {
        return None;
    }
    if !date_part.chars().all(|c| c.is_ascii_digit()) {
        return None;
    }
    if !time_part.chars().all(|c| c.is_ascii_digit()) {
        return None;
    }

    let year: u64 = date_part[0..4].parse().ok()?;
    let month: u64 = date_part[4..6].parse().ok()?;
    let day: u64 = date_part[6..8].parse().ok()?;
    let hour: u64 = time_part[0..2].parse().ok()?;
    let minute: u64 = time_part[2..4].parse().ok()?;
    let second: u64 = time_part[4..6].parse().ok()?;

    // Validate calendar ranges (coarse — not full Gregorian validation).
    if month == 0 || month > 12 || day == 0 || day > 31 {
        return None;
    }
    if hour > 23 || minute > 59 || second > 59 {
        return None;
    }

    // Days since Unix epoch (1970-01-01) using a fast integer algorithm.
    // We use the proleptic Gregorian calendar for simplicity.
    let epoch_days = days_from_ymd(year, month, day)?;
    let secs = epoch_days * 86_400 + hour * 3_600 + minute * 60 + second;
    Some(secs * 1_000)
}

/// Convert a calendar date to days since 1970-01-01 (Julian Day Number
/// approach — no external crate required).
fn days_from_ymd(year: u64, month: u64, day: u64) -> Option<u64> {
    // Algorithm from https://en.wikipedia.org/wiki/Julian_day_number
    // JDN for Unix epoch (1970-01-01) = 2440588
    const UNIX_EPOCH_JDN: u64 = 2_440_588;

    let a = (14u64.wrapping_sub(month)) / 12;
    let y = year.checked_add(4800)?.checked_sub(a)?;
    let m = month + 12 * a - 3;

    let jdn = day
        .checked_add((153 * m + 2) / 5)?
        .checked_add(365 * y)?
        .checked_add(y / 4)?
        .checked_sub(y / 100)?
        .checked_add(y / 400)?
        .checked_sub(32_045)?;

    jdn.checked_sub(UNIX_EPOCH_JDN)
}

/// Scan `output_dir` for `*.png` files, parse their timestamps, then for
/// each segment set `slide_id` to the stem of the slide whose timestamp is
/// ≤ `segment.start_ms` and closest to it.
///
/// Segments that precede all slide timestamps are left with `slide_id = None`.
pub fn align_segments_to_slides(segments: &mut [Segment], output_dir: &Path) {
    // Collect (timestamp_ms, stem) pairs for all parseable PNG files.
    let mut slides: Vec<(u64, String)> = match std::fs::read_dir(output_dir) {
        Ok(entries) => entries
            .filter_map(|e| e.ok())
            .filter_map(|e| {
                let path = e.path();
                if path.extension()?.to_str()? != "png" {
                    return None;
                }
                let stem = path.file_stem()?.to_str()?.to_string();
                let ts = parse_slide_timestamp_ms(&stem)?;
                Some((ts, stem))
            })
            .collect(),
        Err(_) => return,
    };

    if slides.is_empty() {
        return;
    }

    // Sort by timestamp ascending.
    slides.sort_unstable_by_key(|(ts, _)| *ts);

    for seg in segments.iter_mut() {
        // Binary search for the last slide whose timestamp ≤ seg.start_ms.
        let idx = slides.partition_point(|(ts, _)| *ts <= seg.start_ms);
        if idx == 0 {
            // All slide timestamps are after this segment — leave None.
            continue;
        }
        seg.slide_id = Some(slides[idx - 1].1.clone());
    }
}

// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    fn make_transcriber() -> WhisperxTranscriber {
        WhisperxTranscriber::new(PathBuf::from("/tmp/pf_test_output"), None, false)
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
        assert!(
            segs[0].speaker_id.is_none(),
            "backward compat: no speaker_id when absent"
        );
        assert!(segs[0].slide_id.is_none());
        assert_eq!(segs[1].text, "Second line");
    }

    /// Segments produced with `--diarize` carry `speaker_id`; those without
    /// it must still parse correctly (backward compatibility).
    #[test]
    fn parse_segments_with_speaker_id() {
        let ndjson =
            b"{\"start_ms\":0,\"end_ms\":1000,\"text\":\"Hi\",\"speaker_id\":\"SPEAKER_00\"}\n\
                       {\"start_ms\":1000,\"end_ms\":2000,\"text\":\"Bye\"}\n";
        let segs = WhisperxTranscriber::parse_segments(ndjson);
        assert_eq!(segs.len(), 2);
        assert_eq!(segs[0].speaker_id.as_deref(), Some("SPEAKER_00"));
        assert!(segs[1].speaker_id.is_none(), "absent speaker_id is None");
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

    // -----------------------------------------------------------------------
    // Timestamp parsing
    // -----------------------------------------------------------------------

    #[test]
    fn parse_slide_timestamp_ms_known_value() {
        // 1970-01-01T00:00:00Z = 0 ms
        let ms = parse_slide_timestamp_ms("19700101T000000Z");
        assert_eq!(ms, Some(0));
    }

    #[test]
    fn parse_slide_timestamp_ms_example() {
        // 2026-04-15T13:00:50Z
        // Days from 1970-01-01 to 2026-04-15:
        //   = 365*56 + leap_days + day_of_year_offset
        // We trust the algorithm if it produces a reasonable value (> 1.7B secs).
        let ms = parse_slide_timestamp_ms("20260415T130050Z");
        assert!(ms.is_some());
        let ms = ms.unwrap();
        // 2026-04-15 is about 56 years after epoch → roughly 56*365.25*86400*1000
        let approx_lower: u64 = 1_700_000_000_000; // 2023-11
        let approx_upper: u64 = 1_800_000_000_000; // 2027-01
        assert!(
            ms > approx_lower && ms < approx_upper,
            "unexpected ms value: {ms}"
        );
    }

    #[test]
    fn parse_slide_timestamp_ms_rejects_invalid() {
        assert!(parse_slide_timestamp_ms("notadate").is_none());
        assert!(parse_slide_timestamp_ms("20260415T130050").is_none()); // missing Z
        assert!(parse_slide_timestamp_ms("short").is_none());
        assert!(parse_slide_timestamp_ms("20261315T000000Z").is_none()); // month 13
    }

    // -----------------------------------------------------------------------
    // Slide alignment
    // -----------------------------------------------------------------------

    fn seg(start_ms: u64) -> Segment {
        Segment {
            start_ms,
            end_ms: start_ms + 500,
            text: String::new(),
            speaker_id: None,
            slide_id: None,
        }
    }

    #[test]
    fn align_segments_no_slides_dir() {
        let mut segs = vec![seg(1000), seg(2000)];
        // Non-existent dir — should leave all slide_ids as None without panic.
        align_segments_to_slides(&mut segs, Path::new("/nonexistent/path/xyz_pageflip_test"));
        assert!(segs[0].slide_id.is_none());
        assert!(segs[1].slide_id.is_none());
    }

    #[test]
    fn align_segments_to_slides_basic() {
        // Create a temp dir with synthetic PNG filenames.
        let tmp = std::env::temp_dir().join("pf_slide_align_test");
        let _ = std::fs::remove_dir_all(&tmp);
        std::fs::create_dir_all(&tmp).unwrap();

        // Slide A: 1970-01-01T00:00:01Z = 1000 ms
        // Slide B: 1970-01-01T00:00:03Z = 3000 ms
        // Slide C: 1970-01-01T00:00:05Z = 5000 ms
        for name in [
            "19700101T000001Z.png",
            "19700101T000003Z.png",
            "19700101T000005Z.png",
        ] {
            std::fs::write(tmp.join(name), b"").unwrap();
        }

        let mut segs = vec![
            seg(0),    // before all slides
            seg(500),  // before all slides
            seg(1000), // exactly slide A
            seg(2000), // between A and B → A
            seg(3000), // exactly slide B
            seg(4500), // between B and C → B
            seg(5000), // exactly slide C
            seg(9000), // after C → C
        ];

        align_segments_to_slides(&mut segs, &tmp);

        assert!(segs[0].slide_id.is_none(), "before all slides");
        assert!(segs[1].slide_id.is_none(), "before all slides");
        assert_eq!(segs[2].slide_id.as_deref(), Some("19700101T000001Z"));
        assert_eq!(segs[3].slide_id.as_deref(), Some("19700101T000001Z"));
        assert_eq!(segs[4].slide_id.as_deref(), Some("19700101T000003Z"));
        assert_eq!(segs[5].slide_id.as_deref(), Some("19700101T000003Z"));
        assert_eq!(segs[6].slide_id.as_deref(), Some("19700101T000005Z"));
        assert_eq!(segs[7].slide_id.as_deref(), Some("19700101T000005Z"));

        let _ = std::fs::remove_dir_all(&tmp);
    }

    #[test]
    fn align_segments_ignores_non_png_files() {
        let tmp = std::env::temp_dir().join("pf_slide_align_test2");
        let _ = std::fs::remove_dir_all(&tmp);
        std::fs::create_dir_all(&tmp).unwrap();

        // Only a .jpg and a .txt — no PNGs.
        std::fs::write(tmp.join("19700101T000001Z.jpg"), b"").unwrap();
        std::fs::write(tmp.join("19700101T000001Z.txt"), b"").unwrap();

        let mut segs = vec![seg(5000)];
        align_segments_to_slides(&mut segs, &tmp);
        assert!(
            segs[0].slide_id.is_none(),
            "no PNG files means no slide assignment"
        );

        let _ = std::fs::remove_dir_all(&tmp);
    }
}
