package main

import (
	"context"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/replayer"
	"log/slog"
	"os"
	"time"
)

func GetDuration(name string, defVal time.Duration) time.Duration {
	val := os.Getenv(name)
	if val == "" {
		slog.Warn("env not set, assuming default", "name", name, "default", defVal)
		return defVal
	}
	duration, err := time.ParseDuration(val)
	if err != nil {
		slog.Error("cannot parse the env val as duration", "name", name)
		os.Exit(1)
	}
	return duration
}

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

	stabilizeInterval := GetDuration("STABILIZE_INTERVAL", replayer.DefaultStabilizeInterval)
	totalReplayTime := GetDuration("TOTAL_REPLAY_TIME", replayer.DefaultTotalReplayTime)
	replayInterval := GetDuration("REPLAY_INTERVAL", replayer.DefaultReplayInterval)

	defaultReplayer, err := replayer.NewDefaultReplayer(gsh.ReplayerParams{
		DBPath:                       dbPath,
		ReportDir:                    reportDir,
		VirtualAutoScalerConfigPath:  virtualAutoScalerConfig,
		VirtualClusterKubeConfigPath: virtualClusterKubeConfig,
		TotalReplayTime:              totalReplayTime,
		StabilizeInterval:            stabilizeInterval,
		ReplayInterval:               replayInterval,
	})
	if err != nil {
		slog.Error("cannot contruct the default replayer", "error", err)
		os.Exit(1)
	}

	//TODO listen for shutdown and call cancel Fn
	ctx, cancelFn := context.WithTimeout(context.Background(), totalReplayTime)
	err = defaultReplayer.Start(ctx)
	if err != nil {
		slog.Error("cannot start the replayer", "error", err)
		os.Exit(1)
	}

	go apputil.WaitForSignalAndShutdown(cancelFn)

	err = defaultReplayer.Replay(ctx)
	if err != nil {
		slog.Error("cannot replay on the replayer", "error", err)
		os.Exit(3)
	}

}
