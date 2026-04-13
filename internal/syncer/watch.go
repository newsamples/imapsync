package syncer

import (
	"context"
	"time"
)

const idleTimeout = 5 * time.Minute

// Watch runs continuous sync. When interval is 0 it uses IMAP IDLE for
// real-time notifications; otherwise it polls every interval.
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

func (s *Syncer) watchWithIdle(ctx context.Context) error {
	s.log.Info("Watch: using IMAP IDLE for real-time updates")

	for {
		if ctx.Err() != nil {
			return nil
		}

		mailboxes, err := s.getWatchMailboxList(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.log.WithError(err).Error("Watch: failed to list mailboxes, retrying in 30s")
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(30 * time.Second):
			}
			continue
		}

		for _, mailbox := range mailboxes {
			if ctx.Err() != nil {
				return nil
			}

			s.log.Debugf("Watch: IDLE on %s", mailbox)

			idleCtx, idleCancel := context.WithTimeout(ctx, idleTimeout)
			updated, err := s.client.IdleMailbox(idleCtx, mailbox)
			idleCancel()

			if ctx.Err() != nil {
				return nil
			}

			if err != nil {
				s.log.WithError(err).Warnf("Watch: IDLE failed for %s", mailbox)
				continue
			}

			if updated {
				s.log.Infof("Watch: changes detected in %s, syncing...", mailbox)
				if _, err := s.SyncMailbox(ctx, mailbox); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					s.log.WithError(err).Errorf("Watch: failed to sync %s", mailbox)
				}
			}
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
