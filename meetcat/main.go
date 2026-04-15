// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// meetcat — consumer of pageflip's slide-event NDJSON stream.
//
// This is the 🎯T19.1 walking skeleton: meetcat reads slide events from
// stdin, validates required fields, and prints a short summary per event
// to stderr. Subsequent sub-targets (T19.2 session-mode claudia agents,
// T19.3 TUI, T19.4 OSC 8 hyperlinks) build on this shell.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

const version = "0.0.1"

const helpAgent = `meetcat --help-agent
====================

PURPOSE
  Consume pageflip's slide-event NDJSON stream and drive parallel Claude
  specialist sessions (dejargoniser, resolver, skeptic, constructive,
  neutral, contradictions) for each slide. This binary is the 🎯T19.x
  family's primary artifact. Current status: walking skeleton (T19.1) —
  reads and validates the stream; no Claude agents wired yet.

INVOCATION
  pageflip --events-out stdout | meetcat
  meetcat < events.jsonl
  meetcat --help
  meetcat --help-agent
  meetcat --version

INPUT CONTRACT
  Stdin is NDJSON: one JSON object per newline, matching
  docs/slide-event-schema.json in the pageflip repo. Required fields per
  event: slide_id (string), path (string), t_start_ms (uint), t_end_ms
  (uint). Optional: ocr (array), transcript_window (array),
  frontmost_app (string).

OUTPUT CONTRACT
  stderr: one line per validated event, plus a terminal summary.
  stdout: reserved for future machine-readable output (glossary,
          decisions, action items) in 🎯T19.3.

EXIT CODES
  0  Clean exit at EOF with all events validated.
  1  Malformed JSON or missing required fields.
  2  CLI argument parse error (flag package default).

TARGETS
  See pageflip's bullseye.yaml for the T19.x family.
`

// slideEvent mirrors the shape pageflip emits under --events-out. Fields
// marked ",omitempty" are optional per T18's schema — absent until later
// capabilities (OCR, transcript, frontmost-app) land.
type slideEvent struct {
	SlideID          string          `json:"slide_id"`
	Path             string          `json:"path"`
	TStartMs         uint64          `json:"t_start_ms"`
	TEndMs           uint64          `json:"t_end_ms"`
	OCR              json.RawMessage `json:"ocr,omitempty"`
	TranscriptWindow json.RawMessage `json:"transcript_window,omitempty"`
	FrontmostApp     string          `json:"frontmost_app,omitempty"`
}

func (e *slideEvent) validate() error {
	if e.SlideID == "" {
		return errors.New("missing slide_id")
	}
	if e.Path == "" {
		return errors.New("missing path")
	}
	if e.TEndMs < e.TStartMs {
		return fmt.Errorf("t_end_ms (%d) < t_start_ms (%d)", e.TEndMs, e.TStartMs)
	}
	return nil
}

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit.")
	showHelp := flag.Bool("help", false, "Print help and exit.")
	showHelpAgent := flag.Bool("help-agent", false, "Print machine-readable help and exit.")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println("meetcat", version)
		return
	case *showHelpAgent:
		fmt.Print(helpAgent)
		return
	case *showHelp:
		flag.Usage()
		return
	}

	if err := run(os.Stdin, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "meetcat:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, summary io.Writer) error {
	reader := bufio.NewReader(in)
	dec := json.NewDecoder(reader)
	count := 0
	for {
		var ev slideEvent
		switch err := dec.Decode(&ev); {
		case errors.Is(err, io.EOF):
			fmt.Fprintf(summary, "meetcat: processed %d slide event(s)\n", count)
			return nil
		case err != nil:
			return fmt.Errorf("decode event %d: %w", count+1, err)
		}
		if err := ev.validate(); err != nil {
			return fmt.Errorf("event %d: %w", count+1, err)
		}
		count++
		front := ev.FrontmostApp
		if front == "" {
			front = "-"
		}
		fmt.Fprintf(
			summary,
			"[%d] %s (t=%dms, dur=%dms, app=%s) %s\n",
			count, ev.SlideID, ev.TStartMs, ev.TEndMs-ev.TStartMs, front, ev.Path,
		)
	}
}
