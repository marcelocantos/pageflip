// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — session log schema for meetcat.
//
// Design principle: sensitivity by construction. No event type carries
// free-form string fields that could contain meeting content, OCR text,
// transcript fragments, or specialist output. All string fields are
// categorical (enumerated reason/error codes) or opaque identifiers
// (bundle IDs, name labels, hashed session prefixes).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Logger writes NDJSON events to an io.Writer. A nil Logger is a no-op.
type Logger struct {
	w io.Writer
}

// NewFileLogger opens (or creates) path for append-only NDJSON logging.
// Caller is responsible for closing the returned *os.File when done.
func NewFileLogger(path string) (*Logger, *os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	return &Logger{w: f}, f, nil
}

func (l *Logger) emit(v any) {
	if l == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		// Should never happen with well-typed structs; log to stderr.
		fmt.Fprintf(os.Stderr, "meetcat: log marshal error: %v\n", err)
		return
	}
	_, _ = fmt.Fprintf(l.w, "%s\n", data)
}

func nowMs() int64 { return time.Now().UnixMilli() }

// ---------------------------------------------------------------------------
// Event types — each embeds eventBase and adds only non-sensitive fields.
// ---------------------------------------------------------------------------

// eventBase is embedded in every event. It provides the discriminator
// ("event") and a millisecond-resolution timestamp ("t_ms").
type eventBase struct {
	Event string `json:"event"`
	TMs   int64  `json:"t_ms"`
}

// EventReceived is emitted when a slide event arrives from pageflip.
// FrontmostAppBundle is the macOS bundle ID (e.g. "com.microsoft.teams2"),
// never the display name. Omitted when unknown.
type EventReceived struct {
	eventBase
	SlideID            string `json:"slide_id"`
	FrontmostAppBundle string `json:"frontmost_app_bundle,omitempty"`
}

// EventValidated is emitted when a received event passes validation.
type EventValidated struct {
	eventBase
	SlideID string `json:"slide_id"`
}

// EventRejected is emitted when a received event fails validation.
// Reason is a categorical code, never free-form text.
type EventRejected struct {
	eventBase
	ReasonCode string `json:"reason_code"`
}

// SpecialistStart is emitted when a claudia session begins for a specialist.
// SessionIDHash is the first 4 hex characters of SHA-256(session_id).
type SpecialistStart struct {
	eventBase
	Name          string `json:"name"`
	Model         string `json:"model"`
	SessionIDHash string `json:"session_id_hash"`
}

// SpecialistTurn is emitted after each turn in a specialist session.
// No content fields — only timing, token counts, and cost.
type SpecialistTurn struct {
	eventBase
	Name         string  `json:"name"`
	TurnIndex    int     `json:"turn_index"`
	DurationMs   int64   `json:"duration_ms"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// SpecialistError is emitted when a specialist session encounters an error.
// Code is a categorical error code, never free-form text.
type SpecialistError struct {
	eventBase
	Name string `json:"name"`
	Code string `json:"code"`
}

// MeetingEnd is emitted when meetcat processes all slides (or is signalled).
type MeetingEnd struct {
	eventBase
	SlidesProcessed int     `json:"slides_processed"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
}

// ---------------------------------------------------------------------------
// Typed emit helpers — callers use these rather than emit() directly.
// ---------------------------------------------------------------------------

func (l *Logger) LogReceived(slideID, bundleID string) {
	l.emit(EventReceived{
		eventBase:          eventBase{Event: "received", TMs: nowMs()},
		SlideID:            slideID,
		FrontmostAppBundle: bundleID,
	})
}

func (l *Logger) LogValidated(slideID string) {
	l.emit(EventValidated{
		eventBase: eventBase{Event: "validated", TMs: nowMs()},
		SlideID:   slideID,
	})
}

func (l *Logger) LogRejected(reasonCode string) {
	l.emit(EventRejected{
		eventBase:  eventBase{Event: "rejected", TMs: nowMs()},
		ReasonCode: reasonCode,
	})
}

func (l *Logger) LogSpecialistStart(name, model, sessionIDHash string) {
	l.emit(SpecialistStart{
		eventBase:     eventBase{Event: "specialist_start", TMs: nowMs()},
		Name:          name,
		Model:         model,
		SessionIDHash: sessionIDHash,
	})
}

func (l *Logger) LogSpecialistTurn(name string, turnIndex int, durationMs, inputTokens, outputTokens int64, costUSD float64) {
	l.emit(SpecialistTurn{
		eventBase:    eventBase{Event: "specialist_turn", TMs: nowMs()},
		Name:         name,
		TurnIndex:    turnIndex,
		DurationMs:   durationMs,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostUSD:      costUSD,
	})
}

func (l *Logger) LogSpecialistError(name, code string) {
	l.emit(SpecialistError{
		eventBase: eventBase{Event: "specialist_error", TMs: nowMs()},
		Name:      name,
		Code:      code,
	})
}

func (l *Logger) LogMeetingEnd(slidesProcessed int, totalCostUSD float64) {
	l.emit(MeetingEnd{
		eventBase:       eventBase{Event: "meeting_end", TMs: nowMs()},
		SlidesProcessed: slidesProcessed,
		TotalCostUSD:    totalCostUSD,
	})
}
