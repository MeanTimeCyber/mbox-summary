// Package mbox implements disk-backed mailbox record deduplication.
package mbox

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"go.etcd.io/bbolt"
)

// DedupeIndex is a disk-backed index for exact message de-duplication.
type DedupeIndex struct {
	db               *bbolt.DB
	UniqueMessages   int
	DuplicateRecords int
	MessageIDKeys    int
	FallbackKeys     int
}

// OpenDedupeIndex creates a temporary Bolt database for cross-file duplicate detection.
func OpenDedupeIndex() (*DedupeIndex, error) {
	file, err := os.CreateTemp("", "mbox-summary-dedup-*.bolt")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{NoSync: false})
	if err != nil {
		os.Remove(path)
		return nil, err
	}
	index := &DedupeIndex{db: db}
	return index, index.init(path)
}

// init creates the deduplication bucket and retains the temporary path for cleanup.
func (index *DedupeIndex) init(path string) error {
	if err := index.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("dedupe"))
		return err
	}); err != nil {
		return err
	}
	return index.db.View(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte("dedupe")) == nil {
			return os.ErrInvalid
		}
		return nil
	})
}

// Close closes the database and removes its temporary file.
func (index *DedupeIndex) Close() error {
	if index == nil || index.db == nil {
		return nil
	}
	path := index.db.Path()
	err := index.db.Close()
	removeErr := os.Remove(path)
	if err != nil {
		return err
	}
	return removeErr
}

// KeyForMessage returns a stable Message-ID key or a SHA-256 fallback key.
func KeyForMessage(message Message) (key string, messageID bool) {
	if message.MessageID != "" {
		sum := sha256.Sum256([]byte(message.MessageID))
		return hex.EncodeToString(sum[:]), true
	}

	recipientAddresses := make([]string, 0)
	for _, address := range append(append(message.To, message.Cc...), message.Bcc...) {
		if address.Address != "" {
			recipientAddresses = append(recipientAddresses, address.Address)
		}
	}
	normalizedRecipients := strings.Join(recipientAddresses, ",")
	input := strings.Join([]string{
		message.Date.UTC().Format("2006-01-02T15:04:05Z"),
		message.From.Address,
		normalizedRecipients,
		strings.TrimSpace(message.Subject),
	}, "\x00")
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:]), false
}

// Add reports whether a message is unique. Files are processed in Sent-first order.
func (index *DedupeIndex) Add(message Message) (bool, error) {
	key, messageID := KeyForMessage(message)
	unique := false
	err := index.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("dedupe"))
		keyBytes := []byte(key)
		if bucket.Get(keyBytes) != nil {
			return nil
		}
		unique = true
		return bucket.Put(keyBytes, []byte("1"))
	})
	if err != nil {
		return false, err
	}
	if unique {
		index.UniqueMessages++
		if messageID {
			index.MessageIDKeys++
		} else {
			index.FallbackKeys++
		}
	} else {
		index.DuplicateRecords++
	}
	return unique, nil
}
