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
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"
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
  meetcat glossary refresh --confluence-url https://company.atlassian.net/wiki
  meetcat --help
  meetcat --help-agent
  meetcat --version

SUBCOMMANDS
  doctor            Print a markdown diagnostic report (versions, tools, auth, log).
  glossary refresh  Scrape Confluence for glossary entries and populate the cache.

FLAGS
  --log-file <path>       Write a structured NDJSON session log to <path>.
                          Events contain no meeting content — only identifiers,
                          token counts, costs, and categorical codes.
  --agents                Spawn claudia specialist sessions for each slide.
  --work-dir <path>       Working directory passed to claudia agents.
  --glossary-cache <path> Path to glossary JSON cache (default: ~/.pageflip/glossary.json).

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
	PHash            string          `json:"phash,omitempty"`
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

// writeSessionIDsIfPossible persists session IDs to the artifact folder
// when the pool has a workDir configured. Errors are printed to w but
// do not abort the shutdown sequence (🎯T15).
func writeSessionIDsIfPossible(pool *SessionPool, w io.Writer) {
	ids := pool.SessionIDs()
	if len(ids) == 0 {
		return
	}
	// Use workDir as the artifact output root when no explicit config is set.
	aw := NewArtifactWriter(ArtifactConfig{
		OutputDir: pool.workDir,
		MeetingID: pool.meetingID,
	})
	if err := aw.WriteSessionIDs(ids); err != nil {
		fmt.Fprintf(w, "meetcat: write session-ids: %v\n", err)
	}
}

// printResumeHint tells the operator the one command they need to
// pick up where the meeting left off. Every specialist's Claude Code
// session is persisted by JSONL, so resume reconstructs all five
// from a single meeting ID.
func printResumeHint(pool *SessionPool, w io.Writer) {
	if pool == nil {
		return
	}
	fmt.Fprintf(w, "\nmeetcat: meeting saved. To resume:\n")
	fmt.Fprintf(w, "  meetcat resume %s\n", pool.meetingID)
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
		case "glossary":
			runGlossarySubcommand(os.Args[2:])
			return
		case "attach":
			// Legacy attach printed a `tmux ... attach` command that
			// only worked while neutral was kept alive. With every
			// specialist torn down at meeting end (and resume taking
			// over via JSONL persistence), tmux attach is no longer
			// the right primitive. Print a clear redirect.
			fmt.Fprintln(os.Stderr, "meetcat attach is deprecated — use `meetcat resume <meeting-id>`")
			fmt.Fprintln(os.Stderr, "(All specialists' sessions persist via Claude Code JSONL; resume re-spawns them with their accumulated context.)")
			os.Exit(2)
		case "resume":
			runResume(os.Args[2:])
			return
		}
	}

	showVersion := flag.Bool("version", false, "Print version and exit.")
	showHelp := flag.Bool("help", false, "Print help and exit.")
	showHelpAgent := flag.Bool("help-agent", false, "Print machine-readable help and exit.")
	logFile := flag.String("log-file", "", "Path to write NDJSON session log (append, created if absent).")
	noAgents := flag.Bool("no-agents", false, "Disable claudia specialist sessions (decode-only mode).")
	workDir := flag.String("work-dir", ".", "Working directory passed to claudia agents.")
	specialistsFlag := flag.String("specialists", "", "Comma-separated list of specialists to start (default: all). E.g. skeptic,neutral")
	glossaryCachePath := flag.String("glossary-cache", defaultGlossaryCachePath(), "Path to glossary JSON cache.")
	noSpawn := flag.Bool("no-spawn", false, "Don't spawn pageflip; read slide events from stdin instead.")
	regionFlag := flag.String("region", "", "Forwarded to pageflip as --region X,Y,W,H. Omit to run the multi-monitor picker.")
	windowFlag := flag.Bool("window", false, "Forwarded to pageflip as --window (interactive window picker).")
	windowTitleFlag := flag.String("window-title", "", "Forwarded to pageflip as --window-title SUBSTRING.")
	windowIDFlag := flag.String("window-id", "", "Forwarded to pageflip as --window-id ID.")
	flag.Parse()
	enableAgents := !*noAgents

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

	cfg := meetingConfig{
		MeetingID:     MeetingSessionID(),
		WorkDir:       *workDir,
		GlossaryCache: *glossaryCachePath,
		AllowedNames:  ParseSpecialistNames(*specialistsFlag),
		EnableAgents:  enableAgents,
		NoSpawn:       *noSpawn,
		Pageflip: PageflipArgs{
			Region:      *regionFlag,
			Window:      *windowFlag,
			WindowTitle: *windowTitleFlag,
			WindowID:    *windowIDFlag,
		},
		Logger:      logger,
		OpenBrowser: true,
	}

	if err := runMeeting(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "meetcat:", err)
		os.Exit(1)
	}
}

// meetingConfig is the resolved configuration for one meeting run.
// Both the fresh-meeting path (main) and the resume path (runResume)
// build it and hand it to runMeeting, which owns the TUI / pageflip /
// pool lifecycle.
type meetingConfig struct {
	MeetingID     string
	WorkDir       string
	GlossaryCache string
	AllowedNames  map[string]bool
	EnableAgents  bool
	NoSpawn       bool
	Pageflip      PageflipArgs
	Logger        *Logger

	// OpenBrowser controls whether runMeeting auto-opens the user's
	// default browser at the meetcat web URL. Fresh meetings set it
	// true; resume sets it false because the operator's existing
	// browser tab is still open from the prior run and will auto-
	// reconnect via EventSource — opening a new tab would compete
	// with that one for focus.
	OpenBrowser bool
}

// runMeeting wires up the TUI, spawns pageflip (or reads stdin),
// boots the specialist pool, drives the slide-event decode loop, and
// shuts everything down cleanly. The same code path serves a fresh
// meeting and a resumed one — only the meeting ID + per-specialist
// JSONL persistence make resume "do the right thing" via claudia's
// auto-detect.
func runMeeting(cfg meetingConfig) error {
	// Load glossary cache (best-effort; errors are noted but don't abort).
	var glossary *GlossaryCache
	if cfg.GlossaryCache != "" {
		g, err := NewGlossaryCache(cfg.GlossaryCache)
		if err != nil {
			fmt.Fprintf(os.Stderr, "meetcat: glossary cache: %v (continuing without)\n", err)
		} else {
			glossary = g
		}
	}

	// Web mode: when agents are enabled and stderr is a TTY, meetcat
	// serves a local HTTP/SSE page and opens it in the browser. The
	// page renders specialist output as proper markdown and embeds
	// each slide's PNG inline next to the analysis — neither of
	// which the terminal could do well.
	var sink StreamSink = newStderrSink(os.Stderr)
	var webShutdown func()
	if cfg.EnableAgents && stderrIsTTY() {
		absWork, err := filepath.Abs(cfg.WorkDir)
		if err != nil {
			return fmt.Errorf("resolve work dir: %w", err)
		}
		h := newHub()
		url, shutdown, err := startWebServer(cfg.MeetingID, absWork, h)
		if err != nil {
			return fmt.Errorf("start web server: %w", err)
		}
		sink = newWebSink(h, absWork)
		webShutdown = shutdown
		fmt.Fprintf(os.Stderr, "meetcat: open %s in your browser (Ctrl-C to stop)\n", url)
		if cfg.OpenBrowser {
			_ = openInBrowser(url)
		}
	}

	// Persist the manifest as early as possible so an immediate
	// Ctrl-C still leaves enough state on disk for `meetcat resume`
	// to pick up. The manifest dir is also where session-ids.json
	// will land at meeting end.
	manifest := Manifest{
		SchemaVersion: currentManifestSchema,
		MeetingID:     cfg.MeetingID,
		CreatedAt:     time.Now().UTC(),
		WorkDir:       cfg.WorkDir,
		GlossaryCache: cfg.GlossaryCache,
		Specialists:   sortedAllowedNames(cfg.AllowedNames),
		Pageflip:      cfg.Pageflip,
	}
	if err := WriteManifest(cfg.WorkDir, manifest); err != nil {
		// Manifest write failure is loud but not fatal — the meeting
		// can still proceed; resume just won't be available later.
		fmt.Fprintf(os.Stderr, "meetcat: manifest write failed: %v (resume will not be available)\n", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Slide-event source: pipe pageflip's stdout through unless the
	// caller has explicitly opted out (--no-spawn) or stdin is
	// already a pipe (CI / tests).
	var eventStream io.Reader = os.Stdin
	var pageflipCleanup func()
	if !cfg.NoSpawn && stdinIsTTY() {
		stream, cleanup, err := spawnPageflip(ctx, sink, cfg.Pageflip.toFlags())
		if err != nil {
			return err
		}
		eventStream = stream
		pageflipCleanup = cleanup
	}

	// Terminal Ctrl-C propagates to a process shutdown: cancel the
	// context (SIGTERMs pageflip via cmd.Cancel so its stdout closes
	// and our decoder hits EOF) and close stdin as a fallback for
	// the --no-spawn case. The web tab will see the SSE connection
	// drop and stop receiving events.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
			_ = os.Stdin.Close()
		case <-ctx.Done():
		}
	}()

	var pool *SessionPool
	if cfg.EnableAgents {
		pool = NewSessionPool(cfg.MeetingID, cfg.WorkDir, sink, cfg.Logger, cfg.AllowedNames, glossary)
	}

	runErr := run(ctx, eventStream, sink, cfg.Logger, pool)

	// Shutdown: kill pageflip, then close the web server (stops
	// SSE), then the post-meeting writes (session IDs, resume hint)
	// land on the real stderr.
	if pageflipCleanup != nil {
		pageflipCleanup()
	}
	if webShutdown != nil {
		webShutdown()
	}
	if pool != nil {
		writeSessionIDsIfPossible(pool, os.Stderr)
		printResumeHint(pool, os.Stderr)
	}
	return runErr
}

// sortedAllowedNames returns the map keys in canonical order so the
// manifest's `specialists` field is stable across runs (otherwise
// the JSON diff churns on Go's map-iteration order). Returns nil
// when the allow set is empty (semantics: "all specialists").
func sortedAllowedNames(allow map[string]bool) []string {
	if len(allow) == 0 {
		return nil
	}
	out := make([]string, 0, len(allow))
	for name := range allow {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// runResume loads a manifest and re-runs the meeting with the same
// configuration. The per-specialist Claude Code sessions auto-resume
// because their session IDs are deterministic from (meeting_id,
// role) and the underlying JSONLs persist across runs.
func runResume(args []string) {
	resumeFlags := flag.NewFlagSet("resume", flag.ExitOnError)
	if err := resumeFlags.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "meetcat resume:", err)
		os.Exit(2)
	}
	rest := resumeFlags.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: meetcat resume <meeting-id-or-dir>")
		os.Exit(2)
	}
	target := rest[0]

	// Accept both "meetcat-1234" (a bare ID, looked up under cwd) and
	// "path/to/meetcat-1234" (an explicit directory). Either way,
	// resolve the manifest under that directory.
	manifestDir := target
	if _, err := os.Stat(manifestDir); err != nil {
		// Fall back to interpreting target as a bare ID under cwd.
		manifestDir = filepath.Join(".", target)
	}
	manifest, err := ReadManifest(manifestDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "meetcat resume: %v\n", err)
		os.Exit(1)
	}

	// Logger is not currently persisted in the manifest — a future
	// extension could carry the original --log-file path. Resume
	// runs without a session log unless the caller pipes one in via
	// the (still-supported) -log-file on the resume invocation.
	cfg := meetingConfig{
		MeetingID:     manifest.MeetingID,
		WorkDir:       manifest.WorkDir,
		GlossaryCache: manifest.GlossaryCache,
		AllowedNames:  namesToSet(manifest.Specialists),
		EnableAgents:  true, // resume is meaningless without agents
		NoSpawn:       false,
		Pageflip:      manifest.Pageflip,
	}

	fmt.Fprintf(os.Stderr, "meetcat: resuming meeting %s (work_dir=%s)\n", cfg.MeetingID, cfg.WorkDir)
	if err := runMeeting(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "meetcat resume:", err)
		os.Exit(1)
	}
}

func namesToSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// stdinIsTTY reports whether stdin is connected to an interactive
// terminal (not a pipe or redirected file). Used to decide whether
// meetcat should spawn pageflip itself or consume the existing pipe.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// run processes the slide-event stream from in, routing every line
// of output through the supplied sink. pool, if non-nil, receives
// each validated slide event for specialist analysis. logger may be
// nil (no-op). The caller owns TUI / pageflip lifecycle — run is
// straight-line decode + dispatch.
func run(ctx context.Context, in io.Reader, sink StreamSink, logger *Logger, pool *SessionPool) error {
	return runText(ctx, in, sink, logger, pool)
}

// stderrIsTTY reports whether stderr is connected to an interactive
// terminal. Used to decide whether to launch the bubbletea TUI; piped
// stderr (CI, `meetcat 2>log`, tests) falls back to the streaming
// path so output stays grep- and tee-friendly.
func stderrIsTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// runText reads slide events from in and routes every line of output
// through sink. Lifecycle of any TUI / spawned pageflip is owned by
// the caller; runText only knows about the event stream and the
// specialist pool.
func runText(ctx context.Context, in io.Reader, sink StreamSink, logger *Logger, pool *SessionPool) error {
	reader := bufio.NewReader(in)
	dec := json.NewDecoder(reader)
	count := 0
	// Threshold 5 mirrors pageflip's default inter-slide dedup distance:
	// anything within 5 bits of a previously-seen pHash is treated as the
	// same slide returning, not a new one.
	revisits := newRevisitTracker(5)
	phashWarned := false

	if pool != nil {
		// Start all specialist agents immediately, in parallel, in a
		// goroutine — do not wait for the first slide. This gives users
		// a visible stream of "[skeptic] ready" etc. lines as soon as
		// meetcat launches, instead of a silent wait.
		go pool.StartAll(ctx)

		defer func() {
			pool.StopAll()
			ts := pool.TurnSummary()
			sink.SystemLine(fmt.Sprintf("%s specialist turn counts:",
				colorize(colorSystem, "meetcat:")))
			for name, n := range ts {
				sink.SystemLine(fmt.Sprintf("  %s %d", tag(name), n))
			}
		}()
	}

	for {
		var ev slideEvent
		switch err := dec.Decode(&ev); {
		case errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed):
			// os.ErrClosed is the path the bubbletea watcher takes
			// when the user quits the TUI: stdin is closed out from
			// under us, the decoder's Read returns ErrClosed, we
			// treat it as a clean EOF so the deferred StopAll runs.
			logger.LogMeetingEnd(count, 0)
			sink.SystemLine(fmt.Sprintf("%s processed %d slide event(s)",
				colorize(colorSystem, "meetcat:"), count))
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
		if ev.PHash == "" && !phashWarned {
			sink.SystemLine(fmt.Sprintf("%s slide event has empty phash — revisit detection disabled for unstamped events. Is pageflip up to date?",
				colorize(colorSystem, "meetcat:")))
			phashWarned = true
		}
		revisit, firstIdx := revisits.classify(ev.PHash)
		if revisit {
			sink.SystemLine(fmt.Sprintf(
				"%s [%d] %s (t=%dms, dur=%dms, app=%s) %s %s",
				colorize(colorDim, "↺"),
				count, ev.SlideID, ev.TStartMs, ev.TEndMs-ev.TStartMs, front, ev.Path,
				colorize(colorDim, fmt.Sprintf("← slide %d", firstIdx+1)),
			))
			continue
		}
		// OpenSection feeds both the web view (which uses imagePath
		// to compute a clean /slides/* URL) and the stderr fallback
		// (which just prints the header). imagePath is the absolute
		// path to the captured PNG; the web sink rebases it under
		// the meeting work_dir to build a serveable URL.
		sink.OpenSection(ev.SlideID, slideSectionHeader(
			count, ev.SlideID, front, ev.Path,
			ev.TStartMs, ev.TEndMs-ev.TStartMs,
		), ev.Path)

		// Send slide to all specialist agents in parallel. SendSlide
		// returns immediately after queueing per 🎯T23.
		if pool != nil {
			pool.SendSlide(ctx, &ev)
		}
	}
}

func defaultGlossaryCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "glossary.json"
	}
	return filepath.Join(home, ".pageflip", "glossary.json")
}

// runGlossarySubcommand dispatches the "glossary <verb>" subcommand family.
// Currently only "glossary refresh" is implemented.
func runGlossarySubcommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: meetcat glossary <refresh>")
		os.Exit(2)
	}
	switch args[0] {
	case "refresh":
		runGlossaryRefresh(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "meetcat glossary: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// runGlossaryRefresh implements "meetcat glossary refresh".
// It scrapes Confluence for glossary/acronym pages and writes results to
// the local cache. Pass --dry-run to preview without writing.
func runGlossaryRefresh(args []string) {
	fs := flag.NewFlagSet("glossary refresh", flag.ExitOnError)
	confluenceURL := fs.String("confluence-url", "", "Confluence base URL (e.g. https://company.atlassian.net/wiki).")
	cachePath := fs.String("cache-path", defaultGlossaryCachePath(), "Path to glossary JSON cache.")
	workDir := fs.String("work-dir", ".", "Working directory passed to claudia agents.")
	dryRun := fs.Bool("dry-run", false, "Show what would be added without writing to the cache.")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "meetcat glossary refresh:", err)
		os.Exit(2)
	}

	if *confluenceURL == "" {
		fmt.Fprintln(os.Stderr, "meetcat glossary refresh: --confluence-url is required")
		os.Exit(2)
	}

	cache, err := NewGlossaryCache(*cachePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "meetcat glossary refresh:", err)
		os.Exit(1)
	}

	before := cache.Len()
	fmt.Fprintf(os.Stdout, "meetcat: glossary refresh from %s (cache has %d entries)\n", *confluenceURL, before)
	if *dryRun {
		fmt.Fprintln(os.Stdout, "meetcat: --dry-run: no changes will be written")
	}

	added, err := RefreshFromConfluence(context.Background(), *confluenceURL, cache, *workDir, *dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "meetcat glossary refresh:", err)
		// Don't exit — partial results may have been written.
	}

	if *dryRun {
		fmt.Fprintf(os.Stdout, "meetcat: would add %d new entries\n", added)
	} else {
		fmt.Fprintf(os.Stdout, "meetcat: added %d new entries (cache now has %d)\n", added, cache.Len())
	}
}
