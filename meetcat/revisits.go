// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/hex"
	"math/bits"
	"sync"
)

// revisitTracker remembers every perceptual-hash meetcat has accepted
// in this session. A presenter flipping back to an earlier slide
// produces a fresh slide event (pageflip's dedup only compares to the
// most-recently-saved frame), but that event's pHash will match a
// prior one within a small Hamming distance — the tracker flags the
// event as a revisit so the caller can log the ordering without
// re-dispatching the slide to the specialists for analysis.
type revisitTracker struct {
	mu        sync.Mutex
	seen      [][]byte // raw bytes of each accepted pHash
	index     map[string]int
	threshold int // max Hamming distance for "same slide"
}

func newRevisitTracker(threshold int) *revisitTracker {
	if threshold < 0 {
		threshold = 0
	}
	return &revisitTracker{
		seen:      make([][]byte, 0, 64),
		index:     make(map[string]int),
		threshold: threshold,
	}
}

// classify reports whether `phashHex` matches a previously-accepted
// hash within the configured Hamming threshold. Returns:
//   - revisit=true, firstIndex=N  — slide seen before as the Nth (0-based) new slide
//   - revisit=false, firstIndex=N — first time we've seen this slide; it is indexed as N
//
// An empty or malformed hash is always classified as "new" and is
// never stored. Colliding empty strings across unrelated events
// must not be conflated into spurious revisits — that was the whole
// reason we hashed the slide, so an absent hash must fail open, not
// match everything that came before.
func (r *revisitTracker) classify(phashHex string) (revisit bool, firstIndex int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	bytes, err := hex.DecodeString(phashHex)
	if err != nil || len(bytes) == 0 {
		// Treat as always-new. Return a synthetic index so the
		// caller can still count "new" arrivals; don't pollute the
		// seen/index tables with a sentinel that would collide.
		return false, len(r.seen)
	}

	// Fast path: exact hex match against a previously-stored hash.
	if idx, ok := r.index[phashHex]; ok {
		return true, idx
	}

	// Hamming scan against every accepted hash of the same length.
	for i, prev := range r.seen {
		if len(prev) != len(bytes) {
			continue
		}
		if hammingDistance(prev, bytes) <= r.threshold {
			return true, i
		}
	}

	idx := len(r.seen)
	r.seen = append(r.seen, bytes)
	r.index[phashHex] = idx
	return false, idx
}

// hammingDistance returns the bit-wise Hamming distance between two
// equal-length byte slices. Caller must ensure lengths match.
func hammingDistance(a, b []byte) int {
	d := 0
	for i := range a {
		d += bits.OnesCount8(a[i] ^ b[i])
	}
	return d
}
