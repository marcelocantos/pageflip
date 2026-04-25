// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestRevisitTrackerExactMatch(t *testing.T) {
	r := newRevisitTracker(5)
	revisit, idx := r.classify("1234abcd")
	if revisit {
		t.Fatal("first sighting should not be a revisit")
	}
	if idx != 0 {
		t.Fatalf("want idx=0, got %d", idx)
	}
	revisit, idx = r.classify("1234abcd")
	if !revisit {
		t.Fatal("second sighting of the same hex must be a revisit")
	}
	if idx != 0 {
		t.Fatalf("want idx=0 on revisit, got %d", idx)
	}
}

func TestRevisitTrackerDistinctSlides(t *testing.T) {
	r := newRevisitTracker(0)
	_, a := r.classify("0000000000000000")
	_, b := r.classify("ffffffffffffffff")
	if a == b {
		t.Fatal("very-different hashes must get distinct indices")
	}
	revisit, _ := r.classify("ffffffffffffffff")
	if !revisit {
		t.Fatal("identical repeat should be a revisit")
	}
}

func TestRevisitTrackerHammingThreshold(t *testing.T) {
	r := newRevisitTracker(2)
	r.classify("00")                     // 0x00 = 0b00000000
	revisit, _ := r.classify("03")       // 0x03 = 0b00000011, distance 2
	if !revisit {
		t.Fatal("distance 2 within threshold 2 must count as revisit")
	}
	revisit2, _ := r.classify("07")      // distance 3, above threshold
	if revisit2 {
		t.Fatal("distance 3 above threshold 2 must not count as revisit")
	}
}

func TestRevisitTrackerMalformedHash(t *testing.T) {
	r := newRevisitTracker(2)
	if revisit, _ := r.classify("not-hex"); revisit {
		t.Fatal("malformed hash should never be a revisit")
	}
	if revisit, _ := r.classify("not-hex"); revisit {
		t.Fatal("repeated malformed hash must still fail open — identical empty/unparseable strings across unrelated events must not be conflated")
	}
	if revisit, _ := r.classify(""); revisit {
		t.Fatal("empty hash must never be a revisit")
	}
	if revisit, _ := r.classify(""); revisit {
		t.Fatal("repeated empty hash must never be a revisit — this is the bug that caused only the first slide to render in live runs")
	}
}

// TestRevisitTrackerEmptyDoesNotPoisonRealHashes ensures that empty
// phash events (e.g. from an old pageflip binary) do not pollute the
// tracker's state and cause later real hashes to be misclassified.
func TestRevisitTrackerEmptyDoesNotPoisonRealHashes(t *testing.T) {
	r := newRevisitTracker(5)
	_, _ = r.classify("")
	_, _ = r.classify("")
	revisit, idx := r.classify("deadbeefcafef00d")
	if revisit {
		t.Fatal("first real hash after empties must classify as new")
	}
	if idx != 0 {
		t.Fatalf("want first real hash at idx=0, got %d", idx)
	}
	revisit2, idx2 := r.classify("deadbeefcafef00d")
	if !revisit2 {
		t.Fatal("exact repeat of real hash must be a revisit")
	}
	if idx2 != 0 {
		t.Fatalf("want revisit pointing at idx=0, got %d", idx2)
	}
}
