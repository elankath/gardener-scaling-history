package main

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/recorder"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const CLUSTERS_CFG_FILE = "clusters.csv"

func main() {
	mode := os.Getenv("MODE")
	if mode == string(gsh.InUtilityClusterMode) {
		exitCode := launchInUtilityClusterMode()
		if exitCode == 0 {
			return
		} else {
			os.Exit(exitCode)
		}
	}
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
		if !filepath.IsAbs(shootKubeConfigPath) {
			shootKubeConfigPath = filepath.Join(configDir, shootKubeConfigPath)
		}
		seedKubeConfigPath := row[3]
		if !filepath.IsAbs(seedKubeConfigPath) {
			seedKubeConfigPath = filepath.Join(configDir, seedKubeConfigPath)
		}
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
	apputil.WaitForSignalAndShutdown(cancelFunc)

}

func launchInUtilityClusterMode() int {
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	dbDir := os.Getenv("DB_DIR")
	if len(dbDir) == 0 {
		slog.Error("DB_DIR must be set")
		return 1
	}

	shootNamespace := os.Getenv("SHOOT_NAMESPACE")
	if len(shootNamespace) == 0 {
		slog.Error("SHOOT_NAMESPACE must be set")
		return 2
	}

	//Create landscape client
	client, err := CreateLandscapeClient("/app/secrets/gardens/garden-live")
	if err != nil {
		slog.Error("cannot create landscape client", "error", err)
		return 3
	}

	//Create client for shoot
	shootKubeconfigPath, err := GetViewerKubeconfig(ctx, client, "garden-hc-canary", "prod-hna0")
	if err != nil {
		slog.Error("cannot get shoot kubeconfig", "error", err)
		return 4
	}

	//Create client for seed
	seedKubeconfigPath, err := GetViewerKubeconfig(ctx, client, "garden", "aws-eu5")
	if err != nil {
		slog.Error("cannot get seed kubeconfig", "error", err)
		return 5
	}

	recorderParams := gsh.RecorderParams{
		Landscape:           "live", //TODO: do not hardcode
		ShootNameSpace:      shootNamespace,
		ShootKubeConfigPath: shootKubeconfigPath,
		SeedKubeConfigPath:  seedKubeconfigPath,
		DBDir:               dbDir,
	}

	startTime := time.Now()
	defaultRecorder, err := recorder.NewDefaultRecorder(recorderParams, startTime)
	if err != nil {
		slog.Error("cannot create recorder", "error", err, "mode", gsh.InControlPlaneMode)
		time.Sleep(4 * time.Minute)
		return 6
	}
	err = defaultRecorder.Start(ctx)
	slog.Info("STARTED recorder", "startTime", startTime, "params", recorderParams)
	if err != nil {
		slog.Error("cannot start recorder", "error", err, "mode", gsh.InControlPlaneMode)
		time.Sleep(4 * time.Minute)
		return 7
	}

	apputil.WaitForSignalAndShutdown(cancelFunc)

	return 0

}

func GetViewerKubeconfig(ctx context.Context, landscapeClient *kubernetes.Clientset, projectNS string, shootName string) (string, error) {
	url := "/apis/core.gardener.cloud/v1beta1/namespaces/" + projectNS + "/shoots/" + shootName + "/viewerkubeconfig"

	restClient := landscapeClient.CoreV1().RESTClient()

	payload := `{
      "apiVersion": "authentication.gardener.cloud/v1alpha1",
      "kind": "ViewerKubeconfigRequest",
      "spec": {"expirationSeconds": 86400}}`

	result := restClient.Post().AbsPath(url).Body([]byte(payload)).Do(ctx)

	if result.Error() != nil {
		slog.Error("Could not create viewerkubeconfig request", "err", result.Error())
		return "", result.Error()
	}

	responsePayload, err := result.Raw()
	if err != nil {
		slog.Error("Could not read result viewerconfig payload", "err", err)
		return "", err
	}

	payloadMap := make(map[string]any)

	err = json.Unmarshal(responsePayload, &payloadMap)
	if err != nil {
		slog.Error("cannot unmarshal viewerkubeconfig payload", "err", err)
		return "", err
	}

	statusMap, ok := payloadMap["status"].(map[string]any)
	if !ok {
		slog.Error("can't find status field in response payload", "payload", string(responsePayload))
		return "", fmt.Errorf("can't find status field in response payload")
	}
	encodedKubeconfig, ok := statusMap["kubeconfig"].(string)
	if !ok {
		slog.Error("Can't find kubeconfig in status map", "payload", string(responsePayload))
		return "", fmt.Errorf("can't find kubeconfig in status map")
	}
	kubeconfigBytes, err := base64.StdEncoding.DecodeString(encodedKubeconfig)
	if err != nil {
		slog.Error("error decoding kubeconfig", "error", err)
		return "", err
	}

	kubeconfigPath := "/tmp/" + projectNS + "_" + shootName + "-kubeconfig.yaml"
	err = os.WriteFile(kubeconfigPath, kubeconfigBytes, 0644)
	if err != nil {
		return "", err
	}

	slog.Info("Generated kubeconfig", "kubeconfigPath", kubeconfigPath, "shootname", shootName, "projectNamespace", projectNS)

	return kubeconfigPath, nil
}

func CreateLandscapeClient(kubeconfigPath string) (*kubernetes.Clientset, error) {
	landscapeConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	landscapeConfig.Insecure = true

	landscapeClientset, err := kubernetes.NewForConfig(landscapeConfig)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}

	return landscapeClientset, nil
}
