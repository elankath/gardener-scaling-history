package main

import (
	"context"
	"errors"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/replayer"
	"k8s.io/utils/env"
	"log/slog"
	"os"
	"strconv"
	"strings"
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

func GetBool(name string, defVal bool) bool {
	valStr := os.Getenv(name)
	if valStr == "" {
		slog.Warn("env not set, assuming default", "name", name, "default", defVal)
		return defVal
	}
	val, err := strconv.ParseBool(valStr)
	if err != nil {
		slog.Error("cannot parse the env val as boolean", "name", name, "val", valStr)
		os.Exit(1)
	}
	return val
}

func main() {
	inputDataPath := os.Getenv("INPUT_DATA_PATH")
	if len(inputDataPath) == 0 {
		slog.Error("INPUT_DATA_PATH env MUST be set. Must be either a scenario .json file or a recorded .db path")
		os.Exit(1)
	}
	waitDuration := 15 * time.Second
	waitNum := 0
	for {
		fileInfo, err := os.Stat(inputDataPath)
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("No file found at inputDataPath - waiting for work...", "inputDataPath", inputDataPath, "waitNum", waitNum, "waitDuration", waitDuration)
			<-time.After(waitDuration)
			waitNum += 1
			continue
		} else if err != nil {
			slog.Error("cannot getting stat for inputDataPath", "inputDataPath", inputDataPath, "err", err)
			os.Exit(1)
		}
		slog.Info("file found at inputDataPath", "inputDataPath", inputDataPath, "fileInfoSize", fileInfo.Size(), "fileInfoTime", fileInfo.ModTime())
		break
	}
	//virtualClusterKubeConfig := os.Getenv("KUBECONFIG")
	//if len(virtualClusterKubeConfig) == 0 {
	//	virtualClusterKubeConfig = "/tmp/kvcl.yaml"
	//	slog.Warn("KUBECONFIG env must be set. Assuming path.", "virtualClusterKubeConfig", virtualClusterKubeConfig)
	//	if !apputil.FileExists(virtualClusterKubeConfig) {
	//		slog.Error("virtualClusterKubeConfig does not exist. Exiting")
	//		os.Exit(1)
	//	}
	//}
	if strings.HasSuffix(inputDataPath, ".db") {
		autoScalerBinaryPath := "bin/cluster-autoscaler"
		if !apputil.FileExists(autoScalerBinaryPath) {
			slog.Error("Virtual Autoscaler binary is expected at relative path. Kindly execute ./hack/build-replayer.sh .", "autoScalerBinaryPath", autoScalerBinaryPath)
			os.Exit(2)
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

	deployParallel, err := env.GetInt("DEPLOY_PARALLEL", 20)
	if err != nil {
		slog.Error("cannot parse the env val as int", "name", "DEPLOY_PARALLEL", "error", err)
		os.Exit(1)
	}
	replayInterval := GetDuration("REPLAY_INTERVAL", replayer.DefaultReplayInterval)

	replayParams := gsh.ReplayerParams{
		InputDataPath:                inputDataPath,
		ReportDir:                    reportDir,
		VirtualAutoScalerConfigPath:  virtualAutoscalerConfig,
		VirtualClusterKubeConfigPath: "/tmp/kvcl.yaml",
		DeployParallel:               deployParallel,
		ReplayInterval:               replayInterval,
		//RecurConfigUpdate:            recurConfigUpdate,
	}

	exitCode := launch(replayParams)
	if exitCode == 0 {
		return
	} else {
		os.Exit(exitCode)
	}

}

func launch(replayParams gsh.ReplayerParams) int {
	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()
	defaultReplayer, err := replayer.NewDefaultReplayer(ctx, replayParams)
	if err != nil {
		slog.Error("cannot construct the default replayer", "error", err)
		return 1
	}
	defer func() {
		err := defaultReplayer.Close()
		if err != nil {
			slog.Error("problem closing replayer", "error", err)
		}
	}()
	go func() {
		apputil.WaitForSignalAndShutdown(ctx, cancelFn)
		//err = defaultReplayer.Close()
		//if err != nil {
		//	slog.Error("problem closing replayer", "error", err)
		//}
	}()

	err = defaultReplayer.Start()
	if err != nil {
		slog.Error("replayer had an issue.", "error", err)
		return 1
	}
	slog.Info("Replay Successfully Finished")
	return 0
}
