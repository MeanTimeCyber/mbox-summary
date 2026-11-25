// Package mbox_test verifies Thunderbird folder discovery and duplicate detection.
package mbox_test

import (
	"os"
	"path/filepath"
	"testing"

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
