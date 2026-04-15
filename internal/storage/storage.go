package storage

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/sirupsen/logrus"
	_ "modernc.org/sqlite" // sqlite driver
)

type Storage struct {
	db       *sql.DB
	log      *logrus.Logger
	readOnly bool
}

type Email struct {
	UID         uint32     `json:"uid"`
	Mailbox     string     `json:"mailbox"`
	Subject     string     `json:"subject"`
	From        string     `json:"from"`
	To          []string   `json:"to"`
	Date        time.Time  `json:"date"`
	Size        uint32     `json:"size"`
	Flags       []string   `json:"flags"`
	GmailLabels []string   `json:"gmail_labels,omitempty"` // Gmail labels from X-GM-LABELS
	Body        []byte     `json:"body"`
	Headers     []byte     `json:"headers"`
	RawMessage  []byte     `json:"raw_message"`
	Synced      time.Time  `json:"synced"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

type MailboxState struct {
	Name        string    `json:"name"`
	UIDValidity uint32    `json:"uid_validity"`
	LastUID     uint32    `json:"last_uid"`
	LastSync    time.Time `json:"last_sync"`
}

type Option func(*Storage)

func WithReadOnly(readOnly bool) Option {
	return func(s *Storage) {
		if readOnly {
			s.readOnly = true
		}
	}
}

func New(path string, log *logrus.Logger, options ...Option) (*Storage, error) {
	s := &Storage{log: log, readOnly: false}

	for _, option := range options {
		option(s)
	}

	dsn := path
	if s.readOnly {
		dsn = fmt.Sprintf("file:%s?mode=ro", path)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	s.db = db

	if !s.readOnly {
		if err := s.initSchema(); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to initialize schema: %w", err)
		}
	}

	return s, nil
}

func (s *Storage) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS emails (
		mailbox TEXT NOT NULL,
		uid INTEGER NOT NULL,
		subject TEXT,
		from_addr TEXT,
		to_addrs TEXT,
		date INTEGER,
		size INTEGER,
		flags TEXT,
		gmail_labels TEXT,
		synced INTEGER,
		deleted_at INTEGER,
		PRIMARY KEY (mailbox, uid)
	);

	CREATE INDEX IF NOT EXISTS idx_emails_mailbox ON emails(mailbox);
	CREATE INDEX IF NOT EXISTS idx_emails_synced ON emails(synced);

	CREATE TABLE IF NOT EXISTS email_content (
		mailbox TEXT NOT NULL,
		uid INTEGER NOT NULL,
		body BLOB,
		headers BLOB,
		raw_message BLOB,
		PRIMARY KEY (mailbox, uid),
		FOREIGN KEY (mailbox, uid) REFERENCES emails(mailbox, uid) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS mailbox_state (
		name TEXT PRIMARY KEY,
		uid_validity INTEGER NOT NULL,
		last_uid INTEGER NOT NULL,
		last_sync INTEGER NOT NULL
	);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	return s.migrateAddDeletedAt()
}

// migrateAddDeletedAt adds the deleted_at column to older DBs that predate it,
// then ensures the supporting index exists.
func (s *Storage) migrateAddDeletedAt() error {
	var hasCol int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('emails') WHERE name = 'deleted_at'`).Scan(&hasCol)
	if err != nil {
		return fmt.Errorf("failed to check deleted_at column: %w", err)
	}
	if hasCol == 0 {
		if _, err := s.db.Exec(`ALTER TABLE emails ADD COLUMN deleted_at INTEGER`); err != nil {
			return fmt.Errorf("failed to add deleted_at column: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_emails_deleted_at ON emails(deleted_at)`); err != nil {
		return fmt.Errorf("failed to create deleted_at index: %w", err)
	}
	return nil
}

func (s *Storage) Close() error {
	return s.db.Close()
}

func compressData(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)

	if _, err := writer.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write compressed data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}

func decompressData(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer reader.Close()

	result, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read decompressed data: %w", err)
	}

	return result, nil
}

func (s *Storage) SaveEmail(email *Email) error {
	toJSON, err := json.Marshal(email.To)
	if err != nil {
		return fmt.Errorf("failed to marshal to addresses: %w", err)
	}

	flagsJSON, err := json.Marshal(email.Flags)
	if err != nil {
		return fmt.Errorf("failed to marshal flags: %w", err)
	}

	gmailLabelsJSON, err := json.Marshal(email.GmailLabels)
	if err != nil {
		return fmt.Errorf("failed to marshal gmail labels: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Insert metadata
	metadataQuery := `
	INSERT OR REPLACE INTO emails (
		mailbox, uid, subject, from_addr, to_addrs, date, size, flags, gmail_labels, synced
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = tx.Exec(metadataQuery,
		email.Mailbox,
		email.UID,
		email.Subject,
		email.From,
		string(toJSON),
		email.Date.Unix(),
		email.Size,
		string(flagsJSON),
		string(gmailLabelsJSON),
		email.Synced.Unix(),
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to insert email metadata: %w", err)
	}

	// Compress binary content
	compressedBody, err := compressData(email.Body)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to compress body: %w", err)
	}

	compressedHeaders, err := compressData(email.Headers)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to compress headers: %w", err)
	}

	compressedRawMessage, err := compressData(email.RawMessage)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to compress raw message: %w", err)
	}

	// Insert content
	contentQuery := `
	INSERT OR REPLACE INTO email_content (
		mailbox, uid, body, headers, raw_message
	) VALUES (?, ?, ?, ?, ?)`

	_, err = tx.Exec(contentQuery,
		email.Mailbox,
		email.UID,
		compressedBody,
		compressedHeaders,
		compressedRawMessage,
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to insert email content: %w", err)
	}

	return tx.Commit()
}

func (s *Storage) SaveEmailBatch(emails []*Email) error {
	if len(emails) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	metadataStmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO emails (
			mailbox, uid, subject, from_addr, to_addrs, date, size, flags, gmail_labels, synced
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to prepare metadata statement: %w", err)
	}
	defer metadataStmt.Close()

	contentStmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO email_content (
			mailbox, uid, body, headers, raw_message
		) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to prepare content statement: %w", err)
	}
	defer contentStmt.Close()

	for _, email := range emails {
		toJSON, err := json.Marshal(email.To)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to marshal to addresses: %w", err)
		}

		flagsJSON, err := json.Marshal(email.Flags)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to marshal flags: %w", err)
		}

		gmailLabelsJSON, err := json.Marshal(email.GmailLabels)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to marshal gmail labels: %w", err)
		}

		// Insert metadata
		_, err = metadataStmt.Exec(
			email.Mailbox,
			email.UID,
			email.Subject,
			email.From,
			string(toJSON),
			email.Date.Unix(),
			email.Size,
			string(flagsJSON),
			string(gmailLabelsJSON),
			email.Synced.Unix(),
		)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert email metadata: %w", err)
		}

		// Compress binary content
		compressedBody, err := compressData(email.Body)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to compress body: %w", err)
		}

		compressedHeaders, err := compressData(email.Headers)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to compress headers: %w", err)
		}

		compressedRawMessage, err := compressData(email.RawMessage)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to compress raw message: %w", err)
		}

		// Insert content
		_, err = contentStmt.Exec(
			email.Mailbox,
			email.UID,
			compressedBody,
			compressedHeaders,
			compressedRawMessage,
		)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert email content: %w", err)
		}
	}

	return tx.Commit()
}

func (s *Storage) GetEmail(mailbox string, uid uint32) (*Email, error) {
	query := `
		SELECT e.mailbox, e.uid, e.subject, e.from_addr, e.to_addrs, e.date, e.size, e.flags, e.gmail_labels, e.synced, e.deleted_at,
			   c.body, c.headers, c.raw_message
		FROM emails e
		LEFT JOIN email_content c ON e.mailbox = c.mailbox AND e.uid = c.uid
		WHERE e.mailbox = ? AND e.uid = ? AND e.deleted_at IS NULL
	`

	var email Email
	var toJSON, flagsJSON, gmailLabelsJSON string
	var dateUnix, syncedUnix int64
	var deletedAtUnix sql.NullInt64
	var compressedBody, compressedHeaders, compressedRawMessage []byte

	err := s.db.QueryRow(query, mailbox, uid).Scan(
		&email.Mailbox,
		&email.UID,
		&email.Subject,
		&email.From,
		&toJSON,
		&dateUnix,
		&email.Size,
		&flagsJSON,
		&gmailLabelsJSON,
		&syncedUnix,
		&deletedAtUnix,
		&compressedBody,
		&compressedHeaders,
		&compressedRawMessage,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get email: %w", err)
	}

	if err := json.Unmarshal([]byte(toJSON), &email.To); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to addresses: %w", err)
	}

	if err := json.Unmarshal([]byte(flagsJSON), &email.Flags); err != nil {
		return nil, fmt.Errorf("failed to unmarshal flags: %w", err)
	}

	if gmailLabelsJSON != "" && gmailLabelsJSON != "null" {
		if err := json.Unmarshal([]byte(gmailLabelsJSON), &email.GmailLabels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal gmail labels: %w", err)
		}
	}

	// Decompress binary content
	email.Body, err = decompressData(compressedBody)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress body: %w", err)
	}

	email.Headers, err = decompressData(compressedHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress headers: %w", err)
	}

	email.RawMessage, err = decompressData(compressedRawMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress raw message: %w", err)
	}

	email.Date = time.Unix(dateUnix, 0)
	email.Synced = time.Unix(syncedUnix, 0)
	if deletedAtUnix.Valid {
		t := time.Unix(deletedAtUnix.Int64, 0)
		email.DeletedAt = &t
	}

	return &email, nil
}

func (s *Storage) SaveMailboxState(state *MailboxState) error {
	query := `
		INSERT OR REPLACE INTO mailbox_state (name, uid_validity, last_uid, last_sync)
		VALUES (?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		state.Name,
		state.UIDValidity,
		state.LastUID,
		state.LastSync.Unix(),
	)

	return err
}

func (s *Storage) GetMailboxState(mailbox string) (*MailboxState, error) {
	query := `
		SELECT name, uid_validity, last_uid, last_sync
		FROM mailbox_state
		WHERE name = ?
	`

	var state MailboxState
	var lastSyncUnix int64

	err := s.db.QueryRow(query, mailbox).Scan(
		&state.Name,
		&state.UIDValidity,
		&state.LastUID,
		&lastSyncUnix,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get mailbox state: %w", err)
	}

	state.LastSync = time.Unix(lastSyncUnix, 0)

	return &state, nil
}

func (s *Storage) ListMailboxes() ([]string, error) {
	query := `SELECT name FROM mailbox_state ORDER BY name ASC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query mailboxes: %w", err)
	}
	defer rows.Close()

	var mailboxes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan mailbox: %w", err)
		}
		mailboxes = append(mailboxes, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating mailboxes: %w", err)
	}

	sort.Strings(mailboxes)

	return mailboxes, nil
}

func (s *Storage) CountMessages(mailbox string) (int, error) {
	query := `SELECT COUNT(*) FROM emails WHERE mailbox = ? AND deleted_at IS NULL`

	var count int
	err := s.db.QueryRow(query, mailbox).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count messages: %w", err)
	}

	return count, nil
}

func (s *Storage) ListEmails(mailbox string, limit, offset int) ([]*Email, error) {
	query := `
		SELECT mailbox, uid, subject, from_addr, to_addrs, date, size, flags, gmail_labels, synced
		FROM emails
		WHERE mailbox = ? AND deleted_at IS NULL
		ORDER BY uid DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.Query(query, mailbox, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query emails: %w", err)
	}
	defer rows.Close()

	var emails []*Email
	for rows.Next() {
		var email Email
		var toJSON, flagsJSON, gmailLabelsJSON string
		var dateUnix, syncedUnix int64

		err := rows.Scan(
			&email.Mailbox,
			&email.UID,
			&email.Subject,
			&email.From,
			&toJSON,
			&dateUnix,
			&email.Size,
			&flagsJSON,
			&gmailLabelsJSON,
			&syncedUnix,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan email: %w", err)
		}

		if err := json.Unmarshal([]byte(toJSON), &email.To); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to addresses: %w", err)
		}

		if err := json.Unmarshal([]byte(flagsJSON), &email.Flags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal flags: %w", err)
		}

		if gmailLabelsJSON != "" && gmailLabelsJSON != "null" {
			if err := json.Unmarshal([]byte(gmailLabelsJSON), &email.GmailLabels); err != nil {
				return nil, fmt.Errorf("failed to unmarshal gmail labels: %w", err)
			}
		}

		email.Date = time.Unix(dateUnix, 0)
		email.Synced = time.Unix(syncedUnix, 0)

		emails = append(emails, &email)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating emails: %w", err)
	}

	return emails, nil
}

// ListLiveUIDs returns UIDs for a mailbox that are not soft-deleted.
func (s *Storage) ListLiveUIDs(mailbox string) ([]uint32, error) {
	rows, err := s.db.Query(
		`SELECT uid FROM emails WHERE mailbox = ? AND deleted_at IS NULL`,
		mailbox,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query live uids: %w", err)
	}
	defer rows.Close()

	var uids []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("failed to scan uid: %w", err)
		}
		uids = append(uids, uid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating uids: %w", err)
	}
	return uids, nil
}

// PurgeDeletedBefore permanently removes soft-deleted emails whose deleted_at
// is older than the cutoff, from both the emails and email_content tables.
func (s *Storage) PurgeDeletedBefore(cutoff time.Time) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	cutoffUnix := cutoff.Unix()

	if _, err := tx.Exec(
		`DELETE FROM email_content
		 WHERE (mailbox, uid) IN (
			SELECT mailbox, uid FROM emails
			WHERE deleted_at IS NOT NULL AND deleted_at < ?
		 )`,
		cutoffUnix,
	); err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to purge email_content: %w", err)
	}

	res, err := tx.Exec(
		`DELETE FROM emails WHERE deleted_at IS NOT NULL AND deleted_at < ?`,
		cutoffUnix,
	)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to purge emails: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to read rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}
	return int(n), nil
}

// MarkDeleted soft-deletes the given UIDs in a mailbox, preserving any
// existing deleted_at timestamp so the original deletion time isn't overwritten.
func (s *Storage) MarkDeleted(mailbox string, uids []uint32, deletedAt time.Time) (int, error) {
	if len(uids) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	stmt, err := tx.Prepare(
		`UPDATE emails SET deleted_at = ? WHERE mailbox = ? AND uid = ? AND deleted_at IS NULL`,
	)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	ts := deletedAt.Unix()
	var total int64
	for _, uid := range uids {
		res, err := stmt.Exec(ts, mailbox, uid)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to mark deleted: %w", err)
		}
		n, _ := res.RowsAffected()
		total += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}
	return int(total), nil
}
