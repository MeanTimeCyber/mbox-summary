// Package mbox provides safe MIME attachment export helpers.
package mbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExportedAttachment describes one attachment payload written to disk.
type ExportedAttachment struct {
	Index       int
	Name        string
	ExportPath  string
	Size        int64
	ContentType string
	PayloadSHA  string
}

// PayloadExportRegistry deduplicates exported payloads within one processing run.
type PayloadExportRegistry struct {
	directory string
	paths     map[string]string
}

// NewPayloadExportRegistry creates a run-local payload export registry.
func NewPayloadExportRegistry(directory string) *PayloadExportRegistry {
	return &PayloadExportRegistry{directory: directory, paths: make(map[string]string)}
}

// ExportPayloads writes or reuses one copy per payload SHA within this run.
func (registry *PayloadExportRegistry) ExportPayloads(message Message) ([]ExportedAttachment, error) {
	result := make([]ExportedAttachment, 0, len(message.Attachments))
	if len(message.Attachments) == 0 {
		return result, nil
	}
	if err := os.MkdirAll(registry.directory, 0o755); err != nil {
		return nil, err
	}

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
		payloadHash := sha256.Sum256(attachment.Data)
		payloadSHA := hex.EncodeToString(payloadHash[:])
		name := sanitizeFilename(attachment.Name)
		if name == "" {
			name = "attachment"
		}

		target, known := registry.paths[payloadSHA]
		if known {
			if verifyExport(target, payloadHash) {
				result = append(result, ExportedAttachment{
					Index: index + 1, Name: attachment.Name, ExportPath: target,
					Size: attachment.Size, ContentType: attachment.ContentType, PayloadSHA: payloadSHA,
				})
				continue
			}
			if err := os.WriteFile(target, attachment.Data, 0o644); err != nil {
				return nil, err
			}
		} else {
			base := fmt.Sprintf("%s-%s-%03d-%s", date, messageID, index+1, name)
			var err error
			target, err = uniqueTarget(filepath.Join(registry.directory, base))
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, attachment.Data, 0o644); err != nil {
				return nil, err
			}
			registry.paths[payloadSHA] = target
		}
		result = append(result, ExportedAttachment{
			Index: index + 1, Name: attachment.Name, ExportPath: target,
			Size: attachment.Size, ContentType: attachment.ContentType, PayloadSHA: payloadSHA,
		})
	}
	return result, nil
}

// verifyExport checks that an existing payload file matches its SHA-256.
func verifyExport(path string, expected [32]byte) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false
	}
	return string(hash.Sum(nil)) == string(expected[:])
}

// ExportAttachmentsDetailed writes payloads and returns their generated paths.
func ExportAttachmentsDetailed(directory string, message Message) ([]ExportedAttachment, error) {
	registry := NewPayloadExportRegistry(directory)
	return registry.ExportPayloads(message)
}

// ExportAttachments writes unique message attachments using generated safe filenames.
func ExportAttachments(directory string, message Message) (int, int64, error) {
	exported, err := ExportAttachmentsDetailed(directory, message)
	if err != nil {
		return 0, 0, err
	}
	size := int64(0)
	for _, attachment := range exported {
		size += attachment.Size
	}
	return len(exported), size, nil
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
