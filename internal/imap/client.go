package imap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/sirupsen/logrus"
)

type Client struct {
	client           *imapclient.Client
	opts             ConnectOptions
	log              *logrus.Logger
	retries          int
	fetchGmailLabels bool
}

type ConnectOptions struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      bool
	Logger   *logrus.Logger
}

type Message struct {
	UID         uint32
	Flags       []imap.Flag
	Size        uint32
	Envelope    *imap.Envelope
	Body        []byte
	Headers     []byte
	RawMessage  []byte
	GmailLabels []string // Gmail labels from X-GM-LABELS extension
}

func Connect(opts ConnectOptions) (*Client, error) {
	if opts.Logger == nil {
		opts.Logger = logrus.New()
	}

	client := &Client{
		opts:             opts,
		log:              opts.Logger,
		retries:          3,
		fetchGmailLabels: false,
	}

	if err := client.connect(); err != nil {
		return nil, err
	}

	return client, nil
}

// SetFetchGmailLabels enables or disables fetching Gmail labels via X-GM-LABELS extension.
func (c *Client) SetFetchGmailLabels(enabled bool) {
	c.fetchGmailLabels = enabled
}

func (c *Client) connect() error {
	addr := fmt.Sprintf("%s:%d", c.opts.Host, c.opts.Port)

	var client *imapclient.Client
	var err error

	if c.opts.TLS {
		client, err = imapclient.DialTLS(addr, &imapclient.Options{
			TLSConfig: &tls.Config{
				ServerName: c.opts.Host,
			},
		})
	} else {
		client, err = imapclient.DialInsecure(addr, &imapclient.Options{})
	}

	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	if err := client.Login(c.opts.Username, c.opts.Password).Wait(); err != nil {
		client.Close()
		return fmt.Errorf("failed to login: %w", err)
	}

	c.client = client
	return nil
}

func (c *Client) reconnect(ctx context.Context) error {
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}

	maxRetries := c.retries
	backoff := time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		c.log.Infof("Attempting to reconnect (attempt %d/%d)...", attempt, maxRetries)

		if err := c.connect(); err != nil {
			c.log.WithError(err).Warnf("Reconnection attempt %d failed", attempt)

			if attempt < maxRetries {
				c.log.Infof("Waiting %v before retry...", backoff)
				select {
				case <-time.After(backoff):
					backoff *= 2
					if backoff > 30*time.Second {
						backoff = 30 * time.Second
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			continue
		}

		c.log.Info("Reconnected successfully")
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts", maxRetries)
}

func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	errStr := err.Error()
	networkErrorPatterns := []string{
		"connection reset",
		"broken pipe",
		"connection refused",
		"no route to host",
		"network is unreachable",
		"i/o timeout",
		"connection timed out",
	}

	for _, pattern := range networkErrorPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

func (c *Client) withRetry(ctx context.Context, operation func() error) error {
	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if err := c.reconnect(ctx); err != nil {
				return fmt.Errorf("reconnection failed: %w", err)
			}
		}

		err := operation()
		if err == nil {
			return nil
		}

		lastErr = err

		if !isNetworkError(err) {
			return err
		}

		if attempt < c.retries {
			c.log.WithError(err).Warnf("Network error detected, will retry (attempt %d/%d)", attempt+1, c.retries+1)
		}
	}

	return fmt.Errorf("operation failed after %d retries: %w", c.retries+1, lastErr)
}

func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Logout().Wait()
	}
	return nil
}

func (c *Client) ListMailboxes() ([]string, error) {
	return c.ListMailboxesWithContext(context.Background())
}

func (c *Client) ListMailboxesWithContext(ctx context.Context) ([]string, error) {
	var result []string

	err := c.withRetry(ctx, func() error {
		mboxes := c.client.List("", "*", nil)

		result = nil
		for {
			mbox := mboxes.Next()
			if mbox == nil {
				break
			}

			// Skip non-selectable mailboxes (like [Gmail] namespace folder)
			if isNonSelectableMailbox(mbox.Attrs) {
				c.log.Debugf("Skipping non-selectable mailbox: %s", mbox.Mailbox)
				continue
			}

			result = append(result, mbox.Mailbox)
		}

		if err := mboxes.Close(); err != nil {
			return fmt.Errorf("failed to list mailboxes: %w", err)
		}

		sort.Strings(result)
		return nil
	})

	return result, err
}

// isNonSelectableMailbox checks if a mailbox has the \Noselect attribute.
// Non-selectable mailboxes are containers/namespaces that cannot be selected.
func isNonSelectableMailbox(attrs []imap.MailboxAttr) bool {
	for _, attr := range attrs {
		if attr == imap.MailboxAttrNoSelect {
			return true
		}
	}
	return false
}

func (c *Client) SelectMailbox(name string) (*imap.SelectData, error) {
	return c.SelectMailboxWithContext(context.Background(), name)
}

func (c *Client) SelectMailboxWithContext(ctx context.Context, name string) (*imap.SelectData, error) {
	var data *imap.SelectData

	err := c.withRetry(ctx, func() error {
		var err error
		data, err = c.client.Select(name, nil).Wait()
		if err != nil {
			return fmt.Errorf("failed to select mailbox: %w", err)
		}
		return nil
	})

	return data, err
}

func (c *Client) FetchMessages(numSet imap.NumSet) ([]*Message, error) {
	return c.FetchMessagesWithContext(context.Background(), numSet)
}

func (c *Client) FetchMessagesWithContext(ctx context.Context, numSet imap.NumSet) ([]*Message, error) {
	var messages []*Message

	err := c.withRetry(ctx, func() error {
		fetchOptions := &imap.FetchOptions{
			Flags:    true,
			Envelope: true,
			BodySection: []*imap.FetchItemBodySection{
				{Specifier: imap.PartSpecifierHeader},
				{},
			},
			RFC822Size: true,
			UID:        true,
		}

		cmd := c.client.Fetch(numSet, fetchOptions)
		defer cmd.Close()

		messages = nil

		for {
			msg := cmd.Next()
			if msg == nil {
				break
			}

			buf, err := msg.Collect()
			if err != nil {
				return fmt.Errorf("failed to collect message: %w", err)
			}

			message := &Message{
				UID:      uint32(buf.UID),
				Flags:    buf.Flags,
				Size:     uint32(buf.RFC822Size),
				Envelope: buf.Envelope,
			}

			for _, section := range buf.BodySection {
				switch section.Section.Specifier {
				case imap.PartSpecifierHeader:
					message.Headers = section.Bytes
				case imap.PartSpecifierNone:
					message.Body = section.Bytes
					message.RawMessage = section.Bytes
				}
			}

			// Extract Gmail labels from flags if available
			// Gmail labels appear as \Label or X-GM-LABELS in some implementations
			if c.fetchGmailLabels {
				message.GmailLabels = extractGmailLabels(buf.Flags)
			}

			messages = append(messages, message)
		}

		if err := cmd.Close(); err != nil {
			return fmt.Errorf("failed to fetch messages: %w", err)
		}

		return nil
	})

	return messages, err
}

// extractGmailLabels extracts Gmail label information from IMAP flags.
// Gmail exposes labels through custom flags in the format: \Label or similar.
func extractGmailLabels(flags []imap.Flag) []string {
	var labels []string

	for _, flag := range flags {
		flagStr := string(flag)
		// Gmail labels can appear as custom flags
		// Skip standard IMAP flags
		if !isStandardIMAPFlag(flagStr) {
			// Remove backslash prefix if present
			label := strings.TrimPrefix(flagStr, "\\")
			if label != "" && label != flagStr {
				labels = append(labels, label)
			}
		}
	}

	return labels
}

// isStandardIMAPFlag checks if a flag is a standard IMAP flag.
func isStandardIMAPFlag(flag string) bool {
	standardFlags := map[string]bool{
		"\\Seen":     true,
		"\\Answered": true,
		"\\Flagged":  true,
		"\\Deleted":  true,
		"\\Draft":    true,
		"\\Recent":   true,
	}
	return standardFlags[flag]
}

func (c *Client) SearchAll() ([]uint32, error) {
	return c.SearchAllWithContext(context.Background())
}

func (c *Client) SearchAllWithContext(ctx context.Context) ([]uint32, error) {
	var result []uint32

	err := c.withRetry(ctx, func() error {
		criteria := &imap.SearchCriteria{}

		data, err := c.client.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return fmt.Errorf("failed to search: %w", err)
		}

		uids := data.AllUIDs()
		result = make([]uint32, len(uids))
		for i, uid := range uids {
			result[i] = uint32(uid)
		}

		return nil
	})

	return result, err
}

func ParseEnvelopeDate(envelope *imap.Envelope) time.Time {
	if envelope != nil && !envelope.Date.IsZero() {
		return envelope.Date
	}
	return time.Now()
}

func FlagsToStrings(flags []imap.Flag) []string {
	result := make([]string, len(flags))
	for i, flag := range flags {
		result[i] = string(flag)
	}
	return result
}

// IsGmailServer detects if the IMAP server is Gmail by checking for Gmail-specific folders.
// Returns true if any folder starts with [Gmail] or [Google Mail].
func (c *Client) IsGmailServer(ctx context.Context) (bool, error) {
	mailboxes, err := c.ListMailboxesWithContext(ctx)
	if err != nil {
		return false, err
	}

	for _, mailbox := range mailboxes {
		if IsGmailFolder(mailbox) {
			return true, nil
		}
	}

	return false, nil
}

// IsGmailFolder returns true if the folder name is a Gmail system folder.
// Gmail system folders start with [Gmail]/ or [Google Mail]/.
func IsGmailFolder(name string) bool {
	return strings.HasPrefix(name, "[Gmail]/") || strings.HasPrefix(name, "[Google Mail]/")
}

// IsGmailAllMail returns true if the folder is Gmail's All Mail folder.
func IsGmailAllMail(name string) bool {
	return name == "[Gmail]/All Mail" || name == "[Google Mail]/All Mail"
}

// GetGmailFolderType returns the Gmail folder type (Sent, Trash, Spam, etc.) or empty string.
func GetGmailFolderType(name string) string {
	switch {
	case strings.HasPrefix(name, "[Gmail]/"):
		return strings.TrimPrefix(name, "[Gmail]/")
	case strings.HasPrefix(name, "[Google Mail]/"):
		return strings.TrimPrefix(name, "[Google Mail]/")
	default:
		return ""
	}
}
