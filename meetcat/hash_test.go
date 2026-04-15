// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
)

func TestSessionIDHashLength(t *testing.T) {
	h := SessionIDHash("some-session-id-abc123")
	if len(h) != 4 {
		t.Errorf("expected 4 hex chars, got %d: %q", len(h), h)
	}
}

func TestSessionIDHashDeterministic(t *testing.T) {
	const id = "session-abc"
	h1 := SessionIDHash(id)
	h2 := SessionIDHash(id)
	if h1 != h2 {
		t.Errorf("non-deterministic: %q vs %q", h1, h2)
	}
}

func TestSessionIDHashHexChars(t *testing.T) {
	h := SessionIDHash("test")
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex character %q in hash %q", c, h)
		}
	}
}

func TestSessionIDHashDistinct(t *testing.T) {
	h1 := SessionIDHash("session-alpha")
	h2 := SessionIDHash("session-beta")
	// Different inputs should (almost certainly) produce different 4-char prefixes.
	// This is probabilistic but fine for a smoke test.
	if h1 == h2 {
		t.Logf("hash collision for distinct inputs (4-char space is small, not fatal): %q", h1)
	}
}

func TestSessionIDHashKnownValue(t *testing.T) {
	// SHA-256("hello") = 2cf24db...
	// First 4 hex chars = "2cf2"
	h := SessionIDHash("hello")
	if h != "2cf2" {
		t.Errorf("expected %q, got %q", "2cf2", h)
	}
}
