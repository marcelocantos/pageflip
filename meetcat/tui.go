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
	"log/slog"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// sinkWriter is the io.Writer adapter slog uses to write structured
// log lines into the TUI sink. Each Write splits its payload on '\n'
// and forwards each non-empty line as a dim SystemLine so claudia's
// startup chatter doesn't bypass the alt-screen and shred the
// viewport from underneath.
type sinkWriter struct {
	sink StreamSink
}

func (sw sinkWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}
		sw.sink.SystemLine(colorize(colorDim, line))
	}
	return len(p), nil
}

// installSlogIntoSink redirects the global slog default to write
// through the supplied StreamSink at WARN+ level. claudia's "agent
// started" lines are INFO and would otherwise spam the viewport
// during specialist startup; the session IDs they expose are also
// persisted to session-ids.json for `meetcat attach`, so suppressing
// them at INFO is safe. WARN/ERROR still surface — the operator
// needs to see those.
func installSlogIntoSink(sink StreamSink) {
	h := slog.NewTextHandler(sinkWriter{sink: sink}, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})
	slog.SetDefault(slog.New(h))
}

// tuiSlideArrivedMsg fires the moment runText sees a new validated
// slide event — before pool.SendSlide is called. It increments the
// frames counter, resets the age timer, and opens a new section in
// the viewport so subsequent specialist output for this slide_id
// is grouped underneath the section header rather than appearing
// chronologically wherever it lands.
type tuiSlideArrivedMsg struct {
	slideID string
	header  string
	at      time.Time
}

// tuiLineMsg carries one line of streaming output that is NOT
// attributed to a specific slide — system messages, specialist
// startup/shutdown lines, glossary preambles. Rendered as a
// loose-block at its arrival position in the chronological node
// list (so post-slide lines appear after sections, not above them).
type tuiLineMsg struct {
	line string
}

// tuiAttributedLineMsg carries one streaming chunk from a specialist
// for a specific slide. The chunk is appended to that section's
// per-role running buffer, the buffer is glamour-rendered, and the
// rendered output replaces the section's role-tagged sectionLine.
// Rendering on each chunk (rather than only at turn-end) lets the
// operator watch markdown form up live; momentarily-malformed
// markdown self-corrects when the next chunk lands or when
// tuiSpecialistTurnDoneMsg supplies the canonical full text.
type tuiAttributedLineMsg struct {
	role    string
	slideID string
	chunk   string
}

// tuiSpecialistTurnDoneMsg signals that a specialist has finished
// streaming a complete response. The model replaces the running
// raw buffer with the canonical fullText supplied here, then
// re-renders. Sent from the worker after WaitForResponse returns
// so the final state is well-formed even if the streaming chunks
// happened to leave the buffer in a transient odd state (open
// fence, half-emitted bullet, etc.).
type tuiSpecialistTurnDoneMsg struct {
	role    string
	slideID string
	text    string
}

// specialistState is the lifecycle state of one specialist. Used to
// drive the per-specialist icons in the status bar.
type specialistStateValue int

const (
	specialistBooting specialistStateValue = iota
	specialistReady
	specialistStopped
)

// tuiSpecialistStateMsg signals a specialist's lifecycle transition
// (booting → ready → stopped). The status bar renders each
// specialist as an emoji that's dim while booting, full-colour while
// ready, and dim again with a turn count when stopped — moving these
// signals out of the chronological viewport where they were
// otherwise loose lines.
type tuiSpecialistStateMsg struct {
	role  string
	state specialistStateValue
	turns int // only meaningful for specialistStopped
}

// tuiTickMsg refreshes the status-bar age display so a stalled
// pipeline is visually obvious — without a tick the age field would
// only update on slide arrival, which is exactly when the operator
// can't tell anything is wrong.
type tuiTickMsg time.Time

// tuiQuitMsg signals the TUI to exit cleanly (e.g. from runText's
// defer block at EOF).
type tuiQuitMsg struct{}

// tuiTickInterval drives the age-timer refresh. 1 second matches the
// status-bar's whole-second resolution — finer ticks animated a sub-
// second display that the operator just experienced as flicker.
const tuiTickInterval = 1 * time.Second

func tuiTickCmd() tea.Cmd {
	return tea.Tick(tuiTickInterval, func(t time.Time) tea.Msg {
		return tuiTickMsg(t)
	})
}

// sectionLine is one entry inside a slide's grouped output. text may
// span multiple lines (after a glamour render replaces a streamed
// chunk run); the role tag lets the model find this entry on
// SpecialistTurnDone so it can be replaced with the rendered
// markdown.
type sectionLine struct {
	role string // specialist role; "" only for non-specialist lines (rare in sections)
	text string // pre-formatted; may contain '\n'
}

// tuiSection holds one slide's grouped output: the section header
// (a thick visual separator with the slide_id, count, and metadata)
// plus every specialist line attributed to that slide_id, in
// arrival order. Sections render in slide-arrival order regardless
// of when individual specialist output lands, so out-of-order
// specialist completion stays visually grouped.
//
// rawTurns holds the streaming-buffer of each role's current turn
// keyed by role; on every chunk the buffer is appended to and the
// whole accumulated markdown is re-rendered via glamour. The
// render-replace approach lets the operator watch markdown form up
// live; transient malformed states (open code fence, half-emitted
// list) self-correct on the next chunk or the canonical full-text
// override sent on tuiSpecialistTurnDoneMsg.
type tuiSection struct {
	slideID  string
	header   string
	lines    []sectionLine
	rawTurns map[string]string
}

// tuiNode is one renderable block in the viewport. Either a
// `tuiSection` (slide group) or a `tuiLooseBlock` (system messages
// not tied to any slide — startup banners, "ready" lines, "stopped"
// lines). Nodes render in arrival order so post-slide loose lines
// appear after the sections they came after.
type tuiNode struct {
	section   *tuiSection
	loose     []string // non-nil iff section is nil
	looseDone bool     // once a section opens after this loose block, no more lines append
}

// specialistStatus is the per-specialist row in the status bar.
type specialistStatus struct {
	state specialistStateValue
	turns int
}

// specialistOrder is the canonical render order for specialist
// icons in the status bar. Mirrors allSpecialists() so the operator
// always sees the same lineup left-to-right regardless of which
// boots first.
var specialistOrder = []string{"skeptic", "constructive", "neutral", "dejargoniser", "contradictions"}

// specialistEmoji is the icon set chosen for the status bar:
//   - 🔍 skeptic       — magnifier; questioning, examining
//   - 💡 constructive  — lightbulb; ideas, "yes-and"
//   - 😶 neutral       — neutral face; reference / orientation
//   - 📖 dejargoniser  — open book; glossary
//   - 🛑 contradictions — stop sign; alerts a clash
var specialistEmoji = map[string]string{
	"skeptic":        "🔍",
	"constructive":   "💡",
	"neutral":        "😶",
	"dejargoniser":   "📖",
	"contradictions": "🛑",
}

// tuiModel is the bubbletea model. Specialist output is grouped per
// slide via `nodes` + `slideIdx`; loose lines (system messages, no
// slide attribution) are appended to the trailing loose-block. The
// viewport content is re-rendered on every update.
type tuiModel struct {
	meetingID   string
	framesRecv  int
	lastSlide   string
	lastFrameAt time.Time

	width    int
	height   int
	viewport viewport.Model
	ready    bool

	// nodes is the chronological list of viewport blocks. slideIdx
	// maps slide_id → index of that slide's section node so attributed
	// lines can be routed in O(1). Loose lines append to whichever
	// trailing loose-block is "open" (or create a new one).
	nodes    []tuiNode
	slideIdx map[string]int

	// specialistStatus tracks per-specialist lifecycle for the icons
	// in the status bar. Roles not yet booted simply don't appear.
	specialists map[string]specialistStatus

	quitting bool
}

func newTUIModel(meetingID string) tuiModel {
	return tuiModel{
		meetingID:   meetingID,
		slideIdx:    map[string]int{},
		specialists: map[string]specialistStatus{},
	}
}

// renderNodes flattens the node list into a single string for the
// viewport. Sections render as: header line, then each sectionLine
// verbatim — the emoji gutter is already baked into each line's
// text by renderMarkdown / formatWithGutter, so no extra indent
// is applied here. Loose blocks render their lines verbatim with
// no indent.
func (m *tuiModel) renderNodes() string {
	var b strings.Builder
	for i, n := range m.nodes {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch {
		case n.section != nil:
			b.WriteString(n.section.header)
			for _, l := range n.section.lines {
				b.WriteByte('\n')
				b.WriteString(l.text)
			}
		default:
			for j, l := range n.loose {
				if j > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(l)
			}
		}
	}
	return b.String()
}

// appendLoose adds a non-attributed line. If the trailing node is an
// open loose block, append; otherwise start a new loose block.
func (m *tuiModel) appendLoose(line string) {
	if n := len(m.nodes); n > 0 && m.nodes[n-1].section == nil && !m.nodes[n-1].looseDone {
		m.nodes[n-1].loose = append(m.nodes[n-1].loose, line)
		return
	}
	m.nodes = append(m.nodes, tuiNode{loose: []string{line}})
}

// openSection appends a new slide section. Any preceding loose block
// is "closed" so future loose lines start a fresh block below this
// section instead of appending to the now-stale block above.
func (m *tuiModel) openSection(slideID, header string) {
	if n := len(m.nodes); n > 0 && m.nodes[n-1].section == nil {
		m.nodes[n-1].looseDone = true
	}
	sec := &tuiSection{slideID: slideID, header: header}
	m.slideIdx[slideID] = len(m.nodes)
	m.nodes = append(m.nodes, tuiNode{section: sec})
}

// appendChunkAndRender accumulates one streaming chunk into the
// section's running buffer for (slideID, role) and replaces that
// role's sectionLine with a fresh glamour-rendered version of the
// accumulated buffer. Falls back to a loose-block append when the
// slide section hasn't been opened yet (rare race).
func (m *tuiModel) appendChunkAndRender(role, slideID, chunk string) {
	i, ok := m.slideIdx[slideID]
	if !ok {
		m.appendLoose(tag(role) + " " + chunk)
		return
	}
	sec := m.nodes[i].section
	if sec.rawTurns == nil {
		sec.rawTurns = map[string]string{}
	}
	sec.rawTurns[role] += chunk
	rendered := m.renderMarkdown(role, sec.rawTurns[role])
	m.replaceSpecialistLines(role, slideID, rendered)
}

// gutterWidth is the visual column count taken by the emoji gutter
// at the left of every specialist block. Two cells for the emoji
// (most modern terminals render them at width 2) plus one space.
const gutterWidth = 3

// renderMarkdown takes a specialist's running turn text and returns
// a glamour-rendered version with the role's emoji prepended as a
// gutter on the first line and matching whitespace on continuation
// lines. Sized to the current viewport width minus the gutter.
//
// Glamour can choke on streaming-incomplete markdown (open code
// fences, half-emitted lists). On render error we fall back to the
// raw text behind the same gutter so nothing is lost — the next
// chunk usually closes the malformed pattern and the live render
// recovers.
func (m *tuiModel) renderMarkdown(role, text string) string {
	wrap := max(m.width-gutterWidth-1, 20) // gutter + small right margin, with sane floor
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		return formatWithGutter(role, text)
	}
	out, err := r.Render(text)
	if err != nil {
		return formatWithGutter(role, text)
	}
	return formatWithGutter(role, strings.TrimRight(out, "\n"))
}

// formatWithGutter prepends the specialist's emoji + a space to the
// first line of `text` and aligns subsequent lines under the same
// content column with `gutterWidth` worth of leading spaces. The
// result is a single multi-line string suitable for storing in a
// sectionLine.text and emitting verbatim from renderNodes.
func formatWithGutter(role, text string) string {
	emoji, ok := specialistEmoji[role]
	if !ok {
		emoji = "·"
	}
	indent := strings.Repeat(" ", gutterWidth)
	var b strings.Builder
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == 0 {
			b.WriteString(emoji)
			b.WriteByte(' ')
		} else {
			b.WriteString(indent)
		}
		b.WriteString(line)
	}
	return b.String()
}

// replaceSpecialistLines drops every sectionLine in the slide's
// section that's tagged with the given role and appends a single
// rendered block in their place. Called on SpecialistTurnDone so
// the raw streaming chunks for a turn get swapped for one
// glamour-rendered markdown block. If the slide isn't found the
// rendered text is dropped on the floor — the streaming chunks
// stay visible in their loose-block fallback, which is the right
// degraded behaviour.
func (m *tuiModel) replaceSpecialistLines(role, slideID, rendered string) {
	i, ok := m.slideIdx[slideID]
	if !ok {
		return
	}
	sec := m.nodes[i].section
	kept := sec.lines[:0]
	for _, l := range sec.lines {
		if l.role != role {
			kept = append(kept, l)
		}
	}
	sec.lines = append(kept, sectionLine{role: role, text: rendered})
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
		m.viewport.SetContent(m.renderNodes())
		m.viewport.GotoBottom()

	case tuiLineMsg:
		m.appendLoose(msg.line)
		if m.ready {
			m.viewport.SetContent(m.renderNodes())
			m.viewport.GotoBottom()
		}

	case tuiAttributedLineMsg:
		m.appendChunkAndRender(msg.role, msg.slideID, msg.chunk)
		if m.ready {
			m.viewport.SetContent(m.renderNodes())
			m.viewport.GotoBottom()
		}

	case tuiSpecialistTurnDoneMsg:
		// Override the running buffer with the canonical full text
		// (in case the streaming render ended on a transient odd
		// state) and re-render once more.
		if i, ok := m.slideIdx[msg.slideID]; ok {
			sec := m.nodes[i].section
			if sec.rawTurns == nil {
				sec.rawTurns = map[string]string{}
			}
			sec.rawTurns[msg.role] = msg.text
		}
		rendered := m.renderMarkdown(msg.role, msg.text)
		m.replaceSpecialistLines(msg.role, msg.slideID, rendered)
		if m.ready {
			m.viewport.SetContent(m.renderNodes())
			m.viewport.GotoBottom()
		}

	case tuiSlideArrivedMsg:
		m.framesRecv++
		m.lastSlide = msg.slideID
		m.lastFrameAt = msg.at
		m.openSection(msg.slideID, msg.header)
		if m.ready {
			m.viewport.SetContent(m.renderNodes())
			m.viewport.GotoBottom()
		}

	case tuiSpecialistStateMsg:
		st := m.specialists[msg.role]
		st.state = msg.state
		st.turns = msg.turns
		m.specialists[msg.role] = st

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

// specialistsStatusFragment renders the per-specialist icon strip
// for the status bar, e.g. " 🔍 💡 😶 📖 🛑". Booting specialists
// render dim, ready specialists render full-colour, stopped ones
// render dim with a turn count appended.
func (m tuiModel) specialistsStatusFragment() string {
	parts := make([]string, 0, len(specialistOrder))
	for _, role := range specialistOrder {
		st, present := m.specialists[role]
		if !present {
			// Specialist hasn't called sink.SpecialistReady yet (still
			// booting or filtered out via --specialists). Skip — an
			// absent icon is the cleanest "not yet there" signal.
			continue
		}
		emoji, ok := specialistEmoji[role]
		if !ok {
			emoji = "·"
		}
		switch st.state {
		case specialistReady:
			parts = append(parts, emoji)
		case specialistStopped:
			// Faint emoji + turn count so the operator can see how
			// many turns each specialist completed without scrolling
			// to the end-of-meeting summary.
			parts = append(parts, fmt.Sprintf("%s\u202f%d", emoji, st.turns))
		default: // booting (shouldn't see this since absence handles it)
			parts = append(parts, emoji)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ") + " ·"
}

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
		age = fmt.Sprintf("%ds", int(time.Since(m.lastFrameAt).Seconds()))
	}
	bar := fmt.Sprintf(
		" frames: %d · last: %s · age: %s ·%s meeting: %s",
		m.framesRecv, last, age, m.specialistsStatusFragment(), m.meetingID,
	)
	if w := m.width; w > 0 && lipgloss.Width(bar) < w {
		bar += strings.Repeat(" ", w-lipgloss.Width(bar))
	}
	return statusBarStyle.Render(bar) + "\n" + m.viewport.View()
}

// tuiSink is the StreamSink implementation that routes lines into a
// running bubbletea program. Specialist lines tied to a slide_id
// become tuiAttributedLineMsg so the model can park them under the
// matching section; lines without slide attribution become tuiLineMsg
// (loose blocks). OpenSection fires tuiSlideArrivedMsg which both
// updates the status bar and creates the section node. Specialist
// state changes (ready / stopped) become tuiSpecialistStateMsg so
// the status bar shows per-specialist icons rather than scrolling
// "[role] ready" / "[role] stopped (turns: N)" lines through the
// viewport — those are lifecycle signals, not analysis output.
type tuiSink struct {
	prog *tea.Program
}

func newTUISink(p *tea.Program) *tuiSink { return &tuiSink{prog: p} }

func (s *tuiSink) OpenSection(slideID, header string) {
	s.prog.Send(tuiSlideArrivedMsg{slideID: slideID, header: header, at: time.Now()})
}

func (s *tuiSink) SpecialistLine(role, slideID, text string) {
	if slideID == "" {
		// Lifecycle line (startup error etc.) — no slide to attribute
		// to, so format with the [role] tag and route as a loose
		// system line.
		s.prog.Send(tuiLineMsg{line: tag(role) + " " + text})
		return
	}
	// Attributed chunk: hand the raw text to the model, which
	// accumulates it in the section's per-role buffer and re-renders
	// the markdown each time. The emoji gutter is applied by the
	// rendered-output formatter, not here.
	s.prog.Send(tuiAttributedLineMsg{role: role, slideID: slideID, chunk: text})
}

func (s *tuiSink) SpecialistTurnDone(role, slideID, fullText string) {
	if slideID == "" || fullText == "" {
		return
	}
	s.prog.Send(tuiSpecialistTurnDoneMsg{role: role, slideID: slideID, text: fullText})
}

func (s *tuiSink) SpecialistReady(role string) {
	s.prog.Send(tuiSpecialistStateMsg{role: role, state: specialistReady})
}

func (s *tuiSink) SpecialistStopped(role string, turns int) {
	s.prog.Send(tuiSpecialistStateMsg{role: role, state: specialistStopped, turns: turns})
}

func (s *tuiSink) SystemLine(text string) {
	s.prog.Send(tuiLineMsg{line: text})
}

// startTUI launches a bubbletea program in a goroutine and returns:
//   - sink: the StreamSink that routes lines into the program;
//   - cleanup: a quit-and-wait function that's safe to call multiple
//     times (idempotent — the second call returns the moment the
//     first one's <-done observes the close);
//   - done: closed when the program exits via any path (q, Ctrl-C,
//     tuiQuitMsg, or program error). The caller watches it to drive
//     a process-wide shutdown when the user quits the TUI from the
//     keyboard — bubbletea catches Ctrl-C and exits its own loop,
//     but the rest of the process (the stdin decode loop in
//     particular) won't unblock unless we close stdin.
func startTUI(meetingID string) (StreamSink, func(), <-chan struct{}) {
	model := newTUIModel(meetingID)
	prog := tea.NewProgram(model, tea.WithAltScreen())

	done := make(chan struct{})
	go func() {
		_, _ = prog.Run()
		close(done)
	}()

	cleanup := func() {
		prog.Send(tuiQuitMsg{})
		<-done
	}
	return newTUISink(prog), cleanup, done
}
