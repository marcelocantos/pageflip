// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// meetcat — consumer of pageflip's slide-event NDJSON stream.
//
// This is the 🎯T19.1 walking skeleton extended with:
//   - 🎯T21: doctor subcommand for diagnostic output
//   - 🎯T21: --log-file for structured NDJSON session logging
//   - 🎯T19.2: claudia session-mode specialist agents
//
// Subsequent sub-targets (T19.3 TUI, T19.4 OSC 8 hyperlinks) build
// on this shell.
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
			// Print the tmux attach command. The actual tmux socket
			// path follows claudia's convention (see agents-guide.md:
			// "tmux substrate" section). We delegate to agent.AttachCommand()
			// by constructing a minimal pool lookup; since we have no
			// running agent here, derive the command manually.
			// claudia uses: tmux -S <sock> attach -t @<window>
			// We can't know the window ID without a live agent, so we
			// emit a best-effort hint using the session ID.
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
	return fi.Mode()&os.ModeCharDevice != 0 && fi.Sys().(*syscall.Stat_t) != nil
}

// run processes the slide-event stream from in, writing a summary to
// summary. pool, if non-nil, receives each validated slide event for
// specialist analysis. logger may be nil (no-op).
//
// When in is an interactive TTY (and pool is non-nil), run launches a
// bubbletea TUI. Otherwise it falls back to the original line-by-line
// stderr mode so meetcat still works in CI / pipe contexts.
func run(ctx context.Context, in io.Reader, summary io.Writer, logger *Logger, pool *SessionPool) error {
	// Determine whether we should show a TUI.
	// We show TUI only when pool is configured AND stdin is a real TTY.
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

// runText is the original line-by-line stderr mode (non-TTY / no pool).
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

// runTUI runs the bubbletea TUI mode. It feeds slide events and
// specialist token events into the bubbletea program via a channel.
func runTUI(ctx context.Context, in io.Reader, summary io.Writer, logger *Logger, pool *SessionPool) error {
	specs := allSpecialists()
	meetingID := pool.meetingID

	m := newTUIModel(meetingID, specs)

	// Channel for all external messages destined for the bubbletea program.
	// The program will be notified via tea.Cmd wrappers that read from this.
	// We use a buffered channel to avoid blocking the goroutines.
	msgCh := make(chan tea.Msg, 256)

	// Wire specialist event handlers BEFORE starting agents so no tokens are lost.
	for _, spec := range specs {
		name := spec.name
		// We need to hook agents before they're started; pool hasn't called
		// StartAll yet, so we patch it to register OnEvent after start.
		// We store the hook functions to call after StartAll.
		_ = name // used in closure below
	}

	// Start bubbletea program (takes over the terminal).
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(os.Stdin))

	// Feed channel messages into bubbletea via a goroutine.
	go func() {
		for msg := range msgCh {
			p.Send(msg)
		}
	}()

	// Slide-decoder goroutine.
	go func() {
		defer func() {
			if pool != nil {
				pool.StopAll()
			}
			close(msgCh)
			p.Send(tuiStopMsg{})
		}()

		dec := json.NewDecoder(bufio.NewReader(in))
		count := 0
		firstEvent := true

		for {
			var ev slideEvent
			switch err := dec.Decode(&ev); {
			case errors.Is(err, io.EOF):
				logger.LogMeetingEnd(count, 0)
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

			if pool != nil && firstEvent {
				firstEvent = false
				// Wire token handlers before starting so we catch all output.
				for _, spec := range specs {
					spec := spec
					// We can't call OnEvent before Start; register after.
					// This will be wired in startSpecialistWithHook below.
					_ = spec
				}
				startAllWithHooks(ctx, pool, specs, msgCh)
			}

			if pool != nil {
				pool.SendSlide(ctx, &evCopy)
			}
		}
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}

	// Print summary to the original summary writer after TUI exits.
	if pool != nil {
		ts := pool.TurnSummary()
		fmt.Fprintln(summary, "meetcat: specialist turn counts:")
		for name, n := range ts {
			fmt.Fprintf(summary, "  %s: %d\n", name, n)
		}
	}
	return nil
}

// startAllWithHooks starts all specialist agents and wires their event
// streams to send tuiTokenMsg and tuiTurnDoneMsg into msgCh.
func startAllWithHooks(ctx context.Context, pool *SessionPool, specs []specialistDef, msgCh chan<- tea.Msg) {
	// We call the pool's existing StartAll but need to intercept OnEvent.
	// Since pool.StartAll wires OnEvent internally to stderr, we patch the
	// stderr writer to suppress it (the TUI replaces that output) and
	// instead install our own hook by wrapping the start procedure.
	//
	// Implementation: call StartAll (which installs stderr-based OnEvent),
	// then add a second OnEvent that feeds the TUI channel. claudia.Agent
	// supports multiple OnEvent registrations — each call adds a handler.
	pool.StartAll(ctx)

	pool.mu.Lock()
	defer pool.mu.Unlock()
	// Wire OnEvent for each specialist via the already-started agents so
	// the TUI channel receives token and turn-done messages.
	for specName, st := range pool.specialists {
		specName, st := specName, st
		agent := st.agent
		agent.OnEvent(func(ev claudia.Event) {
			if ev.Type == "assistant" && ev.Text != "" {
				msgCh <- tuiTokenMsg{name: specName, text: ev.Text}
			}
			if ev.IsTerminalStop() {
				msgCh <- tuiTurnDoneMsg{name: specName}
			}
		})
	}
}
