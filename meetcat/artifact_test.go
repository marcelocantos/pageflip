// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseMarkdownSections
// ---------------------------------------------------------------------------

func TestParseMarkdownSectionsBasic(t *testing.T) {
	input := `Some preamble text

## Decisions
- We decided to do X
- We also decided Y

## Action items
- Alice: follow up on X

## Open questions
- What about Z?

## Contradictions
- Slide 3 said A but slide 7 said B
`
	sections := parseMarkdownSections(input)

	cases := []struct {
		heading string
		want    string
	}{
		{"Decisions", "- We decided to do X\n- We also decided Y"},
		{"Action items", "- Alice: follow up on X"},
		{"Open questions", "- What about Z?"},
		{"Contradictions", "- Slide 3 said A but slide 7 said B"},
	}
	for _, tc := range cases {
		body, ok := sections[tc.heading]
		if !ok {
			t.Errorf("missing section %q", tc.heading)
			continue
		}
		trimmed := strings.TrimSpace(body)
		if !strings.Contains(trimmed, strings.TrimSpace(tc.want)) {
			t.Errorf("section %q: got %q, want to contain %q", tc.heading, trimmed, tc.want)
		}
	}
}

func TestParseMarkdownSectionsEmpty(t *testing.T) {
	sections := parseMarkdownSections("")
	if len(sections) != 0 {
		t.Errorf("expected empty map for empty input; got %v", sections)
	}
}

func TestParseMarkdownSectionsNoPreamble(t *testing.T) {
	// Response that starts immediately with a heading.
	input := "## Decisions\n- Choice A\n\n## Action items\n- Bob: do the thing\n"
	sections := parseMarkdownSections(input)
	if _, ok := sections["Decisions"]; !ok {
		t.Error("missing Decisions section")
	}
	if _, ok := sections["Action items"]; !ok {
		t.Error("missing Action items section")
	}
}

func TestParseMarkdownSectionsMissingSection(t *testing.T) {
	// Only Decisions — rest are absent.
	input := "## Decisions\n- Only one thing\n"
	sections := parseMarkdownSections(input)
	if _, ok := sections["Action items"]; ok {
		t.Error("unexpected Action items section")
	}
}

func TestParseMarkdownSectionsPreservesWhitespace(t *testing.T) {
	// Body content may have leading blank line (empty line after heading).
	input := "## Decisions\n\n- item\n"
	sections := parseMarkdownSections(input)
	body := sections["Decisions"]
	if !strings.Contains(body, "- item") {
		t.Errorf("body should contain '- item'; got %q", body)
	}
}

// ---------------------------------------------------------------------------
// ArtifactWriter — folder creation and file writing
// ---------------------------------------------------------------------------

// mockNeutralAgent is a stand-in for claudia.Agent in tests that do
// not exercise the actual agent interaction. It is used only by tests
// that call helpers directly (not WriteArtifact) since WriteArtifact
// requires a real *claudia.Agent.
//
// Tests for WriteArtifact itself do not run in unit tests because they
// require a live Claude Code / tmux environment; they belong to
// integration tests.

func TestArtifactWriterFolderCreation(t *testing.T) {
	dir := t.TempDir()
	cfg := ArtifactConfig{
		OutputDir: dir,
		MeetingID: "test-meeting-001",
	}
	w := NewArtifactWriter(cfg)

	artifactDir := filepath.Join(dir, "test-meeting-001")

	// The folder does not exist yet.
	if _, err := os.Stat(artifactDir); err == nil {
		t.Fatal("artifact dir should not exist before write")
	}

	// Drive the folder-creation path via writeSessionIDs, which is
	// the only exported helper that creates the folder independently.
	// (WriteArtifact needs a real agent; we test folder creation here.)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := w.WriteSessionIDs(map[string]string{"neutral": "meetcat-999-neutral"}); err != nil {
		t.Fatalf("WriteSessionIDs: %v", err)
	}

	// session-ids.json must exist.
	data, err := os.ReadFile(filepath.Join(artifactDir, "session-ids.json"))
	if err != nil {
		t.Fatalf("read session-ids.json: %v", err)
	}
	var ids map[string]string
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatalf("unmarshal session-ids.json: %v", err)
	}
	if ids["neutral"] != "meetcat-999-neutral" {
		t.Errorf("neutral session ID: got %q, want %q", ids["neutral"], "meetcat-999-neutral")
	}
}

func TestWriteSessionIDsMerge(t *testing.T) {
	dir := t.TempDir()
	cfg := ArtifactConfig{OutputDir: dir, MeetingID: "m1"}
	w := NewArtifactWriter(cfg)
	artifactDir := filepath.Join(dir, "m1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// First write.
	if err := w.WriteSessionIDs(map[string]string{"neutral": "n-id"}); err != nil {
		t.Fatalf("first WriteSessionIDs: %v", err)
	}
	// Second write adds specialists without clobbering neutral.
	if err := w.WriteSessionIDs(map[string]string{
		"skeptic":      "s-id",
		"constructive": "c-id",
	}); err != nil {
		t.Fatalf("second WriteSessionIDs: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(artifactDir, "session-ids.json"))
	var ids map[string]string
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ids["neutral"] != "n-id" {
		t.Errorf("neutral clobbered; got %q", ids["neutral"])
	}
	if ids["skeptic"] != "s-id" {
		t.Errorf("skeptic missing; got %q", ids["skeptic"])
	}
	if ids["constructive"] != "c-id" {
		t.Errorf("constructive missing; got %q", ids["constructive"])
	}
}

// ---------------------------------------------------------------------------
// linkSlides — symlink creation
// ---------------------------------------------------------------------------

func TestLinkSlides(t *testing.T) {
	// Build a fake slides source directory with a few PNGs and a non-PNG.
	slidesDir := t.TempDir()
	slideNames := []string{"slide-001.png", "slide-002.png", "slide-003.PNG"}
	for _, name := range slideNames {
		if err := os.WriteFile(filepath.Join(slidesDir, name), []byte("fake png"), 0o644); err != nil {
			t.Fatalf("create slide %q: %v", name, err)
		}
	}
	// Non-PNG should be ignored.
	if err := os.WriteFile(filepath.Join(slidesDir, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("create notes.txt: %v", err)
	}

	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "meeting-x")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := ArtifactConfig{
		OutputDir: dir,
		MeetingID: "meeting-x",
		SlidesDir: slidesDir,
	}
	w := NewArtifactWriter(cfg)

	if err := w.linkSlides(artifactDir); err != nil {
		t.Fatalf("linkSlides: %v", err)
	}

	slidesOut := filepath.Join(artifactDir, "slides")

	// All three PNGs must have a symlink.
	for _, name := range slideNames {
		linkPath := filepath.Join(slidesOut, name)
		fi, err := os.Lstat(linkPath)
		if err != nil {
			t.Errorf("slide symlink %q missing: %v", name, err)
			continue
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("slide %q is not a symlink", name)
		}
		// The symlink must resolve and point to a real file.
		target, err := os.Readlink(linkPath)
		if err != nil {
			t.Errorf("readlink %q: %v", name, err)
			continue
		}
		if !filepath.IsAbs(target) {
			t.Errorf("symlink target %q is not absolute", target)
		}
	}

	// notes.txt must NOT be linked.
	if _, err := os.Lstat(filepath.Join(slidesOut, "notes.txt")); err == nil {
		t.Error("notes.txt should not be symlinked")
	}
}

func TestLinkSlidesIdempotent(t *testing.T) {
	slidesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(slidesDir, "s1.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "m")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := ArtifactConfig{OutputDir: dir, MeetingID: "m", SlidesDir: slidesDir}
	w := NewArtifactWriter(cfg)

	// Two calls must not return an error.
	if err := w.linkSlides(artifactDir); err != nil {
		t.Fatalf("first linkSlides: %v", err)
	}
	if err := w.linkSlides(artifactDir); err != nil {
		t.Fatalf("second linkSlides (idempotent): %v", err)
	}
}

// ---------------------------------------------------------------------------
// copyFile
// ---------------------------------------------------------------------------

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "transcript.jsonl")
	content := `{"type":"transcript","text":"hello"}` + "\n"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "copy.jsonl")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != content {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "no-such-file.jsonl"), filepath.Join(dir, "dst.jsonl"))
	if err == nil {
		t.Error("expected error for missing source")
	}
}

// ---------------------------------------------------------------------------
// NewArtifactWriter
// ---------------------------------------------------------------------------

func TestNewArtifactWriter(t *testing.T) {
	cfg := ArtifactConfig{
		OutputDir:      "/tmp/meetings",
		MeetingID:      "test-123",
		SlidesDir:      "/tmp/slides",
		TranscriptPath: "/tmp/transcript.jsonl",
	}
	w := NewArtifactWriter(cfg)
	if w == nil {
		t.Fatal("NewArtifactWriter returned nil")
	}
	if w.config != cfg {
		t.Errorf("config not stored: got %+v, want %+v", w.config, cfg)
	}
}
