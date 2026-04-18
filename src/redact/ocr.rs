// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Public API — CLI wiring lives in main.rs (not yet integrated).
#![allow(dead_code)]

//! OCR of captured frames via Apple Vision `VNRecognizeTextRequest`.
//!
//! Vision returns bounding boxes in *normalised* coordinates (0.0–1.0) with
//! the origin at the **bottom-left** of the image (Core Graphics convention).
//! We flip Y when converting to pixel space (top-left origin, Y down).

use crate::capture::Frame;
use crate::redact::{PixelRect, RedactError};

/// A single recognised word with its pixel-space bounding box.
#[derive(Clone, Debug)]
pub struct OcrWord {
    pub text: String,
    pub bbox: PixelRect,
}

/// Perform OCR on `frame` and return every recognised word with its bounding
/// box.
///
/// On macOS the Apple Vision `VNRecognizeTextRequest` is used.  On other
/// targets — and on macOS when Vision is unavailable (headless CI, sandboxed
/// environment) — an empty `Vec` is returned rather than an error, so the
/// redaction pipeline degrades gracefully.
pub fn ocr_frame(frame: &Frame) -> Result<Vec<OcrWord>, RedactError> {
    ocr_frame_impl(frame)
}

// ---------------------------------------------------------------------------
// macOS implementation
// ---------------------------------------------------------------------------

#[cfg(target_os = "macos")]
fn ocr_frame_impl(frame: &Frame) -> Result<Vec<OcrWord>, RedactError> {
    let png = encode_rgba_to_png(frame).map_err(RedactError::ProcessingFailed)?;
    recognize_text_in_png(&png, frame.width, frame.height).map_err(|e| {
        if e.to_lowercase().contains("permission")
            || e.to_lowercase().contains("not authorized")
            || e.to_lowercase().contains("tccd")
        {
            RedactError::BackendUnavailable(e)
        } else {
            RedactError::ProcessingFailed(e)
        }
    })
}

#[cfg(target_os = "macos")]
fn recognize_text_in_png(
    png_bytes: &[u8],
    frame_width: u32,
    frame_height: u32,
) -> Result<Vec<OcrWord>, String> {
    use objc2::AnyThread;
    use objc2_foundation::{NSArray, NSData, NSDictionary};
    use objc2_vision::{VNImageRequestHandler, VNRecognizeTextRequest, VNRequest};

    // SAFETY: All Foundation/Vision objects are created, used, and released
    // within this function's stack frame.  No cross-thread sharing occurs.
    unsafe {
        let ns_data = NSData::with_bytes(png_bytes);
        let options: objc2::rc::Retained<NSDictionary<_, _>> = NSDictionary::new();
        let handler = VNImageRequestHandler::initWithData_options(
            VNImageRequestHandler::alloc(),
            &ns_data,
            &options,
        );

        let request = VNRecognizeTextRequest::new();

        let request_ref: &VNRequest = &request;
        let requests: objc2::rc::Retained<NSArray<VNRequest>> = NSArray::from_slice(&[request_ref]);

        // Degrade gracefully when Vision cannot create an inference context
        // (e.g. headless CI, no GPU, sandboxed environment).
        if let Err(e) = handler.performRequests_error(&requests) {
            let msg = e.localizedDescription().to_string();
            if msg.contains("inference context") || msg.contains("VNRequest") {
                return Ok(Vec::new());
            }
            return Err(msg);
        }

        let words: Vec<OcrWord> = match request.results() {
            None => Vec::new(),
            Some(arr) => arr
                .iter()
                .filter_map(|obs| {
                    // topCandidates(1) returns at most one candidate.
                    let candidates = obs.topCandidates(1);
                    let candidate = candidates.firstObject()?;
                    let text = candidate.string().to_string();
                    let bbox = obs.boundingBox();
                    let pixel_rect = normalised_to_pixels(bbox, frame_width, frame_height);
                    Some(OcrWord {
                        text,
                        bbox: pixel_rect,
                    })
                })
                .collect(),
        };

        Ok(words)
    }
}

#[cfg(target_os = "macos")]
fn normalised_to_pixels(rect: objc2_core_foundation::CGRect, w: u32, h: u32) -> PixelRect {
    let fw = w as f64;
    let fh = h as f64;

    let px = (rect.origin.x * fw).round() as i64;
    let pw = (rect.size.width * fw).round() as i64;
    // Vision Y=0 is at the bottom; flip to get top-left pixel-space Y.
    let y_top = fh - (rect.origin.y + rect.size.height) * fh;
    let py = y_top.round() as i64;
    let ph = (rect.size.height * fh).round() as i64;

    let x = px.clamp(0, w as i64 - 1) as u32;
    let y = py.clamp(0, h as i64 - 1) as u32;
    let bw = pw.clamp(0, w as i64 - x as i64) as u32;
    let bh = ph.clamp(0, h as i64 - y as i64) as u32;

    PixelRect { x, y, w: bw, h: bh }
}

#[cfg(target_os = "macos")]
fn encode_rgba_to_png(frame: &Frame) -> Result<Vec<u8>, String> {
    use std::io::Cursor;
    let img: xcap::image::RgbaImage =
        xcap::image::ImageBuffer::from_raw(frame.width, frame.height, frame.rgba.clone())
            .ok_or_else(|| "invalid pixel buffer dimensions".to_string())?;
    let mut buf = Cursor::new(Vec::new());
    img.write_to(&mut buf, xcap::image::ImageFormat::Png)
        .map_err(|e| e.to_string())?;
    Ok(buf.into_inner())
}

// ---------------------------------------------------------------------------
// Non-macOS stub — Vision is Apple-only
// ---------------------------------------------------------------------------

#[cfg(not(target_os = "macos"))]
fn ocr_frame_impl(_frame: &Frame) -> Result<Vec<OcrWord>, RedactError> {
    Ok(Vec::new())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::capture::Frame;

    fn minimal_frame() -> Frame {
        Frame {
            width: 4,
            height: 4,
            rgba: vec![128u8; 4 * 4 * 4],
        }
    }

    // ocr_frame on a toy frame must not panic; it either returns an empty
    // vec (no text / Vision unavailable) or BackendUnavailable.
    #[test]
    fn ocr_frame_no_panic_on_toy_input() {
        let frame = minimal_frame();
        match ocr_frame(&frame) {
            Ok(words) => {
                // No recognisable text in a solid-grey 4×4 image.
                let _ = words;
            }
            Err(RedactError::BackendUnavailable(_)) => {
                // Acceptable — Vision rejected the toy input.
            }
            Err(e) => panic!("unexpected error from ocr_frame: {e}"),
        }
    }
}
