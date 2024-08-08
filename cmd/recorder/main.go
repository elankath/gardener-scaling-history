package main

import (
	"context"
	"encoding/csv"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/recorder"
	"github.com/elankath/gardener-scaling-history/server"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"
)

const CLUSTERS_CFG_FILE = "clusters.csv"

func main() {
	pwd, err := os.Getwd()
	if err != nil {
		slog.Error("error fetching current working directory", "err", err)
		os.Exit(1)
	}
	slog.Info("Current working directory", "pwd", pwd)
	configDir := os.Getenv("CONFIG_DIR")
	if len(configDir) == 0 {
		slog.Error("CONFIG_DIR env must be set")
		os.Exit(1)
	}
	dbDir := os.Getenv("DB_DIR")
	if len(dbDir) == 0 {
		slog.Warn("DB_DIR is NOT set. Assuming same as CONFIG_DIR", "config_dir", configDir)
		dbDir = configDir
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
	var recorderParams = make([]gsh.RecorderParams, len(records))
	for rowIndex, row := range records {
		if len(row) != 4 {
			slog.Error("Invalid row in cluster config. Should be 4 columns in row: Landscape, ShootNameSpace, ShootKubeConfigPath, SeedKubeConfigPath", "rowIndex", rowIndex)
			os.Exit(5)
		}
		shootKubeConfigPath := row[2]
		//if !filepath.IsAbs(shootKubeConfigPath) {
		//	shootKubeConfigPath = filepath.Join(configDir, shootKubeConfigPath)
		//}
		seedKubeConfigPath := row[3]
		//if !filepath.IsAbs(seedKubeConfigPath) {
		//	seedKubeConfigPath = filepath.Join(configDir, seedKubeConfigPath)
		//}
		if _, err := os.Stat(shootKubeConfigPath); os.IsNotExist(err) {
			slog.Error("Shoot kubeconfig does not exist", "path", shootKubeConfigPath)
			os.Exit(6)
		}
		if _, err := os.Stat(seedKubeConfigPath); os.IsNotExist(err) {
			slog.Error("Seed kubeconfig does not exist", "path", seedKubeConfigPath)
			os.Exit(6)
		}
		if _, err := os.Stat(dbDir); os.IsNotExist(err) {
			slog.Error("DB Dir", "path", dbDir)
			os.Exit(6)
		}
		recorderParams[rowIndex] = gsh.RecorderParams{
			Landscape:           row[0],
			ShootNameSpace:      row[1],
			ShootKubeConfigPath: shootKubeConfigPath,
			SeedKubeConfigPath:  seedKubeConfigPath,
			DBDir:               dbDir,
		}
	}

	if len(recorderParams) == 0 {
		slog.Error("No shootKubeConfigs found in CONFIG_DIR")
		os.Exit(3)
	}

	slog.Info("Will monitor, record & analyze clusters for scaling history", "shootKubeConfigs", recorderParams[0].ShootKubeConfigPath, "dbdir", dbDir)

	ctx, cancelFunc := context.WithCancel(context.Background())
	for _, rp := range recorderParams {
		startTime := time.Now()
		defaultRecorder, err := recorder.NewDefaultRecorder(rp, startTime)
		if err != nil {
			slog.Error("cannot create recorder recorder", "error", err)
			os.Exit(3)
		}
		err = defaultRecorder.Start(ctx)
		slog.Info("STARTED recorder", "startTime", startTime, "params", rp)
		if err != nil {
			slog.Error("cannot start recorder recorder", "error", err)
			os.Exit(4)
		}
	}

	//launch engine in a goroutine
	go func() {
		err := server.Launch(ctx)
		if err != nil {
			slog.Error("cannot launch server", "error", err)
			os.Exit(5)
		}
	}()

	apputil.WaitForSignalAndShutdown(cancelFunc) //blocking call

}
