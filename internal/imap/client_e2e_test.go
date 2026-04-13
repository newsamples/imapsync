package imap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	imap2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	imapTestUser = "user"
	imapTestPass = "pass"
	imapTestMsg  = "MIME-Version: 1.0\r\nFrom: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test Email\r\nDate: Wed, 01 Jan 2025 12:00:00 +0000\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nTest body."
)

func newTestIMAPServer(t *testing.T) (ConnectOptions, func()) {
	t.Helper()

	mem := imapmemserver.New()
	u := imapmemserver.NewUser(imapTestUser, imapTestPass)
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

	opts := ConnectOptions{
		Host:     "127.0.0.1",
		Port:     ln.Addr().(*net.TCPAddr).Port,
		Username: imapTestUser,
		Password: imapTestPass,
		Logger:   log,
	}
	return opts, func() { srv.Close() } //nolint:errcheck
}

func appendTestMsgs(t *testing.T, opts ConnectOptions, mailbox string, n int) {
	t.Helper()
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	c, err := imapclient.DialInsecure(addr, nil)
	require.NoError(t, err)
	defer func() { c.Logout().Wait() }() //nolint:errcheck
	require.NoError(t, c.Login(opts.Username, opts.Password).Wait())
	for range n {
		cmd := c.Append(mailbox, int64(len(imapTestMsg)), nil)
		_, err = cmd.Write([]byte(imapTestMsg))
		require.NoError(t, err)
		require.NoError(t, cmd.Close())
		_, err = cmd.Wait()
		require.NoError(t, err)
	}
}

// mockNetError satisfies net.Error for testing isNetworkError.
type mockNetError struct{}

func (e *mockNetError) Error() string   { return "mock net error" }
func (e *mockNetError) Timeout() bool   { return false }
func (e *mockNetError) Temporary() bool { return false }

func TestIsNetworkError(t *testing.T) {
	assert.False(t, isNetworkError(nil))
	assert.False(t, isNetworkError(errors.New("some random error")))
	assert.True(t, isNetworkError(io.EOF))
	assert.True(t, isNetworkError(io.ErrUnexpectedEOF))
	assert.True(t, isNetworkError(errors.New("connection reset by peer")))
	assert.True(t, isNetworkError(errors.New("broken pipe")))
	assert.True(t, isNetworkError(errors.New("connection refused")))
	assert.True(t, isNetworkError(errors.New("no route to host")))
	assert.True(t, isNetworkError(errors.New("network is unreachable")))
	assert.True(t, isNetworkError(errors.New("i/o timeout")))
	assert.True(t, isNetworkError(errors.New("connection timed out")))
}

func TestIsNetworkError_NetError(t *testing.T) {
	var err net.Error = &mockNetError{}
	assert.True(t, isNetworkError(err))
}

func TestConnect_Success(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.NoError(t, c.Close())
}

func TestConnect_NilLogger(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	opts.Logger = nil
	c, err := Connect(opts)
	require.NoError(t, err)
	assert.NoError(t, c.Close())
}

func TestConnect_WrongPassword(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	opts.Password = "wrong"
	_, err := Connect(opts)
	assert.Error(t, err)
}

func TestConnect_NoServer(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	_, err := Connect(ConnectOptions{
		Host:     "127.0.0.1",
		Port:     1,
		Username: "u",
		Password: "p",
		Logger:   log,
	})
	assert.Error(t, err)
}

func TestReconnect_Success(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	// Server is still running — reconnect should succeed.
	require.NoError(t, c.reconnect(context.Background()))

	mailboxes, err := c.ListMailboxes()
	require.NoError(t, err)
	assert.NotEmpty(t, mailboxes)
}

func TestReconnect_Failure(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	c := &Client{
		opts: ConnectOptions{
			Host:     "127.0.0.1",
			Port:     1, // nothing listening
			Username: "u",
			Password: "p",
			Logger:   log,
		},
		log:     log,
		retries: 1,
	}

	err := c.reconnect(context.Background())
	assert.Error(t, err)
}

func TestReconnect_ContextCancelled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	c := &Client{
		opts: ConnectOptions{
			Host:     "127.0.0.1",
			Port:     1,
			Username: "u",
			Password: "p",
			Logger:   log,
		},
		log:     log,
		retries: 3,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.reconnect(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWithRetry_Reconnects(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	// Force-close the underlying connection to simulate a network drop.
	c.client.Close() //nolint:errcheck

	// withRetry should detect the error, reconnect, and retry successfully.
	mailboxes, err := c.ListMailboxesWithContext(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, mailboxes)
}

func TestListMailboxes(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	mailboxes, err := c.ListMailboxes()
	require.NoError(t, err)
	assert.Contains(t, mailboxes, "INBOX")
	assert.Contains(t, mailboxes, "Sent")
}

func TestListMailboxesWithContext(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	mailboxes, err := c.ListMailboxesWithContext(context.Background())
	require.NoError(t, err)
	assert.Contains(t, mailboxes, "INBOX")
}

func TestListMailboxesWithContext_NonNetworkError(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	// Cancelled context returns an error that is not a network error,
	// so withRetry returns it immediately without retry.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = c.ListMailboxesWithContext(ctx)
	assert.Error(t, err)
}

func TestSelectMailbox(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	data, err := c.SelectMailbox("INBOX")
	require.NoError(t, err)
	assert.NotNil(t, data)
}

func TestSelectMailboxWithContext(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	data, err := c.SelectMailboxWithContext(context.Background(), "INBOX")
	require.NoError(t, err)
	assert.NotNil(t, data)
}

func TestSearchAll(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	appendTestMsgs(t, opts, "INBOX", 2)

	_, err = c.SelectMailbox("INBOX")
	require.NoError(t, err)

	uids, err := c.SearchAll()
	require.NoError(t, err)
	assert.Len(t, uids, 2)
}

func TestSearchAllWithContext(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	appendTestMsgs(t, opts, "INBOX", 3)

	_, err = c.SelectMailboxWithContext(context.Background(), "INBOX")
	require.NoError(t, err)

	uids, err := c.SearchAllWithContext(context.Background())
	require.NoError(t, err)
	assert.Len(t, uids, 3)
}

func TestSearchAllWithContext_Empty(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.SelectMailbox("INBOX")
	require.NoError(t, err)

	uids, err := c.SearchAll()
	require.NoError(t, err)
	assert.Empty(t, uids)
}

func TestFetchMessages(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	appendTestMsgs(t, opts, "INBOX", 1)

	_, err = c.SelectMailbox("INBOX")
	require.NoError(t, err)

	uids, err := c.SearchAll()
	require.NoError(t, err)
	require.NotEmpty(t, uids)

	seqSet := imap2.UIDSetNum(imap2.UID(uids[0]))
	msgs, err := c.FetchMessages(seqSet)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, uids[0], msgs[0].UID)
}

func TestFetchMessagesWithContext(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	appendTestMsgs(t, opts, "INBOX", 2)

	_, err = c.SelectMailbox("INBOX")
	require.NoError(t, err)

	uids, err := c.SearchAll()
	require.NoError(t, err)
	require.Len(t, uids, 2)

	imapUIDs := make([]imap2.UID, len(uids))
	for i, uid := range uids {
		imapUIDs[i] = imap2.UID(uid)
	}
	seqSet := imap2.UIDSetNum(imapUIDs...)

	msgs, err := c.FetchMessagesWithContext(context.Background(), seqSet)
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.NotNil(t, msgs[0].Envelope)
	assert.Equal(t, "Test Email", msgs[0].Envelope.Subject)
}

func TestIdleMailbox_UpdateReceived(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	var gotUpdate bool
	var idleErr error
	go func() {
		defer close(done)
		gotUpdate, idleErr = c.IdleMailbox(ctx, "INBOX")
	}()

	// Give IDLE time to start.
	time.Sleep(150 * time.Millisecond)

	// Append from another connection to trigger EXISTS notification.
	appendTestMsgs(t, opts, "INBOX", 1)

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("IdleMailbox did not return after append")
	}

	require.NoError(t, idleErr)
	assert.True(t, gotUpdate)
}

func TestIdleMailbox_ContextCancelled(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var gotUpdate bool
	go func() {
		defer close(done)
		gotUpdate, _ = c.IdleMailbox(ctx, "INBOX")
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("IdleMailbox did not return after context cancel")
	}

	assert.False(t, gotUpdate)
}

func TestSetFetchGmailLabels(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	c.SetFetchGmailLabels(true)
	c.SetFetchGmailLabels(false)
}

func TestIsGmailServer_NotGmail(t *testing.T) {
	opts, cleanup := newTestIMAPServer(t)
	defer cleanup()

	c, err := Connect(opts)
	require.NoError(t, err)
	defer c.Close()

	isGmail, err := c.IsGmailServer(context.Background())
	require.NoError(t, err)
	assert.False(t, isGmail)
}
