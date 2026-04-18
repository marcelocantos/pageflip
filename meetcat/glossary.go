// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package main — glossary cache for meetcat's dejargoniser.
//
// Implements 🎯T16: a local Confluence glossary cache layer that feeds
// known acronym expansions to the dejargoniser specialist agent.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/marcelocantos/claudia"
)

// GlossaryEntry holds one acronym-expansion pair with provenance.
type GlossaryEntry struct {
	Acronym    string  `json:"acronym"`
	Expansion  string  `json:"expansion"`
	Source     string  `json:"source"`      // "confluence", "meeting", "research"
	SourceURL  string  `json:"source_url"`  // Confluence page URL if available
	Confidence float64 `json:"confidence"`  // 0.0–1.0
}

// GlossaryCache is a thread-safe in-memory + on-disk store for glossary entries.
// Keys are uppercase acronyms. The JSON file is plain and human-editable.
type GlossaryCache struct {
	entries map[string]*GlossaryEntry // keyed by uppercase acronym
	path    string                    // JSON file path for persistence
	mu      sync.RWMutex
}

// minConfidence is the lowest confidence score accepted into the cache.
// Entries below this threshold (e.g. OCR misreads) are silently dropped.
const minConfidence = 0.3

// NewGlossaryCache loads the cache from path if the file exists,
// or returns an empty cache if the file is absent. A read error
// (other than not-found) is returned as an error.
func NewGlossaryCache(path string) (*GlossaryCache, error) {
	g := &GlossaryCache{
		entries: make(map[string]*GlossaryEntry),
		path:    path,
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return g, nil
	}
	if err != nil {
		return nil, fmt.Errorf("glossary: read %s: %w", path, err)
	}
	var list []GlossaryEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("glossary: parse %s: %w", path, err)
	}
	for i := range list {
		e := &list[i]
		key := strings.ToUpper(e.Acronym)
		g.entries[key] = e
	}
	return g, nil
}

// Lookup returns the entry for acronym (case-insensitive), or nil if unknown.
func (g *GlossaryCache) Lookup(acronym string) *GlossaryEntry {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.entries[strings.ToUpper(acronym)]
}

// Add inserts or replaces an entry, then persists the cache to disk.
// Entries with Confidence < minConfidence are silently dropped to
// prevent OCR errors from polluting the cache.
func (g *GlossaryCache) Add(entry GlossaryEntry) error {
	if entry.Confidence < minConfidence {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	key := strings.ToUpper(entry.Acronym)
	entry.Acronym = key // normalise stored form
	g.entries[key] = &entry

	return g.persistLocked()
}

// All returns a snapshot of all entries in no guaranteed order.
func (g *GlossaryCache) All() []GlossaryEntry {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]GlossaryEntry, 0, len(g.entries))
	for _, e := range g.entries {
		out = append(out, *e)
	}
	return out
}

// Len returns the number of entries in the cache.
func (g *GlossaryCache) Len() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.entries)
}

// persistLocked writes all entries to g.path as a JSON array.
// Caller must hold g.mu (write lock).
func (g *GlossaryCache) persistLocked() error {
	if g.path == "" {
		return nil
	}
	entries := make([]GlossaryEntry, 0, len(g.entries))
	for _, e := range g.entries {
		entries = append(entries, *e)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("glossary: marshal: %w", err)
	}
	// Write via temp file to avoid truncating on error.
	tmp := g.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("glossary: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, g.path); err != nil {
		return fmt.Errorf("glossary: rename %s → %s: %w", tmp, g.path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Acronym detection
// ---------------------------------------------------------------------------

// reAcronym matches 2-5 consecutive uppercase ASCII letters standing alone
// (word boundary on each side). It intentionally excludes very common
// filler words that are all-caps by convention but not acronyms.
var reAcronym = regexp.MustCompile(`\b([A-Z]{2,5})\b`)

// commonWords is the exclusion set for reAcronym — uppercase words that
// appear frequently in prose and are not corporate/technical acronyms.
var commonWords = map[string]bool{
	"THE": true, "AND": true, "FOR": true, "WITH": true, "FROM": true,
	"INTO": true, "ONTO": true, "OVER": true, "THAN": true, "THAT": true,
	"THIS": true, "THEN": true, "WHEN": true, "WHERE": true, "WHICH": true,
	"WHO":  true, "WHY": true, "HOW": true, "BUT": true, "NOT": true,
	"ARE":  true, "WAS": true, "WERE": true, "HAS": true, "HAVE": true,
	"HAD":  true, "WILL": true, "CAN": true, "MAY": true, "MUST": true,
	"ALL":  true, "ANY": true, "EACH": true, "SOME": true, "SUCH": true,
	"NEW":  true, "OLD": true, "USE": true, "USED": true, "USES": true,
	"PER":  true, "VIA": true, "ETC": true,
}

// ExtractAcronyms returns unique acronyms found in text, excluding common
// words. Returned acronyms are uppercased.
func ExtractAcronyms(text string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range reAcronym.FindAllStringSubmatch(text, -1) {
		word := m[1]
		if commonWords[word] {
			continue
		}
		if !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Glossary preamble injection
// ---------------------------------------------------------------------------

// GlossaryPreamble builds the [Glossary: ...] preamble lines for a slide
// message from the slide's text content. It returns an empty string when
// no known acronyms are found.
func GlossaryPreamble(text string, cache *GlossaryCache) string {
	if cache == nil {
		return ""
	}
	acronyms := ExtractAcronyms(text)
	if len(acronyms) == 0 {
		return ""
	}
	var lines []string
	for _, a := range acronyms {
		entry := cache.Lookup(a)
		if entry == nil {
			continue
		}
		line := fmt.Sprintf("[Glossary: %s = %s (%s)]", a, entry.Expansion, entry.Source)
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// ---------------------------------------------------------------------------
// Background research via claudia.Task (haiku)
// ---------------------------------------------------------------------------

// ResearchAcronym spawns a haiku Task to research an unknown acronym in a
// corporate context. The result is cached with source "research" and
// confidence 0.7. Errors are silently absorbed — the glossary pane simply
// won't show an entry for this acronym.
func ResearchAcronym(ctx context.Context, acronym string, cache *GlossaryCache, workDir string) {
	if cache == nil {
		return
	}
	// Don't re-research if we already have an entry.
	if cache.Lookup(acronym) != nil {
		return
	}

	task := claudia.NewTask(claudia.TaskConfig{
		Name:    "glossary-research-" + acronym,
		WorkDir: workDir,
		Model:   "haiku",
	})

	prompt := fmt.Sprintf(
		"What does the acronym %q stand for in a corporate or technical context?"+
			" Consider Confluence, GitHub, and general software/business usage."+
			" Reply with exactly two lines:\n"+
			"EXPANSION: <full expansion>\n"+
			"CONFIDENCE: <0.0-1.0>\n"+
			"If uncertain, use a lower confidence score. Only provide these two lines.",
		acronym,
	)

	ch, err := task.RunTask(ctx, prompt)
	if err != nil {
		return
	}

	var sb strings.Builder
	for ev := range ch {
		if ev.Type == "assistant" {
			sb.WriteString(ev.Content)
		}
		if ev.IsError {
			return
		}
	}

	expansion, confidence := parseResearchReply(sb.String())
	if expansion == "" {
		return
	}

	entry := GlossaryEntry{
		Acronym:    acronym,
		Expansion:  expansion,
		Source:     "research",
		Confidence: confidence,
	}
	_ = cache.Add(entry) // best-effort; errors absorbed
}

// parseResearchReply extracts EXPANSION and CONFIDENCE from a haiku reply.
func parseResearchReply(reply string) (expansion string, confidence float64) {
	confidence = 0.7 // default
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		if exp, ok := strings.CutPrefix(line, "EXPANSION:"); ok {
			expansion = strings.TrimSpace(exp)
		} else if conf, ok := strings.CutPrefix(line, "CONFIDENCE:"); ok {
			var f float64
			if _, err := fmt.Sscanf(strings.TrimSpace(conf), "%f", &f); err == nil {
				confidence = f
			}
		}
	}
	return
}

// ---------------------------------------------------------------------------
// Confluence glossary refresh
// ---------------------------------------------------------------------------

// RefreshFromConfluence uses a one-shot claude -p task to scrape
// acronym/expansion pairs from Confluence glossary pages and bulk-adds
// them to the cache. Incremental: only processes acronyms not already in
// the cache (unless force is true).
func RefreshFromConfluence(ctx context.Context, confluenceURL string, cache *GlossaryCache, workDir string, dryRun bool) (added int, err error) {
	if cache == nil {
		return 0, fmt.Errorf("glossary: nil cache")
	}

	// Count existing entries so we can report net additions.
	existingLen := cache.Len()

	task := claudia.NewTask(claudia.TaskConfig{
		Name:    "glossary-confluence-refresh",
		WorkDir: workDir,
		Model:   "sonnet",
	})

	prompt := fmt.Sprintf(
		"Search the Confluence wiki at %s for pages that define acronyms or"+
			" jargon. Look for pages with titles containing 'glossary', 'acronyms',"+
			" 'abbreviations', or similar. Extract all acronym-expansion pairs you find."+
			"\n\nReturn the results as a JSON array with this exact schema:\n"+
			`[{"acronym":"...", "expansion":"...", "source_url":"..."}]`+
			"\n\nReturn only the JSON array, no other text. If no glossary pages exist,"+
			" return an empty array [].",
		confluenceURL,
	)

	ch, err := task.RunTask(ctx, prompt)
	if err != nil {
		return 0, fmt.Errorf("glossary: confluence task: %w", err)
	}

	var sb strings.Builder
	for ev := range ch {
		if ev.Type == "assistant" {
			sb.WriteString(ev.Content)
		}
		if ev.IsError {
			return 0, fmt.Errorf("glossary: confluence task error")
		}
	}

	type confluenceEntry struct {
		Acronym   string `json:"acronym"`
		Expansion string `json:"expansion"`
		SourceURL string `json:"source_url"`
	}

	// Extract the JSON array from the response (may have surrounding text).
	raw := extractJSONArray(sb.String())
	var list []confluenceEntry
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return 0, fmt.Errorf("glossary: parse confluence response: %w", err)
	}

	for _, ce := range list {
		acronym := strings.ToUpper(strings.TrimSpace(ce.Acronym))
		if acronym == "" || ce.Expansion == "" {
			continue
		}
		// Incremental: skip if already cached.
		if cache.Lookup(acronym) != nil {
			continue
		}
		entry := GlossaryEntry{
			Acronym:    acronym,
			Expansion:  ce.Expansion,
			Source:     "confluence",
			SourceURL:  ce.SourceURL,
			Confidence: 1.0, // Confluence-sourced entries are considered authoritative
		}
		if !dryRun {
			if addErr := cache.Add(entry); addErr != nil {
				// Log but don't abort — partial refresh is better than none.
				err = addErr
			}
		}
	}

	added = cache.Len() - existingLen
	if dryRun {
		added = len(list)
	}
	return added, err
}

// extractJSONArray finds the first [...] JSON array in s.
// Falls back to the full string if no brackets are found.
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return "[]"
}
