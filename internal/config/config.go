package config

import (
	"github.com/vitalvas/gokit/xconfig"
)

type Config struct {
	IMAP    IMAPConfig    `yaml:"imap"`
	Storage StorageConfig `yaml:"storage"`
	Gmail   GmailConfig   `yaml:"gmail"`
}

type IMAPConfig struct {
	Host     string `yaml:"host" validate:"required"`
	Port     int    `yaml:"port" validate:"required,min=1,max=65535"`
	Username string `yaml:"username" validate:"required"`
	Password string `yaml:"password" validate:"required"`
	TLS      bool   `yaml:"tls"`
}

type StorageConfig struct {
	Path string `yaml:"path" validate:"required"`
}

type GmailConfig struct {
	// Enabled controls whether Gmail-specific handling is enabled.
	// When true, the system will detect Gmail servers and apply special handling.
	// Default: true (auto-detect)
	Enabled *bool `yaml:"enabled,omitempty"`

	// SkipAllMail controls whether to skip syncing the [Gmail]/All Mail folder.
	// Gmail's All Mail contains all emails (duplicates from other folders).
	// Default: true
	SkipAllMail *bool `yaml:"skip_all_mail,omitempty"`

	// FetchLabels controls whether to fetch Gmail labels using X-GM-LABELS extension.
	// When enabled, stores Gmail labels metadata for each email.
	// Default: true
	FetchLabels *bool `yaml:"fetch_labels,omitempty"`

	// ExcludeFolders is a list of folder patterns to exclude from sync.
	// Supports exact matches and wildcards.
	// Example: ["[Gmail]/Spam", "[Gmail]/Trash"]
	ExcludeFolders []string `yaml:"exclude_folders,omitempty"`

	// IncludeFolders is a list of folder patterns to include in sync.
	// When set, only these folders will be synced (takes precedence over exclude).
	// Example: ["INBOX", "[Gmail]/Sent Mail"]
	IncludeFolders []string `yaml:"include_folders,omitempty"`
}

// IsEnabled returns whether Gmail handling is enabled.
// Returns true if not explicitly disabled.
func (g *GmailConfig) IsEnabled() bool {
	if g.Enabled == nil {
		return true
	}
	return *g.Enabled
}

// ShouldSkipAllMail returns whether to skip [Gmail]/All Mail folder.
// Returns true by default.
func (g *GmailConfig) ShouldSkipAllMail() bool {
	if g.SkipAllMail == nil {
		return true
	}
	return *g.SkipAllMail
}

// ShouldFetchLabels returns whether to fetch Gmail labels.
// Returns true by default.
func (g *GmailConfig) ShouldFetchLabels() bool {
	if g.FetchLabels == nil {
		return true
	}
	return *g.FetchLabels
}

func Load(path string) (*Config, error) {
	var cfg Config
	if err := xconfig.Load(&cfg, xconfig.WithFiles(path)); err != nil {
		return nil, err
	}
	return &cfg, nil
}
