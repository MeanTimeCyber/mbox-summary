// Package stats_test verifies period buckets, rankings, and identity classification.
package stats_test

import (
	"testing"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
	"github.com/meantimecyber/mbox-summary/internal/stats"
)

// TestSummarizeClassifiesSentReceivedAndHistoricalData verifies the core reporting periods.
func TestSummarizeClassifiesSentReceivedAndHistoricalData(t *testing.T) {
	cutoff := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	messages := []mbox.Message{
		{
			Date: cutoff,
			From: mbox.Address{Address: "me@example.com"},
			To:   []mbox.Address{{Address: "friend@example.net"}},
		},
		{
			Date: cutoff.AddDate(0, 0, -7),
			From: mbox.Address{Address: "friend@example.net"},
			To:   []mbox.Address{{Address: "me@example.com"}},
		},
		{
			Date:        time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC),
			From:        mbox.Address{Address: "old@example.net"},
			To:          []mbox.Address{{Address: "me@example.com"}},
			ListHeaders: map[string]bool{"List-Id": true},
		},
	}

	summary := stats.Summarize(messages, []string{"me@example.com"}, cutoff, 50)
	if summary.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", summary.MessageCount)
	}
	if len(summary.Weekly) != 104 {
		t.Errorf("weekly bucket count = %d, want 104", len(summary.Weekly))
	}
	if len(summary.Yearly) != 1 || summary.Yearly[0].Period != "2020" {
		t.Errorf("yearly buckets = %#v, want 2020", summary.Yearly)
	}

	sentFound := false
	receivedFound := false
	for _, period := range summary.Weekly {
		sentFound = sentFound || period.Sent > 0
		receivedFound = receivedFound || period.Received > 0
	}
	if !sentFound || !receivedFound {
		t.Errorf("weekly sent/received found = %v/%v, want true/true", sentFound, receivedFound)
	}
	if len(summary.TopRecipients) == 0 {
		t.Error("top recipient ranking is empty")
	}
}

// TestSummarizeDeduplicatesRecipientAddresses verifies that one message contributes once per address.
func TestSummarizeDeduplicatesRecipientAddresses(t *testing.T) {
	cutoff := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	message := mbox.Message{
		Date: cutoff,
		From: mbox.Address{Address: "sender@example.net"},
		To:   []mbox.Address{{Address: "me@example.com"}, {Address: "team@example.net"}},
		Cc:   []mbox.Address{{Address: "team@example.net"}},
	}

	summary := stats.Summarize([]mbox.Message{message}, []string{"me@example.com"}, cutoff, 50)
	if len(summary.TopRecipients) != 1 {
		t.Fatalf("recipient count = %d, want 1 after de-duplication", len(summary.TopRecipients))
	}
	if summary.TopRecipients[0].Address != "team@example.net" {
		t.Errorf("recipient address = %q, want team@example.net", summary.TopRecipients[0].Address)
	}
}

// TestSummarizeExcludesFutureMessages verifies that a fixed cutoff caps every ranking and period.
func TestSummarizeExcludesFutureMessages(t *testing.T) {
	cutoff := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	messages := []mbox.Message{
		{
			Date: cutoff.AddDate(0, 0, 1),
			From: mbox.Address{Address: "future@example.net"},
			To:   []mbox.Address{{Address: "me@example.com"}},
		},
	}

	summary := stats.Summarize(messages, []string{"me@example.com"}, cutoff, 50)
	if len(summary.TopSenders) != 0 {
		t.Errorf("top senders = %#v, want no future senders", summary.TopSenders)
	}
	if len(summary.TopRecipients) != 0 {
		t.Errorf("top recipients = %#v, want no future recipients", summary.TopRecipients)
	}
	for _, period := range summary.Weekly {
		if period.Sent != 0 || period.Received != 0 {
			t.Errorf("future message affected period %#v", period)
		}
	}
}
