package app

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/newsamples/imapsync/internal/storage"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitConfig_Verbose(t *testing.T) {
	require.NoError(t, RootCmd.PersistentFlags().Set("verbose", "true"))
	InitConfig()
	require.NoError(t, RootCmd.PersistentFlags().Set("verbose", "false"))
	InitConfig()
}

func writeInvalidConfig(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "invalid-config-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString("invalid: yaml: [unclosed bracket")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func TestRunSync_MissingConfig(t *testing.T) {
	old := CfgFile
	CfgFile = writeInvalidConfig(t)
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("progress", true, "")
	cmd.Flags().Bool("watch", false, "")
	cmd.Flags().Duration("interval", 0, "")

	err := RunSync(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load config")
}

func TestRunServer_MissingConfig(t *testing.T) {
	old := CfgFile
	CfgFile = writeInvalidConfig(t)
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().String("addr", ":8080", "")

	err := RunServer(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load config")
}

func writeValidConfig(t *testing.T, host string, port int, storagePath string) string {
	t.Helper()
	f, err := os.CreateTemp("", "valid-config-*.yaml")
	require.NoError(t, err)
	content := fmt.Sprintf("imap:\n  host: %q\n  port: %d\n  username: \"testuser\"\n  password: \"testpass\"\n  tls: false\nstorage:\n  path: %q\n", host, port, storagePath)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func newMainTestServer(t *testing.T) (host string, port int, cleanup func()) {
	t.Helper()
	mem := imapmemserver.New()
	u := imapmemserver.NewUser("testuser", "testpass")
	require.NoError(t, u.Create("INBOX", nil))
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

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, func() { srv.Close() } //nolint:errcheck
}

func TestRunSync_IMAPConnectFail(t *testing.T) {
	host, _, cleanup := newMainTestServer(t)
	defer cleanup()

	old := CfgFile
	CfgFile = writeValidConfig(t, host, 1, filepath.Join(t.TempDir(), "test.db"))
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("progress", false, "")
	cmd.Flags().Bool("watch", false, "")
	cmd.Flags().Duration("interval", 0, "")

	err := RunSync(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect to IMAP server")
}

func TestRunSync_StorageFail(t *testing.T) {
	host, port, cleanup := newMainTestServer(t)
	defer cleanup()

	old := CfgFile
	CfgFile = writeValidConfig(t, host, port, "/nonexistent_xyz/test.db")
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("progress", false, "")
	cmd.Flags().Bool("watch", false, "")
	cmd.Flags().Duration("interval", 0, "")

	err := RunSync(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open storage")
}

func TestRunSync_FullPath(t *testing.T) {
	host, port, cleanup := newMainTestServer(t)
	defer cleanup()

	old := CfgFile
	CfgFile = writeValidConfig(t, host, port, filepath.Join(t.TempDir(), "test.db"))
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("progress", false, "")
	cmd.Flags().Bool("watch", false, "")
	cmd.Flags().Duration("interval", 0, "")

	err := RunSync(cmd, nil)
	assert.NoError(t, err)
}

func TestRunServer_StorageFail(t *testing.T) {
	host, port, cleanup := newMainTestServer(t)
	defer cleanup()

	old := CfgFile
	CfgFile = writeValidConfig(t, host, port, "/nonexistent_xyz/server.db")
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().String("addr", ":8080", "")

	err := RunServer(cmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open storage")
}

func TestRunServer_InvalidAddr(t *testing.T) {
	host, port, cleanup := newMainTestServer(t)
	defer cleanup()

	dbPath := filepath.Join(t.TempDir(), "server.db")
	s, err := storage.New(dbPath, Log)
	require.NoError(t, err)
	s.Close()

	old := CfgFile
	CfgFile = writeValidConfig(t, host, port, dbPath)
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().String("addr", "127.0.0.1:99999", "")

	runErr := RunServer(cmd, nil)
	assert.Error(t, runErr)
}

func TestRunSync_WatchMode(t *testing.T) {
	host, port, cleanup := newMainTestServer(t)
	defer cleanup()

	old := CfgFile
	CfgFile = writeValidConfig(t, host, port, filepath.Join(t.TempDir(), "test.db"))
	defer func() { CfgFile = old }()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("progress", false, "")
	cmd.Flags().Bool("watch", true, "")
	cmd.Flags().Duration("interval", time.Millisecond, "")

	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	}()

	err := RunSync(cmd, nil)
	assert.NoError(t, err)
}
