// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Inter-slide deduplication via mean per-channel RMS pixel difference.
//!
//! pHash (mean+DCT 8x8 = 64 bits) was the previous primitive. Its
//! intent — "find similar images at different scales / brightness" —
//! is the wrong question for slide capture, where we want to detect
//! "the screen contents have not meaningfully changed since last
//! tick." pHash's 64-bit DCT shadow let visually-distinct slides
//! sharing structural patterns (dark background, centered yellow
//! title) collide at small Hamming distances.
//!
//! RMS over the actual pixel buffer is the correct primitive. It
//! reads a tiny mean-difference (≤1 unit on the 0–255 scale) for two
//! captures of the same slide that differ only by cursor movement
//! and PNG compression noise; for two visibly different slides the
//! mean difference jumps to tens of units. The threshold reads
//! directly as "mean per-channel pixel difference."

use crate::capture::Frame;

/// Last-frame-comparison gate. `should_save` returns true the first
/// time it is called, and thereafter whenever the new frame's mean
/// per-channel RMS difference from the last accepted frame is at or
/// above the configured threshold (in 0–255 units). On acceptance
/// the stored frame is replaced.
pub struct Dedup {
    last: Option<Vec<u8>>,
    last_dims: Option<(u32, u32)>,
    threshold: f64,
}

impl Dedup {
    /// Construct a dedup gate. `threshold` is the minimum mean
    /// per-channel RMS difference (on the 0–255 scale) for a frame
    /// to be considered different enough to save.
    pub fn new(threshold: f64) -> Self {
        Self {
            last: None,
            last_dims: None,
            threshold,
        }
    }

    /// Returns `true` if the frame should be saved, updating the
    /// stored buffer on acceptance. Test-only helper retained for
    /// unit tests; production capture uses `classify_detail` for the
    /// distance-bearing variant.
    #[cfg(test)]
    pub fn should_save(&mut self, frame: &Frame) -> bool {
        self.classify_detail(frame).0
    }

    /// Returns `(save, dist)`:
    /// - `save` is true iff the frame should be persisted (first
    ///   frame, dimension change, or RMS ≥ threshold).
    /// - `dist` is the mean per-channel RMS distance from the last
    ///   accepted frame, or None on the first frame / a dimension
    ///   change.
    pub fn classify_detail(&mut self, frame: &Frame) -> (bool, Option<f64>) {
        let dims = (frame.width, frame.height);
        let dist = match (&self.last, self.last_dims) {
            (Some(prev), Some(prev_dims))
                if prev_dims == dims && prev.len() == frame.rgba.len() =>
            {
                Some(rms_diff(prev, &frame.rgba))
            }
            _ => None,
        };
        let save = match dist {
            None => true,
            Some(d) => d >= self.threshold,
        };
        if save {
            self.last = Some(frame.rgba.clone());
            self.last_dims = Some(dims);
        }
        (save, dist)
    }
}

/// Mean per-channel RMS difference between two equal-length packed
/// pixel buffers. Caller ensures lengths match. Output is on the
/// 0–255 scale.
fn rms_diff(a: &[u8], b: &[u8]) -> f64 {
    debug_assert_eq!(a.len(), b.len());
    if a.is_empty() {
        return 0.0;
    }
    let mut sum_sq: u64 = 0;
    for (x, y) in a.iter().zip(b.iter()) {
        let d = i32::from(*x) - i32::from(*y);
        sum_sq += (d * d) as u64;
    }
    (sum_sq as f64 / a.len() as f64).sqrt()
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

    #[test]
    fn first_frame_always_saves() {
        let mut d = Dedup::new(5.0);
        assert!(d.should_save(&solid_frame(64, 64, [10, 20, 30, 255])));
    }

    #[test]
    fn identical_frames_are_deduplicated() {
        let mut d = Dedup::new(5.0);
        let frame = solid_frame(64, 64, [10, 20, 30, 255]);
        assert!(d.should_save(&frame));
        assert!(!d.should_save(&frame));
        assert!(!d.should_save(&frame));
    }

    #[test]
    fn small_difference_below_threshold_is_deduped() {
        // A 1-unit shift on a few channels averages to RMS ≪ 5 on a
        // 64×64 frame, mimicking cursor / compression noise.
        let mut d = Dedup::new(5.0);
        assert!(d.should_save(&solid_frame(64, 64, [10, 20, 30, 255])));
        assert!(!d.should_save(&solid_frame(64, 64, [11, 21, 31, 255])));
    }

    #[test]
    fn large_difference_above_threshold_saves() {
        let mut d = Dedup::new(5.0);
        assert!(d.should_save(&solid_frame(64, 64, [10, 10, 10, 255])));
        // Each channel differs by ~190; mean RMS ≫ 5.
        assert!(d.should_save(&solid_frame(64, 64, [200, 200, 200, 255])));
    }

    #[test]
    fn dimension_change_saves() {
        // Capture region resized mid-stream — buffers can't be
        // compared, so the new frame is treated as fresh.
        let mut d = Dedup::new(5.0);
        assert!(d.should_save(&solid_frame(64, 64, [10, 20, 30, 255])));
        assert!(d.should_save(&solid_frame(128, 128, [10, 20, 30, 255])));
    }

    #[test]
    fn threshold_zero_saves_any_change() {
        let mut d = Dedup::new(0.0);
        assert!(d.should_save(&solid_frame(64, 64, [10, 20, 30, 255])));
        assert!(d.should_save(&solid_frame(64, 64, [11, 21, 31, 255])));
    }
}
