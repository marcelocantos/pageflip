// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use image_hasher::{HashAlg, Hasher, HasherConfig, ImageHash};
use xcap::image::{DynamicImage, ImageBuffer, Rgba};

use crate::capture::Frame;

/// Perceptual-hash based deduplication gate.
///
/// Holds the hash of the most recently accepted frame. `should_save` returns
/// true the first time it is called, and thereafter whenever the incoming
/// frame's Hamming distance from the stored hash meets or exceeds the
/// configured threshold. On acceptance the stored hash is updated.
pub struct Dedup {
    hasher: Hasher,
    last_hash: Option<ImageHash>,
    threshold: u32,
}

impl Dedup {
    pub fn new(threshold: u32) -> Self {
        // 8x8 hash with DCT preprocessing is the classic pHash: 64-bit output
        // that is robust to scale, brightness, and mild colour changes. With
        // a 64-bit hash the Hamming distance ranges 0..=64, so `threshold`
        // reads as "bits that must differ". Default 10 catches real slide
        // changes while ignoring cursor flicker and dithering.
        let hasher = HasherConfig::new()
            .hash_alg(HashAlg::Mean)
            .hash_size(8, 8)
            .preproc_dct()
            .to_hasher();
        Self {
            hasher,
            last_hash: None,
            threshold,
        }
    }

    /// Returns `true` if the frame should be saved, updating the stored
    /// "last saved" hash on acceptance. Retained for unit tests and
    /// callers that don't need the hash — production capture uses
    /// [`classify`] for the hash-bearing variant.
    #[cfg(test)]
    pub fn should_save(&mut self, frame: &Frame) -> bool {
        self.classify(frame).is_some()
    }

    /// Classify a frame against the last accepted hash. Returns the
    /// frame's perceptual-hash hex string if the frame is "new enough"
    /// to be saved (and updates the stored hash), or `None` if the
    /// frame is too close to the last saved one to be worth saving.
    #[cfg(test)]
    pub fn classify(&mut self, frame: &Frame) -> Option<String> {
        self.classify_detail(frame).0
    }

    /// Same as [`classify`] but also returns the Hamming distance
    /// from the last-accepted frame (None on the very first frame)
    /// so callers can report it.
    pub fn classify_detail(&mut self, frame: &Frame) -> (Option<String>, Option<u32>) {
        let buf: ImageBuffer<Rgba<u8>, Vec<u8>> =
            ImageBuffer::from_raw(frame.width, frame.height, frame.rgba.clone())
                .expect("Frame buffer always matches width*height*4");
        let image = DynamicImage::ImageRgba8(buf);
        let hash = self.hasher.hash_image(&image);

        let (save, dist) = match &self.last_hash {
            None => (true, None),
            Some(prev) => {
                let d = prev.dist(&hash);
                (d >= self.threshold, Some(d))
            }
        };
        let hex = hash_to_hex(&hash);
        if save {
            self.last_hash = Some(hash);
            (Some(hex), dist)
        } else {
            (None, dist)
        }
    }
}

/// Render an `ImageHash` as lowercase hex, so events carry a stable
/// identifier that downstream consumers (e.g. meetcat's revisit
/// detector) can use for exact-match or Hamming-distance comparison.
fn hash_to_hex(h: &ImageHash) -> String {
    h.as_bytes()
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect::<String>()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn solid_frame(w: u32, h: u32, rgba: [u8; 4]) -> Frame {
        let mut buf = Vec::with_capacity((w * h * 4) as usize);
        for _ in 0..(w * h) {
            buf.extend_from_slice(&rgba);
        }
        Frame {
            width: w,
            height: h,
            rgba: buf,
        }
    }

    fn checker_frame(w: u32, h: u32, cell: u32) -> Frame {
        let mut buf = Vec::with_capacity((w * h * 4) as usize);
        for y in 0..h {
            for x in 0..w {
                let on = ((x / cell) + (y / cell)) % 2 == 0;
                let v = if on { 255 } else { 0 };
                buf.extend_from_slice(&[v, v, v, 255]);
            }
        }
        Frame {
            width: w,
            height: h,
            rgba: buf,
        }
    }

    #[test]
    fn first_frame_always_saves() {
        let mut d = Dedup::new(10);
        assert!(d.should_save(&solid_frame(64, 64, [10, 20, 30, 255])));
    }

    #[test]
    fn identical_frames_are_deduplicated() {
        let mut d = Dedup::new(10);
        let frame = solid_frame(64, 64, [10, 20, 30, 255]);
        assert!(d.should_save(&frame));
        // Same image again should be suppressed.
        assert!(!d.should_save(&frame));
        assert!(!d.should_save(&frame));
    }

    #[test]
    fn visibly_different_frame_passes_threshold() {
        // Measure the actual distance rather than asserting a hardcoded
        // threshold: we want to prove the hash *discriminates* a visibly
        // different frame, not that it crosses an arbitrary constant.
        let hasher = HasherConfig::new()
            .hash_alg(HashAlg::Mean)
            .hash_size(8, 8)
            .preproc_dct()
            .to_hasher();
        let a_img = {
            let buf: ImageBuffer<Rgba<u8>, _> =
                ImageBuffer::from_raw(128, 128, solid_frame(128, 128, [255, 255, 255, 255]).rgba)
                    .unwrap();
            DynamicImage::ImageRgba8(buf)
        };
        let b_img = {
            let buf: ImageBuffer<Rgba<u8>, _> =
                ImageBuffer::from_raw(128, 128, checker_frame(128, 128, 16).rgba).unwrap();
            DynamicImage::ImageRgba8(buf)
        };
        let dist = hasher.hash_image(&a_img).dist(&hasher.hash_image(&b_img));
        assert!(
            dist > 0,
            "solid vs checkerboard must have nonzero pHash distance (got {dist})"
        );

        // With threshold 0, the checkerboard after a white frame must save.
        let mut d = Dedup::new(0);
        assert!(d.should_save(&solid_frame(128, 128, [255, 255, 255, 255])));
        assert!(d.should_save(&checker_frame(128, 128, 16)));
    }

    #[test]
    fn threshold_zero_saves_any_change() {
        let mut d = Dedup::new(0);
        let a = solid_frame(64, 64, [10, 20, 30, 255]);
        let b = solid_frame(64, 64, [200, 50, 80, 255]);
        assert!(d.should_save(&a));
        assert!(d.should_save(&b));
    }
}
