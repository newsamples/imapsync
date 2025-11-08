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
