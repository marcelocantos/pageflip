// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// meetcat — consumer of pageflip's slide-event NDJSON stream.
//
// This is the 🎯T19.1 walking skeleton extended with:
//   - 🎯T21: doctor subcommand for diagnostic output
//   - 🎯T21: --log-file for structured NDJSON session logging
//   - 🎯T19.2: claudia session-mode specialist agents
//   - 🎯T19.3: bubbletea TUI with auto-sizing panes per specialist agent
//
// Subsequent sub-targets (T19.4 OSC 8 hyperlinks) build on this shell.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/marcelocantos/claudia"
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
  family's primary artifact.

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
  --agents            Spawn claudia specialist sessions for each slide.
  --work-dir <path>   Working directory passed to claudia agents.

INPUT CONTRACT
  Stdin is NDJSON: one JSON object per newline, matching
  docs/slide-event-schema.json in the pageflip repo. Required fields per
  event: slide_id (string), path (string), t_start_ms (uint), t_end_ms
  (uint). Optional: ocr (array), transcript_window (array),
  frontmost_app (string).

OUTPUT CONTRACT
  TTY + --agents: bubbletea TUI with one pane per specialist.
  Otherwise: one line per validated event on stderr, plus a terminal summary.

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
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "doctor":
			doctorFlags := flag.NewFlagSet("doctor", flag.ExitOnError)
			logFile := doctorFlags.String("log-file", "", "Path to NDJSON session log to tail.")
			if err := doctorFlags.Parse(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "meetcat doctor:", err)
				os.Exit(2)
			}
			runDoctor(os.Stdout, *logFile)
			return
		case "attach":
			attachFlags := flag.NewFlagSet("attach", flag.ExitOnError)
			meetingID := attachFlags.String("meeting", "", "Meeting session ID (meetcat-<timestamp>).")
			if err := attachFlags.Parse(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "meetcat attach:", err)
				os.Exit(2)
			}
			args := attachFlags.Args()
			if len(args) != 1 {
				fmt.Fprintln(os.Stderr, "usage: meetcat attach [--meeting <id>] <specialist>")
				os.Exit(2)
			}
			specialist := args[0]
			if *meetingID == "" {
				fmt.Fprintln(os.Stderr, "meetcat attach: --meeting <id> is required")
				os.Exit(2)
			}
			sessID := specialistSessionID(*meetingID, specialist)
			fmt.Printf("# Attach to specialist %q (session: %s)\n", specialist, sessID)
			fmt.Printf("# Start meetcat, then run:\n")
			fmt.Printf("tmux -L claudia attach-session -t %s\n", sessID)
			return
		}
	}

	showVersion := flag.Bool("version", false, "Print version and exit.")
	showHelp := flag.Bool("help", false, "Print help and exit.")
	showHelpAgent := flag.Bool("help-agent", false, "Print machine-readable help and exit.")
	logFile := flag.String("log-file", "", "Path to write NDJSON session log (append, created if absent).")
	enableAgents := flag.Bool("agents", false, "Spawn claudia specialist sessions for each slide.")
	workDir := flag.String("work-dir", ".", "Working directory passed to claudia agents.")
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

	var pool *SessionPool
	if *enableAgents {
		meetingID := MeetingSessionID()
		pool = NewSessionPool(meetingID, *workDir, os.Stderr, logger)
	}

	if err := run(context.Background(), os.Stdin, os.Stderr, logger, pool); err != nil {
		fmt.Fprintln(os.Stderr, "meetcat:", err)
		os.Exit(1)
	}
}

// isTTY reports whether f is connected to an interactive terminal.
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	_, ok := fi.Sys().(*syscall.Stat_t)
	return ok
}

// run processes the slide-event stream from in, writing a summary to
// summary. pool, if non-nil, receives each validated slide event for
// specialist analysis. logger may be nil (no-op).
//
// When in is a TTY and pool is non-nil, run launches the bubbletea TUI.
// Otherwise it falls back to line-by-line stderr mode for CI / pipe use.
func run(ctx context.Context, in io.Reader, summary io.Writer, logger *Logger, pool *SessionPool) error {
	useTUI := false
	if pool != nil {
		if f, ok := in.(*os.File); ok {
			useTUI = isTTY(f)
		}
	}
	if useTUI {
		return runTUI(ctx, in, summary, logger, pool)
	}
	return runText(ctx, in, summary, logger, pool)
}

// runText is the original line-by-line mode. Used when not on a TTY,
// when agents are disabled, or in tests.
func runText(ctx context.Context, in io.Reader, summary io.Writer, logger *Logger, pool *SessionPool) error {
	reader := bufio.NewReader(in)
	dec := json.NewDecoder(reader)
	count := 0
	firstEvent := true

	if pool != nil {
		defer func() {
			pool.StopAll()
			ts := pool.TurnSummary()
			fmt.Fprintln(summary, "meetcat: specialist turn counts:")
			for name, n := range ts {
				fmt.Fprintf(summary, "  %s: %d\n", name, n)
			}
		}()
	}

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

		// On first slide, spawn agents (if pool configured).
		if pool != nil && firstEvent {
			firstEvent = false
			pool.StartAll(ctx)
		}

		front := ev.FrontmostApp
		if front == "" {
			front = "-"
		}
		fmt.Fprintf(
			summary,
			"[%d] %s (t=%dms, dur=%dms, app=%s) %s\n",
			count, ev.SlideID, ev.TStartMs, ev.TEndMs-ev.TStartMs, front, ev.Path,
		)

		// Send slide to all specialist agents in parallel.
		if pool != nil {
			pool.SendSlide(ctx, &ev)
		}
	}
}

// runTUI starts the bubbletea TUI. It feeds slide events and specialist
// token events into the model and returns when the meeting ends or the
// user presses q.
func runTUI(ctx context.Context, in io.Reader, summary io.Writer, logger *Logger, pool *SessionPool) error {
	specs := allSpecialists()
	m := newTUIModel(pool.meetingID, specs)

	// msgCh carries tea.Msg values from background goroutines into the
	// bubbletea program. Closed by the slide-decoder goroutine on exit.
	msgCh := make(chan tea.Msg, 256)

	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(os.Stdin))

	// Relay messages from msgCh into bubbletea.
	go func() {
		for msg := range msgCh {
			prog.Send(msg)
		}
	}()

	// Slide-decoder goroutine: reads NDJSON, validates, feeds TUI and pool.
	go func() {
		dec := json.NewDecoder(bufio.NewReader(in))
		count := 0
		firstEvent := true

		defer func() {
			logger.LogMeetingEnd(count, 0)
			pool.StopAll()
			close(msgCh)
			prog.Send(tuiStopMsg{})
		}()

		for {
			var ev slideEvent
			switch err := dec.Decode(&ev); {
			case errors.Is(err, io.EOF):
				return
			case err != nil:
				logger.LogRejected("decode_error")
				return
			}

			logger.LogReceived(ev.SlideID, "")
			if err := ev.validate(); err != nil {
				logger.LogRejected("validation_failed")
				return
			}
			logger.LogValidated(ev.SlideID)
			count++

			evCopy := ev
			msgCh <- tuiSlideMsg{ev: &evCopy}

			if firstEvent {
				firstEvent = false
				// Start agents and wire token hooks to feed the TUI.
				// pool.StartAll installs stderr-based OnEvent handlers;
				// we immediately replace them with TUI-forwarding handlers.
				pool.StartAll(ctx)
				wireTokenHooks(pool, msgCh)
			}
			pool.SendSlide(ctx, &evCopy)
		}
	}()

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}

	// Print text summary after TUI exits.
	ts := pool.TurnSummary()
	fmt.Fprintln(summary, "meetcat: specialist turn counts:")
	for name, n := range ts {
		fmt.Fprintf(summary, "  %s: %d\n", name, n)
	}
	return nil
}

// wireTokenHooks replaces each specialist agent's OnEvent handler with
// one that forwards token text and turn-done signals into msgCh. Must be
// called after pool.StartAll — agents must exist before OnEvent is set.
// OnEvent replaces the previous handler (one at a time), which is
// intentional: the TUI channel supersedes the stderr fallback.
func wireTokenHooks(pool *SessionPool, msgCh chan<- tea.Msg) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for name, st := range pool.specialists {
		name, st := name, st
		st.agent.OnEvent(func(ev claudia.Event) {
			if ev.Type == "assistant" && ev.Text != "" {
				msgCh <- tuiTokenMsg{name: name, text: ev.Text}
			}
			if ev.IsTerminalStop() {
				msgCh <- tuiTurnDoneMsg{name: name}
			}
		})
	}
}
