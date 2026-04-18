// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func testModel() tuiModel {
	specs := allSpecialists()
	m := newTUIModel("meetcat-test", specs)
	m.width = 120
	m.height = 30
	return m
}

// ---------------------------------------------------------------------------
// Token routing
// ---------------------------------------------------------------------------

func TestTUITokenRouting(t *testing.T) {
	m := testModel()
	spec := allSpecialists()[0].name // "skeptic"

	m2, _ := m.Update(tuiTokenMsg{name: spec, text: "hello "})
	m2, _ = m2.(tuiModel).Update(tuiTokenMsg{name: spec, text: "world"})

	idx := m2.(tuiModel).paneIndex[spec]
	got := m2.(tuiModel).panes[idx].current
	if got != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", got)
	}
}

func TestTUIUnknownSpecialistIgnored(t *testing.T) {
	m := testModel()
	// Should not panic for unknown specialist name.
	m2, _ := m.Update(tuiTokenMsg{name: "nonexistent", text: "boom"})
	_ = m2
}

// ---------------------------------------------------------------------------
// Slide events
// ---------------------------------------------------------------------------

func TestTUISlideCount(t *testing.T) {
	m := testModel()
	ev := &slideEvent{SlideID: "s1", Path: "/p", TStartMs: 0, TEndMs: 1}
	m2, _ := m.Update(tuiSlideMsg{ev: ev})
	if m2.(tuiModel).slideCount != 1 {
		t.Errorf("expected slideCount 1, got %d", m2.(tuiModel).slideCount)
	}
	if m2.(tuiModel).slideID != "s1" {
		t.Errorf("expected slideID s1, got %q", m2.(tuiModel).slideID)
	}
}

// ---------------------------------------------------------------------------
// Turn commit
// ---------------------------------------------------------------------------

func TestTUITurnCommit(t *testing.T) {
	m := testModel()
	spec := allSpecialists()[1].name // "constructive"

	// Write tokens, then commit.
	m2raw, _ := m.Update(tuiTokenMsg{name: spec, text: "insight"})
	m = m2raw.(tuiModel)
	// Fake the slide ID.
	m.slideID = "slide-1"
	m2, _ := m.Update(tuiTurnDoneMsg{name: spec})

	idx := m2.(tuiModel).paneIndex[spec]
	p := m2.(tuiModel).panes[idx]
	if p.current != "" {
		t.Errorf("expected current to be cleared, got %q", p.current)
	}
	if len(p.history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(p.history))
	}
	if p.history[0].text != "insight" {
		t.Errorf("expected history text %q, got %q", "insight", p.history[0].text)
	}
	if p.history[0].slideID != "slide-1" {
		t.Errorf("expected history slideID %q, got %q", "slide-1", p.history[0].slideID)
	}
}

// ---------------------------------------------------------------------------
// Keyboard navigation
// ---------------------------------------------------------------------------

func TestTUITabCyclesFocus(t *testing.T) {
	m := testModel()
	n := len(m.panes)
	for i := 1; i <= n; i++ {
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = m2.(tuiModel)
		want := i % n
		if m.focused != want {
			t.Errorf("after %d tabs: expected focused=%d, got %d", i, want, m.focused)
		}
	}
}

func TestTUIShiftTabCyclesBackward(t *testing.T) {
	m := testModel()
	n := len(m.panes)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m2.(tuiModel).focused != n-1 {
		t.Errorf("expected focused=%d, got %d", n-1, m2.(tuiModel).focused)
	}
}

func TestTUIQuitKey(t *testing.T) {
	m := testModel()
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m2.(tuiModel).quitting {
		t.Error("expected quitting=true after q")
	}
	if cmd == nil {
		t.Error("expected a Quit cmd after q")
	}
}

func TestTUISearchMode(t *testing.T) {
	m := testModel()
	// Enter search mode.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	tm := m2.(tuiModel)
	if !tm.searchMode {
		t.Fatal("expected searchMode=true after /")
	}
	// Type a query.
	m3, _ := tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("foo")})
	if m3.(tuiModel).panes[m3.(tuiModel).focused].search != "foo" {
		t.Errorf("expected search=%q, got %q", "foo", m3.(tuiModel).panes[m3.(tuiModel).focused].search)
	}
	// Escape closes search.
	m4, _ := m3.(tuiModel).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m4.(tuiModel).searchMode {
		t.Error("expected searchMode=false after Esc")
	}
}

func TestTUIEnterExpandsHistory(t *testing.T) {
	m := testModel()
	spec := allSpecialists()[0].name
	idx := m.paneIndex[spec]

	// Add a history entry manually.
	m.panes[idx].history = append(m.panes[idx].history, historyEntry{slideID: "s1", text: "content"})

	// Press Enter to toggle.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m2.(tuiModel).panes[idx].expanded[0] {
		t.Error("expected history[0] to be expanded after Enter")
	}
	// Press Enter again to collapse.
	m3, _ := m2.(tuiModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m3.(tuiModel).panes[idx].expanded[0] {
		t.Error("expected history[0] to be collapsed after second Enter")
	}
}

// ---------------------------------------------------------------------------
// View rendering
// ---------------------------------------------------------------------------

func TestTUIViewContainsSpecialistNames(t *testing.T) {
	m := testModel()
	view := m.View()
	for _, spec := range allSpecialists() {
		if !strings.Contains(view, spec.name) {
			t.Errorf("view missing specialist name %q", spec.name)
		}
	}
}

func TestTUIViewContainsMeetingID(t *testing.T) {
	m := testModel()
	view := m.View()
	if !strings.Contains(view, "meetcat-test") {
		t.Errorf("view missing meeting ID; view: %q", view[:min(200, len(view))])
	}
}

func TestTUIViewQuittingIsEmpty(t *testing.T) {
	m := testModel()
	m.quitting = true
	if m.View() != "" {
		t.Error("expected empty view when quitting")
	}
}

func TestTUIStatusBarElapsed(t *testing.T) {
	m := testModel()
	m.startTime = time.Now().Add(-65 * time.Second)
	bar := m.renderStatusBar()
	if !strings.Contains(bar, "1:05") {
		t.Errorf("expected 1:05 in status bar, got %q", bar)
	}
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

func TestTruncate(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"hello", 5, "hello"},
		{"hello", 1, "…"},
	}
	for _, tc := range tests {
		got := truncate(tc.in, tc.n)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q; want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestWrapLines(t *testing.T) {
	// No trailing newline in input → no trailing empty string in output.
	lines := wrapLines("abcdef", 3)
	want := []string{"abc", "def"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(lines), lines)
	}
	for i, l := range lines {
		if l != want[i] {
			t.Errorf("line[%d] = %q; want %q", i, l, want[i])
		}
	}
}

func TestWrapLinesTrailingNewline(t *testing.T) {
	// A trailing newline produces a trailing empty string (split behaviour).
	lines := wrapLines("abc\n", 10)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "abc" || lines[1] != "" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestWrapLinesNewline(t *testing.T) {
	lines := wrapLines("ab\ncd", 10)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
}

func TestHighlightSearch(t *testing.T) {
	result := highlightSearch("Hello World", "world")
	// The highlighted version must preserve the matched text (case-insensitive match,
	// but original case is kept in the rendered output).
	if !strings.Contains(result, "World") {
		t.Errorf("highlight should preserve match case; got %q", result)
	}
	// In test environments lipgloss may or may not emit ANSI codes depending on
	// whether a TTY is detected. The invariant we test is that the match text is
	// preserved and non-matching prefix is also present.
	if !strings.Contains(result, "Hello") {
		t.Errorf("highlight should preserve non-matching prefix; got %q", result)
	}
}

func TestHighlightSearchEmpty(t *testing.T) {
	line := "no match here"
	if highlightSearch(line, "") != line {
		t.Error("empty query should return line unchanged")
	}
}

// ---------------------------------------------------------------------------
// Window resize
// ---------------------------------------------------------------------------

func TestTUIWindowResize(t *testing.T) {
	m := testModel()
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	tm := m2.(tuiModel)
	if tm.width != 200 || tm.height != 50 {
		t.Errorf("expected 200×50, got %d×%d", tm.width, tm.height)
	}
}

// ---------------------------------------------------------------------------
// OSC 8 URL wrapping (🎯T19.4)
// ---------------------------------------------------------------------------

func TestWrapURLsNoURL(t *testing.T) {
	s := "no urls here"
	if got := wrapURLs(s); got != s {
		t.Errorf("no-URL string should be unchanged; got %q", got)
	}
}

func TestWrapURLsHTTP(t *testing.T) {
	s := "see http://example.com for details"
	got := wrapURLs(s)
	// Must contain the OSC 8 opener and the URL itself.
	if !strings.Contains(got, "\x1b]8;;http://example.com\x07") {
		t.Errorf("missing OSC 8 open; got %q", got)
	}
	// Must contain the OSC 8 closer.
	if !strings.Contains(got, "\x1b]8;;\x07") {
		t.Errorf("missing OSC 8 close; got %q", got)
	}
	// Surrounding text must be preserved.
	if !strings.Contains(got, "see ") || !strings.Contains(got, " for details") {
		t.Errorf("surrounding text lost; got %q", got)
	}
}

func TestWrapURLsHTTPS(t *testing.T) {
	s := "visit https://example.org"
	got := wrapURLs(s)
	if !strings.Contains(got, "\x1b]8;;https://example.org\x07") {
		t.Errorf("HTTPS URL not wrapped; got %q", got)
	}
}

func TestWrapURLsMultiple(t *testing.T) {
	s := "a http://one.com b https://two.org c"
	got := wrapURLs(s)
	if !strings.Contains(got, "http://one.com") {
		t.Errorf("first URL not present; got %q", got)
	}
	if !strings.Contains(got, "https://two.org") {
		t.Errorf("second URL not present; got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
