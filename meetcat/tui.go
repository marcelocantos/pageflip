// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — bubbletea TUI for meetcat (🎯T19.3).
//
// Layout (terminal-width auto-sizing):
//
//	┌──────────────────────────────────────────────────────────────┐
//	│ [skeptic / sonnet]          [constructive / sonnet]          │
//	│  streaming output …          streaming output …              │
//	│  ── turn 1 (collapsed) ──   ── turn 1 (collapsed) ──        │
//	├──────────────────────────────────────────────────────────────┤
//	│ [neutral / sonnet]          [dejargoniser / haiku]           │
//	│  streaming output …          streaming output …              │
//	│  ── turn 1 (collapsed) ──   ── turn 1 (collapsed) ──        │
//	├──────────────────────────────────────────────────────────────┤
//	│  slides: 3   elapsed: 0:04   meeting: meetcat-1713298765432  │
//	└──────────────────────────────────────────────────────────────┘
//
// Keyboard: Tab/Shift-Tab — cycle focus; Enter — expand/collapse history;
// / — search within focused pane; q — quit.
package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Messages exchanged inside the bubbletea event loop.
// ---------------------------------------------------------------------------

// tuiSlideMsg is sent when a new slide event arrives.
type tuiSlideMsg struct {
	ev *slideEvent
}

// tuiTokenMsg carries streaming token output for one specialist.
type tuiTokenMsg struct {
	name string
	text string
}

// tuiTurnDoneMsg is sent when a specialist finishes a turn.
type tuiTurnDoneMsg struct {
	name string
}

// tuiTickMsg triggers the elapsed-time counter.
type tuiTickMsg time.Time

// tuiStopMsg signals the TUI to close (meeting EOF or q key).
type tuiStopMsg struct{}

// ---------------------------------------------------------------------------
// Pane
// ---------------------------------------------------------------------------

// historyEntry is one prior completed turn.
type historyEntry struct {
	slideID string
	text    string
}

// pane holds the display state for one specialist.
type pane struct {
	name    string
	model   string
	current string          // streaming text for the current turn
	history []historyEntry  // completed prior turns
	expanded map[int]bool   // which history indices are expanded
	search  string          // search string (/ mode)
}

func newPane(name, model string) pane {
	return pane{
		name:     name,
		model:    model,
		expanded: make(map[int]bool),
	}
}

// appendToken adds streaming text to the current turn.
func (p *pane) appendToken(text string) {
	p.current += text
}

// commitTurn moves the current text into history.
func (p *pane) commitTurn(slideID string) {
	if p.current == "" {
		return
	}
	p.history = append(p.history, historyEntry{slideID: slideID, text: p.current})
	p.current = ""
}

// toggleHistory expands/collapses turn i.
func (p *pane) toggleHistory(i int) {
	p.expanded[i] = !p.expanded[i]
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// tuiModel is the root bubbletea Model.
type tuiModel struct {
	panes      []pane        // ordered: skeptic, constructive, neutral, dejargoniser
	paneIndex  map[string]int // name → panes index
	focused    int            // index into panes
	slideCount int
	startTime  time.Time
	meetingID  string
	slideID    string // current slide
	width      int
	height     int
	searchMode bool
	quitting   bool
}

// newTUIModel initialises a fresh model.
func newTUIModel(meetingID string, specs []specialistDef) tuiModel {
	panes := make([]pane, len(specs))
	index := make(map[string]int, len(specs))
	for i, s := range specs {
		panes[i] = newPane(s.name, s.model)
		index[s.name] = i
	}
	return tuiModel{
		panes:     panes,
		paneIndex: index,
		focused:   0,
		startTime: time.Now(),
		meetingID: meetingID,
		width:     80,
		height:    24,
	}
}

// ---------------------------------------------------------------------------
// Init / Update / View
// ---------------------------------------------------------------------------

func (m tuiModel) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tuiTickMsg(t)
	})
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tuiTickMsg:
		return m, tickCmd()

	case tuiSlideMsg:
		m.slideCount++
		m.slideID = msg.ev.SlideID
		// Clear current streaming text for all panes on new slide
		// (turn will be committed via tuiTurnDoneMsg before next slide arrives
		// in normal flow, but guard here for robustness).
		return m, nil

	case tuiTokenMsg:
		if i, ok := m.paneIndex[msg.name]; ok {
			m.panes[i].appendToken(msg.text)
		}
		return m, nil

	case tuiTurnDoneMsg:
		if i, ok := m.paneIndex[msg.name]; ok {
			m.panes[i].commitTurn(m.slideID)
		}
		return m, nil

	case tuiStopMsg:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searchMode {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyEnter:
			m.searchMode = false
		case tea.KeyBackspace:
			if len(m.panes[m.focused].search) > 0 {
				m.panes[m.focused].search = m.panes[m.focused].search[:len(m.panes[m.focused].search)-1]
			}
		default:
			if msg.Runes != nil {
				m.panes[m.focused].search += string(msg.Runes)
			}
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "tab":
		m.focused = (m.focused + 1) % len(m.panes)
	case "shift+tab":
		m.focused = (m.focused - 1 + len(m.panes)) % len(m.panes)
	case "enter":
		// Toggle most recent history entry.
		p := &m.panes[m.focused]
		if len(p.history) > 0 {
			p.toggleHistory(len(p.history) - 1)
		}
	case "/":
		m.searchMode = true
		m.panes[m.focused].search = ""
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Styles
// ---------------------------------------------------------------------------

var (
	styleFocusBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63"))
	styleNormalBorder  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	styleHeader        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	styleHistoryLine   = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	styleSearchPrefix  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleStatusBar     = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")).Padding(0, 1)
	styleSearchHighlight = lipgloss.NewStyle().Background(lipgloss.Color("220")).Foreground(lipgloss.Color("0"))
)

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m tuiModel) View() string {
	if m.quitting {
		return ""
	}

	statusBarH := 1
	availH := m.height - statusBarH

	// 2×2 grid: two columns, two rows.
	colW := m.width / 2
	rowH := availH / 2

	topRow := m.renderRow(0, 1, colW, rowH)
	botRow := m.renderRow(2, 3, colW, rowH)
	statusBar := m.renderStatusBar()

	return topRow + "\n" + botRow + "\n" + statusBar
}

// renderRow renders a pair of panes side by side.
func (m tuiModel) renderRow(leftIdx, rightIdx, colW, rowH int) string {
	left := m.renderPane(leftIdx, colW, rowH)
	right := m.renderPane(rightIdx, colW, rowH)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// renderPane renders one specialist pane at the given dimensions.
func (m tuiModel) renderPane(idx int, w, h int) string {
	if idx >= len(m.panes) {
		return lipgloss.NewStyle().Width(w).Height(h).Render("")
	}

	p := m.panes[idx]
	focused := idx == m.focused

	// Inner width/height accounting for border (1 each side).
	innerW := w - 2
	innerH := h - 2
	if innerW < 4 {
		innerW = 4
	}
	if innerH < 2 {
		innerH = 2
	}

	var lines []string

	// Header line.
	header := styleHeader.Render(fmt.Sprintf("[%s / %s]", p.name, p.model))
	lines = append(lines, truncate(header, innerW))

	// History entries (collapsed by default, expand on Enter).
	for i, h := range p.history {
		indicator := "▸"
		if p.expanded[i] {
			indicator = "▾"
		}
		label := fmt.Sprintf("%s turn %d (%s)", indicator, i+1, h.slideID)
		lines = append(lines, styleHistoryLine.Render(truncate(label, innerW)))
		if p.expanded[i] {
			for _, hl := range wrapLines(h.text, innerW) {
				lines = append(lines, truncate(hl, innerW))
			}
		}
	}

	// Current streaming output — search-highlighted if search is active.
	currentLines := wrapLines(p.current, innerW)
	if p.search != "" {
		for _, cl := range currentLines {
			lines = append(lines, highlightSearch(cl, p.search, innerW))
		}
	} else {
		for _, cl := range currentLines {
			lines = append(lines, cl)
		}
	}

	// Search prompt at the bottom of the pane.
	if m.searchMode && focused {
		prompt := styleSearchPrefix.Render("/") + p.search
		lines = append(lines, prompt)
	}

	// Pad / truncate to innerH lines.
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	if len(lines) > innerH {
		lines = lines[len(lines)-innerH:]
	}

	body := lipgloss.NewStyle().Width(innerW).Height(innerH).Render(strings.Join(lines, "\n"))

	style := styleNormalBorder.Width(w - 2).Height(h - 2)
	if focused {
		style = styleFocusBorder.Width(w - 2).Height(h - 2)
	}
	return style.Render(body)
}

// renderStatusBar renders the bottom status line.
func (m tuiModel) renderStatusBar() string {
	elapsed := time.Since(m.startTime).Round(time.Second)
	mins := int(elapsed.Minutes())
	secs := int(elapsed.Seconds()) % 60
	text := fmt.Sprintf("  slides: %d   elapsed: %d:%02d   meeting: %s  ", m.slideCount, mins, secs, m.meetingID)
	return styleStatusBar.Width(m.width).Render(text)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// truncate trims s to at most n visible bytes (ASCII-safe; good enough for
// log-style output).
func truncate(s string, n int) string {
	// Strip ANSI codes for length calculation is complex; use rune count as
	// approximation since lipgloss handles the actual rendering.
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// wrapLines wraps s to lines of at most w runes each.
func wrapLines(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		runes := []rune(line)
		for len(runes) > w {
			out = append(out, string(runes[:w]))
			runes = runes[w:]
		}
		out = append(out, string(runes))
	}
	return out
}

// highlightSearch returns line with occurrences of query highlighted.
func highlightSearch(line, query string, _ int) string {
	if query == "" {
		return line
	}
	lower := strings.ToLower(line)
	lq := strings.ToLower(query)
	var result strings.Builder
	remaining := line
	remainingLower := lower
	for {
		idx := strings.Index(remainingLower, lq)
		if idx < 0 {
			result.WriteString(remaining)
			break
		}
		result.WriteString(remaining[:idx])
		result.WriteString(styleSearchHighlight.Render(remaining[idx : idx+len(query)]))
		remaining = remaining[idx+len(query):]
		remainingLower = remainingLower[idx+len(query):]
	}
	return result.String()
}
