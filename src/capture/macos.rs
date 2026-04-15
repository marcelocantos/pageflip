// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use xcap::Monitor;

use super::{Capture, CaptureError, Frame, Region};

pub struct MacOsCapture {
    monitors: Vec<Monitor>,
}

impl MacOsCapture {
    pub fn new() -> Result<Self, CaptureError> {
        let monitors = Monitor::all().map_err(|e| classify_xcap_error(e, "Monitor::all"))?;
        if monitors.is_empty() {
            return Err(CaptureError::BackendUnavailable(
                "no monitors detected".to_string(),
            ));
        }
        Ok(Self { monitors })
    }
}

impl Capture for MacOsCapture {
    fn capture(&self, region: Region) -> Result<Frame, CaptureError> {
        let monitor = find_monitor_for_region(&self.monitors, region)?;

        let mon_x = monitor
            .x()
            .map_err(|e| classify_xcap_error(e, "Monitor::x"))?;
        let mon_y = monitor
            .y()
            .map_err(|e| classify_xcap_error(e, "Monitor::y"))?;

        // Translate absolute screen coordinates into monitor-local coordinates.
        // find_monitor_for_region guarantees region.x >= mon_x and region.y >= mon_y,
        // so the subtraction is non-negative and safe to cast to u32.
        let local_x = (region.x - mon_x) as u32;
        let local_y = (region.y - mon_y) as u32;

        let image = monitor
            .capture_region(local_x, local_y, region.w, region.h)
            .map_err(|e| classify_xcap_error(e, "Monitor::capture_region"))?;

        Ok(Frame {
            width: image.width(),
            height: image.height(),
            rgba: image.into_raw(),
        })
    }
}

fn find_monitor_for_region(monitors: &[Monitor], region: Region) -> Result<&Monitor, CaptureError> {
    let containing: Vec<&Monitor> = monitors
        .iter()
        .filter(|m| monitor_contains(m, region.x, region.y))
        .collect();

    if containing.is_empty() {
        return Err(CaptureError::RegionOffScreen(region));
    }

    // Use the first monitor whose bounds fully contain the requested region.
    // If none does, the region straddles a monitor boundary.
    let fully_containing = containing
        .iter()
        .copied()
        .find(|m| monitor_contains_rect(m, region.x, region.y, region.w as i32, region.h as i32));

    fully_containing.ok_or(CaptureError::RegionSpansMonitors(region))
}

fn monitor_contains(monitor: &Monitor, px: i32, py: i32) -> bool {
    let Ok(mx) = monitor.x() else { return false };
    let Ok(my) = monitor.y() else { return false };
    let Ok(mw) = monitor.width() else {
        return false;
    };
    let Ok(mh) = monitor.height() else {
        return false;
    };
    px >= mx && py >= my && px < mx + mw as i32 && py < my + mh as i32
}

fn monitor_contains_rect(monitor: &Monitor, x: i32, y: i32, w: i32, h: i32) -> bool {
    let Ok(mx) = monitor.x() else { return false };
    let Ok(my) = monitor.y() else { return false };
    let Ok(mw) = monitor.width() else {
        return false;
    };
    let Ok(mh) = monitor.height() else {
        return false;
    };
    x >= mx && y >= my && x + w <= mx + mw as i32 && y + h <= my + mh as i32
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
