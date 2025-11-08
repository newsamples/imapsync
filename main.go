package main

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
	"github.com/newsamples/imapsync/internal/sync"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	log     = logrus.New()
)

var rootCmd = &cobra.Command{
	Use:   "imapsync",
	Short: "IMAP email backup tool",
	Long:  "A tool to backup emails from IMAP servers to local storage using badgerdb",
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync emails from IMAP server",
	RunE:  runSync,
}

var serverCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start web server to browse emails",
	RunE:  runServer,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "config.yaml", "config file path")
	rootCmd.PersistentFlags().Bool("verbose", false, "enable verbose logging")

	syncCmd.Flags().Bool("progress", true, "show progress bars")

	serverCmd.Flags().String("addr", ":8080", "server address to listen on")

	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(serverCmd)

	cobra.OnInitialize(initConfig)
}

func initConfig() {
	if verbose, _ := rootCmd.Flags().GetBool("verbose"); verbose {
		log.SetLevel(logrus.DebugLevel)
	} else {
		log.SetLevel(logrus.InfoLevel)
	}

	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
}

func runSync(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Warn("Interrupt signal received, shutting down gracefully...")
		cancel()
	}()

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	showProgress, _ := cmd.Flags().GetBool("progress")

	log.Infof("Connecting to IMAP server: %s:%d", cfg.IMAP.Host, cfg.IMAP.Port)

	client, err := imap.Connect(imap.ConnectOptions{
		Host:     cfg.IMAP.Host,
		Port:     cfg.IMAP.Port,
		Username: cfg.IMAP.Username,
		Password: cfg.IMAP.Password,
		TLS:      cfg.IMAP.TLS,
		Logger:   log,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to IMAP server: %w", err)
	}
	defer client.Close()

	log.Info("Connected to IMAP server successfully")

	store, err := storage.New(cfg.Storage.Path, log)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer store.Close()

	log.Infof("Opened storage at: %s", cfg.Storage.Path)

	syncer := sync.New(client, store, log, sync.WithProgress(showProgress))

	log.Info("Starting email sync...")

	if err := syncer.SyncAll(ctx); err != nil {
		if ctx.Err() == context.Canceled {
			log.Info("Sync cancelled by user")
			return nil
		}
		return fmt.Errorf("sync failed: %w", err)
	}

	log.Info("Email sync completed successfully")

	return nil
}

func runServer(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := storage.New(cfg.Storage.Path, log, storage.WithReadOnly(true))
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer store.Close()

	log.Infof("Opened storage at: %s (read-only)", cfg.Storage.Path)

	srv := server.New(store, log)

	addr, _ := cmd.Flags().GetString("addr")
	return srv.Run(addr)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.WithError(err).Error("Command execution failed")
		os.Exit(1)
	}
}
