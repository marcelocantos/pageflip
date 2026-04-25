// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — claudia session-mode specialist agents for meetcat.
//
// This file implements 🎯T19.2: spawning persistent claudia.Agent
// sessions and streaming slide events into them.
// 🎯T13: prompts loaded from meetcat/prompts/*.md; --specialists flag.
// 🎯T15: neutral session stays alive after StopAll; SessionIDs exported.
// 🎯T23: per-specialist worker goroutine drains a buffered channel
// serially, so SendSlide returns the moment messages are queued.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/marcelocantos/claudia"
)

// Default system prompts (inline fallbacks when config files are absent).
// These strings must exactly match the content in meetcat/prompts/*.md so
// that adding the files has no observable behaviour change.
const noPreambleRule = "\n\nOutput only your analysis of the slide. Never acknowledge your role, explain what you are about to do, ask for input, or emit any preamble, greeting, or sign-off. Your very first token in every response must be substantive analysis."

const defaultSkepticPrompt = `You are a skeptical meeting analyst. For each slide, surface: assumptions that aren't stated, numbers that need sources, claims that contradict prior slides, and questions the presenter should be asked. Be concise — 3-5 bullets max.` + noPreambleRule

const defaultConstructivePrompt = `You are a constructive meeting analyst. For each slide, suggest: additions that would strengthen the argument, connections to other initiatives the team should know about, and "yes-and" extensions. Be concise — 3-5 bullets max.` + noPreambleRule

const defaultNeutralPrompt = `You are a neutral meeting analyst. For each slide, provide: links to relevant prior decisions, paste-able URLs to authoritative sources mentioned, and factual context the audience might lack. Be concise — 3-5 bullets max.` + noPreambleRule

const defaultDejargoniserPrompt = `You are an acronym and jargon tracker for a technically sophisticated audience of experienced software engineers, researchers, and founders. Assume everyone already knows mainstream tech vocabulary: general CS/software terms (repo, branch, PR, CI, API, SDK, ORM, CLI, GUI, OSS, MVP, PoC, stdlib, etc.), common infrastructure (Docker, Kubernetes, AWS/GCP/Azure, S3, SQL, Redis, Kafka, etc.), mainstream AI/ML (LLM, RAG, embedding, transformer, agent, agentic, prompt, token, fine-tune, RLHF, MoE, diffusion, etc.), and well-known products (GitHub, VS Code, ChatGPT, Claude, Copilot, etc.). Do NOT define these. Do NOT define plain English words that happen to appear as nouns.

Flag a term only when it is genuinely non-obvious to this audience: a company-internal code name, a domain-specific acronym from a niche field (biotech, law, finance microstructure, telco, etc.), a narrow research term unlikely to be recognised outside a specialist subfield, or an unusual coinage whose meaning would not be guessable. When in doubt, stay silent — a false miss is cheaper than noise.

For each qualifying term, emit one line: ` + "`TERM — expansion/definition (source or \"unknown — first seen on this slide\")`" + `. Accumulate a running glossary across slides; never re-emit a term already defined earlier in this session.

If a slide contains no qualifying terms, respond with absolutely nothing. Silence is the correct output for most slides.

Never acknowledge your role, explain what you are about to do, ask for input, or emit any preamble, greeting, or sign-off. Your very first token in every response must be either a glossary entry or nothing at all.`

const defaultContradictionsPrompt = `You are a contradiction detector for meeting presentations. Your role is to identify factual conflicts across slides within this meeting and against prior meetings indexed in mnemo.

For each slide you receive:

1. Extract all factual claims: numbers, percentages, dates, deadlines, assignees, decisions, statuses, and key assertions.

2. Compare these claims against every prior slide you have seen in this session. Flag any numeric divergence (different values for the same metric), reversed decisions, changed assignees, or negated assertions.

3. Use the mnemo_search tool to find prior meeting transcripts that discuss the same topics, metrics, or decisions mentioned on this slide. Compare the current claims against those results. If you find a contradiction (different numbers, reversed decisions, changed assignees), flag it clearly.

When a contradiction is found, emit exactly this format:

⚠ CONTRADICTION: [current claim] vs [prior claim from meeting X on date Y]
Source: [artifact path or mnemo reference]

When no contradiction is found for a slide, respond with absolutely nothing — not even an acknowledgement line. Silence is the signal that nothing is wrong. Only emit output when a real contradiction is found.

Never acknowledge your role, explain what you are about to do, ask for input, or emit any preamble, greeting, or sign-off. Your very first token in every response must be either the ⚠ CONTRADICTION line or nothing at all.

Calibration: aim for precision over recall. A long meeting might surface 1–3 genuine contradictions. Do not flag rephrasing, rounding, or estimates-vs-actuals unless the difference is material. Do not flag the same contradiction twice.`

// promptsDir returns the directory that contains meetcat/prompts/ relative
// to the current source file at compile time. Falls back to "" (CWD) when
// the path cannot be determined (e.g. stripped binaries).
func promptsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "prompts")
}

// loadPrompt reads a prompt from {promptsDir}/{name}.md. Falls back to
// the provided default string if the file is absent or unreadable.
func loadPrompt(name, defaultPrompt string) string {
	dir := promptsDir()
	if dir == "" {
		return defaultPrompt
	}
	path := filepath.Join(dir, name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		// File absent or unreadable — use embedded default.
		return defaultPrompt
	}
	// Trim trailing newline added by editors; preserve the inline style.
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return defaultPrompt
	}
	return s
}

// specialistDef describes one specialist agent configuration.
type specialistDef struct {
	name   string
	model  string
	prompt string
}

// allSpecialists returns the canonical list of specialist definitions,
// loading prompts from config files when available.
func allSpecialists() []specialistDef {
	return []specialistDef{
		{"skeptic", "sonnet", loadPrompt("skeptic", defaultSkepticPrompt)},
		{"constructive", "sonnet", loadPrompt("constructive", defaultConstructivePrompt)},
		{"neutral", "sonnet", loadPrompt("neutral", defaultNeutralPrompt)},
		{"dejargoniser", "haiku", loadPrompt("dejargoniser", defaultDejargoniserPrompt)},
		{"contradictions", "opus", loadPrompt("contradictions", defaultContradictionsPrompt)},
	}
}

// filterSpecialists returns only the definitions whose names appear in
// the allow set. If allow is nil or empty, all definitions are returned.
func filterSpecialists(defs []specialistDef, allow map[string]bool) []specialistDef {
	if len(allow) == 0 {
		return defs
	}
	out := make([]specialistDef, 0, len(allow))
	for _, d := range defs {
		if allow[d.name] {
			out = append(out, d)
		}
	}
	return out
}

// ParseSpecialistNames splits a comma-separated list of specialist names
// into a set. Names are trimmed and lowercased. Returns nil on empty input.
func ParseSpecialistNames(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(strings.ToLower(part))
		if name != "" {
			set[name] = true
		}
	}
	return set
}

// slideQueueSize is the per-specialist message queue capacity. It is
// pragmatically unbounded for human-paced meetings (a 1-hour session
// at 1–2 minutes per slide produces 30–60 events). If SendSlide ever
// blocks pushing here, the meeting has either out-paced any plausible
// LLM throughput or one of the workers has stalled — investigate
// rather than raise the limit.
const slideQueueSize = 1024

// slideJob is one queued slide to dispatch to one specialist. The
// slideID travels alongside the rendered body so the agent's OnEvent
// handler can label streaming output with the slide it belongs to —
// even if a slow specialist finishes earlier slides long after the
// next slide's header has been printed.
type slideJob struct {
	slideID string
	body    string
}

// specialistState holds a running specialist agent and its metrics.
type specialistState struct {
	name      string
	model     string
	sessionID string
	agent     *claudia.Agent
	turnCount int
	mu        sync.Mutex // guards turnCount, currentSlideID, turnBuffer

	// currentSlideID is the slide the worker is presently waiting on
	// a response for. Set before agent.Send, cleared after
	// WaitForResponse returns. Read by the OnEvent callback to label
	// streaming token output with the right slide_id.
	currentSlideID string

	// turnBuffer accumulates the streaming chunks for the current
	// turn. After WaitForResponse returns, processSlide emits the
	// concatenated text via sink.SpecialistTurnDone so the TUI can
	// render it as markdown. Reset before each new turn.
	turnBuffer strings.Builder

	// 🎯T23: per-specialist work queue. SendSlide pushes one job per
	// slide; runSpecialistWorker drains it serially, calling Send +
	// WaitForResponse for each. This decouples the slide-event loop
	// from per-specialist latency so meetcat keeps reading pageflip's
	// NDJSON stream regardless of how long the previous slide is
	// still being analysed. Closed by StopAll to signal drain-and-
	// exit; the worker's for-range terminates once the channel is
	// empty.
	queue chan slideJob
}

// StreamSink is the destination for streaming output emitted by the
// SessionPool and runText. The plain (stderrSink) implementation
// writes lines to stderr; the TUI implementation routes lines to a
// bubbletea program that groups specialist output under each slide's
// section header so out-of-order specialist completion stays visually
// grouped per frame.
type StreamSink interface {
	// OpenSection signals that a new slide event has arrived. The
	// sink may use slide_id to group later SpecialistLine calls that
	// name this same slide_id under the section header.
	OpenSection(slideID, header string)

	// SpecialistLine emits one line of specialist output. slideID
	// names the slide the specialist is currently processing, or ""
	// for lifecycle messages (startup errors, etc.). The text comes
	// in as the agent streams tokens — possibly mid-sentence — so
	// implementations should treat each call as a stream chunk
	// rather than a fully-formed line of analysis.
	SpecialistLine(role, slideID, text string)

	// SpecialistTurnDone signals that the specialist has finished
	// streaming a complete response to the slide. fullText is the
	// concatenation of every chunk seen via SpecialistLine for this
	// turn. The TUI sink uses this to replace the raw streaming text
	// with a glamour-rendered markdown block; the stderrSink ignores
	// it since the chunks have already been written to the stream.
	SpecialistTurnDone(role, slideID, fullText string)

	// SpecialistReady signals that a specialist has finished booting
	// and is now accepting slides. The TUI sink uses this to flip
	// the per-specialist icon in the status bar from booting to
	// active; the stderr sink just prints "[role] ready".
	SpecialistReady(role string)

	// SpecialistStopped signals that a specialist has shut down
	// after processing `turns` slides. For neutral, the underlying
	// tmux session is kept alive but the specialist no longer
	// accepts work — the TUI represents both as "stopped".
	SpecialistStopped(role string, turns int)

	// SystemLine emits one line that's not attributed to a specialist
	// (revisits, EOF summary, phash warnings, end-of-meeting tallies).
	SystemLine(text string)
}

// stderrSink is the plain (non-TTY) StreamSink. It writes everything
// in chronological order to a single io.Writer, matching the
// behaviour the project had before the bubbletea TUI was
// re-introduced. Specialist tag is just "[role]"; the slide_id is
// not surfaced in the prefix because the chronological order of
// the stream already pairs each line with the section header above
// it (most of the time — out-of-order completion is the price of
// not running a TUI).
type stderrSink struct {
	w io.Writer
}

func newStderrSink(w io.Writer) *stderrSink { return &stderrSink{w: w} }

func (s *stderrSink) OpenSection(_, header string) {
	fmt.Fprintln(s.w, header)
}

func (s *stderrSink) SpecialistLine(role, _, text string) {
	fmt.Fprintf(s.w, "%s %s\n", tag(role), text)
}

func (s *stderrSink) SpecialistTurnDone(_, _, _ string) {
	// stderr already saw every chunk via SpecialistLine; nothing to
	// re-render. The TUI sink overrides this to swap the raw
	// streamed lines for a glamour-rendered markdown block.
}

func (s *stderrSink) SpecialistReady(role string) {
	fmt.Fprintf(s.w, "%s %s\n", tag(role), colorize(colorDim, "ready"))
}

func (s *stderrSink) SpecialistStopped(role string, turns int) {
	fmt.Fprintf(s.w, "%s %s\n", tag(role), colorize(colorDim, fmt.Sprintf("stopped (turns: %d)", turns)))
}

func (s *stderrSink) SystemLine(text string) {
	fmt.Fprintln(s.w, text)
}

// SessionPool manages the set of specialist agents for one meeting.
type SessionPool struct {
	meetingID string
	workDir   string
	logger    *Logger
	sink      StreamSink
	glossary  *GlossaryCache // optional; nil means no glossary lookups

	// allowedNames is the optional filter set from --specialists.
	// nil means all specialists are allowed.
	allowedNames map[string]bool

	mu          sync.Mutex
	specialists map[string]*specialistState // guarded by mu; keyed by name
	stopped     bool                        // guarded by mu; true after StopAll has closed queues

	// workersWG tracks the per-specialist worker goroutines so
	// StopAll can wait for queued slides to drain before returning.
	workersWG sync.WaitGroup
}

// MeetingSessionID constructs a meeting-unique session ID from the
// current time. Format: meetcat-<unix-ms>.
func MeetingSessionID() string {
	return fmt.Sprintf("meetcat-%d", time.Now().UnixMilli())
}

// meetcatNamespace is a stable UUID v4 used as the DNS-ish namespace
// for deriving deterministic per-specialist session IDs from the
// human-readable meeting ID + specialist name. Regenerating this
// value would invalidate every existing session ID, so don't.
var meetcatNamespace = uuid.MustParse("7e4c8f50-4a3e-4d9a-8c1b-1e6b5f2a9d00")

// specialistSessionID derives a deterministic UUID per
// (meetingID, specialist) pair so Claude Code's --session-id
// validation is satisfied and the same inputs always yield the
// same session (necessary for `meetcat attach` to find the right
// tmux window after a restart).
func specialistSessionID(meetingID, name string) string {
	return uuid.NewSHA1(meetcatNamespace, fmt.Appendf(nil, "%s|%s", meetingID, name)).String()
}

// NewSessionPool creates a pool but does not start any agents yet.
// workDir is the working directory passed to claudia.Start.
// sink receives every streamed line; pass `newStderrSink(os.Stderr)`
// for the plain mode and a `*tuiSink` for the bubbletea TUI.
// allowedNames, if non-nil, restricts which specialists are started.
// glossary may be nil; when provided, slide messages include glossary preambles.
func NewSessionPool(meetingID, workDir string, sink StreamSink, logger *Logger, allowedNames map[string]bool, glossary *GlossaryCache) *SessionPool {
	return &SessionPool{
		meetingID:    meetingID,
		workDir:      workDir,
		logger:       logger,
		sink:         sink,
		glossary:     glossary,
		allowedNames: allowedNames,
		specialists:  make(map[string]*specialistState),
	}
}

// StartAll spawns all specialist agents (filtered by allowedNames)
// concurrently, but serialises their auth/ready phase through a
// 1-slot semaphore. Parallel startup saturates Claude Code's local
// state (auth, config reads, renderer boot) and pushes per-agent
// WaitReady past its 30s budget; gating that one phase keeps
// readiness fast while letting Send + streaming responses run in
// parallel afterwards. Errors on one specialist do not abort the
// others.
func (p *SessionPool) StartAll(ctx context.Context) {
	specs := filterSpecialists(allSpecialists(), p.allowedNames)
	authSem := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for _, spec := range specs {
		wg.Add(1)
		go func(s specialistDef) {
			defer wg.Done()
			p.startSpecialist(ctx, s, authSem)
		}(spec)
	}
	wg.Wait()
}

// startSpecialist spawns one specialist agent and wires up its event
// stream to stderr.
func (p *SessionPool) startSpecialist(ctx context.Context, spec specialistDef, authSem chan struct{}) {
	// Serialise the auth/ready phase. Only one specialist is booting
	// claude at a time; everything after WaitReady runs concurrently.
	select {
	case authSem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	released := false
	release := func() {
		if !released {
			released = true
			<-authSem
		}
	}
	defer release()

	sessID := specialistSessionID(p.meetingID, spec.name)

	agent, err := claudia.Start(claudia.Config{
		WorkDir:   p.workDir,
		Model:     spec.model,
		SessionID: sessID,
		// Replace the default Claude Code system prompt with the
		// specialist's own. Using --append-system-prompt instead
		// leaves the "you are Claude Code" baseline dominant, which
		// is why agents would respond "what would you like me to do?"
		// — they were treating slide messages as ambiguous chat
		// rather than analysis input. --system-prompt drops the
		// general-coding-assistant framing so the role text takes.
		// MCP tools and per-agent settings load normally; only the
		// default prompt is overridden.
		ExtraArgs: []string{"--system-prompt", spec.prompt},
	})
	if err != nil {
		p.sink.SpecialistLine(spec.name, "", colorize(colorError, fmt.Sprintf("start error: %v", err)))
		p.logger.LogSpecialistError(spec.name, "start_failed")
		return
	}

	st := &specialistState{
		name:      spec.name,
		model:     spec.model,
		sessionID: sessID,
		agent:     agent,
	}

	// Wire event handler to stream assistant tokens through the sink.
	// The slide_id of the slide currently being processed is passed
	// alongside so the TUI sink can group tokens under the right
	// section header even when the response arrives long after a
	// newer slide has scrolled past. 🎯T19.4: wrap bare URLs with
	// OSC 8 hyperlinks.
	agent.OnEvent(func(ev claudia.Event) {
		if ev.Type == "assistant" && ev.Text != "" {
			st.mu.Lock()
			slideID := st.currentSlideID
			st.turnBuffer.WriteString(ev.Text)
			st.mu.Unlock()
			p.sink.SpecialistLine(spec.name, slideID, wrapURLs(ev.Text))
		}
	})

	p.logger.LogSpecialistStart(spec.name, spec.model, SessionIDHash(sessID))

	// Wait for claude to finish booting. This is the contended phase
	// — the semaphore ensures only one specialist is doing it at a time.
	if err := agent.WaitReady(ctx); err != nil {
		p.sink.SpecialistLine(spec.name, "", colorize(colorError, fmt.Sprintf("auth/ready error: %v", err)))
		p.logger.LogSpecialistError(spec.name, "ready_failed")
		agent.Stop()
		return
	}
	p.sink.SpecialistReady(spec.name)

	// Release the semaphore so the next specialist can begin booting
	// while this one registers and awaits slide events. The role is
	// already loaded via --append-system-prompt, so there's no
	// role-acknowledgement round-trip to perform here.
	release()

	st.queue = make(chan slideJob, slideQueueSize)

	p.mu.Lock()
	if p.stopped {
		// StopAll ran while we were booting; abandon this specialist.
		p.mu.Unlock()
		agent.Stop()
		return
	}
	p.specialists[spec.name] = st
	p.workersWG.Add(1)
	p.mu.Unlock()

	go p.runSpecialistWorker(ctx, st)
}

// runSpecialistWorker drains the specialist's queue serially. Exits
// when the queue is closed (by StopAll) and fully drained.
func (p *SessionPool) runSpecialistWorker(ctx context.Context, st *specialistState) {
	defer p.workersWG.Done()
	for msg := range st.queue {
		p.processSlide(ctx, st, msg)
	}
}

// processSlide sends one rendered slide message to one specialist
// and waits for the response. Per-specialist serialisation is
// guaranteed because only the worker goroutine ever calls this for
// a given st. The slide_id is parked on st.currentSlideID for the
// duration of the turn so the agent's OnEvent callback can label
// streamed tokens with the slide they belong to.
func (p *SessionPool) processSlide(ctx context.Context, st *specialistState, job slideJob) {
	start := time.Now()

	st.mu.Lock()
	st.currentSlideID = job.slideID
	st.turnBuffer.Reset()
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		st.currentSlideID = ""
		st.mu.Unlock()
	}()

	if err := st.agent.Send(job.body); err != nil {
		p.sink.SpecialistLine(st.name, job.slideID, colorize(colorError, fmt.Sprintf("send error: %v", err)))
		p.logger.LogSpecialistError(st.name, "send_failed")
		return
	}

	if _, err := st.agent.WaitForResponse(ctx); err != nil {
		p.sink.SpecialistLine(st.name, job.slideID, colorize(colorError, fmt.Sprintf("response error: %v", err)))
		p.logger.LogSpecialistError(st.name, "response_failed")
		return
	}

	durationMs := time.Since(start).Milliseconds()

	st.mu.Lock()
	st.turnCount++
	turnIdx := st.turnCount
	fullText := st.turnBuffer.String()
	st.turnBuffer.Reset()
	st.mu.Unlock()

	// Hand the complete turn text to the sink so the TUI can swap
	// the raw streamed chunks for a glamour-rendered markdown block.
	p.sink.SpecialistTurnDone(st.name, job.slideID, fullText)

	// Session mode doesn't provide token counts or cost. Pass zeros.
	p.logger.LogSpecialistTurn(st.name, turnIdx, durationMs, 0, 0, 0)
}

// SendSlide queues a standardised slide message for every running
// specialist and returns immediately. Each specialist's worker
// goroutine drains its own queue serially, so per-specialist
// response ordering is preserved without blocking the slide-event
// loop on any one specialist's latency (🎯T23).
//
// When a glossary cache is configured, known acronyms from the slide
// text are prepended as [Glossary: ...] lines. Unknown acronyms
// trigger a background claudia.Task (haiku) research pass — results
// are cached but not waited on (🎯T16).
func (p *SessionPool) SendSlide(ctx context.Context, ev *slideEvent) {
	msg := renderSlideMessage(ev, p.glossary)

	// Fire background research for unknown acronyms found in the slide.
	if p.glossary != nil {
		slideText := slideEventText(ev)
		for _, acronym := range ExtractAcronyms(slideText) {
			if p.glossary.Lookup(acronym) == nil {
				a := acronym
				go ResearchAcronym(ctx, a, p.glossary, p.workDir)
			}
		}
	}

	// Push under p.mu so we can't race a queue close in StopAll. The
	// channels are deeply buffered (slideQueueSize = 1024); pushing
	// is effectively non-blocking under any plausible meeting load,
	// so holding the pool lock for this loop is fine.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	job := slideJob{slideID: ev.SlideID, body: msg}
	for _, st := range p.specialists {
		st.queue <- job
	}
}

// StopAll cleanly shuts down every specialist agent. The Claude Code
// JSONL files persist regardless of whether the tmux window is torn
// down, so resuming a meeting with `meetcat resume <meeting-id>`
// works for all five specialists with no need to keep tmux windows
// hanging around per-meeting.
//
// 🎯T23: closes each specialist's queue, waits for its worker
// goroutine to finish processing in-flight slides, then stops the
// agent. The drain step is what guarantees the final slide's
// responses are not dropped.
func (p *SessionPool) StopAll() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	snapshot := make(map[string]*specialistState, len(p.specialists))
	for k, v := range p.specialists {
		snapshot[k] = v
		close(v.queue)
	}
	p.mu.Unlock()

	// Wait for every worker to drain its queue before stopping the
	// underlying agents — premature Stop() would race the worker's
	// final WaitForResponse and lose the last slide's output.
	p.workersWG.Wait()

	var wg sync.WaitGroup
	for name, st := range snapshot {
		wg.Add(1)
		go func(name string, st *specialistState) {
			defer wg.Done()
			st.mu.Lock()
			turns := st.turnCount
			st.mu.Unlock()

			// All specialists tear down their tmux windows uniformly
			// at meeting end. The session JSONL persists so resume is
			// equally available for every role.
			st.agent.Stop()
			p.sink.SpecialistStopped(name, turns)
		}(name, st)
	}
	wg.Wait()
}

// SessionIDs returns a map of specialist name → session ID for all
// started specialists. Used by 🎯T15 to write session-ids.json.
func (p *SessionPool) SessionIDs() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make(map[string]string, len(p.specialists))
	for name, st := range p.specialists {
		ids[name] = st.sessionID
	}
	return ids
}

// TurnSummary returns per-specialist turn counts for end-of-meeting
// reporting.
func (p *SessionPool) TurnSummary() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	summary := make(map[string]int, len(p.specialists))
	for name, st := range p.specialists {
		st.mu.Lock()
		summary[name] = st.turnCount
		st.mu.Unlock()
	}
	return summary
}

// renderSlideMessage formats a slideEvent into the standardised
// message injected into each specialist agent.
//
// When glossary is non-nil, any known acronyms found in the slide text are
// prepended as [Glossary: ACRONYM = Expansion (source)] lines so that the
// dejargoniser (and other specialists) can reference authoritative expansions
// without relying solely on their accumulated session memory (🎯T16).
func renderSlideMessage(ev *slideEvent, glossary *GlossaryCache) string {
	// Build the body first so we can extract acronyms from all fields.
	body := fmt.Sprintf("[slide %s @ %d]\nPath: %s", ev.SlideID, ev.TStartMs, ev.Path)
	if len(ev.OCR) > 0 && string(ev.OCR) != "null" {
		body += fmt.Sprintf("\nOCR: %s", string(ev.OCR))
	}
	if len(ev.TranscriptWindow) > 0 && string(ev.TranscriptWindow) != "null" {
		body += fmt.Sprintf("\nTranscript: %s", string(ev.TranscriptWindow))
	}
	if ev.FrontmostApp != "" {
		body += fmt.Sprintf("\nApp: %s", ev.FrontmostApp)
	}

	preamble := GlossaryPreamble(slideEventText(ev), glossary)
	if preamble == "" {
		return body
	}
	return preamble + body
}

// slideEventText returns a single string containing all human-readable text
// from a slide event, used for acronym extraction.
func slideEventText(ev *slideEvent) string {
	parts := []string{ev.Path, ev.FrontmostApp}
	if len(ev.OCR) > 0 && string(ev.OCR) != "null" {
		parts = append(parts, string(ev.OCR))
	}
	if len(ev.TranscriptWindow) > 0 && string(ev.TranscriptWindow) != "null" {
		parts = append(parts, string(ev.TranscriptWindow))
	}
	return strings.Join(parts, " ")
}

// AttachCommand returns the tmux command to attach to the named
// specialist's session. Returns an error message string if the
// specialist is unknown or not running.
func (p *SessionPool) AttachCommand(name string) string {
	p.mu.Lock()
	st, ok := p.specialists[name]
	p.mu.Unlock()
	if !ok {
		return fmt.Sprintf("specialist %q not found or not running", name)
	}
	return st.agent.AttachCommand()
}
