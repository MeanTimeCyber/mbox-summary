// Package sqliteexport writes metadata-only mailbox exports to SQLite.
package sqliteexport

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/mbox"
	"github.com/meantimecyber/mbox-summary/internal/model"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

// ExportedAttachment links a MIME attachment to an exported file.
type ExportedAttachment struct {
	Index       int
	Name        string
	ExportPath  string
	Size        int64
	ContentType string
	PayloadSHA  string
}

// Store owns an append/upsert SQLite database.
type Store struct {
	db              *sql.DB
	tx              *sql.Tx
	runID           string
	batchCount      int
	batchSize       int
	sourceIDs       map[string]string
	emailStmt       *sql.Stmt
	recipientDelete *sql.Stmt
	recipientStmt   *sql.Stmt
	threadStmt      *sql.Stmt
	emailThreadStmt *sql.Stmt
	attachmentStmt  *sql.Stmt
}

// Open creates or opens a SQLite database and initializes the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=30000"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA cache_size=-65536"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA temp_store=MEMORY"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA mmap_size=268435456"); err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, batchSize: 5000, sourceIDs: make(map[string]string)}
	if err := store.createSchema(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.prepareStatements(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the database connection.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	if store.emailStmt != nil {
		store.emailStmt.Close()
	}
	if store.recipientDelete != nil {
		store.recipientDelete.Close()
	}
	if store.recipientStmt != nil {
		store.recipientStmt.Close()
	}
	if store.threadStmt != nil {
		store.threadStmt.Close()
	}
	if store.emailThreadStmt != nil {
		store.emailThreadStmt.Close()
	}
	if store.attachmentStmt != nil {
		store.attachmentStmt.Close()
	}
	return store.db.Close()
}

// createSchema creates all tables and indexes.
func (store *Store) createSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS metadata (
 key TEXT PRIMARY KEY,
 value TEXT NOT NULL
);
INSERT OR REPLACE INTO metadata(key,value) VALUES('schema_version','1');

CREATE TABLE IF NOT EXISTS runs (
 run_id TEXT PRIMARY KEY,
 started_at TEXT NOT NULL,
 completed_at TEXT,
 cutoff TEXT,
 owner_addresses TEXT NOT NULL,
 input_path TEXT,
 tool_version TEXT,
 status TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS source_files (
 source_id TEXT PRIMARY KEY,
 path TEXT NOT NULL,
 folder TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 sha256 TEXT NOT NULL,
 message_count INTEGER NOT NULL DEFAULT 0,
 warning_count INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_path_sha ON source_files(path, sha256);

CREATE TABLE IF NOT EXISTS source_runs (
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 source_id TEXT NOT NULL REFERENCES source_files(source_id),
 message_count INTEGER NOT NULL,
 warning_count INTEGER NOT NULL,
 PRIMARY KEY(run_id, source_id)
);

CREATE TABLE IF NOT EXISTS emails (
 email_id TEXT PRIMARY KEY,
 message_id TEXT,
 fallback_key TEXT,
 date_utc TEXT,
 subject TEXT,
 sender_address TEXT,
 sender_name TEXT,
 folder TEXT,
 sent INTEGER NOT NULL DEFAULT 0,
 received INTEGER NOT NULL DEFAULT 0,
 mailing_list INTEGER NOT NULL DEFAULT 0,
 has_attachment INTEGER NOT NULL DEFAULT 0,
 subject_normalized TEXT,
 source_id TEXT,
 first_seen_run TEXT,
 updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_emails_date ON emails(date_utc);
CREATE INDEX IF NOT EXISTS idx_emails_sender ON emails(sender_address);
CREATE INDEX IF NOT EXISTS idx_emails_folder ON emails(folder);
CREATE UNIQUE INDEX IF NOT EXISTS idx_emails_message_id ON emails(message_id) WHERE message_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS email_recipients (
 email_id TEXT NOT NULL REFERENCES emails(email_id),
 recipient_type TEXT NOT NULL,
 address_normalized TEXT NOT NULL,
 display_name TEXT,
 PRIMARY KEY(email_id, recipient_type, address_normalized)
);
CREATE INDEX IF NOT EXISTS idx_recipients_address ON email_recipients(address_normalized);

CREATE TABLE IF NOT EXISTS threads (
 thread_id TEXT PRIMARY KEY,
 subject TEXT,
 first_seen TEXT,
 last_seen TEXT,
 mailing_list INTEGER NOT NULL DEFAULT 0,
 message_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS email_threads (
 email_id TEXT NOT NULL REFERENCES emails(email_id),
 thread_id TEXT NOT NULL REFERENCES threads(thread_id),
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 PRIMARY KEY(email_id, thread_id, run_id)
);

CREATE TABLE IF NOT EXISTS attachments (
 attachment_id TEXT PRIMARY KEY,
 email_id TEXT NOT NULL REFERENCES emails(email_id),
 filename TEXT,
 content_type TEXT,
 extension TEXT,
 size_bytes INTEGER NOT NULL,
 sha256 TEXT,
 payload_sha256 TEXT,
 export_path TEXT
);
CREATE INDEX IF NOT EXISTS idx_attachments_extension ON attachments(extension);

CREATE TABLE IF NOT EXISTS period_weekly (
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 period TEXT NOT NULL,
 sent INTEGER NOT NULL,
 received INTEGER NOT NULL,
 PRIMARY KEY(run_id, period)
);
CREATE TABLE IF NOT EXISTS period_yearly (
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 period TEXT NOT NULL,
 sent INTEGER NOT NULL,
 received INTEGER NOT NULL,
 PRIMARY KEY(run_id, period)
);
CREATE TABLE IF NOT EXISTS sender_rankings (
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 rank INTEGER NOT NULL,
 address TEXT NOT NULL,
 display_name TEXT,
 count INTEGER NOT NULL,
 mailing_list INTEGER NOT NULL,
 PRIMARY KEY(run_id, rank)
);
CREATE TABLE IF NOT EXISTS recipient_rankings (
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 rank INTEGER NOT NULL,
 address TEXT NOT NULL,
 display_name TEXT,
 count INTEGER NOT NULL,
 mailing_list INTEGER NOT NULL,
 PRIMARY KEY(run_id, rank)
);
CREATE TABLE IF NOT EXISTS folder_stats (
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 folder TEXT NOT NULL,
 messages INTEGER NOT NULL,
 sent INTEGER NOT NULL,
 received INTEGER NOT NULL,
 PRIMARY KEY(run_id, folder)
);
CREATE TABLE IF NOT EXISTS attachment_type_rankings (
 run_id TEXT NOT NULL REFERENCES runs(run_id),
 rank INTEGER NOT NULL,
 file_type TEXT NOT NULL,
 count INTEGER NOT NULL,
 size_bytes INTEGER NOT NULL,
 PRIMARY KEY(run_id, rank)
);`
	_, err := store.db.Exec(schema)
	if err != nil {
		return err
	}
	if err := store.ensureEmailAttachmentColumn(); err != nil {
		return err
	}
	if err := store.ensureAttachmentPayloadColumn(); err != nil {
		return err
	}
	var version string
	if err := store.db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil {
		return err
	}
	if version != fmt.Sprintf("%d", schemaVersion) {
		return fmt.Errorf("unsupported SQLite schema version %s", version)
	}
	return nil
}

// ensureEmailAttachmentColumn adds the attachment flag to databases created before it existed.
func (store *Store) ensureEmailAttachmentColumn() error {
	rows, err := store.db.Query("PRAGMA table_info(emails)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "has_attachment" {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = store.db.Exec("ALTER TABLE emails ADD COLUMN has_attachment INTEGER NOT NULL DEFAULT 0")
	return err
}

// ensureAttachmentPayloadColumn migrates attachment SHA-256 metadata.
func (store *Store) ensureAttachmentPayloadColumn() error {
	rows, err := store.db.Query("PRAGMA table_info(attachments)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "payload_sha256" {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		_, err := store.db.Exec("CREATE INDEX IF NOT EXISTS idx_attachments_payload_sha ON attachments(payload_sha256)")
		return err
	}
	_, err = store.db.Exec("ALTER TABLE attachments ADD COLUMN payload_sha256 TEXT")
	if err != nil {
		return err
	}
	_, err = store.db.Exec("CREATE INDEX IF NOT EXISTS idx_attachments_payload_sha ON attachments(payload_sha256)")
	return err
}

// prepareStatements prepares the high-frequency email write statements once.
func (store *Store) prepareStatements() error {
	var err error
	store.emailStmt, err = store.db.Prepare(`
INSERT INTO emails(email_id,message_id,fallback_key,date_utc,subject,sender_address,sender_name,
 folder,sent,received,mailing_list,has_attachment,subject_normalized,source_id,first_seen_run,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(email_id) DO UPDATE SET
 message_id=excluded.message_id, fallback_key=excluded.fallback_key, date_utc=excluded.date_utc,
 subject=excluded.subject, sender_address=excluded.sender_address, sender_name=excluded.sender_name,
 folder=excluded.folder, sent=MAX(emails.sent,excluded.sent),
 received=MAX(emails.received,excluded.received),
 mailing_list=MAX(emails.mailing_list,excluded.mailing_list),
 has_attachment=MAX(emails.has_attachment,excluded.has_attachment),
 subject_normalized=excluded.subject_normalized, source_id=emails.source_id,
 updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	store.recipientDelete, err = store.db.Prepare("DELETE FROM email_recipients WHERE email_id=?")
	if err != nil {
		return err
	}
	store.recipientStmt, err = store.db.Prepare(`
INSERT INTO email_recipients(email_id,recipient_type,address_normalized,display_name)
VALUES(?,?,?,?)
ON CONFLICT(email_id,recipient_type,address_normalized) DO UPDATE SET display_name=excluded.display_name`)
	if err != nil {
		return err
	}
	store.threadStmt, err = store.db.Prepare(`
INSERT INTO threads(thread_id,subject,first_seen,last_seen,mailing_list,message_count)
VALUES(?,?,?,?,?,1)
ON CONFLICT(thread_id) DO UPDATE SET
 subject=CASE WHEN threads.subject='' THEN excluded.subject ELSE threads.subject END,
 first_seen=CASE WHEN threads.first_seen='' OR excluded.first_seen<threads.first_seen THEN excluded.first_seen ELSE threads.first_seen END,
 last_seen=CASE WHEN threads.last_seen='' OR excluded.last_seen>threads.last_seen THEN excluded.last_seen ELSE threads.last_seen END,
 mailing_list=MAX(threads.mailing_list,excluded.mailing_list),
 message_count=threads.message_count+1`)
	if err != nil {
		return err
	}
	store.emailThreadStmt, err = store.db.Prepare(`
INSERT INTO email_threads(email_id,thread_id,run_id) VALUES(?,?,?)
ON CONFLICT(email_id,thread_id,run_id) DO NOTHING`)
	if err != nil {
		return err
	}
	store.attachmentStmt, err = store.db.Prepare(`
INSERT INTO attachments(attachment_id,email_id,filename,content_type,extension,size_bytes,sha256,payload_sha256,export_path)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(attachment_id) DO UPDATE SET
 filename=excluded.filename, content_type=excluded.content_type,
 extension=excluded.extension, size_bytes=excluded.size_bytes,
 sha256=excluded.sha256, payload_sha256=excluded.payload_sha256,
 export_path=excluded.export_path`)
	return err
}

// UUID returns a timestamp-bearing random run identifier.
func uuid() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// StartRun creates one timestamped run row.
func (store *Store) StartRun(inputPath string, cutoff time.Time, ownerAddresses []string) (string, error) {
	store.runID = uuid()
	owners, err := json.Marshal(ownerAddresses)
	if err != nil {
		return "", err
	}
	_, err = store.db.Exec(`
INSERT INTO runs(run_id,started_at,cutoff,owner_addresses,input_path,status)
VALUES(?,?,?,?,?,?)`,
		store.runID, time.Now().UTC().Format(time.RFC3339Nano),
		cutoff.UTC().Format(time.RFC3339), string(owners), inputPath, "running")
	if err != nil {
		return "", err
	}
	store.beginBatch()
	return store.runID, nil
}

// beginBatch starts a bounded write transaction.
func (store *Store) beginBatch() error {
	if store.tx != nil {
		return nil
	}
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	store.tx = tx
	store.batchCount = 0
	return nil
}

// CommitBatch commits bounded work and starts no new transaction.
func (store *Store) CommitBatch() error {
	if store.tx == nil {
		return nil
	}
	err := store.tx.Commit()
	store.tx = nil
	store.batchCount = 0
	return err
}

// RollbackBatch aborts the current bounded work.
func (store *Store) RollbackBatch() error {
	if store.tx == nil {
		return nil
	}
	err := store.tx.Rollback()
	store.tx = nil
	store.batchCount = 0
	return err
}

// maybeCommitBatch commits after 1000 writes.
func (store *Store) maybeCommitBatch() error {
	store.batchCount++
	if store.batchCount >= store.batchSize {
		return store.CommitBatch()
	}
	if store.tx == nil {
		return store.beginBatch()
	}
	return nil
}

// SetBatchSize changes the number of email writes per SQLite transaction.
func (store *Store) SetBatchSize(size int) error {
	if size < 100 || size > 100000 {
		return fmt.Errorf("SQLite batch size must be between 100 and 100000")
	}
	store.batchSize = size
	return nil
}

// SourceFingerprint returns a stable source ID and content SHA-256.
func SourceFingerprint(path string) (sourceID, contentSHA string, err error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", "", err
	}
	contentSHA = hex.EncodeToString(hash.Sum(nil))
	idHash := sha256.Sum256([]byte(absolute + "\x00" + contentSHA))
	sourceID = hex.EncodeToString(idHash[:])
	return sourceID, contentSHA, nil
}

// UpsertSource inserts source metadata and links it to the current run.
func (store *Store) UpsertSource(source mbox.SourceFile, messageCount, warningCount int) error {
	sourceID, contentSHA, err := SourceFingerprint(source.Path)
	if err != nil {
		return err
	}
	store.sourceIDs[source.Path] = sourceID
	if err := store.beginBatch(); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.tx.Exec(`
INSERT INTO source_files(source_id,path,folder,size_bytes,sha256,message_count,warning_count,updated_at)
VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(source_id) DO UPDATE SET
 path=excluded.path, folder=excluded.folder, size_bytes=excluded.size_bytes,
 sha256=excluded.sha256, message_count=excluded.message_count,
 warning_count=excluded.warning_count, updated_at=excluded.updated_at`,
		sourceID, source.Path, string(source.Folder), source.Size, contentSHA,
		messageCount, warningCount, now); err != nil {
		return err
	}
	if _, err := store.tx.Exec(`
INSERT INTO source_runs(run_id,source_id,message_count,warning_count)
VALUES(?,?,?,?)
ON CONFLICT(run_id,source_id) DO UPDATE SET
 message_count=excluded.message_count, warning_count=excluded.warning_count`,
		store.runID, sourceID, messageCount, warningCount); err != nil {
		return err
	}
	store.batchCount++
	return store.maybeCommitBatch()
}

// normalizeThreadKey derives a stable thread key from references or subject.
func normalizeThreadKey(message mbox.Message) (key, normalizedSubject string) {
	normalizedSubject = strings.ToLower(strings.TrimSpace(message.Subject))
	for {
		changed := false
		for _, prefix := range []string{"re:", "fwd:", "fw:"} {
			if strings.HasPrefix(normalizedSubject, prefix) {
				normalizedSubject = strings.TrimSpace(normalizedSubject[len(prefix):])
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	normalizedSubject = strings.Join(strings.Fields(normalizedSubject), " ")
	base := ""
	if len(message.References) > 0 {
		base = "ref:" + message.References[0]
	} else if len(message.InReplyTo) > 0 {
		base = "reply:" + message.InReplyTo[0]
	} else if message.MessageID != "" {
		base = "subject:" + normalizedSubject + ":" + message.MessageID
	} else if normalizedSubject != "" {
		base = "subject:" + normalizedSubject
	} else {
		base = "unknown:" + message.Date.UTC().Format(time.RFC3339) + ":" + message.From.Address
	}
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:]), normalizedSubject
}

// UpsertEmail inserts or updates one deduplicated email, recipients, thread link, and attachments.
func (store *Store) UpsertEmail(source mbox.SourceFile, message mbox.Message,
	ownerAddresses map[string]bool, exported []ExportedAttachment) error {
	emailKey, messageIDKey := mbox.KeyForMessage(message)
	_ = messageIDKey
	now := time.Now().UTC().Format(time.RFC3339Nano)
	sent := ownerAddresses[message.From.Address] || message.Folder == mbox.FolderSent || message.SentFolder
	received := false
	recipients := uniqueRecipientAddresses(message)
	for _, address := range recipients {
		if ownerAddresses[address.Address] {
			received = true
			break
		}
	}
	mailingList := message.ListHeaders["List-Id"] ||
		message.ListHeaders["List-Post"] ||
		message.ListHeaders["List-Unsubscribe"]
	hasAttachment := len(message.Attachments) > 0
	threadKey, normalizedSubject := normalizeThreadKey(message)
	sourceID := store.sourceIDs[source.Path]
	if sourceID == "" {
		var err error
		sourceID, _, err = SourceFingerprint(source.Path)
		if err != nil {
			return err
		}
		store.sourceIDs[source.Path] = sourceID
	}
	if err := store.beginBatch(); err != nil {
		return err
	}
	if _, err := store.tx.Exec("DELETE FROM attachments WHERE email_id=?", emailKey); err != nil {
		return err
	}
	dateUTC := ""
	if message.DateValid {
		dateUTC = message.Date.UTC().Format(time.RFC3339)
	}
	if _, err := store.tx.Stmt(store.emailStmt).Exec(
		emailKey, optionalMessageID(message.MessageID), emailKey,
		dateUTC, message.Subject, message.From.Address, message.From.Name,
		string(message.Folder), boolInt(sent), boolInt(received), boolInt(mailingList),
		boolInt(hasAttachment),
		normalizedSubject, sourceID, store.runID, now); err != nil {
		return err
	}
	for _, recipient := range recipients {
		recipientType := "to"
		if containsAddress(message.Cc, recipient.Address) {
			recipientType = "cc"
		}
		if containsAddress(message.Bcc, recipient.Address) {
			recipientType = "bcc"
		}
		if _, err := store.tx.Stmt(store.recipientStmt).Exec(
			emailKey, recipientType, recipient.Address, recipient.Name); err != nil {
			return err
		}
	}
	if _, err := store.tx.Stmt(store.threadStmt).Exec(
		threadKey, message.Subject, dateUTC, dateUTC, boolInt(mailingList)); err != nil {
		return err
	}
	if _, err := store.tx.Stmt(store.emailThreadStmt).Exec(
		emailKey, threadKey, store.runID); err != nil {
		return err
	}
	for _, attachment := range exported {
		attachmentSHA := attachment.PayloadSHA
		if attachmentSHA == "" {
			fallback := sha256.Sum256([]byte(attachment.Name))
			attachmentSHA = hex.EncodeToString(fallback[:])
		}
		attachmentID := fmt.Sprintf("%s:%03d", emailKey, attachment.Index)
		if _, err := store.tx.Stmt(store.attachmentStmt).Exec(
			attachmentID, emailKey, attachment.Name, attachment.ContentType,
			strings.TrimPrefix(strings.ToLower(filepath.Ext(attachment.Name)), "."),
			attachment.Size, attachmentSHA, attachment.PayloadSHA, attachment.ExportPath); err != nil {
			return err
		}
	}
	store.batchCount++
	return store.maybeCommitBatch()
}

// optionalMessageID returns a NULL-compatible message ID.
func optionalMessageID(value string) interface{} {
	value = strings.Trim(strings.TrimSpace(value), "<>")
	if value == "" {
		return nil
	}
	return strings.ToLower(value)
}

// boolInt converts a boolean to SQLite integer storage.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// uniqueRecipientAddresses combines To, Cc, and Bcc once per address.
func uniqueRecipientAddresses(message mbox.Message) []mbox.Address {
	seen := make(map[string]bool)
	result := make([]mbox.Address, 0)
	for _, group := range [][]mbox.Address{message.To, message.Cc, message.Bcc} {
		for _, address := range group {
			if address.Address == "" || seen[address.Address] {
				continue
			}
			seen[address.Address] = true
			result = append(result, address)
		}
	}
	return result
}

// containsAddress reports whether an address appears in a recipient group.
func containsAddress(addresses []mbox.Address, address string) bool {
	for _, candidate := range addresses {
		if candidate.Address == address {
			return true
		}
	}
	return false
}

// StoreSummary replaces run-specific aggregate and ranking tables.
func (store *Store) StoreSummary(summary model.Summary) error {
	if err := store.beginBatch(); err != nil {
		return err
	}
	deleteTables := []string{
		"period_weekly", "period_yearly", "sender_rankings",
		"recipient_rankings", "folder_stats", "attachment_type_rankings",
	}
	for _, table := range deleteTables {
		if _, err := store.tx.Exec("DELETE FROM "+table+" WHERE run_id=?", store.runID); err != nil {
			return err
		}
	}
	for _, period := range summary.Weekly {
		if _, err := store.tx.Exec(`
INSERT INTO period_weekly(run_id,period,sent,received) VALUES(?,?,?,?)`,
			store.runID, period.Period, period.Sent, period.Received); err != nil {
			return err
		}
	}
	for _, period := range summary.Yearly {
		if _, err := store.tx.Exec(`
INSERT INTO period_yearly(run_id,period,sent,received) VALUES(?,?,?,?)`,
			store.runID, period.Period, period.Sent, period.Received); err != nil {
			return err
		}
	}
	for rank, entry := range summary.TopSenders {
		if _, err := store.tx.Exec(`
INSERT INTO sender_rankings(run_id,rank,address,display_name,count,mailing_list)
VALUES(?,?,?,?,?,?)`,
			store.runID, rank+1, entry.Address, entry.Name, entry.Count, boolInt(entry.MailingList)); err != nil {
			return err
		}
	}
	for rank, entry := range summary.TopRecipients {
		if _, err := store.tx.Exec(`
INSERT INTO recipient_rankings(run_id,rank,address,display_name,count,mailing_list)
VALUES(?,?,?,?,?,?)`,
			store.runID, rank+1, entry.Address, entry.Name, entry.Count, boolInt(entry.MailingList)); err != nil {
			return err
		}
	}
	for _, folder := range summary.FolderStats {
		if _, err := store.tx.Exec(`
INSERT INTO folder_stats(run_id,folder,messages,sent,received) VALUES(?,?,?,?,?)`,
			store.runID, folder.Folder, folder.Messages, folder.Sent, folder.Received); err != nil {
			return err
		}
	}
	for rank, attachment := range summary.Attachments.TopSentTypes {
		if _, err := store.tx.Exec(`
INSERT INTO attachment_type_rankings(run_id,rank,file_type,count,size_bytes)
VALUES(?,?,?,?,?)`,
			store.runID, rank+1, attachment.FileType, attachment.Count, attachment.SizeBytes); err != nil {
			return err
		}
	}
	return store.CommitBatch()
}

// FinishRun marks a run completed.
func (store *Store) FinishRun() error {
	_, err := store.db.Exec(`
UPDATE runs SET completed_at=?, status='completed' WHERE run_id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), store.runID)
	return err
}

// FailRun marks a run failed.
func (store *Store) FailRun() error {
	_, err := store.db.Exec(`
UPDATE runs SET completed_at=?, status='failed' WHERE run_id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), store.runID)
	return err
}
