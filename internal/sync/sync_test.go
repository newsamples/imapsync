package sync

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	imapClient "github.com/newsamples/imapsync/internal/imap"
	"github.com/newsamples/imapsync/internal/storage"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterUIDs(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	s := &Syncer{log: log, showProgress: false}

	t.Run("filter with start UID", func(t *testing.T) {
		uids := []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		result := s.filterUIDs(uids, 5)
		assert.Equal(t, []uint32{5, 6, 7, 8, 9, 10}, result)
	})

	t.Run("filter with start UID 1", func(t *testing.T) {
		uids := []uint32{1, 2, 3, 4, 5}
		result := s.filterUIDs(uids, 1)
		assert.Equal(t, uids, result)
	})

	t.Run("filter with high start UID", func(t *testing.T) {
		uids := []uint32{1, 2, 3}
		result := s.filterUIDs(uids, 100)
		assert.Empty(t, result)
	})

	t.Run("empty UIDs", func(t *testing.T) {
		uids := []uint32{}
		result := s.filterUIDs(uids, 1)
		assert.Empty(t, result)
	})
}

func TestConvertToEmail(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	s := &Syncer{log: log, showProgress: false}

	t.Run("convert message with envelope", func(t *testing.T) {
		msg := &imapClient.Message{
			UID:   123,
			Flags: []imap.Flag{imap.FlagSeen},
			Size:  1024,
			Envelope: &imap.Envelope{
				Subject: "Test Subject",
				Date:    time.Now(),
				From: []imap.Address{
					{Mailbox: "sender", Host: "example.com"},
				},
				To: []imap.Address{
					{Mailbox: "recipient", Host: "example.com"},
				},
			},
			Body:    []byte("Test body"),
			Headers: []byte("Header: value\r\n"),
		}

		email := s.convertToEmail("INBOX", msg)

		require.NotNil(t, email)
		assert.Equal(t, uint32(123), email.UID)
		assert.Equal(t, "INBOX", email.Mailbox)
		assert.Equal(t, "Test Subject", email.Subject)
		assert.Equal(t, "sender@example.com", email.From)
		assert.Equal(t, []string{"recipient@example.com"}, email.To)
		assert.Equal(t, uint32(1024), email.Size)
		assert.Equal(t, []string{"\\Seen"}, email.Flags)
		assert.Equal(t, []byte("Test body"), email.Body)
		assert.Equal(t, []byte("Header: value\r\n"), email.Headers)
	})

	t.Run("convert message without envelope", func(t *testing.T) {
		msg := &imapClient.Message{
			UID:     456,
			Flags:   []imap.Flag{},
			Size:    512,
			Body:    []byte("Body"),
			Headers: []byte("Headers"),
		}

		email := s.convertToEmail("Sent", msg)

		require.NotNil(t, email)
		assert.Equal(t, uint32(456), email.UID)
		assert.Equal(t, "Sent", email.Mailbox)
		assert.Empty(t, email.Subject)
		assert.Empty(t, email.From)
		assert.Empty(t, email.To)
	})
}

func TestUpdateMailboxState(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	store, err := storage.New(dbPath, log)
	require.NoError(t, err)
	defer store.Close()

	s := &Syncer{
		storage:      store,
		log:          log,
		showProgress: false,
	}

	err = s.updateMailboxState("INBOX", 12345, 100)
	require.NoError(t, err)

	state, err := store.GetMailboxState("INBOX")
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, "INBOX", state.Name)
	assert.Equal(t, uint32(12345), state.UIDValidity)
	assert.Equal(t, uint32(100), state.LastUID)
}

func TestPrioritizeInbox(t *testing.T) {
	t.Run("inbox in the middle", func(t *testing.T) {
		mailboxes := []string{"Archive", "Drafts", "INBOX", "Sent", "Spam"}
		result := prioritizeInbox(mailboxes)
		expected := []string{"INBOX", "Archive", "Drafts", "Sent", "Spam"}
		assert.Equal(t, expected, result)
	})

	t.Run("inbox at the end", func(t *testing.T) {
		mailboxes := []string{"Archive", "Drafts", "Sent", "INBOX"}
		result := prioritizeInbox(mailboxes)
		expected := []string{"INBOX", "Archive", "Drafts", "Sent"}
		assert.Equal(t, expected, result)
	})

	t.Run("inbox already first", func(t *testing.T) {
		mailboxes := []string{"INBOX", "Archive", "Drafts"}
		result := prioritizeInbox(mailboxes)
		assert.Equal(t, mailboxes, result)
	})

	t.Run("no inbox", func(t *testing.T) {
		mailboxes := []string{"Archive", "Drafts", "Sent"}
		result := prioritizeInbox(mailboxes)
		assert.Equal(t, mailboxes, result)
	})

	t.Run("empty list", func(t *testing.T) {
		mailboxes := []string{}
		result := prioritizeInbox(mailboxes)
		assert.Empty(t, result)
	})

	t.Run("only inbox", func(t *testing.T) {
		mailboxes := []string{"INBOX"}
		result := prioritizeInbox(mailboxes)
		assert.Equal(t, mailboxes, result)
	})
}
