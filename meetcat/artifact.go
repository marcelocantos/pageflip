// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — artifact writer for meetcat.
//
// This file implements 🎯T12: at meeting-stop the neutral specialist
// agent is asked to emit structured meeting outputs, which are then
// written to a folder alongside slides and transcript.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcelocantos/claudia"
)

// artifactPrompt is sent to the neutral agent at meeting-stop. It
// instructs the agent to produce a structured response partitioned
// into named markdown sections, each prefixed with a `## ` heading.
const artifactPrompt = `The meeting has ended. Based on our entire conversation about the slides and transcript, please produce the following sections, each starting with a markdown heading:

## Decisions
(list key decisions made during the meeting)

## Action items
(list with assignees if inferrable)

## Open questions
(list unresolved items)

## Contradictions
(list any contradictions you noticed)

Please be concise. Use bullet points.`

// ArtifactConfig carries the parameters needed to locate inputs and
// choose the output destination.
type ArtifactConfig struct {
	// OutputDir is the base destination folder. The artifact is written
	// to {OutputDir}/{MeetingID}/.
	OutputDir string

	// MeetingID is used as the leaf folder name and as the key for
	// session-id pointers.
	MeetingID string

	// SlidesDir is pageflip's output directory containing slide PNGs.
	// Its contents are symlinked into {artifact}/slides/.
	SlidesDir string

	// TranscriptPath is the path to transcript.jsonl produced by
	// pageflip (T9.2/T9.3). It is copied into {artifact}/transcript.jsonl.
	TranscriptPath string
}

// ArtifactWriter produces a structured meeting artifact folder by
// interrogating the neutral specialist agent and assembling auxiliary
// files from the meeting's working directory.
type ArtifactWriter struct {
	config ArtifactConfig
}

// NewArtifactWriter returns an ArtifactWriter configured by config.
func NewArtifactWriter(config ArtifactConfig) *ArtifactWriter {
	return &ArtifactWriter{config: config}
}

// WriteArtifact asks neutralAgent to emit structured meeting outputs
// and writes them plus auxiliary files to the artifact folder.
//
// Folder layout produced:
//
//	{OutputDir}/{MeetingID}/
//	  decisions.md
//	  actions.md
//	  open-questions.md
//	  contradictions.md
//	  references.json       (empty stub; agent references are plain text)
//	  session-ids.json      (specialist session-id map)
//	  transcript.jsonl      (copied from TranscriptPath if non-empty)
//	  slides/               (symlinks to PNGs from SlidesDir if non-empty)
func (w *ArtifactWriter) WriteArtifact(ctx context.Context, neutralAgent *claudia.Agent) error {
	artifactDir := filepath.Join(w.config.OutputDir, w.config.MeetingID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}

	// Step 1: symlink slides.
	if w.config.SlidesDir != "" {
		if err := w.linkSlides(artifactDir); err != nil {
			return fmt.Errorf("link slides: %w", err)
		}
	}

	// Step 2: copy transcript.
	if w.config.TranscriptPath != "" {
		dst := filepath.Join(artifactDir, "transcript.jsonl")
		if err := copyFile(w.config.TranscriptPath, dst); err != nil {
			return fmt.Errorf("copy transcript: %w", err)
		}
	}

	// Step 3: ask neutral agent for structured outputs.
	if err := neutralAgent.Send(artifactPrompt); err != nil {
		return fmt.Errorf("send artifact prompt: %w", err)
	}
	response, err := neutralAgent.WaitForResponse(ctx)
	if err != nil {
		return fmt.Errorf("wait for artifact response: %w", err)
	}

	// Step 4: parse and write markdown sections.
	sections := parseMarkdownSections(response)
	sectionFiles := map[string]string{
		"Decisions":      "decisions.md",
		"Action items":   "actions.md",
		"Open questions": "open-questions.md",
		"Contradictions": "contradictions.md",
	}
	for heading, filename := range sectionFiles {
		content := sections[heading]
		// Always write the file, even if the agent produced no content
		// for this section, so downstream consumers can rely on its presence.
		path := filepath.Join(artifactDir, filename)
		if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", filename, err)
		}
	}

	// Step 5: write references.json as an empty stub. The agent's
	// reference text lives in decisions.md / actions.md; a structured
	// resolver pass (T11) will populate this properly.
	refsPath := filepath.Join(artifactDir, "references.json")
	if err := os.WriteFile(refsPath, []byte("[]\n"), 0o644); err != nil {
		return fmt.Errorf("write references.json: %w", err)
	}

	// Step 6: write session-ids.json.
	// The session ID of the neutral agent is captured here; specialist
	// IDs are added by the caller (SessionPool) after stop. We write
	// at least the neutral agent's ID so the artifact is self-contained.
	ids := map[string]string{
		"neutral": neutralAgent.SessionID(),
	}
	return w.writeSessionIDs(artifactDir, ids)
}

// WriteSessionIDs (re-)writes session-ids.json, merging ids into any
// existing content. It is safe to call after WriteArtifact with the
// full specialist ID map returned by SessionPool.TurnSummary.
func (w *ArtifactWriter) WriteSessionIDs(ids map[string]string) error {
	artifactDir := filepath.Join(w.config.OutputDir, w.config.MeetingID)
	return w.writeSessionIDs(artifactDir, ids)
}

func (w *ArtifactWriter) writeSessionIDs(artifactDir string, ids map[string]string) error {
	path := filepath.Join(artifactDir, "session-ids.json")

	// Merge with existing content if present.
	merged := make(map[string]string)
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &merged)
	}
	for k, v := range ids {
		merged[k] = v
	}

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session-ids: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write session-ids.json: %w", err)
	}
	return nil
}

// linkSlides creates {artifactDir}/slides/ and, for each PNG in
// SlidesDir, creates a symlink pointing at the source file.
func (w *ArtifactWriter) linkSlides(artifactDir string) error {
	slidesOut := filepath.Join(artifactDir, "slides")
	if err := os.MkdirAll(slidesOut, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(w.config.SlidesDir)
	if err != nil {
		return fmt.Errorf("read slides dir %q: %w", w.config.SlidesDir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".png") {
			continue
		}
		// Use an absolute path so the symlink is valid regardless of
		// the process working directory.
		src, err := filepath.Abs(filepath.Join(w.config.SlidesDir, name))
		if err != nil {
			return fmt.Errorf("abs path for slide %q: %w", name, err)
		}
		dst := filepath.Join(slidesOut, name)
		// Remove stale symlink before re-creating to be idempotent.
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err != nil {
			return fmt.Errorf("symlink slide %q: %w", name, err)
		}
	}
	return nil
}

// parseMarkdownSections splits a markdown response into sections keyed
// by the text that follows the `## ` prefix. The body of each section
// is everything between that heading and the next `## ` heading (or
// the end of the string). Leading/trailing whitespace of each body is
// preserved as-is so callers can trim as needed.
func parseMarkdownSections(response string) map[string]string {
	sections := make(map[string]string)
	lines := strings.Split(response, "\n")

	currentHeading := ""
	var bodyLines []string

	flush := func() {
		if currentHeading != "" {
			sections[currentHeading] = strings.Join(bodyLines, "\n")
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			currentHeading = strings.TrimPrefix(line, "## ")
			bodyLines = nil
		} else {
			bodyLines = append(bodyLines, line)
		}
	}
	flush()

	return sections
}

// copyFile copies the file at src to dst, creating dst if needed.
// Existing dst content is overwritten.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
