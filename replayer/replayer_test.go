package replayer

import (
	"context"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/samber/lo"
	assert "github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"testing"
	"time"
)

func TestNodeCreation(t *testing.T) {
	kubeconfigPath := "/Users/i585976/go/src/github.com/mcm-poc/kvcl/tmp/kubeconfig.yaml"
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	assert.Nil(t, err)
	clientSet, err := kubernetes.NewForConfig(config)
	assert.Nil(t, err)
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-a",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "dummy//:node-a",
		},
	}
	nd, err := clientSet.CoreV1().Nodes().Create(context.Background(), &node, metav1.CreateOptions{})
	assert.Nil(t, err)

	nd.Spec.Taints = lo.Filter(nd.Spec.Taints, func(item corev1.Taint, index int) bool {
		return item.Key != "node.kubernetes.io/not-ready"
	})

	nd, err = clientSet.CoreV1().Nodes().Update(context.Background(), nd, metav1.UpdateOptions{})
	assert.Nil(t, err)

	nodeReadyCondition := corev1.NodeCondition{
		Type:               corev1.NodeReady,
		Status:             corev1.ConditionTrue,
		LastHeartbeatTime:  metav1.Time{Time: time.Now()},
		LastTransitionTime: metav1.Time{Time: time.Now()},
		Reason:             "KubeletReady",
		Message:            "virtual cloud provider marking node as ready",
	}

	var conditions []corev1.NodeCondition
	found := false
	for _, condition := range nd.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			conditions = append(conditions, nodeReadyCondition)
			found = true
		} else {
			conditions = append(conditions, condition)
		}
	}
	if !found {
		conditions = append(conditions, nodeReadyCondition)
	}

	nd.Status.Conditions = conditions
	nd.Status.Phase = corev1.NodeRunning
	nd, err = clientSet.CoreV1().Nodes().UpdateStatus(context.Background(), nd, metav1.UpdateOptions{})
	assert.Nil(t, err)
}

func TestIncrementMap(t *testing.T) {
	mp := make(map[string]int)
	mp["a"]++
	assert.Equal(t, 1, mp["a"])
	mp["a"]++
	assert.Equal(t, 2, mp["a"])
}

func TestGetReplayScalingRecommenderReportFormat(t *testing.T) {
	replayCAReportPath := "/tmp/live_hc-eu30_prod-gc-haas_ca-replay-1.json"
	replaySRReportFmt, err := GetReplayScalingRecommenderReportFormat(replayCAReportPath)
	if err != nil {
		return
	}
	assert.Nil(t, err)
	t.Logf("replayCAReportPath: %q", replaySRReportFmt)
	assert.Equal(t, "live_hc-eu30_prod-gc-haas_sr-replay-%d.json", replaySRReportFmt)
}

func TestGetRecordedClusterSnapshotTime(t *testing.T) {
	dbPath := "/Users/I034796/go/src/github.com/elankath/gardener-scaling-history/gen/live_hc-canary_prod-bds.db"
	dataAccess := db.NewDataAccess(dbPath)
	err := dataAccess.Init()
	assert.Nil(t, err)
	snapshot, err := GetRecordedClusterSnapshot(dataAccess, 1, "test", time.Now())
	assert.Nil(t, err)
	for _, n := range snapshot.Nodes {
		assert.True(t, n.AllocatableVolumes > 0, "node.AllocatableVolumes should be greater than 0 for node", n.Name)
	}
}

//
