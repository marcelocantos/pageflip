// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Public API — CLI wiring lives in main.rs (not yet integrated).
#![allow(dead_code)]

//! Regex-based PII detection over a slice of OCR words.
//!
//! Patterns are compiled once on first use via `std::sync::OnceLock`.
//! Detection works by building a single concatenated string from the OCR
//! words (space-separated), running each regex against that string, then
//! mapping each match back to the word(s) it overlaps.

use regex::Regex;
use std::sync::OnceLock;

use crate::redact::{ocr::OcrWord, PixelRect};

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

/// The category of personally-identifiable information that was detected.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PiiKind {
    Email,
    Phone,
    CreditCard,
    GovernmentId,
    IpAddress,
}

/// A PII match — one or more adjacent OCR words that together form a PII
/// token, along with the bounding box that covers all of them.
#[derive(Clone, Debug)]
pub struct PiiMatch {
    pub kind: PiiKind,
    /// Indices into the original `words` slice.
    pub word_indices: Vec<usize>,
    /// Union of the bounding boxes of all matched words.
    pub bbox: PixelRect,
}

// ---------------------------------------------------------------------------
// Regex patterns — compiled once on first use via OnceLock (stable ≥ 1.70)
// ---------------------------------------------------------------------------

fn re_email() -> &'static Regex {
    static CELL: OnceLock<Regex> = OnceLock::new();
    CELL.get_or_init(|| Regex::new(r"(?i)\b[\w.+\-]+@[\w\-]+\.[\w.\-]+\b").unwrap())
}

fn re_phone() -> &'static Regex {
    static CELL: OnceLock<Regex> = OnceLock::new();
    CELL.get_or_init(|| {
        Regex::new(r"\b(\+?\d{1,3}[.\s\-]?)?\(?\d{2,4}\)?[.\s\-]?\d{3,4}[.\s\-]?\d{3,4}\b").unwrap()
    })
}

fn re_credit_card() -> &'static Regex {
    static CELL: OnceLock<Regex> = OnceLock::new();
    CELL.get_or_init(|| Regex::new(r"\b\d{4}[\s\-]?\d{4}[\s\-]?\d{4}[\s\-]?\d{4}\b").unwrap())
}

fn re_government_id() -> &'static Regex {
    static CELL: OnceLock<Regex> = OnceLock::new();
    // US SSN: ddd-dd-dddd (with or without hyphens)
    CELL.get_or_init(|| Regex::new(r"\b\d{3}[\-]?\d{2}[\-]?\d{4}\b").unwrap())
}

fn re_ip_address() -> &'static Regex {
    static CELL: OnceLock<Regex> = OnceLock::new();
    CELL.get_or_init(|| Regex::new(r"\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b").unwrap())
}

/// Ordered so that more-specific patterns are checked before less-specific
/// ones (e.g. credit card before phone, which both consist of digits).
fn patterns() -> [(PiiKind, &'static Regex); 5] {
    [
        (PiiKind::Email, re_email()),
        (PiiKind::CreditCard, re_credit_card()),
        (PiiKind::GovernmentId, re_government_id()),
        (PiiKind::IpAddress, re_ip_address()),
        (PiiKind::Phone, re_phone()),
    ]
}

// ---------------------------------------------------------------------------
// Detection
// ---------------------------------------------------------------------------

/// Detect PII in a slice of OCR words.
///
/// The words are concatenated with a single space separator so that
/// multi-word tokens (e.g. a credit-card number split by OCR across multiple
/// words) are still matched.  Each regex match is then mapped back to the
/// word(s) whose character spans overlap the match, and the union of their
/// bounding boxes becomes the `PiiMatch::bbox`.
pub fn detect_pii(words: &[OcrWord]) -> Vec<PiiMatch> {
    if words.is_empty() {
        return Vec::new();
    }

    // Build the concatenated string and record the byte-offset of each word.
    let mut text = String::new();
    let mut offsets: Vec<usize> = Vec::with_capacity(words.len());
    for (i, word) in words.iter().enumerate() {
        if i > 0 {
            text.push(' ');
        }
        offsets.push(text.len());
        text.push_str(&word.text);
    }
    // Sentinel: one past the end of the last word.
    offsets.push(text.len());

    let mut matches: Vec<PiiMatch> = Vec::new();

    for (kind, re) in patterns() {
        for m in re.find_iter(&text) {
            let match_start = m.start();
            let match_end = m.end();

            // Find all word indices whose character span overlaps [match_start, match_end).
            let word_indices: Vec<usize> = (0..words.len())
                .filter(|&i| {
                    let word_start = offsets[i];
                    let word_end = offsets[i + 1];
                    word_start < match_end && word_end > match_start
                })
                .collect();

            if word_indices.is_empty() {
                continue;
            }

            let bbox = union_bbox(word_indices.iter().map(|&i| words[i].bbox));
            matches.push(PiiMatch {
                kind,
                word_indices,
                bbox,
            });
        }
    }

    matches
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Compute the bounding box that covers all given pixel rects.
pub fn union_bbox(rects: impl Iterator<Item = PixelRect>) -> PixelRect {
    let mut x0 = u32::MAX;
    let mut y0 = u32::MAX;
    let mut x1 = 0u32;
    let mut y1 = 0u32;

    for r in rects {
        x0 = x0.min(r.x);
        y0 = y0.min(r.y);
        x1 = x1.max(r.x + r.w);
        y1 = y1.max(r.y + r.h);
    }

    if x0 == u32::MAX {
        return PixelRect {
            x: 0,
            y: 0,
            w: 0,
            h: 0,
        };
    }

    PixelRect {
        x: x0,
        y: y0,
        w: x1 - x0,
        h: y1 - y0,
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::redact::PixelRect;

    fn word(text: &str) -> OcrWord {
        OcrWord {
            text: text.to_string(),
            bbox: PixelRect {
                x: 0,
                y: 0,
                w: 10,
                h: 10,
            },
        }
    }

    fn words(texts: &[&str]) -> Vec<OcrWord> {
        texts
            .iter()
            .enumerate()
            .map(|(i, t)| OcrWord {
                text: t.to_string(),
                bbox: PixelRect {
                    x: i as u32 * 20,
                    y: 0,
                    w: 18,
                    h: 10,
                },
            })
            .collect()
    }

    fn kinds(matches: &[PiiMatch]) -> Vec<PiiKind> {
        matches.iter().map(|m| m.kind).collect()
    }

    // ----- Email -----

    #[test]
    fn detects_email() {
        let ws = vec![word("user@example.com")];
        let m = detect_pii(&ws);
        assert!(
            kinds(&m).contains(&PiiKind::Email),
            "expected Email in {m:?}"
        );
    }

    #[test]
    fn detects_email_plus_addressing() {
        let ws = vec![word("user+tag@sub.example.org")];
        let m = detect_pii(&ws);
        assert!(kinds(&m).contains(&PiiKind::Email));
    }

    #[test]
    fn no_false_positive_email_no_domain() {
        let ws = vec![word("not-an-email")];
        let m = detect_pii(&ws);
        assert!(!kinds(&m).contains(&PiiKind::Email));
    }

    // ----- Phone -----

    #[test]
    fn detects_phone_us_format() {
        let ws = vec![word("415-555-1234")];
        let m = detect_pii(&ws);
        assert!(
            kinds(&m).contains(&PiiKind::Phone),
            "expected Phone in {m:?}"
        );
    }

    #[test]
    fn detects_phone_international() {
        let ws = vec![word("+1-800-555-0100")];
        let m = detect_pii(&ws);
        assert!(kinds(&m).contains(&PiiKind::Phone));
    }

    #[test]
    fn no_false_positive_short_number() {
        // A 3-digit number should not match as a phone.
        let ws = vec![word("123")];
        let m = detect_pii(&ws);
        assert!(!kinds(&m).contains(&PiiKind::Phone));
    }

    // ----- Credit card -----

    #[test]
    fn detects_credit_card_spaced() {
        let ws = vec![word("4111 1111 1111 1111")];
        let m = detect_pii(&ws);
        assert!(
            kinds(&m).contains(&PiiKind::CreditCard),
            "expected CreditCard in {m:?}"
        );
    }

    #[test]
    fn detects_credit_card_dashes() {
        let ws = vec![word("4111-1111-1111-1111")];
        let m = detect_pii(&ws);
        assert!(kinds(&m).contains(&PiiKind::CreditCard));
    }

    #[test]
    fn detects_credit_card_contiguous() {
        let ws = vec![word("4111111111111111")];
        let m = detect_pii(&ws);
        assert!(kinds(&m).contains(&PiiKind::CreditCard));
    }

    #[test]
    fn no_false_positive_12_digit_number() {
        // 12 digits — too short for a credit card.
        let ws = vec![word("123456789012")];
        let m = detect_pii(&ws);
        assert!(!kinds(&m).contains(&PiiKind::CreditCard));
    }

    // ----- Government ID (SSN) -----

    #[test]
    fn detects_ssn_with_hyphens() {
        let ws = vec![word("123-45-6789")];
        let m = detect_pii(&ws);
        assert!(
            kinds(&m).contains(&PiiKind::GovernmentId),
            "expected GovernmentId in {m:?}"
        );
    }

    #[test]
    fn detects_ssn_without_hyphens() {
        let ws = vec![word("123456789")];
        let m = detect_pii(&ws);
        assert!(kinds(&m).contains(&PiiKind::GovernmentId));
    }

    // ----- IP address -----

    #[test]
    fn detects_ipv4() {
        let ws = vec![word("192.168.1.1")];
        let m = detect_pii(&ws);
        assert!(
            kinds(&m).contains(&PiiKind::IpAddress),
            "expected IpAddress in {m:?}"
        );
    }

    #[test]
    fn no_false_positive_plain_text() {
        let ws = vec![word("Hello"), word("world")];
        let m = detect_pii(&ws);
        assert!(m.is_empty(), "expected no PII matches for plain text");
    }

    // ----- Multi-word matching -----

    #[test]
    fn multi_word_credit_card() {
        // OCR may split a credit card across several words.
        let ws = words(&["4111", "1111", "1111", "1111"]);
        let m = detect_pii(&ws);
        assert!(
            kinds(&m).contains(&PiiKind::CreditCard),
            "expected CreditCard in {m:?}"
        );
        let cc = m.iter().find(|x| x.kind == PiiKind::CreditCard).unwrap();
        // All four words should be included.
        assert_eq!(cc.word_indices.len(), 4);
    }

    // ----- Empty input -----

    #[test]
    fn empty_words_returns_empty() {
        assert!(detect_pii(&[]).is_empty());
    }

    // ----- union_bbox -----

    #[test]
    fn union_bbox_single() {
        let r = PixelRect {
            x: 10,
            y: 20,
            w: 30,
            h: 40,
        };
        let u = union_bbox([r].into_iter());
        assert_eq!((u.x, u.y, u.w, u.h), (10, 20, 30, 40));
    }

    #[test]
    fn union_bbox_two_adjacent() {
        let r1 = PixelRect {
            x: 0,
            y: 0,
            w: 10,
            h: 10,
        };
        let r2 = PixelRect {
            x: 20,
            y: 5,
            w: 10,
            h: 10,
        };
        let u = union_bbox([r1, r2].into_iter());
        assert_eq!((u.x, u.y, u.w, u.h), (0, 0, 30, 15));
    }
}
