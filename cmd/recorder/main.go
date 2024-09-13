package main

import (
	"context"
	"errors"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/recorder"
	"log/slog"
	"os"
	"time"
)

func main() {
	mode := os.Getenv("MODE")
	var exitCode int
	//if mode == string(gsh.InUtilityClusterRecorderMode) {
	//	exitCode = launchInUtilityClusterMode()
	//} else {
	//	exitCode = launchInLocalMode()
	//}
	if len(mode) == 0 {
		slog.Error("MODE environment variable must be set. Choices are local or in-utility-cluster")
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exitCode = launch(ctx, cancel, gsh.ExecutionMode(mode))
	if exitCode == 0 {
		return
	} else {
		os.Exit(exitCode)
	}

}

func launch(ctx context.Context, cancelFunc context.CancelFunc, mode gsh.ExecutionMode) int {
	configDir := os.Getenv("CONFIG_DIR")
	if len(configDir) == 0 {
		if mode == gsh.InUtilityClusterRecorderMode {
			configDir = "/cfg"
		} else if mode == gsh.LocalRecorderMode {
			configDir = "cfg"
		} else {
			slog.Error("CONFIG_DIR env must be set. This is the dir holding the 'clusters.csv' file")
			return 1
		}
		slog.Info("Assumed default configDir for mode", "configDir", configDir, "mode", mode)
	}
	dbDir := os.Getenv("DB_DIR")
	if len(dbDir) == 0 {
		if mode == gsh.LocalRecorderMode {
			dbDir = "gen"
		} else {
			slog.Error("DB_DIR env must be set for non-local mode")
			return 2
		}
		slog.Info("Assumed default dbDir for mode", "dbDir", dbDir, "mode", mode)
	}

	recorderParams, err := recorder.CreateRecorderParams(ctx, mode, configDir, dbDir)
	if err != nil {
		slog.Error("cannot create recorder params", "error", err)
		return 3
	}

	if len(recorderParams) == 0 {
		slog.Error("no recorder params")
		return 4
	}

	for _, rp := range recorderParams {
		startTime := time.Now()
		defaultRecorder, err := recorder.NewDefaultRecorder(ctx, rp, startTime)
		if err != nil {
			slog.Error("cannot create recorder recorder", "error", err)
			return 5
		}
		err = defaultRecorder.Start()
		slog.Info("STARTED recorder", "startTime", startTime, "params", rp)
		if err != nil {
			slog.Error("cannot start recorder recorder", "error", err)
			return 6
		}
	}
	reportDir := os.Getenv("REPORT_DIR")
	if len(reportDir) == 0 {
		reportDir = "/tmp"
		slog.Warn("REPORT_DIR not set. Assuming tmp dir", "reportDir", reportDir)
	}
	//launch engine in a goroutine
	go func() {
		err := recorder.LaunchFileServer(ctx, dbDir, reportDir)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				slog.Info("recorder fileserver was cancelled", "error", err)
			} else {
				slog.Warn("recorder fileserver ran into error", "error", err)
			}
		}
	}()
	apputil.WaitForSignalAndShutdown(ctx, cancelFunc)
	return 0
}
