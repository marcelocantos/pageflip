// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// ANSI SGR codes for per-specialist tags. Bright colours are chosen so the
// tag stands out against typical terminal backgrounds without overwhelming
// the message text that follows.
var specialistColors = map[string]string{
	"skeptic":        "\x1b[91m", // bright red
	"constructive":   "\x1b[92m", // bright green
	"neutral":        "\x1b[96m", // bright cyan
	"dejargoniser":   "\x1b[93m", // bright yellow
	"contradictions": "\x1b[95m", // bright magenta
}

const (
	colorReset  = "\x1b[0m"
	colorDim    = "\x1b[90m"
	colorSlide  = "\x1b[1;97m" // bold bright white — slide event header
	colorSystem = "\x1b[94m"   // bright blue — meetcat's own messages
	colorError  = "\x1b[31m"   // red — errors
)

var (
	colorsOnce    sync.Once
	colorsEnabled bool
)

// colorsOn reports whether stderr is a TTY — the sole gate on whether we
// emit ANSI escapes. When meetcat is piped into another tool, output is
// plain text so consumers (grep, tee, redirects) see unadorned tags.
func colorsOn() bool {
	colorsOnce.Do(func() {
		fi, err := os.Stderr.Stat()
		if err != nil {
			return
		}
		colorsEnabled = fi.Mode()&os.ModeCharDevice != 0
	})
	return colorsEnabled
}

// tag returns `[name]` in the specialist's colour when colours are enabled,
// or the plain bracketed form otherwise. Reset is appended so subsequent
// text renders in the default style.
func tag(name string) string {
	if !colorsOn() {
		return fmt.Sprintf("[%s]", name)
	}
	col, ok := specialistColors[name]
	if !ok {
		col = colorDim
	}
	return fmt.Sprintf("%s[%s]%s", col, name, colorReset)
}

// colorize wraps `text` in the given ANSI escape when colours are enabled.
func colorize(code, text string) string {
	if !colorsOn() {
		return text
	}
	return code + text + colorReset
}

// wrapURLs rewrites bare URLs in s as OSC 8 hyperlinks (T19.4) so
// terminals that support them (iTerm2, WezTerm, kitty, recent macOS
// Terminal) render them clickable. Plain text is returned when colours
// are disabled since OSC 8 sequences aren't useful in non-TTY output.
func wrapURLs(s string) string {
	if !colorsOn() {
		return s
	}
	var out []byte
	i := 0
	for i < len(s) {
		// Find the next "http://" or "https://".
		j := i
		for j < len(s) {
			if (j+7 <= len(s) && s[j:j+7] == "http://") ||
				(j+8 <= len(s) && s[j:j+8] == "https://") {
				break
			}
			j++
		}
		out = append(out, s[i:j]...)
		if j >= len(s) {
			break
		}
		// Find the end of the URL (first whitespace or terminator).
		k := j
		for k < len(s) && s[k] > 0x20 && s[k] != '<' && s[k] != '>' && s[k] != '"' {
			k++
		}
		// Trim trailing punctuation that's unlikely to be part of the URL.
		end := k
		for end > j && strings.ContainsRune(".,;:!?)]}", rune(s[end-1])) {
			end--
		}
		url := s[j:end]
		out = append(out, "\x1b]8;;"...)
		out = append(out, url...)
		out = append(out, '\x07')
		out = append(out, url...)
		out = append(out, "\x1b]8;;\x07"...)
		out = append(out, s[end:k]...)
		i = k
	}
	return string(out)
}
