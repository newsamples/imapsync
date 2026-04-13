package main

import (
	"os"

	"github.com/newsamples/imapsync/internal/app"
)

func main() {
	if err := app.RootCmd.Execute(); err != nil {
		app.Log.WithError(err).Error("Command execution failed")
		os.Exit(1)
	}
}
