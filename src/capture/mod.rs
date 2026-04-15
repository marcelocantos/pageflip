// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::fmt;
use std::path::Path;

#[cfg(target_os = "macos")]
pub mod macos;

/// A window-relative crop rectangle expressed as fractional coordinates in
/// [0.0, 1.0].  Stored at snapshot time and re-applied every tick so the crop
/// follows the window if it resizes.
#[derive(Clone, Copy, Debug)]
pub struct CropSpec {
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
}

impl CropSpec {
    /// Convert to pixel offsets (left, top, width, height) for a frame of
    /// the given physical dimensions.  All values are clamped so the crop
    /// stays within the frame; a zero-area result is an error (degenerate spec).
    pub fn to_pixels(self, width: u32, height: u32) -> Result<(u32, u32, u32, u32), String> {
        let x = (self.x.clamp(0.0, 1.0) * width as f32).round() as u32;
        let y = (self.y.clamp(0.0, 1.0) * height as f32).round() as u32;
        let right = ((self.x + self.w).clamp(0.0, 1.0) * width as f32).round() as u32;
        let bottom = ((self.y + self.h).clamp(0.0, 1.0) * height as f32).round() as u32;
        let pw = right.saturating_sub(x);
        let ph = bottom.saturating_sub(y);
        if pw == 0 || ph == 0 {
            return Err(format!(
                "crop spec ({}, {}, {}, {}) produces zero-area region at {}x{}",
                self.x, self.y, self.w, self.h, width, height
            ));
        }
        Ok((x, y, pw, ph))
    }
}

/// A rectangle on the virtual desktop in screen pixels.
///
/// `x` and `y` are absolute coordinates relative to the primary monitor's
/// top-left corner (negative values address monitors to the left of or above
/// the primary). `w` and `h` are both positive by construction.
#[derive(Clone, Copy, Debug)]
pub struct Region {
    pub x: i32,
    pub y: i32,
    pub w: u32,
    pub h: u32,
}

/// A captured frame: RGBA pixels laid out row-major, width×height in pixels.
///
/// The pixel dimensions are *physical* — on a Retina display this will be
/// larger than the logical region the user requested.
pub struct Frame {
    pub width: u32,
    pub height: u32,
    pub rgba: Vec<u8>,
}

impl Frame {
    /// Write this frame as a PNG at `path`.
    pub fn save_png(&self, path: &Path) -> Result<(), CaptureError> {
        let buffer: xcap::image::RgbaImage =
            xcap::image::ImageBuffer::from_raw(self.width, self.height, self.rgba.clone())
                .ok_or_else(|| CaptureError::EncodeFailed("invalid pixel buffer".to_string()))?;
        buffer
            .save(path)
            .map_err(|e: xcap::image::ImageError| CaptureError::EncodeFailed(e.to_string()))
    }
}

/// Identifies a window for targeted capture.
#[derive(Clone, Debug)]
pub enum WindowSpec {
    /// Match the first window whose title contains this substring (case-sensitive).
    TitleContains(String),
    /// Match the window whose title equals this string exactly (case-sensitive).
    #[allow(dead_code)] // reserved for a future `--window-title-exact` flag
    TitleExact(String),
    /// Match by numeric window ID as returned by [`WindowInfo::id`].
    Id(u32),
}

/// Metadata about a capturable window.
#[derive(Clone, Debug)]
pub struct WindowInfo {
    pub id: u32,
    pub title: String,
    pub app_name: String,
}

/// Abstract capture backend. Exactly one implementation is wired in per
/// target OS; Windows and Linux backends will plug in at this trait.
pub trait Capture {
    /// Capture the given region once.
    fn capture(&self, region: Region) -> Result<Frame, CaptureError>;

    /// Return metadata for all visible, non-minimized windows.
    fn list_windows(&self) -> Result<Vec<WindowInfo>, CaptureError>;

    /// Capture the single window matched by `spec`.
    ///
    /// Returns `Err(WindowNotFound)` when no window matches and
    /// `Err(AmbiguousWindow)` when a `TitleContains`/`TitleExact` spec matches
    /// more than one window.
    fn capture_window(&self, spec: &WindowSpec) -> Result<Frame, CaptureError>;

    /// Capture `spec` and apply `crop`, returning only the cropped sub-image.
    ///
    /// The default implementation captures the full window then crops in-process
    /// using the `image` crate.  Platform impls may override for efficiency.
    fn capture_window_cropped(
        &self,
        spec: &WindowSpec,
        crop: &CropSpec,
    ) -> Result<Frame, CaptureError> {
        let frame = self.capture_window(spec)?;
        let (cx, cy, cw, ch) = crop
            .to_pixels(frame.width, frame.height)
            .map_err(CaptureError::BackendFailed)?;

        // SAFETY: pixel buffer dimensions match frame.width × frame.height by
        // construction; xcap always delivers complete RGBA buffers.
        let img: xcap::image::RgbaImage =
            xcap::image::ImageBuffer::from_raw(frame.width, frame.height, frame.rgba).ok_or_else(
                || CaptureError::EncodeFailed("invalid pixel buffer for crop".to_string()),
            )?;

        let cropped = xcap::image::imageops::crop_imm(&img, cx, cy, cw, ch).to_image();
        Ok(Frame {
            width: cropped.width(),
            height: cropped.height(),
            rgba: cropped.into_raw(),
        })
    }
}

#[derive(Debug)]
pub enum CaptureError {
    /// The OS denied screen-capture access (e.g. macOS Screen Recording).
    PermissionDenied(String),
    /// No monitor contains the requested region.
    RegionOffScreen(Region),
    /// The region crosses a monitor boundary (not yet supported).
    RegionSpansMonitors(Region),
    /// The backend could not enumerate monitors.
    BackendUnavailable(String),
    /// The backend returned an error during capture.
    BackendFailed(String),
    /// PNG encoding failed.
    EncodeFailed(String),
    /// No window matched the given spec.
    WindowNotFound(String),
    /// Multiple windows matched a title-based spec; the caller should present
    /// the list so the user can disambiguate.
    AmbiguousWindow(Vec<WindowInfo>),
}

impl fmt::Display for CaptureError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            CaptureError::PermissionDenied(detail) => {
                write!(
                    f,
                    "screen-capture permission denied: {detail}\n\n\
                     On macOS: open System Settings → Privacy & Security → \
                     Screen Recording, enable pageflip (or your terminal), then retry."
                )
            }
            CaptureError::RegionOffScreen(r) => write!(
                f,
                "region {},{},{},{} is not on any connected monitor",
                r.x, r.y, r.w, r.h
            ),
            CaptureError::RegionSpansMonitors(r) => write!(
                f,
                "region {},{},{},{} spans multiple monitors (not yet supported)",
                r.x, r.y, r.w, r.h
            ),
            CaptureError::BackendUnavailable(detail) => {
                write!(f, "capture backend unavailable: {detail}")
            }
            CaptureError::BackendFailed(detail) => write!(f, "capture failed: {detail}"),
            CaptureError::EncodeFailed(detail) => write!(f, "PNG encode failed: {detail}"),
            CaptureError::WindowNotFound(spec) => {
                write!(f, "no window matched: {spec}")
            }
            CaptureError::AmbiguousWindow(windows) => {
                write!(
                    f,
                    "{} windows matched; use --window-id to pick one:",
                    windows.len()
                )?;
                for w in windows {
                    write!(
                        f,
                        "\n  id={} app={:?} title={:?}",
                        w.id, w.app_name, w.title
                    )?;
                }
                Ok(())
            }
        }
    }
}

impl std::error::Error for CaptureError {}

/// Construct the default capture backend for this build's target OS.
#[cfg(target_os = "macos")]
pub fn default_backend() -> Result<Box<dyn Capture>, CaptureError> {
    Ok(Box::new(macos::MacOsCapture::new()?))
}

#[cfg(not(target_os = "macos"))]
pub fn default_backend() -> Result<Box<dyn Capture>, CaptureError> {
    Err(CaptureError::BackendUnavailable(format!(
        "no capture backend for this platform yet (target_os = {})",
        std::env::consts::OS
    )))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_info(id: u32, title: &str, app: &str) -> WindowInfo {
        WindowInfo {
            id,
            title: title.to_string(),
            app_name: app.to_string(),
        }
    }

    #[test]
    fn window_spec_debug_round_trips() {
        let s = format!("{:?}", WindowSpec::TitleContains("Teams".to_string()));
        assert!(s.contains("TitleContains"));
        let s = format!("{:?}", WindowSpec::TitleExact("Teams".to_string()));
        assert!(s.contains("TitleExact"));
        let s = format!("{:?}", WindowSpec::Id(42));
        assert!(s.contains("42"));
    }

    #[test]
    fn window_info_clone() {
        let info = make_info(1, "Slide deck", "Keynote");
        let cloned = info.clone();
        assert_eq!(cloned.id, 1);
        assert_eq!(cloned.title, "Slide deck");
        assert_eq!(cloned.app_name, "Keynote");
    }

    #[test]
    fn ambiguous_window_error_lists_all() {
        let windows = vec![
            make_info(10, "Teams (1)", "Microsoft Teams"),
            make_info(20, "Teams (2)", "Microsoft Teams"),
        ];
        let err = CaptureError::AmbiguousWindow(windows);
        let msg = err.to_string();
        assert!(msg.contains("2 windows matched"));
        assert!(msg.contains("id=10"));
        assert!(msg.contains("id=20"));
    }

    #[test]
    fn window_not_found_error_includes_spec() {
        let err = CaptureError::WindowNotFound("title=\"Foo\"".to_string());
        assert!(err.to_string().contains("Foo"));
    }

    // --- CropSpec tests ---

    #[test]
    fn crop_spec_full_frame() {
        let c = CropSpec {
            x: 0.0,
            y: 0.0,
            w: 1.0,
            h: 1.0,
        };
        let (x, y, w, h) = c.to_pixels(400, 300).unwrap();
        assert_eq!((x, y, w, h), (0, 0, 400, 300));
    }

    #[test]
    fn crop_spec_centre_quarter() {
        let c = CropSpec {
            x: 0.25,
            y: 0.25,
            w: 0.5,
            h: 0.5,
        };
        let (x, y, w, h) = c.to_pixels(400, 200).unwrap();
        assert_eq!((x, y, w, h), (100, 50, 200, 100));
    }

    #[test]
    fn crop_spec_clamped_overflow() {
        // right edge beyond 1.0 — should clamp, not panic
        let c = CropSpec {
            x: 0.9,
            y: 0.0,
            w: 0.5,
            h: 1.0,
        };
        let (x, _y, w, _h) = c.to_pixels(100, 100).unwrap();
        assert_eq!(x, 90);
        assert_eq!(w, 10); // clamped to frame edge
    }

    #[test]
    fn crop_spec_zero_area_rejected() {
        let c = CropSpec {
            x: 0.5,
            y: 0.5,
            w: 0.0,
            h: 0.5,
        };
        assert!(c.to_pixels(100, 100).is_err());
    }

    #[test]
    fn crop_spec_degenerate_tiny_frame() {
        // 1×1 pixel; centre quarter rounds to zero area → error
        let c = CropSpec {
            x: 0.25,
            y: 0.25,
            w: 0.5,
            h: 0.5,
        };
        // For a 1×1 frame: x=round(0.25)=0, right=round(0.75)=1 → w=1; ok actually
        let result = c.to_pixels(1, 1);
        assert!(result.is_ok());
        let (x, y, w, h) = result.unwrap();
        assert_eq!((x, y, w, h), (0, 0, 1, 1));
    }

    #[test]
    fn crop_spec_x_greater_than_right_edge_zero_area() {
        // w=0.0 means right == left → zero area
        let c = CropSpec {
            x: 0.0,
            y: 0.0,
            w: 0.0,
            h: 1.0,
        };
        assert!(c.to_pixels(100, 100).is_err());
    }
}
