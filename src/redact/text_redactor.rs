// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Public API — CLI wiring lives in main.rs (not yet integrated).
#![allow(dead_code)]

//! `TextPiiRedactor` — OCR every frame, detect PII, and fill each match's
//! bounding box with an opaque black rectangle.

use crate::capture::Frame;
use crate::redact::{ocr::ocr_frame, pii::detect_pii, RedactError, Redactor};

/// Redacts text PII (emails, phone numbers, credit-card numbers, government
/// IDs, and IP addresses) by drawing opaque black rectangles over the
/// bounding boxes reported by `detect_pii`.
///
/// If OCR produces no words, or if no PII is found, the frame is returned
/// unchanged — neither case is an error.
pub struct TextPiiRedactor;

impl Redactor for TextPiiRedactor {
    fn redact(&self, frame: Frame) -> Result<Frame, RedactError> {
        let words = ocr_frame(&frame)?;
        if words.is_empty() {
            return Ok(frame);
        }

        let pii = detect_pii(&words);
        if pii.is_empty() {
            return Ok(frame);
        }

        let mut out = frame;
        for m in &pii {
            fill_black_rect(&mut out.rgba, out.width, out.height, m.bbox);
        }
        Ok(out)
    }
}

/// Fill the pixel rectangle `rect` with opaque black (R=0, G=0, B=0, A=255).
pub fn fill_black_rect(pixels: &mut [u8], width: u32, height: u32, rect: crate::redact::PixelRect) {
    let x0 = rect.x;
    let y0 = rect.y;
    let x1 = (rect.x + rect.w).min(width);
    let y1 = (rect.y + rect.h).min(height);

    if x0 >= x1 || y0 >= y1 {
        return;
    }

    let stride = width as usize * 4;
    for y in y0..y1 {
        let row = y as usize * stride;
        for x in x0..x1 {
            let off = row + x as usize * 4;
            pixels[off] = 0;
            pixels[off + 1] = 0;
            pixels[off + 2] = 0;
            pixels[off + 3] = 255;
        }
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::capture::Frame;
    use crate::redact::{
        face_blur::FaceBlurRedactor, ocr::OcrWord, pii::detect_pii, PixelRect, RedactPipeline,
    };

    fn white_frame(w: u32, h: u32) -> Frame {
        Frame {
            width: w,
            height: h,
            rgba: vec![255u8; (w * h * 4) as usize],
        }
    }

    fn pixel_at(frame: &Frame, x: u32, y: u32) -> [u8; 4] {
        let off = (y * frame.width + x) as usize * 4;
        [
            frame.rgba[off],
            frame.rgba[off + 1],
            frame.rgba[off + 2],
            frame.rgba[off + 3],
        ]
    }

    // ----- fill_black_rect -----

    #[test]
    fn black_rect_fills_bbox() {
        let w = 20u32;
        let h = 20u32;
        let mut frame = white_frame(w, h);

        let rect = PixelRect {
            x: 5,
            y: 5,
            w: 10,
            h: 10,
        };
        fill_black_rect(&mut frame.rgba, w, h, rect);

        // Interior of rect must be black.
        for y in 5..15u32 {
            for x in 5..15u32 {
                assert_eq!(
                    pixel_at(&frame, x, y),
                    [0, 0, 0, 255],
                    "pixel ({x},{y}) should be black"
                );
            }
        }

        // Pixels outside the rect must be untouched (white).
        assert_eq!(pixel_at(&frame, 0, 0), [255, 255, 255, 255]);
        assert_eq!(pixel_at(&frame, 19, 19), [255, 255, 255, 255]);
        assert_eq!(pixel_at(&frame, 4, 4), [255, 255, 255, 255]);
        assert_eq!(pixel_at(&frame, 15, 15), [255, 255, 255, 255]);
    }

    #[test]
    fn black_rect_zero_size_noop() {
        let mut frame = white_frame(10, 10);
        let original = frame.rgba.clone();
        fill_black_rect(
            &mut frame.rgba,
            10,
            10,
            PixelRect {
                x: 5,
                y: 5,
                w: 0,
                h: 0,
            },
        );
        assert_eq!(frame.rgba, original);
    }

    #[test]
    fn black_rect_clamps_to_frame() {
        let mut frame = white_frame(10, 10);
        // Rect extends beyond the frame boundary — must not panic or corrupt.
        fill_black_rect(
            &mut frame.rgba,
            10,
            10,
            PixelRect {
                x: 8,
                y: 8,
                w: 100,
                h: 100,
            },
        );
        // Pixels inside [8,10) × [8,10) should be black.
        assert_eq!(pixel_at(&frame, 9, 9), [0, 0, 0, 255]);
        // Pixels outside should be white.
        assert_eq!(pixel_at(&frame, 7, 7), [255, 255, 255, 255]);
    }

    // ----- TextPiiRedactor in isolation -----

    // On a solid frame with no text the redactor is identity.
    #[test]
    fn text_pii_redactor_no_text_is_identity() {
        let frame = white_frame(4, 4);
        let expected = frame.rgba.clone();
        let r = TextPiiRedactor;
        match r.redact(frame) {
            Ok(out) => assert_eq!(out.rgba, expected),
            Err(RedactError::BackendUnavailable(_)) => { /* Vision unavailable on CI */ }
            Err(e) => panic!("unexpected error: {e}"),
        }
    }

    // ----- Pipeline composition: FaceBlurRedactor → TextPiiRedactor -----

    #[test]
    fn pipeline_face_then_text_no_panic() {
        let frame = white_frame(8, 8);
        let mut pipeline = RedactPipeline::new();
        pipeline.push(Box::new(FaceBlurRedactor::default()));
        pipeline.push(Box::new(TextPiiRedactor));
        match pipeline.apply(frame) {
            Ok(_) => {}
            Err(RedactError::BackendUnavailable(_)) => { /* Vision unavailable on CI */ }
            Err(e) => panic!("unexpected error: {e}"),
        }
    }

    // ----- detect_pii + fill integration -----

    // Simulate what TextPiiRedactor does: build OcrWords manually, detect PII,
    // fill rects, verify pixels changed inside bbox and not outside.
    #[test]
    fn pii_detection_drives_black_fill() {
        let w = 100u32;
        let h = 20u32;
        let mut frame = white_frame(w, h);

        // Simulate OCR returning an email address at a known position.
        let words = vec![OcrWord {
            text: "user@example.com".to_string(),
            bbox: PixelRect {
                x: 10,
                y: 2,
                w: 60,
                h: 16,
            },
        }];

        let pii = detect_pii(&words);
        assert!(!pii.is_empty(), "email should be detected as PII");

        for m in &pii {
            fill_black_rect(&mut frame.rgba, w, h, m.bbox);
        }

        // A pixel inside the bbox must be black.
        assert_eq!(pixel_at(&frame, 40, 10), [0, 0, 0, 255]);
        // A pixel outside the bbox must be white.
        assert_eq!(pixel_at(&frame, 0, 0), [255, 255, 255, 255]);
        assert_eq!(pixel_at(&frame, 99, 19), [255, 255, 255, 255]);
    }
}
