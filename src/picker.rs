// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

use std::fmt;
use std::num::NonZeroU32;
use std::rc::Rc;

use softbuffer::{Context, Surface};
use winit::application::ApplicationHandler;
use winit::dpi::PhysicalPosition;
use winit::event::{ElementState, MouseButton, WindowEvent};
use winit::event_loop::{ActiveEventLoop, EventLoop};
use winit::keyboard::{Key, NamedKey};
use winit::window::{CursorIcon, Fullscreen, Window, WindowId, WindowLevel};

use crate::capture::Region;

#[derive(Debug)]
pub enum PickerError {
    EventLoop(String),
    Surface(String),
    NoMonitor,
}

impl fmt::Display for PickerError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            PickerError::EventLoop(m) => write!(f, "picker event loop failed: {m}"),
            PickerError::Surface(m) => write!(f, "picker surface failed: {m}"),
            PickerError::NoMonitor => write!(f, "no primary monitor available for picker overlay"),
        }
    }
}

impl std::error::Error for PickerError {}

/// Open a fullscreen overlay on the primary monitor and return the region the
/// user draws (in logical points, absolute screen coordinates), or `Ok(None)`
/// if the user pressed Escape / closed the window.
pub fn pick_region() -> Result<Option<Region>, PickerError> {
    let event_loop = EventLoop::new().map_err(|e| PickerError::EventLoop(e.to_string()))?;
    let mut app = PickerApp::default();
    event_loop
        .run_app(&mut app)
        .map_err(|e| PickerError::EventLoop(e.to_string()))?;
    if let Some(err) = app.error {
        return Err(err);
    }
    Ok(app.result)
}

#[derive(Default)]
struct PickerApp {
    window: Option<Rc<Window>>,
    surface: Option<Surface<Rc<Window>, Rc<Window>>>,
    monitor_origin: (i32, i32),
    scale_factor: f64,
    pointer_px: Option<PhysicalPosition<f64>>,
    start_px: Option<PhysicalPosition<f64>>,
    result: Option<Region>,
    error: Option<PickerError>,
}

impl ApplicationHandler for PickerApp {
    fn resumed(&mut self, event_loop: &ActiveEventLoop) {
        let monitor = match event_loop.primary_monitor() {
            Some(m) => m,
            None => {
                self.error = Some(PickerError::NoMonitor);
                event_loop.exit();
                return;
            }
        };
        let origin = monitor.position();
        self.monitor_origin = (origin.x, origin.y);

        let attrs = Window::default_attributes()
            .with_title("pageflip region picker")
            .with_transparent(true)
            .with_decorations(false)
            .with_resizable(false)
            .with_window_level(WindowLevel::AlwaysOnTop)
            .with_fullscreen(Some(Fullscreen::Borderless(Some(monitor))));
        let window = match event_loop.create_window(attrs) {
            Ok(w) => Rc::new(w),
            Err(e) => {
                self.error = Some(PickerError::Surface(e.to_string()));
                event_loop.exit();
                return;
            }
        };
        window.set_cursor(CursorIcon::Crosshair);

        self.scale_factor = window.scale_factor();
        let size = window.inner_size();

        let context = match Context::new(window.clone()) {
            Ok(c) => c,
            Err(e) => {
                self.error = Some(PickerError::Surface(e.to_string()));
                event_loop.exit();
                return;
            }
        };
        let mut surface = match Surface::new(&context, window.clone()) {
            Ok(s) => s,
            Err(e) => {
                self.error = Some(PickerError::Surface(e.to_string()));
                event_loop.exit();
                return;
            }
        };
        if let (Some(w), Some(h)) = (NonZeroU32::new(size.width), NonZeroU32::new(size.height)) {
            if let Err(e) = surface.resize(w, h) {
                self.error = Some(PickerError::Surface(e.to_string()));
                event_loop.exit();
                return;
            }
        }

        self.window = Some(window);
        self.surface = Some(surface);
    }

    fn window_event(&mut self, event_loop: &ActiveEventLoop, _id: WindowId, event: WindowEvent) {
        match event {
            WindowEvent::CloseRequested => {
                event_loop.exit();
            }
            WindowEvent::KeyboardInput { event, .. } => {
                if event.state == ElementState::Pressed
                    && matches!(event.logical_key, Key::Named(NamedKey::Escape))
                {
                    self.result = None;
                    event_loop.exit();
                }
            }
            WindowEvent::CursorMoved { position, .. } => {
                self.pointer_px = Some(position);
                if self.start_px.is_some() {
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            }
            WindowEvent::MouseInput {
                state,
                button: MouseButton::Left,
                ..
            } => match state {
                ElementState::Pressed => {
                    self.start_px = self.pointer_px;
                }
                ElementState::Released => {
                    if let (Some(a), Some(b)) = (self.start_px, self.pointer_px) {
                        if let Some(region) = self.region_from_drag(a, b) {
                            self.result = Some(region);
                            event_loop.exit();
                            return;
                        }
                    }
                    self.start_px = None;
                    if let Some(w) = &self.window {
                        w.request_redraw();
                    }
                }
            },
            WindowEvent::RedrawRequested => {
                self.render();
            }
            WindowEvent::Resized(size) => {
                if let Some(surface) = &mut self.surface {
                    if let (Some(w), Some(h)) =
                        (NonZeroU32::new(size.width), NonZeroU32::new(size.height))
                    {
                        let _ = surface.resize(w, h);
                    }
                }
                if let Some(w) = &self.window {
                    w.request_redraw();
                }
            }
            WindowEvent::ScaleFactorChanged { scale_factor, .. } => {
                self.scale_factor = scale_factor;
            }
            _ => {}
        }
    }
}

impl PickerApp {
    /// Translate two pointer positions (window-physical pixels) into a
    /// `Region` in logical points, absolute to the virtual desktop, clamped
    /// to a positive-area rectangle.
    fn region_from_drag(
        &self,
        a: PhysicalPosition<f64>,
        b: PhysicalPosition<f64>,
    ) -> Option<Region> {
        let scale = self.scale_factor;
        let ax = a.x / scale;
        let ay = a.y / scale;
        let bx = b.x / scale;
        let by = b.y / scale;
        let x = ax.min(bx).round() as i32 + self.monitor_origin.0;
        let y = ay.min(by).round() as i32 + self.monitor_origin.1;
        let w = (ax - bx).abs().round() as u32;
        let h = (ay - by).abs().round() as u32;
        if w == 0 || h == 0 {
            return None;
        }
        Some(Region { x, y, w, h })
    }

    fn render(&mut self) {
        let Some(window) = self.window.as_ref() else {
            return;
        };
        let Some(surface) = self.surface.as_mut() else {
            return;
        };
        let size = window.inner_size();
        if size.width == 0 || size.height == 0 {
            return;
        }

        let Ok(mut buffer) = surface.buffer_mut() else {
            return;
        };

        // Clear: softbuffer presents 0x00RRGGBB into a transparent window. A
        // value of 0 is "no contribution", so the rest of the overlay reveals
        // the screen beneath. The rubber-band rectangle itself is drawn with
        // bright opaque pixels so it's visible against any background.
        for px in buffer.iter_mut() {
            *px = 0;
        }

        if let (Some(start_px), Some(cur_px)) = (self.start_px, self.pointer_px) {
            let x0 = start_px.x.min(cur_px.x).round() as i32;
            let y0 = start_px.y.min(cur_px.y).round() as i32;
            let x1 = start_px.x.max(cur_px.x).round() as i32;
            let y1 = start_px.y.max(cur_px.y).round() as i32;
            draw_rect_outline(
                &mut buffer,
                size.width as i32,
                size.height as i32,
                Rect { x0, y0, x1, y1 },
                0xFFFF66FF,
                2,
            );
        }

        window.pre_present_notify();
        let _ = buffer.present();
    }
}

struct Rect {
    x0: i32,
    y0: i32,
    x1: i32,
    y1: i32,
}

fn draw_rect_outline(
    buf: &mut [u32],
    width: i32,
    height: i32,
    rect: Rect,
    colour: u32,
    thickness: i32,
) {
    let x_lo = rect.x0.clamp(0, width.saturating_sub(1));
    let y_lo = rect.y0.clamp(0, height.saturating_sub(1));
    let x_hi = rect.x1.clamp(0, width.saturating_sub(1));
    let y_hi = rect.y1.clamp(0, height.saturating_sub(1));
    for t in 0..thickness {
        for x in x_lo..=x_hi {
            for &ty in &[y_lo + t, y_hi - t] {
                if (0..height).contains(&ty) && (0..width).contains(&x) {
                    buf[(ty * width + x) as usize] = colour;
                }
            }
        }
        for y in y_lo..=y_hi {
            for &tx in &[x_lo + t, x_hi - t] {
                if (0..width).contains(&tx) && (0..height).contains(&y) {
                    buf[(y * width + tx) as usize] = colour;
                }
            }
        }
    }
}
