// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"image"
	"math"
	"os"
	"sync"

	// PNG decoder side-effect import.
	_ "image/png"
)

// revisitTracker remembers each accepted slide's downsampled pixel
// buffer and detects when a new slide is a return to a previously-
// seen one via mean per-channel RMS difference.
//
// Why RMS over pHash:
//
// pHash (mean+DCT 8x8 = 64 bits) was designed to find similar
// images — same photo at different scales, brightness, mild
// colour shifts. That is the *wrong* primitive for slide
// deduplication, where the question is "are these two captures
// pixel-identical except for cursor movement and compression
// noise?" 64-bit DCT shadows let visually-distinct slides with
// shared structural patterns (dark background, centered yellow
// title, bullet list at the bottom) collide at small Hamming
// distances. RMS on the actual pixel buffers is much more
// discriminating: cursor jitter contributes fractions of a unit
// to the mean, while a different slide contributes tens.
//
// Memory:
//
// Each accepted slide's downsampled buffer is held in RAM so future
// slides can be compared against it. Frames are clamped to
// `targetMaxPixels` (1 MP) keeping aspect ratio and full RGBA, so a
// 1280×720 capture is stored verbatim (~3.6 MB) and a 4K capture
// downsamples to ~1 MP first. A typical meeting has well under 100
// slides, so the upper bound is roughly 360 MB — comfortable on a
// modern Mac.
type revisitTracker struct {
	mu        sync.Mutex
	seen      []revisitFrame
	threshold float64 // mean per-channel RMS difference; <= threshold ⇒ revisit
}

// revisitFrame is one accepted slide's downsampled pixel data plus
// its dimensions. Stored as packed RGBA so the RMS loop is a single
// linear scan with no branching for pixel layout.
type revisitFrame struct {
	w, h   int
	pixels []byte
}

// targetMaxPixels caps the in-memory pixel count per stored frame.
// 1,000,000 = 1 megapixel. A native 1280×720 capture comes in
// under-cap so we store it as-is; anything larger is downsampled
// (aspect ratio preserved, full colour). The cap exists purely to
// bound memory, not for any rendering reason — RMS doesn't need
// detail beyond "is this the same slide content?"
const targetMaxPixels = 1_000_000

func newRevisitTracker(threshold float64) *revisitTracker {
	if threshold < 0 {
		threshold = 0
	}
	return &revisitTracker{
		seen:      make([]revisitFrame, 0, 64),
		threshold: threshold,
	}
}

// classify decodes the slide PNG at `path`, downsamples to ≤1 MP,
// and returns:
//   - revisit=true, firstIdx=N, dist=D — RMS diff D against prior slide N is at or below threshold
//   - revisit=false, firstIdx=N, dist=D — closest prior slide had RMS diff D (NaN when no priors); the new frame is appended at index N
//
// Any decode/IO error returns (false, len(seen), NaN, err) without
// modifying tracker state — caller logs the failure and treats the
// slide as fresh.
func (r *revisitTracker) classify(path string) (revisit bool, firstIndex int, dist float64, err error) {
	frame, err := loadDownsampledFrame(path, targetMaxPixels)
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return false, len(r.seen), math.NaN(), err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	bestIdx := -1
	bestDist := math.NaN()
	for i, prev := range r.seen {
		if prev.w != frame.w || prev.h != frame.h {
			// Different region size (capture region changed
			// mid-meeting). Can't compare; treat as no-match for
			// this prior frame.
			continue
		}
		d := rmsDiff(prev.pixels, frame.pixels)
		if math.IsNaN(bestDist) || d < bestDist {
			bestDist = d
			bestIdx = i
		}
	}

	if bestIdx >= 0 && bestDist <= r.threshold {
		return true, bestIdx, bestDist, nil
	}

	idx := len(r.seen)
	r.seen = append(r.seen, frame)
	return false, idx, bestDist, nil
}

// rmsDiff returns the mean per-channel RMS difference between two
// equal-length packed-pixel buffers, on the 0–255 scale. Cursor
// drift contributes fractions of a unit; a fully different slide
// contributes tens of units.
func rmsDiff(a, b []byte) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return math.MaxFloat64
	}
	var sumSq float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(a)))
}

// loadDownsampledFrame decodes the PNG at `path` and returns its
// pixels as packed RGBA, downsampled (aspect-preserving) to at most
// `maxPixels`. The downsampler is a simple box-average over an
// integer stride; for "is this the same slide?" RMS comparisons,
// resampling fidelity is unimportant.
func loadDownsampledFrame(path string, maxPixels int) (revisitFrame, error) {
	f, err := os.Open(path)
	if err != nil {
		return revisitFrame{}, fmt.Errorf("open slide png: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return revisitFrame{}, fmt.Errorf("decode slide png: %w", err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return revisitFrame{}, fmt.Errorf("zero-size slide png")
	}
	stride := 1
	for (w/stride)*(h/stride) > maxPixels {
		stride++
	}
	dw, dh := w/stride, h/stride
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	pixels := make([]byte, dw*dh*4)
	for dy := 0; dy < dh; dy++ {
		sy := bounds.Min.Y + dy*stride
		for dx := 0; dx < dw; dx++ {
			sx := bounds.Min.X + dx*stride
			r, g, b, a := img.At(sx, sy).RGBA()
			off := (dy*dw + dx) * 4
			pixels[off+0] = byte(r >> 8)
			pixels[off+1] = byte(g >> 8)
			pixels[off+2] = byte(b >> 8)
			pixels[off+3] = byte(a >> 8)
		}
	}
	return revisitFrame{w: dw, h: dh, pixels: pixels}, nil
}
