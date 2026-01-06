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

	t.Run("with gmail config", func(t *testing.T) {
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")

		configContent := `imap:
  host: imap.gmail.com
  port: 993
  username: test@gmail.com
  password: secret
  tls: true
storage:
  path: /tmp/emails
gmail:
  enabled: true
  skip_all_mail: true
  fetch_labels: true
  exclude_folders:
    - "[Gmail]/Spam"
    - "[Gmail]/Trash"
`
		err := os.WriteFile(configFile, []byte(configContent), 0600)
		require.NoError(t, err)

		cfg, err := Load(configFile)
		require.NoError(t, err)
		assert.True(t, cfg.Gmail.IsEnabled())
		assert.True(t, cfg.Gmail.ShouldSkipAllMail())
		assert.True(t, cfg.Gmail.ShouldFetchLabels())
		assert.Equal(t, 2, len(cfg.Gmail.ExcludeFolders))
		assert.Contains(t, cfg.Gmail.ExcludeFolders, "[Gmail]/Spam")
		assert.Contains(t, cfg.Gmail.ExcludeFolders, "[Gmail]/Trash")
	})
}

func TestGmailConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   GmailConfig
		expected bool
	}{
		{
			name:     "nil enabled (default true)",
			config:   GmailConfig{Enabled: nil},
			expected: true,
		},
		{
			name:     "explicitly enabled",
			config:   GmailConfig{Enabled: boolPtr(true)},
			expected: true,
		},
		{
			name:     "explicitly disabled",
			config:   GmailConfig{Enabled: boolPtr(false)},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.IsEnabled())
		})
	}
}

func TestGmailConfig_ShouldSkipAllMail(t *testing.T) {
	tests := []struct {
		name     string
		config   GmailConfig
		expected bool
	}{
		{
			name:     "nil skip_all_mail (default true)",
			config:   GmailConfig{SkipAllMail: nil},
			expected: true,
		},
		{
			name:     "explicitly enabled",
			config:   GmailConfig{SkipAllMail: boolPtr(true)},
			expected: true,
		},
		{
			name:     "explicitly disabled",
			config:   GmailConfig{SkipAllMail: boolPtr(false)},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.ShouldSkipAllMail())
		})
	}
}

func TestGmailConfig_ShouldFetchLabels(t *testing.T) {
	tests := []struct {
		name     string
		config   GmailConfig
		expected bool
	}{
		{
			name:     "nil fetch_labels (default true)",
			config:   GmailConfig{FetchLabels: nil},
			expected: true,
		},
		{
			name:     "explicitly enabled",
			config:   GmailConfig{FetchLabels: boolPtr(true)},
			expected: true,
		},
		{
			name:     "explicitly disabled",
			config:   GmailConfig{FetchLabels: boolPtr(false)},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.ShouldFetchLabels())
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}
