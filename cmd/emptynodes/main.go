package main

import (
	"context"
	"fmt"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	ctx := context.Background()
	err := run(ctx)
	if err != nil {
		panic(err)
	}
}
func run(ctx context.Context) error {
	restConfig, err := clientcmd.BuildConfigFromFlags("", "/tmp/kvcl.yaml")
	if err != nil {
		return err
	}
	// Create clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("cannot create shoot clientset: %w", err)
	}
	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	nodeNames := lo.Map(nodeList.Items, func(item corev1.Node, _ int) string {
		return item.Name
	})
	nodeNamesSet := sets.New(nodeNames...)
	podList, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	podNodeNames := lo.FilterMap(podList.Items, func(item corev1.Pod, _ int) (podNodeName string, ok bool) {
		if item.Namespace == "kube-system" || item.Spec.NodeName == "" {
			ok = false
			return
		}
		ok = true
		podNodeName = item.Spec.NodeName
		return
	})
	nodeNamesSet.Delete(podNodeNames...)
	fmt.Println("Empty Nodes")
	for n, _ := range nodeNamesSet {
		fmt.Println(n)
	}
	return nil
}
