// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// runDoctor writes a markdown diagnostic report to w.
// logFile is the --log-file path; if empty the "Recent session log"
// section notes that no log file was configured.
func runDoctor(w io.Writer, logFile string) {
	fmt.Fprintln(w, "# meetcat doctor")
	fmt.Fprintln(w)

	writeVersions(w)
	writeExternalTools(w)
	writeAuthState(w)
	writeActiveSubprocesses(w)
	writeRecentLog(w, logFile)
}

// ---------------------------------------------------------------------------
// Sections
// ---------------------------------------------------------------------------

func writeVersions(w io.Writer) {
	fmt.Fprintln(w, "## Versions")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- **meetcat** %s (sha: %s)\n", version, gitSHA)
	fmt.Fprintf(w, "- **Go runtime** %s\n", runtime.Version())
	fmt.Fprintln(w)
}

func writeExternalTools(w io.Writer) {
	fmt.Fprintln(w, "## External tools")
	fmt.Fprintln(w)

	// tmux
	if p, err := exec.LookPath("tmux"); err == nil {
		ver := toolVersion("tmux", "-V")
		fmt.Fprintf(w, "- **tmux** ✓ `%s` — %s\n", p, ver)
	} else {
		fmt.Fprintln(w, "- **tmux** ✗ not found (required for session-mode agents)")
	}

	// claude CLI
	if p, err := exec.LookPath("claude"); err == nil {
		ver := toolVersion("claude", "--version")
		fmt.Fprintf(w, "- **claude CLI** ✓ `%s` — %s\n", p, ver)
	} else {
		fmt.Fprintln(w, "- **claude CLI** ✗ not found (required for claudia session agents)")
	}

	// claudia — Go module dep, not a subprocess
	fmt.Fprintln(w, "- **claudia** — Go module dependency (linked at build time, no PATH entry needed)")

	fmt.Fprintln(w)
}

func writeAuthState(w io.Writer) {
	fmt.Fprintln(w, "## Anthropic auth state")
	fmt.Fprintln(w)

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	credPath := filepath.Join(homeDir(), ".claude", ".credentials.json")
	_, credErr := os.Stat(credPath)

	switch {
	case apiKey != "":
		fmt.Fprintln(w, "- **ANTHROPIC_API_KEY** ✓ set in environment")
	case credErr == nil:
		fmt.Fprintf(w, "- **~/.claude/.credentials.json** ✓ present at `%s`\n", credPath)
	default:
		fmt.Fprintln(w, "- ✗ No API key found: set `$ANTHROPIC_API_KEY` or log in with `claude login`")
	}

	fmt.Fprintln(w)
}

func writeActiveSubprocesses(w io.Writer) {
	fmt.Fprintln(w, "## Active subprocesses")
	fmt.Fprintln(w)
	// Active agents are only available while meetcat is running with
	// --agents. The doctor subcommand runs standalone, so it cannot
	// enumerate live sessions here. Use `meetcat attach` to inspect
	// a running specialist session.
	fmt.Fprintln(w, "- (doctor cannot enumerate live agents; run `meetcat attach` while meetcat is active)")
	fmt.Fprintln(w)
}

func writeRecentLog(w io.Writer, logFile string) {
	fmt.Fprintln(w, "## Recent session log")
	fmt.Fprintln(w)

	if logFile == "" {
		fmt.Fprintln(w, "_No `--log-file` configured. Pass `--log-file <path>` to enable structured logging._")
		fmt.Fprintln(w)
		return
	}

	f, err := os.Open(logFile)
	if err != nil {
		fmt.Fprintf(w, "_Cannot open log file `%s`: %v_\n", logFile, err)
		fmt.Fprintln(w)
		return
	}
	defer f.Close()

	const tailLines = 20
	lines := tailFile(f, tailLines)
	if len(lines) == 0 {
		fmt.Fprintf(w, "_Log file `%s` is empty._\n", logFile)
		fmt.Fprintln(w)
		return
	}

	fmt.Fprintf(w, "_Last %d line(s) of `%s`:_\n\n", len(lines), logFile)
	fmt.Fprintln(w, "```ndjson")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// toolVersion runs cmd with args and returns the first line of output,
// trimmed. On error it returns "(version unknown)".
func toolVersion(cmd string, args ...string) string {
	out, err := exec.Command(cmd, args...).Output()
	if err != nil {
		return "(version unknown)"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// homeDir returns the user's home directory, falling back to "/root".
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/root"
}

// tailFile returns the last n lines of r.
func tailFile(r io.Reader, n int) []string {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}
