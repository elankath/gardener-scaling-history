package main

import (
	"context"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/replayer"
	"log/slog"
	"os"
	"strconv"
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
		slog.Error("DB_PATH env MUST be set")
		os.Exit(1)
	}
	virtualClusterKubeConfig := os.Getenv("KUBECONFIG")
	if len(virtualClusterKubeConfig) == 0 {
		virtualClusterKubeConfig = "/tmp/vck.yaml"
		slog.Warn("KUBECONFIG env must be set. Assuming path.", "virtualClusterKubeConfig", virtualClusterKubeConfig)
		if !apputil.FileExists(virtualClusterKubeConfig) {
			slog.Error("virtualClusterKubeConfig does not exist. Exiting")
			os.Exit(1)
		}
	}
	reportDir := os.Getenv("REPORT_DIR")
	if len(reportDir) == 0 {
		reportDir = "/tmp"
		slog.Warn("REPORT_DIR not set. Assuming tmp dir", "reportDir", reportDir)
	}
	virtualAutoscalerConfig := os.Getenv("VIRTUAL_AUTOSCALER_CONFIG")
	if len(virtualAutoscalerConfig) == 0 {
		virtualAutoscalerConfig = "/tmp/vas-config.json"
		slog.Error("VIRTUAL_AUTOSCALER_CONFIG env is not set - Assuming path.", "virtualAutoscalerConfig", virtualAutoscalerConfig)
	}

	stabilizeInterval := GetDuration("STABILIZE_INTERVAL", replayer.DefaultStabilizeInterval)
	totalReplayTime := GetDuration("TOTAL_REPLAY_TIME", replayer.DefaultTotalReplayTime)
	replayInterval := GetDuration("REPLAY_INTERVAL", replayer.DefaultReplayInterval)
	recurConfigUpdateBool := os.Getenv("RECUR_CONFIG_UPDATE")
	var recurConfigUpdate bool
	if recurConfigUpdateBool != "" {
		var err error
		recurConfigUpdate, err = strconv.ParseBool(recurConfigUpdateBool)
		if err != nil {
			slog.Error("RECUR_CONFIG_UPDATE must be a boolean")
			os.Exit(1)
		}
	}

	defaultReplayer, err := replayer.NewDefaultReplayer(gsh.ReplayerParams{
		DBPath:                       dbPath,
		ReportDir:                    reportDir,
		VirtualAutoScalerConfigPath:  virtualAutoscalerConfig,
		VirtualClusterKubeConfigPath: virtualClusterKubeConfig,
		TotalReplayTime:              totalReplayTime,
		StabilizeInterval:            stabilizeInterval,
		ReplayInterval:               replayInterval,
		RecurConfigUpdate:            recurConfigUpdate,
	})
	if err != nil {
		slog.Error("cannot construct the default replayer", "error", err)
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
