package main

import (
	"context"
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
	//if mode == string(gsh.InUtilityClusterMode) {
	//	exitCode = launchInUtilityClusterMode()
	//} else {
	//	exitCode = launchInLocalMode()
	//}
	if len(mode) == 0 {
		slog.Error("MODE environment variable must be set. Choices are local or in-utility-cluster")
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exitCode = launch(ctx, cancel, gsh.RecorderMode(mode))
	if exitCode == 0 {
		return
	} else {
		os.Exit(exitCode)
	}

}

func launch(ctx context.Context, cancelFunc context.CancelFunc, mode gsh.RecorderMode) int {
	configDir := os.Getenv("CONFIG_DIR")
	if len(configDir) == 0 {
		slog.Error("CONFIG_DIR env must be set")
		return 1
	}
	dbDir := os.Getenv("DB_DIR")
	if len(dbDir) == 0 {
		slog.Error("DB_DIR env must be set")
		return 2
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
		defaultRecorder, err := recorder.NewDefaultRecorder(rp, startTime)
		if err != nil {
			slog.Error("cannot create recorder recorder", "error", err)
			return 5
		}
		err = defaultRecorder.Start(ctx)
		slog.Info("STARTED recorder", "startTime", startTime, "params", rp)
		if err != nil {
			slog.Error("cannot start recorder recorder", "error", err)
			return 6
		}
	}
	apputil.WaitForSignalAndShutdown(cancelFunc)
	return 0
}

//func launchInUtilityClusterMode() int {
//	ctx, cancelFunc := context.WithCancel(context.Background())
//	defer cancelFunc()
//	dbDir := os.Getenv("DB_DIR")
//	if len(dbDir) == 0 {
//		slog.Error("DB_DIR must be set")
//		return 1
//	}
//
//	shootNamespace := os.Getenv("SHOOT_NAMESPACE")
//	if len(shootNamespace) == 0 {
//		slog.Error("SHOOT_NAMESPACE must be set")
//		return 2
//	}
//
//	//Create landscape client
//	client, err := apputil.CreateLandscapeClient("/app/secrets/gardens/garden-live")
//	if err != nil {
//		slog.Error("cannot create landscape client", "error", err)
//		return 3
//	}
//
//	//Create client for shoot
//	shootKubeconfigPath, err := GetViewerKubeconfig(ctx, client, "garden-hc-canary", "prod-hna0")
//	if err != nil {
//		slog.Error("cannot get shoot kubeconfig", "error", err)
//		return 4
//	}
//
//	//Create client for seed
//	seedKubeconfigPath, err := GetViewerKubeconfig(ctx, client, "garden", "aws-eu5")
//	if err != nil {
//		slog.Error("cannot get seed kubeconfig", "error", err)
//		return 5
//	}
//
//	recorderParams := gsh.RecorderParams{
//		Landscape:           "live", //TODO: do not hardcode
//		ShootNameSpace:      shootNamespace,
//		ShootKubeConfigPath: shootKubeconfigPath,
//		SeedKubeConfigPath:  seedKubeconfigPath,
//		DBDir:               dbDir,
//	}
//
//	startTime := time.Now()
//	defaultRecorder, err := recorder.NewDefaultRecorder(recorderParams, startTime)
//	if err != nil {
//		slog.Error("cannot create recorder", "error", err, "mode", gsh.InControlPlaneMode)
//		time.Sleep(4 * time.Minute)
//		return 6
//	}
//	err = defaultRecorder.Start(ctx)
//	slog.Info("STARTED recorder", "startTime", startTime, "params", recorderParams)
//	if err != nil {
//		slog.Error("cannot start recorder", "error", err, "mode", gsh.InControlPlaneMode)
//		time.Sleep(4 * time.Minute)
//		return 7
//	}
//
//	apputil.WaitForSignalAndShutdown(cancelFunc)
//
//	return 0
//
//}
