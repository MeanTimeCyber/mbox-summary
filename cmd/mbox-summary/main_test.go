// Package main_test verifies command-line validation and identity flag collection.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunRejectsEmptyDirectory verifies that directory input discovery requires mailbox exports.
func TestRunRejectsEmptyDirectory(t *testing.T) {
	directory := t.TempDir()
	err := run([]string{"--input", directory, "--identity", "owner@example.com"})
	if err == nil || !strings.Contains(err.Error(), "contains no .mbox") {
		t.Fatalf("run() error = %v, want empty-directory discovery error", err)
	}
}

// TestRunRejectsInputAsOutput verifies accidental replacement of the source mailbox.
func TestRunRejectsInputAsOutput(t *testing.T) {
	input := filepath.Join(t.TempDir(), "mailbox.mbox")
	// This fixture intentionally is not a full valid message; the same-file check runs first.
	if err := os.WriteFile(input, []byte("From owner@example.com Mon Nov 24 10:00:00 2025\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := run([]string{"--input", input, "--output", input, "--identity", "owner@example.com"})
	if err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("run() error = %v, want same-file validation error", err)
	}
}

// TestIdentityFlagsRejectsEmptyValue verifies repeatable identity validation.
func TestIdentityFlagsRejectsEmptyValue(t *testing.T) {
	var identities identityFlags
	if err := identities.Set("  "); err == nil {
		t.Fatal("Set() error = nil, want empty identity error")
	}
}
