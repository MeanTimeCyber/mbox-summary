// Package mbox_test verifies mailbox parsing and owner identity inference.
package mbox_test

import (
	"strings"
	"testing"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
)

// TestParseExtractsMetadata verifies that addresses, dates, and list headers are extracted.
func TestParseExtractsMetadata(t *testing.T) {
	input := "From sender@example.net Mon Nov 24 10:00:00 2025\n" +
		"From: Sender <sender@example.net>\n" +
		"To: me@example.com, friend@example.org\n" +
		"Cc: Copy <copy@example.org>\n" +
		"Date: Mon, 24 Nov 2025 10:00:00 +0000\n" +
		"List-Id: Team <team.example.org>\n" +
		"X-Gmail-Labels: Inbox\n\n" +
		"Body\n"

	result, err := mbox.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Parse() message count = %d, want 1", len(result.Messages))
	}

	message := result.Messages[0]
	if message.From.Address != "sender@example.net" {
		t.Errorf("From address = %q, want sender@example.net", message.From.Address)
	}
	if len(message.To) != 2 || message.Cc[0].Address != "copy@example.org" {
		t.Errorf("recipient metadata = %#v / %#v", message.To, message.Cc)
	}
	if !message.ListHeaders["List-Id"] {
		t.Error("List-Id header was not detected")
	}
	if message.Date.Year() != 2025 {
		t.Errorf("Date year = %d, want 2025", message.Date.Year())
	}
	if !message.DateValid {
		t.Error("DateValid = false, want true")
	}
}

// TestParseRejectsFileWithoutMboxEnvelope verifies strict archive-envelope validation.
func TestParseRejectsFileWithoutMboxEnvelope(t *testing.T) {
	input := "From: sender@example.net\nSubject: no envelope\n\nBody\n"

	result, err := mbox.Parse(strings.NewReader(input))
	if err == nil {
		t.Fatalf("Parse() = %#v, nil; want invalid format error", result)
	}
}

// TestInferIdentityUsesFolderEvidence verifies that sent-folder metadata outranks frequency.
func TestInferIdentityUsesFolderEvidence(t *testing.T) {
	messages := []mbox.Message{
		{From: mbox.Address{Address: "owner@example.com"}, SentFolder: true},
		{From: mbox.Address{Address: "owner@example.com"}},
		{From: mbox.Address{Address: "other@example.com"}},
	}

	inference := mbox.InferIdentity(messages)
	if !inference.Confident {
		t.Error("InferIdentity() should be confident for dominant sent-folder evidence")
	}
	if inference.Address != "owner@example.com" {
		t.Errorf("infer address = %q, want owner@example.com", inference.Address)
	}
}

// TestInferIdentityRejectsAmbiguousFrequency verifies that a close tie remains ambiguous.
func TestInferIdentityRejectsAmbiguousFrequency(t *testing.T) {
	messages := []mbox.Message{
		{From: mbox.Address{Address: "owner-a@example.com"}},
		{From: mbox.Address{Address: "owner-a@example.com"}},
		{From: mbox.Address{Address: "owner-b@example.com"}},
		{From: mbox.Address{Address: "owner-b@example.com"}},
	}

	inference := mbox.InferIdentity(messages)
	if inference.Confident {
		t.Error("InferIdentity() should be ambiguous for a two-address tie")
	}
}
