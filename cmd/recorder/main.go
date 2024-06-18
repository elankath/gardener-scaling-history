package main

import (
	"context"
	"encoding/csv"
	"github.com/elankath/gardener-cluster-recorder"
	"github.com/elankath/gardener-cluster-recorder/recorder"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const CLUSTERS_CFG_FILE = "clusters.csv"

func main() {
	configDir := os.Getenv("CONFIG_DIR")
	if len(configDir) == 0 {
		slog.Error("CONFIG_DIR env must be set")
		os.Exit(1)
	}
	dbDir := os.Getenv("DB_DIR")
	if len(dbDir) == 0 {
		slog.Error("DB_DIR env must be set")
		os.Exit(2)
	}

	result, err := os.ReadFile(path.Join(configDir, CLUSTERS_CFG_FILE))
	if err != nil {
		slog.Error("cannot read clusters config", "config-file", CLUSTERS_CFG_FILE, "error", err)
		os.Exit(4)
	}
	reader := csv.NewReader(strings.NewReader(string(result)))
	reader.Comment = '#'
	records, err := reader.ReadAll()
	if err != nil {
		slog.Error("cannot read clusters config", "config-file", CLUSTERS_CFG_FILE, "error", err)
		os.Exit(5)
	}
	var recorderParams = make([]gcr.RecorderParams, len(records))
	for rowNum, row := range records {
		recorderParams[rowNum] = gcr.RecorderParams{
			ShootKubeConfigPath: filepath.Join(configDir, strings.TrimSpace(row[0])),
			ShootNameSpace:      row[1],
			SeedKubeConfigPath:  filepath.Join(configDir, strings.TrimSpace(row[2])),
			DBDir:               dbDir,
		}
	}

	if len(recorderParams) == 0 {
		slog.Error("No shootKubeConfigs found in CONFIG_DIR")
		os.Exit(3)
	}

	for _, rp := range recorderParams {
		if _, err := os.Stat(rp.ShootKubeConfigPath); os.IsNotExist(err) {
			slog.Error("Shoot kubeconfig does not exist", "path", rp.ShootKubeConfigPath)
			os.Exit(6)
		}
		if _, err := os.Stat(rp.SeedKubeConfigPath); os.IsNotExist(err) {
			slog.Error("Seed kubeconfig does not exist", "path", rp.SeedKubeConfigPath)
			os.Exit(6)
		}
		if _, err := os.Stat(rp.DBDir); os.IsNotExist(err) {
			slog.Error("Seed kubeconfig does not exist", "path", rp.DBDir)
			os.Exit(6)
		}
	}
	slog.Info("Will monitor, record & analyze clusters for scaling history", "shootKubeConfigs", recorderParams[0].ShootKubeConfigPath, "dbdir", dbDir)

	startTime := time.Now()
	defaultRecorder, err := recorder.NewDefaultRecorder(recorderParams[0], startTime)
	if err != nil {
		slog.Error("cannot create recorder recorder", "error", err)
		os.Exit(3)
	}
	ctx, cancelFunc := context.WithCancel(context.Background())
	err = defaultRecorder.Start(ctx)
	if err != nil {
		slog.Error("cannot start recorder recorder", "error", err)
		os.Exit(4)
	}
	waitForSignalAndShutdown(cancelFunc)

}

func waitForSignalAndShutdown(cancelFunc context.CancelFunc) {
	slog.Info("Waiting until quit...")
	quit := make(chan os.Signal, 1)

	/// Use signal.Notify() to listen for incoming SIGINT and SIGTERM signals and relay them to the quit channel.
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)
	s := <-quit
	slog.Warn("Cleanup and Exit!", "signal", s.String())
	cancelFunc()
	//TODO: Clean up and exit code here.

}
