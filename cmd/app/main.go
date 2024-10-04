package main

import (
	"context"
	"errors"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/app"
	"github.com/elankath/gardener-scaling-history/apputil"
	"log/slog"
	"os"
	"runtime"
	"time"
)

var (
	// how long we give in flight queries to complete
	shutdownGracePeriod time.Duration = 30 * time.Second
)

func main() {
	// See https://www.reddit.com/r/golang/comments/1f7xl7x/comment/llf8n5k/?utm_source=share&utm_medium=web3x&utm_name=web3xcss&utm_term=1&utm_content=share_button
	ctx, cancel := context.WithCancel(context.Background())
	var exitCode int
	exitCode = launch(ctx, cancel)
	if exitCode == 0 {
		return
	} else {
		os.Exit(exitCode)
	}
}

func launch(ctx context.Context, cancelFunc context.CancelFunc) int {
	var mode string
	mode = os.Getenv("MODE")
	if mode == "" {
		switch osName := runtime.GOOS; osName {
		case "darwin":
			mode = "local"
			slog.Info("Running on macOS. Assuming local mode", "os", osName, "mode", mode)
		case "linux":
			mode = "in-utility-cluster"
			slog.Info("Running on linux. Assuming in-utility-cluster mode", "os", osName, "mode", mode)
		default:
			slog.Error("Cannot determine mode. Kindly set the same")
			return 1
		}
	}
	dbDir := os.Getenv("DB_DIR")
	reportsDir := os.Getenv("REPORT_DIR")
	dockerHubUser := os.Getenv("DOCKERHUB_USER")
	if dockerHubUser == "" {
		slog.Error("Missing env DOCKERHUB_USER")
		return 1
	}

	if mode == "local" {
		if len(dbDir) == 0 {
			dbDir = "gen"
			slog.Info("Assumed default dbDir for mode", "dbDir", dbDir, "mode", mode)
		}

		if len(reportsDir) == 0 {
			reportsDir = "/tmp"
			slog.Warn("REPORT_DIR not set. Assuming tmp dir", "reportsDir", reportsDir)
		}
	} else if mode == "in-utility-cluster" {
		if len(dbDir) == 0 {
			slog.Error("DB_DIR not set for in-utility-cluster.", "dbDir", dbDir)
			return 3
		}
		if !apputil.DirExists(dbDir) {
			slog.Warn("dbDir does not exist. Creating...", "dbDir", dbDir)
			err := os.MkdirAll(dbDir, 0755)
			if err != nil {
				slog.Error("Failed to create dbDir", "err", err)
				return 3
			}
		}

		if len(reportsDir) == 0 {
			slog.Error("REPORT_DIR not set for in-utility-cluster.", "reportsDir", reportsDir)
			return 3
		}
		if !apputil.DirExists(dbDir) {
			slog.Warn("reportsDir does not exist. Creating...", "reportsDir", reportsDir)
			err := os.MkdirAll(reportsDir, 0755)
			if err != nil {
				slog.Error("Failed to create reportsDir", "err", err)
				return 3
			}
		}
	}

	appParams := app.Params{
		DBDir:         dbDir,
		ReportsDir:    reportsDir,
		DockerHubUser: dockerHubUser,
		Mode:          gsh.ExecutionMode(mode),
	}
	application, err := app.New(ctx, appParams)
	if err != nil {
		slog.Error("Error constructing application", "err", err)
		return 2
	}
	go func() {
		err := application.Start()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Info("scaling-history-app was cancelled", "error", err)
			} else {
				slog.Error("scaling-history-app ran into error", "error", err)
				os.Exit(1)
			}
		}
	}()
	apputil.WaitForSignalAndShutdown(ctx, cancelFunc)
	return 0

}
