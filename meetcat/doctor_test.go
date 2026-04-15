// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorOutputSections(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf, "")
	out := buf.String()

	sections := []string{
		"## Versions",
		"## External tools",
		"## Anthropic auth state",
		"## Active subprocesses",
		"## Recent session log",
	}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("doctor output missing section %q", s)
		}
	}
}

func TestDoctorVersionsSection(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf, "")
	out := buf.String()

	if !strings.Contains(out, "meetcat") {
		t.Error("Versions section missing 'meetcat'")
	}
	if !strings.Contains(out, "Go runtime") {
		t.Error("Versions section missing 'Go runtime'")
	}
}

func TestDoctorNoLogFileConfigured(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf, "")
	out := buf.String()

	if !strings.Contains(out, "--log-file") {
		t.Error("Recent session log section should mention --log-file when not configured")
	}
}

func TestDoctorWithLogFile(t *testing.T) {
	// Write a small log file and verify doctor tails it.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.ndjson")
	content := `{"event":"received","t_ms":1000,"slide_id":"s1"}` + "\n" +
		`{"event":"validated","t_ms":1001,"slide_id":"s1"}` + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	var buf bytes.Buffer
	runDoctor(&buf, logPath)
	out := buf.String()

	if !strings.Contains(out, "received") {
		t.Errorf("doctor output should contain log content 'received'; got:\n%s", out)
	}
	if !strings.Contains(out, "validated") {
		t.Errorf("doctor output should contain log content 'validated'; got:\n%s", out)
	}
}

func TestDoctorWithMissingLogFile(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf, "/nonexistent/path/session.ndjson")
	out := buf.String()

	// Should not panic; should mention inability to open the file.
	if !strings.Contains(out, "Cannot open") && !strings.Contains(out, "nonexistent") {
		t.Errorf("doctor should report missing log file; got:\n%s", out)
	}
}

func TestDoctorActiveSubprocessesPlaceholder(t *testing.T) {
	var buf bytes.Buffer
	runDoctor(&buf, "")
	out := buf.String()

	// T19.2 not built yet; doctor should say so.
	if !strings.Contains(out, "T19.2") {
		t.Errorf("Active subprocesses section should mention T19.2; got:\n%s", out)
	}
}
