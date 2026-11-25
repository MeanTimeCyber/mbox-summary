// Package report_test verifies standalone HTML report generation.
package report_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/model"
	"github.com/meantimecyber/mbox-summary/internal/report"
)

// TestWriteFileProducesStandaloneReport verifies HTML output and embedded report data.
func TestWriteFileProducesStandaloneReport(t *testing.T) {
	output := filepath.Join(t.TempDir(), "summary.html")
	summary := model.Summary{
		GeneratedAt:     time.Now().UTC(),
		Cutoff:          time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC),
		MessageCount:    42,
		OwnerIdentities: []string{"owner@example.com"},
		Weekly:          []model.PeriodStats{{Period: "2026-W47", Sent: 3, Received: 5}},
		Yearly:          []model.PeriodStats{{Period: "2024", Sent: 12, Received: 20}},
		TopSenders:      []model.RankEntry{{Address: "sender@example.net", Count: 7, Percentage: 100}},
	}

	if err := report.WriteFile(output, summary); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	html := string(data)
	for _, expected := range []string{"<!doctype html>", "Mailbox Summary", "2026-W47", "owner@example.com", "sender@example.net", "<canvas"} {
		if !strings.Contains(html, expected) {
			t.Errorf("HTML missing %q", expected)
		}
	}
}
