package main

import (
	"context"
	"errors"
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
	dbDir := os.Getenv("DB_DIR")
	reportsDir := os.Getenv("REPORT_DIR")

	if mode == "local" {
		if len(dbDir) == 0 {
			if mode == "local" {
				dbDir = "gen"
			} else {
				slog.Error("DB_DIR env must be set for non-local mode")
				return 2
			}
			slog.Info("Assumed default dbDir for mode", "dbDir", dbDir, "mode", mode)
		}

		if len(reportsDir) == 0 {
			reportsDir = "/tmp"
			slog.Warn("REPORT_DIR not set. Assuming tmp dir", "reportsDir", reportsDir)
		}
	} else if mode == "in-utility-cluster" {
		if dbDir == "" {
			dbDir = "/data/db"
			slog.Error("DB_DIR not set for in-utility-cluster. Assuming default.", "dbDir", dbDir)
			if !apputil.DirExists("/data/db") {
				slog.Error("dbDir does not exist!", "dbDir", dbDir)
				return 3
			}
		}

		if reportsDir == "" {
			reportsDir = "/data/reports"
			slog.Error("REPORT_DIR not set for in-utility-cluster. Assuming default.", "reportsDir", reportsDir)
			if !apputil.DirExists(reportsDir) {
				slog.Error("reportsDir does not exist!", "reportsDir", reportsDir)
				return 4
			}
		}
	}

	appParams := app.Params{
		DBDir:      dbDir,
		ReportsDir: reportsDir,
	}
	//launch engine in a goroutine
	go func() {
		application := app.New(ctx, appParams)
		err := application.Start()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Info("scaling-history-app was cancelled", "error", err)
			} else {
				slog.Warn("scaling-history-app ran into error", "error", err)
			}
		}
	}()
	apputil.WaitForSignalAndShutdown(ctx, cancelFunc)
	return 0

}
