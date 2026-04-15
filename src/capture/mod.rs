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

/// Abstract capture backend. Exactly one implementation is wired in per
/// target OS; Windows and Linux backends will plug in at this trait.
pub trait Capture {
    /// Capture the given region once.
    fn capture(&self, region: Region) -> Result<Frame, CaptureError>;
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
