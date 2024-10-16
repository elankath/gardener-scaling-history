package replayer

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"github.com/elankath/gardener-scaling-common/resutil"
	"io"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/set"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	gsc "github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-common/clientutil"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/elankath/gardener-scaling-history/recorder"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
)

const DefaultReplayInterval = time.Duration(5 * time.Minute)

var ErrNoScenario = errors.New("no-scenario")

type ReplayMode int

const ReplayCAMode ReplayMode = 0
const ReplayScalingRecommenderMode ReplayMode = 1

type defaultReplayer struct {
	ctx                      context.Context
	kvclCancelFn             context.CancelFunc
	scalerCancelFn           context.CancelFunc
	replayMode               ReplayMode
	dataAccess               *db.DataAccess
	clientSet                *kubernetes.Clientset
	params                   gsh.ReplayerParams
	replayCount              int
	lastClusterSnapshot      gsc.ClusterSnapshot
	reportPathFormat         string
	nextScaleUpRunBeginIndex int
	scaleUpEvents            []gsc.EventInfo
	currentEventIndex        int
	lastScalingRun           ScalingRun
	inputScenario            gsh.Scenario
	lastScenario             gsh.Scenario
	kvclCmd                  *exec.Cmd
	scalerCmd                *exec.Cmd
}

type ScalingRun struct {
	BeginIndex int
	EndIndex   int
	BeginTime  time.Time
	EndTime    time.Time
}

func (sr ScalingRun) IsZero() bool {
	return sr.BeginTime.IsZero()
}
func (sr ScalingRun) Equal(o ScalingRun) bool {
	return sr.BeginIndex == o.BeginIndex && sr.EndIndex == o.EndIndex && sr.BeginTime == o.BeginTime && sr.EndTime == o.EndTime
}

var _ gsh.Replayer = (*defaultReplayer)(nil)

func NewDefaultReplayer(ctx context.Context, params gsh.ReplayerParams) (gsh.Replayer, error) {
	var replayMode ReplayMode
	var dataAccess *db.DataAccess
	var kvclCmd *exec.Cmd
	var err error
	if strings.HasSuffix(params.InputDataPath, ".db") {
		replayMode = ReplayCAMode
		dataAccess = db.NewDataAccess(params.InputDataPath)
	} else if strings.HasSuffix(params.InputDataPath, ".json") {
		replayMode = ReplayScalingRecommenderMode
	} else {
		return nil, fmt.Errorf("invalid DB path for DB-report %q", params.InputDataPath)
	}
	kvclCtx, kvclCancelFn := context.WithCancel(ctx)
	if params.AutoLaunchDependencies {
		kvclCmd, err = launchKvcl(kvclCtx)
		if err != nil {
			kvclCancelFn()
			return nil, err
		}
	}
	config, err := clientcmd.BuildConfigFromFlags("", params.VirtualClusterKubeConfigPath)
	if err != nil {
		kvclCancelFn()
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	// Create clientset
	config.QPS = 30
	config.Burst = 20
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		kvclCancelFn()
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}

	dr := &defaultReplayer{
		ctx:          ctx,
		kvclCancelFn: kvclCancelFn,
		dataAccess:   dataAccess,
		replayMode:   replayMode,
		clientSet:    clientset,
		params:       params,
		kvclCmd:      kvclCmd,
	}

	context.AfterFunc(dr.ctx, func() {
		_ = dr.doClose()
	})

	return dr, nil
}

func writeAutoscalerConfig(id string, autoscalerConfig gsc.AutoscalerConfig, path string) error {
	for i := 0; i < len(autoscalerConfig.ExistingNodes); i++ {
		autoscalerConfig.ExistingNodes[i].ProviderID = autoscalerConfig.ExistingNodes[i].Name
	}
	data, err := json.Marshal(autoscalerConfig)
	if err != nil {
		return err
	}
	err = os.WriteFile(path, data, 0644)
	if err != nil {
		return err
	}
	slog.Info("writeAutoscalerConfig success.", "id", id, "path", path)
	return nil
}

func (r *defaultReplayer) Replay() error {
	if r.replayMode == ReplayCAMode {
		return r.ReplayCA()
	} else {
		// slog.Info("Running scenario report against scaling-recommender. Please ensure that the scaling-recommender has been started.")
		return r.ReplayScalingRecommender()
	}
}

func adjustSchedulerName(pods []gsc.PodInfo) {
	for i := 0; i < len(pods); i++ {
		pods[i].Spec.SchedulerName = "bin-packing-scheduler"
	}
}

func (r *defaultReplayer) ReplayScalingRecommender() error {
	//for _, s := range r.inputScenario {
	s := r.inputScenario
	err := writeAutoscalerConfig(s.ClusterSnapshot.ID, s.ClusterSnapshot.AutoscalerConfig, r.params.VirtualAutoScalerConfigPath)
	if err != nil {
		err = fmt.Errorf("cannot write autoscaler config at time %q to path %q: %w", s.ClusterSnapshot.SnapshotTime, r.params.VirtualAutoScalerConfigPath, err)
		return err
	}
	err = syncNodes(r.ctx, r.clientSet, s.ClusterSnapshot.ID, s.ClusterSnapshot.AutoscalerConfig.ExistingNodes)
	if err != nil {
		return err
	}
	adjustSchedulerName(s.ClusterSnapshot.Pods)
	_, err = r.computeAndApplyDeltaWork(r.ctx, s.ClusterSnapshot, nil)
	if err != nil {
		return err
	}
	//writeClusterSnapshot(s.ClusterSnapshot)
	stabilizeInterval := 30 * time.Second
	numPods := len(s.ClusterSnapshot.Pods)
	stabilizeInterval = stabilizeInterval + time.Duration(numPods)*100*time.Millisecond
	slog.Info("waiting for a stabilize interval before posting cluster snapshot", "stabilizeInterval", stabilizeInterval)
	<-time.After(stabilizeInterval)
	if err = postClusterSnapshot(s.ClusterSnapshot); err != nil {
		return err
	}

	r.lastClusterSnapshot = s.ClusterSnapshot
	slog.Info("waiting for a stabilize interval after posting cluster snapshot", "stabilizeInterval", stabilizeInterval)
	<-time.After(stabilizeInterval)
	outputScenario, err := r.createScenario(r.ctx, s.ClusterSnapshot)
	if err != nil {
		if errors.Is(err, ErrNoScenario) {
			//continue
			slog.Error("No output scenario created by scaling recommender", "err", err)
		}
		return err
	}
	err = r.writeScenario(outputScenario, s.ClusterSnapshot)
	if err != nil {
		return err
	}
	//}
	return nil
}

func postClusterSnapshot(cs gsc.ClusterSnapshot) error {
	reqURL := "http://localhost:8080/recommend/"
	slog.Info("Posting clusterSnapshot to scaling-recommender...", "requestURL", reqURL)
	reqBytes, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	r := bytes.NewReader(reqBytes)
	req, err := http.NewRequest(http.MethodPost, reqURL, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode == http.StatusOK {
		var resBytes []byte
		resBytes, err = io.ReadAll(res.Body)
		if err != nil {
			return err
		}
		slog.Info("recommender response", "body", string(resBytes))
	} else {
		slog.Error("failed simulation", "StatusCode", res.StatusCode, "Status", res.Status)
		return fmt.Errorf("failed simulation: %s, StatusCode: %d, Status:%s", cs.ID, res.StatusCode, res.Status)
	}
	return nil
}

func syncNodes(ctx context.Context, clientSet *kubernetes.Clientset, snapshotID string, nodeInfos []gsc.NodeInfo) error {
	deletedNodeNames := sets.New[string]()
	nodeInfosByName := lo.Associate(nodeInfos, func(item gsc.NodeInfo) (string, struct{}) {
		return item.Name, struct{}{}
	})
	virtualNodes, err := clientutil.ListAllNodes(ctx, clientSet)
	if err != nil {
		return fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
	}
	virtualNodesMap := lo.KeyBy(virtualNodes, func(item corev1.Node) string {
		return item.Name
	})

	for _, vn := range virtualNodes {
		_, ok := nodeInfosByName[vn.Name]
		if ok {
			continue
		}
		err := clientSet.CoreV1().Nodes().Delete(ctx, vn.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("%s | cannot delete the virtual node %q: %w", snapshotID, vn.Name, err)
		}
		deletedNodeNames.Insert(vn.Name)
		//delete(virtualNodesMap, vn.Name)
		slog.Info("%s | synchronizeNodes deleted the virtual node %q", snapshotID, vn.Name)
	}
	podsList, err := clientutil.ListAllPods(ctx, clientSet)
	if err != nil {
		return fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
	}
	for _, pod := range podsList {
		if deletedNodeNames.Has(pod.Spec.NodeName) {
			pod.Spec.NodeName = ""
			_, err := clientSet.CoreV1().Pods(pod.Namespace).Update(ctx, &pod, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("cannot update the pod %q: %w", pod.Name, err)
			}
			slog.Info("Cleared NodeName from pod", "pod.Name", pod.Name)
		}
	}

	for _, nodeInfo := range nodeInfos {
		oldVNode, exists := virtualNodesMap[nodeInfo.Name]
		var sameLabels, sameTaints bool
		if exists {
			sameLabels = maps.Equal(oldVNode.Labels, nodeInfo.Labels)
			sameTaints = slices.EqualFunc(oldVNode.Spec.Taints, nodeInfo.Taints, gsc.IsEqualTaint)
		}
		if exists && sameLabels && sameTaints {
			continue
		}
		node := corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:      nodeInfo.Name,
				Namespace: nodeInfo.Namespace,
				Labels:    nodeInfo.Labels,
			},
			Spec: corev1.NodeSpec{
				Taints:     nodeInfo.Taints,
				ProviderID: nodeInfo.ProviderID,
			},
			Status: corev1.NodeStatus{
				Capacity:    nodeInfo.Capacity,
				Allocatable: nodeInfo.Allocatable,
			},
		}
		nodeStatus := node.Status
		if !exists {
			_, err = clientSet.CoreV1().Nodes().Create(context.Background(), &node, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(err) {
				slog.Warn("synchronizeNodes: node already exists. updating node %q", "snapshotID", snapshotID, "nodeName", node.Name)
				_, err = clientSet.CoreV1().Nodes().Update(context.Background(), &node, metav1.UpdateOptions{})
			}
			if err == nil {
				slog.Info(" synchronizeNodes CREATED node.", "snapshotID", snapshotID, "nodeName", node.Name)
			}
		} else {
			_, err = clientSet.CoreV1().Nodes().Update(context.Background(), &node, metav1.UpdateOptions{})
			slog.Info(" synchronizeNodes UPDATED node.", "snapshotID", snapshotID, "nodeName", node.Name)
		}
		if err != nil {
			return fmt.Errorf("synchronizeNodes cannot create/update node with name %q: %w", node.Name, err)
		}
		node.Status = nodeStatus
		node.Status.Conditions = buildReadyConditions()
		err = adjustNode(clientSet, node.Name, node.Status)
		if err != nil {
			return fmt.Errorf("synchronizeNodes cannot adjust the node with name %q: %w", node.Name, err)
		}
	}
	return nil
}

func adjustNode(clientSet *kubernetes.Clientset, nodeName string, nodeStatus corev1.NodeStatus) error {

	nd, err := clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cannot get node with name %q: %w", nd.Name, err)
	}
	nd.Spec.Taints = lo.Filter(nd.Spec.Taints, func(item corev1.Taint, index int) bool {
		return item.Key != "node.kubernetes.io/not-ready"
	})
	nd, err = clientSet.CoreV1().Nodes().Update(context.Background(), nd, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("cannot update node with name %q: %w", nd.Name, err)
	}
	//nd.Status.Conditions = cloudprovider.BuildReadyConditions()
	//nd.Status.Phase = corev1.NodeRunning
	//TODO set the nodeInfo in node status
	nd.Status = nodeStatus
	nd.Status.Phase = corev1.NodeRunning
	nd, err = clientSet.CoreV1().Nodes().UpdateStatus(context.Background(), nd, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("cannot update the status of node with name %q: %w", nd.Name, err)
	}
	return nil
}

func buildReadyConditions() []corev1.NodeCondition {
	lastTransition := time.Now().Add(-time.Minute)
	return []corev1.NodeCondition{
		{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		{
			Type:               corev1.NodeNetworkUnavailable,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		{
			Type:               corev1.NodeDiskPressure,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
		{
			Type:               corev1.NodeMemoryPressure,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: lastTransition},
		},
	}
}

func (r *defaultReplayer) ReplayCA() error {
	loopNum := 0
	for {
		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		default:
			loopNum++
			replayEvent := r.getNextReplayEvent()
			replayMarkTime := replayEvent.EventTime.UTC()
			replayMarkTimeMicros := replayMarkTime.UnixMicro()
			slog.Info("ReplayCA is considering replayEvent.", "replayEvent", replayEvent, "replayEventIndex", r.currentEventIndex, "replayMarkTimeMicros", replayMarkTimeMicros)
			if replayMarkTime.IsZero() {
				slog.Info("no more scale ups to be replayed. Exiting", "loopNum", loopNum)
				return nil
			}
			if replayMarkTime.After(time.Now()) {
				//slog.Warn("replayMarkTime now exceeds current time. Exiting", "BeginTime", replayRun.BeginIndex, "EndTime", replayRun.EndTime, "loopNum", loopNum, "replayCount", r.replayCount)
				slog.Warn("replayMarkTime now exceeds current time. Exiting", "replayMarkTime", replayMarkTime, "replayMarkTimeMicros", replayMarkTimeMicros, "loopNum", loopNum, "replayCount", r.replayCount)
				return nil
			}
			r.replayCount++
			slog.Info("Invoking GetRecordedClusterSnapshot with replayMarkTime.", "replayMarkTime", replayMarkTime, "replayMarkTimeMicros", replayMarkTimeMicros, "loopNum", loopNum, "replayCount", r.replayCount)
			clusterSnapshot, err := r.GetRecordedClusterSnapshot(replayMarkTime)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					slog.Info("No more recorded work after replayMarkTime! Replay done", "replayMarkTime", replayMarkTime, "replayMarkTimeMicros", replayMarkTimeMicros, "loopNum", loopNum, "replayCount", r.replayCount)
					return nil
				}
				return err
			}

			// UNCOMMENT ME FOR DIAGNOSIS ONLY
			//writeClusterSnapshot(clusterSnapshot)

			if clusterSnapshot.Hash == r.lastClusterSnapshot.Hash {
				slog.Info("skipping replay since clusterSnapshot.Hash unchanged from", "Hash", clusterSnapshot.Hash, "loopNum", loopNum, "replayMarkTime", replayMarkTime, "replayMarkTimeMicros", replayMarkTimeMicros, "replayCount", r.replayCount)
				r.lastClusterSnapshot = clusterSnapshot
				continue
			}

			//if true { //DIAGNOSIS
			//	continue
			//}
			var deletedPendingPods []corev1.Pod
			//if clusterSnapshot.AutoscalerConfig.Hash != r.lastClusterSnapshot.AutoscalerConfig.Hash {
			// Before writing autoscaler config, I must delete any unscheduled Pods .
			// Other-wise CA will call VirtualNodeGroup.IncreaseSize just after AutoScalerConfig is written
			// which isn't good since we want VirtualNodeGroup.IncreaseSize to be called only AFTER computeAndApplyDeltaWork
			deletedPendingPods, err = deletePendingUnscheduledPods(r.ctx, r.clientSet)
			if err != nil {
				err = fmt.Errorf("cannot delete pendign unscheduled pods: %w", err)
				return err
			}
			if len(deletedPendingPods) > 0 || len(r.lastScenario.ScalingResult.ScaledUpNodes) != 0 {
				//FIXME: Hack to ensure that virtual CA deletes virtually scaled nodes during its
				// sync nodes operation in VirtualNodeGroup.Refresh()
				slog.Info("RESET autoscaler config hash to clear virtual scaled nodes", "loopNum", loopNum, "replayCount", r.replayCount)
				clusterSnapshot.AutoscalerConfig.Hash = rand.String(6)
			}
			if r.lastClusterSnapshot.AutoscalerConfig.Hash != clusterSnapshot.AutoscalerConfig.Hash {
				slog.Info("writing autoscaler config", "snapshotNumber", clusterSnapshot.Number, "currHash", clusterSnapshot.AutoscalerConfig.Hash, "loopNum", loopNum, "replayCount", r.replayCount)
				err = writeAutoscalerConfigAndWaitForSignal(r.ctx, clusterSnapshot.ID, clusterSnapshot.AutoscalerConfig, r.params.VirtualAutoScalerConfigPath)
				if err != nil {
					return err
				}
			} else {
				slog.Info("skip writeAutoscalerConfigAndWaitForSignal since hash unchanged", "hash", clusterSnapshot.AutoscalerConfig.Hash, "loopNum", loopNum, "replayCount", r.replayCount)
			}
			deltaWork, err := computeDeltaWork(r.ctx, r.clientSet, clusterSnapshot, deletedPendingPods)
			if err != nil {
				return err
			}
			deltaWork.DeployParallel = r.params.DeployParallel
			if deltaWork.IsEmpty() {
				slog.Info("deltaWork is empty. Skipping this loop.", "loopNum", loopNum, "replayCount", r.replayCount)
				continue
			}

			err = applyDeltaWork(r.ctx, r.clientSet, deltaWork)
			if err != nil {
				return err
			}
			stabilizeInterval := 30 * time.Second
			numPods := len(clusterSnapshot.Pods)
			stabilizeInterval = stabilizeInterval + time.Duration(numPods)*100*time.Millisecond
			slog.Info("waiting for a stabilize interval for scheduler to finish its job", "stabilizeInterval", stabilizeInterval)
			<-time.After(stabilizeInterval)

			clusterSnapshot.AutoscalerConfig.Mode = gsc.AutoscalerReplayerRunMode
			slog.Info("writing autoscaler config with replay run mode", "snapshotNumber", clusterSnapshot.Number, "currHash", clusterSnapshot.AutoscalerConfig.Hash, "loopNum", loopNum, "replayCount", r.replayCount)
			err = writeAutoscalerConfigAndWaitForSignal(r.ctx, clusterSnapshot.ID, clusterSnapshot.AutoscalerConfig, r.params.VirtualAutoScalerConfigPath)
			if err != nil {
				return err
			}
			r.lastClusterSnapshot = clusterSnapshot
			scalingOccurred, err := waitAndCheckVirtualScaling(r.ctx, r.clientSet, r.replayCount)
			if !scalingOccurred {
				slog.Info("No virtual-scaling occurred while replaying real scaling event", "replayCount", r.replayCount, "dbPath", r.params.InputDataPath, "replayEvent", replayEvent)
				continue
			}
			slog.Info("waiting for a stabilize interval after virtual scaling", "stabilizeInterval", stabilizeInterval)
			<-time.After(stabilizeInterval)
			scenario, err := r.createScenario(r.ctx, clusterSnapshot)
			if err != nil {
				if errors.Is(err, ErrNoScenario) {
					continue
				} else {
					return err
				}
			}
			r.lastScenario = scenario
			err = r.writeScenario(scenario, clusterSnapshot)
			if err != nil {
				return err
			}
		}
	}
}

func writeAutoscalerConfigAndWaitForSignal(ctx context.Context, id string, asConfig gsc.AutoscalerConfig, asConfigWritePath string) error {
	err := writeAutoscalerConfig(id, asConfig, asConfigWritePath)
	if err != nil {
		err = fmt.Errorf("cannot write autoscaler config for snapshot %q to path %q: %w", id, asConfigWritePath, err)
		return err
	}
	err = waitForVirtualCARefresh(ctx, len(asConfig.ExistingNodes), asConfig.SuccessSignalPath, asConfig.ErrorSignalPath)
	if err != nil {
		return err
	}
	return nil
}

func waitForVirtualCARefresh(ctx context.Context, numNodes int, successSignalPath string, errorSignalPath string) error {
	slog.Info("waitForVirtualCARefresh entered..", "successSignalPath", successSignalPath, "errorSignalPath", errorSignalPath)
	waitInterval := 20 * time.Second
	timeout := 5 * time.Minute
	computedTimeout := time.Duration(3*numNodes) * time.Second
	if computedTimeout > timeout {
		timeout = computedTimeout
	}
	timeoutCh := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			slog.Warn("waitForVirtualCARefresh context cancelled or timed out:", "error", ctx.Err())
			return ctx.Err()
		case <-timeoutCh:
			slog.Error("waitForVirtualCARefresh exceeded timeoutCh.", "timeout", timeout)
			return fmt.Errorf("waitForVirtualCARefresh exceeded signalTimeout %q waiting for %q or %q", timeout, successSignalPath, errorSignalPath)
		case <-time.After(waitInterval):
			slog.Info("waitForVirtualCARefresh checking signal paths.", "successSignalPath", successSignalPath, "errorSignalPath", errorSignalPath)
			data, err := os.ReadFile(errorSignalPath)
			if data != nil {
				errorSignal := string(data)
				slog.Error("waitForVirtualCARefresh obtained error signal.", "errorSignal", errorSignal, "errorSignalPath", errorSignalPath)
				return fmt.Errorf("virtual CA signalled issue: %s", errorSignal)
			}
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("waitForVirtualCARefresh got error reading %q: %w", errorSignalPath, err)
			}
			data, err = os.ReadFile(successSignalPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("waitForVirtualCARefresh got error reading %q: %w", successSignalPath, err)
			}
			if data != nil {
				successSignal := string(data)
				slog.Info("waitForVirtualCARefresh obtained success signal.", "successSignal", successSignal, "successSignalPath", successSignalPath)
				if err = os.Remove(successSignalPath); err != nil {
					return err
				}
				return nil
			}
		}
	}
}

func writeClusterSnapshot(cs gsc.ClusterSnapshot) {
	csBytes, err := json.Marshal(cs)
	if err != nil {
		slog.Warn("Failed to marshal clusterSnapshot", "error", err)
		return
	}
	snapShotPath := fmt.Sprintf("/tmp/cs_%s.json", cs.ID)
	if err = os.WriteFile(snapShotPath, csBytes, 0644); err != nil {
		slog.Warn("Failed to write clusterSnapshot to file", "path", snapShotPath, "error", err)
		return
	}
	slog.Info("obtained recorded cluster snapshot for replay.",
		"Number", cs.Number,
		"SnapshotTime", cs.SnapshotTime,
		"snapShotPath", snapShotPath,
		"Hash", cs.Hash,
	)
}

func (r *defaultReplayer) CleanCluster(ctx context.Context) error {
	slog.Info("Cleaning the virtual cluster  nodes, priority-classes...")
	err := clearPods(ctx, r.clientSet)
	if err != nil {
		return err
	}
	nodes, err := r.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the nodes", "error", err)
		return err
	}
	slog.Info("Deleting nodes.", "numNodes", len(nodes.Items))
	for _, node := range nodes.Items {
		err = r.clientSet.CoreV1().Nodes().Delete(ctx, node.Name, metav1.DeleteOptions{})
		if err != nil {
			slog.Error("cannot delete the node", "node.Name", node.Name, "error", err)
			return err
		}
	}

	pcs, err := r.clientSet.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the priority classes", "error", err)
		return err
	}
	for _, pc := range pcs.Items {
		if strings.HasPrefix(pc.Name, "system-") {
			continue
		}
		slog.Info("Deleting Priority Class", "priorityCLass", pc.Name)
		err = r.clientSet.SchedulingV1().PriorityClasses().Delete(ctx, pc.Name, metav1.DeleteOptions{})
		if err != nil {
			slog.Error("cannot delete the priority class", "pc.Name", pc.Name, "error", err)
			return err
		}
	}
	slog.Info("cleaned the cluster, deleted all pods in all namespaces")
	return nil
}

func clearPods(ctx context.Context, c *kubernetes.Clientset) error {
	pods, err := c.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("cannot list the pods", "error", err)
		return err
	}
	for _, pod := range pods.Items {
		err = c.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		})
		if err != nil {
			slog.Error("cannot delete the pod", "pod.Name", pod.Name, "error", err)
			return err
		}
	}
	return err
}

func (r *defaultReplayer) Start() error {
	var err error
	if r.replayMode == ReplayCAMode {
		err = r.dataAccess.Init()
		if err != nil {
			return err
		}
		r.scaleUpEvents, err = r.dataAccess.LoadTriggeredScaleUpEvents()
		if err != nil {
			return err
		}
		if len(r.scaleUpEvents) == 0 {
			return fmt.Errorf("no TriggeredScaleUp events found in recorded data")
		}
		for i, suEvent := range r.scaleUpEvents {
			slog.Info("Loaded scale up event", "index", i, "event", suEvent)
		}
		slog.Info("Replayer started in replayFromDB mode")
		reportFileFormat := apputil.FilenameWithoutExtension(r.params.InputDataPath) + "_ca-replay-%d.json"
		r.reportPathFormat = path.Join(r.params.ReportDir, reportFileFormat)
		// Launch the VCA using the saved arguments.
		caSettings, err := r.dataAccess.LoadCASettingsBefore(time.Now().UTC())
		if err != nil {
			return err
		}
		if r.params.AutoLaunchDependencies {
			caCtx, caCancelFn := context.WithCancel(r.ctx)
			caCmd, err := launchCA(caCtx, r.clientSet, r.params.VirtualClusterKubeConfigPath, caSettings) // must launch CA in go-routine and return processId and error
			if err != nil {
				caCancelFn()
				return err
			}
			r.scalerCmd = caCmd
			r.scalerCancelFn = caCancelFn
		} else {
			slog.Info("NO_AUTO_LAUNCH is set. Please launch gardener-virtual-autoscaler separately.")
		}
	} else {
		sBytes, err := os.ReadFile(r.params.InputDataPath)
		if err != nil {
			return err
		}
		//var inputReport gsh.ReplayReport
		err = json.Unmarshal(sBytes, &r.inputScenario)
		if err != nil {
			return fmt.Errorf("cannot unmarshal input scenario %q: %w", r.params.InputDataPath, err)
		}
		//r.inputScenarios = inputReport.Scenarios
		//if len(r.inputScenarios) == 0 {
		//	return fmt.Errorf("no scenarios found in the report")
		//}
		slog.Info("Replayer started in replayFromReport mode")

		//reportFileName := strings.TrimSuffix(fileNameWithoutExtension, "_ca-replay") + "-report-replay.json" //OLD format
		reportFileFormat, err := GetReplayScalingRecommenderReportFormat(r.params.InputDataPath)
		if err != nil {
			return err
		}
		srCtx, srCancelFn := context.WithCancel(r.ctx)
		r.reportPathFormat = path.Join(r.params.ReportDir, reportFileFormat)
		if r.params.AutoLaunchDependencies {
			scalerCmd, err := launchScalingRecommender(srCtx, r.params.VirtualClusterKubeConfigPath, r.params.InputDataPath) // must launch scaling recommender in go-routine and return processId and error
			if err != nil {
				srCancelFn()
				return err
			}
			r.scalerCmd = scalerCmd
			r.scalerCancelFn = srCancelFn
		} else {
			slog.Info("NO_AUTO_LAUNCH is set. Please launch scaling-recommender separately.")
		}
	}
	err = r.CleanCluster(r.ctx)
	if err != nil {
		return err
	}
	return r.Replay()
}

func (r *defaultReplayer) doClose() error {
	var err error
	if r.kvclCmd != nil && r.kvclCmd.Process != nil {
		slog.Info("doClose is Killing kvcl...")
		//r.kvclCancelFn()
		err = r.kvclCmd.Process.Kill()
		if err != nil {
			slog.Warn("doClose cannot kill kvcl", "error", err)
		}
	}
	if r.scalerCmd != nil && r.scalerCmd.Process != nil {
		slog.Info("doClose is Killing scaler process...")
		//r.scalerCancelFn()
		err = r.scalerCmd.Process.Kill()
		if err != nil {
			slog.Warn("doClose cannot kill scaler", "error", err)
		}
	}
	if r.dataAccess != nil {
		slog.Info("doClose is Closing data access...")
		err = r.dataAccess.Close()
		if err != nil {
			slog.Warn("doClose cannot close dataAccess. ", "error", err)
		}
	}
	if r.params.Mode == gsh.InUtilityClusterMode {
		cleanupProcs := []string{"etcd", "kube-apiserver"}
		slog.Info("doClose is killing cleanupProcs since in mode.", "mode", r.params.Mode, "cleanupProces", cleanupProcs)
		for _, processName := range cleanupProcs {
			err = killProcessByName(processName)
			if err != nil {
				slog.Warn("doClose cannot kill proc.", "processName", processName, "error", err)
			}
		}
	}
	return err
}

func launchKvcl(ctx context.Context) (*exec.Cmd, error) {
	// TODO: change to using Start and Wait with graceful termination later
	err := killProcessByName("kube-apiserver")
	if err != nil {
		return nil, err
	}
	err = killProcessByName("etcd")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "bin/kvcl")
	cmd.Env = append(os.Environ(), fmt.Sprintf("BINARY_ASSETS_DIR=%s", "bin"))
	cmd.WaitDelay = time.Second
	slog.Info("Launching kvcl", "cmd", cmd)
	go func() {
		err := cmd.Run()
		if err != nil {
			slog.Error("Failed to launch kvcl.", "error", err, "cmd", cmd)
		}
	}()
	waitSecs := 7
	slog.Info("Waiting  for bin/kvcl to start", "waitSecs", waitSecs)
	<-time.After(time.Duration(waitSecs) * time.Second)
	return cmd, nil
}

func launchScalingRecommender(ctx context.Context, kubeconfigPath string, inputDataPath string) (*exec.Cmd, error) {
	// TODO: change to using Start and Wait with graceful termination later
	cmd := exec.CommandContext(ctx, "bin/scaling-recommender")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdin

	//FIXME: hack to get provider
	provider := "aws"
	if strings.Contains(inputDataPath, "-gc-") {
		provider = "gcp"
	}
	//cmd.Args = append(cmd.Args, fmt.Sprintf("--target-kvcl-kubeconfig=%s", kubeconfigPath), fmt.Sprintf("--provider=%s", provider), fmt.Sprintf("--binary-assets-path=%s", "/Users/i544000/go/src/github.com/elankath/gardener-scaling-history/bin"))
	cmd.Args = append(cmd.Args, fmt.Sprintf("--target-kvcl-kubeconfig=%s", kubeconfigPath), fmt.Sprintf("--provider=%s", provider), fmt.Sprintf("--binary-assets-path=%s", "bin"))
	slog.Info("Launching scaling recommender", "cmd", cmd)
	go func() {
		err := cmd.Run()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
					slog.Warn("bin/scaling-autoscaler was killed by SIGKILL (signal: killed)")
				} else {
					slog.Error("bin/scaling-autoscaler ran into error", "error", err)
				}
			}
		}
	}()
	waitSecs := 6
	slog.Info("Waiting  for bin/scaling-recommender to start", "waitSecs", waitSecs)
	<-time.After(time.Duration(waitSecs) * time.Second)
	return cmd, nil
}

func killProcessByName(name string) error {
	listStaleProcCmd := exec.Command("pgrep", "-f", name)
	slog.Info("killProcessByName listing stale processes using command.", "listStaleProcCmd", listStaleProcCmd)
	var stdout, stderr bytes.Buffer
	listStaleProcCmd.Stdout = &stdout
	listStaleProcCmd.Stderr = &stderr
	err := listStaleProcCmd.Run()
	if listStaleProcCmd.ProcessState != nil && listStaleProcCmd.ProcessState.ExitCode() == 1 {
		return nil
	}
	if err != nil {
		return err
	}
	procListStr := strings.TrimSpace(stdout.String())
	if procListStr == "" {
		slog.Info("killProcessByName found no processes found with name.", "name", name)
		return nil
	}
	pidStrings := strings.Fields(procListStr)
	if len(pidStrings) == 0 {
		slog.Info("killProcessByName found no processes found with name.", "name", name)
	}
	for _, pStr := range pidStrings {
		pid, err := strconv.Atoi(pStr)
		if err != nil {
			return fmt.Errorf("killProcessByName cannot kill process with bad pid %s: %w", pStr, err)
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("killProcessByName cannot find  process with pid %d: %w", pid, err)
		}
		err = process.Signal(syscall.SIGKILL)
		if err != nil {
			return fmt.Errorf("killProcessByName unable to signal SIGKILL to proces %d: %w", pid, err)
		}
		slog.Info("killProcessByName sent SIGKILL to process with pid.", "pid", pid)
	}
	return nil
}

func launchCA(ctx context.Context, clientSet *kubernetes.Clientset, kubeconfigPath string, settings gsc.CASettingsInfo) (*exec.Cmd, error) {
	if settings.Expander == "priority" {
		settings.Priorities = strings.TrimSpace(settings.Priorities)
		if settings.Priorities == "" {
			return nil, fmt.Errorf("launchCA found that settings.Expander is priority yet there are no persisted priorities")
		}
		// deploy the priority config map
		cmName := "cluster-autoscaler-priority-expander"
		cm := corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: "kube-system",
			},
			Data: map[string]string{
				"priorities": settings.Priorities,
			},
			BinaryData: nil,
		}
		slog.Warn("NOT DEPLOYING CONFIGMAP SINCE OVERRIDDEN TO USE LEAST WASTE")
		//settings.Expander = "least-waste"
		createdCm, err := clientSet.CoreV1().ConfigMaps("kube-system").Create(ctx, &cm, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("cannot create %q config map: %w", cmName, err)
		}
		slog.Info("Created ConfigMap", "name", cmName, "obj", createdCm)
	}
	// TODO: change to using Start and Wait with graceful termination later
	settings.IgnoreDaemonSetUtilization = true
	var args []string
	for ngName, mm := range settings.NodeGroupsMinMax {
		// --nodes=1:3:shoot--i034796--g2.shoot--i034796--g2-p1-z1
		arg := fmt.Sprintf("--nodes=%d:%d:%s", mm.Min, mm.Max, ngName)
		slog.Info("Adding --nodes arg to VCA.", "arg", arg)
		args = append(args, arg)
	}
	expanderArg := fmt.Sprintf("--expander=%s", settings.Expander)
	args = append(args, "--kubeconfig="+kubeconfigPath)
	args = append(args, expanderArg)
	args = append(args, fmt.Sprintf("--ignore-daemonsets-utilization=%t", settings.IgnoreDaemonSetUtilization))
	args = append(args, fmt.Sprintf("--max-graceful-termination-sec=%d", settings.MaxGracefulTerminationSeconds))
	args = append(args, fmt.Sprintf("--max-empty-bulk-delete=%d", settings.MaxEmptyBulkDelete))
	args = append(args, "--balance-similar-node-groups=true")
	args = append(args, "--new-pod-scale-up-delay=0s")
	args = append(args, "--max-nodes-total=4096")
	args = append(args, "--scale-down-enabled=false")
	args = append(args, "--v=2")
	args = append(args, "--expendable-pods-priority-cutoff=-10")
	args = append(args, "--kube-client-qps=30")
	args = append(args, "--kube-client-burst=20")
	//args = append(args, "--scale-down-unneeded-time=0s")
	//args = append(args, "--scale-down-delay-after-add=0s")

	caCmd := exec.Command("bin/cluster-autoscaler", args...)
	caCmd.Stdout = os.Stdout
	caCmd.Stderr = os.Stdin
	slog.Info("Launching virtual bin/cluster-autoscaler with args.", "args", args)
	//err := caCmd.Start()
	//if err != nil {
	//	output, err := caCmd.Output()
	//	if err != nil {
	//		slog.Error("Cannot get output of bin/cluster-autoscaler with args.", "error", err, "args", args)
	//		return err
	//	}
	//	slog.Error("Cannot launch bin/cluster-autoscaler with args.", "error", err, "output", output)
	//	os.Exit(9)
	//}
	go func() {
		err := caCmd.Run()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
					slog.Info("bin/cluster-autoscaler was killed by SIGKILL (signal: killed)")
				} else {
					slog.Error("bin/cluster-autoscaler ran into error", "error", err)
				}
			}
		}
	}()
	waitSecs := 8
	slog.Info("Waiting  for virtual bin/cluster-autoscaler to start", "waitSecs", waitSecs)
	<-time.After(time.Duration(waitSecs) * time.Second)
	return caCmd, nil
	//err := caCmd.Start()
	//if err != nil {
	//	slog.Error("Cannot launch bin/cluster-autoscaler with args.", "error", err, "args", args)
	//	return err
	//}
	//slog.Info("Invoked start of bin/cluster-autoscaler", "args", args)
	//go func() {
	//	err = caCmd.Wait()
	//	slog.Error("got error running virtual bin/cluster-autoscaler. Exiting", "error", err)
	//	os.Exit(9)
	//}()
}

type PodWork struct {
	// ToDelete has the Pod names to delete
	ToDelete sets.Set[types.NamespacedName]
	ToDeploy []gsc.PodInfo
}

type PriorityClassWork struct {
	ToDeploy []gsc.PriorityClassInfo
	// TODO: consider introducing ToDelete sets.Set[string] here too
}
type NamespaceWork struct {
	ToCreate sets.Set[string]
	ToDelete sets.Set[string]
	// TODO: consider introducing ToDelete here too
}

type DeltaWork struct {
	Number            int
	PriorityClassWork PriorityClassWork
	NamespaceWork     NamespaceWork
	PodWork           PodWork
	DeployParallel    int
}

func (d DeltaWork) IsEmpty() bool {
	return len(d.PodWork.ToDeploy) == 0 &&
		len(d.PodWork.ToDelete) == 0 &&
		len(d.NamespaceWork.ToCreate) == 0 &&
		len(d.NamespaceWork.ToDelete) == 0 &&
		len(d.PriorityClassWork.ToDeploy) == 0
}

func (d DeltaWork) String() string {
	var sb strings.Builder
	sb.WriteString("#")
	sb.WriteString(strconv.Itoa(d.Number))
	sb.WriteString("| ")
	sb.WriteString(fmt.Sprintf("#podsToDelete: (%d)", len(d.PodWork.ToDelete)))
	sb.WriteString(fmt.Sprintf("#podsToDeploy: (%d)", len(d.PodWork.ToDeploy)))
	//lo.Reduce(d.PodWork.ToDeploy, func(agg *strings.Builder, item gsc.PodInfo, index int) *strings.Builder {
	//	agg.WriteString(item.Name + ",")
	//	return agg
	//}, &sb)
	sb.WriteString(")")
	return sb.String()
}

func getCorePodFromPodInfo(podInfo gsc.PodInfo) corev1.Pod {
	pod := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels:    podInfo.Labels,
			Name:      podInfo.Name,
			Namespace: podInfo.Namespace,
			UID:       types.UID(podInfo.UID),
		},
		Spec: podInfo.Spec,
	}
	pod.Spec.NodeName = podInfo.NodeName
	pod.Status.NominatedNodeName = podInfo.NominatedNodeName
	return pod
}

func getNamespaces(ctx context.Context, clientSet *kubernetes.Clientset) (sets.Set[string], error) {
	virtualNamespaceList, err := clientSet.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the namespaces in virtual cluster")
		return nil, err
	}
	return sets.New[string](lo.Map(virtualNamespaceList.Items, func(item corev1.Namespace, index int) string {
		return item.Name
	})...), nil
}

func createNamespaces(ctx context.Context, clientSet *kubernetes.Clientset, nss ...string) error {
	for _, ns := range nss {
		namespace := corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		_, err := clientSet.CoreV1().Namespaces().Create(ctx, &namespace, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("cannot create the namespace %q in virtual cluster: %w", ns, err)
		}
		slog.Info("created namespace", "namespace", ns)
	}
	return nil
}

func deleteNamespaces(ctx context.Context, clientSet *kubernetes.Clientset, nss ...string) error {
	for _, ns := range nss {
		err := clientSet.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("cannot delete the namespace %q in virtual cluster: %w", ns, err)
		}
	}
	return nil
}

func applyDeltaWork(ctx context.Context, clientSet *kubernetes.Clientset, deltaWork DeltaWork) error {
	replayCount := deltaWork.Number
	slog.Info("applyDeltaWork commencing.", "replayCount", replayCount, "deltaWork", deltaWork)
	var err error
	for _, pcInfo := range deltaWork.PriorityClassWork.ToDeploy {
		_, err = clientSet.SchedulingV1().PriorityClasses().Create(ctx, &pcInfo.PriorityClass, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("applyDeltaWork cannot create the priority class %s: %w", pcInfo.Name, err)
		}
		slog.Info("applyDeltaWork successfully created the priority class", "replayCount", replayCount, "pc.Name", pcInfo.Name)
	}
	err = createNamespaces(ctx, clientSet, deltaWork.NamespaceWork.ToCreate.UnsortedList()...)
	if err != nil {
		return fmt.Errorf("applyDeltaWork cannot create namespaces: %w", err)
	}

	podsToDelete := deltaWork.PodWork.ToDelete.UnsortedList()
	for _, p := range podsToDelete {
		err = clientSet.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		})
		if err != nil {
			if apierrors.IsNotFound(err) {
				slog.Warn("applyDeltaWork cannot delete pod since already deleted", "replayCount", replayCount, "pod", p.Name, "namespace", p.Namespace, "err", err)
			} else {
				return err
			}
		}
		slog.Info("applyDeltaWork successfully deleted the pod", "replayCount", replayCount, "pod.Name", p.Name)
	}

	podsToDeploy := deltaWork.PodWork.ToDeploy
	deltaWork.DeployParallel = 1

	// deploy kube-system pods and pods that have assigned Node names first.
	//slices.SortFunc(podsToDeploy, apputil.SortPodInfoForDeployment)
	//partitionedPods := lo.PartitionBy(podsToDeploy, func(item gsc.PodInfo) string {
	//	if item.Spec.Affinity != nil && item.Spec.Affinity.NodeAffinity != nil {
	//		return "a"
	//	} else {
	//		return "b"
	//	}
	//})
	//var deployCount = 0
	podGroups := lo.GroupBy(podsToDeploy, func(p gsc.PodInfo) string {
		if p.Spec.NodeName != "" {
			return "scheduledPod"
		}
		if _, ok := p.Labels["previouslyAssignedPod"]; ok {
			return "previouslyAssignedPod"
		}
		return "unscheduledPod"
	})
	deployCount := 0
	podSlice := podGroups["scheduledPod"]
	slices.SortFunc(podSlice, apputil.SortPodInfoByCreationTimestamp)
	for _, pod := range podSlice {
		deployCount++
		slog.Info("deploying scheduled Pod", "pod", pod.Name, "pod.Spec.nodeName", pod.Spec.NodeName)
		err = doDeployPod(ctx, clientSet, replayCount, deployCount, getCorePodFromPodInfo(pod))
		if err != nil {
			return err
		}
	}
	podSlice = podGroups["previouslyAssignedPod"]
	slices.SortFunc(podSlice, apputil.SortPodInfoByCreationTimestamp)
	for _, pod := range podSlice {
		deployCount++
		slog.Info("deploying previouslyAssignedPod", "pod", pod.Name)
		err = doDeployPod(ctx, clientSet, replayCount, deployCount, getCorePodFromPodInfo(pod))
		if err != nil {
			return err
		}
	}
	podSlice = podGroups["unscheduledPod"]
	slices.SortFunc(podSlice, apputil.SortPodInfoByCreationTimestamp)
	for _, pod := range podSlice {
		deployCount++
		slog.Info("deploying unscheduledPod", "pod", pod.Name)
		err = doDeployPod(ctx, clientSet, replayCount, deployCount, getCorePodFromPodInfo(pod))
		if err != nil {
			return err
		}
	}
	slog.Info("applyDeltaWork finished deploying pods", "deployCount", deployCount)
	//for _, partition := range partitionedPods {
	//	chunks := lo.Chunk(partition, deltaWork.DeployParallel)
	//	for j, chunk := range chunks {
	//		var g errgroup.Group
	//		for _, podInfo := range chunk {
	//			pod := getCorePodFromPodInfo(podInfo)
	//			deployCount++
	//			g.Go(func() error { //Note: for loop lambdas were fixed in Go 1.22 https://tip.golang.org/doc/go1.22#language
	//				err = doDeployPod(ctx, clientSet, replayCount, deployCount, pod)
	//				if err != nil {
	//					return err
	//				}
	//				return nil
	//			})
	//		}
	//		if err := g.Wait(); err != nil {
	//			return err
	//		}
	//		slog.Info("applyDeltaWork finished deploying pod chunk", "chunkIndex", j, "deployCount", deployCount)
	//	}
	//}

	err = deleteNamespaces(ctx, clientSet, deltaWork.NamespaceWork.ToDelete.UnsortedList()...)
	if err != nil {
		return fmt.Errorf("applyDeltaWork cannot delete un-used namespaces: %w", err)
	}
	slog.Info("applyDeltaWork was successful", "replayCount", replayCount)
	return nil
}

func doDeployPod(ctx context.Context, clientSet *kubernetes.Clientset, replayCount int, deployCount int, pod corev1.Pod) error {
	podNew, err := clientSet.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("doDeployPod cannot create the pod  %s: %w", pod.Name, err)
	}
	if podNew.Spec.NodeName != "" {
		podNew.Status.Phase = corev1.PodRunning
		_, err = clientSet.CoreV1().Pods(pod.Namespace).UpdateStatus(ctx, podNew, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("doDeployPod cannot change the pod Phase to Running for %s: %w", pod.Name, err)
		}
	}
	slog.Info("doDeployPod finished.", "replayCount", replayCount, "deployCount", deployCount, "pod.Name", pod.Name, "pod.Namespace", pod.Namespace, "pod.NodeName", pod.Spec.NodeName)
	return nil
}

// applyDeltaWorkOld is dead code and can be removed later
func (r *defaultReplayer) applyDeltaWorkOld(ctx context.Context, clusterSnapshot gsc.ClusterSnapshot) (workDone bool, err error) {

	virtualPCs, err := r.clientSet.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the priority classes in virtual cluster: %w", err)
		return
	}
	virtualPCsMap := lo.KeyBy(virtualPCs.Items, func(item schedulingv1.PriorityClass) string {
		return item.Name
	})
	for _, pcInfo := range clusterSnapshot.PriorityClasses {
		_, ok := virtualPCsMap[pcInfo.Name]
		if ok {
			continue
		}
		_, err = r.clientSet.SchedulingV1().PriorityClasses().Create(ctx, &pcInfo.PriorityClass, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("cannot create the priority class %s: %w", pcInfo.Name, err)
		}
		slog.Info("successfully create the priority class", "pc.Name", pcInfo.Name)
		workDone = true
	}

	podNamespaces := clusterSnapshot.GetPodNamspaces()
	virtualNamespaces, err := getNamespaces(ctx, r.clientSet)
	if err != nil {
		return
	}

	//nsToDelete := virtualNamespaces.Difference(podNamespaces)
	nsToDeploy := podNamespaces.Difference(virtualNamespaces)

	err = createNamespaces(ctx, r.clientSet, nsToDeploy.UnsortedList()...)
	if err != nil {
		return
	}

	virtualPodsList, err := r.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	virtualPodsByName := lo.Associate(virtualPodsList.Items, func(item corev1.Pod) (string, gsc.PodInfo) {
		return item.Name, recorder.PodInfoFromPod(&item)
	})
	clusterSnapshotPodsByName := lo.KeyBy(clusterSnapshot.Pods, func(item gsc.PodInfo) string {
		return item.Name
	})
	for _, podInfo := range virtualPodsByName {
		snapshotPodInfo, ok := clusterSnapshotPodsByName[podInfo.Name]
		if ok && (snapshotPodInfo.NominatedNodeName == podInfo.NominatedNodeName || snapshotPodInfo.NodeName == podInfo.NodeName) {
			continue
		}
		err = r.clientSet.CoreV1().Pods(podInfo.Namespace).Delete(ctx, podInfo.Name, metav1.DeleteOptions{
			GracePeriodSeconds: pointer.Int64(0),
		})
		if err != nil {
			err = fmt.Errorf("cannot delete the pod %q: %w", podInfo.Name, err)
			return
		}
		delete(virtualPodsByName, podInfo.UID)
		workDone = true
	}
	deployCount := 0
	for _, podInfo := range clusterSnapshot.Pods {
		_, ok := virtualPodsByName[podInfo.Name]
		if ok {
			continue
		}
		pod := getCorePodFromPodInfo(podInfo)
		deployCount++
		slog.Info("deploying pod", "deployCount", deployCount, "pod.Name", pod.Name, "pod.Namespace", pod.Namespace, "pod.NodeName", pod.Spec.NodeName)
		_, err = r.clientSet.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
		if podInfo.NodeName != "" {

		}
		if err != nil {
			err = fmt.Errorf("cannot create the pod  %s: %w", pod.Name, err)
			return
		}
		workDone = true
	}
	return
}

func eventTimeDiffGreaterThan(a, b gsc.EventInfo, span time.Duration) bool {
	return isDifferenceGreaterThan(a.EventTime, b.EventTime, span)
}

func isDifferenceGreaterThan(t1, t2 time.Time, d time.Duration) bool {
	// Calculate the absolute difference between the two times
	difference := t1.Sub(t2)
	if difference < 0 {
		difference = -difference
	}
	// Check if the absolute difference is lesss than the given duration
	return difference > d
}

func GetNextReplayEvent(events []gsc.EventInfo, currEventIndex int) (nextEventIndex int, nextEvent gsc.EventInfo) {
	numEvents := len(events)
	if currEventIndex == 0 && numEvents == 1 {
		slog.Info("GetNextReplayEvent returning single scale-up event")
		return 1, events[0]
	}
	if currEventIndex >= numEvents-1 {
		slog.Info("GetNextReplayEvent could find no more scale-up events")
		nextEventIndex = -1
		return
	}
	span := 12 * time.Second
	currEvent := events[currEventIndex]

	for i := currEventIndex + 1; i < numEvents; i++ {
		nextEvent = events[i]
		//if nextEvent.Message == currEvent.Message {
		//	continue
		//}
		if eventTimeDiffGreaterThan(nextEvent, currEvent, span) {
			nextEventIndex = i
			nextEvent = events[nextEventIndex]
			return
		}
	}
	nextEventIndex = numEvents - 1 // last event if there is a contiguous sequence of events within span till the end.
	nextEvent = events[nextEventIndex]
	return
}

func (r *defaultReplayer) getNextReplayEvent() (nextEvent gsc.EventInfo) {
	r.currentEventIndex, nextEvent = GetNextReplayEvent(r.scaleUpEvents, r.currentEventIndex)
	return
}

func (r *defaultReplayer) computeAndApplyDeltaWork(ctx context.Context, clusterSnapshot gsc.ClusterSnapshot, pendingPods []corev1.Pod) (workDone bool, err error) {
	deltaWork, err := computeDeltaWork(ctx, r.clientSet, clusterSnapshot, pendingPods)
	if err != nil {
		return
	}
	if deltaWork.IsEmpty() {
		slog.Info("deltaWork is empty. Skipping applying work")
		return
	}
	r.replayCount++
	deltaWork.Number = r.replayCount
	err = applyDeltaWork(ctx, r.clientSet, deltaWork)
	if err != nil {
		return
	}
	workDone = true
	return
}

func computeDeltaWork(ctx context.Context, clientSet *kubernetes.Clientset, clusterSnapshot gsc.ClusterSnapshot, pendingPods []corev1.Pod) (deltaWork DeltaWork, err error) {
	pcWork, err := computePriorityClassWork(ctx, clientSet, clusterSnapshot.PriorityClasses)
	if err != nil {
		return
	}

	nsWork, err := computeNamespaceWork(ctx, clientSet, clusterSnapshot.GetPodNamspaces())
	if err != nil {
		return
	}

	podWork, err := computePodWork(ctx, clientSet, clusterSnapshot.Pods, pendingPods)
	if err != nil {
		return
	}
	deltaWork.Number = clusterSnapshot.Number
	deltaWork.PodWork = podWork
	deltaWork.PriorityClassWork = pcWork
	deltaWork.NamespaceWork = nsWork
	slog.Info("computeDeltaWork for clusterSnapshot.", "clusterSnapshot.Number", clusterSnapshot.Number, "clusterSnapshot.SnapshotTime.UnixMicro", clusterSnapshot.SnapshotTime.UnixMicro(), "deltaWork", deltaWork)

	return
}

func computePodWork(ctx context.Context, clientSet *kubernetes.Clientset, snapshotPods []gsc.PodInfo, pendingPods []corev1.Pod) (podWork PodWork, err error) {
	//virtualNodes, err := clientutil.ListAllNodes(ctx, clientSet)
	//if err != nil {
	//	err = fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
	//	return
	//}
	//virtualNodeNames := lo.Map(virtualNodes, func(item corev1.Node, index int) string {
	//	return item.Name
	//})
	//podWork.ToDelete, err = getPodNamesNotAssignedToNodes(ctx, clientSet, virtualNodeNames)
	virtualPods, err := clientutil.ListAllPods(ctx, clientSet)
	//virtualPods, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	podWork.ToDelete = sets.New(lo.Map(virtualPods, func(p corev1.Pod, _ int) types.NamespacedName {
		return types.NamespacedName{
			Namespace: p.Namespace,
			Name:      p.Name,
		}
	})...)
	//virtualPods = append(virtualPods, pendingPods...)
	//virtualPodsByName := lo.Associate(virtualPods, func(item corev1.Pod) (string, gsc.PodInfo) {
	//	return item.Name, recorder.PodInfoFromPod(&item)
	//})
	//clusterSnapshotPodsByName := lo.KeyBy(snapshotPods, func(item gsc.PodInfo) string {
	//	return item.Name
	//})

	//for _, podInfo := range virtualPodsByName {
	//	snapshotPodInfo, ok := clusterSnapshotPodsByName[podInfo.Name]
	//	if ok && (snapshotPodInfo.NominatedNodeName == podInfo.NominatedNodeName || snapshotPodInfo.NodeName == podInfo.NodeName) {
	//		continue
	//	}
	//	podWork.ToDelete.Insert(types.NamespacedName{Namespace: podInfo.Namespace, Name: podInfo.Name})
	//	delete(virtualPodsByName, podInfo.Name)
	//}

	for _, podInfo := range snapshotPods {
		//_, ok := virtualPodsByName[podInfo.Name]
		//if ok {
		//	continue
		//}
		podInfo = adjustPodInfo(podInfo)
		podWork.ToDeploy = append(podWork.ToDeploy, podInfo)
	}
	slices.SortFunc(podWork.ToDeploy, func(a, b gsc.PodInfo) int {
		return a.CreationTimestamp.Compare(b.CreationTimestamp)
	})
	return
}

// adjustPodInfo adjusts the old PodInfo to make it more suitable for deployment onto the virtual cluster.
// This includes removing persistentVolumeClaims, etc.
func adjustPodInfo(old gsc.PodInfo) (new gsc.PodInfo) {
	new = old
	if new.Spec.Volumes != nil {
		for i := range new.Spec.Volumes {
			if new.Spec.Volumes[i].PersistentVolumeClaim != nil {
				new.Spec.Volumes[i].PersistentVolumeClaim = nil
			}
		}
	}
	for i := 0; i < len(new.Spec.Containers); i++ {
		if new.Spec.Containers[i].Resources.Requests != nil {
			delete(new.Spec.Containers[i].Resources.Requests, corev1.ResourceEphemeralStorage)
		}
		if new.Spec.Containers[i].Resources.Limits != nil {
			delete(new.Spec.Containers[i].Resources.Limits, corev1.ResourceEphemeralStorage)
		}
	}
	//spec:
	//affinity:
	//nodeAffinity:
	//requiredDuringSchedulingIgnoredDuringExecution:
	//nodeSelectorTerms:
	//	- matchFields:
	//	- key: metadata.name
	//operator: In
	//values:
	//	- shoot--hc-eu30--prod-gc-orc-default-z1-5b99b-4rttc
	if new.Spec.Affinity != nil && new.Spec.Affinity.NodeAffinity != nil && new.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		ns := new.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		for _, nsTerm := range ns.NodeSelectorTerms {
			for _, matchf := range nsTerm.MatchFields {
				if matchf.Key == "metadata.name" {
					new.Spec.Affinity.NodeAffinity = nil
					slog.Info("Clearing NodeAffinity from pod", "podName", new.Name, "nodeAffinity", matchf.Values)
					break
				}
			}
		}
	}
	new.Spec.PriorityClassName = ""
	new.Spec.PreemptionPolicy = nil
	new.Spec.Priority = nil

	if new.Spec.RuntimeClassName != nil {
		new.Spec.RuntimeClassName = nil
	}
	//new.NodeName = ""
	//new.Spec.NodeName = ""
	return
}

func computePriorityClassWork(ctx context.Context, clientSet *kubernetes.Clientset, snapshotPCs []gsc.PriorityClassInfo) (pcWork PriorityClassWork, err error) {
	virtualPCs, err := clientSet.SchedulingV1().PriorityClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the priority classes in virtual cluster: %w", err)
		return
	}
	virtualPCsMap := lo.KeyBy(virtualPCs.Items, func(item schedulingv1.PriorityClass) string {
		return item.Name
	})
	for _, pcInfo := range snapshotPCs {
		_, ok := virtualPCsMap[pcInfo.Name]
		if ok {
			continue
		}
		pcWork.ToDeploy = append(pcWork.ToDeploy, pcInfo)
	}
	return
}

func computeNamespaceWork(ctx context.Context, clientSet *kubernetes.Clientset, snapshotPodNamespaces sets.Set[string]) (nsWork NamespaceWork, err error) {
	virtualNamespaces, err := getNamespaces(ctx, clientSet)
	if err != nil {
		return
	}
	nsWork.ToCreate = nsWork.ToCreate.Union(snapshotPodNamespaces.Difference(virtualNamespaces))
	// FIXME: uncomment me after recording all namespaces
	//nsWork.ToDelete.Union(virtualNamespaces.Difference(snapshotPodNamespaces))
	return
}

func getPodNamesNotAssignedToNodes(ctx context.Context, clientSet *kubernetes.Clientset, nodeNames []string) (podNames sets.Set[types.NamespacedName], err error) {
	nodeNameSet := sets.New[string](nodeNames...)
	podsList, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	podNames = make(sets.Set[types.NamespacedName])
	for _, pod := range podsList.Items {
		if nodeNameSet.Has(pod.Spec.NodeName) {
			continue
		}
		podNames.Insert(types.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		})
	}
	return
}

func (r *defaultReplayer) Close() error {
	//// TODO: clean up cluster can be done here.
	//if r.kvclCancelFn != nil {
	//	slog.Info("Cancelling kvcl...")
	//	r.kvclCancelFn()
	//}
	//if r.scalerCancelFn != nil {
	//	slog.Info("Cancelling scaler proces...")
	//	r.scalerCancelFn()
	//}
	//return r.dataAccess.Close()

	return r.doClose()
}

func (r *defaultReplayer) GetRecordedClusterSnapshot(runMarkTime time.Time) (cs gsc.ClusterSnapshot, err error) {
	snapshotID := fmt.Sprintf("%s-%d", apputil.FilenameWithoutExtension(r.params.InputDataPath), cs.Number)
	cs, err = GetRecordedClusterSnapshot(r.dataAccess, r.replayCount, snapshotID, runMarkTime)
	return
}

func modifyNodeAllocatable(nodeTemplates map[string]gsc.NodeTemplate, nodes []gsc.NodeInfo) ([]gsc.NodeInfo, error) {
	updatedNodes := make([]gsc.NodeInfo, 0, len(nodes))
	for _, no := range nodes {
		zone, ok := gsc.GetZone(no.Labels) //getZonefromNodeLabels(nodeInfo.Labels)
		if !ok {
			return nil, fmt.Errorf("cannot find zone for node %q", no.Labels)
		}
		nodeTemplate := findNodeTemplate(nodeTemplates, no.Labels["worker.gardener.cloud/pool"], zone)
		if nodeTemplate == nil {
			return nil, fmt.Errorf("cannot find the node template for node %q", no.Name)
		}
		no.Allocatable = nodeTemplate.Allocatable
		updatedNodes = append(updatedNodes, no)
	}
	return updatedNodes, nil
}

func findNodeTemplate(nodeTemplates map[string]gsc.NodeTemplate, poolName, zone string) *gsc.NodeTemplate {
	for _, nt := range nodeTemplates {
		slog.Debug("Node capacity", "name", nt.Name, "zone", nt.Zone, "allocatable", nt.Allocatable)
		if nt.Zone == zone && nt.Labels["worker.gardener.cloud/pool"] == poolName {
			return &nt
		}
	}
	return nil
}

func clearNodeNamesNotIn(pods []gsc.PodInfo, nodeNames []string) {
	nameSet := sets.New(nodeNames...)
	for i := range pods {
		if !nameSet.Has(pods[i].Spec.NodeName) {
			pods[i].NodeName = ""
			pods[i].Spec.NodeName = ""
			if pods[i].Labels == nil {
				pods[i].Labels = make(map[string]string)
			}
			pods[i].Labels["previouslyAssignedPod"] = "1"
		}
	}
}

// filterNodeInfos filters the nodes given nodes removing nodes that have the CA NoSchedule taints like`ToBeDeletedByClusterAutoscaler`
// We do not filter CA `DeletionCandidateOfClusterAutoscaler` since the latter has PreferNoSchedule
func filterNodeInfos(nodes []gsc.NodeInfo) []gsc.NodeInfo {
	return lo.Filter(nodes, func(n gsc.NodeInfo, _ int) bool {
		if len(n.Taints) == 0 {
			return true
		}
		for _, t := range n.Taints {
			if t.Key == "ToBeDeletedByClusterAutoscaler" {
				return false
			}
		}
		return true
	})
}

func adjustNodeTemplates(nodeTemplates map[string]gsc.NodeTemplate, kubeSystemResources corev1.ResourceList) {
	for ngName, nodeTemplate := range nodeTemplates {
		nodeTemplate.Allocatable = resutil.ComputeRevisedResources(nodeTemplate.Capacity, kubeSystemResources)
		nodeTemplate.Allocatable[corev1.ResourcePods] = resource.MustParse("110")
		nodeTemplates[ngName] = nodeTemplate
	}
}

func (r *defaultReplayer) GetParams() gsh.ReplayerParams {
	//TODO implement me
	panic("implement me")
}

func getNodeGroupsByPoolZone(nodeGroupsByName map[string]gsc.NodeGroupInfo) map[gsh.PoolZone]gsc.NodeGroupInfo {
	poolZoneMap := make(map[gsh.PoolZone]gsc.NodeGroupInfo)
	for _, ng := range nodeGroupsByName {
		poolKey := gsh.PoolZone{
			PoolName: ng.PoolName,
			Zone:     ng.Zone,
		}
		poolZoneMap[poolKey] = ng
	}
	return poolZoneMap
}

func (r *defaultReplayer) writeScenario(scenario gsh.Scenario, clusterSnapshot gsc.ClusterSnapshot) error {
	reportPath := fmt.Sprintf(r.reportPathFormat, clusterSnapshot.Number)
	//slog.Info("appended scenario for", "snapshotTime", clusterSnapshot.SnapshotTime, "scaledUpNodeGroups", scenario.ScalingResult.ScaledUpNodeGroups)
	rBytes, err := json.Marshal(scenario)
	if err != nil {
		return fmt.Errorf("cannot marshal the scenario report for snapshot %d: %w", clusterSnapshot.Number, err)
	}
	err = os.WriteFile(reportPath, rBytes, 0666)
	if err != nil {
		return fmt.Errorf("cannot write to report to file %q: %w", r.reportPathFormat, err)
	}
	slog.Info("Wrote scenario report.", "reportPath", reportPath, "snapshotTime", clusterSnapshot.SnapshotTime,
		"scaledUpNodeGroups", scenario.ScalingResult.ScaledUpNodeGroups)
	if r.params.Mode != gsh.LocalMode {
		err = apputil.UploadReport(r.ctx, reportPath)
		if err != nil {
			slog.Error("error uploading report", "reportPath", reportPath, "error", err)
			return err
		}
	}
	return nil

}

func (r *defaultReplayer) createScenario(ctx context.Context, clusterSnapshot gsc.ClusterSnapshot) (scenario gsh.Scenario, err error) {
	postVirtualNodesList, err := r.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
		return
	}

	var scaledUpNodes []gsc.NodeInfo
	postVirtualNodes := postVirtualNodesList.Items
	preVirtualNodesMap := lo.KeyBy(clusterSnapshot.Nodes, func(item gsc.NodeInfo) string {
		return item.Name
	})
	var scaledUpNodeNames = make(set.Set[string])
	var allNodeNames = make(set.Set[string])
	for _, node := range postVirtualNodes {
		allNodeNames.Insert(node.Name)
		_, ok := preVirtualNodesMap[node.Name]
		if ok {
			continue
		}
		//TODO how to replay csi nodes ?
		scaledUpNodes = append(scaledUpNodes, gsh.NodeInfoFromNode(&node, 0))
		scaledUpNodeNames.Insert(node.Name)
	}
	if len(scaledUpNodes) == 0 {
		slog.Warn("NO SCALE-UP in this replay interval, so skipping appending this scenario to report", "snapshotTime", clusterSnapshot.SnapshotTime)
		err = ErrNoScenario
		return
	}
	scenario.BeginTime = clusterSnapshot.SnapshotTime.UTC()
	scenario.ClusterSnapshot = clusterSnapshot
	scenario.ScalingResult.ScaledUpNodes = scaledUpNodes
	scenario.ScalingResult.ScaledUpNodeGroups = make(map[string]int)
	poolZoneMap := getNodeGroupsByPoolZone(clusterSnapshot.AutoscalerConfig.NodeGroups)
	for _, node := range scaledUpNodes {
		poolZone := gsh.PoolZone{
			PoolName: node.Labels["worker.gardener.cloud/pool"],
			Zone:     node.Labels["topology.kubernetes.io/zone"],
		}
		ng, ok := poolZoneMap[poolZone]
		if !ok {
			err = fmt.Errorf("cannot find associated PoolZone %q for node %q", poolZone, node.Name)
			return
		}
		scenario.ScalingResult.ScaledUpNodeGroups[ng.Name]++
	}
	pods, err := clientutil.ListAllPods(ctx, r.clientSet)
	//pods, err := r.clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	nodeUtilizationMap := make(map[string]corev1.ResourceList)
	emptyNodeNames := allNodeNames.Clone()
	//scenario.ClusterSnapshot.Pods = []gsc.PodInfo{}
	for _, pod := range pods {
		podInfo := recorder.PodInfoFromPod(&pod)
		//podInfoCopy := podInfo
		//podInfoCopy.Spec.NodeName = ""
		//podInfoCopy.NodeName = ""
		//scenario.ClusterSnapshot.Pods = append(scenario.ClusterSnapshot.Pods, podInfoCopy)
		// FIXME: BUGGY
		//if podInfo.PodScheduleStatus == gsc.PodUnscheduled || podInfo.PodScheduleStatus == gsc.PodSchedulePending || pod.Spec.NodeName == "" {
		if pod.Spec.NodeName == "" {
			scenario.ScalingResult.PendingUnscheduledPods = append(scenario.ScalingResult.PendingUnscheduledPods, podInfo)
		} else {
			scenario.ScalingResult.ScheduledPods = append(scenario.ScalingResult.ScheduledPods, podInfo)
			emptyNodeNames.Delete(pod.Spec.NodeName)
		}
		//if !scaledUpNodeNames.Has(pod.Spec.NodeName) {
		//	continue
		//}
		if pod.Spec.NodeName == "" {
			continue
		}
		util, ok := nodeUtilizationMap[pod.Spec.NodeName]
		if !ok {
			util = make(corev1.ResourceList)
		}
		sumPodRequests := gsc.CumulatePodRequests(&pod)
		util = gsc.SumResources([]corev1.ResourceList{sumPodRequests, util})
		nodeUtilizationMap[pod.Spec.NodeName] = util
	}
	scenario.ScalingResult.NodesUtilization = nodeUtilizationMap
	scenario.ScalingResult.EmptyNodeNames = emptyNodeNames.SortedList()

	return
}

func populateAllocatableVolumes(dataAccess *db.DataAccess, nodes []gsc.NodeInfo, beginTime time.Time) error {
	allCSINodeRows, err := dataAccess.LoadCSINodeRowsBefore(beginTime)
	if err != nil {
		return err
	}
	csiNodeRowsByName := lo.GroupBy(allCSINodeRows, func(cn db.CSINodeRow) string {
		return cn.Name
	})
	for i, n := range nodes {
		if n.AllocatableVolumes > 0 {
			continue
		}
		csiNodeRows, ok := csiNodeRowsByName[n.Name]
		if !ok || len(csiNodeRows) == 0 {
			slog.Warn("populateAllocatableVolumes could not find csiNodeRows for node.", "nodeName", n.Name)
			continue
		}
		csiNodeRow := csiNodeRows[0]
		n.AllocatableVolumes = csiNodeRow.AllocatableVolumes
		slog.Debug("populateAllocatableVolumes found AllocatableVolumes from csiNodeRow", "nodeName", n.Name, "allocatableVolumes", csiNodeRow.AllocatableVolumes)
		nodes[i] = n
	}
	return nil
	//BRB 1m
}

func GetNodeGroupNameFromMCCName(namespace, mccName string) string {
	idx := strings.LastIndex(mccName, "-")
	// mcc name - shoot--i585976--suyash-local-worker-1-z1-0af3f , we omit the hash from the mcc name to match it with the nodegroup name
	trimmedName := mccName[0:idx]
	return fmt.Sprintf("%s.%s", namespace, trimmedName)
}

func constructNodeTemplateFromMCC(mcc gsh.MachineClassInfo) gsc.NodeTemplate {
	mccLabels := maps.Clone(mcc.Labels)
	mccLabels["node.kubernetes.io/instance-type"] = mcc.InstanceType
	for k := range mccLabels {
		if strings.Count(k, "/") > 1 {
			delete(mccLabels, k)
		}
	}
	return gsc.NodeTemplate{
		Name:         GetNodeGroupNameFromMCCName(mcc.Namespace, mcc.Name),
		Capacity:     mcc.Capacity,
		InstanceType: mcc.InstanceType,
		Region:       mcc.Region,
		Zone:         mcc.Zone,
		Labels:       mccLabels,
		Taints:       nil,
	}
}

func GetNodeTemplates(mccs []gsh.MachineClassInfo, mcds []gsc.MachineDeploymentInfo) (nodeTemplates map[string]gsc.NodeTemplate, err error) {
	nodeTemplates = make(map[string]gsc.NodeTemplate)
	for _, mcc := range mccs {
		nodeTemplate := constructNodeTemplateFromMCC(mcc)
		nodeTemplates[nodeTemplate.Name] = nodeTemplate
	}
	for _, mcd := range mcds {
		ngName := fmt.Sprintf("%s.%s", mcd.Namespace, mcd.Name)
		nodeTemplate, ok := nodeTemplates[ngName]
		if !ok {
			err = fmt.Errorf("cannot find the node template for nodegroup: %s", ngName)
			return
		}
		nodeTemplate.Taints = mcd.Taints
		maps.Copy(nodeTemplate.Labels, mcd.Labels)
		nodeTemplate.Hash = nodeTemplate.GetHash()
		nodeTemplates[ngName] = nodeTemplate
	}
	return
}

func deriveNodeGroups(mcds []gsc.MachineDeploymentInfo, ngMinMaxMap map[string]gsc.MinMax) (nodeGroups map[string]gsc.NodeGroupInfo, err error) {
	nodeGroups = make(map[string]gsc.NodeGroupInfo)
	for _, mcd := range mcds {
		ngName := fmt.Sprintf("%s.%s", mcd.Namespace, mcd.Name)
		minMax, ok := ngMinMaxMap[ngName]
		if !ok {
			slog.Warn("cannot find NodeGroup with name", "nodeGroupName", ngName)
			//err = fmt.Errorf("cannot find NodeGroup with name %q in ngMinMaxMap map %q", ngName, ngMinMaxMap)
		}
		nodeGroup := gsc.NodeGroupInfo{
			Name:       ngName,
			PoolName:   mcd.PoolName,
			Zone:       mcd.Zone,
			TargetSize: mcd.Replicas,
			MinSize:    minMax.Min,
			MaxSize:    minMax.Max,
		}
		nodeGroup.Hash = nodeGroup.GetHash()
		nodeGroups[nodeGroup.Name] = nodeGroup
	}
	return
}

func deletePendingUnscheduledPods(ctx context.Context, clientSet *kubernetes.Clientset) (deletedPods []corev1.Pod, err error) {
	pods, err := clientutil.ListAllPods(ctx, clientSet)
	if err != nil {
		return
	}
	for _, p := range pods {
		ss := recorder.ComputePodScheduleStatus(&p)
		if ss == gsc.PodUnscheduled {
			err = clientSet.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{
				GracePeriodSeconds: pointer.Int64(0),
			})
			if err != nil && !apierrors.IsNotFound(err) {
				return
			}
			deletedPods = append(deletedPods, p)
			slog.Info("Deleted pending unscheduled pod before resetting autoscaler config", "pod.Name", p.Name)
		}
	}
	return
}

func waitAndCheckVirtualScaling(ctx context.Context, clientSet *kubernetes.Clientset, replayCount int) (scalingOccurred bool, err error) {
	var nodes []corev1.Node
	var pods []corev1.Pod
	var waitInterval = 1 * time.Minute

	err = waitForAllPodsToHaveSomeCondition(ctx, clientSet, replayCount, waitInterval)
	if err != nil {
		return
	}

	waitNum := 0
	signalFilePath := "/tmp/ca-scale-up-done.txt"
	_ = os.Remove(signalFilePath)
	doneCount := 0
	doneLimit := 1
	for {
		nodes, err = clientutil.ListAllNodes(ctx, clientSet)
		virtualScaledNodeNames := lo.FilterMap(nodes, func(n corev1.Node, _ int) (nodeName string, ok bool) {
			_, ok = n.Labels[gsc.LabelVirtualScaled]
			if !ok {
				return
			}
			nodeName = n.Name
			return
		})
		if len(virtualScaledNodeNames) > 0 {
			scalingOccurred = true
		}
		pods, err = clientutil.ListAllPods(ctx, clientSet)
		if err != nil {
			return
		}
		unscheduledPods := lo.Filter(pods, func(pod corev1.Pod, index int) bool {
			return pod.Spec.NodeName == ""
			//_, scheduledCondition := apputil.GetPodCondition(&pod.Status, corev1.PodScheduled)
			//if scheduledCondition == nil {
			//	return false
			//}
			//if scheduledCondition.Status != corev1.ConditionFalse || scheduledCondition.Reason != "Unschedulable" {
			//	return false
			//}
			//return true
		})
		if len(unscheduledPods) == 0 {
			if scalingOccurred {
				slog.Info("waitAndCheckVirtualScaling found zero unscheduledPods; continue scenario creation", "waitNum", waitNum, "numNodes", len(nodes), "numPods", len(pods), "numVirtualScaledNodes", len(virtualScaledNodeNames), "replayCount", replayCount)
			} else {
				slog.Info("waitAndCheckVirtualScaling found zero unscheduledPods AND zero virtualScaledNodeNames; skip scenario creation", "waitNum", waitNum, "numNodes", len(nodes), "numPods", len(pods), "replayCount", replayCount)
			}
			return
		}
		slog.Info("waitAndCheckVirtualScaling waiting for unscheduledPods to be scheduled.", "replayCount", replayCount, "waitNum", waitNum, "numUnscheduledPods", len(unscheduledPods), "numVirtualScaledNodes", len(virtualScaledNodeNames), "waitInterval", waitInterval, "replayCount", replayCount, "doneCount", doneCount, "doneLimit", doneLimit)
		select {
		case <-ctx.Done():
			slog.Warn("waitAndCheckVirtualScaling received context cancelled or timed out:", "waitNum", waitNum, "error", ctx.Err(), "replayCount", replayCount)
			err = ctx.Err()
		case <-time.After(waitInterval):
			slog.Debug("waitAndCheckVirtualScaling finished wait.", "replayCount", replayCount, "waitNum", waitNum, "waitInterval", waitInterval, "numVirtualScaledNodeNames", len(virtualScaledNodeNames), "replayCount", replayCount)
			var data []byte
			data, err = os.ReadFile(signalFilePath)
			if data != nil {
				message := string(data)
				slog.Info("waitAndCheckVirtualScaling obtained done signal.", "waitNum", waitNum, "message", message, "signalFilePath", signalFilePath, "doneCount", doneCount)
				if doneCount >= doneLimit {
					slog.Warn("waitAndCheckVirtualScaling obtained doneCount >= doneLimit done signal, wait finished.", "waitNum", waitNum, "message", message, "signalFilePath", signalFilePath, "doneCount", doneCount, "doneLimit", doneLimit)
					return
				} else {
					doneCount++
					_ = os.Remove(signalFilePath)
				}
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("waitAndCheckVirtualScaling encountered error reading signal", "waitNum", waitNum, "error", err, "signalFilePath", signalFilePath)
			}
			waitNum++
		}
	}
}

func waitForAllPodsToHaveSomeCondition(ctx context.Context, clientSet *kubernetes.Clientset, replayCount int, waitInterval time.Duration) (err error) {
	// first wait till until all deployed Pods have a Pod condition.
	conditionWaitTimedoutCh := time.After(10 * time.Minute)
	var allPods []corev1.Pod
	waitNum := 0
outer:
	for {
		select {
		case <-ctx.Done():
			slog.Warn("waitForAllPodsToHaveSomeCondition received context cancelled or timed out:", "waitNum", waitNum, "error", ctx.Err(), "replayCount", replayCount)
			err = ctx.Err()
			return
		case <-conditionWaitTimedoutCh:
			slog.Error("waitForAllPodsToHaveSomeCondition timedout waiting for all deployed Pods to have a Pod Condition.", "replayCount", replayCount, "waitNum", waitNum, "waitInterval", waitInterval)
			err = fmt.Errorf("timed out waiting for all deployed Pdos to have a Pod condition for replay %d", replayCount)
			return
		case <-time.After(waitInterval):
			allPods, err = clientutil.ListAllPods(ctx, clientSet)
			if err != nil {
				return
			}
			numPods := len(allPods)
			numPodsWithoutConditions := 0
			for _, p := range allPods {
				if len(p.Status.Conditions) == 0 && p.Spec.NodeName == "" {
					numPodsWithoutConditions++
				}
			}
			if numPodsWithoutConditions > 0 {
				slog.Info("waitForAllPodsToHaveSomeCondition saw that some pods don't have conditions, waiting....", "numPodsWithoutConditions", numPodsWithoutConditions, "numPods", numPods)
				<-time.After(waitInterval)
				continue
			} else {
				slog.Info("waitForAllPodsToHaveSomeCondition saw that all pods have conditions.", "numPods", numPods)
				break outer
			}
		}
	}
	return
}

func filterAppPods(pods []gsc.PodInfo) (filteredPods []gsc.PodInfo) {
	// First filter out "kube-system" pods
	pods = lo.Filter(pods, func(p gsc.PodInfo, _ int) bool {
		return p.Namespace != "kube-system"
	})
	// group PodInfos by UID
	podInfosByUID := lo.GroupBy(pods, apputil.PodUID)
	// Now go through each name, []gscPodInfo pair and check if there is a Pod in PodSucceeded or PodFailedPhase.
	// If so, remove the entry as this indicates that the Pod stopped actively running sometime before runMarkTime.
	// Deleted Pods are already handled by the sql query.
	// Ideally one should do the below in SQL using CTE expressions but logic in GO is clearer.
OUTER:
	for _, infos := range podInfosByUID {
		slices.SortFunc(infos, apputil.ComparePodInfoByRowID)
		for _, pi := range infos {
			if pi.PodPhase == corev1.PodSucceeded || pi.PodPhase == corev1.PodFailed {
				continue OUTER
			}
		}
		filteredPods = append(filteredPods, infos[0])
	}
	return
}

func GetNextScalingRun(scaleUpEvents []gsc.EventInfo, lastRun ScalingRun, runInterval time.Duration) (run ScalingRun) {
	numScaleUpEvents := len(scaleUpEvents)
	if numScaleUpEvents == 1 {
		run.BeginIndex = 0
		run.EndIndex = 0
		run.BeginTime = scaleUpEvents[0].EventTime.UTC()
		run.EndTime = run.BeginTime.Add(runInterval).UTC()
		return
	}

	if lastRun.EndIndex >= numScaleUpEvents {
		slog.Info("GetNextScalingRun found no more scaling runs.", "lastRun.EndIndex", lastRun.EndIndex, "numScaleUpEvents", numScaleUpEvents)
		return
	}
	run.BeginIndex = lastRun.EndIndex
	run.BeginTime = scaleUpEvents[run.BeginIndex].EventTime.UTC()
	for i := lastRun.EndIndex + 1; i < len(scaleUpEvents); i++ { //e1;lastrun.Endindex=0, e2;run.BeginIdex=i=1 , e3.. beginIndex=1
		currEvent := scaleUpEvents[i]
		prevEvent := scaleUpEvents[i-1]
		limitTime := prevEvent.EventTime.UTC().Add(runInterval)
		if currEvent.EventTime.UTC().After(limitTime) {
			run.EndIndex = i - 1
			run.EndTime = limitTime
			slog.Info("GetNextScalingRun obtained scaling run.",
				"run.BeginIndex", run.BeginIndex,
				"run.BeginTime", run.BeginTime,
				"run.EndIndex", run.EndIndex,
				"run.EndTime", run.EndTime,
				"run.BeginEvent", scaleUpEvents[run.BeginIndex],
				"run.EndEvent", scaleUpEvents[run.EndIndex])
			return
		}
	}
	run.EndIndex = len(scaleUpEvents) - 1
	run.EndTime = scaleUpEvents[run.EndIndex].EventTime.UTC().Add(runInterval).UTC()
	slog.Info("GetNextScalingRun obtained contiguous run of scaling events found from BeginIndex to EndIndex.",
		"run.BeginIndex", run.BeginIndex,
		"run.EndIndex", run.EndIndex,
		"run.BeginEvent", scaleUpEvents[run.BeginIndex],
		"run.EndEvent", scaleUpEvents[run.EndIndex])
	return
}

func GetReplayScalingRecommenderReportFormat(replayCAReportPath string) (reportFormat string, err error) {
	fullClusterName, err := apputil.GetClusterName(replayCAReportPath)
	if err != nil {
		return
	}
	reportFormat = fullClusterName + "_sr-replay-%d.json"
	//.strings.TrimSuffix(fileNameWithoutExtension, "_ca-replay") + "-report-replay.json"
	return
}

func GetRecordedClusterSnapshot(dataAccess *db.DataAccess, snapshotNumber int, snapshotID string, runMarkTime time.Time) (cs gsc.ClusterSnapshot, err error) {
	slog.Info("GetRecordedClusterSnapshot INVOKED.",
		"snapshotNumber", snapshotNumber,
		"snapshotID", snapshotID,
		"runMarkTime", runMarkTime)
	cs.Number = snapshotNumber
	cs.ID = snapshotID
	cs.SnapshotTime = runMarkTime

	mccs, err := dataAccess.LoadMachineClassInfosBefore(runMarkTime)
	if err != nil {
		return
	}

	mcds, err := dataAccess.LoadMachineDeploymentInfosBefore(runMarkTime)
	if err != nil {
		return
	}
	workerPools, err := dataAccess.LoadWorkerPoolInfosBefore(runMarkTime)
	if err != nil {
		return
	}
	cs.WorkerPools = workerPools

	cs.AutoscalerConfig.NodeTemplates, err = GetNodeTemplates(mccs, mcds)
	if err != nil {
		return
	}

	cs.PriorityClasses, err = dataAccess.LoadLatestPriorityClassInfoBeforeSnapshotTime(runMarkTime)
	if err != nil {
		return
	}

	allPods, err := dataAccess.GetLatestPodInfosBeforeSnapshotTimestamp(runMarkTime)

	slices.SortFunc(allPods, func(a, b gsc.PodInfo) int {
		return b.SnapshotTimestamp.Compare(a.SnapshotTimestamp) // most recent first
	})
	//allPods = lo.UniqBy(allPods, func(p gsc.PodInfo) string {
	//	return p.Name
	//})

	kubeSystemResources := resutil.ComputeKubeSystemResources(allPods)
	adjustNodeTemplates(cs.AutoscalerConfig.NodeTemplates, kubeSystemResources)

	cs.AutoscalerConfig.CASettings, err = dataAccess.LoadCASettingsBefore(runMarkTime)
	if err != nil {
		return
	}
	cs.AutoscalerConfig.Mode = gsc.AutoscalerReplayerPauseMode

	nodes, err := dataAccess.LoadNodeInfosBefore(runMarkTime)
	if err != nil {
		err = fmt.Errorf("cannot get the node infos before markTime %q: %w", runMarkTime, err)
		return
	}
	err = populateAllocatableVolumes(dataAccess, nodes, runMarkTime)
	if err != nil {
		err = fmt.Errorf("cannot populateAllocatableVolumes in nodes before markTime %q: %w", runMarkTime, err)
		return
	}
	if len(nodes) == 0 {
		err = fmt.Errorf("no existingNodes available before markTime %q", runMarkTime)
		return
	}
	cs.Nodes = filterNodeInfos(nodes)
	cs.Nodes, err = modifyNodeAllocatable(cs.AutoscalerConfig.NodeTemplates, cs.Nodes)
	if err != nil {
		return
	}
	cs.Pods = filterAppPods(allPods)
	nodeNames := lo.Map(cs.Nodes, func(n gsc.NodeInfo, _ int) string {
		return n.Name
	})
	clearNodeNamesNotIn(cs.Pods, nodeNames)
	apputil.SortPodInfosForReadability(cs.Pods)
	cs.AutoscalerConfig.ExistingNodes = cs.Nodes
	cs.AutoscalerConfig.NodeGroups, err = deriveNodeGroups(mcds, cs.AutoscalerConfig.CASettings.NodeGroupsMinMax)
	if err != nil {
		return
	}
	successSignalFileName := fmt.Sprintf("vas-success-%d.txt", cs.Number)
	errorSignalFileName := fmt.Sprintf("vas-error-%d.txt", cs.Number)
	cs.AutoscalerConfig.SuccessSignalPath = path.Join(os.TempDir(), successSignalFileName)
	cs.AutoscalerConfig.ErrorSignalPath = path.Join(os.TempDir(), errorSignalFileName)
	_ = os.Remove(cs.AutoscalerConfig.SuccessSignalPath)
	_ = os.Remove(cs.AutoscalerConfig.ErrorSignalPath)
	cs.AutoscalerConfig.Hash = cs.AutoscalerConfig.GetHash()
	cs.Hash = cs.GetHash()
	return

}
