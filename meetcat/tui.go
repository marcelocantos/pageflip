// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// 🎯T19.3 (resurrected): bubbletea TUI focused on one job — surface
// the moment a slide event arrives at the head of meetcat's pipeline,
// independently of any specialist response. The status bar at the top
// updates the instant a slide passes validation; the scrolling
// viewport below carries the chronological log of section headers and
// specialist tokens.
//
// Layout:
//
//   ┌──────────────────────────────────────────────────────────────────────┐
//   │ frames: 17 · last: s2026-… · age: 1.2s · meeting: meetcat-…          │  ← status bar
//   ├──────────────────────────────────────────────────────────────────────┤
//   │ ──── ◆ [17] s2026-… ──── (t=…ms, dur=…ms, app=Keynote) /path         │
//   │ [skeptic | s2026-…] First bullet…                                    │
//   │ [neutral | s2026-…] …                                                │
//   │ ↑ scrollback                                                         │
//   └──────────────────────────────────────────────────────────────────────┘
//
// Keys: q / Ctrl-C quits, PgUp/PgDn / arrows scroll the viewport.
//
// Activation: only when stderr is a TTY. Piped invocations (CI, tests,
// `meetcat | tee`) keep the plain streaming path.
package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// tuiSlideArrivedMsg fires the moment runText sees a new validated
// slide event — before pool.SendSlide is called. It increments the
// frames counter and resets the age timer so the operator can see
// the pipeline is moving even when every specialist is silent.
type tuiSlideArrivedMsg struct {
	slideID string
	at      time.Time
}

// tuiLineMsg carries one line of streaming output (specialist tokens,
// system messages, slide section headers) for the viewport.
type tuiLineMsg struct {
	line string
}

// tuiTickMsg refreshes the status-bar age display so a stalled
// pipeline is visually obvious — without a tick the age field would
// only update on slide arrival, which is exactly when the operator
// can't tell anything is wrong.
type tuiTickMsg time.Time

// tuiQuitMsg signals the TUI to exit cleanly (e.g. from runText's
// defer block at EOF).
type tuiQuitMsg struct{}

// tuiTickInterval drives the age-timer refresh. 100 ms is fast enough
// to feel live without burning a noticeable amount of CPU.
const tuiTickInterval = 100 * time.Millisecond

func tuiTickCmd() tea.Cmd {
	return tea.Tick(tuiTickInterval, func(t time.Time) tea.Msg {
		return tuiTickMsg(t)
	})
}

// tuiModel is the bubbletea model. Lines accumulate in `lines` and
// are re-rendered into the viewport on each update. The buffer is
// not pruned: a multi-hour meeting at one slide per minute produces
// at most a few thousand lines of specialist output, well within
// memory budget. If that ever becomes an issue, cap the slice.
type tuiModel struct {
	meetingID   string
	framesRecv  int
	lastSlide   string
	lastFrameAt time.Time

	width    int
	height   int
	viewport viewport.Model
	ready    bool
	lines    []string

	quitting bool
}

func newTUIModel(meetingID string) tuiModel {
	return tuiModel{meetingID: meetingID}
}

func (m tuiModel) Init() tea.Cmd {
	return tuiTickCmd()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
		if m.ready {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

	case tea.WindowSizeMsg:
		barH := 1
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-barH)
			m.viewport.YPosition = barH
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - barH
		}
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetContent(strings.Join(m.lines, "\n"))
		m.viewport.GotoBottom()

	case tuiLineMsg:
		m.lines = append(m.lines, msg.line)
		if m.ready {
			m.viewport.SetContent(strings.Join(m.lines, "\n"))
			m.viewport.GotoBottom()
		}

	case tuiSlideArrivedMsg:
		m.framesRecv++
		m.lastSlide = msg.slideID
		m.lastFrameAt = msg.at

	case tuiTickMsg:
		return m, tuiTickCmd()

	case tuiQuitMsg:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// statusBarStyle is the persistent top bar. Dim background + bright
// text so it stands out against the viewport without being garish.
var statusBarStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("63")).
	Foreground(lipgloss.Color("231")).
	Bold(true)

func (m tuiModel) View() string {
	if !m.ready {
		return "meetcat: initialising TUI…"
	}
	last := m.lastSlide
	if last == "" {
		last = "—"
	}
	age := "—"
	if !m.lastFrameAt.IsZero() {
		age = fmt.Sprintf("%.1fs", time.Since(m.lastFrameAt).Seconds())
	}
	bar := fmt.Sprintf(
		" frames: %d · last: %s · age: %s · meeting: %s",
		m.framesRecv, last, age, m.meetingID,
	)
	if w := m.width; w > 0 && len(bar) < w {
		bar += strings.Repeat(" ", w-lipgloss.Width(bar))
	}
	return statusBarStyle.Render(bar) + "\n" + m.viewport.View()
}

// tuiWriter is the io.Writer adapter that pumps streaming output
// (pool stderr, summary writes) into the TUI viewport. Each call's
// payload is split on newlines so a single fmt.Fprintf with embedded
// "\n" produces multiple viewport lines, the way the operator
// expects from a stream-of-lines mental model.
type tuiWriter struct {
	prog *tea.Program

	mu      sync.Mutex
	pending strings.Builder
}

func newTUIWriter(p *tea.Program) *tuiWriter {
	return &tuiWriter{prog: p}
}

func (w *tuiWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending.Write(p)
	buf := w.pending.String()
	idx := strings.LastIndexByte(buf, '\n')
	if idx < 0 {
		// No complete line yet — keep buffering.
		return len(p), nil
	}
	complete := buf[:idx]
	tail := buf[idx+1:]
	w.pending.Reset()
	w.pending.WriteString(tail)
	for _, line := range strings.Split(complete, "\n") {
		w.prog.Send(tuiLineMsg{line: line})
	}
	return len(p), nil
}

// flush forwards any unterminated pending text as a final line. Call
// after the TUI exits if there's a chance of a tail without a newline
// (rare in this codebase — every print site uses Fprintln/%s\n).
func (w *tuiWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() == 0 {
		return
	}
	w.prog.Send(tuiLineMsg{line: w.pending.String()})
	w.pending.Reset()
}

// runWithTUI wraps a streaming run with a bubbletea TUI. The pool's
// stderr writer is replaced with a tuiWriter for the TUI's lifetime
// so specialist output flows into the viewport; the caller's
// `summary` is also redirected so slide section headers and
// end-of-meeting turn counts land in the same place.
//
// onSlide(ev) is invoked from the decode loop the moment a slide is
// validated, *before* pool.SendSlide. The TUI status bar receives a
// tuiSlideArrivedMsg with the slide_id and current time so the age
// counter can start from zero on every arrival.
//
// Returns a cleanup function that quits the TUI and waits for it to
// exit. The cleanup must be called before any post-meeting writes
// to os.Stderr (otherwise the alt-screen restoration races the
// stderr write and the operator sees garbled output).
func runWithTUI(meetingID string, pool *SessionPool, summary io.Writer) (
	*tea.Program, io.Writer, func(), func(slideID string),
) {
	model := newTUIModel(meetingID)
	prog := tea.NewProgram(model, tea.WithAltScreen())
	writer := newTUIWriter(prog)

	if pool != nil {
		pool.stderr = writer
	}

	done := make(chan struct{})
	go func() {
		_, _ = prog.Run()
		close(done)
	}()

	cleanup := func() {
		prog.Send(tuiQuitMsg{})
		<-done
		writer.flush()
	}
	onSlide := func(slideID string) {
		prog.Send(tuiSlideArrivedMsg{slideID: slideID, at: time.Now()})
	}
	return prog, writer, cleanup, onSlide
}
