// Package sqliteexport_test verifies SQLite schema, append runs, and upserts.
package sqliteexport_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
	"github.com/meantimecyber/mbox-summary/internal/model"
	sqliteexport "github.com/meantimecyber/mbox-summary/internal/sqliteexport"
)

// TestSQLiteAppendRun verifies creation, email upsert, source linking, and aggregate storage.
func TestSQLiteAppendRun(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "Sent.mbox")
	content := "From owner@example.com Mon Nov 24 10:00:00 2025\nMessage-ID: <one@example.com>\nFrom: Owner <owner@example.com>\nTo: friend@example.net\nDate: Mon, 24 Nov 2025 10:00:00 +0000\nSubject: Test\n\nBody\n"
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := sqliteexport.Open(filepath.Join(directory, "summary.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	cutoff := time.Date(2025, 11, 24, 0, 0, 0, 0, time.UTC)
	runID, err := store.StartRun(directory, cutoff, []string{"owner@example.com"})
	if err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	source := mbox.SourceFile{Path: sourcePath, Folder: mbox.FolderSent, Size: sourceInfo.Size()}
	if err := store.UpsertSource(source, 1, 0); err != nil {
		t.Fatalf("UpsertSource() error = %v", err)
	}
	message := mbox.Message{
		Date:      cutoff,
		DateValid: true,
		MessageID: "<one@example.com>",
		Subject:   "Test",
		From:      mbox.Address{Address: "owner@example.com", Name: "Owner"},
		To:        []mbox.Address{{Address: "friend@example.net", Name: "Friend"}},
		Folder:    mbox.FolderSent,
	}
	if err := store.UpsertEmail(source, message, map[string]bool{"owner@example.com": true}, nil); err != nil {
		t.Fatalf("UpsertEmail() error = %v", err)
	}
	if err := store.StoreSummary(model.Summary{
		Weekly:     []model.PeriodStats{{Period: "2025-W48", Sent: 1, Received: 0}},
		Yearly:     []model.PeriodStats{},
		TopSenders: []model.RankEntry{{Address: "owner@example.com", Count: 1}},
	}); err != nil {
		t.Fatalf("StoreSummary() error = %v", err)
	}
	if err := store.FinishRun(); err != nil {
		t.Fatalf("FinishRun() error = %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(directory, "summary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, storedRun string
	if err := db.QueryRow("SELECT run_id,status FROM runs").Scan(&storedRun, &status); err != nil {
		t.Fatalf("query runs: %v", err)
	}
	if storedRun != runID || status != "completed" {
		t.Errorf("run = %q/%q, want %q/completed", storedRun, status, runID)
	}
	var emailCount, recipientCount, sourceCount, weeklyCount int
	if err := db.QueryRow("SELECT count(*) FROM emails").Scan(&emailCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM email_recipients").Scan(&recipientCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM source_files").Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM period_weekly").Scan(&weeklyCount); err != nil {
		t.Fatal(err)
	}
	if emailCount != 1 || recipientCount != 1 || sourceCount != 1 || weeklyCount != 1 {
		t.Errorf("counts emails/recipients/sources/weekly = %d/%d/%d/%d, want 1/1/1/1",
			emailCount, recipientCount, sourceCount, weeklyCount)
	}
	var bodyTable int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='email_bodies'").Scan(&bodyTable); err != nil {
		t.Fatal(err)
	}
	if bodyTable != 0 {
		t.Error("SQLite schema unexpectedly contains email_bodies")
	}
}
