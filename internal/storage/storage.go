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

	_ "modernc.org/sqlite" // sqlite driver
	"github.com/sirupsen/logrus"
)

type Storage struct {
	db       *sql.DB
	log      *logrus.Logger
	readOnly bool
}

type Email struct {
	UID        uint32    `json:"uid"`
	Mailbox    string    `json:"mailbox"`
	Subject    string    `json:"subject"`
	From       string    `json:"from"`
	To         []string  `json:"to"`
	Date       time.Time `json:"date"`
	Size       uint32    `json:"size"`
	Flags      []string  `json:"flags"`
	Body       []byte    `json:"body"`
	Headers    []byte    `json:"headers"`
	RawMessage []byte    `json:"raw_message"`
	Synced     time.Time `json:"synced"`
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
		dsn = "file:" + path + "?mode=ro"
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
		synced INTEGER,
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

	_, err := s.db.Exec(schema)
	return err
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

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Insert metadata
	metadataQuery := `
	INSERT OR REPLACE INTO emails (
		mailbox, uid, subject, from_addr, to_addrs, date, size, flags, synced
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = tx.Exec(metadataQuery,
		email.Mailbox,
		email.UID,
		email.Subject,
		email.From,
		string(toJSON),
		email.Date.Unix(),
		email.Size,
		string(flagsJSON),
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
			mailbox, uid, subject, from_addr, to_addrs, date, size, flags, synced
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		SELECT e.mailbox, e.uid, e.subject, e.from_addr, e.to_addrs, e.date, e.size, e.flags, e.synced,
			   c.body, c.headers, c.raw_message
		FROM emails e
		LEFT JOIN email_content c ON e.mailbox = c.mailbox AND e.uid = c.uid
		WHERE e.mailbox = ? AND e.uid = ?
	`

	var email Email
	var toJSON, flagsJSON string
	var dateUnix, syncedUnix int64
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
		&syncedUnix,
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
	query := `SELECT COUNT(*) FROM emails WHERE mailbox = ?`

	var count int
	err := s.db.QueryRow(query, mailbox).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count messages: %w", err)
	}

	return count, nil
}

func (s *Storage) ListEmails(mailbox string, limit, offset int) ([]*Email, error) {
	query := `
		SELECT mailbox, uid, subject, from_addr, to_addrs, date, size, flags, synced
		FROM emails
		WHERE mailbox = ?
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
		var toJSON, flagsJSON string
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

		email.Date = time.Unix(dateUnix, 0)
		email.Synced = time.Unix(syncedUnix, 0)

		emails = append(emails, &email)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating emails: %w", err)
	}

	return emails, nil
}
