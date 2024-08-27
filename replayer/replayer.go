package replayer

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"github.com/elankath/gardener-scaling-common"
	"github.com/elankath/gardener-scaling-common/clientutil"
	"github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/db"
	"github.com/elankath/gardener-scaling-history/recorder"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
	"io"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"log/slog"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

const DefaultStabilizeInterval = time.Duration(12 * time.Second)
const DefaultReplayInterval = time.Duration(5 * time.Minute)

var ErrNoScenario = errors.New("no-scenario")

type ReplayMode int

const ReplayFromDBMode ReplayMode = 0
const ReplayFromReportMode ReplayMode = 1

type defaultReplayer struct {
	replayMode          ReplayMode
	dataAccess          *db.DataAccess
	clientSet           *kubernetes.Clientset
	params              gsh.ReplayerParams
	snapshotCount       int
	workCount           int
	lastClusterSnapshot gsc.ClusterSnapshot
	report              *gsh.ReplayReport
	reportPath          string
	scaleUpEventCounter int
	scaleUpEvents       []gsc.EventInfo
	inputScenarios      []gsh.Scenario
	lastScenario        gsh.Scenario
}

var _ gsh.Replayer = (*defaultReplayer)(nil)

func NewDefaultReplayer(params gsh.ReplayerParams) (gsh.Replayer, error) {
	// Load kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", params.VirtualClusterKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("cannot create client config: %w", err)
	}
	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("cannot create clientset: %w", err)
	}
	var replayMode ReplayMode
	var dataAccess *db.DataAccess
	if strings.HasSuffix(params.InputDataPath, ".db") {
		replayMode = ReplayFromDBMode
		dataAccess = db.NewDataAccess(params.InputDataPath)
	} else if strings.HasSuffix(params.InputDataPath, ".json") {
		replayMode = ReplayFromReportMode
	} else {
		return nil, fmt.Errorf("invalid DB path for DB-report %q", params.InputDataPath)
	}
	return &defaultReplayer{
		dataAccess: dataAccess,
		replayMode: replayMode,
		clientSet:  clientset,
		params:     params,
	}, nil
}

func writeAutoscalerConfig(id string, autoscalerConfig gsc.AutoscalerConfig, path string) error {
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

func (r *defaultReplayer) Replay(ctx context.Context) error {
	if r.replayMode == ReplayFromDBMode {
		return r.ReplayFromDB(ctx)
	} else {
		slog.Info("Running scenario report against scaling-recommender. Please ensure that the scaling-recommender has been started.")
		return r.ReplayFromReport(ctx)
	}
}

func (r *defaultReplayer) ReplayFromReport(ctx context.Context) error {
	for _, s := range r.inputScenarios {
		err := writeAutoscalerConfig(s.ClusterSnapshot.ID, s.ClusterSnapshot.AutoscalerConfig, r.params.VirtualAutoScalerConfigPath)
		if err != nil {
			err = fmt.Errorf("cannot write autoscaler config at time %q to path %q: %w", s.ClusterSnapshot.SnapshotTime, r.params.VirtualAutoScalerConfigPath, err)
			return err
		}
		err = syncNodes(ctx, r.clientSet, s.ClusterSnapshot.AutoscalerConfig.ExistingNodes)
		if err != nil {
			return err
		}
		_, err = r.computeAndApplyDeltaWork(ctx, s.ClusterSnapshot, nil)
		if err != nil {
			return err
		}
		slog.Info("applied work, waiting for cluster to stabilize", "stabilizeInterval", r.params.StabilizeInterval, "workCount", r.workCount)
		select {
		case <-time.After(r.params.StabilizeInterval):
		case <-ctx.Done():
			slog.Warn("Context cancelled or timed out:", ctx.Err())
			return ctx.Err()
		}

		writeClusterSnapshot(s.ClusterSnapshot)
		if err = postClusterSnapshot(s.ClusterSnapshot); err != nil {
			return err
		}

		r.lastClusterSnapshot = s.ClusterSnapshot
		outputScenario, err := r.createScenario(ctx, s.ClusterSnapshot)
		if err != nil {
			if errors.Is(err, ErrNoScenario) {
				continue
			} else {
				return err
			}
		}
		err = r.appendScenario(outputScenario, s.ClusterSnapshot)
		if err != nil {
			return err
		}
	}
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
		Timeout: 5 * time.Minute,
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

func syncNodes(ctx context.Context, clientSet *kubernetes.Clientset, nodeInfos []gsc.NodeInfo) error {
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
			return fmt.Errorf("cannot delete the virtual node %q: %w", vn.Name, err)
		}
		//delete(virtualNodesMap, vn.Name)
		klog.V(3).Infof("synchronizeNodes deleted the virtual node %q", vn.Name)
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
				klog.Warningf("synchronizeNodes: node already exists. updating node %q", node.Name)
				_, err = clientSet.CoreV1().Nodes().Update(context.Background(), &node, metav1.UpdateOptions{})
			}
			if err == nil {
				klog.V(3).Infof("synchronizeNodes created node %q", node.Name)
			}
		} else {
			_, err = clientSet.CoreV1().Nodes().Update(context.Background(), &node, metav1.UpdateOptions{})
			klog.V(3).Infof("synchronizeNodes updated node %q", node.Name)
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

func (r *defaultReplayer) ReplayFromDB(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			slog.Warn("context is cancelled/done, exiting re-player")
			return ctx.Err()
		default:
			replayMarkTime := r.getNextReplayMarkTime()
			if replayMarkTime.IsZero() {
				slog.Info("no more scale ups to be replayed. Exiting")
				return nil
			}
			if replayMarkTime.After(time.Now()) {
				slog.Warn("replayMarkTime now exceeds current time. Exiting", "replayMarkTime", replayMarkTime, "replayMarkTimeUnixNanos", replayMarkTime.UnixNano())
				return nil
			}
			slog.Info("Invoking GetRecordedClusterSnapshot with replayMarkTime.", "replayMarkTime", replayMarkTime, "replayMarkTimeUnixNanos", replayMarkTime.UnixNano())
			clusterSnapshot, err := r.GetRecordedClusterSnapshot(replayMarkTime)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					slog.Info("No more recorded work after replayMarkTime! Replay done", "replayMarkTime", replayMarkTime, "replayMarkTimeUnixNanos", replayMarkTime.UnixNano())
					return nil
				}
				return err
			}
			// UNCOMMENT ME FOR DIAGNOSIS ONLY
			//writeClusterSnapshot(clusterSnapshot)
			if clusterSnapshot.Hash == r.lastClusterSnapshot.Hash {
				slog.Info("skipping replay since clusterSnapshot.Hash unchanged from", "Hash", clusterSnapshot.Hash)
				r.lastClusterSnapshot = clusterSnapshot
				continue
			}
			var deletedPendingPods []corev1.Pod
			//if clusterSnapshot.AutoscalerConfig.Hash != r.lastClusterSnapshot.AutoscalerConfig.Hash {
			// Before writing autoscaler config, I must delete any unscheduled Pods .
			// Other-wise CA will call VirtualNodeGroup.IncreaseSize just after AutoScalerConfig is written
			// which isn't good since we want VirtualNodeGroup.IncreaseSize to be called only AFTER computeAndApplyDeltaWork
			deletedPendingPods, err = deletePendingUnscheduledPods(ctx, r.clientSet)
			if err != nil {
				err = fmt.Errorf("cannot delete pendign unscheduled pods: %w", err)
				return err
			}
			if len(deletedPendingPods) > 0 || len(r.lastScenario.ScalingResult.ScaledUpNodes) != 0 {
				//FIXME: Hack to ensure that virtual CA deletes virtually scaled nodes during its
				// sync nodes operation in VirtualNodeGroup.Refresh()
				slog.Info("RESET autoscaler config hash to clear virtual scaled nodes")
				clusterSnapshot.AutoscalerConfig.Hash = "reset"
			}
			if r.lastClusterSnapshot.AutoscalerConfig.Hash != clusterSnapshot.AutoscalerConfig.Hash {
				slog.Info("writing autoscaler config", "snapshotNumber", clusterSnapshot.Number, "currHash", clusterSnapshot.AutoscalerConfig.Hash)
				err = writeAutoscalerConfigAndWaitForSignal(ctx, clusterSnapshot.ID, clusterSnapshot.AutoscalerConfig, r.params.VirtualAutoScalerConfigPath, r.params.StabilizeInterval)
				if err != nil {
					return err
				}
			} else {
				slog.Info("skip writeAutoscalerConfigAndWaitForSignal since hash unchanged", "hash", clusterSnapshot.AutoscalerConfig.Hash)
			}
			_, err = r.computeAndApplyDeltaWork(ctx, clusterSnapshot, deletedPendingPods)
			if err != nil {
				return err
			}
			r.lastClusterSnapshot = clusterSnapshot
			scalingOccurred, err := checkVirtualScaling(ctx, r.clientSet, r.params.StabilizeInterval, r.workCount)
			if !scalingOccurred {
				continue
			}
			scenario, err := r.createScenario(ctx, clusterSnapshot)
			if err != nil {
				if errors.Is(err, ErrNoScenario) {
					continue
				} else {
					return err
				}
			}
			r.lastScenario = scenario
			err = r.appendScenario(scenario, clusterSnapshot)
			if err != nil {
				return err
			}
		}
	}
}

func writeAutoscalerConfigAndWaitForSignal(ctx context.Context, id string, asConfig gsc.AutoscalerConfig, asConfigWritePath string, stabilizeInterval time.Duration) error {
	err := writeAutoscalerConfig(id, asConfig, asConfigWritePath)
	if err != nil {
		err = fmt.Errorf("cannot write autoscaler config for snapshot %q to path %q: %w", id, asConfigWritePath, err)
		return err
	}
	err = waitForVirtualCASignal(ctx, asConfig.SuccessSignalPath, asConfig.ErrorSignalPath, stabilizeInterval)
	if err != nil {
		return err
	}
	return nil
}

func waitForVirtualCASignal(ctx context.Context, successSignalPath string, errorSignalPath string, stabilizeInterval time.Duration) error {
	slog.Info("waitForVirtualCASignal entered..", "successSignalPath", successSignalPath, "errorSignalPath", errorSignalPath, "stabilizeInterval", stabilizeInterval)
	signalTimeout := 4 * time.Minute
	for {
		select {
		case <-ctx.Done():
			slog.Warn("waitForVirtualCASignal context cancelled or timed out:", ctx.Err())
			return ctx.Err()
		case <-time.After(signalTimeout):
			slog.Error("waitForVirtualCASignal exceeded signalTimeout.", "signalTimeout", signalTimeout)
			return fmt.Errorf("waitForVirtualCASignal exceeded signalTimeout %q waiting for %q or %q", signalTimeout, successSignalPath, errorSignalPath)
		case <-time.After(stabilizeInterval):
			slog.Info("waitForVirtualCASignal checking signal paths.", "successSignalPath", successSignalPath, "errorSignalPath", errorSignalPath)
			data, err := os.ReadFile(errorSignalPath)
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("waitForVirtualCASignal got error reading %q: %w", errorSignalPath, err)
			}
			if data != nil {
				errorSignal := string(data)
				slog.Error("waitForVirtualCASignal obtained error signal.", "errorSignal", errorSignal, "errorSignalPath", errorSignalPath)
				return fmt.Errorf("virtual CA signalled issue: %s", errorSignal)
			}
			data, err = os.ReadFile(successSignalPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("waitForVirtualCASignal got error reading %q: %w", successSignalPath, err)
			}
			if data != nil {
				successSignal := string(data)
				slog.Info("waitForVirtualCASignal obtained success signal.", "successSignal", successSignal, "successSignalPath", successSignalPath)
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

func (r *defaultReplayer) Start(ctx context.Context) error {
	err := r.CleanCluster(ctx)
	if err != nil {
		return err
	}
	if r.replayMode == ReplayFromDBMode {
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
		slog.Info("Replayer started in replayFromDB mode")
		reportFileName := apputil.FilenameWithoutExtension(r.params.InputDataPath) + "-db-replay.json"
		r.reportPath = path.Join(r.params.ReportDir, reportFileName)

	} else {
		sBytes, err := os.ReadFile(r.params.InputDataPath)
		if err != nil {
			return err
		}
		var inputReport gsh.ReplayReport
		err = json.Unmarshal(sBytes, &inputReport)
		if err != nil {
			return err
		}
		r.inputScenarios = inputReport.Scenarios
		if len(r.inputScenarios) == 0 {
			return fmt.Errorf("no scenarios found in the report")
		}
		slog.Info("Replayer started in replayFromReport mode")

		fileNameWithoutExtension := apputil.FilenameWithoutExtension(r.params.InputDataPath)
		reportFileName := strings.TrimSuffix(fileNameWithoutExtension, "-db-replay") + "-report-replay.json"
		r.reportPath = path.Join(r.params.ReportDir, reportFileName)
	}
	return nil
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

func GetPodsByUID(pods []gsc.PodInfo) (podsMap map[string]gsc.PodInfo) {
	return lo.KeyBy(pods, func(item gsc.PodInfo) string {
		return item.UID
	})
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
	slog.Info("applyDeltaWork commencing.", "deltaWork", deltaWork)
	var err error
	for _, pcInfo := range deltaWork.PriorityClassWork.ToDeploy {
		_, err = clientSet.SchedulingV1().PriorityClasses().Create(ctx, &pcInfo.PriorityClass, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("applyDeltaWork cannot create the priority class %s: %w", pcInfo.Name, err)
		}
		slog.Info("applyDeltaWork successfully created the priority class", "pc.Name", pcInfo.Name)
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
				slog.Warn("applyDeltaWork cannot delete pod since already deleted", "pod", p.Name, "namespace", p.Namespace, "err", err)
			} else {
				return err
			}
		}
		slog.Info("applyDeltaWork successfully deleted the pod", "pod.Name", p.Name)
	}

	podsToDeploy := deltaWork.PodWork.ToDeploy

	// deploy kube-system pods and pods that have assigned Node names first.
	slices.SortFunc(podsToDeploy, apputil.SortPodInfoForDeployment)

	for i, podInfo := range podsToDeploy {
		pod := getCorePodFromPodInfo(podInfo)
		//if pod.Namespace != "default" {
		//	err = createNamespaces(ctx, clientSet, pod.Namespace)
		//	if err != nil && !apierrors.IsAlreadyExists(err) {
		//		return err
		//	}
		//}
		slog.Info("applyDeltaWork is deploying pod", "deployCount", i+1, "pod.Name", pod.Name, "pod.Namespace", pod.Namespace, "pod.NodeName", pod.Spec.NodeName)
		podNew, err := clientSet.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("applyDeltaWork cannot create the pod  %s: %w", pod.Name, err)
		}
		if podNew.Spec.NodeName != "" {
			podNew.Status.Phase = corev1.PodRunning
			_, err = clientSet.CoreV1().Pods(pod.Namespace).UpdateStatus(ctx, podNew, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("applyDeltaWork cannot change the pod Phase to Running for %s: %w", pod.Name, err)
			}
		}
	}

	err = deleteNamespaces(ctx, clientSet, deltaWork.NamespaceWork.ToDelete.UnsortedList()...)
	if err != nil {
		return fmt.Errorf("applyDeltaWork cannot delete un-used namespaces: %w", err)
	}
	slog.Info("applyDeltaWork success", "workCount", deltaWork.Number)
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

func (r *defaultReplayer) getNextReplayMarkTime() (replayTime time.Time) {
	if r.scaleUpEventCounter >= len(r.scaleUpEvents) {
		slog.Info("getNextReplayMarkTime could find no more scale-up events")
		return
	}
	nextScaleUpEvent := r.scaleUpEvents[r.scaleUpEventCounter]
	r.scaleUpEventCounter++
	replayTime = nextScaleUpEvent.EventTime.UTC()
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
	r.workCount++
	deltaWork.Number = r.workCount
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
	slog.Info("computeDeltaWork for clusterSnapshot.", "clusterSnapshot.Number", clusterSnapshot.Number, "clusterSnapshot.SnapshotTime.UnixNano", clusterSnapshot.SnapshotTime.UnixNano(), "deltaWork", deltaWork)

	return
}

func computePodWork(ctx context.Context, clientSet *kubernetes.Clientset, snapshotPods []gsc.PodInfo, pendingPods []corev1.Pod) (podWork PodWork, err error) {
	virtualNodes, err := clientutil.ListAllNodes(ctx, clientSet)
	if err != nil {
		err = fmt.Errorf("cannot list the nodes in virtual cluster: %w", err)
		return
	}
	virtualNodeNames := lo.Map(virtualNodes, func(item corev1.Node, index int) string {
		return item.Name
	})
	podWork.ToDelete, err = getPodNamesNotAssignedToNodes(ctx, clientSet, virtualNodeNames)
	virtualPods, err := clientutil.ListAllPods(ctx, clientSet)
	if err != nil {
		err = fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
		return
	}
	virtualPods = append(virtualPods, pendingPods...)
	virtualPodsByName := lo.Associate(virtualPods, func(item corev1.Pod) (string, gsc.PodInfo) {
		return item.Name, recorder.PodInfoFromPod(&item)
	})
	clusterSnapshotPodsByName := lo.KeyBy(snapshotPods, func(item gsc.PodInfo) string {
		return item.Name
	})

	for _, podInfo := range virtualPodsByName {
		snapshotPodInfo, ok := clusterSnapshotPodsByName[podInfo.Name]
		if ok && (snapshotPodInfo.NominatedNodeName == podInfo.NominatedNodeName || snapshotPodInfo.NodeName == podInfo.NodeName) {
			continue
		}
		podWork.ToDelete.Insert(types.NamespacedName{Namespace: podInfo.Namespace, Name: podInfo.Name})
		delete(virtualPodsByName, podInfo.Name)
	}

	for _, podInfo := range snapshotPods {
		_, ok := virtualPodsByName[podInfo.Name]
		if ok {
			continue
		}
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
	new.NodeName = ""
	new.Spec.NodeName = ""
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

func deletePodsNotBelongingTo(ctx context.Context, clientSet *kubernetes.Clientset, nodes []string) error {
	nodeNames := sets.New[string](nodes...)
	podsList, err := clientSet.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("cannot list the pods in virtual cluster: %w", err)
	}
	for _, pod := range podsList.Items {
		if nodeNames.Has(pod.Spec.NodeName) {
			continue
		}
		err = clientSet.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("cannot delete the pod %q in namespace %q: %w", pod.Name, pod.Namespace, err)
		}
	}
	return nil
}

func (r *defaultReplayer) Close() error {
	// TODO: clean up cluster can be done here.
	return r.dataAccess.Close()
}

func (r *defaultReplayer) GetRecordedClusterSnapshot(markTime time.Time) (cs gsc.ClusterSnapshot, err error) {
	r.snapshotCount++
	cs.Number = r.snapshotCount
	cs.ID = fmt.Sprintf("%s-%d", apputil.FilenameWithoutExtension(r.params.InputDataPath), cs.Number)
	cs.SnapshotTime = markTime

	mccs, err := r.dataAccess.LoadMachineClassInfosBefore(markTime)
	if err != nil {
		return
	}

	mcds, err := r.dataAccess.LoadMachineDeploymentInfosBefore(markTime)
	if err != nil {
		return
	}
	workerPools, err := r.dataAccess.LoadWorkerPoolInfosBefore(markTime)
	if err != nil {
		return
	}
	cs.WorkerPools = workerPools

	cs.AutoscalerConfig.NodeTemplates, err = GetNodeTemplates(mccs, mcds)
	if err != nil {
		return
	}

	cs.PriorityClasses, err = r.dataAccess.LoadLatestPriorityClassInfoBeforeSnapshotTime(markTime)
	if err != nil {
		return
	}

	allPods, err := r.dataAccess.GetLatestPodInfosBeforeCreationTime(markTime)
	apputil.SortPodInfosForReadability(allPods)

	cs.AutoscalerConfig.CASettings, err = r.dataAccess.LoadCASettingsBefore(markTime)
	if err != nil {
		return
	}
	cs.AutoscalerConfig.Mode = gsc.AutoscalerReplayerMode

	nodes, err := r.dataAccess.LoadNodeInfosBefore(markTime)
	if err != nil {
		err = fmt.Errorf("cannot get the node infos before markTime %q: %w", markTime, err)
		return
	}
	if len(nodes) == 0 {
		err = fmt.Errorf("no existingNodes available before markTime %q", markTime)
		return
	}
	//adjustNodes(nodes, allPods)
	cs.Nodes = nodes
	cs.Pods = filterAppPods(allPods)
	cs.AutoscalerConfig.ExistingNodes = nodes
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

func (r *defaultReplayer) appendScenario(scenario gsh.Scenario, clusterSnapshot gsc.ClusterSnapshot) error {
	if r.report == nil {
		r.report = &gsh.ReplayReport{
			StartTime: clusterSnapshot.SnapshotTime,
			Scenarios: make([]gsh.Scenario, 0),
		}
	}
	r.report.Scenarios = append(r.report.Scenarios, scenario)
	slog.Info("appended scenario for", "snapshotTime", clusterSnapshot.SnapshotTime, "scaledUpNodeGroups", scenario.ScalingResult.ScaledUpNodeGroups)
	rBytes, err := json.Marshal(r.report)
	if err != nil {
		return fmt.Errorf("cannot marshal the scenario report: %w", err)
	}

	err = os.WriteFile(r.reportPath, rBytes, 0666)
	if err != nil {
		return fmt.Errorf("cannot write to report to file %q: %w", r.reportPath, err)
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
	for _, node := range postVirtualNodes {
		_, ok := preVirtualNodesMap[node.Name]
		if ok {
			continue
		}
		//TODO how to replay csi nodes ?
		scaledUpNodes = append(scaledUpNodes, gsh.NodeInfoFromNode(&node, 0))
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
	for _, pod := range pods {
		podInfo := recorder.PodInfoFromPod(&pod)
		// FIXME: BUGGY
		if podInfo.PodScheduleStatus == gsc.PodUnscheduled || podInfo.PodScheduleStatus == gsc.PodSchedulePending {
			scenario.ScalingResult.PendingUnscheduledPods = append(scenario.ScalingResult.PendingUnscheduledPods, podInfo)
		}
	}

	return
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
			err = fmt.Errorf("cannot find NodeGroup with name %q in ngMinMaxMap map %q", ngName, ngMinMaxMap)
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

func waitTillNodesStarted(ctx context.Context, clientSet *kubernetes.Clientset, snapshotNumber int, numWaitNodes int) error {
	for {
		select {
		case <-ctx.Done():
			slog.Warn("Context cancelled or timed out:", ctx.Err())
			return ctx.Err()
		case <-time.After(3 * time.Second):
			slog.Info("waitTillNodesStarted listing nodes of virtual cluster", "snapshotNumber", snapshotNumber, "numWaitNodes", numWaitNodes)
			nodes, err := clientutil.ListAllNodes(ctx, clientSet)
			if err != nil {
				return fmt.Errorf("waitTillNodesStarted for %d nodes got error: %w", numWaitNodes, err)
			}
			numRunningNodes := 0
			for _, n := range nodes {
				if n.Status.Phase == corev1.NodeRunning {
					numRunningNodes++
				}
			}
			if numRunningNodes >= numWaitNodes {
				slog.Info("waitTillNodesStarted has reached required numRunningNodes", "numRunningNodes", numRunningNodes, "numWaitNodes", numWaitNodes)
				return nil
			} else {
				slog.Info("waitTillNodesStarted has not yet reached required numWaitNodes",
					"snapshotNumber", snapshotNumber,
					"numRunningNodes", numRunningNodes,
					"numWaitNodes", numWaitNodes)
			}

		}
	}
}

func checkVirtualScaling(ctx context.Context, clientSet *kubernetes.Clientset, stabilizeInterval time.Duration, workCount int) (scalingOccurred bool, err error) {
	var nodes []corev1.Node
	var pods []corev1.Pod
	for i := 0; i < 2; i++ {
		nodes, err = clientutil.ListAllNodes(ctx, clientSet)
		virtualScaledNodes := lo.Filter(nodes, func(n corev1.Node, _ int) bool {
			_, ok := n.Labels[gsc.LabelVirtualScaled]
			return ok
		})
		if len(virtualScaledNodes) > 0 {
			scalingOccurred = true
		}
		pods, err = clientutil.ListAllPods(ctx, clientSet)
		if err != nil {
			return
		}
		unscheduledPods := lo.Filter(pods, func(item corev1.Pod, index int) bool {
			return item.Spec.NodeName == ""
		})
		if len(unscheduledPods) == 0 {
			if scalingOccurred {
				slog.Info("Zero unscheduledPods after virtual scaling; continue scenario creation", "numNodes", len(nodes), "numPods", len(pods), "numVirtualScaledNodes", len(virtualScaledNodes), "workCount", workCount)
			} else {
				slog.Info("Zero unscheduledPods and zero virtualScaledNodes; skip scenario creation", "numNodes", len(nodes), "numPods", len(pods), "workCount", workCount)
			}
			return
		}
		slog.Info("checkVirtualScaling waiting for unscheduledPods to be scheduled...", "numUnscheduledPods", len(unscheduledPods), "numVirtualScaledNodes", len(virtualScaledNodes), "stabilizeInterval", stabilizeInterval, "workCount", workCount)
		select {
		case <-time.After(stabilizeInterval):
			slog.Info("checkVirtualScaling finished wait of stabilizeInterval.", "stabilizeInterval", stabilizeInterval, "virtualScaledNodes", virtualScaledNodes, "workCount", workCount)
		case <-ctx.Done():
			slog.Warn("checkVirtualScaling received context cancelled or timed out:", "error", ctx.Err(), "workCount", workCount)
			err = ctx.Err()
		}
	}
	return
}

func adjustNodes(nodes []gsc.NodeInfo, pods []gsc.PodInfo) {
	for i := 0; i < len(nodes); i++ {
		node := nodes[i]
		nodeSystemPods := filterSystemPodsForNode(pods, node.Name)
		systemPodRequests := lo.Map(nodeSystemPods, func(p gsc.PodInfo, _ int) corev1.ResourceList {
			return p.Requests
		})
		sumSystemPodRequests := gsc.SumResources(systemPodRequests)
		slog.Info("adjustedNodes found numNodeSystemPods on node", "nodeName", node.Name, "numNodeSystemPods", len(nodeSystemPods), "sumSystemPodRequests", gsc.ResourcesAsString(sumSystemPodRequests))
		newAllocatable := computeNodeAllocatable(node.Capacity, sumSystemPodRequests)
		for resourceName, _ := range node.Capacity {
			newQuant, found := newAllocatable[resourceName]
			if !found {
				continue
			}
			node.Allocatable[resourceName] = newQuant
			continue
		}
		// virtual world old allocatable = 6474244Ki = 6322 MB
		// real world node allocatable = 6322.50
		// virtual world node allocatable = 6858.75 MB
		slog.Info("adjustNodes adjusted node.Allocatable.", "nodeName", node.Name, "nodeAllocatable", gsc.ResourcesAsString(node.Allocatable))
		nodes[i] = node
	}
}

func computeNodeAllocatable(nodeCapacity corev1.ResourceList, sumSystemPodRequests corev1.ResourceList) (allocatable corev1.ResourceList) {
	revisedMem := nodeCapacity.Memory()
	revisedMem.Sub(sumSystemPodRequests[corev1.ResourceMemory])
	revisedCpu := nodeCapacity.Cpu()
	revisedCpu.Sub(sumSystemPodRequests[corev1.ResourceCPU])
	revisedStorage := nodeCapacity.Storage()
	revisedStorage.Sub(sumSystemPodRequests[corev1.ResourceStorage])
	revisedEphStorage := nodeCapacity.StorageEphemeral()
	revisedEphStorage.Sub(sumSystemPodRequests[corev1.ResourceEphemeralStorage])

	allocatable = corev1.ResourceList{
		corev1.ResourceMemory:           *revisedMem,
		corev1.ResourceCPU:              *revisedCpu,
		corev1.ResourceStorage:          *revisedStorage,
		corev1.ResourceEphemeralStorage: *revisedEphStorage,
	}

	return
}

func filterSystemPodsForNode(pods []gsc.PodInfo, nodeName string) []gsc.PodInfo {
	return lo.Filter(pods, func(p gsc.PodInfo, _ int) bool {
		return p.NodeName == nodeName && p.Namespace == "kube-system"
	})
}

func filterAppPods(pods []gsc.PodInfo) []gsc.PodInfo {
	return lo.Filter(pods, func(p gsc.PodInfo, _ int) bool {
		return p.Namespace != "kube-system"
	})
}
func filterKubeSystemPods(pods []gsc.PodInfo) []gsc.PodInfo {
	return lo.Filter(pods, func(p gsc.PodInfo, _ int) bool {
		return p.Namespace == "kube-system"
	})
}
