package syncer

import (
	"context"
	"time"
)

var (
	idleTimeout      = 5 * time.Minute
	defaultPollOther = 5 * time.Minute
	idleRetryBackoff = 30 * time.Second
)

// Watch runs continuous sync. When interval is 0 it uses IMAP IDLE on INBOX for
// real-time notifications and periodically polls other folders; otherwise it
// polls every interval across all folders.
func (s *Syncer) Watch(ctx context.Context, interval time.Duration) error {
	if ctx.Err() != nil {
		return nil
	}

	s.log.Info("Starting watch mode, performing initial sync...")

	if err := s.SyncAll(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	if interval == 0 {
		return s.watchWithIdle(ctx)
	}

	return s.watchWithInterval(ctx, interval)
}

func (s *Syncer) watchWithInterval(ctx context.Context, interval time.Duration) error {
	s.log.Infof("Watch: polling every %v", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.log.Info("Watch: interval elapsed, syncing...")
			if err := s.SyncAll(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				s.log.WithError(err).Error("Watch: sync failed, will retry on next interval")
			}
		}
	}
}

// watchWithIdle runs IMAP IDLE on INBOX for real-time notifications and
// periodically polls all other folders. IDLE and polling share the same
// connection, so they alternate: after each IDLE cycle (change detected or
// timeout), other folders are polled if enough time has elapsed.
func (s *Syncer) watchWithIdle(ctx context.Context) error {
	s.log.Infof("Watch: IDLE on INBOX for real-time updates, polling other folders every %v", defaultPollOther)

	lastOtherPoll := time.Now()

	for {
		if ctx.Err() != nil {
			return nil
		}

		idleCtx, idleCancel := context.WithTimeout(ctx, idleTimeout)
		updated, err := s.client.IdleMailbox(idleCtx, "INBOX")
		idleCancel()

		if ctx.Err() != nil {
			return nil
		}

		if err != nil {
			s.log.WithError(err).Warnf("Watch: IDLE on INBOX failed, retrying in %v", idleRetryBackoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(idleRetryBackoff):
			}
			continue
		}

		if updated {
			s.log.Info("Watch: change detected in INBOX, syncing...")
			stats, err := s.SyncMailbox(ctx, "INBOX")
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				s.log.WithError(err).Error("Watch: failed to sync INBOX")
			} else if stats.NewMessages > 0 || stats.DeletedMessages > 0 {
				s.log.Infof("Watch: INBOX %d new, %d deleted", stats.NewMessages, stats.DeletedMessages)
			}
		}

		if time.Since(lastOtherPoll) >= defaultPollOther {
			s.pollOtherMailboxes(ctx)
			lastOtherPoll = time.Now()
		}
	}
}

func (s *Syncer) pollOtherMailboxes(ctx context.Context) {
	mailboxes, err := s.getWatchMailboxList(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.log.WithError(err).Warn("Watch: failed to list mailboxes for polling")
		return
	}

	for _, mailbox := range mailboxes {
		if ctx.Err() != nil {
			return
		}
		if mailbox == "INBOX" {
			continue
		}
		stats, err := s.SyncMailbox(ctx, mailbox)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.WithError(err).Warnf("Watch: failed to poll %s", mailbox)
			continue
		}
		if stats.NewMessages > 0 || stats.DeletedMessages > 0 {
			s.log.Infof("Watch: %s %d new, %d deleted", mailbox, stats.NewMessages, stats.DeletedMessages)
		}
	}
}

func (s *Syncer) getWatchMailboxList(ctx context.Context) ([]string, error) {
	mailboxes, err := s.client.ListMailboxesWithContext(ctx)
	if err != nil {
		return nil, err
	}

	if s.gmailFilter != nil {
		mailboxes = s.gmailFilter.FilterMailboxes(mailboxes)
	}

	return prioritizeInbox(mailboxes), nil
}
