// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed web/*
var webAssets embed.FS

// markdownEngine is built once at server start and reused across
// every chunk render. Goldmark's parser is goroutine-safe; the only
// shared mutable state is the chroma formatter's stylesheet, which
// we render statically and serve as part of the page.
var markdownEngine = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		extension.Strikethrough,
		extension.Linkify,
		extension.TaskList,
		highlighting.NewHighlighting(
			highlighting.WithStyle("github-dark"),
			highlighting.WithFormatOptions(
				chromahtml.WithClasses(true),
			),
		),
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		html.WithUnsafe(),
	),
)

// renderMarkdownToHTML runs the specialist's running buffer through
// goldmark with GFM + linkify + task lists + syntax highlighting. On
// parse error returns the raw text wrapped in a <pre> so the
// operator never sees a blank specialist block.
func renderMarkdownToHTML(src string) string {
	var buf bytes.Buffer
	if err := markdownEngine.Convert([]byte(src), &buf); err != nil {
		return "<pre>" + htmlEscape(src) + "</pre>"
	}
	return buf.String()
}

// htmlEscape is the bare-minimum escaper for fallback output. We
// only hit it on parse error so the input is already known to be
// malformed; goldmark's normal output is already escaped.
func htmlEscape(s string) string {
	r := []rune(s)
	var b bytes.Buffer
	for _, c := range r {
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// webEvent is one SSE message destined for connected browser tabs.
// `kind` selects which event listener fires on the client side
// (`slide`, `specialist`, `turn-done`, `state`, `system`,
// `meeting`); `payload` is JSON-serialisable data the client renders.
type webEvent struct {
	kind    string
	payload map[string]any
}

// hub is the SSE fan-out point. publish() broadcasts to every
// connected client; new clients get a snapshot of every event seen
// so far so the page renders the same regardless of when the tab
// was opened.
type hub struct {
	mu      sync.Mutex
	clients map[chan webEvent]struct{}
	// history retains every event in arrival order so a freshly-
	// connected tab can replay the meeting up to "now". Cap on a
	// few thousand events; a 1-hour meeting at one chunk per
	// specialist per second hits ~18k, still tiny in memory but
	// worth a hard limit so a forgotten browser tab doesn't
	// unbounded-grow the slice.
	history    []webEvent
	historyCap int
}

func newHub() *hub {
	return &hub{
		clients:    map[chan webEvent]struct{}{},
		historyCap: 32_768,
	}
}

func (h *hub) publish(ev webEvent) {
	h.mu.Lock()
	if len(h.history) >= h.historyCap {
		// Drop the oldest 25% to amortise. Snapshot replay loses
		// some early events under this path, which is acceptable
		// for an interactive tool — the user's viewport already
		// scrolled past them.
		h.history = append([]webEvent(nil), h.history[h.historyCap/4:]...)
	}
	h.history = append(h.history, ev)
	clients := make([]chan webEvent, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		// Non-blocking: a slow client mustn't stall the publisher.
		// The client buffer is generous (256); if it overflows the
		// client will fall behind and the tab will diverge. The
		// reconnect path on the browser side recovers it.
		select {
		case c <- ev:
		default:
		}
	}
}

func (h *hub) subscribe() (chan webEvent, []webEvent) {
	c := make(chan webEvent, 256)
	h.mu.Lock()
	h.clients[c] = struct{}{}
	snapshot := append([]webEvent(nil), h.history...)
	h.mu.Unlock()
	return c, snapshot
}

func (h *hub) unsubscribe(c chan webEvent) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c)
}

// webSink is the StreamSink implementation that publishes meeting
// events to a hub for SSE fan-out. Each method translates a sink
// call into a webEvent — slide arrival, specialist chunk, turn
// done, lifecycle state, system line — with goldmark-rendered HTML
// for anything markdown-shaped.
//
// Per-(slide, role) running buffers live on the sink so we can
// re-render the whole accumulated text on every chunk; this matches
// the TUI's stream-render-replace model the user already validated.
type webSink struct {
	hub *hub

	// absWorkDir is the absolute, cleaned work_dir for the meeting.
	// It's used to rebase the slide PNG's absolute filesystem path
	// to a /slides/* URL the embedded HTTP server will serve. Stored
	// in absolute form so OpenSection's rebase doesn't depend on
	// process cwd at call time.
	absWorkDir string

	mu      sync.Mutex
	buffers map[string]string // key: slideID + "|" + role
}

func newWebSink(h *hub, absWorkDir string) *webSink {
	return &webSink{hub: h, absWorkDir: absWorkDir, buffers: map[string]string{}}
}

func (s *webSink) OpenSection(slideID, header, imagePath string) {
	imageURL := ""
	if imagePath != "" {
		if rel, err := filepath.Rel(s.absWorkDir, imagePath); err == nil && !strings.HasPrefix(rel, "..") {
			// URL-escape each path segment but keep the slashes so
			// the server's /slides/<rel> handler routes correctly.
			imageURL = "/slides/" + escapeURLPath(rel)
		}
	}
	s.hub.publish(webEvent{
		kind: "slide",
		payload: map[string]any{
			"slide_id":  slideID,
			"header":    header,
			"image_url": imageURL,
			"at":        time.Now().UnixMilli(),
		},
	})
}

// escapeURLPath URL-escapes each path segment while preserving the
// "/" boundaries so a path like "pageflip-1234/20260425T....png"
// yields the same shape with each segment correctly path-escaped.
func escapeURLPath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}

func (s *webSink) SpecialistLine(role, slideID, text string) {
	if slideID == "" {
		// Lifecycle / system message tied to a specialist (startup
		// errors, etc.). Surface as a system line tagged with the
		// role so the page can colour it appropriately.
		s.hub.publish(webEvent{
			kind: "system",
			payload: map[string]any{
				"role": role,
				"text": text,
			},
		})
		return
	}
	key := slideID + "|" + role
	s.mu.Lock()
	s.buffers[key] += text
	full := s.buffers[key]
	s.mu.Unlock()
	s.hub.publish(webEvent{
		kind: "specialist",
		payload: map[string]any{
			"slide_id": slideID,
			"role":     role,
			"html":     renderMarkdownToHTML(full),
			"raw":      full,
		},
	})
}

func (s *webSink) SpecialistTurnDone(role, slideID, fullText string) {
	if slideID == "" || fullText == "" {
		return
	}
	key := slideID + "|" + role
	s.mu.Lock()
	s.buffers[key] = fullText
	s.mu.Unlock()
	s.hub.publish(webEvent{
		kind: "turn-done",
		payload: map[string]any{
			"slide_id": slideID,
			"role":     role,
			"html":     renderMarkdownToHTML(fullText),
			"raw":      fullText,
		},
	})
}

func (s *webSink) SpecialistReady(role string) {
	s.hub.publish(webEvent{
		kind: "state",
		payload: map[string]any{
			"role":  role,
			"state": "ready",
		},
	})
}

func (s *webSink) SpecialistStopped(role string, turns int) {
	s.hub.publish(webEvent{
		kind: "state",
		payload: map[string]any{
			"role":  role,
			"state": "stopped",
			"turns": turns,
		},
	})
}

func (s *webSink) SystemLine(text string) {
	s.hub.publish(webEvent{
		kind: "system",
		payload: map[string]any{
			"text": text,
		},
	})
}

// startWebServer binds an HTTP listener on localhost (auto-port),
// returns the public URL plus a shutdown function. The server hosts
// three routes:
//
//   - GET /            — embedded index.html
//   - GET /events      — SSE stream of webEvents
//   - GET /slides/...  — slide PNGs from the work directory
//
// shutdown blocks until in-flight requests complete or a 2 s grace
// timer expires, then closes the listener.
func startWebServer(meetingID, workDir string, h *hub) (string, func(), error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := webAssets.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/style.css", func(w http.ResponseWriter, r *http.Request) {
		data, err := webAssets.ReadFile("web/style.css")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/script.js", func(w http.ResponseWriter, r *http.Request) {
		data, err := webAssets.ReadFile("web/script.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/meeting", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meeting_id": meetingID,
		})
	})

	// Slide PNG passthrough. pageflip writes images into a
	// pageflip-<ts>/ directory under work_dir; we serve any PNG that
	// resolves under work_dir, with a path-traversal guard so a
	// malicious URL can't escape the meeting's directory.
	absWork, err := filepath.Abs(workDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve work dir: %w", err)
	}
	mux.HandleFunc("/slides/", func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Path[len("/slides/"):]
		full := filepath.Join(absWork, rel)
		clean, err := filepath.Abs(full)
		if err != nil || !pathInside(clean, absWork) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, clean)
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch, snapshot := h.subscribe()
		defer h.unsubscribe(ch)

		// Replay history first so a fresh tab catches up.
		for _, ev := range snapshot {
			if !writeSSE(w, flusher, ev) {
				return
			}
		}
		// Live stream from now on.
		ctx := r.Context()
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if !writeSSE(w, flusher, ev) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	})

	// Fixed port so a `meetcat resume` instance binds the same URL
	// that the original tab is still pointed at. EventSource auto-
	// reconnects on the same URL, so the open tab picks the new
	// session up without the operator touching anything. Picked
	// from the IANA dynamic range (49152-65535) to avoid the
	// commonly-used dev-tool ports clustered in the 8xxx-9xxx
	// neighbourhood. The trade-off — only one meetcat at a time —
	// is acceptable for the single-operator workflow this tool
	// serves; a daemon mode would need a port broker, but that's
	// a separate target.
	const meetcatPort = 49831
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", meetcatPort))
	if err != nil {
		return "", nil, fmt.Errorf("bind 127.0.0.1:%d: %w (is another meetcat already running?)", meetcatPort, err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://127.0.0.1:%d/", addr.Port)

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(listener)
	}()

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return url, shutdown, nil
}

// writeSSE emits one event in SSE wire format. Returns false on
// write failure so the caller can break out of the loop.
func writeSSE(w io.Writer, flusher http.Flusher, ev webEvent) bool {
	data, err := json.Marshal(ev.payload)
	if err != nil {
		return true // skip malformed event but keep stream alive
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.kind, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// pathInside reports whether `child` is `parent` or below it,
// after both have been cleaned to absolute form. Used to gate
// /slides/* requests against directory traversal.
func pathInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !startsWithDotDot(rel)
}

func startsWithDotDot(p string) bool {
	return len(p) >= 3 && p[:3] == ".."+string(filepath.Separator)
}

// openInBrowser nudges the user's default browser at the URL. Best-
// effort: we don't surface failures because the URL is also printed
// to stderr for the operator to click manually.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return errors.New("unsupported platform for browser auto-open")
	}
	return cmd.Start()
}
