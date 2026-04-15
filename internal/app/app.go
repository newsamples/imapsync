package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/newsamples/imapsync/internal/config"
	"github.com/newsamples/imapsync/internal/imap"
	"github.com/newsamples/imapsync/internal/server"
	"github.com/newsamples/imapsync/internal/storage"
	"github.com/newsamples/imapsync/internal/syncer"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	CfgFile string
	Log     = logrus.New()
)

var RootCmd = &cobra.Command{
	Use:   "imapsync",
	Short: "IMAP email backup tool",
	Long:  "A tool to backup emails from IMAP servers to local storage using badgerdb",
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync emails from IMAP server",
	RunE:  RunSync,
}

var serverCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start web server to browse emails",
	RunE:  RunServer,
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&CfgFile, "config", "c", "config.yaml", "config file path")
	RootCmd.PersistentFlags().Bool("verbose", false, "enable verbose logging")

	syncCmd.Flags().Bool("progress", false, "show progress bars")
	syncCmd.Flags().Bool("watch", false, "watch for changes and sync continuously")
	syncCmd.Flags().Duration("interval", 0, "polling interval for watch mode; 0 uses IMAP IDLE (real-time)")

	serverCmd.Flags().String("addr", ":8080", "server address to listen on")

	RootCmd.AddCommand(syncCmd)
	RootCmd.AddCommand(serverCmd)

	cobra.OnInitialize(InitConfig)
}

func InitConfig() {
	if verbose, _ := RootCmd.PersistentFlags().GetBool("verbose"); verbose {
		Log.SetLevel(logrus.DebugLevel)
	} else {
		Log.SetLevel(logrus.InfoLevel)
	}

	Log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
}

func RunSync(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		Log.Warn("Interrupt signal received, shutting down gracefully...")
		cancel()
	}()

	cfg, err := config.Load(CfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	showProgress, _ := cmd.Flags().GetBool("progress")
	watchMode, _ := cmd.Flags().GetBool("watch")
	interval, _ := cmd.Flags().GetDuration("interval")

	Log.Infof("Connecting to IMAP server: %s:%d", cfg.IMAP.Host, cfg.IMAP.Port)

	client, err := imap.Connect(imap.ConnectOptions{
		Host:     cfg.IMAP.Host,
		Port:     cfg.IMAP.Port,
		Username: cfg.IMAP.Username,
		Password: cfg.IMAP.Password,
		TLS:      cfg.IMAP.TLS,
		Logger:   Log,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to IMAP server: %w", err)
	}
	defer client.Close()

	Log.Info("Connected to IMAP server successfully")

	store, err := storage.New(cfg.Storage.Path, Log)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer store.Close()

	Log.Infof("Opened storage at: %s", cfg.Storage.Path)

	// Detect if server is Gmail
	isGmail := false
	if cfg.Gmail.IsEnabled() {
		detected, err := client.IsGmailServer(ctx)
		if err != nil {
			Log.WithError(err).Warn("Failed to detect Gmail server, continuing without Gmail-specific handling")
		} else {
			isGmail = detected
			if isGmail {
				Log.Info("Gmail server detected, applying Gmail-specific configuration")
			}
		}
	}

	s := syncer.New(client, store, Log,
		syncer.WithProgress(showProgress),
		syncer.WithGmailConfig(&cfg.Gmail, isGmail),
		syncer.WithPurgeAfterDays(cfg.Storage.PurgeAfterDaysOrDefault()),
	)

	if watchMode {
		if interval == 0 {
			Log.Info("Starting watch mode with IMAP IDLE (real-time)")
		} else {
			Log.Infof("Starting watch mode with %v polling interval", interval)
		}

		if err := s.Watch(ctx, interval); err != nil {
			return fmt.Errorf("watch failed: %w", err)
		}

		Log.Info("Watch mode stopped")
		return nil
	}

	Log.Info("Starting email sync...")

	if err := s.SyncAll(ctx); err != nil {
		if ctx.Err() == context.Canceled {
			Log.Info("Sync cancelled by user")
			return nil
		}
		return fmt.Errorf("sync failed: %w", err)
	}

	Log.Info("Email sync completed successfully")

	return nil
}

func RunServer(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(CfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.New(cfg.Storage.Path, Log, storage.WithReadOnly(true))
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer store.Close()

	Log.Infof("Opened storage at: %s (read-only)", cfg.Storage.Path)

	srv := server.New(store, Log)

	addr, _ := cmd.Flags().GetString("addr")
	return srv.Run(addr)
}
