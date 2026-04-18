// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — claudia session-mode specialist agents for meetcat.
//
// This file implements 🎯T19.2: spawning persistent claudia.Agent
// sessions and streaming slide events into them.
package main

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/marcelocantos/claudia"
)

// System prompts for each specialist. Short inline strings; will be
// moved to config files in 🎯T13.
const skepticPrompt = `You are a skeptical meeting analyst. For each slide, surface: assumptions that aren't stated, numbers that need sources, claims that contradict prior slides, and questions the presenter should be asked. Be concise — 3-5 bullets max.`

const constructivePrompt = `You are a constructive meeting analyst. For each slide, suggest: additions that would strengthen the argument, connections to other initiatives the team should know about, and "yes-and" extensions. Be concise — 3-5 bullets max.`

const neutralPrompt = `You are a neutral meeting analyst. For each slide, provide: links to relevant prior decisions, paste-able URLs to authoritative sources mentioned, and factual context the audience might lack. Be concise — 3-5 bullets max.`

const dejargoniserPrompt = `You are an acronym and jargon tracker. For each slide, identify abbreviations and jargon. If you know the expansion, state it. If unknown, say "unknown — first seen on this slide". Accumulate a running glossary across slides.`

// specialistDef describes one specialist agent configuration.
type specialistDef struct {
	name   string
	model  string
	prompt string
}

// allSpecialists returns the canonical list of specialist definitions.
func allSpecialists() []specialistDef {
	return []specialistDef{
		{"skeptic", "sonnet", skepticPrompt},
		{"constructive", "sonnet", constructivePrompt},
		{"neutral", "sonnet", neutralPrompt},
		{"dejargoniser", "haiku", dejargoniserPrompt},
	}
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
func NewSessionPool(meetingID, workDir string, stderr io.Writer, logger *Logger) *SessionPool {
	return &SessionPool{
		meetingID:   meetingID,
		workDir:     workDir,
		logger:      logger,
		stderr:      stderr,
		specialists: make(map[string]*specialistState),
	}
}

// StartAll spawns all specialist agents in parallel. Returns when all
// have either started or failed. Errors from individual agents are
// printed to stderr but do not abort others.
func (p *SessionPool) StartAll(ctx context.Context) {
	specs := allSpecialists()
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
	agent.OnEvent(func(ev claudia.Event) {
		if ev.Type == "assistant" && ev.Text != "" {
			fmt.Fprintf(p.stderr, "[%s] %s\n", spec.name, ev.Text)
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

// StopAll cleanly shuts down all running agents, prints per-specialist
// turn counts to stderr.
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
			st.agent.Stop()
			st.mu.Lock()
			turns := st.turnCount
			st.mu.Unlock()
			fmt.Fprintf(p.stderr, "[%s] stopped (turns: %d)\n", name, turns)
		}()
	}
	wg.Wait()
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
