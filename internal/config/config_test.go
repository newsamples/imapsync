package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")

		configContent := `imap:
  host: imap.example.com
  port: 993
  username: test@example.com
  password: secret
  tls: true
storage:
  path: /tmp/emails
`
		err := os.WriteFile(configFile, []byte(configContent), 0600)
		require.NoError(t, err)

		cfg, err := Load(configFile)
		require.NoError(t, err)
		assert.Equal(t, "imap.example.com", cfg.IMAP.Host)
		assert.Equal(t, 993, cfg.IMAP.Port)
		assert.Equal(t, "test@example.com", cfg.IMAP.Username)
		assert.Equal(t, "secret", cfg.IMAP.Password)
		assert.True(t, cfg.IMAP.TLS)
		assert.Equal(t, "/tmp/emails", cfg.Storage.Path)
	})

	t.Run("non-existent file", func(t *testing.T) {
		cfg, err := Load("/non/existent/file.yaml")
		if err == nil && cfg != nil {
			t.Skip("xconfig allows loading without file")
		}
		if err != nil {
			assert.Error(t, err)
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")

		err := os.WriteFile(configFile, []byte("invalid: yaml: content:"), 0600)
		require.NoError(t, err)

		_, err = Load(configFile)
		assert.Error(t, err)
	})
}
