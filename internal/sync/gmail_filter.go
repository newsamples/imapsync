package sync

import (
	"github.com/newsamples/imapsync/internal/config"
	"github.com/newsamples/imapsync/internal/imap"
)

// GmailFilter handles Gmail-specific folder filtering logic.
type GmailFilter struct {
	config  *config.GmailConfig
	isGmail bool
	enabled bool
}

// NewGmailFilter creates a new Gmail filter with the given configuration.
func NewGmailFilter(cfg *config.GmailConfig, isGmail bool) *GmailFilter {
	return &GmailFilter{
		config:  cfg,
		isGmail: isGmail,
		enabled: cfg.IsEnabled() && isGmail,
	}
}

// ShouldSkipMailbox returns true if the mailbox should be skipped based on Gmail configuration.
func (f *GmailFilter) ShouldSkipMailbox(mailbox string) bool {
	if !f.enabled {
		return false
	}

	// Check include list first (if set, only include these)
	if len(f.config.IncludeFolders) > 0 {
		return !f.matchesAnyPattern(mailbox, f.config.IncludeFolders)
	}

	// Skip [Gmail]/All Mail by default
	if f.config.ShouldSkipAllMail() && imap.IsGmailAllMail(mailbox) {
		return true
	}

	// Check exclude list
	if len(f.config.ExcludeFolders) > 0 {
		return f.matchesAnyPattern(mailbox, f.config.ExcludeFolders)
	}

	return false
}

// FilterMailboxes filters a list of mailboxes based on Gmail configuration.
// Returns a new slice with filtered mailboxes.
func (f *GmailFilter) FilterMailboxes(mailboxes []string) []string {
	if !f.enabled {
		return mailboxes
	}

	filtered := make([]string, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		if !f.ShouldSkipMailbox(mailbox) {
			filtered = append(filtered, mailbox)
		}
	}

	return filtered
}

// matchesAnyPattern checks if a mailbox matches any of the given patterns.
// Supports exact matches and wildcard patterns (* and ?).
func (f *GmailFilter) matchesAnyPattern(mailbox string, patterns []string) bool {
	for _, pattern := range patterns {
		if f.matchesPattern(mailbox, pattern) {
			return true
		}
	}
	return false
}

// matchesPattern checks if a mailbox matches a specific pattern.
// Supports exact matches and wildcard patterns (* matches any characters).
func (f *GmailFilter) matchesPattern(mailbox, pattern string) bool {
	// Exact match
	if mailbox == pattern {
		return true
	}

	// Simple wildcard match: * matches any characters
	// This is simpler than filepath.Match and works better with IMAP folder names
	// that contain special characters like brackets
	return simpleWildcardMatch(pattern, mailbox)
}

// simpleWildcardMatch performs simple wildcard matching where * matches any characters.
func simpleWildcardMatch(pattern, str string) bool {
	// If no wildcard, it's a simple comparison
	if pattern == str {
		return true
	}

	// Find the first * in the pattern
	starIdx := -1
	for i, c := range pattern {
		if c == '*' {
			starIdx = i
			break
		}
	}

	// No wildcard found
	if starIdx == -1 {
		return false
	}

	// Check if prefix matches
	prefix := pattern[:starIdx]
	if len(str) < len(prefix) || str[:len(prefix)] != prefix {
		return false
	}

	// If * is at the end, we're done
	if starIdx == len(pattern)-1 {
		return true
	}

	// Check suffix after *
	suffix := pattern[starIdx+1:]
	return len(str) >= len(suffix) && str[len(str)-len(suffix):] == suffix
}

// GetFilteredMailboxCount returns how many mailboxes would be filtered out.
func (f *GmailFilter) GetFilteredMailboxCount(mailboxes []string) (total, filtered, skipped int) {
	total = len(mailboxes)
	filtered = len(f.FilterMailboxes(mailboxes))
	skipped = total - filtered
	return
}

// IsGmailSystemFolder returns true if the folder is a Gmail system folder.
func IsGmailSystemFolder(mailbox string) bool {
	return imap.IsGmailFolder(mailbox)
}

// GetGmailFolderInfo returns information about a Gmail folder.
func GetGmailFolderInfo(mailbox string) (isGmail bool, folderType string, isAllMail bool) {
	isGmail = imap.IsGmailFolder(mailbox)
	if isGmail {
		folderType = imap.GetGmailFolderType(mailbox)
		isAllMail = imap.IsGmailAllMail(mailbox)
	}
	return
}
