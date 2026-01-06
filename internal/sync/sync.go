package sync

import (
	"context"
	"fmt"
	"time"

	imap2 "github.com/emersion/go-imap/v2"
	"github.com/newsamples/imapsync/internal/config"
	"github.com/newsamples/imapsync/internal/imap"
	"github.com/newsamples/imapsync/internal/storage"
	"github.com/schollz/progressbar/v3"
	"github.com/sirupsen/logrus"
)

type Syncer struct {
	client       *imap.Client
	storage      *storage.Storage
	log          *logrus.Logger
	showProgress bool
	gmailFilter  *GmailFilter
}

type Option func(*Syncer)

func WithProgress(enabled bool) Option {
	return func(s *Syncer) {
		s.showProgress = enabled
	}
}

func WithGmailConfig(cfg *config.GmailConfig, isGmail bool) Option {
	return func(s *Syncer) {
		s.gmailFilter = NewGmailFilter(cfg, isGmail)
		// Enable Gmail label fetching if configured
		if cfg.IsEnabled() && cfg.ShouldFetchLabels() && isGmail {
			s.client.SetFetchGmailLabels(true)
		}
	}
}

func New(client *imap.Client, store *storage.Storage, log *logrus.Logger, opts ...Option) *Syncer {
	s := &Syncer{
		client:       client,
		storage:      store,
		log:          log,
		showProgress: false,
		gmailFilter:  nil, // Will be set when Gmail config is provided
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

type Stats struct {
	TotalMessages int
	NewMessages   int
}

func (s *Syncer) SyncAll(ctx context.Context) error {
	mailboxes, err := s.client.ListMailboxesWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to list mailboxes: %w", err)
	}

	originalCount := len(mailboxes)

	// Apply Gmail filtering if configured
	if s.gmailFilter != nil {
		mailboxes = s.gmailFilter.FilterMailboxes(mailboxes)
		if filteredCount := originalCount - len(mailboxes); filteredCount > 0 {
			s.log.Infof("Gmail filter: skipped %d mailboxes (%.0f%%)", filteredCount, float64(filteredCount)/float64(originalCount)*100)
		}
	}

	mailboxes = prioritizeInbox(mailboxes)

	s.log.Infof("Found %d mailboxes to sync", len(mailboxes))

	var totalStats Stats
	processedMailboxes := 0

	for _, mailbox := range mailboxes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !s.showProgress {
			s.log.Infof("Syncing mailbox: %s", mailbox)
		}

		stats, err := s.SyncMailbox(ctx, mailbox)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.WithError(err).Errorf("Failed to sync mailbox: %s", mailbox)
			continue
		}

		processedMailboxes++
		totalStats.TotalMessages += stats.TotalMessages
		totalStats.NewMessages += stats.NewMessages

		if !s.showProgress {
			s.log.Infof("Completed sync for mailbox: %s", mailbox)
		}
	}

	s.log.Infof("Sync completed: %d mailboxes processed, %d messages total, %d new messages synced",
		processedMailboxes, totalStats.TotalMessages, totalStats.NewMessages)

	return nil
}

func (s *Syncer) SyncMailbox(ctx context.Context, mailbox string) (*Stats, error) {
	selectData, err := s.client.SelectMailboxWithContext(ctx, mailbox)
	if err != nil {
		return nil, fmt.Errorf("failed to select mailbox: %w", err)
	}

	state, err := s.storage.GetMailboxState(mailbox)
	if err != nil {
		return nil, fmt.Errorf("failed to get mailbox state: %w", err)
	}

	if state != nil && state.UIDValidity != selectData.UIDValidity {
		s.log.Warnf("UIDValidity changed for mailbox %s, performing full resync", mailbox)
		state = nil
	}

	var startUID uint32 = 1
	if state != nil {
		startUID = state.LastUID + 1
	}

	if selectData.NumMessages == 0 {
		s.log.Infof("Mailbox %s: 0 messages total, 0 new messages (empty)", mailbox)
		return &Stats{TotalMessages: 0, NewMessages: 0}, s.updateMailboxState(mailbox, selectData.UIDValidity, 0)
	}

	uids, err := s.client.SearchAllWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}

	if len(uids) == 0 {
		s.log.Infof("Mailbox %s: 0 messages total, 0 new messages (empty)", mailbox)
		return &Stats{TotalMessages: 0, NewMessages: 0}, s.updateMailboxState(mailbox, selectData.UIDValidity, 0)
	}

	uidsToSync := s.filterUIDs(uids, startUID)

	if len(uidsToSync) == 0 {
		s.log.Infof("Mailbox %s: %d messages total, 0 new messages", mailbox, len(uids))
		return &Stats{TotalMessages: len(uids), NewMessages: 0}, nil
	}

	var bar *progressbar.ProgressBar
	if s.showProgress {
		bar = progressbar.NewOptions(len(uidsToSync),
			progressbar.OptionSetDescription(fmt.Sprintf("%-30s", mailbox)),
			progressbar.OptionShowCount(),
			progressbar.OptionSetWidth(40),
			progressbar.OptionShowIts(),
			progressbar.OptionSetItsString("msgs"),
		)
	} else {
		s.log.Infof("Syncing %d messages from mailbox %s", len(uidsToSync), mailbox)
	}

	batchSize := 5
	for i := 0; i < len(uidsToSync); i += batchSize {
		select {
		case <-ctx.Done():
			if bar != nil {
				bar.Finish()
				fmt.Println()
			}
			return nil, ctx.Err()
		default:
		}

		end := i + batchSize
		if end > len(uidsToSync) {
			end = len(uidsToSync)
		}

		batch := uidsToSync[i:end]
		if err := s.syncBatch(ctx, mailbox, batch, bar); err != nil {
			if ctx.Err() != nil {
				if bar != nil {
					bar.Finish()
					fmt.Println()
				}
				return nil, ctx.Err()
			}
			if bar != nil {
				bar.Finish()
				fmt.Println()
			}
			return nil, fmt.Errorf("failed to sync batch: %w", err)
		}

		if bar == nil {
			s.log.Infof("Synced batch %d-%d of %d messages", i+1, end, len(uidsToSync))
		}
	}

	if bar != nil {
		bar.Finish()
		fmt.Println()
	}

	s.log.Infof("Mailbox %s: %d messages total, %d new messages synced", mailbox, len(uids), len(uidsToSync))

	maxUID := uidsToSync[len(uidsToSync)-1]
	err = s.updateMailboxState(mailbox, selectData.UIDValidity, maxUID)
	return &Stats{TotalMessages: len(uids), NewMessages: len(uidsToSync)}, err
}

func (s *Syncer) syncBatch(ctx context.Context, mailbox string, uids []uint32, bar *progressbar.ProgressBar) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	imapUIDs := make([]imap2.UID, len(uids))
	for i, uid := range uids {
		imapUIDs[i] = imap2.UID(uid)
	}
	seqSet := imap2.UIDSetNum(imapUIDs...)

	messages, err := s.client.FetchMessagesWithContext(ctx, seqSet)
	if err != nil {
		return fmt.Errorf("failed to fetch messages: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	emails := make([]*storage.Email, 0, len(messages))
	for _, msg := range messages {
		email := s.convertToEmail(mailbox, msg)
		emails = append(emails, email)
	}

	if err := s.storage.SaveEmailBatch(emails); err != nil {
		return fmt.Errorf("failed to save emails: %w", err)
	}

	if bar != nil {
		bar.Add(len(emails))
	}

	return nil
}

func (s *Syncer) convertToEmail(mailbox string, msg *imap.Message) *storage.Email {
	var subject, from string
	var to []string

	if msg.Envelope != nil {
		subject = msg.Envelope.Subject

		if len(msg.Envelope.From) > 0 {
			addr := msg.Envelope.From[0]
			from = fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host)
		}

		for _, addr := range msg.Envelope.To {
			to = append(to, fmt.Sprintf("%s@%s", addr.Mailbox, addr.Host))
		}
	}

	return &storage.Email{
		UID:         msg.UID,
		Mailbox:     mailbox,
		Subject:     subject,
		From:        from,
		To:          to,
		Date:        imap.ParseEnvelopeDate(msg.Envelope),
		Size:        msg.Size,
		Flags:       imap.FlagsToStrings(msg.Flags),
		GmailLabels: msg.GmailLabels, // Include Gmail labels if fetched
		Body:        msg.Body,
		Headers:     msg.Headers,
		RawMessage:  msg.RawMessage,
		Synced:      time.Now(),
	}
}

func (s *Syncer) filterUIDs(uids []uint32, startUID uint32) []uint32 {
	var result []uint32
	for _, uid := range uids {
		if uid >= startUID {
			result = append(result, uid)
		}
	}
	return result
}

func (s *Syncer) updateMailboxState(mailbox string, uidValidity, lastUID uint32) error {
	state := &storage.MailboxState{
		Name:        mailbox,
		UIDValidity: uidValidity,
		LastUID:     lastUID,
		LastSync:    time.Now(),
	}

	return s.storage.SaveMailboxState(state)
}

func prioritizeInbox(mailboxes []string) []string {
	if len(mailboxes) == 0 {
		return mailboxes
	}

	inboxIndex := -1
	for i, mailbox := range mailboxes {
		if mailbox == "INBOX" {
			inboxIndex = i
			break
		}
	}

	if inboxIndex == -1 || inboxIndex == 0 {
		return mailboxes
	}

	result := make([]string, 0, len(mailboxes))
	result = append(result, "INBOX")
	result = append(result, mailboxes[:inboxIndex]...)
	result = append(result, mailboxes[inboxIndex+1:]...)

	return result
}
