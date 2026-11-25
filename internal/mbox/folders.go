// Package mbox includes mailbox discovery and Thunderbird folder classification.
package mbox

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FolderRole identifies a mailbox export folder's semantic role.
type FolderRole string

// FolderRole values used for sent/received and source classification.
const (
	FolderUnknown FolderRole = "Unknown"
	FolderInbox   FolderRole = "Inbox"
	FolderSent    FolderRole = "Sent"
	FolderDraft   FolderRole = "Draft"
	FolderArchive FolderRole = "Archive"
	FolderTrash   FolderRole = "Trash"
	FolderJunk    FolderRole = "Junk"
	FolderAllMail FolderRole = "All Mail"
)

// SourceFile describes a validated mailbox file and its inferred folder.
type SourceFile struct {
	Path   string
	Folder FolderRole
	Size   int64
}

// folderPriority sorts Sent exports before Inbox and other exports for deterministic deduplication.
func folderPriority(folder FolderRole) int {
	switch folder {
	case FolderSent:
		return 0
	case FolderInbox:
		return 1
	case FolderArchive, FolderAllMail:
		return 2
	case FolderDraft:
		return 3
	case FolderJunk:
		return 4
	case FolderTrash:
		return 5
	default:
		return 6
	}
}

// Discover validates a file or recursively discovers supported mailbox exports.
func Discover(input string, explicit map[string]string) ([]SourceFile, error) {
	info, err := os.Stat(input)
	if err != nil {
		return nil, err
	}
	if info.Mode().IsRegular() {
		if !isMailboxExtension(input) && !hasMboxMagic(input) {
			return nil, os.ErrInvalid
		}
		folder := FolderFromPath(input)
		if override, ok := matchingFolderOverride(explicit, input); ok {
			folder = override
		}
		return []SourceFile{{Path: input, Folder: folder, Size: info.Size()}}, nil
	}
	if !info.IsDir() {
		return nil, os.ErrInvalid
	}

	sources := make([]SourceFile, 0)
	err = filepath.WalkDir(input, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !isMailboxExtension(path) && !hasMboxMagic(path) {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return nil
		}
		folder := FolderFromPath(path)
		if override, ok := matchingFolderOverride(explicit, path); ok {
			folder = override
		}
		sources = append(sources, SourceFile{Path: path, Folder: folder, Size: fileInfo.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(sources, func(i, j int) bool {
		left, right := folderPriority(sources[i].Folder), folderPriority(sources[j].Folder)
		if left != right {
			return left < right
		}
		return sources[i].Path < sources[j].Path
	})
	if len(sources) == 0 {
		return nil, os.ErrNotExist
	}
	return sources, nil
}

// isMailboxExtension accepts Thunderbird mbox export conventions.
func isMailboxExtension(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mbox", ".mbx":
		return true
	default:
		return false
	}
}

// hasMboxMagic checks the beginning of a file for a valid Unix mbox separator.
// A 64 KiB bounded read is sufficient for the first envelope and avoids reading
// large mailboxes just to discover their file type.
func hasMboxMagic(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	reader := bufio.NewReaderSize(io.LimitReader(file, 64*1024), 4096)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return false
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if !strings.HasPrefix(line, "From ") {
		return false
	}

	fields := strings.Fields(line)
	if len(fields) < 3 {
		return false
	}
	if strings.Contains(line, "\x00") {
		return false
	}
	return true
}

// matchingFolderOverride matches an exact file or directory prefix.
func matchingFolderOverride(explicit map[string]string, path string) (FolderRole, bool) {
	cleanPath := filepath.Clean(path)
	for folderName, mappedPath := range explicit {
		cleanMapped := filepath.Clean(mappedPath)
		mappedInfo, err := os.Stat(cleanMapped)
		if err == nil && mappedInfo.IsDir() {
			relative, err := filepath.Rel(cleanMapped, cleanPath)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return ParseFolderRole(folderName), true
			}
		}
		if cleanMapped == cleanPath {
			return ParseFolderRole(folderName), true
		}
	}
	return FolderUnknown, false
}

// ParseFolderRole converts common folder labels to stable semantic roles.
func ParseFolderRole(value string) FolderRole {
	compact := strings.ToLower(strings.Join(strings.Fields(value), " "))
	switch compact {
	case "sent", "sent mail", "sent items", "sent-mail":
		return FolderSent
	case "inbox", "in", "received":
		return FolderInbox
	case "draft", "drafts":
		return FolderDraft
	case "archive", "archives", "archived":
		return FolderArchive
	case "trash", "deleted", "deleted items", "bin":
		return FolderTrash
	case "junk", "spam", "bulk":
		return FolderJunk
	case "all mail", "all-mail":
		return FolderAllMail
	default:
		return FolderUnknown
	}
}

// FolderFromPath uses basename and parent names to infer common Thunderbird folders.
func FolderFromPath(path string) FolderRole {
	clean := filepath.Clean(path)
	name := strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))
	if role := ParseFolderRole(name); role != FolderUnknown {
		return role
	}
	parent := filepath.Base(filepath.Dir(clean))
	if role := ParseFolderRole(parent); role != FolderUnknown {
		return role
	}
	return FolderUnknown
}
