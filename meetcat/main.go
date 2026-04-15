// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// meetcat — consumer of pageflip's slide-event NDJSON stream.
//
// This is the 🎯T19.1 walking skeleton extended with:
//   - 🎯T21: doctor subcommand for diagnostic output
//   - 🎯T21: --log-file for structured NDJSON session logging
//
// Subsequent sub-targets (T19.2 session-mode claudia agents, T19.3 TUI,
// T19.4 OSC 8 hyperlinks) build on this shell.
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

// gitSHA is optionally set at link time via:
//
//	go build -ldflags "-X main.gitSHA=$(git rev-parse --short HEAD)"
//
// Falls back to "(dev)" when not provided.
var gitSHA = "(dev)"

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
  meetcat --log-file session.ndjson
  meetcat doctor [--log-file session.ndjson]
  meetcat --help
  meetcat --help-agent
  meetcat --version

SUBCOMMANDS
  doctor   Print a markdown diagnostic report (versions, tools, auth, log).

FLAGS
  --log-file <path>   Write a structured NDJSON session log to <path>.
                      Events contain no meeting content — only identifiers,
                      token counts, costs, and categorical codes.

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
  See pageflip's bullseye.yaml for the T19.x and T21 families.
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
	// Subcommand dispatch: check os.Args[1] before flag.Parse.
	if len(os.Args) >= 2 && os.Args[1] == "doctor" {
		doctorFlags := flag.NewFlagSet("doctor", flag.ExitOnError)
		logFile := doctorFlags.String("log-file", "", "Path to NDJSON session log to tail.")
		if err := doctorFlags.Parse(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "meetcat doctor:", err)
			os.Exit(2)
		}
		runDoctor(os.Stdout, *logFile)
		return
	}

	showVersion := flag.Bool("version", false, "Print version and exit.")
	showHelp := flag.Bool("help", false, "Print help and exit.")
	showHelpAgent := flag.Bool("help-agent", false, "Print machine-readable help and exit.")
	logFile := flag.String("log-file", "", "Path to write NDJSON session log (append, created if absent).")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Printf("meetcat %s (sha: %s)\n", version, gitSHA)
		return
	case *showHelpAgent:
		fmt.Print(helpAgent)
		return
	case *showHelp:
		flag.Usage()
		return
	}

	var logger *Logger
	if *logFile != "" {
		var f *os.File
		var err error
		logger, f, err = NewFileLogger(*logFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "meetcat:", err)
			os.Exit(1)
		}
		defer f.Close()
	}

	if err := run(os.Stdin, os.Stderr, logger); err != nil {
		fmt.Fprintln(os.Stderr, "meetcat:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, summary io.Writer, logger *Logger) error {
	reader := bufio.NewReader(in)
	dec := json.NewDecoder(reader)
	count := 0
	for {
		var ev slideEvent
		switch err := dec.Decode(&ev); {
		case errors.Is(err, io.EOF):
			logger.LogMeetingEnd(count, 0)
			fmt.Fprintf(summary, "meetcat: processed %d slide event(s)\n", count)
			return nil
		case err != nil:
			logger.LogRejected("decode_error")
			return fmt.Errorf("decode event %d: %w", count+1, err)
		}

		logger.LogReceived(ev.SlideID, "")

		if err := ev.validate(); err != nil {
			logger.LogRejected("validation_failed")
			return fmt.Errorf("event %d: %w", count+1, err)
		}

		logger.LogValidated(ev.SlideID)
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
