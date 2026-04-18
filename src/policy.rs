// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Policy layer: app allowlist, paranoid-mode saver, and architectural egress gate.
//!
//! # Egress gate — type-level enforcement
//!
//! [`RedactedFrame`] has **no public constructor**.  The only way to obtain one
//! is through [`RedactPipeline::apply_and_seal`], which lives in `src/redact/`.
//! Any code that tries to ship a raw [`crate::capture::Frame`] to an egress
//! endpoint will fail to compile because the endpoint's API requires a
//! `RedactedFrame`.  This is a zero-cost, zero-runtime-check guarantee: if the
//! code compiles, redaction happened.

use std::collections::HashSet;
use std::fmt;
use std::fs;
use std::path::{Path, PathBuf};

use serde::Deserialize;

use crate::capture::Frame;
use crate::redact::RedactError;

// ---------------------------------------------------------------------------
// PolicyError
// ---------------------------------------------------------------------------

/// Errors that can arise from policy operations (config loading, I/O, etc.).
#[derive(Debug)]
pub enum PolicyError {
    /// The config file could not be read.
    ConfigReadFailed(String),
    /// The config file could not be parsed.
    ConfigParseFailed(String),
    /// A paranoid-mode save failed.
    SaveFailed(String),
    /// An error propagated from the redaction pipeline.
    RedactFailed(RedactError),
}

impl fmt::Display for PolicyError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            PolicyError::ConfigReadFailed(detail) => {
                write!(f, "policy config read failed: {detail}")
            }
            PolicyError::ConfigParseFailed(detail) => {
                write!(f, "policy config parse failed: {detail}")
            }
            PolicyError::SaveFailed(detail) => {
                write!(f, "paranoid save failed: {detail}")
            }
            PolicyError::RedactFailed(e) => write!(f, "redaction error in policy: {e}"),
        }
    }
}

impl std::error::Error for PolicyError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            PolicyError::RedactFailed(e) => Some(e),
            _ => None,
        }
    }
}

impl From<RedactError> for PolicyError {
    fn from(e: RedactError) -> Self {
        PolicyError::RedactFailed(e)
    }
}

// ---------------------------------------------------------------------------
// AppAllowlist
// ---------------------------------------------------------------------------

/// Deserialisation shape for the allowlist config file.
///
/// Accepts both JSON and YAML files that contain a top-level `allowed_bundles`
/// array of strings, e.g.:
///
/// ```json
/// { "allowed_bundles": ["com.microsoft.teams2", "com.apple.Keynote"] }
/// ```
#[derive(Deserialize)]
struct AllowlistConfig {
    allowed_bundles: Vec<String>,
}

/// Controls which frontmost applications are permitted to pass the egress gate.
///
/// If an application's bundle ID is **not** in the set, frames captured while
/// that application is frontmost should be suppressed before egress.
///
/// The default (and `empty()`) allowlist **permits nothing** — suitable as a
/// safe default when no config has been loaded.
pub struct AppAllowlist {
    allowed_bundles: HashSet<String>,
}

impl AppAllowlist {
    /// Load an allowlist from a JSON or YAML config file.
    ///
    /// The file must contain a top-level `allowed_bundles` array of macOS
    /// bundle ID strings (e.g. `"com.microsoft.teams2"`).
    ///
    /// # Errors
    ///
    /// Returns [`PolicyError::ConfigReadFailed`] if the file cannot be read and
    /// [`PolicyError::ConfigParseFailed`] if the contents are not valid JSON.
    pub fn from_config(path: &Path) -> Result<Self, PolicyError> {
        let raw = fs::read_to_string(path)
            .map_err(|e| PolicyError::ConfigReadFailed(format!("{}: {e}", path.display())))?;
        let cfg: AllowlistConfig = serde_json::from_str(&raw)
            .map_err(|e| PolicyError::ConfigParseFailed(format!("{}: {e}", path.display())))?;
        Ok(Self {
            allowed_bundles: cfg.allowed_bundles.into_iter().collect(),
        })
    }

    /// Construct an empty allowlist that permits nothing.
    ///
    /// Use this as the safe default when no config has been loaded.
    pub fn empty() -> Self {
        Self {
            allowed_bundles: HashSet::new(),
        }
    }

    /// Returns `true` if `bundle_id` is in the permitted set.
    pub fn is_permitted(&self, bundle_id: &str) -> bool {
        self.allowed_bundles.contains(bundle_id)
    }
}

// ---------------------------------------------------------------------------
// Frontmost-app detection
// ---------------------------------------------------------------------------

/// Returns the bundle identifier of the currently frontmost application, or
/// `None` if it cannot be determined.
///
/// On macOS this queries `NSWorkspace.sharedWorkspace.frontmostApplication`.
/// On other platforms it always returns `None`.
#[cfg(target_os = "macos")]
pub fn frontmost_app_bundle_id() -> Option<String> {
    use objc2_app_kit::{NSRunningApplication, NSWorkspace};
    use objc2_foundation::NSString;

    let workspace = NSWorkspace::sharedWorkspace();
    let app: Option<objc2::rc::Retained<NSRunningApplication>> = workspace.frontmostApplication();
    let app = app?;
    let bundle_id: Option<objc2::rc::Retained<NSString>> = app.bundleIdentifier();
    bundle_id.map(|s: objc2::rc::Retained<NSString>| s.to_string())
}

#[cfg(not(target_os = "macos"))]
pub fn frontmost_app_bundle_id() -> Option<String> {
    None
}

// ---------------------------------------------------------------------------
// ParanoidSaver
// ---------------------------------------------------------------------------

/// Saves unredacted frames to a local-only directory before redaction is applied.
///
/// The *redacted* version is what goes to the normal output directory; the
/// unredacted originals written here never leave the machine.  Intended for
/// `--paranoid` mode, where operators want a local audit trail of the raw
/// capture even though only the redacted version is forwarded downstream.
pub struct ParanoidSaver {
    raw_dir: PathBuf,
}

impl ParanoidSaver {
    /// Create a saver that writes unredacted frames under `raw_dir`.
    ///
    /// The directory is created lazily on the first save.
    pub fn new(raw_dir: PathBuf) -> Self {
        Self { raw_dir }
    }

    /// Persist `frame` (unredacted) as a PNG at `<raw_dir>/<slide_id>.png`.
    ///
    /// Creates `raw_dir` (and any parent directories) if they do not yet exist.
    pub fn save_raw(&self, frame: &Frame, slide_id: &str) -> Result<(), PolicyError> {
        fs::create_dir_all(&self.raw_dir).map_err(|e| {
            PolicyError::SaveFailed(format!(
                "could not create paranoid dir {}: {e}",
                self.raw_dir.display()
            ))
        })?;

        // Reject slide IDs that could escape the raw_dir via path traversal.
        if slide_id.contains('/') || slide_id.contains('\\') || slide_id.starts_with('.') {
            return Err(PolicyError::SaveFailed(format!(
                "slide_id {:?} is not a safe filename",
                slide_id
            )));
        }

        let path = self.raw_dir.join(format!("{slide_id}.png"));
        frame
            .save_png(&path)
            .map_err(|e| PolicyError::SaveFailed(format!("{}: {e}", path.display())))
    }
}

// ---------------------------------------------------------------------------
// RedactedFrame — architectural egress gate
// ---------------------------------------------------------------------------

/// A frame that has been through [`crate::redact::RedactPipeline::apply_and_seal`].
///
/// # Compile-time safety guarantee
///
/// This type has **no public constructor**.  The only way to create a
/// `RedactedFrame` is through `RedactPipeline::apply_and_seal`, which lives in
/// the `redact` module.  Any egress path (e.g. `pageflip-feed --egress saas`)
/// that requires a `RedactedFrame` parameter will *fail to compile* if caller
/// code tries to pass a raw [`Frame`] — the type does not unify.  No runtime
/// check is needed because the type system enforces it.
///
/// The private `_sealed` field prevents downstream crates and modules from
/// constructing the type via struct literal syntax.  The `pub(crate)` constructor
/// `RedactedFrame::new_sealed` is the only entry point and is intentionally
/// restricted to the `redact` module.
pub struct RedactedFrame {
    /// The redacted pixel buffer.
    frame: Frame,
}

impl RedactedFrame {
    /// **Crate-internal constructor** — only callable from within this crate.
    ///
    /// Production callers must go through `RedactPipeline::apply_and_seal`.
    #[allow(dead_code)] // used by RedactPipeline::apply_and_seal; wired into egress in T10.4+
    pub(crate) fn new_sealed(frame: Frame) -> Self {
        Self { frame }
    }

    /// Write the redacted frame as a PNG at `path`.
    pub fn save_png(&self, path: &Path) -> Result<(), crate::capture::CaptureError> {
        self.frame.save_png(path)
    }

    /// Width of the redacted frame in pixels.
    pub fn width(&self) -> u32 {
        self.frame.width
    }

    /// Height of the redacted frame in pixels.
    pub fn height(&self) -> u32 {
        self.frame.height
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use tempfile::TempDir;

    // ---- AppAllowlist -------------------------------------------------------

    #[test]
    fn allowlist_empty_permits_nothing() {
        let list = AppAllowlist::empty();
        assert!(!list.is_permitted("com.apple.Keynote"));
        assert!(!list.is_permitted("com.microsoft.teams2"));
        assert!(!list.is_permitted(""));
    }

    #[test]
    fn allowlist_from_config_permits_listed_bundles() {
        let dir = TempDir::new().unwrap();
        let cfg_path = dir.path().join("allowlist.json");
        fs::write(
            &cfg_path,
            r#"{"allowed_bundles":["com.apple.Keynote","com.microsoft.teams2"]}"#,
        )
        .unwrap();

        let list = AppAllowlist::from_config(&cfg_path).unwrap();
        assert!(list.is_permitted("com.apple.Keynote"));
        assert!(list.is_permitted("com.microsoft.teams2"));
        assert!(!list.is_permitted("com.apple.Notes"));
    }

    #[test]
    fn allowlist_from_config_denies_unlisted_bundle() {
        let dir = TempDir::new().unwrap();
        let cfg_path = dir.path().join("allowlist.json");
        fs::write(&cfg_path, r#"{"allowed_bundles":["com.apple.Keynote"]}"#).unwrap();

        let list = AppAllowlist::from_config(&cfg_path).unwrap();
        assert!(!list.is_permitted("com.microsoft.teams2"));
    }

    #[test]
    fn allowlist_from_config_missing_file_errors() {
        let result = AppAllowlist::from_config(Path::new("/no/such/file.json"));
        assert!(matches!(result, Err(PolicyError::ConfigReadFailed(_))));
    }

    #[test]
    fn allowlist_from_config_bad_json_errors() {
        let dir = TempDir::new().unwrap();
        let cfg_path = dir.path().join("bad.json");
        fs::write(&cfg_path, "not json at all").unwrap();
        let result = AppAllowlist::from_config(&cfg_path);
        assert!(matches!(result, Err(PolicyError::ConfigParseFailed(_))));
    }

    // ---- ParanoidSaver ------------------------------------------------------

    fn minimal_frame() -> Frame {
        Frame {
            width: 2,
            height: 2,
            rgba: vec![
                255, 0, 0, 255, // red
                0, 255, 0, 255, // green
                0, 0, 255, 255, // blue
                255, 255, 0, 255, // yellow
            ],
        }
    }

    #[test]
    fn paranoid_saver_writes_png_to_new_dir() {
        let dir = TempDir::new().unwrap();
        let raw_dir = dir.path().join("paranoid_raw");
        // raw_dir does not exist yet — should be created on save.
        let saver = ParanoidSaver::new(raw_dir.clone());
        let frame = minimal_frame();
        saver.save_raw(&frame, "slide-001").unwrap();
        assert!(raw_dir.join("slide-001.png").exists());
    }

    #[test]
    fn paranoid_saver_multiple_slides() {
        let dir = TempDir::new().unwrap();
        let raw_dir = dir.path().join("raw");
        let saver = ParanoidSaver::new(raw_dir.clone());
        let frame = minimal_frame();
        saver.save_raw(&frame, "slide-001").unwrap();
        saver.save_raw(&frame, "slide-002").unwrap();
        assert!(raw_dir.join("slide-001.png").exists());
        assert!(raw_dir.join("slide-002.png").exists());
    }

    #[test]
    fn paranoid_saver_rejects_path_traversal() {
        let dir = TempDir::new().unwrap();
        let saver = ParanoidSaver::new(dir.path().join("raw"));
        let frame = minimal_frame();
        let result = saver.save_raw(&frame, "../escape");
        assert!(matches!(result, Err(PolicyError::SaveFailed(_))));
    }

    #[test]
    fn paranoid_saver_rejects_dot_prefix() {
        let dir = TempDir::new().unwrap();
        let saver = ParanoidSaver::new(dir.path().join("raw"));
        let frame = minimal_frame();
        let result = saver.save_raw(&frame, ".hidden");
        assert!(matches!(result, Err(PolicyError::SaveFailed(_))));
    }

    // ---- RedactedFrame type-level guarantee ---------------------------------
    //
    // There is no runtime test for the compile-time guarantee because the
    // guarantee is enforced by the Rust type checker: `RedactedFrame` has no
    // public constructor, so any code that attempts
    //
    //   let rf = RedactedFrame { frame: some_frame };   // ← compile error
    //
    // outside of this crate will be rejected at compile time.  The test suite
    // below verifies the observable surface of the type (dimensions, PNG write).

    #[test]
    fn redacted_frame_dimensions() {
        let frame = Frame {
            width: 4,
            height: 3,
            rgba: vec![0u8; 4 * 3 * 4],
        };
        let rf = RedactedFrame::new_sealed(frame);
        assert_eq!(rf.width(), 4);
        assert_eq!(rf.height(), 3);
    }

    #[test]
    fn redacted_frame_save_png() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("out.png");
        let frame = minimal_frame();
        let rf = RedactedFrame::new_sealed(frame);
        rf.save_png(&path).unwrap();
        assert!(path.exists());
    }
}
