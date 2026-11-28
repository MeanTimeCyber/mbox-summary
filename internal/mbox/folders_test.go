// Package mbox_test verifies Thunderbird folder discovery and duplicate detection.
package mbox_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
)

// TestDiscoverRecursiveMailboxFiles verifies flat and nested folder exports.
func TestDiscoverRecursiveMailboxFiles(t *testing.T) {
	root := t.TempDir()
	inboxDir := filepath.Join(root, "Inbox")
	sentDir := filepath.Join(root, "Mail", "Sent")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(inboxDir, "messages.mbox")
	sent := filepath.Join(sentDir, "messages.mbx")
	if err := os.WriteFile(inbox, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sent, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignore.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	sources, err := mbox.Discover(root, nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("Discover() source count = %d, want 2", len(sources))
	}

	foundSent := false
	for _, source := range sources {
		if source.Folder == mbox.FolderSent {
			foundSent = true
		}
	}
	if !foundSent {
		t.Error("Sent folder was not inferred from a nested export path")
	}
}

// TestDiscoverExplicitFolderMapping verifies explicit file and directory mappings.
func TestDiscoverExplicitFolderMapping(t *testing.T) {
	root := t.TempDir()
	exportDir := filepath.Join(root, "Export")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mailbox := filepath.Join(exportDir, "mail.mbox")
	if err := os.WriteFile(mailbox, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	sources, err := mbox.Discover(root, map[string]string{"Sent": exportDir})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 1 || sources[0].Folder != mbox.FolderSent {
		t.Fatalf("Discover() = %#v, want explicit Sent folder", sources)
	}
}

// TestFolderRoleAndPathInference verifies common Thunderbird folder names.
func TestFolderRoleAndPathInference(t *testing.T) {
	tests := []struct {
		path string
		want mbox.FolderRole
	}{
		{"/exports/Inbox.mbox", mbox.FolderInbox},
		{"/exports/Sent Items.mbox", mbox.FolderSent},
		{"/exports/Drafts/messages.mbx", mbox.FolderDraft},
		{"/exports/Archive.mbox", mbox.FolderArchive},
		{"/exports/unknown.mbox", mbox.FolderUnknown},
	}
	for _, test := range tests {
		if got := mbox.FolderFromPath(test.path); got != test.want {
			t.Errorf("FolderFromPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

// TestDiscoverExtensionlessFilesByMagic verifies that Thunderbird exports named Inbox, Sent, and Drafts are found.
func TestDiscoverExtensionlessFilesByMagic(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Inbox", "Sent", "Drafts"} {
		path := filepath.Join(root, name)
		content := "From sender@example.com Mon Nov 24 10:00:00 2025\n" +
			"From: Sender <sender@example.com>\nDate: Mon, 24 Nov 2025 10:00:00 +0000\n\nBody\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not an mbox export\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sources, err := mbox.Discover(root, nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 3 {
		t.Fatalf("Discover() source count = %d, want 3; got %#v", len(sources), sources)
	}

	roles := make(map[mbox.FolderRole]bool)
	for _, source := range sources {
		roles[source.Folder] = true
	}
	for _, role := range []mbox.FolderRole{mbox.FolderInbox, mbox.FolderSent, mbox.FolderDraft} {
		if !roles[role] {
			t.Errorf("Discover() did not infer folder role %q", role)
		}
	}
}

// TestDiscoverSingleExtensionlessMailbox verifies file magic works for direct file input.
func TestDiscoverSingleExtensionlessMailbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox")
	content := "From sender@example.com Mon Nov 24 10:00:00 2025\nFrom: Sender <sender@example.com>\n\nBody\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	sources, err := mbox.Discover(path, nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 1 || sources[0].Folder != mbox.FolderInbox {
		t.Fatalf("Discover() = %#v, want one inferred Inbox file", sources)
	}
}

// TestPayloadExportRegistryDeduplicatesPayloads verifies identical payload bytes export once.
func TestPayloadExportRegistryDeduplicatesPayloads(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "attachments")
	registry := mbox.NewPayloadExportRegistry(directory)
	first := mbox.Message{
		DateValid: true,
		Date:      time.Date(2025, 11, 24, 10, 0, 0, 0, time.UTC),
		MessageID: "<first@example.com>",
		Attachments: []mbox.Attachment{
			{Name: "signature.png", ContentType: "image/png", Size: 5, Data: []byte("same")},
		},
	}
	second := mbox.Message{
		DateValid: true,
		Date:      time.Date(2025, 11, 25, 10, 0, 0, 0, time.UTC),
		MessageID: "<second@example.com>",
		Attachments: []mbox.Attachment{
			{Name: "logo.png", ContentType: "image/png", Size: 5, Data: []byte("same")},
		},
	}

	firstExport, err := registry.ExportPayloads(first)
	if err != nil {
		t.Fatalf("first ExportPayloads() error = %v", err)
	}
	secondExport, err := registry.ExportPayloads(second)
	if err != nil {
		t.Fatalf("second ExportPayloads() error = %v", err)
	}
	if firstExport[0].ExportPath != secondExport[0].ExportPath {
		t.Errorf("duplicate payload paths = %q / %q, want equal", firstExport[0].ExportPath, secondExport[0].ExportPath)
	}
	if firstExport[0].PayloadSHA != secondExport[0].PayloadSHA {
		t.Errorf("duplicate payload hashes = %q / %q, want equal", firstExport[0].PayloadSHA, secondExport[0].PayloadSHA)
	}
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("exported file count = %d, want 1", len(files))
	}
}

// TestDedupeIndexMessageIDAndFallback verifies exact and fallback disk-backed keys.
func TestDedupeIndexMessageIDAndFallback(t *testing.T) {
	index, err := mbox.OpenDedupeIndex()
	if err != nil {
		t.Fatalf("OpenDedupeIndex() error = %v", err)
	}
	defer index.Close()

	withID := mbox.Message{MessageID: "<same@example.com>"}
	if unique, err := index.Add(withID); err != nil || !unique {
		t.Fatalf("first Add() = %v, %v; want true, nil", unique, err)
	}
	if unique, err := index.Add(withID); err != nil || unique {
		t.Fatalf("duplicate Add() = %v, %v; want false, nil", unique, err)
	}

	fallback := mbox.Message{
		Subject: "No ID",
		From:    mbox.Address{Address: "sender@example.com"},
		To:      []mbox.Address{{Address: "owner@example.com"}},
	}
	if unique, err := index.Add(fallback); err != nil || !unique {
		t.Fatalf("fallback first Add() = %v, %v; want true, nil", unique, err)
	}
	if unique, err := index.Add(fallback); err != nil || unique {
		t.Fatalf("fallback duplicate Add() = %v, %v; want false, nil", unique, err)
	}

	if index.UniqueMessages != 2 || index.DuplicateRecords != 2 {
		t.Errorf("dedupe totals = unique %d, duplicate %d; want 2, 2",
			index.UniqueMessages, index.DuplicateRecords)
	}
	if index.MessageIDKeys != 1 || index.FallbackKeys != 1 {
		t.Errorf("dedupe key totals = Message-ID %d, fallback %d; want 1, 1",
			index.MessageIDKeys, index.FallbackKeys)
	}
}

// TestExportAttachmentsCreatesSafeGeneratedNames verifies attachment export naming and counts.
func TestExportAttachmentsCreatesSafeGeneratedNames(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "attachments")
	message := mbox.Message{
		DateValid: true,
		Date:      time.Date(2025, 11, 24, 10, 0, 0, 0, time.UTC),
		MessageID: "<sender@EXAMPLE.com>",
		Attachments: []mbox.Attachment{
			{Name: "../report.pdf", ContentType: "application/pdf", Size: 5, Data: []byte("hello")},
		},
	}

	count, size, err := mbox.ExportAttachments(directory, message)
	if err != nil {
		t.Fatalf("ExportAttachments() error = %v", err)
	}
	if count != 1 || size != 5 {
		t.Errorf("export count/size = %d/%d, want 1/5", count, size)
	}
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("exported files = %d, want 1", len(files))
	}
	if !strings.Contains(files[0].Name(), "20251124-sender@exam") {
		t.Errorf("generated filename = %q, want date and truncated Message-ID", files[0].Name())
	}
}
