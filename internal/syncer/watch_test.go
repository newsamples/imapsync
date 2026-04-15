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

func TestWatch_WithInterval_CompletesInitialSync(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	s, _ := newTestSyncer(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := s.Watch(ctx, time.Hour)
	assert.NoError(t, err)
}

func TestWatch_WithIdle_CompletesInitialSync(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	s, _ := newTestSyncer(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := s.Watch(ctx, 0)
	assert.NoError(t, err)
}

func TestWatchWithIdle_CancelsDuringIdle(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	s, _ := newTestSyncer(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := s.watchWithIdle(ctx)
	assert.NoError(t, err)
}

func TestWatchWithIdle_PollsOtherMailboxesAfterIdleTimeout(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "Sent", 2)

	s, store := newTestSyncer(t, opts)

	origIdle := idleTimeout
	origPoll := defaultPollOther
	idleTimeout = 100 * time.Millisecond
	defaultPollOther = 50 * time.Millisecond
	t.Cleanup(func() {
		idleTimeout = origIdle
		defaultPollOther = origPoll
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := s.watchWithIdle(ctx)
	assert.NoError(t, err)

	sentCount, err := store.CountMessages("Sent")
	require.NoError(t, err)
	assert.Equal(t, 2, sentCount)
}

func TestPollOtherMailboxes_SyncsNonInboxFolders(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "Sent", 2)

	s, store := newTestSyncer(t, opts)
	s.pollOtherMailboxes(context.Background())

	sentCount, err := store.CountMessages("Sent")
	require.NoError(t, err)
	assert.Equal(t, 2, sentCount)

	inboxCount, err := store.CountMessages("INBOX")
	require.NoError(t, err)
	assert.Equal(t, 0, inboxCount)
}

func TestPollOtherMailboxes_ContextCancelled(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	s, _ := newTestSyncer(t, opts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.pollOtherMailboxes(ctx)
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
