// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestEventReceivedShape verifies that EventReceived serialises with the
// expected JSON keys and that no content fields are present.
func TestEventReceivedShape(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogReceived("slide-42", "com.microsoft.teams2")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, buf.String())
	}

	mustHaveString(t, m, "event", "received")
	mustHaveKey(t, m, "t_ms")
	mustHaveString(t, m, "slide_id", "slide-42")
	mustHaveString(t, m, "frontmost_app_bundle", "com.microsoft.teams2")
	mustNotHaveKey(t, m, "text")
	mustNotHaveKey(t, m, "content")
	mustNotHaveKey(t, m, "ocr")
	mustNotHaveKey(t, m, "transcript")
}

func TestEventReceivedOmitEmptyBundle(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogReceived("slide-1", "")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustNotHaveKey(t, m, "frontmost_app_bundle")
}

func TestEventValidatedShape(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogValidated("slide-99")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustHaveString(t, m, "event", "validated")
	mustHaveString(t, m, "slide_id", "slide-99")
}

func TestEventRejectedShape(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogRejected("validation_failed")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustHaveString(t, m, "event", "rejected")
	mustHaveString(t, m, "reason_code", "validation_failed")
	// reason_code must not be a free-form message
	if v, ok := m["reason_code"].(string); ok {
		if strings.Contains(v, " ") {
			t.Errorf("reason_code looks like free-form text: %q", v)
		}
	}
}

func TestSpecialistStartShape(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogSpecialistStart("dejargoniser", "claude-sonnet-4-5", "ab12")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustHaveString(t, m, "event", "specialist_start")
	mustHaveString(t, m, "name", "dejargoniser")
	mustHaveString(t, m, "model", "claude-sonnet-4-5")
	mustHaveString(t, m, "session_id_hash", "ab12")
	mustNotHaveKey(t, m, "session_id")
	mustNotHaveKey(t, m, "prompt")
	mustNotHaveKey(t, m, "output")
}

func TestSpecialistTurnShape(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogSpecialistTurn("skeptic", 3, 1234, 500, 300, 0.0042)

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustHaveString(t, m, "event", "specialist_turn")
	mustHaveString(t, m, "name", "skeptic")

	mustNotHaveKey(t, m, "text")
	mustNotHaveKey(t, m, "content")
	mustNotHaveKey(t, m, "output")

	// Numeric fields present
	for _, key := range []string{"turn_index", "duration_ms", "input_tokens", "output_tokens", "cost_usd"} {
		mustHaveKey(t, m, key)
	}
}

func TestSpecialistErrorShape(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogSpecialistError("resolver", "timeout")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustHaveString(t, m, "event", "specialist_error")
	mustHaveString(t, m, "code", "timeout")
	mustNotHaveKey(t, m, "message")
}

func TestMeetingEndShape(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogMeetingEnd(12, 0.87)

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mustHaveString(t, m, "event", "meeting_end")
	mustHaveKey(t, m, "slides_processed")
	mustHaveKey(t, m, "total_cost_usd")
}

func TestNilLoggerIsNoop(t *testing.T) {
	var l *Logger
	// None of these should panic.
	l.LogReceived("s1", "bundle")
	l.LogValidated("s1")
	l.LogRejected("code")
	l.LogSpecialistStart("n", "m", "ab12")
	l.LogSpecialistTurn("n", 0, 0, 0, 0, 0)
	l.LogSpecialistError("n", "e")
	l.LogMeetingEnd(0, 0)
}

func TestLoggerProducesNDJSON(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{w: &buf}
	l.LogReceived("s1", "")
	l.LogValidated("s1")
	l.LogMeetingEnd(1, 0)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 NDJSON lines, got %d: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line %d not valid JSON: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustHaveKey(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if _, ok := m[key]; !ok {
		t.Errorf("missing key %q in %v", key, m)
	}
}

func mustNotHaveKey(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if _, ok := m[key]; ok {
		t.Errorf("unexpected key %q in %v", key, m)
	}
}

func mustHaveString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("missing key %q", key)
		return
	}
	s, ok := v.(string)
	if !ok {
		t.Errorf("key %q: expected string, got %T", key, v)
		return
	}
	if s != want {
		t.Errorf("key %q: got %q, want %q", key, s, want)
	}
}
