package imap

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
)

func TestParseEnvelopeDate(t *testing.T) {
	t.Run("with valid date", func(t *testing.T) {
		now := time.Now()
		envelope := &imap.Envelope{
			Date: now,
		}

		result := ParseEnvelopeDate(envelope)
		assert.Equal(t, now, result)
	})

	t.Run("with nil envelope", func(t *testing.T) {
		result := ParseEnvelopeDate(nil)
		assert.WithinDuration(t, time.Now(), result, time.Second)
	})

	t.Run("with zero date", func(t *testing.T) {
		envelope := &imap.Envelope{
			Date: time.Time{},
		}

		result := ParseEnvelopeDate(envelope)
		assert.WithinDuration(t, time.Now(), result, time.Second)
	})
}

func TestFlagsToStrings(t *testing.T) {
	t.Run("convert flags", func(t *testing.T) {
		flags := []imap.Flag{
			imap.FlagSeen,
			imap.FlagAnswered,
			imap.FlagFlagged,
		}

		result := FlagsToStrings(flags)
		assert.Len(t, result, 3)
		assert.Contains(t, result, "\\Seen")
		assert.Contains(t, result, "\\Answered")
		assert.Contains(t, result, "\\Flagged")
	})

	t.Run("empty flags", func(t *testing.T) {
		flags := []imap.Flag{}
		result := FlagsToStrings(flags)
		assert.Len(t, result, 0)
	})
}

func TestIsNonSelectableMailbox(t *testing.T) {
	t.Run("non-selectable mailbox", func(t *testing.T) {
		attrs := []imap.MailboxAttr{imap.MailboxAttrNoSelect}
		result := isNonSelectableMailbox(attrs)
		assert.True(t, result)
	})

	t.Run("selectable mailbox", func(t *testing.T) {
		attrs := []imap.MailboxAttr{imap.MailboxAttrHasChildren}
		result := isNonSelectableMailbox(attrs)
		assert.False(t, result)
	})

	t.Run("empty attributes", func(t *testing.T) {
		attrs := []imap.MailboxAttr{}
		result := isNonSelectableMailbox(attrs)
		assert.False(t, result)
	})

	t.Run("mixed attributes with noselect", func(t *testing.T) {
		attrs := []imap.MailboxAttr{
			imap.MailboxAttrHasChildren,
			imap.MailboxAttrNoSelect,
		}
		result := isNonSelectableMailbox(attrs)
		assert.True(t, result)
	})
}

func TestIsGmailFolder(t *testing.T) {
	tests := []struct {
		name     string
		folder   string
		expected bool
	}{
		{"Gmail All Mail", "[Gmail]/All Mail", true},
		{"Gmail Sent Mail", "[Gmail]/Sent Mail", true},
		{"Google Mail Spam", "[Google Mail]/Spam", true},
		{"Google Mail Trash", "[Google Mail]/Trash", true},
		{"Regular INBOX", "INBOX", false},
		{"Regular folder", "Work", false},
		{"Empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsGmailFolder(tt.folder)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsGmailAllMail(t *testing.T) {
	tests := []struct {
		name     string
		folder   string
		expected bool
	}{
		{"Gmail All Mail", "[Gmail]/All Mail", true},
		{"Google Mail All Mail", "[Google Mail]/All Mail", true},
		{"Gmail Sent Mail", "[Gmail]/Sent Mail", false},
		{"INBOX", "INBOX", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsGmailAllMail(tt.folder)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetGmailFolderType(t *testing.T) {
	tests := []struct {
		name     string
		folder   string
		expected string
	}{
		{"Gmail All Mail", "[Gmail]/All Mail", "All Mail"},
		{"Gmail Sent Mail", "[Gmail]/Sent Mail", "Sent Mail"},
		{"Gmail Spam", "[Gmail]/Spam", "Spam"},
		{"Google Mail Trash", "[Google Mail]/Trash", "Trash"},
		{"INBOX", "INBOX", ""},
		{"Regular folder", "Work", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetGmailFolderType(tt.folder)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractGmailLabels(t *testing.T) {
	t.Run("no labels", func(t *testing.T) {
		flags := []imap.Flag{
			imap.FlagSeen,
			imap.FlagAnswered,
		}
		result := extractGmailLabels(flags)
		assert.Empty(t, result)
	})

	t.Run("with custom labels", func(t *testing.T) {
		flags := []imap.Flag{
			imap.FlagSeen,
			imap.Flag("\\Important"),
			imap.Flag("\\Work"),
			imap.FlagAnswered,
		}
		result := extractGmailLabels(flags)
		assert.Len(t, result, 2)
		assert.Contains(t, result, "Important")
		assert.Contains(t, result, "Work")
	})

	t.Run("only standard flags", func(t *testing.T) {
		flags := []imap.Flag{
			imap.FlagSeen,
			imap.FlagAnswered,
			imap.FlagFlagged,
			imap.FlagDeleted,
			imap.FlagDraft,
		}
		result := extractGmailLabels(flags)
		assert.Empty(t, result)
	})
}

func TestIsStandardIMAPFlag(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		expected bool
	}{
		{"Seen flag", "\\Seen", true},
		{"Answered flag", "\\Answered", true},
		{"Flagged flag", "\\Flagged", true},
		{"Deleted flag", "\\Deleted", true},
		{"Draft flag", "\\Draft", true},
		{"Recent flag", "\\Recent", true},
		{"Custom flag", "\\Important", false},
		{"Custom flag 2", "\\Work", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isStandardIMAPFlag(tt.flag)
			assert.Equal(t, tt.expected, result)
		})
	}
}
