package main

import (
	"context"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/desktopapp"
	"log/slog"
	"os"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	var exitCode int
	exitCode = launch(ctx, cancel)
	if exitCode == 0 {
		return
	} else {
		slog.Error("Exiting with code.", "exitCode", exitCode)
		os.Exit(exitCode)
	}
}

func launch(ctx context.Context, cancelFunc context.CancelFunc) int {
	cloudAppHost := os.Getenv("CLOUD_APP_HOST")
	if cloudAppHost == "" {
		slog.Error("CLOUD_APP_HOST not set - set this to ExternalIP of app LB in utility-int")
		return 1
	}
	reportsDir := os.Getenv("REPORT_DIR")
	if len(reportsDir) == 0 {
		reportsDir = "gen"
		slog.Warn("REPORT_DIR not set -  Assuming dir", "reportsDir", reportsDir)
	}
	params := desktopapp.Params{
		ReportsDir:   reportsDir,
		CloudAppHost: cloudAppHost,
	}
	_, err := desktopapp.New(ctx, params)
	if err != nil {
		slog.Error("Error creating desktop app instance", "error", err)
		return 2
	}
	apputil.WaitForSignalAndShutdown(ctx, cancelFunc)
	return 0
}
