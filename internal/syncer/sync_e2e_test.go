package syncer

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/newsamples/imapsync/internal/config"
	imapClient "github.com/newsamples/imapsync/internal/imap"
	"github.com/newsamples/imapsync/internal/storage"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	syncTestUser = "syncuser"
	syncTestPass = "syncpass"
	syncTestMsg  = "MIME-Version: 1.0\r\nFrom: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Sync Test\r\nDate: Wed, 01 Jan 2025 12:00:00 +0000\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSync test body."
)

func newSyncTestServer(t *testing.T) (imapClient.ConnectOptions, func()) {
	t.Helper()

	mem := imapmemserver.New()
	u := imapmemserver.NewUser(syncTestUser, syncTestPass)
	require.NoError(t, u.Create("INBOX", nil))
	require.NoError(t, u.Create("Sent", nil))
	mem.AddUser(u)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(ln) //nolint:errcheck

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	opts := imapClient.ConnectOptions{
		Host:     "127.0.0.1",
		Port:     ln.Addr().(*net.TCPAddr).Port,
		Username: syncTestUser,
		Password: syncTestPass,
		Logger:   log,
	}
	return opts, func() { srv.Close() } //nolint:errcheck
}

func appendSyncMsgs(t *testing.T, opts imapClient.ConnectOptions, mailbox string, n int) {
	t.Helper()
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	c, err := imapclient.DialInsecure(addr, nil)
	require.NoError(t, err)
	defer func() { c.Logout().Wait() }() //nolint:errcheck
	require.NoError(t, c.Login(opts.Username, opts.Password).Wait())
	for range n {
		cmd := c.Append(mailbox, int64(len(syncTestMsg)), nil)
		_, err = cmd.Write([]byte(syncTestMsg))
		require.NoError(t, err)
		require.NoError(t, cmd.Close())
		_, err = cmd.Wait()
		require.NoError(t, err)
	}
}

func newTestSyncer(t *testing.T, opts imapClient.ConnectOptions) (*Syncer, *storage.Storage) {
	t.Helper()
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	client, err := imapClient.Connect(opts)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() }) //nolint:errcheck

	store, err := storage.New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() }) //nolint:errcheck

	return New(client, store, log), store
}

func TestSyncAll_EmptyMailboxes(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	s, _ := newTestSyncer(t, opts)
	require.NoError(t, s.SyncAll(context.Background()))
}

func TestSyncAll_WithMessages(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 3)
	appendSyncMsgs(t, opts, "Sent", 2)

	s, store := newTestSyncer(t, opts)
	require.NoError(t, s.SyncAll(context.Background()))

	inboxCount, err := store.CountMessages("INBOX")
	require.NoError(t, err)
	assert.Equal(t, 3, inboxCount)

	sentCount, err := store.CountMessages("Sent")
	require.NoError(t, err)
	assert.Equal(t, 2, sentCount)
}

func TestSyncAll_IncrementalSync(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 2)

	s, store := newTestSyncer(t, opts)
	require.NoError(t, s.SyncAll(context.Background()))

	count, err := store.CountMessages("INBOX")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Append more messages, sync again — only new ones should be synced.
	appendSyncMsgs(t, opts, "INBOX", 3)
	require.NoError(t, s.SyncAll(context.Background()))

	count, err = store.CountMessages("INBOX")
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestSyncMailbox_EmptyMailbox(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	s, _ := newTestSyncer(t, opts)
	stats, err := s.SyncMailbox(context.Background(), "INBOX")
	require.NoError(t, err)
	assert.Equal(t, 0, stats.TotalMessages)
	assert.Equal(t, 0, stats.NewMessages)
}

func TestSyncMailbox_WithMessages(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 4)

	s, store := newTestSyncer(t, opts)
	stats, err := s.SyncMailbox(context.Background(), "INBOX")
	require.NoError(t, err)
	assert.Equal(t, 4, stats.TotalMessages)
	assert.Equal(t, 4, stats.NewMessages)

	count, err := store.CountMessages("INBOX")
	require.NoError(t, err)
	assert.Equal(t, 4, count)
}

func TestSyncMailbox_NothingNew(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 2)

	s, _ := newTestSyncer(t, opts)
	// First sync.
	_, err := s.SyncMailbox(context.Background(), "INBOX")
	require.NoError(t, err)

	// Second sync — no new messages.
	stats, err := s.SyncMailbox(context.Background(), "INBOX")
	require.NoError(t, err)
	assert.Equal(t, 2, stats.TotalMessages)
	assert.Equal(t, 0, stats.NewMessages)
}

func TestSyncMailbox_UIDValidityChanged(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 1)

	s, store := newTestSyncer(t, opts)

	// Save a stale state with a wrong UIDValidity to trigger resync.
	require.NoError(t, store.SaveMailboxState(&storage.MailboxState{
		Name:        "INBOX",
		UIDValidity: 99999,
		LastUID:     100,
		LastSync:    time.Now(),
	}))

	stats, err := s.SyncMailbox(context.Background(), "INBOX")
	require.NoError(t, err)
	// Full resync after UIDValidity change.
	assert.Equal(t, 1, stats.NewMessages)
}

func TestSyncAll_ContextCancelled(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 1)

	s, _ := newTestSyncer(t, opts)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should return nil (not error) when context is cancelled.
	err := s.SyncAll(ctx)
	// Context cancelled before or during sync — either nil or ctx error.
	if err != nil {
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func TestWatch_IntervalMode(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 1)

	s, _ := newTestSyncer(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// Watch with a long interval — it does initial sync then waits.
	// The context times out before the next poll, so it returns nil.
	err := s.Watch(ctx, time.Hour)
	assert.NoError(t, err)
}

func TestSyncAll_WithGmailFilter(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 2)
	appendSyncMsgs(t, opts, "Sent", 1)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	client, err := imapClient.Connect(opts)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() }) //nolint:errcheck

	store, err := storage.New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() }) //nolint:errcheck

	// IncludeFolders: only INBOX — filter skips Sent, so filteredCount > 0
	cfg := &config.GmailConfig{IncludeFolders: []string{"INBOX"}}
	s := New(client, store, log, WithGmailConfig(cfg, true))

	require.NoError(t, s.SyncAll(context.Background()))

	inboxCount, err := store.CountMessages("INBOX")
	require.NoError(t, err)
	assert.Equal(t, 2, inboxCount)

	// Sent was filtered out
	sentCount, err := store.CountMessages("Sent")
	require.NoError(t, err)
	assert.Equal(t, 0, sentCount)
}

func TestSyncMailbox_WithProgress(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 3)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	client, err := imapClient.Connect(opts)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() }) //nolint:errcheck

	store, err := storage.New(filepath.Join(t.TempDir(), "test.db"), log)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() }) //nolint:errcheck

	s := New(client, store, log, WithProgress(true))

	stats, err := s.SyncMailbox(context.Background(), "INBOX")
	require.NoError(t, err)
	assert.Equal(t, 3, stats.NewMessages)

	count, err := store.CountMessages("INBOX")
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestWatch_IdleMode(t *testing.T) {
	opts, cleanup := newSyncTestServer(t)
	defer cleanup()

	appendSyncMsgs(t, opts, "INBOX", 1)

	s, _ := newTestSyncer(t, opts)

	// Short timeout: initial sync completes, then watchWithIdle enters IDLE.
	// Timeout fires during IDLE → clean exit.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := s.Watch(ctx, 0)
	assert.NoError(t, err)
}
