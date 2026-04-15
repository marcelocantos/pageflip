// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMinimalEvent(t *testing.T) {
	in := strings.NewReader(`{"slide_id":"s1","path":"/tmp/s1.png","t_start_ms":100,"t_end_ms":120}` + "\n")
	var out bytes.Buffer
	if err := run(in, &out); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "s1") {
		t.Fatalf("summary missing slide_id: %q", out.String())
	}
	if !strings.Contains(out.String(), "processed 1") {
		t.Fatalf("summary missing count: %q", out.String())
	}
}

func TestRunRichEvent(t *testing.T) {
	evt := `{"slide_id":"deck-17","path":"/p/17.png","t_start_ms":5000,"t_end_ms":5500,` +
		`"ocr":[{"text":"Q3 revenue"}],"transcript_window":[{"text":"revenue is ten"}],` +
		`"frontmost_app":"Teams"}`
	in := strings.NewReader(evt + "\n")
	var out bytes.Buffer
	if err := run(in, &out); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "app=Teams") {
		t.Fatalf("summary missing frontmost_app: %q", out.String())
	}
}

func TestRunRejectsMissingSlideID(t *testing.T) {
	in := strings.NewReader(`{"path":"/p.png","t_start_ms":0,"t_end_ms":1}` + "\n")
	var out bytes.Buffer
	if err := run(in, &out); err == nil {
		t.Fatalf("expected error on missing slide_id")
	} else if !strings.Contains(err.Error(), "slide_id") {
		t.Fatalf("error did not mention slide_id: %v", err)
	}
}

func TestRunRejectsTimeInversion(t *testing.T) {
	in := strings.NewReader(`{"slide_id":"s","path":"/p","t_start_ms":100,"t_end_ms":50}` + "\n")
	var out bytes.Buffer
	if err := run(in, &out); err == nil {
		t.Fatalf("expected error on inverted timestamps")
	}
}

func TestRunMultiEvent(t *testing.T) {
	events := []string{
		`{"slide_id":"s1","path":"/p1","t_start_ms":0,"t_end_ms":10}`,
		`{"slide_id":"s2","path":"/p2","t_start_ms":20,"t_end_ms":30}`,
		`{"slide_id":"s3","path":"/p3","t_start_ms":40,"t_end_ms":50}`,
	}
	in := strings.NewReader(strings.Join(events, "\n") + "\n")
	var out bytes.Buffer
	if err := run(in, &out); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "processed 3") {
		t.Fatalf("expected processed 3 in %q", out.String())
	}
}
