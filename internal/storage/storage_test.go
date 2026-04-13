package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage(t *testing.T) {
	log := logrus.New()
	log.SetOutput(logrus.StandardLogger().Out)

	t.Run("save and get email", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		s, err := New(dbPath, log)
		require.NoError(t, err)
		defer s.Close()

		rawMessage := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test Email\r\n\r\nTest body")
		email := &Email{
			UID:        123,
			Mailbox:    "INBOX",
			Subject:    "Test Email",
			From:       "sender@example.com",
			To:         []string{"recipient@example.com"},
			Date:       time.Now(),
			Size:       1024,
			Flags:      []string{"\\Seen"},
			Body:       []byte("Test body"),
			Headers:    []byte("Header: value\r\n"),
			RawMessage: rawMessage,
			Synced:     time.Now(),
		}

		err = s.SaveEmail(email)
		require.NoError(t, err)

		retrieved, err := s.GetEmail("INBOX", 123)
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, email.UID, retrieved.UID)
		assert.Equal(t, email.Mailbox, retrieved.Mailbox)
		assert.Equal(t, email.Subject, retrieved.Subject)
		assert.Equal(t, email.From, retrieved.From)
		assert.Equal(t, email.To, retrieved.To)
		assert.Equal(t, email.Body, retrieved.Body)
		assert.Equal(t, email.Headers, retrieved.Headers)
		assert.Equal(t, email.RawMessage, retrieved.RawMessage)
	})

	t.Run("save batch of emails", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		s, err := New(dbPath, log)
		require.NoError(t, err)
		defer s.Close()

		emails := []*Email{
			{
				UID:        1,
				Mailbox:    "INBOX",
				Subject:    "Email 1",
				From:       "sender1@example.com",
				To:         []string{"recipient@example.com"},
				Date:       time.Now(),
				Size:       100,
				RawMessage: []byte("From: sender1@example.com\r\n\r\nBody 1"),
			},
			{
				UID:        2,
				Mailbox:    "INBOX",
				Subject:    "Email 2",
				From:       "sender2@example.com",
				To:         []string{"recipient@example.com"},
				Date:       time.Now(),
				Size:       200,
				RawMessage: []byte("From: sender2@example.com\r\n\r\nBody 2"),
			},
			{
				UID:        3,
				Mailbox:    "INBOX",
				Subject:    "Email 3",
				From:       "sender3@example.com",
				To:         []string{"recipient@example.com"},
				Date:       time.Now(),
				Size:       300,
				RawMessage: []byte("From: sender3@example.com\r\n\r\nBody 3"),
			},
		}

		err = s.SaveEmailBatch(emails)
		require.NoError(t, err)

		for _, email := range emails {
			retrieved, err := s.GetEmail(email.Mailbox, email.UID)
			require.NoError(t, err)
			require.NotNil(t, retrieved)
			assert.Equal(t, email.UID, retrieved.UID)
			assert.Equal(t, email.Subject, retrieved.Subject)
			assert.Equal(t, email.From, retrieved.From)
			assert.Equal(t, email.RawMessage, retrieved.RawMessage)
		}
	})

	t.Run("save empty batch", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		s, err := New(dbPath, log)
		require.NoError(t, err)
		defer s.Close()

		err = s.SaveEmailBatch([]*Email{})
		require.NoError(t, err)
	})

	t.Run("get non-existent email", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		s, err := New(dbPath, log)
		require.NoError(t, err)
		defer s.Close()

		email, err := s.GetEmail("INBOX", 999)
		require.NoError(t, err)
		assert.Nil(t, email)
	})

	t.Run("save and get mailbox state", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		s, err := New(dbPath, log)
		require.NoError(t, err)
		defer s.Close()

		state := &MailboxState{
			Name:        "INBOX",
			UIDValidity: 123456,
			LastUID:     100,
			LastSync:    time.Now(),
		}

		err = s.SaveMailboxState(state)
		require.NoError(t, err)

		retrieved, err := s.GetMailboxState("INBOX")
		require.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, state.Name, retrieved.Name)
		assert.Equal(t, state.UIDValidity, retrieved.UIDValidity)
		assert.Equal(t, state.LastUID, retrieved.LastUID)
	})

	t.Run("get non-existent mailbox state", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		s, err := New(dbPath, log)
		require.NoError(t, err)
		defer s.Close()

		state, err := s.GetMailboxState("NonExistent")
		require.NoError(t, err)
		assert.Nil(t, state)
	})

	t.Run("list mailboxes", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")
		s, err := New(dbPath, log)
		require.NoError(t, err)
		defer s.Close()

		states := []*MailboxState{
			{Name: "INBOX", UIDValidity: 1, LastUID: 10, LastSync: time.Now()},
			{Name: "Sent", UIDValidity: 2, LastUID: 20, LastSync: time.Now()},
			{Name: "Drafts", UIDValidity: 3, LastUID: 30, LastSync: time.Now()},
		}

		for _, state := range states {
			err = s.SaveMailboxState(state)
			require.NoError(t, err)
		}

		mailboxes, err := s.ListMailboxes()
		require.NoError(t, err)
		assert.Len(t, mailboxes, 3)
		assert.Contains(t, mailboxes, "INBOX")
		assert.Contains(t, mailboxes, "Sent")
		assert.Contains(t, mailboxes, "Drafts")
	})
}

func TestCountMessages(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := New(dbPath, log)
	require.NoError(t, err)
	defer s.Close()

	t.Run("count messages in empty mailbox", func(t *testing.T) {
		count, err := s.CountMessages("INBOX")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("count messages after saving", func(t *testing.T) {
		emails := []*Email{
			{UID: 1, Mailbox: "INBOX", Subject: "Test 1", Synced: time.Now()},
			{UID: 2, Mailbox: "INBOX", Subject: "Test 2", Synced: time.Now()},
			{UID: 3, Mailbox: "Sent", Subject: "Test 3", Synced: time.Now()},
		}

		for _, email := range emails {
			err := s.SaveEmail(email)
			require.NoError(t, err)
		}

		inboxCount, err := s.CountMessages("INBOX")
		require.NoError(t, err)
		assert.Equal(t, 2, inboxCount)

		sentCount, err := s.CountMessages("Sent")
		require.NoError(t, err)
		assert.Equal(t, 1, sentCount)
	})

	t.Run("count messages in non-existent mailbox", func(t *testing.T) {
		count, err := s.CountMessages("NonExistent")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestCompression(t *testing.T) {
	t.Run("compress and decompress data", func(t *testing.T) {
		original := []byte(fmt.Sprintf("This is a test message with some content that should be compressed. %s", strings.Repeat("Repetitive data. ", 50)))

		compressed, err := compressData(original)
		require.NoError(t, err)
		assert.NotEmpty(t, compressed)
		assert.Less(t, len(compressed), len(original))

		decompressed, err := decompressData(compressed)
		require.NoError(t, err)
		assert.Equal(t, original, decompressed)
	})

	t.Run("compress empty data", func(t *testing.T) {
		original := []byte{}

		compressed, err := compressData(original)
		require.NoError(t, err)
		assert.Empty(t, compressed)

		decompressed, err := decompressData(compressed)
		require.NoError(t, err)
		assert.Empty(t, decompressed)
	})

	t.Run("compress large data", func(t *testing.T) {
		original := make([]byte, 10000)
		for i := range original {
			original[i] = byte(i % 256)
		}

		compressed, err := compressData(original)
		require.NoError(t, err)
		assert.NotEmpty(t, compressed)

		decompressed, err := decompressData(compressed)
		require.NoError(t, err)
		assert.Equal(t, original, decompressed)
	})

	t.Run("decompress invalid data", func(t *testing.T) {
		invalid := []byte("not gzip data")

		_, err := decompressData(invalid)
		assert.Error(t, err)
	})
}

func TestListEmails(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	tmpDir := t.TempDir()
	s, err := New(filepath.Join(tmpDir, "test.db"), log)
	require.NoError(t, err)
	defer s.Close()

	emails := []*Email{
		{
			UID:     1,
			Mailbox: "INBOX",
			Subject: "First",
			From:    "a@b.com",
			To:      []string{"c@d.com"},
			Date:    time.Now(),
			Size:    100,
			Synced:  time.Now(),
		},
		{
			UID:     2,
			Mailbox: "INBOX",
			Subject: "Second",
			From:    "a@b.com",
			To:      []string{"c@d.com"},
			Date:    time.Now(),
			Size:    200,
			Synced:  time.Now(),
		},
		{
			UID:     3,
			Mailbox: "INBOX",
			Subject: "Third",
			From:    "a@b.com",
			To:      []string{"c@d.com"},
			Date:    time.Now(),
			Size:    300,
			Synced:  time.Now(),
		},
		{
			UID:     1,
			Mailbox: "Sent",
			Subject: "Sent1",
			From:    "a@b.com",
			To:      []string{"c@d.com"},
			Date:    time.Now(),
			Size:    50,
			Synced:  time.Now(),
		},
	}

	for _, e := range emails {
		require.NoError(t, s.SaveEmail(e))
	}

	t.Run("list all inbox", func(t *testing.T) {
		result, err := s.ListEmails("INBOX", 10, 0)
		require.NoError(t, err)
		assert.Len(t, result, 3)
	})

	t.Run("list with limit", func(t *testing.T) {
		result, err := s.ListEmails("INBOX", 2, 0)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("list with offset", func(t *testing.T) {
		result, err := s.ListEmails("INBOX", 10, 2)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})

	t.Run("list different mailbox", func(t *testing.T) {
		result, err := s.ListEmails("Sent", 10, 0)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "Sent1", result[0].Subject)
	})

	t.Run("list empty mailbox", func(t *testing.T) {
		result, err := s.ListEmails("Drafts", 10, 0)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestEmailCompressionRoundTrip(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := New(dbPath, log)
	require.NoError(t, err)
	defer s.Close()

	originalEmail := &Email{
		UID:        123,
		Mailbox:    "INBOX",
		Subject:    "Test Subject",
		From:       "sender@example.com",
		To:         []string{"recipient@example.com"},
		Date:       time.Now().Truncate(time.Second),
		Size:       1024,
		Flags:      []string{"\\Seen"},
		Body:       []byte(fmt.Sprintf("This is a long email body that should benefit from compression. %s", strings.Repeat("Lorem ipsum dolor sit amet. ", 100))),
		Headers:    []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test\r\n\r\n"),
		RawMessage: []byte(fmt.Sprintf("Raw email message content that is quite long. %s", strings.Repeat("Data data data. ", 100))),
		Synced:     time.Now().Truncate(time.Second),
	}

	err = s.SaveEmail(originalEmail)
	require.NoError(t, err)

	retrievedEmail, err := s.GetEmail("INBOX", 123)
	require.NoError(t, err)
	require.NotNil(t, retrievedEmail)

	assert.Equal(t, originalEmail.UID, retrievedEmail.UID)
	assert.Equal(t, originalEmail.Mailbox, retrievedEmail.Mailbox)
	assert.Equal(t, originalEmail.Subject, retrievedEmail.Subject)
	assert.Equal(t, originalEmail.From, retrievedEmail.From)
	assert.Equal(t, originalEmail.To, retrievedEmail.To)
	assert.Equal(t, originalEmail.Body, retrievedEmail.Body)
	assert.Equal(t, originalEmail.Headers, retrievedEmail.Headers)
	assert.Equal(t, originalEmail.RawMessage, retrievedEmail.RawMessage)
}

func TestWithReadOnly(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a writable storage first to initialize the schema
	s, err := New(dbPath, log)
	require.NoError(t, err)
	require.NoError(t, s.SaveMailboxState(&MailboxState{
		Name:        "INBOX",
		UIDValidity: 1,
		LastUID:     1,
		LastSync:    time.Now(),
	}))
	s.Close()

	// Re-open in read-only mode
	sRO, err := New(dbPath, log, WithReadOnly(true))
	require.NoError(t, err)
	defer sRO.Close()

	mailboxes, err := sRO.ListMailboxes()
	require.NoError(t, err)
	assert.Contains(t, mailboxes, "INBOX")
}

func TestWithReadOnly_False(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// WithReadOnly(false) should have no effect (default is read-write)
	s, err := New(dbPath, log, WithReadOnly(false))
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.SaveMailboxState(&MailboxState{
		Name:        "INBOX",
		UIDValidity: 1,
		LastUID:     1,
		LastSync:    time.Now(),
	}))
}

func TestNew_ReadOnlyNonExistent(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	_, err := New("/nonexistent_dir_xyz/test.db", log, WithReadOnly(true))
	assert.Error(t, err)
}

func TestDecompressData_TruncatedGzip(t *testing.T) {
	original := []byte("data to compress for truncation test")
	compressed, err := compressData(original)
	require.NoError(t, err)
	require.Greater(t, len(compressed), 10)

	// Truncate to just the gzip header — gzip.NewReader succeeds but io.ReadAll fails
	truncated := compressed[:10]
	_, err = decompressData(truncated)
	assert.Error(t, err)
}

func TestSaveEmail_ClosedDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	s.Close()

	err = s.SaveEmail(&Email{UID: 1, Mailbox: "INBOX"})
	assert.Error(t, err)
}

func TestSaveEmail_ReadOnlyDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := New(dbPath, log)
	require.NoError(t, err)
	s.Close()

	sRO, err := New(dbPath, log, WithReadOnly(true))
	require.NoError(t, err)
	defer sRO.Close()

	err = sRO.SaveEmail(&Email{UID: 1, Mailbox: "INBOX", Subject: "test"})
	assert.Error(t, err)
}

func TestSaveEmailBatch_ClosedDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	s.Close()

	err = s.SaveEmailBatch([]*Email{{UID: 1, Mailbox: "INBOX"}})
	assert.Error(t, err)
}

func TestSaveEmailBatch_NoTable(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	defer s.Close()

	_, dropErr := s.db.Exec("DROP TABLE emails")
	require.NoError(t, dropErr)

	err = s.SaveEmailBatch([]*Email{{UID: 1, Mailbox: "INBOX"}})
	assert.Error(t, err)
}

func TestSaveEmailBatch_ReadOnlyDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := New(dbPath, log)
	require.NoError(t, err)
	s.Close()

	sRO, err := New(dbPath, log, WithReadOnly(true))
	require.NoError(t, err)
	defer sRO.Close()

	err = sRO.SaveEmailBatch([]*Email{{UID: 1, Mailbox: "INBOX", Subject: "test"}})
	assert.Error(t, err)
}

func TestGetEmail_ClosedDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	s.Close()

	_, err = s.GetEmail("INBOX", 1)
	assert.Error(t, err)
}

func TestGetEmail_CorruptedToAddrs(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	defer s.Close()

	_, execErr := s.db.Exec(
		`INSERT OR REPLACE INTO emails (mailbox, uid, subject, from_addr, to_addrs, date, size, flags, gmail_labels, synced)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"INBOX", 999, "Corrupted", "sender@example.com", "[invalid-json", 0, 0, "[]", "null", 0,
	)
	require.NoError(t, execErr)

	_, err = s.GetEmail("INBOX", 999)
	assert.Error(t, err)
}

func TestGetEmail_CorruptedFlags(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	defer s.Close()

	_, execErr := s.db.Exec(
		`INSERT OR REPLACE INTO emails (mailbox, uid, subject, from_addr, to_addrs, date, size, flags, gmail_labels, synced)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"INBOX", 998, "Corrupted Flags", "sender@example.com", "[]", 0, 0, "[invalid-flags", "null", 0,
	)
	require.NoError(t, execErr)

	_, err = s.GetEmail("INBOX", 998)
	assert.Error(t, err)
}

func TestGetEmail_CorruptedGmailLabels(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	defer s.Close()

	_, execErr := s.db.Exec(
		`INSERT OR REPLACE INTO emails (mailbox, uid, subject, from_addr, to_addrs, date, size, flags, gmail_labels, synced)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"INBOX", 997, "Corrupted Gmail Labels", "sender@example.com", "[]", 0, 0, "[]", "[invalid-labels", 0,
	)
	require.NoError(t, execErr)

	_, err = s.GetEmail("INBOX", 997)
	assert.Error(t, err)
}

func TestGetMailboxState_ClosedDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	s.Close()

	_, err = s.GetMailboxState("INBOX")
	assert.Error(t, err)
}

func TestListMailboxes_ClosedDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	s.Close()

	_, err = s.ListMailboxes()
	assert.Error(t, err)
}

func TestCountMessages_ClosedDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	s.Close()

	_, err = s.CountMessages("INBOX")
	assert.Error(t, err)
}

func TestListEmails_ClosedDB(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	s, err := New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	s.Close()

	_, err = s.ListEmails("INBOX", 10, 0)
	assert.Error(t, err)
}
