package apputil

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	gsc "github.com/elankath/gardener-scaling-common"
	gsh "github.com/elankath/gardener-scaling-history"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"
)

func WaitForSignalAndShutdown(ctx context.Context, cancelFunc context.CancelFunc) {
	slog.Info("Waiting until quit...")
	quit := make(chan os.Signal, 1)

	/// Use signal.Notify() to listen for incoming SIGINT and SIGTERM signals and relay them to the quit channel.
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)
	select {
	case s := <-quit:
		slog.Warn("Received quit", "signal", s.String())
	case <-ctx.Done():
		slog.Warn("Received cancel", "ctx-error", ctx.Err())
	}
	cancelFunc()
}

func FileExists(filepath string) bool {
	fileinfo, err := os.Stat(filepath)
	if err != nil {
		return false
	}
	if fileinfo.IsDir() {
		return false
	}
	return true
}

func FilenameWithoutExtension(fp string) string {
	fn := path.Base(fp)
	return strings.TrimSuffix(fn, path.Ext(fn))
}

func DirExists(filepath string) bool {
	fileinfo, err := os.Stat(filepath)
	if err != nil {
		return false
	}
	if fileinfo.IsDir() {
		return true
	}
	return false
}

// SortPodInfosForReadability sorts the given podInfos so that application pods and unscheduled pods appear first in the slice.
func SortPodInfosForReadability(podInfos []gsc.PodInfo) {
	slices.SortFunc(podInfos, func(a, b gsc.PodInfo) int {
		s1 := a.PodScheduleStatus
		s2 := b.PodScheduleStatus

		n1 := a.Name
		n2 := b.Name
		ns1 := a.Namespace
		ns2 := b.Namespace

		if ns1 == "kube-system" && ns1 != ns2 {
			return 1
		}
		if ns2 == "kube-system" && ns2 != ns1 {
			return -1
		}
		if s1 == gsc.PodUnscheduled {
			return -1
		}
		if s2 == gsc.PodUnscheduled {
			return 1
		}
		return cmp.Compare(n1, n2)
	})
}

// SortPodInfoForDeployment sorts the given podInfos so that kube-system and higher priority pods are sorted first.
func SortPodInfoForDeployment(a, b gsc.PodInfo) int {
	ns1 := a.Namespace
	ns2 := b.Namespace
	s1 := a.PodScheduleStatus
	s2 := b.PodScheduleStatus
	p1 := a.Spec.Priority
	p2 := b.Spec.Priority
	c1 := a.CreationTimestamp
	c2 := b.CreationTimestamp
	aff1 := a.Spec.Affinity
	aff2 := b.Spec.Affinity

	// Any pod with an affinity is sorted earlier
	if aff1 != nil && aff1.NodeAffinity != nil && aff2 == nil {
		return -1
	}
	if aff1 == nil && aff2 != nil && aff2.NodeAffinity != nil {
		return 1
	}

	if ns1 == "kube-system" && ns1 != ns2 {
		return -1
	}
	if ns2 == "kube-system" && ns2 != ns1 {
		return 1
	}

	if s1 == gsc.PodScheduleCommited && s1 != s2 {
		return -1
	}

	if s2 == gsc.PodScheduleCommited && s2 != s1 {
		return 1
	}

	if p1 != nil && p2 != nil {
		// higher priority must come earlier
		return cmp.Compare(*p2, *p1)
	}
	return cmp.Compare(c1.UnixMilli(), c2.UnixMilli())
}

func SortPodInfoByCreationTimestamp(a, b gsc.PodInfo) int {
	return a.CreationTimestamp.Compare(b.CreationTimestamp)
}

//func ListAllNodes(ctx context.Context, clientSet *kubernetes.Clientset)

func CreateLandscapeClient(kubeconfigPath string, mode gsh.ExecutionMode) (*kubernetes.Clientset, error) {
	landscapeConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	if mode == gsh.InUtilityClusterMode {
		landscapeConfig.Insecure = true
	}

	landscapeClientset, err := kubernetes.NewForConfig(landscapeConfig)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}

	return landscapeClientset, nil
}

func GetViewerKubeconfig(ctx context.Context, landscapeClient *kubernetes.Clientset, landscapeName, projectName, shootName string) (string, error) {
	var projectNS string

	if projectName == "garden" {
		projectNS = projectName
	} else {
		projectNS = "garden-" + projectName
	}
	url := "/apis/core.gardener.cloud/v1beta1/namespaces/" + projectNS + "/shoots/" + shootName + "/viewerkubeconfig"

	restClient := landscapeClient.CoreV1().RESTClient()

	expirationSecs := 86400
	//expirationSecs := 600
	payload := fmt.Sprintf(`{
      "apiVersion": "authentication.gardener.cloud/v1alpha1",
      "kind": "ViewerKubeconfigRequest",
      "spec": {"expirationSeconds": %d}}`, expirationSecs)

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
	responsePath := "/tmp/" + landscapeName + "_" + projectName + "_" + shootName + ".json"
	err = os.WriteFile(responsePath, responsePayload, 0644)
	if err != nil {
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

	kubeconfigPath := "/tmp/" + landscapeName + "_" + projectName + "_" + shootName + ".yaml"
	err = os.WriteFile(kubeconfigPath, kubeconfigBytes, 0644)
	if err != nil {
		return "", err
	}

	slog.Info("Generated kubeconfig", "kubeconfigPath", kubeconfigPath, "shootname", shootName, "projectNamespace", projectNS)

	return kubeconfigPath, nil
}

func GetAdminKubeconfig(ctx context.Context, landscapeClient *kubernetes.Clientset, landscapeName, projectName, shootName string) (string, error) {
	var projectNS string

	if projectName == "garden" {
		projectNS = projectName
	} else {
		projectNS = "garden-" + projectName
	}
	url := "/apis/core.gardener.cloud/v1beta1/namespaces/" + projectNS + "/shoots/" + shootName + "/adminkubeconfig"

	restClient := landscapeClient.CoreV1().RESTClient()

	expirationSecs := 86400
	//expirationSecs := 600
	payload := fmt.Sprintf(`{
      "apiVersion": "authentication.gardener.cloud/v1alpha1",
      "kind": "AdminKubeconfigRequest",
      "spec": {"expirationSeconds": %d}}`, expirationSecs)

	result := restClient.Post().AbsPath(url).Body([]byte(payload)).Do(ctx)

	if result.Error() != nil {
		slog.Error("Could not create adminkubeconfig request", "err", result.Error())
		return "", result.Error()
	}

	responsePayload, err := result.Raw()
	if err != nil {
		slog.Error("Could not read result adminconfig payload", "err", err)
		return "", err
	}
	responsePath := "/tmp/" + landscapeName + "_" + projectName + "_" + shootName + ".json"
	err = os.WriteFile(responsePath, responsePayload, 0644)
	if err != nil {
		return "", err
	}

	payloadMap := make(map[string]any)

	err = json.Unmarshal(responsePayload, &payloadMap)
	if err != nil {
		slog.Error("cannot unmarshal adminkubeconfig payload", "err", err)
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

	kubeconfigPath := "/tmp/" + landscapeName + "_" + projectName + "_" + shootName + ".yaml"
	err = os.WriteFile(kubeconfigPath, kubeconfigBytes, 0644)
	if err != nil {
		return "", err
	}

	slog.Info("Generated admin kubeconfig", "kubeconfigPath", kubeconfigPath, "shootname", shootName, "projectNamespace", projectNS)

	return kubeconfigPath, nil
}

func GetSeedName(ctx context.Context, landscapeClient *kubernetes.Clientset, projectName string, shootName string) (seedName string, err error) {
	url := "/apis/core.gardener.cloud/v1beta1/namespaces/garden-" + projectName + "/shoots/" + shootName

	restClient := landscapeClient.CoreV1().RESTClient()

	result := restClient.Get().AbsPath(url).Do(ctx)
	if result.Error() != nil {
		slog.Error("Could not fetch shoot object", "err", result.Error(), "projectName", projectName, "shootName", shootName, "URL", url)
		err = result.Error()
		return
	}

	responsePayload, err := result.Raw()
	if err != nil {
		slog.Error("Could not read result shoot payload", "err", err)
		return
	}

	payloadMap := make(map[string]any)

	err = json.Unmarshal(responsePayload, &payloadMap)
	if err != nil {
		slog.Error("cannot unmarshal shoot payload", "err", err)
		return
	}

	shootSpecMap, ok := payloadMap["spec"].(map[string]any)
	if !ok {
		err = fmt.Errorf("cannot find spec in shoot object %q", shootName)
		return
	}

	seedName, ok = shootSpecMap["seedName"].(string)
	if !ok {
		err = fmt.Errorf("cannot find seedName in spec for shoot %q", shootName)
		return
	}

	slog.Info("Obtained seedName", "seedName", seedName, "shootName", shootName)
	return

}

func GetLandscapeKubeconfigs(mode gsh.ExecutionMode) (map[string]string, error) {
	landscapeKubeconfigs := make(map[string]string)
	if mode == gsh.InUtilityClusterMode {
		landscapeKubeconfigs["live"] = "/app/secrets/gardens/garden-live"
		landscapeKubeconfigs["canary"] = "/app/secrets/gardens/garden-canary"
		landscapeKubeconfigs["staging"] = "/app/secrets/gardens/garden-staging"
		landscapeKubeconfigs["dev"] = "/app/secrets/gardens/garden-dev"
	} else if mode == gsh.LocalMode {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		for _, landscape := range []string{"dev", "staging", "canary", "live"} {
			kubeconfigPath := path.Join(home, ".garden", "landscapes", landscape, "oidc-kubeconfig.yaml")
			_, err = os.Stat(kubeconfigPath)
			if err != nil {
				return nil, fmt.Errorf("cannot find kubeconfig for landscape %q: %w", landscape, err)
			}
			landscapeKubeconfigs[landscape] = kubeconfigPath
		}
	} else {
		return nil, fmt.Errorf("unsupported mode %q", mode)
	}
	return landscapeKubeconfigs, nil
}

// GetPodCondition extracts the provided condition from the given status and returns that.
// Returns nil and -1 if the condition is not present, and the index of the located condition.
func GetPodCondition(status *corev1.PodStatus, conditionType corev1.PodConditionType) (int, *corev1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	return GetPodConditionFromList(status.Conditions, conditionType)
}

// GetPodConditionFromList extracts the provided condition from the given list of condition and
// returns the index of the condition and the condition. Returns -1 and nil if the condition is not present.
func GetPodConditionFromList(conditions []corev1.PodCondition, conditionType corev1.PodConditionType) (int, *corev1.PodCondition) {
	if conditions == nil {
		return -1, nil
	}
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return i, &conditions[i]
		}
	}
	return -1, nil
}

func GetClusterName(replayReportPath string) (fullClusterName string, err error) {
	fileNameWithoutExtension := FilenameWithoutExtension(replayReportPath)
	idx := strings.LastIndex(fileNameWithoutExtension, "_")
	if idx == -1 {
		err = fmt.Errorf("invalid CA replay report %q", replayReportPath)
		return
	}
	fullClusterName = fileNameWithoutExtension[0:idx]
	return
}

func CopySQLiteDB(srcDBPath, dstDBPath string) error {
	//Ensure that destDB file does not exist
	err := os.Remove(dstDBPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Open source database connection
	db, err := sql.Open("sqlite3", srcDBPath)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	// Execute the VACUUM INTO command to copy the database safely
	query := fmt.Sprintf("VACUUM INTO '%s';", dstDBPath)
	_, err = db.Exec(query)
	if err != nil {
		return err
	}
	return nil
}

func DownloadDBFromApp(dbPath string) error {
	if !strings.HasSuffix(dbPath, ".db") {
		return fmt.Errorf("dbPath does not end with .db: %s", dbPath)
	}
	dbName := path.Base(dbPath)

	client := http.Client{
		Timeout: 15 * time.Minute,
	}

	dbUrl := "http://10.47.254.238/api/db/" + dbName
	slog.Info("Downloading db...", "url", dbUrl)

	resp, err := client.Get(dbUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading db failed with status code %d", resp.StatusCode)
	}

	dbfile, err := os.Create(dbPath)
	if err != nil {
		return fmt.Errorf("error creating db file: %w", err)
	}
	defer dbfile.Close()
	_, err = io.Copy(dbfile, resp.Body)
	if err != nil {
		return fmt.Errorf("error downloading db %q to path %q: %w", dbName, dbPath, err)
	}
	slog.Info("Successfully downloaded db", "path", dbPath)
	return nil
}

// IsNodeReady to check if a node is Ready (Running)
func IsNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func DownloadReportFromApp(reportPath string) error {
	if !strings.HasSuffix(reportPath, ".json") {
		return fmt.Errorf("reportPath does not end with .json: %s", reportPath)
	}
	reportName := path.Base(reportPath)

	client := http.Client{
		Timeout: 15 * time.Minute,
	}

	reportUrl := "http://10.47.254.238/api/reports/" + reportName
	slog.Info("Downloading report...", "url", reportUrl)

	resp, err := client.Get(reportUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading report failed with status code %d", resp.StatusCode)
	}

	reportFile, err := os.Create(reportPath)
	if err != nil {
		return fmt.Errorf("error creating report file: %w", err)
	}
	defer reportFile.Close()
	_, err = io.Copy(reportFile, resp.Body)
	if err != nil {
		return fmt.Errorf("error downloading report %q to path %q: %w", reportName, reportPath, err)
	}
	slog.Info("Successfully downloaded report", "path", reportPath)
	return nil
}

func UploadReport(ctx context.Context, reportPath string) error {
	reportName := path.Base(reportPath)

	client := http.Client{
		Timeout: 15 * time.Minute,
	}

	reportUrl := "http://10.47.254.238/api/reports/" + reportName
	slog.Info("Uploading report...", "reportPath", reportPath, "reportUrl", reportUrl)

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("error reading report file %q: %w", reportPath, err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", reportUrl, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("error creating report upload request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error uploading report %q: %w", reportPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("uploading report failed with status code %d", resp.StatusCode)
	}
	slog.Info("Successfully uploaded report", "reportPath", reportPath, "reportUrl", reportUrl)

	return nil
}

func GuessProvider(s gsh.Scenario) (string, error) {
	for _, nt := range s.ClusterSnapshot.AutoscalerConfig.NodeTemplates {
		for k, _ := range nt.Labels {
			if k == "topology.ebs.csi.aws.com/zone" {
				return "aws", nil
			}
			if k == "topology.gke.io/zone" {
				return "gcp", nil
			}
		}
	}

	return "", fmt.Errorf("could not guess provider for cluster")
}

func SortFileInfosByLastModifiedDesc(fileInfos []gsh.FileInfo) {
	slices.SortFunc(fileInfos, func(a, b gsh.FileInfo) int {
		return cmp.Compare(b.LastModified.UnixMilli(), a.LastModified.UnixMilli())
	})
}

func GetSRReportPath(dir, caReportFileName string) string {
	return path.Join(dir, strings.ReplaceAll(caReportFileName, "ca-replay", "sr-replay"))
}

// ListAllReplayReportPairs lists all sr and ca reports
func ListAllReplayReportPairs(dir string) (reportPathPairs map[string][]string, err error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var statInfo os.FileInfo
	reportPathPairs = make(map[string][]string)
	for _, f := range files {
		if strings.Contains(f.Name(), "ca-replay") {
			//saReportPath = os.Join(dir, strings.Replace(f.Name(), "ca-replay", "sr-replay"))
			srReportPath := GetSRReportPath(dir, f.Name())
			statInfo, err = os.Stat(srReportPath)
			if err != nil {
				slog.Info("ListAllReplayReportPairs Cannot get stat for srReportPath", "srReportPath", srReportPath, "error", err)
				continue
			}
			srLastMod := statInfo.ModTime()
			caReportPath := path.Join(dir, f.Name())
			statInfo, err = os.Stat(caReportPath)
			if err != nil {
				slog.Info("ListAllReplayReportPairs Cannot get stat for caReportPath", "caReportPath", caReportPath, "error", err)
				continue
			}
			caLastMod := statInfo.ModTime()

			//clusterName := GetClusterNameFromCAReportPath(caReportPath)

			if srLastMod.After(caLastMod) {
				//reportPathPairs[clusterName] = []string{caReportPath, srReportPath}
				reportPathPairs[f.Name()] = []string{caReportPath, srReportPath}
			}
		}
	}
	return
}

func GetClusterNameFromCAReportPath(caReportPath string) string {
	reportName := FilenameWithoutExtension(caReportPath)
	clusterName := reportName[:strings.LastIndex(reportName, "_")]
	return clusterName
}

func GetNodeName(n gsc.NodeInfo, _ int) string {
	return n.Name
}

func NodeHasMatchingName(name string) func(n gsc.NodeInfo) bool {
	return func(n gsc.NodeInfo) bool {
		if n.Name == name {
			return true
		} else {
			return false
		}
	}
}

func PodName(p gsc.PodInfo) string {
	return p.Name
}

func PodUID(p gsc.PodInfo) string {
	return p.UID
}

func ComparePodInfoByRowID(a gsc.PodInfo, b gsc.PodInfo) int {
	return cmp.Compare(b.RowID, a.RowID)
}
