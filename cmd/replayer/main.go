package main

import (
	"context"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/replayer"
	"k8s.io/utils/env"
	"log/slog"
	"os"
	"runtime"
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
			os.Exit(1)
		}
	}

	if mode == "in-utility-cluster" {
		if strings.HasSuffix(inputDataPath, ".db") {
			err := apputil.DownloadDBFromApp(inputDataPath)
			if err != nil {
				slog.Error("Error downloading DB from app", "err", err)
				os.Exit(2)
			}
		} else if strings.HasSuffix(inputDataPath, ".json") {
			err := apputil.DownloadReportFromApp(inputDataPath)
			if err != nil {
				slog.Error("Error downloading report from app", "err", err)
				os.Exit(2)
			}
		}
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
	if !apputil.DirExists(reportDir) {
		err := os.MkdirAll(reportDir, os.ModePerm)
		if err != nil {
			slog.Error("Error creating report dir", "reportDir", reportDir, "err", err)
			os.Exit(3)
		}
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

	var autoLaunchDeps bool
	if os.Getenv("NO_AUTO_LAUNCH") == "" || os.Getenv("NO_AUTO_LAUNCH") == "false" {
		autoLaunchDeps = true
	}

	replayParams := gsh.ReplayerParams{
		InputDataPath:                inputDataPath,
		ReportDir:                    reportDir,
		VirtualAutoScalerConfigPath:  virtualAutoscalerConfig,
		VirtualClusterKubeConfigPath: "/tmp/kvcl.yaml",
		DeployParallel:               deployParallel,
		ReplayInterval:               replayInterval,
		AutoLaunchDependencies:       autoLaunchDeps,
		Mode:                         gsh.ExecutionMode(mode),
		//RecurConfigUpdate:            recurConfigUpdate,
	}

	exitCode := launch(replayParams)
	if exitCode == 0 {
		slog.Info("Exiting with OK exitCode", "exitCode", exitCode)
		return
	} else {
		slog.Warn("Exiting with BAD exitCode", "exitCode", exitCode)
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
