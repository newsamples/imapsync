package main

import (
	"os"

	"github.com/newsamples/imapsync/internal/app"
)

func main() {
	rootCmd := app.NewRootCommand()
	if err := rootCmd.Execute(); err != nil {
		app.Log.WithError(err).Error("Command execution failed")
		os.Exit(1)
	}
}
