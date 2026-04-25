// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// manifestFile is the basename of the per-meeting metadata JSON
// written into {workDir}/{meetingID}/. Reading it gives `meetcat
// resume <meeting-id>` enough information to re-spawn pageflip with
// the same target args and reconstruct the SessionPool with the
// same meetingID — claudia auto-resumes the per-specialist Claude
// Code sessions via their persisted JSONLs.
const manifestFile = "meeting.json"

// Manifest is the on-disk record of one meeting's invocation
// parameters. It is written early in the meeting (before the first
// slide arrives) so that an accidental Ctrl-C still leaves enough
// state on disk for `meetcat resume` to pick up where the operator
// left off. Fields here are deliberately limited to what the resume
// flow needs: target args for pageflip, work_dir, glossary cache,
// and the specialist allow-list. Session IDs themselves live in
// session-ids.json (one file, one purpose) and are looked up by
// (meeting_id, role) via specialistSessionID at resume time.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	MeetingID     string    `json:"meeting_id"`
	CreatedAt     time.Time `json:"created_at"`
	WorkDir       string    `json:"work_dir"`
	GlossaryCache string    `json:"glossary_cache,omitempty"`
	Specialists   []string  `json:"specialists,omitempty"` // empty = all (default)
	Pageflip      PageflipArgs `json:"pageflip,omitempty"`
}

// PageflipArgs is the subset of pageflip CLI args that meetcat
// forwards on its behalf — everything the operator can specify via
// `meetcat --region` etc. Empty fields mean "let pageflip pick"
// (typically the multi-monitor picker).
type PageflipArgs struct {
	Region      string `json:"region,omitempty"`
	Window      bool   `json:"window,omitempty"`
	WindowTitle string `json:"window_title,omitempty"`
	WindowID    string `json:"window_id,omitempty"`
}

// toFlags renders the args back into the `--region X,Y,W,H`-style
// strings that spawnPageflip forwards to the pageflip subprocess.
// Unset fields produce no flag, so the resulting slice mirrors what
// the operator originally typed (or empty, meaning "run the
// picker").
func (p PageflipArgs) toFlags() []string {
	var out []string
	if p.Region != "" {
		out = append(out, "--region", p.Region)
	}
	if p.Window {
		out = append(out, "--window")
	}
	if p.WindowTitle != "" {
		out = append(out, "--window-title", p.WindowTitle)
	}
	if p.WindowID != "" {
		out = append(out, "--window-id", p.WindowID)
	}
	return out
}

const currentManifestSchema = 1

// WriteManifest persists the manifest to {workDir}/{meeting_id}/meeting.json.
// Creates the artifact directory if missing — same MkdirAll pattern as
// session-ids.json so an early Ctrl-C still leaves a usable file.
func WriteManifest(workDir string, m Manifest) error {
	dir := filepath.Join(workDir, m.MeetingID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir manifest dir: %w", err)
	}
	path := filepath.Join(dir, manifestFile)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// ReadManifest loads the manifest for a given meeting. The meetingID
// can be either the bare ID (`meetcat-1234`) or a path to the
// directory itself; ReadManifest resolves both forms relative to the
// caller's cwd. Returns a structured error wrapping fs.ErrNotExist
// when the manifest is absent so callers can distinguish "no such
// meeting" from "manifest is malformed".
func ReadManifest(meetingDir string) (Manifest, error) {
	path := filepath.Join(meetingDir, manifestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if m.SchemaVersion != currentManifestSchema {
		return Manifest{}, fmt.Errorf(
			"%s: schema version %d not supported (expected %d)",
			path, m.SchemaVersion, currentManifestSchema,
		)
	}
	return m, nil
}
