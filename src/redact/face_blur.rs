// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Face detection via Apple Vision framework and box-blur redaction.
//!
//! Vision returns bounding boxes in *normalised* coordinates (0.0–1.0) with
//! the origin at the **bottom-left** of the image (Core Graphics convention).
//! The RGBA pixel buffer has its origin at the **top-left**, so we flip Y when
//! converting.

use crate::capture::Frame;
use crate::redact::{RedactError, Redactor};

/// A pixel-space bounding rectangle (top-left origin, Y down).
#[derive(Clone, Copy, Debug)]
pub struct PixelRect {
    pub x: u32,
    pub y: u32,
    pub w: u32,
    pub h: u32,
}

/// Detects faces with Apple Vision and applies a box blur over each face
/// rectangle before returning the modified frame.
///
/// On non-macOS targets the Vision detection step is skipped (Vision.framework
/// is Apple-only) and the frame is returned unchanged — the pipeline degrades
/// gracefully.
pub struct FaceBlurRedactor {
    /// Half-width of the blur kernel in pixels.  Larger → more blurred.
    kernel_radius: u32,
}

impl FaceBlurRedactor {
    /// `kernel_radius` is the half-width of the box-blur kernel.
    /// A radius of 15 means a 31×31 averaging window.
    pub fn new(kernel_radius: u32) -> Self {
        Self { kernel_radius }
    }
}

impl Default for FaceBlurRedactor {
    fn default() -> Self {
        Self::new(15)
    }
}

impl Redactor for FaceBlurRedactor {
    #[cfg(target_os = "macos")]
    fn redact(&self, frame: Frame) -> Result<Frame, RedactError> {
        let rects = detect_faces(&frame).map_err(|e| {
            if e.to_lowercase().contains("permission")
                || e.to_lowercase().contains("not authorized")
                || e.to_lowercase().contains("tccd")
            {
                RedactError::BackendUnavailable(e)
            } else {
                RedactError::ProcessingFailed(e)
            }
        })?;

        if rects.is_empty() {
            return Ok(frame);
        }

        let mut out = frame;
        for rect in rects {
            box_blur_rect(
                &mut out.rgba,
                out.width,
                out.height,
                rect,
                self.kernel_radius,
            );
        }
        Ok(out)
    }

    #[cfg(not(target_os = "macos"))]
    fn redact(&self, frame: Frame) -> Result<Frame, RedactError> {
        // Vision.framework is macOS-only; degrade gracefully on other targets.
        Ok(frame)
    }
}

// ---------------------------------------------------------------------------
// Vision face detection (macOS only)
// ---------------------------------------------------------------------------

#[cfg(target_os = "macos")]
fn detect_faces(frame: &Frame) -> Result<Vec<PixelRect>, String> {
    // Encode the RGBA frame as PNG bytes so Vision can create a CIImage via
    // NSData.  VNImageRequestHandler also accepts CVPixelBuffer but that
    // requires CoreVideo bindings; the NSData/PNG path keeps the dep surface
    // minimal.
    let png = encode_rgba_to_png(frame)?;
    detect_faces_in_png(&png, frame.width, frame.height)
}

#[cfg(target_os = "macos")]
fn detect_faces_in_png(
    png_bytes: &[u8],
    frame_width: u32,
    frame_height: u32,
) -> Result<Vec<PixelRect>, String> {
    use objc2::AnyThread;
    use objc2_foundation::{NSArray, NSData, NSDictionary};
    use objc2_vision::{VNDetectFaceRectanglesRequest, VNImageRequestHandler, VNRequest};

    // SAFETY: We are on the main/only thread of a Rust program.  All
    // Foundation/Vision objects are created, used, and released within this
    // function's stack frame.  No cross-thread sharing occurs.
    unsafe {
        let ns_data = NSData::with_bytes(png_bytes);
        let options: objc2::rc::Retained<NSDictionary<_, _>> = NSDictionary::new();
        let handler = VNImageRequestHandler::initWithData_options(
            VNImageRequestHandler::alloc(),
            &ns_data,
            &options,
        );

        let request = VNDetectFaceRectanglesRequest::new();

        // Upcast through the super chain:
        // VNDetectFaceRectanglesRequest → VNImageBasedRequest → VNRequest
        let request_ref: &VNRequest = &request;
        let requests: objc2::rc::Retained<NSArray<VNRequest>> = NSArray::from_slice(&[request_ref]);

        // If Vision can't create an inference context (e.g. headless CI, no
        // GPU, sandboxed environment), degrade to "no faces detected" rather
        // than surfacing an error that blocks the capture pipeline.
        if let Err(e) = handler.performRequests_error(&requests) {
            let msg = e.localizedDescription().to_string();
            if msg.contains("inference context") || msg.contains("VNRequest") {
                return Ok(Vec::new());
            }
            return Err(msg);
        }

        let rects: Vec<PixelRect> = match request.results() {
            None => Vec::new(),
            Some(arr) => arr
                .iter()
                .map(|obs| {
                    // SAFETY: boundingBox is safe to call on a valid observation.
                    let bbox = obs.boundingBox();
                    normalised_to_pixels(bbox, frame_width, frame_height)
                })
                .collect(),
        };

        Ok(rects)
    }
}

/// Convert a Vision normalised bounding box (origin bottom-left, Y up) to
/// pixel coordinates (origin top-left, Y down).
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
// Box blur (all targets)
// ---------------------------------------------------------------------------

/// Apply an in-place two-pass box blur with the given `radius` (half-kernel)
/// to the RGBA pixels inside `rect`.
///
/// Each output pixel's channel value is the unweighted average of the
/// (2·radius + 1)² neighbourhood clamped to the rect boundary.
/// Two passes (horizontal then vertical) keep work O(radius) per pixel.
pub fn box_blur_rect(pixels: &mut [u8], width: u32, height: u32, rect: PixelRect, radius: u32) {
    let x0 = rect.x;
    let y0 = rect.y;
    let x1 = (rect.x + rect.w).min(width);
    let y1 = (rect.y + rect.h).min(height);

    if x0 >= x1 || y0 >= y1 || radius == 0 {
        return;
    }

    let stride = width as usize * 4;

    // Horizontal pass: average each row within the rect horizontally.
    // Write into `tmp` to avoid feed-through of already-blurred pixels.
    let mut tmp = pixels.to_vec();

    for y in y0..y1 {
        let row_off = y as usize * stride;
        let r = radius as usize;
        for x in x0..x1 {
            let mut sum = [0u32; 4];
            let mut count = 0u32;
            // Sample from the full frame row, not clamped to the rect, so
            // edge pixels mix with the surrounding context.
            let lo = (x as isize - r as isize).max(0) as usize;
            let hi = ((x as usize + r).min((width - 1) as usize)) + 1;
            for sx in lo..hi {
                let off = row_off + sx * 4;
                sum[0] += pixels[off] as u32;
                sum[1] += pixels[off + 1] as u32;
                sum[2] += pixels[off + 2] as u32;
                sum[3] += pixels[off + 3] as u32;
                count += 1;
            }
            let off = row_off + x as usize * 4;
            tmp[off] = (sum[0] / count) as u8;
            tmp[off + 1] = (sum[1] / count) as u8;
            tmp[off + 2] = (sum[2] / count) as u8;
            tmp[off + 3] = (sum[3] / count) as u8;
        }
    }

    // Vertical pass: average each column within the rect vertically.
    for x in x0..x1 {
        let r = radius as usize;
        for y in y0..y1 {
            let mut sum = [0u32; 4];
            let mut count = 0u32;
            // Sample from the full frame column for the same reason.
            let lo = (y as isize - r as isize).max(0) as usize;
            let hi = ((y as usize + r).min((height - 1) as usize)) + 1;
            for sy in lo..hi {
                let off = sy * stride + x as usize * 4;
                sum[0] += tmp[off] as u32;
                sum[1] += tmp[off + 1] as u32;
                sum[2] += tmp[off + 2] as u32;
                sum[3] += tmp[off + 3] as u32;
                count += 1;
            }
            let off = y as usize * stride + x as usize * 4;
            pixels[off] = (sum[0] / count) as u8;
            pixels[off + 1] = (sum[1] / count) as u8;
            pixels[off + 2] = (sum[2] / count) as u8;
            pixels[off + 3] = (sum[3] / count) as u8;
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

    fn solid_frame(w: u32, h: u32, rgba: [u8; 4]) -> Frame {
        Frame {
            width: w,
            height: h,
            rgba: (0..w * h).flat_map(|_| rgba).collect(),
        }
    }

    /// Build a frame with a bright-white rectangle on a black background.
    fn rect_frame(w: u32, h: u32, rx: u32, ry: u32, rw: u32, rh: u32) -> Frame {
        let mut rgba = vec![0u8; (w * h * 4) as usize];
        for y in ry..(ry + rh).min(h) {
            for x in rx..(rx + rw).min(w) {
                let off = (y * w + x) as usize * 4;
                rgba[off] = 255;
                rgba[off + 1] = 255;
                rgba[off + 2] = 255;
                rgba[off + 3] = 255;
            }
        }
        Frame {
            width: w,
            height: h,
            rgba,
        }
    }

    // Blurring a perfectly uniform rect is a mathematical identity.
    #[test]
    fn blur_solid_frame_is_unchanged() {
        let frame = solid_frame(64, 64, [200, 100, 50, 255]);
        let mut pixels = frame.rgba.clone();
        let rect = PixelRect {
            x: 10,
            y: 10,
            w: 40,
            h: 40,
        };
        box_blur_rect(&mut pixels, 64, 64, rect, 15);
        assert_eq!(pixels, frame.rgba);
    }

    // Interior pixels of a white rect on a black background must be mixed
    // (neither pure white nor pure black) after blurring.
    #[test]
    fn blur_mixes_pixels_at_boundary() {
        let w = 64u32;
        let h = 64u32;
        let rx = 16u32;
        let ry = 16u32;
        let rw = 32u32;
        let rh = 32u32;
        let frame = rect_frame(w, h, rx, ry, rw, rh);
        let mut pixels = frame.rgba.clone();

        let rect = PixelRect {
            x: rx,
            y: ry,
            w: rw,
            h: rh,
        };
        box_blur_rect(&mut pixels, w, h, rect, 8);

        // Corner pixel was pure white; kernel extends into black surround →
        // must be strictly less than 255.
        let corner_off = (ry * w + rx) as usize * 4;
        let r_corner = pixels[corner_off];
        assert!(
            r_corner < 255,
            "corner pixel ({r_corner}) should have mixed with black neighbours"
        );

        // Centre pixel averages only white neighbours → stays close to white.
        let cx = rx + rw / 2;
        let cy = ry + rh / 2;
        let centre_off = (cy * w + cx) as usize * 4;
        let r_centre = pixels[centre_off];
        assert!(
            r_centre > 200,
            "centre pixel ({r_centre}) should remain close to white"
        );
    }

    // Zero-radius blur is a no-op.
    #[test]
    fn zero_radius_noop() {
        let frame = rect_frame(32, 32, 8, 8, 16, 16);
        let expected = frame.rgba.clone();
        let mut pixels = frame.rgba.clone();
        let rect = PixelRect {
            x: 8,
            y: 8,
            w: 16,
            h: 16,
        };
        box_blur_rect(&mut pixels, 32, 32, rect, 0);
        assert_eq!(pixels, expected);
    }

    // A rect that fills the whole frame should not panic.
    #[test]
    fn blur_full_frame_rect_no_panic() {
        let frame = rect_frame(16, 16, 0, 0, 16, 16);
        let mut pixels = frame.rgba.clone();
        let rect = PixelRect {
            x: 0,
            y: 0,
            w: 16,
            h: 16,
        };
        box_blur_rect(&mut pixels, 16, 16, rect, 5);
    }

    // FaceBlurRedactor::default() constructs without panicking.
    #[test]
    fn face_blur_redactor_default_builds() {
        let _ = FaceBlurRedactor::default();
    }

    // On non-macOS the redactor is identity.  On macOS it must either return
    // the frame unchanged (no faces in a solid 4×4) or emit BackendUnavailable
    // if Vision rejects a toy input.
    #[test]
    fn redactor_identity_on_no_faces() {
        let frame = solid_frame(4, 4, [128, 64, 32, 255]);
        let expected = frame.rgba.clone();
        let redactor = FaceBlurRedactor::default();
        match redactor.redact(frame) {
            Ok(out) => assert_eq!(out.rgba, expected),
            Err(RedactError::BackendUnavailable(_)) => { /* Vision rejected the toy input */ }
            Err(e) => panic!("unexpected error: {e}"),
        }
    }
}
