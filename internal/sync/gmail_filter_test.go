package sync

import (
	"testing"

	"github.com/newsamples/imapsync/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestGmailFilter_ShouldSkipMailbox(t *testing.T) {
	tests := []struct {
		name       string
		config     config.GmailConfig
		isGmail    bool
		mailbox    string
		shouldSkip bool
	}{
		{
			name:       "skip all mail by default",
			config:     config.GmailConfig{},
			isGmail:    true,
			mailbox:    "[Gmail]/All Mail",
			shouldSkip: true,
		},
		{
			name:       "skip google mail all mail",
			config:     config.GmailConfig{},
			isGmail:    true,
			mailbox:    "[Google Mail]/All Mail",
			shouldSkip: true,
		},
		{
			name:       "don't skip inbox",
			config:     config.GmailConfig{},
			isGmail:    true,
			mailbox:    "INBOX",
			shouldSkip: false,
		},
		{
			name: "don't skip when all mail skipping disabled",
			config: config.GmailConfig{
				SkipAllMail: boolPtr(false),
			},
			isGmail:    true,
			mailbox:    "[Gmail]/All Mail",
			shouldSkip: false,
		},
		{
			name: "skip excluded folders",
			config: config.GmailConfig{
				ExcludeFolders: []string{"[Gmail]/Spam", "[Gmail]/Trash"},
			},
			isGmail:    true,
			mailbox:    "[Gmail]/Spam",
			shouldSkip: true,
		},
		{
			name: "don't skip non-excluded folders",
			config: config.GmailConfig{
				ExcludeFolders: []string{"[Gmail]/Spam"},
			},
			isGmail:    true,
			mailbox:    "[Gmail]/Sent Mail",
			shouldSkip: false,
		},
		{
			name: "include list - skip non-included",
			config: config.GmailConfig{
				IncludeFolders: []string{"INBOX", "[Gmail]/Sent Mail"},
			},
			isGmail:    true,
			mailbox:    "[Gmail]/Drafts",
			shouldSkip: true,
		},
		{
			name: "include list - don't skip included",
			config: config.GmailConfig{
				IncludeFolders: []string{"INBOX", "[Gmail]/Sent Mail"},
			},
			isGmail:    true,
			mailbox:    "INBOX",
			shouldSkip: false,
		},
		{
			name: "include list overrides exclude",
			config: config.GmailConfig{
				IncludeFolders: []string{"INBOX", "[Gmail]/All Mail"},
				ExcludeFolders: []string{"[Gmail]/All Mail"},
			},
			isGmail:    true,
			mailbox:    "[Gmail]/All Mail",
			shouldSkip: false,
		},
		{
			name:       "not gmail - no filtering",
			config:     config.GmailConfig{},
			isGmail:    false,
			mailbox:    "[Gmail]/All Mail",
			shouldSkip: false,
		},
		{
			name: "disabled - no filtering",
			config: config.GmailConfig{
				Enabled: boolPtr(false),
			},
			isGmail:    true,
			mailbox:    "[Gmail]/All Mail",
			shouldSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := NewGmailFilter(&tt.config, tt.isGmail)
			result := filter.ShouldSkipMailbox(tt.mailbox)
			assert.Equal(t, tt.shouldSkip, result)
		})
	}
}

func TestGmailFilter_FilterMailboxes(t *testing.T) {
	mailboxes := []string{
		"INBOX",
		"[Gmail]/All Mail",
		"[Gmail]/Sent Mail",
		"[Gmail]/Drafts",
		"[Gmail]/Spam",
		"[Gmail]/Trash",
		"Work",
		"Personal",
	}

	tests := []struct {
		name     string
		config   config.GmailConfig
		isGmail  bool
		expected []string
	}{
		{
			name:    "skip all mail by default",
			config:  config.GmailConfig{},
			isGmail: true,
			expected: []string{
				"INBOX",
				"[Gmail]/Sent Mail",
				"[Gmail]/Drafts",
				"[Gmail]/Spam",
				"[Gmail]/Trash",
				"Work",
				"Personal",
			},
		},
		{
			name: "exclude spam and trash",
			config: config.GmailConfig{
				ExcludeFolders: []string{"[Gmail]/Spam", "[Gmail]/Trash"},
			},
			isGmail: true,
			expected: []string{
				"INBOX",
				"[Gmail]/Sent Mail",
				"[Gmail]/Drafts",
				"Work",
				"Personal",
			},
		},
		{
			name: "include only specific folders",
			config: config.GmailConfig{
				IncludeFolders: []string{"INBOX", "[Gmail]/Sent Mail"},
			},
			isGmail: true,
			expected: []string{
				"INBOX",
				"[Gmail]/Sent Mail",
			},
		},
		{
			name:     "not gmail - no filtering",
			config:   config.GmailConfig{},
			isGmail:  false,
			expected: mailboxes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := NewGmailFilter(&tt.config, tt.isGmail)
			result := filter.FilterMailboxes(mailboxes)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGmailFilter_matchesPattern(t *testing.T) {
	filter := NewGmailFilter(&config.GmailConfig{}, true)

	tests := []struct {
		name     string
		mailbox  string
		pattern  string
		expected bool
	}{
		{
			name:     "exact match",
			mailbox:  "[Gmail]/Spam",
			pattern:  "[Gmail]/Spam",
			expected: true,
		},
		{
			name:     "wildcard all gmail folders",
			mailbox:  "[Gmail]/Spam",
			pattern:  "[Gmail]/*",
			expected: true,
		},
		{
			name:     "wildcard match with suffix",
			mailbox:  "[Gmail]/Spam",
			pattern:  "*Spam",
			expected: true,
		},
		{
			name:     "wildcard match with prefix",
			mailbox:  "[Gmail]/Spam",
			pattern:  "[Gmail]*",
			expected: true,
		},
		{
			name:     "no match",
			mailbox:  "[Gmail]/Spam",
			pattern:  "[Gmail]/Trash",
			expected: false,
		},
		{
			name:     "wildcard no match",
			mailbox:  "INBOX",
			pattern:  "[Gmail]/*",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filter.matchesPattern(tt.mailbox, tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetGmailFolderInfo(t *testing.T) {
	tests := []struct {
		name            string
		mailbox         string
		expectedGmail   bool
		expectedType    string
		expectedAllMail bool
	}{
		{
			name:            "gmail all mail",
			mailbox:         "[Gmail]/All Mail",
			expectedGmail:   true,
			expectedType:    "All Mail",
			expectedAllMail: true,
		},
		{
			name:            "gmail sent mail",
			mailbox:         "[Gmail]/Sent Mail",
			expectedGmail:   true,
			expectedType:    "Sent Mail",
			expectedAllMail: false,
		},
		{
			name:            "google mail spam",
			mailbox:         "[Google Mail]/Spam",
			expectedGmail:   true,
			expectedType:    "Spam",
			expectedAllMail: false,
		},
		{
			name:            "regular folder",
			mailbox:         "INBOX",
			expectedGmail:   false,
			expectedType:    "",
			expectedAllMail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isGmail, folderType, isAllMail := GetGmailFolderInfo(tt.mailbox)
			assert.Equal(t, tt.expectedGmail, isGmail)
			assert.Equal(t, tt.expectedType, folderType)
			assert.Equal(t, tt.expectedAllMail, isAllMail)
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}
