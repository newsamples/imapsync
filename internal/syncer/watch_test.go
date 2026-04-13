package syncer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	imapClient "github.com/newsamples/imapsync/internal/imap"
	"github.com/newsamples/imapsync/internal/storage"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchWithInterval_ContextAlreadyCancelled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	s := &Syncer{log: log}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.watchWithInterval(ctx, time.Minute)
	assert.NoError(t, err)
}

func TestWatchWithInterval_CancelBeforeFirstTick(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	s := &Syncer{log: log}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- s.watchWithInterval(ctx, time.Hour)
	}()

	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("watchWithInterval did not return after context cancel")
	}
}

func TestWatchWithIdle_ContextAlreadyCancelled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	s := &Syncer{log: log}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.watchWithIdle(ctx)
	assert.NoError(t, err)
}

func TestWatch_ContextCancelledBeforeInitialSync(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	s := &Syncer{log: log}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// SyncAll with a nil client will fail, but since ctx is cancelled it returns nil.
	err := s.Watch(ctx, 0)
	assert.NoError(t, err)
}

func TestGetWatchMailboxList_NilGmailFilter(t *testing.T) {
	// getWatchMailboxList calls client.ListMailboxesWithContext which needs a
	// real connection; test the nil-filter path using the internal function only.
	mailboxes := []string{"Archive", "INBOX", "Sent"}
	result := prioritizeInbox(mailboxes)

	require.Equal(t, "INBOX", result[0])
}

func TestWatchWithInterval_TickerFires(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	client, err := imapClient.Connect(opts)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() }) //nolint:errcheck

	store, err := storage.New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() }) //nolint:errcheck

	s := New(client, store, log)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err = s.watchWithInterval(ctx, time.Millisecond)
	assert.NoError(t, err)
}

func BenchmarkWatchWithInterval_ContextCancelled(b *testing.B) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	s := &Syncer{log: log}

	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = s.watchWithInterval(ctx, time.Hour)
	}
}
