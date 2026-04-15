// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::fmt;
use std::path::Path;

#[cfg(target_os = "macos")]
pub mod macos;

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
#[allow(dead_code)]
pub enum WindowSpec {
    /// Match the first window whose title contains this substring (case-sensitive).
    TitleContains(String),
    /// Match the window whose title equals this string exactly (case-sensitive).
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
    #[allow(dead_code)]
    fn list_windows(&self) -> Result<Vec<WindowInfo>, CaptureError>;

    /// Capture the single window matched by `spec`.
    ///
    /// Returns `Err(WindowNotFound)` when no window matches and
    /// `Err(AmbiguousWindow)` when a `TitleContains`/`TitleExact` spec matches
    /// more than one window.
    #[allow(dead_code)]
    fn capture_window(&self, spec: &WindowSpec) -> Result<Frame, CaptureError>;
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
    #[allow(dead_code)]
    WindowNotFound(String),
    /// Multiple windows matched a title-based spec; the caller should present
    /// the list so the user can disambiguate.
    #[allow(dead_code)]
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
}
