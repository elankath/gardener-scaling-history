package apputil

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"github.com/elankath/gardener-scaling-common"
	gsh "github.com/elankath/gardener-scaling-history"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"slices"
	"strings"
	"syscall"
)

func WaitForSignalAndShutdown(cancelFunc context.CancelFunc) {
	slog.Info("Waiting until quit...")
	quit := make(chan os.Signal, 1)

	/// Use signal.Notify() to listen for incoming SIGINT and SIGTERM signals and relay them to the quit channel.
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)
	s := <-quit
	slog.Warn("Cleanup and Exit!", "signal", s.String())
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

//func ListAllNodes(ctx context.Context, clientSet *kubernetes.Clientset)

func CreateLandscapeClient(kubeconfigPath string, mode gsh.RecorderMode) (*kubernetes.Clientset, error) {
	landscapeConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	if mode == gsh.InUtilityClusterRecorderMode {
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

	//expirationSecs := 86400
	expirationSecs := 600
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

func GetLandscapeKubeconfigs(mode gsh.RecorderMode) (map[string]string, error) {
	landscapeKubeconfigs := make(map[string]string)
	if mode == gsh.InUtilityClusterRecorderMode {
		landscapeKubeconfigs["live"] = "/app/secrets/gardens/garden-live"
		landscapeKubeconfigs["canary"] = "/app/secrets/gardens/garden-canary"
		landscapeKubeconfigs["staging"] = "/app/secrets/gardens/garden-staging"
		landscapeKubeconfigs["dev"] = "/app/secrets/gardens/garden-dev"
	} else if mode == gsh.LocalRecorderMode {
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
