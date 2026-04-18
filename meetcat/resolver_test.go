// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
)

// --------------------------------------------------------------------------
// Extract tests
// --------------------------------------------------------------------------

func TestExtract_URLs(t *testing.T) {
	r := NewResolver(t.TempDir())
	text := "See https://example.com/path and http://other.io/page for details."
	refs := r.Extract(text)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	if refs[0] != "https://example.com/path" {
		t.Errorf("refs[0] = %q, want https://example.com/path", refs[0])
	}
	if refs[1] != "http://other.io/page" {
		t.Errorf("refs[1] = %q, want http://other.io/page", refs[1])
	}
}

func TestExtract_Jira(t *testing.T) {
	r := NewResolver(t.TempDir())
	text := "Fixes FOO-123 and BAR-456."
	refs := r.Extract(text)
	// reURL won't match these, so only reJira captures them.
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	if refs[0] != "FOO-123" {
		t.Errorf("refs[0] = %q, want FOO-123", refs[0])
	}
	if refs[1] != "BAR-456" {
		t.Errorf("refs[1] = %q, want BAR-456", refs[1])
	}
}

func TestExtract_GitHub(t *testing.T) {
	r := NewResolver(t.TempDir())
	text := "Closes org/repo#456, also my-org/my-repo#1."
	refs := r.Extract(text)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %v", len(refs), refs)
	}
	if refs[0] != "org/repo#456" {
		t.Errorf("refs[0] = %q, want org/repo#456", refs[0])
	}
	if refs[1] != "my-org/my-repo#1" {
		t.Errorf("refs[1] = %q, want my-org/my-repo#1", refs[1])
	}
}

func TestExtract_Mixed(t *testing.T) {
	r := NewResolver(t.TempDir())
	text := "See https://example.com and FOO-123. Also org/repo#7."
	refs := r.Extract(text)
	// Expected: URL, Jira key, GitHub ref (3 total, in that order).
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d: %v", len(refs), refs)
	}
}

func TestExtract_Deduplicate(t *testing.T) {
	r := NewResolver(t.TempDir())
	text := "https://example.com repeated https://example.com again."
	refs := r.Extract(text)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref (deduped), got %d: %v", len(refs), refs)
	}
}

func TestExtract_Empty(t *testing.T) {
	r := NewResolver(t.TempDir())
	refs := r.Extract("no references here at all")
	if len(refs) != 0 {
		t.Fatalf("expected 0 refs, got %d: %v", len(refs), refs)
	}
}

// --------------------------------------------------------------------------
// classifyRef tests
// --------------------------------------------------------------------------

func TestClassifyRef(t *testing.T) {
	cases := []struct {
		ref  string
		want RefKind
	}{
		{"https://example.com/path", RefURL},
		{"http://plain.io", RefURL},
		{"https://acme.slack.com/archives/C123/p456", RefSlack},
		{"FOO-123", RefJira},
		{"AB-1", RefJira},
		{"org/repo#456", RefGitHub},
		{"my-org/my-repo#1", RefGitHub},
		{"random text", RefUnknown},
	}
	for _, tc := range cases {
		got := classifyRef(tc.ref)
		if got != tc.want {
			t.Errorf("classifyRef(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// --------------------------------------------------------------------------
// parseClaudeReply tests
// --------------------------------------------------------------------------

func TestParseClaudeReply_Full(t *testing.T) {
	reply := "TITLE: My Ticket\nURL: https://jira.example.com/browse/FOO-123"
	ref := parseClaudeReply("FOO-123", RefJira, reply)
	if ref.Title != "My Ticket" {
		t.Errorf("Title = %q, want %q", ref.Title, "My Ticket")
	}
	if ref.URL != "https://jira.example.com/browse/FOO-123" {
		t.Errorf("URL = %q, want %q", ref.URL, "https://jira.example.com/browse/FOO-123")
	}
	if ref.Kind != RefJira {
		t.Errorf("Kind = %q, want %q", ref.Kind, RefJira)
	}
}

func TestParseClaudeReply_MissingTitle(t *testing.T) {
	// When no TITLE line is present, falls back to raw ref.
	reply := "URL: https://jira.example.com/browse/FOO-123"
	ref := parseClaudeReply("FOO-123", RefJira, reply)
	if ref.Title != "FOO-123" {
		t.Errorf("Title = %q, want %q (raw fallback)", ref.Title, "FOO-123")
	}
}

func TestParseClaudeReply_ExtraWhitespace(t *testing.T) {
	reply := "  TITLE:   Padded Title  \n  URL:   https://example.com  "
	ref := parseClaudeReply("raw", RefUnknown, reply)
	if ref.Title != "Padded Title" {
		t.Errorf("Title = %q, want %q", ref.Title, "Padded Title")
	}
	if ref.URL != "https://example.com" {
		t.Errorf("URL = %q, want %q", ref.URL, "https://example.com")
	}
}

// --------------------------------------------------------------------------
// Resolve cache-hit / cache-miss tests (plain URL path — no Claude call)
// --------------------------------------------------------------------------

func TestResolveURL_CacheHit(t *testing.T) {
	r := NewResolver(t.TempDir())
	ctx := context.Background()

	refs1 := r.Resolve(ctx, []string{"https://example.com/page"})
	if len(refs1) != 1 {
		t.Fatalf("expected 1 result, got %d", len(refs1))
	}
	first := refs1[0]
	if first.Kind != RefURL {
		t.Errorf("Kind = %q, want url", first.Kind)
	}
	if first.Title != "example.com" {
		t.Errorf("Title = %q, want example.com", first.Title)
	}

	// Second call must return the same pointer (cache hit).
	refs2 := r.Resolve(ctx, []string{"https://example.com/page"})
	if refs2[0] != first {
		t.Error("expected cache hit (same pointer), got different object")
	}
}

func TestResolveURL_Slack(t *testing.T) {
	r := NewResolver(t.TempDir())
	ctx := context.Background()
	u := "https://acme.slack.com/archives/C123ABC/p1234567890"
	refs := r.Resolve(ctx, []string{u})
	if refs[0].Kind != RefSlack {
		t.Errorf("Kind = %q, want slack", refs[0].Kind)
	}
	if refs[0].URL != u {
		t.Errorf("URL = %q, want %q", refs[0].URL, u)
	}
}

func TestResolveMultiple_NoDuplicateLookup(t *testing.T) {
	r := NewResolver(t.TempDir())
	ctx := context.Background()

	// Resolve the same URL three times in one batch; all three results
	// should be identical (same pointer, one cache entry).
	u := "https://example.com/"
	results := r.Resolve(ctx, []string{u, u, u})
	if results[0] != results[1] || results[1] != results[2] {
		t.Error("expected identical pointers for deduplicated URL lookups")
	}
}
