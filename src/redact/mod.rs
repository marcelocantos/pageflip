// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

mod face_blur;

use std::fmt;

use crate::capture::Frame;

pub use face_blur::FaceBlurRedactor;

/// A redactor that can process a captured frame in-place.
pub trait Redactor {
    fn redact(&self, frame: Frame) -> Result<Frame, RedactError>;
}

/// Chains zero or more `Redactor` instances.  Frames flow through in
/// push order; the output of each stage becomes the input of the next.
pub struct RedactPipeline {
    redactors: Vec<Box<dyn Redactor>>,
}

impl RedactPipeline {
    pub fn new() -> Self {
        Self {
            redactors: Vec::new(),
        }
    }

    /// Append a redactor and return `&mut Self` so calls can be chained.
    pub fn push(&mut self, r: Box<dyn Redactor>) -> &mut Self {
        self.redactors.push(r);
        self
    }

    /// Run every registered redactor in order.  Short-circuits on the first
    /// error; the partially-redacted frame is discarded.
    pub fn apply(&self, frame: Frame) -> Result<Frame, RedactError> {
        self.redactors.iter().try_fold(frame, |f, r| r.redact(f))
    }
}

impl Default for RedactPipeline {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Debug)]
pub enum RedactError {
    /// The detection backend (Vision, CoreML) is unavailable or not authorised.
    BackendUnavailable(String),
    /// Detection succeeded but the pixel buffer could not be processed.
    ProcessingFailed(String),
}

impl fmt::Display for RedactError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            RedactError::BackendUnavailable(detail) => {
                write!(f, "redaction backend unavailable: {detail}")
            }
            RedactError::ProcessingFailed(detail) => {
                write!(f, "redaction processing failed: {detail}")
            }
        }
    }
}

impl std::error::Error for RedactError {}

#[cfg(test)]
mod tests {
    use super::*;

    struct IdentityRedactor;
    impl Redactor for IdentityRedactor {
        fn redact(&self, frame: Frame) -> Result<Frame, RedactError> {
            Ok(frame)
        }
    }

    struct PrependRedactor(u8);
    impl Redactor for PrependRedactor {
        fn redact(&self, mut frame: Frame) -> Result<Frame, RedactError> {
            // Tag the first byte so we can verify ordering.
            if !frame.rgba.is_empty() {
                frame.rgba[0] = self.0;
            }
            Ok(frame)
        }
    }

    struct FailRedactor;
    impl Redactor for FailRedactor {
        fn redact(&self, _frame: Frame) -> Result<Frame, RedactError> {
            Err(RedactError::ProcessingFailed(
                "intentional failure".to_string(),
            ))
        }
    }

    fn minimal_frame() -> Frame {
        Frame {
            width: 1,
            height: 1,
            rgba: vec![0, 0, 0, 255],
        }
    }

    // Empty pipeline returns frame unchanged.
    #[test]
    fn empty_pipeline_identity() {
        let pipeline = RedactPipeline::new();
        let frame = minimal_frame();
        let out = pipeline.apply(frame).unwrap();
        assert_eq!(out.rgba, vec![0, 0, 0, 255]);
    }

    // Single identity redactor passes the frame through.
    #[test]
    fn single_identity_redactor() {
        let mut pipeline = RedactPipeline::new();
        pipeline.push(Box::new(IdentityRedactor));
        let frame = minimal_frame();
        let out = pipeline.apply(frame).unwrap();
        assert_eq!(out.rgba, vec![0, 0, 0, 255]);
    }

    // Two redactors run in the order they were pushed.
    #[test]
    fn chaining_order_is_push_order() {
        let mut pipeline = RedactPipeline::new();
        pipeline.push(Box::new(PrependRedactor(42)));
        pipeline.push(Box::new(PrependRedactor(7)));
        let frame = minimal_frame();
        let out = pipeline.apply(frame).unwrap();
        // Last writer wins on byte 0 — proves both stages ran and 7 ran after 42.
        assert_eq!(out.rgba[0], 7);
    }

    // An error from any stage short-circuits and bubbles up.
    #[test]
    fn error_propagates() {
        let mut pipeline = RedactPipeline::new();
        pipeline.push(Box::new(IdentityRedactor));
        pipeline.push(Box::new(FailRedactor));
        pipeline.push(Box::new(IdentityRedactor));
        let result = pipeline.apply(minimal_frame());
        assert!(matches!(result, Err(RedactError::ProcessingFailed(_))));
    }

    // Width/height are preserved through an identity pipeline.
    #[test]
    fn dimensions_preserved() {
        let frame = Frame {
            width: 10,
            height: 20,
            rgba: vec![0u8; 10 * 20 * 4],
        };
        let pipeline = RedactPipeline::new();
        let out = pipeline.apply(frame).unwrap();
        assert_eq!((out.width, out.height), (10, 20));
    }
}
