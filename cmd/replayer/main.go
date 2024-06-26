package main

import (
	"context"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/replayer"
	"log/slog"
	"os"
)

func main() {

	dbPath := os.Getenv("DB_PATH")
	if len(dbPath) == 0 {
		slog.Error("DB_PATH env must be set")
		os.Exit(1)
	}
	reportDir := os.Getenv("REPORT_DIR")
	if len(reportDir) == 0 {
		slog.Error("REPORT_DIR env must be set")
		os.Exit(1)
	}
	virtualAutoScalerConfig := os.Getenv("VIRTUAL_AUTOSCALER_CONFIG")
	if len(virtualAutoScalerConfig) == 0 {
		slog.Error("VIRTUAL_AUTOSCALER_CONFIG env must be set")
		os.Exit(1)
	}
	virtualClusterKubeConfig := os.Getenv("VIRTUAL_CLUSTER_KUBECONFIG")
	if len(virtualAutoScalerConfig) == 0 {
		slog.Error("VIRTUAL_CLUSTER_KUBECONFIG env must be set")
		os.Exit(1)
	}
	defaultReplayer, err := replayer.NewDefaultReplayer(gsh.ReplayerParams{
		DBPath:                   dbPath,
		ReportDir:                reportDir,
		VirtualAutoScalerConfig:  virtualAutoScalerConfig,
		VirtualClusterKubeConfig: virtualClusterKubeConfig,
	})
	if err != nil {
		slog.Error("cannot contruct the default replayer", "error", err)
		os.Exit(1)
	}
	//TODO make context cancellable and listen for shutdown signal
	err = defaultReplayer.Start(context.Background())
	if err != nil {
		slog.Error("cannot start the replayer", "error", err)
		os.Exit(1)
	}

}
