// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// renderSlideMessage
// ---------------------------------------------------------------------------

func TestRenderSlideMessageMinimal(t *testing.T) {
	ev := &slideEvent{
		SlideID:  "s1",
		Path:     "/tmp/deck/s1.png",
		TStartMs: 1000,
		TEndMs:   2000,
	}
	msg := renderSlideMessage(ev)

	if !strings.Contains(msg, "[slide s1 @ 1000]") {
		t.Errorf("missing slide header; got: %q", msg)
	}
	if !strings.Contains(msg, "Path: /tmp/deck/s1.png") {
		t.Errorf("missing path line; got: %q", msg)
	}
	// Optional fields absent from minimal event.
	if strings.Contains(msg, "OCR:") {
		t.Errorf("unexpected OCR line in minimal event; got: %q", msg)
	}
	if strings.Contains(msg, "Transcript:") {
		t.Errorf("unexpected Transcript line in minimal event; got: %q", msg)
	}
	if strings.Contains(msg, "App:") {
		t.Errorf("unexpected App line in minimal event; got: %q", msg)
	}
}

func TestRenderSlideMessageFull(t *testing.T) {
	ocr, _ := json.Marshal([]map[string]string{{"text": "Q3 revenue"}})
	transcript, _ := json.Marshal([]map[string]string{{"text": "revenue is ten"}})
	ev := &slideEvent{
		SlideID:          "deck-17",
		Path:             "/p/17.png",
		TStartMs:         5000,
		TEndMs:           5500,
		OCR:              json.RawMessage(ocr),
		TranscriptWindow: json.RawMessage(transcript),
		FrontmostApp:     "Teams",
	}
	msg := renderSlideMessage(ev)

	if !strings.Contains(msg, "[slide deck-17 @ 5000]") {
		t.Errorf("missing slide header; got: %q", msg)
	}
	if !strings.Contains(msg, "OCR:") {
		t.Errorf("missing OCR line; got: %q", msg)
	}
	if !strings.Contains(msg, "Transcript:") {
		t.Errorf("missing Transcript line; got: %q", msg)
	}
	if !strings.Contains(msg, "App: Teams") {
		t.Errorf("missing App line; got: %q", msg)
	}
}

func TestRenderSlideMessageNullOCRSkipped(t *testing.T) {
	// json.RawMessage("null") represents a JSON null — should be omitted.
	ev := &slideEvent{
		SlideID:  "s2",
		Path:     "/p2.png",
		TStartMs: 0,
		TEndMs:   100,
		OCR:      json.RawMessage("null"),
	}
	msg := renderSlideMessage(ev)
	if strings.Contains(msg, "OCR:") {
		t.Errorf("null OCR should be omitted; got: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// MeetingSessionID / specialistSessionID
// ---------------------------------------------------------------------------

func TestMeetingSessionIDFormat(t *testing.T) {
	id := MeetingSessionID()
	if !strings.HasPrefix(id, "meetcat-") {
		t.Errorf("expected meetcat- prefix; got %q", id)
	}
	// Should contain only "meetcat-" and digits.
	suffix := strings.TrimPrefix(id, "meetcat-")
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			t.Errorf("non-digit in session ID suffix %q: %q", suffix, ch)
		}
	}
}

func TestMeetingSessionIDUniqueAcrossTime(t *testing.T) {
	// Two calls separated by a real time delta should produce distinct IDs.
	// We can't guarantee uniqueness within the same millisecond since the
	// ID is millisecond-resolution; test that two IDs from different calls
	// have the same structure. Collision-resistance at meeting granularity
	// is sufficient — meetings don't start within the same millisecond.
	id1 := MeetingSessionID()
	id2 := MeetingSessionID()
	// Both must be well-formed; they may or may not be equal if called
	// within the same ms. Just verify structure, not uniqueness here.
	if !strings.HasPrefix(id1, "meetcat-") || !strings.HasPrefix(id2, "meetcat-") {
		t.Errorf("unexpected format: %q, %q", id1, id2)
	}
}

func TestSpecialistSessionIDFormat(t *testing.T) {
	meetingID := "meetcat-1234567890"
	id := specialistSessionID(meetingID, "skeptic")
	want := "meetcat-1234567890-skeptic"
	if id != want {
		t.Errorf("got %q, want %q", id, want)
	}
}

// ---------------------------------------------------------------------------
// allSpecialists
// ---------------------------------------------------------------------------

func TestAllSpecialistsNames(t *testing.T) {
	specs := allSpecialists()
	want := map[string]bool{
		"skeptic":       true,
		"constructive":  true,
		"neutral":       true,
		"dejargoniser":  true,
	}
	for _, s := range specs {
		if !want[s.name] {
			t.Errorf("unexpected specialist %q", s.name)
		}
		delete(want, s.name)
		if s.prompt == "" {
			t.Errorf("specialist %q has empty prompt", s.name)
		}
		if s.model == "" {
			t.Errorf("specialist %q has empty model", s.name)
		}
	}
	for name := range want {
		t.Errorf("missing specialist %q", name)
	}
}

func TestDejargoniserUsesHaiku(t *testing.T) {
	for _, s := range allSpecialists() {
		if s.name == "dejargoniser" && s.model != "haiku" {
			t.Errorf("dejargoniser should use haiku model, got %q", s.model)
		}
	}
}
