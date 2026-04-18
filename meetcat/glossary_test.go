// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// GlossaryCache CRUD + persistence
// ---------------------------------------------------------------------------

func TestGlossaryCacheNewEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, err := NewGlossaryCache(path)
	if err != nil {
		t.Fatalf("NewGlossaryCache: %v", err)
	}
	if g.Len() != 0 {
		t.Errorf("expected empty cache, got %d entries", g.Len())
	}
}

func TestGlossaryCacheAdd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)

	entry := GlossaryEntry{
		Acronym:    "API",
		Expansion:  "Application Programming Interface",
		Source:     "confluence",
		SourceURL:  "https://wiki.example.com/API",
		Confidence: 1.0,
	}
	if err := g.Add(entry); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if g.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", g.Len())
	}
}

func TestGlossaryCacheLookupCaseInsensitive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)
	_ = g.Add(GlossaryEntry{Acronym: "SLA", Expansion: "Service Level Agreement", Source: "confluence", Confidence: 1.0})

	for _, input := range []string{"SLA", "sla", "Sla"} {
		entry := g.Lookup(input)
		if entry == nil {
			t.Errorf("Lookup(%q) returned nil, want entry", input)
			continue
		}
		if entry.Expansion != "Service Level Agreement" {
			t.Errorf("Lookup(%q).Expansion = %q, want %q", input, entry.Expansion, "Service Level Agreement")
		}
	}
}

func TestGlossaryCacheLookupMiss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)
	if got := g.Lookup("UNKNOWN"); got != nil {
		t.Errorf("expected nil for unknown acronym, got %+v", got)
	}
}

func TestGlossaryCacheAddReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)
	_ = g.Add(GlossaryEntry{Acronym: "OKR", Expansion: "Old Expansion", Source: "research", Confidence: 0.7})
	_ = g.Add(GlossaryEntry{Acronym: "OKR", Expansion: "Objectives and Key Results", Source: "confluence", Confidence: 1.0})

	entry := g.Lookup("OKR")
	if entry == nil {
		t.Fatal("expected entry after add")
	}
	if entry.Expansion != "Objectives and Key Results" {
		t.Errorf("expected updated expansion, got %q", entry.Expansion)
	}
	if g.Len() != 1 {
		t.Errorf("expected 1 entry (replace, not append), got %d", g.Len())
	}
}

// ---------------------------------------------------------------------------
// Persistence round-trip
// ---------------------------------------------------------------------------

func TestGlossaryCachePersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "glossary.json")

	g1, _ := NewGlossaryCache(path)
	entries := []GlossaryEntry{
		{Acronym: "API", Expansion: "Application Programming Interface", Source: "confluence", SourceURL: "https://wiki.example.com/api", Confidence: 1.0},
		{Acronym: "SLO", Expansion: "Service Level Objective", Source: "research", Confidence: 0.7},
		{Acronym: "KPI", Expansion: "Key Performance Indicator", Source: "meeting", Confidence: 0.9},
	}
	for _, e := range entries {
		if err := g1.Add(e); err != nil {
			t.Fatalf("Add(%q): %v", e.Acronym, err)
		}
	}
	if g1.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", g1.Len())
	}

	// Reload from disk.
	g2, err := NewGlossaryCache(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if g2.Len() != 3 {
		t.Fatalf("after reload: expected 3 entries, got %d", g2.Len())
	}
	for _, e := range entries {
		got := g2.Lookup(e.Acronym)
		if got == nil {
			t.Errorf("after reload: Lookup(%q) = nil", e.Acronym)
			continue
		}
		if got.Expansion != e.Expansion {
			t.Errorf("after reload: Lookup(%q).Expansion = %q, want %q", e.Acronym, got.Expansion, e.Expansion)
		}
	}
}

func TestGlossaryCacheFileIsHumanReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)
	_ = g.Add(GlossaryEntry{Acronym: "CI", Expansion: "Continuous Integration", Source: "confluence", Confidence: 1.0})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Must be valid JSON.
	var list []GlossaryEntry
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("not valid JSON: %v\ncontents: %s", err, data)
	}
	// Must be indented (human-readable).
	if len(data) == 0 || data[0] != '[' {
		t.Errorf("expected JSON array, got: %s", data[:minInt(50, len(data))])
	}
	if !containsByte(data, '\n') {
		t.Errorf("expected indented (multi-line) JSON, got: %s", data)
	}
}

// ---------------------------------------------------------------------------
// Low-confidence entries are rejected
// ---------------------------------------------------------------------------

func TestGlossaryCacheRejectLowConfidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)

	// Below threshold — should be silently dropped.
	err := g.Add(GlossaryEntry{Acronym: "OCR", Expansion: "guess", Source: "research", Confidence: 0.1})
	if err != nil {
		t.Fatalf("Add low-confidence: unexpected error %v", err)
	}
	if g.Len() != 0 {
		t.Errorf("expected 0 entries after low-confidence add, got %d", g.Len())
	}
}

func TestGlossaryCacheExactlyAtThreshold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)

	// Exactly at minConfidence (0.3) — should be accepted.
	err := g.Add(GlossaryEntry{Acronym: "OCR", Expansion: "Optical Character Recognition", Source: "research", Confidence: minConfidence})
	if err != nil {
		t.Fatalf("Add at threshold: unexpected error %v", err)
	}
	if g.Len() != 1 {
		t.Errorf("expected 1 entry at threshold, got %d", g.Len())
	}
}

// ---------------------------------------------------------------------------
// All() returns snapshot
// ---------------------------------------------------------------------------

func TestGlossaryCacheAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)
	_ = g.Add(GlossaryEntry{Acronym: "A", Expansion: "Alpha", Source: "meeting", Confidence: 0.9})
	_ = g.Add(GlossaryEntry{Acronym: "B", Expansion: "Beta", Source: "meeting", Confidence: 0.9})

	all := g.All()
	if len(all) != 2 {
		t.Errorf("All() = %d entries, want 2", len(all))
	}
}

// ---------------------------------------------------------------------------
// ExtractAcronyms
// ---------------------------------------------------------------------------

func TestExtractAcronymsBasic(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"The SLA for API is defined in the KPI dashboard", []string{"SLA", "API", "KPI"}},
		{"No acronyms here at all", nil},
		{"THE AND FOR WITH are excluded", nil},
		{"FOOBAR is six chars so excluded", nil},
		{"AB is two chars — included", []string{"AB"}},
		{"ABC DE FGH", []string{"ABC", "DE", "FGH"}},
	}
	for _, tc := range cases {
		got := ExtractAcronyms(tc.text)
		if len(got) != len(tc.want) {
			t.Errorf("ExtractAcronyms(%q) = %v, want %v", tc.text, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("ExtractAcronyms(%q)[%d] = %q, want %q", tc.text, i, got[i], tc.want[i])
			}
		}
	}
}

func TestExtractAcronymsDeduplicated(t *testing.T) {
	got := ExtractAcronyms("SLA SLA SLA")
	if len(got) != 1 || got[0] != "SLA" {
		t.Errorf("expected deduped [SLA], got %v", got)
	}
}

func TestExtractAcronymsWordBoundary(t *testing.T) {
	// "APIFOO" is 6 chars — must not match. "API" inside a larger word boundary
	// also should not match unless it is a standalone word.
	got := ExtractAcronyms("APIFOO is long")
	for _, a := range got {
		if a == "APIFOO" {
			t.Errorf("APIFOO (6 chars) should not match")
		}
	}
}

func TestExtractAcronymsCommonWordsExcluded(t *testing.T) {
	for word := range commonWords {
		got := ExtractAcronyms(word)
		if len(got) > 0 {
			t.Errorf("common word %q should be excluded, got %v", word, got)
		}
	}
}

// ---------------------------------------------------------------------------
// GlossaryPreamble
// ---------------------------------------------------------------------------

func TestGlossaryPreambleKnownAcronym(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)
	_ = g.Add(GlossaryEntry{Acronym: "SLA", Expansion: "Service Level Agreement", Source: "confluence", SourceURL: "https://wiki.example.com/sla", Confidence: 1.0})

	preamble := GlossaryPreamble("Reviewing SLA targets for Q3", g)
	if preamble == "" {
		t.Fatal("expected non-empty preamble for known acronym SLA")
	}
	want := "[Glossary: SLA = Service Level Agreement (confluence)]"
	if !containsStr(preamble, want) {
		t.Errorf("preamble %q does not contain %q", preamble, want)
	}
}

func TestGlossaryPreambleUnknownAcronym(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)

	preamble := GlossaryPreamble("Reviewing SLA targets", g)
	if preamble != "" {
		t.Errorf("expected empty preamble for unknown acronym, got %q", preamble)
	}
}

func TestGlossaryPreambleNilCache(t *testing.T) {
	preamble := GlossaryPreamble("SLA targets", nil)
	if preamble != "" {
		t.Errorf("expected empty preamble for nil cache, got %q", preamble)
	}
}

func TestGlossaryPreambleMultiple(t *testing.T) {
	path := filepath.Join(t.TempDir(), "glossary.json")
	g, _ := NewGlossaryCache(path)
	_ = g.Add(GlossaryEntry{Acronym: "SLA", Expansion: "Service Level Agreement", Source: "confluence", Confidence: 1.0})
	_ = g.Add(GlossaryEntry{Acronym: "KPI", Expansion: "Key Performance Indicator", Source: "confluence", Confidence: 1.0})

	preamble := GlossaryPreamble("SLA and KPI dashboard", g)
	if !containsStr(preamble, "SLA") {
		t.Errorf("preamble missing SLA: %q", preamble)
	}
	if !containsStr(preamble, "KPI") {
		t.Errorf("preamble missing KPI: %q", preamble)
	}
}

// ---------------------------------------------------------------------------
// parseResearchReply
// ---------------------------------------------------------------------------

func TestParseResearchReplyFull(t *testing.T) {
	reply := "EXPANSION: Continuous Integration\nCONFIDENCE: 0.85"
	exp, conf := parseResearchReply(reply)
	if exp != "Continuous Integration" {
		t.Errorf("expansion = %q, want %q", exp, "Continuous Integration")
	}
	if conf != 0.85 {
		t.Errorf("confidence = %v, want 0.85", conf)
	}
}

func TestParseResearchReplyMissingConfidence(t *testing.T) {
	reply := "EXPANSION: Some Expansion"
	exp, conf := parseResearchReply(reply)
	if exp != "Some Expansion" {
		t.Errorf("expansion = %q, want %q", exp, "Some Expansion")
	}
	if conf != 0.7 {
		t.Errorf("confidence = %v, want default 0.7", conf)
	}
}

func TestParseResearchReplyEmpty(t *testing.T) {
	exp, conf := parseResearchReply("")
	if exp != "" {
		t.Errorf("expected empty expansion, got %q", exp)
	}
	if conf != 0.7 {
		t.Errorf("expected default confidence 0.7, got %v", conf)
	}
}

// ---------------------------------------------------------------------------
// extractJSONArray
// ---------------------------------------------------------------------------

func TestExtractJSONArrayWrapped(t *testing.T) {
	s := `Here are the results: [{"acronym":"API","expansion":"Application Programming Interface","source_url":"https://example.com"}]`
	got := extractJSONArray(s)
	if got[0] != '[' || got[len(got)-1] != ']' {
		t.Errorf("extractJSONArray returned non-array: %s", got)
	}
	var list []map[string]string
	if err := json.Unmarshal([]byte(got), &list); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
}

func TestExtractJSONArrayEmpty(t *testing.T) {
	got := extractJSONArray("No JSON here")
	if got != "[]" {
		t.Errorf("expected [] fallback, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && indexStr(s, substr) >= 0)
}

func indexStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func containsByte(b []byte, c byte) bool {
	for _, x := range b {
		if x == c {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
