package main

import (
	"context"
	"fmt"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"os"
)

func main() {
	// Load kubeconfig file
	ctx := context.Background()
	inClusterClientset, err := createInClusterClient()
	if err != nil {
		slog.Error("error creating in-cluster client: %v", err)
		os.Exit(1)
	}
	url := "/apis/core.gardener.cloud/v1beta1/namespaces/garden-hc-canary/shoots/prod-hna0/viewerkubeconfig"

	restClient := inClusterClientset.CoreV1().RESTClient()

	payload := `{
      'apiVersion': 'authentication.gardener.cloud/v1alpha1',
      'kind': 'ViewerKubeconfigRequest',
      'spec': {'expirationSeconds': 86400}}`

	result := restClient.Post().AbsPath(url).Body([]byte(payload)).Do(ctx)

	if result.Error() != nil {
		slog.Error("Could not create viewerkubeconfig request", "err", result.Error())
		os.Exit(2)
	}

	responsePayload, err := result.Raw()
	if err != nil {
		slog.Error("Could not read result viewerconfig payload", "err", err)
	}

	slog.Info("Successfully created viewerkubeconfig request", "responsePayload", string(responsePayload))
}

func createInClusterClient() (*kubernetes.Clientset, error) {
	inClusterConfig, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	// Create clientset
	inClusterClientset, err := kubernetes.NewForConfig(inClusterConfig)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}

	return inClusterClientset, nil
}
