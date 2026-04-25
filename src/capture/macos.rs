// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::process::Command;

use image::{ImageReader, RgbaImage};
use xcap::{Monitor, Window};

use super::{Capture, CaptureError, Frame, Region, WindowInfo, WindowSpec};

pub struct MacOsCapture {}

impl MacOsCapture {
    pub fn new() -> Result<Self, CaptureError> {
        // Validate that at least one monitor is reachable at startup. Region
        // capture itself uses /usr/sbin/screencapture (Apple's own CLI,
        // backed by ScreenCaptureKit on modern macOS) rather than xcap's
        // deprecated CGWindowListCreateImage path, which on macOS 14+
        // returns stale cached frames when polled at sub-second intervals.
        let monitors = Monitor::all().map_err(|e| classify_xcap_error(e, "Monitor::all"))?;
        if monitors.is_empty() {
            return Err(CaptureError::BackendUnavailable(
                "no monitors detected".to_string(),
            ));
        }
        Ok(Self {})
    }
}

impl Capture for MacOsCapture {
    fn capture(&self, region: Region) -> Result<Frame, CaptureError> {
        capture_via_screencapture(region)
    }

    fn list_windows(&self) -> Result<Vec<WindowInfo>, CaptureError> {
        let windows = Window::all().map_err(|e| classify_xcap_error(e, "Window::all"))?;

        // xcap already excludes minimized windows (CGWindowListOption::OptionOnScreenOnly).
        // Additionally skip windows with no title — these are system UI surfaces (menu bar
        // extras, dock overlays, etc.) that aren't useful to the user.
        let infos = windows
            .into_iter()
            .filter_map(|w| {
                let id = w.id().ok()?;
                let title = w.title().ok()?;
                let app_name = w.app_name().ok()?;
                if title.is_empty() {
                    return None;
                }
                Some(WindowInfo {
                    id,
                    title,
                    app_name,
                })
            })
            .collect();

        Ok(infos)
    }

    fn capture_window(&self, spec: &WindowSpec) -> Result<Frame, CaptureError> {
        let window = find_window_for_spec(spec)?;
        let image = window
            .capture_image()
            .map_err(|e| classify_xcap_error(e, "Window::capture_image"))?;
        Ok(Frame {
            width: image.width(),
            height: image.height(),
            rgba: image.into_raw(),
        })
    }
}

#[allow(dead_code)]
fn find_window_for_spec(spec: &WindowSpec) -> Result<Window, CaptureError> {
    let windows = Window::all().map_err(|e| classify_xcap_error(e, "Window::all"))?;

    match spec {
        WindowSpec::Id(target_id) => windows
            .into_iter()
            .find(|w| w.id().ok() == Some(*target_id))
            .ok_or_else(|| CaptureError::WindowNotFound(format!("id={target_id}"))),

        WindowSpec::TitleExact(target) => {
            let matches: Vec<Window> = windows
                .into_iter()
                .filter(|w| w.title().ok().as_deref() == Some(target.as_str()))
                .collect();
            match matches.len() {
                0 => Err(CaptureError::WindowNotFound(format!("title={target:?}"))),
                1 => Ok(matches.into_iter().next().unwrap()),
                _ => Err(CaptureError::AmbiguousWindow(windows_to_infos(matches))),
            }
        }

        WindowSpec::TitleContains(needle) => {
            let matches: Vec<Window> = windows
                .into_iter()
                .filter(|w| {
                    w.title()
                        .ok()
                        .map(|t| t.contains(needle.as_str()))
                        .unwrap_or(false)
                })
                .collect();
            match matches.len() {
                0 => Err(CaptureError::WindowNotFound(format!(
                    "title contains {needle:?}"
                ))),
                1 => Ok(matches.into_iter().next().unwrap()),
                _ => Err(CaptureError::AmbiguousWindow(windows_to_infos(matches))),
            }
        }
    }
}

#[allow(dead_code)]
fn windows_to_infos(windows: Vec<Window>) -> Vec<super::WindowInfo> {
    windows
        .into_iter()
        .filter_map(|w| {
            Some(super::WindowInfo {
                id: w.id().ok()?,
                title: w.title().ok()?,
                app_name: w.app_name().ok()?,
            })
        })
        .collect()
}

/// Capture a region by shelling out to `/usr/sbin/screencapture`. Apple's
/// own CLI uses the modern, supported capture path (ScreenCaptureKit on
/// recent macOS releases) and reliably returns a fresh frame on every
/// invocation — unlike xcap's CGWindowListCreateImage path, which returns
/// stale cached pixels under sub-second polling on macOS 14+.
fn capture_via_screencapture(region: Region) -> Result<Frame, CaptureError> {
    // -R x,y,w,h captures the rectangle in *global display* coordinates.
    // -t png writes PNG. -x suppresses the shutter sound and screen flash.
    // -o omits the window shadow (no-op for region capture but harmless).
    let tmp = tempfile::Builder::new()
        .prefix("pageflip-")
        .suffix(".png")
        .tempfile()
        .map_err(|e| CaptureError::BackendFailed(format!("tempfile: {e}")))?;

    let rect = format!("{},{},{},{}", region.x, region.y, region.w, region.h);

    let output = Command::new("/usr/sbin/screencapture")
        .args(["-R", &rect, "-t", "png", "-x", "-o"])
        .arg(tmp.path())
        .output()
        .map_err(|e| CaptureError::BackendFailed(format!("screencapture spawn: {e}")))?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        let lower = stderr.to_lowercase();
        if lower.contains("not authorized")
            || lower.contains("permission")
            || lower.contains("screen recording")
        {
            return Err(CaptureError::PermissionDenied(stderr.into_owned()));
        }
        return Err(CaptureError::BackendFailed(format!(
            "screencapture exited with {}: {}",
            output.status, stderr
        )));
    }

    let image: RgbaImage = ImageReader::open(tmp.path())
        .map_err(|e| CaptureError::BackendFailed(format!("open captured png: {e}")))?
        .decode()
        .map_err(|e| CaptureError::BackendFailed(format!("decode captured png: {e}")))?
        .to_rgba8();

    Ok(Frame {
        width: image.width(),
        height: image.height(),
        rgba: image.into_raw(),
    })
}

/// Translate an xcap error into a CaptureError, preserving permission-denial
/// specifically so the user gets actionable advice on macOS.
fn classify_xcap_error(err: xcap::XCapError, op: &str) -> CaptureError {
    let msg = err.to_string();
    let lower = msg.to_lowercase();
    if lower.contains("permission")
        || lower.contains("not authorized")
        || lower.contains("screen recording")
        || lower.contains("tccd")
    {
        CaptureError::PermissionDenied(msg)
    } else {
        CaptureError::BackendFailed(format!("{op}: {msg}"))
    }
}
