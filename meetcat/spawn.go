// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// spawnCapture starts pageflip-capture as a child process so the user
// only has to invoke `pageflip` instead of stitching
// `pageflip-capture ... | pageflip`. args are forwarded verbatim to
// pageflip-capture (e.g. --region X,Y,W,H, --window, --window-title);
// pageflip always adds --events-out stdout so the NDJSON event stream
// lands on the pipe we read from.
//
// Returns:
//   - stdout: pageflip-capture's stdout pipe — the NDJSON event stream
//     the orchestrator decodes in runText. Consumers MUST read until
//     EOF or pageflip-capture will block on a full pipe.
//   - cleanup: signals SIGTERM, waits up to 2s, then SIGKILLs. Safe to
//     call multiple times. The first call blocks until the process has
//     fully exited so callers can rely on lifecycle ordering at
//     shutdown.
//
// pageflip-capture's stderr is forwarded line-by-line into the sink as
// SystemLines so capture-side diagnostics live in the same screen as
// pageflip's specialist output.
func spawnCapture(ctx context.Context, sink StreamSink, args []string) (io.ReadCloser, func(), error) {
	full := append([]string{"--events-out", "stdout"}, args...)
	cmd := exec.CommandContext(ctx, "pageflip-capture", full...)

	// Use SIGTERM (not the CommandContext default of SIGKILL) so
	// pageflip-capture's signal handler runs — it stops the audio
	// capture cleanly and lets the picker tear down its NSWindows.
	// SIGKILL is applied as a fallback after WaitDelay.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("pageflip-capture stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("pageflip-capture stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("pageflip-capture start: %w (is `pageflip-capture` on PATH?)", err)
	}

	// Forward pageflip-capture's stderr line-by-line into the sink. Dim
	// formatting so capture diagnostics don't visually compete with
	// specialist output, but they're available in the same scrollback
	// for live debugging — which was the whole reason we're embedding
	// pageflip-capture rather than asking the operator to tail two streams.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			// pageflip-capture self-prefixes its messages with "pageflip:" so
			// don't add a second one — just dim the line so it sits
			// quietly in the viewport without competing with the
			// specialist output.
			sink.SystemLine(colorize(colorDim, scanner.Text()))
		}
	}()

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	cleanupCalled := false
	cleanup := func() {
		if cleanupCalled {
			<-done
			return
		}
		cleanupCalled = true
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
	}

	return stdout, cleanup, nil
}
