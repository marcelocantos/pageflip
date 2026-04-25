// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writePNG materialises an image to a temp file and returns the path.
// Used to feed the revisitTracker, which decodes from disk.
func writePNG(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return path
}

// solidImage returns a w×h image filled with `c`.
func solidImage(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// halfImage returns a w×h image with the left half coloured `left`
// and the right half `right`. Different layouts but same dimensions
// stress the RMS path: an entirely-different image's mean diff is
// large, well above the threshold.
func halfImage(w, h int, left, right color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				img.Set(x, y, left)
			} else {
				img.Set(x, y, right)
			}
		}
	}
	return img
}

func TestRevisitTrackerExactMatch(t *testing.T) {
	dir := t.TempDir()
	a := writePNG(t, dir, "a.png", solidImage(64, 64, color.RGBA{20, 30, 40, 255}))
	b := writePNG(t, dir, "b.png", solidImage(64, 64, color.RGBA{20, 30, 40, 255}))

	r := newRevisitTracker(5)
	revisit, idx, dist, err := r.classify(a)
	if err != nil {
		t.Fatalf("classify a: %v", err)
	}
	if revisit {
		t.Fatal("first sighting should not be a revisit")
	}
	if idx != 0 {
		t.Fatalf("want idx=0, got %d", idx)
	}
	if !math.IsNaN(dist) {
		t.Fatalf("first sighting should have NaN dist, got %v", dist)
	}

	revisit, idx, dist, err = r.classify(b)
	if err != nil {
		t.Fatalf("classify b: %v", err)
	}
	if !revisit {
		t.Fatalf("identical second sighting must be a revisit; got dist=%v", dist)
	}
	if idx != 0 {
		t.Fatalf("want revisit pointing at idx=0, got %d", idx)
	}
	if dist > 0.001 {
		t.Errorf("identical images should have RMS ≈ 0, got %v", dist)
	}
}

func TestRevisitTrackerDistinctSlides(t *testing.T) {
	dir := t.TempDir()
	white := writePNG(t, dir, "w.png", solidImage(64, 64, color.RGBA{255, 255, 255, 255}))
	black := writePNG(t, dir, "b.png", solidImage(64, 64, color.RGBA{0, 0, 0, 255}))

	r := newRevisitTracker(5)
	if revisit, _, _, _ := r.classify(white); revisit {
		t.Fatal("first sighting must not be revisit")
	}
	revisit, _, dist, err := r.classify(black)
	if err != nil {
		t.Fatalf("classify black: %v", err)
	}
	if revisit {
		t.Fatalf("white vs black are visibly different — must not be a revisit (dist=%v)", dist)
	}
	if dist < 50 {
		t.Errorf("expected large RMS for white-vs-black, got %v", dist)
	}
}

func TestRevisitTrackerLayoutSimilarDistinctContent(t *testing.T) {
	// The slides the user reported false-positives on were "structurally
	// similar but content-different" — same dark background, same yellow
	// title placement, different bullets / diagrams. Approximate that
	// here with two halves-images that differ in one half: same overall
	// brightness pattern, different actual content.
	dir := t.TempDir()
	a := writePNG(t, dir, "a.png", halfImage(64, 64,
		color.RGBA{20, 30, 40, 255},
		color.RGBA{200, 200, 50, 255},
	))
	b := writePNG(t, dir, "b.png", halfImage(64, 64,
		color.RGBA{20, 30, 40, 255},
		color.RGBA{50, 200, 200, 255},
	))

	r := newRevisitTracker(5)
	if revisit, _, _, _ := r.classify(a); revisit {
		t.Fatal("first sighting must not be revisit")
	}
	revisit, _, dist, _ := r.classify(b)
	if revisit {
		t.Fatalf("structurally-similar but content-different slides must not collide; got dist=%v", dist)
	}
}

func TestRevisitTrackerCursorJitterStaysSameSlide(t *testing.T) {
	// Two captures of the same slide differ only in a few-pixel cursor
	// move plus PNG compression noise. The mean RMS should be tiny —
	// well below the threshold.
	dir := t.TempDir()
	base := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			base.Set(x, y, color.RGBA{20, 30, 40, 255})
		}
	}
	withCursorAt := func(cx, cy int) image.Image {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				img.Set(x, y, color.RGBA{20, 30, 40, 255})
			}
		}
		// 4-pixel "cursor" block.
		for dy := 0; dy < 4; dy++ {
			for dx := 0; dx < 4; dx++ {
				img.Set(cx+dx, cy+dy, color.RGBA{255, 255, 255, 255})
			}
		}
		return img
	}
	a := writePNG(t, dir, "a.png", withCursorAt(10, 10))
	b := writePNG(t, dir, "b.png", withCursorAt(12, 12))

	r := newRevisitTracker(5)
	if revisit, _, _, _ := r.classify(a); revisit {
		t.Fatal("first sighting must not be revisit")
	}
	revisit, _, dist, _ := r.classify(b)
	if !revisit {
		t.Fatalf("same slide with a 2-pixel cursor move must be a revisit; got dist=%v", dist)
	}
}

func TestRevisitTrackerMissingFile(t *testing.T) {
	r := newRevisitTracker(5)
	_, idx, _, err := r.classify("/nonexistent/file.png")
	if err == nil {
		t.Fatal("expected an error when the slide PNG can't be opened")
	}
	if idx != 0 {
		t.Fatalf("on error, idx should be the next-available index (0); got %d", idx)
	}
}
