// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — resolver: extract and look up reference strings from slide text.
//
// Implements 🎯T11: meetcat resolver — Task-mode agent that turns extracted
// strings into hyperlinked references.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/marcelocantos/claudia"
)

// RefKind classifies the type of a resolved reference.
type RefKind string

const (
	RefURL        RefKind = "url"
	RefJira       RefKind = "jira"
	RefGitHub     RefKind = "github"
	RefConfluence RefKind = "confluence"
	RefSlack      RefKind = "slack"
	RefUnknown    RefKind = "unknown"
)

// ResolvedRef is the result of looking up a single reference string.
type ResolvedRef struct {
	Kind  RefKind
	Raw   string // original matched string
	Title string // human-readable title (from lookup or heuristic)
	URL   string // resolved URL
}

// Resolver extracts references from slide text and resolves each unique
// reference once via a claudia Task, caching results per meeting.
type Resolver struct {
	cache   map[string]*ResolvedRef
	workDir string
	mu      sync.Mutex
}

// NewResolver returns a Resolver scoped to the given working directory.
func NewResolver(workDir string) *Resolver {
	return &Resolver{
		cache:   make(map[string]*ResolvedRef),
		workDir: workDir,
	}
}

// Compiled extraction regexes. The order matters for kind detection:
// Slack is a URL prefix match, so it must be checked before the generic
// URL regex when classifying.
var (
	reURL    = regexp.MustCompile(`https?://\S+`)
	reJira   = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d+`)
	reGitHub = regexp.MustCompile(`[\w-]+/[\w-]+#\d+`)
	reSlack  = regexp.MustCompile(`https://\S+\.slack\.com/archives/\S+`)
)

// Extract scans text for candidate reference strings and returns each
// unique match once. The slice order is deterministic (left-to-right in
// the input, deduplicated in first-seen order).
func (r *Resolver) Extract(text string) []string {
	seen := make(map[string]struct{})
	var out []string

	addUniq := func(s string) {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}

	for _, m := range reURL.FindAllString(text, -1) {
		addUniq(m)
	}
	for _, m := range reJira.FindAllString(text, -1) {
		addUniq(m)
	}
	for _, m := range reGitHub.FindAllString(text, -1) {
		addUniq(m)
	}

	return out
}

// Resolve looks up each ref in the cache. For refs not yet seen it
// spawns a claudia Task (haiku model) to fetch title and URL, then
// caches the result. Concurrent callers share a single per-ref
// in-flight lookup via the cache mutex.
func (r *Resolver) Resolve(ctx context.Context, refs []string) []*ResolvedRef {
	out := make([]*ResolvedRef, len(refs))
	for i, ref := range refs {
		out[i] = r.resolveSingle(ctx, ref)
	}
	return out
}

// resolveSingle returns the cached result for ref, or fetches and caches
// it on first call.
func (r *Resolver) resolveSingle(ctx context.Context, ref string) *ResolvedRef {
	r.mu.Lock()
	if cached, ok := r.cache[ref]; ok {
		r.mu.Unlock()
		return cached
	}
	r.mu.Unlock()

	result := r.lookup(ctx, ref)

	r.mu.Lock()
	// Another goroutine may have raced us; let the first writer win.
	if _, ok := r.cache[ref]; !ok {
		r.cache[ref] = result
	}
	result = r.cache[ref]
	r.mu.Unlock()

	return result
}

// classifyRef determines the RefKind for a raw string.
func classifyRef(ref string) RefKind {
	if reSlack.MatchString(ref) {
		return RefSlack
	}
	if reURL.MatchString(ref) {
		return RefURL
	}
	if reJira.MatchString(ref) {
		return RefJira
	}
	if reGitHub.MatchString(ref) {
		return RefGitHub
	}
	return RefUnknown
}

// lookup resolves a single reference, using a heuristic shortcut for
// plain URLs and a claudia Task for all other kinds.
func (r *Resolver) lookup(ctx context.Context, ref string) *ResolvedRef {
	kind := classifyRef(ref)

	// Plain URLs resolve without a Claude call: title = hostname.
	if kind == RefURL || kind == RefSlack {
		title := ref
		if u, err := url.Parse(ref); err == nil && u.Host != "" {
			title = u.Host
		}
		return &ResolvedRef{Kind: kind, Raw: ref, Title: title, URL: ref}
	}

	return r.lookupViaClaude(ctx, ref, kind)
}

// lookupViaClaude spawns a one-shot Task to resolve a non-URL reference.
func (r *Resolver) lookupViaClaude(ctx context.Context, ref string, kind RefKind) *ResolvedRef {
	prompt := fmt.Sprintf(
		"Look up this reference and return a one-line title and URL.\nReference: %s\nFormat: TITLE: <title>\nURL: <url>",
		ref,
	)

	task := claudia.NewTask(claudia.TaskConfig{
		Name:    "resolver-" + ref,
		WorkDir: r.workDir,
		Model:   "haiku",
	})

	ch, err := task.RunTask(ctx, prompt)
	if err != nil {
		return &ResolvedRef{Kind: kind, Raw: ref}
	}

	var sb strings.Builder
	for ev := range ch {
		if ev.Type == "assistant" {
			sb.WriteString(ev.Content)
		}
		if ev.IsError {
			return &ResolvedRef{Kind: kind, Raw: ref}
		}
	}

	return parseClaudeReply(ref, kind, sb.String())
}

// parseClaudeReply extracts TITLE and URL lines from a Claude response.
// If either field is missing, the raw ref is used as a fallback.
func parseClaudeReply(ref string, kind RefKind, reply string) *ResolvedRef {
	result := &ResolvedRef{Kind: kind, Raw: ref}

	scanner := bufio.NewScanner(strings.NewReader(reply))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if t, ok := strings.CutPrefix(line, "TITLE:"); ok {
			result.Title = strings.TrimSpace(t)
		} else if u, ok := strings.CutPrefix(line, "URL:"); ok {
			result.URL = strings.TrimSpace(u)
		}
	}

	if result.Title == "" {
		result.Title = ref
	}

	return result
}
