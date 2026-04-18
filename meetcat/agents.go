// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — claudia session-mode specialist agents for meetcat.
//
// This file implements 🎯T19.2: spawning persistent claudia.Agent
// sessions and streaming slide events into them.
// 🎯T13: prompts loaded from meetcat/prompts/*.md; --specialists flag.
// 🎯T15: neutral session stays alive after StopAll; SessionIDs exported.
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

	"github.com/marcelocantos/claudia"
)

// Default system prompts (inline fallbacks when config files are absent).
// These strings must exactly match the content in meetcat/prompts/*.md so
// that adding the files has no observable behaviour change.
const defaultSkepticPrompt = `You are a skeptical meeting analyst. For each slide, surface: assumptions that aren't stated, numbers that need sources, claims that contradict prior slides, and questions the presenter should be asked. Be concise — 3-5 bullets max.`

const defaultConstructivePrompt = `You are a constructive meeting analyst. For each slide, suggest: additions that would strengthen the argument, connections to other initiatives the team should know about, and "yes-and" extensions. Be concise — 3-5 bullets max.`

const defaultNeutralPrompt = `You are a neutral meeting analyst. For each slide, provide: links to relevant prior decisions, paste-able URLs to authoritative sources mentioned, and factual context the audience might lack. Be concise — 3-5 bullets max.`

const defaultDejargoniserPrompt = `You are an acronym and jargon tracker. For each slide, identify abbreviations and jargon. If you know the expansion, state it. If unknown, say "unknown — first seen on this slide". Accumulate a running glossary across slides.`

const defaultContradictionsPrompt = `You are a contradiction detector for meeting presentations. Your role is to identify factual conflicts across slides within this meeting and against prior meetings indexed in mnemo.

For each slide you receive:

1. Extract all factual claims: numbers, percentages, dates, deadlines, assignees, decisions, statuses, and key assertions.

2. Compare these claims against every prior slide you have seen in this session. Flag any numeric divergence (different values for the same metric), reversed decisions, changed assignees, or negated assertions.

3. Use the mnemo_search tool to find prior meeting transcripts that discuss the same topics, metrics, or decisions mentioned on this slide. Compare the current claims against those results. If you find a contradiction (different numbers, reversed decisions, changed assignees), flag it clearly.

When a contradiction is found, emit exactly this format:

⚠ CONTRADICTION: [current claim] vs [prior claim from meeting X on date Y]
Source: [artifact path or mnemo reference]

When no contradiction is found for a slide, respond with a single line: ✓ No contradictions detected.

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

// specialistState holds a running specialist agent and its metrics.
type specialistState struct {
	name      string
	model     string
	sessionID string
	agent     *claudia.Agent
	turnCount int
	mu        sync.Mutex // guards turnCount
}

// SessionPool manages the set of specialist agents for one meeting.
type SessionPool struct {
	meetingID string
	workDir   string
	logger    *Logger
	stderr    io.Writer

	// allowedNames is the optional filter set from --specialists.
	// nil means all specialists are allowed.
	allowedNames map[string]bool

	mu          sync.Mutex
	specialists map[string]*specialistState // guarded by mu; keyed by name
}

// MeetingSessionID constructs a meeting-unique session ID from the
// current time. Format: meetcat-<unix-ms>.
func MeetingSessionID() string {
	return fmt.Sprintf("meetcat-%d", time.Now().UnixMilli())
}

// specialistSessionID derives a per-specialist session ID from the
// meeting ID. Format: meetcat-<unix-ms>-<specialist-name>.
func specialistSessionID(meetingID, name string) string {
	return fmt.Sprintf("%s-%s", meetingID, name)
}

// NewSessionPool creates a pool but does not start any agents yet.
// workDir is the working directory passed to claudia.Start.
// allowedNames, if non-nil, restricts which specialists are started.
func NewSessionPool(meetingID, workDir string, stderr io.Writer, logger *Logger, allowedNames map[string]bool) *SessionPool {
	return &SessionPool{
		meetingID:    meetingID,
		workDir:      workDir,
		logger:       logger,
		stderr:       stderr,
		allowedNames: allowedNames,
		specialists:  make(map[string]*specialistState),
	}
}

// StartAll spawns all specialist agents (filtered by allowedNames) in
// parallel. Returns when all have either started or failed. Errors from
// individual agents are printed to stderr but do not abort others.
func (p *SessionPool) StartAll(ctx context.Context) {
	specs := filterSpecialists(allSpecialists(), p.allowedNames)
	var wg sync.WaitGroup
	for _, spec := range specs {
		spec := spec
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.startSpecialist(ctx, spec)
		}()
	}
	wg.Wait()
}

// startSpecialist spawns one specialist agent and wires up its event
// stream to stderr.
func (p *SessionPool) startSpecialist(ctx context.Context, spec specialistDef) {
	sessID := specialistSessionID(p.meetingID, spec.name)

	agent, err := claudia.Start(claudia.Config{
		WorkDir:   p.workDir,
		Model:     spec.model,
		SessionID: sessID,
	})
	if err != nil {
		fmt.Fprintf(p.stderr, "[%s] start error: %v\n", spec.name, err)
		p.logger.LogSpecialistError(spec.name, "start_failed")
		return
	}

	st := &specialistState{
		name:      spec.name,
		model:     spec.model,
		sessionID: sessID,
		agent:     agent,
	}

	// Wire event handler to stream assistant tokens to stderr, tagged.
	// 🎯T19.4: wrap bare URLs with OSC 8 hyperlinks in text mode output.
	agent.OnEvent(func(ev claudia.Event) {
		if ev.Type == "assistant" && ev.Text != "" {
			fmt.Fprintf(p.stderr, "[%s] %s\n", spec.name, wrapURLs(ev.Text))
		}
	})

	p.logger.LogSpecialistStart(spec.name, spec.model, SessionIDHash(sessID))
	fmt.Fprintf(p.stderr, "[%s] session started (%s)\n", spec.name, sessID)

	// Send the system prompt as the first message.
	if err := agent.Send(spec.prompt); err != nil {
		fmt.Fprintf(p.stderr, "[%s] prompt send error: %v\n", spec.name, err)
		p.logger.LogSpecialistError(spec.name, "prompt_send_failed")
		agent.Stop()
		return
	}

	// Wait for the initial response (the agent acknowledging its role).
	if _, err := agent.WaitForResponse(ctx); err != nil {
		fmt.Fprintf(p.stderr, "[%s] initial response error: %v\n", spec.name, err)
		p.logger.LogSpecialistError(spec.name, "initial_response_failed")
		agent.Stop()
		return
	}

	p.mu.Lock()
	p.specialists[spec.name] = st
	p.mu.Unlock()
}

// SendSlide injects a standardised slide message into all running
// agents in parallel and waits for all responses.
func (p *SessionPool) SendSlide(ctx context.Context, ev *slideEvent) {
	msg := renderSlideMessage(ev)

	p.mu.Lock()
	snapshot := make(map[string]*specialistState, len(p.specialists))
	for k, v := range p.specialists {
		snapshot[k] = v
	}
	p.mu.Unlock()

	var wg sync.WaitGroup
	for name, st := range snapshot {
		name, st := name, st
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.sendToSpecialist(ctx, name, st, msg)
		}()
	}
	wg.Wait()
}

// sendToSpecialist sends msg to one specialist, waits for the
// response, and records the turn in the logger.
func (p *SessionPool) sendToSpecialist(ctx context.Context, name string, st *specialistState, msg string) {
	start := time.Now()

	if err := st.agent.Send(msg); err != nil {
		fmt.Fprintf(p.stderr, "[%s] send error: %v\n", name, err)
		p.logger.LogSpecialistError(name, "send_failed")
		return
	}

	if _, err := st.agent.WaitForResponse(ctx); err != nil {
		fmt.Fprintf(p.stderr, "[%s] response error: %v\n", name, err)
		p.logger.LogSpecialistError(name, "response_failed")
		return
	}

	durationMs := time.Since(start).Milliseconds()

	st.mu.Lock()
	st.turnCount++
	turnIdx := st.turnCount
	st.mu.Unlock()

	// Session mode doesn't provide token counts or cost. Pass zeros.
	p.logger.LogSpecialistTurn(name, turnIdx, durationMs, 0, 0, 0)
}

// StopAll cleanly shuts down all running agents except the neutral
// session, which is kept alive in tmux for post-meeting use (🎯T15).
// The neutral agent is detached from the pool but its tmux session
// persists. Turn counts are printed to stderr for all agents.
func (p *SessionPool) StopAll() {
	p.mu.Lock()
	snapshot := make(map[string]*specialistState, len(p.specialists))
	for k, v := range p.specialists {
		snapshot[k] = v
	}
	p.mu.Unlock()

	var wg sync.WaitGroup
	for name, st := range snapshot {
		name, st := name, st
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.mu.Lock()
			turns := st.turnCount
			st.mu.Unlock()

			if name == "neutral" {
				// 🎯T15: keep the neutral session alive in tmux.
				fmt.Fprintf(p.stderr, "[%s] kept alive (turns: %d)\n", name, turns)
			} else {
				st.agent.Stop()
				fmt.Fprintf(p.stderr, "[%s] stopped (turns: %d)\n", name, turns)
			}
		}()
	}
	wg.Wait()
}

// NeutralAgent returns the neutral specialist's agent, if started.
// Returns nil when the neutral specialist is not in the pool (e.g.
// filtered out via --specialists).
func (p *SessionPool) NeutralAgent() *claudia.Agent {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.specialists["neutral"]; ok {
		return st.agent
	}
	return nil
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
func renderSlideMessage(ev *slideEvent) string {
	msg := fmt.Sprintf("[slide %s @ %d]\nPath: %s", ev.SlideID, ev.TStartMs, ev.Path)
	if len(ev.OCR) > 0 && string(ev.OCR) != "null" {
		msg += fmt.Sprintf("\nOCR: %s", string(ev.OCR))
	}
	if len(ev.TranscriptWindow) > 0 && string(ev.TranscriptWindow) != "null" {
		msg += fmt.Sprintf("\nTranscript: %s", string(ev.TranscriptWindow))
	}
	if ev.FrontmostApp != "" {
		msg += fmt.Sprintf("\nApp: %s", ev.FrontmostApp)
	}
	return msg
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
