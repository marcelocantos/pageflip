// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — bubbletea TUI for meetcat (🎯T19.3).
//
// Layout (terminal-width auto-sizing, 2×2 grid):
//
//	┌──────────────────────────┐┌──────────────────────────┐
//	│ [skeptic / sonnet]       ││ [constructive / sonnet]  │
//	│  streaming output …      ││  streaming output …      │
//	│  ▸ turn 1 (s1)           ││  ▸ turn 1 (s1)           │
//	└──────────────────────────┘└──────────────────────────┘
//	┌──────────────────────────┐┌──────────────────────────┐
//	│ [neutral / sonnet]       ││ [dejargoniser / haiku]   │
//	│  streaming output …      ││  streaming output …      │
//	└──────────────────────────┘└──────────────────────────┘
//	  slides: 3   elapsed: 0:04   meeting: meetcat-…
//
// Keyboard:
//   Tab / Shift-Tab — cycle focus
//   Enter           — expand/collapse most recent history entry
//   /               — incremental search within focused pane (Esc to close)
//   q / Ctrl-C      — quit
package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// urlRe matches http/https URLs. Used for OSC 8 hyperlink wrapping (🎯T19.4).
var urlRe = regexp.MustCompile(`https?://\S+`)

// wrapURLs replaces bare URLs in s with OSC 8 hyperlink escape sequences
// so terminal emulators that support OSC 8 render them as clickable links.
// Terminals that do not support OSC 8 display the URL text unchanged.
//
// OSC 8 format: \x1b]8;;<URL>\x07<TEXT>\x1b]8;;\x07
func wrapURLs(s string) string {
	return urlRe.ReplaceAllStringFunc(s, func(url string) string {
		return "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
	})
}

// ---------------------------------------------------------------------------
// Messages exchanged inside the bubbletea event loop
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
// Pane state
// ---------------------------------------------------------------------------

// historyEntry is one completed prior turn.
type historyEntry struct {
	slideID string
	text    string
}

// pane holds the display state for one specialist.
type pane struct {
	name     string
	model    string
	current  string         // streaming text for the current turn
	history  []historyEntry // completed prior turns
	expanded map[int]bool   // which history indices are expanded
	search   string         // active search query (empty = no search)
}

func newPane(name, model string) pane {
	return pane{
		name:     name,
		model:    model,
		expanded: make(map[int]bool),
	}
}

func (p *pane) appendToken(text string) { p.current += text }

func (p *pane) commitTurn(slideID string) {
	if p.current == "" {
		return
	}
	p.history = append(p.history, historyEntry{slideID: slideID, text: p.current})
	p.current = ""
}

func (p *pane) toggleHistory(i int) { p.expanded[i] = !p.expanded[i] }

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

// tuiModel is the root bubbletea Model.
type tuiModel struct {
	panes      []pane
	paneIndex  map[string]int // specialist name → panes index
	focused    int
	slideCount int
	startTime  time.Time
	meetingID  string
	slideID    string // most recently seen slide ID
	width      int
	height     int
	searchMode bool
	quitting   bool
}

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

	case tuiTickMsg:
		return m, tickCmd()

	case tuiSlideMsg:
		m.slideCount++
		m.slideID = msg.ev.SlideID

	case tuiTokenMsg:
		if i, ok := m.paneIndex[msg.name]; ok {
			m.panes[i].appendToken(msg.text)
		}

	case tuiTurnDoneMsg:
		if i, ok := m.paneIndex[msg.name]; ok {
			m.panes[i].commitTurn(m.slideID)
		}

	case tuiStopMsg:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m tuiModel) handleKey(msg tea.KeyMsg) (tuiModel, tea.Cmd) {
	if m.searchMode {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyEnter:
			m.searchMode = false
		case tea.KeyBackspace:
			s := m.panes[m.focused].search
			if len(s) > 0 {
				m.panes[m.focused].search = s[:len(s)-1]
			}
		default:
			if len(msg.Runes) > 0 {
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
	styleFocusBorder      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63"))
	styleNormalBorder     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	styleHeader           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	styleHistoryLine      = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	styleSearchPrefix     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleStatusBar        = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")).Padding(0, 1)
	styleSearchHighlight  = lipgloss.NewStyle().Background(lipgloss.Color("220")).Foreground(lipgloss.Color("0"))
)

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m tuiModel) View() string {
	if m.quitting {
		return ""
	}

	const statusBarH = 1
	availH := m.height - statusBarH
	colW := m.width / 2
	rowH := availH / 2

	topRow := m.renderRow(0, 1, colW, rowH)
	botRow := m.renderRow(2, 3, colW, rowH)
	status := m.renderStatusBar()

	return topRow + "\n" + botRow + "\n" + status
}

// renderRow renders two adjacent panes side by side.
func (m tuiModel) renderRow(leftIdx, rightIdx, colW, rowH int) string {
	left := m.renderPane(leftIdx, colW, rowH)
	right := m.renderPane(rightIdx, colW, rowH)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// renderPane renders one specialist pane at the given outer dimensions.
func (m tuiModel) renderPane(idx int, w, h int) string {
	// Border takes 1 cell on each side.
	innerW := w - 2
	innerH := h - 2
	if innerW < 4 {
		innerW = 4
	}
	if innerH < 2 {
		innerH = 2
	}

	if idx >= len(m.panes) {
		empty := lipgloss.NewStyle().Width(innerW).Height(innerH).Render("")
		return styleNormalBorder.Width(innerW).Height(innerH).Render(empty)
	}

	p := m.panes[idx]
	focused := idx == m.focused

	var lines []string

	// Header.
	header := styleHeader.Render(fmt.Sprintf("[%s / %s]", p.name, p.model))
	lines = append(lines, truncate(header, innerW))

	// History entries (collapsed by default; Enter expands the most recent).
	for i, he := range p.history {
		indicator := "▸"
		if p.expanded[i] {
			indicator = "▾"
		}
		label := fmt.Sprintf("%s turn %d (%s)", indicator, i+1, he.slideID)
		lines = append(lines, styleHistoryLine.Render(truncate(label, innerW)))
		if p.expanded[i] {
			for _, wl := range wrapLines(he.text, innerW) {
				// 🎯T19.4: wrap bare URLs as OSC 8 hyperlinks.
				lines = append(lines, truncate(wrapURLs(wl), innerW))
			}
		}
	}

	// Current streaming output, optionally search-highlighted.
	// 🎯T19.4: wrap bare URLs as OSC 8 hyperlinks before highlighting.
	for _, cl := range wrapLines(p.current, innerW) {
		cl = wrapURLs(cl)
		if p.search != "" {
			lines = append(lines, highlightSearch(cl, p.search))
		} else {
			lines = append(lines, cl)
		}
	}

	// Search prompt at the bottom when focused.
	if m.searchMode && focused {
		lines = append(lines, styleSearchPrefix.Render("/")+p.search)
	}

	// Pad to innerH; keep last innerH lines when content overflows.
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	if len(lines) > innerH {
		lines = lines[len(lines)-innerH:]
	}

	body := lipgloss.NewStyle().Width(innerW).Height(innerH).Render(strings.Join(lines, "\n"))

	border := styleNormalBorder
	if focused {
		border = styleFocusBorder
	}
	return border.Width(innerW).Height(innerH).Render(body)
}

// renderStatusBar renders the bottom status line.
func (m tuiModel) renderStatusBar() string {
	elapsed := time.Since(m.startTime).Round(time.Second)
	mins := int(elapsed.Minutes())
	secs := int(elapsed.Seconds()) % 60
	return styleStatusBar.Width(m.width).Render(
		fmt.Sprintf("slides: %d   elapsed: %d:%02d   meeting: %s", m.slideCount, mins, secs, m.meetingID),
	)
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

// truncate trims s to at most n runes, appending "…" if trimmed.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// wrapLines hard-wraps s into lines of at most w runes each.
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

// highlightSearch returns line with all case-insensitive occurrences of
// query wrapped in the search highlight style.
func highlightSearch(line, query string) string {
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
