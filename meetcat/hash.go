// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"fmt"
)

// SessionIDHash returns the first 4 hex characters of SHA-256(id).
// This provides a short, opaque correlation handle for log events
// without exposing the raw session identifier.
func SessionIDHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%x", sum[:2])[:4]
}
