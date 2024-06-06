package recorder

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type ConnChecker struct {
	shootConfig      rest.Config
	shootClientSet   *kubernetes.Clientset
	controlConfig    rest.Config
	controlClientSet *kubernetes.Clientset
}

func NewConnChecker(shootConfig, controlConfig *rest.Config) (*ConnChecker, error) {
	shootClientset, err := kubernetes.NewForConfig(shootConfig)
	if err != nil {
		return nil, err
	}
	controlClientset, err := kubernetes.NewForConfig(controlConfig)
	if err != nil {
		return nil, err
	}
	return &ConnChecker{
		shootConfig:      *shootConfig,
		controlConfig:    *controlConfig,
		shootClientSet:   shootClientset,
		controlClientSet: controlClientset,
	}, nil
}

func (c *ConnChecker) TestConnection(ctx context.Context) error {
	//TODO do better test connection
	_, err := c.shootClientSet.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("shoot connection test failed : %w", err)
	}

	_, err = c.controlClientSet.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("seed connection test failed : %w", err)
	}

	return nil
}
