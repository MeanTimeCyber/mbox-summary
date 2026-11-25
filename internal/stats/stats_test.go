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

// TestSummarizeSeparatesMailingListRecipient verifies list metadata is applied to the list address only.
func TestSummarizeSeparatesMailingListRecipient(t *testing.T) {
	cutoff := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	message := mbox.Message{
		Date: cutoff,
		From: mbox.Address{Address: "sender@example.net"},
		To: []mbox.Address{
			{Address: "team@example.org"},
			{Address: "personal@example.net"},
		},
		ListHeaders:   map[string]bool{"List-Post": true},
		ListAddresses: []mbox.Address{{Address: "team@example.org"}},
	}

	summary := stats.Summarize([]mbox.Message{message}, []string{"me@example.com"}, cutoff, 50)
	teamFound := false
	personalFound := false
	for _, recipient := range summary.TopRecipients {
		if recipient.Address == "team@example.org" {
			teamFound = recipient.MailingList
		}
		if recipient.Address == "personal@example.net" {
			personalFound = !recipient.MailingList
		}
	}
	if !teamFound || !personalFound {
		t.Errorf("mailing-list separation team/personal = %v/%v, want true/true", teamFound, personalFound)
	}
}

// TestSummarizeBuildsTopConversationThreads verifies ID grouping and participant exclusion.
func TestSummarizeBuildsTopConversationThreads(t *testing.T) {
	cutoff := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	messages := []mbox.Message{
		{
			Date:       cutoff.AddDate(0, 0, -14),
			Subject:    "Re: Project update",
			MessageID:  "<one@example.com>",
			References: []string{"<root@example.com>"},
			From:       mbox.Address{Address: "owner@example.com", Name: "Owner"},
			To:         []mbox.Address{{Address: "friend@example.net", Name: "Friend"}},
		},
		{
			Date:      cutoff.AddDate(0, 0, -7),
			Subject:   "Re: Project update",
			MessageID: "<two@example.com>",
			InReplyTo: []string{"<root@example.com>"},
			From:      mbox.Address{Address: "friend@example.net", Name: "Friend"},
			To:        []mbox.Address{{Address: "owner@example.com", Name: "Owner"}},
			Cc:        []mbox.Address{{Address: "third@example.org", Name: "Third"}},
		},
	}

	summary := stats.Summarize(messages, []string{"owner@example.com"}, cutoff, 50)
	if len(summary.TopThreads) != 1 {
		t.Fatalf("top thread count = %d, want 1", len(summary.TopThreads))
	}
	thread := summary.TopThreads[0]
	if thread.MessageCount != 2 {
		t.Errorf("thread message count = %d, want 2", thread.MessageCount)
	}
	if thread.FirstSeen.IsZero() || thread.LastSeen.IsZero() {
		t.Errorf("thread dates = %v / %v, want both populated", thread.FirstSeen, thread.LastSeen)
	}
	if thread.ParticipantCount != 2 {
		t.Errorf("thread participant count = %d, want 2", thread.ParticipantCount)
	}
	for _, participant := range thread.Participants {
		if participant.Address == "owner@example.com" {
			t.Error("thread participants include the mailbox owner")
		}
	}
}

// TestSummarizeSentAttachmentTypes verifies only sent mail contributes to attachment totals.
func TestSummarizeSentAttachmentTypes(t *testing.T) {
	cutoff := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	messages := []mbox.Message{
		{
			Date:        cutoff,
			From:        mbox.Address{Address: "owner@example.com"},
			Attachments: []mbox.Attachment{{Name: "report.pdf", Size: 100}},
		},
		{
			Date:        cutoff,
			From:        mbox.Address{Address: "friend@example.net"},
			Attachments: []mbox.Attachment{{Name: "photo.jpg", Size: 500}},
		},
		{
			Date:        cutoff,
			From:        mbox.Address{Address: "owner@example.com"},
			Attachments: []mbox.Attachment{{Name: "notes.txt", Size: 50}},
		},
	}

	summary := stats.Summarize(messages, []string{"owner@example.com"}, cutoff, 50)
	if len(summary.Attachments.TopSentTypes) != 2 {
		t.Fatalf("sent attachment type count = %d, want 2", len(summary.Attachments.TopSentTypes))
	}
	total := int64(0)
	for _, typeStats := range summary.Attachments.TopSentTypes {
		total += typeStats.SizeBytes
		if typeStats.FileType == "jpg" {
			t.Error("received attachment photo.jpg appeared in sent-only totals")
		}
	}
	if total != 150 {
		t.Errorf("sent attachment total = %d, want 150", total)
	}
}
