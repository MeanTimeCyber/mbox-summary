// Package mbox provides safe MIME attachment export helpers.
package mbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExportAttachments writes unique message attachments using generated safe filenames.
func ExportAttachments(directory string, message Message) (int, int64, error) {
	if len(message.Attachments) == 0 {
		return 0, 0, nil
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return 0, 0, err
	}

	exported := 0
	var exportedBytes int64
	messageID := strings.ToLower(strings.Trim(strings.TrimSpace(message.MessageID), "<>"))
	if len(messageID) > 12 {
		messageID = messageID[:12]
	}
	messageID = sanitizeFilename(messageID)
	if messageID == "" {
		messageID = "no-message-id"
	}
	date := "undated"
	if message.DateValid {
		date = message.Date.UTC().Format("20060102")
	}

	for index, attachment := range message.Attachments {
		name := sanitizeFilename(attachment.Name)
		if name == "" {
			name = "attachment"
		}
		base := fmt.Sprintf("%s-%s-%03d-%s", date, messageID, index+1, name)
		target, err := uniqueTarget(filepath.Join(directory, base))
		if err != nil {
			return exported, exportedBytes, err
		}
		if err := os.WriteFile(target, attachment.Data, 0o644); err != nil {
			return exported, exportedBytes, err
		}
		exported++
		exportedBytes += attachment.Size
	}
	return exported, exportedBytes, nil
}

// sanitizeFilename removes path and control characters and limits component length.
func sanitizeFilename(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "_"))
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.Map(func(r rune) rune {
		switch {
		case r < 32:
			return '_'
		case r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			return '_'
		default:
			return r
		}
	}, value)
	value = strings.Trim(value, ". ")
	if len(value) > 180 {
		extension := filepath.Ext(value)
		value = value[:180-len(extension)] + extension
	}
	return value
}

// uniqueTarget appends a counter only when the generated target already exists.
func uniqueTarget(target string) (string, error) {
	_, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return target, nil
	}
	if err != nil {
		return "", err
	}
	extension := filepath.Ext(target)
	stem := strings.TrimSuffix(target, extension)
	for counter := 2; ; counter++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, counter, extension)
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}
